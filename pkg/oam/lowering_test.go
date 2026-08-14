package oam

import (
	stderrors "errors"
	"strings"
	"testing"
)

// TestLower_EmptyRegistry_ReturnsSamePointer is the bit-identity proof for the
// lowering engine (C1): with no rules registered at any position, lower must not
// copy, mutate, or re-validate app — it returns the exact same pointer it was given.
func TestLower_EmptyRegistry_ReturnsSamePointer(t *testing.T) {
	tr := NewTransformer(nil, nil)
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower returned error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly 1 document, got %d", len(docs))
	}
	if docs[0] != app {
		t.Fatal("lower did not return the same pointer with an empty registry")
	}
}

func TestHasLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	if tr.hasLoweringRules() {
		t.Fatal("expected hasLoweringRules to be false on a fresh Transformer")
	}
}

const nonTerminalKindYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: WebApplication
metadata:
  name: myapp
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx
`

const nonTerminalComponentTypeYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: shop
      type: web-and-cache
      properties: {}
`

const nonTerminalTraitTypeYAML = `apiVersion: launcher.gokure.dev/v1alpha1
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
        - type: expose-plus
          properties: {}
`

// TestParseWithExtraTypes_Kind proves a non-terminal kind is rejected by plain
// Parse/ParseWithExtraTraitTypes, and accepted by ParseWithExtraTypes only when
// LowerableTypes.Kinds claims it.
func TestParseWithExtraTypes_Kind(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalKindYAML)); err == nil {
		t.Fatal("expected Parse to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTraitTypes([]byte(nonTerminalKindYAML), nil); err == nil {
		t.Fatal("expected ParseWithExtraTraitTypes to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalKindYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject kind WebApplication")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalKindYAML), nil, LowerableTypes{Kinds: []string{"WebApplication"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with Kinds:[WebApplication] to accept it, got: %v", err)
	}
}

// TestParseWithExtraTypes_ComponentType is the same proof for the component position.
func TestParseWithExtraTypes_ComponentType(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalComponentTypeYAML)); err == nil {
		t.Fatal("expected Parse to reject component type web-and-cache")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalComponentTypeYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject component type web-and-cache")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalComponentTypeYAML), nil, LowerableTypes{ComponentTypes: []string{"web-and-cache"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with ComponentTypes:[web-and-cache] to accept it, got: %v", err)
	}
}

const lowerableComponentWithRestrictedTraitYAML = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: myapp
spec:
  components:
    - name: shop
      type: web-and-cache
      properties: {}
      traits:
        - type: scaler
          properties: {}
