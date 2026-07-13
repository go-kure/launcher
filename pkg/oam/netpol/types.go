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
// that namespace (matchLabels only; nil = all pods); Ports are the destination
// ports (TCP). A peer with no Ports is skipped by synthesis.
type EgressPeer struct {
	Namespace   string
	PodSelector *metav1.LabelSelector // nil = all pods; matchLabels only
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
