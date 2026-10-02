package components

import (
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PersistentVolumeClaimHandler handles OAM persistentvolumeclaim components:
// the kind-named projection of corev1.PersistentVolumeClaim
// (go-kure/launcher#702).
//
// It emits the claim and nothing else. The component name is the claim's
// name, so a workload volume's `claimName` names it. The claim is built by the
// same BuildPVC as a workload's `pvc` volume and the `pvc` trait, so the three
// agree on every field they share.
type PersistentVolumeClaimHandler struct{}

// CanHandle returns true for the persistentvolumeclaim component type.
func (h *PersistentVolumeClaimHandler) CanHandle(componentType string) bool {
	return componentType == "persistentvolumeclaim"
}

// PropertySchema declares the persistentvolumeclaim component's user-facing
// properties.
func (h *PersistentVolumeClaimHandler) PropertySchema() map[string]oam.PropertySchema {
	modes := make([]any, 0, len(validAccessModes))
	for _, m := range []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadOnlyMany, corev1.ReadWriteMany, corev1.ReadWriteOncePod} {
		modes = append(modes, string(m))
	}
	return map[string]oam.PropertySchema{
		"size": {
			Type:        oam.PropertyTypeString,
			Description: "Requested storage as a positive Kubernetes quantity (e.g. 10Gi). May instead come from an EnvironmentPolicy storage default.",
		},
		"storageClassName": {
			Type:        oam.PropertyTypeString,
			Description: "StorageClass backing the claim. Unset uses the cluster's default class; an empty string requests no class (no dynamic provisioning).",
		},
		"accessModes": {
			Type:        oam.PropertyTypeArray,
			Default:     []any{string(corev1.ReadWriteOnce)},
			Description: "Access modes requested for the claim. ReadWriteOncePod must be the only mode.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: modes, Description: "A volume access mode."},
		},
		"volumeMode": {
			Type:        oam.PropertyTypeString,
			Enum:        []any{string(corev1.PersistentVolumeFilesystem), string(corev1.PersistentVolumeBlock)},
			Description: "The claim's volumeMode. Unset leaves it to the API server, which defaults it to Filesystem; Block provisions a raw block device.",
		},
	}
}

// ToApplicationConfig converts an OAM persistentvolumeclaim component to a
// PersistentVolumeClaimConfig.
func (h *PersistentVolumeClaimHandler) ToApplicationConfig(component *oam.Component, namespace string) (stack.ApplicationConfig, error) {
	c, err := parsePersistentVolumeClaim(component)
	if err != nil {
		return nil, err
	}
	c.Namespace = namespace
	return c, nil
}

// PersistentVolumeClaimConfig implements stack.ApplicationConfig for
// persistentvolumeclaim components.
type PersistentVolumeClaimConfig struct {
	Name      string
	Namespace string
	// Claim carries the claim's fields; its Name is unused, since Generate
	// names the claim after the Application. Claim.Size is "" until a policy
	// default fills an unauthored size.
	Claim PVCConfig
}

// ApplyPolicy fills an unauthored size from the policy's storage default and
// enforces the policy's maximum. Precedence: authored > policy default; with
// neither, the size is refused.
func (c *PersistentVolumeClaimConfig) ApplyPolicy(p oam.Policy) error {
	if p != nil && c.Claim.Size == "" {
		c.Claim.Size = p.DefaultStorageSize()
	}
	if err := c.validateSize(); err != nil {
		return err
	}
	if p != nil {
		return enforceMaxStorageSize(c.Claim.Size, p.MaxStorageSize())
	}
	return nil
}

// validateSize refuses an unset or non-positive size. It runs from ApplyPolicy,
// once a policy default had its chance, and again from Generate, so a config
// built without ApplyPolicy cannot emit a claim with no size.
func (c *PersistentVolumeClaimConfig) validateSize() error {
	if c.Claim.Size == "" {
		return errors.New("size: required (set it on the component or via an EnvironmentPolicy storage default)")
	}
	qty, err := resource.ParseQuantity(c.Claim.Size)
	if err != nil {
		return errors.Errorf("size: invalid quantity %q: %w", c.Claim.Size, err)
	}
	if qty.Sign() <= 0 {
		return errors.Errorf("size: must be positive, got %q", c.Claim.Size)
	}
	return nil
}

// Generate creates the PersistentVolumeClaim.
func (c *PersistentVolumeClaimConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	if err := c.validateSize(); err != nil {
		return nil, err
	}
	claim := c.Claim
	claim.Name = app.Name
	pvc, err := BuildPVC(claim, app.Namespace, appLabels(app.Name))
	if err != nil {
		return nil, err
	}
	obj := client.Object(pvc)
	return []*client.Object{&obj}, nil
}

// parsePersistentVolumeClaim reads a persistentvolumeclaim component's
// properties with the parsers a workload's `pvc` volume uses for the same
// fields.
func parsePersistentVolumeClaim(component *oam.Component) (*PersistentVolumeClaimConfig, error) {
	props := component.Properties
	c := &PersistentVolumeClaimConfig{Name: component.Name}

	if size, present, err := parseStringField(props, "size", "size"); err != nil {
		return nil, err
	} else if present {
		c.Claim.Size = size
		// An authored size is checked now; an unauthored one waits for the
		// policy default (ApplyPolicy).
		if err := c.validateSize(); err != nil {
			return nil, err
		}
	}

	storageClass, explicitEmpty, err := parseStorageClassKey(props, "storageClassName", "storageClassName")
	if err != nil {
		return nil, err
	}
	c.Claim.StorageClass = storageClass
	c.Claim.StorageClassExplicitEmpty = explicitEmpty

	accessModes, err := parseAccessModes(props)
	if err != nil {
		return nil, err
	}
	c.Claim.AccessModes = accessModes

	if vm, present, err := parseStringField(props, "volumeMode", "volumeMode"); err != nil {
		return nil, err
	} else if present {
		mode, err := parseVolumeModeValue(vm, "volumeMode")
		if err != nil {
			return nil, err
		}
		c.Claim.VolumeMode = mode
	}
	return c, nil
}
