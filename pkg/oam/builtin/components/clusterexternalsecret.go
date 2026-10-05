package components

import (
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	"github.com/go-kure/kure/pkg/kubernetes/externalsecrets"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ClusterExternalSecretHandler handles OAM clusterexternalsecret components:
// the kind-named projection of an external-secrets.io/v1
// ClusterExternalSecret, which is cluster-scoped (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// esv1.ClusterExternalSecretSpec, under their json names, decoded strictly
// (decodeKindSpec). It emits the ClusterExternalSecret, named after the
// component unless `objectName` names it, with no namespace, and nothing else:
// the ExternalSecrets are the operator's to create, in the namespaces the
// object selects. TestCoreKindSchemas_CoverSpec keeps the published key set
// equal to the upstream json tags.
type ClusterExternalSecretHandler struct{}

// CanHandle returns true for the clusterexternalsecret component type.
func (h *ClusterExternalSecretHandler) CanHandle(componentType string) bool {
	return componentType == "clusterexternalsecret"
}

// PropertySchema declares every top-level esv1.ClusterExternalSecretSpec field
// by its json name. Structured fields are open objects whose content is
// checked by the strict decode, not by this schema.
func (h *ClusterExternalSecretHandler) PropertySchema() map[string]oam.PropertySchema {
	return clusterExternalSecretSchema()
}

// clusterExternalSecretKind is the clusterexternalsecret kind: see
// policyHeldKind. validate refuses the one authored value the object cannot
// carry (refuseDataGeneratorRef); enforce, a target.manifest of a kind the
// environment policy checks, in the spec of the ExternalSecrets it creates
// (enforceTargetManifest). The API's other rules are left to the API server.
var clusterExternalSecretKind = &policyHeldKind[esv1.ClusterExternalSecretSpec]{
	policyFreeKind: policyFreeKind[esv1.ClusterExternalSecretSpec]{
		upstream: "external-secrets.io/v1 ClusterExternalSecretSpec",
		required: clusterExternalSecretRequired,
		validate: validateClusterExternalSecret,
		build: func(name, _ string, spec *esv1.ClusterExternalSecretSpec) client.Object {
			secret := externalsecrets.CreateClusterExternalSecret(name)
			spec.DeepCopyInto(&secret.Spec)
			return secret
		},
	},
	enforce: enforceClusterExternalSecretPolicy,
}

// ToApplicationConfig decodes an OAM clusterexternalsecret component into its
// config. The object is cluster-scoped and takes no namespace.
func (h *ClusterExternalSecretHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return clusterExternalSecretKind.config(component)
}
