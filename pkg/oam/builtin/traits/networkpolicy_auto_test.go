package traits_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// component's pods via the configured component label key (default
// {gokure.dev/component: <component>}).
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
	// Default target selector is gokure.dev/component (the downstream runtime stamps it on every pod).
	if sel := synthesizedPodSelector(t, cluster, "web-allow-ingress-traffic"); sel["gokure.dev/component"] != "web" {
		t.Errorf("default ingress podSelector = %v, want gokure.dev/component=web", sel)
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

// synthesizedPodSelector finds the named synthesized app, runs its config's Generate,
// and returns the emitted NetworkPolicy's top-level podSelector matchLabels — proving
// the configured ComponentLabelKey is threaded all the way through Phase 4.
func synthesizedPodSelector(t *testing.T, c *stack.Cluster, name string) map[string]string {
	t.Helper()
	var found *stack.Application
	var visitBundle func(b *stack.Bundle)
	visitBundle = func(b *stack.Bundle) {
		if b == nil || found != nil {
			return
		}
		for _, a := range b.Applications {
			if a.Name == name {
				found = a
				return
			}
		}
		for _, ch := range b.Children {
			visitBundle(ch)
		}
	}
	var visitNode func(n *stack.Node)
	visitNode = func(n *stack.Node) {
		if n == nil || found != nil {
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
	if found == nil {
		t.Fatalf("synthesized app %q not found; cluster apps: %v", name, clusterAppNames(c))
	}
	objs, err := found.Config.Generate(found)
	if err != nil {
		t.Fatalf("Generate %q: %v", name, err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object from %q, got %d", name, len(objs))
	}
	np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("expected *NetworkPolicy from %q, got %T", name, *objs[0])
	}
	return np.Spec.PodSelector.MatchLabels
}

// End-to-end: a webservice with downstream-supplied (non-authorable) egress peers on the
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
	// Default egress source-pod selector is gokure.dev/component.
	if sel := synthesizedPodSelector(t, cluster, "web-allow-egress-traffic"); sel["gokure.dev/component"] != "web" {
		t.Errorf("default egress podSelector = %v, want gokure.dev/component=web", sel)
	}
}

// End-to-end: TransformContext.ComponentLabelKey overrides the synthesized podSelector
// key for BOTH inbound and egress families. The "app" case is the escape hatch that
// restores pre-change behavior for non-downstream/kurel callers; a custom key proves the
// value is passed through verbatim.
func TestTransform_ComponentLabelKey_Override(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantKey string
	}{
		{"escape_hatch_app", "app", "app"},
		{"custom_key", "example.com/name", "example.com/name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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

			ctx := oam.TransformContext{
				Namespace:         "default",
				ComponentLabelKey: tc.key,
				EgressPeers: map[string][]netpol.EgressPeer{
					"web": {{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
				},
			}
			cluster, _, err := tr.TransformWithPolicy(app, ctx)
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}

			for _, npName := range []string{"web-allow-ingress-traffic", "web-allow-egress-traffic"} {
				sel := synthesizedPodSelector(t, cluster, npName)
				if sel[tc.wantKey] != "web" {
					t.Errorf("%s podSelector = %v, want %s=web", npName, sel, tc.wantKey)
				}
				if _, hasDefault := sel["gokure.dev/component"]; hasDefault && tc.wantKey != "gokure.dev/component" {
					t.Errorf("%s podSelector should not carry default key when overridden: %v", npName, sel)
				}
			}
		})
	}
}

// End-to-end: a postgresql component + platform-supplied IngressPeers yields a synthesized
// {comp}-allow-endpoint-ingress policy whose podSelector is the endpoint's own operator label
// (cnpg.io/cluster), not the component-label key.
func TestTransform_IngressPeers_SynthesizesEndpointIngressNetworkPolicy(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("postgresql", &components.PostgresqlHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{Name: "orders-db", Type: "postgresql"}},
		},
	}
	ctx := oam.TransformContext{
		Namespace: "default",
		IngressPeers: map[string][]netpol.IngressPeer{
			"orders-db": {{
				Endpoint: netpol.Endpoint{
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"cnpg.io/cluster": "orders-db"}},
					Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
				},
				Sources: []netpol.TrafficSource{{
					Namespace:   "app",
					PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
				}},
			}},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, ctx)
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if !clusterHasApp(cluster, "orders-db-allow-endpoint-ingress") {
		t.Fatalf("expected orders-db-allow-endpoint-ingress; apps: %v", clusterAppNames(cluster))
	}
	sel := synthesizedPodSelector(t, cluster, "orders-db-allow-endpoint-ingress")
	if sel["cnpg.io/cluster"] != "orders-db" || len(sel) != 1 {
		t.Errorf("podSelector = %v, want single cnpg.io/cluster=orders-db", sel)
	}
}
