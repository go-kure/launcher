package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// IngressClassHandler handles OAM ingressclass components: the kind-named
// projection of a networking.k8s.io/v1 IngressClass (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// networkingv1.IngressClassSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the IngressClass, named after the component unless
// `objectName` names it, and nothing else. An IngressClass is cluster-scoped:
// the object carries no namespace, whatever namespace the application is built
// for.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type IngressClassHandler struct{}

// CanHandle returns true for the ingressclass component type.
func (h *IngressClassHandler) CanHandle(componentType string) bool {
	return componentType == "ingressclass"
}

// PropertySchema declares every top-level networkingv1.IngressClassSpec field
// by its json name. parameters is an open object whose content is checked by
// the strict decode, not by this schema.
func (h *IngressClassHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"controller": {
			Type:        oam.PropertyTypeString,
			Description: "IngressClass spec.controller: the controller that handles Ingresses of this class, a domain-prefixed path such as k8s.io/ingress-nginx. Immutable once created.",
		},
		"parameters": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "IngressClass spec.parameters: the object that holds the controller's configuration for this class: kind and name (both required by the API), apiGroup, scope (Cluster or Namespace; the API server defaults it to Cluster) and, for the Namespace scope, namespace. Decoded strictly into the Kubernetes API type: see IngressClassParametersReference in the Kubernetes API reference.",
		},
	}
}

// ingressClassKind is the ingressclass kind: see policyFreeKind. The API
// requires no top-level field of the spec; the value rules inside parameters
// are left to the API server.
var ingressClassKind = &policyFreeKind[networkingv1.IngressClassSpec]{
	upstream: "networking.k8s.io/v1 IngressClassSpec",
	build: func(name, _ string, spec *networkingv1.IngressClassSpec) client.Object {
		ic := kubernetes.CreateIngressClass(name)
		spec.DeepCopyInto(&ic.Spec)
		return ic
	},
}

// ToApplicationConfig decodes an OAM ingressclass component into its config.
// The build namespace is not used: an IngressClass is cluster-scoped.
func (h *IngressClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return ingressClassKind.config(component)
}
