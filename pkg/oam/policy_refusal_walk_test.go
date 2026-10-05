package oam

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The walk reads the source of the packages that hold the built-in refusals
// and holds two things: an error whose text carries the wording of a refusal by
// the environment policy is built by NewPolicyRefusal, and every call of that
// constructor names a class of the closed set.
//
// It reads text, so its reach is the wording below, in string literals and in
// constants: a refusal worded otherwise, or one whose text arrives through a
// variable or a function, is not seen here, and is held by the per-class tests
// of the packages instead.
//
// It does not resolve scope. A name the directory also declares below package
// level (a constant, a variable, a parameter) may stand for either, so the walk
// trusts none: such a name excuses no error and names no class.

// refusalWalkDirs are the directories walked, relative to this package.
var refusalWalkDirs = []string{".", "builtin/components", "builtin/traits"}

// policyWording matches the text a refusal by the environment policy carries.
var policyWording = regexp.MustCompile(`environment policy|enforced max|allowed[ -]registr|allowed list|required capability`)

// notPolicyRefusals are the errors that carry the wording and are not refusals
// by the policy, each by a fragment of its text, with the reason it stays
// unclassified.
var notPolicyRefusals = map[string]string{
	"invalid enforced max %s value %q": "the maximum the policy itself gives does not parse: a defect of the policy, not a refusal of the document",
	"undeclared %s %s: the %s %s type": "refused with or without a policy: the library's own rule on a workload or a claim that sets a field its type does not declare",
}

// refusalWalk is what one walk found.
type refusalWalk struct {
	fset *token.FileSet
	// own says the directory being walked is this package, where a class
	// constant is written without the package name.
	own bool
	// consts holds what the constants of the directory being walked are
	// declared as, by name.
	consts map[string][]ast.Expr
	// local holds the names the directory being walked declares below package
	// level.
	local map[string]bool
	// classUses counts the constructor calls per class constant name.
	classUses map[string]int
	// exemptUses counts the errors each notPolicyRefusals entry excused.
	exemptUses map[string]int
	// findings are the defects, each with its position.
	findings []string
}

// isRefusalConstructor reports whether call is NewPolicyRefusal, written from
// this package or from one that imports it.
func isRefusalConstructor(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "NewPolicyRefusal"
	case *ast.SelectorExpr:
		return fn.Sel.Name == "NewPolicyRefusal"
	}
	return false
}

// isErrorBuilder reports whether call builds an error from text: the New,
// Errorf, Wrap and Wrapf of an errors package, and fmt.Errorf.
func isErrorBuilder(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch pkg.Name {
	case "errors":
		return slices.Contains([]string{"New", "Errorf", "Wrap", "Wrapf"}, sel.Sel.Name)
	case "fmt":
		return sel.Sel.Name == "Errorf"
	}
	return false
}

// stringValue is the value of a string literal, or of a concatenation of them.
func stringValue(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := stringValue(v.X)
		r, rok := stringValue(v.Y)
		return l + r, lok && rok
	case *ast.ParenExpr:
		return stringValue(v.X)
	}
	return "", false
}

// text is every string an expression carries, joined: its literals and the
// string constants it names. It does not read the arguments of a refusal
// constructor inside it: that text is classified already. With sure it leaves
// out what a name of w.local stands for.
func (w *refusalWalk) text(e ast.Expr, sure bool) string {
	var b strings.Builder
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			return !isRefusalConstructor(v)
		case *ast.BasicLit:
			if s, ok := stringValue(v); ok {
				b.WriteString(s)
			}
		case *ast.Ident:
			b.WriteString(w.constText(v, map[string]bool{}, sure))
		}
		return true
	})
	return b.String()
}

