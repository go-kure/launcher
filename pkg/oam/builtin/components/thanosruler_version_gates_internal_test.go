package components

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/blang/semver/v4"

	"github.com/go-kure/launcher/pkg/oam"
)

// thanosRulerGateFiles are the files of the operator's package pkg/thanos in
// the excerpt, its tests aside.
func thanosRulerGateFiles(t *testing.T, src *vendoredPromOp) []string {
	t.Helper()
	var files []string
	for rel := range src.files {
		if dir, name := path.Split(rel); dir == "pkg/thanos/" && !strings.HasSuffix(name, "_test.go") {
			files = append(files, rel)
		}
	}
	if len(files) == 0 {
		t.Fatal("the excerpt holds no file of pkg/thanos/")
	}
	slices.Sort(files)
	return files
}

// thanosRulerGateRows classify the gates of thanosRulerGateFiles one by one.
func thanosRulerGateRows() []gateRow {
	const (
		op  = "pkg/thanos/operator.go"
		sts = "pkg/thanos/statefulset.go"
		cfg = "(*Operator).createOrUpdateRulerConfigSecret"
		mss = "makeStatefulSetSpec"
	)
	gate := func(file, fn, cond string, paths ...string) gateRow {
		return gateRow{file: file, fn: fn, cond: cond, paths: paths}
	}
	rw := func(cond string, fields ...string) gateRow {
		paths := make([]string, len(fields))
		for i, f := range fields {
			paths[i] = "remoteWrite[]." + f
		}
		return gate(op, cfg, cond, paths...)
	}
	return []gateRow{
		// pkg/thanos/operator.go: the remote write configuration, refused
		// below minRemoteWriteVersion, and the fields of an endpoint reset
		// below the version that reads them.
		gate(op, cfg, `version.LT(minRemoteWriteVersion)`, "remoteWrite"),
		rw(`version.LT(semver.MustParse("0.40.0"))`, `azureAd.managedIdentity.clientId=""`),
		rw(`version.LT(semver.MustParse("0.38.0"))`, "roundRobinDNS"),
		rw(`version.LT(semver.MustParse("0.37.0"))`, "messageVersion"),
		rw(`version.LT(semver.MustParse("0.36.0"))`, "azureAd.sdk"),
		rw(`version.LT(semver.MustParse("0.32.0"))`, "queueConfig.sampleAgeLimit", "noProxy", "proxyFromEnvironment", "proxyConnectHeader"),
		rw(`version.LT(semver.MustParse("0.31.0"))`, "azureAd.oauth", "azureAd"),
		rw(`version.LT(semver.MustParse("0.30.0"))`, "tlsConfig.maxVersion", "sendNativeHistograms"),
		rw(`version.LT(semver.MustParse("0.28.0"))`, "tlsConfig.minVersion"),

		// pkg/thanos/statefulset.go: the flags of the thanos-ruler container
		// and its web configuration.
		gate(sts, mss, `version.GTE(semver.MustParse("0.38.0")) && tr.Spec.RuleQueryOffset != nil && len(*tr.Spec.RuleQueryOffset) > 0`, "ruleQueryOffset"),
		gate(sts, mss, `version.GTE(semver.MustParse("0.37.0")) && tr.Spec.RuleConcurrentEval != nil && *tr.Spec.RuleConcurrentEval > 0`, "ruleConcurrentEval"),
		gate(sts, mss, `version.GTE(semver.MustParse("0.30.0")) && tr.Spec.RuleOutageTolerance != nil && len(*tr.Spec.RuleOutageTolerance) > 0`, "ruleOutageTolerance"),
		gate(sts, mss, `version.GTE(semver.MustParse("0.30.0")) && tr.Spec.RuleGracePeriod != nil && len(*tr.Spec.RuleGracePeriod) > 0`, "ruleGracePeriod"),
		gate(sts, mss, `version.GTE(semver.MustParse("0.39.0")) && len(tr.Spec.EnableFeatures) > 0`, "enableFeatures"),
		gate(sts, mss, `version.GTE(minRemoteWriteVersion) && len(tr.Spec.RemoteWrite) > 0`, "remoteWrite"),
		gate(sts, mss, `tr.Spec.Web != nil && tr.Spec.Web.TLSConfig != nil && version.GTE(semver.MustParse("0.21.0"))`, "web.tlsConfig"),
		gate(sts, mss, `tlsMinVersion != "" && version.GTE(semver.MustParse("0.37.0"))`, "grpcServerTlsConfig.minVersion"),
		gate(sts, mss, `len(tls.CipherSuites) > 0 && version.GTE(semver.MustParse("0.42.0"))`, "grpcServerTlsConfig.cipherSuites"),
		gate(sts, mss, `len(tls.Curves) > 0 && version.GTE(semver.MustParse("0.42.0"))`, "grpcServerTlsConfig.curves"),
		gate(sts, mss, `version.GTE(semver.MustParse("0.21.0"))`, "web.tlsConfig", "web.httpConfig"),
	}
}

