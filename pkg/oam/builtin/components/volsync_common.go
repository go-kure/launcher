package components

import (
	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// This file holds what the kind components of VolSync's volsync.backube/v1alpha1
// API share (go-kure/launcher#790): replicationsource and
// replicationdestination. Each is a policyHeldKind: the object runs no pod and
// holds no image, but its mover is the pod the operator starts for a
// synchronization, and the object sizes the volumes the operator provisions
// for it. Three things an author writes there are held to the environment
// policy (enforceVolsyncMovers): a capacity, the mover's cpu and memory, and
// the mover's pod-level hostProcess switch.
//
// Not held, since the object does not hold it or the policy has no dimension
// for it: a capacity the author left out, which is then the operator's to
// choose; the rest of moverSecurityContext, a pod security context of which
// only windowsOptions.hostProcess is held (the user and groups the mover runs
// as, its sysctls, its SELinux and seccomp settings), the policy having no
// dimension for it; the service account the mover runs under
// (moverServiceAccount), an identity carried as authored; the mover's
// affinity and the volumes mounted into it (moverVolumes: a Secret, a claim
// or an NFS export, never a host path); the type of the Service a mover is
// reached through; and whether the mover runs with elevated permissions at
// all, which is an annotation an administrator puts on the namespace
// (volsync.backube/privileged-movers), not a field of the object.
//
// The module ships its CRDs. TestVolsyncKinds_RequiredMatchCRD holds each
// kind's required list to them, TestVolsyncKinds_NoDefaults the claim that
// they default nothing under spec, so that no authored 0 or false is lost on
// these types, and TestVolsyncKinds_NoExpressionRules the claim that they
// declare no expression rule for a kind to check.
//
// Two fields the linked Kubernetes type holds under a mounted Secret are no
// property of those CRDs, since the linked Kubernetes API is newer than the
// one they were generated from: defaultUser and items[].user. Each is refused
// when authored (refuseVolsyncAbsentFields).
//
// The operator refuses, when it reconciles, an object that configures no
// mover or more than one. That rule is the operator's, and launcher does not
// repeat it. The parameters of an external provider (external.parameters) are
// the provider's and are not checked.
//
// A host these objects name is one the mover reaches, not an artifact source:
// the remote side of an rsync connection, a Syncthing peer, the server of an
// NFS export among moverVolumes. None is held to the environment policy's
// allowed registries.

// volsyncMover is what the two kinds read of one authored mover.
type volsyncMover struct {
	// name is the mover's property ("restic").
	name string
	// capacities are the mover's fields that size a volume the operator
	// provisions.
	capacities []volsyncCapacity
	// resources is the mover container's cpu and memory.
	resources *corev1.ResourceRequirements
	// config is what the mover shares with the others of its family. Nil for
	// rsync over SSH, whose type has neither a security context, an affinity
	// nor volumes.
	config *volsyncv1alpha1.MoverConfig
}

// volsyncCapacity is one capacity field of a mover; quantity is nil when it
// was not authored.
type volsyncCapacity struct {
	field    string
	quantity *resource.Quantity
}

// replicationSourceMovers returns the movers a ReplicationSource's spec
// authors, in the order of the type's fields. On a source, capacity sizes the
// point-in-time copy of the volume, cacheCapacity restic's metadata cache and
// configCapacity Syncthing's configuration volume.
func replicationSourceMovers(spec *volsyncv1alpha1.ReplicationSourceSpec) []volsyncMover {
	var movers []volsyncMover
	if m := spec.Rsync; m != nil {
		movers = append(movers, volsyncMover{
			name: "rsync", capacities: []volsyncCapacity{{"capacity", m.Capacity}}, resources: m.MoverResources,
		})
	}
	if m := spec.RsyncTLS; m != nil {
		movers = append(movers, volsyncMover{
			name: "rsyncTLS", capacities: []volsyncCapacity{{"capacity", m.Capacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	if m := spec.Rclone; m != nil {
		movers = append(movers, volsyncMover{
			name: "rclone", capacities: []volsyncCapacity{{"capacity", m.Capacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	if m := spec.Restic; m != nil {
		movers = append(movers, volsyncMover{
			name: "restic", capacities: []volsyncCapacity{{"capacity", m.Capacity}, {"cacheCapacity", m.CacheCapacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	if m := spec.Syncthing; m != nil {
		movers = append(movers, volsyncMover{
			name: "syncthing", capacities: []volsyncCapacity{{"configCapacity", m.ConfigCapacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	return movers
}

// replicationDestinationMovers returns the movers a ReplicationDestination's
// spec authors, in the order of the type's fields. On a destination, capacity
// sizes the volume the data is received into and cacheCapacity restic's
// metadata cache.
func replicationDestinationMovers(spec *volsyncv1alpha1.ReplicationDestinationSpec) []volsyncMover {
	var movers []volsyncMover
	if m := spec.Rsync; m != nil {
		movers = append(movers, volsyncMover{
			name: "rsync", capacities: []volsyncCapacity{{"capacity", m.Capacity}}, resources: m.MoverResources,
		})
	}
	if m := spec.RsyncTLS; m != nil {
		movers = append(movers, volsyncMover{
			name: "rsyncTLS", capacities: []volsyncCapacity{{"capacity", m.Capacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	if m := spec.Rclone; m != nil {
		movers = append(movers, volsyncMover{
			name: "rclone", capacities: []volsyncCapacity{{"capacity", m.Capacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	if m := spec.Restic; m != nil {
		movers = append(movers, volsyncMover{
			name: "restic", capacities: []volsyncCapacity{{"capacity", m.Capacity}, {"cacheCapacity", m.CacheCapacity}},
			resources: m.MoverResources, config: &m.MoverConfig,
		})
	}
	return movers
}

// volsyncMoverRequired is the required list of the movers named, each of
// which shares MoverConfig: of a volume mounted into the mover that is
// authored, the API requires where it is mounted and what is mounted, and the
// Go type writes both whether or not they were authored.
func volsyncMoverRequired(movers ...string) map[string]string {
	out := make(map[string]string, 2*len(movers))
	for _, mover := range movers {
		out[mover+".moverVolumes[].mountPath"] = "the path under /mnt the volume is mounted at in the mover pod"
		out[mover+".moverVolumes[].volumeSource"] = "what is mounted: a secret, an nfs export or a persistentVolumeClaim"
	}
	return out
}

// replicationSourceRequired is the required list of the replicationsource
// kind: the fields the ReplicationSource CRD requires that the Go types write
// whether or not they were authored. None is at the top level.
// TestVolsyncKinds_RequiredMatchCRD holds the list to the CRD.
var replicationSourceRequired = requiredFields(
	volsyncMoverRequired("rclone", "restic", "rsyncTLS", "syncthing"),
	map[string]string{
		"syncthing.peers[].address":    "the address the Syncthing node connects to the peer at",
		"syncthing.peers[].ID":         "the peer's Syncthing device ID",
		"syncthing.peers[].introducer": "whether the peer introduces this node to the other peers that share the volume; no default is filled",
	},
)

// replicationDestinationRequired is the required list of the
// replicationdestination kind, as replicationSourceRequired is the source's.
// A destination has no Syncthing mover.
var replicationDestinationRequired = volsyncMoverRequired("rclone", "restic", "rsyncTLS")

// refuseVolsyncNullNodeSelectorTerms refuses a mover whose affinity authors a
// required node affinity without its terms: corev1.NodeSelector writes
// nodeSelectorTerms: null, which the CRDs require and the API server drops
// before it validates. TestKindComponents_NullRequired shows the refusal on
// the linked CRDs.
func refuseVolsyncNullNodeSelectorTerms(movers []volsyncMover) error {
	for _, mover := range movers {
		if mover.config == nil || mover.config.MoverAffinity == nil {
			continue
		}
		if err := refuseNullNodeSelectorTerms(mover.name+".moverAffinity.nodeAffinity", mover.config.MoverAffinity.NodeAffinity); err != nil {
			return err
		}
	}
	return nil
}

// volsyncAbsent is what the refusal of a field the CRDs do not hold says.
const volsyncAbsent = "no field of the volsync.backube/v1alpha1 API: its CRD has no such property"

// refuseVolsyncAbsentFields refuses the fields of a mounted Secret that the
// linked Kubernetes type holds and the CRDs do not: the owner UID of the files
// (defaultUser) and of one file (items[].user). The strict decode takes them,
// since it reads the Go type, and the object would carry a value the API it is
// written for has no place for: the API server names each as an unknown
// field, which refuses a request made with strict field validation, and
// prunes it. TestVolsyncKinds_AbsentFromCRD derives the fields from the linked
// CRDs, holds this function to them and shows that answer with the API
// server's own create sequence.
func refuseVolsyncAbsentFields(movers []volsyncMover) error {
	for _, mover := range movers {
		if mover.config == nil {
			continue
		}
		for i, volume := range mover.config.MoverVolumes {
			secret := volume.VolumeSource.Secret
			if secret == nil {
				continue
			}
			if secret.DefaultUser != nil {
				return errors.Errorf("%s.moverVolumes[%d].volumeSource.secret.defaultUser: %s", mover.name, i, volsyncAbsent)
			}
			for j, item := range secret.Items {
				if item.User != nil {
					return errors.Errorf("%s.moverVolumes[%d].volumeSource.secret.items[%d].user: %s", mover.name, i, j, volsyncAbsent)
				}
			}
		}
	}
	return nil
}

// validateVolsyncMovers is the two kinds' validate: what the CRDs refuse of an
// authored mover that the strict decode and the required list do not see.
func validateVolsyncMovers(movers []volsyncMover) error {
	if err := refuseVolsyncNullNodeSelectorTerms(movers); err != nil {
		return err
	}
	return refuseVolsyncAbsentFields(movers)
}

// enforceVolsyncMovers holds the authored movers to the environment policy:
//
//   - every authored capacity, which sizes a volume the operator provisions,
//     to the storage maximum;
//   - the cpu and memory limits and requests of moverResources to the maxima,
//     as a container's are. The request is not held to the limit: that is the
//     API server's on the pod the operator creates;
//   - moverSecurityContext.windowsOptions.hostProcess, the pod-level switch
//     that runs the mover's containers as Windows HostProcess containers, is
//     refused unless the policy allows privileged workloads.
//
// See the top of this file for what is not held.
func enforceVolsyncMovers(movers []volsyncMover, p oam.Policy) error {
	for _, mover := range movers {
		for _, capacity := range mover.capacities {
			if capacity.quantity == nil {
				continue
			}
			if err := enforceMaxStorageAt(capacity.quantity.String(), p.MaxStorageSize(), mover.name+"."+capacity.field); err != nil {
				return err
			}
		}
		if mover.resources != nil {
			if err := enforceMaxContainerResources(*mover.resources, p); err != nil {
				return errors.Wrap(err, mover.name+".moverResources")
			}
		}
		if mover.config == nil || p.AllowPrivileged() {
			continue
		}
		if sc := mover.config.MoverSecurityContext; sc != nil && sc.WindowsOptions != nil &&
			sc.WindowsOptions.HostProcess != nil && *sc.WindowsOptions.HostProcess {
			return oam.NewPolicyRefusal(oam.RefusalPrivileged, mover.name+".moverSecurityContext.windowsOptions.hostProcess is not allowed by environment policy")
		}
	}
	return nil
}

// volsyncSchema returns the properties the two kinds share: the top-level
// fields ReplicationSourceSpec and ReplicationDestinationSpec both hold. kind
// names the object in each description ("ReplicationSource"), and capacity
// says what a mover's capacity sizes on that object.
func volsyncSchema(kind, capacity string) map[string]oam.PropertySchema {
	spec := kind + " spec."
	const (
		decoded = " Decoded strictly into VolSync's API type: see "
		held    = " An authored capacity is held to the EnvironmentPolicy storage maximum, and the cpu and memory of moverResources to its maxima."
		config  = " moverSecurityContext.windowsOptions.hostProcess is refused unless the policy allows privileged workloads; of a moverVolumes entry, mountPath and volumeSource are required."
	)
	mover := func(name, what, typ string) oam.PropertySchema {
		rule := held
		if name != "rsync" {
			rule += config
		}
		return oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + name + ": " + what + " capacity " + capacity + "." + rule + decoded + typ + " in its API reference.",
		}
	}
	return map[string]oam.PropertySchema{
		"trigger": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "trigger: when a synchronization runs: schedule, a cron expression, or manual, a value the operator copies to status.lastManualSync once the synchronization it triggers is done." + decoded + kind + "TriggerSpec in its API reference.",
		},
		"rsync":    mover("rsync", "synchronize with rsync over SSH.", kind+"RsyncSpec"),
		"rsyncTLS": mover("rsyncTLS", "synchronize with rsync over TLS.", kind+"RsyncTLSSpec"),
		"rclone":   mover("rclone", "synchronize with Rclone; rcloneConfig names the Secret that holds its configuration.", kind+"RcloneSpec"),
		"restic":   mover("restic", "synchronize with Restic; repository names the Secret that holds the repository's location and credentials. cacheCapacity sizes the metadata cache volume and is held to the storage maximum too.", kind+"ResticSpec"),
		"external": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: spec + "external: hand the replication to an external provider: provider, its name in the form domain.com/provider, and parameters, a map of strings the provider reads, which launcher does not check." + decoded + kind + "ExternalSpec in its API reference.",
		},
		"paused": {
			Type:        oam.PropertyTypeBoolean,
			Description: spec + "paused: true stops replication until it is false again.",
		},
	}
}
