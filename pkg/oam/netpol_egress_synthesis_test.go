package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// --- buildEgressPeers ---

func TestBuildEgressPeers_SkipsEmptyPorts(t *testing.T) {
	peers := buildEgressPeers([]netpol.EgressPeer{
		{Namespace: "db", Ports: nil},
	})
	if len(peers) != 0 {
		t.Errorf("expected 0 peers (empty ports), got %d", len(peers))
	}
}

func TestBuildEgressPeers_DeduplicatesIdentical(t *testing.T) {
	mk := func() netpol.EgressPeer {
		return netpol.EgressPeer{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}
	}
	peers := buildEgressPeers([]netpol.EgressPeer{mk(), mk()})
	if len(peers) != 1 {
		t.Errorf("expected 1 deduplicated peer, got %d", len(peers))
	}
}

func TestBuildEgressPeers_DistinctPeersPreserved(t *testing.T) {
	peers := buildEgressPeers([]netpol.EgressPeer{
		{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}},
		{Namespace: "cache", Ports: []intstr.IntOrString{intstr.FromInt32(6379)}},
	})
	if len(peers) != 2 {
		t.Errorf("expected 2 distinct peers, got %d", len(peers))
	}
}

// TestBuildEgressPeers_DeterministicOrder verifies that shuffled peer order and
// shuffled per-peer port order produce byte-identical normalized output.
func TestBuildEgressPeers_DeterministicOrder(t *testing.T) {
	a := buildEgressPeers([]netpol.EgressPeer{
		{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(443), intstr.FromInt32(80)}},
		{Namespace: "cache", Ports: []intstr.IntOrString{intstr.FromInt32(6379)}},
	})
	b := buildEgressPeers([]netpol.EgressPeer{
		{Namespace: "cache", Ports: []intstr.IntOrString{intstr.FromInt32(6379)}},
		{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(80), intstr.FromInt32(443)}},
	})
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if egressPeerKey(a[i]) != egressPeerKey(b[i]) {
			t.Errorf("peer %d order/normalization differs:\n a=%s\n b=%s", i, egressPeerKey(a[i]), egressPeerKey(b[i]))
		}
	}
	// db (namespace-sorted after "cache") must carry ports in ascending order.
	if a[1].Namespace != "db" || a[1].Ports[0].IntVal != 80 || a[1].Ports[1].IntVal != 443 {
		t.Errorf("expected db peer with ports [80 443], got %+v", a[1])
	}
}

// --- componentEgressPolicyConfig.Generate (full shape) ---

func egressPolicyFromGenerate(t *testing.T, cfg *componentEgressPolicyConfig) *networkingv1.NetworkPolicy {
	t.Helper()
	app := stack.NewApplication(cfg.ComponentName+"-allow-egress-traffic", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	np, ok := (*objs[0]).(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("expected *NetworkPolicy, got %T", *objs[0])
	}
	return np
}

func TestComponentEgressPolicyConfig_Generate_Shape(t *testing.T) {
	cfg := &componentEgressPolicyConfig{
		ComponentName: "web",
		Peers: []netpol.EgressPeer{
			{
				Namespace:   "db",
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}},
				Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
			},
			{
				Namespace:   "cache",
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "redis"}},
				Ports:       []intstr.IntOrString{intstr.FromInt32(6379)},
			},
		},
	}
	np := egressPolicyFromGenerate(t, cfg)

	if np.Name != "web-allow-egress-traffic" {
		t.Errorf("name = %q, want web-allow-egress-traffic", np.Name)
	}
	if np.Labels != nil || np.Annotations != nil {
		t.Errorf("expected nil Labels/Annotations, got labels=%v annotations=%v", np.Labels, np.Annotations)
	}
	if got := np.Spec.PodSelector.MatchLabels["gokure.dev/component"]; got != "web" {
		t.Errorf("podSelector gokure.dev/component = %q, want web", got)
	}
	if _, hasApp := np.Spec.PodSelector.MatchLabels["app"]; hasApp {
		t.Errorf("podSelector should not carry legacy app key: %v", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress]", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 0 {
		t.Errorf("expected no ingress rules, got %d", len(np.Spec.Ingress))
	}
	// One egress rule per peer, in deterministic (namespace-sorted) order:
	// "cache" before "db".
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("expected 2 egress rules (one per peer), got %d", len(np.Spec.Egress))
	}

	// First peer: cache with pod selector app=redis, port 6379/TCP.
	r0 := np.Spec.Egress[0]
	if len(r0.To) != 1 {
		t.Fatalf("rule 0: expected 1 peer, got %d", len(r0.To))
	}
	if got := r0.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; got != "cache" {
		t.Errorf("rule 0 namespace selector = %q, want cache", got)
	}
	if r0.To[0].PodSelector == nil || r0.To[0].PodSelector.MatchLabels["app"] != "redis" {
		t.Errorf("rule 0 pod selector = %v, want app=redis", r0.To[0].PodSelector)
	}
	if len(r0.Ports) != 1 || r0.Ports[0].Port.IntVal != 6379 || *r0.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("rule 0 ports = %v, want [6379/TCP]", r0.Ports)
	}

	// Second peer: db with pod selector + port 5432/TCP.
	r1 := np.Spec.Egress[1]
	if got := r1.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; got != "db" {
		t.Errorf("rule 1 namespace selector = %q, want db", got)
	}
	if r1.To[0].PodSelector == nil || r1.To[0].PodSelector.MatchLabels["app"] != "postgres" {
		t.Errorf("rule 1 pod selector = %v, want app=postgres", r1.To[0].PodSelector)
	}
	if len(r1.Ports) != 1 || r1.Ports[0].Port.IntVal != 5432 || *r1.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("rule 1 ports = %v, want [5432/TCP]", r1.Ports)
	}
}

