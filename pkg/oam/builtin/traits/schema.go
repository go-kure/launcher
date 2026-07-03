package traits

import "github.com/go-kure/launcher/pkg/oam"

// This file holds PropertySchema fragments shared by the routing traits
// (ingress/httproute/expose). `networkPolicy` is a platform-reserved key
// populated by capability rendering (see parseTrafficSources in
// networkpolicy_auto.go); it is modeled here so direct use validates too.

// schemaNetworkPolicy describes the platform-reserved `networkPolicy` property.
func schemaNetworkPolicy() oam.PropertySchema {
	return oam.PropertySchema{
		Type: oam.PropertyTypeObject,
		Properties: map[string]oam.PropertySchema{
			"trafficSources": {
				Type: oam.PropertyTypeArray,
				Items: &oam.PropertySchema{
					Type: oam.PropertyTypeObject,
					Properties: map[string]oam.PropertySchema{
						"namespace":   {Type: oam.PropertyTypeString, Required: true},
						"podSelector": {Type: oam.PropertyTypeObject, AdditionalProperties: true},
					},
				},
			},
		},
	}
}
