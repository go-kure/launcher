package oam

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// A component rule that hand-writes a trait is sealed by the engine when its input
// counted as checked, so the trait skips capability binding, the missing-binding
// refusal, the platform-reserved refusal (D3) and the consumption record
// (go-kure/launcher#1562). A rule that declares EmitsAuthoredTraits writes traits
// that are authored in kind (a model's traits), so they travel unsealed and take
// every one of those steps. These tests pin each skipped step, each with an authored
// control that must behave the same, and the sealed case that must not change.

// schemaEmitRule declares a property schema, so the engine counts its input as
// checked, and emits one part carrying a trait it wrote itself.
type schemaEmitRule struct {
	typ   string
	trait string
	props map[string]any
}

func (r schemaEmitRule) ComponentType() string { return r.typ }
func (schemaEmitRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (r schemaEmitRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	props := map[string]any{}
	for k, v := range r.props {
		props[k] = v
	}
	part := Component{
		Name:       c.Name,
		Type:       "part",
		Properties: map[string]any{},
		Traits:     []Trait{{Type: r.trait, Properties: props}},
	}
	return LoweringResult{Components: []Component{part}}, nil
}

// authoredEmitRule is the same rule marked as writing authored traits.
type authoredEmitRule struct{ schemaEmitRule }

func (authoredEmitRule) EmitsAuthoredTraits() bool { return true }

// bindingHandler is a capability-aware trait handler that records the properties
// its Apply received, and declares one platform-reserved property.
type bindingHandler struct {
	typ string
	got *map[string]any
}

func (h bindingHandler) CanHandle(t string) bool { return t == h.typ }
func (bindingHandler) CapabilityRequired() bool  { return true }
func (bindingHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}
func (bindingHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"class": {Type: PropertyTypeString, PlatformReserved: true},
		"host":  {Type: PropertyTypeString},
	}
}

func (h bindingHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.got = trait.Properties
	return nil
}

var exposeCapabilities = map[string]CapabilityBinding{
	"expose": {Rendering: map[string]any{"class": "platform"}},
}

// runAuthoredTraitCase transforms one component "web" through a rule registered
// as a non-builtin component rule, or, when authored is true, authors the same
// trait on a plain part component.
func runAuthoredTraitCase(t *testing.T, rule ComponentLoweringRule, authoredTrait *Trait, caps map[string]CapabilityBinding) (map[string]any, *PolicyResult, error) {
	t.Helper()
	var got map[string]any
	tr := NewTransformer(
		map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
		map[string]TraitHandler{"expose": bindingHandler{typ: "expose", got: &got}},
	)
	comp := Component{Name: "web", Type: "part", Properties: map[string]any{}}
	if rule != nil {
		tr.RegisterComponentLowering(rule)
		comp.Type = rule.ComponentType()
	}
	if authoredTrait != nil {
		comp.Traits = []Trait{*authoredTrait}
	}
	_, result, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{Capabilities: caps})
	return got, result, err
}

func exposeTrait(props map[string]any) *Trait {
	return &Trait{Type: "expose", Properties: props}
}

// T1: the capability rendering is merged into the trait.
func TestAuthoredEmit_CapabilityRenderingIsMerged(t *testing.T) {
	control, _, err := runAuthoredTraitCase(t, nil, exposeTrait(map[string]any{"host": "a.example"}), exposeCapabilities)
	if err != nil {
		t.Fatalf("authored control: %v", err)
	}
	want := map[string]any{"class": "platform", "host": "a.example"}
	if !reflect.DeepEqual(control, want) {
		t.Fatalf("authored control got %v, want %v", control, want)
	}

	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "expose", props: map[string]any{"host": "a.example"}}}
	got, _, err := runAuthoredTraitCase(t, rule, nil, exposeCapabilities)
	if err != nil {
		t.Fatalf("authored-emitting rule: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted trait got %v, want the capability rendering merged: %v", got, want)
	}
}

// T1b: a capability-aware trait with no binding is refused, naming component and
// trait, once.
func TestAuthoredEmit_MissingBindingIsRefused(t *testing.T) {
	_, _, err := runAuthoredTraitCase(t, nil, exposeTrait(map[string]any{}), nil)
	if !errors.Is(err, ErrMissingCapability) {
		t.Fatalf("authored control: want ErrMissingCapability, got %v", err)
	}

	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "expose"}}
	_, _, err = runAuthoredTraitCase(t, rule, nil, nil)
	if !errors.Is(err, ErrMissingCapability) {
		t.Fatalf("emitted trait: want ErrMissingCapability, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `component "web"`) || !strings.Contains(msg, `trait "expose"`) {
		t.Errorf("refusal must name the component and the trait, got: %s", msg)
	}
}

