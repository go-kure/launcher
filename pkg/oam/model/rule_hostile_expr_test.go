package model_test

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests are the hostile corpus of the limits of one expression (go-kure/launcher#1261): the
// cost of one hole, the size of one result, the nesting of cel.bind, the size of the text of an
// expression and its recursion depth. Each row is a hostile model and a control one step under the
// limit, both seen through a real Transform or through Register, so the limit is read where a
// consumer meets it. A row passes only when the hostile case fails with the error class of its
// limit and the control builds, so a stub that refuses everything fails the control.

// exprModel is the document of a model whose one part is a ConfigMap with one data key set to
// the expression. The hole has no space inside it, so the parser reads exactly the code points of expr.
func exprModel(expr string) string {
	return modelHeader + `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "${` + expr + `}"
`
}

// exprRegister loads and registers doc on a fresh Transformer and returns the Register error.
func exprRegister(t *testing.T, doc string) (*oam.Transformer, error) {
	t.Helper()
	tr := expandTransformer(t)
	m, err := model.Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return tr, model.NewRegistry(tr).Register(m)
}

// exprBuilds builds the model of expr through a Transform and fails the test if it does not.
func exprBuilds(t *testing.T, expr, what string) {
	t.Helper()
	tr, err := exprRegister(t, exprModel(expr))
	if err != nil {
		t.Fatalf("%s: Register: %v", what, err)
	}
	if _, err := objects(t, tr, orders(), nil); err != nil {
		t.Fatalf("%s: Transform: %v", what, err)
	}
}

// exprRefused registers the model of expr and wants a refusal that names substr.
func exprRefused(t *testing.T, expr, substr string) {
	t.Helper()
	_, err := exprRegister(t, exprModel(expr))
	if err == nil {
		t.Fatalf("a model with the expression %.60q was registered, want a refusal naming %q", expr, substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not name %q", err, substr)
	}
}

// exprCostError builds the model of expr and wants an error that wraps ErrCostLimit and names substr.
func exprCostError(t *testing.T, expr, substr string) {
	t.Helper()
	tr, err := exprRegister(t, exprModel(expr))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = objects(t, tr, orders(), nil)
	if err == nil {
		t.Fatalf("a hostile expression %.60q built, want an error naming %q", expr, substr)
	}
	if !errors.Is(err, model.ErrCostLimit) {
		t.Errorf("error %q does not wrap ErrCostLimit", err)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q does not name %q", err, substr)
	}
}

// Row 1: one hole that would spend more than 1,000,000 cost units stops on ErrCostLimit and names the
// limit. The bomb is two lists of 1,000,000 elements, which spend over 1,000,000 and under 10,000,000
// units in about 15 ms. A bomb of comprehension steps takes 0.5 s or more under -race and coverage,
// too near the 1 s deadline of a build, which would then stop it on ErrLimit instead. The control,
// two lists of 300,000 elements, builds, so a limit cut to a tenth of itself fails the control. The
// hostile error is the plain cost stop, `<site>: more than 1000000`, and not one of the size texts.
func TestHostileExpr_CostOfOneHole(t *testing.T) {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "cel.dev/cel-go" {
				t.Logf("cel.dev/cel-go %s", dep.Version)
			}
		}
	}
	lists := func(n int) string {
		return fmt.Sprintf("string(lists.range(%d).size() + lists.range(%d).size())", n, n)
	}
	exprBuilds(t, lists(300000), "two lists of 300,000 elements")
	exprCostError(t, lists(1000000), "properties.data.k: more than 1000000")
}

