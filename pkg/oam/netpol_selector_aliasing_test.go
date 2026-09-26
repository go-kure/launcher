package oam

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// selectorRef is one caller-reachable label selector in a generated NetworkPolicy,
// named by where it lives so a failure says which two positions alias.
type selectorRef struct {
	where string
	sel   *metav1.LabelSelector
}

// generatedSelectors runs Generate on cfg and returns every pod selector the emitted
// NetworkPolicies carry: each policy's own spec.podSelector and each ingress/egress
// peer's. Namespace selectors are built fresh per peer from a namespace name and
// are not inputs, so they are not listed.
func generatedSelectors(t *testing.T, cfg stack.ApplicationConfig) []selectorRef {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("web", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var refs []selectorRef
	for _, o := range objs {
		np, ok := (*o).(*networkingv1.NetworkPolicy)
		if !ok {
			continue
		}
		refs = append(refs, selectorRef{np.Name + " spec.podSelector", &np.Spec.PodSelector})
		for i, r := range np.Spec.Ingress {
			for j, p := range r.From {
				if p.PodSelector != nil {
					refs = append(refs, selectorRef{fmt.Sprintf("%s ingress[%d].from[%d]", np.Name, i, j), p.PodSelector})
				}
			}
		}
		for i, r := range np.Spec.Egress {
			for j, p := range r.To {
				if p.PodSelector != nil {
					refs = append(refs, selectorRef{fmt.Sprintf("%s egress[%d].to[%d]", np.Name, i, j), p.PodSelector})
				}
			}
		}
	}
	if len(refs) < 2 {
		t.Fatalf("generated %d selectors, want the policy's own plus at least one peer", len(refs))
	}
	return refs
}

func mapPtr(m map[string]string) uintptr {
	if m == nil {
		return 0
	}
	return reflect.ValueOf(m).Pointer()
}

// assertNoSelectorAliasing fails when an emitted selector is an input selector, or
// shares its MatchLabels map with an input or with another emitted selector.
// Pointer identity, not content: two equal maps and one map read twice compare
// equal by content, and only the second lets an edit to one policy rewrite another.
func assertNoSelectorAliasing(t *testing.T, inputs []*metav1.LabelSelector, emitted []selectorRef) {
	t.Helper()
	for _, e := range emitted {
		for _, in := range inputs {
			if e.sel == in {
				t.Errorf("%s is the input selector itself", e.where)
			}
			if p := mapPtr(e.sel.MatchLabels); p != 0 && p == mapPtr(in.MatchLabels) {
				t.Errorf("%s shares its matchLabels map with the input selector", e.where)
			}
		}
	}
	for i := range emitted {
		for j := i + 1; j < len(emitted); j++ {
			a, b := emitted[i], emitted[j]
			if a.sel == b.sel {
				t.Errorf("%s and %s are one selector", a.where, b.where)
			}
			if p := mapPtr(a.sel.MatchLabels); p != 0 && p == mapPtr(b.sel.MatchLabels) {
				t.Errorf("%s and %s share one matchLabels map", a.where, b.where)
			}
		}
	}
}

// TestSynthesizedNetworkPolicies_ShareNoSelector is go-kure/launcher#396 part 1.
// Synthesis handed the traffic source's own *LabelSelector to every emitted peer
// and copied a selector struct by value (which shares its MatchLabels map) into
// spec.podSelector, so a consumer stamping a label onto one generated policy
// rewrote every other policy built from the same source, and the retained trait
// configuration with it. Each synthesized family is covered, with one source used
// by two rules so two peers in one policy would share it too.
func TestSynthesizedNetworkPolicies_ShareNoSelector(t *testing.T) {
	ports := func(p ...int32) []intstr.IntOrString {
		out := make([]intstr.IntOrString, len(p))
		for i, v := range p {
			out[i] = intstr.FromInt32(v)
		}
		return out
	}
	newSel := func(v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"app": v}}
	}

	t.Run("component allow-ingress", func(t *testing.T) {
		src := newSel("router")
		sources := []netpol.TrafficSource{{Namespace: "ingress", PodSelector: src}}
		cfg := &componentAllowPolicyConfig{ComponentName: "web", Rules: []trafficRule{
			{Sources: sources, Ports: ports(80)},
			{Sources: sources, Ports: ports(8080)},
		}}
		assertNoSelectorAliasing(t, []*metav1.LabelSelector{src}, generatedSelectors(t, cfg))
	})

	t.Run("external backend allow-ingress", func(t *testing.T) {
		backend, src := newSel("backend"), newSel("router")
		sources := []netpol.TrafficSource{{Namespace: "ingress", PodSelector: src}}
		cfg := &backendIngressAllowPolicyConfig{PolicyName: "ext-allow", PodSelector: backend, Rules: []trafficRule{
			{Sources: sources, Ports: ports(80)},
			{Sources: sources, Ports: ports(8080)},
		}}
		assertNoSelectorAliasing(t, []*metav1.LabelSelector{backend, src}, generatedSelectors(t, cfg))
	})

	t.Run("component allow-egress", func(t *testing.T) {
		dst := newSel("db")
		cfg := &componentEgressPolicyConfig{ComponentName: "web", Peers: []netpol.EgressPeer{
			{Namespace: "data", PodSelector: dst, Ports: ports(5432)},
			{Namespace: "data", PodSelector: dst, Ports: ports(6432)},
		}}
		assertNoSelectorAliasing(t, []*metav1.LabelSelector{dst}, generatedSelectors(t, cfg))
	})

	t.Run("endpoint allow-ingress", func(t *testing.T) {
		ep, src := newSel("operator"), newSel("prometheus")
		sources := []netpol.TrafficSource{{Namespace: "monitoring", PodSelector: src}}
		cfg := &componentEndpointIngressPolicyConfig{
			ComponentName: "web",
			Endpoint:      netpol.Endpoint{PodSelector: ep, Ports: ports(9090)},
			Rules:         []endpointIngressRule{{Sources: sources}, {Sources: sources}},
		}
		assertNoSelectorAliasing(t, []*metav1.LabelSelector{ep, src}, generatedSelectors(t, cfg))
	})
}
