package components

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// vendoredK8sDir holds the excerpt of the Kubernetes API server's defaulting
// code the defaulted-zero tables of the Kubernetes kinds are held to: copied
// unmodified from kubernetes/kubernetes at the release of the linked
// k8s.io/api, under its Apache-2.0 licence, by scripts/vendor-k8s-defaulters.sh,
// with kubernetes/kubernetes's LICENSE. Its SOURCE file names the tag and the
// git blob id of each file and of LICENSE.
const vendoredK8sDir = "testdata/upstream/kubernetes"

// vendoredK8s is the parsed excerpt, by package import path.
type vendoredK8s struct {
	fset *token.FileSet
	tag  string
	pkgs map[string]*vendoredPkg
}

// vendoredPkg is one package of the excerpt: its functions, its
// package-level vars and consts, and every name it declares at package level.
type vendoredPkg struct {
	path   string
	funcs  map[string]*vendoredFunc
	values map[string]*vendoredValue
	names  map[string]bool
}

// vendoredFunc is a function of the excerpt with the file that declares it,
// whose imports name the packages it refers to.
type vendoredFunc struct {
	decl *ast.FuncDecl
	file *ast.File
	pkg  *vendoredPkg
}

// vendoredValue is a package-level var or const: its initialiser, nil when it
// has none (the zero value).
type vendoredValue struct {
	expr ast.Expr
	file *ast.File
	pkg  *vendoredPkg
}

// gitBlobID is the git blob id of content, the id SOURCE names a file by.
func gitBlobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// loadVendoredK8s reads SOURCE and parses every file it names, failing on a
// file whose blob id differs, that lacks its licence header, or that is in the
// directory and not named, and on an excerpt without the licence's full text
// (LICENSE, named on SOURCE's licence line): Apache-2.0 asks a redistributor
// to include a copy of it, not only the headers.
func loadVendoredK8s(t *testing.T) *vendoredK8s {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(vendoredK8sDir, "SOURCE"))
	if err != nil {
		t.Fatalf("read SOURCE: %v", err)
	}
	src := &vendoredK8s{fset: token.NewFileSet(), pkgs: map[string]*vendoredPkg{}}
	listed := map[string]bool{"SOURCE": true}
	licence := false
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || strings.HasPrefix(line, "#"):
		case fields[0] == "tag" && len(fields) == 2:
			src.tag = fields[1]
		case fields[0] == "tag-object" && len(fields) == 2:
			// Provenance for whoever refreshes the excerpt; offline, the tests
			// hold the tag to go.mod and each file to its blob id, not the
			// files to the tag.
		case fields[0] == "licence" && len(fields) == 3:
			rel := fields[1]
			listed[rel] = true
			body, err := os.ReadFile(filepath.Join(vendoredK8sDir, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("SOURCE names %s: %v", rel, err)
			}
			if got := gitBlobID(body); got != fields[2] {
				t.Fatalf("%s has blob id %s, SOURCE says %s: the licence is copied unmodified; re-run scripts/vendor-k8s-defaulters.sh", rel, got, fields[2])
			}
			if !bytes.Contains(body, []byte("Apache License")) || !bytes.Contains(body, []byte("Version 2.0, January 2004")) {
				t.Fatalf("%s is not the Apache License, Version 2.0", rel)
			}
			licence = true
		case fields[0] == "file" && len(fields) == 3:
			rel := fields[1]
			listed[rel] = true
			body, err := os.ReadFile(filepath.Join(vendoredK8sDir, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("SOURCE names %s: %v", rel, err)
			}
			if got := gitBlobID(body); got != fields[2] {
				t.Fatalf("%s has blob id %s, SOURCE says %s: the excerpt is copied unmodified; re-run scripts/vendor-k8s-defaulters.sh", rel, got, fields[2])
			}
			if !bytes.Contains(body, []byte("Licensed under the Apache License, Version 2.0")) {
				t.Fatalf("%s does not carry its Apache-2.0 header", rel)
			}
			file, err := parser.ParseFile(src.fset, rel, body, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			pkgPath := "k8s.io/kubernetes/" + path.Dir(rel)
			pkg := src.pkgs[pkgPath]
			if pkg == nil {
				pkg = &vendoredPkg{path: pkgPath, funcs: map[string]*vendoredFunc{}, values: map[string]*vendoredValue{}, names: map[string]bool{}}
				src.pkgs[pkgPath] = pkg
			}
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						pkg.funcs[d.Name.Name] = &vendoredFunc{decl: d, file: file, pkg: pkg}
						pkg.names[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok {
							pkg.names[ts.Name.Name] = true
						}
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, name := range vs.Names {
							pkg.names[name.Name] = true
							v := &vendoredValue{file: file, pkg: pkg}
							if i < len(vs.Values) {
								v.expr = vs.Values[i]
							}
							pkg.values[name.Name] = v
						}
					}
				}
			}
		default:
			t.Fatalf("SOURCE: unreadable line %q", line)
		}
	}
	err = filepath.WalkDir(vendoredK8sDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(vendoredK8sDir, p)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s is in the excerpt and not named in SOURCE", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", vendoredK8sDir, err)
	}
	if !licence {
		t.Fatal("SOURCE names no licence file: the excerpt carries a copy of the Apache License, Version 2.0")
	}
	return src
}

// fn returns the function name of the package at pkgPath, failing when the
// excerpt does not hold it.
func (s *vendoredK8s) fn(t *testing.T, pkgPath, name string) *vendoredFunc {
	t.Helper()
	pkg := s.pkgs[pkgPath]
	if pkg == nil || pkg.funcs[name] == nil {
		t.Fatalf("the excerpt holds no %s.%s", pkgPath, name)
	}
	return pkg.funcs[name]
}

// text is the source text of node.
func (s *vendoredK8s) text(node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, s.fset, node); err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return buf.String()
}

// importPath resolves name, as file refers to an imported package, to the
// package's import path.
func importPath(file *ast.File, name string) (string, bool) {
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == name {
			return p, true
		}
	}
	return "", false
}

const (
	k8sCoreV1        = "k8s.io/kubernetes/pkg/apis/core/v1"
	k8sAppsV1        = "k8s.io/kubernetes/pkg/apis/apps/v1"
	k8sAutoscalingV2 = "k8s.io/kubernetes/pkg/apis/autoscaling/v2"
	k8sNetworkingV1  = "k8s.io/kubernetes/pkg/apis/networking/v1"
	k8sParsers       = "k8s.io/kubernetes/pkg/util/parsers"
)

// vendoredK8sTypes are the k8s.io/api types the excerpt names that the test
// evaluates, by import path and name. A type the excerpt names in an
// evaluated expression that is not here fails the test.
var vendoredK8sTypes = map[string]reflect.Type{
	"k8s.io/api/core/v1.Container":                   reflect.TypeFor[corev1.Container](),
	"k8s.io/api/autoscaling/v2.MetricSpec":           reflect.TypeFor[autoscalingv2.MetricSpec](),
	"k8s.io/api/autoscaling/v2.ResourceMetricSource": reflect.TypeFor[autoscalingv2.ResourceMetricSource](),
	"k8s.io/api/autoscaling/v2.MetricTarget":         reflect.TypeFor[autoscalingv2.MetricTarget](),
	"k8s.io/api/autoscaling/v2.HPAScalingRules":      reflect.TypeFor[autoscalingv2.HPAScalingRules](),
	"k8s.io/api/autoscaling/v2.HPAScalingPolicy":     reflect.TypeFor[autoscalingv2.HPAScalingPolicy](),
	"k8s.io/api/networking/v1.PolicyType":            reflect.TypeFor[networkingv1.PolicyType](),
}

// vendoredK8sConsts are the k8s.io/api constants the excerpt's defaults are
// written with, by import path and name, as the linked module defines them.
// A constant the excerpt writes a default with that is not here fails the
// test.
var vendoredK8sConsts = map[string]any{
	"k8s.io/api/core/v1.DNSClusterFirst":                 corev1.DNSClusterFirst,
	"k8s.io/api/core/v1.RestartPolicyAlways":             corev1.RestartPolicyAlways,
	"k8s.io/api/core/v1.DefaultSchedulerName":            corev1.DefaultSchedulerName,
	"k8s.io/api/core/v1.TerminationMessagePathDefault":   corev1.TerminationMessagePathDefault,
	"k8s.io/api/core/v1.TerminationMessageReadFile":      corev1.TerminationMessageReadFile,
	"k8s.io/api/core/v1.PullAlways":                      corev1.PullAlways,
	"k8s.io/api/core/v1.PullIfNotPresent":                corev1.PullIfNotPresent,
	"k8s.io/api/core/v1.URISchemeHTTP":                   corev1.URISchemeHTTP,
	"k8s.io/api/core/v1.ResourceCPU":                     corev1.ResourceCPU,
	"k8s.io/api/autoscaling/v2.ResourceMetricSourceType": autoscalingv2.ResourceMetricSourceType,
	"k8s.io/api/autoscaling/v2.UtilizationMetricType":    autoscalingv2.UtilizationMetricType,
	"k8s.io/api/autoscaling/v2.PodsScalingPolicy":        autoscalingv2.PodsScalingPolicy,
	"k8s.io/api/autoscaling/v2.PercentScalingPolicy":     autoscalingv2.PercentScalingPolicy,
	"k8s.io/api/autoscaling/v2.MaxChangePolicySelect":    autoscalingv2.MaxChangePolicySelect,
	"k8s.io/api/networking/v1.PolicyTypeIngress":         networkingv1.PolicyTypeIngress,
	"k8s.io/api/networking/v1.PolicyTypeEgress":          networkingv1.PolicyTypeEgress,
}

