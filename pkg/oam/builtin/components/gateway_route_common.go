package components

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of the Gateway API's
// gateway.networking.k8s.io/v1 routes that carry no HTTP share
// (go-kure/launcher#790): tcproute, udproute and tlsroute. Each is a policyFreeKind, as the kinds of
// the API's infrastructure objects are (gateway_common.go): a route runs no
// pod, holds no image, requests no storage and has no replica count, and no
// field of one holds a literal secret.
//
// A route is an authored object. A parent (`parentRefs`) and a backend
// (`backendRefs`) are references as written, and either may name another
// namespace: launcher points neither at a component, does not look for the
// target in the document, and reports no backend to the NetworkPolicy
// synthesis, which allows no traffic for it.
//
// The API requires of a route what the type leaves out where it is not
// authored: its rules, the backends of a rule, and of a TLSRoute its host
// names. Each kind's validate refuses those, as the API server refuses the object. It also holds the one rule the
// CRDs write as an expression that asks for a field: the port of a backend
// that is a Service (gatewayRouteBackendRefs).
// TestGatewayRouteKinds_ExpressionRules holds that refusal to the API
// server's own answer, and names every other expression rule of the CRDs with
// the reason it is left to the API server.

// gatewayRouteParentRefs is the schema of a route's parentRefs.
func gatewayRouteParentRefs(spec string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeArray,
		Description: spec + "parentRefs: the Gateways (or other parents) the route attaches to, at most 32. No parent is defaulted.",
		Items: &oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "One parent: `name`, required, with optional `group`, `kind`, `namespace`, `sectionName` and `port`." + gatewayDecoded + "ParentReference in its API reference.",
		},
	}
}

// gatewayRouteUseDefaultGateways is the schema of a route's useDefaultGateways.
func gatewayRouteUseDefaultGateways(spec, kind string) oam.PropertySchema {
	return oam.PropertySchema{
		Type:        oam.PropertyTypeString,
		Description: spec + "useDefaultGateways: the scope of default Gateways the route also attaches to (All, None). An experimental-channel field: the standard channel's " + kind + " CRD does not hold it.",
	}
}

// gatewayRouteRequired is the required list the routes share: of a parent and
// of a backend that are authored, the name, which the API requires and the type
// would write empty.
func gatewayRouteRequired() map[string]string {
	return map[string]string{
		"parentRefs[].name":          "the name of the Gateway, or of the other parent, the route attaches to",
		"rules[].backendRefs[].name": "the name of the backend, a Service unless its group and kind say otherwise",
	}
}

// gatewayRouteBackendRefs refuses the backends of the rule at index rule that
// the API server refuses for a field left out: no backend at all, absent or
// empty, since the type omits both; and a Service without its port.
//
// The second is the CRDs' expression rule
// `(size(self.group) == 0 && self.kind == 'Service') ? has(self.port) : true`,
// which the API server evaluates after it has filled the defaults of group
// ("") and kind (Service): a backend that names neither is a Service too.
func gatewayRouteBackendRefs(rule int, refs []gatewayv1.BackendRef) error {
	if len(refs) == 0 {
		return errors.Errorf("rules[%d].backendRefs: required (at least one backend the rule sends traffic to)", rule)
	}
	for i, ref := range refs {
		core := ref.Group == nil || *ref.Group == ""
		service := ref.Kind == nil || *ref.Kind == "Service"
		if core && service && ref.Port == nil {
			return errors.Errorf("rules[%d].backendRefs[%d].port: required (the port of the Service: a backend is a Service unless its group and kind say otherwise)", rule, i)
		}
	}
	return nil
}
