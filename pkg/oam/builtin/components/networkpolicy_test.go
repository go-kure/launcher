package components_test

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestNetworkPolicyHandler_CanHandle(t *testing.T) {
	h := &components.NetworkPolicyHandler{}
	if !h.CanHandle("networkpolicy") {
		t.Error("CanHandle(networkpolicy) = false")
	}
	if h.CanHandle("cilium-networkpolicy") {
		t.Error("CanHandle(cilium-networkpolicy) = true")
	}
}

// TestNetworkPolicyHandler_EmitsIdentityOnly: no field of the spec is required
// by the decode, and none is filled. The pod selector stays the empty one the
// API reads as every pod of the namespace: the kind does not default it to a
// component's pods, as the trait of the same name does.
func TestNetworkPolicyHandler_EmitsIdentityOnly(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"no properties":    nil,
		"empty properties": {},
		"null fields":      {"podSelector": nil, "ingress": nil, "egress": nil, "policyTypes": nil},
		"empty selector":   {"podSelector": map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			np := generateCoreKind(t, &components.NetworkPolicyHandler{}, "networkpolicy", "db-allow", props).(*networkingv1.NetworkPolicy)
			if np.APIVersion != "networking.k8s.io/v1" || np.Kind != "NetworkPolicy" {
				t.Errorf("GVK = %s %s, want networking.k8s.io/v1 NetworkPolicy", np.APIVersion, np.Kind)
			}
			if np.Namespace != coreKindNamespace {
				t.Errorf("namespace = %q, want the build namespace %q", np.Namespace, coreKindNamespace)
			}
			if !reflect.DeepEqual(np.Spec, networkingv1.NetworkPolicySpec{}) {
				t.Errorf("spec = %+v, want empty", np.Spec)
			}
		})
	}
}

// TestNetworkPolicyHandler_EmitsAuthoredSpec: the selector, the rules and the
// policy types as written. A peer's selector picks pods by label; it is not a
// component reference and nothing resolves it.
func TestNetworkPolicyHandler_EmitsAuthoredSpec(t *testing.T) {
	np := generateCoreKind(t, &components.NetworkPolicyHandler{}, "networkpolicy", "db-allow", map[string]any{
		"podSelector": map[string]any{"matchLabels": map[string]any{"role": "db"}},
		"policyTypes": []any{"Ingress", "Egress"},
		"ingress": []any{map[string]any{
			"from": []any{
				map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"role": "api"}}},
				map[string]any{"namespaceSelector": map[string]any{}},
			},
			"ports": []any{map[string]any{"protocol": "TCP", "port": 5432}},
		}},
		"egress": []any{map[string]any{
			"to":    []any{map[string]any{"ipBlock": map[string]any{"cidr": "10.0.0.0/8", "except": []any{"10.1.0.0/16"}}}},
			"ports": []any{map[string]any{"port": "dns", "endPort": 5353}},
		}},
	}).(*networkingv1.NetworkPolicy)

	tcp, port, dns, endPort := corev1.ProtocolTCP, intstr.FromInt32(5432), intstr.FromString("dns"), int32(5353)
	want := networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"role": "db"}},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		Ingress: []networkingv1.NetworkPolicyIngressRule{{
			From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "api"}}},
				{NamespaceSelector: &metav1.LabelSelector{}},
			},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
		}},
		Egress: []networkingv1.NetworkPolicyEgressRule{{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8", Except: []string{"10.1.0.0/16"}}}},
			Ports: []networkingv1.NetworkPolicyPort{{Port: &dns, EndPort: &endPort}},
		}},
	}
	if !reflect.DeepEqual(np.Spec, want) {
		t.Errorf("spec = %+v, want %+v", np.Spec, want)
	}
}

