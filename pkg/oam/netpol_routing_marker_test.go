package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// go-kure/launcher#790: a serviceRoutingTargeter may answer a non-nil selector
// without labels, the marker "owns its Service name and selects no pods". The
// tests below pin that the marker never becomes a policy: it yields no rule, and
// every place a selector could reach an object refuses it.

var markerRule = []trafficRule{{
	Sources: []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
	Ports:   []intstr.IntOrString{intstr.FromInt32(5432)},
}}

// markerStub answers the marker and no port, as an ExternalName Service does.
type markerStub struct{ siblingStub }

func (*markerStub) ServiceRoutingTarget([]intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	return &metav1.LabelSelector{}, nil
}

// The marker comes with no port, so no rule survives and no selector is returned.
func TestRetargetTrafficRules_MarkerDropsEveryRule(t *testing.T) {
	sel, rules, err := retargetTrafficRules("db", &markerStub{}, markerRule)
	if err != nil {
		t.Fatalf("retargetTrafficRules: %v", err)
	}
	if sel != nil || len(rules) != 0 {
		t.Fatalf("retargetTrafficRules = (%v, %v), want no selector and no rule", sel, rules)
	}
}

// A config that answered the marker together with a port would ask for a policy
// on every pod of the namespace: the synthesis fails instead of writing it.
// siblingStub returns the routed ports as they came.
func TestRetargetTrafficRules_MarkerWithASurvivingPortFails(t *testing.T) {
	leaky := &siblingStub{selector: &metav1.LabelSelector{}}
	sel, rules, err := retargetTrafficRules("db", leaky, markerRule)
	if err == nil {
		t.Fatalf("retargetTrafficRules = (%v, %v, nil), want an error", sel, rules)
	}
	for _, want := range []string{`component "db": routing target`, "pod selector must have non-empty matchLabels"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
	if sel != nil || rules != nil {
		t.Errorf("retargetTrafficRules returned (%v, %v) beside the error", sel, rules)
	}
}

// A sibling group never reads the marker as selecting a sibling's pods, even
// from a member whose ports all map to themselves.
func TestSiblingGroup_MarkerSelectsNoSibling(t *testing.T) {
	pods := &podsStub{labels: map[string]string{"app": "db"}}
	route := &identityStub{siblingStub: siblingStub{selector: &metav1.LabelSelector{}}, identity: true}
	g := &siblingGroupConfig{members: []*stack.Application{
		stack.NewApplication("db", "ns", pods),
		stack.NewApplication("db", "ns", route),
	}}
	if g.selectsSibling(1, &metav1.LabelSelector{}) {
		t.Error("selectsSibling = true for a selector without labels")
	}
	if g.routesToOwnPods() {
		t.Error("routesToOwnPods = true for a member answering the marker")
	}
}

// A directly-built backend policy carrying the marker writes no object.
func TestBackendIngressAllowPolicy_MarkerWritesNoObject(t *testing.T) {
	cfg := &backendIngressAllowPolicyConfig{
		ComponentName: "db", PolicyName: "db-allow-ingress-traffic",
		PodSelector: &metav1.LabelSelector{}, Rules: markerRule,
	}
	objs, err := cfg.Generate(stack.NewApplication("db-allow-ingress-traffic", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("Generate wrote %d object(s), want none", len(objs))
	}
	// The control: the same config with a real selector does write one.
	cfg.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}}
	if objs, err := cfg.Generate(stack.NewApplication("db-allow-ingress-traffic", "default", cfg)); err != nil || len(objs) != 1 {
		t.Fatalf("Generate with a real selector = (%d object(s), %v), want one", len(objs), err)
	}
}
