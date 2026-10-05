package components

import (
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/kubernetes/externalsecrets"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// SecretStoreHandler handles OAM secretstore components: the kind-named
// projection of an external-secrets.io/v1 SecretStore (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of esv1.SecretStoreSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// SecretStore, named after the component unless `objectName` names it, in the
// build namespace, and nothing else: no Secret or ServiceAccount a provider
// names is created. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type SecretStoreHandler struct{}

// CanHandle returns true for the secretstore component type.
func (h *SecretStoreHandler) CanHandle(componentType string) bool {
	return componentType == "secretstore"
}

// PropertySchema declares every top-level esv1.SecretStoreSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *SecretStoreHandler) PropertySchema() map[string]oam.PropertySchema {
	return secretStoreSchema("SecretStore")
}

// secretStoreKind is the secretstore kind: see policyHeldKind. validate holds
// the API's rule that a store configures exactly one provider; enforce, the
// environment policy's on a credential written into the object. The API's
// other rules are left to the API server.
var secretStoreKind = &policyHeldKind[esv1.SecretStoreSpec]{
	policyFreeKind: policyFreeKind[esv1.SecretStoreSpec]{
		upstream:       "external-secrets.io/v1 SecretStoreSpec",
		required:       secretStoreRequired,
		defaultedZeros: secretStoreDefaultedZeros,
		validate:       validateSecretStore,
		build: func(name, namespace string, spec *esv1.SecretStoreSpec) client.Object {
			store := externalsecrets.CreateSecretStore(name, namespace)
			spec.DeepCopyInto(&store.Spec)
			return store
		},
	},
	enforce: enforceSecretStorePolicy,
}

// ToApplicationConfig decodes an OAM secretstore component into its config.
// The namespace is taken from the application at Generate time.
func (h *SecretStoreHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return secretStoreKind.config(component)
}
