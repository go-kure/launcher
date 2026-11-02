package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// go-kure/launcher#1157: a routing trait's own-Service ports belong to the component that owns that
// Service, and a target the routing component owns is injected, not skipped.

// ownSvcStub is a router collector that reports the Service its own ports belong to
// (ownServiceReporter) and may also route to other Services.
type ownSvcStub struct {
	stubCollector
	own     string
	targets []netpol.BackendTarget
}

func (s *ownSvcStub) OwnServiceName() string                 { return s.own }
func (s *ownSvcStub) BackendTargets() []netpol.BackendTarget { return s.targets }

// targetStub is extBackendStub-like without reporting an own Service name.
type targetStub struct {
	stubCollector
	targets []netpol.BackendTarget
}

func (s *targetStub) BackendTargets() []netpol.BackendTarget { return s.targets }

// routingTargetStub is a component that owns Service svc and fronts the pods selector selects, the
// way a `service` component does: a Service port in ports becomes its target port, any other is
// dropped.
type routingTargetStub struct {
	svc      string
	selector map[string]string
	ports    map[int32]int32 // Service port -> target port
}

func (*routingTargetStub) Generate(_ *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *routingTargetStub) BackendServiceName() string                            { return s.svc }

func (s *routingTargetStub) ServiceRoutingTarget(servicePorts []intstr.IntOrString) (*metav1.LabelSelector, []intstr.IntOrString) {
	var out []intstr.IntOrString
	for _, p := range servicePorts {
		if target, ok := s.ports[p.IntVal]; ok {
			out = append(out, intstr.FromInt32(target))
		}
	}
	return &metav1.LabelSelector{MatchLabels: s.selector}, out
}

var gatewaySources = []netpol.TrafficSource{{Namespace: "gateway-system"}}

func portsOf(p int32) []intstr.IntOrString { return []intstr.IntOrString{intstr.FromInt32(p)} }

// ingressPorts returns the TCP port numbers of a policy's ingress rules, in order.
func ingressPorts(np *networkingv1.NetworkPolicy) []int32 {
	var out []int32
	for _, r := range np.Spec.Ingress {
		for _, p := range r.Ports {
			out = append(out, p.Port.IntVal)
		}
	}
	return out
}

func equalPorts(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ownerSplitCluster places a routing component "router" (carrying collector in its "router-ingress"
// sub-application) and a Service-owning component "owner" in two leaf bundles, in either order.
func ownerSplitCluster(collector stack.ApplicationConfig, routerConfig, ownerConfig stack.ApplicationConfig, routerFirst bool) (*stack.Cluster, *stack.Bundle, *stack.Bundle, map[string]componentEntry) {
	routerApp := stack.NewApplication("router", "default", routerConfig)
	sub := stack.NewApplication("router-ingress", "default", collector)
	ownerApp := stack.NewApplication("owner", "default", ownerConfig)
	routerBundle := &stack.Bundle{Applications: []*stack.Application{routerApp, sub}}
	ownerBundle := &stack.Bundle{Applications: []*stack.Application{ownerApp}}
	children := []*stack.Node{{Bundle: routerBundle}, {Bundle: ownerBundle}}
	if !routerFirst {
		children = []*stack.Node{{Bundle: ownerBundle}, {Bundle: routerBundle}}
	}
	cluster := &stack.Cluster{Node: &stack.Node{Children: children}}
	return cluster, routerBundle, ownerBundle, map[string]componentEntry{"router": {app: routerApp}, "owner": {app: ownerApp}}
}

// The router's own-Service ports land on the owner of that Service, in the owner's bundle, whichever
// bundle the walk meets first. The router gets no allow on its own pods for them.
func TestSynthesizeNetworkPolicies_OwnPortsGoToTheServiceOwner(t *testing.T) {
	for _, routerFirst := range []bool{true, false} {
		name := "router bundle first"
		if !routerFirst {
			name = "owner bundle first"
		}
		t.Run(name, func(t *testing.T) {
			col := &ownSvcStub{
				stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)},
				own:           "api",
			}
			cluster, routerBundle, ownerBundle, componentMap := ownerSplitCluster(col, noopConfig{}, svcNamerConfig{svc: "api"}, routerFirst)
			if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
				t.Fatalf("synthesizeNetworkPolicies: %v", err)
			}
			if bundleHasApp(routerBundle, "router-allow-ingress-traffic") {
				t.Errorf("the router owns no Service the route names; got %v", bundleAppNames(routerBundle))
			}
			if !bundleHasApp(ownerBundle, "owner-allow-ingress-traffic") {
				t.Fatalf("expected owner-allow-ingress-traffic in the owner's bundle; got %v", bundleAppNames(ownerBundle))
			}
			np := synthesizedNPInBundle(t, ownerBundle, "owner-allow-ingress-traffic")
			if got := ingressPorts(np); !equalPorts(got, []int32{80}) {
				t.Errorf("ports = %v, want [80]", got)
			}
			if np.Spec.PodSelector.MatchLabels[ComponentLabel] != "owner" {
				t.Errorf("podSelector = %v, want %s=owner", np.Spec.PodSelector.MatchLabels, ComponentLabel)
			}
		})
	}
}

