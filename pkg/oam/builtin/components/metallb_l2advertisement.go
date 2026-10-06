package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBL2AdvertisementHandler handles OAM metallb-l2advertisement
// components: the kind-named projection of a metallb.io/v1beta1
// L2Advertisement (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta1.L2AdvertisementSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the L2Advertisement, named after the component
// unless `objectName` names it, in the build namespace, and nothing else. The
// API requires no top-level field, and each one narrows the advertisement: a
// component that authors none limits it to no pool, node, interface or
// Service. TestCoreKindSchemas_CoverSpec keeps the published key set equal to
// the upstream json tags.
type MetalLBL2AdvertisementHandler struct{}

// CanHandle returns true for the metallb-l2advertisement component type.
func (h *MetalLBL2AdvertisementHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-l2advertisement"
}

// PropertySchema declares every top-level metallbv1beta1.L2AdvertisementSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *MetalLBL2AdvertisementHandler) PropertySchema() map[string]oam.PropertySchema {
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
		"ipAddressPools":         names("L2Advertisement spec.ipAddressPools: the pools whose addresses this advertisement announces on the local network, by name. With no pool named here and none selected by ipAddressPoolSelectors, it announces every pool.", "The name of an IPAddressPool in the same namespace."),
		"ipAddressPoolSelectors": selectors("L2Advertisement spec.ipAddressPoolSelectors: the pools whose addresses this advertisement announces, by their labels. With no pool selected here and none named by ipAddressPools, it announces every pool."),
		"nodeSelectors":          selectors("L2Advertisement spec.nodeSelectors: limits the nodes announced as next hops for the addresses. Unset, the advertisement excludes no node."),
		"interfaces":             names("L2Advertisement spec.interfaces: the host interfaces the addresses are announced from. Unset, every interface of the host.", "The name of a network interface of the node."),
		"serviceSelectors":       selectors("L2Advertisement spec.serviceSelectors: the Services whose addresses are announced. Unset, every Service that has an address of the pools."),
	}
}

// metallbL2AdvertisementKind is the metallb-l2advertisement kind: see
// policyFreeKind. The API requires no top-level field of the spec, only the
// key and the operator of a selector's match expression, and the CRD has no
// default and no expression rule.
var metallbL2AdvertisementKind = &policyFreeKind[metallbv1beta1.L2AdvertisementSpec]{
	upstream: "metallb.io/v1beta1 L2AdvertisementSpec",
	required: metallbSelectorsRequired("ipAddressPoolSelectors", "nodeSelectors", "serviceSelectors"),
	build: func(name, namespace string, spec *metallbv1beta1.L2AdvertisementSpec) client.Object {
		advertisement := kuremetallb.CreateL2Advertisement(name, namespace)
		spec.DeepCopyInto(&advertisement.Spec)
		return advertisement
	},
}

// ToApplicationConfig decodes an OAM metallb-l2advertisement component into
// its config. The object lands in the application's namespace at Generate.
func (h *MetalLBL2AdvertisementHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbL2AdvertisementKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBL2AdvertisementHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-l2advertisement")
}

// ComponentObject declares the metallb-l2advertisement kind's L2Advertisement.
func (h *MetalLBL2AdvertisementHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("L2Advertisement"), oam.ObjectScopeNamespaced
}
