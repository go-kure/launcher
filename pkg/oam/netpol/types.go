// Package netpol contains shared types for automatic NetworkPolicy synthesis.
// It lives outside the oam package so that both pkg/oam (the synthesis stage)
// and pkg/oam/builtin/traits (the routing configs that expose the collected
// sources) can import it without creating an import cycle.
package netpol

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TrafficSource represents one allowed ingress traffic source for auto-generated
// NetworkPolicy rules. Namespace is required; PodSelector narrows to specific
// pods within that namespace (matchLabels only; nil = all pods).
type TrafficSource struct {
	Namespace   string
	PodSelector *metav1.LabelSelector // nil = all pods; matchLabels only
}

// EgressPeer represents one allowed egress destination for an auto-generated
// per-component egress NetworkPolicy. It is a downstream-supplied, non-authorable
// synthesis input surfaced from the downstream runtime's dependency graph — OAM authors cannot
// set it. Namespace is required; PodSelector narrows to specific pods within
// that namespace (matchLabels only); Ports are the destination ports (TCP).
//
// Fail-fast (aligned with the endpoint-ingress family): a peer that carries Ports but a nil,
// empty-matchLabels, or expression-bearing PodSelector is a producer bug and is rejected with a
// build error — it would otherwise emit a namespace-wide egress allow (to.PodSelector nil/empty =
// all pods). A peer with no Ports is the documented escape hatch: it is silently skipped by
// synthesis (the destination stays authored), so a ported-but-selector-less peer never slips
// through as "all pods".
type EgressPeer struct {
	Namespace   string
	PodSelector *metav1.LabelSelector // required matchLabels when Ports set (else the peer is skipped)
	Ports       []intstr.IntOrString
}

// BackendTarget is one expose/routing backendRef that routes to a **separate** in-cluster
// backend Service — not the exposing component's own. Ingress synthesis uses it to retarget the
// synthesized allow onto the pods that actually receive the traffic (resolved from the backend
// Service name to a sibling component). ServiceName is the referenced Kubernetes Service; Ports
// are the referenced backend ports.
type BackendTarget struct {
	ServiceName string
	Ports       []intstr.IntOrString
}

// Endpoint is a component's declared in-cluster data-plane endpoint (e.g. an operator-managed
// database's instance pods). Namespace is deliberately absent — the platform caller knows the
// target's namespace, and the synthesized NetworkPolicy is emitted in the component's namespace.
// PodSelector is required (matchLabels only); Ports is required and non-empty (TCP).
type Endpoint struct {
	PodSelector *metav1.LabelSelector // required; matchLabels only
	Ports       []intstr.IntOrString  // required, non-empty; TCP
}

// IngressPeer is a target-side allow: who may reach one of a component's endpoints. It is a
// platform-supplied, non-authorable synthesis input (never from OAM YAML or capability
// rendering); launcher only emits. All selectors are platform-supplied.
//
// Unlike TrafficSource (where a nil PodSelector means "all pods"), endpoint-ingress synthesis
// is fail-closed: each Source MUST carry a namespace and a non-empty matchLabels pod selector
// (matchLabels only) — a namespace-wide source is dropped, since it would let every pod in that
// namespace reach the endpoint on the endpoint ports.
type IngressPeer struct {
	Endpoint Endpoint        // NetworkPolicy podSelector + ports
	Sources  []TrafficSource // required matchLabels pod selector + namespace per source
}
