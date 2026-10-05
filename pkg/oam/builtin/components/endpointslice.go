package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	discoveryv1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// EndpointSliceHandler handles OAM endpointslice components: the kind-named
// projection of a discovery.k8s.io/v1 EndpointSlice (go-kure/launcher#790).
//
// An EndpointSlice has no spec: its properties are the object's own top-level
// fields, under their json names, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the EndpointSlice,
// named after the component unless `objectName` names it, in the build
// namespace, and nothing else. The slice belongs to a Service only through its
// `kubernetes.io/service-name` label, which the author writes under `labels`:
// launcher derives it from nothing. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags, less the object's own
// identity.
type EndpointSliceHandler struct{}

// CanHandle returns true for the endpointslice component type.
func (h *EndpointSliceHandler) CanHandle(componentType string) bool {
	return componentType == "endpointslice"
}

// PropertySchema declares every authorable discoveryv1.EndpointSlice field by
// its json name. The endpoints and the ports are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *EndpointSliceHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"addressType": {
			Type:        oam.PropertyTypeString,
			Required:    true,
			Description: "Required. EndpointSlice addressType: the type of every address in the slice: IPv4, IPv6 or FQDN (deprecated). Immutable once created.",
		},
		"endpoints": {
			Type:        oam.PropertyTypeArray,
			Description: "EndpointSlice endpoints: the backends of the slice. Unauthored, the object carries endpoints: null, a slice with no endpoint.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One endpoint: addresses (required), conditions, hostname, targetRef, nodeName, zone, hints, deprecatedTopology. Decoded strictly into the Kubernetes API type: see Endpoint in the Kubernetes API reference.",
			},
		},
		"ports": {
			Type:        oam.PropertyTypeArray,
			Description: "EndpointSlice ports: the ports every endpoint of the slice exposes. Unauthored, the object carries ports: null, a slice with no port.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One port: name, protocol, port, appProtocol. Decoded strictly into the Kubernetes API type: see EndpointPort in the Kubernetes API reference.",
			},
		},
	}
}

// endpointSliceKind is the endpointslice kind: see policyFreeKind. The
// required list holds what the API requires and the Go type writes when it was
// not authored; TestBuiltinMarkerKinds_RequiredMatchMarkers derives it from
// the markers of the linked k8s.io/api source.
//
// The API server's own validation is in no linked module. Read by hand
// (pkg/apis/discovery/validation/validation.go, Kubernetes v1.37.1), it
// requires one field beyond the markers, a port's protocol, which the API
// server's defaulting fills before it validates
// (pkg/apis/discovery/v1/defaults.go): a port without one passes that
// validation, and the kind does not refuse it. Every rule on the form of a value
// (an address against the address type, the limits on endpoints, addresses,
// ports and hints, unique port names) is left to the API server, and so is
// the rule that addressType does not change once the slice exists.
var endpointSliceKind = &policyFreeKind[discoveryv1.EndpointSlice]{
	upstream:    "discovery.k8s.io/v1 EndpointSlice (an endpointslice component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	required: map[string]string{
		"addressType":                       "the type of every address in the slice: IPv4, IPv6 or FQDN",
		"endpoints[].addresses":             "the addresses of the endpoint, at least one",
		"endpoints[].hints.forZones[].name": "the name of a zone the endpoint is to be consumed by",
		"endpoints[].hints.forNodes[].name": "the name of a node the endpoint is to be consumed by",
	},
	build: func(name, namespace string, authored *discoveryv1.EndpointSlice) client.Object {
		identity := kubernetes.CreateEndpointSlice(name, namespace)
		slice := authored.DeepCopy()
		slice.TypeMeta, slice.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return slice
	},
}

// ToApplicationConfig decodes an OAM endpointslice component into its config.
// The object lands in the application's namespace at Generate.
func (h *EndpointSliceHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return endpointSliceKind.config(component)
}
