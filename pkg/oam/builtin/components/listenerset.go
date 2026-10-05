package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ListenerSetHandler handles OAM listenerset components: the kind-named
// projection of a gateway.networking.k8s.io/v1 ListenerSet
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// gatewayv1.ListenerSetSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ListenerSet, named after the component unless
// `objectName` names it, in the build namespace, and nothing else: the Gateway
// `parentRef` names is not created, and whether that Gateway allows the
// attachment is its own `allowedListeners`. TestCoreKindSchemas_CoverSpec
// keeps the published key set equal to the upstream json tags.
type ListenerSetHandler struct{}

// CanHandle returns true for the listenerset component type.
func (h *ListenerSetHandler) CanHandle(componentType string) bool {
	return componentType == "listenerset"
}

// PropertySchema declares every top-level gatewayv1.ListenerSetSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *ListenerSetHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ListenerSet spec."
	return map[string]oam.PropertySchema{
		"parentRef": {
			Type: oam.PropertyTypeObject, Required: true, AdditionalProperties: true,
			Description: "Required. " + spec + "parentRef: the Gateway the listeners attach to: `name`, required, and `group`, `kind` (Gateway, the API's default) and `namespace` (the ListenerSet's own, unset)." + gatewayDecoded + "ParentGatewayReference in its API reference.",
		},
		"listeners": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "listeners: the listeners added to the Gateway, at least one and at most 64, each of a unique name and a unique combination of port, protocol and hostname.",
			Items:       gatewayListenerItems("ListenerEntry"),
		},
	}
}

// listenerSetKind is the listenerset kind: see policyFreeKind. The API
// requires `parentRef` with its name, which the type would write empty, and at
// least one of `listeners`, which the type omits when empty: validate refuses
// that one, as the API server refuses a ListenerSet without it. A listener's
// name, port and protocol are required too, and the type omits each one that
// is not authored or is authored empty (a port of 0 included): validate
// refuses those as well, naming the listener by its index. The API's value
// rules are left to the API server. TestGatewayKinds_RequiredMatchCRD holds
// the list to the CRD, and what validate refuses to the fields the CRD
// requires that the type omits.
var listenerSetKind = &policyFreeKind[gatewayv1.ListenerSetSpec]{
	upstream: "gateway.networking.k8s.io/v1 ListenerSetSpec",
	required: requiredFields(
		map[string]string{
			"parentRef":      "the Gateway the listeners attach to",
			"parentRef.name": "the name of the Gateway the listeners attach to",
		},
		gatewayListenerRequired("listeners[]"),
	),
	validate: func(spec *gatewayv1.ListenerSetSpec) error {
		if len(spec.Listeners) == 0 {
			return errors.New("listeners: required (at least one listener to add to the Gateway)")
		}
		for i, listener := range spec.Listeners {
			switch {
			case listener.Name == "":
				return errors.Errorf("listeners[%d].name: required (the name of the listener, unique in the ListenerSet)", i)
			case listener.Port == 0:
				return errors.Errorf("listeners[%d].port: required (the network port the listener is bound on)", i)
			case listener.Protocol == "":
				return errors.Errorf("listeners[%d].protocol: required (the protocol the listener expects, such as HTTP or HTTPS)", i)
			}
		}
		return nil
	},
	build: func(name, namespace string, spec *gatewayv1.ListenerSetSpec) client.Object {
		set := kubernetes.CreateListenerSet(name, namespace)
		spec.DeepCopyInto(&set.Spec)
		return set
	},
}

// ToApplicationConfig decodes an OAM listenerset component into its config.
// The object takes the namespace of the application it is generated in.
func (h *ListenerSetHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return listenerSetKind.config(component)
}
