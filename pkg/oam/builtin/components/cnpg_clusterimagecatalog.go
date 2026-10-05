package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	kurecnpg "github.com/go-kure/kure/pkg/kubernetes/cnpg"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CnpgClusterImageCatalogHandler handles OAM cnpg-clusterimagecatalog
// components: the kind-named projection of a CloudNativePG
// postgresql.cnpg.io/v1 ClusterImageCatalog (go-kure/launcher#790), an image
// catalog the Clusters of every namespace may refer to.
//
// Its properties are exactly the top-level fields of cnpgv1.ImageCatalogSpec,
// the spec type the ClusterImageCatalog shares with the ImageCatalog, under
// their json names, decoded strictly (decodeKindSpec). It emits the
// ClusterImageCatalog, which is cluster-scoped: named after the component
// unless `objectName` names it, with no namespace, and nothing else.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CnpgClusterImageCatalogHandler struct{}

// CanHandle returns true for the cnpg-clusterimagecatalog component type.
func (h *CnpgClusterImageCatalogHandler) CanHandle(componentType string) bool {
	return componentType == "cnpg-clusterimagecatalog"
}

// PropertySchema declares every top-level cnpgv1.ImageCatalogSpec field by its
// json name, as cnpg-imagecatalog does.
func (h *CnpgClusterImageCatalogHandler) PropertySchema() map[string]oam.PropertySchema {
	return cnpgImageCatalogSchema("ClusterImageCatalog")
}

// cnpgClusterImageCatalogKind is the cnpg-clusterimagecatalog kind: what
// cnpgImageCatalogKind is, for the cluster-scoped object.
var cnpgClusterImageCatalogKind = &policyHeldKind[cnpgv1.ImageCatalogSpec]{
	policyFreeKind: policyFreeKind[cnpgv1.ImageCatalogSpec]{
		upstream: "postgresql.cnpg.io/v1 ImageCatalogSpec",
		required: cnpgImageCatalogRequired,
		validate: validateCnpgImageCatalog,
		build: func(name, _ string, spec *cnpgv1.ImageCatalogSpec) client.Object {
			catalog := kurecnpg.CreateClusterImageCatalog(name)
			spec.DeepCopyInto(&catalog.Spec)
			return catalog
		},
	},
	enforce: enforceCnpgImageCatalogPolicy,
}

// ToApplicationConfig decodes an OAM cnpg-clusterimagecatalog component into
// its config. The object is cluster-scoped and takes no namespace.
func (h *CnpgClusterImageCatalogHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return cnpgClusterImageCatalogKind.config(component)
}

// ContractMetadata implements oam.ContractDescriber.
func (h *CnpgClusterImageCatalogHandler) ContractMetadata() oam.ContractMetadata {
	return contract("cnpg-clusterimagecatalog")
}

// ComponentObject declares the cnpg-clusterimagecatalog kind's
// ClusterImageCatalog, which is cluster-scoped.
func (h *CnpgClusterImageCatalogHandler) ComponentObject() (schema.GroupKind, oam.ObjectScope) {
	return cnpgKind(cnpgv1.ClusterImageCatalogKind), oam.ObjectScopeCluster
}
