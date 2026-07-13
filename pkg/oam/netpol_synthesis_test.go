package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// stubCollector implements trafficSourceCollector and stack.ApplicationConfig for synthesis tests.
type stubCollector struct {
	component string
	sources   []netpol.TrafficSource
	ports     []intstr.IntOrString
}

func (s *stubCollector) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *stubCollector) TrafficSources() []netpol.TrafficSource                  { return s.sources }
func (s *stubCollector) TargetComponentName() string                             { return s.component }
func (s *stubCollector) BackendPorts() []intstr.IntOrString                      { return s.ports }

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

	synthesizeForBundle(bundle, ComponentLabel)

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

	synthesizeForBundle(bundle, ComponentLabel)

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

	synthesizeForBundle(bundle, ComponentLabel)

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
	if got := np.Spec.PodSelector.MatchLabels["wharf.zone/component"]; got != "web" { // allow-term:wharf tracked by #215
		t.Errorf("podSelector wharf.zone/component = %q, want web", got) // allow-term:wharf tracked by #215
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
// empty defaults to wharf.zone/component, and a non-empty PodSelectorKey wins (e.g. a  allow-term:wharf tracked by #215
// non-downstream caller opting back to "app").
func TestComponentAllowPolicyConfig_PodSelectorKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantKey string
	}{
		{"default", "", "wharf.zone/component"}, // allow-term:wharf tracked by #215
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
