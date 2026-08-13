package oam

import (
	"errors"
	"testing"
)

// renamingTraitRule is a TraitLoweringRule that renames its claimed trait type into
// another terminal trait type (no property changes). It exists only to prove the
// authoredTraitTypes capture in TransformWithPolicy (the C6 review Critical fix):
// a Policy capability constraint must be evaluated against what the human wrote
// (from), never what a lowering rule renamed it to (to).
type renamingTraitRule struct{ from, to string }

func (r renamingTraitRule) TraitType() string { return r.from }

func (r renamingTraitRule) LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: r.to, Properties: map[string]any{}}}}, nil
}

// TestTransform_CapabilityConstraint_EvaluatesAuthoredTraitType_NotLowered is the
// regression test for the Critical review finding on Task C6: TransformWithPolicy
// wired t.lower() in but originally called collectTraitTypes(app) AFTER lowering had
// already reassigned app, so a Policy constraining trait types silently evaluated
// against the LOWERED type instead of the authored one. A ForbiddenCapabilities
// entry naming the authored type (here "expose", lowered to "ingress" by
// renamingTraitRule) must still be caught; a ForbiddenCapabilities entry naming only
// the lowered type ("ingress") must NOT catch an authored "expose" trait — the
// Policy is meant to police what the human wrote, not a synthesized detail.
func TestTransform_CapabilityConstraint_EvaluatesAuthoredTraitType_NotLowered(t *testing.T) {
	newTransformer := func() *Transformer {
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
			map[string]TraitHandler{"ingress": &stubTraitHandler{typ: "ingress"}},
		)
		tr.RegisterTraitLowering(renamingTraitRule{from: "expose", to: "ingress"})
		return tr
	}
	appWithExpose := func() *Application {
		comp := Component{
			Name:   "web",
			Type:   "webservice",
			Traits: []Trait{{Type: "expose", Properties: map[string]any{}}},
		}
		app := makeApp("myapp", comp)
		// lower() runs validateSettled (D4) once a rule is registered, which requires
		// a real apiVersion/kind — makeApp leaves both zero, fine for the no-lowering
		// tests elsewhere in this package but not here.
		app.APIVersion = SupportedAPIVersion
		app.Kind = terminalDocumentKind
		return app
	}

	t.Run("forbidding the authored type still rejects it after lowering renames it", func(t *testing.T) {
		tr := newTransformer()
		policy := &constrainedPolicy{forbidden: []string{"expose"}}
		_, err := tr.Transform(appWithExpose(), TransformContext{Policy: policy})
		if err == nil {
			t.Fatal("expected ViolationError: authored trait type \"expose\" is forbidden")
		}
		var ve *ViolationError
		if !errors.As(err, &ve) {
			t.Errorf("expected ViolationError, got %T: %v", err, err)
		}
	})

	t.Run("forbidding only the lowered type does not falsely reject the authored trait", func(t *testing.T) {
		tr := newTransformer()
		policy := &constrainedPolicy{forbidden: []string{"ingress"}}
		_, err := tr.Transform(appWithExpose(), TransformContext{Policy: policy})
		if err != nil {
			t.Fatalf("expected no error: the authored type is %q, not %q — got: %v", "expose", "ingress", err)
		}
	})
}
