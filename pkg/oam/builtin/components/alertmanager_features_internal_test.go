package components

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// vendoredAlertmanagerDir holds the copies of Alertmanager's
// featurecontrol/featurecontrol.go that alertmanagerFeatureFlags is held to:
// one per minor at its latest release, copied unmodified from
// prometheus/alertmanager under its Apache-2.0 licence by
// scripts/vendor-alertmanager-features.sh, each with the release's NOTICE, and
// the project's LICENSE. Its SOURCE file names each tag and the git blob id of
// each file.
const vendoredAlertmanagerDir = "testdata/upstream/alertmanager"

// vendoredAlertmanagerRelease is one vendored featurecontrol.go, parsed.
type vendoredAlertmanagerRelease struct {
	tag     string
	version semver.Version
	fset    *token.FileSet
	file    *ast.File
}

// loadVendoredAlertmanager reads SOURCE and parses every featurecontrol.go it
// names, failing on a file whose blob id differs, a Go file without its
// licence header, a file in the directory that SOURCE does not name, a
// licence that is not the Apache License, Version 2.0, and a tag that is not a
// release of 0.x, comes twice for one minor, or lacks its NOTICE or its
// featurecontrol.go.
func loadVendoredAlertmanager(t *testing.T) []vendoredAlertmanagerRelease {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vendoredAlertmanagerDir, "SOURCE"))
	if err != nil {
		t.Fatalf("read SOURCE: %v", err)
	}
	listed := map[string]bool{"SOURCE": true}
	read := func(rel, blob string) []byte {
		listed[rel] = true
		body, err := os.ReadFile(filepath.Join(vendoredAlertmanagerDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("SOURCE names %s: %v", rel, err)
		}
		if got := vendoredBlobID(body); got != blob {
			t.Fatalf("%s has blob id %s, SOURCE says %s: the copies are unmodified; re-run scripts/vendor-alertmanager-features.sh", rel, got, blob)
		}
		return body
	}
	var releases []vendoredAlertmanagerRelease
	notices := map[string]bool{}
	licence := false
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || strings.HasPrefix(line, "#"):
		case fields[0] == "licence" && len(fields) == 3:
			body := read(fields[1], fields[2])
			if !bytes.Contains(body, []byte("Apache License")) || !bytes.Contains(body, []byte("Version 2.0, January 2004")) {
				t.Fatalf("%s is not the Apache License, Version 2.0", fields[1])
			}
			licence = true
		case fields[0] == "tag" && len(fields) == 3:
			// The tag object is provenance for whoever refreshes the copies;
			// offline, the test holds each file to its blob id.
			tag := fields[1]
			version, err := semver.Parse(strings.TrimPrefix(tag, "v"))
			if err != nil || !strings.HasPrefix(tag, "v") || version.Major != 0 || len(version.Pre) > 0 || len(version.Build) > 0 {
				t.Fatalf("SOURCE: tag %s is not a release of Alertmanager 0.x", tag)
			}
			if slices.ContainsFunc(releases, func(r vendoredAlertmanagerRelease) bool { return r.version.Minor == version.Minor }) {
				t.Fatalf("SOURCE: tag %s is a second release of 0.%d", tag, version.Minor)
			}
			releases = append(releases, vendoredAlertmanagerRelease{tag: tag, version: version})
		case fields[0] == "notice" && len(fields) == 3:
			read(fields[1], fields[2])
			notices[fields[1]] = true
		case fields[0] == "file" && len(fields) == 3:
			rel := fields[1]
			body := read(rel, fields[2])
			if !bytes.Contains(body, []byte("Licensed under the Apache License, Version 2.0")) {
				t.Fatalf("%s does not carry its Apache-2.0 header", rel)
			}
			i := slices.IndexFunc(releases, func(r vendoredAlertmanagerRelease) bool {
				return rel == r.tag+"/featurecontrol/featurecontrol.go"
			})
			if i < 0 || releases[i].file != nil {
				t.Fatalf("SOURCE: %s is not the featurecontrol.go of a tag named before it", rel)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, rel, body, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			releases[i].fset, releases[i].file = fset, file
		default:
			t.Fatalf("SOURCE: unreadable line %q", line)
		}
	}
	err = filepath.WalkDir(vendoredAlertmanagerDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(vendoredAlertmanagerDir, p)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s is in the copies and not named in SOURCE", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", vendoredAlertmanagerDir, err)
	}
	if !licence {
		t.Fatal("SOURCE names no licence: the copies carry the Apache License, Version 2.0")
	}
	for _, r := range releases {
		if r.file == nil || !notices[r.tag+"/NOTICE"] {
			t.Fatalf("SOURCE: %s has featurecontrol.go %v, NOTICE %v: each tag carries both", r.tag, r.file != nil, notices[r.tag+"/NOTICE"])
		}
	}
	return releases
}

// text is n as written in r's file.
func (r vendoredAlertmanagerRelease) text(t *testing.T, n ast.Node) string {
	t.Helper()
	var b strings.Builder
	if err := printer.Fprint(&b, r.fset, n); err != nil {
		t.Fatalf("%s: print: %v", r.tag, err)
	}
	return b.String()
}