// constText is the text a constant expression stands for: a string literal, a
// concatenation, and a constant named by another, followed to its literals.
// seen holds the names being followed, so that a cycle ends. Two constants of
// one name in a directory are both read, which errs towards more text: right
// for finding the wording, wrong for excusing an error, so with sure a name of
// w.local stands for nothing.
func (w *refusalWalk) constText(e ast.Expr, seen map[string]bool, sure bool) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		s, _ := stringValue(v)
		return s
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			return w.constText(v.X, seen, sure) + w.constText(v.Y, seen, sure)
		}
	case *ast.ParenExpr:
		return w.constText(v.X, seen, sure)
	case *ast.Ident:
		if seen[v.Name] || sure && w.local[v.Name] {
			return ""
		}
		seen[v.Name] = true
		defer delete(seen, v.Name)
		var b strings.Builder
		for _, decl := range w.consts[v.Name] {
			b.WriteString(w.constText(decl, seen, sure))
		}
		return b.String()
	}
	return ""
}

// collectConsts records what the constants of a file are declared as, at any
// depth. A constant written without a value in a group takes the expressions
// of the last one written with them, as the language gives it.
func (w *refusalWalk) collectConsts(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		var values []ast.Expr
		for _, spec := range decl.Specs {
			vs := spec.(*ast.ValueSpec)
			if len(vs.Values) > 0 {
				values = vs.Values
			}
			for i, name := range vs.Names {
				if i < len(values) {
					w.consts[name.Name] = append(w.consts[name.Name], values[i])
				}
			}
		}
		return true
	})
}

// collectLocal records the names a file declares below package level: a
// constant, a variable or a type inside a function, a short variable
// declaration, and the parameters, results and receiver of a function.
func (w *refusalWalk) collectLocal(file *ast.File) {
	top := map[ast.Spec]bool{}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				top[spec] = true
			}
		}
	}
	declare := func(exprs ...ast.Expr) {
		for _, e := range exprs {
			if id, ok := e.(*ast.Ident); ok {
				w.local[id.Name] = true
			}
		}
	}
	fields := func(lists ...*ast.FieldList) {
		for _, list := range lists {
			if list == nil {
				continue
			}
			for _, f := range list.List {
				for _, name := range f.Names {
					declare(name)
				}
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.ValueSpec:
			if !top[v] {
				for _, name := range v.Names {
					declare(name)
				}
			}
		case *ast.TypeSpec:
			if !top[v] {
				declare(v.Name)
			}
		case *ast.AssignStmt:
			if v.Tok == token.DEFINE {
				declare(v.Lhs...)
			}
		case *ast.RangeStmt:
			if v.Tok == token.DEFINE {
				declare(v.Key, v.Value)
			}
		case *ast.FuncDecl:
			fields(v.Recv)
		case *ast.FuncType:
			fields(v.TypeParams, v.Params, v.Results)
		}
		return true
	})
}

// className is the constant of this package a constructor call names as its
// class: the bare name in this package, oam.<name> in one that imports it. It
// is "" for anything else: a constant of the walked package named like a class
// of the set is not that class, and neither is a name of w.local, which may
// shadow the class or the package.
func (w *refusalWalk) className(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		if w.own && !w.local[v.Name] {
			return v.Name
		}
	case *ast.SelectorExpr:
		if pkg, ok := v.X.(*ast.Ident); ok && pkg.Name == "oam" && !w.own && !w.local["oam"] {
			return v.Sel.Name
		}
	}
	return ""
}

// walkFile checks every call of a file.
func (w *refusalWalk) walkFile(file *ast.File, classes map[string]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		at := w.fset.Position(call.Pos()).String()
		switch {
		case isRefusalConstructor(call):
			if len(call.Args) != 2 {
				w.findings = append(w.findings, at+": NewPolicyRefusal is not called with a class and a message")
				return true
			}
			name := w.className(call.Args[0])
			switch _, declared := classes[name]; {
			case name == "RefusalUnclassified":
				w.findings = append(w.findings, at+": a built-in refusal is given the unclassified value")
			case !declared:
				w.findings = append(w.findings, at+": the class of a built-in refusal is not a constant of the closed set")
			default:
				w.classUses[name]++
			}
		case isErrorBuilder(call):
			var b, s strings.Builder
			for _, arg := range call.Args {
				b.WriteString(w.text(arg, false))
				s.WriteString(w.text(arg, true))
			}
			msg, sure := b.String(), s.String()
			if !policyWording.MatchString(msg) {
				return true
			}
			for fragment := range notPolicyRefusals {
				if strings.Contains(sure, fragment) {
					w.exemptUses[fragment]++
					return true
				}
			}
			w.findings = append(w.findings, at+": an error worded as a refusal by the environment policy carries no class; build it with NewPolicyRefusal, or list it in notPolicyRefusals with the reason: "+strconv.Quote(msg))
		}
		return true
	})
}

