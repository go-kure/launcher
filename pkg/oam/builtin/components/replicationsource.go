package components

import (
	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	kurevol "github.com/go-kure/kure/pkg/kubernetes/volsync"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ReplicationSourceHandler handles OAM replicationsource components: the
// kind-named projection of a volsync.backube/v1alpha1 ReplicationSource
// (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// volsyncv1alpha1.ReplicationSourceSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the ReplicationSource, named after the
// component unless `objectName` names it, in the build namespace, and nothing
// else: no claim `sourcePVC` names and no Secret a mover names is created.
// The `volsync` trait is the ReplicationSource launcher derives for a
// workload's claim; this kind is the authored object, and it does not read
// the cluster's volsync capability. TestCoreKindSchemas_CoverSpec keeps the
// published key set equal to the upstream json tags.
type ReplicationSourceHandler struct{}

// CanHandle returns true for the replicationsource component type.
func (h *ReplicationSourceHandler) CanHandle(componentType string) bool {
	return componentType == "replicationsource"
}

// PropertySchema declares every top-level ReplicationSourceSpec field by its
// json name. Each mover is an open object whose content is checked by the
// strict decode, not by this schema.
func (h *ReplicationSourceHandler) PropertySchema() map[string]oam.PropertySchema {
	const spec = "ReplicationSource spec."
	schema := volsyncSchema("ReplicationSource", "overrides the size of the point-in-time copy of the volume")
	schema["sourcePVC"] = oam.PropertySchema{
		Type:        oam.PropertyTypeString,
		Description: spec + "sourcePVC: the name of the PersistentVolumeClaim to replicate, in the object's namespace.",
	}
	schema["syncthing"] = oam.PropertySchema{
		Type: oam.PropertyTypeObject, AdditionalProperties: true,
		Description: spec + "syncthing: synchronize with Syncthing peers; of each of peers, address, ID and introducer are required. configCapacity sizes the configuration volume and is held to the EnvironmentPolicy storage maximum, and the cpu and memory of moverResources to its maxima. moverSecurityContext.windowsOptions.hostProcess is refused unless the policy allows privileged workloads; of a moverVolumes entry, mountPath and volumeSource are required. Decoded strictly into VolSync's API type: see ReplicationSourceSyncthingSpec in its API reference.",
	}
	return schema
}

// replicationSourceKind is the replicationsource kind: see policyHeldKind.
// The API requires no field at the top level of the spec; of a mover that is
// authored it requires the fields replicationSourceRequired lists, each of
// which the type would write empty. The policy reaches what sizes the mover
// and its volumes (enforceVolsyncMovers).
var replicationSourceKind = &policyHeldKind[volsyncv1alpha1.ReplicationSourceSpec]{
	policyFreeKind: policyFreeKind[volsyncv1alpha1.ReplicationSourceSpec]{
		upstream: "volsync.backube/v1alpha1 ReplicationSourceSpec",
		required: replicationSourceRequired,
		validate: func(spec *volsyncv1alpha1.ReplicationSourceSpec) error {
			return validateVolsyncMovers(replicationSourceMovers(spec))
		},
		build: func(name, namespace string, spec *volsyncv1alpha1.ReplicationSourceSpec) client.Object {
			source := kurevol.CreateReplicationSource(name, namespace)
			spec.DeepCopyInto(&source.Spec)
			return source
		},
	},
	enforce: func(spec *volsyncv1alpha1.ReplicationSourceSpec, p oam.Policy) error {
		return enforceVolsyncMovers(replicationSourceMovers(spec), p)
	},
}

// ToApplicationConfig decodes an OAM replicationsource component into its
// config. The object takes the namespace of the application it is generated
// in.
func (h *ReplicationSourceHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return replicationSourceKind.config(component)
}
