package components

import (
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
// TestGatewayKinds_RequiredMatchCRD holds each kind's required list to them,
// and TestGatewayKinds_NoDefaultedZeros the claim that no authored 0 or false
// is lost on these types.
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
		Description: "One listener: `name`, `port` (1 to 65535) and `protocol` (HTTP, HTTPS, TLS, TCP, UDP, or a domain-prefixed one), which the API requires and launcher asks for; `hostname`, `tls` (`mode`, `certificateRefs`, `options`) and `allowedRoutes` (`namespaces`, `kinds`) are optional." + gatewayDecoded + typ + " in its API reference.",
	}
}

// gatewayListenerRequired is the required list of the listeners under the
// path at ("listeners[]") that a Gateway and a ListenerSet share: of a
// certificate reference and of an allowed route kind that are authored, the
// field the API requires that the type would write empty.
func gatewayListenerRequired(at string) map[string]string {
	return map[string]string{
		at + ".tls.certificateRefs[].name": "the name of the Secret, or of the other object, that holds the certificate",
		at + ".allowedRoutes.kinds[].kind": "the kind of route the listener allows, such as HTTPRoute",
	}
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
