package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// GatewayClassHandler handles OAM gatewayclass components: the kind-named
// projection of a gateway.networking.k8s.io/v1 GatewayClass, which is
// cluster-scoped (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// gatewayv1.GatewayClassSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the GatewayClass, named after the component
// unless `objectName` names it, with no namespace, and nothing else: the
// object `parametersRef` names is not created. TestCoreKindSchemas_CoverSpec
// keeps the published key set equal to the upstream json tags.
type GatewayClassHandler struct{}

// CanHandle returns true for the gatewayclass component type.
func (h *GatewayClassHandler) CanHandle(componentType string) bool {
	return componentType == "gatewayclass"
}

// PropertySchema declares every top-level gatewayv1.GatewayClassSpec field by
// its json name.
func (h *GatewayClassHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "GatewayClass spec."
	return map[string]oam.PropertySchema{
		"controllerName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "controllerName: the controller that manages Gateways of this class, as a domain-prefixed path (example.net/gateway-controller). The API refuses a change to it once the object exists.",
		},
		"parametersRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "parametersRef: the object that holds the class's configuration, as the controller defines it: `group`, `kind` and `name`, all three required, and `namespace` for a namespaced one." + gatewayDecoded + "ParametersReference in its API reference.",
		},
		"description": {
			Type:        oam.PropertyTypeString,
			Description: spec + "description: a description of the class, of at most 64 characters.",
		},
	}
}

// gatewayClassKind is the gatewayclass kind: see policyFreeKind. The API
// requires `controllerName`, and of a `parametersRef` that is authored its
// group, kind and name; the type would write each one empty. The API's value
// rules are left to the API server. TestGatewayKinds_RequiredMatchCRD holds
// the list to the CRD.
var gatewayClassKind = &policyFreeKind[gatewayv1.GatewayClassSpec]{
	upstream: "gateway.networking.k8s.io/v1 GatewayClassSpec",
	required: requiredFields(
		map[string]string{"controllerName": "the controller that manages Gateways of this class, as a domain-prefixed path"},
		gatewayReferenceRequired("the object that holds the class's parameters", "parametersRef"),
	),
	build: func(name, _ string, spec *gatewayv1.GatewayClassSpec) client.Object {
		class := kubernetes.CreateGatewayClass(name)
		spec.DeepCopyInto(&class.Spec)
		return class
	},
}

// ToApplicationConfig decodes an OAM gatewayclass component into its config.
// The build namespace is not used: a GatewayClass is cluster-scoped.
func (h *GatewayClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return gatewayClassKind.config(component)
}