// TestThanosRulerVersionGates_MatchVendoredSource holds thanosRulerVersionGates
// and the default it judges an unset version at to the operator's source:
// every comparison of a version in pkg/thanos is a row at its bound.
func TestThanosRulerVersionGates_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	if got := src.stringConst(t, "pkg/operator/defaults.go", "DefaultThanosVersion"); got != prometheusThanosDefaultVersion {
		t.Errorf("the operator's DefaultThanosVersion is %s, prometheusThanosDefaultVersion %s", got, prometheusThanosDefaultVersion)
	}
	holdVersionGates(t, src.versionGates(t, thanosRulerGateFiles(t, src)...), thanosRulerGateRows(), nil,
		idGateEntriesOf(thanosRulerVersionGates), nil, nil)
}

// thanosRulerAzureADMethods are the authentication methods of azureAd, of
// which validateRemoteWriteSpec (pkg/prometheus/operator.go) requires one.
var thanosRulerAzureADMethods = []string{"managedIdentity", "oauth", "sdk", "workloadIdentity"}

// thanosRulerIgnoredLog matches the message the operator logs a remote write
// field it ignores with, naming the field.
var thanosRulerIgnoredLog = regexp.MustCompile(`^ignoring "([^"]+)" not supported by Thanos$`)

// TestThanosRulerRemoteWriteTexts_MatchVendoredSource holds the text of each
// remote write field's gate to how the operator resets the field
// (pkg/thanos/operator.go): its loop ranges over tr.Spec.RemoteWrite by
// value, so a reset of a field of the entry itself (&rw.Field) acts on the
// copy and the field is written all the same, while a reset through one of
// its pointers (&rw.AzureAD.OAuth) drops the field. A dropped method of
// azureAd leaves the object refused where none remains, and azureAd itself,
// written all the same, is refused where no method survives.
//
// A field the operator ignores at every version, with minimum version
// "none", is held to thanosRulerNeverReadFields instead, in either form the
// operator writes it: a reset made by resetFieldFn("none"), or the log of a
// literal field name with minimum version "none" followed by an assignment
// that clears the field.
func TestThanosRulerRemoteWriteTexts_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	const op = "pkg/thanos/operator.go"
	file := src.files[op]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", op)
	}
	byValue := false
	want, never := map[string]string{}, map[string]string{}
	// text is what the operator does with the field at path, cleared through
	// x: an identifier for a field of the entry itself (rw), nil through one
	// of its pointers.
	text := func(path string, x *ast.Ident) string {
		method := slices.ContainsFunc(thanosRulerAzureADMethods, func(m string) bool { return path == "remoteWrite[].azureAd."+m })
		switch {
		case x != nil && x.Name == "rw" && path == "remoteWrite[].azureAd":
			return thanosRulerRemoteWriteAzureAD
		case x != nil && x.Name == "rw":
			return thanosRulerRemoteWriteKept
		case method:
			return thanosRulerRemoteWriteNoAuth
		default:
			return thanosRulerRemoteWriteDropped
		}
	}
	fieldPath := func(name string) string {
		return "remoteWrite[]." + strings.Replace(name, "azureAD", "azureAd", 1)
	}
	// minimum is the minimum version the reset in scope was made with, by
	// resetFieldFn: each reset is assigned in the block of its calls, ahead
	// of them, and the walk is in source order.
	minimum := ""
	ast.Inspect(file, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok && r.Value != nil && src.text(r.Value) == "rw" {
			byValue = src.text(r.X) == "tr.Spec.RemoteWrite" && r.Tok == token.DEFINE
		}
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && src.text(a.Lhs[0]) == "reset" {
			fn, isCall := a.Rhs[0].(*ast.CallExpr)
			var lit *ast.BasicLit
			if isCall && src.text(fn.Fun) == "resetFieldFn" && len(fn.Args) == 1 {
				lit, _ = fn.Args[0].(*ast.BasicLit)
			}
			v, err := "", error(nil)
			if lit != nil {
				v, err = strconv.Unquote(lit.Value)
			}
			if lit == nil || err != nil {
				t.Errorf("%s: reset is not made by resetFieldFn with a literal minimum version", src.fset.Position(a.Pos()))
			}
			minimum = v
			return true
		}
		if b, ok := n.(*ast.BlockStmt); ok {
			for i, s := range b.List {
				name, logged := thanosRulerIgnoredAtEveryVersion(src, s)
				if !logged {
					continue
				}
				path := fieldPath(name)
				var sel *ast.SelectorExpr
				if i+1 < len(b.List) {
					sel = thanosRulerClears(src, b.List[i+1], name)
				}
				if sel == nil {
					t.Errorf("%s: %s is logged as ignored at every version, and the next statement does not clear rw.%s", src.fset.Position(s.Pos()), path, name)
					continue
				}
				x, _ := sel.X.(*ast.Ident)
				never[path] = text(path, x)
			}
			return true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, isIdent := ast.Unparen(call.Fun).(*ast.Ident); !isIdent || id.Name != "reset" {
			return true
		}
		if len(call.Args) != 2 {
			t.Errorf("%s: reset is called with %d arguments", src.fset.Position(call.Pos()), len(call.Args))
			return true
		}
		lit, isLit := call.Args[0].(*ast.BasicLit)
		addr, isAddr := call.Args[1].(*ast.UnaryExpr)
		if !isLit || !isAddr || addr.Op != token.AND {
			t.Errorf("%s: reset is not called with a field name and the address of a field", src.fset.Position(call.Pos()))
			return true
		}
		sel, isSel := addr.X.(*ast.SelectorExpr)
		name, err := strconv.Unquote(lit.Value)
		if !isSel || err != nil {
			t.Errorf("%s: reset is not called with a field name and the address of a field", src.fset.Position(call.Pos()))
			return true
		}
		path := fieldPath(name)
		x, _ := sel.X.(*ast.Ident)
		if minimum == "none" {
			never[path] = text(path, x)
		} else {
			want[path] = text(path, x)
		}
		return true
	})
	if !byValue {
		t.Fatalf("%s: no loop ranges over tr.Spec.RemoteWrite by value into rw; the texts of the remote write gates rest on that copy", op)
	}
	if len(never) == 0 {
		t.Errorf("%s: no field is ignored at every version; the walk reads neither form", op)
	}
	for _, f := range thanosRulerNeverReadFields {
		w, ok := never[f.path]
		delete(never, f.path)
		switch {
		case !ok:
			t.Errorf("%s: the operator no longer ignores it at every version", f.path)
		case f.below != w:
			t.Errorf("%s: the refusal says the operator %q; its reset says %q", f.path, f.below, w)
		}
	}
	for p := range never {
		t.Errorf("%s: the operator ignores it at every version, and thanosRulerNeverReadFields does not list it", p)
	}
	for _, g := range thanosRulerVersionGates {
		if g.id != "" || !strings.HasPrefix(g.gate.path, "remoteWrite[].") {
			continue
		}
		w, ok := want[g.gate.path]
		delete(want, g.gate.path)
		switch {
		case !ok:
			t.Errorf("%s: the operator resets no such field", g.gate.path)
		case g.gate.below != w:
			t.Errorf("%s: the gate says the operator %q; its reset says %q", g.gate.path, g.gate.below, w)
		}
	}
	for p := range want {
		t.Errorf("%s: the operator resets it, and no gate of the kind holds it", p)
	}
}

