package oam

import (
	"strings"
	"testing"
)

// --- C4 fixtures -----------------------------------------------------------

// extraTypesComponentRule / extraTypesTraitRule / extraTypesPolicyRule exist only to
// be REGISTERED, so LowerableTypes() reports their type names. Their Lower* bodies are
// never reached by the tests in this file, which stop at parse/validation.
type extraTypesComponentRule struct{ typeName string }

func (r extraTypesComponentRule) ComponentType() string { return r.typeName }

func (r extraTypesComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       comp.Name,
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx"},
	}}}, nil
}

type extraTypesTraitRule struct{ typeName string }

func (r extraTypesTraitRule) TraitType() string { return r.typeName }

func (r extraTypesTraitRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}, nil
}

type extraTypesPolicyRule struct{ typeName string }

func (r extraTypesPolicyRule) PolicyType() string { return r.typeName }

func (r extraTypesPolicyRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "override"}}}, nil
}

// --- C4a: the channel is per position, never a blanket hole ----------------

// TestParseWithExtraTypes_PositionIsolation proves a claimed name widens exactly one
// position: the same string admitted as a component type does not become an acceptable
// kind or trait type, and vice versa. Without this, one lowering rule would quietly
// open the gate everywhere its type name happened to appear.
func TestParseWithExtraTypes_PositionIsolation(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		lowerable LowerableTypes
		wantField string
	}{
		{
			name:      "component-type claim does not admit a kind",
			doc:       nonTerminalKindYAML,
			lowerable: LowerableTypes{ComponentTypes: []string{"WebApplication"}, TraitTypes: []string{"WebApplication"}},
			wantField: "kind",
		},
		{
			name:      "trait-type claim does not admit a component type",
			doc:       nonTerminalComponentTypeYAML,
			lowerable: LowerableTypes{Kinds: []string{"web-and-cache"}, TraitTypes: []string{"web-and-cache"}},
			wantField: "type",
		},
		{
			name:      "component-type claim does not admit a trait type",
			doc:       nonTerminalTraitTypeYAML,
			lowerable: LowerableTypes{Kinds: []string{"expose-plus"}, ComponentTypes: []string{"expose-plus"}},
			wantField: "type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseWithExtraTypes([]byte(tc.doc), nil, tc.lowerable)
			if err == nil {
				t.Fatalf("expected rejection, got none (lowerable=%+v)", tc.lowerable)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("expected the error to name field %q, got: %v", tc.wantField, err)
			}
		})
	}
}

// TestParseWithExtraTypes_PolicyTypeNeedsNoWidening documents why
// validateWithExtraTypes never reads lowerable.PolicyTypes: policy types have no
// allowlist to widen. An arbitrary policy type parses with an EMPTY LowerableTypes,
// and naming it in PolicyTypes changes nothing.
func TestParseWithExtraTypes_PolicyTypeNeedsNoWidening(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
  policies:
    - name: ordering
      type: staged-rollout
`
	if _, err := ParseWithExtraTypes([]byte(doc), nil, LowerableTypes{}); err != nil {
		t.Fatalf("expected an unclaimed policy type to parse (policy types are open-ended), got: %v", err)
	}
	if _, err := ParseWithExtraTypes([]byte(doc), nil, LowerableTypes{PolicyTypes: []string{"staged-rollout"}}); err != nil {
		t.Fatalf("expected claiming the policy type to change nothing, got: %v", err)
	}
}

// --- C4b: additive, not a replacement --------------------------------------

// TestParseWithExtraTypes_CapabilityAndLoweringTraitTypesCombine proves the two trait
// channels stack: a CapabilityDefinition-supplied type and a lowering-claimed type are
// both accepted in one document, and dropping either one re-rejects that half.
func TestParseWithExtraTypes_CapabilityAndLoweringTraitTypesCombine(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
      traits:
        - type: site-mesh
          properties: {}
        - type: expose-plus
          properties: {}
`
	capTypes := []string{"site-mesh"}
	lowerable := LowerableTypes{TraitTypes: []string{"expose-plus"}}

	if _, err := ParseWithExtraTypes([]byte(doc), capTypes, lowerable); err != nil {
		t.Fatalf("expected both trait channels to apply together, got: %v", err)
	}
	if _, err := ParseWithExtraTypes([]byte(doc), nil, lowerable); err == nil {
		t.Fatal("expected rejection of the capability-defined trait type when it is not supplied")
	}
	if _, err := ParseWithExtraTypes([]byte(doc), capTypes, LowerableTypes{}); err == nil {
		t.Fatal("expected rejection of the lowering-claimed trait type when LowerableTypes is empty")
	}
}

// TestParseWithExtraTypes_LeavesCallerTraitSetUnmodified guards the merge helper: the
// caller's custom-trait slice must not pick up lowering claims, which would make a
// later parse with an empty LowerableTypes silently permissive.
func TestParseWithExtraTypes_LeavesCallerTraitSetUnmodified(t *testing.T) {
	base := map[string]bool{"site-mesh": true}
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
				Traits:     []Trait{{Type: "expose-plus"}},
			}},
		},
	}

	if err := validateWithExtraTypes(app, base, nil, LowerableTypes{TraitTypes: []string{"expose-plus"}}); err != nil {
		t.Fatalf("validateWithExtraTypes: %v", err)
	}
	if len(base) != 1 || !base["site-mesh"] {
		t.Fatalf("the caller's custom trait set was mutated: %v", base)
	}
}

