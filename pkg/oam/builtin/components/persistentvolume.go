package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// PersistentVolumeHandler handles OAM persistentvolume components: the
// kind-named projection of a v1 PersistentVolume (go-kure/launcher#790).
//
// Its properties are exactly the top-level json fields of
// corev1.PersistentVolumeSpec, decoded strictly (decodeKindSpec). The spec
// embeds corev1.PersistentVolumeSource, so each volume source (nfs, csi,
// hostPath, ...) is a top-level property, as it is a top-level field of the
// object's spec. It emits the PersistentVolume, named after the component, and
// nothing else. A PersistentVolume is cluster-scoped: the object carries no
// namespace, whatever namespace the application is built for.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags, the promoted ones included.
type PersistentVolumeHandler struct{}

// CanHandle returns true for the persistentvolume component type.
func (h *PersistentVolumeHandler) CanHandle(componentType string) bool {
	return componentType == "persistentvolume"
}

// persistentVolumeSources are the volume sources of
// corev1.PersistentVolumeSource, the type PersistentVolumeSpec embeds: the json
// name of each, what it names, and the API type it decodes into.
var persistentVolumeSources = []struct{ key, names, apiType string }{
	{"gcePersistentDisk", "a Google Compute Engine persistent disk", "GCEPersistentDiskVolumeSource"},
	{"awsElasticBlockStore", "an AWS Elastic Block Store volume", "AWSElasticBlockStoreVolumeSource"},
	{"hostPath", "a file or directory on the node. Refused unless the environment policy allows hostPath volumes", "HostPathVolumeSource"},
	{"glusterfs", "a Glusterfs volume", "GlusterfsPersistentVolumeSource"},
	{"nfs", "an NFS export", "NFSVolumeSource"},
	{"rbd", "a Ceph RADOS block device", "RBDPersistentVolumeSource"},
	{"iscsi", "an iSCSI target", "ISCSIPersistentVolumeSource"},
	{"cinder", "an OpenStack Cinder volume", "CinderPersistentVolumeSource"},
	{"cephfs", "a CephFS mount", "CephFSPersistentVolumeSource"},
	{"fc", "a Fibre Channel volume", "FCVolumeSource"},
	{"flocker", "a Flocker dataset", "FlockerVolumeSource"},
	{"flexVolume", "a volume served by a FlexVolume driver. Not checked against the environment policy", "FlexPersistentVolumeSource"},
	{"azureFile", "an Azure File share", "AzureFilePersistentVolumeSource"},
	{"vsphereVolume", "a vSphere virtual disk", "VsphereVirtualDiskVolumeSource"},
	{"quobyte", "a Quobyte volume", "QuobyteVolumeSource"},
	{"azureDisk", "an Azure data disk", "AzureDiskVolumeSource"},
	{"photonPersistentDisk", "a Photon Controller persistent disk", "PhotonPersistentDiskVolumeSource"},
	{"portworxVolume", "a Portworx volume", "PortworxVolumeSource"},
	{"scaleIO", "a ScaleIO volume", "ScaleIOPersistentVolumeSource"},
	{"local", "a disk, partition or directory on the node, used with nodeAffinity. Refused unless the environment policy allows hostPath volumes", "LocalVolumeSource"},
	{"storageos", "a StorageOS volume", "StorageOSPersistentVolumeSource"},
	{"csi", "a volume served by a CSI driver. Not checked against the environment policy", "CSIPersistentVolumeSource"},
}

