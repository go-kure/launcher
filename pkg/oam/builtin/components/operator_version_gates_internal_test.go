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
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
)

// vendoredPromOpDir holds the excerpt of the Prometheus operator's source the
// version gates of the kinds whose pods it runs are held to
// (monitoring_version_gates.go), and the CRDs of its kinds launcher writes:
// copied unmodified from prometheus-operator/prometheus-operator at the
// release of the linked monitoring API, under its Apache-2.0 licence, by
// scripts/vendor-prometheus-operator-gates.sh, with the project's LICENSE and
// NOTICE. Its SOURCE file names the tag and the git blob id of each file.
const vendoredPromOpDir = "testdata/upstream/prometheus-operator"

// vendoredPromOp is the parsed excerpt, by the path of each file in the
// operator's repository.
type vendoredPromOp struct {
	fset  *token.FileSet
	tag   string
	files map[string]*ast.File
	// crds are the CustomResourceDefinitions of the excerpt, by the path of
	// each in the operator's repository: the kinds' CRDs, which the linked
	// module does not ship (go-kure/launcher#883).
	crds map[string][]byte
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

// loadVendoredPromOp reads SOURCE, parses every Go file it names and reads
// every CRD it names (a `.yaml` file), failing on a file whose blob id
// differs, a Go file without its licence header, a CRD that is not one of
// monitoring.coreos.com, a file in the directory that SOURCE does not name, an
// excerpt without the licence's full text or the project's NOTICE (Apache-2.0
// asks a redistributor to pass on both), and a tag other than the linked
// release.
func loadVendoredPromOp(t *testing.T) *vendoredPromOp {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vendoredPromOpDir, "SOURCE"))
	if err != nil {
		t.Fatalf("read SOURCE: %v", err)
	}
	src := &vendoredPromOp{fset: token.NewFileSet(), files: map[string]*ast.File{}, crds: map[string][]byte{}}
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
		case fields[0] == "file" && len(fields) == 3 && strings.HasSuffix(fields[1], ".yaml"):
			rel := fields[1]
			body := read(rel, fields[2])
			if !bytes.Contains(body, []byte("kind: CustomResourceDefinition")) || !bytes.Contains(body, []byte("group: monitoring.coreos.com")) {
				t.Fatalf("%s is not a CustomResourceDefinition of monitoring.coreos.com", rel)
			}
			src.crds[rel] = body
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
func (v *vendoredPromOp) stringConst(t testing.TB, rel, name string) string {
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

// packageDecl is the expression the package-level constant or variable name
// of the excerpt's file rel is declared with.
func (v *vendoredPromOp) packageDecl(t testing.TB, rel, name string) ast.Expr {
	t.Helper()
	file := v.files[rel]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", rel)
	}
	if e := declaredValue(file, name); e != nil {
		return e
	}
	t.Fatalf("%s declares no constant or variable %s with a value", rel, name)
	return nil
}

// declaredValue is the expression the package-level constant or variable
// name of file is declared with, nil where it declares none.
func declaredValue(file *ast.File, name string) ast.Expr {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if id.Name == name && i < len(vs.Values) {
					return vs.Values[i]
				}
			}
		}
	}
	return nil
}

// stringLiteral is the value of a string literal, ok false for any other
// expression.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := ast.Unparen(e).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// stringSliceElem is element i, counted from the end where negative, of the
// package-level []string composite literal name of the excerpt's file rel.
func (v *vendoredPromOp) stringSliceElem(t testing.TB, rel, name string, i int) string {
	t.Helper()
	lit, ok := v.packageDecl(t, rel, name).(*ast.CompositeLit)
	if !ok {
		t.Fatalf("%s: %s is not a composite literal", rel, name)
	}
	if typ, ok := lit.Type.(*ast.ArrayType); !ok || typ.Len != nil || v.text(typ.Elt) != "string" {
		t.Fatalf("%s: %s is not a []string literal", rel, name)
	}
	if i < 0 {
		i += len(lit.Elts)
	}
	if i < 0 || i >= len(lit.Elts) {
		t.Fatalf("%s: %s has %d elements, none at %d", rel, name, len(lit.Elts), i)
	}
	s, ok := stringLiteral(lit.Elts[i])
	if !ok {
		t.Fatalf("%s: element %d of %s is not a string literal", rel, i, name)
	}
	return s
}

