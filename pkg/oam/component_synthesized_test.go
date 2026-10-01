package oam

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- go-kure/launcher#429: rule-synthesized components and PlatformReserved -------

// reservedSinkHandler is a dispatchable component handler whose schema declares one
// platform-reserved property, so createApplications' D3 check has something to
// enforce at the terminal position.
type reservedSinkHandler struct{}

func (reservedSinkHandler) CanHandle(t string) bool { return t == "reserved-sink" }

func (reservedSinkHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}

func (reservedSinkHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"image":         {Type: PropertyTypeString, Description: "Authored freely."},
		"networkPolicy": {Type: PropertyTypeObject, PlatformReserved: true, AdditionalProperties: true, Description: "Platform-supplied."},
	}
}

// renderedNetworkPolicy is the reserved value a rule writes from the capability
// rendering it was handed.
func renderedNetworkPolicy(lctx LoweringContext) map[string]any {
	return lctx.Capabilities["netpol"].Rendering
}

func netpolCapability() TransformContext {
	return TransformContext{Capabilities: map[string]CapabilityBinding{
		"netpol": {Rendering: map[string]any{
			"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
		}},
	}}
}

// rendersReservedComponentRule lowers a "renders-reserved" component into the
// component type named by target, writing the reserved property itself from the
// capability rendering — the case createApplications' former KNOWN LIMITATION named.
// It declares a schema, so its authored input is checked before it runs and its
// output counts as synthesized.
type rendersReservedComponentRule struct{ target string }

func (rendersReservedComponentRule) ComponentType() string { return "renders-reserved" }

func (rendersReservedComponentRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (r rendersReservedComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       comp.Name,
		Type:       r.target,
		Properties: map[string]any{"image": "nginx", "networkPolicy": renderedNetworkPolicy(lctx)},
	}}}, nil
}

// rendersReservedDocRule is the same at document position: it constructs a new
// component and writes the reserved property from the capability rendering. Unlike
// the component and trait cases, its output stays authored.
type rendersReservedDocRule struct{}

func (rendersReservedDocRule) Kind() string { return "Rendering" }

func (rendersReservedDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "reserved-sink",
			Properties: map[string]any{"image": "nginx", "networkPolicy": renderedNetworkPolicy(lctx)},
		}}},
	}}}, nil
}

// traitCopyingDocRule builds a reserved-sink component from the authored properties
// of the first component's first trait, which nothing checks before a document rule.
type traitCopyingDocRule struct{}

func (traitCopyingDocRule) Kind() string { return "TraitCopying" }

func (traitCopyingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "reserved-sink",
			Properties: doc.Spec.Components[0].Traits[0].Properties,
		}}},
	}}}, nil
}

// rendersReservedTraitRule is the same at trait position: a trait rule may emit a
// whole component, here one carrying the reserved property. It declares a schema too.
type rendersReservedTraitRule struct{}

func (rendersReservedTraitRule) TraitType() string { return "renders-reserved-sidecar" }

func (rendersReservedTraitRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (rendersReservedTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "sidecar",
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "envoy", "networkPolicy": renderedNetworkPolicy(lctx)},
	}}}, nil
}

// passThroughComponentRule declares no schema and copies its authored properties
// into a reserved-sink component: nothing checked them before it ran, so its output
// is not synthesized.
type passThroughComponentRule struct{}

func (passThroughComponentRule) ComponentType() string { return "pass-through" }

func (passThroughComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "reserved-sink", Properties: comp.Properties}}}, nil
}

// passThroughTraitRule is the same at trait position: no schema, and it copies the
// authored trait properties into a component it emits.
type passThroughTraitRule struct{}

func (passThroughTraitRule) TraitType() string { return "pass-through-sidecar" }

func (passThroughTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: "web", Type: "reserved-sink", Properties: trait.Properties}}}, nil
}

// schemaPassThroughTraitRule declares a schema that reserves networkPolicy, and
// copies the trait properties into a component it emits. Over a sealed trait that
// is not synthesized, the schema is still checked before the rule runs.
type schemaPassThroughTraitRule struct{}

func (schemaPassThroughTraitRule) TraitType() string { return "pass-through-sidecar" }

func (schemaPassThroughTraitRule) PropertySchema() map[string]PropertySchema {
	return reservedSinkHandler{}.PropertySchema()
}

func (schemaPassThroughTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: "web", Type: "reserved-sink", Properties: trait.Properties}}}, nil
}

