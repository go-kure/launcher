package components

import (
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/kubernetes/externalsecrets"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ExternalSecretHandler handles OAM externalsecret components: the kind-named
// projection of an external-secrets.io/v1 ExternalSecret
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of esv1.ExternalSecretSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// ExternalSecret, named after the component unless `objectName` names it, in
// the build namespace, and nothing else: no store is created or looked up, and
// the store reference is the author's, where the `external-secret` trait can
// take its from the cluster profile. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type ExternalSecretHandler struct{}

// CanHandle returns true for the externalsecret component type.
func (h *ExternalSecretHandler) CanHandle(componentType string) bool {
	return componentType == "externalsecret"
}

// PropertySchema declares every top-level esv1.ExternalSecretSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *ExternalSecretHandler) PropertySchema() map[string]oam.PropertySchema {
	return externalSecretSchema()
}

// externalSecretKind is the externalsecret kind: see policyFreeKind. validate
// refuses the one authored value the object cannot carry
// (refuseDataGeneratorRef); the API's other rules are left to the API server.
var externalSecretKind = &policyFreeKind[esv1.ExternalSecretSpec]{
	upstream: "external-secrets.io/v1 ExternalSecretSpec",
	required: externalSecretRequired,
	validate: validateExternalSecret,
	build: func(name, namespace string, spec *esv1.ExternalSecretSpec) client.Object {
		secret := externalsecrets.CreateExternalSecret(name, namespace)
		spec.DeepCopyInto(&secret.Spec)
		return secret
	},
}

// ToApplicationConfig decodes an OAM externalsecret component into its config.
// The namespace is taken from the application at Generate time.
func (h *ExternalSecretHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return externalSecretKind.config(component)
}
