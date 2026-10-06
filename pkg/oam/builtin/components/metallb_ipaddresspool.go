package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBIPAddressPoolHandler handles OAM metallb-ipaddresspool components:
// the kind-named projection of a metallb.io/v1beta1 IPAddressPool
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta1.IPAddressPoolSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the IPAddressPool, named after the component
// unless `objectName` names it, in the build namespace, and nothing else. The
// addresses and the selectors under `serviceAllocation` are the author's:
// launcher reads neither. TestCoreKindSchemas_CoverSpec keeps the published
// key set equal to the upstream json tags.
type MetalLBIPAddressPoolHandler struct{}

// CanHandle returns true for the metallb-ipaddresspool component type.
func (h *MetalLBIPAddressPoolHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-ipaddresspool"
}

// PropertySchema declares every top-level metallbv1beta1.IPAddressPoolSpec
// field by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *MetalLBIPAddressPoolHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"addresses": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. IPAddressPool spec.addresses: the address ranges MetalLB may hand to the LoadBalancer Services of the cluster. All ranges of one pool share its settings. An empty list is a pool with no address.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A CIDR prefix, e.g. 192.0.2.0/24, or a first and a last address, e.g. 192.0.2.10-192.0.2.20."},
		},
		"autoAssign": {
			Type:        oam.PropertyTypeBoolean,
			Description: "IPAddressPool spec.autoAssign: false, MetalLB does not allocate from this pool on its own. Unset or true, it does: the API's default is true.",
		},
		"avoidBuggyIPs": {
			Type:        oam.PropertyTypeBoolean,
			Description: "IPAddressPool spec.avoidBuggyIPs: true, the addresses ending in .0 and .255 are not handed out. Unset or false, they are: the API's default is false.",
		},
		"serviceAllocation": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "IPAddressPool spec.serviceAllocation: which Services may take an address of this pool: priority, namespaces, namespaceSelectors and serviceSelectors. Unset, every Service of every namespace. Decoded strictly into MetalLB's API type: see ServiceAllocation in its API reference.",
		},
	}
}

// metallbIPAddressPoolKind is the metallb-ipaddresspool kind: see
// policyFreeKind. The CRD has no expression rule, and an address range is not
// read: its form is MetalLB's to refuse.
var metallbIPAddressPoolKind = &policyFreeKind[metallbv1beta1.IPAddressPoolSpec]{
	upstream: "metallb.io/v1beta1 IPAddressPoolSpec",
	required: requiredFields(map[string]string{
		"addresses": "the address ranges MetalLB may hand to Services; an empty list is a pool with no address",
	}, metallbSelectorsRequired("serviceAllocation.namespaceSelectors", "serviceAllocation.serviceSelectors")),
	build: func(name, namespace string, spec *metallbv1beta1.IPAddressPoolSpec) client.Object {
		pool := kuremetallb.CreateIPAddressPool(name, namespace)
		spec.DeepCopyInto(&pool.Spec)
		return pool
	},
}

// ToApplicationConfig decodes an OAM metallb-ipaddresspool component into its
// config. The object lands in the application's namespace at Generate.
func (h *MetalLBIPAddressPoolHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbIPAddressPoolKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBIPAddressPoolHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-ipaddresspool")
}

// ComponentObject declares the metallb-ipaddresspool kind's IPAddressPool.
func (h *MetalLBIPAddressPoolHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("IPAddressPool"), oam.ObjectScopeNamespaced
}
