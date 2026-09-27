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

// scanOptionalWrappers parses every non-test Go file directly in dir and
// returns one "<path> declares <name>" finding per optional<X> wrapper, plus
// the number of files it parsed.
func scanOptionalWrappers(t testing.TB, dir string) (findings []string, scanned int) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}
	fset := token.NewFileSet()
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
			findings = append(findings, path+" declares "+name)
		}
	}
	return findings, scanned
}

// TestNoOptionalFieldWrappers fails if any non-test file in this package
// declares an optional<X> field wrapper (go-kure/launcher#459).
func TestNoOptionalFieldWrappers(t *testing.T) {
	findings, scanned := scanOptionalWrappers(t, ".")
	for _, finding := range findings {
		t.Errorf("%s: %s", finding, optionalWrapperReason)
	}
	// A glob that matched nothing (wrong working directory) would pass vacuously.
	if scanned == 0 {
		t.Fatal("scanned no non-test Go files; the guard is not looking at this package")
	}
}

// TestScanOptionalWrappersScansEveryFile proves the directory scan reaches
// every non-test file, not only common.go where the abolished wrappers lived,
// and that it skips _test.go files.
func TestScanOptionalWrappersScansEveryFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"common.go":       "package components\n\nfunc parseStringField() {}\n",
		"podspec.go":      "package components\n\nfunc optionalObjectList() {}\n",
		"planted_test.go": "package components\n\nfunc optionalIgnoredInTests() {}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	findings, scanned := scanOptionalWrappers(t, dir)
	if scanned != 2 {
		t.Errorf("scanned %d files, want 2 (common.go, podspec.go)", scanned)
	}
	want := filepath.Join(dir, "podspec.go") + " declares optionalObjectList"
	if len(findings) != 1 || findings[0] != want {
		t.Fatalf("findings = %q, want [%q]", findings, want)
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
