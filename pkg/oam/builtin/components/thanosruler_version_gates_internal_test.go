package components

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"path"
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

// thanosRulerAlwaysDropped are the remote write fields the operator resets at
// every Thanos version (minimum "none"), which no version gate holds
// (go-kure/launcher#1020), with the text its reset reads as. The test sees
// reset calls only: sigv4.externalId, which the operator clears by
// assignment at every version, is not among them.
var thanosRulerAlwaysDropped = map[string]string{
	"remoteWrite[].azureAd.workloadIdentity": thanosRulerRemoteWriteNoAuth,
	"remoteWrite[].azureAd.scope":            thanosRulerRemoteWriteDropped,
}

// thanosRulerAzureADMethods are the authentication methods of azureAd, of
// which validateRemoteWriteSpec (pkg/prometheus/operator.go) requires one.
var thanosRulerAzureADMethods = []string{"managedIdentity", "oauth", "sdk", "workloadIdentity"}

// TestThanosRulerRemoteWriteTexts_MatchVendoredSource holds the text of each
// remote write field's gate to how the operator resets the field
// (pkg/thanos/operator.go): its loop ranges over tr.Spec.RemoteWrite by
// value, so a reset of a field of the entry itself (&rw.Field) acts on the
// copy and the field is written all the same, while a reset through one of
// its pointers (&rw.AzureAD.OAuth) drops the field. A dropped method of
// azureAd leaves the object refused where none remains, and azureAd itself,
// written all the same, is refused where no method survives.
func TestThanosRulerRemoteWriteTexts_MatchVendoredSource(t *testing.T) {
	src := loadVendoredPromOp(t)
	const op = "pkg/thanos/operator.go"
	file := src.files[op]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", op)
	}
	byValue := false
	want := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		if r, ok := n.(*ast.RangeStmt); ok && r.Value != nil && src.text(r.Value) == "rw" {
			byValue = src.text(r.X) == "tr.Spec.RemoteWrite" && r.Tok == token.DEFINE
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
		path := "remoteWrite[]." + strings.Replace(name, "azureAD", "azureAd", 1)
		method := slices.ContainsFunc(thanosRulerAzureADMethods, func(m string) bool { return path == "remoteWrite[].azureAd."+m })
		switch x, _ := sel.X.(*ast.Ident); {
		case x != nil && x.Name == "rw" && path == "remoteWrite[].azureAd":
			want[path] = thanosRulerRemoteWriteAzureAD
		case x != nil && x.Name == "rw":
			want[path] = thanosRulerRemoteWriteKept
		case method:
			want[path] = thanosRulerRemoteWriteNoAuth
		default:
			want[path] = thanosRulerRemoteWriteDropped
		}
		return true
	})
	if !byValue {
		t.Fatalf("%s: no loop ranges over tr.Spec.RemoteWrite by value into rw; the texts of the remote write gates rest on that copy", op)
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
	for p, text := range thanosRulerAlwaysDropped {
		w, ok := want[p]
		delete(want, p)
		switch {
		case !ok:
			t.Errorf("%s: the operator no longer resets it", p)
		case w != text:
			t.Errorf("%s: its reset reads as %q, not %q", p, w, text)
		}
	}
	for p := range want {
		t.Errorf("%s: the operator resets it, and no gate of the kind holds it", p)
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
		"remoteWrite[].azureAd.oauth": f(azure(m{"oauth": m{
			"clientId": "00000000-0000-0000-0000-000000000000", "clientSecret": ref("azure", "secret"), "tenantId": "00000000-0000-0000-0000-000000000001",
		}}), "v0.30.0"),
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
// path, the bound, the version and its own text of what the operator does;
// at its bound it builds, and with the version unset too, where the
// operator's default is compared.
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
			if fx.path != "" || fx.bound != "" {
				t.Fatalf("the fixture of %s names another gate (%s at %s): each gate of the kind is refused alone", id, fx.path, fx.bound)
			}
			path, bound := g.gate.path, g.gate.minimum
			want := fmt.Sprintf("%s: read by the Prometheus operator only for Thanos %s and later; at version %s it %s; ", path, bound, fx.below, g.gate.below)
			if err := trBuild(fx.props, fx.below); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("at %s: err = %v, want one mentioning %q", fx.below, err, want)
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
