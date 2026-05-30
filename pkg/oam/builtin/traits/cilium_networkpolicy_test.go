package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestCiliumNetworkPolicyHandler_Apply_PropagatesNamespace verifies that the
// emitted CiliumNetworkPolicy sub-app inherits its namespace from the component
// application — confirming the app parameter is correctly threaded through Apply.
func TestCiliumNetworkPolicyHandler_Apply_PropagatesNamespace(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	app := stack.NewApplication("myapp", "production", nil)
	bundle := &stack.Bundle{}
	trait := &oam.Trait{
		Type: "cilium-networkpolicy",
		Properties: map[string]any{
			"name":   "allow-egress",
			"egress": []any{},
		},
	}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 app, got %d", len(bundle.Applications))
	}
	cnpApp := bundle.Applications[0]
	if cnpApp.Namespace != "production" {
		t.Errorf("cnpApp.Namespace = %q, want %q", cnpApp.Namespace, "production")
	}
}