// newFlagsOutline is NewFlags's body, statement by statement with its
// whitespace collapsed, as refuseUnusableAlertmanagerFeatures models it: an
// empty value is no feature; any other is split on ","; each element is
// matched by a switch (newFlagsLoop, checked case by case); every option the
// switch collects is applied; classic-mode with utf8-strict-mode is an error.
// A statement added, dropped or changed fails the test, to be read anew.
var newFlagsOutline = []string{
	"fc := &Flags{logger: logger}",
	"opts := []flagOption{}",
	"if len(features) == 0 { return NoopFlags{}, nil }",
	newFlagsLoop,
	"for _, opt := range opts { opt(fc) }",
	`if fc.classicMode && fc.utf8StrictMode { return nil, errors.New("cannot have both classic and UTF-8 modes enabled") }`,
	"return fc, nil",
}

// newFlagsLoop stands in newFlagsOutline for the loop over the split value.
const newFlagsLoop = "<the loop over the split value>"

// newFlagsModeSetters are the bodies of the setters that the cases of
// classic-mode and utf8-strict-mode call, setting the fields the combination
// is refused on.
var newFlagsModeSetters = map[string]string{
	"classic-mode":     "{ return func(configs *Flags) { configs.classicMode = true } }",
	"utf8-strict-mode": "{ return func(configs *Flags) { configs.utf8StrictMode = true } }",
}

// appendOption is a case's first statement, appending the option of a setter.
var appendOption = regexp.MustCompile(`^opts = append\(opts, (\w+)\(\)\)$`)

// collapsed is n as written in r's file, its whitespace collapsed to one space.
func (r vendoredAlertmanagerRelease) collapsed(t *testing.T, n ast.Node) string {
	t.Helper()
	return strings.Join(strings.Fields(r.text(t, n)), " ")
}

// featureFlags is the names NewFlags takes, failing unless its body is
// newFlagsOutline, its loop ranges over features split on "," with only a
// switch on each element, the switch's default returns an error, every other
// case appends an option of a named setter and only logs besides, and the
// cases of the two modes call setters of newFlagsModeSetters.
func (r vendoredAlertmanagerRelease) featureFlags(t *testing.T) []string {
	t.Helper()
	consts := map[string]string{}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range r.file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.CONST {
				continue
			}
			for _, spec := range d.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							value, err := strconv.Unquote(lit.Value)
							if err != nil {
								t.Fatalf("%s: const %s: %v", r.tag, name.Name, err)
							}
							consts[name.Name] = value
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil {
				funcs[d.Name.Name] = d
			}
		}
	}
	newFlags := funcs["NewFlags"]
	if newFlags == nil {
		t.Fatalf("%s: no func NewFlags", r.tag)
	}
	params := newFlags.Type.Params.List
	if last := params[len(params)-1]; len(last.Names) != 1 || last.Names[0].Name != "features" || r.text(t, last.Type) != "string" {
		t.Fatalf("%s: NewFlags's last parameter is not features string", r.tag)
	}
	body := newFlags.Body.List
	if len(body) != len(newFlagsOutline) {
		t.Fatalf("%s: NewFlags has %d statements, newFlagsOutline %d", r.tag, len(body), len(newFlagsOutline))
	}
	var names []string
	for i, stmt := range body {
		if newFlagsOutline[i] != newFlagsLoop {
			if got := r.collapsed(t, stmt); got != newFlagsOutline[i] {
				t.Fatalf("%s: NewFlags statement %d is %q, newFlagsOutline has %q", r.tag, i+1, got, newFlagsOutline[i])
			}
			continue
		}
		loop, ok := stmt.(*ast.RangeStmt)
		if !ok || loop.Tok != token.DEFINE {
			t.Fatalf("%s: NewFlags statement %d is not a loop defining its variable", r.tag, i+1)
		}
		element := loop.Key
		if loop.Value != nil {
			if r.text(t, loop.Key) != "_" {
				t.Fatalf("%s: NewFlags's loop keeps the index", r.tag)
			}
			element = loop.Value
		}
		if r.text(t, element) != "feature" {
			t.Fatalf("%s: NewFlags's loop variable is not feature", r.tag)
		}
		if x := r.text(t, loop.X); x != `strings.Split(features, ",")` && x != `strings.SplitSeq(features, ",")` {
			t.Fatalf("%s: NewFlags loops over %s, not features split on \",\"", r.tag, x)
		}
		if len(loop.Body.List) != 1 {
			t.Fatalf("%s: NewFlags's loop is not one switch", r.tag)
		}
		sw, ok := loop.Body.List[0].(*ast.SwitchStmt)
		if !ok || sw.Init != nil || sw.Tag == nil || r.text(t, sw.Tag) != "feature" {
			t.Fatalf("%s: NewFlags's loop is not a switch on feature", r.tag)
		}
		refusesOthers := false
		for _, c := range sw.Body.List {
			cc := c.(*ast.CaseClause)
			if cc.List == nil {
				if len(cc.Body) == 1 {
					ret, ok := cc.Body[0].(*ast.ReturnStmt)
					refusesOthers = ok && len(ret.Results) == 2 && r.text(t, ret.Results[0]) == "nil" &&
						strings.HasPrefix(r.text(t, ret.Results[1]), "fmt.Errorf(") && strings.Contains(r.text(t, ret.Results[1]), "for --enable-feature")
				}
				continue
			}
			var appendCall []string
			if len(cc.Body) > 0 {
				appendCall = appendOption.FindStringSubmatch(r.collapsed(t, cc.Body[0]))
			}
			if appendCall == nil {
				t.Fatalf("%s: NewFlags case %s does not first append an option", r.tag, r.text(t, cc.List[0]))
			}
			for _, rest := range cc.Body[1:] {
				if _, ok := rest.(*ast.ExprStmt); !ok {
					t.Fatalf("%s: NewFlags case %s does more than append an option and log: %s", r.tag, r.text(t, cc.List[0]), r.collapsed(t, rest))
				}
			}
			for _, e := range cc.List {
				id, ok := e.(*ast.Ident)
				if !ok {
					t.Fatalf("%s: NewFlags case %s is not a constant", r.tag, r.text(t, e))
				}
				value, known := consts[id.Name]
				if !known {
					t.Fatalf("%s: NewFlags case %s is not a string constant of the file", r.tag, id.Name)
				}
				names = append(names, value)
				if want, mode := newFlagsModeSetters[value]; mode {
					setter := funcs[appendCall[1]]
					if setter == nil || r.collapsed(t, setter.Body) != want {
						t.Fatalf("%s: NewFlags case %s calls %s, whose body is not %q", r.tag, id.Name, appendCall[1], want)
					}
				}
			}
		}
		if !refusesOthers {
			t.Fatalf("%s: NewFlags's switch on feature does not only return an error by default", r.tag)
		}
	}
	slices.Sort(names)
	return names
}