// PropertySchema declares every top-level corev1.PersistentVolumeSpec field by
// its json name, the promoted volume sources included. Structured fields are
// open objects whose content is checked by the strict decode, not by this
// schema.
func (h *PersistentVolumeHandler) PropertySchema() map[string]oam.PropertySchema {
	schema := map[string]oam.PropertySchema{
		"capacity": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PersistentVolume spec.capacity: the volume's resources, each a quantity (storage: 10Gi). storage is held to the environment policy's storage maximum.",
		},
		"accessModes": {
			Type:        oam.PropertyTypeArray,
			Description: "PersistentVolume spec.accessModes: the ways the volume can be mounted.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "An access mode: ReadWriteOnce, ReadOnlyMany, ReadWriteMany or ReadWriteOncePod."},
		},
		"claimRef": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PersistentVolume spec.claimRef: the PersistentVolumeClaim this volume is reserved for (namespace, name). Decoded strictly into the Kubernetes API type: see ObjectReference in the Kubernetes API reference.",
		},
		"persistentVolumeReclaimPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "PersistentVolume spec.persistentVolumeReclaimPolicy: what happens to the volume when its claim is released: Retain, Delete or Recycle. The API server defaults it to Retain.",
		},
		"storageClassName": {
			Type:        oam.PropertyTypeString,
			Description: "PersistentVolume spec.storageClassName: the StorageClass this volume belongs to. Unset or empty, it belongs to no class.",
		},
		"mountOptions": {
			Type:        oam.PropertyTypeArray,
			Description: "PersistentVolume spec.mountOptions: mount options passed on as written; an invalid one fails the mount, not the build.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A mount option, e.g. ro or nfsvers=4.1."},
		},
		"volumeMode": {
			Type:        oam.PropertyTypeString,
			Description: "PersistentVolume spec.volumeMode: Filesystem or Block. The API server defaults it to Filesystem.",
		},
		"nodeAffinity": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PersistentVolume spec.nodeAffinity: the nodes the volume can be reached from (required.nodeSelectorTerms). Decoded strictly into the Kubernetes API type: see VolumeNodeAffinity in the Kubernetes API reference.",
		},
		"volumeAttributesClassName": {
			Type:        oam.PropertyTypeString,
			Description: "PersistentVolume spec.volumeAttributesClassName: the VolumeAttributesClass of a CSI volume.",
		},
	}
	for _, src := range persistentVolumeSources {
		schema[src.key] = oam.PropertySchema{
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "PersistentVolume spec." + src.key + ": the volume source, " + src.names +
				". Decoded strictly into the Kubernetes API type: see " + src.apiType + " in the Kubernetes API reference.",
		}
	}
	return schema
}

// ToApplicationConfig decodes an OAM persistentvolume component into a
// PersistentVolumeConfig, under the package's null contract and the strict
// decode every spec-projecting kind uses. The build namespace is not used: a
// PersistentVolume is cluster-scoped.
func (h *PersistentVolumeHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	spec, props, err := decodeKindSpec[corev1.PersistentVolumeSpec](component.Properties, "v1 PersistentVolumeSpec")
	if err != nil {
		return nil, err
	}
	if err := refuseUncarriedSpecValues(props, spec, defaultedZeroFields{}); err != nil {
		return nil, err
	}
	return &PersistentVolumeConfig{Name: component.Name, Spec: *spec}, nil
}

// PersistentVolumeConfig implements stack.ApplicationConfig for
// persistentvolume components. Spec is the decoded PersistentVolumeSpec exactly
// as authored.
type PersistentVolumeConfig struct {
	Name string
	Spec corev1.PersistentVolumeSpec
}

// ApplyPolicy holds the volume to the environment policy
// (enforcePersistentVolumePolicy): a hostPath or local source needs the
// policy to allow hostPath volumes, and capacity.storage is held to the storage
// maximum. A nil policy checks nothing.
func (c *PersistentVolumeConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil {
		return nil
	}
	return enforcePersistentVolumePolicy("", &c.Spec, p)
}

// Generate emits the PersistentVolume: kure's identity-only constructor plus a
// deep copy of the spec.
func (c *PersistentVolumeConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	pv := kubernetes.CreatePersistentVolume(app.Name)
	c.Spec.DeepCopyInto(&pv.Spec)
	obj := client.Object(pv)
	return []*client.Object{&obj}, nil
}
