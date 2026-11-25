package oam

import (
	"errors"
	"testing"
)

// capabilityRefusal is one site that names a capability in its refusal text
// (go-kure/launcher#1687): the key the text names, the text the site had before
// CapabilityError existed, and a way to provoke it through the entry its
// neighbouring test uses.
type capabilityRefusal struct {
	name  string
	key   string
	text  string
	build func(t *testing.T) error
	// inner checks that what the refusal was before is still found through it.
	inner func(t *testing.T, err error)
}

func innerPropertyError(t *testing.T, err error) {
	t.Helper()
	var pe *PropertyError
	if !errors.As(err, &pe) {
		t.Errorf("errors.As *PropertyError failed for %T: %v", err, err)
	}
}

func innerMissingCapability(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrMissingCapability) {
		t.Errorf("errors.Is ErrMissingCapability failed for %T: %v", err, err)
	}
}

func innerTraitCapabilityViolation(t *testing.T, err error) {
	t.Helper()
	var ve *ViolationError
	if !errors.As(err, &ve) || ve.Class != RefusalTraitCapability {
		t.Errorf("want a ViolationError of class %q, got %T: %v", RefusalTraitCapability, err, err)
	}
	var pr *PolicyRefusal
	if !errors.As(err, &pr) {
		t.Errorf("errors.As *PolicyRefusal failed for %T: %v", err, err)
	}
}

