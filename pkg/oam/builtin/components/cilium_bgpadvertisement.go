package components

import (
	"fmt"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	kurecilium "github.com/go-kure/kure/pkg/kubernetes/cilium"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// CiliumBGPAdvertisementHandler handles OAM cilium-bgpadvertisement
// components: the kind-named projection of a cilium.io/v2
// CiliumBGPAdvertisement (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// ciliumv2.CiliumBGPAdvertisementSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the CiliumBGPAdvertisement, named after
// the component unless `objectName` names it, and nothing else. The object is
// cluster-scoped: it carries no namespace, whatever namespace the application
// is built for. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type CiliumBGPAdvertisementHandler struct{}

// CanHandle returns true for the cilium-bgpadvertisement component type.
func (h *CiliumBGPAdvertisementHandler) CanHandle(componentType string) bool {
	return componentType == "cilium-bgpadvertisement"
}

// PropertySchema declares every top-level ciliumv2.CiliumBGPAdvertisementSpec
// field by its json name. An entry is an open object whose content is checked
// by the strict decode, not by this schema.
func (h *CiliumBGPAdvertisementHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"advertisements": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. CiliumBGPAdvertisement spec.advertisements: what is advertised to the peers whose CiliumBGPPeerConfig selects this object by its labels.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One advertisement: advertisementType (required: PodCIDR, CiliumPodIPPool, Service or Interface), service (addresses and the aggregation lengths; required with Service and refused with another type), interface (name; required with Interface and refused with another type), selector (the label query over the pools or Services advertised; refused with PodCIDR) and attributes (communities, localPreference). Decoded strictly into Cilium's API type: see BGPAdvertisement in its API reference.",
			},
		},
	}
}

// ciliumBGPAdvertisementKind is the cilium-bgpadvertisement kind: see
// policyFreeKind. validate holds the five expression rules of the CRD, each
// one comparison of an entry's advertisementType with the presence of one
// sibling. The API's other value rules are left to the API server.
var ciliumBGPAdvertisementKind = &policyFreeKind[ciliumv2.CiliumBGPAdvertisementSpec]{
	upstream: "cilium.io/v2 CiliumBGPAdvertisementSpec",
	required: requiredFields(map[string]string{
		"advertisements":                     "the list of what is advertised",
		"advertisements[].advertisementType": "what the entry advertises: PodCIDR, CiliumPodIPPool, Service or Interface",
	}, ciliumSelectorRequired("advertisements[].selector")),
	validate: validateCiliumBGPAdvertisements,
	build: func(name, _ string, spec *ciliumv2.CiliumBGPAdvertisementSpec) client.Object {
		advertisement := kurecilium.CreateCiliumBGPAdvertisement(name)
		spec.DeepCopyInto(&advertisement.Spec)
		return advertisement
	},
}

// validateCiliumBGPAdvertisements refuses an entry the CRD's expression rules
// refuse: a Service entry without `service` and another with it, an Interface
// entry without `interface` and another with it, and a PodCIDR entry with a
// `selector`. Presence is the decoded pointer's, which is what the emitted
// object shows: an authored null is an unset field, an authored {} a set one.
func validateCiliumBGPAdvertisements(spec *ciliumv2.CiliumBGPAdvertisementSpec) error {
	for i, entry := range spec.Advertisements {
		typ := entry.AdvertisementType
		refuse := func(field, rule string) error {
			return errors.Errorf("advertisements[%d].%s: %s", i, field, fmt.Sprintf(rule, typ))
		}
		switch {
		case typ == ciliumv2.BGPServiceAdvert && entry.Service == nil:
			return refuse("service", "required with advertisementType %q")
		case typ != ciliumv2.BGPServiceAdvert && entry.Service != nil:
			return refuse("service", "not allowed with advertisementType %q, only with \"Service\"")
		case typ == ciliumv2.BGPInterfaceAdvert && entry.Interface == nil:
			return refuse("interface", "required with advertisementType %q")
		case typ != ciliumv2.BGPInterfaceAdvert && entry.Interface != nil:
			return refuse("interface", "not allowed with advertisementType %q, only with \"Interface\"")
		case typ == ciliumv2.BGPPodCIDRAdvert && entry.Selector != nil:
			return refuse("selector", "not allowed with advertisementType %q")
		}
	}
	return nil
}

// ToApplicationConfig decodes an OAM cilium-bgpadvertisement component into
// its config. The build namespace is not used: the object is cluster-scoped.
func (h *CiliumBGPAdvertisementHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ciliumBGPAdvertisementKind.config(component)
}