// declaredRefusalClasses reads the constants of type RefusalClass from the
// files of this package: name to value.
func declaredRefusalClasses(files []*ast.File) map[string]string {
	classes := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "RefusalClass" {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if s, ok := stringValue(vs.Values[i]); ok {
							classes[name.Name] = s
						}
					}
				}
			}
		}
	}
	return classes
}

// parseSources parses the non-test Go files of dir.
func parseSources(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no Go source: the walk would pass on nothing", dir)
	}
	return files
}

// TestBuiltinPolicyRefusalsCarryAClass walks the built-in refusals: an error
// worded as a refusal by the environment policy that is not built by
// NewPolicyRefusal fails it, unless notPolicyRefusals names it with the reason;
// so does a constructor call whose class is not a constant of the closed set,
// a class no built-in refusal uses, and a notPolicyRefusals entry that excuses
// nothing.
func TestBuiltinPolicyRefusalsCarryAClass(t *testing.T) {
	fset := token.NewFileSet()
	own := parseSources(t, fset, ".")
	classes := declaredRefusalClasses(own)

	var declared []RefusalClass
	for name, value := range classes {
		if name != "RefusalUnclassified" {
			declared = append(declared, RefusalClass(value))
		}
	}
	listed := RefusalClasses()
	slices.Sort(declared)
	slices.Sort(listed)
	if !slices.Equal(declared, listed) {
		t.Fatalf("the declared classes %q are not the ones RefusalClasses lists, %q", declared, listed)
	}

	w := &refusalWalk{fset: fset, classUses: map[string]int{}, exemptUses: map[string]int{}}
	for _, dir := range refusalWalkDirs {
		files := own
		if dir != "." {
			files = parseSources(t, fset, dir)
		}
		w.own = dir == "."
		w.consts = map[string][]ast.Expr{}
		w.local = map[string]bool{}
		for _, file := range files {
			w.collectConsts(file)
			w.collectLocal(file)
		}
		for _, file := range files {
			w.walkFile(file, classes)
		}
	}

	for _, finding := range w.findings {
		t.Error(finding)
	}
	for name := range classes {
		if name != "RefusalUnclassified" && w.classUses[name] == 0 {
			t.Errorf("no built-in refusal has the class %s: the walk found no NewPolicyRefusal call naming it", name)
		}
	}
	for fragment, reason := range notPolicyRefusals {
		if w.exemptUses[fragment] == 0 {
			t.Errorf("notPolicyRefusals lists %q (%s), and no error carries that text", fragment, reason)
		}
	}
}

