package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// HTTPRouteHandler handles OAM httproute components: the kind-named projection
// of a gateway.networking.k8s.io/v1 HTTPRoute (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of gatewayv1.HTTPRouteSpec,
// those of its inlined CommonRouteSpec included, under their json names,
// decoded strictly (decodeKindSpec). It emits the HTTPRoute, named after the
// component in the build namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is an authored object, not the `httproute` trait: a backendRef is a
// reference as written, not a component, so the NetworkPolicy synthesis does
// not read it and allows no traffic for it, and the trait's platform inputs
// (traffic sources, the gateway a capability names) do not reach it.
type HTTPRouteHandler struct{}

// CanHandle returns true for the httproute component type.
func (h *HTTPRouteHandler) CanHandle(componentType string) bool {
	return componentType == "httproute"
}

// PropertySchema declares every top-level gatewayv1.HTTPRouteSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *HTTPRouteHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"parentRefs": {
			Type:        oam.PropertyTypeArray,
			Description: "HTTPRoute spec.parentRefs: the Gateways (or other parents) the route attaches to. No parent is defaulted.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One parent: `name`, with optional `group`, `kind`, `namespace`, `sectionName` and `port`. Decoded strictly into ParentReference.",
			},
		},
		"useDefaultGateways": {
			Type:        oam.PropertyTypeString,
			Description: "HTTPRoute spec.useDefaultGateways: the scope of default Gateways the route also attaches to (All, None). An experimental-channel field: a cluster whose HTTPRoute CRD is the standard channel's refuses it.",
		},
		"hostnames": {
			Type:        oam.PropertyTypeArray,
			Description: "HTTPRoute spec.hostnames: the Host headers the route matches, each a hostname or a `*.` wildcard. Unset matches every host the parent's listener accepts.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A hostname, optionally prefixed with a `*.` wildcard label."},
		},
		"rules": {
			Type:        oam.PropertyTypeArray,
			Description: "HTTPRoute spec.rules: the routing rules, each matching requests and sending them to backends.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One rule: `matches`, `filters`, `backendRefs`, `timeouts` and the rest of HTTPRouteRule, decoded strictly into it.",
			},
		},
	}
}

// ToApplicationConfig decodes an OAM httproute component into an
// HTTPRouteConfig, under the package's null contract and the strict decode
// every spec-projecting kind uses.
func (h *HTTPRouteHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[gatewayv1.HTTPRouteSpec](component.Properties, "gateway.networking.k8s.io/v1 HTTPRouteSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	return &HTTPRouteConfig{Name: component.Name, ObjectName: componentObjectName(component), Namespace: namespace, Spec: *spec}, nil
}

// HTTPRouteConfig implements stack.ApplicationConfig for httproute components.
// Spec is the decoded HTTPRouteSpec exactly as authored.
//
// It reports no traffic source and no backend target, so the NetworkPolicy
// synthesis allows nothing for it: those are the `httproute` trait's, which
// receives them from capability rendering.
type HTTPRouteConfig struct {
	Name string
	// ObjectName names the HTTPRoute (oam.Component.ObjectName); empty when it
	// is the component's name.
	ObjectName string
	Namespace  string
	Spec       gatewayv1.HTTPRouteSpec
}

// ApplyPolicy is a no-op: the environment policy holds no rule for an
// HTTPRoute. Its capability lists gate trait types, so a policy that forbids
// the `httproute` trait does not refuse this component; a consumer that
// restricts routing restricts the component types it registers.
func (c *HTTPRouteConfig) ApplyPolicy(oam.Policy) error {
	return nil
}

// Generate emits the HTTPRoute: kure's identity-only constructor plus a deep
// copy of the spec.
func (c *HTTPRouteConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	route := kubernetes.CreateHTTPRoute(kindObjectName(c.ObjectName, app.Name), app.Namespace)
	c.Spec.DeepCopyInto(&route.Spec)
	obj := client.Object(route)
	return []*client.Object{&obj}, nil
}
