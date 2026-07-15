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

// synthesizedNetworkPolicy finds a named synthesized app and returns its emitted NetworkPolicy.
func synthesizedNetworkPolicy(t *testing.T, c *stack.Cluster, name string) *networkingv1.NetworkPolicy {
	t.Helper()
	var found *stack.Application
	var visit func(b *stack.Bundle)
	visit = func(b *stack.Bundle) {
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
			visit(ch)
		}
	}
	var visitNode func(n *stack.Node)
	visitNode = func(n *stack.Node) {
		if n == nil || found != nil {
			return
		}
		visit(n.Bundle)
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
	np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("expected *NetworkPolicy from %q, got %T", name, *objs[0])
	}
	return np
}

// #227: an httproute whose backendRef routes to a SEPARATE in-bundle backend component lands the
// synthesized ingress allow on the backend's pods + backendRef port, not the router's own. The
// router itself (which has no self backend port) gets no empty self policy.
func TestTransform_BackendRef_RetargetsToBackendComponent(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							"rules": []any{map[string]any{
								"backendRefs": []any{map[string]any{"name": "backend", "port": 9000}},
							}},
							"networkPolicy": map[string]any{
								"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
							},
						},
					}},
				},
				{
					Name:       "backend",
					Type:       "webservice",
					Properties: map[string]any{"image": "api:1.0", "port": 9000},
				},
			},
		},
	}

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}

	// The allow lands on the backend component.
	if !clusterHasApp(cluster, "backend-allow-ingress-traffic") {
		t.Fatalf("expected synthesized \"backend-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
	}
	// The router-only exposer gets no empty self policy.
	if clusterHasApp(cluster, "router-allow-ingress-traffic") {
		t.Errorf("did not expect a self policy for the router-only exposer; apps: %v", clusterAppNames(cluster))
	}
	np := synthesizedNetworkPolicy(t, cluster, "backend-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "backend" {
		t.Errorf("target selector = %v, want gokure.dev/component=backend", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 9000 {
		t.Errorf("expected a single ingress rule on port 9000, got %+v", np.Spec.Ingress)
	}
	if len(np.Spec.Ingress[0].From) != 1 ||
		np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "gateway-system" {
		t.Errorf("expected the router's traffic source (gateway-system), got %+v", np.Spec.Ingress[0].From)
	}
}

// #227: a backendRef naming a Service with no owning component in the bundle is left authored —
// no synthesized policy for it, and no panic.
func TestTransform_BackendRef_Unresolvable_LeavesAuthored(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "httproute",
					Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules": []any{map[string]any{
							"backendRefs": []any{map[string]any{"name": "external-svc", "port": 9000}},
						}},
						"networkPolicy": map[string]any{
							"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
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
	for _, n := range clusterAppNames(cluster) {
		if n == "external-svc-allow-ingress-traffic" || n == "router-allow-ingress-traffic" {
			t.Errorf("expected no synthesized ingress policy for an unresolvable backendRef, got %q", n)
		}
	}
}

// #227: the ingress trait's external-backend path (collectIngressBackendTargets) also retargets,
// including the named-port (PortName) branch that only the ingress collector exercises.
func TestTransform_IngressBackendPath_RetargetsToBackend(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "ingress",
						Properties: map[string]any{
							"rules": []any{map[string]any{
								"host": "example.com",
								"paths": []any{map[string]any{
									"path":     "/",
									"backend":  "backend",
									"portName": "http",
								}},
							}},
							"networkPolicy": map[string]any{
								"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}},
							},
						},
					}},
				},
				{
					Name:       "backend",
					Type:       "webservice",
					Properties: map[string]any{"image": "api:1.0", "port": 9000},
				},
			},
		},
	}

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if !clusterHasApp(cluster, "backend-allow-ingress-traffic") {
		t.Fatalf("expected \"backend-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
	}
	if clusterHasApp(cluster, "router-allow-ingress-traffic") {
		t.Errorf("did not expect a self policy for the router-only exposer; apps: %v", clusterAppNames(cluster))
	}
	np := synthesizedNetworkPolicy(t, cluster, "backend-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "backend" {
		t.Errorf("target selector = %v, want gokure.dev/component=backend", np.Spec.PodSelector.MatchLabels)
	}
	// Named-port branch: the retargeted rule carries the port by name, not number.
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 ||
		np.Spec.Ingress[0].Ports[0].Port.StrVal != "http" {
		t.Errorf("expected a single named ingress port \"http\", got %+v", np.Spec.Ingress)
	}
}

