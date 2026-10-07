package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBBGPAdvertisementHandler handles OAM metallb-bgpadvertisement
// components: the kind-named projection of a metallb.io/v1beta1
// BGPAdvertisement (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta1.BGPAdvertisementSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the BGPAdvertisement, named after the
// component unless `objectName` names it, in the build namespace, and nothing
// else. The API requires no top-level field, and the fields that name or
// select something narrow the advertisement: a component that authors none
// is the widest advertisement, of every pool, to every peer, for every
// Service, with no node excluded. TestCoreKindSchemas_CoverSpec
// keeps the published key set equal to the upstream json tags.
type MetalLBBGPAdvertisementHandler struct{}

// CanHandle returns true for the metallb-bgpadvertisement component type.
func (h *MetalLBBGPAdvertisementHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-bgpadvertisement"
}

// PropertySchema declares every top-level metallbv1beta1.BGPAdvertisementSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *MetalLBBGPAdvertisementHandler) PropertySchema() map[string]oam.PropertySchema {
	selectors := func(description string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, Description: description,
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "A Kubernetes label selector: matchLabels and matchExpressions.",
			},
		}
	}
	names := func(description, item string) oam.PropertySchema {
		return oam.PropertySchema{
			Type: oam.PropertyTypeArray, Description: description,
			Items: &oam.PropertySchema{Type: oam.PropertyTypeString, Description: item},
		}
	}
	return map[string]oam.PropertySchema{
		"aggregationLength": {
			Type:        oam.PropertyTypeInteger,
			Description: "BGPAdvertisement spec.aggregationLength: the length of the IPv4 prefix the addresses are rolled up into before they are announced. Unset, the API fills 32, one route per address. Another value is refused with serviceSelectors.",
		},
		"aggregationLengthV6": {
			Type:        oam.PropertyTypeInteger,
			Description: "BGPAdvertisement spec.aggregationLengthV6: the length of the IPv6 prefix the addresses are rolled up into before they are announced. Unset, the API fills 128, one route per address. Another value is refused with serviceSelectors.",
		},
		"localPref": {
			Type:        oam.PropertyTypeInteger,
			Description: "BGPAdvertisement spec.localPref: the BGP LOCAL_PREF attribute of the announcement; a path with a higher one is preferred. Unset and 0 are the same advertisement: MetalLB's type holds one value for both, and the field is left out. What MetalLB then sends is its BGP backend's choice: at MetalLB v0.16.1 the native backend sends LOCAL_PREF 0 on an iBGP session; the FRR backends set no local preference.",
		},
		"communities":            names("BGPAdvertisement spec.communities: the BGP communities attached to the announcement.", "A standard community (1234:1234), a large one (large:1234:1234:1234) or the name of an alias a MetalLB Community object defines."),
		"ipAddressPools":         names("BGPAdvertisement spec.ipAddressPools: the pools whose addresses this advertisement announces over BGP, by name. With no pool named here and none selected by ipAddressPoolSelectors, it applies to every pool.", "The name of an IPAddressPool in the same namespace."),
		"ipAddressPoolSelectors": selectors("BGPAdvertisement spec.ipAddressPoolSelectors: the pools whose addresses this advertisement announces, by their labels. With no pool selected here and none named by ipAddressPools, it applies to every pool."),
		"nodeSelectors":          selectors("BGPAdvertisement spec.nodeSelectors: limits the nodes announced as next hops for the addresses. Unset, the advertisement excludes no node."),
		"peers":                  names("BGPAdvertisement spec.peers: limits the BGP peers the addresses are announced to. Unset, they are announced to every BGPPeer MetalLB is configured with.", "The name of a BGPPeer."),
		"serviceSelectors":       selectors("BGPAdvertisement spec.serviceSelectors: the Services whose addresses are announced. Unset, every Service that has an address of the pools. Refused with an aggregationLength other than 32 or an aggregationLengthV6 other than 128."),
	}
}

// metallbBGPAdvertisementKind is the metallb-bgpadvertisement kind: see
// policyFreeKind. The API requires no top-level field of the spec, only the
// key and the operator of a selector's match expression. validate holds the
// CRD's one expression rule; its other value rules (the minimum of
// aggregationLength) are left to the API server.
var metallbBGPAdvertisementKind = &policyFreeKind[metallbv1beta1.BGPAdvertisementSpec]{
	upstream: "metallb.io/v1beta1 BGPAdvertisementSpec",
	required: metallbSelectorsRequired("ipAddressPoolSelectors", "nodeSelectors", "serviceSelectors"),
	validate: validateMetalLBBGPAdvertisement,
	build: func(name, namespace string, spec *metallbv1beta1.BGPAdvertisementSpec) client.Object {
		advertisement := kuremetallb.CreateBGPAdvertisement(name, namespace)
		spec.DeepCopyInto(&advertisement.Spec)
		return advertisement
	},
}

// The two aggregation lengths the CRD's expression rule allows beside a
// service selector: one route per address, which are also the values the CRD
// fills where a length is not authored. The rule names both numbers itself,
// and TestMetalLBKinds_ExpressionRules holds this kind to its text and to the
// API server's answer, so a change of either in the linked module fails there.
const (
	metallbPerAddressAggregationLength   int32 = 32
	metallbPerAddressAggregationLengthV6 int32 = 128
)

// validateMetalLBBGPAdvertisement refuses a service selector beside an
// aggregation length that rolls addresses up, the CRD's expression rule on the
// spec. The API server evaluates the rule after it has filled the defaults,
// which are the two lengths the rule allows: an unauthored length keeps the
// rule, as one authored at that value does. An empty list of selectors is none.
func validateMetalLBBGPAdvertisement(spec *metallbv1beta1.BGPAdvertisementSpec) error {
	if len(spec.ServiceSelectors) == 0 {
		return nil
	}
	const only = "the API takes a service selector only with one route per address (aggregationLength 32, aggregationLengthV6 128, the values it fills where they are not set)"
	if length := spec.AggregationLength; length != nil && *length != metallbPerAddressAggregationLength {
		return errors.Errorf("serviceSelectors: not allowed with aggregationLength %d: %s", *length, only)
	}
	if length := spec.AggregationLengthV6; length != nil && *length != metallbPerAddressAggregationLengthV6 {
		return errors.Errorf("serviceSelectors: not allowed with aggregationLengthV6 %d: %s", *length, only)
	}
	return nil
}

// ToApplicationConfig decodes an OAM metallb-bgpadvertisement component into
// its config. The object lands in the application's namespace at Generate.
func (h *MetalLBBGPAdvertisementHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbBGPAdvertisementKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBBGPAdvertisementHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-bgpadvertisement")
}

// ComponentObject declares the metallb-bgpadvertisement kind's BGPAdvertisement.
func (h *MetalLBBGPAdvertisementHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("BGPAdvertisement"), oam.ObjectScopeNamespaced
}
