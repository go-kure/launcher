package components

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The walker of operator_version_gates_internal_test.go, held to small
// synthetic sources: what it reads as a gate and a version, and what
// holdVersionGates accepts and refuses of a kind's rows and tables.

// walkerSources is a synthetic excerpt: one package, pkg/a, split over two
// files, and another, pkg/b, whose constant pkg/a cannot resolve.
var walkerSources = map[string]string{
	"pkg/a/gen.go": `package a

const minConst = "2.3.0"

var matrix = []string{"v2.0.0", "v2.1.0", "v2.2.0"}

func gates(cg *ConfigGenerator, v semver.Version) {
	cg.WithMinimumVersion("2.1.0").Append("a")
	cg.WithMaximumVersion("2.2.0").Append("b")
	if v.GTE(semver.MustParse("2.6.0")) {
	}
	if v.LT(semver.MustParse(minConst)) {
	}
	if v.GTE(minVar) {
	}
	if v.GTE(semver.MustParse(otherConst)) {
	}
	cg.WithMinimumVersion("2.7.0").WithMaximumVersion("2.8.0").Append("c")
	if v.Major >= 3 {
	}
	client.Patch(ctx)
}

func shadowed(v semver.Version) {
	minConst := "9.9.9"
	if v.GTE(semver.MustParse(minConst)) {
	}
}

func limits(cg *ConfigGenerator, k limitKey) {
	cg.WithMinimumVersion(k.minVersion).Append("d")
}

func attach(cg *ConfigGenerator, c *attachConfig) {
	cg.WithMinimumVersion(c.MinimumVersion).Append("e")
}

func callers() {
	merge(x, "2.9.0")
	merge(y, "2.10.0")
}

func scrapeObjects(cg *ConfigGenerator, minVersion string) {
	cg.WithMinimumVersion(minVersion).Append("f")
}

func NewGen(version semver.Version) {
	if version.Major != 2 && version.Major != 3 {
	}
}
`,
	"pkg/a/vars.go": `package a

var minVar = semver.MustParse("2.4.0")

var keyA = limitKey{minVersion: "2.5.0"}

var keyB = limitKey{name: "b"}
`,
	"pkg/b/b.go": `package b

const otherConst = "2.0.0"
`,
	"pkg/c/value.go": `package c

var outside = v.GTE(semver.MustParse("1.0.0"))

func value(v semver.Version) {
	f := v.GTE
	_ = f
}

func guard(cg *ConfigGenerator) {
	g := cg.WithMaximumVersion
	_ = g
}
`,
	"pkg/d/forms.go": `package d

var atLeast = semver.MustParseRange(">=2.30.0")

func methodExpr(v semver.Version) {
	if semver.Version.LT(v, semver.MustParse("3.0.0")) {
	}
}

func pointerMethodExpr(v semver.Version) {
	if (*semver.Version).GTE(&v, semver.MustParse("2.40.0")) {
	}
}

func mustRange(v semver.Version) {
	if semver.MustParseRange(">=2.30.0 <3.0.0")(v) {
	}
}

func parsedRange(v semver.Version) {
	r, _ := semver.ParseRange(">=2.30.0")
	_ = r(v)
}

func notAComparison(v semver.Version) {
	_ = semver.Version.String(v)
}
`,
	"pkg/d/typealias.go": `package d

import (
	"cmp"

	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
)

type V = semver.Version

func typeAlias(v V) bool {
	return V.LT(v, semver.MustParse("3.0.0"))
}

func otherPackages(a, b string) bool {
	return cmp.Compare(a, b) < 0 && corev1.Compare(a, b)
}
`,
	"pkg/e/alias.go": `package e

import sv "github.com/blang/semver/v4"

func aliased(v sv.Version) bool {
	return sv.Version.LT(v, sv.MustParse("3.0.0")) || sv.MustParseRange(">=2.30.0")(v)
}
`,
	"pkg/e/dot.go": `package e

import . "github.com/blang/semver/v4"

func dotted(v Version) bool {
	return MustParseRange(">=2.30.0")(v)
}
`,
	"pkg/e/plain.go": `package e

import (
	_ "github.com/blang/semver/v4"
	semver "github.com/blang/semver/v4"
)

func plain(v semver.Version) bool {
	return v.GTE(semver.MustParse("2.6.0"))
}
`,
}

