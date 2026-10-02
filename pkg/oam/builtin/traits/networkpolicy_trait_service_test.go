package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// go-kure/launcher#399: a trait-level serviceName names the Service a routing trait targets. When that Service is
// not the routing component's own, automatic NetworkPolicy synthesis must treat it as an external
// backend — retarget the allow onto the component owning it, or leave it authored when nothing in
// the package owns it — and never land it on the routing component's pods.

// traitServiceRoute builds a routing trait of the given type that reaches its backend only through
// the trait-level serviceName/servicePort pair (no path/backendRef-level name).
func traitServiceRoute(traitType, serviceName string, servicePort int) oam.Trait {
	props := map[string]any{
		"serviceName": serviceName,
		"servicePort": servicePort,
	}
	switch traitType {
	case "ingress":
		props["rules"] = []any{map[string]any{
			"host":  "example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}}
	case "httproute":
		props["parentRefs"] = []any{map[string]any{"name": "gw"}}
		props["rules"] = []any{map[string]any{}}
	}
	return oam.Trait{Type: traitType, Properties: props}
}

func traitServiceTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	registerWebservice(tr) // also registers "deployment", "service" and "topology-spread"
	tr.RegisterComponentLowering(components.WorkerRule{})
	tr.RegisterComponent("statefulset", &components.StatefulsetHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	return tr
}

func traitServiceCapabilities(traitType string) map[string]oam.CapabilityBinding {
	if traitType == "ingress" {
		return ingressNetworkPolicyCapabilities("ingress-nginx")
	}
	return httprouteNetworkPolicyCapabilities("gateway-system")
}

func traitServiceSourceNamespace(traitType string) string {
	if traitType == "ingress" {
		return "ingress-nginx"
	}
	return "gateway-system"
}

// routerProps are the properties of a Service-less router of kind. A statefulset names a governing
// Service it does not own (go-kure/launcher#690).
func routerProps(kind string) map[string]any {
	props := map[string]any{"image": "api:1.0"}
	if kind == "statefulset" {
		props["serviceName"] = "api-headless"
	}
	return props
}

// billingComponents are a backend workload and the authored `service` that owns billing-svc and
// selects its pods, on port 8080.
func billingComponents() []oam.Component {
	return []oam.Component{
		{Name: "billing", Type: "deployment", Properties: map[string]any{"image": "billing:1.0"}},
		{Name: "billing-svc", Type: "service", Properties: map[string]any{
			"selector": map[string]any{"app": "billing"},
			"ports":    []any{map[string]any{"name": "http", "port": 8080, "targetPort": 8080}},
		}},
	}
}

func transformTraitService(t *testing.T, traitType string, comps []oam.Component) *stack.Cluster {
	t.Helper()
	// apiVersion and kind are set because worker is a lowering rule: once any
	// rule is registered, the engine validates the settled document.
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	cluster, _, err := traitServiceTransformer().TransformWithPolicy(app,
		oam.TransformContext{Namespace: "default", Capabilities: traitServiceCapabilities(traitType)})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	return cluster
}

// The issue's reproduction: a Service-less router (worker, deployment, or a statefulset whose
// serviceName names a Service it does not own) routes through a trait-level serviceName to a
// Service owned by another component in the package. The allow lands on the pods that Service
// selects, on the trait's servicePort, and the router gets none.
func TestTransform_TraitServiceName_RetargetsToOwningComponent(t *testing.T) {
	for _, traitType := range []string{"ingress", "httproute"} {
		for _, kind := range []string{"worker", "deployment", "statefulset"} {
			t.Run(traitType+"/"+kind, func(t *testing.T) {
				cluster := transformTraitService(t, traitType, append([]oam.Component{{
					Name:       "api",
					Type:       kind,
					Properties: routerProps(kind),
					Traits:     []oam.Trait{traitServiceRoute(traitType, "billing-svc", 8080)},
				}}, billingComponents()...))

				if clusterHasApp(cluster, "api-allow-ingress-traffic") {
					t.Errorf("router %q must get no allow for a Service it does not own; apps: %v", "api", clusterAppNames(cluster))
				}
				if !clusterHasApp(cluster, "billing-svc-allow-ingress-traffic") {
					t.Fatalf("expected the allow retargeted to \"billing-svc-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
				}
				np := synthesizedNetworkPolicy(t, cluster, "billing-svc-allow-ingress-traffic")
				if got := np.Spec.PodSelector.MatchLabels["app"]; got != "billing" {
					t.Errorf("target selector = %v, want app=billing", np.Spec.PodSelector.MatchLabels)
				}
				if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 ||
					np.Spec.Ingress[0].Ports[0].Port.IntVal != 8080 {
					t.Fatalf("expected a single ingress rule on port 8080, got %+v", np.Spec.Ingress)
				}
				from := np.Spec.Ingress[0].From
				if len(from) != 1 || from[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != traitServiceSourceNamespace(traitType) {
					t.Errorf("expected the router's traffic source %q, got %+v", traitServiceSourceNamespace(traitType), from)
				}
			})
		}
	}
}

// A trait-level serviceName no component in the package owns takes the external-bare-Service path:
// with no backendSelector to say which pods back it, it is left authored — no policy for it, and
// none on the router. A statefulset's serviceName does not make it the owner of the Service it
// names: routing to that name is external too (go-kure/launcher#690).
func TestTransform_TraitServiceName_Unowned_LeavesAuthored(t *testing.T) {
	for _, traitType := range []string{"ingress", "httproute"} {
		for _, tc := range []struct{ kind, service string }{
			{"worker", "billing-svc"},
			{"deployment", "billing-svc"},
			{"statefulset", "billing-svc"},
			{"statefulset", "api-headless"},
		} {
			t.Run(traitType+"/"+tc.kind+"/"+tc.service, func(t *testing.T) {
				cluster := transformTraitService(t, traitType, []oam.Component{{
					Name:       "api",
					Type:       tc.kind,
					Properties: routerProps(tc.kind),
					Traits:     []oam.Trait{traitServiceRoute(traitType, tc.service, 8080)},
				}})
				for _, n := range clusterAppNames(cluster) {
					if n == "api-allow-ingress-traffic" || n == tc.service+"-allow-ingress-traffic" {
						t.Errorf("expected no synthesized ingress policy for an unowned trait-level Service, got %q", n)
					}
				}
			})
		}
	}
}

// A statefulset routing to its governing Service, authored as a `service` component: the allow
// lands on the pods that Service selects, under the Service's name, not on the router
// (go-kure/launcher#690).
func TestTransform_TraitServiceName_StatefulsetGoverningService(t *testing.T) {
	for _, traitType := range []string{"ingress", "httproute"} {
		t.Run(traitType, func(t *testing.T) {
			cluster := transformTraitService(t, traitType, []oam.Component{
				{
					Name:       "api",
					Type:       "statefulset",
					Properties: routerProps("statefulset"),
					Traits:     []oam.Trait{traitServiceRoute(traitType, "api-headless", 8080)},
				},
				{Name: "api-headless", Type: "service", Properties: map[string]any{
					"clusterIP": "None",
					"selector":  map[string]any{"app": "api"},
					"ports":     []any{map[string]any{"name": "tcp", "port": 8080}},
				}},
			})
			if clusterHasApp(cluster, "api-allow-ingress-traffic") {
				t.Errorf("router must get no allow under its own name; apps: %v", clusterAppNames(cluster))
			}
			if !clusterHasApp(cluster, "api-headless-allow-ingress-traffic") {
				t.Fatalf("expected \"api-headless-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
			}
			np := synthesizedNetworkPolicy(t, cluster, "api-headless-allow-ingress-traffic")
			if got := np.Spec.PodSelector.MatchLabels["app"]; got != "api" {
				t.Errorf("target selector = %v, want app=api", np.Spec.PodSelector.MatchLabels)
			}
			if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 ||
				np.Spec.Ingress[0].Ports[0].Port.IntVal != 8080 {
				t.Errorf("expected a single ingress rule on port 8080, got %+v", np.Spec.Ingress)
			}
		})
	}
}

// Regression guard: servicePort alone (no serviceName) routes to the Service named after the
// component — the documented "chart creates it" pattern — and stays a self backend.
func TestTransform_TraitServicePortOnly_StaysSelf(t *testing.T) {
	for _, traitType := range []string{"ingress", "httproute"} {
		t.Run(traitType, func(t *testing.T) {
			trait := traitServiceRoute(traitType, "", 8080)
			delete(trait.Properties, "serviceName")
			cluster := transformTraitService(t, traitType, []oam.Component{{
				Name:       "api",
				Type:       "worker",
				Properties: map[string]any{"image": "api:1.0"},
				Traits:     []oam.Trait{trait},
			}})
			if !clusterHasApp(cluster, "api-allow-ingress-traffic") {
				t.Errorf("expected the self allow \"api-allow-ingress-traffic\"; apps: %v", clusterAppNames(cluster))
			}
		})
	}
}

// A trait-level external default combined with an explicit path/backendRef to another Service in
// the package: each policy gets only its own ports — the default's servicePort on billing-svc's
// pods, the explicit route's port on api-headless's. Before go-kure/launcher#690 the router, a
// statefulset, owned api-headless itself; it is now an authored `service`.
func TestTransform_TraitServiceName_MixedWithSelfRoute_SplitsPorts(t *testing.T) {
	for _, traitType := range []string{"ingress", "httproute"} {
		t.Run(traitType, func(t *testing.T) {
			trait := traitServiceRoute(traitType, "billing-svc", 8080)
			switch traitType {
			case "ingress":
				trait.Properties["rules"] = []any{map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{"path": "/"},
						map[string]any{"path": "/self", "backend": "api-headless", "port": 9000},
					},
				}}
			case "httproute":
				trait.Properties["rules"] = []any{
					map[string]any{},
					map[string]any{"backendRefs": []any{map[string]any{"name": "api-headless", "port": 9000}}},
				}
			}
			cluster := transformTraitService(t, traitType, append([]oam.Component{
				{Name: "api", Type: "statefulset", Properties: routerProps("statefulset"), Traits: []oam.Trait{trait}},
				{Name: "api-headless", Type: "service", Properties: map[string]any{
					"clusterIP": "None",
					"selector":  map[string]any{"app": "api"},
					"ports":     []any{map[string]any{"name": "tcp", "port": 9000}},
				}},
			}, billingComponents()...))
			if clusterHasApp(cluster, "api-allow-ingress-traffic") {
				t.Errorf("router must get no allow under its own name; apps: %v", clusterAppNames(cluster))
			}
			for comp, want := range map[string]int32{"api-headless": 9000, "billing-svc": 8080} {
				name := comp + "-allow-ingress-traffic"
				if !clusterHasApp(cluster, name) {
					t.Fatalf("expected %q; apps: %v", name, clusterAppNames(cluster))
				}
				np := synthesizedNetworkPolicy(t, cluster, name)
				if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 ||
					np.Spec.Ingress[0].Ports[0].Port.IntVal != want {
					t.Errorf("%s: expected a single ingress rule on port %d, got %+v", name, want, np.Spec.Ingress)
				}
			}
		})
	}
}
