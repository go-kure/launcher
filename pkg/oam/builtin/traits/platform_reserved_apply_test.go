package traits_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestHandlers_ReservedNetworkPolicy_StillAppliesDirectly is the regression guard the
// ADR-035 D3 correction exists for. Declaring networkPolicy platform-reserved must not
// reach IngressHandler.Apply / HTTPRouteHandler.Apply: the pre-existing tests in
// networkpolicy_auto_test.go and domain_resolution_test.go author networkPolicy
// straight onto the trait, deliberately bypassing any ClusterProfile, so netpol
// synthesis can be exercised without a capability round-trip. Reservation is enforced
// where capability rendering is merged in, not by the handler — which is why marking
// the shared fragment reserved leaves this pattern intact.
func TestHandlers_ReservedNetworkPolicy_StillAppliesDirectly(t *testing.T) {
	netpolProps := map[string]any{
		"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
	}

	t.Run("ingress", func(t *testing.T) {
		trait := &oam.Trait{Type: "ingress", Properties: map[string]any{
			"rules": []any{map[string]any{
				"host":  "example.com",
				"paths": []any{map[string]any{"path": "/"}},
			}},
			"networkPolicy": netpolProps,
		}}

		bundle := newBundle()
		h := &traits.IngressHandler{}
		if err := h.Apply(trait, newWebApp("my-app", "default"), bundle); err != nil {
			t.Fatalf("Apply must still accept a directly authored networkPolicy: %v", err)
		}
		cfg, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
		if !ok {
			t.Fatalf("expected *traits.IngressConfig, got %T", bundle.Applications[0].Config)
		}
		if srcs := cfg.TrafficSources(); len(srcs) != 1 || srcs[0].Namespace != "ingress-nginx" {
			t.Fatalf("unexpected traffic sources: %#v", srcs)
		}
	})

	t.Run("httproute", func(t *testing.T) {
		trait := &oam.Trait{Type: "httproute", Properties: map[string]any{
			"parentRefs":    []any{map[string]any{"name": "gw"}},
			"rules":         []any{map[string]any{}},
			"networkPolicy": netpolProps,
		}}

		bundle := newBundle()
		h := &traits.HTTPRouteHandler{}
		if err := h.Apply(trait, newWebApp("my-app", "default"), bundle); err != nil {
			t.Fatalf("Apply must still accept a directly authored networkPolicy: %v", err)
		}
		cfg, ok := bundle.Applications[0].Config.(*traits.HTTPRouteConfig)
		if !ok {
			t.Fatalf("expected *traits.HTTPRouteConfig, got %T", bundle.Applications[0].Config)
		}
		if srcs := cfg.TrafficSources(); len(srcs) != 1 || srcs[0].Namespace != "ingress-nginx" {
			t.Fatalf("unexpected traffic sources: %#v", srcs)
		}
	})
}