// TestKubernetesDefaulters_ExcerptMatchesLinkedRelease: the excerpt is of the
// Kubernetes release whose k8s.io/api the module links (v0.X.Y pairs with
// vX.Y), so a dependency bump without a re-vendor fails here.
func TestKubernetesDefaulters_ExcerptMatchesLinkedRelease(t *testing.T) {
	src := loadVendoredK8s(t)
	gomod, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	m := regexp.MustCompile(`(?m)^\s*k8s\.io/api v0\.(\d+\.\d+)\s*$`).FindSubmatch(gomod)
	if m == nil {
		t.Fatal("go.mod requires no k8s.io/api v0.X.Y")
	}
	if want := "v1." + string(m[1]); src.tag != want {
		t.Errorf("the excerpt is of kubernetes %s, the linked k8s.io/api is of %s: run scripts/vendor-k8s-defaulters.sh %s", src.tag, want, want)
	}
}

// specWrite is one assignment the API server's defaulting makes to a field of
// a pod spec, as the walk of the excerpt finds it.
type specWrite struct {
	// path is the field's json path under the pod spec, [] for an element.
	path string
	kind reflect.Kind
	// value is the default: a JSON literal, or {path} for another field's
	// value.
	value string
	// guarded says an enclosing condition is the field's own zero test: the
	// default replaces an omitted value only.
	guarded bool
	// conds are the other enclosing conditions, nil checks left out, as source
	// text with each field written {path}.
	conds []string
	// fn names the function that makes the assignment.
	fn  string
	pos token.Position
}

// defaulterWalk follows the defaulting of one object, from its generated
// SetObjectDefaults_ function through every function of the excerpt it calls,
// and records the assignments to fields under the object's pod spec.
type defaulterWalk struct {
	t      *testing.T
	src    *vendoredK8s
	prefix string // the pod spec's json path in the object, ending in a dot
	writes []specWrite
	depth  int
}

// pathVal is an expression that denotes a field of the object: its Go type,
// and its json path from the object ({} for a map entry).
type pathVal struct {
	typ     reflect.Type
	path    string
	mapElem bool
}

// walkFrame is one function being walked.
type walkFrame struct {
	w    *defaulterWalk
	fn   *vendoredFunc
	vars map[string]pathVal
	// copies are range values and variables given a field's value: copies,
	// through which a write would change nothing the walk can follow.
	copies map[string]bool
	// locals are the other variables, as the source text of their value.
	locals map[string]string
	// ranges are the index variables of ranges over a list of the object,
	// with the list's path.
	ranges map[string]string
	// elems are the value variables of ranges, as what they range over: the
	// element type of a list of the object, else the list's text. A method
	// is listed by its receiver's identity, which these give.
	elems map[string]string
	// scopes are the names each open block declares, outermost first. The
	// maps above are flat, so a name leaves them when its block ends, and a
	// declaration that would shadow a name of an enclosing block fails.
	scopes []map[string]bool
	conds  []string
	// exitCond is the negated condition of the last if walked, which holds
	// after it when its body leaves the block.
	exitCond string
}

func (f *walkFrame) open() {
	f.scopes = append(f.scopes, map[string]bool{})
}

func (f *walkFrame) close() {
	for name := range f.scopes[len(f.scopes)-1] {
		delete(f.vars, name)
		delete(f.locals, name)
		delete(f.copies, name)
		delete(f.ranges, name)
		delete(f.elems, name)
	}
	f.scopes = f.scopes[:len(f.scopes)-1]
}

// declare records name as declared in the innermost block. A name the
// block already declares is assigned, as := does.
func (f *walkFrame) declare(pos token.Pos, name string) {
	cur := f.scopes[len(f.scopes)-1]
	if name == "_" || cur[name] {
		return
	}
	for _, s := range f.scopes[:len(f.scopes)-1] {
		if s[name] {
			f.w.fail(pos, "%s shadows a variable of an enclosing block, which the walk does not follow", name)
		}
	}
	cur[name] = true
}

func (w *defaulterWalk) fail(pos token.Pos, format string, args ...any) {
	w.t.Helper()
	w.t.Fatalf("%s: %s", w.src.fset.Position(pos), fmt.Sprintf(format, args...))
}

// call walks fn with its parameters bound to args, evaluated in caller.
func (w *defaulterWalk) call(fn *vendoredFunc, args []ast.Expr, caller *walkFrame) {
	w.depth++
	defer func() { w.depth-- }()
	if w.depth > 30 {
		w.fail(fn.decl.Pos(), "the call depth passes 30: a recursion the walk does not follow")
	}
	f := &walkFrame{w: w, fn: fn, vars: map[string]pathVal{}, copies: map[string]bool{}, locals: map[string]string{}, ranges: map[string]string{}, elems: map[string]string{}, conds: caller.conds}
	f.open()
	i := 0
	for _, field := range fn.decl.Type.Params.List {
		for _, name := range field.Names {
			if i >= len(args) {
				w.fail(fn.decl.Pos(), "%s takes more parameters than the call passes", fn.decl.Name.Name)
			}
			f.declare(name.Pos(), name.Name)
			if pv, ok := caller.alias(args[i]); ok {
				f.vars[name.Name] = pv
			} else {
				f.locals[name.Name] = caller.render(args[i])
			}
			i++
		}
	}
	f.stmts(fn.decl.Body.List)
}

// root walks the generated SetObjectDefaults_ function name of pkgPath for an
// object of type typ, whose pod spec sits at prefix, and returns the writes
// under the pod spec, by path relative to it. ephemeralContainers is left out:
// the kinds refuse the field whole.
func (s *vendoredK8s) podSpecWrites(t *testing.T, pkgPath, name string, typ reflect.Type, prefix string) map[string][]specWrite {
	t.Helper()
	fn := s.fn(t, pkgPath, name)
	w := &defaulterWalk{t: t, src: s, prefix: prefix}
	param := fn.decl.Type.Params.List[0].Names[0].Name
	top := &walkFrame{w: w, fn: fn, vars: map[string]pathVal{param: {typ: typ}}, copies: map[string]bool{}, locals: map[string]string{}, ranges: map[string]string{}, elems: map[string]string{}, scopes: []map[string]bool{{param: true}}}
	top.stmts(fn.decl.Body.List)
	out := map[string][]specWrite{}
	for _, wr := range w.writes {
		strip := func(s string) string { return strings.ReplaceAll(s, "{"+prefix, "{") }
		wr.path = strings.TrimPrefix(wr.path, prefix)
		wr.value = strip(wr.value)
		for i := range wr.conds {
			wr.conds[i] = strip(wr.conds[i])
		}
		if strings.HasPrefix(wr.path, "ephemeralContainers[]") {
			continue
		}
		out[wr.path] = append(out[wr.path], wr)
	}
	return out
}

// endsInExit says the block's last statement leaves it: a return, continue or
// break.
func endsInExit(b *ast.BlockStmt) bool {
	if len(b.List) == 0 {
		return false
	}
	switch s := b.List[len(b.List)-1].(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return s.Tok == token.CONTINUE || s.Tok == token.BREAK
	default:
		return false
	}
}

// stmts walks a block. What follows an if whose body leaves the block, with
// no else, runs only when its condition is false.
// block walks list as a block of its own.
func (f *walkFrame) block(list []ast.Stmt) {
	f.open()
	defer f.close()
	f.stmts(list)
}

func (f *walkFrame) stmts(list []ast.Stmt) {
	saved := f.conds
	defer func() { f.conds = saved }()
	for _, s := range list {
		if f.stmt(s) {
			return
		}
		if is, ok := s.(*ast.IfStmt); ok && is.Else == nil && endsInExit(is.Body) {
			f.conds = append(slices.Clip(f.conds), f.exitCond)
		}
	}
}

// conjuncts splits a condition on &&.
func conjuncts(e ast.Expr) []ast.Expr {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.LAND {
			return append(conjuncts(x.X), conjuncts(x.Y)...)
		}
	case *ast.ParenExpr:
		return conjuncts(x.X)
	}
	return []ast.Expr{e}
}

