package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// GatewayHandler handles OAM gateway components: the kind-named projection of
// a gateway.networking.k8s.io/v1 Gateway (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of gatewayv1.GatewaySpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// Gateway, named after the component unless `objectName` names it, in the
// build namespace, and nothing else: no GatewayClass, no Secret a listener
// names and none of what the Gateway's controller creates for it.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
//
// It is the authored object. The Gateway an `httproute` trait takes its
// parent from, where it authors none, is the one a capability names
// (`gatewayName`, `gatewayNamespace`), which this kind neither reads nor
// provides.
type GatewayHandler struct{}

// CanHandle returns true for the gateway component type.
func (h *GatewayHandler) CanHandle(componentType string) bool {
	return componentType == "gateway"
}

// PropertySchema declares every top-level gatewayv1.GatewaySpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *GatewayHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "Gateway spec."
	return map[string]oam.PropertySchema{
		"gatewayClassName": {
			Type: oam.PropertyTypeString, Required: true,
			Description: "Required. " + spec + "gatewayClassName: the name of the GatewayClass whose controller implements the Gateway. It is the author's: launcher points it at no component.",
		},
		"listeners": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. " + spec + "listeners: the logical endpoints bound on the Gateway's addresses, at least one and at most 64, each of a unique name and a unique combination of port, protocol and hostname.",
			Items:       gatewayListenerItems("Listener"),
		},
		"addresses": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "addresses: the addresses requested for the Gateway, at most 16. Unset, the controller assigns one.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One address: `type` (IPAddress, the API's default, Hostname, or a domain-prefixed one) and `value`." + gatewayDecoded + "GatewaySpecAddress in its API reference.",
			},
		},
		"infrastructure": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "infrastructure: what the controller applies to the resources it creates for the Gateway: `labels` (at most 8) and `annotations` (at most 16), and `parametersRef` (`group`, `kind` and `name`, all three required), an object of the Gateway's namespace that holds its configuration. These are not the Gateway's own labels and annotations." + gatewayDecoded + "GatewayInfrastructure in its API reference.",
		},
		"allowedListeners": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "allowedListeners: the namespaces whose ListenerSets may attach to the Gateway: `namespaces` with `from` (All, Selector, Same, or None, the API's default) and `selector`. Unset, none may." + gatewayDecoded + "AllowedListeners in its API reference.",
		},
		"tls": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "tls: the Gateway-wide TLS configuration: `backend` (`clientCertificateRef`, the client certificate the Gateway presents to its backends) and `frontend` (`default`, required, and `perPort`, each with the `validation` of client certificates: `caCertificateRefs`, required, and `mode`)." + gatewayDecoded + "GatewayTLSConfig in its API reference.",
		},
		"defaultScope": {
			Type:        oam.PropertyTypeString,
			Description: spec + "defaultScope: whether the Gateway is a default one, to which a route that asks for default Gateways attaches (All, None). An experimental-channel field: a cluster whose Gateway CRD is the standard channel's does not hold it.",
		},
	}
}

// gatewayRequired is the required list of the gateway kind: the fields the
// Gateway CRD requires that the Go types write whether or not they were
// authored. TestGatewayKinds_RequiredMatchCRD holds the list to the CRD.
var gatewayRequired = func() map[string]string {
	const (
		frontend   = "tls.frontend."
		validation = ".validation.caCertificateRefs"
		caRefs     = "the objects that hold the CA certificates a client certificate is validated against"
		caRef      = "the object that holds the CA certificates"
	)
	return requiredFields(
		map[string]string{
			"gatewayClassName":     "the name of the GatewayClass whose controller implements the Gateway",
			"listeners":            "the listeners of the Gateway, at least one",
			"listeners[].name":     "the name of the listener, unique in the Gateway",
			"listeners[].port":     "the network port the listener is bound on",
			"listeners[].protocol": "the protocol the listener expects, such as HTTP or HTTPS",

			"tls.backend.clientCertificateRef.name": "the name of the Secret that holds the client certificate",

			frontend + "default":                    "the TLS configuration of every HTTPS listener no perPort entry covers; {} validates no client certificate",
			frontend + "default" + validation:       caRefs,
			frontend + "perPort[].port":             "the port the configuration applies to",
			frontend + "perPort[].tls":              "the TLS configuration of the HTTPS listeners on the port",
			frontend + "perPort[].tls" + validation: caRefs,
		},
		gatewayListenerRequired("listeners[]"),
		gatewayNamespacesRequired("allowedListeners.namespaces"),
		gatewayReferenceRequired("the object that holds the Gateway's parameters", "infrastructure.parametersRef"),
		gatewayReferenceRequired(caRef, frontend+"default"+validation+"[]", frontend+"perPort[].tls"+validation+"[]"),
	)
}()

// gatewayKind is the gateway kind: see policyFreeKind. The API requires
// `gatewayClassName` and `listeners`, of a listener its name, port and
// protocol, and of what is authored below them the fields gatewayRequired
// lists; the type would write each one empty. The API's value rules, those it
// writes as expressions included (a listener's tls against its protocol, the
// uniqueness of a listener), are left to the API server.
var gatewayKind = &policyFreeKind[gatewayv1.GatewaySpec]{
	upstream: "gateway.networking.k8s.io/v1 GatewaySpec",
	required: gatewayRequired,
	build: func(name, namespace string, spec *gatewayv1.GatewaySpec) client.Object {
		gateway := kubernetes.CreateGateway(name, namespace)
		spec.DeepCopyInto(&gateway.Spec)
		return gateway
	},
}

// ToApplicationConfig decodes an OAM gateway component into its config. The
// object takes the namespace of the application it is generated in.
func (h *GatewayHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return gatewayKind.config(component)
}