// structStringField is the string field field of the package-level variable
// name of the excerpt's file rel, declared with a keyed composite literal: ""
// where the literal leaves the field out.
func (v *vendoredPromOp) structStringField(t testing.TB, rel, name, field string) string {
	t.Helper()
	lit, ok := v.packageDecl(t, rel, name).(*ast.CompositeLit)
	if !ok {
		t.Fatalf("%s: %s is not a composite literal", rel, name)
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			t.Fatalf("%s: %s is not a keyed literal", rel, name)
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
			s, ok := stringLiteral(kv.Value)
			if !ok {
				t.Fatalf("%s: %s.%s is not a string literal", rel, name, field)
			}
			return s
		}
	}
	return ""
}

// stringArgCall is one call that callStringArgs reads: the function that
// makes it and the string literal it passes.
type stringArgCall struct {
	fn, arg string
}

// callStringArgs is every call of the function or method callee in the
// excerpt's file rel, in source order, with the string literal each passes
// as its argument at index arg. A call that passes anything else fails the
// test.
func (v *vendoredPromOp) callStringArgs(t testing.TB, rel, callee string, arg int) []stringArgCall {
	t.Helper()
	file := v.files[rel]
	if file == nil {
		t.Fatalf("the excerpt holds no %s", rel)
	}
	var calls []stringArgCall
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch f := ast.Unparen(call.Fun).(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			if name != callee {
				return true
			}
			if arg >= len(call.Args) {
				t.Fatalf("%s: %s calls %s with %d arguments", v.fset.Position(call.Pos()), funcName(fd), callee, len(call.Args))
			}
			s, ok := stringLiteral(call.Args[arg])
			if !ok {
				t.Fatalf("%s: %s passes %s argument %d that is not a string literal", v.fset.Position(call.Pos()), funcName(fd), callee, arg)
			}
			calls = append(calls, stringArgCall{fn: funcName(fd), arg: s})
			return true
		})
	}
	return calls
}

// operatorGate is one gate of the operator's source: the condition of an if,
// the value of an assignment, or the statement it is in, with the versions
// its comparisons read.
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
	// minima are the versions it compares against: the argument of a
	// comparison method of semver.Version or of WithMinimumVersion.
	minima []string
	// maxima are the arguments of WithMaximumVersion, from which on the
	// operator drops what is appended through the generator it returns.
	maxima []string
	// unread holds, as "minimum" or "maximum", each comparison whose version
	// the walker cannot resolve (resolveVersion).
	unread []string
	pos    token.Position
}

func (g operatorGate) key() string {
	return fmt.Sprintf("%s %s %q #%d", g.file, g.fn, g.cond, g.ordinal)
}

// versionComparisons are the methods of semver.Version that compare it.
var versionComparisons = map[string]bool{"GTE": true, "GT": true, "LTE": true, "LT": true, "EQ": true, "NE": true, "Compare": true, "Equals": true}

// versionMethods are the methods readAt reads a bound from only where they
// are called: the comparisons, and the ConfigGenerator's version guards.
var versionMethods = func() map[string]bool {
	m := maps.Clone(versionComparisons)
	m["WithMinimumVersion"], m["WithMaximumVersion"] = true, true
	return m
}()

// versionRead is how a node of the source reads a version.
type versionRead int

const (
	noRead versionRead = iota
	// compared is a call of a comparison method of semver.Version on one
	// argument: its version is a minimum.
	compared
	// minimumOf is a call of the operator's WithMinimumVersion, whose
	// generator drops what is appended through it below its argument.
	minimumOf
	// maximumOf is a call of WithMaximumVersion, whose generator drops what
	// is appended through it from its argument on.
	maximumOf
	// component is a read of a version's Major, Minor or Patch.
	component
)

// readAt is how n, whose ancestors are stack, reads a version. A Major, Minor
// or Patch selector that is called is a method of another type (a client's
// Patch), not a version's component.
func readAt(n ast.Node, stack []ast.Node) versionRead {
	switch n := n.(type) {
	case *ast.CallExpr:
		sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr)
		if !ok || len(n.Args) != 1 {
			return noRead
		}
		switch {
		case versionComparisons[sel.Sel.Name]:
			return compared
		case sel.Sel.Name == "WithMinimumVersion":
			return minimumOf
		case sel.Sel.Name == "WithMaximumVersion":
			return maximumOf
		}
	case *ast.SelectorExpr:
		switch n.Sel.Name {
		case "Major", "Minor", "Patch":
			if !calledAt(stack, n) {
				return component
			}
		}
	}
	return noRead
}

