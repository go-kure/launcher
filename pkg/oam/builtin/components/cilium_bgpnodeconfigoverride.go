package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumBGPNodeConfigOverrideHandler handles OAM cilium-bgpnodeconfigoverride
// components: the kind-named projection of a cilium.io/v2
// CiliumBGPNodeConfigOverride (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumBGPNodeConfigOverrideSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumBGPNodeConfigOverride, named
// after the component unless `objectName` names it, and nothing else. The
// object is cluster-scoped, and it takes effect on the CiliumBGPNodeConfig of
// the same name, the per-node object the Cilium operator generates.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CiliumBGPNodeConfigOverrideHandler struct{}

// CanHandle returns true for the cilium-bgpnodeconfigoverride component type.
func (h *CiliumBGPNodeConfigOverrideHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-bgpnodeconfigoverride"
}

// PropertySchema declares every top-level
// ciliumv2.CiliumBGPNodeConfigOverrideSpec field by its json name. An entry is
// an open object whose content is checked by the strict decode, not by this
// schema.
func (h *CiliumBGPNodeConfigOverrideHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"bgpInstances": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumBGPNodeConfigOverride spec.bgpInstances: the BGP instances whose configuration is overridden in the CiliumBGPNodeConfig of the object's name, at least one, each under the name its CiliumBGPClusterConfig gives it.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One instance: name (required), routerID (an IPv4 address), localPort, localASN and peers. A peer has a name (required), a localAddress and a localPort: the source address and port of the session with that peer. Decoded strictly into Cilium's API type: see CiliumBGPNodeConfigInstanceOverride in its API reference.",
			},
		},
	}
}

// ciliumBGPNodeConfigOverrideKind is the cilium-bgpnodeconfigoverride kind:
// see policyFreeKind. The CRD has no expression rule; its value rules (at
// least one instance, a name unique in its list, the form of an address) are
// left to the API server.
var ciliumBGPNodeConfigOverrideKind = &policyFreeKind[ciliumv2.CiliumBGPNodeConfigOverrideSpec]{
	upstream: "cilium.io/v2 CiliumBGPNodeConfigOverrideSpec",
	required: map[string]string{
		"bgpInstances":                "the BGP instances overridden, at least one",
		"bgpInstances[].name":         "the name of the BGP instance, as its CiliumBGPClusterConfig gives it",
		"bgpInstances[].peers[].name": "the name of the peer, as its CiliumBGPClusterConfig gives it",
	},
	build: func(name, _ string, spec *ciliumv2.CiliumBGPNodeConfigOverrideSpec) client.Object {
		override := kurecilium.CreateCiliumBGPNodeConfigOverride(name)
		spec.DeepCopyInto(&override.Spec)
		return override
	},
}

// ToApplicationConfig decodes an OAM cilium-bgpnodeconfigoverride component
// into its config. The build namespace is not used: the object is
// cluster-scoped.
func (h *CiliumBGPNodeConfigOverrideHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumBGPNodeConfigOverrideKind.config(component)
}
