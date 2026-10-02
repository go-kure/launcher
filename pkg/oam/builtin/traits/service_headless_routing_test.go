package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// go-kure/launcher#690: a headless `service` may have no ports. Such a Service still owns its
// name, and routing must neither invent a port on it nor treat it as an external backend.

func portlessHeadlessService(traits ...oam.Trait) oam.Component {
	return oam.Component{
		Name: "db",
		Type: "service",
		Properties: map[string]any{
			"clusterIP": "None",
			"selector":  map[string]any{"app": "db"},
		},
		Traits: traits,
	}
}

func headlessRoutingApp(components ...oam.Component) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: components},
	}
}

// A trait-level servicePort would route to a port the Service does not have.
func TestTransform_PortlessHeadlessService_RefusesTraitServicePort(t *testing.T) {
	tests := map[string]oam.Trait{
		"ingress": {Type: "ingress", Properties: map[string]any{
			"servicePort": 80,
			"rules": []any{map[string]any{"host": "db.example.com", "paths": []any{
				map[string]any{"path": "/"},
			}}},
		}},
		"httproute": {Type: "httproute", Properties: map[string]any{
			"servicePort": 80,
			"parentRefs":  []any{map[string]any{"name": "gw"}},
			"rules":       []any{map[string]any{}},
		}},
	}
	for name, trait := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := serviceKindTransformer().TransformWithPolicy(
				headlessRoutingApp(portlessHeadlessService(trait)),
				oam.TransformContext{Namespace: "default"})
			want := `servicePort may not be set on component "db": its Service has no ports`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
	}
}

// A route naming the port-less Service is a route to a Service this application owns, so the
// route's backendSelector is not trusted: no policy may open the routed port on the pods that
// selector names. Before the fix, the Service read as unowned and db-allow-ingress-traffic
// opened TCP 80 on app=unrelated.
func TestTransform_PortlessHeadlessService_IsNotAnExternalBackend(t *testing.T) {
	web := oam.Component{
		Name:       "web",
		Type:       "deployment",
		Properties: map[string]any{"image": "ghcr.io/example/web:1.0"},
		Traits: []oam.Trait{ingressTrait(map[string]any{
			"path":            "/",
			"backend":         "db",
			"port":            80,
			"backendSelector": map[string]any{"matchLabels": map[string]any{"app": "unrelated"}},
		})},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(
		headlessRoutingApp(portlessHeadlessService(), web),
		oam.TransformContext{Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx")})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	// The owned Service has no TCP port to translate port 80 to, so nothing is opened at all.
	if clusterHasApp(cluster, "db-allow-ingress-traffic") {
		t.Errorf("a route to the port-less Service synthesized db-allow-ingress-traffic; apps: %v", clusterAppNames(cluster))
	}
	for _, name := range clusterAppNames(cluster) {
		if !strings.HasSuffix(name, "-allow-ingress-traffic") {
			continue
		}
		np := synthesizedNetworkPolicy(t, cluster, name)
		if np.Spec.PodSelector.MatchLabels["app"] == "unrelated" {
			t.Errorf("%s opens the routed port on app=unrelated, trusting backendSelector for an owned Service", name)
		}
	}
}
