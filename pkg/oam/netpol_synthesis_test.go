package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// extBackendStub is a router collector (traffic sources + external backend targets, no self ports)
// used to exercise external-backend synthesis across multiple leaf bundles.
type extBackendStub struct {
	component string
	sources   []netpol.TrafficSource
	targets   []netpol.BackendTarget
}

func (s *extBackendStub) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *extBackendStub) TrafficSources() []netpol.TrafficSource                  { return s.sources }
func (s *extBackendStub) TargetComponentName() string                             { return s.component }
func (s *extBackendStub) BackendPorts() []intstr.IntOrString                      { return nil }
func (s *extBackendStub) BackendTargets() []netpol.BackendTarget                  { return s.targets }

// twoLeafBundleCluster builds a cluster whose root node has two child leaf bundles, each holding one
// application — the minimal multi-bundle shape a dependency-aware/hierarchical transform produces.
func twoLeafBundleCluster(a, b *stack.Application) *stack.Cluster {
	return &stack.Cluster{Node: &stack.Node{Children: []*stack.Node{
		{Bundle: &stack.Bundle{Applications: []*stack.Application{a}}},
		{Bundle: &stack.Bundle{Applications: []*stack.Application{b}}},
	}}}
}

func countClusterApps(c *stack.Cluster, name string) int {
	n := 0
	walkLeafBundles(c.Node, func(bundle *stack.Bundle) {
		for _, a := range bundle.Applications {
			if a.Name == name {
				n++
			}
		}
	})
	return n
}

// stubCollector implements trafficSourceCollector and stack.ApplicationConfig for synthesis tests.
// servicePort > 0 makes it also own a Service (a valid backendRef target); 0 (the default) leaves it
// Service-less.
type stubCollector struct {
	component   string
	sources     []netpol.TrafficSource
	ports       []intstr.IntOrString
	servicePort int32
}

func (s *stubCollector) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *stubCollector) TrafficSources() []netpol.TrafficSource                  { return s.sources }
func (s *stubCollector) TargetComponentName() string                             { return s.component }
func (s *stubCollector) BackendPorts() []intstr.IntOrString                      { return s.ports }
func (s *stubCollector) ServicePort() int32                                      { return s.servicePort }

// --- buildTrafficRules ---

func TestBuildTrafficRules_SingleCollector(t *testing.T) {
	col := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	rules := buildTrafficRules([]trafficSourceCollector{col})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].Sources) != 1 || rules[0].Sources[0].Namespace != "ingress-nginx" {
		t.Errorf("unexpected sources: %v", rules[0].Sources)
	}
	if len(rules[0].Ports) != 1 || rules[0].Ports[0].IntVal != 80 {
		t.Errorf("unexpected ports: %v", rules[0].Ports)
	}
}

func TestBuildTrafficRules_SkipsEmptyPorts(t *testing.T) {
	// External-only backend: sources present but no component-local ports.
	col := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     nil,
	}
	if rules := buildTrafficRules([]trafficSourceCollector{col}); len(rules) != 0 {
		t.Errorf("expected 0 rules (empty ports), got %d", len(rules))
	}
}

func TestBuildTrafficRules_SkipsEmptySources(t *testing.T) {
	col := &stubCollector{
		component: "web",
		sources:   nil,
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	if rules := buildTrafficRules([]trafficSourceCollector{col}); len(rules) != 0 {
		t.Errorf("expected 0 rules (empty sources), got %d", len(rules))
	}
}

func TestBuildTrafficRules_DeduplicatesIdentical(t *testing.T) {
	mk := func() *stubCollector {
		return &stubCollector{
			component: "web",
			sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			ports:     []intstr.IntOrString{intstr.FromInt32(80)},
		}
	}
	rules := buildTrafficRules([]trafficSourceCollector{mk(), mk()})
	if len(rules) != 1 {
		t.Errorf("expected 1 deduplicated rule, got %d", len(rules))
	}
}

func TestBuildTrafficRules_DistinctSourcesProduceSeparateRules(t *testing.T) {
	col1 := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	col2 := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(8080)},
	}
	if rules := buildTrafficRules([]trafficSourceCollector{col1, col2}); len(rules) != 2 {
		t.Errorf("expected 2 distinct rules, got %d", len(rules))
	}
}

// --- synthesizeForBundle ---

