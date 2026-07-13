package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// src builds a fail-closed traffic source (namespace + matchLabels pod selector).
func src(ns, k, v string) netpol.TrafficSource {
	return netpol.TrafficSource{Namespace: ns, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}}
}

// endpointPolicyOrNil runs Generate and returns nil for zero objects or the single
// NetworkPolicy for one object (fail on anything else) — invalid-input cases assert nil.
func endpointPolicyOrNil(t *testing.T, cfg *componentEndpointIngressPolicyConfig) *networkingv1.NetworkPolicy {
	t.Helper()
	app := stack.NewApplication(cfg.ComponentName+"-allow-endpoint-ingress", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	switch len(objs) {
	case 0:
		return nil
	case 1:
		np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
		if !ok {
			t.Fatalf("expected *NetworkPolicy, got %T", *objs[0])
		}
		return np
	default:
		t.Fatalf("expected 0 or 1 object, got %d", len(objs))
		return nil
	}
}

func validEndpoint() netpol.Endpoint {
	return netpol.Endpoint{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"cnpg.io/cluster": "pg"}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
	}
}

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		ep      netpol.Endpoint
		wantErr bool
	}{
		{"valid", validEndpoint(), false},
		{"nil selector", netpol.Endpoint{Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, true},
		{"empty matchLabels", netpol.Endpoint{
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{}},
			Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
		}, true},
		{"matchExpressions present", netpol.Endpoint{
			PodSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{"a": "b"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "x", Operator: metav1.LabelSelectorOpExists}},
			},
			Ports: []intstr.IntOrString{intstr.FromInt32(5432)},
		}, true},
		{"empty ports", netpol.Endpoint{
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEndpoint(tc.ep)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestComponentEndpointIngressPolicyConfig_Generate_Shape(t *testing.T) {
	cfg := &componentEndpointIngressPolicyConfig{
		ComponentName: "pg",
		Endpoint:      validEndpoint(),
		Rules:         []endpointIngressRule{{Sources: []netpol.TrafficSource{src("app", "app", "web")}}},
	}
	np := endpointPolicyOrNil(t, cfg)
	if np == nil {
		t.Fatal("expected a policy, got nil")
	}
	if np.Name != "pg-allow-endpoint-ingress" {
		t.Errorf("name = %q", np.Name)
	}
	if np.Labels != nil || np.Annotations != nil {
		t.Errorf("expected nil labels/annotations")
	}
	if got := np.Spec.PodSelector.MatchLabels["cnpg.io/cluster"]; got != "pg" || len(np.Spec.PodSelector.MatchLabels) != 1 {
		t.Errorf("podSelector = %v, want single cnpg.io/cluster=pg", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("policyTypes = %v, want [Ingress]", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 0 {
		t.Errorf("expected no egress rules")
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].From) != 1 {
		t.Fatalf("expected 1 ingress rule with 1 peer, got %+v", np.Spec.Ingress)
	}
	from := np.Spec.Ingress[0].From[0]
	if from.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "app" {
		t.Errorf("namespace selector = %v, want app", from.NamespaceSelector)
	}
	if from.PodSelector == nil || from.PodSelector.MatchLabels["app"] != "web" {
		t.Errorf("pod selector = %v, want app=web", from.PodSelector)
	}
	if len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 5432 {
		t.Errorf("ports = %v, want [5432]", np.Spec.Ingress[0].Ports)
	}
}

func TestComponentEndpointIngressPolicyConfig_Generate_FailClosed(t *testing.T) {
	base := validEndpoint()
	cases := []struct {
		name string
		cfg  *componentEndpointIngressPolicyConfig
	}{
		{"nil rules", &componentEndpointIngressPolicyConfig{ComponentName: "pg", Endpoint: base, Rules: nil}},
		{"rule with empty sources", &componentEndpointIngressPolicyConfig{ComponentName: "pg", Endpoint: base,
			Rules: []endpointIngressRule{{Sources: nil}}}},
		{"rule with only namespace-wide source", &componentEndpointIngressPolicyConfig{ComponentName: "pg", Endpoint: base,
			Rules: []endpointIngressRule{{Sources: []netpol.TrafficSource{{Namespace: "app"}}}}}},
		{"invalid endpoint", &componentEndpointIngressPolicyConfig{ComponentName: "pg",
			Endpoint: netpol.Endpoint{Ports: []intstr.IntOrString{intstr.FromInt32(5432)}},
			Rules:    []endpointIngressRule{{Sources: []netpol.TrafficSource{src("app", "app", "web")}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if np := endpointPolicyOrNil(t, tc.cfg); np != nil {
				t.Errorf("expected no policy, got %+v", np.Spec)
			}
		})
	}
}

func TestGroupIngressPeers_DropsMalformedAndDedups(t *testing.T) {
	peers := []netpol.IngressPeer{
		{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}},
		// duplicate source set for the same endpoint → deduped
		{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}},
		// malformed endpoint (nil selector) → dropped
		{Endpoint: netpol.Endpoint{Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, Sources: []netpol.TrafficSource{src("app", "app", "web")}},
		// all sources namespace-wide → dropped
		{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{{Namespace: "x"}}},
	}
	groups := groupIngressPeers(peers)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].Rules) != 1 {
		t.Errorf("expected 1 deduped rule, got %d", len(groups[0].Rules))
	}
}

func TestGroupIngressPeers_TwoDistinctEndpoints(t *testing.T) {
	ep2 := netpol.Endpoint{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"cnpg.io/cluster": "pg", "role": "pooler"}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(6432)},
	}
	groups := groupIngressPeers([]netpol.IngressPeer{
		{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}},
		{Endpoint: ep2, Sources: []netpol.TrafficSource{src("app", "app", "web")}},
	})
	if len(groups) != 2 {
		t.Fatalf("expected 2 endpoint groups, got %d", len(groups))
	}
}

