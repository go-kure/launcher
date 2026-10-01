package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// countingEngineTrait is an engine-only trait handler that counts its Apply calls.
type countingEngineTrait struct{ applied *int }

func (countingEngineTrait) CanHandle(t string) bool { return t == "engine-trait" }

func (h countingEngineTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error {
	*h.applied++
	return nil
}

func (countingEngineTrait) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

// engineTraitAttachingRule declares a schema, so the engine-trait it attaches to
// the component it emits is synthesized.
type engineTraitAttachingRule struct{}

func (engineTraitAttachingRule) ComponentType() string { return "attaches-engine-trait" }

func (engineTraitAttachingRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (engineTraitAttachingRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       comp.Name,
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "nginx"},
		Traits:     append([]Trait{{Type: "engine-trait", Properties: map[string]any{}}}, comp.Traits...),
	}}}, nil
}

// schemalessEngineTraitAttachingRule is engineTraitAttachingRule without a
// schema: nothing checked its input, so the trait it attaches is not synthesized.
type schemalessEngineTraitAttachingRule struct{}

func (schemalessEngineTraitAttachingRule) ComponentType() string { return "attaches-engine-trait" }

func (schemalessEngineTraitAttachingRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return engineTraitAttachingRule{}.LowerComponent(comp, lctx)
}

func engineTraitTransformer(applied *int) *Transformer {
	tr := reservedSinkTransformer()
	tr.RegisterEngineTrait("engine-trait", countingEngineTrait{applied: applied})
	return tr
}

func expectEngineOnly(t *testing.T, err error) {
	t.Helper()
	const want = `trait type "engine-trait" is engine-only: a lowering rule attaches it, and a document may not author it`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one containing %q", err, want)
	}
}

// A trait a schema-declaring rule attached is dispatched: the synthesized flag
// survives every copy between the rule's emission and applyTraits.
func TestEngineTrait_SynthesizedTraitIsDispatched(t *testing.T) {
	applied := 0
	tr := engineTraitTransformer(&applied)
	tr.RegisterComponentLowering(engineTraitAttachingRule{})
	if _, err := tr.Transform(singleComponentApp("Application", "attaches-engine-trait", map[string]any{}), TransformContext{}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if applied != 1 {
		t.Errorf("engine trait applied %d times, want 1", applied)
	}
}

// An authored engine-only trait is refused at dispatch, on a terminal component
// with no lowering rule involved.
func TestEngineTrait_AuthoredIsRefusedAtDispatch(t *testing.T) {
	applied := 0
	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "engine-trait", Properties: map[string]any{}}}
	_, err := engineTraitTransformer(&applied).Transform(app, TransformContext{})
	expectEngineOnly(t, err)
	if applied != 0 {
		t.Errorf("engine trait applied %d times, want 0", applied)
	}
}

// An authored engine-only trait on a component a rule lowers is refused too, and
// the rule's own attachment of the same type does not make it acceptable.
func TestEngineTrait_AuthoredBesideASynthesizedOneIsRefused(t *testing.T) {
	applied := 0
	tr := engineTraitTransformer(&applied)
	tr.RegisterComponentLowering(engineTraitAttachingRule{})
	app := singleComponentApp("Application", "attaches-engine-trait", map[string]any{})
	app.Spec.Components[0].Traits = []Trait{{Type: "engine-trait", Properties: map[string]any{}}}
	_, err := tr.Transform(app, TransformContext{})
	expectEngineOnly(t, err)
}

// ValidateAuthoredProperties refuses it before any transform.
func TestEngineTrait_AuthoredIsRefusedByAuthoredValidation(t *testing.T) {
	applied := 0
	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "engine-trait", Properties: map[string]any{}}}
	expectEngineOnly(t, engineTraitTransformer(&applied).ValidateAuthoredProperties(app))
}

// A rule whose input nothing checked cannot attach one: its trait is not
// synthesized.
func TestEngineTrait_UnsynthesizedRuleEmissionIsRefused(t *testing.T) {
	applied := 0
	tr := engineTraitTransformer(&applied)
	tr.RegisterComponentLowering(schemalessEngineTraitAttachingRule{})
	_, err := tr.Transform(singleComponentApp("Application", "attaches-engine-trait", map[string]any{}), TransformContext{})
	expectEngineOnly(t, err)
}

// An engine-only trait is not published as something to author.
func TestEngineTrait_IsNotPublished(t *testing.T) {
	applied := 0
	tr := engineTraitTransformer(&applied)
	if _, ok := tr.HandlerSchemas().Traits["engine-trait"]; ok {
		t.Error("HandlerSchemas() publishes the engine-only trait")
	}
	if _, ok := tr.HandlerContracts().Traits["engine-trait"]; ok {
		t.Error("HandlerContracts() lists the engine-only trait")
	}
}
