package oam

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- go-kure/launcher#611: rule-emitted traits and PlatformReserved ---------------

// reservedTraitHandler is a dispatchable trait handler whose schema reserves
// networkPolicy, so applyTraits' D3 check has something to enforce on a trait.
type reservedTraitHandler struct{}

func (reservedTraitHandler) CanHandle(t string) bool { return t == "reserved-trait" }

func (reservedTraitHandler) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }

func (reservedTraitHandler) PropertySchema() map[string]PropertySchema {
	return reservedSinkHandler{}.PropertySchema()
}

func reservedTraitTransformer() *Transformer {
	tr := reservedSinkTransformer()
	tr.RegisterTrait("reserved-trait", reservedTraitHandler{})
	return tr
}

// traitEmittingComponentRule declares no schema and copies its authored properties
// into a reserved-trait on the component it emits. That trait is sealed, but
// nothing checked what it carries.
type traitEmittingComponentRule struct{}

func (traitEmittingComponentRule) ComponentType() string { return "trait-emitting" }

func (traitEmittingComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "web",
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: "reserved-trait", Properties: comp.Properties}},
	}}}, nil
}

// emitsRenderedTrait builds the reserved-sink component the rules below emit: it
// carries a trait of type traitType whose reserved value comes from the capability
// rendering, not from anything a user wrote. It is named "main", so a trait rule
// that emits a "web" component next to it does not collide.
func emitsRenderedTrait(traitType string, lctx LoweringContext) LoweringResult {
	return LoweringResult{Components: []Component{{
		Name:       "main",
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: traitType, Properties: map[string]any{"image": "envoy", "networkPolicy": renderedNetworkPolicy(lctx)}}},
	}}}
}

// rendersTraitComponentRule declares a schema and emits a trait of type traitType
// whose reserved value it rendered itself. Its input was checked, so that trait is
// synthesized.
type rendersTraitComponentRule struct{ traitType string }

func (rendersTraitComponentRule) ComponentType() string { return "renders-trait" }

func (rendersTraitComponentRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (r rendersTraitComponentRule) LowerComponent(_ *Component, lctx LoweringContext) (LoweringResult, error) {
	return emitsRenderedTrait(r.traitType, lctx), nil
}

// schemalessRendersTraitComponentRule is rendersTraitComponentRule without a schema:
// nothing checked its input, so the trait it emits is not synthesized, even though
// the reserved value in it is one the rule rendered.
type schemalessRendersTraitComponentRule struct{}

func (schemalessRendersTraitComponentRule) ComponentType() string { return "renders-trait" }

func (schemalessRendersTraitComponentRule) LowerComponent(_ *Component, lctx LoweringContext) (LoweringResult, error) {
	return emitsRenderedTrait("reserved-trait", lctx), nil
}

// traitEmittingTraitRule declares no schema and copies the properties of the trait it
// lowers into a reserved-trait it emits.
type traitEmittingTraitRule struct{}

func (traitEmittingTraitRule) TraitType() string { return "trait-wrapper" }

func (traitEmittingTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "reserved-trait", Properties: trait.Properties}}}, nil
}

// rendersTraitTraitRule declares a schema and emits a reserved-trait whose reserved
// value it rendered itself.
type rendersTraitTraitRule struct{}

func (rendersTraitTraitRule) TraitType() string { return "renders-trait-wrapper" }

func (rendersTraitTraitRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (rendersTraitTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "reserved-trait", Properties: map[string]any{"networkPolicy": renderedNetworkPolicy(lctx)}}}}, nil
}

// rendersNestedTraitTraitRule declares a schema and emits a component carrying a
// reserved-trait whose reserved value it rendered itself.
type rendersNestedTraitTraitRule struct{}

func (rendersNestedTraitTraitRule) TraitType() string { return "renders-nested-trait" }

func (rendersNestedTraitTraitRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (rendersNestedTraitTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	return emitsRenderedTrait("reserved-trait", lctx), nil
}

// sidecarEmittingComponentRule declares no schema and emits a component carrying an
// empty trait of type traitType: the trait holds no reserved value, but it is
// sealed and not synthesized.
type sidecarEmittingComponentRule struct{ traitType string }

func (sidecarEmittingComponentRule) ComponentType() string { return "sidecar-emitting" }

func (r sidecarEmittingComponentRule) LowerComponent(_ *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "main",
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: r.traitType, Properties: map[string]any{}}},
	}}}, nil
}

// rendersTraitDocRule builds a component carrying a reserved-trait whose reserved
// value it rendered itself. A document rule's output stays authored, traits
// included.
type rendersTraitDocRule struct{}

func (rendersTraitDocRule) Kind() string { return "TraitRendering" }

func (rendersTraitDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	emitted := emitsRenderedTrait("reserved-trait", lctx)
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: emitted.Components},
	}}}, nil
}

// expectReservedTraitRejected asserts err is a platform-reserved refusal naming the
// trait and component at the site that is expected to reject it.
func expectReservedTraitRejected(t *testing.T, err error, site string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a platform-reserved trait property to be rejected")
	}
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, site) || !strings.Contains(msg, "networkPolicy") {
		t.Errorf("expected the error to name %s and the property, got: %v", site, msg)
	}
}

