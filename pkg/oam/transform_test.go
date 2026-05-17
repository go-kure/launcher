package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- stub handlers for tests ---

type stubComponentHandler struct{ typ string }

func (h *stubComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubComponentHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return nil, nil
}

type stubTraitHandler struct{ typ string }

func (h *stubTraitHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubTraitHandler) Apply(_ *Trait, _ *stack.Application, _ *stack.Bundle) error {
	return nil
}

type capAwareHandler struct {
	stubTraitHandler
}

func (h *capAwareHandler) CapabilityRequired() bool { return true }

type capAwareWithVAD struct {
	stubTraitHandler
}

func (h *capAwareWithVAD) CapabilityRequired() bool { return true }
func (h *capAwareWithVAD) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	return rendering, nil
}

type stubPolicyHandler struct{ typ string }

func (h *stubPolicyHandler) CanHandle(t string) bool { return t == h.typ }
func (h *stubPolicyHandler) Apply(_ *ApplicationPolicy, _ []string, _ *PolicyResult) error {
	return nil
}

// mustPanic asserts that f panics.
func mustPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got none")
		}
	}()
	f()
}

// --- NewTransformer ---

func TestNewTransformer_Empty(t *testing.T) {
	// Both nil and empty maps are valid.
	tr1 := NewTransformer(nil, nil)
	if tr1.findComponentHandler("any") != nil {
		t.Error("expected nil for unregistered component handler")
	}
	if tr1.findTraitHandler("any") != nil {
		t.Error("expected nil for unregistered trait handler")
	}

	tr2 := NewTransformer(map[string]ComponentHandler{}, map[string]TraitHandler{})
	if tr2.findComponentHandler("any") != nil {
		t.Error("expected nil for unregistered component handler (empty map)")
	}
}

func TestNewTransformer_WithHandlers(t *testing.T) {
	ch := &stubComponentHandler{typ: "webservice"}
	th := &stubTraitHandler{typ: "expose"}

	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": ch},
		map[string]TraitHandler{"expose": th},
	)

	if got := tr.findComponentHandler("webservice"); got != ch {
		t.Errorf("findComponentHandler(webservice) = %v, want %v", got, ch)
	}
	if got := tr.findTraitHandler("expose"); got != th {
		t.Errorf("findTraitHandler(expose) = %v, want %v", got, th)
	}
}

func TestNewTransformer_PanicsIfPreloadedCapabilityAwareWithoutVAD(t *testing.T) {
	mustPanic(t, func() {
		NewTransformer(nil, map[string]TraitHandler{
			"ingress": &capAwareHandler{stubTraitHandler{typ: "ingress"}},
		})
	})
}

// --- RegisterComponent ---

func TestTransformer_RegisterComponent_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	ch := &stubComponentHandler{typ: "worker"}
	tr.RegisterComponent("worker", ch)
	if got := tr.findComponentHandler("worker"); got != ch {
		t.Errorf("findComponentHandler(worker) = %v, want %v", got, ch)
	}
}

func TestTransformer_RegisterComponent_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("worker", &stubComponentHandler{typ: "worker"})
	mustPanic(t, func() {
		tr.RegisterComponent("worker", &stubComponentHandler{typ: "worker"})
	})
}

func TestTransformer_RegisterComponent_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterComponent("worker", &stubComponentHandler{typ: "webservice"})
	})
}

// --- RegisterTrait ---

func TestTransformer_RegisterTrait_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	th := &stubTraitHandler{typ: "expose"}
	tr.RegisterTrait("expose", th)
	if got := tr.findTraitHandler("expose"); got != th {
		t.Errorf("findTraitHandler(expose) = %v, want %v", got, th)
	}
}

func TestTransformer_RegisterTrait_PanicsIfCapabilityAwareWithoutVAD(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterTrait("ingress", &capAwareHandler{stubTraitHandler{typ: "ingress"}})
	})
}

func TestTransformer_RegisterTrait_CapabilityAwareWithVAD_OK(t *testing.T) {
	tr := NewTransformer(nil, nil)
	th := &capAwareWithVAD{stubTraitHandler{typ: "ingress"}}
	tr.RegisterTrait("ingress", th)
	if got := tr.findTraitHandler("ingress"); got != th {
		t.Errorf("findTraitHandler(ingress) = %v, want %v", got, th)
	}
}

func TestTransformer_RegisterTrait_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTrait("expose", &stubTraitHandler{typ: "expose"})
	mustPanic(t, func() {
		tr.RegisterTrait("expose", &stubTraitHandler{typ: "expose"})
	})
}

func TestTransformer_RegisterTrait_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterTrait("expose", &stubTraitHandler{typ: "certificate"})
	})
}

// --- RegisterPolicy ---

func TestTransformer_RegisterPolicy(t *testing.T) {
	tr := NewTransformer(nil, nil)
	ph := &stubPolicyHandler{typ: "dependency"}
	tr.RegisterPolicy("dependency", ph)
	if got := tr.findPolicyHandler("dependency"); got != ph {
		t.Errorf("findPolicyHandler(dependency) = %v, want %v", got, ph)
	}
}

func TestTransformer_RegisterPolicy_PanicsOnDuplicate(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "dependency"})
	mustPanic(t, func() {
		tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "dependency"})
	})
}

func TestTransformer_RegisterPolicy_PanicsOnCanHandleMismatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	mustPanic(t, func() {
		tr.RegisterPolicy("dependency", &stubPolicyHandler{typ: "placement"})
	})
}

// --- find* with no match ---

func TestTransformer_FindHandler_NoMatch(t *testing.T) {
	tr := NewTransformer(nil, nil)
	if got := tr.findComponentHandler("unknown"); got != nil {
		t.Errorf("findComponentHandler(unknown) = %v, want nil", got)
	}
	if got := tr.findTraitHandler("unknown"); got != nil {
		t.Errorf("findTraitHandler(unknown) = %v, want nil", got)
	}
	if got := tr.findPolicyHandler("unknown"); got != nil {
		t.Errorf("findPolicyHandler(unknown) = %v, want nil", got)
	}
}