func capabilityRefusals() []capabilityRefusal {
	oneOf := func(props, rendering map[string]any) func(t *testing.T) error {
		return func(t *testing.T) error {
			tr := oneOfTransformer(&oneOfTrait{})
			app := authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: props})
			caps := map[string]CapabilityBinding{"oneof": {Rendering: rendering}}
			_, err := tr.ValidateAuthoredPropertiesWithCapabilities(app, caps)
			return err
		}
	}
	store := func(h ComponentHandler, rendering map[string]any, props map[string]any) func(t *testing.T) error {
		return func(t *testing.T) error {
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			caps := map[string]CapabilityBinding{"store": {Rendering: rendering}}
			app := storeApp(Component{Name: "data", Type: "store", Properties: props})
			_, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps})
			return err
		}
	}
	policy := func(comp Component, p *constrainedPolicy) func(t *testing.T) error {
		return func(t *testing.T) error {
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"expose": &stubTraitHandler{typ: "expose"}},
			)
			_, err := tr.Transform(makeApp("myapp", comp), TransformContext{Policy: p})
			return err
		}
	}
	exposed := Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}
	return []capabilityRefusal{
		{name: "required, nested, named capability", key: "issuer",
			text:  `component "web": trait "issuer": properties.issuerRef: "name" is required; neither the trait nor capability "issuer"'s rendering sets it`,
			inner: innerPropertyError,
			build: func(t *testing.T) error {
				got := map[string]any{}
				tr := issuerTransformer(&issuerTrait{}, &got)
				app := issuerApp("issuer", map[string]any{"secretName": "tls", "issuerRef": map[string]any{"kind": "Issuer"}})
				caps := issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"kind": "ClusterIssuer"}})
				_, err := tr.ValidateAuthoredPropertiesWithCapabilities(app, caps)
				return err
			}},
		{name: "required-when, named capability", key: "store",
			text:  `properties.o: "csi" is required when "type" is "csi"; neither the trait nor capability "store"'s rendering sets it`,
			inner: innerPropertyError,
			build: func(t *testing.T) error {
				p := map[string]any{"o": map[string]any{"type": "csi"}}
				return checkNestedRequired(volumeLikeObject(), nil, p, "properties", "store")
			}},
		{name: "exclusive, both set", key: "oneof",
			text:  `component "web": trait "oneof": properties.source: "git" and "oci" are mutually exclusive; the trait merged with capability "oneof"'s rendering sets them together`,
			inner: innerPropertyError,
			build: oneOf(
				map[string]any{"inline": "x", "source": map[string]any{"git": "a"}},
				map[string]any{"source": map[string]any{"oci": "b"}})},
		{name: "exclusive, none set", key: "oneof",
			text:  `component "web": trait "oneof": properties.source: exactly one of "git" and "oci" is required; neither the trait nor capability "oneof"'s rendering sets one`,
			inner: innerPropertyError,
			build: oneOf(
				map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}},
				map[string]any{"source": map[string]any{"digest": "sha"}})},
		{name: "EvaluateProfile, handler refuses the rendering", key: "expose",
			text: `capability "expose": bad rendering`,
			build: func(t *testing.T) error {
				h := &errVADHandler{stubTraitHandler: stubTraitHandler{typ: "expose"}, err: errors.New("bad rendering")}
				tr := NewTransformer(nil, map[string]TraitHandler{"expose": h})
				profile := &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
					"expose": {Rendering: map[string]any{"controllerType": "unknown"}},
				}}}
				_, err := tr.EvaluateProfile(profile)
				return err
			}},
		{name: "EvaluateProfile, definition schema refuses the rendering", key: "custom",
			text: `capability "custom" definition schema: required rendering property "timeout" is missing`,
			build: func(t *testing.T) error {
				tr := NewTransformer(nil, nil)
				tr.RegisterTrait("custom", &customVADHandler{stubTraitHandler: stubTraitHandler{typ: "custom"}})
				tr.SetCapabilityDefs(map[string]*CapabilityDefinition{"custom": capDefFixture("custom")})
				profile := &ClusterProfile{Spec: ClusterProfileSpec{Capabilities: map[string]CapabilityBinding{
					"custom": {Rendering: map[string]any{"mode": "fast"}},
				}}}
				_, err := tr.EvaluateProfile(profile)
				return err
			}},
		{name: "capability missing, trait handler", key: "ingress",
			text:  `component "web" trait "ingress": capability "ingress" not found in ClusterProfile: oam: capability key not found in ClusterProfile`,
			inner: innerMissingCapability,
			build: func(t *testing.T) error {
				tr := NewTransformer(
					map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
					map[string]TraitHandler{"ingress": &capAwarePipelineHandler{typ: "ingress"}},
				)
				comp := Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "ingress", Properties: map[string]any{}}}}
				_, err := tr.Transform(makeApp("myapp", comp), TransformContext{})
				return err
			}},
		{name: "capability missing, lowering rule", key: "needs-cap",
			text:  `lowering document "myapp" in document "myapp" (kind "Application"): trait "needs-cap" on component "app" in document "myapp" (kind "Application"): capability "needs-cap" not found in ClusterProfile: oam: capability key not found in ClusterProfile`,
			inner: innerMissingCapability,
			build: func(t *testing.T) error {
				tr := NewTransformer(map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}}, nil)
				tr.RegisterTraitLowering(capAwareTraitLoweringRuleWithVAD{capAwareTraitLoweringRule{typ: "needs-cap"}})
				comp := Component{Name: "app", Type: "webservice", Traits: []Trait{{Type: "needs-cap", Properties: map[string]any{}}}}
				_, err := tr.Transform(storeApp(comp), TransformContext{})
				return err
			}},
		{name: "defaults, malformed schema group", key: "store",
			text: `component "data": capability "store" defaults: properties: schema declares exclusive key "inline" Required; a group's keys are optional, and the group's Required says one is set`,
			build: store(&requiredInGroupComponent{oneOfDefaultsComponent{oneOfComponent: oneOfComponent{typ: "store"}}},
				map[string]any{"url": 123}, map[string]any{})},
		{name: "defaults, exclusive group after the fill", key: "store",
			text: `component "data": capability "store" defaults: properties: "inline" and "url" are mutually exclusive`,
			build: store(&oneOfDefaultsComponent{oneOfComponent: oneOfComponent{typ: "store"}},
				map[string]any{"url": "https://example.com/x.yaml"}, map[string]any{"inline": "x"})},
		{name: "defaults, uncopyable value", key: "store",
			text: `capability "store" defaults: rendering key "class": value is or holds chan int, which is not a property value (a string, boolean, number, list or string-keyed object)`,
			build: func(t *testing.T) error {
				h := &defaultsComponentHandler{}
				tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
				caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"class": make(chan int)}}}
				_, err := tr.applyComponentCapabilityDefaults(h, map[string]any{}, TransformContext{Capabilities: caps}, nil)
				return err
			}},
		{name: "defaults, nothing validates the rendering", key: "store",
			text: `component "data": capability "store" defaults: nothing validates rendering keys ["class"]: the component handler declares no PropertySchema, and no trait handler or trait lowering rule for type "store" validates the rendering (none implements ValidateAndApplyDefaults, and no CapabilityDefinition applies to it); implement PropertySchemaProvider on the component handler, or ValidateAndApplyDefaults on the trait`,
			build: store(&keyedDefaultsHandler{key: "store", keys: []string{"class"}},
				map[string]any{"class": "fast"}, map[string]any{})},
		{name: "defaults, key the component does not declare", key: "store",
			text: `component "data": capability "store" defaults: rendering key "tier" is not a property the component declares (allowed: class)`,
			build: store(&schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class", "tier"}}},
				map[string]any{"class": "fast", "tier": "gold"}, map[string]any{})},
		{name: "defaults, value fails the component schema", key: "store",
			text: `component "data": capability "store" defaults: properties.class: expected string, got int`,
			build: store(&schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}},
				map[string]any{"class": 5}, map[string]any{})},
		{name: "rendering key holds no property value", key: "store",
			text:  `capability "store" rendering key "class": value is or holds chan int, which is not a property value (a string, boolean, number, list or string-keyed object)`,
			build: store(&defaultsComponentHandler{}, map[string]any{"class": make(chan int)}, map[string]any{})},
		{name: "policy, forbidden", key: "expose",
			text:  `component "myapp": capability "expose" is forbidden by environment policy`,
			inner: innerTraitCapabilityViolation,
			build: policy(exposed, &constrainedPolicy{forbidden: []string{"expose"}})},
		{name: "policy, not in the allowed list", key: "expose",
			text:  `component "myapp": capability "expose" is not in the allowed list`,
			inner: innerTraitCapabilityViolation,
			build: policy(exposed, &constrainedPolicy{allowed: []string{"ingress"}})},
		{name: "policy, required and missing", key: "ingress",
			text:  `component "myapp": required capability "ingress" is missing`,
			inner: innerTraitCapabilityViolation,
			build: policy(makeComponent("web", "webservice"), &constrainedPolicy{required: []string{"ingress"}})},
	}
}

