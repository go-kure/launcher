package traits_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
	"github.com/go-kure/launcher/pkg/oam/netpol"
)

func ingressTrafficSourcesTrait(extra map[string]any) *oam.Trait {
	props := map[string]any{
		"rules": []any{
			map[string]any{
				"host":  "example.com",
				"paths": []any{map[string]any{"path": "/"}},
			},
		},
	}
	maps.Copy(props, extra)
	return &oam.Trait{Type: "ingress", Properties: props}
}

func ingressConfigFromApply(t *testing.T, app *stack.Application, trait *oam.Trait) *traits.IngressConfig {
	t.Helper()
	h := &traits.IngressHandler{}
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
	if !ok {
		t.Fatalf("expected *traits.IngressConfig, got %T", bundle.Applications[0].Config)
	}
	return cfg
}

func TestIngressHandler_TrafficSources_Parsed(t *testing.T) {
	trait := ingressTrafficSourcesTrait(map[string]any{
		"networkPolicy": map[string]any{
			"trafficSources": []any{
				map[string]any{"namespace": "ingress-nginx"},
			},
		},
	})
	cfg := ingressConfigFromApply(t, newWebApp("my-app", "default"), trait)

	srcs := cfg.TrafficSources()
	if len(srcs) != 1 || srcs[0].Namespace != "ingress-nginx" {
		t.Fatalf("unexpected traffic sources: %#v", srcs)
	}
	ports := cfg.BackendPorts()
	if len(ports) != 1 || ports[0].IntVal != 80 {
		t.Errorf("unexpected backend ports: %#v", ports)
	}
	if cfg.TargetComponentName() != "my-app" {
		t.Errorf("TargetComponentName = %q, want \"my-app\"", cfg.TargetComponentName())
	}
}

func TestIngressHandler_TrafficSources_AbsentIsNil(t *testing.T) {
	cfg := ingressConfigFromApply(t, newWebApp("my-app", "default"), ingressTrafficSourcesTrait(nil))
	if cfg.TrafficSources() != nil {
		t.Errorf("expected nil traffic sources when networkPolicy absent, got %#v", cfg.TrafficSources())
	}
}

func TestIngressHandler_TrafficSources_EmptyListDisables(t *testing.T) {
	trait := ingressTrafficSourcesTrait(map[string]any{
		"networkPolicy": map[string]any{"trafficSources": []any{}},
	})
	cfg := ingressConfigFromApply(t, newWebApp("my-app", "default"), trait)
	if cfg.TrafficSources() != nil {
		t.Errorf("expected nil traffic sources for explicit empty list, got %#v", cfg.TrafficSources())
	}
}

func TestIngressHandler_TrafficSources_MalformedErrors(t *testing.T) {
	trait := ingressTrafficSourcesTrait(map[string]any{
		"networkPolicy": map[string]any{
			"trafficSources": []any{
				map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"k": "v"}}}, // missing namespace
			},
		},
	})
	h := &traits.IngressHandler{}
	if err := h.Apply(trait, newWebApp("my-app", "default"), newBundle()); err == nil {
		t.Fatal("expected error for trafficSource missing namespace")
	}
}

// TargetComponentName must be the OAM component label, never the (possibly
// overridden) K8s Service name, so the synthesized NetworkPolicy selects the
// component's pods via {app: <component>}.
func TestIngressConfig_TargetComponentName_IsComponentNotService(t *testing.T) {
	app := stack.NewApplication("web", "default", &namedWebConfig{port: 80, serviceName: "web-headless"})
	trait := ingressTrafficSourcesTrait(map[string]any{
		"networkPolicy": map[string]any{
			"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
		},
	})
	cfg := ingressConfigFromApply(t, app, trait)
	if cfg.ServiceName != "web-headless" {
		t.Fatalf("precondition: expected ServiceName \"web-headless\", got %q", cfg.ServiceName)
	}
	if cfg.TargetComponentName() != "web" {
		t.Errorf("TargetComponentName = %q, want \"web\" (component, not service)", cfg.TargetComponentName())
	}
	if ports := cfg.BackendPorts(); len(ports) != 1 || ports[0].IntVal != 80 {
		t.Errorf("expected self-backend port 80, got %#v", ports)
	}
}

func TestHTTPRouteHandler_TrafficSources_Parsed(t *testing.T) {
	h := &traits.HTTPRouteHandler{}
	trait := &oam.Trait{
		Type: "httproute",
		Properties: map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{}},
			"networkPolicy": map[string]any{
				"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newWebApp("web", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, ok := bundle.Applications[0].Config.(*traits.HTTPRouteConfig)
	if !ok {
		t.Fatalf("expected *traits.HTTPRouteConfig, got %T", bundle.Applications[0].Config)
	}
	if srcs := cfg.TrafficSources(); len(srcs) != 1 || srcs[0].Namespace != "gateway-system" {
		t.Fatalf("unexpected traffic sources: %#v", srcs)
	}
	if ports := cfg.BackendPorts(); len(ports) != 1 || ports[0].IntVal != 80 {
		t.Errorf("unexpected backend ports: %#v", ports)
	}
	if cfg.TargetComponentName() != "web" {
		t.Errorf("TargetComponentName = %q, want \"web\"", cfg.TargetComponentName())
	}
}

// End-to-end: a webservice + ingress trait carrying trafficSources flows through
// the full transform and yields a synthesized {component}-allow-ingress-traffic policy.
func TestTransform_IngressTrafficSources_SynthesizesNetworkPolicy(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
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

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}

	if !clusterHasApp(cluster, "web-allow-ingress-traffic") {
		t.Errorf("expected synthesized app \"web-allow-ingress-traffic\"; cluster apps: %v", clusterAppNames(cluster))
	}
}

func clusterAppNames(c *stack.Cluster) []string {
	var names []string
	var visitBundle func(b *stack.Bundle)
	visitBundle = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		for _, a := range b.Applications {
			names = append(names, a.Name)
		}
		for _, ch := range b.Children {
			visitBundle(ch)
		}
	}
	var visitNode func(n *stack.Node)
	visitNode = func(n *stack.Node) {
		if n == nil {
			return
		}
		visitBundle(n.Bundle)
		for _, ch := range n.Children {
			visitNode(ch)
		}
	}
	if c != nil {
		visitNode(c.Node)
	}
	return names
}

func clusterHasApp(c *stack.Cluster, name string) bool {
	return slices.Contains(clusterAppNames(c), name)
}

// End-to-end: a webservice with crane-supplied (non-authorable) egress peers on the
// transform context yields a synthesized {component}-allow-egress-traffic policy, and
// no inbound policy is synthesized absent trafficSources (additive, distinct paths).
func TestTransform_EgressPeers_SynthesizesEgressNetworkPolicy(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
			}},
		},
	}

	ctx := oam.TransformContext{
		Namespace: "default",
		EgressPeers: map[string][]netpol.EgressPeer{
			"web": {{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, ctx)
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}

	if !clusterHasApp(cluster, "web-allow-egress-traffic") {
		t.Errorf("expected synthesized \"web-allow-egress-traffic\"; cluster apps: %v", clusterAppNames(cluster))
	}
	if clusterHasApp(cluster, "web-allow-ingress-traffic") {
		t.Errorf("did not expect inbound synthesis without trafficSources; cluster apps: %v", clusterAppNames(cluster))
	}
}
