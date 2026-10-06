package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta2 "go.universe.tf/metallb/api/v1beta2"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBBGPPeerHandler handles OAM metallb-bgppeer components: the kind-named
// projection of a metallb.io/v1beta2 BGPPeer (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta2.BGPPeerSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the BGPPeer, named after the component unless
// `objectName` names it, in the build namespace, and nothing else: no Secret
// for `passwordSecret`, no BFDProfile for `bfdProfile`. A peer is a router
// MetalLB opens a BGP session with, and every MetalLB BGPAdvertisement that
// names no peer announces to it. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
//
// The other kinds of MetalLB's API are metallb.io/v1beta1 objects. The
// module's BGPPeer CRD serves v1beta1 too, deprecated, and stores v1beta2,
// which is the version the base library's constructor builds.
type MetalLBBGPPeerHandler struct{}

// CanHandle returns true for the metallb-bgppeer component type.
func (h *MetalLBBGPPeerHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-bgppeer"
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBBGPPeerHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-bgppeer")
}

// ComponentObject declares the metallb-bgppeer kind's BGPPeer.
func (h *MetalLBBGPPeerHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("BGPPeer"), oam.ObjectScopeNamespaced
}

// PropertySchema declares every top-level metallbv1beta2.BGPPeerSpec field by
// its json name. Structured fields are open objects whose content is checked
// by the strict decode, not by this schema.
func (h *MetalLBBGPPeerHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "BGPPeer spec."
	const sameAsUnset = " Unset and 0 are the same peer: MetalLB's type holds one value for both, and the field is left out."
	const duration = ", as a Go duration (\"90s\"). The object carries it in Go's own spelling (1m30s)."
	text := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeString, Description: spec + description}
	}
	number := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeInteger, Description: spec + description}
	}
	flag := func(description string) oam.PropertySchema {
		return oam.PropertySchema{Type: oam.PropertyTypeBoolean, Description: spec + description}
	}
	return map[string]oam.PropertySchema{
		"myASN": {
			Type: oam.PropertyTypeInteger, Required: true,
			Description: "Required. " + spec + "myASN: the AS number of the local end of the session. An authored 0 is written.",
		},
		"peerASN":       number("peerASN: the AS number expected from the remote end of the session." + sameAsUnset + " Upstream states that this and dynamicASN are mutually exclusive and that one of them must be specified; the CRD's schema does not say so, and it is not checked here."),
		"localASN":      number("localASN: an AS number advertised to this peer in place of myASN, through BGP's local-as feature. Upstream states that the native BGP mode does not support it." + sameAsUnset),
		"dynamicASN":    text("dynamicASN: detects the AS number of the remote end instead of naming it in peerASN: internal (a neighbor whose AS number differs from myASN is denied) or external (one whose AS number is myASN is denied)."),
		"peerAddress":   text("peerAddress: the address to dial when establishing the session. Upstream states that this and interface are mutually exclusive and that one of them must be specified; the CRD's schema does not say so, and it is not checked here."),
		"interface":     text("interface: the node interface over which an unnumbered BGP peering is established. The API does not validate the name."),
		"sourceAddress": text("sourceAddress: the source address to use when establishing the session."),
		"peerPort":      number("peerPort: the port to dial when establishing the session, from 1 to 16384. Unset, the API fills 179. An authored 0 is refused: the type leaves a 0 out, and the API server would fill 179 in its place."),
		"holdTime":      text("holdTime: the requested BGP hold time, per RFC 4271" + duration),
		"keepaliveTime": text("keepaliveTime: the requested BGP keepalive time, per RFC 4271" + duration),
		"connectTime":   text("connectTime: the requested BGP connect time, how long BGP waits between connection attempts to a neighbor" + duration + " From 1 to 65535 seconds and a whole number of seconds, read as the CRD's two expression rules read it, in whole milliseconds: a part under a millisecond is not seen. Checked here as the API server evaluates them."),
		"routerID":      text("routerID: the BGP router ID to advertise to the peer."),
		"nodeSelectors": {
			Type:        oam.PropertyTypeArray,
			Description: spec + "nodeSelectors: only the nodes that match one of these selectors connect to this peer.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "A Kubernetes label selector: matchLabels and matchExpressions.",
			},
		},
		"password": text("password: the authentication password for routers that enforce TCP MD5 authenticated sessions, written into the object. Refused under an EnvironmentPolicy that forbids explicit secrets: name a Secret in passwordSecret instead."),
		"passwordSecret": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "passwordSecret: the Secret that holds the session password, by name and namespace. Upstream states that it must be of type kubernetes.io/basic-auth, in the namespace of the MetalLB deployment, with the password under the key password. Launcher does not create it and does not check that it exists. Unset, the object carries an empty reference, which MetalLB reads as none.",
		},
		"bfdProfile":             text("bfdProfile: the name of the BFD profile used for the BFD session of this BGP session. Unset, no BFD session is set up."),
		"enableGracefulRestart":  flag("enableGracefulRestart: lets the peer keep forwarding packets along known routes while the routing protocol information is restored. Upstream states that the FRR-based modes alone support it. The CRD refuses a change of it on an existing object, which a build does not see."),
		"ebgpMultiHop":           flag("ebgpMultiHop: says the peer is several hops away. Upstream states that the FRR-based modes alone need it."),
		"vrf":                    text("vrf: the host VRF to which the interface the session uses belongs."),
		"disableMP":              flag("disableMP: disables MP BGP, so that IPv4 and IPv6 routes are exchanged in separate BGP sessions. Deprecated upstream in favour of dualStackAddressFamily."),
		"dualStackAddressFamily": flag("dualStackAddressFamily: enables the neighbor for the other IP family too, so that IPv4 prefixes may be advertised and received over an IPv6 session and the reverse."),
	}
}

