package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumLoadBalancerIPPoolHandler handles OAM cilium-loadbalancerippool
// components: the kind-named projection of a cilium.io/v2
// CiliumLoadBalancerIPPool (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumLoadBalancerIPPoolSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumLoadBalancerIPPool, named
// after the component unless `objectName` names it, and nothing else. The
// object is cluster-scoped: it carries no namespace, whatever namespace the
// application is built for. `serviceSelector` is the author's.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CiliumLoadBalancerIPPoolHandler struct{}

// CanHandle returns true for the cilium-loadbalancerippool component type.
func (h *CiliumLoadBalancerIPPoolHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-loadbalancerippool"
}

// PropertySchema declares every top-level
// ciliumv2.CiliumLoadBalancerIPPoolSpec field by its json name. Structured
// fields are open objects whose content is checked by the strict decode, not
// by this schema.
func (h *CiliumLoadBalancerIPPoolHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"serviceSelector": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "CiliumLoadBalancerIPPool spec.serviceSelector: the label query over the Services that may receive an address from this pool (matchLabels, matchExpressions). Unset, every Service. Decoded strictly into Cilium's API type.",
		},
		"allowFirstLastIPs": {
			Type:        oam.PropertyTypeString,
			Description: "CiliumLoadBalancerIPPool spec.allowFirstLastIPs: Yes or No. Yes or unset, the first and the last address of each CIDR are allocatable; No reserves them.",
		},
		"blocks": {
			Type:        oam.PropertyTypeArray,
			Description: "CiliumLoadBalancerIPPool spec.blocks: the address blocks of the pool.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One block: a cidr, or a start address with an optional stop address. Decoded strictly into Cilium's API type: see CiliumLoadBalancerIPPoolIPBlock in its API reference.",
			},
		},
		"disabled": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CiliumLoadBalancerIPPool spec.disabled: true, no new address is allocated from this pool; the allocations made stay. Unset or false, the pool allocates: the API's default is false.",
		},
	}
}

// ciliumLoadBalancerIPPoolKind is the cilium-loadbalancerippool kind: see
// policyFreeKind. The CRD has no expression rule; its value rules (the two
// values of allowFirstLastIPs, the form of a block) are left to the API
// server.
var ciliumLoadBalancerIPPoolKind = &policyFreeKind[ciliumv2.CiliumLoadBalancerIPPoolSpec]{
	upstream: "cilium.io/v2 CiliumLoadBalancerIPPoolSpec",
	required: ciliumSelectorRequired("serviceSelector"),
	build: func(name, _ string, spec *ciliumv2.CiliumLoadBalancerIPPoolSpec) client.Object {
		pool := kurecilium.CreateCiliumLoadBalancerIPPool(name)
		spec.DeepCopyInto(&pool.Spec)
		return pool
	},
}

// ToApplicationConfig decodes an OAM cilium-loadbalancerippool component into
// its config. The build namespace is not used: the object is cluster-scoped.
func (h *CiliumLoadBalancerIPPoolHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumLoadBalancerIPPoolKind.config(component)
}