// TestComponentEgressPolicyConfig_PodSelectorKey verifies the top-level podSelector key
// (the egress source pods): empty defaults to gokure.dev/component, and a non-empty
// PodSelectorKey wins (e.g. a non-downstream caller opting back to "app").
func TestComponentEgressPolicyConfig_PodSelectorKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantKey string
	}{
		{"default", "", "gokure.dev/component"},
		{"override_app", "app", "app"},
		{"override_custom", "example.com/name", "example.com/name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &componentEgressPolicyConfig{
				ComponentName:  "web",
				PodSelectorKey: tc.key,
				Peers: []netpol.EgressPeer{
					{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}},
				},
			}
			np := egressPolicyFromGenerate(t, cfg)
			if got := np.Spec.PodSelector.MatchLabels[tc.wantKey]; got != "web" {
				t.Errorf("podSelector[%q] = %q, want web (labels=%v)", tc.wantKey, got, np.Spec.PodSelector.MatchLabels)
			}
			if len(np.Spec.PodSelector.MatchLabels) != 1 {
				t.Errorf("podSelector should have exactly one label, got %v", np.Spec.PodSelector.MatchLabels)
			}
		})
	}
}

// TestComponentEgressPolicyConfig_Generate_SkipsEmptyPortPeer guards the security
// invariant at the Generate boundary: a directly-built config with an empty-port
// peer must not emit an all-ports egress rule.
func TestComponentEgressPolicyConfig_Generate_SkipsEmptyPortPeer(t *testing.T) {
	cfg := &componentEgressPolicyConfig{
		ComponentName: "web",
		Peers: []netpol.EgressPeer{
			{Namespace: "db", Ports: nil}, // no derivable ports → must be dropped
			{Namespace: "cache", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "redis"}}, Ports: []intstr.IntOrString{intstr.FromInt32(6379)}},
		},
	}
	np := egressPolicyFromGenerate(t, cfg)
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("expected 1 egress rule (empty-port peer dropped), got %d", len(np.Spec.Egress))
	}
	if got := np.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; got != "cache" {
		t.Errorf("surviving rule namespace = %q, want cache", got)
	}
}

// --- fail-fast on invalid egress peers (#224) ---

// TestSynthesizeEgress_ErrorsOnInvalidSelector is the loud layer: a ported peer with a nil,
// empty-matchLabels, or expression-bearing selector fails the build rather than emitting a
// namespace-wide egress allow. The len(Ports)==0 escape hatch stays a silent skip.
func TestSynthesizeEgress_ErrorsOnInvalidSelector(t *testing.T) {
	cases := []struct {
		name    string
		peer    netpol.EgressPeer
		wantErr bool
	}{
		{"nil selector with ports", netpol.EgressPeer{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, true},
		{"empty matchLabels with ports", netpol.EgressPeer{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, true},
		{"matchExpressions with ports", netpol.EgressPeer{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: metav1.LabelSelectorOpExists}}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, true},
		{"nil selector no ports (escape hatch)", netpol.EgressPeer{Namespace: "db"}, false},
		{"valid selector with ports", netpol.EgressPeer{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster, componentMap, _ := egressFixture("web", "default")
			peers := map[string][]netpol.EgressPeer{"web": {tc.peer}}
			err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %s: %v", tc.name, err)
			}
		})
	}
}

// TestSynthesizeEgress_ErrorsOnUnmatchedComponent verifies the loud layer validates the whole
// non-authorable input up front — a malformed peer keyed by a component absent from the bundle
// still fails the build (a producer bug should not depend on which components render).
func TestSynthesizeEgress_ErrorsOnUnmatchedComponent(t *testing.T) {
	cluster, componentMap, _ := egressFixture("web", "default")
	peers := map[string][]netpol.EgressPeer{
		"other": {{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}}, // ported, selector-less
	}
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel); err == nil {
		t.Error("expected error for malformed peer on unmatched component, got nil")
	}
}