// TestAlertmanagerFeatureFlags_MatchVendoredSource holds alertmanagerFeatureFlags
// to Alertmanager's source in both directions: each vendored release's minor
// has a row whose names are exactly those NewFlags takes, each row has a
// vendored release, and the rows run from the operator's first version to
// read enableFeatures through the operator's default version, one minor each.
func TestAlertmanagerFeatureFlags_MatchVendoredSource(t *testing.T) {
	releases := loadVendoredAlertmanager(t)
	for _, r := range releases {
		i := slices.IndexFunc(alertmanagerFeatureFlags, func(row alertmanagerFeatureFlagsOfMinor) bool { return row.minor == r.version.Minor })
		got := r.featureFlags(t)
		if i < 0 {
			t.Errorf("%s is vendored and alertmanagerFeatureFlags has no row for 0.%d; NewFlags takes %v", r.tag, r.version.Minor, got)
			continue
		}
		if want := alertmanagerFeatureFlags[i].names; !slices.Equal(got, want) {
			t.Errorf("alertmanagerFeatureFlags row 0.%d is %v; NewFlags at %s takes %v", r.version.Minor, want, r.tag, got)
		}
	}
	for _, row := range alertmanagerFeatureFlags {
		if !slices.ContainsFunc(releases, func(r vendoredAlertmanagerRelease) bool { return r.version.Minor == row.minor }) {
			t.Errorf("alertmanagerFeatureFlags has a row for 0.%d and no release of it is vendored; re-run scripts/vendor-alertmanager-features.sh", row.minor)
		}
		if !slices.IsSorted(row.names) {
			t.Errorf("alertmanagerFeatureFlags row 0.%d is not sorted: %v", row.minor, row.names)
		}
	}

	gate := slices.IndexFunc(alertmanagerVersionGates, func(g versionGate[monitoringv1.AlertmanagerSpec]) bool { return g.path == "enableFeatures" })
	if gate < 0 {
		t.Fatal("alertmanagerVersionGates has no enableFeatures gate")
	}
	first := semver.MustParse(alertmanagerVersionGates[gate].minimum)
	last := semver.MustParse(strings.TrimPrefix(alertmanagerDefaultVersion, "v"))
	if first.Major != 0 || last.Major != 0 {
		t.Fatalf("the enableFeatures gate (%s) and the default version (%s) are not 0.x, as the table's rows are", first, last)
	}
	for i, row := range alertmanagerFeatureFlags {
		if want := first.Minor + uint64(i); row.minor != want {
			t.Fatalf("alertmanagerFeatureFlags row %d is 0.%d, not 0.%d: the rows run one minor each from 0.%d, the first the operator passes enableFeatures to", i, row.minor, want, first.Minor)
		}
	}
	if n := len(alertmanagerFeatureFlags); n == 0 || alertmanagerFeatureFlags[n-1].minor < last.Minor {
		t.Errorf("alertmanagerFeatureFlags stops before 0.%d, the operator's default version %s: an unset version would be judged at no row", last.Minor, alertmanagerDefaultVersion)
	}
}