// stmt walks one statement and says whether it leaves the block.
func (f *walkFrame) stmt(s ast.Stmt) bool {
	w := f.w
	switch x := s.(type) {
	case *ast.BlockStmt:
		f.block(x.List)
	case *ast.ReturnStmt:
		for _, r := range x.Results {
			f.check(r)
		}
		return true
	case *ast.BranchStmt:
		// A break leaves a range before its later elements, which the walk,
		// visiting the body once, cannot tell from a continue.
		if x.Tok != token.CONTINUE {
			w.fail(x.Pos(), "a %s the walk does not follow", x.Tok)
		}
		return true
	case *ast.IfStmt:
		f.open()
		defer f.close()
		if x.Init != nil {
			f.stmt(x.Init)
		}
		saved := f.conds
		for _, c := range conjuncts(x.Cond) {
			f.check(c)
			f.conds = append(slices.Clip(f.conds), f.render(c))
		}
		f.block(x.Body.List)
		f.conds = saved
		if x.Else != nil {
			f.conds = append(slices.Clip(saved), "!("+f.render(x.Cond)+")")
			f.stmt(x.Else)
			f.conds = saved
		}
		// Rendered while the variables of the if's initialiser are bound.
		f.exitCond = "!(" + f.render(x.Cond) + ")"
	case *ast.SwitchStmt:
		f.open()
		defer f.close()
		if x.Init != nil {
			f.stmt(x.Init)
		}
		if x.Tag != nil {
			w.fail(x.Pos(), "a switch on a value, which the walk does not follow")
		}
		saved := f.conds
		for _, c := range x.Body.List {
			clause := c.(*ast.CaseClause)
			cond := "default"
			if clause.List != nil {
				var parts []string
				for _, e := range clause.List {
					f.check(e)
					parts = append(parts, f.render(e))
				}
				cond = "(" + strings.Join(parts, " || ") + ")"
			}
			f.conds = append(slices.Clip(saved), cond)
			f.block(clause.Body)
		}
		f.conds = saved
	case *ast.RangeStmt:
		f.check(x.X)
		f.open()
		defer f.close()
		for _, kv := range []ast.Expr{x.Key, x.Value} {
			id, ok := kv.(*ast.Ident)
			if kv != nil && !ok {
				w.fail(x.Pos(), "a range assigning to %s, which the walk does not follow", f.render(kv))
			}
			if ok && x.Tok == token.DEFINE {
				f.declare(id.Pos(), id.Name)
			}
		}
		if k, ok := x.Key.(*ast.Ident); ok && k.Name != "_" {
			f.bindLocal(k.Name, k.Name)
			if pv, ok := f.resolve(x.X); ok {
				f.ranges[k.Name] = pv.path
			}
		}
		if v, ok := x.Value.(*ast.Ident); ok && v.Name != "_" {
			f.bindLocal(v.Name, v.Name)
			f.copies[v.Name] = true
			f.elems[v.Name] = "an element of " + f.render(x.X)
			if pv, ok := f.resolve(x.X); ok {
				if t := derefType(pv.typ); t.Kind() == reflect.Map || t.Kind() == reflect.Slice {
					f.elems[v.Name] = "a " + t.Elem().PkgPath() + "." + t.Elem().Name()
				}
			}
		}
		f.block(x.Body.List)
	case *ast.DeclStmt:
		gen, ok := x.Decl.(*ast.GenDecl)
		if !ok {
			w.fail(x.Pos(), "a declaration the walk does not follow")
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				w.fail(spec.Pos(), "a %T the walk does not follow", spec)
			}
			f.checkType(vs.Type)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					f.check(vs.Values[i])
				}
				f.declare(name.Pos(), name.Name)
				if i < len(vs.Values) {
					f.bindValue(name.Name, vs.Values[i])
				} else {
					f.bindLocal(name.Name, name.Name)
				}
			}
		}
	case *ast.ExprStmt:
		call, ok := x.X.(*ast.CallExpr)
		if !ok {
			w.fail(x.Pos(), "an expression statement the walk does not follow")
		}
		for _, a := range call.Args {
			f.check(a)
		}
		if fn := f.callee(call); fn != nil {
			w.call(fn, call.Args, f)
			return false
		}
		fun := ast.Unparen(call.Fun)
		if id, ok := fun.(*ast.Ident); ok {
			w.fail(call.Pos(), "a call to %s, which the excerpt does not hold", id.Name)
		}
		for _, a := range call.Args {
			if _, ok := f.resolve(a); ok {
				w.fail(call.Pos(), "%s is passed the object to a function the excerpt does not hold", f.render(call))
			}
		}
		// A method called on the object can change it as an argument can.
		if sel, ok := fun.(*ast.SelectorExpr); ok {
			if _, ok := f.resolve(sel.X); ok {
				w.fail(call.Pos(), "%s calls a method on the object, which the walk does not follow", f.render(call))
			}
		}
		if key := f.callKey(call); !walkCalls[key] {
			w.fail(call.Pos(), "a call to %s, which the walk does not follow", key)
		}
	case *ast.AssignStmt:
		f.assign(x)
	default:
		w.fail(s.Pos(), "a %T the walk does not follow", s)
	}
	return false
}

// assign binds the variables an assignment defines and records its writes to
// the object.
func (f *walkFrame) assign(x *ast.AssignStmt) {
	w := f.w
	if len(x.Lhs) != len(x.Rhs) {
		if len(x.Rhs) != 1 {
			w.fail(x.Pos(), "an assignment the walk does not follow")
		}
		f.check(x.Rhs[0])
		rhs := f.render(x.Rhs[0])
		for i, l := range x.Lhs {
			id, ok := l.(*ast.Ident)
			if !ok {
				w.fail(x.Pos(), "a tuple assignment to %s, which the walk does not follow", f.render(l))
			}
			if x.Tok == token.DEFINE {
				f.declare(id.Pos(), id.Name)
			}
			if id.Name != "_" {
				f.bindLocal(id.Name, fmt.Sprintf("%s[%d]", rhs, i))
			}
		}
		return
	}
	for i, l := range x.Lhs {
		r := x.Rhs[i]
		f.check(r)
		f.check(l)
		if x.Tok != token.ASSIGN && x.Tok != token.DEFINE {
			w.fail(x.Pos(), "the assignment %s, which the walk does not follow", x.Tok)
		}
		if id, ok := l.(*ast.Ident); ok {
			if id.Name == "_" {
				continue
			}
			if x.Tok == token.DEFINE {
				f.declare(id.Pos(), id.Name)
			}
			f.bindValue(id.Name, r)
			continue
		}
		if x.Tok != token.ASSIGN {
			w.fail(x.Pos(), "a %s the walk does not follow", x.Tok)
		}
		f.write(l, r)
	}
}

// alias is the field of the object e refers to, when a write through e
// reaches the object: a variable bound to it, its address, a pointer, map or
// list field, or a conversion of one to a pointer type. A struct or basic
// field read by name, or anything read through a dereference (*p), is a
// copy, not an alias.
func (f *walkFrame) alias(e ast.Expr) (pathVal, bool) {
	pv, ok := f.resolve(e)
	if !ok {
		return pathVal{}, false
	}
	for {
		p, isParen := e.(*ast.ParenExpr)
		if !isParen {
			break
		}
		e = p.X
	}
	switch x := e.(type) {
	case *ast.Ident, *ast.CallExpr:
		return pv, true
	case *ast.UnaryExpr:
		return pv, x.Op == token.AND
	case *ast.StarExpr:
		// resolve keeps the pointer's type through a dereference, so the
		// kind below would take *p for the pointer p.
		return pathVal{}, false
	}
	switch pv.typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		return pv, true
	default:
		return pathVal{}, false
	}
}

// bindValue binds name, declared or assigned with value: to the field of the
// object value refers to, so writes through it are followed, else to value's
// text; a copy of a field of the object is marked so, and a write through it
// fails the walk.
func (f *walkFrame) bindValue(name string, value ast.Expr) {
	if pv, ok := f.alias(value); ok {
		f.vars[name] = pv
		delete(f.locals, name)
		delete(f.copies, name)
		delete(f.ranges, name)
		delete(f.elems, name)
		return
	}
	_, isCopy := f.resolve(value)
	f.bindLocal(name, f.render(value))
	if isCopy {
		f.copies[name] = true
	}
}

func (f *walkFrame) bindLocal(name, text string) {
	delete(f.vars, name)
	delete(f.copies, name)
	delete(f.ranges, name)
	delete(f.elems, name)
	f.locals[name] = text
}

// rootIdent is the variable an lvalue writes through.
func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// nilCheck matches a condition that only tests that a field is set, in
// either form, and captures the field's path.
var nilCheck = regexp.MustCompile(`^(?:\{([^{}]*)\} != nil|!\(\{([^{}]*)\} == nil\))$`)

// isAncestorCheck says c only tests that a field enclosing path is set, the
// check a write under it needs to reach the field: it says nothing about when
// the field is defaulted.
func isAncestorCheck(c, path string) bool {
	m := nilCheck.FindStringSubmatch(c)
	if m == nil {
		return false
	}
	field := m[1] + m[2]
	return strings.HasPrefix(path, field+".") || strings.HasPrefix(path, field+"[]")
}

// write records an assignment to a field under the pod spec. A pointer, map
// or struct field is not recorded: a pointer the author sets is carried
// whatever it points to, the API server fills a map's entries whether it is
// authored empty or not, and the one struct it sets (a volume's source, when
// none is set) holds no authored value. A list is recorded, so a list default
// fails the comparison.
func (f *walkFrame) write(lhs, rhs ast.Expr) {
	w := f.w
	pv, ok := f.resolve(lhs)
	if !ok {
		root := rootIdent(lhs)
		switch {
		case root == nil:
			w.fail(lhs.Pos(), "a write to %s, which the walk does not follow", f.render(lhs))
		case f.copies[root.Name]:
			w.fail(lhs.Pos(), "a write through the range copy %s", root.Name)
		case strings.Contains(f.locals[root.Name], "{"):
			w.fail(lhs.Pos(), "a write through %s, a value read from the object (%s), which the walk cannot place in it", root.Name, f.locals[root.Name])
		default:
			if _, bound := f.vars[root.Name]; bound {
				w.fail(lhs.Pos(), "a write to %s, which the walk cannot place in the object", f.render(lhs))
			}
		}
		return
	}
	if !strings.HasPrefix(pv.path, w.prefix) || pv.mapElem {
		return
	}
	wr := specWrite{path: pv.path, kind: pv.typ.Kind(), fn: f.fn.decl.Name.Name, pos: w.src.fset.Position(lhs.Pos())}
	switch pv.typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Struct, reflect.Interface:
		return
	case reflect.Slice, reflect.Array:
		wr.value = f.render(rhs)
	default:
		wr.value = f.value(rhs)
	}
	field := "{" + pv.path + "}"
	zero := map[string]bool{field + ` == ""`: true, field + " == 0": true, "len(" + field + ") == 0": true}
	for _, c := range f.conds {
		switch {
		case zero[c]:
			wr.guarded = true
		case isAncestorCheck(c, pv.path):
		default:
			wr.conds = append(wr.conds, c)
		}
	}
	w.writes = append(w.writes, wr)
}

