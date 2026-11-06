package components

import (
	"maps"

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

// The keys a webservice and a worker declare besides the claims'.
const (
	outputKeyDeployment     = "deployment"
	outputKeyService        = "service"
	outputKeyServiceAccount = "serviceaccount"
	// outputKeyClaimPrefix starts the key of a generated claim: claim_<volume>.
	outputKeyClaimPrefix = "claim_"
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

// memberOutput declares the object of the member a role rule emits for comp,
// named as the rule names it (nameRoleMember): key, role and the property that
// names it.
func memberOutput(comp *oam.Component, lctx oam.LoweringContext, key, typ string, handler oam.ComponentObjectProvider, role oam.NameRole, property, object string) (oam.OutputDecl, error) {
	member := &oam.Component{Name: comp.Name, Type: typ}
	if err := nameRoleMember(comp, lctx, member, handler, role, property, object); err != nil {
		return oam.OutputDecl{}, err
	}
	kind, _ := handler.ComponentObject()
	return oam.OutputDecl{Key: key, Role: role, Kind: kind, Default: comp.Name, Name: member.ObjectName()}, nil
}

// roleOutputs declares what a role component (webservice, worker) generates, in
// the order its rule names it: one claim per `pvc` volume that references none
// (claim_<volume>), the Deployment, the Service when service is set, and the
// ServiceAccount unless serviceAccountName names an existing one.
func roleOutputs(comp *oam.Component, lctx oam.LoweringContext, service bool) ([]oam.OutputDecl, error) {
	props := maps.Clone(comp.Properties)
	decls, err := roleClaimOutputs(comp, props, lctx)
	if err != nil {
		return nil, err
	}
	deployment, err := memberOutput(comp, lctx, outputKeyDeployment, "deployment", &DeploymentHandler{}, oam.NameRoleWorkloadDeployment, deploymentObjectNameProperty, "Deployment")
	if err != nil {
		return nil, err
	}
	decls = append(decls, deployment)
	if service {
		svc, err := memberOutput(comp, lctx, outputKeyService, "service", &ServiceHandler{}, oam.NameRoleWorkloadService, serviceObjectNameProperty, "Service")
		if err != nil {
			return nil, err
		}
		decls = append(decls, svc)
	}
	sa, err := roleServiceAccount(comp, props, comp.Traits, lctx)
	if err != nil {
		return nil, err
	}
	if sa != nil {
		kind, _ := (&ServiceAccountHandler{}).ComponentObject()
		decls = append(decls, oam.OutputDecl{
			Key: outputKeyServiceAccount, Role: oam.NameRoleWorkloadServiceAccount, Kind: kind,
			Default: comp.Name, Name: sa.ObjectName(),
		})
	}
	return decls, nil
}

// Outputs implements oam.OutputProvider for a webservice: its Deployment under
// "deployment", its Service under "service", its ServiceAccount under
// "serviceaccount" unless serviceAccountName names an existing one, and each
// claim a `pvc` volume generates under "claim_<volume>".
func (r WebserviceRule) Outputs(comp *oam.Component, lctx oam.LoweringContext) ([]oam.OutputDecl, error) {
	if _, err := parseWebservice(comp); err != nil {
		return nil, err
	}
	return roleOutputs(comp, lctx, true)
}

// Outputs implements oam.OutputProvider for a worker: a webservice's, without
// the Service.
func (r WorkerRule) Outputs(comp *oam.Component, lctx oam.LoweringContext) ([]oam.OutputDecl, error) {
	if _, err := parseWorker(comp.Name, comp.Properties); err != nil {
		return nil, err
	}
	return roleOutputs(comp, lctx, false)
}