// T2: the consumed capability key is recorded.
func TestAuthoredEmit_ConsumptionIsRecorded(t *testing.T) {
	_, control, err := runAuthoredTraitCase(t, nil, exposeTrait(map[string]any{}), exposeCapabilities)
	if err != nil {
		t.Fatalf("authored control: %v", err)
	}
	if want := []string{"expose"}; !reflect.DeepEqual(control.ConsumedCapabilities, want) {
		t.Fatalf("authored control consumed %v, want %v", control.ConsumedCapabilities, want)
	}

	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "expose"}}
	_, got, err := runAuthoredTraitCase(t, rule, nil, exposeCapabilities)
	if err != nil {
		t.Fatalf("emitted trait: %v", err)
	}
	if !reflect.DeepEqual(got.ConsumedCapabilities, control.ConsumedCapabilities) {
		t.Fatalf("emitted trait consumed %v, want the authored control's %v", got.ConsumedCapabilities, control.ConsumedCapabilities)
	}
}

// T3: a value for a platform-reserved property is refused (D3), once.
func TestAuthoredEmit_ReservedPropertyIsRefused(t *testing.T) {
	props := map[string]any{"class": "evil"}
	_, _, err := runAuthoredTraitCase(t, nil, exposeTrait(props), exposeCapabilities)
	if !errors.Is(err, ErrPlatformReserved) {
		t.Fatalf("authored control: want ErrPlatformReserved, got %v", err)
	}

	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "expose", props: props}}
	_, _, err = runAuthoredTraitCase(t, rule, nil, exposeCapabilities)
	if !errors.Is(err, ErrPlatformReserved) {
		t.Fatalf("emitted trait: want ErrPlatformReserved, got %v", err)
	}
	if n := strings.Count(err.Error(), `trait "expose"`); n != 1 {
		t.Errorf("the refusal must name the trait once, named it %d times: %v", n, err)
	}
	if !strings.Contains(err.Error(), `component "web"`) {
		t.Errorf("refusal must name the component, got: %v", err)
	}
}

// A rule without the marker keeps sealing what it writes: no binding is merged,
// no missing-binding refusal, nothing consumed. The same trait that the three
// tests above refuse passes, so the marker is what changes behaviour.
func TestAuthoredEmit_UnmarkedRuleStaysSealed(t *testing.T) {
	rule := schemaEmitRule{typ: "bundle", trait: "expose", props: map[string]any{"class": "kept"}}

	got, result, err := runAuthoredTraitCase(t, rule, nil, exposeCapabilities)
	if err != nil {
		t.Fatalf("unmarked rule with a reserved value in its own trait: %v", err)
	}
	if want := map[string]any{"class": "kept"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sealed trait got %v, want it untouched %v", got, want)
	}
	if len(result.ConsumedCapabilities) != 0 {
		t.Errorf("sealed trait consumed %v, want nothing", result.ConsumedCapabilities)
	}

	if _, _, err := runAuthoredTraitCase(t, rule, nil, nil); err != nil {
		t.Errorf("sealed trait with no binding must not be refused: %v", err)
	}
}

// authoredFnRule is provenance_test.go's emitRule marked as writing authored traits.
type authoredFnRule struct{ emitRule }

func (authoredFnRule) EmitsAuthoredTraits() bool { return true }

// A explicit null for a platform-reserved property is refused for a rule's own trait
// as it is for an authored one: emission validation drops a null, so the check must
// run before it.
func TestAuthoredEmit_ReservedNullIsRefused(t *testing.T) {
	props := map[string]any{"class": nil}
	_, _, err := runAuthoredTraitCase(t, nil, exposeTrait(props), exposeCapabilities)
	if !errors.Is(err, ErrPlatformReserved) {
		t.Fatalf("authored control: want ErrPlatformReserved, got %v", err)
	}

	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "expose", props: props}}
	_, _, err = runAuthoredTraitCase(t, rule, nil, exposeCapabilities)
	if !errors.Is(err, ErrPlatformReserved) {
		t.Fatalf("emitted trait: want ErrPlatformReserved, got %v", err)
	}
}

