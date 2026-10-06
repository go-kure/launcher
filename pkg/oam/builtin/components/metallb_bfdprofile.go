package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBBFDProfileHandler handles OAM metallb-bfdprofile components: the
// kind-named projection of a metallb.io/v1beta1 BFDProfile
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta1.BFDProfileSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the BFDProfile, named after the component unless
// `objectName` names it, in the build namespace, and nothing else. The API
// requires no field. A profile is the settings of a BFD session, and a MetalLB
// BGPPeer names the one its session uses. TestCoreKindSchemas_CoverSpec keeps
// the published key set equal to the upstream json tags.
type MetalLBBFDProfileHandler struct{}

// CanHandle returns true for the metallb-bfdprofile component type.
func (h *MetalLBBFDProfileHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-bfdprofile"
}

// PropertySchema declares every top-level metallbv1beta1.BFDProfileSpec field
// by its json name.
func (h *MetalLBBFDProfileHandler) PropertySchema() map[string]oam.PropertySchema {
	milliseconds := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: description + " In milliseconds; the API takes 10 to 60000."}
	}
	return map[string]oam.PropertySchema{
		"receiveInterval":  milliseconds("BFDProfile spec.receiveInterval: the minimum interval at which this system can receive BFD control packets."),
		"transmitInterval": milliseconds("BFDProfile spec.transmitInterval: the minimum interval, less jitter, at which this system wants to send BFD control packets."),
		"echoInterval":     milliseconds("BFDProfile spec.echoInterval: the minimum echo receive interval this system can handle."),
		"detectMultiplier": {
			Type:        oam.PropertyTypeInteger,
			Description: "BFDProfile spec.detectMultiplier: the number the remote transmission interval is multiplied by to get the time after which the connection is taken for lost. The API takes 2 to 255.",
		},
		"echoMode": {
			Type:        oam.PropertyTypeBoolean,
			Description: "BFDProfile spec.echoMode: whether the echo transmission mode is on. Not supported on multi-hop setups.",
		},
		"passiveMode": {
			Type:        oam.PropertyTypeBoolean,
			Description: "BFDProfile spec.passiveMode: true, the session is passive: it does not start the connection and waits for the peer's control packets before it replies.",
		},
		"minimumTtl": {
			Type:        oam.PropertyTypeInteger,
			Description: "BFDProfile spec.minimumTtl: for multi-hop sessions only, the minimum time-to-live expected of an incoming BFD control packet. The API takes 1 to 254.",
		},
	}
}

// metallbBFDProfileKind is the metallb-bfdprofile kind: see policyFreeKind.
// The API requires no field, and the CRD has no default and no expression
// rule. Every field is a pointer, so an authored 0 or false is written. The
// bounds the CRD sets on the numbers are left to the API server.
var metallbBFDProfileKind = &policyFreeKind[metallbv1beta1.BFDProfileSpec]{
	upstream: "metallb.io/v1beta1 BFDProfileSpec",
	build: func(name, namespace string, spec *metallbv1beta1.BFDProfileSpec) client.Object {
		profile := kuremetallb.CreateBFDProfile(name, namespace)
		spec.DeepCopyInto(&profile.Spec)
		return profile
	},
}

// ToApplicationConfig decodes an OAM metallb-bfdprofile component into its
// config. The object lands in the application's namespace at Generate.
func (h *MetalLBBFDProfileHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbBFDProfileKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBBFDProfileHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-bfdprofile")
}

// ComponentObject declares the metallb-bfdprofile kind's BFDProfile.
func (h *MetalLBBFDProfileHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("BFDProfile"), oam.ObjectScopeNamespaced
}