func TestSynthesizeForBundle_AddsNetworkPolicyApp(t *testing.T) {
	col := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	app := stack.NewApplication("web-ingress", "default", col)
	bundle := &stack.Bundle{Applications: []*stack.Application{app}}

	reg := newNPSynthesisRegistry()
	if err := synthesizeForBundle(bundle, reg); err != nil {
		t.Fatalf("synthesizeForBundle: %v", err)
	}
	if err := reg.emitComponents(ComponentLabel); err != nil {
		t.Fatalf("emitComponents: %v", err)
	}
	reg.flush()

	if len(bundle.Applications) != 2 {
		t.Fatalf("expected 2 applications after synthesis, got %d", len(bundle.Applications))
	}
	last := bundle.Applications[len(bundle.Applications)-1]
	if last.Name != "web-allow-ingress-traffic" {
		t.Errorf("expected app name web-allow-ingress-traffic, got %q", last.Name)
	}
}

func TestSynthesizeForBundle_NoSynthesisWithoutSources(t *testing.T) {
	col := &stubCollector{
		component: "web",
		sources:   nil,
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	app := stack.NewApplication("web-ingress", "default", col)
	bundle := &stack.Bundle{Applications: []*stack.Application{app}}

	reg := newNPSynthesisRegistry()
	if err := synthesizeForBundle(bundle, reg); err != nil {
		t.Fatalf("synthesizeForBundle: %v", err)
	}
	if err := reg.emitComponents(ComponentLabel); err != nil {
		t.Fatalf("emitComponents: %v", err)
	}
	reg.flush()

	if len(bundle.Applications) != 1 {
		t.Errorf("expected no synthesis (no sources), got %d apps", len(bundle.Applications))
	}
}

func TestSynthesizeForBundle_TwoCollectorsSameComponent(t *testing.T) {
	col1 := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	}
	col2 := &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(8080)},
	}
	app1 := stack.NewApplication("web-ingress", "default", col1)
	app2 := stack.NewApplication("web-httproute", "default", col2)
	bundle := &stack.Bundle{Applications: []*stack.Application{app1, app2}}

	reg := newNPSynthesisRegistry()
	if err := synthesizeForBundle(bundle, reg); err != nil {
		t.Fatalf("synthesizeForBundle: %v", err)
	}
	if err := reg.emitComponents(ComponentLabel); err != nil {
		t.Fatalf("emitComponents: %v", err)
	}
	reg.flush()

	// One synthesized policy per component (not per collector).
	count := 0
	for _, a := range bundle.Applications {
		if a.Name == "web-allow-ingress-traffic" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 synthesized policy, got %d", count)
	}
	synthesized := bundle.Applications[len(bundle.Applications)-1]
	cfg, ok := synthesized.Config.(*componentAllowPolicyConfig)
	if !ok {
		t.Fatalf("expected *componentAllowPolicyConfig, got %T", synthesized.Config)
	}
	if len(cfg.Rules) != 2 {
		t.Errorf("expected 2 rules (one per distinct collector), got %d", len(cfg.Rules))
	}
}

func TestComponentAllowPolicyConfig_Generate(t *testing.T) {
	cfg := &componentAllowPolicyConfig{
		ComponentName: "web",
		Rules: []trafficRule{{
			Sources: []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			Ports:   []intstr.IntOrString{intstr.FromInt32(80)},
		}},
	}
	app := stack.NewApplication("web-allow-ingress-traffic", "default", cfg)
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
	if np.Name != "web-allow-ingress-traffic" {
		t.Errorf("name = %q, want web-allow-ingress-traffic", np.Name)
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
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("policyTypes = %v, want [Ingress]", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 0 {
		t.Errorf("expected no egress rules, got %d", len(np.Spec.Egress))
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(np.Spec.Ingress))
	}
	r := np.Spec.Ingress[0]
	if len(r.From) != 1 || r.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "ingress-nginx" {
		t.Errorf("ingress peer = %v, want namespace ingress-nginx", r.From)
	}
	if len(r.Ports) != 1 || r.Ports[0].Port.IntVal != 80 || *r.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("ingress ports = %v, want [80/TCP]", r.Ports)
	}
}