func TestSynthesizeEndpointIngress_AppendsPolicy(t *testing.T) {
	cluster, componentMap, _ := egressFixture("pg", "default")
	peers := map[string][]netpol.IngressPeer{
		"pg": {{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}}},
	}
	synthesizeEndpointIngressNetworkPolicies(cluster, componentMap, peers)
	names := egressAppNames(cluster)
	found := false
	for _, n := range names {
		if n == "pg-allow-endpoint-ingress" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pg-allow-endpoint-ingress; apps: %v", names)
	}
}

func TestSynthesizeEndpointIngress_NoOpAndGuards(t *testing.T) {
	// no-op when no peers
	cluster, componentMap, _ := egressFixture("pg", "default")
	synthesizeEndpointIngressNetworkPolicies(cluster, componentMap, nil)
	if got := len(egressAppNames(cluster)); got != 1 {
		t.Errorf("expected no synthesis without peers, got %d apps", got)
	}
	// no-op when no component match
	cluster2, componentMap2, _ := egressFixture("pg", "default")
	synthesizeEndpointIngressNetworkPolicies(cluster2, componentMap2, map[string][]netpol.IngressPeer{
		"other": {{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}}},
	})
	if got := len(egressAppNames(cluster2)); got != 1 {
		t.Errorf("expected no synthesis for unmatched component, got %d apps", got)
	}
	// multi-endpoint → skip (no app emitted)
	cluster3, componentMap3, _ := egressFixture("pg", "default")
	ep2 := netpol.Endpoint{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}, Ports: []intstr.IntOrString{intstr.FromInt32(6432)}}
	synthesizeEndpointIngressNetworkPolicies(cluster3, componentMap3, map[string][]netpol.IngressPeer{
		"pg": {
			{Endpoint: validEndpoint(), Sources: []netpol.TrafficSource{src("app", "app", "web")}},
			{Endpoint: ep2, Sources: []netpol.TrafficSource{src("app", "app", "web")}},
		},
	})
	for _, n := range egressAppNames(cluster3) {
		if n == "pg-allow-endpoint-ingress" {
			t.Errorf("expected multi-endpoint skip, but a policy was emitted")
		}
	}
}

func TestValidSources_DropsInvalidVariants(t *testing.T) {
	cases := []struct {
		name string
		s    netpol.TrafficSource
		keep bool
	}{
		{"valid", src("app", "app", "web"), true},
		{"empty namespace", netpol.TrafficSource{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}, false},
		{"nil pod selector", netpol.TrafficSource{Namespace: "app"}, false},
		{"empty matchLabels", netpol.TrafficSource{Namespace: "app", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{}}}, false},
		{"matchExpressions present", netpol.TrafficSource{Namespace: "app", PodSelector: &metav1.LabelSelector{
			MatchLabels:      map[string]string{"app": "web"},
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "x", Operator: metav1.LabelSelectorOpExists}},
		}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validSources([]netpol.TrafficSource{tc.s})
			if tc.keep && len(got) != 1 {
				t.Errorf("expected source kept, got %d", len(got))
			}
			if !tc.keep && len(got) != 0 {
				t.Errorf("expected source dropped, got %d", len(got))
			}
		})
	}
}

// Emitted rule From order is byte-stable regardless of caller source order.
func TestGenerate_StableSourceOrder(t *testing.T) {
	a, b := src("app", "app", "web"), src("db", "app", "api")
	fwd := &componentEndpointIngressPolicyConfig{ComponentName: "pg", Endpoint: validEndpoint(),
		Rules: []endpointIngressRule{{Sources: []netpol.TrafficSource{a, b}}}}
	rev := &componentEndpointIngressPolicyConfig{ComponentName: "pg", Endpoint: validEndpoint(),
		Rules: []endpointIngressRule{{Sources: []netpol.TrafficSource{b, a}}}}
	nsOrder := func(cfg *componentEndpointIngressPolicyConfig) []string {
		np := endpointPolicyOrNil(t, cfg)
		var out []string
		for _, p := range np.Spec.Ingress[0].From {
			out = append(out, p.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
		}
		return out
	}
	f, r := nsOrder(fwd), nsOrder(rev)
	if len(f) != 2 || len(r) != 2 {
		t.Fatalf("expected 2 sources each, got fwd=%v rev=%v", f, r)
	}
	for i := range f {
		if f[i] != r[i] {
			t.Errorf("From order not stable: fwd=%v rev=%v", f, r)
		}
	}
}