// metallbBGPPeerDefaultedZeros is the peer's defaulted-zero list for
// refuseUncarriedSpecValues: the one number of metallbv1beta2.BGPPeerSpec whose
// encoding omits a 0 and to which the CRD gives another default, so that the
// API server would replace the authored value. TestMetalLBKinds_NoDefaultIsLost
// holds the list to the CRD's defaults, in both directions.
var metallbBGPPeerDefaultedZeros = defaultedZeroFields{
	api:       "MetalLB",
	defaulter: "API server",
	fields:    map[string]string{"peerPort": "179"},
}

// metallbBGPPeerKind is the metallb-bgppeer kind: see policyHeldKind. The API
// requires `myASN`, which the type would write as 0, and the key and the
// operator of a node selector's match expression. validate holds the CRD's two
// expression rules on `connectTime`; the third rule the CRD declares, that
// `enableGracefulRestart` does not change, compares with the stored object and
// is the API server's, as are the CRD's value rules (the bounds of the AS
// numbers and of `peerPort`, the two values of `dynamicASN`). The policy
// reaches the session password written into the object
// (enforceMetalLBBGPPeerPolicy).
var metallbBGPPeerKind = &policyHeldKind[metallbv1beta2.BGPPeerSpec]{
	policyFreeKind: policyFreeKind[metallbv1beta2.BGPPeerSpec]{
		upstream: "metallb.io/v1beta2 BGPPeerSpec",
		required: requiredFields(
			map[string]string{"myASN": "the API requires it: the AS number of the local end of the session"},
			metallbSelectorsRequired("nodeSelectors"),
		),
		defaultedZeros: metallbBGPPeerDefaultedZeros,
		validate:       validateMetalLBBGPPeer,
		build: func(name, namespace string, spec *metallbv1beta2.BGPPeerSpec) client.Object {
			peer := kuremetallb.CreateBGPPeer(name, namespace)
			spec.DeepCopyInto(&peer.Spec)
			return peer
		},
	},
	enforce: enforceMetalLBBGPPeerPolicy,
}

// The bounds, in seconds, the CRD's first expression rule on connectTime
// names. TestMetalLBKinds_ExpressionRules holds this kind to the rule's text
// and to the API server's answer, so a change of either in the linked module
// fails there.
const (
	metallbConnectTimeMinSeconds int64 = 1
	metallbConnectTimeMaxSeconds int64 = 65535
)

// validateMetalLBBGPPeer refuses a connect time the CRD's two expression rules
// refuse: one outside 1 to 65535 seconds, and one that is no whole number of
// seconds. Both are computed as the API server computes them: the seconds of a
// duration are its whole seconds and its milliseconds its whole milliseconds,
// each with the rest dropped, so that 65535.5s is in range and no whole
// number, and a part under a millisecond is not seen by either rule.
func validateMetalLBBGPPeer(spec *metallbv1beta2.BGPPeerSpec) error {
	if spec.ConnectTime == nil {
		return nil
	}
	d := spec.ConnectTime.Duration
	if seconds := int64(d.Seconds()); seconds < metallbConnectTimeMinSeconds || seconds > metallbConnectTimeMaxSeconds {
		return errors.Errorf("connectTime: %s is not between %d and %d seconds, which the API requires", d, metallbConnectTimeMinSeconds, metallbConnectTimeMaxSeconds)
	}
	if d.Milliseconds()%1000 != 0 {
		return errors.Errorf("connectTime: %s is not a whole number of seconds, which the API requires", d)
	}
	return nil
}

// enforceMetalLBBGPPeerPolicy holds a BGPPeer's spec to the environment
// policy: under one that forbids explicit secrets (oam.ExplicitSecretPolicy)
// the session password written into the object is refused, since the object,
// and with it the password, is in the build's output. The message quotes
// nothing of the value. A policy that does not implement that interface allows
// it. An empty password is none: the type leaves it out of the object.
func enforceMetalLBBGPPeerPolicy(spec *metallbv1beta2.BGPPeerSpec, p oam.Policy) error {
	if oam.ExplicitSecretsAllowed(p) || spec.Password == "" {
		return nil
	}
	return oam.NewPolicyRefusal(oam.RefusalExplicitSecret, "password: holds the BGP session password in the object, and the environment policy forbids explicit secrets; name a Secret created out of band in passwordSecret instead")
}

// ToApplicationConfig decodes an OAM metallb-bgppeer component into its
// config. The object lands in the application's namespace at Generate.
func (h *MetalLBBGPPeerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbBGPPeerKind.config(component)
}