// requiredHandler declares one required property and no reserved one, so a binding
// can supply what the rule leaves out.
type requiredHandler struct {
	got *map[string]any
	// aware makes the trait capability-aware, so a missing binding is refused on its own.
	aware bool
}

func (requiredHandler) CanHandle(t string) bool    { return t == "secure" }
func (h requiredHandler) CapabilityRequired() bool { return h.aware }
func (requiredHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}
func (requiredHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"level": {Type: PropertyTypeString, Required: true}}
}

func (h requiredHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.got = trait.Properties
	return nil
}

func runRequiredCase(t *testing.T, rule ComponentLoweringRule, authored bool, caps map[string]CapabilityBinding) (map[string]any, error) {
	t.Helper()
	var got map[string]any
	tr := NewTransformer(
		map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
		map[string]TraitHandler{"secure": requiredHandler{got: &got}},
	)
	comp := Component{Name: "web", Type: "part", Properties: map[string]any{}}
	if authored {
		comp.Traits = []Trait{{Type: "secure", Properties: map[string]any{}}}
	} else {
		tr.RegisterComponentLowering(rule)
		comp.Type = rule.ComponentType()
	}
	_, _, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{Capabilities: caps})
	return got, err
}

// A required key the capability binding supplies is not refused at emission, as it
// is not for an authored trait; a key nobody supplies still is.
func TestAuthoredEmit_RequiredKeyTheBindingSuppliesIsAccepted(t *testing.T) {
	caps := map[string]CapabilityBinding{"secure": {Rendering: map[string]any{"level": "restricted"}}}
	want := map[string]any{"level": "restricted"}
	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "secure"}}

	control, err := runRequiredCase(t, nil, true, caps)
	if err != nil || !reflect.DeepEqual(control, want) {
		t.Fatalf("authored control: got %v, %v; want %v", control, err, want)
	}
	got, err := runRequiredCase(t, rule, false, caps)
	if err != nil {
		t.Fatalf("emitted trait with the key supplied by the binding: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted trait got %v, want %v", got, want)
	}

	if _, err := runRequiredCase(t, nil, true, nil); err == nil || !strings.Contains(err.Error(), "level") {
		t.Fatalf("authored control with no binding: want a refusal naming level, got %v", err)
	}
	if _, err := runRequiredCase(t, rule, false, nil); err == nil || !strings.Contains(err.Error(), "level") {
		t.Fatalf("emitted trait with no binding: want a refusal naming level, got %v", err)
	}
}

// groupHandler declares a required exclusive group of two keys, as a budget trait
// does: exactly one of them, which a binding may supply.
type groupHandler struct{ got *map[string]any }

func (groupHandler) CanHandle(t string) bool  { return t == "budget" }
func (groupHandler) CapabilityRequired() bool { return false }
func (groupHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}
func (groupHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"min": {Type: PropertyTypeInteger},
		"max": {Type: PropertyTypeInteger},
	}
}
func (groupHandler) ExclusiveProperties() []ExclusiveGroup {
	return []ExclusiveGroup{{Keys: []string{"min", "max"}, Required: true}}
}

func (h groupHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.got = trait.Properties
	return nil
}

// A member of a required exclusive group the binding supplies is not refused at
// emission, as it is not for an authored trait.
func TestAuthoredEmit_ExclusiveGroupMemberTheBindingSuppliesIsAccepted(t *testing.T) {
	caps := map[string]CapabilityBinding{"budget": {Rendering: map[string]any{"min": 1}}}
	run := func(rule ComponentLoweringRule) (map[string]any, error) {
		var got map[string]any
		tr := NewTransformer(
			map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
			map[string]TraitHandler{"budget": groupHandler{got: &got}},
		)
		comp := Component{Name: "web", Type: "part", Properties: map[string]any{}}
		if rule == nil {
			comp.Traits = []Trait{{Type: "budget", Properties: map[string]any{}}}
		} else {
			tr.RegisterComponentLowering(rule)
			comp.Type = rule.ComponentType()
		}
		_, _, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{Capabilities: caps})
		return got, err
	}

	want := map[string]any{"min": 1}
	control, err := run(nil)
	if err != nil || !reflect.DeepEqual(control, want) {
		t.Fatalf("authored control: got %v, %v; want %v", control, err, want)
	}
	got, err := run(authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "budget"}})
	if err != nil {
		t.Fatalf("emitted trait with a group member supplied by the binding: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted trait got %v, want %v", got, want)
	}
}

