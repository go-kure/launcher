package kurel

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// kindLists names the lists that hold one entry per component type. A change
// that adds a kind adds a line to each, and two such changes written side by
// side merge without a conflict only where their lines land between different
// neighbours. So every list here stands in the order of its component types,
// as sort.Strings gives it, and a new entry goes at its position instead of at
// the end: TestKindLists_InOrder holds that.
//
// file is relative to this package. name is a package-level variable,
// "Func.variable" for a variable a function declares, or "Func.return" for the
// literal a function returns.
var kindLists = []struct {
	file, name string
}{
	{"build.go", "builtinComponentHandlers.return"},
	{"build_test.go", "TestBuiltinComponentHandlers_RegisteredTypes.wantHandlers"},
	{"component_label_invariant_test.go", "componentLabelFixtures"},
	{"../../oam/builtin/components/core_kinds_test.go", "coreKindSchemas"},
	{"../../oam/builtin/components/kind_api_sets_internal_test.go", "apiSetKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "policyFreeKinds"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_GenerateCopies.reaches"},
	{"../../oam/builtin/components/kind_policy_free_test.go", "TestPolicyFreeKinds_Refusals.cases"},
	{"../../oam/validate.go", "validComponentTypes"},
}

// TestKindLists_InOrder fails on an entry that stands before its place, naming
// it and the entry it follows. It reads the lists from their source: the order
// a map literal is written in is not the map's.
func TestKindLists_InOrder(t *testing.T) {
	for _, list := range kindLists {
		t.Run(list.name, func(t *testing.T) {
			keys := goListKeys(t, list.file, list.name)
			if len(keys) == 0 {
				t.Fatalf("%s: %s holds no entry", list.file, list.name)
			}
			for _, defect := range kindListMisplaced(keys) {
				t.Errorf("%s: in %s, %s", list.file, list.name, defect)
			}
		})
	}
}

// TestKindListChecks_NameEachDefect shows the check on lists that are wrong in
// one way each, so that it does not pass for want of looking.
func TestKindListChecks_NameEachDefect(t *testing.T) {
	order := []struct {
		name string
		rows []string
		want []string
	}{
		{"in order", []string{"alpha", "beta"}, nil},
		{"misplaced", []string{"beta", "alpha"}, []string{`"alpha" stands after "beta"`}},
		{"doubled", []string{"alpha", "alpha", "beta"}, []string{`"alpha" stands after "alpha"`}},
	}
	for _, tt := range order {
		t.Run("order/"+tt.name, func(t *testing.T) {
			expectDefects(t, kindListMisplaced(tt.rows), tt.want)
		})
	}
}

// expectDefects holds got to one defect per entry of want, each beginning
// with it.
func expectDefects(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("defects = %q, want %d beginning with %q", got, len(want), want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("defect %d = %q, want it to begin with %q", i, got[i], want[i])
		}
	}
}

// kindListMisplaced names every entry that does not stand after the one
// before it in the order of sort.Strings; an entry written twice is one.
func kindListMisplaced(keys []string) []string {
	var defects []string
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			defects = append(defects, fmt.Sprintf("%q stands after %q; the entries are in the order of their component type, and a new one goes at its position", keys[i], keys[i-1]))
		}
	}
	return defects
}

// goListKeys returns, in the order they are written, the component types of
// the entries of the composite literal a Go source file gives the named
// variable.
func goListKeys(t *testing.T, file, name string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	lit := goListLiteral(parsed, name)
	if lit == nil {
		t.Fatalf("%s: no composite literal is given to %s", file, name)
	}
	keys := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		key, ok := goListKey(elt)
		if !ok {
			t.Fatalf("%s: an entry of %s names no component type this test can read: write the type as a string literal", file, name)
		}
		keys = append(keys, key)
	}
	return keys
}

// goListLiteral finds the literal: the value of the package-level variable
// name, or, in the function before the dot, of the variable after it or of the
// first return where "return" stands there.
func goListLiteral(file *ast.File, name string) *ast.CompositeLit {
	fn, local, inFunc := strings.Cut(name, ".")
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if inFunc {
				continue
			}
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || len(value.Values) != 1 || value.Names[0].Name != name {
					continue
				}
				if lit, ok := value.Values[0].(*ast.CompositeLit); ok {
					return lit
				}
			}
		case *ast.FuncDecl:
			if !inFunc || decl.Name.Name != fn || decl.Body == nil {
				continue
			}
			var found *ast.CompositeLit
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				if found != nil {
					return false
				}
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
						return true
					}
					if ident, ok := n.Lhs[0].(*ast.Ident); ok && ident.Name == local {
						found, _ = n.Rhs[0].(*ast.CompositeLit)
					}
				case *ast.ReturnStmt:
					if local == "return" && len(n.Results) == 1 {
						found, _ = n.Results[0].(*ast.CompositeLit)
					}
				}
				return found == nil
			})
			return found
		}
	}
	return nil
}

// goListKey reads the component type of one entry: a string, the key of a map
// entry, or of a struct the field named component, or its first field where
// the struct is written without field names.
func goListKey(elt ast.Expr) (string, bool) {
	switch elt := elt.(type) {
	case *ast.BasicLit:
		if elt.Kind != token.STRING {
			return "", false
		}
		key, err := strconv.Unquote(elt.Value)
		return key, err == nil
	case *ast.KeyValueExpr:
		return goListKey(elt.Key)
	case *ast.CompositeLit:
		if len(elt.Elts) == 0 {
			return "", false
		}
		if _, named := elt.Elts[0].(*ast.KeyValueExpr); !named {
			return goListKey(elt.Elts[0])
		}
		for _, field := range elt.Elts {
			pair, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if ident, ok := pair.Key.(*ast.Ident); ok && ident.Name == "component" {
				return goListKey(pair.Value)
			}
		}
	}
	return "", false
}
