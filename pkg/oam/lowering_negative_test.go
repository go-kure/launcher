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

// duplicateSiblingNameRule emits TWO components from one LowerComponent call, and
// (buggily) calls lctx.Namer.Name with the identical base/suffix for both — the
// within-one-invocation sibling collision F4 covers, distinct from colliderRule's
// cross-origin collision above.
type duplicateSiblingNameRule struct{}

func (duplicateSiblingNameRule) ComponentType() string { return "duplicator" }

func (duplicateSiblingNameRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	name1, err := lctx.Namer.Name("dup", "child", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	name2, err := lctx.Namer.Name("dup", "child", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{
		{Name: name1, Type: "webservice", Properties: map[string]any{"image": "nginx"}},
		{Name: name2, Type: "webservice", Properties: map[string]any{"image": "nginx"}},
	}}, nil
}

// TestLower_DuplicateSiblingName_Rejected is the regression test for the Codex review
// finding F4: before the fix, NameAllocator.Reserve treated two Reserve calls sharing
// one Origin as an idempotent no-op regardless of when they happened, so a rule that
// (by bug) generated the identical name for two DIFFERENT emitted siblings — sharing
// one Origin, since LoweringResult carries no per-sibling discriminator — silently
// succeeded instead of failing as a real collision.
func TestLower_DuplicateSiblingName_Rejected(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(duplicateSiblingNameRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "dup-me", Type: "duplicator", Properties: map[string]any{}}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected a same-round sibling name collision error")
	}
	if !strings.Contains(err.Error(), "same lowering round") {
		t.Fatalf("expected a same-round collision message, got: %v", err)
	}
}

// crossRoundRootRule emits two siblings from one LowerComponent call: a leaf that
// reserves a generated name immediately (round N), and a "cross-round-chain"-typed
// placeholder that crossRoundChainRule picks up next round and reserves the SAME
// generated name for. Both siblings share the identical Origin — the one call that
// emitted them — so their name claims land in different rounds even though they are
// different elements: the round-3 Codex finding this pair reproduces.
type crossRoundRootRule struct{}

func (crossRoundRootRule) ComponentType() string { return "cross-round-root" }

func (crossRoundRootRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	leafName, err := lctx.Namer.Name("dup", "child", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{
		{Name: leafName, Type: "webservice", Properties: map[string]any{"image": "nginx"}},
		{Name: "placeholder", Type: "cross-round-chain", Properties: map[string]any{}},
	}}, nil
}

type crossRoundChainRule struct{}

func (crossRoundChainRule) ComponentType() string { return "cross-round-chain" }

func (crossRoundChainRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	name, err := lctx.Namer.Name("dup", "child", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Components: []Component{{Name: name, Type: "webservice", Properties: map[string]any{"image": "nginx"}}}}, nil
}

// TestLower_CrossRoundSiblingNameCollision_Rejected is the round-3 Codex regression:
// two siblings emitted from ONE origin (LoweringResult carries no per-sibling
// discriminator) that independently reserve the SAME generated name in DIFFERENT
// rounds must still be rejected as a collision — not silently accepted as "the same
// conceptual element re-affirming its name", which is what NameAllocator.Reserve did
// before this fix whenever the round differed.
func TestLower_CrossRoundSiblingNameCollision_Rejected(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(crossRoundRootRule{})
	tr.RegisterComponentLowering(crossRoundChainRule{})

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "root", Type: "cross-round-root", Properties: map[string]any{}}},
		},
	}

	_, err := tr.lower(app, TransformContext{})
	if err == nil {
		t.Fatal("expected a cross-round sibling name collision error")
	}
	if !strings.Contains(err.Error(), "earlier lowering round") {
		t.Fatalf("expected a cross-round collision message, got: %v", err)
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

// chainDepthRule is a component-lowering rule for exactly one type in a fixed chain
// "chain-0" -> "chain-1" -> ... -> "chain-6" -> "webservice". Each hop is a genuine
// expansion (changed=true); "webservice" is terminal (no lowering rule registered
// for it), so the chain settles there with no further change.
type chainDepthRule struct{ depth int }

func (r chainDepthRule) ComponentType() string { return "chain-" + strconv.Itoa(r.depth) }

func (r chainDepthRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	next := "chain-" + strconv.Itoa(r.depth+1)
	if r.depth+1 == wantRealExpansions {
		next = "webservice"
	}
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: next, Properties: map[string]any{"image": "nginx"}}}}, nil
}

// wantRealExpansions is deliberately a literal, not MaxLoweringDepth-1: this test's
// whole point is proving the engine supports 8 genuine expansion rounds regardless of
// how the production constant is currently set, so it must not silently track a
// regression in that constant.
const wantRealExpansions = 8

// TestLower_ExactlyMaxRealExpansions_Settles is the round-7 Codex regression test
// (lowering.go:522): a chain performing exactly wantRealExpansions genuine
// expansions — the terminal element emitted on the last of those rounds — must still
// succeed. The engine needs one further round after that last expansion purely to
// observe that nothing changed; before the fix, the depth guard fired on exactly that
// observing round instead of letting it run, rejecting a chain that never actually
// exceeded the advertised budget.
func TestLower_ExactlyMaxRealExpansions_Settles(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		nil,
	)
	for d := 0; d < wantRealExpansions; d++ {
		tr.RegisterComponentLowering(chainDepthRule{depth: d})
	}

	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "myapp"},
		Spec: ApplicationSpec{
			Components: []Component{{Name: "web", Type: "chain-0", Properties: map[string]any{}}},
		},
	}

	got, err := tr.lower(app, TransformContext{})
	if err != nil {
		t.Fatalf("lower: a chain of exactly %d real expansions must settle, not exceed the depth budget: %v", wantRealExpansions, err)
	}
	if len(got) != 1 || len(got[0].Spec.Components) != 1 || got[0].Spec.Components[0].Type != "webservice" {
		t.Fatalf("expected the chain to settle on a single webservice component, got: %+v", got)
	}
}