// TestComponentAllowPolicyConfig_PodSelectorKey verifies the top-level podSelector key:
// empty defaults to gokure.dev/component, and a non-empty PodSelectorKey wins (e.g. a
// non-downstream caller opting back to "app").
func TestComponentAllowPolicyConfig_PodSelectorKey(t *testing.T) {
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
			cfg := &componentAllowPolicyConfig{
				ComponentName:  "web",
				PodSelectorKey: tc.key,
				Rules: []trafficRule{{
					Sources: []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
					Ports:   []intstr.IntOrString{intstr.FromInt32(80)},
				}},
			}
			np := generatedNetworkPolicy(t, cfg)
			if got := np.Spec.PodSelector.MatchLabels[tc.wantKey]; got != "web" {
				t.Errorf("podSelector[%q] = %q, want web (labels=%v)", tc.wantKey, got, np.Spec.PodSelector.MatchLabels)
			}
			if len(np.Spec.PodSelector.MatchLabels) != 1 {
				t.Errorf("podSelector should have exactly one label, got %v", np.Spec.PodSelector.MatchLabels)
			}
		})
	}
}

// generatedNetworkPolicy runs an ApplicationConfig's Generate and returns the single
// emitted *NetworkPolicy, failing the test on any deviation.
func generatedNetworkPolicy(t *testing.T, cfg interface {
	Generate(*stack.Application) ([]*client.Object, error)
}) *networkingv1.NetworkPolicy {
	t.Helper()
	app := stack.NewApplication("np", "default", cfg)
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

// --- #239: external-backend synthesis is cluster-wide, not per-bundle ---

// Two routers in DIFFERENT leaf bundles naming the same external Service (same namespace + selector)
// must emit exactly ONE policy — a per-bundle accumulator would emit a duplicate resource id.
func TestSynthesizeNetworkPolicies_ExternalBackend_MultiBundle_SameSelector_SinglePolicy(t *testing.T) {
	mk := func(comp string) *stack.Application {
		return stack.NewApplication(comp+"-ingress", "default", &extBackendStub{
			component: comp,
			sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			targets: []netpol.BackendTarget{{
				ServiceName: "external-svc",
				Ports:       []intstr.IntOrString{intstr.FromInt32(8081)},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "external"}},
			}},
		})
	}
	cluster := twoLeafBundleCluster(mk("router-a"), mk("router-b"))
	if err := synthesizeNetworkPolicies(cluster, nil, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if got := countClusterApps(cluster, "external-svc-allow-ingress-traffic"); got != 1 {
		t.Errorf("expected exactly one external-svc-allow-ingress-traffic across bundles, got %d", got)
	}
}

// The same two routers with DIFFERENT selectors for one external Service must fail the transform —
// a per-bundle check would miss the cross-bundle conflict and emit two colliding allows.
func TestSynthesizeNetworkPolicies_ExternalBackend_MultiBundle_ConflictingSelectors_Errors(t *testing.T) {
	mk := func(comp, label string) *stack.Application {
		return stack.NewApplication(comp+"-ingress", "default", &extBackendStub{
			component: comp,
			sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			targets: []netpol.BackendTarget{{
				ServiceName: "external-svc",
				Ports:       []intstr.IntOrString{intstr.FromInt32(8081)},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": label}},
			}},
		})
	}
	cluster := twoLeafBundleCluster(mk("router-a", "external"), mk("router-b", "other"))
	if err := synthesizeNetworkPolicies(cluster, nil, ComponentLabel); err == nil {
		t.Fatal("expected an error for one external Service given different selectors across bundles")
	}
}

// On any synthesis error, every append is deferred past the failure point, so the cluster is left
// completely unmutated — an earlier bundle's component policy must not survive a later conflict.
func TestSynthesizeNetworkPolicies_ExternalConflict_LeavesClusterUnmutated(t *testing.T) {
	web := stack.NewApplication("web-ingress", "default", &stubCollector{
		component: "web",
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	})
	mkRouter := func(comp, label string) *stack.Application {
		return stack.NewApplication(comp+"-ingress", "default", &extBackendStub{
			component: comp,
			sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
			targets: []netpol.BackendTarget{{
				ServiceName: "external-svc",
				Ports:       []intstr.IntOrString{intstr.FromInt32(8081)},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": label}},
			}},
		})
	}
	// Bundle A also carries a component (web) that would emit an inbound policy; bundle B triggers a
	// cross-bundle external-selector conflict.
	cluster := &stack.Cluster{Node: &stack.Node{Children: []*stack.Node{
		{Bundle: &stack.Bundle{Applications: []*stack.Application{web, mkRouter("router-a", "external")}}},
		{Bundle: &stack.Bundle{Applications: []*stack.Application{mkRouter("router-b", "other")}}},
	}}}
	if err := synthesizeNetworkPolicies(cluster, nil, ComponentLabel); err == nil {
		t.Fatal("expected a cross-bundle selector conflict error")
	}
	if got := countClusterApps(cluster, "web-allow-ingress-traffic"); got != 0 {
		t.Errorf("expected cluster unmutated after error, found %d web-allow-ingress-traffic", got)
	}
	if got := countClusterApps(cluster, "external-svc-allow-ingress-traffic"); got != 0 {
		t.Errorf("expected no external policy after error, found %d", got)
	}
}