// traitWrappingComponentRule declares no schema and copies its authored properties
// into a pass-through-sidecar trait on the component it emits. That trait is sealed,
// as every rule-emitted trait is, though nothing checked what it carries.
type traitWrappingComponentRule struct{}

func (traitWrappingComponentRule) ComponentType() string { return "trait-wrapping" }

func (traitWrappingComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "main",
		Type:       "reserved-sink",
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: "pass-through-sidecar", Properties: comp.Properties}},
	}}}, nil
}

// retypingDocRule rebuilds the first component by value as a reserved-sink,
// copying its authored properties.
type retypingDocRule struct{}

func (retypingDocRule) Kind() string { return "Retyping" }

func (retypingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	comp := doc.Spec.Components[0]
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: []Component{{Name: comp.Name, Type: "reserved-sink", Properties: comp.Properties}}},
	}}}, nil
}

func reservedSinkTransformer() *Transformer {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("reserved-sink", reservedSinkHandler{})
	return tr
}

func singleComponentApp(kind, typ string, props map[string]any) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       kind,
		Metadata:   Metadata{Name: "myapp", Namespace: "test"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: typ, Properties: props}},
		},
	}
}

func authoredNetworkPolicy() map[string]any {
	return map[string]any{"image": "nginx", "networkPolicy": map[string]any{"trafficSources": []any{}}}
}

func expectPlatformReserved(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an authored platform-reserved component property to be rejected")
	}
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "networkPolicy") || !strings.Contains(msg, "web") {
		t.Errorf("expected the error to name the component and the property, got: %v", msg)
	}
}

// TestTransform_ComponentRuleMayWriteReservedProperty is the go-kure/launcher#429
// fix at createApplications: a reserved value a ComponentLoweringRule wrote is the
// rule's output, not authored input, and reaches the handler instead of being
// rejected as if a user had typed it.
func TestTransform_ComponentRuleMayWriteReservedProperty(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(rendersReservedComponentRule{target: "reserved-sink"})

	app := singleComponentApp("Application", "renders-reserved", map[string]any{})
	if _, err := tr.Transform(app, netpolCapability()); err != nil {
		t.Fatalf("a rule-written reserved property must be accepted, got: %v", err)
	}
}

// TestTransform_DocumentRuleWrittenReservedPropertyIsRejected: a document rule's
// output is never synthesized, since nothing checks its whole input (here an empty
// document) before it runs. A reserved value it wrote is rejected, as on main.
func TestTransform_DocumentRuleWrittenReservedPropertyIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterDocumentLowering(rendersReservedDocRule{})

	app := &Application{APIVersion: SupportedAPIVersion, Kind: "Rendering", Metadata: Metadata{Name: "myapp", Namespace: "test"}}
	_, err := tr.Transform(app, netpolCapability())
	expectPlatformReserved(t, err)
}

// TestTransform_DocumentRuleCopyingAuthoredTraitIsRejected: the input component has a
// schema and passes the pre-rule check, but the rule builds its output from an
// authored trait's properties. That output stays authored, so the reserved value is
// rejected at the handler.
func TestTransform_DocumentRuleCopyingAuthoredTraitIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterDocumentLowering(traitCopyingDocRule{})

	app := singleComponentApp("TraitCopying", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "anything", Properties: authoredNetworkPolicy()}}
	_, err := tr.Transform(app, TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_TraitRuleMayWriteReservedComponentProperty: a component a trait
// rule emitted is synthesized too.
func TestTransform_TraitRuleMayWriteReservedComponentProperty(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterTraitLowering(rendersReservedTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "renders-reserved-sidecar", Properties: map[string]any{}}}
	if _, err := tr.Transform(app, netpolCapability()); err != nil {
		t.Fatalf("a trait-rule-written reserved component property must be accepted, got: %v", err)
	}
}

// TestLower_SynthesizedComponentSkipsTheNextRulesReservedCheck covers the pre-rule
// check in lowerDocumentBody: a component an earlier rule synthesized, of a type that
// is itself claimed by a ComponentLoweringRule declaring the property reserved, is
// handed to that rule rather than rejected.
func TestLower_SynthesizedComponentSkipsTheNextRulesReservedCheck(t *testing.T) {
	next := &reservingComponentRule{}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(rendersReservedComponentRule{target: next.ComponentType()})
	tr.RegisterComponentLowering(next)

	app := singleComponentApp("Application", "renders-reserved", map[string]any{})
	if _, err := tr.lower(app, netpolCapability()); err != nil {
		t.Fatalf("a synthesized component must not be checked as authored by the next rule, got: %v", err)
	}
	if next.seen["networkPolicy"] == nil {
		t.Fatalf("expected the rule-written reserved value to reach the next rule, got: %#v", next.seen)
	}
}

