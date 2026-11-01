package oam

import (
	"slices"
	"sort"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// RoutedPodsKind says where the pods a route lands on are found.
type RoutedPodsKind string

const (
	// RoutedPodsSelector: the Service's owner is a routing targeter (a `service`
	// component, or a group or wrapper that forwards to one), and
	// RoutedPods.Selector is the selector its Service carries.
	RoutedPodsSelector RoutedPodsKind = "selector"
	// RoutedPodsOwner: a component owns the Service but is not a routing
	// targeter. The route lands on that component's own pods, on the port as
	// the route names it.
	RoutedPodsOwner RoutedPodsKind = "owner"
	// RoutedPodsNone: the Service selects no pods (an ExternalName Service).
	RoutedPodsNone RoutedPodsKind = "none"
	// RoutedPodsUnowned: no component of the document owns the Service. The
	// route names a bare Service; RoutedPods.Selector is the backend selector the
	// route authored for it, if any.
	RoutedPodsUnowned RoutedPodsKind = "unowned"
)

// RoutedPortStatus says whether a routed port was found on the Service.
type RoutedPortStatus string

const (
	// RoutedPortMatched: a TCP port of the Service matches the routed port, by
	// number or by name. RoutedPort.TargetPorts are the ports its pods listen on.
	RoutedPortMatched RoutedPortStatus = "matched"
	// RoutedPortUnmatched: the Service's owner was asked and no TCP port of the
	// Service matches. A UDP or SCTP port reads as unmatched, as in the
	// NetworkPolicy synthesis, which opens TCP only; so does any port of an
	// ExternalName Service.
	RoutedPortUnmatched RoutedPortStatus = "unmatched"
	// RoutedPortUnchecked: there is no list of Service ports to ask (the owner is
	// not a routing targeter, or no component owns the Service). It is neither a
	// match nor a mismatch.
	RoutedPortUnchecked RoutedPortStatus = "unchecked"
)

// RoutedBackend is one backend a routing trait routes to: one row per routing
// trait sub-application and Service, except that a Service no component owns
// has one row per backend selector the route authored for it.
type RoutedBackend struct {
	// Component carries the routing trait.
	Component string `json:"component"`
	// Application is the name of the trait's sub-application. With Component it
	// does not make a unique key: equally named sub-applications can sit in
	// different bundles of one component.
	Application string `json:"application"`
	// Service is the Service the route names. For a route to the component's own
	// Service it is the Service name the routing trait resolves for the
	// application it ran on: that application's BackendServiceName, else its name.
	Service string `json:"service"`
	// Owner is the component the route lands on: the routing component itself
	// for a route to its own Service, else the component that owns Service.
	// Empty when no component of the document owns it.
	Owner string       `json:"owner,omitempty"`
	Pods  RoutedPods   `json:"pods"`
	Ports []RoutedPort `json:"ports"`
}

// RoutedPods is the pods a route lands on.
type RoutedPods struct {
	Kind RoutedPodsKind `json:"kind"`
	// Selector is set for RoutedPodsSelector and, when the route authored one,
	// for RoutedPodsUnowned. It is a copy.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// RoutedPort is one port a route names on a Service.
type RoutedPort struct {
	// Port is the Service port as the route names it: a number or a Service port
	// name. It is in the Service's port space, never in a container's.
	Port   intstr.IntOrString `json:"port"`
	Status RoutedPortStatus   `json:"status"`
	// TargetPorts is set for RoutedPortMatched: the target ports of the matching
	// Service ports, a number or a container port name each.
	TargetPorts []intstr.IntOrString `json:"targetPorts,omitempty"`
}

// RoutedBackends reports where each route of each routing trait of app lands:
// the Service it names, the component that owns that Service, the pods the
// Service selects and, for each routed port, whether the Service has it. It is
// read-only, and a downstream consumer calls it to check its own view of the
// document against what the routing traits resolve.
//
// It answers with the resolution the NetworkPolicy synthesis makes, not a copy
// of it: the Service owners come from the synthesis's own lookups, and a routed
// port is matched by asking the owner's ServiceRoutingTarget once per port. A
// route is reported whether or not any traffic source is configured. A route
// whose port the Service does not have is reported as unmatched, not dropped.
// A route to a component that is not a routing targeter, or to a Service no
// component owns, has no Service port list to ask: its ports are unchecked.
//
// The transform runs through lowering, policy handling, trait application and
// the checks that precede the synthesis, and stops there. An error any of those
// raises is returned. What runs after is not seen: the NetworkPolicy, egress and
// endpoint-ingress synthesis, naming, decoration, the cluster-wide application
// name checks and component ownership. The result is therefore not the build's
// verdict on the document. A routing trait sub-application a trait put straight
// into a child bundle has no recorded owning application, so the Service name of
// a route to its own Service is resolved from the component's application,
// which for a sibling group is the group.
//
// The rows are ordered by Component, Application and Service. Equally ordered
// rows keep the order of the walk of the cluster's bundles.
func (t *Transformer) RoutedBackends(app *Application, ctx TransformContext) ([]RoutedBackend, error) {
	out := []RoutedBackend{}
	hook := &transformHook{
		stop: true,
		observe: func(cluster *stack.Cluster, components map[string]componentEntry, ctx TransformContext) error {
			var err error
			out, err = collectRoutedBackends(cluster, components, ctx)
			return err
		},
	}
	if _, _, err := t.transform(app, ctx, hook); err != nil {
		return nil, err
	}
	return out, nil
}

// collectRoutedBackends reads the routing trait sub-applications of a built
// cluster, before any decorator wraps them, and resolves each route against the
// owners the NetworkPolicy synthesis would find. It changes nothing.
func collectRoutedBackends(cluster *stack.Cluster, components map[string]componentEntry, ctx TransformContext) ([]RoutedBackend, error) {
	reg := newNPSynthesisRegistry()
	if err := reg.buildLookups(cluster, components); err != nil {
		return nil, err
	}
	// The trait additions are an ownership index only: a later trait may remove
	// a sub-application, so the inventory is the cluster, not this log.
	ranOn := map[*stack.Application]*stack.Application{}
	if ctx.traitSubApps != nil {
		for _, rec := range *ctx.traitSubApps {
			for _, sub := range rec.subApps {
				ranOn[sub] = rec.owner
			}
		}
	}

	out := []RoutedBackend{}
	walkBundles(cluster.Node, func(bundle *stack.Bundle) {
		for _, sub := range bundle.Applications {
			col, ok := sub.Config.(trafficSourceCollector)
			if !ok {
				continue
			}
			out = append(out, routedBackendsOf(reg, components, ranOn, sub, col)...)
		}
	})
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Application != b.Application {
			return a.Application < b.Application
		}
		return a.Service < b.Service
	})
	return out, nil
}

