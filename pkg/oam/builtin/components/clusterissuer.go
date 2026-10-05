package components

import (
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-kure/kure/pkg/kubernetes/certmanager"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ClusterIssuerHandler handles OAM clusterissuer components: the kind-named
// projection of a cert-manager.io/v1 ClusterIssuer, which is cluster-scoped
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of certv1.IssuerSpec, which
// a ClusterIssuer shares with an Issuer, under their json names, decoded
// strictly (decodeKindSpec). It emits the ClusterIssuer, named after the
// component unless `objectName` names it, with no namespace, and nothing else:
// no Secret an issuer type names is created. cert-manager reads those Secrets
// from its cluster resource namespace, which is a flag of its controller and
// not a field of the object. TestCoreKindSchemas_CoverSpec keeps the published
// key set equal to the upstream json tags.
type ClusterIssuerHandler struct{}

// CanHandle returns true for the clusterissuer component type.
func (h *ClusterIssuerHandler) CanHandle(componentType string) bool {
	return componentType == "clusterissuer"
}

// PropertySchema declares every top-level certv1.IssuerSpec field by its json
// name: one per issuer type. Each is an open object whose content is checked
// by the strict decode, not by this schema.
func (h *ClusterIssuerHandler) PropertySchema() map[string]oam.PropertySchema {
	return issuerSchema("ClusterIssuer")
}

// clusterIssuerKind is the clusterissuer kind: issuerKind, building the
// cluster-scoped object.
var clusterIssuerKind = &policyHeldKind[certv1.IssuerSpec]{
	policyFreeKind: policyFreeKind[certv1.IssuerSpec]{
		upstream: "cert-manager.io/v1 IssuerSpec",
		required: issuerRequired,
		build: func(name, _ string, spec *certv1.IssuerSpec) client.Object {
			issuer := certmanager.CreateClusterIssuer(name)
			spec.DeepCopyInto(&issuer.Spec)
			return issuer
		},
	},
	enforce: enforceIssuerPolicy,
}

// ToApplicationConfig decodes an OAM clusterissuer component into its config.
// The object is cluster-scoped and takes no namespace.
func (h *ClusterIssuerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return clusterIssuerKind.config(component)
}
