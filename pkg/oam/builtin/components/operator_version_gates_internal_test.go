package components

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
)

// vendoredPromOpDir holds the excerpt of the Prometheus operator's source the
// version gates of the kinds whose pods it runs are held to
// (monitoring_version_gates.go): copied unmodified from
// prometheus-operator/prometheus-operator at the release of the linked
// monitoring API, under its Apache-2.0 licence, by
// scripts/vendor-prometheus-operator-gates.sh, with the project's LICENSE and
// NOTICE. Its SOURCE file names the tag and the git blob id of each file.
const vendoredPromOpDir = "testdata/upstream/prometheus-operator"

// vendoredPromOp is the parsed excerpt, by the path of each file in the
// operator's repository.
type vendoredPromOp struct {
	fset  *token.FileSet
	tag   string
	files map[string]*ast.File
}

// vendoredBlobID is the git blob id of content, the id SOURCE names a file by.
func vendoredBlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// linkedPromOpVersion is the version go.mod links the operator's monitoring
// API at, the release the excerpt must be cut from.
func linkedPromOpVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.Lines(string(data)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring" {
			return f[1]
		}
	}
	t.Fatal("go.mod does not link github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring")
	return ""
}

// loadVendoredPromOp reads SOURCE and parses every Go file it names, failing
// on a file whose blob id differs, a Go file without its licence header, a
// file in the directory that SOURCE does not name, an excerpt without the
// licence's full text or the project's NOTICE (Apache-2.0 asks a
// redistributor to pass on both), and a tag other than the linked release.
func loadVendoredPromOp(t *testing.T) *vendoredPromOp {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vendoredPromOpDir, "SOURCE"))
	if err != nil {
		t.Fatalf("read SOURCE: %v", err)
	}
	src := &vendoredPromOp{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	listed := map[string]bool{"SOURCE": true}
	read := func(rel, blob string) []byte {
		listed[rel] = true
		body, err := os.ReadFile(filepath.Join(vendoredPromOpDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("SOURCE names %s: %v", rel, err)
		}
		if got := vendoredBlobID(body); got != blob {
			t.Fatalf("%s has blob id %s, SOURCE says %s: the excerpt is copied unmodified; re-run scripts/vendor-prometheus-operator-gates.sh", rel, got, blob)
		}
		return body
	}
	licence, notice := false, false
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || strings.HasPrefix(line, "#"):
		case fields[0] == "tag" && len(fields) == 2:
			src.tag = fields[1]
		case fields[0] == "tag-object" && len(fields) == 2:
			// Provenance for whoever refreshes the excerpt; offline, the test
			// holds the tag to go.mod and each file to its blob id.
		case fields[0] == "licence" && len(fields) == 3:
			body := read(fields[1], fields[2])
			if !bytes.Contains(body, []byte("Apache License")) || !bytes.Contains(body, []byte("Version 2.0, January 2004")) {
				t.Fatalf("%s is not the Apache License, Version 2.0", fields[1])
			}
			licence = true
		case fields[0] == "notice" && len(fields) == 3:
			read(fields[1], fields[2])
			notice = true
		case fields[0] == "file" && len(fields) == 3:
			rel := fields[1]
			body := read(rel, fields[2])
			if !bytes.Contains(body, []byte("Licensed under the Apache License, Version 2.0")) {
				t.Fatalf("%s does not carry its Apache-2.0 header", rel)
			}
			file, err := parser.ParseFile(src.fset, rel, body, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			src.files[rel] = file
		default:
			t.Fatalf("SOURCE: unreadable line %q", line)
		}
	}
	err = filepath.WalkDir(vendoredPromOpDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(vendoredPromOpDir, p)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s is in the excerpt and not named in SOURCE", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", vendoredPromOpDir, err)
	}
	if !licence || !notice {
		t.Fatalf("SOURCE names licence %v, notice %v: the excerpt carries the Apache License, Version 2.0 and the project's NOTICE", licence, notice)
	}
	if linked := linkedPromOpVersion(t); src.tag != linked {
		t.Fatalf("the excerpt is of %s, go.mod links %s: re-run scripts/vendor-prometheus-operator-gates.sh %s", src.tag, linked, linked)
	}
	return src
}

// stringConst is the value of the package-level string constant name of the
// excerpt's file rel.
func (v *vendoredPromOp) stringConst(t *testing.T, rel, name string) string {
	t.Helper()
	file := v.files[rel]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", rel)
	}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if id.Name != name || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: %s is not a string literal", rel, name)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %s: %v", rel, name, err)
				}
				return s
			}
		}
	}
	t.Fatalf("%s declares no constant %s", rel, name)
	return ""
}