// TestTransform_SchemalessComponentRuleTraitCopyIsRejected is go-kure/launcher#611:
// an authored reserved value a schema-less component rule copied into a trait it
// emits is sealed but not synthesized. It is rejected as the rule emits it
// (go-kure/launcher#626), before applyTraits would have.
func TestTransform_SchemalessComponentRuleTraitCopyIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(traitEmittingComponentRule{})

	_, err := tr.Transform(singleComponentApp("Application", "trait-emitting", authoredNetworkPolicy()), TransformContext{})
	expectReservedTraitRejected(t, err, `trait "reserved-trait" of component "web" emitted by rule component/trait-emitting:`)
}

// TestTransform_SchemalessComponentRuleRenderedTraitIsRejected: fail-closed. A
// schema-less rule's emitted trait is not synthesized even when the reserved value
// in it is one the rule rendered, since nothing proves it was not copied.
func TestTransform_SchemalessComponentRuleRenderedTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(schemalessRendersTraitComponentRule{})

	_, err := tr.Transform(singleComponentApp("Application", "renders-trait", map[string]any{}), netpolCapability())
	expectReservedTraitRejected(t, err, `trait "reserved-trait" of component "main" emitted by rule component/renders-trait:`)
}

// TestTransform_ComponentRuleMayWriteReservedTraitProperty: a schema-declaring
// component rule's emitted trait is synthesized, so a reserved value it rendered is
// accepted at applyTraits.
func TestTransform_ComponentRuleMayWriteReservedTraitProperty(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(rendersTraitComponentRule{traitType: "reserved-trait"})

	if _, err := tr.Transform(singleComponentApp("Application", "renders-trait", map[string]any{}), netpolCapability()); err != nil {
		t.Fatalf("a rule-written reserved trait property must be accepted, got: %v", err)
	}
}

// TestTransform_SchemalessTraitRuleTraitCopyIsRejected is the trait-position case: a
// schema-less trait rule copies an authored trait's reserved value into the trait
// it emits, which is rejected as emitted.
func TestTransform_SchemalessTraitRuleTraitCopyIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "trait-wrapper", Properties: authoredNetworkPolicy()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedTraitRejected(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// TestTransform_TraitRuleMayWriteReservedTraitProperty: a schema-declaring trait rule
// over an authored trait emits a synthesized trait.
func TestTransform_TraitRuleMayWriteReservedTraitProperty(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(rendersTraitTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "renders-trait-wrapper", Properties: map[string]any{}}}
	if _, err := tr.Transform(app, netpolCapability()); err != nil {
		t.Fatalf("a rule-written reserved trait property must be accepted, got: %v", err)
	}
}

// TestTransform_TraitRuleMayWriteReservedNestedTraitProperty: a trait a
// schema-declaring trait rule nests in a component it emits is synthesized too.
func TestTransform_TraitRuleMayWriteReservedNestedTraitProperty(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(rendersNestedTraitTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "renders-nested-trait", Properties: map[string]any{}}}
	if _, err := tr.Transform(app, netpolCapability()); err != nil {
		t.Fatalf("a rule-written reserved trait property must be accepted, got: %v", err)
	}
}

// TestTransform_SynthesizedTraitStaysSynthesizedThroughSchemalessTraitRule: a trait
// rule's input that is itself synthesized was checked, so the rule's output is
// synthesized whether or not it declares a schema.
func TestTransform_SynthesizedTraitStaysSynthesizedThroughSchemalessTraitRule(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(rendersTraitComponentRule{traitType: "trait-wrapper"})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	if _, err := tr.Transform(singleComponentApp("Application", "renders-trait", map[string]any{}), netpolCapability()); err != nil {
		t.Fatalf("a reserved value carried from a synthesized trait must be accepted, got: %v", err)
	}
}

// TestTransform_TraitRuleOverSynthesizedTraitEmitsSynthesizedComponent: a trait rule
// over a sealed and synthesized trait emits synthesized components (provenance holds
// through the chain), so a reserved value that arrived from a checked rule is
// accepted at createApplications.
func TestTransform_TraitRuleOverSynthesizedTraitEmitsSynthesizedComponent(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(rendersTraitComponentRule{traitType: "pass-through-sidecar"})
	tr.RegisterTraitLowering(schemaPassThroughTraitRule{})

	if _, err := tr.Transform(singleComponentApp("Application", "renders-trait", map[string]any{}), netpolCapability()); err != nil {
		t.Fatalf("a reserved value carried from a synthesized trait must be accepted, got: %v", err)
	}
}

// TestTransform_TraitRuleOverUnsynthesizedSealedTraitEmitsAuthoredComponent: the
// sealed trait carries no reserved value, so the check before the schema-declaring
// rule passes, but that check covers only the trait's own reserved keys. The rule's
// output is not synthesized, and the reserved value it wrote is rejected at
// createApplications.
func TestTransform_TraitRuleOverUnsynthesizedSealedTraitEmitsAuthoredComponent(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(sidecarEmittingComponentRule{traitType: "renders-reserved-sidecar"})
	tr.RegisterTraitLowering(rendersReservedTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "sidecar-emitting", map[string]any{}), netpolCapability())
	expectReservedTraitRejected(t, err, `component "sidecar"`)
}

// TestTransform_DocumentRuleRenderedTraitIsRejected: a trait a document rule builds is
// never synthesized, so a reserved value it rendered is rejected as emitted.
func TestTransform_DocumentRuleRenderedTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterDocumentLowering(rendersTraitDocRule{})

	app := &Application{APIVersion: SupportedAPIVersion, Kind: "TraitRendering", Metadata: Metadata{Name: "myapp", Namespace: "test"}}
	_, err := tr.Transform(app, netpolCapability())
	expectReservedTraitRejected(t, err, `trait "reserved-trait" of component "main" emitted by rule document/TraitRendering:`)
}
