package components

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of the Gateway API's
// gateway.networking.k8s.io/v1 infrastructure objects share
// (go-kure/launcher#790): gatewayclass, gateway, listenerset, referencegrant
// and backendtlspolicy. Each is a policyFreeKind: the object runs no pod,
// holds no image, requests no storage and has no replica count. A Gateway's
// controller may start pods for it, and the object sizes none of them.
//
// The module ships the CRDs of both channels of the API.
// Each kind's required list is generated from them (kind_required.go),
// TestGatewayKinds_WrittenEmptyAccepted holds the fields the types write
// unauthored to them, and TestGatewayKinds_DefaultedZeros the claim that a CRD default turns an
// authored 0, false or "" into another value only on the fields of a kind's
// defaultedZeros, where the kind refuses one.
//
// A host these objects name is one a Gateway serves or a backend is checked
// against, not an artifact source: a listener's hostname, a Gateway's
// requested address, the hostname and the subject alternative names a
// BackendTLSPolicy validates. None is held to the environment policy's
// allowed registries.
//
// No field holds a literal secret: a certificate is a reference to a Secret or
// to another object. The two free maps, a listener's tls.options and a
// BackendTLSPolicy's options, are written as authored.

// gatewayDecoded ends the description of a property the strict decode checks
// below the schema's depth.
const gatewayDecoded = " Decoded strictly into the Gateway API's type: see "

// gatewayListenerItems is the item schema of a list of listeners, on a
// Gateway and on a ListenerSet. Launcher asks for name, port and protocol on
// both: a Gateway's type writes them unauthored and its required list names
// them, a ListenerSet's omits them and its validate refuses the omission.
func gatewayListenerItems(typ string) *oam.PropertySchema {
	return &oam.PropertySchema{
		Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: "One listener: `name`, `port` (1 to 65535) and `protocol` (HTTP, HTTPS, TLS, TCP, UDP, or a domain-prefixed one), which the API requires and launcher asks for; `hostname`, `tls` (`mode`, `certificateRefs`, `options`), `allowedRoutes` (`namespaces`, `kinds`) and `filters` (`requests`, each an ExternalAuth or ExtensionRef filter; an experimental-channel field) are optional." + gatewayDecoded + typ + " in its API reference.",
	}
}

// gatewayListenerRequired is the required list of the listeners under the
// path at ("listeners[]") that a Gateway and a ListenerSet share: of a
// certificate reference, of an allowed route kind and of a request filter that
// are authored, the fields the API requires that the type would write empty,
// and the key and the operator of a match expression in the selector of the
// namespaces routes may attach from (gatewayNamespacesRequired).
func gatewayListenerRequired(at string) map[string]string {
	const filter = ".filters.requests[]"
	return requiredFields(
		map[string]string{
			at + ".tls.certificateRefs[].name": "the name of the Secret, or of the other object, that holds the certificate",
			at + ".allowedRoutes.kinds[].kind": "the kind of route the listener allows, such as HTTPRoute",

			at + filter + ".type":                         "the type of the filter, ExternalAuth or ExtensionRef",
			at + filter + ".externalAuth.backendRef":      "the backend the authorization requests are sent to",
			at + filter + ".externalAuth.backendRef.name": "the name of the backend the authorization requests are sent to",
		},
		gatewayNamespacesRequired(at+".allowedRoutes.namespaces"),
		gatewayReferenceRequired("the object that implements the filter", at+filter+".extensionRef"),
	)
}

// refuseOmittedListenerFilterFields refuses a request filter of the listener
// at index i that leaves out a field the API requires and the type omits
// when it is not authored or is authored empty: the protocol of an external
// authorization. A filter's other required fields the type writes, and the
// kind's required list names them (gatewayListenerRequired).
func refuseOmittedListenerFilterFields(i int, filters *gatewayv1.ListenerFilters) error {
	if filters == nil {
		return nil
	}
	for j, filter := range filters.Requests {
		if filter.ExternalAuth != nil && filter.ExternalAuth.ExternalAuthProtocol == "" {
			return errors.Errorf("listeners[%d].filters.requests[%d].externalAuth.protocol: required (the protocol the authorization server speaks, HTTP or GRPC)", i, j)
		}
	}
	return nil
}

// gatewayNamespacesRequired is the required list of the namespace choices
// under the paths at ("listeners[].allowedRoutes.namespaces"): the key and the
// operator of a match expression of their `selector`, a Kubernetes label
// selector (labelSelectorRequired). The CRDs require both, which
// TestLabelSelectorKinds_CoverEverySelector holds them to; the rest of an
// expression is left to the API server.
func gatewayNamespacesRequired(at ...string) map[string]string {
	selectors := make([]string, 0, len(at))
	for _, path := range at {
		selectors = append(selectors, path+".selector")
	}
	return labelSelectorRequired(selectors...)
}

// gatewayReferenceRequired is the required list of the references under the
// paths at whose group, kind and name the API requires and the type would
// write empty. what says what a reference names ("the parameters object").
func gatewayReferenceRequired(what string, at ...string) map[string]string {
	out := make(map[string]string, 3*len(at))
	for _, path := range at {
		out[path+".group"] = "the API group of " + what + `; "" is the core group`
		out[path+".kind"] = "the kind of " + what
		out[path+".name"] = "the name of " + what
	}
	return out
}