// operatorGate is one comparison of a version in the operator's source: the
// condition of an if, the value of an assignment, or the statement it is in,
// with every literal version it compares against.
type operatorGate struct {
	file string
	// fn is the declaring function, as makeStatefulSetSpec or
	// (*httpClientConfig).sanitize.
	fn string
	// cond is the condition's source text, its whitespace collapsed.
	cond string
	// ordinal counts the gates of one file, function and condition from 1, in
	// source order: the same block test twice in one function.
	ordinal int
	minima  []string
	pos     token.Position
}

func (g operatorGate) key() string {
	return fmt.Sprintf("%s %s %q #%d", g.file, g.fn, g.cond, g.ordinal)
}

// versionComparisons are the methods of semver.Version that compare it.
var versionComparisons = map[string]bool{"GTE": true, "GT": true, "LTE": true, "LT": true, "EQ": true, "NE": true, "Compare": true, "Equals": true}

// versionGates walks the named files of the excerpt and returns every gate:
// a call of a comparison method of semver.Version on one argument, and a
// read of a version's Major, Minor or Patch. A comparison whose argument is
// not semver.MustParse of a string literal is reported, not guessed at: its
// minimum cannot be read.
func (v *vendoredPromOp) versionGates(t *testing.T, files ...string) []operatorGate {
	t.Helper()
	var gates []operatorGate
	for _, rel := range files {
		file := v.files[rel]
		if file == nil {
			t.Fatalf("the excerpt holds no %s", rel)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				// A comparison outside a function would escape the walk.
				ast.Inspect(decl, func(n ast.Node) bool {
					if isVersionRead(n) {
						t.Errorf("%s: a version comparison outside a function", v.fset.Position(n.Pos()))
					}
					return true
				})
				continue
			}
			gates = append(gates, v.funcGates(t, rel, fd)...)
		}
	}
	return gates
}

// isVersionRead reports whether n compares a version or reads one of its
// components.
func isVersionRead(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CallExpr:
		sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr)
		return ok && versionComparisons[sel.Sel.Name] && len(n.Args) == 1
	case *ast.SelectorExpr:
		switch n.Sel.Name {
		case "Major", "Minor", "Patch":
			return true
		}
	}
	return false
}

// calledAt reports whether sel, whose ancestors are path, is the function of
// a call, parentheses around it aside.
func calledAt(path []ast.Node, sel *ast.SelectorExpr) bool {
	for i := len(path) - 1; i >= 0; i-- {
		switch p := path[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.CallExpr:
			return ast.Unparen(p.Fun) == sel
		}
		return false
	}
	return false
}

// funcName is fd's name with its receiver: makeStatefulSetSpec,
// (*httpClientConfig).sanitize.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	var b bytes.Buffer
	printer.Fprint(&b, token.NewFileSet(), fd.Recv.List[0].Type)
	return "(" + b.String() + ")." + fd.Name.Name
}

func (v *vendoredPromOp) funcGates(t *testing.T, rel string, fd *ast.FuncDecl) []operatorGate {
	t.Helper()
	fn := funcName(fd)
	var stack []ast.Node
	byNode := map[ast.Node]*operatorGate{}
	var order []ast.Node
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		defer func() { stack = append(stack, n) }()
		if sel, ok := n.(*ast.SelectorExpr); ok && versionComparisons[sel.Sel.Name] && !calledAt(stack, sel) {
			t.Errorf("%s: %s takes the comparison method %s as a value; the test cannot read its minimum", v.fset.Position(n.Pos()), fn, sel.Sel.Name)
			return true
		}
		if !isVersionRead(n) {
			return true
		}
		minimum := ""
		if call, ok := n.(*ast.CallExpr); ok {
			minimum = mustParseLiteral(call.Args[0])
			if minimum == "" {
				t.Errorf("%s: %s compares a version with something other than semver.MustParse of a literal; the test cannot read its minimum", v.fset.Position(n.Pos()), fn)
				return true
			}
		}
		at := gateNode(append(stack, n))
		g := byNode[at]
		if g == nil {
			g = &operatorGate{file: rel, fn: fn, cond: v.text(at), pos: v.fset.Position(at.Pos())}
			byNode[at] = g
			order = append(order, at)
		}
		if minimum != "" {
			g.minima = append(g.minima, minimum)
		}
		return true
	})
	var gates []operatorGate
	seen := map[string]int{}
	for _, at := range order {
		g := *byNode[at]
		seen[g.cond]++
		g.ordinal = seen[g.cond]
		gates = append(gates, g)
	}
	return gates
}