// --- #242: backendRef retargeting resolves and emits across leaf bundles ---

// noopConfig is a minimal ApplicationConfig for a component that is neither a router nor emits a
// policy of its own — it just occupies a bundle so it can be a cross-bundle backend target.
type noopConfig struct{}

func (noopConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }

// svcNamerConfig implements serviceBackendNamer to exercise Service-name resolution/collision.
type svcNamerConfig struct{ svc string }

func (svcNamerConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (c svcNamerConfig) BackendServiceName() string                            { return c.svc }

// svcPortConfig implements servicePortProvider: a component that owns a Service named after itself
// (webservice convention) when port > 0, and owns none when port == 0 (e.g. a port-less daemonset).
type svcPortConfig struct{ port int32 }

func (svcPortConfig) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (c svcPortConfig) ServicePort() int32                                    { return c.port }

func bundleAppNames(b *stack.Bundle) []string {
	out := make([]string, 0, len(b.Applications))
	for _, a := range b.Applications {
		out = append(out, a.Name)
	}
	return out
}

func bundleHasApp(b *stack.Bundle, name string) bool {
	for _, a := range b.Applications {
		if a.Name == name {
			return true
		}
	}
	return false
}

func synthesizedNPInBundle(t *testing.T, b *stack.Bundle, name string) *networkingv1.NetworkPolicy {
	t.Helper()
	for _, a := range b.Applications {
		if a.Name != name {
			continue
		}
		objs, err := a.Config.Generate(a)
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
		return np
	}
	t.Fatalf("app %q not found in bundle; apps: %v", name, bundleAppNames(b))
	return nil
}

// crossBundleCluster wires router (bundle A) and backend (bundle B) into a two-leaf-bundle cluster
// with a matching componentMap — the shape a dependency-aware/hierarchical transform produces.
func crossBundleCluster(router, backend *stack.Application) (*stack.Cluster, *stack.Bundle, *stack.Bundle, map[string]componentEntry) {
	bundleA := &stack.Bundle{Applications: []*stack.Application{router}}
	bundleB := &stack.Bundle{Applications: []*stack.Application{backend}}
	cluster := &stack.Cluster{Node: &stack.Node{Children: []*stack.Node{{Bundle: bundleA}, {Bundle: bundleB}}}}
	componentMap := map[string]componentEntry{"router": {app: router}, "backend": {app: backend}}
	return cluster, bundleA, bundleB, componentMap
}

// A backendRef to a component in a DIFFERENT leaf bundle now retargets — the allow lands on the
// backend's pods in the BACKEND's bundle (pre-fix: nothing was synthesized).
func TestSynthesizeNetworkPolicies_CrossBundleRetarget_LandsInBackendBundle(t *testing.T) {
	backend := stack.NewApplication("backend", "default", svcPortConfig{port: 9000}) // owns Service "backend"
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets:   []netpol.BackendTarget{{ServiceName: "backend", Ports: []intstr.IntOrString{intstr.FromInt32(9000)}}},
	})
	cluster, bundleA, bundleB, componentMap := crossBundleCluster(router, backend)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if !bundleHasApp(bundleB, "backend-allow-ingress-traffic") {
		t.Fatalf("expected backend-allow-ingress-traffic in the backend bundle; got %v", bundleAppNames(bundleB))
	}
	if bundleHasApp(bundleA, "backend-allow-ingress-traffic") {
		t.Errorf("backend policy must not land in the router's bundle; got %v", bundleAppNames(bundleA))
	}
	if countClusterApps(cluster, "router-allow-ingress-traffic") != 0 {
		t.Errorf("router (pure exposer, no self ports) must get no self policy")
	}
	np := synthesizedNPInBundle(t, bundleB, "backend-allow-ingress-traffic")
	if np.Spec.PodSelector.MatchLabels[ComponentLabel] != "backend" {
		t.Errorf("podSelector = %v, want %s=backend", np.Spec.PodSelector.MatchLabels, ComponentLabel)
	}
	if len(np.Spec.Ingress) != 1 || len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 9000 {
		t.Errorf("expected ingress on port 9000, got %+v", np.Spec.Ingress)
	}
}

