package oam

import (
	stderrors "errors"
	"strconv"
	"strings"
	"testing"
)

// colliderRule always claims the same generated name ("shared-child") regardless of
// which component invoked it, so two "collider" components in one document force a
// NameAllocator collision.
type colliderRule struct{}

func (colliderRule) ComponentType() string { return "collider" }

func (colliderRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	name, err := lctx.Namer.Name("shared", "child", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: name, Type: "webservice", Properties: map[string]any{"image": "nginx"}}}}, nil
}

// TestLower_NameCollision_NamesBothOrigins is the negative-test proof that a
// generated-name collision fails with BOTH the origin that claimed the name first
// and the origin that collided with it, not just one.
func TestLower_NameCollision_NamesBothOrigins(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(colliderRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "a", Type: "collider", Properties: map[string]any{}},
				{Name: "b", Type: "collider", Properties: map[string]any{}},
			},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected a name collision error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `component "a"`) || !strings.Contains(msg, `component "b"`) {
		t.Fatalf("expected the error to name both colliding origins (component \"a\" and component \"b\"), got: %v", msg)
	}
}

// loopyRule always re-emits a "loopy" component, so it never reaches a fixpoint —
// proving the MaxLoweringDepth guard actually fires rather than looping forever.
type loopyRule struct{}

func (loopyRule) ComponentType() string { return "loopy" }

func (loopyRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "loopy", Properties: comp.Properties}}}, nil
}

// TestLower_DepthLimit_PrintsFullChain is the negative-test proof that exceeding
// MaxLoweringDepth fails with ErrLoweringDepthExceeded and the chain records every
// round up to the limit.
func TestLower_DepthLimit_PrintsFullChain(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(loopyRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "loop", Type: "loopy", Properties: map[string]any{}}},
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
	msg := loweringErr.Error()
	for round := range MaxLoweringDepth {
		want := "round " + strconv.Itoa(round) + ":"
		if !strings.Contains(msg, want) {
			t.Fatalf("expected the error to print %q, got: %v", want, msg)
		}
	}
}

// policyEmitsTraitRule is a policy-position rule that illegally tries to emit a
// Trait — only the Policies slice is allowed at the policy position
// (loweringPositionRules).
type policyEmitsTraitRule struct{}

func (policyEmitsTraitRule) PolicyType() string { return "illegal-policy" }

func (policyEmitsTraitRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "ingress", Properties: map[string]any{}}}}, nil
}

// TestLower_IllegalPositionEmission_Rejected is the negative-test proof that a rule
// emitting into a slice its position does not permit is rejected by
// validatePositionResult before the illegal element ever reaches the document.
func TestLower_IllegalPositionEmission_Rejected(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicyLowering(policyEmitsTraitRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx"}}},
			Policies:   []ApplicationPolicy{{Name: "bad", Type: "illegal-policy", Properties: map[string]any{}}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected an illegal-position-emission error")
	}
	if !strings.Contains(err.Error(), "policy-position rule may not emit Traits") {
		t.Fatalf("expected a policy-position/Traits rejection message, got: %v", err)
	}
}
