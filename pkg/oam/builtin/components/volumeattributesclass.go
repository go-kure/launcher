package components

import (
	"github.com/go-kure/kure/pkg/kubernetes"
	"github.com/go-kure/kure/pkg/stack"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// VolumeAttributesClassHandler handles OAM volumeattributesclass components:
// the kind-named projection of a storage.k8s.io/v1 VolumeAttributesClass
// (go-kure/launcher#790).
//
// A VolumeAttributesClass has no spec: its properties are the object's own
// top-level fields, under their json names, decoded strictly; its kind,
// apiVersion and metadata are launcher's to set and are refused. It emits the
// VolumeAttributesClass, named after the component unless `objectName` names
// it, and nothing else. The object is cluster-scoped: it carries no namespace,
// whatever namespace the application is built for.
// TestCoreKindSchemas_CoverSpec keeps the published key set equal to the
// upstream json tags, less the object's own identity.
type VolumeAttributesClassHandler struct{}

// CanHandle returns true for the volumeattributesclass component type.
func (h *VolumeAttributesClassHandler) CanHandle(componentType string) bool {
	return componentType == "volumeattributesclass"
}

// PropertySchema declares every authorable storagev1.VolumeAttributesClass
// field by its json name.
func (h *VolumeAttributesClassHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"driverName": {
			Type:        oam.PropertyTypeString,
			Required:    true,
			Description: "Required. VolumeAttributesClass driverName: the CSI driver the class applies to, e.g. ebs.csi.aws.com. Immutable once created.",
		},
		"parameters": {
			Type: oam.PropertyTypeObject, AdditionalProperties: true,
			Required:    true,
			Description: "Required, with at least one entry. VolumeAttributesClass parameters: string volume attributes passed to the CSI driver, which alone interprets them. Immutable once created.",
		},
	}
}

// volumeAttributesClassKind is the volumeattributesclass kind: see
// policyFreeKind. The API requires both fields: driverName, which the Go type
// would write as "" when unauthored, and at least one parameter. The API's
// other value rules are left to the API server.
var volumeAttributesClassKind = &policyFreeKind[storagev1.VolumeAttributesClass]{
	upstream:    "storage.k8s.io/v1 VolumeAttributesClass (a volumeattributesclass component authors its fields other than kind, apiVersion and metadata)",
	wholeObject: true,
	validate: func(vac *storagev1.VolumeAttributesClass) error {
		if vac.DriverName == "" {
			return errors.New("driverName: required (the CSI driver the class applies to)")
		}
		if len(vac.Parameters) == 0 {
			return errors.New("parameters: required (the API requires at least one volume attribute)")
		}
		return nil
	},
	build: func(name, _ string, authored *storagev1.VolumeAttributesClass) client.Object {
		identity := kubernetes.CreateVolumeAttributesClass(name)
		vac := authored.DeepCopy()
		vac.TypeMeta, vac.ObjectMeta = identity.TypeMeta, identity.ObjectMeta
		return vac
	},
}

// ToApplicationConfig decodes an OAM volumeattributesclass component into its
// config. The build namespace is not used: the object is cluster-scoped.
func (h *VolumeAttributesClassHandler) ToApplicationConfig(component *oam.Component, _ string) (stack.ApplicationConfig, error) {
	return volumeAttributesClassKind.config(component)
}
