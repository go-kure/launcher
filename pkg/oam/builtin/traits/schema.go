package traits

import "github.com/go-kure/launcher/pkg/oam"

// This file holds PropertySchema fragments shared by the routing traits
// (ingress/httproute/expose). `networkPolicy` is a platform-reserved key
// populated by capability rendering (see parseTrafficSources in
// networkpolicy_auto.go); it is modeled here so direct use validates too.

// schemaNetworkPolicy describes the platform-reserved `networkPolicy` property.
func schemaNetworkPolicy() oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeObject,
		Description: "Platform-reserved network policy configuration derived from cluster capabilities.",
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
