package traits

import (
	"slices"

	"github.com/go-kure/launcher/pkg/oam"
)

// exposeConsumedKeys are the properties the expose rule takes away from the
// trait it emits under controllerType: the emitted trait does not receive the
// value it was handed under these names. LowerTrait deletes by this list and
// LoweringKeys subtracts it. "tls" is no expose property: the ingress path drops
// it, because the rule manages the TLS entry itself.
func exposeConsumedKeys(controllerType string) []string {
	keys := []string{"controllerType", "certManagerClusterIssuer", "allowedHostnameWildcard"}
	switch controllerType {
	case "ingress":
		return append(keys, "hostnames", "secretName", "tls", "sslRedirect", "forceSslRedirect",
			"allowedGroups", "authURL", "authSigninURL", "authResponseHeaders")
	case "gateway":
		return append(keys, "gatewayName", "gatewayNamespace")
	}
	return keys
}

// dropConsumed deletes from props the properties the rule consumes under
// controllerType.
func dropConsumed(props map[string]any, controllerType string) {
	for _, key := range exposeConsumedKeys(controllerType) {
		delete(props, key)
	}
}

// exposeBuiltPlaces are, per controllerType, the paths of the emitted trait the
// rule builds from a key itself. They are declared beside LowerTrait and held to
// what it emits by the key map tests of the registry package.
var exposeBuiltPlaces = map[string]map[string][]string{
	"ingress": {
		"certManagerClusterIssuer": {platformAnnotationsProperty, managedTLSProperty},
		"secretName":               {managedTLSProperty + ".secretName"},
		"hostnames":                {"rules", managedTLSProperty + ".hosts"},
		"rules":                    {managedTLSProperty + ".hosts"},
		"sslRedirect":              {platformAnnotationsProperty},
		"forceSslRedirect":         {platformAnnotationsProperty},
		"allowedGroups":            {platformAnnotationsProperty},
		"authURL":                  {platformAnnotationsProperty},
		"authSigninURL":            {platformAnnotationsProperty},
		"authResponseHeaders":      {platformAnnotationsProperty},
	},
	"gateway": {
		"hostnames":        {"rules"},
		"gatewayName":      {"parentRefs"},
		"gatewayNamespace": {"parentRefs"},
	},
}

// LoweringKeys implements oam.LoweringKeyDeclarer: where the value of each
// expose property lands, on the ingress trait (controllerType ingress) or the
// httproute trait (controllerType gateway) the rule emits. A key is passed
// through where the emitted trait's handler declares it too and the rule does
// not consume it; the paths the rule builds are exposeBuiltPlaces.
func (r ExposeRule) LoweringKeys() map[string][]oam.LoweredKey {
	schema := r.PropertySchema()
	keys := make(map[string][]oam.LoweredKey, len(schema))
	routes := []struct {
		controllerType, trait string
		handler               oam.PropertySchemaProvider
	}{
		{"ingress", "ingress", &IngressHandler{}},
		{"gateway", "httproute", &HTTPRouteHandler{}},
	}
	for key := range schema {
		keys[key] = nil
		for _, route := range routes {
			place := func(path string, unchanged bool) oam.LoweredKey {
				return oam.LoweredKey{
					Trait: route.trait, Path: path, Unchanged: unchanged,
					When: []oam.LoweredWhen{{Key: "controllerType", Values: []string{route.controllerType}}},
				}
			}
			_, declared := route.handler.PropertySchema()[key]
			if declared && !slices.Contains(exposeConsumedKeys(route.controllerType), key) {
				keys[key] = append(keys[key], place(key, true))
			}
			for _, path := range exposeBuiltPlaces[route.controllerType][key] {
				keys[key] = append(keys[key], place(path, false))
			}
		}
	}
	return keys
}
