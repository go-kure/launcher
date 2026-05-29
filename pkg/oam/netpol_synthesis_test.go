package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
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

	synthesizeForBundle(bundle)

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

	synthesizeForBundle(bundle)

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

	synthesizeForBundle(bundle)

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
}