// thanosRulerClears returns the field s clears, where s assigns its zero
// value to rw.<name> (the field name case aside): a plain = of "", 0, false
// or nil. An assignment of anything else, as rw.Sigv4.ExternalID =
// strings.TrimSpace(rw.Sigv4.ExternalID), keeps the field, and is nil.
func thanosRulerClears(src *vendoredPromOp, s ast.Stmt, name string) *ast.SelectorExpr {
	a, ok := s.(*ast.AssignStmt)
	if !ok || a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
		return nil
	}
	sel, ok := a.Lhs[0].(*ast.SelectorExpr)
	if !ok || !strings.EqualFold(src.text(sel), "rw."+name) {
		return nil
	}
	switch rhs := ast.Unparen(a.Rhs[0]).(type) {
	case *ast.BasicLit:
		if rhs.Value == `""` || rhs.Value == "``" || rhs.Value == "0" {
			return sel
		}
	case *ast.Ident:
		if rhs.Name == "nil" || rhs.Name == "false" {
			return sel
		}
	}
	return nil
}

// TestThanosRulerClears holds what the never-read walk reads as clearing a
// field: the operator's own form, and not an assignment that keeps it.
func TestThanosRulerClears(t *testing.T) {
	for stmt, clears := range map[string]bool{
		`rw.Sigv4.ExternalID = ""`:                                     true,
		`rw.AzureAD.Scope = nil`:                                       true,
		`rw.Sigv4.ExternalID = strings.TrimSpace(rw.Sigv4.ExternalID)`: false,
		`rw.Sigv4.ExternalID = "payments"`:                             false,
		`rw.Sigv4.ExternalID += ""`:                                    false,
		`rw.Sigv4.RoleArn = ""`:                                        false,
		`rw.Sigv4.ExternalID, x = "", 1`:                               false,
	} {
		src := &vendoredPromOp{fset: token.NewFileSet()}
		file, err := parser.ParseFile(src.fset, "s.go", "package p\nfunc f() {\n"+stmt+"\n}\n", 0)
		if err != nil {
			t.Fatalf("parse %q: %v", stmt, err)
		}
		s := file.Decls[0].(*ast.FuncDecl).Body.List[0]
		if got := thanosRulerClears(src, s, "sigv4.externalId") != nil || thanosRulerClears(src, s, "azureAD.scope") != nil; got != clears {
			t.Errorf("%s: clears = %v, want %v", stmt, got, clears)
		}
	}
}

