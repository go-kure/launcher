package oam

import "testing"

// applicationRule records the application a LoweringContext names, in the query for its outputs
// and in the lowering of a build.
type applicationRule struct {
	asked, lowered *string
}

func (applicationRule) ComponentType() string { return "app-aware" }

func (r applicationRule) Outputs(_ *Component, lctx LoweringContext) ([]OutputDecl, error) {
	*r.asked = lctx.Application()
	return nil, nil
}

func (r applicationRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.lowered = lctx.Application()
	return LoweringResult{Components: []Component{{Name: c.Name + "-inner", Type: "cfg", Properties: map[string]any{}}}}, nil
}

// TestLoweringContext_ApplicationIsTheOneTheNamingHookIsAsked: a rule that names its parts after
// the application reads the name a Naming hook is asked (NameRequest.Application), whether it is
// asked for its outputs under an application the caller names or lowers a document of a build.
func TestLoweringContext_ApplicationIsTheOneTheNamingHookIsAsked(t *testing.T) {
	var asked, lowered string
	tr := NewTransformer(map[string]ComponentHandler{"cfg": &objectOutputHandler{typ: "cfg"}}, nil)
	tr.RegisterComponentLowering(applicationRule{asked: &asked, lowered: &lowered})

	if _, err := tr.ComponentOutputsNamed("shop", &Component{Name: "x", Type: "app-aware"}, nil); err != nil {
		t.Fatalf("ComponentOutputsNamed: %v", err)
	}
	if asked != "shop" {
		t.Errorf("the query named the application %q, want shop", asked)
	}

	app := terminalApp(Component{Name: "pg", Type: "app-aware"})
	app.Metadata.Name = "orders"
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if lowered != "orders" {
		t.Errorf("the lowering named the application %q, want orders", lowered)
	}
}