// #227: a backendRef naming a component whose Service name differs from its component name (e.g. a
// statefulset's headless service) resolves via BackendServiceName() (the serviceBackendNamer
// branch) — a break there would silently fall back to "authored".
func TestTransform_BackendRef_ResolvesViaServiceName(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							"rules": []any{map[string]any{
								// Names the statefulset's headless Service, not its component name.
								"backendRefs": []any{map[string]any{"name": "db-headless", "port": 5432}},
							}},
							"networkPolicy": map[string]any{
								"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
							},
						},
					}},
				},
				{
					Name:       "db",
					Type:       "statefulset",
					Properties: map[string]any{"image": "postgres:16", "port": 5432, "serviceName": "db-headless"},
				},
			},
		},
	}

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	// The Service name "db-headless" resolves to component "db".
	if !clusterHasApp(cluster, "db-allow-ingress-traffic") {
		t.Fatalf("expected \"db-allow-ingress-traffic\" (resolved via BackendServiceName); apps: %v", clusterAppNames(cluster))
	}
	np := synthesizedNetworkPolicy(t, cluster, "db-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "db" {
		t.Errorf("target selector = %v, want gokure.dev/component=db", np.Spec.PodSelector.MatchLabels)
	}
}

// #227: a self-referencing backendRef (the component's own service) is unchanged — the allow
// stays on the exposing component.
func TestTransform_BackendRef_SelfTarget_Unchanged(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "httproute",
					Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules": []any{map[string]any{
							"backendRefs": []any{map[string]any{"name": "web", "port": 8080}},
						}},
						"networkPolicy": map[string]any{
							"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
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
		t.Errorf("expected self policy \"web-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
	}
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
			"web": {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
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

// End-to-end: a malformed non-authorable egress peer (ported but selector-less) fails the whole
// transform (fail-fast), rather than silently emitting a namespace-wide egress allow.
func TestTransform_EgressPeers_InvalidSelector_Errors(t *testing.T) {
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
			"web": {{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}}, // ported, selector-less
		},
	}
	if _, _, err := tr.TransformWithPolicy(app, ctx); err == nil {
		t.Error("expected TransformWithPolicy to fail on a ported selector-less egress peer, got nil")
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
					"web": {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
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

// --- #239: external (non-component) routing backends with an explicit backendSelector ---

// ingressExternalBackendApp builds a single-webservice app whose ingress trait routes to one
// external backend path. selector nil omits backendSelector.
func ingressExternalBackendApp(backend string, port int, selector map[string]any) *oam.Application {
	pathMap := map[string]any{"path": "/", "backend": backend, "port": port}
	if selector != nil {
		pathMap["backendSelector"] = map[string]any{"matchLabels": selector}
	}
	return &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules":         []any{map[string]any{"host": "example.com", "paths": []any{pathMap}}},
						"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}}},
					},
				}},
			}},
		},
	}
}

// An ingress path routing to an external bare Service with an explicit backendSelector synthesizes
// an ingress allow onto the selector's pods (not the router), on the backend port, with the
// namespace-wide routing traffic source preserved.
func TestTransform_ExternalBackend_Ingress_WithSelector_SynthesizesNP(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := ingressExternalBackendApp("external-svc", 8081, map[string]any{"app.kubernetes.io/name": "external"})
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	np := synthesizedNetworkPolicy(t, cluster, "external-svc-allow-ingress-traffic")
	if np.Namespace != "default" {
		t.Errorf("namespace = %q, want default", np.Namespace)
	}
	if got := np.Spec.PodSelector.MatchLabels; got["app.kubernetes.io/name"] != "external" || len(got) != 1 {
		t.Errorf("podSelector = %v, want single app.kubernetes.io/name=external", got)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 8081 {
		t.Errorf("expected single ingress port 8081, got %+v", np.Spec.Ingress)
	}
	from := np.Spec.Ingress[0].From
	if len(from) != 1 || from[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "ingress-nginx" || from[0].PodSelector != nil {
		t.Errorf("expected namespace-wide From ingress-nginx, got %+v", from)
	}
	if clusterHasApp(cluster, "router-allow-ingress-traffic") {
		t.Errorf("did not expect a self policy for the pure-exposer router; apps: %v", clusterAppNames(cluster))
	}
}

// The HTTPRoute shape: a backendRef to an external bare Service + backendSelector synthesizes the
// same kind of policy.
func TestTransform_ExternalBackend_HTTPRoute_WithSelector_SynthesizesNP(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "httproute",
					Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules": []any{map[string]any{
							"backendRefs": []any{map[string]any{
								"name":            "external-svc",
								"port":            8081,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}},
							}},
						}},
						"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
					},
				}},
			}},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	np := synthesizedNetworkPolicy(t, cluster, "external-svc-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels; got["app.kubernetes.io/name"] != "external" || len(got) != 1 {
		t.Errorf("podSelector = %v, want single app.kubernetes.io/name=external", got)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 8081 {
		t.Errorf("expected single ingress port 8081, got %+v", np.Spec.Ingress)
	}
}

