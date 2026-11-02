package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack/stacktest"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// A trait keeps its parsed properties in its config and builds the objects from
// them on every Generate. What Generate returns belongs to the caller, so a
// slice, map or pointer taken from the config must be copied into the object:
// otherwise two results share it, and an edit to one object changes the config
// and every later result.
//
// stacktest.AssertGeneration calls Generate twice and fails on any value the two
// results share, or that one result reaches at two paths.

// assertTraitGeneration applies a trait to a fresh application and checks the
// generation contract on the application the trait wrote to.
func assertTraitGeneration(t *testing.T, h oam.TraitHandler, trait *oam.Trait) {
	t.Helper()
	app := newWebApp("api", "default")
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	stacktest.AssertGeneration(t, bundle.Applications[len(bundle.Applications)-1])
}

func TestIngressTrait_GenerationDoesNotShareConfig(t *testing.T) {
	assertTraitGeneration(t, &traits.IngressHandler{}, &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"rules": []any{map[string]any{
				"host":  "example.com",
				"paths": []any{map[string]any{"path": "/"}},
			}},
			"tls": []any{map[string]any{
				"hosts":      []any{"example.com", "www.example.com"},
				"secretName": "example-tls",
			}},
		},
	})
}

func TestRBACTrait_GenerationDoesNotShareConfig(t *testing.T) {
	rules := []any{map[string]any{
		"apiGroups": []any{""},
		"resources": []any{"pods", "configmaps"},
		"verbs":     []any{"get", "list"},
	}}
	for _, tc := range []struct {
		name  string
		props map[string]any
	}{
		{"namespaced", map[string]any{"rules": rules}},
		// The Role and the ClusterRole read the same rule: they must not share.
		{"clusterWide", map[string]any{"rules": rules, "clusterWide": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTraitGeneration(t, &traits.RBACHandler{}, &oam.Trait{Type: "rbac", Properties: tc.props})
		})
	}
}

func TestNetworkPolicyTrait_GenerationDoesNotShareConfig(t *testing.T) {
	peers := []any{
		map[string]any{
			"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "client"}},
			"namespaceSelector": map[string]any{"matchLabels": map[string]any{"team": "a"}},
		},
		map[string]any{
			"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16"}},
		},
	}
	for _, tc := range []struct {
		name  string
		props map[string]any
	}{
		{"ingress", map[string]any{"ingress": []any{map[string]any{"from": peers}}}},
		{"egress", map[string]any{"egress": []any{map[string]any{"to": peers}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTraitGeneration(t, &traits.NetworkPolicyHandler{}, &oam.Trait{Type: "networkpolicy", Properties: tc.props})
		})
	}
}

func TestHTTPRouteTrait_GenerationDoesNotShareConfig(t *testing.T) {
	assertTraitGeneration(t, &traits.HTTPRouteHandler{}, &oam.Trait{
		Type: "httproute",
		Properties: map[string]any{
			"parentRefs":  []any{map[string]any{"name": "gw"}},
			"rules":       []any{map[string]any{}},
			"annotations": map[string]any{"example.com/owner": "platform-team"},
		},
	})
}