// unreadForm names a comparison n makes that readAt reads no bound of: a
// call of a version method on other than one argument, as the method
// expression semver.Version.LT(v, w), whose bound is its second argument,
// whatever the receiver type is named (a type alias of semver.Version
// included); and any use of a range of semver, as
// semver.MustParseRange(">=2.30.0")(v), which holds no single bound. Neither
// occurs at v0.94.1; the walker fails on each, as on a version method taken
// as a value, so that a re-vendor that brings one in is classified by hand
// (go-kure/launcher#1024). A function of another package the file imports,
// as cmp.Compare(a, b), is no version method. "" is no such form.
func unreadForm(n ast.Node, others map[string]bool) string {
	if call, ok := n.(*ast.CallExpr); ok {
		sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		if pkg, isIdent := sel.X.(*ast.Ident); isIdent && others[pkg.Name] {
			return ""
		}
		if versionMethods[sel.Sel.Name] && len(call.Args) != 1 {
			var b bytes.Buffer
			printer.Fprint(&b, token.NewFileSet(), sel)
			return fmt.Sprintf("the method expression %s, on %d arguments", b.String(), len(call.Args))
		}
		return ""
	}
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "semver" && strings.Contains(sel.Sel.Name, "Range") {
		return "the range semver." + sel.Sel.Name
	}
	return ""
}

// semverImportPath is the import path of the semver package the vendored
// source compares versions with.
const semverImportPath = "github.com/blang/semver/v4"

// otherImports are the names file may refer to its imports by, semver's
// aside: the name given, or else the last element of the path and, where
// that is a major version (k8s.io/api/core/v1, a module's /v2), the element
// before it too, since only the package itself says which it declares.
func otherImports(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p == semverImportPath {
			continue
		}
		if imp.Name != nil {
			names[imp.Name.Name] = true
			continue
		}
		elems := strings.Split(p, "/")
		last := elems[len(elems)-1]
		names[last] = true
		if len(elems) > 1 && majorVersion.MatchString(last) {
			names[elems[len(elems)-2]] = true
		}
	}
	return names
}

// declImports are the otherImports of file that decl does not shadow: a name
// decl declares is not the import it shadows, as type cmp = semver.Version
// makes cmp.LT(v, w) a method expression of semver's, in a function or in a
// function literal outside one (go-kure/launcher#1033).
func declImports(file *ast.File, decl ast.Decl) map[string]bool {
	others := otherImports(file)
	for name := range localNames(decl) {
		delete(others, name)
	}
	return others
}

// majorVersion matches a path element naming a major version.
var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// renamedSemver returns file's import of semver under a name other than
// semver, a dot import included, or nil. The walker reads a comparison, a
// method expression or a range of semver by that name only (readAt,
// unreadForm, resolveVersion), so one made through another name would escape
// it (go-kure/launcher#1024). A blank import compares nothing.
func renamedSemver(file *ast.File) *ast.ImportSpec {
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == semverImportPath &&
			imp.Name != nil && imp.Name.Name != "semver" && imp.Name.Name != "_" {
			return imp
		}
	}
	return nil
}

// inspectWithStack calls visit for every node under root, with its
// ancestors, root first.
func inspectWithStack(root ast.Node, visit func(n ast.Node, stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		visit(n, stack)
		stack = append(stack, n)
		return true
	})
}

