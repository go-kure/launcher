package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ServiceCIDRHandler handles OAM servicecidr components: the kind-named
// projection of a networking.k8s.io/v1 ServiceCIDR (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// networkingv1.ServiceCIDRSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ServiceCIDR, named after the component unless
// `objectName` names it, and nothing else. A ServiceCIDR is cluster-scoped:
// the object carries no namespace, whatever namespace the application is built
// for. TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ServiceCIDRHandler struct{}

// CanHandle returns true for the servicecidr component type.
func (h *ServiceCIDRHandler) CanHandle(componentType string) bool {
	return componentType == "servicecidr"
}

// PropertySchema declares every top-level networkingv1.ServiceCIDRSpec field
// by its json name.
func (h *ServiceCIDRHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"cidrs": {
			Type: oam.PropertyTypeArray, Required: true,
			Description: "Required. ServiceCIDR spec.cidrs: the IP blocks, in CIDR notation, the API server assigns Service cluster IPs from: one, or two of different IP families. The API refuses a change to a block once the object exists; a single block may gain a second.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An IP block in CIDR notation, e.g. 10.96.0.0/16 or fd00:10:96::/112."},
		},
	}
}

// serviceCIDRKind is the servicecidr kind: see policyFreeKind. The API
// requires at least one of cidrs: the Go type omits an empty list, and the API
// server refuses a ServiceCIDR without one. The API's other value rules, the
// limit of two blocks included, are left to the API server.
var serviceCIDRKind = &policyFreeKind[networkingv1.ServiceCIDRSpec]{
	upstream: "networking.k8s.io/v1 ServiceCIDRSpec",
	validate: func(spec *networkingv1.ServiceCIDRSpec) error {
		if len(spec.CIDRs) == 0 {
			return errors.New("cidrs: required (at least one IP block, in CIDR notation, to assign Service cluster IPs from)")
		}
		return nil
	},
	build: func(name, _ string, spec *networkingv1.ServiceCIDRSpec) client.Object {
		cidr := kubernetes.CreateServiceCIDR(name)
		spec.DeepCopyInto(&cidr.Spec)
		return cidr
	},
}

// ToApplicationConfig decodes an OAM servicecidr component into its config.
// The build namespace is not used: a ServiceCIDR is cluster-scoped.
func (h *ServiceCIDRHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return serviceCIDRKind.config(component)
}