// callee is the function of the excerpt call calls, or nil for a method, a
// builtin, a conversion or a function of a package the excerpt does not hold.
func (f *walkFrame) callee(call *ast.CallExpr) *vendoredFunc {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return f.fn.pkg.funcs[fun.Name]
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		if !ok {
			return nil
		}
		if _, ok := f.vars[id.Name]; ok {
			return nil
		}
		if _, ok := f.locals[id.Name]; ok {
			return nil
		}
		p, ok := importPath(f.fn.file, id.Name)
		if !ok {
			return nil
		}
		pkg := f.w.src.pkgs[p]
		if pkg == nil {
			return nil
		}
		fn := pkg.funcs[fun.Sel.Name]
		if fn == nil {
			f.w.fail(call.Pos(), "%s.%s is not in the excerpt", p, fun.Sel.Name)
		}
		return fn
	default:
		return nil
	}
}

// walkCalls are the calls the walk accepts, other than to a function of the
// excerpt, keyed by callKey, and walkUnary the unary operators: those the
// excerpt at the vendored tag uses, none of which changes the object (RoundUp
// rounds a range copy, written back by an assignment the walk records). A
// method is keyed by its receiver's identity, a builtin only while no name of
// the excerpt shadows it. The walk is closed: any other call, operator, type
// or kind of expression fails it, so a re-vendoring that brings one fails
// until it is understood and listed here.
var (
	walkCalls = setOf(
		"int32", "int64", "len", "make", "new",
		"conversion *v1.Container",
		"k8s.io/api/core/v1.AzureDataDiskCachingMode",
		"k8s.io/api/core/v1.AzureDataDiskKind",
		"k8s.io/api/core/v1.ResourceName",
		"k8s.io/component-helpers/resource.AggregateContainerLimits",
		"k8s.io/component-helpers/resource.AggregateContainerRequests",
		"k8s.io/component-helpers/resource.IsSupportedPodLevelResource",
		"k8s.io/kubernetes/pkg/apis/core/v1/helper.IsHugePageResourceName",
		"k8s.io/kubernetes/pkg/apis/core/v1/helper.IsOvercommitAllowed",
		"k8s.io/utils/ptr.AllPtrFieldsNil",
		"k8s.io/utils/ptr.To[int64]",
		"method k8s.io/apiserver/pkg/util/feature.DefaultFeatureGate.Enabled",
		"method a k8s.io/apimachinery/pkg/api/resource.Quantity.DeepCopy",
		"method a k8s.io/apimachinery/pkg/api/resource.Quantity.RoundUp",
		"method an element of resourcehelper.AggregateContainerLimits({}, resourcehelper.PodResourcesOptions{}).DeepCopy",
		"method an element of resourcehelper.AggregateContainerRequests({}, resourcehelper.PodResourcesOptions{}).DeepCopy",
		"method time.Hour.Seconds",
	)
	walkUnary = map[token.Token]bool{token.NOT: true, token.AND: true, token.SUB: true}
)

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// check fails on an expression the walk does not understand: a kind of node,
// a unary operator or a call it does not list, or a call to a function of the
// excerpt, which the walk follows only as a statement. ParseImageName is the
// exception: it reads its argument and writes nothing, and
// checkParseImageName holds what the defaults read from it.
func (f *walkFrame) check(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Ident, *ast.BasicLit:
	case *ast.ParenExpr:
		f.check(x.X)
	case *ast.StarExpr:
		f.check(x.X)
	case *ast.UnaryExpr:
		if !walkUnary[x.Op] {
			f.w.fail(x.Pos(), "the unary %s, which the walk does not follow", x.Op)
		}
		f.check(x.X)
	case *ast.BinaryExpr:
		f.check(x.X)
		f.check(x.Y)
	case *ast.SelectorExpr:
		f.check(x.X)
		// On the object, a name that is not a field is a method value, which
		// can change the object wherever it is called.
		if _, ok := f.resolve(x.X); ok {
			if _, ok := f.resolve(x); !ok {
				f.w.fail(x.Pos(), "%s is a method value of the object, which the walk does not follow", f.render(x))
			}
		}
	case *ast.IndexExpr:
		f.check(x.X)
		f.check(x.Index)
	case *ast.CompositeLit:
		f.checkType(x.Type)
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				f.check(kv.Key)
				f.check(kv.Value)
				continue
			}
			f.check(el)
		}
	case *ast.CallExpr:
		for _, a := range x.Args {
			f.check(a)
		}
		if fn := f.callee(x); fn != nil {
			if fn.pkg.path != k8sParsers || fn.decl.Name.Name != "ParseImageName" {
				f.w.fail(x.Pos(), "a call to %s inside an expression, which the walk does not follow", fn.decl.Name.Name)
			}
			return
		}
		if key := f.callKey(x); !walkCalls[key] {
			f.w.fail(x.Pos(), "a call to %s, which the walk does not follow", key)
		}
	default:
		f.w.fail(e.Pos(), "a %T the walk does not follow", e)
	}
}

// callKey names a call to anything but a function of the excerpt: a builtin or
// predeclared type by name, a function or type of a package by import path
// and name, a conversion by its type, and a method by its name and whether it
// is called on the object.
func (f *walkFrame) callKey(call *ast.CallExpr) string {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		// A builtin or predeclared type only while nothing shadows it.
		if f.bound(fun.Name) || f.fn.pkg.names[fun.Name] {
			f.w.fail(call.Pos(), "a call to %s, a name the excerpt binds, which the walk does not follow", fun.Name)
		}
		return fun.Name
	case *ast.SelectorExpr:
		if p, ok := f.pkgName(fun.X); ok {
			return p + "." + fun.Sel.Name
		}
		f.check(fun.X)
		if _, ok := f.resolve(fun.X); ok {
			return "method of the object " + fun.Sel.Name
		}
		return "method " + f.receiver(call, fun.X) + "." + fun.Sel.Name
	case *ast.IndexExpr:
		// An instantiated generic function, ptr.To[int64].
		return f.callKey(&ast.CallExpr{Fun: fun.X}) + "[" + f.w.src.text(fun.Index) + "]"
	case *ast.StarExpr, *ast.ArrayType, *ast.MapType:
		return "conversion " + f.w.src.text(fun)
	default:
		f.w.fail(call.Pos(), "a call through %T, which the walk does not follow", fun)
		return ""
	}
}

// pkgName gives the import path of e when it names an imported package.
func (f *walkFrame) pkgName(e ast.Expr) (string, bool) {
	id, ok := e.(*ast.Ident)
	if !ok || f.bound(id.Name) {
		return "", false
	}
	return importPath(f.fn.file, id.Name)
}

// receiver names the receiver of a method call by its identity: a variable
// of an imported package by import path and name, a range value by what it
// ranges over. The walk does not follow a method on any other receiver,
// whose type it cannot tell.
func (f *walkFrame) receiver(call *ast.CallExpr, e ast.Expr) string {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		if p, ok := f.pkgName(x.X); ok {
			return p + "." + x.Sel.Name
		}
	case *ast.Ident:
		if s, ok := f.elems[x.Name]; ok {
			return s
		}
	}
	f.w.fail(call.Pos(), "%s calls a method on a receiver the walk cannot name", f.render(call))
	return ""
}

// checkType fails on a type the walk does not follow: one that is not a
// name, a pointer, an array of constant length, a slice or a map of those.
func (f *walkFrame) checkType(e ast.Expr) {
	switch x := e.(type) {
	case nil, *ast.Ident:
	case *ast.SelectorExpr:
		if _, ok := f.pkgName(x.X); !ok {
			f.w.fail(x.Pos(), "the type %s, which the walk does not follow", f.w.src.text(x))
		}
	case *ast.StarExpr:
		f.checkType(x.X)
	case *ast.ArrayType:
		if _, ok := x.Len.(*ast.BasicLit); x.Len != nil && !ok {
			f.w.fail(x.Pos(), "the type %s, which the walk does not follow", f.w.src.text(x))
		}
		f.checkType(x.Elt)
	case *ast.MapType:
		f.checkType(x.Key)
		f.checkType(x.Value)
	default:
		f.w.fail(e.Pos(), "the type %s, which the walk does not follow", f.w.src.text(e))
	}
}