// versionGates walks the named files of the excerpt and returns every gate:
// a call of a comparison method of semver.Version on one argument, of
// WithMinimumVersion or of WithMaximumVersion, and a read of a version's
// Major, Minor or Patch. A version the walker cannot resolve is recorded as
// unread, which holdVersionGates fails unless the gate is classified as not
// the kind's or the test reads its bounds itself. A file that imports semver
// under another name fails the walk (renamedSemver).
func (v *vendoredPromOp) versionGates(t testing.TB, files ...string) []operatorGate {
	t.Helper()
	var gates []operatorGate
	for _, rel := range files {
		file := v.files[rel]
		if file == nil {
			t.Fatalf("the excerpt holds no %s", rel)
		}
		if imp := renamedSemver(file); imp != nil {
			t.Errorf("%s: %s imports %s as %s; the walker reads it only as semver", v.fset.Position(imp.Pos()), rel, semverImportPath, imp.Name.Name)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				// A comparison outside a function would escape the walk, and
				// so would a version method taken there as a value, as var
				// less = semver.Version.LT (go-kure/launcher#1033).
				others := declImports(file, decl)
				inspectWithStack(decl, func(n ast.Node, stack []ast.Node) {
					if readAt(n, stack) != noRead || unreadForm(n, others) != "" || methodValue(n, stack) != "" {
						t.Errorf("%s: a version comparison outside a function", v.fset.Position(n.Pos()))
					}
				})
				continue
			}
			gates = append(gates, v.funcGates(t, rel, fd)...)
		}
	}
	return gates
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

// methodValue names the version method n, whose ancestors are stack, takes
// as a value: a selector of one of versionMethods that is not called, as
// f := v.GTE or semver.Version.LT passed on. "" is none.
func methodValue(n ast.Node, stack []ast.Node) string {
	if sel, ok := n.(*ast.SelectorExpr); ok && versionMethods[sel.Sel.Name] && !calledAt(stack, sel) {
		return sel.Sel.Name
	}
	return ""
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

func (v *vendoredPromOp) funcGates(t testing.TB, rel string, fd *ast.FuncDecl) []operatorGate {
	t.Helper()
	fn := funcName(fd)
	locals := localNames(fd)
	others := declImports(v.files[rel], fd)
	byNode := map[ast.Node]*operatorGate{}
	var order []ast.Node
	inspectWithStack(fd, func(n ast.Node, stack []ast.Node) {
		if form := unreadForm(n, others); form != "" {
			t.Errorf("%s: %s compares a version through %s; the test cannot read its bound", v.fset.Position(n.Pos()), fn, form)
			return
		}
		if name := methodValue(n, stack); name != "" {
			t.Errorf("%s: %s takes the version method %s as a value; the test cannot read its bound", v.fset.Position(n.Pos()), fn, name)
			return
		}
		read := readAt(n, stack)
		if read == noRead {
			return
		}
		at := gateNode(append(slices.Clip(stack), n))
		g := byNode[at]
		if g == nil {
			g = &operatorGate{file: rel, fn: fn, cond: v.text(at), pos: v.fset.Position(at.Pos())}
			byNode[at] = g
			order = append(order, at)
		}
		if read == component {
			return
		}
		bound, ok := v.resolveVersion(rel, locals, n.(*ast.CallExpr).Args[0], 0)
		switch {
		case !ok && read == maximumOf:
			g.unread = append(g.unread, "maximum")
		case !ok:
			g.unread = append(g.unread, "minimum")
		case read == maximumOf:
			g.maxima = append(g.maxima, bound)
		default:
			g.minima = append(g.minima, bound)
		}
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

// localNames are the names decl declares: a function's receiver, parameters
// and results, and every name declared in its body or a function literal's,
// a type included, outside a function too. The walker resolves no such name
// to a package-level one or to an import, in whatever scope of decl it is
// declared: a name shadowed anywhere is judged as shadowed everywhere, which
// can only report a call more, never one less.
func localNames(decl ast.Decl) map[string]bool {
	names := map[string]bool{}
	add := func(ids ...*ast.Ident) {
		for _, id := range ids {
			names[id.Name] = true
		}
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			add(n.Names...)
		case *ast.ValueSpec:
			add(n.Names...)
		case *ast.TypeSpec:
			add(n.Name)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, e := range n.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						add(id)
					}
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						add(id)
					}
				}
			}
		}
		return true
	})
	return names
}