// Row 2: the size of one result. An input of 500,000 bytes is read three times, once as the text of a
// template and once in a single hole. Each costs a few units, far under the cost limit, so only the
// size of the result can stop it, and it stops on ErrCostLimit with the text of its own limit: the
// size of a template text, and the charge of a value in a single hole. The same input read once
// builds, and so does a template of two holes under the limit, so a check that refuses every
// template with a hole fails the control.
func TestHostileExpr_SizeOfOneResult(t *testing.T) {
	blob := func(field string) string {
		return modelHeader[:strings.Index(modelHeader, "  parts:")] +
			"  inputs:\n    blob:\n      type: string\n      required: true\n  parts:\n" + `
    - id: cfg
      type: configmap@v1
      properties:
        data:
          k: "` + field + `"
`
	}
	build := func(field string, bytes int) error {
		tr, err := exprRegister(t, blob(field))
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		comps := []oam.Component{{Name: "orders", Type: webappType, Properties: map[string]any{"blob": strings.Repeat("x", bytes)}}}
		_, err = objects(t, tr, comps, nil)
		return err
	}
	if err := build("${ inputs.blob }", 500000); err != nil {
		t.Fatalf("one read of 500,000 bytes: %v", err)
	}
	if err := build("${ inputs.blob }${ inputs.blob }", 20000); err != nil {
		t.Fatalf("a template of two holes and 40,000 bytes: %v", err)
	}
	for _, tc := range []struct{ field, want string }{
		{"${ inputs.blob }${ inputs.blob }${ inputs.blob }", "the text holds more than 1000000 bytes"},
		{"${ inputs.blob + inputs.blob + inputs.blob }", "a value holds more than 1000000 bytes or elements"},
	} {
		err := build(tc.field, 500000)
		if err == nil {
			t.Fatalf("%s built a result of 1,500,000 bytes", tc.field)
		}
		if !errors.Is(err, model.ErrCostLimit) {
			t.Errorf("%s: error %q does not wrap ErrCostLimit", tc.field, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not say %q", tc.field, err, tc.want)
		}
	}
}

// Row 2b: the Go copy of one result holds at most 1,000,000 elements or bytes. One value of 10,000
// bytes that 150 places share costs a few thousand units and copies to 1,500,000 bytes, so only the
// copy limit can stop it. A list of elements would do as well, but its copy takes 1.5 s under -race
// and coverage, longer than the deadline of a build. The same value shared by 11 places (110,000 bytes) builds, so a copy limit cut to a tenth of itself fails the control. The control is not nearer the limit because the build of a result of 500,000 bytes takes 1.8 s under -race and coverage, in the emission after the rule.
func TestHostileExpr_CopyOfOneResult(t *testing.T) {
	build := func(places int) error {
		tr, err := exprRegister(t, modelHeader+fmt.Sprintf(`
    - id: raw
      name: raw
      type: passthrough@v1
      properties:
        object:
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: raw
          extra: "${ cel.bind(b, bytes(lists.range(1000).map(i, 'xxxxxxxxxx').join('')), lists.range(%d).map(i, b)) }"
`, places))
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		_, err = objects(t, tr, orders(), nil)
		return err
	}
	if err := build(11); err != nil {
		t.Fatalf("a result of 110,000 bytes: %v", err)
	}
	err := build(150)
	if err == nil {
		t.Fatal("a result of 1,500,000 bytes built")
	}
	if !errors.Is(err, model.ErrCostLimit) {
		t.Errorf("error %q does not wrap ErrCostLimit", err)
	}
	if !strings.Contains(err.Error(), "the result holds more than 1000000 elements") {
		t.Errorf("error %q does not name the result size limit", err)
	}
}

// Row 3: cel.bind nests at most 8 deep, counting the binds that the sortBy macro makes. Register
// refuses a model that nests 9, and a model that nests 8 registers and builds.
func TestHostileExpr_BindDepth(t *testing.T) {
	bindNest := func(n int) string {
		expr := "1"
		for i := 1; i <= n; i++ {
			expr = fmt.Sprintf("cel.bind(v%d, 1, %s)", i, expr)
		}
		return "string(" + expr + ")"
	}
	sortBys := func(n int) string {
		return "string([1]" + strings.Repeat(".sortBy(x, x)", n) + ".size())"
	}
	exprBuilds(t, bindNest(8), "8 nested binds")
	exprRefused(t, bindNest(9), "nesting")
	exprBuilds(t, sortBys(8), "8 chained sortBy calls")
	exprRefused(t, sortBys(9), "nesting")
}

// Row 4: the text of one expression has at most 4096 code points and at most 64 levels of recursion.
// The node count limit of 10,000 cannot be reached under the size limit, so no row builds one.
func TestHostileExpr_SizeAndRecursionOfTheText(t *testing.T) {
	literal := func(n int) string { return "'" + strings.Repeat("a", n-2) + "'" }
	parens := func(n int) string { return strings.Repeat("(", n) + "'a'" + strings.Repeat(")", n) }
	exprBuilds(t, literal(4096), "an expression of 4096 code points")
	exprRefused(t, literal(4097), "size")
	// The parser counts depth its own way, so the row pins a depth far above the limit and does not
	// guess the exact boundary, as the unit test of the compiler does.
	exprBuilds(t, parens(8), "8 levels of parentheses")
	exprRefused(t, parens(200), "recursion")
}