// syntheticPromOp parses walkerSources as an excerpt.
func syntheticPromOp(t *testing.T) *vendoredPromOp {
	t.Helper()
	src := &vendoredPromOp{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for rel, body := range walkerSources {
		file, err := parser.ParseFile(src.fset, rel, body, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		src.files[rel] = file
	}
	return src
}

// gateRecorder is a testing.TB that records what the walker reports instead
// of failing the test.
type gateRecorder struct {
	testing.TB
	errs []string
}

func (r *gateRecorder) Helper() {}

func (r *gateRecorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *gateRecorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

// recordGateErrors runs f with a gateRecorder, in its own goroutine so that
// a Fatalf ends f alone, and returns what it reported.
func recordGateErrors(t *testing.T, f func(tb testing.TB)) []string {
	r := &gateRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r.errs
}

// TestVersionGateWalker_Reads holds what the walker reads of each gate: W1
// the bound of WithMinimumVersion and WithMaximumVersion, apart; W2 a
// literal, semver.MustParse of one, a constant or a variable of the same
// package, and not one of another package or a shadowed name; W3 an
// argument it cannot resolve, recorded as unread; W5 a read of Major as a
// gate without a bound, and a called Patch as none.
func TestVersionGateWalker_Reads(t *testing.T) {
	src := syntheticPromOp(t)
	type read struct {
		fn, cond               string
		minima, maxima, unread []string
	}
	want := []read{
		{"gates", `cg.WithMinimumVersion("2.1.0").Append("a")`, []string{"2.1.0"}, nil, nil},
		{"gates", `cg.WithMaximumVersion("2.2.0").Append("b")`, nil, []string{"2.2.0"}, nil},
		{"gates", `v.GTE(semver.MustParse("2.6.0"))`, []string{"2.6.0"}, nil, nil},
		{"gates", `v.LT(semver.MustParse(minConst))`, []string{"2.3.0"}, nil, nil},
		{"gates", `v.GTE(minVar)`, []string{"2.4.0"}, nil, nil},
		{"gates", `v.GTE(semver.MustParse(otherConst))`, nil, nil, []string{"minimum"}},
		{"gates", `cg.WithMinimumVersion("2.7.0").WithMaximumVersion("2.8.0").Append("c")`, []string{"2.7.0"}, []string{"2.8.0"}, nil},
		{"gates", `v.Major >= 3`, nil, nil, nil},
		{"shadowed", `v.GTE(semver.MustParse(minConst))`, nil, nil, []string{"minimum"}},
		{"limits", `cg.WithMinimumVersion(k.minVersion).Append("d")`, nil, nil, []string{"minimum"}},
		{"attach", `cg.WithMinimumVersion(c.MinimumVersion).Append("e")`, nil, nil, []string{"minimum"}},
		{"scrapeObjects", `cg.WithMinimumVersion(minVersion).Append("f")`, nil, nil, []string{"minimum"}},
		{"NewGen", `version.Major != 2 && version.Major != 3`, nil, nil, nil},
	}
	var got []read
	for _, g := range src.versionGates(t, "pkg/a/gen.go") {
		got = append(got, read{g.fn, g.cond, g.minima, g.maxima, g.unread})
	}
	if len(got) != len(want) {
		t.Fatalf("the walker reads %d gates, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.fn != w.fn || g.cond != w.cond || !slices.Equal(g.minima, w.minima) || !slices.Equal(g.maxima, w.maxima) || !slices.Equal(g.unread, w.unread) {
			t.Errorf("gate %d = %+v, want %+v", i, g, w)
		}
	}

	// A comparison outside a function, or a comparison method or version
	// guard taken as a value, cannot be classified; the walker reports each.
	errs := recordGateErrors(t, func(tb testing.TB) { src.versionGates(tb, "pkg/c/value.go") })
	for _, want := range []string{"a version comparison outside a function", "takes the version method GTE as a value", "takes the version method WithMaximumVersion as a value"} {
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, want) }) {
			t.Errorf("pkg/c/value.go: reported %q, want one mentioning %q", errs, want)
		}
	}

	// W11 a method expression of a comparison method of semver.Version, and
	// a range of semver, in a function or outside one, carry no bound the
	// walker reads; it reports each, and nothing of a method expression that
	// compares nothing.
	errs = recordGateErrors(t, func(tb testing.TB) { src.versionGates(tb, "pkg/d/forms.go") })
	wantForms := []string{
		"pkg/d/forms.go:3:15: a version comparison outside a function",
		"pkg/d/forms.go:6:5: methodExpr compares a version through the method expression semver.Version.LT, on 2 arguments; the test cannot read its bound",
		"pkg/d/forms.go:11:5: pointerMethodExpr compares a version through the method expression (*semver.Version).GTE, on 2 arguments; the test cannot read its bound",
		"pkg/d/forms.go:16:5: mustRange compares a version through the range semver.MustParseRange; the test cannot read its bound",
		"pkg/d/forms.go:21:10: parsedRange compares a version through the range semver.ParseRange; the test cannot read its bound",
	}
	if !slices.Equal(errs, wantForms) {
		t.Errorf("pkg/d/forms.go: reported\n%s\nwant\n%s", strings.Join(errs, "\n"), strings.Join(wantForms, "\n"))
	}

	// W13 the method expression is read by its call, not by the name of its
	// receiver type: through a type alias of semver.Version it is reported
	// too, and a function of another package the file imports, as
	// cmp.Compare(a, b), is no version method.
	errs = recordGateErrors(t, func(tb testing.TB) { src.versionGates(tb, "pkg/d/typealias.go") })
	wantAlias := []string{"pkg/d/typealias.go:13:9: typeAlias compares a version through the method expression V.LT, on 2 arguments; the test cannot read its bound"}
	if !slices.Equal(errs, wantAlias) {
		t.Errorf("pkg/d/typealias.go: reported %q, want %q", errs, wantAlias)
	}

	// W12 semver imported under another name, or dot-imported, would let a
	// comparison or a range escape the walk, which reads semver by that
	// name; the walker reports the import. One imported as itself, beside a
	// blank import, is walked.
	for rel, want := range map[string][]string{
		"pkg/e/alias.go": {
			"pkg/e/alias.go:3:8: pkg/e/alias.go imports github.com/blang/semver/v4 as sv; the walker reads it only as semver",
			"pkg/e/alias.go:6:9: aliased compares a version through the method expression sv.Version.LT, on 2 arguments; the test cannot read its bound",
		},
		"pkg/e/dot.go":   {"pkg/e/dot.go:3:8: pkg/e/dot.go imports github.com/blang/semver/v4 as .; the walker reads it only as semver"},
		"pkg/e/plain.go": nil,
	} {
		if errs := recordGateErrors(t, func(tb testing.TB) { src.versionGates(tb, rel) }); !slices.Equal(errs, want) {
			t.Errorf("%s: reported %q, want %q", rel, errs, want)
		}
	}
}