`

// TestParseWithExtraTypes_DefersTraitRestrictionForLowerableComponent proves that a
// trait restricted to specific component types ("scaler": webservice/worker only,
// traitComponentRestrictions in validate.go) is NOT rejected against a lowerable
// component type's authored type. web-and-cache is not terminal — a registered
// ComponentLoweringRule would rewrite it before the fixpoint settles, so validating
// the restriction against "web-and-cache" here would check the wrong component type.
// validateSettled re-runs this same restriction after lowering, against whatever
// terminal type the rule actually emits.
func TestParseWithExtraTypes_DefersTraitRestrictionForLowerableComponent(t *testing.T) {
	if _, err := ParseWithExtraTypes([]byte(lowerableComponentWithRestrictedTraitYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject non-terminal component type web-and-cache")
	}
	if _, err := ParseWithExtraTypes([]byte(lowerableComponentWithRestrictedTraitYAML), nil, LowerableTypes{ComponentTypes: []string{"web-and-cache"}}); err != nil {
		t.Fatalf("expected the scaler trait-component restriction to be deferred for a lowerable component type, got: %v", err)
	}
}

// TestParseWithExtraTypes_TraitType is the same proof for the trait position.
func TestParseWithExtraTypes_TraitType(t *testing.T) {
	if _, err := Parse([]byte(nonTerminalTraitTypeYAML)); err == nil {
		t.Fatal("expected Parse to reject trait type expose-plus")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalTraitTypeYAML), nil, LowerableTypes{}); err == nil {
		t.Fatal("expected ParseWithExtraTypes with empty LowerableTypes to reject trait type expose-plus")
	}
	if _, err := ParseWithExtraTypes([]byte(nonTerminalTraitTypeYAML), nil, LowerableTypes{TraitTypes: []string{"expose-plus"}}); err != nil {
		t.Fatalf("expected ParseWithExtraTypes with TraitTypes:[expose-plus] to accept it, got: %v", err)
	}
}

// TestTransformer_LowerableTypes proves the accessor reflects exactly what is
// registered, on all four positions.
func TestTransformer_LowerableTypes(t *testing.T) {
	tr := NewTransformer(nil, nil)
	lt := tr.LowerableTypes()
	if len(lt.Kinds) != 0 || len(lt.ComponentTypes) != 0 || len(lt.TraitTypes) != 0 || len(lt.PolicyTypes) != 0 {
		t.Fatalf("expected all-empty LowerableTypes on a fresh Transformer, got %+v", lt)
	}
}

// --- C2 fixtures -----------------------------------------------------------

// testDocRule implements DocumentLoweringRule and nothing else. It renames the
// document it lowers (name + "-lowered", kind "Application") and carries its
// components through untouched, so a test can observe what a document-position
// rename does to the provenance of everything below it.
//
// It deliberately does NOT implement RawDocumentLoweringRule: the two LowerDocument
// signatures differ, so no single concrete type can satisfy both interfaces. That is
// the compile-time half of the mutual exclusion between the two rule flavours.
type testDocRule struct {
	kind string
}

func (r testDocRule) Kind() string { return r.kind }

func (r testDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name + "-lowered"},
		Spec:       ApplicationSpec{Components: doc.Spec.Components},
	}}}, nil
}

// loopyDocRule re-emits its own kind forever, so the document position never reaches
// a fixpoint — the document-level counterpart of loopyRule (lowering_negative_test.go).
type loopyDocRule struct{}

func (loopyDocRule) Kind() string { return "LoopyDoc" }

func (loopyDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: doc.APIVersion,
		Kind:       "LoopyDoc",
		Metadata:   doc.Metadata,
		Spec:       doc.Spec,
	}}}, nil
}

// originCaptureRule records the LoweringContext.Origin it was handed, so a test can
// assert what provenance the engine attributes to a component.
type originCaptureRule struct {
	seen *Origin
}

func (originCaptureRule) ComponentType() string { return "probe" }

func (r originCaptureRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = lctx.Origin
	return LoweringResult{Components: []Component{{
		Name:       comp.Name + "-web",
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx"},
	}}}, nil
}

// mustPanicContaining asserts that f panics with a message containing want.
func mustPanicContaining(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected a panic mentioning %q, got none", want)
			return
		}
		msg, ok := r.(string)
		if !ok {
			t.Errorf("expected a string panic value, got %T: %v", r, r)
			return
		}
		if !strings.Contains(msg, want) {
			t.Errorf("expected the panic message to mention %q, got: %s", want, msg)
		}
	}()
	f()
}

// --- C2a: pointer identity -------------------------------------------------

func TestLower_NoRules_ReturnsSamePointer(t *testing.T) {
	// apiVersion must be SupportedAPIVersion and spec.components must be non-empty,
	// or MustParse panics before the pointer-identity assertion below ever runs.
	tr := NewTransformer(nil, nil)
	app := MustParse([]byte("apiVersion: " + SupportedAPIVersion + "\nkind: Application\nmetadata: {name: a}\nspec: {components: [{name: web, type: webservice, properties: {image: nginx}}]}\n"))
	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(got) != 1 || got[0] != app {
		t.Fatalf("lower returned %d docs, want the same pointer back", len(got))
	}
}

func TestLower_RawRulesOnly_StillReturnsSamePointer(t *testing.T) {
	// A raw rule is unreachable from the in-transform path, so registering one must
	// not cost the in-transform path its pointer identity.
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})
	app := MustParse([]byte("apiVersion: " + SupportedAPIVersion + "\nkind: Application\nmetadata: {name: a}\nspec: {components: [{name: web, type: webservice, properties: {image: nginx}}]}\n"))
	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(got) != 1 || got[0] != app {
		t.Fatalf("registering a raw-only rule broke pointer identity")
	}
}

// TestLower_ComponentOriginSurvivesDocumentLowering proves the Origin doctrine holds
// across a document-position rename: once a document rule has renamed a document, its
// Metadata.Name/Kind hold a SYNTHESIZED identity, and a component-position rule's
// Origin must still name the ORIGINAL authored document.
func TestLower_ComponentOriginSurvivesDocumentLowering(t *testing.T) {
	var seen Origin
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(testDocRule{kind: "Renamer"})
	tr.RegisterComponentLowering(originCaptureRule{seen: &seen})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Renamer",
		Metadata:   Metadata{Name: "authored"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "probe-me", Type: "probe", Properties: map[string]any{}}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 lowered document, got %d", len(docs))
	}
	if docs[0].Metadata.Name != "authored-lowered" {
		t.Fatalf("expected the document rule's rename to stick, got %q", docs[0].Metadata.Name)
	}
	if seen.Document != "authored" {
		t.Errorf("component Origin.Document = %q, want the AUTHORED document name %q", seen.Document, "authored")
	}
	if seen.DocumentKind != "Renamer" {
		t.Errorf("component Origin.DocumentKind = %q, want the AUTHORED kind %q", seen.DocumentKind, "Renamer")
	}
	if seen.Component != "probe-me" {
		t.Errorf("component Origin.Component = %q, want %q", seen.Component, "probe-me")
	}
}

// --- F2: origin survives a SECOND round of component-position lowering -----
//
// stage1Rule and stage2CaptureRule chain two component-lowering rounds: stage1Rule
// (round 1) emits a "stage2"-typed component, which stage2CaptureRule (round 2) then
// claims. Before F2, compOrigin at the round-2 site was always rebuilt from the
// component's CURRENT (already-renamed) Name/Type, discarding the round-1-stamped
// authored origin that comp.Origin() carries.

type stage1Rule struct{}

func (stage1Rule) ComponentType() string { return "stage1" }

func (stage1Rule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name + "-s2", Type: "stage2", Properties: map[string]any{}}}}, nil
}

// stage2CaptureRule records the Origin it was handed at the component position, so a
// test can assert whether it is the round-1-stamped authored origin or a re-derived
// one built from the round-2 synthesized name/type.
type stage2CaptureRule struct {
	seen *Origin
}

func (stage2CaptureRule) ComponentType() string { return "stage2" }

func (r stage2CaptureRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = lctx.Origin
	return LoweringResult{Components: []Component{{Name: comp.Name + "-web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}}}, nil
}

// TestLower_ComponentOriginSurvivesSecondRoundLowering is the regression test for the
// Codex review finding F2.
func TestLower_ComponentOriginSurvivesSecondRoundLowering(t *testing.T) {
	var seen Origin
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(stage1Rule{})
	tr.RegisterComponentLowering(stage2CaptureRule{seen: &seen})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "shop", Type: "stage1", Properties: map[string]any{}}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(docs) != 1 || len(docs[0].Spec.Components) != 1 {
		t.Fatalf("expected exactly 1 document with 1 settled component, got %+v", docs)
	}
	if got := docs[0].Spec.Components[0].Name; got != "shop-s2-web" {
		t.Fatalf("expected the settled component name %q, got %q", "shop-s2-web", got)
	}
	if seen.Component != "shop" {
		t.Errorf("round-2 Origin.Component = %q, want the ROUND-1-stamped authored name %q, not one re-derived from the round-2 (already-renamed) component", seen.Component, "shop")
	}
	if seen.ComponentType != "stage1" {
		t.Errorf("round-2 Origin.ComponentType = %q, want the ROUND-1-stamped authored type %q, not the round-2 type %q", seen.ComponentType, "stage1", "stage2")
	}
}

// --- F3: validateSettled must not drop a registered custom trait handler ---

// TestLower_ValidateSettled_AcceptsHandlerRegisteredCustomTraitWithNoCapabilityDef is
// the regression test for the Codex review finding F3: validateSettled built its
// settled-validation allowlist solely from t.capabilityDefs, so a custom trait type
// accepted via a registered TraitHandler (RegisterTrait) but with NO
// CapabilityDefinition loaded for it was rejected purely because some OTHER lowering
// rule happened to be registered on the same Transformer — hasLoweringRules() is what
// routes execution through validateSettled at all.
func TestLower_ValidateSettled_AcceptsHandlerRegisteredCustomTraitWithNoCapabilityDef(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"custom-trait": &stubTraitHandler{typ: "custom-trait"}},
	)
	// No SetCapabilityDefs call: "custom-trait" has a registered handler but no loaded
	// CapabilityDefinition.
	//
	// Register an unrelated lowering rule purely so hasLoweringRules() is true and
	// lower() actually exercises the fixpoint (and therefore validateSettled) —
	// without any rule registered anywhere, lower() short-circuits to the bit-identity
	// no-op path and validateSettled never runs at all.
	tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "widget"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
				Traits:     []Trait{{Type: "custom-trait", Properties: map[string]any{}}},
			}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("expected the custom trait %q to survive validateSettled, got: %v", "custom-trait", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly 1 settled document, got %d", len(docs))
	}
}

// TestLower_ValidateSettled_AcceptsHandlerRegisteredCustomComponentWithNoCapabilityDef
// is the component-side mirror of the F3 regression test above, for a second-round
// Codex review finding: validateSettled built its ComponentTypes widening from an
// empty LowerableTypes, so a custom component type accepted via a registered
// ComponentHandler (RegisterComponent) but with NO CapabilityDefinition loaded for it
// was rejected purely because some OTHER lowering rule happened to be registered on
// the same Transformer — the exact same routing-through-validateSettled cause as F3,
// just on the component position instead of the trait position.
func TestLower_ValidateSettled_AcceptsHandlerRegisteredCustomComponentWithNoCapabilityDef(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"custom-component": &pipelineComponentHandler{typ: "custom-component"}},
		nil,
	)
	// No SetCapabilityDefs call: "custom-component" has a registered handler but no
	// loaded CapabilityDefinition.
	//
	// Register an unrelated lowering rule purely so hasLoweringRules() is true and
	// lower() actually exercises the fixpoint (and therefore validateSettled) —
	// without any rule registered anywhere, lower() short-circuits to the bit-identity
	// no-op path and validateSettled never runs at all.
	tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "widget"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "custom",
				Type:       "custom-component",
				Properties: map[string]any{"image": "nginx"},
			}},
		},
	}

	docs, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("expected the custom component type %q to survive validateSettled, got: %v", "custom-component", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly 1 settled document, got %d", len(docs))
	}
}

// TestLower_ValidateSettled_HandlerRegisteredComponentStillEnforcesTraitRestriction is
// the regression test for the round-5 Codex finding at lowering.go:589 (the
// "lowerable-types leak"): validateSettled populated LowerableTypes.ComponentTypes from
// t.componentHandlers — terminal, already-settled types — but LowerableTypes.ComponentTypes
// is also the signal validateTrait (validate.go) reads to defer the trait/component
// restriction check ("componentIsLowerable"), which is correct only for a component
// still awaiting a ComponentLoweringRule rewrite. Routing a terminal handler-backed
// type through that channel let it wrongly skip the restriction recheck at settlement.
//
// "gizmo" here is accepted only via a registered ComponentHandler (never via a
// ComponentLoweringRule, so it is never genuinely still-lowering); its "scaler" trait is
// restricted by traitComponentRestrictions to "webservice"/"worker" only
// (validate.go:56). Pre-fix, this incorrectly settled with no error. Post-fix, the
// restriction is re-enforced and this must be rejected.
func TestLower_ValidateSettled_HandlerRegisteredComponentStillEnforcesTraitRestriction(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"gizmo": &pipelineComponentHandler{typ: "gizmo"}},
		map[string]TraitHandler{"scaler": &stubTraitHandler{typ: "scaler"}},
	)
	// Register an unrelated lowering rule purely so hasLoweringRules() is true and
	// lower() actually exercises the fixpoint (and therefore validateSettled) —
	// without any rule registered anywhere, lower() short-circuits to the bit-identity
	// no-op path and validateSettled never runs at all. "gizmo" itself is never claimed
	// by any lowering rule, so it never enters LowerableTypes.ComponentTypes on its own.
	tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "widget"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "gadget",
				Type:       "gizmo",
				Properties: map[string]any{},
				Traits:     []Trait{{Type: "scaler", Properties: map[string]any{}}},
			}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected settlement to reject \"scaler\" on component type \"gizmo\" (not webservice/worker), got nil error")
	}
	if !strings.Contains(err.Error(), "scaler") || !strings.Contains(err.Error(), "gizmo") {
		t.Fatalf("expected the restriction error to name the trait and component type, got: %v", err)
	}
}

// --- C2b: two registrars, one kind each ------------------------------------

// The same-value case — one concrete type satisfying both DocumentLoweringRule and
// RawDocumentLoweringRule — needs no test: the two LowerDocument signatures differ and
// Go forbids two methods of the same name on one type, so such a type cannot be
// written. Only two DIFFERENT types claiming one kind string can reach these guards.

func TestRegisterDocumentLowering_KindClaimedByRawRegistrar(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "K"})
	mustPanicContaining(t, "RegisterRawDocumentLowering", func() {
		tr.RegisterDocumentLowering(testDocRule{kind: "K"})
	})
}

func TestRegisterRawDocumentLowering_KindClaimedByDocRegistrar(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(testDocRule{kind: "K"})
	mustPanicContaining(t, "RegisterDocumentLowering", func() {
		tr.RegisterRawDocumentLowering(testRawRule{kind: "K"})
	})
}

// TestRegisterDocumentLowering_RejectsEmptyKind and
// TestRegisterRawDocumentLowering_RejectsEmptyKind are the regression tests for the
// Codex review finding F7: a DocumentLoweringRule/RawDocumentLoweringRule
// implementation with a bug returning "" from Kind() was accepted at registration,
// which then let ParseWithExtraTypes accept a document with an entirely missing
// `kind` field and dispatch it to that rule — replacing the caller's canonical
// "malformed document" error with whatever the buggy rule does instead.
func TestRegisterDocumentLowering_RejectsEmptyKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "empty kind", func() {
		tr.RegisterDocumentLowering(testDocRule{kind: ""})
	})
}

func TestRegisterRawDocumentLowering_RejectsEmptyKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "empty kind", func() {
		tr.RegisterRawDocumentLowering(testRawRule{kind: ""})
	})
}

func TestRegisterDocumentLowering_RejectsTerminalKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "terminal kind", func() {
		tr.RegisterDocumentLowering(testDocRule{kind: terminalDocumentKind})
	})
}

func TestRegisterRawDocumentLowering_RejectsTerminalKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "terminal kind", func() {
		tr.RegisterRawDocumentLowering(testRawRule{kind: terminalDocumentKind})
	})
}

func TestRegisterDocumentLowering_RejectsDuplicateKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(testDocRule{kind: "K"})
	mustPanicContaining(t, "already registered for kind K", func() {
		tr.RegisterDocumentLowering(testDocRule{kind: "K"})
	})
}

func TestRegisterRawDocumentLowering_RejectsDuplicateKind(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "K"})
	mustPanicContaining(t, "already registered for kind K", func() {
		tr.RegisterRawDocumentLowering(testRawRule{kind: "K"})
	})
}

// TestRegisterComponentLowering_RejectsEmptyType,
// TestRegisterTraitLowering_RejectsEmptyType and
// TestRegisterPolicyLowering_RejectsEmptyType are the round-9 Codex regression tests:
// RegisterDocumentLowering/RegisterRawDocumentLowering already reject a rule whose
// Kind() returns "" (TestRegisterDocumentLowering_RejectsEmptyKind above, Codex
// finding F7), but the three component/trait/policy-position registrars had no
// equivalent guard — a buggy rule returning "" from ComponentType()/TraitType()/
// PolicyType() was silently accepted and registered under the empty string.
func TestRegisterComponentLowering_RejectsEmptyType(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "empty type", func() {
		tr.RegisterComponentLowering(extraTypesComponentRule{typeName: ""})
	})
}

func TestRegisterTraitLowering_RejectsEmptyType(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "empty type", func() {
		tr.RegisterTraitLowering(extraTypesTraitRule{typeName: ""})
	})
}

func TestRegisterPolicyLowering_RejectsEmptyType(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "empty type", func() {
		tr.RegisterPolicyLowering(extraTypesPolicyRule{typeName: ""})
	})
}

// --- RegisterTraitLowering: CapabilityAware⇒ValidateAndApplyDefaults guard --------
//
// RegisterTrait (transform.go) panics at registration time if a dispatchable
// TraitHandler implements CapabilityAware without also implementing
// ValidateAndApplyDefaults, because EvaluateProfile's dispatch needs
// ValidateAndApplyDefaults to validate/default a capability-rendered binding before
// use. RegisterTraitLowering must enforce the identical guard for a TraitLoweringRule,
// since EvaluateProfile's trait-lowering-rule fallback (transform.go) has the same
// need and the same silent-unvalidated-acceptance failure mode otherwise.

// capAwareTraitLoweringRule implements TraitLoweringRule + CapabilityAware, but NOT
// ValidateAndApplyDefaults — the shape RegisterTraitLowering must now reject.
type capAwareTraitLoweringRule struct{ typ string }

func (r capAwareTraitLoweringRule) TraitType() string { return r.typ }
func (r capAwareTraitLoweringRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "ingress", Properties: map[string]any{}}}}, nil
}
func (r capAwareTraitLoweringRule) CapabilityRequired() bool { return true }

// capAwareTraitLoweringRuleWithVAD adds ValidateAndApplyDefaults to the above — the
// shape RegisterTraitLowering must continue to accept, exactly as ExposeRule does today.
type capAwareTraitLoweringRuleWithVAD struct {
	capAwareTraitLoweringRule
}

func (r capAwareTraitLoweringRuleWithVAD) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	return rendering, nil
}

func TestRegisterTraitLowering_PanicsIfCapabilityAwareWithoutVAD(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanicContaining(t, "implements CapabilityAware but not ValidateAndApplyDefaults", func() {
		tr.RegisterTraitLowering(capAwareTraitLoweringRule{typ: "reserving-trait"})
	})
}

func TestRegisterTraitLowering_CapabilityAwareWithVAD_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	r := capAwareTraitLoweringRuleWithVAD{capAwareTraitLoweringRule{typ: "reserving-trait"}}
	tr.RegisterTraitLowering(r)
	if _, ok := tr.traitLoweringRules["reserving-trait"]; !ok {
		t.Fatal("expected the rule to be registered")
	}
}

// TestLower_StrictCapabilities_MissingDefinitionRejected is the round-7 Codex
// regression test (lowering.go:815): applyTraits (transform.go) errors, under
// SetStrictCapabilities(true), when a custom (non-built-in) trait's capability
// rendering resolved in the profile but no CapabilityDefinition was loaded for it —
// but a TraitLoweringRule dispatch never runs through applyTraits, so it had no
// equivalent check and silently accepted the same situation. Register a custom
// CapabilityAware TraitLoweringRule, resolve its capability via the profile, enable
// strict mode, and load no CapabilityDefinition for the type: expect a rejection, not
// a silent merge.
func TestLower_StrictCapabilities_MissingDefinitionRejected(t *testing.T) {
	traitRule := capAwareTraitLoweringRuleWithVAD{capAwareTraitLoweringRule{typ: "needs-cap"}}

	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	tr.RegisterTraitLowering(traitRule)
	tr.SetStrictCapabilities(true)
	// Deliberately no SetCapabilityDefs call: no CapabilityDefinition is loaded for
	// "needs-cap", the exact gap this test proves is now rejected.

	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "needs-cap", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	caps := map[string]CapabilityBinding{
		"needs-cap": {Rendering: map[string]any{"key": "value"}},
	}

	_, err := tr.lower(app, TransformContext{Capabilities: caps})
	if err == nil {
		t.Fatal("expected a strict-capabilities rejection for a custom trait with no loaded CapabilityDefinition")
	}
	if !strings.Contains(err.Error(), "no CapabilityDefinition found for custom trait") {
		t.Fatalf("expected a missing-CapabilityDefinition error, got: %v", err)
	}
}

// TestLowerableTypes_ExcludesRawRegisteredKinds proves the other half of the mutual
// exclusion: a raw-registered kind has no LowerableTypes entry, so ParseWithExtraTypes
// can never be pointed at it and the in-transform parse gate cannot admit it.
func TestLowerableTypes_ExcludesRawRegisteredKinds(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterRawDocumentLowering(testRawRule{kind: "WebApplication"})
	tr.RegisterDocumentLowering(testDocRule{kind: "OrderedApplication"})

	lt := tr.LowerableTypes()
	if len(lt.Kinds) != 1 || lt.Kinds[0] != "OrderedApplication" {
		t.Fatalf("LowerableTypes().Kinds = %v, want exactly [OrderedApplication]", lt.Kinds)
	}
	for _, k := range lt.Kinds {
		if k == "WebApplication" {
			t.Fatal("a raw-registered kind must never appear in LowerableTypes")
		}
	}
}

// --- C2c: the shared fixpoint ----------------------------------------------

// TestLower_DepthExceeded_NamesTheDocument proves the depth guard fires after exactly
// MaxLoweringDepth rounds and attributes the failure to the document that kept
// expanding.
func TestLower_DepthExceeded_NamesTheDocument(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(loopyDocRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "LoopyDoc",
		Metadata:   Metadata{Name: "loopdoc"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected a depth-limit error")
	}
	var loweringErr *LoweringError
	if !stderrors.As(err, &loweringErr) {
		t.Fatalf("expected *LoweringError, got %T: %v", err, err)
	}
	if !stderrors.Is(loweringErr.Cause, ErrLoweringDepthExceeded) {
		t.Fatalf("expected ErrLoweringDepthExceeded, got: %v", loweringErr.Cause)
	}
	if len(loweringErr.Chain) != MaxLoweringDepth {
		t.Fatalf("expected the chain to record all %d rounds, got %d: %+v", MaxLoweringDepth, len(loweringErr.Chain), loweringErr.Chain)
	}
	if loweringErr.Origin.Document != "loopdoc" || loweringErr.Origin.DocumentKind != "LoopyDoc" {
		t.Fatalf("expected the error to name document %q (kind %q), got %+v", "loopdoc", "LoopyDoc", loweringErr.Origin)
	}
}

// --- G1: sealed traits must not be re-merged with capability rendering ------

// twoStageTraitRule is a TraitLoweringRule that claims fromType and emits a trait of
// toType, optionally capturing the trait it actually received so a test can assert
// whether capability rendering was merged into it.
type twoStageTraitRule struct {
	fromType, toType string
	emitProperties   map[string]any
	captured         *Trait
}

func (r *twoStageTraitRule) TraitType() string { return r.fromType }

func (r *twoStageTraitRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	if r.captured != nil {
		*r.captured = *trait
	}
	return LoweringResult{Traits: []Trait{{Type: r.toType, Properties: r.emitProperties}}}, nil
}

// TestLower_SealedTrait_NotReMergedWithCapabilityRendering is the regression test for
// G1 (Codex-bot wave 2): the trait-lowering-rule dispatch path in lowering.go merged
// capability rendering into a trait's Properties unconditionally, even when that trait
// had already been sealed by an earlier lowering round — violating the same D5
// information-closure invariant applyTraits (transform.go) already enforces for a
// dispatchable TraitHandler. A first rule emits a sealed trait of a type a SECOND rule
// claims; a capability is registered for that second type. The second rule must
// receive the trait's ORIGINAL properties, never capability-rendering-merged a second
// time.
func TestLower_SealedTrait_NotReMergedWithCapabilityRendering(t *testing.T) {
	var captured Trait
	stage1 := &twoStageTraitRule{
		fromType:       "stage1",
		toType:         "stage2",
		emitProperties: map[string]any{"orig": "value"},
	}
	stage2 := &twoStageTraitRule{
		fromType:       "stage2",
		toType:         "final",
		emitProperties: map[string]any{},
		captured:       &captured,
	}

	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"final": &stubTraitHandler{typ: "final"}},
	)
	tr.RegisterTraitLowering(stage1)
	tr.RegisterTraitLowering(stage2)

	comp := Component{
		Name:   "web",
		Type:   "webservice",
		Traits: []Trait{{Type: "stage1", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	caps := map[string]CapabilityBinding{
		"stage2": {Rendering: map[string]any{"injected": "leaked"}},
	}

	if _, err := tr.lower(app, TransformContext{Capabilities: caps}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	if _, leaked := captured.Properties["injected"]; leaked {
		t.Fatalf("stage2 rule received capability-rendering-merged properties on a sealed trait: %+v", captured.Properties)
	}
	if got, want := captured.Properties["orig"], "value"; got != want {
		t.Fatalf(`stage2 rule's captured trait.Properties["orig"] = %v, want %v`, got, want)
	}
	if !captured.sealed {
		t.Fatal("expected the trait passed to stage2 to be sealed=true (emitted by stage1's lowering round)")
	}
}