// thanosRulerIgnoredAtEveryVersion reports the field name s logs as ignored
// with minimum version "none", where s is a call of a method of the
// operator's logger with a literal message and those literal arguments.
func thanosRulerIgnoredAtEveryVersion(src *vendoredPromOp, s ast.Stmt) (string, bool) {
	e, ok := s.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := e.X.(*ast.CallExpr)
	if !ok || !strings.HasPrefix(src.text(call.Fun), "o.logger.") || len(call.Args) == 0 {
		return "", false
	}
	lits := make([]string, len(call.Args))
	for i, a := range call.Args {
		if lit, isLit := a.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
			lits[i], _ = strconv.Unquote(lit.Value)
		}
	}
	m := thanosRulerIgnoredLog.FindStringSubmatch(lits[0])
	if m == nil {
		return "", false
	}
	for i := 1; i+1 < len(lits); i++ {
		if lits[i] == "minimum_version" && lits[i+1] == "none" {
			return m[1], true
		}
	}
	return "", false
}

// thanosRulerNeverReadFixtures author each field of
// thanosRulerNeverReadFields in a remote write entry the version gates admit
// at v0.31.0 and later.
func thanosRulerNeverReadFixtures() map[string]map[string]any {
	type m = map[string]any
	rw := func(extra m) m {
		e := m{"url": "https://metrics.example.com/api/v1/write"}
		maps.Copy(e, extra)
		return m{"remoteWrite": []any{e}}
	}
	managed := m{"clientId": "00000000-0000-0000-0000-000000000000"}
	return map[string]map[string]any{
		"remoteWrite[].azureAd.workloadIdentity": rw(m{"azureAd": m{"cloud": "AzurePublic", "workloadIdentity": m{
			"clientId": "00000000-0000-0000-0000-000000000000", "tenantId": "00000000-0000-0000-0000-000000000001",
		}}}),
		"remoteWrite[].azureAd.scope": rw(m{"azureAd": m{"managedIdentity": managed, "scope": "https://monitor.azure.com/.default"}}),
		"remoteWrite[].sigv4.externalId": rw(m{"sigv4": m{
			"region": "eu-west-1", "roleArn": "arn:aws:iam::123456789012:role/metrics", "externalId": "payments",
		}}),
	}
}