// bound says name is a variable of the function being walked.
func (f *walkFrame) bound(name string) bool {
	_, isVar := f.vars[name]
	_, isLocal := f.locals[name]
	return isVar || isLocal
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func joinJSONPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

// resolve says which field of the object e denotes, if any.
func (f *walkFrame) resolve(e ast.Expr) (pathVal, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		pv, ok := f.vars[x.Name]
		return pv, ok
	case *ast.ParenExpr:
		return f.resolve(x.X)
	case *ast.StarExpr:
		return f.resolve(x.X)
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return f.resolve(x.X)
		}
	case *ast.SelectorExpr:
		base, ok := f.resolve(x.X)
		if !ok || base.mapElem {
			return pathVal{}, false
		}
		t := derefType(base.typ)
		if t.Kind() != reflect.Struct {
			return pathVal{}, false
		}
		sf, ok := t.FieldByName(x.Sel.Name)
		if !ok {
			return pathVal{}, false // a method
		}
		p, cur := base.path, t
		for _, i := range sf.Index {
			field := cur.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if !field.Anonymous || name != "" {
				if name == "" {
					name = field.Name
				}
				p = joinJSONPath(p, name)
			}
			cur = derefType(field.Type)
		}
		return pathVal{typ: sf.Type, path: p}, true
	case *ast.IndexExpr:
		base, ok := f.resolve(x.X)
		if !ok || base.mapElem {
			return pathVal{}, false
		}
		t := derefType(base.typ)
		switch t.Kind() {
		case reflect.Slice, reflect.Array:
			// Every element under the pod spec is reached only through the
			// index of a range over the list itself: an element reached
			// otherwise, or by another list's index, may be one the
			// defaulting never visits.
			if id, ok := x.Index.(*ast.Ident); strings.HasPrefix(base.path, f.w.prefix) && (!ok || f.ranges[id.Name] != base.path) {
				f.w.fail(x.Pos(), "%s indexes %s by something other than a range over it, which the walk cannot place", f.w.src.text(x), base.path)
			}
			return pathVal{typ: t.Elem(), path: base.path + "[]"}, true
		case reflect.Map:
			return pathVal{typ: t.Elem(), path: base.path + "{}", mapElem: true}, true
		default:
			return pathVal{}, false
		}
	case *ast.CallExpr:
		// A conversion to a pointer type: (*v1.Container)(x).
		paren, ok := x.Fun.(*ast.ParenExpr)
		if !ok || len(x.Args) != 1 {
			return pathVal{}, false
		}
		star, ok := paren.X.(*ast.StarExpr)
		if !ok {
			return pathVal{}, false
		}
		base, ok := f.resolve(x.Args[0])
		if !ok {
			return pathVal{}, false
		}
		return pathVal{typ: f.w.src.typeOf(f.w.t, f.fn.file, star.X), path: base.path}, true
	}
	return pathVal{}, false
}

// typeOf is the linked type a selector of file names.
func (s *vendoredK8s) typeOf(t *testing.T, file *ast.File, e ast.Expr) reflect.Type {
	t.Helper()
	switch x := e.(type) {
	case *ast.ArrayType:
		if x.Len == nil {
			return reflect.SliceOf(s.typeOf(t, file, x.Elt))
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p, ok := importPath(file, id.Name); ok {
				if typ, ok := vendoredK8sTypes[p+"."+x.Sel.Name]; ok {
					return typ
				}
				t.Fatalf("%s: the type %s.%s is not in vendoredK8sTypes", s.fset.Position(e.Pos()), p, x.Sel.Name)
			}
		}
	}
	t.Fatalf("%s: a type the test does not read: %s", s.fset.Position(e.Pos()), s.text(e))
	return nil
}

// render is e as source text, each field of the object written {path} and
// each local variable as its value.
func (f *walkFrame) render(e ast.Expr) string {
	if pv, ok := f.resolve(e); ok {
		return "{" + pv.path + "}"
	}
	switch x := e.(type) {
	case *ast.Ident:
		if s, ok := f.locals[x.Name]; ok {
			return s
		}
		return x.Name
	case *ast.BasicLit:
		return x.Value
	case *ast.ParenExpr:
		return "(" + f.render(x.X) + ")"
	case *ast.UnaryExpr:
		return x.Op.String() + f.render(x.X)
	case *ast.StarExpr:
		return "*" + f.render(x.X)
	case *ast.BinaryExpr:
		return f.render(x.X) + " " + x.Op.String() + " " + f.render(x.Y)
	case *ast.SelectorExpr:
		return f.render(x.X) + "." + x.Sel.Name
	case *ast.IndexExpr:
		return f.render(x.X) + "[" + f.render(x.Index) + "]"
	case *ast.CallExpr:
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			args[i] = f.render(a)
		}
		return f.render(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	default:
		return f.w.src.text(e)
	}
}

// value is the default an assignment writes to a number, boolean or string
// field: its JSON literal, or {path} for another field's value.
func (f *walkFrame) value(e ast.Expr) string {
	if pv, ok := f.resolve(e); ok {
		return "{" + pv.path + "}"
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.STRING:
			s, err := strconv.Unquote(x.Value)
			if err == nil {
				return strconv.Quote(s)
			}
		case token.INT:
			return x.Value
		default:
		}
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" {
			return x.Name
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p, ok := importPath(f.fn.file, id.Name); ok {
				c, ok := vendoredK8sConsts[p+"."+x.Sel.Name]
				if !ok {
					f.w.fail(x.Pos(), "the constant %s.%s is not in vendoredK8sConsts", p, x.Sel.Name)
				}
				data, err := json.Marshal(c)
				if err != nil {
					f.w.fail(x.Pos(), "encode %s: %v", x.Sel.Name, err)
				}
				return string(data)
			}
		}
	}
	f.w.fail(e.Pos(), "a default the walk cannot evaluate: %s", f.render(e))
	return ""
}

// podDefaulterRoots are the generated defaulting functions of the objects the
// pod kinds emit, with the pod spec's path in each.
var podDefaulterRoots = []struct {
	pkg, fn string
	typ     reflect.Type
	prefix  string
}{
	{k8sCoreV1, "SetObjectDefaults_Pod", reflect.TypeFor[corev1.Pod](), "spec."},
	{k8sCoreV1, "SetObjectDefaults_PodTemplate", reflect.TypeFor[corev1.PodTemplate](), "template.spec."},
	{k8sCoreV1, "SetObjectDefaults_ReplicationController", reflect.TypeFor[corev1.ReplicationController](), "spec.template.spec."},
	{k8sAppsV1, "SetObjectDefaults_ReplicaSet", reflect.TypeFor[appsv1.ReplicaSet](), "spec.template.spec."},
}

// declarativeDefault marks a default k8s.io/api declares with a +default
// marker on the field, which the generated SetObjectDefaults_ functions apply.
const declarativeDefault = "declarative"

// defaultsOutsideSetDefaults are the rows of podSpecDefaultedZeros whose
// default the API server sets outside a SetDefaults_ function, with where:
// declarativeDefault, or the function that sets it. Every other row's default
// is set by a SetDefaults_ function.
var defaultsOutsideSetDefaults = map[string]string{
	"initContainers[].ports[].protocol": declarativeDefault,
	"containers[].ports[].protocol":     declarativeDefault,
	"volumes[].iscsi.iscsiInterface":    declarativeDefault,
	"volumes[].rbd.pool":                declarativeDefault,
	"volumes[].rbd.user":                declarativeDefault,
	"volumes[].rbd.keyring":             declarativeDefault,
	"volumes[].scaleIO.storageMode":     declarativeDefault,
	"volumes[].scaleIO.fsType":          declarativeDefault,
	// SetDefaults_Pod calls it when hostNetwork is true.
	"initContainers[].ports[].hostPort": "defaultHostNetworkPorts",
	"containers[].ports[].hostPort":     "defaultHostNetworkPorts",
}

// notDefaultedZeros are the pod spec's strings the API server sets that
// podSpecDefaultedZeros does not list, with the value the walk finds and why:
// an authored "" there keeps its meaning.
var notDefaultedZeros = map[string]struct{ value, reason string }{
	"serviceAccountName": {"{serviceAccount}", `the default is the other spelling's value, so an authored "" with neither set stays ""; the "default" service account is set by an admission plugin, not by defaulting`},
	"serviceAccount":     {"{serviceAccountName}", "the deprecated spelling is kept equal to serviceAccountName"},
}

// imagePullPolicyRows are the rows given imagePullPolicyDefault, with the
// field holding the image whose tag the default reads and any other condition
// the default is applied under.
var imagePullPolicyRows = map[string]struct{ image, cond string }{
	"initContainers[].imagePullPolicy": {image: "initContainers[].image"},
	"containers[].imagePullPolicy":     {image: "containers[].image"},
	// The ImageVolume feature gate is GA and locked on since Kubernetes 1.36
	// (pkg/features/kube_features.go, not in the excerpt), so the condition
	// always holds.
	"volumes[].image.pullPolicy": {image: "volumes[].image.reference", cond: "utilfeature.DefaultFeatureGate.Enabled(features.ImageVolume)"},
}