// componentWithNestedTraitRule is a component-position rule that emits a Component
// carrying an already-populated nested Trait — the shape a real rule uses when it
// hard-codes a terminal trait's final properties into the component it constructs,
// rather than emitting the trait through a registered TraitLoweringRule of its own.
type componentWithNestedTraitRule struct {
	nestedTraitType string
}

func (r componentWithNestedTraitRule) ComponentType() string { return "wraps-trait" }

func (r componentWithNestedTraitRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       comp.Name + "-web",
		Type:       "webservice",
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: r.nestedTraitType, Properties: map[string]any{"orig": "value"}}},
	}}}, nil
}

// TestLower_NestedTraitInEmittedComponent_IsSealed is the regression test for the
// round-5 Codex finding (lowering.go:712): a component-position rule cannot set the
// unexported Trait.sealed field itself (different package), so a terminal trait it
// hard-codes into an emitted component was, before this fix, indistinguishable from
// an authored one — leaving it to pick up a second, redundant capability-rendering
// merge in applyTraits (transform.go) if no TraitLoweringRule ever claims its type.
// Proves the engine now seals it itself, at emission, exactly as it already does for
// a trait a TraitLoweringRule returns directly (TestLower_SealedTrait_
// NotReMergedWithCapabilityRendering above).
func TestLower_NestedTraitInEmittedComponent_IsSealed(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"final": &stubTraitHandler{typ: "final"}},
	)
	tr.RegisterComponentLowering(componentWithNestedTraitRule{nestedTraitType: "final"})

	comp := Component{Name: "app", Type: "wraps-trait", Properties: map[string]any{}}
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("lower returned %d docs, want 1", len(got))
	}
	settled := got[0]
	if len(settled.Spec.Components) != 1 {
		t.Fatalf("settled components = %d, want 1", len(settled.Spec.Components))
	}
	web := settled.Spec.Components[0]
	if len(web.Traits) != 1 {
		t.Fatalf("emitted component's traits = %d, want 1", len(web.Traits))
	}
	nested := web.Traits[0]
	if !nested.sealed {
		t.Fatal("expected the nested trait to be sealed=true at emission")
	}
	if _, ok := nested.Origin(); !ok {
		t.Fatal("expected the nested trait to carry a stamped origin")
	}
}

