// Package netpol contains shared types for automatic NetworkPolicy synthesis.
// It lives outside the oam package so that both pkg/oam (the synthesis stage)
// and pkg/oam/builtin/traits (the routing configs that expose the collected
// sources) can import it without creating an import cycle.
package netpol

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TrafficSource represents one allowed ingress traffic source for auto-generated
// NetworkPolicy rules. Namespace is required; PodSelector narrows to specific
// pods within that namespace (matchLabels only; nil = all pods).
type TrafficSource struct {
	Namespace   string
	PodSelector *metav1.LabelSelector // nil = all pods; matchLabels only
}
