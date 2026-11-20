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

type runStateKey struct{}

// runStateProbe keeps what RunState hands it: the inner rule in the query for its outputs, the
// outer rule in its lowering, where it asks the Transformer about the inner component.
type runStateProbe struct {
	tr         *Transformer
	asked, own *any
}

func (runStateProbe) ComponentType() string { return "outer" }

func (r runStateProbe) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.own = lctx.RunState(runStateKey{}, func() any { return new(int) })
	if _, err := r.tr.ComponentOutputsWithin(lctx, &Component{Name: "q", Type: "inner"}, nil); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: c.Name + "-cfg", Type: "cfg", Properties: map[string]any{}}}}, nil
}

type runStateInner struct{ asked *any }

func (runStateInner) ComponentType() string { return "inner" }

func (r runStateInner) LowerComponent(*Component, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}

func (r runStateInner) Outputs(_ *Component, lctx LoweringContext) ([]OutputDecl, error) {
	*r.asked = lctx.RunState(runStateKey{}, func() any { return new(int) })
	return nil, nil
}

// TestComponentOutputsWithin_SharesTheRunOfTheBuild: what a rule keeps for the whole build
// (LoweringContext.RunState) is the same value in the query a rule makes about a component it
// emits, so a chain of rules that each ask about the next spends from one budget.
func TestComponentOutputsWithin_SharesTheRunOfTheBuild(t *testing.T) {
	var asked, own any
	tr := NewTransformer(map[string]ComponentHandler{"cfg": &objectOutputHandler{typ: "cfg"}}, nil)
	tr.RegisterComponentLowering(runStateProbe{tr: tr, asked: &asked, own: &own})
	tr.RegisterComponentLowering(runStateInner{asked: &asked})

	if _, err := tr.Transform(terminalApp(Component{Name: "pg", Type: "outer"}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if own == nil || asked == nil {
		t.Fatalf("the probe saw the run state %v and the query saw %v: a rule did not run", own, asked)
	}
	if own != asked {
		t.Errorf("the query got its own run state (%p), want the one of the build (%p)", asked, own)
	}
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