// TestKubernetesDefaulters_MatchVendoredSource holds podSpecDefaultedZeros,
// strings and numbers, to the API server's defaulting code in the excerpt.
// For each object a pod kind emits, it walks the generated SetObjectDefaults_
// function and every defaulting function of the excerpt it calls, and records
// each assignment to a number, boolean, string or list under the pod spec.
// Then every assignment is answered: a row of the list, given the default the
// code writes and applied only to an omitted value; one of notDefaultedZeros;
// and the list has no row the code does not write. An image pull policy row is
// held to the code's branch on the image's tag, and the hostPort row to the
// Pod's hostNetwork condition: the Pod's defaulting applies it, and the
// templates' does not, since a pod created from a template is defaulted as a
// Pod. A write the walk cannot place, a call it cannot follow or a default it
// cannot evaluate fails the test, naming the source line.
func TestKubernetesDefaulters_MatchVendoredSource(t *testing.T) {
	src := loadVendoredK8s(t)
	want := podSpecDefaultedZeros("", &corev1.PodSpec{HostNetwork: true}).fields
	for _, root := range podDefaulterRoots {
		t.Run(root.fn, func(t *testing.T) {
			writes := src.podSpecWrites(t, root.pkg, root.fn, root.typ, root.prefix)
			isPod := root.fn == "SetObjectDefaults_Pod"
			// Vacuity guard: the walk reaches the containers, their probes and
			// the volumes. At v1.37.1 it finds 70 fields on the Pod and 68 on
			// each template.
			if len(writes) < 50 {
				t.Fatalf("the walk found %d defaulted fields under the pod spec, want >= 50: it stops early", len(writes))
			}
			t.Logf("the walk found %d defaulted fields under the pod spec", len(writes))
			for _, p := range slices.Sorted(maps.Keys(writes)) {
				ws := writes[p]
				for _, wr := range ws {
					if wr.kind == reflect.Slice || wr.kind == reflect.Array {
						t.Errorf("%s: %s: the API server defaults the list %s to %s, which no list of the pod kinds refuses", wr.pos, wr.fn, p, wr.value)
					}
				}
				def, listed := want[p]
				if not, ok := notDefaultedZeros[p]; ok {
					if listed {
						t.Errorf("%s is listed and is answered in notDefaultedZeros", p)
					}
					for _, wr := range ws {
						if wr.value != not.value {
							t.Errorf("%s: %s: %s is set to %s, notDefaultedZeros says %s", wr.pos, wr.fn, p, wr.value, not.value)
						}
					}
					continue
				}
				if !listed {
					t.Errorf("%s: %s sets %s to %s when it is %v (under %v): not in podSpecDefaultedZeros", ws[0].pos, ws[0].fn, p, ws[0].value, zeroWord(ws[0].guarded), ws[0].conds)
					continue
				}
				checkDefaultOrigin(t, p, ws)
				switch img, isPull := imagePullPolicyRows[p]; {
				case isPull:
					checkImagePullPolicy(t, p, img.image, img.cond, def, ws)
				case strings.HasSuffix(p, ".hostPort"):
					checkHostPort(t, p, def, ws, isPod)
				default:
					if len(ws) != 1 || !ws[0].guarded || len(ws[0].conds) != 0 {
						t.Errorf("%s: %s is not set once, to a value, only when omitted: %+v", ws[0].pos, p, ws)
						continue
					}
					if ws[0].value != def {
						t.Errorf("%s: %s: the API server sets %s to %s, podSpecDefaultedZeros says %s", ws[0].pos, ws[0].fn, p, ws[0].value, def)
					}
				}
			}
			for _, p := range slices.Sorted(maps.Keys(want)) {
				if writes[p] == nil && (isPod || !strings.HasSuffix(p, ".hostPort")) {
					t.Errorf("stale %s: %s does not set it", p, root.fn)
				}
			}
		})
	}
	checkParseImageName(t, src)
}

func zeroWord(guarded bool) string {
	if guarded {
		return "omitted"
	}
	return "set or not"
}

// checkDefaultOrigin holds a row's writes to defaultsOutsideSetDefaults.
func checkDefaultOrigin(t *testing.T, p string, ws []specWrite) {
	t.Helper()
	where, outside := defaultsOutsideSetDefaults[p]
	for _, wr := range ws {
		switch {
		case strings.HasPrefix(wr.fn, "SetDefaults_"):
			if outside {
				t.Errorf("%s: %s sets %s, defaultsOutsideSetDefaults says %s", wr.pos, wr.fn, p, where)
			}
		case !outside:
			t.Errorf("%s: %s sets %s outside a SetDefaults_ function: answer it in defaultsOutsideSetDefaults", wr.pos, wr.fn, p)
		case where == declarativeDefault && !strings.HasPrefix(wr.fn, "SetObjectDefaults_"),
			where != declarativeDefault && wr.fn != where:
			t.Errorf("%s: %s sets %s, defaultsOutsideSetDefaults says %s", wr.pos, wr.fn, p, where)
		}
	}
}

// checkImagePullPolicy holds an image pull policy row to the code's branch:
// "Always" when ParseImageName reads the image's tag as latest, else
// "IfNotPresent", only when omitted.
func checkImagePullPolicy(t *testing.T, p, image, cond, def string, ws []specWrite) {
	t.Helper()
	latest := fmt.Sprintf(`parsers.ParseImageName({%s})[1] == "latest"`, image)
	var pre []string
	if cond != "" {
		pre = []string{cond}
	}
	want := []specWrite{
		{value: strconv.Quote(string(corev1.PullAlways)), guarded: true, conds: append(slices.Clip(pre), latest)},
		{value: strconv.Quote(string(corev1.PullIfNotPresent)), guarded: true, conds: append(slices.Clip(pre), "!("+latest+")")},
	}
	got := make([]specWrite, len(ws))
	for i, wr := range ws {
		got[i] = specWrite{value: wr.value, guarded: wr.guarded, conds: wr.conds}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: %s is set %+v, want %+v", ws[0].pos, p, got, want)
	}
	if def != imagePullPolicyDefault {
		t.Errorf("%s records %s, want imagePullPolicyDefault", p, def)
	}
}

// checkHostPort holds a hostPort row to the Pod's defaulting: set to the
// port's containerPort when hostNetwork is true, only when omitted. A
// template's defaulting does not set it.
func checkHostPort(t *testing.T, p, def string, ws []specWrite, isPod bool) {
	t.Helper()
	if !isPod {
		t.Errorf("%s: %s sets %s on a template", ws[0].pos, ws[0].fn, p)
		return
	}
	containerPort := "{" + strings.TrimSuffix(p, "hostPort") + "containerPort}"
	if len(ws) != 1 || !ws[0].guarded || !slices.Equal(ws[0].conds, []string{"{hostNetwork}"}) || ws[0].value != containerPort {
		t.Errorf("%s: %s is set %+v, want %s only when omitted, under {hostNetwork}", ws[0].pos, p, ws, containerPort)
	}
	if def != hostNetworkHostPortDefault {
		t.Errorf("%s records %s, want hostNetworkHostPortDefault", p, def)
	}
}