// TestComponentEgressPolicyConfig_Generate_RejectsInvalidSelector is the residual layer: even a
// directly-built config (bypassing the synthesis boundary) must not emit a ported peer with an
// invalid selector.
func TestComponentEgressPolicyConfig_Generate_RejectsInvalidSelector(t *testing.T) {
	cfg := &componentEgressPolicyConfig{
		ComponentName: "web",
		Peers: []netpol.EgressPeer{
			{Namespace: "db", Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}, // ported, selector-less
		},
	}
	app := stack.NewApplication("web-allow-egress-traffic", "default", cfg)
	if _, err := cfg.Generate(app); err == nil {
		t.Error("expected Generate to reject a ported selector-less peer, got nil")
	}
}

// --- synthesizeEgressNetworkPolicies ---

// egressFixture builds a single-leaf cluster with one primary component app plus a
// matching componentMap entry.
func egressFixture(compName, namespace string) (*stack.Cluster, map[string]componentEntry, *stack.Application) {
	primary := stack.NewApplication(compName, namespace, &stubAppConfig{})
	bundle := &stack.Bundle{Applications: []*stack.Application{primary}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{
		compName: {component: Component{Name: compName}, app: primary},
	}
	return cluster, componentMap, primary
}

func egressAppNames(cluster *stack.Cluster) []string {
	var names []string
	walkLeafBundles(cluster.Node, func(b *stack.Bundle) {
		for _, a := range b.Applications {
			names = append(names, a.Name)
		}
	})
	return names
}

func TestSynthesizeEgress_AppendsPolicy(t *testing.T) {
	cluster, componentMap, primary := egressFixture("web", "default")
	peers := map[string][]netpol.EgressPeer{
		"web": {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
	}

	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel); err != nil {
		t.Fatalf("synthesizeEgressNetworkPolicies: %v", err)
	}

	leaf := cluster.Node.Bundle
	if len(leaf.Applications) != 2 {
		t.Fatalf("expected 2 apps after synthesis, got %d (%v)", len(leaf.Applications), egressAppNames(cluster))
	}
	added := leaf.Applications[1]
	if added.Name != "web-allow-egress-traffic" {
		t.Errorf("added app name = %q, want web-allow-egress-traffic", added.Name)
	}
	if added.Namespace != primary.Namespace {
		t.Errorf("added app namespace = %q, want %q", added.Namespace, primary.Namespace)
	}
	if _, ok := added.Config.(*componentEgressPolicyConfig); !ok {
		t.Errorf("expected *componentEgressPolicyConfig, got %T", added.Config)
	}
}

func TestSynthesizeEgress_NoOpWhenNoPeers(t *testing.T) {
	cluster, componentMap, _ := egressFixture("web", "default")
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, nil, ComponentLabel); err != nil {
		t.Fatalf("synthesizeEgressNetworkPolicies: %v", err)
	}
	if n := len(cluster.Node.Bundle.Applications); n != 1 {
		t.Errorf("expected no synthesis for nil peers, got %d apps", n)
	}
}

func TestSynthesizeEgress_NoOpWhenNoComponentMatch(t *testing.T) {
	cluster, componentMap, _ := egressFixture("web", "default")
	// Peers keyed by a component that is not present in the bundle/componentMap.
	peers := map[string][]netpol.EgressPeer{
		"other": {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
	}
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel); err != nil {
		t.Fatalf("synthesizeEgressNetworkPolicies: %v", err)
	}
	if n := len(cluster.Node.Bundle.Applications); n != 1 {
		t.Errorf("expected no synthesis for unmatched component, got %d apps", n)
	}
}

// TestSynthesizeEgress_IdentityGuard ensures a trait sub-app whose Name collides
// with a component name does NOT receive the egress policy: only the primary
// entry.app matches, by pointer identity.
func TestSynthesizeEgress_IdentityGuard(t *testing.T) {
	primary := stack.NewApplication("web", "app-ns", &stubAppConfig{})
	// A different application that happens to share the component's name.
	collider := stack.NewApplication("web", "other-ns", &stubAppConfig{})
	bundle := &stack.Bundle{Applications: []*stack.Application{collider, primary}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{
		"web": {component: Component{Name: "web"}, app: primary},
	}
	peers := map[string][]netpol.EgressPeer{
		"web": {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
	}

	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel); err != nil {
		t.Fatalf("synthesizeEgressNetworkPolicies: %v", err)
	}

	var policies []*stack.Application
	for _, a := range bundle.Applications {
		if a.Name == "web-allow-egress-traffic" {
			policies = append(policies, a)
		}
	}
	if len(policies) != 1 {
		t.Fatalf("expected exactly 1 egress policy, got %d", len(policies))
	}
	// It must be scoped to the primary app's namespace, not the collider's.
	if policies[0].Namespace != "app-ns" {
		t.Errorf("egress policy namespace = %q, want app-ns (primary), not the collider's", policies[0].Namespace)
	}
}