// resolveVersion is the version e names: a string literal; semver.MustParse
// of one; or a package-level constant or variable of rel's package, not
// shadowed by a name of locals, declared as either. ok is false for anything
// else: a parameter, a field, a call of another function.
func (v *vendoredPromOp) resolveVersion(rel string, locals map[string]bool, e ast.Expr, depth int) (string, bool) {
	if depth > 4 {
		return "", false
	}
	switch e := ast.Unparen(e).(type) {
	case *ast.BasicLit:
		return stringLiteral(e)
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "MustParse" || len(e.Args) != 1 {
			return "", false
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "semver" {
			return "", false
		}
		return v.resolveVersion(rel, locals, e.Args[0], depth+1)
	case *ast.Ident:
		if locals[e.Name] {
			return "", false
		}
		for other, file := range v.files {
			if path.Dir(other) != path.Dir(rel) {
				continue
			}
			if value := declaredValue(file, e.Name); value != nil {
				// A package-level value is resolved in its own scope.
				return v.resolveVersion(rel, nil, value, depth+1)
			}
		}
	}
	return "", false
}

// gateNode is what the innermost statement around the last node of path
// makes of it: the condition of an if, the tag of a switch, the expression of
// a case, the value of an assignment or a var declaration, or the statement
// itself.
func gateNode(path []ast.Node) ast.Node {
	for i := len(path) - 1; i >= 0; i-- {
		var child ast.Node
		if i+1 < len(path) {
			child = path[i+1]
		}
		switch s := path[i].(type) {
		case *ast.IfStmt:
			// Only a comparison in the condition is the if's own: one in its
			// body or its init has a statement nearer.
			return s.Cond
		case *ast.SwitchStmt:
			if child != nil && child == s.Tag {
				return s.Tag
			}
			return s
		case *ast.CaseClause:
			if e, ok := child.(ast.Expr); ok {
				return e
			}
			return s
		case *ast.ValueSpec:
			// One value of a var declaration, as of an assignment.
			if e, ok := child.(ast.Expr); ok && slices.Contains(s.Values, e) {
				return e
			}
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

// gateEntry is one row of a kind's table as holdVersionGates sees it: the id
// of a field and its bound, a minimum or a maximum.
type gateEntry struct {
	id, bound string
}

// gateEntriesOf is a versionGate table by path.
func gateEntriesOf[S any](gates []versionGate[S]) []gateEntry {
	entries := make([]gateEntry, len(gates))
	for i, g := range gates {
		entries[i] = gateEntry{g.path, g.minimum}
	}
	return entries
}

// idGateEntriesOf is an idGate table by id, or path where the id is empty.
func idGateEntriesOf[S any](table []idGate[S]) []gateEntry {
	entries := make([]gateEntry, len(table))
	for i, g := range table {
		id := g.id
		if id == "" {
			id = g.gate.path
		}
		entries[i] = gateEntry{id, g.gate.minimum}
	}
	return entries
}

// ceilingEntriesOf is a versionCeiling table by id, or path where the id is
// empty.
func ceilingEntriesOf[S any](table []versionCeiling[S]) []gateEntry {
	entries := make([]gateEntry, len(table))
	for i, c := range table {
		id := c.id
		if id == "" {
			id = c.path
		}
		entries[i] = gateEntry{id, c.maximum}
	}
	return entries
}

// gateRow classifies one gate of the operator's source for a kind, by
// exactly one of: the ids of the kind's fields it gates at its own bounds
// (paths); the bound of each id where the walker cannot read the gate's
// version and the test reads it from the source itself (bounds); the floor
// of the kind's version check; or why it is not the kind's (not).
type gateRow struct {
	file, fn, cond string
	ordinal        int // 0 is 1
	// paths are ids of the kind's tables, each at the gate's minimum, its
	// maximum, or both.
	paths []string
	// major, beside paths, is for a gate that reads only a version's Major:
	// the least version, verified by hand from the condition, from which the
	// operator drops the field. Each id of paths is a ceiling at it.
	major string
	// bounds is the bound of each id, read from the source by the test, for
	// a gate whose one version the walker cannot read. An empty bound is no
	// gate: the id is not in the kind's tables.
	bounds map[string]string
	floor  bool
	not    string
}

// holdVersionGates holds a kind's tables, minima and maxima (nil where the
// kind has no ceilings), to the gates of the operator's source: every gate is
// classified exactly once, by a row of rows or, for every gate of a function,
// by funcs (keyed "<file> <fn>", with the reason); each row and each funcs
// entry names a gate; an id a row names is in the table of each of its
// gate's bounds, at that bound, and every id of both tables is named; and the
// floor's minima are floor, none where the floor reads only the major.
func holdVersionGates(t testing.TB, gates []operatorGate, rows []gateRow, funcs map[string]string, minima, maxima []gateEntry, floor []string) {
	t.Helper()
	index := func(kind string, entries []gateEntry) map[string]string {
		bounds := map[string]string{}
		for _, e := range entries {
			if _, dup := bounds[e.id]; dup {
				t.Errorf("the %s table lists %s twice", kind, e.id)
			}
			if _, err := semver.Parse(e.bound); err != nil {
				t.Errorf("%s: %s %q: %v", e.id, kind, e.bound, err)
			}
			bounds[e.id] = e.bound
		}
		return bounds
	}
	tables := map[string]map[string]string{"minimum": index("minimum", minima), "maximum": index("maximum", maxima)}
	named := map[string]map[string]bool{"minimum": {}, "maximum": {}}
	check := func(g operatorGate, kind, id, bound string) {
		named[kind][id] = true
		b, ok := tables[kind][id]
		switch {
		case !ok:
			t.Errorf("%s: %s gates %s at the %s %s, which the kind's %s table does not list", g.pos, g.key(), id, kind, bound, kind)
		case b != bound:
			t.Errorf("%s: %s gates %s at the %s %s; the kind's table says %s", g.pos, g.key(), id, kind, bound, b)
		}
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
		for _, set := range []bool{len(r.paths) > 0, r.bounds != nil, r.floor, r.not != ""} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			t.Errorf("the row of %s names %d of paths, bounds, floor and a reason; want exactly one", k, kinds)
		}
		if r.major != "" && len(r.paths) == 0 {
			t.Errorf("the row of %s names a major ceiling without paths", k)
		}
	}
	usedRows := make([]bool, len(rows))
	usedFuncs := map[string]bool{}
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
			t.Errorf("%s: %s, comparing against minima %v, maxima %v and %d it cannot read, is not classified: name the kind's fields it gates, or why it is not the kind's", g.pos, g.key(), g.minima, g.maxima, len(g.unread))
			continue
		}
		usedRows[i] = true
		r := rows[i]
		switch {
		case r.floor:
			if len(g.unread) > 0 || len(g.maxima) > 0 || !slices.Equal(g.minima, floor) {
				t.Errorf("%s: the floor compares against minima %v, maxima %v and %d it cannot read; the kind refuses under %v", g.pos, g.minima, g.maxima, len(g.unread), floor)
			}
		case r.not != "":
		case r.bounds != nil:
			if len(g.unread) != 1 || len(g.minima)+len(g.maxima) > 0 {
				t.Errorf("%s: %s: bounds stand for one version the walker cannot read; it reads minima %v and maxima %v and cannot read %d", g.pos, g.key(), g.minima, g.maxima, len(g.unread))
				continue
			}
			for id, b := range r.bounds {
				if b != "" {
					check(g, g.unread[0], id, b)
				}
			}
		case len(g.unread) > 0:
			t.Errorf("%s: %s compares against a version the walker cannot read: name the bound of each id (bounds), or why it is not the kind's", g.pos, g.key())
		case r.major != "":
			if len(g.minima)+len(g.maxima) > 0 {
				t.Errorf("%s: %s compares against minima %v and maxima %v; a major ceiling stands for a read of the major alone", g.pos, g.key(), g.minima, g.maxima)
				continue
			}
			for _, id := range r.paths {
				check(g, "maximum", id, r.major)
			}
		default:
			if len(g.minima) > 1 || len(g.maxima) > 1 || len(g.minima)+len(g.maxima) == 0 {
				t.Errorf("%s: %s compares against minima %v and maxima %v; a gate of the kind's fields has one minimum, one maximum, or one of each", g.pos, g.key(), g.minima, g.maxima)
				continue
			}
			for _, id := range r.paths {
				for _, b := range g.minima {
					check(g, "minimum", id, b)
				}
				for _, b := range g.maxima {
					check(g, "maximum", id, b)
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
	for kind, entries := range map[string][]gateEntry{"minimum": minima, "maximum": maxima} {
		for _, e := range entries {
			if !named[kind][e.id] {
				t.Errorf("the kind's %s table lists %s, which no gate of the source names", kind, e.id)
			}
		}
	}
}
