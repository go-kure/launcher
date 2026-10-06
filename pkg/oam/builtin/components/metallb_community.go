package components

import (
	kuremetallb "github.com/go-kure/kure/pkg/kubernetes/metallb"
	"github.com/go-kure/kure/pkg/stack"
	metallbv1beta1 "go.universe.tf/metallb/api/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// MetalLBCommunityHandler handles OAM metallb-community components: the
// kind-named projection of a metallb.io/v1beta1 Community
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// metallbv1beta1.CommunitySpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the Community, named after the component unless
// `objectName` names it, in the build namespace, and nothing else. The API
// requires no field. A Community gives names to BGP community values, and a
// MetalLB BGPAdvertisement may attach a community by such a name.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type MetalLBCommunityHandler struct{}

// CanHandle returns true for the metallb-community component type.
func (h *MetalLBCommunityHandler) CanHandle(componentType string) bool {
	return componentType == "metallb-community"
}

// PropertySchema declares every top-level metallbv1beta1.CommunitySpec field
// by its json name. The aliases are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *MetalLBCommunityHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"communities": {
			Type:        oam.PropertyTypeArray,
			Description: "Community spec.communities: the aliases this object defines, each a name for one BGP community value.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "An alias: name, and value, a standard community (1234:1234) or a large one (large:1234:1234:1234).",
			},
		},
	}
}

// metallbCommunityKind is the metallb-community kind: see policyFreeKind. The
// API requires no field, of the spec or of an alias, and the CRD has no default
// and no expression rule. The name and the value of an alias are strings the
// upstream type omits when empty, so an alias authored with an empty one is
// written without it. The form of a value is not checked.
var metallbCommunityKind = &policyFreeKind[metallbv1beta1.CommunitySpec]{
	upstream: "metallb.io/v1beta1 CommunitySpec",
	build: func(name, namespace string, spec *metallbv1beta1.CommunitySpec) client.Object {
		community := kuremetallb.CreateCommunity(name, namespace)
		spec.DeepCopyInto(&community.Spec)
		return community
	},
}

// ToApplicationConfig decodes an OAM metallb-community component into its
// config. The object lands in the application's namespace at Generate.
func (h *MetalLBCommunityHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return metallbCommunityKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *MetalLBCommunityHandler) ContractMetadata() oam.ContractMetadata {
	return contract("metallb-community")
}

// ComponentObject declares the metallb-community kind's Community.
func (h *MetalLBCommunityHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return metallbKind("Community"), oam.ObjectScopeNamespaced
}