// When the backend also has its own inbound rule (its bundle) AND receives a cross-bundle injection,
// exactly ONE merged policy is emitted in the backend's bundle carrying both sources (anti-duplicate).
func TestSynthesizeNetworkPolicies_CrossBundle_MergesWithBackendOwnRules(t *testing.T) {
	backend := stack.NewApplication("backend", "default", &stubCollector{
		component:   "backend",
		sources:     []netpol.TrafficSource{{Namespace: "mesh"}},
		ports:       []intstr.IntOrString{intstr.FromInt32(9000)},
		servicePort: 9000, // also owns Service "backend" so the router's ref resolves
	})
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets:   []netpol.BackendTarget{{ServiceName: "backend", Ports: []intstr.IntOrString{intstr.FromInt32(9000)}}},
	})
	cluster, _, bundleB, componentMap := crossBundleCluster(router, backend)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if got := countClusterApps(cluster, "backend-allow-ingress-traffic"); got != 1 {
		t.Fatalf("expected exactly one merged backend policy, got %d", got)
	}
	np := synthesizedNPInBundle(t, bundleB, "backend-allow-ingress-traffic")
	if len(np.Spec.Ingress) != 2 {
		t.Fatalf("expected 2 merged ingress rules (own + injected), got %d", len(np.Spec.Ingress))
	}
	seen := map[string]bool{}
	for _, r := range np.Spec.Ingress {
		if len(r.From) == 1 {
			seen[r.From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]] = true
		}
	}
	if !seen["mesh"] || !seen["gateway-system"] {
		t.Errorf("expected both mesh (own) and gateway-system (injected) sources, got %v", seen)
	}
}

// A cross-bundle backendRef to a Service with no owning component and no selector stays authored.
func TestSynthesizeNetworkPolicies_CrossBundle_UnresolvedNoSelector_LeavesAuthored(t *testing.T) {
	backend := stack.NewApplication("backend", "default", noopConfig{})
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets:   []netpol.BackendTarget{{ServiceName: "nonexistent", Ports: []intstr.IntOrString{intstr.FromInt32(9000)}}},
	})
	cluster, _, _, componentMap := crossBundleCluster(router, backend)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if countClusterApps(cluster, "nonexistent-allow-ingress-traffic") != 0 {
		t.Errorf("unresolved backendRef with no selector must stay authored")
	}
}

// A cross-bundle backendRef carrying an authored backendSelector now resolves to the component and
// ignores the selector — component-label targeting wins (intended #239→#242 precedence).
func TestSynthesizeNetworkPolicies_CrossBundleBackendSelector_Ignored(t *testing.T) {
	backend := stack.NewApplication("backend", "default", svcPortConfig{port: 9000}) // owns Service "backend"
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets: []netpol.BackendTarget{{
			ServiceName: "backend",
			Ports:       []intstr.IntOrString{intstr.FromInt32(9000)},
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "external"}},
		}},
	})
	cluster, bundleA, bundleB, componentMap := crossBundleCluster(router, backend)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	np := synthesizedNPInBundle(t, bundleB, "backend-allow-ingress-traffic")
	if np.Spec.PodSelector.MatchLabels[ComponentLabel] != "backend" || len(np.Spec.PodSelector.MatchLabels) != 1 {
		t.Errorf("resolvable cross-bundle ref must use component-label, got %v", np.Spec.PodSelector.MatchLabels)
	}
	if bundleHasApp(bundleA, "backend-allow-ingress-traffic") || countClusterApps(cluster, "backend-allow-ingress-traffic") != 1 {
		t.Errorf("expected exactly one backend policy, in the backend bundle")
	}
}

