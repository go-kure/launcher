package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestTraitConfigs_ComponentName verifies every trait sub-app config exposes the
// owning OAM component through the oam.ComponentNamed accessor, and that the value
// is the component name (app.Name) — never a sub-app or K8s Service name.
func TestTraitConfigs_ComponentName(t *testing.T) {
	const component = "api"

	cases := []struct {
		name    string
		handler oam.TraitHandler
		app     *stack.Application
		trait   *oam.Trait
	}{
		{
			name:    "configmap",
			handler: &traits.ConfigMapHandler{},
			app:     newApp(component, "default"),
			trait:   &oam.Trait{Type: "configmap", Properties: map[string]any{"name": "cfg"}},
		},
		{
			name:    "scaler",
			handler: &traits.ScalerHandler{},
			app:     newApp(component, "default"),
			trait:   &oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 2, "maxReplicas": 10}},
		},
		{
			name:    "networkpolicy",
			handler: &traits.NetworkPolicyHandler{},
			app:     newApp(component, "default"),
			trait: &oam.Trait{Type: "networkpolicy", Properties: map[string]any{
				"egress": []any{map[string]any{
					"to": []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "backend"}}}},
				}},
			}},
		},
		{
			name:    "cilium-networkpolicy",
			handler: &traits.CiliumNetworkPolicyHandler{},
			app:     newApp(component, "default"),
			trait: &oam.Trait{Type: "cilium-networkpolicy", Properties: map[string]any{
				"name":    "test-policy",
				"ingress": []any{map[string]any{"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"app": "frontend"}}}}},
			}},
		},
		{
			name:    "ingress",
			handler: &traits.IngressHandler{},
			app:     newWebApp(component, "default"),
			trait:   ingressTrafficSourcesTrait(nil),
		},
		{
			name:    "httproute",
			handler: &traits.HTTPRouteHandler{},
			app:     newWebApp(component, "default"),
			trait:   httprouteTrait(map[string]any{"gatewayName": "public"}),
		},
		{
			name:    "certificate",
			handler: &traits.CertificateHandler{},
			app:     newApp(component, "default"),
			trait: &oam.Trait{Type: "certificate", Properties: map[string]any{
				"secretName": "my-tls",
				"issuerRef":  map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"},
				"dnsNames":   []any{"example.com"},
			}},
		},
		{
			name:    "pvc",
			handler: &traits.PVCHandler{},
			app:     newApp(component, "default"),
			trait:   &oam.Trait{Type: "pvc", Properties: map[string]any{"name": "shared-data", "size": "5Gi"}},
		},
		{
			name:    "external-secret",
			handler: &traits.ExternalSecretHandler{},
			app:     newApp(component, "default"),
			trait: &oam.Trait{Type: "external-secret", Properties: map[string]any{
				"secretName":     "my-secret",
				"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
				"data": []any{map[string]any{
					"secretKey": "DB_PASS",
					"remoteRef": map[string]any{"key": "prod/db", "property": "password"},
				}},
			}},
		},
		{
			name:    "rbac",
			handler: &traits.RBACHandler{},
			app:     newApp(component, "default"),
			trait: &oam.Trait{Type: "rbac", Properties: map[string]any{
				"rules": []any{map[string]any{
					"apiGroups": []any{""},
					"resources": []any{"pods"},
					"verbs":     []any{"get", "list"},
				}},
			}},
		},
		{
			name:    "volsync",
			handler: &traits.VolSyncHandler{},
			app:     newApp(component, "default"),
			trait:   &oam.Trait{Type: "volsync", Properties: map[string]any{"sourcePVC": "data", "schedule": "@daily"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := newBundle()
			if err := tc.handler.Apply(tc.trait, tc.app, bundle); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(bundle.Applications) == 0 {
				t.Fatal("handler appended no sub-application")
			}
			cfg := bundle.Applications[len(bundle.Applications)-1].Config
			named, ok := cfg.(oam.ComponentNamed)
			if !ok {
				t.Fatalf("%T does not implement oam.ComponentNamed", cfg)
			}
			if got := named.ComponentName(); got != component {
				t.Errorf("ComponentName() = %q, want %q", got, component)
			}
		})
	}
}