// TestNetworkPolicyHandler_EmptyRuleIsAuthored: an empty rule is the API's
// allow-all and is carried when the author writes it. It is never the result of
// a null (TestNetworkPolicyHandler_Refusals).
func TestNetworkPolicyHandler_EmptyRuleIsAuthored(t *testing.T) {
	np := generateCoreKind(t, &components.NetworkPolicyHandler{}, "networkpolicy", "allow-all", map[string]any{
		"ingress": []any{map[string]any{}},
	}).(*networkingv1.NetworkPolicy)
	want := networkingv1.NetworkPolicySpec{Ingress: []networkingv1.NetworkPolicyIngressRule{{}}}
	if !reflect.DeepEqual(np.Spec, want) {
		t.Errorf("spec = %+v, want %+v", np.Spec, want)
	}
}

// TestNetworkPolicyConfig_ReportsNoTraffic: the config is an authored object.
// It implements none of the methods the NetworkPolicy synthesis reads a trait's
// config through, so the synthesis neither allows anything for it nor counts it
// as the policy of a component.
func TestNetworkPolicyConfig_ReportsNoTraffic(t *testing.T) {
	var cfg any = &components.NetworkPolicyConfig{}
	if _, ok := cfg.(interface{ TargetComponentName() string }); ok {
		t.Error("NetworkPolicyConfig reports a target component; the NetworkPolicy synthesis would read it as a routing trait")
	}
	if _, ok := cfg.(interface{ ComponentName() string }); ok {
		t.Error("NetworkPolicyConfig names an owning component; it is a component's own config, not a trait's")
	}
}

func TestNetworkPolicyHandler_Refusals(t *testing.T) {
	const notASpec = "properties do not decode into a networking.k8s.io/v1 NetworkPolicySpec"
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"unknown key":          {map[string]any{"selector": map[string]any{}}, notASpec},
		"trait property":       {map[string]any{"name": "db-allow"}, notASpec},
		"cilium field":         {map[string]any{"endpointSelector": map[string]any{}}, notASpec},
		"status":               {map[string]any{"status": map[string]any{}}, notASpec},
		"metadata":             {map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, notASpec},
		"ingress a map":        {map[string]any{"ingress": map[string]any{"from": []any{}}}, notASpec},
		"selector sub-key":     {map[string]any{"podSelector": map[string]any{"labels": map[string]any{"a": "b"}}}, notASpec},
		"peer sub-key":         {map[string]any{"ingress": []any{map[string]any{"from": []any{map[string]any{"pods": map[string]any{}}}}}}, notASpec},
		"rule direction":       {map[string]any{"ingress": []any{map[string]any{"to": []any{}}}}, notASpec},
		"policy type a number": {map[string]any{"policyTypes": []any{1}}, notASpec},
		"null ingress rule":    {map[string]any{"ingress": []any{nil}}, "ingress[0]"},
		"null egress rule":     {map[string]any{"egress": []any{map[string]any{}, nil}}, "egress[1]"},
		"null peer":            {map[string]any{"ingress": []any{map[string]any{"from": []any{nil}}}}, "ingress[0].from[0]"},
		"null port":            {map[string]any{"egress": []any{map[string]any{"ports": []any{nil}}}}, "egress[0].ports[0]"},
		"two spellings":        {map[string]any{"ingress": []any{}, "Ingress": []any{}}, "sets the same field as"},
	} {
		t.Run(name, func(t *testing.T) {
			err := coreKindErr(&components.NetworkPolicyHandler{}, "networkpolicy", "db-allow", tc.props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestNetworkPolicyHandler_TypedNilElement: a rule that is a typed nil (a
// document built in Go, not parsed from YAML) is refused as a null one is, not
// decoded to an empty rule, which allows everything.
func TestNetworkPolicyHandler_TypedNilElement(t *testing.T) {
	err := coreKindErr(&components.NetworkPolicyHandler{}, "networkpolicy", "db-allow", map[string]any{
		"ingress": []any{map[string]any(nil)},
	})
	if err == nil || !strings.Contains(err.Error(), "ingress[0]") {
		t.Fatalf("err = %v, want one naming ingress[0]", err)
	}
}
