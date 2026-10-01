package traits_test

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam"
)

// go-kure/launcher#280: a sibling group whose `service` member selects its own
// `deployment` member's pods. When every Service port targets its own number, the
// group keeps the inbound policy on its component label, as one component
// deploying both does; any remapped or named target port keeps the policy on the
// Service's selector pods, on the target ports.

// ownPodsRule emits a `deployment` and a `service` selecting its pods under the
// authored name. The authored traits go to the service member, depTraits to the
// deployment member.
type ownPodsRule struct {
	ports     []any
	depTraits []oam.Trait
}

func (ownPodsRule) ComponentType() string { return "own-pods" }

func (r ownPodsRule) LowerComponent(c *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{
		{Name: c.Name, Type: "deployment", Properties: map[string]any{"image": "ghcr.io/example/web:1.0"}, Traits: r.depTraits},
		{Name: c.Name, Type: "service", Properties: map[string]any{
			"selector": map[string]any{"app": c.Name},
			"ports":    r.ports,
		}, Traits: c.Traits},
	}}, nil
}

// ownPodsPolicy transforms one own-pods component "web" with an ingress trait (and
// any extra service-member traits before it) and returns its synthesized policy.
func ownPodsPolicy(t *testing.T, rule ownPodsRule, svcTraits ...oam.Trait) *networkingv1.NetworkPolicy {
	t.Helper()
	tr := serviceKindTransformer()
	tr.RegisterComponentLowering(rule)
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name: "web", Type: "own-pods",
			Traits: append(svcTraits, ingressTrait(map[string]any{"path": "/"})),
		}}},
	}
	cluster, _, err := tr.TransformWithPolicy(app, oam.TransformContext{
		Namespace: "default", Capabilities: ingressNetworkPolicyCapabilities("ingress-nginx"),
	})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	return synthesizedNetworkPolicy(t, cluster, "web-allow-ingress-traffic")
}

func assertAllow(t *testing.T, np *networkingv1.NetworkPolicy, wantSelector map[string]string, wantPort intstr.IntOrString) {
	t.Helper()
	if sel := np.Spec.PodSelector.MatchLabels; !reflect.DeepEqual(sel, wantSelector) {
		t.Errorf("podSelector = %v, want %v", sel, wantSelector)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected one ingress rule, got %+v", np.Spec.Ingress)
	}
	ports := np.Spec.Ingress[0].Ports
	if len(ports) != 1 || *ports[0].Port != wantPort || *ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("ports = %+v, want only TCP %s", ports, wantPort.String())
	}
}

var (
	componentLabel = map[string]string{"gokure.dev/component": "web"}
	selectorLabel  = map[string]string{"app": "web"}
	identityPorts  = []any{map[string]any{"name": "http", "port": 8080}}
)

func TestSiblingGroup_OwnPodsOnIdentityPortsKeepComponentPolicy(t *testing.T) {
	assertAllow(t, ownPodsPolicy(t, ownPodsRule{ports: identityPorts}), componentLabel, intstr.FromInt32(8080))
}

func TestSiblingGroup_OwnPodsOnRemappedPortsTargetSelectorPods(t *testing.T) {
	cases := []struct {
		name  string
		ports []any
		want  intstr.IntOrString
	}{
		{"remapped port", []any{map[string]any{"name": "http", "port": 80, "targetPort": 8080}}, intstr.FromInt32(8080)},
		{"named targetPort", []any{map[string]any{"name": "http", "port": 8080, "targetPort": "http"}}, intstr.FromString("http")},
		{"remapped non-TCP port", []any{
			map[string]any{"name": "http", "port": 8080},
			map[string]any{"name": "dns", "port": 53, "targetPort": 5353, "protocol": "UDP"},
		}, intstr.FromInt32(8080)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertAllow(t, ownPodsPolicy(t, ownPodsRule{ports: tc.ports}), selectorLabel, tc.want)
		})
	}
}

// A trait decorator wrapping either member must not hide what the group reads.
func TestSiblingGroup_OwnPodsDecoratedMembersKeepComponentPolicy(t *testing.T) {
	prune := oam.Trait{Type: "prune-protection", Properties: map[string]any{}}
	t.Run("deployment member", func(t *testing.T) {
		np := ownPodsPolicy(t, ownPodsRule{ports: identityPorts, depTraits: []oam.Trait{prune}})
		assertAllow(t, np, componentLabel, intstr.FromInt32(8080))
	})
	t.Run("service member", func(t *testing.T) {
		np := ownPodsPolicy(t, ownPodsRule{ports: identityPorts}, prune)
		assertAllow(t, np, componentLabel, intstr.FromInt32(8080))
	})
}