// documentWithNestedTraitRule is a document-position rule that emits a fresh
// component carrying an already-populated nested Trait — the document-position
// analogue of componentWithNestedTraitRule above.
type documentWithNestedTraitRule struct {
	kind            string
	nestedTraitType string
}

func (r documentWithNestedTraitRule) Kind() string { return r.kind }

func (r documentWithNestedTraitRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "webservice",
			Properties: map[string]any{"image": "nginx"},
			Traits:     []Trait{{Type: r.nestedTraitType, Properties: map[string]any{"orig": "value"}}},
		}}},
	}}}, nil
}

// TestLower_NestedTraitInDocumentRuleOutput_IsSealed is the round-9-batch-2 Codex
// regression test (lowering.go:717): a DocumentLoweringRule emits a whole
// *Application, and — before this fix — only the document itself got an origin
// stamp; a terminal trait the rule hard-coded into one of the document's components
// was left unsealed, indistinguishable from an authored one, and would pick up a
// second, redundant capability-rendering merge in applyTraits (transform.go) if no
// TraitLoweringRule ever claimed its type. Proves the engine now seals it at
// document-rule emission too, exactly as TestLower_NestedTraitInEmittedComponent_
// IsSealed above proves for component-position emission.
func TestLower_NestedTraitInDocumentRuleOutput_IsSealed(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"final": &stubTraitHandler{typ: "final"}},
	)
	tr.RegisterDocumentLowering(documentWithNestedTraitRule{kind: "Wrapper", nestedTraitType: "final"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Wrapper",
		Metadata:   Metadata{Name: "myapp"},
	}

	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("lower returned %d docs, want 1", len(got))
	}
	settled := got[0]
	if len(settled.Spec.Components) != 1 {
		t.Fatalf("settled components = %d, want 1", len(settled.Spec.Components))
	}
	web := settled.Spec.Components[0]
	if len(web.Traits) != 1 {
		t.Fatalf("emitted component's traits = %d, want 1", len(web.Traits))
	}
	nested := web.Traits[0]
	if !nested.sealed {
		t.Fatal("expected the nested trait to be sealed=true at emission")
	}
	if _, ok := nested.Origin(); !ok {
		t.Fatal("expected the nested trait to carry a stamped origin")
	}
}

