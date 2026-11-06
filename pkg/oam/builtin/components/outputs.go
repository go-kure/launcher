package components

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/oam"
)

// The output keys the builtin components declare (oam.OutputProvider,
// go-kure/launcher#1259). A key is one identifier, addressed as
// parts.<part>.<key> in a document.
const (
	outputKeyObject      = "object"
	outputKeyCluster     = "cluster"
	outputKeyAppSecret   = "app_secret"
	outputKeyObjectStore = "objectstore"
)

// cnpgAppSecretSuffix is what CloudNativePG appends to the Cluster's name for
// the Secret it generates for the application user. The README says so
// (pkg/oam/README.md, the Cluster name warning).
const cnpgAppSecretSuffix = "-app"

// objectOutputs declares the one object of a kind component under the key
// "object": the name the transform gives it (Component.ObjectName), which is
// the author's `objectName`, else the consumer hook's, else the component name.
func objectOutputs(comp *oam.Component, handler oam.ComponentObjectProvider) []oam.OutputDecl {
	kind, _ := handler.ComponentObject()
	return []oam.OutputDecl{{Key: outputKeyObject, Role: oam.NameRoleObject, Kind: kind, Default: comp.Name, Name: comp.ObjectName()}}
}

// Outputs implements oam.OutputProvider: the ConfigMap, under "object".
func (h *ConfigMapHandler) Outputs(comp *oam.Component, _ oam.LoweringContext) ([]oam.OutputDecl, error) {
	return objectOutputs(comp, h), nil
}

// Outputs implements oam.OutputProvider: the Secret, under "object".
func (h *SecretHandler) Outputs(comp *oam.Component, _ oam.LoweringContext) ([]oam.OutputDecl, error) {
	return objectOutputs(comp, h), nil
}

// Outputs implements oam.OutputProvider for a postgresql component:
//
//   - "cluster": the Cluster, named by postgresqlClusterMember, the one place
//     LowerComponent names it from too.
//   - "app_secret": the Secret CloudNativePG generates for the application
//     user, <cluster>-app. It is declared when the operator generates it, and
//     not when recovery.secret.name is set: the operator then uses that Secret
//     and generates none. It is not in the built application, so it is derived
//     from the Cluster's name, which the build holds.
//   - "objectstore": the backup's ObjectStore, when backup is set, named by
//     postgresqlObjectStoreMember as LowerComponent names it.
func (r PostgresqlRule) Outputs(comp *oam.Component, lctx oam.LoweringContext) ([]oam.OutputDecl, error) {
	cluster, err := postgresqlClusterMember(lctx, comp)
	if err != nil {
		return nil, err
	}
	c, err := r.parse(comp)
	if err != nil {
		return nil, err
	}
	clusterKind, _ := (&CnpgClusterHandler{}).ComponentObject()
	decls := []oam.OutputDecl{{
		Key: outputKeyCluster, Role: oam.NameRolePostgresqlCluster, Kind: clusterKind,
		Default: comp.Name, Name: cluster.ObjectName(),
	}}
	if c.Recovery == nil || c.Recovery.Secret == "" {
		decls = append(decls, oam.OutputDecl{
			Key: outputKeyAppSecret, Kind: schema.GroupKind{Kind: "Secret"}, From: outputKeyCluster,
			Derive: func(clusterName string) string { return clusterName + cnpgAppSecretSuffix },
		})
	}
	if c.Backup != nil {
		store, err := postgresqlObjectStoreMember(comp, lctx, c.Backup)
		if err != nil {
			return nil, err
		}
		storeKind, _ := (&CnpgObjectStoreHandler{}).ComponentObject()
		decls = append(decls, oam.OutputDecl{
			Key: outputKeyObjectStore, Role: oam.NameRolePostgresqlObjectStore, Kind: storeKind,
			Default: comp.Name, Name: store.ObjectName(),
		})
	}
	return decls, nil
}
