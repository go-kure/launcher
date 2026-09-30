package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// longNetpolComponentName is a valid component name (a DNS-1123 subdomain) well
// over the 63-character label-value limit.
func longNetpolComponentName(t *testing.T, stem string) string {
	t.Helper()
	name := stem + "." + strings.Repeat("long-component-name-segment.", 5) + "end"
	if len(name) <= validation.LabelValueMaxLength {
		t.Fatalf("test name is %d characters, want more than %d", len(name), validation.LabelValueMaxLength)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Fatalf("test name %q is not a valid component name: %v", name, errs)
	}
	return name
}

// assertComponentSelector checks a synthesized policy's own-pod selector: exactly
// the component label, valued at ComponentLabelValue(component) — the value the
// platform stamps with the same function — and a valid label value.
func assertComponentSelector(t *testing.T, sel metav1.LabelSelector, key, component string) {
	t.Helper()
	got, ok := sel.MatchLabels[key]
	if !ok || len(sel.MatchLabels) != 1 {
		t.Fatalf("podSelector = %v, want exactly %s", sel.MatchLabels, key)
	}
	if want := ComponentLabelValue(component); got != want {
		t.Errorf("podSelector[%s] = %q, want ComponentLabelValue(name) = %q", key, got, want)
	}
	if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
		t.Errorf("podSelector[%s] = %q is not a valid label value: %v", key, got, errs)
	}
}

// TestSynthesizeNetworkPolicies_LongComponentName_ProjectsSelector pins
// go-kure/launcher#572 on the inbound family: a component whose name exceeds 63
// characters gets a policy whose selector value is its projected label value,
// both for its own routing trait and when another component's backendRef
// retargets onto it.
func TestSynthesizeNetworkPolicies_LongComponentName_ProjectsSelector(t *testing.T) {
	self := longNetpolComponentName(t, "router")
	backend := longNetpolComponentName(t, "backend")

	routerApp := stack.NewApplication(self+"-ingress", "default", &stubCollector{
		component: self,
		sources:   []netpol.TrafficSource{{Namespace: "ingress-nginx"}},
		ports:     []intstr.IntOrString{intstr.FromInt32(80)},
	})
	backendApp := stack.NewApplication(backend, "default", svcPortConfig{port: 9000}) // owns Service <backend>
	retarget := stack.NewApplication("edge-ingress", "default", &extBackendStub{
		component: "edge",
		sources:   []netpol.TrafficSource{{Namespace: "gateway-system"}},
		targets:   []netpol.BackendTarget{{ServiceName: backend, Ports: []intstr.IntOrString{intstr.FromInt32(9000)}}},
	})
	bundle := &stack.Bundle{Applications: []*stack.Application{routerApp, backendApp, retarget}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{
		self:    {app: routerApp},
		backend: {app: backendApp},
		"edge":  {app: retarget},
	}
	const key = "launcher.gokure.dev/component"
	if err := synthesizeNetworkPolicies(cluster, componentMap, key); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	assertComponentSelector(t, synthesizedNPInBundle(t, bundle, self+"-allow-ingress-traffic").Spec.PodSelector, key, self)
	assertComponentSelector(t, synthesizedNPInBundle(t, bundle, backend+"-allow-ingress-traffic").Spec.PodSelector, key, backend)
}

// TestSynthesizeEgress_LongComponentName_ProjectsSelector: the egress family
// selects its source pods by the same projected value.
func TestSynthesizeEgress_LongComponentName_ProjectsSelector(t *testing.T) {
	name := longNetpolComponentName(t, "client")
	cluster, componentMap, _ := egressFixture(name, "default")
	peers := map[string][]netpol.EgressPeer{
		name: {{Namespace: "db", PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgres"}}, Ports: []intstr.IntOrString{intstr.FromInt32(5432)}}},
	}
	if err := synthesizeEgressNetworkPolicies(cluster, componentMap, peers, ComponentLabel); err != nil {
		t.Fatalf("synthesizeEgressNetworkPolicies: %v", err)
	}
	np := synthesizedNPInBundle(t, cluster.Node.Bundle, name+"-allow-egress-traffic")
	assertComponentSelector(t, np.Spec.PodSelector, ComponentLabel, name)
}