// TestRefusalWalk_FindsAnUnclassifiedRefusal holds the walk itself to a source
// it must object to and one it must pass: without it, a walk that reads
// nothing would pass too.
func TestRefusalWalk_FindsAnUnclassifiedRefusal(t *testing.T) {
	const src = `package sample

import (
	"errors"
	"fmt"

	"example.test/oam"
)

const unreadable = "the object cannot be checked against environment policy"

func plain() error      { return errors.New("hostNetwork is not allowed by environment policy") }
func formatted() error  { return fmt.Errorf("replicas %d exceeds enforced maximum %d", 5, 3) }
func named(e error) error { return errors.Wrap(e, unreadable) }
func split() error {
	return errors.Errorf("the host cannot be checked against "+
		"the allowed registries %v", nil)
}
func unclassified() error { return oam.NewPolicyRefusal(oam.RefusalUnclassified, "x") }
func computed(c oam.RefusalClass) error { return oam.NewPolicyRefusal(c, "x") }
func foreign() error { return oam.NewPolicyRefusal(oam.RefusalMadeUp, "x") }
const reason = "hostIPC is not allowed by environment policy"
const aliased = reason
func alias() error { return errors.New(aliased) }
const RefusalHostNamespace = oam.RefusalUnclassified
func shadowed() error { return oam.NewPolicyRefusal(RefusalHostNamespace, "x") }
func borrowed(e error) error {
	const msg = "invalid enforced max %s value %q"
	return errors.Wrapf(e, msg, "cpu", "x")
}
func lender() error {
	const msg = "hostPID is not allowed by environment policy"
	return errors.New(msg)
}
const (
	first = "hostNetwork is not allowed by environment policy"
	second
)
func inherited() error { return errors.New(second) }

func classified() error {
	return oam.NewPolicyRefusal(oam.RefusalHostNamespace, "hostNetwork is not allowed by environment policy")
}
func wrapped(e error) error {
	return errors.Errorf("%w: %w", oam.NewPolicyRefusal(oam.RefusalUnreadableObject, unreadable), e)
}
func exempt(e error) error { return errors.Wrapf(e, "invalid enforced max %s value %q", "cpu", "x") }
func unrelated() error    { return errors.New("the chart does not render") }
`
	classes := map[string]string{
		"RefusalUnclassified":     "",
		"RefusalHostNamespace":    "host-namespace",
		"RefusalRegistry":         "registry",
		"RefusalUnreadableObject": "unreadable-object",
	}
	walk := func(name, src string, own bool) *refusalWalk {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		w := &refusalWalk{fset: fset, own: own, consts: map[string][]ast.Expr{}, local: map[string]bool{}, classUses: map[string]int{}, exemptUses: map[string]int{}}
		w.collectConsts(file)
		w.collectLocal(file)
		w.walkFile(file, classes)
		return w
	}
	wantFindings := func(w *refusalWalk, wantLines ...string) {
		t.Helper()
		if len(w.findings) != len(wantLines) {
			t.Fatalf("the walk reported %d findings, want %d:\n%s", len(w.findings), len(wantLines), strings.Join(w.findings, "\n"))
		}
		for i, want := range wantLines {
			if !strings.HasPrefix(w.findings[i], want) {
				t.Errorf("finding %d is %q, want one at %s", i, w.findings[i], want)
			}
		}
	}

	// One finding per function of the first group, in source order, and none
	// for the second. The two constants named msg are each inside a function:
	// the one cannot lend its excuse to the other, and neither is excused. The
	// constant second has the text of first.
	w := walk("sample.go", src, false)
	wantFindings(w, "sample.go:12:", "sample.go:13:", "sample.go:14:", "sample.go:16:", "sample.go:19:", "sample.go:20:", "sample.go:21:", "sample.go:24:", "sample.go:26:", "sample.go:29:", "sample.go:33:", "sample.go:39:")
	if w.classUses["RefusalHostNamespace"] != 1 || w.classUses["RefusalUnreadableObject"] != 1 {
		t.Errorf("class uses = %v, want one each for the two classified refusals", w.classUses)
	}
	if w.exemptUses["invalid enforced max %s value %q"] != 1 {
		t.Errorf("exempt uses = %v, want the invalid maximum excused once", w.exemptUses)
	}

	// In this package a class is named bare, so a name declared inside a
	// function can shadow it: a constant, a parameter, a variable.
	const ownSrc = `package oam

func constant() error {
	const RefusalRegistry = RefusalUnclassified
	return NewPolicyRefusal(RefusalRegistry, "x")
}
func parameter(RefusalHostNamespace RefusalClass) error {
	return NewPolicyRefusal(RefusalHostNamespace, "x")
}

func classified() error { return NewPolicyRefusal(RefusalUnreadableObject, "x") }
`
	w = walk("own.go", ownSrc, true)
	wantFindings(w, "own.go:5:", "own.go:8:")
	if len(w.classUses) != 1 || w.classUses["RefusalUnreadableObject"] != 1 {
		t.Errorf("class uses = %v, want the one unshadowed class", w.classUses)
	}

	// Outside it the package name can be shadowed the same way.
	const shadowSrc = `package sample

import "example.test/oam"

type classes struct{ RefusalRegistry string }

func shadowed(oam classes) error { return oam.NewPolicyRefusal(oam.RefusalRegistry, "x") }
`
	wantFindings(walk("shadow.go", shadowSrc, false), "shadow.go:7:")
}