// documentWithNestedComponentAndPolicyRule is a document-position rule that emits a
// fresh component and a fresh policy, neither forwarded from doc — the round-11-
// batch-2 regression fixture: nested COMPONENTS and POLICIES, not just traits
// (documentWithNestedTraitRule above), left unstamped at document-rule emission.
type documentWithNestedComponentAndPolicyRule struct{ kind string }

func (r documentWithNestedComponentAndPolicyRule) Kind() string { return r.kind }

func (r documentWithNestedComponentAndPolicyRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx"},
			}},
			Policies: []ApplicationPolicy{{
				Name:       "pol",
				Type:       "widget-policy",
				Properties: map[string]any{},
			}},
		},
	}}}, nil
}

// TestLower_ComponentAndPolicyInDocumentRuleOutput_AreStamped is the round-11-batch-2
// Codex regression test (pullrequestreview-4937433461, flagging lowering.go:717 as
// reviewed at commit b9bfe94): a DocumentLoweringRule's emitted Application had only
// the Application ITSELF stamped with the authored origin (line 723); nested
// components and policies fell through to lowerDocumentBody's fallback derivation a
// round later, or — for an already-terminal component/policy no later round ever
// touches — were never stamped at all, so Origin() on the final settled output
// returned false, violating the "stamped once, copied verbatim onto every element it
// expands into at any depth" doctrine (Origin's own doc comment). Proves both are now
// stamped at document-rule emission time, with the authored document identity
// preserved (Document/DocumentKind/Namespace) and their own Component/PolicyName
// filled in.
func TestLower_ComponentAndPolicyInDocumentRuleOutput_AreStamped(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	tr.RegisterPolicy("widget-policy", &stubPolicyHandler{typ: "widget-policy"})
	tr.RegisterDocumentLowering(documentWithNestedComponentAndPolicyRule{kind: "Wrapper"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Wrapper",
		Metadata:   Metadata{Name: "myapp", Namespace: "team-a"},
	}

	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("lower returned %d docs, want 1", len(got))
	}
	settled := got[0]

	if len(settled.Spec.Components) != 1 {
		t.Fatalf("settled components = %d, want 1", len(settled.Spec.Components))
	}
	comp := settled.Spec.Components[0]
	compOrigin, ok := comp.Origin()
	if !ok {
		t.Fatal("expected the emitted component to carry a stamped origin")
	}
	if compOrigin.Document != "myapp" || compOrigin.DocumentKind != "Wrapper" || compOrigin.Namespace != "team-a" {
		t.Errorf("component origin = %+v, want authored document identity (myapp/Wrapper/team-a)", compOrigin)
	}
	if compOrigin.Component != "web" || compOrigin.ComponentType != "webservice" {
		t.Errorf("component origin = %+v, want Component=web ComponentType=webservice", compOrigin)
	}

	if len(settled.Spec.Policies) != 1 {
		t.Fatalf("settled policies = %d, want 1", len(settled.Spec.Policies))
	}
	pol := settled.Spec.Policies[0]
	polOrigin, ok := pol.Origin()
	if !ok {
		t.Fatal("expected the emitted policy to carry a stamped origin")
	}
	if polOrigin.Document != "myapp" || polOrigin.DocumentKind != "Wrapper" || polOrigin.Namespace != "team-a" {
		t.Errorf("policy origin = %+v, want authored document identity (myapp/Wrapper/team-a)", polOrigin)
	}
	if polOrigin.PolicyName != "pol" {
		t.Errorf("policy origin = %+v, want PolicyName=pol", polOrigin)
	}
}