// TestCapabilityRefusals_CarryTheKey: a refusal that names a capability in its text
// carries that key in a CapabilityError errors.As finds, the text is what the site
// had before, and what the refusal was before is still found through it
// (go-kure/launcher#1687).
func TestCapabilityRefusals_CarryTheKey(t *testing.T) {
	for _, tc := range capabilityRefusals() {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.build(t)
			if err == nil {
				t.Fatal("no refusal")
			}
			var ce *CapabilityError
			if !errors.As(err, &ce) {
				t.Fatalf("errors.As *CapabilityError failed for %T: %v", err, err)
			}
			if ce.Capability != tc.key {
				t.Errorf("Capability = %q, want %q", ce.Capability, tc.key)
			}
			if got := err.Error(); got != tc.text {
				t.Errorf("text changed:\n got  %q\n want %q", got, tc.text)
			}
			if tc.inner != nil {
				tc.inner(t, err)
			}
		})
	}
}

// TestCapabilityError_TextIsTheRefusals: the wrapper adds nothing to the text and
// unwraps to the refusal.
func TestCapabilityError_TextIsTheRefusals(t *testing.T) {
	inner := errors.New("a refusal")
	ce := &CapabilityError{Capability: "store", Err: inner}
	if ce.Error() != "a refusal" {
		t.Errorf("Error() = %q, want the refusal's text", ce.Error())
	}
	if !errors.Is(ce, inner) {
		t.Error("errors.Is does not reach the refusal")
	}
}
