package components

import (
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/kubernetes/externalsecrets"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ClusterSecretStoreHandler handles OAM clustersecretstore components: the
// kind-named projection of an external-secrets.io/v1 ClusterSecretStore, which
// is cluster-scoped (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of esv1.SecretStoreSpec,
// which a ClusterSecretStore shares with a SecretStore, under their json
// names, decoded strictly (decodeKindSpec). It emits the ClusterSecretStore,
// named after the component unless `objectName` names it, with no namespace,
// and nothing else: no Secret or ServiceAccount a provider names is created.
// Such a reference on a cluster-scoped store names its own namespace, which is
// a field of the reference and the author's to write.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ClusterSecretStoreHandler struct{}

// CanHandle returns true for the clustersecretstore component type.
func (h *ClusterSecretStoreHandler) CanHandle(componentType string) bool {
	return componentType == "clustersecretstore"
}

// PropertySchema declares every top-level esv1.SecretStoreSpec field by its
// json name. Structured fields are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *ClusterSecretStoreHandler) PropertySchema() map[string]oam.PropertySchema {
	return secretStoreSchema("ClusterSecretStore")
}

// clusterSecretStoreKind is the clustersecretstore kind: secretStoreKind,
// building the cluster-scoped object.
var clusterSecretStoreKind = &policyHeldKind[esv1.SecretStoreSpec]{
	policyFreeKind: policyFreeKind[esv1.SecretStoreSpec]{
		upstream:       "external-secrets.io/v1 SecretStoreSpec",
		required:       secretStoreRequired,
		defaultedZeros: secretStoreDefaultedZeros,
		validate:       validateSecretStore,
		build: func(name, _ string, spec *esv1.SecretStoreSpec) client.Object {
			store := externalsecrets.CreateClusterSecretStore(name)
			spec.DeepCopyInto(&store.Spec)
			return store
		},
	},
	enforce: enforceSecretStorePolicy,
}

// ToApplicationConfig decodes an OAM clustersecretstore component into its
// config. The object is cluster-scoped and takes no namespace.
func (h *ClusterSecretStoreHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return clusterSecretStoreKind.config(component)
}