// TestThanosRulerNeverRead_Refused: each field of thanosRulerNeverReadFields
// is refused at every version from v0.31.0 on, where the gates admit its
// fixture, the default included, naming the path and its own text of what the
// operator does; below thanosRulerMinRemoteWrite the gate of remoteWrite is
// refused instead, as the operator refuses the entries before it reads them,
// with azureAd's where the fixture authors one (refuseVersionMisses), and a
// version the operator cannot parse is left to it. Beside an entry the
// operator refuses for a managed identity without a client id, that gate is
// refused instead, as the ruler writes nothing there; beside a field the
// operator drops (enableFeatures below v0.39.0), it is refused itself, as a
// workloadIdentity whose drop leaves no method gets the object refused, where
// the ruler would not run without the flag. The entry without it
// builds, and so does an externalId without a roleArn, which the API server
// refuses (thanosRulerRulesLeft).
func TestThanosRulerNeverRead_Refused(t *testing.T) {
	fixtures := thanosRulerNeverReadFixtures()
	unset := map[string]func(entry map[string]any){
		"remoteWrite[].azureAd.workloadIdentity": func(e map[string]any) {
			e["azureAd"] = map[string]any{"managedIdentity": map[string]any{"clientId": "00000000-0000-0000-0000-000000000000"}}
		},
		"remoteWrite[].azureAd.scope":    func(e map[string]any) { delete(e["azureAd"].(map[string]any), "scope") },
		"remoteWrite[].sigv4.externalId": func(e map[string]any) { delete(e["sigv4"].(map[string]any), "externalId") },
	}
	for _, f := range thanosRulerNeverReadFields {
		props, ok := fixtures[f.path]
		delete(fixtures, f.path)
		t.Run(f.path, func(t *testing.T) {
			if !ok || unset[f.path] == nil {
				t.Fatalf("no fixture authors %s", f.path)
			}
			want := fmt.Sprintf("%s: the Prometheus operator does not pass it to Thanos at any version: it %s; leave %s unset", f.path, f.below, f.path)
			for _, version := range []string{"", "v0.31.0", "v0.42.4", "v1.0.0"} {
				if err := trBuild(props, version); err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("at %q: err = %v, want one mentioning %q", version, err, want)
				}
			}
			below := "remoteWrite: read by the Prometheus operator only for Thanos 0.24.0 and later"
			if _, azure := props["remoteWrite"].([]any)[0].(map[string]any)["azureAd"]; azure {
				below = "version: refused: fields outside their versions at version v0.23.0: remoteWrite (read from Thanos 0.24.0), remoteWrite[].azureAd (read from Thanos 0.31.0); "
			}
			if err := trBuild(props, "v0.23.0"); err == nil || !strings.HasPrefix(err.Error(), below) {
				t.Errorf("at v0.23.0: err = %v, want the gate of remoteWrite, with azureAd's where it is authored", err)
			}
			if err := trBuild(props, "main"); err != nil {
				t.Errorf("at an unparsable version: err = %v, want it left to the operator", err)
			}
			beside := map[string]any{"remoteWrite": append(slices.Clone(props["remoteWrite"].([]any)), map[string]any{
				"url": "https://metrics.example.com/api/v1/write", "azureAd": map[string]any{"managedIdentity": map[string]any{}},
			})}
			if err := trBuild(beside, "v0.39.0"); err == nil || !strings.HasPrefix(err.Error(), "remoteWrite[].azureAd.managedIdentity: read by the Prometheus operator only for Thanos 0.40.0 and later") {
				t.Errorf("beside a managed identity without a client id at v0.39.0: err = %v, want its gate", err)
			}
			flagged := maps.Clone(props)
			flagged["enableFeatures"] = []any{"promql-experimental-functions"}
			if err := trBuild(flagged, "v0.38.0"); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("with enableFeatures, dropped at v0.38.0: err = %v, want one mentioning %q", err, want)
			}
			entry := maps.Clone(props["remoteWrite"].([]any)[0].(map[string]any))
			for k, v := range entry {
				if inner, isMap := v.(map[string]any); isMap {
					entry[k] = maps.Clone(inner)
				}
			}
			unset[f.path](entry)
			if err := trBuild(map[string]any{"remoteWrite": []any{entry}}, ""); err != nil {
				t.Errorf("without it: err = %v, want it built", err)
			}
		})
	}
	for p := range fixtures {
		t.Errorf("a fixture authors %s, which thanosRulerNeverReadFields does not list", p)
	}
	noRole := map[string]any{"remoteWrite": []any{map[string]any{
		"url": "https://metrics.example.com/api/v1/write", "sigv4": map[string]any{"region": "eu-west-1", "externalId": "payments"},
	}}}
	if err := trBuild(noRole, ""); err != nil {
		t.Errorf("an externalId without a roleArn: err = %v, want it left to the API server", err)
	}
	// A workloadIdentity on one entry is refused ahead of a scope on an
	// earlier one: the operator drops both, then refuses the object for the
	// azureAd left with no method, so the ruler does not write without scope.
	scopeThenWorkload := map[string]any{"remoteWrite": []any{
		map[string]any{"url": "https://metrics.example.com/api/v1/write", "azureAd": map[string]any{
			"cloud": "AzurePublic", "scope": "https://monitor.azure.com/.default",
			"managedIdentity": map[string]any{"clientId": "00000000-0000-0000-0000-000000000000"},
		}},
		map[string]any{"url": "https://metrics.example.com/api/v1/write", "azureAd": map[string]any{
			"cloud": "AzurePublic", "workloadIdentity": map[string]any{
				"clientId": "00000000-0000-0000-0000-000000000000", "tenantId": "00000000-0000-0000-0000-000000000000",
			},
		}},
	}}
	if err := trBuild(scopeThenWorkload, ""); err == nil || !strings.HasPrefix(err.Error(), "remoteWrite[].azureAd.workloadIdentity: ") {
		t.Errorf("a scope ahead of a workloadIdentity-only azureAd: err = %v, want the workloadIdentity refused", err)
	}
}

