package model

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
)

const goldenFunctions = "testdata/celenv1-functions.txt"

// functionLines lists every function of an environment as `name: overload,overload`,
// sorted, one line per function. The overload ids are part of the line because a new
// overload changes what an expression can mean. A function whose declaration is
// disabled is written `name [disabled]: ...`: the environment keeps its runtime
// binding, but the type checker refuses a call in source. The names and the ids alone
// cannot tell that state.
func functionLines(env *cel.Env) string {
	var lines []string
	for name, fn := range env.Functions() {
		var ids []string
		for _, o := range fn.OverloadDecls() {
			ids = append(ids, o.ID())
		}
		sort.Strings(ids)
		if fn.IsDeclarationDisabled() {
			name += " [disabled]"
		}
		lines = append(lines, name+": "+strings.Join(ids, ","))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// firstDifference names the first line where two listings differ.
func firstDifference(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d: got %q, want %q", i+1, gl, wl)
		}
	}
	return "no difference"
}

// TestEnvFunctionSetIsFrozen compares the functions of the celEnv 1 environment with a
// golden file. The golden file is the contract of `celEnv: "1"`: a cel-go upgrade that
// adds a function or an overload must fail here, so a model written today means the
// same later.
//
// On cel-go v0.31.0 and v0.32.0 alone this test cannot tell a pinned library from an unpinned one,
// because every pin equals the library default at those releases. It passes with or
// without the pins and discriminates nothing about them. It earns its place on an older
// release (v0.30.0, run with -modfile) and after a later move of cel-go. The golden file
// is written with CELENV_GOLDEN_OUT=<path>, never by hand.
func TestEnvFunctionSetIsFrozen(t *testing.T) {
	env, err := newEnv()
	if err != nil {
		t.Fatalf("newEnv: %v", err)
	}
	got := functionLines(env)
	// Control: the listing must hold functions from the standard library and from each
	// extension, or an empty listing would match an empty golden file.
	for _, name := range []string{"size: ", "_+_: ", "upperAscii: ", "slice: "} {
		if !strings.Contains("\n"+got, "\n"+name) {
			t.Fatalf("the listing has no %q line: the environment lacks a library", name)
		}
	}
	if out := os.Getenv("CELENV_GOLDEN_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden file: %v", err)
		}
		t.Skipf("golden file written to %s (%d functions)", out, strings.Count(got, "\n"))
	}
	want, err := os.ReadFile(goldenFunctions)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	if got != string(want) {
		t.Fatalf("the function set of celEnv 1 changed, first difference at %s", firstDifference(got, string(want)))
	}
}

// The listing must see the state of a declaration. Two environments with the same
// function name and the same overload id differ when one disables the declaration, and
// the type checker then refuses every call of it in source. This test passes at the
// commit that adds the marker: it guards the listing, and the golden test above guards
// the environment.
func TestFunctionLinesSeeDisabledDeclarations(t *testing.T) {
	overload := cel.Overload("probe_int", []*cel.Type{cel.IntType}, cel.IntType)
	on, err := cel.NewEnv(cel.Function("probe", overload))
	if err != nil {
		t.Fatalf("enabled env: %v", err)
	}
	off, err := cel.NewEnv(cel.Function("probe", cel.DisableDeclaration(true), overload))
	if err != nil {
		t.Fatalf("disabled env: %v", err)
	}
	gotOn, gotOff := functionLines(on), functionLines(off)
	if !strings.Contains(gotOn, "probe: probe_int\n") {
		t.Errorf("enabled listing has no `probe: probe_int` line:\n%s", gotOn)
	}
	if !strings.Contains(gotOff, "probe [disabled]: probe_int\n") {
		t.Errorf("disabled listing has no `probe [disabled]: probe_int` line:\n%s", gotOff)
	}
	if gotOn == gotOff {
		t.Error("the listing is the same for an enabled and a disabled declaration")
	}
}

// The limits are stated in the README as start values. A change to one is a change to
// the format, so a test pins each.
func TestLimitValues(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"expression size", maxExpressionSize, 4096},
		{"recursion depth", maxRecursionDepth, 64},
		{"node count", maxNodeCount, 10000},
		{"bind depth", maxBindDepth, 8},
	} {
		if c.got != c.want {
			t.Errorf("%s limit = %d, want %d", c.name, c.got, c.want)
		}
	}
}
