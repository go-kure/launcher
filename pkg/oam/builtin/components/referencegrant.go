package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// ReferenceGrantHandler handles OAM referencegrant components: the kind-named
// projection of a gateway.networking.k8s.io/v1 ReferenceGrant
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// gatewayv1.ReferenceGrantSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ReferenceGrant, named after the component
// unless `objectName` names it, in the build namespace, and nothing else. The
// grant allows references into the namespace it is in, so it belongs to the
// application of the objects referred to, not to the one that refers.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ReferenceGrantHandler struct{}

// CanHandle returns true for the referencegrant component type.
func (h *ReferenceGrantHandler) CanHandle(componentType string) bool {
	return componentType == "referencegrant"
}

// PropertySchema declares every top-level gatewayv1.ReferenceGrantSpec field
// by its json name.
func (h *ReferenceGrantHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ReferenceGrant spec."
	return map[string]oam.PropertySchema{
		"from": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "from: the kinds of object, and the namespace each is in, that may refer to the objects `to` names, at least one and at most 16.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One source of references: `group` (\"\" is the core group), `kind` and `namespace`, all three required." + gatewayDecoded + "ReferenceGrantFrom in its API reference.",
			},
		},
		"to": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "to: the kinds of object in the grant's namespace that may be referred to, at least one and at most 16.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One target of references: `group` (\"\" is the core group) and `kind`, both required, and `name`; unset, every object of the kind in the namespace." + gatewayDecoded + "ReferenceGrantTo in its API reference.",
			},
		},
	}
}

// referenceGrantKind is the referencegrant kind: see policyFreeKind. The API
// requires `from` and `to`, of a source its group, kind and namespace and of a
// target its group and kind; the type would write each one empty. The API's
// value rules are left to the API server. TestGatewayKinds_RequiredMatchCRD
// holds the list to the CRD.
var referenceGrantKind = &policyFreeKind[gatewayv1.ReferenceGrantSpec]{
	upstream: "gateway.networking.k8s.io/v1 ReferenceGrantSpec",
	required: map[string]string{
		"from":             "the kinds of object, and their namespaces, that may refer to the objects `to` names, at least one",
		"from[].group":     `the API group of the objects that refer; "" is the core group`,
		"from[].kind":      "the kind of the objects that refer, such as HTTPRoute",
		"from[].namespace": "the namespace of the objects that refer",
		"to":               "the kinds of object in this namespace that may be referred to, at least one",
		"to[].group":       `the API group of the objects referred to; "" is the core group`,
		"to[].kind":        "the kind of the objects referred to, such as Service or Secret",
	},
	build: func(name, namespace string, spec *gatewayv1.ReferenceGrantSpec) client.Object {
		grant := kubernetes.CreateReferenceGrant(name, namespace)
		spec.DeepCopyInto(&grant.Spec)
		return grant
	},
}

// ToApplicationConfig decodes an OAM referencegrant component into its config.
// The object takes the namespace of the application it is generated in.
func (h *ReferenceGrantHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return referenceGrantKind.config(component)
}