// --- C4c: everything else still applies ------------------------------------

// TestParseWithExtraTypes_AdmittedKindStillFullyValidated proves the channel only
// widens the three allowlists: a document whose kind is claimed still has to satisfy
// every other rule.
func TestParseWithExtraTypes_AdmittedKindStillFullyValidated(t *testing.T) {
	lowerable := LowerableTypes{Kinds: []string{"WebApplication"}}

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "wrong apiVersion",
			doc: `apiVersion: launcher.gokure.dev/v1
kind: WebApplication
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
`,
			want: "apiVersion",
		},
		{
			name: "no components",
			doc: `apiVersion: launcher.gokure.dev/v1alpha1
kind: WebApplication
metadata:
  name: myapp
spec:
  components: []
`,
			want: "spec.components",
		},
		{
			name: "duplicate component names",
			doc: `apiVersion: launcher.gokure.dev/v1alpha1
kind: WebApplication
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
    - name: web
      type: webservice
      properties:
        image: nginx
`,
			want: "duplicate component name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseWithExtraTypes([]byte(tc.doc), nil, lowerable)
			if err == nil {
				t.Fatal("expected rejection, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected the error to mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// TestParseWithExtraTypes_DecodeStaysStrict proves the channel widens VALIDATION only.
// Decoding is still KnownFields(true), so an authored field ApplicationSpec cannot hold
// fails even for a claimed kind — that document belongs to the raw entry point instead.
func TestParseWithExtraTypes_DecodeStaysStrict(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: WebApplication
metadata:
  name: myapp
spec:
  hostname: shop.example.com
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
`
	_, err := ParseWithExtraTypes([]byte(doc), nil, LowerableTypes{Kinds: []string{"WebApplication"}})
	if err == nil {
		t.Fatal("expected the strict decode to reject an unknown spec field even for a claimed kind")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("expected the error to name the unknown field, got: %v", err)
	}
}

// --- C4d: the intended wiring ----------------------------------------------

// TestParseWithExtraTypes_FromTransformerLowerableTypes is the end-to-end shape a
// caller uses: register rules, hand Transformer.LowerableTypes() to the parser, and a
// document authored entirely in claimed types parses — while the same bytes are
// rejected by every parse entry point that does not carry the set.
func TestParseWithExtraTypes_FromTransformerLowerableTypes(t *testing.T) {
	const doc = `apiVersion: launcher.gokure.dev/v1alpha1
kind: OrderedApplication
metadata:
  name: myapp
spec:
  components:
    - name: shop
      type: web-and-cache
      properties: {}
      traits:
        - type: expose-plus
          properties: {}
  policies:
    - name: ordering
      type: staged-rollout
`
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(testDocRule{kind: "OrderedApplication"})
	tr.RegisterComponentLowering(extraTypesComponentRule{typeName: "web-and-cache"})
	tr.RegisterTraitLowering(extraTypesTraitRule{typeName: "expose-plus"})
	tr.RegisterPolicyLowering(extraTypesPolicyRule{typeName: "staged-rollout"})

	if _, err := ParseWithExtraTypes([]byte(doc), nil, tr.LowerableTypes()); err != nil {
		t.Fatalf("expected LowerableTypes() from the registering transformer to admit the document, got: %v", err)
	}

	// The channel is additive: without it, the very same bytes are still rejected.
	if _, err := Parse([]byte(doc)); err == nil {
		t.Fatal("Parse must still reject a document authored in lowerable types")
	}
	if _, err := ParseWithExtraTraitTypes([]byte(doc), []string{"expose-plus"}); err == nil {
		t.Fatal("ParseWithExtraTraitTypes must still reject the non-terminal kind")
	}
	if _, err := ParseWithExtraTypes([]byte(doc), nil, LowerableTypes{}); err == nil {
		t.Fatal("ParseWithExtraTypes with an empty LowerableTypes must behave exactly like the strict path")
	}
}

// TestValidate_UnchangedByTheChannel pins the "empty set == today's behavior" claim on
// the two pre-existing wrappers, which now route through validateWithExtraTypes.
func TestValidate_UnchangedByTheChannel(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
				Traits:     []Trait{{Type: "site-mesh"}},
			}},
		},
	}

	if err := validate(app); err == nil {
		t.Fatal("validate must still reject an unknown trait type")
	}
	if err := validateWithCustomTraits(app, map[string]bool{"site-mesh": true}); err != nil {
		t.Fatalf("validateWithCustomTraits must still admit a capability-defined trait type, got: %v", err)
	}

	app.Spec.Components[0].Type = "web-and-cache"
	if err := validateWithCustomTraits(app, map[string]bool{"site-mesh": true}); err == nil {
		t.Fatal("validateWithCustomTraits must not admit a lowerable component type")
	}
}
