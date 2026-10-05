package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumBGPPeerConfigHandler handles OAM cilium-bgppeerconfig components: the
// kind-named projection of a cilium.io/v2 CiliumBGPPeerConfig
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumBGPPeerConfigSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the CiliumBGPPeerConfig, named after the
// component unless `objectName` names it, and nothing else. The object is
// cluster-scoped: it carries no namespace, whatever namespace the application
// is built for. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type CiliumBGPPeerConfigHandler struct{}

// CanHandle returns true for the cilium-bgppeerconfig component type.
func (h *CiliumBGPPeerConfigHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-bgppeerconfig"
}

// PropertySchema declares every top-level ciliumv2.CiliumBGPPeerConfigSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *CiliumBGPPeerConfigHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"transport": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumBGPPeerConfig spec.transport: the transport of the session: peerPort (unset, the API fills 179) and sourceInterface (the interface whose address is the session's source).",
		},
		"timers": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumBGPPeerConfig spec.timers: connectRetryTimeSeconds, holdTimeSeconds and keepAliveTimeSeconds; unset, the API fills 120, 90 and 30. keepAliveTimeSeconds may not be larger than holdTimeSeconds.",
		},
		"authSecretRef": {
			Type:        oam.PropertyTypeString,
			Description: "CiliumBGPPeerConfig spec.authSecretRef: the name of the Secret Cilium fetches the session's TCP authentication password from. A name, not the password; unset, the session is not authenticated.",
		},
		"gracefulRestart": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumBGPPeerConfig spec.gracefulRestart: enabled (required) and restartTimeSeconds (unset, the API fills 120).",
		},
		"ebgpMultihop": {
			Type:        oam.PropertyTypeInteger,
			Description: "CiliumBGPPeerConfig spec.ebgpMultihop: the time-to-live of the BGP packets sent to an eBGP peer, 1 to 255. Unset, the API fills 1. Not read for an iBGP peer.",
		},
		"families": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumBGPPeerConfig spec.families: the address families negotiated with the peer, each with the advertisements sent in it. Unset, Cilium negotiates IPv4 and IPv6 unicast.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One family: afi (required: ipv4, ipv6, l2vpn, ls or opaque), safi (required: unicast, multicast and the others the API lists) and advertisements, the label query over the CiliumBGPAdvertisement objects sent in this family (matchLabels, matchExpressions; unset, none is sent). Decoded strictly into Cilium's API type: see CiliumBGPFamilyWithAdverts in its API reference.",
			},
		},
	}
}

// ciliumBGPPeerConfigKind is the cilium-bgppeerconfig kind: see
// policyFreeKind. validate holds the CRD's one expression rule where both of
// its fields were authored. The API's other value rules are left to the API
// server.
var ciliumBGPPeerConfigKind = &policyFreeKind[ciliumv2.CiliumBGPPeerConfigSpec]{
	upstream: "cilium.io/v2 CiliumBGPPeerConfigSpec",
	required: requiredFields(map[string]string{
		"families[].afi":          "the address family identifier: ipv4, ipv6, l2vpn, ls or opaque",
		"families[].safi":         "the subsequent address family identifier, unicast for one",
		"gracefulRestart.enabled": "whether graceful restart is negotiated with the peer; no default is filled",
	}, ciliumSelectorRequired("families[].advertisements")),
	validate: validateCiliumBGPPeerConfig,
	build: func(name, _ string, spec *ciliumv2.CiliumBGPPeerConfigSpec) client.Object {
		config := kurecilium.CreateCiliumBGPPeerConfig(name)
		spec.DeepCopyInto(&config.Spec)
		return config
	},
}

// validateCiliumBGPPeerConfig refuses a keepalive time larger than the hold
// time, the CRD's expression rule on `timers`, where both were authored. With
// one of the two authored the API server compares it with the default it
// fills for the other, which is the installed CRD's to say; that comparison is
// left to it.
func validateCiliumBGPPeerConfig(spec *ciliumv2.CiliumBGPPeerConfigSpec) error {
	timers := spec.Timers
	if timers == nil || timers.KeepAliveTimeSeconds == nil || timers.HoldTimeSeconds == nil {
		return nil
	}
	if keepAlive, hold := *timers.KeepAliveTimeSeconds, *timers.HoldTimeSeconds; keepAlive > hold {
		return errors.Errorf("timers.keepAliveTimeSeconds: %d is larger than timers.holdTimeSeconds (%d)", keepAlive, hold)
	}
	return nil
}

// ToApplicationConfig decodes an OAM cilium-bgppeerconfig component into its
// config. The build namespace is not used: the object is cluster-scoped.
func (h *CiliumBGPPeerConfigHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumBGPPeerConfigKind.config(component)
}