// Without a backendSelector, an unresolvable external backend stays authored — no synthesized
// policy (the pre-#239 behavior).
func TestTransform_ExternalBackend_WithoutSelector_LeavesAuthored(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := ingressExternalBackendApp("external-svc", 8081, nil)
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if clusterHasApp(cluster, "external-svc-allow-ingress-traffic") {
		t.Errorf("expected no synthesized policy without a selector; apps: %v", clusterAppNames(cluster))
	}
}

// Mixed presence for one external Service: a selector-bearing occurrence is synthesized on its own
// port; a selectorless occurrence stays authored and must not widen the synthesized policy's ports.
func TestTransform_ExternalBackend_MixedSelectorPresence_DoesNotWiden(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{"host": "example.com", "paths": []any{
							map[string]any{"path": "/a", "backend": "external-svc", "port": 8081,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}}},
							map[string]any{"path": "/b", "backend": "external-svc", "port": 9091},
						}}},
						"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}}},
					},
				}},
			}},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	np := synthesizedNetworkPolicy(t, cluster, "external-svc-allow-ingress-traffic")
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 8081 {
		t.Errorf("expected only the selected port 8081 (9091 stays authored), got %+v", np.Spec.Ingress)
	}
}

// Two paths giving the SAME external Service two different selectors is an authoring conflict — a
// Service has one selector — rejected at parse.
func TestTransform_ExternalBackend_ConflictingSelectorsInTrait_Rejected(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{"host": "example.com", "paths": []any{
							map[string]any{"path": "/a", "backend": "external-svc", "port": 8081,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}}},
							map[string]any{"path": "/b", "backend": "external-svc", "port": 8082,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "other"}}},
						}}},
						"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}}},
					},
				}},
			}},
		},
	}
	if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"}); err == nil {
		t.Fatal("expected an error for conflicting backendSelector values on one Service")
	}
}

// Two routers naming the same external Service in the same namespace with different selectors is a
// conflict resolved only at synthesis — it must fail the transform, not emit two colliding allows.
func TestTransform_ExternalBackend_ConflictingSelectorsAcrossRouters_FailsTransform(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	mkRouter := func(name, label string) oam.Component {
		return oam.Component{
			Name:       name,
			Type:       "webservice",
			Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
			Traits: []oam.Trait{{
				Type: "ingress",
				Properties: map[string]any{
					"rules": []any{map[string]any{"host": name + ".example.com", "paths": []any{
						map[string]any{"path": "/", "backend": "external-svc", "port": 8081,
							"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": label}}},
					}}},
					"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}}},
				},
			}},
		}
	}
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:     oam.ApplicationSpec{Components: []oam.Component{mkRouter("router-a", "external"), mkRouter("router-b", "other")}},
	}
	if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"}); err == nil {
		t.Fatal("expected an error for an external Service given two different selectors across routers")
	}
}

// An external policy name that collides with an ACTUALLY-EMITTED component inbound policy fails the
// transform. Component db (statefulset, Service db-headless) emits db-allow-ingress-traffic; a
// router routing to a bare external Service named db would collide.
func TestTransform_ExternalBackend_NameCollisionWithEmittedComponent_FailsTransform(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "db",
					Type:       "statefulset",
					Properties: map[string]any{"image": "postgres:16", "port": 5432, "serviceName": "db-headless"},
					// db's own routing trait → emits db-allow-ingress-traffic.
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs":    []any{map[string]any{"name": "gw"}},
							"rules":         []any{map[string]any{"backendRefs": []any{map[string]any{"name": "db-headless", "port": 5432}}}},
							"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
						},
					}},
				},
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							// Bare external Service "db" (not db-headless) → unresolved → db-allow-ingress-traffic.
							"rules": []any{map[string]any{"backendRefs": []any{map[string]any{
								"name": "db", "port": 8080,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}},
							}}}},
							"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
						},
					}},
				},
			},
		},
	}
	if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"}); err == nil {
		t.Fatal("expected an error for an external backend policy name colliding with an emitted component policy")
	}
}

// The collision check keys on emitted policy names, not on component existence: a same-named
// component that emits NO inbound policy is not a conflict, so the external policy is still emitted.
func TestTransform_ExternalBackend_NameNoCollisionWhenComponentHasNoPolicy(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				// db has a differing Service name and no routing trait → emits no db-allow-ingress-traffic.
				{
					Name:       "db",
					Type:       "statefulset",
					Properties: map[string]any{"image": "postgres:16", "port": 5432, "serviceName": "db-headless"},
				},
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							"rules": []any{map[string]any{"backendRefs": []any{map[string]any{
								"name": "db", "port": 8080,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}},
							}}}},
							"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
						},
					}},
				},
			},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	np := synthesizedNetworkPolicy(t, cluster, "db-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels; got["app.kubernetes.io/name"] != "external" || len(got) != 1 {
		t.Errorf("podSelector = %v, want the external backend selector app.kubernetes.io/name=external", got)
	}
}

