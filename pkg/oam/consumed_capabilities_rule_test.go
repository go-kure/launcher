package oam

import (
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- PolicyResult.ConsumedCapabilities, rule-side reads (go-kure/launcher#686) ---
//
// Each capReadXxxRule reads one capability through LoweringContext.Capability at its
// own position, plus "absent-cap", which the profile does not bind.

type capReadComponentRule struct{}

func (capReadComponentRule) ComponentType() string { return "cap-read" }

func (capReadComponentRule) LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error) {
	lctx.Capability("component-cap")
	lctx.Capability("absent-cap")
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "webservice"}}}, nil
}

type capReadTraitRule struct{}

func (capReadTraitRule) TraitType() string { return "cap-read" }

func (capReadTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	lctx.Capability("trait-cap")
	lctx.Capability("absent-cap")
	return LoweringResult{Traits: []Trait{{Type: "cap-read-done", Properties: map[string]any{}}}}, nil
}

type capReadDocRule struct{}

func (capReadDocRule) Kind() string { return "CapRead" }

func (capReadDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	lctx.Capability("document-cap")
	lctx.Capability("absent-cap")
	comp := doc.Spec.Components[0]
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: []Component{{Name: comp.Name, Type: comp.Type}}},
	}}}, nil
}

type capReadPolicyRule struct{}

func (capReadPolicyRule) PolicyType() string { return "cap-read" }

func (capReadPolicyRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	lctx.Capability("policy-cap")
	lctx.Capability("absent-cap")
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "cap-read-done"}}}, nil
}

type capReadDoneTraitHandler struct{}

func (capReadDoneTraitHandler) CanHandle(t string) bool { return t == "cap-read-done" }
func (capReadDoneTraitHandler) Apply(_ *Trait, _ *stack.Application, _ *stack.Bundle) error {
	return nil
}

type capReadDonePolicyHandler struct{}

func (capReadDonePolicyHandler) CanHandle(t string) bool { return t == "cap-read-done" }
func (capReadDonePolicyHandler) Apply(_ *ApplicationPolicy, _ []string, _ *PolicyResult) error {
	return nil
}

// capReadProfile binds a capability for every position, and one no rule reads.
func capReadProfile() map[string]CapabilityBinding {
	caps := map[string]CapabilityBinding{}
	for _, k := range []string{"component-cap", "trait-cap", "document-cap", "policy-cap", "unread-cap"} {
		caps[k] = CapabilityBinding{Rendering: map[string]any{"k": k}}
	}
	return caps
}

// TestConsumedCapabilities_RuleReads is go-kure/launcher#686: a capability a lowering
// rule reads through LoweringContext.Capability is listed in ConsumedCapabilities at
// each in-transform rule position, exactly as a trait's resolved key is. A key the
// profile does not bind, and a bound key nothing reads, are not.
func TestConsumedCapabilities_RuleReads(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Transformer)
		app      func() *Application
		want     []string
	}{
		{
			name:     "component rule",
			register: func(tr *Transformer) { tr.RegisterComponentLowering(capReadComponentRule{}) },
			app: func() *Application {
				return singleComponentApp(terminalDocumentKind, "cap-read", nil)
			},
			want: []string{"component-cap"},
		},
		{
			name:     "trait rule",
			register: func(tr *Transformer) { tr.RegisterTraitLowering(capReadTraitRule{}) },
			app: func() *Application {
				app := singleComponentApp(terminalDocumentKind, "webservice", nil)
				app.Spec.Components[0].Traits = []Trait{{Type: "cap-read", Properties: map[string]any{}}}
				return app
			},
			want: []string{"trait-cap"},
		},
		{
			name:     "document rule",
			register: func(tr *Transformer) { tr.RegisterDocumentLowering(capReadDocRule{}) },
			app: func() *Application {
				return singleComponentApp("CapRead", "webservice", nil)
			},
			want: []string{"document-cap"},
		},
		{
			name:     "policy rule",
			register: func(tr *Transformer) { tr.RegisterPolicyLowering(capReadPolicyRule{}) },
			app: func() *Application {
				app := singleComponentApp(terminalDocumentKind, "webservice", nil)
				app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "cap-read"}}
				return app
			},
			want: []string{"policy-cap"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"cap-read-done": capReadDoneTraitHandler{}},
			)
			tr.RegisterPolicy("cap-read-done", capReadDonePolicyHandler{})
			tc.register(tr)
			app := tc.app()
			app.APIVersion = SupportedAPIVersion

			_, result, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: capReadProfile()})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(result.ConsumedCapabilities, tc.want) {
				t.Errorf("ConsumedCapabilities = %v, want %v", result.ConsumedCapabilities, tc.want)
			}

			// The plain Transform path collects nothing, and a rule's read there must not
			// fail for want of a set to record into.
			if _, err := tr.Transform(tc.app(), TransformContext{Capabilities: capReadProfile()}); err != nil {
				t.Fatalf("Transform: unexpected error: %v", err)
			}
		})
	}
}

// TestLoweringContext_Capability covers the accessor itself: a bound key is returned
// and recorded, an unbound one is neither, and a context without a set to record into
// (WithCapabilities, LowerRaws) still reads.
func TestLoweringContext_Capability(t *testing.T) {
	caps := capReadProfile()
	consumed := map[string]struct{}{}
	lctx := LoweringContext{capabilities: caps, consumed: consumed}

	got, ok := lctx.Capability("trait-cap")
	if !ok || got.Rendering["k"] != "trait-cap" {
		t.Fatalf("Capability(trait-cap) = %v, %v; want the bound value", got, ok)
	}
	if _, ok := lctx.Capability("absent-cap"); ok {
		t.Fatal("Capability(absent-cap) reported a binding the profile does not have")
	}
	if _, recorded := consumed["trait-cap"]; !recorded || len(consumed) != 1 {
		t.Errorf("consumed = %v, want exactly trait-cap", consumed)
	}

	driven := lctx.WithCapabilities(map[string]CapabilityBinding{"x": {Rendering: map[string]any{"a": "b"}}})
	if got, ok := driven.Capability("x"); !ok || got.Rendering["a"] != "b" {
		t.Fatalf("WithCapabilities: Capability(x) = %v, %v; want the supplied value", got, ok)
	}
	if _, ok := driven.Capability("trait-cap"); ok {
		t.Error("WithCapabilities must replace the capabilities, not merge them")
	}
	if len(consumed) != 1 {
		t.Errorf("a read through WithCapabilities' copy was recorded in the original set: %v", consumed)
	}
	if _, ok := (LoweringContext{}).Capability("x"); ok {
		t.Error("a zero LoweringContext has no capabilities")
	}
}
