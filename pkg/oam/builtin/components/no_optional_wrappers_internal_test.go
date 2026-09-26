package components

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// optionalWrapperReason is the failure message the guard carries: the wrappers
// were abolished by go-kure/launcher#444 because the parse<X>Field family reads an
// explicit null as absence itself, so a new wrapper duplicates that check and its
// doc comment implies the plain helper does not handle null — which is false.
const optionalWrapperReason = "use the parse<X>Field / parseObjectList / parseStringList family instead: " +
	"it reads an explicit null as absence itself (authoredValue, common.go), so an optional<X> " +
	"wrapper duplicates that check (go-kure/launcher#444, go-kure/launcher#459)"

// optionalWrappers returns the name of every function or method declared in f
// whose name starts with "optional".
func optionalWrappers(f *ast.File) []string {
	var names []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "optional") {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

// TestNoOptionalFieldWrappers fails if any non-test file in this package
// declares an optional<X> field wrapper (go-kure/launcher#459).
func TestNoOptionalFieldWrappers(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		for _, name := range optionalWrappers(f) {
			t.Errorf("%s declares %s: %s", path, name, optionalWrapperReason)
		}
	}
	// A glob that matched nothing (wrong working directory) would pass vacuously.
	if scanned == 0 {
		t.Fatal("scanned no non-test Go files; the guard is not looking at this package")
	}
}

// TestOptionalWrappersDetectsPlantedWrapper proves the detector can fail: a
// guard never observed to fail is not known to be wired up.
func TestOptionalWrappersDetectsPlantedWrapper(t *testing.T) {
	const src = `package components

func optionalInt64(raw map[string]any, key, label string) (int64, bool, error) { return 0, false, nil }

type cfg struct{}

func (cfg) optionalString() {}

func parseInt64Field() {}
`
	f, err := parser.ParseFile(token.NewFileSet(), "planted.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse planted source: %v", err)
	}
	got := optionalWrappers(f)
	want := []string{"optionalInt64", "optionalString"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("optionalWrappers = %v, want %v", got, want)
	}
}