// checkParseImageName holds imagePullPolicyDefault's reading of an image that
// names neither a tag nor a digest to ParseImageName: its second result is
// the tag, set to "latest" when neither is named.
func checkParseImageName(t *testing.T, src *vendoredK8s) {
	t.Helper()
	fn := src.fn(t, k8sParsers, "ParseImageName")
	body := fn.decl.Body.List
	ret, ok := body[len(body)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 4 || src.text(ret.Results[1]) != "tag" {
		t.Errorf("ParseImageName's second result is not tag: %s", src.text(body[len(body)-1]))
	}
	is := findIf(src, fn.decl.Body, "len(tag) == 0 && len(digest) == 0")
	if is == nil || len(is.Body.List) != 1 || src.text(is.Body.List[0]) != `tag = "latest"` {
		t.Error(`ParseImageName does not set tag to "latest" when neither a tag nor a digest is named`)
	}
}

// findIf is the if statement directly in body whose condition reads cond,
// with neither an initialiser nor an else, or nil.
func findIf(src *vendoredK8s, body *ast.BlockStmt, cond string) *ast.IfStmt {
	for _, s := range body.List {
		if is, ok := s.(*ast.IfStmt); ok && is.Init == nil && is.Else == nil && src.text(is.Cond) == cond {
			return is
		}
	}
	return nil
}

// stmtTexts is the source text of each statement of body.
func stmtTexts(src *vendoredK8s, body *ast.BlockStmt) []string {
	out := make([]string, len(body.List))
	for i, s := range body.List {
		out[i] = src.text(s)
	}
	return out
}

// literalEval evaluates an initialiser of the excerpt into a value of a
// linked k8s.io/api type.
type literalEval struct {
	t      *testing.T
	src    *vendoredK8s
	pkg    *vendoredPkg
	file   *ast.File
	locals map[string]ast.Expr
}

func (e *literalEval) fail(n ast.Node, format string, args ...any) {
	e.t.Helper()
	e.t.Fatalf("%s: %s", e.src.fset.Position(n.Pos()), fmt.Sprintf(format, args...))
}

// eval is x as a value of type want.
func (e *literalEval) eval(x ast.Expr, want reflect.Type) reflect.Value {
	e.t.Helper()
	switch v := x.(type) {
	case *ast.CompositeLit:
		typ := want
		if v.Type != nil {
			typ = e.src.typeOf(e.t, e.file, v.Type)
		}
		switch typ.Kind() {
		case reflect.Slice:
			out := reflect.MakeSlice(typ, 0, len(v.Elts))
			for _, elt := range v.Elts {
				out = reflect.Append(out, e.eval(elt, typ.Elem()))
			}
			return out
		case reflect.Struct:
			out := reflect.New(typ).Elem()
			for _, elt := range v.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					e.fail(elt, "an unkeyed struct literal")
				}
				key, _ := kv.Key.(*ast.Ident)
				if key == nil {
					e.fail(kv, "a struct key the test does not read")
				}
				field := out.FieldByName(key.Name)
				if !field.IsValid() {
					e.fail(kv, "%s has no field %s", typ, key.Name)
				}
				field.Set(e.eval(kv.Value, field.Type()))
			}
			return out
		default:
			e.fail(v, "a %s literal the test does not read", typ.Kind())
		}
	case *ast.UnaryExpr:
		if v.Op == token.AND && want.Kind() == reflect.Pointer {
			p := reflect.New(want.Elem())
			p.Elem().Set(e.eval(v.X, want.Elem()))
			return p
		}
	case *ast.BasicLit:
		out := reflect.New(want).Elem()
		switch {
		case v.Kind == token.INT && out.CanInt():
			n, err := strconv.ParseInt(v.Value, 0, 64)
			if err != nil {
				e.fail(v, "%v", err)
			}
			out.SetInt(n)
			return out
		case v.Kind == token.STRING && want.Kind() == reflect.String:
			s, err := strconv.Unquote(v.Value)
			if err != nil {
				e.fail(v, "%v", err)
			}
			out.SetString(s)
			return out
		}
	case *ast.Ident:
		if v.Name == "nil" {
			return reflect.Zero(want)
		}
		if local, ok := e.locals[v.Name]; ok {
			return e.eval(local, want)
		}
		if val, ok := e.pkg.values[v.Name]; ok {
			if val.expr == nil {
				return reflect.Zero(want)
			}
			sub := &literalEval{t: e.t, src: e.src, pkg: val.pkg, file: val.file}
			return sub.eval(val.expr, want)
		}
	case *ast.SelectorExpr:
		id, ok := v.X.(*ast.Ident)
		if !ok {
			break
		}
		p, ok := importPath(e.file, id.Name)
		if !ok {
			break
		}
		if pkg := e.src.pkgs[p]; pkg != nil {
			val, ok := pkg.values[v.Sel.Name]
			if !ok || val.expr == nil {
				e.fail(v, "%s.%s has no value in the excerpt", p, v.Sel.Name)
			}
			sub := &literalEval{t: e.t, src: e.src, pkg: val.pkg, file: val.file}
			return sub.eval(val.expr, want)
		}
		c, ok := vendoredK8sConsts[p+"."+v.Sel.Name]
		if !ok {
			e.fail(v, "the constant %s.%s is not in vendoredK8sConsts", p, v.Sel.Name)
		}
		return reflect.ValueOf(c).Convert(want)
	case *ast.CallExpr:
		// A conversion to a basic type: int32(x).
		if id, ok := v.Fun.(*ast.Ident); ok && len(v.Args) == 1 && slices.Contains([]string{"int32", "int64", "string"}, id.Name) {
			return e.eval(v.Args[0], want)
		}
	}
	e.fail(x, "an expression the test does not evaluate: %s", e.src.text(x))
	return reflect.Value{}
}

// evalJSON is x, of type want, encoded as the emitted object would be.
func (e *literalEval) evalJSON(x ast.Expr, want reflect.Type) string {
	e.t.Helper()
	data, err := json.Marshal(e.eval(x, want).Interface())
	if err != nil {
		e.fail(x, "encode: %v", err)
	}
	return string(data)
}

// assignmentIn is the assignment to lhs directly in body, or nil.
func assignmentIn(src *vendoredK8s, body *ast.BlockStmt, lhs string) *ast.AssignStmt {
	for _, s := range body.List {
		if as, ok := s.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 && src.text(as.Lhs[0]) == lhs {
			return as
		}
	}
	return nil
}

// flatStmts is the source text of each statement of body, its whitespace
// collapsed to single spaces.
func flatStmts(src *vendoredK8s, body *ast.BlockStmt) []string {
	out := stmtTexts(src, body)
	for i, s := range out {
		out[i] = strings.Join(strings.Fields(s), " ")
	}
	return out
}

// writesField says the lvalue e writes to a field named field or to anything
// beneath one: an element, or a field of it.
func writesField(e ast.Expr, field string) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			if x.Sel.Name == field {
				return true
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return false
		}
	}
}

