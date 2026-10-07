package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// GRPCRouteHandler handles OAM grpcroute components: the kind-named projection
// of a gateway.networking.k8s.io/v1 GRPCRoute (go-kure/launcher#790).
//
// It is built as the httproute kind is, not as the routes that carry no HTTP
// (gateway_route_common.go): a GRPCRoute, like an HTTPRoute, requires no rule,
// no backend and no host name, so the kind requires and fills nothing. Its
// properties are exactly the top-level fields of gatewayv1.GRPCRouteSpec, those
// of its inlined CommonRouteSpec included, under their json names, decoded
// strictly (decodeKindSpec). It emits the GRPCRoute, named after the component
// unless `objectName` names it, in the build namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is an authored object: a parent and a backend are references as written,
// either may name another namespace, and the NetworkPolicy synthesis does not
// read them and allows no traffic for them.
type GRPCRouteHandler struct{}

// CanHandle returns true for the grpcroute component type.
func (h *GRPCRouteHandler) CanHandle(componentType string) bool {
	return componentType == "grpcroute"
}

// PropertySchema declares every top-level gatewayv1.GRPCRouteSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *GRPCRouteHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"parentRefs": {
			Type:        oam.PropertyTypeArray,
			Description: "GRPCRoute spec.parentRefs: the Gateways (or other parents) the route attaches to. No parent is defaulted.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One parent: `name`, with optional `group`, `kind`, `namespace`, `sectionName` and `port`. Decoded strictly into ParentReference.",
			},
		},
		"useDefaultGateways": {
			Type:        oam.PropertyTypeString,
			Description: "GRPCRoute spec.useDefaultGateways: the scope of default Gateways the route also attaches to (All, None). An experimental-channel field: the standard channel's GRPCRoute CRD does not hold it.",
		},
		"hostnames": {
			Type:        oam.PropertyTypeArray,
			Description: "GRPCRoute spec.hostnames: the Host headers (the HTTP/2 :authority) the route matches, each a hostname or a `*.` wildcard. Unset matches every host the parent's listener accepts.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname, optionally prefixed with a `*.` wildcard label."},
		},
		"rules": {
			Type:        oam.PropertyTypeArray,
			Description: "GRPCRoute spec.rules: the routing rules, each matching gRPC requests and sending them to backends.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: `matches`, `filters`, `backendRefs` and the rest of GRPCRouteRule, decoded strictly into it.",
			},
		},
	}
}

// ToApplicationConfig decodes an OAM grpcroute component into a
// GRPCRouteConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses.
func (h *GRPCRouteHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[gatewayv1.GRPCRouteSpec](component.Properties, "gateway.networking.k8s.io/v1 GRPCRouteSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	return &GRPCRouteConfig{Name: component.Name, ObjectName: componentObjectName(component), Metadata: component.ObjectMetadata(), Namespace: namespace, Spec: *spec}, nil
}

// GRPCRouteConfig implements stack.ApplicationConfig for grpcroute components.
// Spec is the decoded GRPCRouteSpec exactly as authored.
//
// It reports no traffic source and no backend target, so the NetworkPolicy
// synthesis allows nothing for it.
type GRPCRouteConfig struct {
	Name string
	// ObjectName names the GRPCRoute (oam.Component.ObjectName); empty when it
	// is the component's name.
	ObjectName string
	// Metadata is the labels and annotations authored for the GRPCRoute
	// (oam.Component.ObjectMetadata).
	Metadata  oam.ObjectMetadata
	Namespace string
	Spec      gatewayv1.GRPCRouteSpec
}

// ApplyPolicy is a no-op: the environment policy holds no rule for a
// GRPCRoute, as for an HTTPRoute. Its capability lists gate trait types; a
// consumer that restricts routing restricts the component types it registers.
func (c *GRPCRouteConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the GRPCRoute: kure's identity-only constructor plus a deep
// copy of the spec.
func (c *GRPCRouteConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	route := kubernetes.CreateGRPCRoute(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&route.Spec)
	return kindObject(route, c.Metadata)
}