// A routed port the owner's Service does not have is dropped by the owner's routing target, so the
// route allows nothing: not on the router's pods either.
func TestSynthesizeNetworkPolicies_OwnPortsOfAnUnmatchedOwnerAllowNothing(t *testing.T) {
	col := &ownSvcStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)},
		own:           "api",
	}
	owner := &routingTargetStub{svc: "api", ports: map[int32]int32{8081: 8081}, selector: map[string]string{"app": "api-server"}}
	cluster, _, _, componentMap := ownerSplitCluster(col, noopConfig{}, owner, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if n := countClusterApps(cluster, "router-allow-ingress-traffic") + countClusterApps(cluster, "owner-allow-ingress-traffic"); n != 0 {
		t.Errorf("expected no synthesized allow, found %d", n)
	}
}

// The owner's routing target translates the Service port into the pods' target port, on the owner's
// selector.
func TestSynthesizeNetworkPolicies_OwnPortsThroughTheOwnersRoutingTarget(t *testing.T) {
	col := &ownSvcStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)},
		own:           "api",
	}
	owner := &routingTargetStub{svc: "api", ports: map[int32]int32{80: 8080}, selector: map[string]string{"app": "api-server"}}
	cluster, _, ownerBundle, componentMap := ownerSplitCluster(col, noopConfig{}, owner, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if countClusterApps(cluster, "router-allow-ingress-traffic") != 0 {
		t.Errorf("the router must get no allow for a Service it does not own")
	}
	np := synthesizedNPInBundle(t, ownerBundle, "owner-allow-ingress-traffic")
	if got := np.Spec.PodSelector.MatchLabels["app"]; got != "api-server" {
		t.Errorf("podSelector = %v, want app=api-server", np.Spec.PodSelector.MatchLabels)
	}
	if got := ingressPorts(np); !equalPorts(got, []int32{8080}) {
		t.Errorf("ports = %v, want [8080]", got)
	}
}

// A target Service the routing component owns is injected onto the router's own entry.
func TestSynthesizeNetworkPolicies_TargetOwnedByTheRouterIsInjected(t *testing.T) {
	col := &ownSvcStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources},
		own:           "router",
		targets:       []netpol.BackendTarget{{ServiceName: "metrics-http", Ports: portsOf(9090)}},
	}
	cluster, routerBundle, _, componentMap := ownerSplitCluster(col, svcNamerConfig{svc: "metrics-http"}, noopConfig{}, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if !bundleHasApp(routerBundle, "router-allow-ingress-traffic") {
		t.Fatalf("expected router-allow-ingress-traffic; got %v", bundleAppNames(routerBundle))
	}
	if got := ingressPorts(synthesizedNPInBundle(t, routerBundle, "router-allow-ingress-traffic")); !equalPorts(got, []int32{9090}) {
		t.Errorf("ports = %v, want [9090]", got)
	}
}

// The same holds for a collector that reports no own-Service name: its target owned by the router is
// no longer skipped.
func TestSynthesizeNetworkPolicies_TargetOwnedByTheRouterIsInjectedWithoutAReportedName(t *testing.T) {
	col := &targetStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources},
		targets:       []netpol.BackendTarget{{ServiceName: "metrics-http", Ports: portsOf(9090)}},
	}
	cluster, routerBundle, _, componentMap := ownerSplitCluster(col, svcNamerConfig{svc: "metrics-http"}, noopConfig{}, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if got := ingressPorts(synthesizedNPInBundle(t, routerBundle, "router-allow-ingress-traffic")); !equalPorts(got, []int32{9090}) {
		t.Errorf("ports = %v, want [9090]", got)
	}
}