// A backendSelector on a ref that resolves to a sibling in-bundle component is ignored — #227
// component-label targeting takes precedence and no error is raised.
func TestTransform_ExternalBackend_SelectorOnSiblingComponent_Ignored(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							"rules": []any{map[string]any{"backendRefs": []any{map[string]any{
								"name": "backend", "port": 9000,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}},
							}}}},
							"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "gateway-system"}}},
						},
					}},
				},
				{Name: "backend", Type: "webservice", Properties: map[string]any{"image": "api:1.0", "port": 9000}},
			},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	np := synthesizedNetworkPolicy(t, cluster, "backend-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "backend" {
		t.Errorf("selector = %v, want #227 component-label gokure.dev/component=backend (authored selector ignored)", np.Spec.PodSelector.MatchLabels)
	}
	if clusterHasApp(cluster, "external-svc-allow-ingress-traffic") || clusterHasApp(cluster, "backend-allow-backend-ingress") {
		t.Errorf("did not expect an external-backend policy for a resolvable ref; apps: %v", clusterAppNames(cluster))
	}
}

// A backendSelector on a self/implicit backend can never take effect (the allow is retargeted onto
// the component's own pods) → rejected at parse.
func TestTransform_ExternalBackend_SelectorOnSelfBackend_Rejected(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{"host": "example.com", "paths": []any{
							map[string]any{"path": "/", "port": 8080,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "external"}}},
						}}},
					},
				}},
			}},
		},
	}
	if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"}); err == nil {
		t.Fatal("expected an error for backendSelector on a self/implicit backend")
	}
}

// An empty matchLabels backendSelector is rejected — it would otherwise select every pod.
func TestTransform_ExternalBackend_EmptyMatchLabels_Rejected(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := ingressExternalBackendApp("external-svc", 8081, map[string]any{})
	if _, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"}); err == nil {
		t.Fatal("expected an error for an empty matchLabels backendSelector")
	}
}

// Two distinct external Services each with a selector yield two distinct, deterministically named
// policies.
func TestTransform_ExternalBackend_MultipleServices_DistinctNames(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{"host": "example.com", "paths": []any{
							map[string]any{"path": "/a", "backend": "external-a", "port": 8081,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "a"}}},
							map[string]any{"path": "/b", "backend": "external-b", "port": 8082,
								"backendSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "b"}}},
						}}},
						"networkPolicy": map[string]any{"trafficSources": []any{map[string]any{"namespace": "ingress-nginx"}}},
					},
				}},
			}},
		},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	for _, name := range []string{"external-a-allow-ingress-traffic", "external-b-allow-ingress-traffic"} {
		if !clusterHasApp(cluster, name) {
			t.Errorf("expected synthesized %q; apps: %v", name, clusterAppNames(cluster))
		}
	}
}

// #242: a backendRef whose backend component lands in a DIFFERENT tier bundle (hierarchical cluster)
// still retargets — the allow is synthesized on the backend's pods in the backend's tier bundle.
func TestTransform_BackendRef_RetargetsAcrossTierBundles(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})

	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name:       "router",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
					Traits: []oam.Trait{{
						Type: "httproute",
						Properties: map[string]any{
							"parentRefs": []any{map[string]any{"name": "gw"}},
							"rules": []any{map[string]any{
								"backendRefs": []any{map[string]any{"name": "db-headless", "port": 5432}},
							}},
							"networkPolicy": map[string]any{
								"trafficSources": []any{map[string]any{"namespace": "gateway-system"}},
							},
						},
					}},
				},
				{
					// Annotated into the services tier so router (apps) and db (services) land in
					// separate leaf bundles → forces the cross-bundle resolution path.
					Name:        "db",
					Type:        "statefulset",
					Annotations: map[string]string{"gokure.dev/tier": "services"},
					Properties:  map[string]any{"image": "postgres:16", "port": 5432, "serviceName": "db-headless"},
				},
			},
		},
	}

	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if !clusterHasApp(cluster, "db-allow-ingress-traffic") {
		t.Fatalf("expected db-allow-ingress-traffic across tier bundles; apps: %v", clusterAppNames(cluster))
	}
	np := synthesizedNetworkPolicy(t, cluster, "db-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "db" {
		t.Errorf("target selector = %v, want gokure.dev/component=db", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 5432 {
		t.Errorf("expected a single ingress rule on port 5432, got %+v", np.Spec.Ingress)
	}
	if clusterHasApp(cluster, "router-allow-ingress-traffic") {
		t.Errorf("router-only exposer should get no self policy; apps: %v", clusterAppNames(cluster))
	}
}
