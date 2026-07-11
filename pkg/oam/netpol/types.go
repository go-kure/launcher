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
// per-component egress NetworkPolicy. It is a crane-supplied, non-authorable
// synthesis input surfaced from crane's dependency graph — OAM authors cannot
// set it. Namespace is required; PodSelector narrows to specific pods within
// that namespace (matchLabels only; nil = all pods); Ports are the destination
// ports (TCP). A peer with no Ports is skipped by synthesis.
type EgressPeer struct {
	Namespace   string
	PodSelector *metav1.LabelSelector // nil = all pods; matchLabels only
	Ports       []intstr.IntOrString
}
