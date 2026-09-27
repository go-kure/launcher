package traits_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// go-kure/launcher#411: a `service` component fronts pods another component owns. Routing
// traits on it (or backendRefs naming it) must land their synthesized allow on the service's
// `selector` pods, on the target ports those pods listen on — never on the component label,
// which no pod of a differently named workload carries.

func serviceKindTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("service", &components.ServiceHandler{})
	tr.RegisterComponent("deployment", &components.DeploymentHandler{})
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	tr.RegisterBuiltinTrait("httproute", &traits.HTTPRouteHandler{})
	tr.RegisterBuiltinTrait("prune-protection", &traits.PruneProtectionHandler{})
	return tr
}

// apiServiceComponent is a service named "api" in front of the deployment "api-server": http
// 80 -> 8080 over TCP, plus a UDP port.
func apiServiceComponent(traits ...oam.Trait) oam.Component {
	return oam.Component{
		Name: "api",
		Type: "service",
		Properties: map[string]any{
			"selector": map[string]any{"app": "api-server"},
			"ports": []any{
				map[string]any{"name": "http", "port": 80, "targetPort": 8080},
				map[string]any{"name": "dns", "port": 53, "protocol": "UDP"},
			},
		},
		Traits: traits,
	}
}

func apiServerComponent() oam.Component {
	return oam.Component{
		Name:       "api-server",
		Type:       "deployment",
		Properties: map[string]any{"image": "ghcr.io/example/api:1.0"},
	}
}

func ingressTrait(path map[string]any) oam.Trait {
	return oam.Trait{Type: "ingress", Properties: map[string]any{
		"rules": []any{map[string]any{"host": "api.example.com", "paths": []any{path}}},
	}}
}

// assertSelectorTargetedAllow checks the one synthesized ingress rule selects the service's
// selector pods on TCP 8080, from the given traffic-source namespace.
func assertSelectorTargetedAllow(t *testing.T, np *networkingv1.NetworkPolicy, sourceNS string) {
	t.Helper()
	if sel := np.Spec.PodSelector.MatchLabels; len(sel) != 1 || sel["app"] != "api-server" {
		t.Errorf("podSelector = %v, want the service selector app=api-server (not the component label)", sel)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected one ingress rule, got %+v", np.Spec.Ingress)
	}
	rule := np.Spec.Ingress[0]
	if len(rule.Ports) != 1 || rule.Ports[0].Port.IntVal != 8080 || *rule.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("ports = %+v, want only TCP 8080 (the targetPort, not the Service port 80)", rule.Ports)
	}
	if len(rule.From) != 1 || rule.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != sourceNS {
		t.Errorf("from = %+v, want the %s traffic source", rule.From, sourceNS)
	}
}

func TestTransform_ServiceKind_IngressTargetsSelectorPodsOnTargetPort(t *testing.T) {
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			apiServerComponent(),
			apiServiceComponent(ingressTrait(map[string]any{"path": "/"})),
		}},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx"),
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	assertSelectorTargetedAllow(t, synthesizedNetworkPolicy(t, cluster, "api-allow-ingress-traffic"), "ingress-nginx")
	if clusterHasApp(cluster, "api-server-allow-ingress-traffic") {
		t.Errorf("the workload has no routing trait of its own; apps: %v", clusterAppNames(cluster))
	}
}

// A trait decorator wrapping the service config must not hide the retargeting.
func TestTransform_ServiceKind_DecoratedStillTargetsSelectorPods(t *testing.T) {
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			apiServerComponent(),
			apiServiceComponent(
				oam.Trait{Type: "prune-protection", Properties: map[string]any{}},
				ingressTrait(map[string]any{"path": "/"}),
			),
		}},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx"),
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	assertSelectorTargetedAllow(t, synthesizedNetworkPolicy(t, cluster, "api-allow-ingress-traffic"), "ingress-nginx")
}

// A router in another component names the service by backendRef: the injected allow lands on
// the service's selector pods on the targetPort.
func TestTransform_ServiceKind_BackendRefRetargetsToSelectorPods(t *testing.T) {
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8081},
				Traits: []oam.Trait{{
					Type: "httproute",
					Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules": []any{map[string]any{
							"backendRefs": []any{map[string]any{"name": "api", "port": 80}},
						}},
					},
				}},
			},
			apiServerComponent(),
			apiServiceComponent(),
		}},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: httprouteNetworkPolicyCapabilities("gateway-system"),
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	assertSelectorTargetedAllow(t, synthesizedNetworkPolicy(t, cluster, "api-allow-ingress-traffic"), "gateway-system")
}

// Routing traits default to, and accept only, the first port: a path naming the service's
// second port without an explicit backend is refused.
func TestTransform_ServiceKind_NonFirstPortNeedsExplicitBackend(t *testing.T) {
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			apiServerComponent(),
			apiServiceComponent(ingressTrait(map[string]any{"path": "/", "port": 53})),
		}},
	}
	_, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{Namespace: "default"})
	if err == nil || !strings.Contains(err.Error(), "cannot route implicit backend to port 53") {
		t.Fatalf("expected the implicit-backend port refusal, got %v", err)
	}
}

// A route whose only port is the UDP one — reachable with an explicit backend — synthesizes no
// allow: the rules are TCP, and a TCP allow on a UDP targetPort would admit the wrong traffic.
func TestTransform_ServiceKind_UDPOnlyRouteSynthesizesNothing(t *testing.T) {
	app := &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{
				Name:       "router",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8081},
				Traits: []oam.Trait{{
					Type: "httproute",
					Properties: map[string]any{
						"parentRefs": []any{map[string]any{"name": "gw"}},
						"rules": []any{map[string]any{
							"backendRefs": []any{map[string]any{"name": "api", "port": 53}},
						}},
					},
				}},
			},
			apiServerComponent(),
			apiServiceComponent(),
		}},
	}
	cluster, _, err := serviceKindTransformer().TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: httprouteNetworkPolicyCapabilities("gateway-system"),
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if clusterHasApp(cluster, "api-allow-ingress-traffic") {
		t.Errorf("expected no synthesized allow for a UDP-only route; apps: %v", clusterAppNames(cluster))
	}
}