// unscopedHandler declares no `scope`, the engine-owned key that picks a scoped binding.
type unscopedHandler struct{ got *map[string]any }

func (unscopedHandler) CanHandle(t string) bool { return t == "stage" }
func (unscopedHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}
func (unscopedHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"x": {Type: PropertyTypeString}}
}

func (h unscopedHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.got = trait.Properties
	return nil
}

// The engine-owned `scope` key is accepted on a rule's own trait as on an authored
// one, and picks the scoped binding.
func TestAuthoredEmit_EngineOwnedScopeIsAccepted(t *testing.T) {
	caps := map[string]CapabilityBinding{"stage.internal": {Rendering: map[string]any{"x": "from-binding"}}}
	run := func(rule ComponentLoweringRule) (map[string]any, *PolicyResult, error) {
		var got map[string]any
		tr := NewTransformer(
			map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
			map[string]TraitHandler{"stage": unscopedHandler{got: &got}},
		)
		comp := Component{Name: "web", Type: "part", Properties: map[string]any{}}
		if rule == nil {
			comp.Traits = []Trait{{Type: "stage", Properties: map[string]any{"scope": "internal"}}}
		} else {
			tr.RegisterComponentLowering(rule)
			comp.Type = rule.ComponentType()
		}
		_, res, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{Capabilities: caps})
		return got, res, err
	}

	control, controlRes, err := run(nil)
	if err != nil {
		t.Fatalf("authored control: %v", err)
	}
	rule := authoredEmitRule{schemaEmitRule{typ: "bundle", trait: "stage", props: map[string]any{"scope": "internal"}}}
	got, res, err := run(rule)
	if err != nil {
		t.Fatalf("emitted trait with scope: %v", err)
	}
	if got["x"] != "from-binding" || !reflect.DeepEqual(got, control) {
		t.Fatalf("emitted trait got %v, want the authored control's %v", got, control)
	}
	if !reflect.DeepEqual(res.ConsumedCapabilities, controlRes.ConsumedCapabilities) || len(res.ConsumedCapabilities) != 1 {
		t.Fatalf("consumed %v, want the control's %v", res.ConsumedCapabilities, controlRes.ConsumedCapabilities)
	}
}

// midEmitRule is a marked rule whose part is lowered again by another component rule.
type midEmitRule struct{}

func (midEmitRule) ComponentType() string                     { return "bundle" }
func (midEmitRule) PropertySchema() map[string]PropertySchema { return map[string]PropertySchema{} }
func (midEmitRule) EmitsAuthoredTraits() bool                 { return true }
func (midEmitRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	mid := Component{Name: c.Name, Type: "mid", Properties: map[string]any{}, Traits: []Trait{{Type: "secure", Properties: map[string]any{}}}}
	return LoweringResult{Components: []Component{mid}}, nil
}

// dropTraitsRule consumes the traits of the component it lowers and returns none.
type dropTraitsRule struct{}

func (dropTraitsRule) ComponentType() string { return "mid" }
func (dropTraitsRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: c.Name, Type: "part", Properties: map[string]any{}}}}, nil
}

// A required key nobody supplies is refused at emission even when a later component
// rule consumes the trait, which leaves applyTraits and the lowering fixpoint nothing
// to check; and a binding that does supply it passes.
func TestAuthoredEmit_RequiredKeyIsCheckedBeforeAnotherRuleConsumesTheTrait(t *testing.T) {
	run := func(caps map[string]CapabilityBinding) error {
		var got map[string]any
		tr := NewTransformer(
			map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
			map[string]TraitHandler{"secure": requiredHandler{got: &got}},
		)
		tr.RegisterComponentLowering(midEmitRule{})
		tr.RegisterComponentLowering(dropTraitsRule{})
		comp := Component{Name: "web", Type: "bundle", Properties: map[string]any{}}
		_, _, err := tr.TransformWithPolicy(siblingDoc(comp), TransformContext{Capabilities: caps})
		return err
	}

	if err := run(nil); err == nil || !strings.Contains(err.Error(), "level") {
		t.Fatalf("no binding: want a refusal naming level, got %v", err)
	}
	caps := map[string]CapabilityBinding{"secure": {Rendering: map[string]any{"level": "restricted"}}}
	if err := run(caps); err != nil {
		t.Fatalf("binding supplies level: %v", err)
	}
}