// mustParseLiteral is the version of semver.MustParse("<v>"), "" for any
// other expression.
func mustParseLiteral(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "MustParse" {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "semver" {
		return ""
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// gateNode is what the innermost statement around the last node of path
// makes of it: the condition of an if, the value of an assignment, or the
// statement itself.
func gateNode(path []ast.Node) ast.Node {
	for i := len(path) - 1; i >= 0; i-- {
		switch s := path[i].(type) {
		case *ast.IfStmt:
			// Only a comparison in the condition is the if's own: one in its
			// body has a statement nearer.
			return s.Cond
		case *ast.AssignStmt:
			if len(s.Rhs) == 1 {
				return s.Rhs[0]
			}
			return s
		case ast.Stmt:
			return s
		}
	}
	return path[len(path)-1]
}

// text is n's source, its whitespace collapsed.
func (v *vendoredPromOp) text(n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, v.fset, n); err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// gateRow classifies one gate of the operator's source for a kind: the
// kind's fields it gates (each a row of the kind's versionGate table at the
// gate's minimum), the floor of the kind's version check, or why it is not
// the kind's.
type gateRow struct {
	file, fn, cond string
	ordinal        int // 0 is 1
	paths          []string
	floor          bool
	not            string
}

// holdVersionGates holds a kind's gate table to the gates of the operator's
// source: every gate is classified exactly once, by a row of rows or, for
// every gate of a function, by funcs (keyed "<file> <fn>", with the reason);
// each row and each funcs entry names a gate; a row's paths are rows of
// table at the gate's one minimum, and every row of table is named; and the
// floor's minimum is floor.
func holdVersionGates[S any](t *testing.T, gates []operatorGate, rows []gateRow, funcs map[string]string, table []versionGate[S], floor string) {
	t.Helper()
	minimum := map[string]string{}
	for _, g := range table {
		if _, dup := minimum[g.path]; dup {
			t.Errorf("the table lists %s twice", g.path)
		}
		if _, err := semver.Parse(g.minimum); err != nil {
			t.Errorf("%s: minimum %q: %v", g.path, g.minimum, err)
		}
		minimum[g.path] = g.minimum
	}
	byKey := map[string]int{}
	for i, r := range rows {
		n := r.ordinal
		if n == 0 {
			n = 1
		}
		k := operatorGate{file: r.file, fn: r.fn, cond: r.cond, ordinal: n}.key()
		if _, dup := byKey[k]; dup {
			t.Errorf("two rows classify %s", k)
		}
		byKey[k] = i
		kinds := 0
		for _, set := range []bool{len(r.paths) > 0, r.floor, r.not != ""} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			t.Errorf("the row of %s names %d of paths, floor and a reason; want exactly one", k, kinds)
		}
	}
	usedRows := make([]bool, len(rows))
	usedFuncs := map[string]bool{}
	named := map[string]bool{}
	for _, g := range gates {
		i, ok := byKey[g.key()]
		_, byFunc := funcs[g.file+" "+g.fn]
		switch {
		case ok && byFunc:
			t.Errorf("%s: %s is classified by a row and by its function", g.pos, g.key())
		case byFunc:
			usedFuncs[g.file+" "+g.fn] = true
			continue
		case !ok:
			t.Errorf("%s: %s, comparing against %v, is not classified: name the kind's fields it gates, or why it is not the kind's", g.pos, g.key(), g.minima)
			continue
		}
		usedRows[i] = true
		r := rows[i]
		switch {
		case r.floor:
			if !slices.Equal(g.minima, []string{floor}) {
				t.Errorf("%s: the floor compares against %v, the kind refuses under %s", g.pos, g.minima, floor)
			}
		case len(r.paths) > 0:
			if len(g.minima) != 1 {
				t.Errorf("%s: %s compares against %v; a gate of the kind's fields has one minimum", g.pos, g.key(), g.minima)
				continue
			}
			for _, p := range r.paths {
				named[p] = true
				m, ok := minimum[p]
				switch {
				case !ok:
					t.Errorf("%s: %s gates %s, which the kind's table does not list", g.pos, g.key(), p)
				case m != g.minima[0]:
					t.Errorf("%s: %s gates %s at %s; the kind's table says %s", g.pos, g.key(), p, g.minima[0], m)
				}
			}
		}
	}
	for i, r := range rows {
		if !usedRows[i] {
			t.Errorf("the row of %s %s %q #%d names no gate of the source", r.file, r.fn, r.cond, max(r.ordinal, 1))
		}
	}
	for k := range funcs {
		if !usedFuncs[k] {
			t.Errorf("%s holds no gate of the source", k)
		}
	}
	for _, g := range table {
		if !named[g.path] {
			t.Errorf("the kind's table lists %s, which no gate of the source names", g.path)
		}
	}
}