// TestThanosRulerAzureADWithoutMethod_Refused: an azureAd authored with no
// authentication method, which the operator refuses at reconcile whatever it
// drops, is refused as such from v0.31.0 on, naming the methods the operator
// passes to Thanos at the version, ahead of a gate of a field it drops (here
// enableFeatures, below v0.39.0) and of a field it passes to Thanos at no
// version: the scope it would drop, or what is left where the
// workloadIdentity of the fixture is unset. Below v0.31.0 the gate of azureAd
// is refused instead, and below thanosRulerMinRemoteWrite that of
// remoteWrite too, each named with enableFeatures's as fields outside their
// versions (refuseVersionMisses); a version the operator cannot parse is left
// to it.
func TestThanosRulerAzureADWithoutMethod_Refused(t *testing.T) {
	type m = map[string]any
	rw := func(azure m) m {
		return m{"remoteWrite": []any{m{"url": "https://metrics.example.com/api/v1/write", "azureAd": azure}}, "enableFeatures": []any{"promql-experimental-functions"}}
	}
	fixture := thanosRulerNeverReadFixtures()["remoteWrite[].azureAd.workloadIdentity"]["remoteWrite"].([]any)[0].(m)
	withoutWorkload := maps.Clone(fixture["azureAd"].(m))
	delete(withoutWorkload, "workloadIdentity")
	want := func(set string) string {
		return "remoteWrite[0].azureAd: no authentication method: the Prometheus operator requires one of managedIdentity, oauth, sdk and workloadIdentity, and refuses the object at reconcile without one, at every version, wherever it generates the remote write configuration; set " + set + " (the operator does not pass workloadIdentity to Thanos), or leave azureAd unset"
	}
	sets := map[string]string{
		"":        "managedIdentity, oauth or sdk",
		"v0.42.4": "managedIdentity, oauth or sdk",
		"v0.40.0": "managedIdentity, oauth or sdk",
		"v0.38.0": "managedIdentity with a clientId, oauth or sdk",
		"v0.36.0": "managedIdentity with a clientId, oauth or sdk",
		"v0.35.0": "managedIdentity with a clientId or oauth",
		"v0.31.0": "managedIdentity with a clientId or oauth",
	}
	for name, props := range map[string]m{
		"cloud only": rw(m{"cloud": "AzurePublic"}),
		"scope only": rw(m{"scope": "https://monitor.azure.com/.default"}),
		"the workloadIdentity fixture without it": rw(withoutWorkload),
	} {
		for version, set := range sets {
			if err := trBuild(props, version); err == nil || err.Error() != want(set) {
				t.Errorf("%s at %q: err = %v, want %q", name, version, err, want(set))
			}
		}
		if err := trBuild(props, "v0.30.0"); err == nil || !strings.HasPrefix(err.Error(), "version: refused: fields outside their versions at version v0.30.0: enableFeatures (read from Thanos 0.39.0), remoteWrite[].azureAd (read from Thanos 0.31.0); ") {
			t.Errorf("%s at v0.30.0: err = %v, want the gates of enableFeatures and azureAd", name, err)
		}
		if err := trBuild(props, "v0.23.0"); err == nil || !strings.HasPrefix(err.Error(), "version: refused: fields outside their versions at version v0.23.0: enableFeatures (read from Thanos 0.39.0), remoteWrite (read from Thanos 0.24.0), remoteWrite[].azureAd (read from Thanos 0.31.0); ") {
			t.Errorf("%s at v0.23.0: err = %v, want the gates of enableFeatures, remoteWrite and azureAd", name, err)
		}
		if err := trBuild(props, "main"); err != nil {
			t.Errorf("%s at an unparsable version: err = %v, want it left to the operator", name, err)
		}
	}
}

