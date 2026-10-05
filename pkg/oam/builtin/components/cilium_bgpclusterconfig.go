package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumBGPClusterConfigHandler handles OAM cilium-bgpclusterconfig
// components: the kind-named projection of a cilium.io/v2
// CiliumBGPClusterConfig (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumBGPClusterConfigSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumBGPClusterConfig, named after
// the component unless `objectName` names it, and nothing else. The object is
// cluster-scoped: it carries no namespace, whatever namespace the application
// is built for. `nodeSelector` is the author's. TestCoreKindSchemas_CoverSpec
// keeps the published key set equal to the upstream json tags.
type CiliumBGPClusterConfigHandler struct{}

// CanHandle returns true for the cilium-bgpclusterconfig component type.
func (h *CiliumBGPClusterConfigHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-bgpclusterconfig"
}

// PropertySchema declares every top-level ciliumv2.CiliumBGPClusterConfigSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *CiliumBGPClusterConfigHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"nodeSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumBGPClusterConfig spec.nodeSelector: the label query over the nodes this BGP configuration applies to (matchLabels, matchExpressions). Unset or empty, every node. Decoded strictly into Cilium's API type.",
		},
		"bgpInstances": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumBGPClusterConfig spec.bgpInstances: the BGP instances the selected nodes run, one to sixteen, each under a name of its own.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One instance: name (required), localASN, localPort (unset, the instance listens for no incoming connection) and peers. A peer has a name (required), a peerAddress or an autoDiscovery (mode, required, and defaultGateway with its addressFamily, required), a peerASN (unset or 0, any ASN the peer opens with is accepted) and a peerConfigRef (name, required: the CiliumBGPPeerConfig the peer is configured by). Decoded strictly into Cilium's API type: see CiliumBGPInstance in its API reference.",
			},
		},
	}
}

// ciliumBGPClusterConfigKind is the cilium-bgpclusterconfig kind: see
// policyFreeKind. The CRD has no expression rule; its value rules (one to
// sixteen instances, a name unique in its list, the ranges of an ASN and a
// port, the form of an address) are left to the API server.
var ciliumBGPClusterConfigKind = &policyFreeKind[ciliumv2.CiliumBGPClusterConfigSpec]{
	upstream: "cilium.io/v2 CiliumBGPClusterConfigSpec",
	required: requiredFields(map[string]string{
		"bgpInstances":                              "the BGP instances the selected nodes run, at least one",
		"bgpInstances[].name":                       "the name of the BGP instance",
		"bgpInstances[].peers[].name":               "the name of the peer",
		"bgpInstances[].peers[].autoDiscovery.mode": "how the peer is discovered: DefaultGateway",
		"bgpInstances[].peers[].autoDiscovery.defaultGateway.addressFamily": "the address family of the default gateway the peer is discovered by: ipv4 or ipv6",
		"bgpInstances[].peers[].peerConfigRef.name":                         "the name of the CiliumBGPPeerConfig the peer is configured by",
	}, ciliumSelectorRequired("nodeSelector")),
	build: func(name, _ string, spec *ciliumv2.CiliumBGPClusterConfigSpec) client.Object {
		config := kurecilium.CreateCiliumBGPClusterConfig(name)
		spec.DeepCopyInto(&config.Spec)
		return config
	},
}

// ToApplicationConfig decodes an OAM cilium-bgpclusterconfig component into
// its config. The build namespace is not used: the object is cluster-scoped.
func (h *CiliumBGPClusterConfigHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumBGPClusterConfigKind.config(component)
}