// R3: two components whose BackendServiceName() collide → ambiguous backendRef target → error.
func TestSynthesizeNetworkPolicies_AmbiguousServiceName_SameBackendServiceName_Errors(t *testing.T) {
	appA := stack.NewApplication("comp-a", "default", svcNamerConfig{svc: "shared"})
	appB := stack.NewApplication("comp-b", "default", svcNamerConfig{svc: "shared"})
	bundle := &stack.Bundle{Applications: []*stack.Application{appA, appB}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{"comp-a": {app: appA}, "comp-b": {app: appB}}
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err == nil {
		t.Fatal("expected an error for two components sharing a Service name")
	}
}

// R3: a Service-owning component's name equals another component's BackendServiceName() → same
// ambiguity → error.
func TestSynthesizeNetworkPolicies_AmbiguousServiceName_NameEqualsOtherBackendServiceName_Errors(t *testing.T) {
	appA := stack.NewApplication("shared", "default", svcPortConfig{port: 8080})     // owns Service "shared"
	appB := stack.NewApplication("comp-b", "default", svcNamerConfig{svc: "shared"}) // BackendServiceName = "shared"
	bundle := &stack.Bundle{Applications: []*stack.Application{appA, appB}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{"shared": {app: appA}, "comp-b": {app: appB}}
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err == nil {
		t.Fatal("expected an error: component name collides with another's BackendServiceName")
	}
}

// A Service-LESS component (e.g. a worker) named the same as a bare external Service must NOT shadow
// it: the router's backendRef stays external and is synthesized from its authored backendSelector,
// not resolved to the worker's component-label pods.
func TestSynthesizeNetworkPolicies_ServicelessComponent_DoesNotShadowExternalBackend(t *testing.T) {
	worker := stack.NewApplication("worker", "default", noopConfig{}) // Service-less
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets: []netpol.BackendTarget{{
			ServiceName: "worker",
			Ports:       []intstr.IntOrString{intstr.FromInt32(9000)},
			PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "external"}},
		}},
	})
	cluster, bundleA, _, componentMap := crossBundleCluster(router, worker) // maps "backend"→worker; add "worker" too
	componentMap["worker"] = componentEntry{app: worker}
	delete(componentMap, "backend")
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	np := synthesizedNPInBundle(t, bundleA, "worker-allow-ingress-traffic") // external → router's bundle
	if np.Spec.PodSelector.MatchLabels["app.kubernetes.io/name"] != "external" || len(np.Spec.PodSelector.MatchLabels) != 1 {
		t.Errorf("expected the authored external selector, got %v (Service-less component wrongly shadowed the external backend)", np.Spec.PodSelector.MatchLabels)
	}
}

// A Service-LESS component name must NOT fabricate an R3 ambiguity with another component's real
// BackendServiceName().
func TestSynthesizeNetworkPolicies_ServicelessComponent_NoFalseAmbiguity(t *testing.T) {
	// "db-headless" is a Service-less component; "db" is a statefulset whose Service is "db-headless".
	appA := stack.NewApplication("db-headless", "default", noopConfig{})
	appB := stack.NewApplication("db", "default", svcNamerConfig{svc: "db-headless"})
	bundle := &stack.Bundle{Applications: []*stack.Application{appA, appB}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{"db-headless": {app: appA}, "db": {app: appB}}
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("a Service-less component name must not collide with a real Service name: %v", err)
	}
}

// A component with an optional Service but no port (ServicePort() == 0, e.g. a port-less daemonset)
// owns no Service and must not be a backendRef target.
func TestSynthesizeNetworkPolicies_ZeroPortComponent_NotAServiceOwner(t *testing.T) {
	backend := stack.NewApplication("zero", "default", svcPortConfig{port: 0}) // no Service
	router := stack.NewApplication("router-ingress", "default", &extBackendStub{
		component: "router",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets:   []netpol.BackendTarget{{ServiceName: "zero", Ports: []intstr.IntOrString{intstr.FromInt32(9000)}}}, // no selector
	})
	cluster, _, _, componentMap := crossBundleCluster(router, backend)
	componentMap["zero"] = componentEntry{app: backend}
	delete(componentMap, "backend")
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	// Not registered as a Service owner → backendRef unresolved → no selector → nothing synthesized.
	if countClusterApps(cluster, "zero-allow-ingress-traffic") != 0 {
		t.Errorf("a port-less component must not be a backendRef target")
	}
}
