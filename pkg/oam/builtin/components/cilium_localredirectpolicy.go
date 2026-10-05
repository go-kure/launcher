package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumLocalRedirectPolicyHandler handles OAM cilium-localredirectpolicy
// components: the kind-named projection of a cilium.io/v2
// CiliumLocalRedirectPolicy (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumLocalRedirectPolicySpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumLocalRedirectPolicy, named
// after the component unless `objectName` names it, in the build namespace,
// and nothing else. The backend's `localEndpointSelector` is the author's.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CiliumLocalRedirectPolicyHandler struct{}

// CanHandle returns true for the cilium-localredirectpolicy component type.
func (h *CiliumLocalRedirectPolicyHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-localredirectpolicy"
}

// PropertySchema declares every top-level
// ciliumv2.CiliumLocalRedirectPolicySpec field by its json name. Structured
// fields are open objects whose content is checked by the strict decode, not
// by this schema.
func (h *CiliumLocalRedirectPolicyHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"redirectFrontend": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. CiliumLocalRedirectPolicy spec.redirectFrontend: the traffic to redirect, by exactly one of addressMatcher (ip and toPorts, both required) and serviceMatcher (serviceName and namespace, both required, and toPorts; without toPorts, every port of the Service). A port has a port and a protocol (both required: TCP or UDP) and a name. The API refuses a change once the object exists.",
		},
		"redirectBackend": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true, Required: true,
			Description: "Required. CiliumLocalRedirectPolicy spec.redirectBackend: where the traffic goes: localEndpointSelector (required: the label query over the pods of the node, matchLabels and matchExpressions) and toPorts (required: the ports of those pods, each with a port and a protocol, both required, and a name). The API refuses a change once the object exists.",
		},
		"skipRedirectFromBackend": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CiliumLocalRedirectPolicy spec.skipRedirectFromBackend: true, traffic the backend pods themselves send to the frontend is not redirected. Unset or false, it is: the API's default is false. The API refuses a change once the object exists.",
		},
		"description": {
			Type:        oam.PropertyTypeString,
			Description: "CiliumLocalRedirectPolicy spec.description: what the policy is for, in the author's words.",
		},
	}
}

// ciliumPortRequired is the required list of one list of ports under the path
// at ("redirectBackend.toPorts"): the two fields the API requires of a port,
// each of which the Go type would write empty.
func ciliumPortRequired(at string) map[string]string {
	return map[string]string{
		at + "[].port":     "the port number, as a string",
		at + "[].protocol": "the port's protocol: TCP or UDP",
	}
}

// ciliumLocalRedirectPolicyKind is the cilium-localredirectpolicy kind: see
// policyFreeKind. validate holds the one-of the CRD's schema declares on
// `redirectFrontend`. The CRD's three expression rules compare a field with
// its value on the stored object and say nothing of a new one; they and the
// API's value rules are left to the API server.
var ciliumLocalRedirectPolicyKind = &policyFreeKind[ciliumv2.CiliumLocalRedirectPolicySpec]{
	upstream: "cilium.io/v2 CiliumLocalRedirectPolicySpec",
	required: requiredFields(
		map[string]string{
			"redirectFrontend":                            "the traffic to redirect",
			"redirectFrontend.addressMatcher.ip":          "the destination address of the traffic to redirect",
			"redirectFrontend.addressMatcher.toPorts":     "the destination ports of the traffic to redirect",
			"redirectFrontend.serviceMatcher.serviceName": "the name of the Service whose traffic is redirected",
			"redirectFrontend.serviceMatcher.namespace":   "the namespace of that Service",
			"redirectBackend":                             "where the traffic is redirected to",
			"redirectBackend.localEndpointSelector":       "the label query over the pods of the node the traffic is redirected to",
			"redirectBackend.toPorts":                     "the ports of those pods the traffic is redirected to",
		},
		ciliumPortRequired("redirectFrontend.addressMatcher.toPorts"),
		ciliumPortRequired("redirectFrontend.serviceMatcher.toPorts"),
		ciliumPortRequired("redirectBackend.toPorts"),
		ciliumSelectorRequired("redirectBackend.localEndpointSelector"),
	),
	validate: validateCiliumLocalRedirectPolicy,
	build: func(name, namespace string, spec *ciliumv2.CiliumLocalRedirectPolicySpec) client.Object {
		policy := kurecilium.CreateCiliumLocalRedirectPolicy(name, namespace)
		spec.DeepCopyInto(&policy.Spec)
		return policy
	},
}

// validateCiliumLocalRedirectPolicy refuses a frontend with no matcher and
// one with both: the CRD's schema takes exactly one of `addressMatcher` and
// `serviceMatcher` (its oneOf on `redirectFrontend`,
// TestCiliumPlainKinds_RedirectFrontendTakesOneMatcher).
func validateCiliumLocalRedirectPolicy(spec *ciliumv2.CiliumLocalRedirectPolicySpec) error {
	frontend := spec.RedirectFrontend
	switch {
	case frontend.AddressMatcher == nil && frontend.ServiceMatcher == nil:
		return errors.New("redirectFrontend: one of addressMatcher and serviceMatcher is required")
	case frontend.AddressMatcher != nil && frontend.ServiceMatcher != nil:
		return errors.New("redirectFrontend: addressMatcher and serviceMatcher are both set; the API takes exactly one")
	}
	return nil
}

// ToApplicationConfig decodes an OAM cilium-localredirectpolicy component into
// its config. The object lands in the application's namespace at Generate.
func (h *CiliumLocalRedirectPolicyHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumLocalRedirectPolicyKind.config(component)
}