// forwardingComponentRule is a component-position rule that retypes a component but
// preserves whatever traits were already attached to it, using the natural
// `Traits: comp.Traits` idiom described in the round-7 Codex finding (lowering.go:945).
type forwardingComponentRule struct{ fromType, toType string }

func (r forwardingComponentRule) ComponentType() string { return r.fromType }

func (r forwardingComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       comp.Name,
		Type:       r.toType,
		Properties: map[string]any{"image": "nginx"},
		Traits:     comp.Traits,
	}}}, nil
}

// TestLower_ForwardedAuthoredTrait_NotSealed is the round-7 Codex regression test
// (lowering.go:945): a component-position rule that preserves attached authored
// traits via `Traits: comp.Traits` must not have those traits swept up by
// sealEmittedNestedTraits. Sealing skips ALL capability processing — both the
// engine's own CapabilityAware/ErrMissingCapability check here (lowering.go) and
// applyTraits' capability-rendering merge (transform.go) — so a forwarded
// CapabilityAware trait such as expose would silently never receive its
// ClusterProfile rendering or its required-capability enforcement.
//
// Proof: register the forwarded trait's type against a CapabilityAware
// TraitLoweringRule with no matching capability in the context. Before the fix, the
// forwarded trait is sealed at round 0, so round 1's CapabilityRequired check is
// skipped entirely and lower() succeeds. After the fix, the trait is left unsealed,
// the check runs, and lower() fails with ErrMissingCapability.
func TestLower_ForwardedAuthoredTrait_NotSealed(t *testing.T) {
	traitRule := capAwareTraitLoweringRuleWithVAD{capAwareTraitLoweringRule{typ: "needs-cap"}}

	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	tr.RegisterComponentLowering(forwardingComponentRule{fromType: "wrapper", toType: "webservice"})
	tr.RegisterTraitLowering(traitRule)

	comp := Component{
		Name:   "app",
		Type:   "wrapper",
		Traits: []Trait{{Type: "needs-cap", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	_, err := tr.lower(app, TransformContext{}) // no capabilities registered
	if err == nil {
		t.Fatal("expected ErrMissingCapability: a forwarded authored trait must still undergo normal capability enforcement, not be silently sealed past it")
	}
	if !stderrors.Is(err, ErrMissingCapability) {
		t.Fatalf("expected ErrMissingCapability, got: %v", err)
	}
}

// renamingForwardingComponentRule renames a component (both Name and Type) while
// forwarding its authored Traits unchanged via `Traits: comp.Traits` — the same
// idiom forwardingComponentRule above uses, but renaming Name too, so the round-1
// trait-origin fallback (below) has a component identity that actually differs from
// the original authored one to get wrong.
type renamingForwardingComponentRule struct{ fromType, toName, toType string }

func (r renamingForwardingComponentRule) ComponentType() string { return r.fromType }

func (r renamingForwardingComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       r.toName,
		Type:       r.toType,
		Properties: map[string]any{"image": "nginx"},
		Traits:     comp.Traits,
	}}}, nil
}

