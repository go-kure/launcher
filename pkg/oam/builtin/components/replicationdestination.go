package components

import (
	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	kurevol "github.com/go-kure/kure/pkg/kubernetes/volsync"
	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// ReplicationDestinationHandler handles OAM replicationdestination
// components: the kind-named projection of a volsync.backube/v1alpha1
// ReplicationDestination (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of
// volsyncv1alpha1.ReplicationDestinationSpec, under their json names, decoded
// strictly (decodeKindSpec). It emits the ReplicationDestination, named after
// the component unless `objectName` names it, in the build namespace, and
// nothing else: no claim a mover's `destinationPVC` names and no Secret a
// mover names is created. It does not read the cluster's volsync capability.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type ReplicationDestinationHandler struct{}

// CanHandle returns true for the replicationdestination component type.
func (h *ReplicationDestinationHandler) CanHandle(componentType string) bool {
	return componentType == "replicationdestination"
}

// PropertySchema declares every top-level ReplicationDestinationSpec field by
// its json name. Each mover is an open object whose content is checked by the
// strict decode, not by this schema.
func (h *ReplicationDestinationHandler) PropertySchema() map[string]oam.PropertySchema {
	return volsyncSchema("ReplicationDestination", "sizes the volume the data is received into")
}

// replicationDestinationKind is the replicationdestination kind: see
// policyHeldKind. The API requires no field at the top level of the spec; of
// a mover that is authored it requires the fields
// replicationDestinationRequired lists, each of which the type would write
// empty. The policy reaches what sizes the mover and its volumes
// (enforceVolsyncMovers).
var replicationDestinationKind = &policyHeldKind[volsyncv1alpha1.ReplicationDestinationSpec]{
	policyFreeKind: policyFreeKind[volsyncv1alpha1.ReplicationDestinationSpec]{
		upstream: "volsync.backube/v1alpha1 ReplicationDestinationSpec",
		required: replicationDestinationRequired,
		validate: func(spec *volsyncv1alpha1.ReplicationDestinationSpec) error {
			return validateVolsyncMovers(replicationDestinationMovers(spec))
		},
		build: func(name, namespace string, spec *volsyncv1alpha1.ReplicationDestinationSpec) client.Object {
			destination := kurevol.CreateReplicationDestination(name, namespace)
			spec.DeepCopyInto(&destination.Spec)
			return destination
		},
	},
	enforce: func(spec *volsyncv1alpha1.ReplicationDestinationSpec, p oam.Policy) error {
		return enforceVolsyncMovers(replicationDestinationMovers(spec), p)
	},
}

// ToApplicationConfig decodes an OAM replicationdestination component into
// its config. The object takes the namespace of the application it is
// generated in.
func (h *ReplicationDestinationHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return replicationDestinationKind.config(component)
}