// TestVersionGateWalker_SourceReads holds the helpers that read a bound from
// the source itself: W7 an element of a []string literal, W9 a field of a
// struct literal (empty where it is left out), W10 the literal each call of a
// helper passes.
func TestVersionGateWalker_SourceReads(t *testing.T) {
	src := syntheticPromOp(t)
	for i, want := range map[int]string{-1: "v2.2.0", 0: "v2.0.0"} {
		if got := src.stringSliceElem(t, "pkg/a/gen.go", "matrix", i); got != want {
			t.Errorf("matrix[%d] = %s, want %s", i, got, want)
		}
	}
	for name, want := range map[string]string{"keyA": "2.5.0", "keyB": ""} {
		if got := src.structStringField(t, "pkg/a/vars.go", name, "minVersion"); got != want {
			t.Errorf("%s.minVersion = %q, want %q", name, got, want)
		}
	}
	want := []stringArgCall{{"callers", "2.9.0"}, {"callers", "2.10.0"}}
	if got := src.callStringArgs(t, "pkg/a/gen.go", "merge", 1); !slices.Equal(got, want) {
		t.Errorf("the calls of merge pass %v, want %v", got, want)
	}
}

// walkerKind is a kind's classification of walkerSources: rows, funcs and
// tables that holdVersionGates accepts.
type walkerKind struct {
	rows           []gateRow
	funcs          map[string]string
	minima, maxima []gateEntry
	floor          []string
}