// originCaptureTraitRule records the LoweringContext.Origin handed to LowerTrait.
type originCaptureTraitRule struct {
	typ  string
	seen *Origin
}

func (r originCaptureTraitRule) TraitType() string { return r.typ }

func (r originCaptureTraitRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	*r.seen = lctx.Origin
	return LoweringResult{Traits: []Trait{{Type: "expose", Properties: map[string]any{}}}}, nil
}

// TestLower_ForwardedTraitOrigin_KeepsOriginalComponentIdentity is the round-9 Codex
// regression test (lowering.go, the trait-origin fallback in lowerDocumentBody): a
// forwarded trait (sealEmittedNestedTraits' carve-out above deliberately leaves it
// unstamped, so trait.Origin() returns !ok on the round it is reprocessed) must fall
// back to the component's already-resolved AUTHORED origin (compOrigin, itself
// falling back to comp.Origin() first), not to the CURRENT component's possibly-
// renamed Name/Type. Before the fix, a component rule that renames a component while
// forwarding its traits unchanged (exactly renamingForwardingComponentRule below)
// makes the forwarded trait's Origin.Component/ComponentType report the SYNTHESIZED
// identity ("app-renamed"/"webservice") instead of the authored one ("app"/"wrapper")
// — violating the Origin doctrine's "authored location first" rule for exactly the
// case it exists to cover.
func TestLower_ForwardedTraitOrigin_KeepsOriginalComponentIdentity(t *testing.T) {
	var seen Origin
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(renamingForwardingComponentRule{fromType: "wrapper", toName: "app-renamed", toType: "webservice"})
	tr.RegisterTraitLowering(originCaptureTraitRule{typ: "probe-trait", seen: &seen})

	comp := Component{
		Name:   "app",
		Type:   "wrapper",
		Traits: []Trait{{Type: "probe-trait", Properties: map[string]any{}}},
	}
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind

	if _, err := tr.lower(app, TransformContext{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if seen.Component != "app" {
		t.Errorf("Origin.Component = %q, want the authored name %q (not the renamed %q)", seen.Component, "app", "app-renamed")
	}
	if seen.ComponentType != "wrapper" {
		t.Errorf("Origin.ComponentType = %q, want the authored type %q (not the renamed %q)", seen.ComponentType, "wrapper", "webservice")
	}
}

// malformedNestedComponentDocRule emits an Application whose one component already
// carries a lowerable type (needs-image, registered separately as a
// ComponentLoweringRule below) with malformed properties — a document-position rule
// emitting an already-typed nested element directly, rather than a component-position
// rule emitting it at its own position.
type malformedNestedComponentDocRule struct{ kind string }

func (r malformedNestedComponentDocRule) Kind() string { return r.kind }

func (r malformedNestedComponentDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "needs-image",
			Properties: map[string]any{}, // missing required "image"
		}}},
	}}}, nil
}

