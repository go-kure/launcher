package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// StorageClassHandler handles OAM storageclass components: the kind-named
// projection of a storage.k8s.io/v1 StorageClass (go-kure/launcher#790).
//
// A StorageClass has no spec: its properties are the object's own top-level
// fields, under their json names, decoded strictly; its kind, apiVersion and
// metadata are launcher's to set and are refused. It emits the StorageClass,
// named after the component unless `objectName` names it, and nothing else. A
// StorageClass is cluster-scoped: the object carries no namespace, whatever
// namespace the application is built for. TestCoreKindSchemas_CoverSpec keeps
// the published key set equal to the upstream json tags, less the object's own
// identity.
type StorageClassHandler struct{}

// CanHandle returns true for the storageclass component type.
func (h *StorageClassHandler) CanHandle(componentType string) bool {
	return componentType == "storageclass"
}

// PropertySchema declares every authorable storagev1.StorageClass field by its
// json name. The topology terms are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *StorageClassHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"provisioner": {
			Type:        oam.PropertyTypeString,
			Required:    true,
			Description: "Required. StorageClass provisioner: the volume plugin or CSI driver that provisions volumes of this class, e.g. ebs.csi.aws.com. Immutable once created.",
		},
		"parameters": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Description: "StorageClass parameters: string options passed to the provisioner, which alone interprets them. Immutable once created.",
		},
		"reclaimPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "StorageClass reclaimPolicy: what happens to a dynamically provisioned volume when its claim is released: Delete or Retain. The API server defaults it to Delete.",
		},
		"mountOptions": {
			Type:        oam.PropertyTypeArray,
			Description: "StorageClass mountOptions: mount options of the volumes provisioned, passed on as written; an invalid one fails the mount, not the build.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A mount option, e.g. ro or nfsvers=4.1."},
		},
		"allowVolumeExpansion": {
			Type:        oam.PropertyTypeBoolean,
			Description: "StorageClass allowVolumeExpansion: whether a claim of this class may be resized after creation.",
		},
		"volumeBindingMode": {
			Type:        oam.PropertyTypeString,
			Description: "StorageClass volumeBindingMode: when a claim is bound and its volume provisioned: Immediate or WaitForFirstConsumer. The API server defaults it to Immediate.",
		},
		"allowedTopologies": {
			Type:        oam.PropertyTypeArray,
			Description: "StorageClass allowedTopologies: the node topologies volumes may be provisioned in; an empty list means no restriction.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One term: matchLabelExpressions, each a key and its values. Decoded strictly into the Kubernetes API type: see TopologySelectorTerm in the Kubernetes API reference.",
			},
		},
	}
}

// storageClassKind is the storageclass kind: see policyFreeKind. The API
// requires provisioner, which the Go type would write as "" when unauthored.
// The API's other value rules are left to the API server.
var storageClassKind = &policyFreeKind[storagev1.StorageClass]{
	upstream:    "storage.k8s.io/v1 StorageClass (a storageclass component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	validate: func(sc *storagev1.StorageClass) error {
		if sc.Provisioner == "" {
			return errors.New("provisioner: required (the volume plugin or CSI driver that provisions volumes of this class)")
		}
		return nil
	},
	build: func(name, _ string, authored *storagev1.StorageClass) client.Object {
		identity := kubernetes.CreateStorageClass(name)
		sc := authored.DeepCopy()
		sc.TypeMeta, sc.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return sc
	},
}

// ToApplicationConfig decodes an OAM storageclass component into its config.
// The build namespace is not used: a StorageClass is cluster-scoped.
func (h *StorageClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return storageClassKind.config(component)
}