func newWalkerKind(t *testing.T, src *vendoredPromOp) walkerKind {
	const gen = "pkg/a/gen.go"
	gate := func(fn, cond string, paths ...string) gateRow {
		return gateRow{file: gen, fn: fn, cond: cond, paths: paths}
	}
	not := func(fn, cond, why string) gateRow { return gateRow{file: gen, fn: fn, cond: cond, not: why} }
	attach := map[string]string{}
	for i, c := range src.callStringArgs(t, gen, "merge", 1) {
		attach[[]string{"pod", "svc"}[i]] = c.arg
	}
	return walkerKind{
		rows: []gateRow{
			gate("gates", `cg.WithMinimumVersion("2.1.0").Append("a")`, "a"),
			gate("gates", `cg.WithMaximumVersion("2.2.0").Append("b")`, "b"),
			gate("gates", `v.GTE(semver.MustParse("2.6.0"))`, "six"),
			gate("gates", `v.LT(semver.MustParse(minConst))`, "three"),
			gate("gates", `v.GTE(minVar)`, "four"),
			not("gates", `v.GTE(semver.MustParse(otherConst))`, "another package's version"),
			gate("gates", `cg.WithMinimumVersion("2.7.0").WithMaximumVersion("2.8.0").Append("c")`, "c"),
			{file: gen, fn: "gates", cond: `v.Major >= 3`, paths: []string{"major"}, major: "3.0.0-0"},
			not("shadowed", `v.GTE(semver.MustParse(minConst))`, "a local version"),
			{file: gen, fn: "limits", cond: `cg.WithMinimumVersion(k.minVersion).Append("d")`, bounds: map[string]string{
				"limA": src.structStringField(t, "pkg/a/vars.go", "keyA", "minVersion"),
				"limB": src.structStringField(t, "pkg/a/vars.go", "keyB", "minVersion"),
			}},
			{file: gen, fn: "attach", cond: `cg.WithMinimumVersion(c.MinimumVersion).Append("e")`, bounds: attach},
			{file: gen, fn: "NewGen", cond: `version.Major != 2 && version.Major != 3`, floor: true},
		},
		funcs: map[string]string{gen + " scrapeObjects": "another object's fields"},
		minima: []gateEntry{
			{"a", "2.1.0"}, {"six", "2.6.0"}, {"three", "2.3.0"}, {"four", "2.4.0"}, {"c", "2.7.0"},
			{"limA", "2.5.0"}, {"pod", "2.9.0"}, {"svc", "2.10.0"},
		},
		maxima: []gateEntry{{"b", "2.2.0"}, {"c", "2.8.0"}, {"major", "3.0.0-0"}},
		floor:  []string{},
	}
}