// routedBackendsOf is the rows of one routing trait sub-application: one for
// the component's own Service when it routes any port to it, one for each
// other Service it names.
func routedBackendsOf(reg *npSynthesisRegistry, components map[string]componentEntry, ranOn map[*stack.Application]*stack.Application, sub *stack.Application, col trafficSourceCollector) []RoutedBackend {
	router := col.TargetComponentName()
	var rows []RoutedBackend

	// An external-only route has no ports of its own: it gets no row for the
	// component's Service (the synthesis skips it the same way).
	if ports := col.BackendPorts(); len(ports) > 0 {
		own := ranOn[sub]
		if own == nil {
			own = components[router].app
		}
		rows = append(rows, resolveRoutedBackend(reg, router, sub.Name, routingServiceName(own, router), router, ports, nil))
	}
	bt, ok := sub.Config.(backendRefTargetCollector)
	if !ok {
		return rows
	}
	// BackendTargets groups by Service and authored selector. An owned Service
	// ignores the authored selector (its owner decides the pods), so the groups of
	// one owned Service are one row; an unowned Service keeps one row per
	// selector, because the pods differ.
	type routedTarget struct {
		service, owner string
		ports          []intstr.IntOrString
		selector       *metav1.LabelSelector
	}
	var targets []routedTarget
	ownedAt := map[string]int{}
	for _, target := range bt.BackendTargets() {
		// BackendTargets never holds the Service the route resolved for the
		// component itself (the row above), so a target owned by the router is
		// another Service of the same component, such as a sibling group's, and is
		// a route like any other.
		owner, owned := reg.serviceToComponent[target.ServiceName]
		if !owned {
			targets = append(targets, routedTarget{service: target.ServiceName, ports: target.Ports, selector: target.PodSelector})
			continue
		}
		i, seen := ownedAt[target.ServiceName]
		if !seen {
			i = len(targets)
			ownedAt[target.ServiceName] = i
			targets = append(targets, routedTarget{service: target.ServiceName, owner: owner})
		}
		for _, p := range target.Ports {
			if !slices.Contains(targets[i].ports, p) {
				targets[i].ports = append(targets[i].ports, p)
			}
		}
	}
	for _, t := range targets {
		rows = append(rows, resolveRoutedBackend(reg, router, sub.Name, t.service, t.owner, t.ports, t.selector))
	}
	return rows
}