// A target that is the very Service the collector reports as its own is covered by its own ports and
// is skipped: its target ports never widen the allow.
func TestSynthesizeNetworkPolicies_TargetEqualToTheReportedOwnServiceIsSkipped(t *testing.T) {
	col := &ownSvcStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)},
		own:           "api",
		targets:       []netpol.BackendTarget{{ServiceName: "api", Ports: portsOf(8080)}},
	}
	cluster, routerBundle, _, componentMap := ownerSplitCluster(col, svcNamerConfig{svc: "api"}, noopConfig{}, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if got := ingressPorts(synthesizedNPInBundle(t, routerBundle, "router-allow-ingress-traffic")); !equalPorts(got, []int32{80}) {
		t.Errorf("ports = %v, want only the own port [80]", got)
	}
}

// Controls: a route to the router's own Service owned by the router, and a collector that reports
// no name, both keep the allow on the router.
func TestSynthesizeNetworkPolicies_OwnPortsStayWithTheRouter(t *testing.T) {
	tests := []struct {
		name string
		col  stack.ApplicationConfig
	}{
		{"reported own Service owned by the router", &ownSvcStub{
			stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)}, own: "router",
		}},
		{"reported own Service owned by no component", &ownSvcStub{
			stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)}, own: "bare",
		}},
		{"no reported name", &stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster, routerBundle, _, componentMap := ownerSplitCluster(tt.col, svcNamerConfig{svc: "router"}, svcNamerConfig{svc: "other"}, true)
			if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
				t.Fatalf("synthesizeNetworkPolicies: %v", err)
			}
			if got := ingressPorts(synthesizedNPInBundle(t, routerBundle, "router-allow-ingress-traffic")); !equalPorts(got, []int32{80}) {
				t.Errorf("ports = %v, want [80]", got)
			}
			if countClusterApps(cluster, "owner-allow-ingress-traffic") != 0 {
				t.Errorf("the other component must get no allow")
			}
		})
	}
}

// With no traffic sources nothing is generated in either place.
func TestSynthesizeNetworkPolicies_OwnPortsWithoutSourcesGenerateNothing(t *testing.T) {
	col := &ownSvcStub{stubCollector: stubCollector{component: "router", ports: portsOf(80)}, own: "api"}
	cluster, _, _, componentMap := ownerSplitCluster(col, noopConfig{}, svcNamerConfig{svc: "api"}, true)
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	if n := countClusterApps(cluster, "router-allow-ingress-traffic") + countClusterApps(cluster, "owner-allow-ingress-traffic"); n != 0 {
		t.Errorf("expected no synthesized allow, found %d", n)
	}
}

// An owner that also routes to itself on the same port with the same sources gets one rule, not two.
func TestSynthesizeNetworkPolicies_OwnerRoutingToItselfIsNotDuplicated(t *testing.T) {
	own := &ownSvcStub{
		stubCollector: stubCollector{component: "owner", sources: gatewaySources, ports: portsOf(80)},
		own:           "api",
	}
	routed := &ownSvcStub{
		stubCollector: stubCollector{component: "router", sources: gatewaySources, ports: portsOf(80)},
		own:           "api",
	}
	ownerApp := stack.NewApplication("owner", "default", svcNamerConfig{svc: "api"})
	routerApp := stack.NewApplication("router", "default", noopConfig{})
	bundle := &stack.Bundle{Applications: []*stack.Application{
		ownerApp, routerApp,
		stack.NewApplication("owner-ingress", "default", own),
		stack.NewApplication("router-ingress", "default", routed),
	}}
	cluster := &stack.Cluster{Node: &stack.Node{Bundle: bundle}}
	componentMap := map[string]componentEntry{"owner": {app: ownerApp}, "router": {app: routerApp}}
	if err := synthesizeNetworkPolicies(cluster, componentMap, ComponentLabel); err != nil {
		t.Fatalf("synthesizeNetworkPolicies: %v", err)
	}
	np := synthesizedNPInBundle(t, bundle, "owner-allow-ingress-traffic")
	if len(np.Spec.Ingress) != 1 {
		t.Errorf("expected one deduplicated ingress rule, got %+v", np.Spec.Ingress)
	}
}
