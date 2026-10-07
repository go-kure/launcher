package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// TCPRouteHandler handles OAM tcproute components: the kind-named projection
// of a gateway.networking.k8s.io/v1 TCPRoute (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of gatewayv1.TCPRouteSpec,
// those of its inlined CommonRouteSpec included, under their json names,
// decoded strictly (decodeKindSpec). It emits the TCPRoute, named after the
// component unless `objectName` names it, in the build namespace, and nothing
// else: no Gateway it attaches to and no Service it sends connections to.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type TCPRouteHandler struct{}

// CanHandle returns true for the tcproute component type.
func (h *TCPRouteHandler) CanHandle(componentType string) bool {
	return componentType == "tcproute"
}

// PropertySchema declares every top-level gatewayv1.TCPRouteSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *TCPRouteHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "TCPRoute spec."
	return map[string]oam.PropertySchema{
		"parentRefs":         gatewayRouteParentRefs(spec),
		"useDefaultGateways": gatewayRouteUseDefaultGateways(spec, "TCPRoute"),
		"rules": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "rules: the rule of the route; the API accepts exactly one.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: `backendRefs`, required, at least one and at most 16, each with `name`, required, `port`, required of a Service, and optional `group`, `kind`, `namespace` and `weight`; and an optional `name`." + gatewayDecoded + "TCPRouteRule in its API reference.",
			},
		},
	}
}

// tcpRouteKind is the tcproute kind: see policyFreeKind and
// gateway_route_common.go. The API requires `rules`, and of a rule its
// `backendRefs`, which the type omits when empty: validate refuses both, and a
// Service backend without its port, as the API server refuses a TCPRoute
// without them. Of a parent and of a backend that are authored it requires the
// name, which the list holds. The API's other value rules, the single rule a
// TCPRoute may hold among them, are left to the API server.
// TestGatewayKinds_RequiredMatchCRD holds the list to the CRD.
var tcpRouteKind = &policyFreeKind[gatewayv1.TCPRouteSpec]{
	upstream: "gateway.networking.k8s.io/v1 TCPRouteSpec",
	required: gatewayRouteRequired(),
	validate: func(spec *gatewayv1.TCPRouteSpec) error {
		if len(spec.Rules) == 0 {
			return errors.New("rules: required (the rule of the route, which names the backends connections are sent to)")
		}
		for i, rule := range spec.Rules {
			if err := gatewayRouteBackendRefs(i, rule.BackendRefs); err != nil {
				return err
			}
		}
		return nil
	},
	build: func(name, namespace string, spec *gatewayv1.TCPRouteSpec) client.Object {
		route := kubernetes.CreateTCPRoute(name, namespace)
		spec.DeepCopyInto(&route.Spec)
		return route
	},
}

// ToApplicationConfig decodes an OAM tcproute component into its config. The
// object takes the namespace of the application it is generated in.
func (h *TCPRouteHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return tcpRouteKind.config(component)
}
