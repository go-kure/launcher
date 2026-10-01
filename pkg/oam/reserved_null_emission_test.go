package oam

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// --- go-kure/launcher#609: an authored reserved null through a lowering rule ------
//
// enforcePlatformReserved counts an explicit null as an authored value, but
// emission validation normalizes that null to absence. A rule whose output is not
// synthesized therefore has its emitted components checked for reserved keys before
// emission validation runs, so the null it copied through is still seen.

// authoredReservedNull is authoredNetworkPolicy with the reserved key written as an
// explicit null.
func authoredReservedNull() map[string]any {
	return map[string]any{"image": "nginx", "networkPolicy": nil}
}

// expectReservedNullRefusedAtEmission is expectPlatformReserved plus the emission
// site: the refusal names the component the rule emitted.
func expectReservedNullRefusedAtEmission(t *testing.T, err error) {
	t.Helper()
	expectPlatformReserved(t, err)
	if msg := err.Error(); !strings.Contains(msg, `emitted component "web" (type "reserved-sink")`) {
		t.Errorf("expected the refusal to name the emitted component, got: %v", msg)
	}
}

// checkedPassThroughComponentRule declares a schema with nothing reserved and copies
// its properties into a reserved-sink component. Its input is checked, so its output
// is synthesized and the reserved key it writes is the rule's own.
type checkedPassThroughComponentRule struct{}

func (checkedPassThroughComponentRule) ComponentType() string { return "checked-pass-through" }

func (checkedPassThroughComponentRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"image":         {Type: PropertyTypeString, Description: "Authored freely."},
		"networkPolicy": {Type: PropertyTypeObject, AdditionalProperties: true, Description: "Not reserved here."},
	}
}

func (checkedPassThroughComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "reserved-sink", Properties: comp.Properties}}}, nil
}

// rawReservedSinkRule is a RawDocumentLoweringRule that writes one reserved-sink
// component with the given properties.
type rawReservedSinkRule struct{ props map[string]any }

func (rawReservedSinkRule) Kind() string { return "WebApplication" }

func (rawReservedSinkRule) DecodeDocument(raw []byte) (any, error) {
	var doc testRawDoc
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r rawReservedSinkRule) LowerDocument(doc any, _ LoweringContext) (LoweringResult, error) {
	src := doc.(*testRawDoc)
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: src.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "reserved-sink",
			Properties: r.props,
		}}},
	}}}, nil
}

// TestTransform_AuthoredReservedNullIsRejected is the authored-surface baseline the
// lowering paths below must match: a reserved key written as null is refused.
func TestTransform_AuthoredReservedNullIsRejected(t *testing.T) {
	_, err := reservedSinkTransformer().Transform(singleComponentApp("Application", "reserved-sink", authoredReservedNull()), TransformContext{})
	expectPlatformReserved(t, err)
}

// TestTransform_SchemaLessComponentRulePassThroughOfReservedNullIsRejected: a
// schema-less ComponentLoweringRule copies an authored reserved null into a
// reserved-sink.
func TestTransform_SchemaLessComponentRulePassThroughOfReservedNullIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})

	_, err := tr.Transform(singleComponentApp("Application", "pass-through", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedAtEmission(t, err)
}

// TestTransform_SchemaLessTraitRulePassThroughOfReservedNullIsRejected: a
// schema-less TraitLoweringRule copies authored trait properties holding a reserved
// null into a reserved-sink it emits.
func TestTransform_SchemaLessTraitRulePassThroughOfReservedNullIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterTraitLowering(passThroughTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Name = "main"
	app.Spec.Components[0].Traits = []Trait{{Type: "pass-through-sidecar", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedAtEmission(t, err)
}

// TestTransform_SchemaLessTraitRuleOverSealedTraitOfReservedNullIsRejected: the null
// travels through a sealed trait a schema-less component rule filled, then into the
// component a schema-less trait rule emits.
func TestTransform_SchemaLessTraitRuleOverSealedTraitOfReservedNullIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(traitWrappingComponentRule{})
	tr.RegisterTraitLowering(passThroughTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "trait-wrapping", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedAtEmission(t, err)
}

// TestTransform_DocumentRuleCopyingUncheckedReservedNullIsRejected: a document rule
// rebuilds by value a component whose type has no schema, so nothing checked it
// before the rule ran, and copies its reserved null into a reserved-sink.
func TestTransform_DocumentRuleCopyingUncheckedReservedNullIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})
	tr.RegisterDocumentLowering(retypingDocRule{})

	_, err := tr.Transform(singleComponentApp("Retyping", "pass-through", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedAtEmission(t, err)
}

// TestLowerRaws_RawRuleWritingReservedNullIsRejected: a raw rule's output is authored
// input, and the reserved null it writes is refused before LowerRaws serializes the
// document — after that, the null would be gone.
func TestLowerRaws_RawRuleWritingReservedNullIsRejected(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterRawDocumentLowering(rawReservedSinkRule{props: authoredReservedNull()})

	_, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
	expectReservedNullRefusedAtEmission(t, err)
}

// TestTransform_SynthesizedReservedNullIsAccepted pins the exemption the check keeps:
// a rule that declares a schema had its input checked, so the reserved key it writes
// — here an explicit null, which emission validation then drops — is its own output.
func TestTransform_SynthesizedReservedNullIsAccepted(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(checkedPassThroughComponentRule{})

	if _, err := tr.Transform(singleComponentApp("Application", "checked-pass-through", authoredReservedNull()), TransformContext{}); err != nil {
		t.Fatalf("a synthesized component's reserved null must be accepted, got: %v", err)
	}
}

// TestTransform_SchemaLessRuleWithoutReservedKeyIsAccepted: the emission-site check
// refuses the reserved key only, not everything a schema-less rule passes through.
func TestTransform_SchemaLessRuleWithoutReservedKeyIsAccepted(t *testing.T) {
	tr := reservedSinkTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})

	if _, err := tr.Transform(singleComponentApp("Application", "pass-through", map[string]any{"image": "nginx"}), TransformContext{}); err != nil {
		t.Fatalf("an unreserved pass-through must be accepted, got: %v", err)
	}
}