// thanosRulerGateFixtures are the fixtures of thanosRulerVersionGates, by id,
// as promGateFixture describes them.
func thanosRulerGateFixtures() map[string]promGateFixture {
	type m = map[string]any
	ref := func(name, key string) m { return m{"name": name, "key": key} }
	merge := func(base, extra m) m {
		out := maps.Clone(base)
		maps.Copy(out, extra)
		return out
	}
	rw := func(extra m) m {
		return m{"remoteWrite": []any{merge(m{"url": "https://metrics.example.com/api/v1/write"}, extra)}}
	}
	azure := func(extra m) m { return rw(m{"azureAd": extra}) }
	grpc := func(extra m) m { return m{"grpcServerTlsConfig": extra} }
	f := func(props m, below string) promGateFixture { return promGateFixture{props: props, below: below} }
	return map[string]promGateFixture{
		"ruleOutageTolerance":              f(m{"ruleOutageTolerance": "1h"}, "v0.29.0"),
		"ruleGracePeriod":                  f(m{"ruleGracePeriod": "10m"}, "v0.29.0"),
		"ruleConcurrentEval":               f(m{"ruleConcurrentEval": 4}, "v0.36.0"),
		"ruleQueryOffset":                  f(m{"ruleQueryOffset": "30s"}, "v0.37.0"),
		"enableFeatures":                   f(m{"enableFeatures": []any{"promql-experimental-functions"}}, "v0.38.0"),
		"grpcServerTlsConfig.minVersion":   f(grpc(m{"minVersion": "TLS12"}), "v0.36.0"),
		"grpcServerTlsConfig.cipherSuites": f(grpc(m{"cipherSuites": []any{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}}), "v0.41.0"),
		"grpcServerTlsConfig.curves":       f(grpc(m{"curves": []any{"X25519"}}), "v0.41.0"),
		"web.tlsConfig": f(m{"web": m{"tlsConfig": m{
			"keySecret": ref("thanos-ruler-tls", "tls.key"), "cert": m{"secret": ref("thanos-ruler-tls", "tls.crt")},
		}}}, "v0.20.0"),
		"web.httpConfig": f(m{"web": m{"httpConfig": m{"http2": true}}}, "v0.20.0"),

		"remoteWrite":                              f(rw(m{}), "v0.23.0"),
		"remoteWrite[].tlsConfig.minVersion":       f(rw(m{"tlsConfig": m{"minVersion": "TLS12"}}), "v0.27.0"),
		"remoteWrite[].tlsConfig.maxVersion":       f(rw(m{"tlsConfig": m{"maxVersion": "TLS13"}}), "v0.29.0"),
		"remoteWrite[].sendNativeHistograms":       f(rw(m{"sendNativeHistograms": true}), "v0.29.0"),
		"remoteWrite[].azureAd":                    f(azure(m{"managedIdentity": m{"clientId": "00000000-0000-0000-0000-000000000000"}}), "v0.30.0"),
		"remoteWrite[].queueConfig.sampleAgeLimit": f(rw(m{"queueConfig": m{"sampleAgeLimit": "5m"}}), "v0.31.0"),
		"remoteWrite[].noProxy": f(rw(m{"proxyUrl": "http://proxy.example.com:3128", "noProxy": "internal.example.com"}),
			"v0.31.0"),
		"remoteWrite[].proxyFromEnvironment": f(rw(m{"proxyFromEnvironment": true}), "v0.31.0"),
		"remoteWrite[].proxyConnectHeader": f(rw(m{"proxyUrl": "http://proxy.example.com:3128",
			"proxyConnectHeader": m{"X-Team": []any{ref("proxy", "team")}}}), "v0.31.0"),
		"remoteWrite[].azureAd.sdk":                         f(azure(m{"sdk": m{"tenantId": "00000000-0000-0000-0000-000000000001"}}), "v0.35.0"),
		"remoteWrite[].messageVersion":                      f(rw(m{"messageVersion": "V2.0"}), "v0.36.0"),
		"remoteWrite[].roundRobinDNS":                       f(rw(m{"roundRobinDNS": true}), "v0.37.0"),
		`remoteWrite[].azureAd.managedIdentity.clientId=""`: f(azure(m{"managedIdentity": m{}}), "v0.39.0"),
		// azureAd itself is gated at the same minimum, and named with it.
		"remoteWrite[].azureAd.oauth": {props: azure(m{"oauth": m{
			"clientId": "00000000-0000-0000-0000-000000000000", "clientSecret": ref("azure", "secret"), "tenantId": "00000000-0000-0000-0000-000000000001",
		}}), below: "v0.30.0", path: "remoteWrite[].azureAd"},
	}
}

