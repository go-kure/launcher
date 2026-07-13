package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// ingressTrafficSourcesApp builds a webservice + ingress trait carrying trafficSources,
// which yields a synthesized {comp}-allow-ingress-traffic policy after transform.
func ingressTrafficSourcesApp() *oam.Application {
	return &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{
							"host":  "example.com",
							"paths": []any{map[string]any{"path": "/"}},
						}},
						"networkPolicy": map[string]any{
							"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
						},
					},
				}},
			}},
		},
	}
}

func domainTestTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	return tr
}

// TransformContext.Domain drives the synthesized podSelector key: <domain>/component.
func TestTransform_Domain_DrivesIngressSelector(t *testing.T) {
	tr := domainTestTransformer()
	cluster, _, err := tr.TransformWithPolicy(ingressTrafficSourcesApp(),
		oam.TransformContext{Namespace: "default", Domain: "example.com"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	sel := synthesizedPodSelector(t, cluster, "web-allow-ingress-traffic")
	if sel["example.com/component"] != "web" {
		t.Errorf("podSelector = %v, want example.com/component=web", sel)
	}
	if len(sel) != 1 {
		t.Errorf("podSelector should have exactly one key, got %v", sel)
	}
}

// Empty Domain resolves to the library default gokure.dev.
func TestTransform_DefaultDomain_IngressSelector(t *testing.T) {
	tr := domainTestTransformer()
	cluster, _, err := tr.TransformWithPolicy(ingressTrafficSourcesApp(),
		oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	sel := synthesizedPodSelector(t, cluster, "web-allow-ingress-traffic")
	if sel["gokure.dev/component"] != "web" {
		t.Errorf("podSelector = %v, want gokure.dev/component=web", sel)
	}
}

// ComponentLabelKey (full-key override) wins over Domain.
func TestTransform_ComponentLabelKey_WinsOverDomain(t *testing.T) {
	tr := domainTestTransformer()
	cluster, _, err := tr.TransformWithPolicy(ingressTrafficSourcesApp(),
		oam.TransformContext{Namespace: "default", Domain: "example.com", ComponentLabelKey: "app"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	sel := synthesizedPodSelector(t, cluster, "web-allow-ingress-traffic")
	if sel["app"] != "web" {
		t.Errorf("podSelector = %v, want app=web (ComponentLabelKey wins)", sel)
	}
	if _, hasDomain := sel["example.com/component"]; hasDomain {
		t.Errorf("podSelector should not carry the domain key when ComponentLabelKey is set: %v", sel)
	}
}

func TestTransform_InvalidDomain_Errors(t *testing.T) {
	tr := domainTestTransformer()
	for _, bad := range []string{"https://example.com", "example.com/", "Example.com"} {
		_, _, err := tr.TransformWithPolicy(ingressTrafficSourcesApp(),
			oam.TransformContext{Namespace: "default", Domain: bad})
		if err == nil {
			t.Errorf("expected error for invalid Domain %q, got nil", bad)
		}
	}
}

func TestTransform_InvalidComponentLabelKey_Errors(t *testing.T) {
	tr := domainTestTransformer()
	for _, bad := range []string{"/component", "example.com/", "Upper/Name", "example.com/" + strings.Repeat("a", 64)} {
		_, _, err := tr.TransformWithPolicy(ingressTrafficSourcesApp(),
			oam.TransformContext{Namespace: "default", ComponentLabelKey: bad})
		if err == nil {
			t.Errorf("expected error for invalid ComponentLabelKey %q, got nil", bad)
		}
	}
}