// TestTransform_AuthoredReservedComponentPropertyStillRejected pins the unchanged
// half: with no rule involved, an authored reserved value fails in createApplications.
func TestTransform_AuthoredReservedComponentPropertyStillRejected(t *testing.T) {
	_, err := reservedSinkTransformer().Transform(singleComponentApp("Application", "reserved-sink", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_DocumentRuleForwardingAuthoredReservedIsRejected: an authored
// component a document rule forwards (the same element, isForwardedComponent) stays
// authored.
func TestTransform_DocumentRuleForwardingAuthoredReservedIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterDocumentLowering(forwardingComponentsDocRule{kind: "Wrapper"})

	_, err := tr.Transform(singleComponentApp("Wrapper", "reserved-sink", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_DocumentRuleCopyingAuthoredReservedIsRejected: a document rule that
// rebuilds the authored component by value emits a component that is not
// pointer-identical to its input. The authored reserved value is rejected before the
// rule runs (enforceAuthoredComponentReservations).
func TestTransform_DocumentRuleCopyingAuthoredReservedIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterDocumentLowering(forwardingDocRule{kind: "Wrapper"})

	_, err := tr.Transform(singleComponentApp("Wrapper", "reserved-sink", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestLower_DocumentRuleChecksAuthoredComponentAgainstItsLoweringRule: before a
// document rule, a component whose type a ComponentLoweringRule claims is checked
// against that rule's schema, the one lowerDocumentBody would use.
func TestLower_DocumentRuleChecksAuthoredComponentAgainstItsLoweringRule(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(&reservingComponentRule{})
	tr.RegisterDocumentLowering(forwardingDocRule{kind: "Wrapper"})

	_, err := tr.lower(singleComponentApp("Wrapper", "reserving-component", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_SchemaLessComponentRulePassThroughIsRejected: a ComponentLoweringRule
// that declares no schema had nothing check its authored input, so the component it
// emits is not synthesized, and an authored reserved value it copied through is
// rejected at the handler.
func TestTransform_SchemaLessComponentRulePassThroughIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})

	_, err := tr.Transform(singleComponentApp("Application", "pass-through", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_SchemaLessTraitRulePassThroughIsRejected is the same at trait
// position: a schema-less trait rule's emitted component is not synthesized.
func TestTransform_SchemaLessTraitRulePassThroughIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterTraitLowering(passThroughTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Name = "main"
	app.Spec.Components[0].Traits = []Trait{{Type: "pass-through-sidecar", Properties: authoredNetworkPolicy()}}
	_, err := tr.Transform(app, TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_SchemaLessTraitRuleOverSealedTraitIsRejected: a sealed trait is
// rule output, not checked input. A schema-less component rule copies an authored
// reserved value into a sealed trait, and a schema-less trait rule copies it into a
// component; that component is not synthesized, so the handler rejects the value.
func TestTransform_SchemaLessTraitRuleOverSealedTraitIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(traitWrappingComponentRule{})
	tr.RegisterTraitLowering(passThroughTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "trait-wrapping", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_SchemaTraitRuleOverSealedTraitIsRejected: a schema-less component
// rule fills a sealed trait with authored properties, so the trait is not
// synthesized. A trait rule that declares a schema has that schema checked against
// it before it runs (go-kure/launcher#611), and the authored reserved value is
// rejected there.
func TestTransform_SchemaTraitRuleOverSealedTraitIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(traitWrappingComponentRule{})
	tr.RegisterTraitLowering(schemaPassThroughTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "trait-wrapping", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
	// Since go-kure/launcher#611 the sealed trait is checked before the
	// schema-declaring rule runs, so the refusal names the trait, not only the
	// component createApplications would have named.
	if msg := err.Error(); !strings.Contains(msg, `trait "pass-through-sidecar" on component "web"`) {
		t.Errorf("expected the refusal at the trait rule, naming the trait, got: %v", msg)
	}
}

// TestTransform_DocumentRuleCopyingUncheckedComponentIsRejected: before a document
// rule, a component whose type has no schema is not checked, so the rule's output is
// not synthesized and an authored reserved value it copied into a reserved-sink is
// rejected at the handler.
func TestTransform_DocumentRuleCopyingUncheckedComponentIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})
	tr.RegisterDocumentLowering(retypingDocRule{})

	_, err := tr.Transform(singleComponentApp("Retyping", "pass-through", authoredNetworkPolicy()), TransformContext{})
	expectPlatformReserved(t, err)
}