// TestLower_DocumentRuleEmission_ValidatesNestedComponentSchema is the round-9 Codex
// regression test (lowering.go:710): a DocumentLoweringRule/RawDocumentLoweringRule
// hands back a whole *Application, and until this fix nothing ever validated its
// nested components/traits/policies against their own handler's PropertySchema —
// validatePositionResult only checks arity, and validateSettled (post-fixpoint) only
// checks identity/type allowlists. A component arriving already lowerable-typed
// inside a document rule's emission — rather than being emitted at its own
// component-position — silently bypassed the same emission-time check
// validateEmittedComponent already applies at the component and trait positions.
func TestLower_DocumentRuleEmission_ValidatesNestedComponentSchema(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(malformedNestedComponentDocRule{kind: "Wrapper"})
	tr.RegisterComponentLowering(requiredSchemaComponentLoweringRule{typ: "needs-image"})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Wrapper",
		Metadata:   Metadata{Name: "myapp"},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected the document rule's emitted nested component to be validated against its schema and rejected")
	}
	if !strings.Contains(err.Error(), `"image" is required`) {
		t.Errorf("expected a required-field error, got: %v", err)
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// componentAndPolicyRule is a component-position rule that emits both a
// Component and a Policy in one call — a position loweringPositionRules
// explicitly permits (PositionComponent: {components: true, policies: true}).
type componentAndPolicyRule struct{}

func (componentAndPolicyRule) ComponentType() string { return "web-and-db" }
func (componentAndPolicyRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{
		Components: []Component{{Name: comp.Name + "-web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
		Policies:   []ApplicationPolicy{{Name: comp.Name + "-db-order", Type: "dependency", Properties: map[string]any{"rules": []any{}}}},
	}, nil
}

// TestLower_ComponentStep_RecordsEmittedPolicyNames is the round-3 Codex
// regression: a component-position rule's LoweringStep.To was built only from
// result.Components, silently dropping any result.Policies it also emitted —
// making the recorded expansion chain understate what the rule actually did.
func TestLower_ComponentStep_RecordsEmittedPolicyNames(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(componentAndPolicyRule{})
	doc := makeApp("myapp", Component{Name: "shop", Type: "web-and-db", Properties: map[string]any{}})
	doc.APIVersion = SupportedAPIVersion
	doc.Kind = terminalDocumentKind

	_, steps, err := tr.lowerDocumentBody(doc, TransformContext{}, newNameAllocator(), 0)
	if err != nil {
		t.Fatalf("lowerDocumentBody: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d: %+v", len(steps), steps)
	}
	to := steps[0].To
	if !containsName(to, "shop-web") {
		t.Errorf("step.To = %v, missing emitted component name %q", to, "shop-web")
	}
	if !containsName(to, "shop-db-order") {
		t.Errorf("step.To = %v, missing emitted policy name %q", to, "shop-db-order")
	}
}

// traitEmitsEverythingRule is a trait-position rule that emits a Trait, a
// Component, and a Policy in one call — all three are permitted at
// PositionTrait.
type traitEmitsEverythingRule struct{}

func (traitEmitsEverythingRule) TraitType() string { return "provision" }
func (traitEmitsEverythingRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{
		Traits:     []Trait{{Type: "expose", Properties: map[string]any{}}},
		Components: []Component{{Name: "sidecar", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
		Policies:   []ApplicationPolicy{{Name: "provision-order", Type: "dependency", Properties: map[string]any{"rules": []any{}}}},
	}, nil
}

// TestLower_TraitStep_RecordsEmittedComponentAndPolicyNames is the round-3
// Codex regression for the trait-position analogue: LoweringStep.To was built
// only from result.Traits, silently dropping any result.Components/
// result.Policies the rule also emitted.
func TestLower_TraitStep_RecordsEmittedComponentAndPolicyNames(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(traitEmitsEverythingRule{})
	comp := Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "provision", Properties: map[string]any{}}}}
	doc := makeApp("myapp", comp)
	doc.APIVersion = SupportedAPIVersion
	doc.Kind = terminalDocumentKind

	_, steps, err := tr.lowerDocumentBody(doc, TransformContext{}, newNameAllocator(), 0)
	if err != nil {
		t.Fatalf("lowerDocumentBody: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d: %+v", len(steps), steps)
	}
	to := steps[0].To
	if !containsName(to, "expose") {
		t.Errorf("step.To = %v, missing emitted trait type %q", to, "expose")
	}
	if !containsName(to, "sidecar") {
		t.Errorf("step.To = %v, missing emitted component name %q", to, "sidecar")
	}
	if !containsName(to, "provision-order") {
		t.Errorf("step.To = %v, missing emitted policy name %q", to, "provision-order")
	}
}

// addsSiblingComponentRule expands one component into itself plus a NEW sibling
// ("extra"), so newComponents (lowerDocumentBody's local accumulator) differs from
// doc.Spec.Components as it stood at the START of this round.
type addsSiblingComponentRule struct{}

func (addsSiblingComponentRule) ComponentType() string { return "adds-sibling" }
func (addsSiblingComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{
		{Name: comp.Name, Type: "webservice", Properties: map[string]any{"image": "nginx"}},
		{Name: "extra", Type: "webservice", Properties: map[string]any{"image": "nginx"}},
	}}, nil
}

// policySnapshotRule records the component names it observes via lctx.Document —
// the same *Application pointer every rule in the round is handed — so the test can
// tell whether it saw the PRE-round or the just-committed component list.
type policySnapshotRule struct {
	seen *[]string
}

func (r policySnapshotRule) PolicyType() string { return "snapshot" }
func (r policySnapshotRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	for _, c := range lctx.Document.Spec.Components {
		*r.seen = append(*r.seen, c.Name)
	}
	return LoweringResult{Policies: []ApplicationPolicy{{Name: "settled", Type: "noop-settled", Properties: map[string]any{}}}}, nil
}

// TestLower_PolicyRule_SeesPreRoundComponentSnapshot is the round-4 Codex regression:
// a component-position rule and a policy-position rule firing in the SAME round must
// see the SAME document snapshot via lctx.Document — the one that stood at the start
// of the round — not have the policy rule observe components the component rule just
// emitted THIS round while the component rule itself saw the round's starting state.
func TestLower_PolicyRule_SeesPreRoundComponentSnapshot(t *testing.T) {
	var seen []string
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(addsSiblingComponentRule{})
	tr.RegisterPolicyLowering(policySnapshotRule{seen: &seen})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "adds-sibling", Properties: map[string]any{}}},
			Policies:   []ApplicationPolicy{{Name: "pol1", Type: "snapshot", Properties: map[string]any{}}},
		},
	}

	if _, err := tr.lower(app, TransformContext{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(seen) != 1 || seen[0] != "web" {
		t.Fatalf("policy rule saw components %v, want exactly [\"web\"] (the pre-round snapshot) — not the sibling \"extra\" the component rule emitted this same round", seen)
	}
}
