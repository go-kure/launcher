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