// trBuild builds props as the thanosruler kind, with version where not
// empty.
func trBuild(props map[string]any, version string) error {
	p := maps.Clone(props)
	if version != "" {
		p["version"] = version
	}
	_, err := (&ThanosRulerHandler{}).ToApplicationConfig(&oam.Component{Name: "main", Type: "thanosruler", Properties: p}, "monitoring")
	return err
}

// TestThanosRulerVersionGates_Refused: each id of thanosRulerVersionGates,
// authored alone with a version under its minimum, is refused naming the
// path, the bound, the version and its own text of what the operator does,
// or, authored with another field below its version, naming both with their
// versions (refuseVersionMisses); at its bound it builds, and with the
// version unset too, where the operator's default is compared.
func TestThanosRulerVersionGates_Refused(t *testing.T) {
	fixtures := thanosRulerGateFixtures()
	for _, g := range thanosRulerVersionGates {
		id := cmp.Or(g.id, g.gate.path)
		fx, ok := fixtures[id]
		delete(fixtures, id)
		t.Run(id, func(t *testing.T) {
			if !ok {
				t.Fatalf("no fixture authors %s", id)
			}
			minimum := semver.MustParse(g.gate.minimum)
			below, err := semver.ParseTolerant(fx.below)
			if err != nil || !below.LT(minimum) {
				t.Fatalf("below %s: want a version that parses and is under %s (err %v)", fx.below, g.gate.minimum, err)
			}
			path, bound := g.gate.path, g.gate.minimum
			want := []string{fmt.Sprintf("%s: read by the Prometheus operator only for Thanos %s and later; at version %s it %s; ", path, bound, fx.below, g.gate.below)}
			if fx.path != "" {
				bound = cmp.Or(fx.bound, bound)
				want = []string{
					fmt.Sprintf("version: refused: fields outside their versions at version %s: ", fx.below),
					fmt.Sprintf("%s (read from Thanos %s)", fx.path, bound),
					fmt.Sprintf("%s (read from Thanos %s)", path, g.gate.minimum),
				}
			}
			err = trBuild(fx.props, fx.below)
			for _, w := range want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("at %s: err = %v, want one mentioning %q", fx.below, err, w)
				}
			}
			if err := trBuild(fx.props, "v"+bound); err != nil {
				t.Errorf("at v%s: err = %v, want it built", bound, err)
			}
			if err := trBuild(fx.props, ""); err != nil {
				t.Errorf("version unset: err = %v, want it built at the default", err)
			}
		})
	}
	for id := range fixtures {
		t.Errorf("a fixture authors %s, which thanosRulerVersionGates does not list", id)
	}
}

// TestThanosRulerVersionGates_Cases holds the version check's cases beside
// the table: an unparsable version, the default named where the version is
// unset, and the conditions the operator tests beside the version.
func TestThanosRulerVersionGates_Cases(t *testing.T) {
	type m = map[string]any
	cases := []struct {
		name    string
		props   m
		version string
		want    string // "" builds
	}{
		{"unparsable version admits a gated field", m{"enableFeatures": []any{"promql-experimental-functions"}}, "main", ""},
		{"ruleConcurrentEval of 0 is passed at no version", m{"ruleConcurrentEval": 0}, "v0.36.0", ""},
		{"no remote write below v0.24.0", m{}, "v0.23.0", ""},
		{"a gate one patch below", m{"remoteWrite": []any{m{"url": "https://metrics.example.com/api/v1/write"}}}, "v0.23.9",
			"remoteWrite: read by the Prometheus operator only for Thanos 0.24.0 and later; at version v0.23.9 it refuses the object at reconcile"},
		{"a prerelease below its release", m{"ruleQueryOffset": "30s"}, "v0.38.0-rc.0",
			"ruleQueryOffset: read by the Prometheus operator only for Thanos 0.38.0 and later; at version v0.38.0-rc.0 it does not pass it to Thanos Ruler"},
		// go-kure/launcher#1022: a refused and a dropped field are named
		// together, with no consequence claimed for either.
		{"a refused and a dropped field name both", m{"ruleQueryOffset": "30s", "remoteWrite": []any{m{"url": "https://metrics.example.com/api/v1/write"}}}, "v0.23.0",
			"version: refused: fields outside their versions at version v0.23.0: ruleQueryOffset (read from Thanos 0.38.0), remoteWrite (read from Thanos 0.24.0); the Prometheus operator does not run the object as authored there; raise version to 0.38.0 or later, or leave those fields unset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := trBuild(c.props, c.version)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("err = %v, want it built", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}
