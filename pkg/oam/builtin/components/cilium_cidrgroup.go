package components

import (
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumCIDRGroupHandler handles OAM cilium-cidrgroup components: the
// kind-named projection of a cilium.io/v2 CiliumCIDRGroup
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumCIDRGroupSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the CiliumCIDRGroup, named after the component
// unless `objectName` names it, and nothing else. The object is
// cluster-scoped: it carries no namespace, whatever namespace the application
// is built for. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type CiliumCIDRGroupHandler struct{}

// CanHandle returns true for the cilium-cidrgroup component type.
func (h *CiliumCIDRGroupHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-cidrgroup"
}

// PropertySchema declares every top-level ciliumv2.CiliumCIDRGroupSpec field
// by its json name.
func (h *CiliumCIDRGroupHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"externalCIDRs": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumCIDRGroup spec.externalCIDRs: the CIDRs of the group, which select peers outside the cluster. A Cilium network policy refers to the group by its name. An empty list is a group that selects no peer.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An IP block in CIDR notation, e.g. 192.0.2.0/24 or 2001:db8::/32."},
		},
	}
}

// ciliumCIDRGroupKind is the cilium-cidrgroup kind: see policyFreeKind. The
// CRD has no expression rule; the form of a CIDR is left to the API server.
var ciliumCIDRGroupKind = &policyFreeKind[ciliumv2.CiliumCIDRGroupSpec]{
	upstream: "cilium.io/v2 CiliumCIDRGroupSpec",
	required: map[string]string{
		"externalCIDRs": "the CIDRs of the group; an empty list is a group that selects no peer",
	},
	build: func(name, _ string, spec *ciliumv2.CiliumCIDRGroupSpec) client.Object {
		group := kurecilium.CreateCiliumCIDRGroup(name)
		spec.DeepCopyInto(&group.Spec)
		return group
	},
}

// ToApplicationConfig decodes an OAM cilium-cidrgroup component into its
// config. The build namespace is not used: the object is cluster-scoped.
func (h *CiliumCIDRGroupHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumCIDRGroupKind.config(component)
}
