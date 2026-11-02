package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
)

// go-kure/launcher#1157: the allow a routing trait gets is built from the owner of the Service it
// routes to and that owner's target ports, not from the routing component, when the Service is not
// named after the routing component.

// ownerRoute is a routing trait on a component, as the cases below author it.
type ownerRoute struct {
	name  string
	trait oam.Trait
	caps  map[string]oam.CapabilityBinding
}

// ownerRoutesToOwnService routes to the component's own Service on port 80 through a trait-level
// servicePort, as a component that exposes no Service port of its own must.
func ownerRoutesToOwnService() []ownerRoute {
	rules := []any{map[string]any{"host": "api.example.com", "paths": []any{map[string]any{"path": "/"}}}}
	return []ownerRoute{
		{"ingress", oam.Trait{Type: "ingress", Properties: map[string]any{"servicePort": 80, "rules": rules}},
			ingressNetworkPolicyCapabilities("ingress-nginx")},
		{"httproute", oam.Trait{Type: "httproute", Properties: map[string]any{
			"servicePort": 80,
			"parentRefs":  []any{map[string]any{"name": "gw"}},
			"rules":       []any{map[string]any{"backendRefs": []any{map[string]any{"port": 80}}}},
		}}, httprouteNetworkPolicyCapabilities("gateway-system")},
	}
}

// apiRouter is a deployment named "api" that carries the routing trait but exposes no Service port.
func apiRouter(trait oam.Trait) oam.Component {
	return oam.Component{
		Name:       "api",
		Type:       "deployment",
		Properties: map[string]any{"image": "ghcr.io/example/api:1.0"},
		Traits:     []oam.Trait{trait},
	}
}

// frontService is a `service` named "front" whose Service is called "api" (objectName) and selects
// the "api-server" pods.
func frontService(ports ...any) oam.Component {
	return oam.Component{
		Name: "front",
		Type: "service",
		Properties: map[string]any{
			"objectName": "api",
			"selector":   map[string]any{"app": "api-server"},
			"ports":      ports,
		},
	}
}

func transformOwnerCase(t *testing.T, caps map[string]oam.CapabilityBinding, components ...oam.Component) *stack.Cluster {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: components},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: caps,
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	return cluster
}

// Case 1: the Service the route names is owned by another component. The route lands on that
// component's selector pods, on its target port.
func TestTransform_SelfRoutedServiceOwnedByAnotherComponent_AllowsTheOwnersPods(t *testing.T) {
	for _, r := range ownerRoutesToOwnService() {
		t.Run(r.name, func(t *testing.T) {
			cluster := transformOwnerCase(t, r.caps,
				apiRouter(r.trait),
				frontService(map[string]any{"name": "http", "port": 80, "targetPort": 8080}),
			)
			np := synthesizedNetworkPolicy(t, cluster, "front-allow-ingress-traffic")
			assertAllow(t, np, map[string]string{"app": "api-server"}, intstr.FromInt32(8080))
			if clusterHasApp(cluster, "api-allow-ingress-traffic") {
				t.Errorf("the route never reaches the pods of api, which owns no Service it names; apps: %v", clusterAppNames(cluster))
			}
		})
	}
}

// Case 1b: the owner's Service has no TCP port the route names, so the route allows nothing, on the
// owner's pods or the router's.
func TestTransform_SelfRoutedServiceOwnedByAnotherComponent_NoMatchingPortAllowsNothing(t *testing.T) {
	owners := map[string]oam.Component{
		"UDP only":           frontService(map[string]any{"name": "dns", "port": 80, "protocol": "UDP"}),
		"routed port absent": frontService(map[string]any{"name": "http", "port": 8081}),
		"ExternalName": {
			Name: "front", Type: "service",
			Properties: map[string]any{"objectName": "api", "type": "ExternalName", "externalName": "api.example.com"},
		},
	}
	for ownerName, owner := range owners {
		for _, r := range ownerRoutesToOwnService() {
			t.Run(ownerName+"/"+r.name, func(t *testing.T) {
				cluster := transformOwnerCase(t, r.caps, apiRouter(r.trait), owner)
				for _, name := range []string{"api-allow-ingress-traffic", "front-allow-ingress-traffic"} {
					if clusterHasApp(cluster, name) {
						t.Errorf("unexpected %s; apps: %v", name, clusterAppNames(cluster))
					}
				}
			})
		}
	}
}