// checkOnlyWrites fails unless the assignments to a field named field, or to
// anything beneath one, through any variable, in every function of the package
// at pkgPath are exactly want, by source text: a second write, before or
// after, would change the default the checks below read from one of them.
func checkOnlyWrites(t *testing.T, src *vendoredK8s, pkgPath, field string, want ...string) {
	t.Helper()
	var got []string
	for _, name := range slices.Sorted(maps.Keys(src.pkgs[pkgPath].funcs)) {
		ast.Inspect(src.pkgs[pkgPath].funcs[name].decl.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				if slices.ContainsFunc(s.Lhs, func(l ast.Expr) bool { return writesField(l, field) }) {
					got = append(got, name+": "+src.text(s))
				}
			case *ast.IncDecStmt:
				if writesField(s.X, field) {
					got = append(got, name+": "+src.text(s))
				}
			}
			return true
		})
	}
	if !slices.Equal(got, want) {
		t.Errorf("the writes to .%s in %s are\n%s\nwant\n%s", field, pkgPath, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestKubernetesDefaulters_ListDefaultsMatchVendoredSource holds the list
// defaults of hpaDefaultedZeros and networkPolicyDefaultedZeros to the API
// server's defaulting code in the excerpt: each default is the literal the
// code assigns, evaluated into the linked type and encoded, and each is
// assigned only when the list is omitted or empty. The functions that reach
// the defaults are held to their exact statements, so a return, branch or
// initialiser added to them cannot change when a default applies unseen, and
// checkOnlyWrites holds each default to being the only write to its field or
// beneath it, so a write elsewhere cannot change it unseen.
func TestKubernetesDefaulters_ListDefaultsMatchVendoredSource(t *testing.T) {
	src := loadVendoredK8s(t)

	t.Run("horizontalpodautoscaler", func(t *testing.T) {
		hpa := src.fn(t, k8sAutoscalingV2, "SetDefaults_HorizontalPodAutoscaler")
		metrics := findIf(src, hpa.decl.Body, "len(obj.Spec.Metrics) == 0")
		if metrics == nil {
			t.Fatal("SetDefaults_HorizontalPodAutoscaler does not test len(obj.Spec.Metrics) == 0 in an if with no initialiser and no else")
		}
		ev := &literalEval{t: t, src: src, pkg: hpa.pkg, file: hpa.file, locals: map[string]ast.Expr{}}
		assign := assignmentIn(src, metrics.Body, "obj.Spec.Metrics")
		if assign == nil {
			t.Fatal("SetDefaults_HorizontalPodAutoscaler does not assign obj.Spec.Metrics when it is empty")
		}
		for _, s := range metrics.Body.List {
			switch as, ok := s.(*ast.AssignStmt); {
			case s == ast.Stmt(assign):
			case ok && as.Tok == token.DEFINE && len(as.Lhs) == 1 && len(as.Rhs) == 1:
				ev.locals[src.text(as.Lhs[0])] = as.Rhs[0]
			default:
				t.Errorf("%s: a statement the metrics default does not hold: %s", src.fset.Position(s.Pos()), src.text(s))
			}
		}
		checkOnlyWrites(t, src, k8sAutoscalingV2, "Metrics", "SetDefaults_HorizontalPodAutoscaler: "+src.text(assign))
		if got, want := ev.evalJSON(assign.Rhs[0], reflect.TypeFor[[]autoscalingv2.MetricSpec]()), hpaDefaultedZeros.fields["metrics"]; got != want {
			t.Errorf("the API server defaults metrics to %s, hpaDefaultedZeros says %s", got, want)
		}
		// The functions that reach the defaults are held to their exact
		// statements: a return, a branch, an initialiser or a write added
		// anywhere in them could change when or whether a default applies.
		hpaStmts := hpa.decl.Body.List
		if len(hpaStmts) != 3 || flatStmts(src, hpa.decl.Body)[0] != "if obj.Spec.MinReplicas == nil { obj.Spec.MinReplicas = ptr.To[int32](1) }" ||
			hpaStmts[1] != ast.Stmt(metrics) || src.text(hpaStmts[2]) != "SetDefaults_HorizontalPodAutoscalerBehavior(obj)" {
			t.Errorf("SetDefaults_HorizontalPodAutoscaler is not the minReplicas default, the metrics default, then SetDefaults_HorizontalPodAutoscalerBehavior(obj): %q", flatStmts(src, hpa.decl.Body))
		}

		behavior := src.fn(t, k8sAutoscalingV2, "SetDefaults_HorizontalPodAutoscalerBehavior")
		wantBehavior := []string{"if obj.Spec.Behavior != nil { " +
			"obj.Spec.Behavior.ScaleUp = GenerateHPAScaleUpRules(obj.Spec.Behavior.ScaleUp) " +
			"obj.Spec.Behavior.ScaleDown = GenerateHPAScaleDownRules(obj.Spec.Behavior.ScaleDown) }"}
		if got := flatStmts(src, behavior.decl.Body); !slices.Equal(got, wantBehavior) {
			t.Errorf("SetDefaults_HorizontalPodAutoscalerBehavior does not only generate both directions' rules from the authored ones: %q", got)
		}
		checkOnlyWrites(t, src, k8sAutoscalingV2, "Behavior",
			"SetDefaults_HorizontalPodAutoscalerBehavior: obj.Spec.Behavior.ScaleUp = GenerateHPAScaleUpRules(obj.Spec.Behavior.ScaleUp)",
			"SetDefaults_HorizontalPodAutoscalerBehavior: obj.Spec.Behavior.ScaleDown = GenerateHPAScaleDownRules(obj.Spec.Behavior.ScaleDown)")
		checkOnlyWrites(t, src, k8sAutoscalingV2, "ScaleUp",
			"SetDefaults_HorizontalPodAutoscalerBehavior: obj.Spec.Behavior.ScaleUp = GenerateHPAScaleUpRules(obj.Spec.Behavior.ScaleUp)")
		checkOnlyWrites(t, src, k8sAutoscalingV2, "ScaleDown",
			"SetDefaults_HorizontalPodAutoscalerBehavior: obj.Spec.Behavior.ScaleDown = GenerateHPAScaleDownRules(obj.Spec.Behavior.ScaleDown)")
		copyRules := src.fn(t, k8sAutoscalingV2, "copyHPAScalingRules")
		wantCopy := []string{
			"if from == nil { return to }",
			"if from.SelectPolicy != nil { to.SelectPolicy = from.SelectPolicy }",
			"if from.StabilizationWindowSeconds != nil { to.StabilizationWindowSeconds = from.StabilizationWindowSeconds }",
			"if from.Policies != nil { to.Policies = from.Policies }",
			"if from.Tolerance != nil { to.Tolerance = from.Tolerance }",
			"return to",
		}
		if got := flatStmts(src, copyRules.decl.Body); !slices.Equal(got, wantCopy) {
			t.Errorf("copyHPAScalingRules does not only copy each authored field over the defaults, keeping the default policies when the authored ones are nil: %q", got)
		}
		checkOnlyWrites(t, src, k8sAutoscalingV2, "Policies", "copyHPAScalingRules: to.Policies = from.Policies")
		for _, dir := range []struct{ fn, rules, path string }{
			{"GenerateHPAScaleUpRules", "defaultHPAScaleUpRules", "behavior.scaleUp.policies"},
			{"GenerateHPAScaleDownRules", "defaultHPAScaleDownRules", "behavior.scaleDown.policies"},
		} {
			gen := src.fn(t, k8sAutoscalingV2, dir.fn)
			wantGen := []string{
				"defaultScalingRules := " + dir.rules + ".DeepCopy()",
				"return copyHPAScalingRules(scalingRules, defaultScalingRules)",
			}
			if !slices.Equal(stmtTexts(src, gen.decl.Body), wantGen) {
				t.Errorf("%s does not copy the authored rules over %s", dir.fn, dir.rules)
			}
			rules := ev.eval(&ast.Ident{Name: dir.rules}, reflect.TypeFor[autoscalingv2.HPAScalingRules]()).Interface().(autoscalingv2.HPAScalingRules)
			data, err := json.Marshal(rules.Policies)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(data), hpaDefaultedZeros.fields[dir.path]; got != want {
				t.Errorf("the API server defaults %s to %s, hpaDefaultedZeros says %s", dir.path, got, want)
			}
		}
	})

	t.Run("networkpolicy", func(t *testing.T) {
		np := src.fn(t, k8sNetworkingV1, "SetDefaults_NetworkPolicy")
		empty := findIf(src, np.decl.Body, "len(obj.Spec.PolicyTypes) == 0")
		if empty == nil {
			t.Fatal("SetDefaults_NetworkPolicy does not test len(obj.Spec.PolicyTypes) == 0 in an if with no initialiser and no else")
		}
		ev := &literalEval{t: t, src: src, pkg: np.pkg, file: np.file}
		assign := assignmentIn(src, empty.Body, "obj.Spec.PolicyTypes")
		egress := findIf(src, empty.Body, "len(obj.Spec.Egress) != 0")
		// The default is the base list, then the egress type appended to it:
		// exactly those two statements, in that order.
		if len(np.decl.Body.List) != 1 || np.decl.Body.List[0] != ast.Stmt(empty) {
			t.Fatalf("SetDefaults_NetworkPolicy is not only the policyTypes default: %q", flatStmts(src, np.decl.Body))
		}
		if assign == nil || egress == nil || len(empty.Body.List) != 2 || empty.Body.List[0] != ast.Stmt(assign) || empty.Body.List[1] != ast.Stmt(egress) || len(egress.Body.List) != 1 {
			t.Fatalf("SetDefaults_NetworkPolicy's default when policyTypes is empty is not an assignment then an egress branch: %q", stmtTexts(src, empty.Body))
		}
		base := ev.evalJSON(assign.Rhs[0], reflect.TypeFor[[]networkingv1.PolicyType]())
		addAssign := assignmentIn(src, egress.Body, "obj.Spec.PolicyTypes")
		var add *ast.CallExpr
		if addAssign != nil {
			add, _ = addAssign.Rhs[0].(*ast.CallExpr)
		}
		if add == nil || src.text(add.Fun) != "append" || len(add.Args) != 2 || src.text(add.Args[0]) != "obj.Spec.PolicyTypes" {
			t.Fatal("SetDefaults_NetworkPolicy does not append to obj.Spec.PolicyTypes when an egress rule is authored")
		}
		checkOnlyWrites(t, src, k8sNetworkingV1, "PolicyTypes", "SetDefaults_NetworkPolicy: "+src.text(assign), "SetDefaults_NetworkPolicy: "+src.text(addAssign))
		added := ev.evalJSON(add.Args[1], reflect.TypeFor[networkingv1.PolicyType]())
		want := fmt.Sprintf("%s, with %s when an egress rule is authored", base, added)
		if got := networkPolicyDefaultedZeros.fields["policyTypes"]; got != want {
			t.Errorf("networkPolicyDefaultedZeros says %s, the API server's default is %s", got, want)
		}
	})
}

// setAuthoredPath sets the json path p, [] for the first element, in tree to
// value, adding what is missing.
func setAuthoredPath(tree map[string]any, p string, value any) {
	segs := strings.Split(p, ".")
	m := tree
	for _, seg := range segs[:len(segs)-1] {
		key, isList := strings.CutSuffix(seg, "[]")
		if !isList {
			next, _ := m[key].(map[string]any)
			if next == nil {
				next = map[string]any{}
				m[key] = next
			}
			m = next
			continue
		}
		list, _ := m[key].([]any)
		if len(list) == 0 {
			list = []any{map[string]any{}}
			m[key] = list
		}
		m = list[0].(map[string]any)
	}
	m[segs[len(segs)-1]] = value
}

// TestPodKinds_RefuseEveryDefaultedZero: on each kind that holds a pod spec,
// an authored "" or 0 on every row of podSpecDefaultedZeros is refused by the
// path authored, naming the row's default; hostPort with hostNetwork true.
func TestPodKinds_RefuseEveryDefaultedZero(t *testing.T) {
	kinds := []struct {
		name   string
		prefix string
		build  func(map[string]any) error
	}{
		{"pod", "", func(p map[string]any) error {
			_, err := (&PodHandler{}).ToApplicationConfig(&oam.Component{Name: "runner", Properties: p}, "ns")
			return err
		}},
		{"podtemplate", "template.spec.", func(p map[string]any) error {
			_, err := (&PodTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "runner", Properties: p}, "ns")
			return err
		}},
		{"replicaset", "template.spec.", func(p map[string]any) error {
			_, err := (&ReplicaSetHandler{}).ToApplicationConfig(&oam.Component{Name: "runner", Properties: p}, "ns")
			return err
		}},
		{"replicationcontroller", "template.spec.", func(p map[string]any) error {
			_, err := (&ReplicationControllerHandler{}).ToApplicationConfig(&oam.Component{Name: "runner", Properties: p}, "ns")
			return err
		}},
	}
	rows := podSpecDefaultedZeros("", &corev1.PodSpec{HostNetwork: true}).fields
	for _, k := range kinds {
		for _, p := range slices.Sorted(maps.Keys(rows)) {
			t.Run(k.name+"/"+p, func(t *testing.T) {
				def := rows[p]
				hostPort := strings.HasSuffix(p, ".hostPort")
				var zero any = ""
				zeroText := `""`
				if _, err := strconv.Atoi(def); err == nil || hostPort {
					zero, zeroText = 0, "0"
				}
				spec := map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/app:1.2.3"}}}
				setAuthoredPath(spec, p, zero)
				if hostPort {
					spec["hostNetwork"] = true
				}
				props := spec
				if k.prefix != "" {
					props = map[string]any{"template": map[string]any{"spec": spec}}
				}
				authored := k.prefix + strings.ReplaceAll(p, "[]", "[0]")
				want := fmt.Sprintf("%s: %s cannot be carried by the Kubernetes API types (the field is omitted when zero, so the API server would apply its default %s)", authored, zeroText, def)
				if err := k.build(props); err == nil || err.Error() != want {
					t.Fatalf("err = %v\nwant %s", err, want)
				}
				if hostPort {
					delete(spec, "hostNetwork")
					if err := k.build(props); err != nil && strings.Contains(err.Error(), "cannot be carried") {
						t.Errorf("hostPort 0 without hostNetwork refused: %v", err)
					}
				}
			})
		}
	}
}