// TestVersionGateWalker_Hold holds holdVersionGates to what it accepts and
// refuses: the kind of walkerSources passes, and each change below fails
// with its message.
func TestVersionGateWalker_Hold(t *testing.T) {
	src := syntheticPromOp(t)
	gates := src.versionGates(t, "pkg/a/gen.go")
	hold := func(k walkerKind) []string {
		return recordGateErrors(t, func(tb testing.TB) { holdVersionGates(tb, gates, k.rows, k.funcs, k.minima, k.maxima, k.floor) })
	}
	if errs := hold(newWalkerKind(t, src)); len(errs) > 0 {
		t.Fatalf("the synthetic kind is refused: %q", errs)
	}
	without := func(rows []gateRow, cond string) []gateRow {
		return slices.DeleteFunc(slices.Clone(rows), func(r gateRow) bool { return r.cond == cond })
	}
	cases := []struct {
		name   string
		change func(k *walkerKind)
		want   string
	}{
		{"a gate without a row", func(k *walkerKind) {
			k.rows = without(k.rows, `cg.WithMinimumVersion("2.1.0").Append("a")`)
			k.minima = k.minima[1:]
		}, "is not classified"},
		{"a row without a gate", func(k *walkerKind) {
			k.rows = append(k.rows, gateRow{file: "pkg/a/gen.go", fn: "gates", cond: "nothing", not: "none"})
		}, "names no gate of the source"},
		{"a bound that differs", func(k *walkerKind) { k.minima[0].bound = "2.1.1" }, "the kind's table says 2.1.1"},
		{"a table id no gate names", func(k *walkerKind) { k.minima = append(k.minima, gateEntry{"z", "1.0.0"}) }, "lists z, which no gate of the source names"},
		{"an id listed twice", func(k *walkerKind) { k.minima = append(k.minima, gateEntry{"a", "2.1.0"}) }, "the minimum table lists a twice"},
		// W3: an unread version passes only where funcs or a not row
		// classifies it.
		{"an unread version outside funcs", func(k *walkerKind) { k.funcs = map[string]string{} }, "scrapeObjects"},
		{"an unread version under paths", func(k *walkerKind) {
			k.rows = without(k.rows, `v.GTE(semver.MustParse(otherConst))`)
			k.rows = append(k.rows, gateRow{file: "pkg/a/gen.go", fn: "gates", cond: `v.GTE(semver.MustParse(otherConst))`, paths: []string{"a"}})
		}, "compares against a version the walker cannot read"},
		// W4: a maximum is held to the maximum table only.
		{"a maximum as a minimum", func(k *walkerKind) {
			k.maxima = k.maxima[1:]
			k.minima = append(k.minima, gateEntry{"b", "2.2.0"})
		}, "gates b at the maximum 2.2.0, which the kind's maximum table does not list"},
		// W6: the floor's minima.
		{"a floor with a minimum", func(k *walkerKind) { k.floor = []string{"2.0.0"} }, "the floor compares against minima"},
		// W8: a major row's ids are ceilings at its major.
		{"a major ceiling elsewhere", func(k *walkerKind) { k.maxima[2].bound = "3.0.0" }, "gates major at the maximum 3.0.0-0; the kind's table says 3.0.0"},
		// W9: an empty bound is no gate.
		{"an id of an empty bound", func(k *walkerKind) { k.minima = append(k.minima, gateEntry{"limB", "2.0.0"}) }, "lists limB, which no gate of the source names"},
		{"a row and a function", func(k *walkerKind) { k.funcs["pkg/a/gen.go limits"] = "both" }, "is classified by a row and by its function"},
		{"a function without a gate", func(k *walkerKind) { k.funcs["pkg/a/gen.go callers"] = "none" }, "pkg/a/gen.go callers holds no gate of the source"},
		{"a row of two kinds", func(k *walkerKind) { k.rows[0].not = "also" }, "names 2 of paths, bounds, floor and a reason"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newWalkerKind(t, src)
			c.change(&k)
			errs := hold(k)
			if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
				t.Errorf("reported %q, want one mentioning %q", errs, c.want)
			}
		})
	}
}
