package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// CSIDriverHandler handles OAM csidriver components: the kind-named projection
// of a storage.k8s.io/v1 CSIDriver (go-kure/launcher#790).
//
// Its properties are exactly the top-level fields of storagev1.CSIDriverSpec,
// under their json names, decoded strictly (decodeKindSpec). It emits the
// CSIDriver, named after the component unless `objectName` names it, and
// nothing else. A CSIDriver is cluster-scoped: the object carries no
// namespace, whatever namespace the application is built for. The object's
// name, the component's or its `objectName`, is the name the CSI driver
// reports, since the API identifies the driver by the object's name.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags.
type CSIDriverHandler struct{}

// CanHandle returns true for the csidriver component type.
func (h *CSIDriverHandler) CanHandle(componentType string) bool {
	return componentType == "csidriver"
}

// PropertySchema declares every top-level storagev1.CSIDriverSpec field by its
// json name. The token requests are open objects whose content is checked by
// the strict decode, not by this schema.
func (h *CSIDriverHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"attachRequired": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.attachRequired: whether volumes of the driver need an attach operation before they are mounted; false skips it. Immutable once created.",
		},
		"podInfoOnMount": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.podInfoOnMount: whether the kubelet passes the pod's name, namespace and UID to the driver when it mounts a volume.",
		},
		"volumeLifecycleModes": {
			Type:        oam.PropertyTypeArray,
			Description: "CSIDriver spec.volumeLifecycleModes: the kinds of volume the driver supports; an empty list means Persistent. Immutable once created.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A lifecycle mode: Persistent or Ephemeral."},
		},
		"storageCapacity": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.storageCapacity: whether pod scheduling considers the capacity the driver reports in CSIStorageCapacity objects.",
		},
		"fsGroupPolicy": {
			Type:        oam.PropertyTypeString,
			Description: "CSIDriver spec.fsGroupPolicy: whether Kubernetes changes a volume's ownership and permissions to the pod's fsGroup before mounting: ReadWriteOnceWithFSType, File or None. The API server defaults it to ReadWriteOnceWithFSType.",
		},
		"tokenRequests": {
			Type:        oam.PropertyTypeArray,
			Description: "CSIDriver spec.tokenRequests: the service account tokens of the pod the kubelet passes to the driver when it mounts a volume.",
			Items: &oam.PropertySchema{
				Type: oam.PropertyTypeObject, AdditionalProperties: true,
				Description: "One token: audience (required by the API; \"\" is the API server's own audience) and expirationSeconds. Decoded strictly into the Kubernetes API type: see TokenRequest in the Kubernetes API reference.",
			},
		},
		"requiresRepublish": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.requiresRepublish: whether the kubelet calls the driver periodically to refresh a mounted volume's contents.",
		},
		"seLinuxMount": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.seLinuxMount: whether the driver supports mounting each volume with its own SELinux context mount option.",
		},
		"nodeAllocatableUpdatePeriodSeconds": {
			Type:        oam.PropertyTypeInteger,
			Description: "CSIDriver spec.nodeAllocatableUpdatePeriodSeconds: the interval, in seconds, at which the kubelet refreshes the number of volumes of the driver a node can still take, at least 10. Unset, the number is never refreshed.",
		},
		"serviceAccountTokenInSecrets": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.serviceAccountTokenInSecrets: whether the tokens of tokenRequests reach the driver in the secrets field of the mount call, not in its volume context. The API allows it only with tokenRequests.",
		},
		"preventPodSchedulingIfMissing": {
			Type:        oam.PropertyTypeBoolean,
			Description: "CSIDriver spec.preventPodSchedulingIfMissing: whether the scheduler keeps a pod that uses the driver off a node where the driver is not installed.",
		},
	}
}

// csiDriverKind is the csidriver kind: see policyFreeKind. The API requires no
// field of the spec; its value rules, and the rule that the object's name is
// the driver's, are left to the API server.
var csiDriverKind = &policyFreeKind[storagev1.CSIDriverSpec]{
	upstream: "storage.k8s.io/v1 CSIDriverSpec",
	build: func(name, _ string, spec *storagev1.CSIDriverSpec) client.Object {
		driver := kubernetes.CreateCSIDriver(name)
		spec.DeepCopyInto(&driver.Spec)
		return driver
	},
}

// ToApplicationConfig decodes an OAM csidriver component into its config. The
// build namespace is not used: a CSIDriver is cluster-scoped.
func (h *CSIDriverHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return csiDriverKind.config(component)
}
