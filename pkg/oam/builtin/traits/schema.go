package traits

import "github.com/go-kure/launcher/pkg/oam"

// This file holds PropertySchema fragments shared by the routing traits
// (ingress/httproute/expose). `networkPolicy` is a platform-reserved key
// populated by capability rendering (see parseTrafficSources in
// networkpolicy_auto.go); it is modeled here so direct use validates too.

// schemaNetworkPolicy describes the `networkPolicy` property. reserved is required
// (not defaulted) at every call site (D3): every current caller passes true — the
// property is platform-reserved everywhere it is declared — but an explicit argument
// means a future caller that wants otherwise has to say so, rather than one call site
// silently drifting from the others the way an implicit shared default would allow.
func schemaNetworkPolicy(reserved bool) oam.PropertySchema {
	return oam.PropertySchema{
		Type:             oam.PropertyTypeObject,
		PlatformReserved: reserved,
		Description:      "Platform-reserved network policy configuration derived from cluster capabilities.",
		Properties: map[string]oam.PropertySchema{
			"trafficSources": {
				Type:        oam.PropertyTypeArray,
				Description: "Sources allowed to reach this workload.",
				Items: &oam.PropertySchema{
					Type:        oam.PropertyTypeObject,
					Description: "A single allowed traffic source.",
					Properties: map[string]oam.PropertySchema{
						"namespace":   {Type: oam.PropertyTypeString, Required: true, Description: "Namespace the traffic originates from."},
						"podSelector": {Type: oam.PropertyTypeObject, AdditionalProperties: true, Description: "Label selector narrowing which pods in the namespace are allowed."},
					},
				},
			},
		},
	}
}
