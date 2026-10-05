package components

import (
	certv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-kure/kure/pkg/kubernetes/certmanager"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// IssuerHandler handles OAM issuer components: the kind-named projection of a
// cert-manager.io/v1 Issuer (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of certv1.IssuerSpec, under
// their json names, decoded strictly (decodeKindSpec). It emits the Issuer,
// named after the component unless `objectName` names it, in the build
// namespace, and nothing else: no Secret an issuer type names is created.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type IssuerHandler struct{}

// CanHandle returns true for the issuer component type.
func (h *IssuerHandler) CanHandle(componentType string) bool {
	return componentType == "issuer"
}

// PropertySchema declares every top-level certv1.IssuerSpec field by its json
// name: one per issuer type. Each is an open object whose content is checked
// by the strict decode, not by this schema.
func (h *IssuerHandler) PropertySchema() map[string]oam.PropertySchema {
	return issuerSchema("Issuer")
}

// issuerKind is the issuer kind: see policyHeldKind. The API requires no field
// of the spec; of an issuer type that is authored it requires the fields
// issuerRequired lists, each of which the type would write empty. The API's
// value rules and cert-manager's webhook are left to them
// (certmanager_common.go). The policy reaches the pod template of an ACME
// HTTP01 solver (enforceIssuerPolicy).
var issuerKind = &policyHeldKind[certv1.IssuerSpec]{
	policyFreeKind: policyFreeKind[certv1.IssuerSpec]{
		upstream: "cert-manager.io/v1 IssuerSpec",
		required: issuerRequired,
		build: func(name, namespace string, spec *certv1.IssuerSpec) client.Object {
			issuer := certmanager.CreateIssuer(name, namespace)
			spec.DeepCopyInto(&issuer.Spec)
			return issuer
		},
	},
	enforce: enforceIssuerPolicy,
}

// ToApplicationConfig decodes an OAM issuer component into its config. The
// object takes the namespace of the application it is generated in.
func (h *IssuerHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return issuerKind.config(component)
}