// routingServiceName is the Service name a routing trait resolves for the
// component's own Service: the BackendServiceName of the application it ran on,
// else that application's name (the traits' own resolveServiceName). It is not
// componentServiceName, which is "" for a component that declares no Service.
func routingServiceName(own *stack.Application, router string) string {
	if own == nil {
		return router
	}
	if n, ok := own.Config.(serviceBackendNamer); ok && n.BackendServiceName() != "" {
		return n.BackendServiceName()
	}
	return own.Name
}

// resolveRoutedBackend resolves the routed ports of one Service against its
// owner. authored is the backend selector the route carried for a Service no
// component owns.
func resolveRoutedBackend(reg *npSynthesisRegistry, router, application, service, owner string, ports []intstr.IntOrString, authored *metav1.LabelSelector) RoutedBackend {
	row := RoutedBackend{Component: router, Application: application, Service: service, Owner: owner, Ports: make([]RoutedPort, 0, len(ports))}
	rt, targeter := reg.routingTargets[owner]
	switch {
	case owner == "":
		row.Pods = RoutedPods{Kind: RoutedPodsUnowned, Selector: authored.DeepCopy()}
	case !targeter:
		row.Pods = RoutedPods{Kind: RoutedPodsOwner}
	default:
		// The selector does not depend on the ports: buildLookups registered the
		// owner by asking it for ports-less.
		sel, _ := rt.ServiceRoutingTarget(nil)
		if isNoPodsSelector(sel) {
			row.Pods = RoutedPods{Kind: RoutedPodsNone}
		} else {
			row.Pods = RoutedPods{Kind: RoutedPodsSelector, Selector: sel.DeepCopy()}
		}
	}
	for _, p := range ports {
		rp := RoutedPort{Port: p, Status: RoutedPortUnchecked}
		if targeter {
			// One port at a time: the owner's own matching decides each port, and
			// an empty answer is a port the Service does not have.
			if _, target := rt.ServiceRoutingTarget([]intstr.IntOrString{p}); len(target) > 0 {
				rp.Status, rp.TargetPorts = RoutedPortMatched, target
			} else {
				rp.Status = RoutedPortUnmatched
			}
		}
		row.Ports = append(row.Ports, rp)
	}
	return row
}

// isNoPodsSelector reports whether a routing target's selector is the marker of
// a Service that selects no pods (an ExternalName Service): non-nil, with no
// labels and no expressions (serviceRoutingTargeter).
func isNoPodsSelector(sel *metav1.LabelSelector) bool {
	return sel != nil && len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0
}