// Controls: the Service the route names is owned by no component, so the allow stays on the routing
// component's pods; and with no traffic source nothing is synthesized in either case.
func TestTransform_SelfRoutedServiceOwnedByNoComponent_StaysOnTheRouter(t *testing.T) {
	for _, r := range ownerRoutesToOwnService() {
		t.Run(r.name, func(t *testing.T) {
			cluster := transformOwnerCase(t, r.caps, apiRouter(r.trait))
			assertAllow(t, synthesizedNetworkPolicy(t, cluster, "api-allow-ingress-traffic"),
				map[string]string{"gokure.dev/component": "api"}, intstr.FromInt32(80))
		})
	}
}

func TestTransform_SelfRoutedServiceOwnedByAnotherComponent_NoTrafficSourcesGenerateNothing(t *testing.T) {
	for _, r := range ownerRoutesToOwnService() {
		t.Run(r.name, func(t *testing.T) {
			cluster := transformOwnerCase(t, nil,
				apiRouter(r.trait),
				frontService(map[string]any{"name": "http", "port": 80, "targetPort": 8080}),
			)
			for _, name := range []string{"api-allow-ingress-traffic", "front-allow-ingress-traffic"} {
				if clusterHasApp(cluster, name) {
					t.Errorf("unexpected %s with no traffic sources; apps: %v", name, clusterAppNames(cluster))
				}
			}
		})
	}
}

// metricsWebservice is a webservice named "metrics" that listens on port, so the component is a
// sibling group of a deployment and a service, and the authored routing trait runs on the
// deployment member. The consumer's Naming hook (transformMetrics) calls the group's Service
// "metrics-http", which the deployment member does not know.
func metricsWebservice(port int, trait oam.Trait) oam.Component {
	return oam.Component{
		Name:       "metrics",
		Type:       "webservice",
		Properties: map[string]any{"image": "ghcr.io/example/metrics:1.0", "port": port},
		Traits:     []oam.Trait{trait},
	}
}

// transformMetrics transforms the "metrics" webservice with a Naming hook that names its Service
// "metrics-http".
func transformMetrics(t *testing.T, r ownerRoute, port int) *stack.Cluster {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: []oam.Component{metricsWebservice(port, r.trait)}},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: r.caps,
		Naming: func(req oam.NameRequest) (string, bool) {
			if req.Component == "metrics" && req.Role == oam.NameRoleWorkloadService {
				return "metrics-http", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	return cluster
}

// routesToRenamedService are routes from the "metrics" group's deployment member to the Service the
// group's service member names "metrics-http", on port 9090.
func routesToRenamedService() []ownerRoute {
	return []ownerRoute{
		{"ingress", ingressTrait(map[string]any{"path": "/", "backend": "metrics-http", "port": 9090}),
			ingressNetworkPolicyCapabilities("ingress-nginx")},
		{"httproute", oam.Trait{Type: "httproute", Properties: map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "metrics-http", "port": 9090}}}},
		}}, httprouteNetworkPolicyCapabilities("gateway-system")},
	}
}

// Case 2 control: a route from the group's deployment member to the Service the group's service
// member names apart. A Naming hook's name reaches the trait as its own Service name, so the
// route already counts as a self route and the allow lands on the group's own pods. The cluster
// level cannot reach the dropped-target shape of go-kure/launcher#1157 case 2 (the group's
// routing config reporting no own port while a target names its own group); that shape is pinned
// by the TargetOwnedByTheRouterIsInjected unit tests in pkg/oam.
func TestTransform_RouteToTheGroupsRenamedService_AllowsTheGroup(t *testing.T) {
	for _, r := range routesToRenamedService() {
		t.Run(r.name, func(t *testing.T) {
			cluster := transformMetrics(t, r, 9090)
			assertAllow(t, synthesizedNetworkPolicy(t, cluster, "metrics-allow-ingress-traffic"),
				map[string]string{"gokure.dev/component": "metrics"}, intstr.FromInt32(9090))
		})
	}
}

// Control: the same route with no traffic source generates nothing.
func TestTransform_RouteToTheGroupsRenamedService_NoTrafficSourcesGenerateNothing(t *testing.T) {
	for _, r := range routesToRenamedService() {
		t.Run(r.name, func(t *testing.T) {
			cluster := transformMetrics(t, ownerRoute{name: r.name, trait: r.trait}, 9090)
			if clusterHasApp(cluster, "metrics-allow-ingress-traffic") {
				t.Errorf("unexpected allow with no traffic sources; apps: %v", clusterAppNames(cluster))
			}
		})
	}
}
