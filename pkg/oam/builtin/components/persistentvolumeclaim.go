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
// same BuildPVC as a workload's `pvc` volume, and the `pvc` trait is this
// kind's twin: it runs the same ParseClaimProperties, ApplyClaimPolicy and
// GenerateClaim (go-kure/launcher#741). Both take an unauthored
// storageClassName from the ClusterProfile `pvc` capability (CapabilityDefaults).
type PersistentVolumeClaimHandler struct{}

// CanHandle returns true for the persistentvolumeclaim component type.
func (h *PersistentVolumeClaimHandler) CanHandle(componentType string) bool {
	return componentType == "persistentvolumeclaim"
}

// CapabilityDefaults reads the `pvc` capability binding, the one the pvc trait
// merges, for storageClassName only (oam.ComponentCapabilityDefaults). An authored
// value, "" included, wins.
func (h *PersistentVolumeClaimHandler) CapabilityDefaults() (string, []string) {
	return "pvc", []string{"storageClassName"}
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
			Description: "StorageClass backing the claim. Unset takes the ClusterProfile pvc capability's storageClassName, else the cluster's default class; an empty string requests no class (no dynamic provisioning).",
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
	Name string
	// ObjectName names the PersistentVolumeClaim (oam.Component.ObjectName);
	// its labels keep Name. Empty for the application's name.
	ObjectName string
	Namespace  string
	// Claim carries the claim's fields; its Name is unused, since Generate
	// names the claim by ObjectName or after the Application. Claim.Size is "" until a policy
	// default fills an unauthored size.
	Claim PVCConfig
}

// ApplyPolicy fills an unauthored size from the policy's storage default and
// enforces the policy's maximum.
func (c *PersistentVolumeClaimConfig) ApplyPolicy(p oam.Policy) error {
	return ApplyClaimPolicy(&c.Claim, p)
}

// Generate creates the PersistentVolumeClaim, named and labelled after the
// Application.
func (c *PersistentVolumeClaimConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return GenerateClaim(c.Claim, kindObjectName(c.ObjectName, app.Name), app.Namespace, appLabels(app.Name))
}

// parsePersistentVolumeClaim reads a persistentvolumeclaim component's
// properties.
func parsePersistentVolumeClaim(component *oam.Component) (*PersistentVolumeClaimConfig, error) {
	claim, err := ParseClaimProperties(component.Properties)
	if err != nil {
		return nil, err
	}
	return &PersistentVolumeClaimConfig{Name: component.Name, ObjectName: componentObjectName(component), Claim: claim}, nil
}

// The three functions below are the kind's whole claim path: parse, policy,
// generate. The pvc trait, the kind's twin (go-kure/launcher#741), runs the
// same three, so the two build the same claim from the same properties and
// differ only in what ownership means: the claim's name, its labels, its
// namespace and the bundle it is placed in, all passed to GenerateClaim.

// ParseClaimProperties reads a claim's size, storageClassName, accessModes
// and volumeMode with the parsers a workload's `pvc` volume uses for the same
// fields. Keys it does not know are left to the caller: the pvc trait reads
// `name`, and the engine reads `scope`. The returned Name is unset.
func ParseClaimProperties(props map[string]any) (PVCConfig, error) {
	var claim PVCConfig
	if size, present, err := parseStringField(props, "size", "size"); err != nil {
		return PVCConfig{}, err
	} else if present {
		// An authored size is checked now; an unauthored one waits for the
		// policy default (ApplyClaimPolicy).
		if err := validateClaimSize(size); err != nil {
			return PVCConfig{}, err
		}
		claim.Size = size
	}

	storageClass, explicitEmpty, err := parseStorageClassKey(props, "storageClassName", "storageClassName")
	if err != nil {
		return PVCConfig{}, err
	}
	claim.StorageClass = storageClass
	claim.StorageClassExplicitEmpty = explicitEmpty

	accessModes, err := parseAccessModes(props)
	if err != nil {
		return PVCConfig{}, err
	}
	claim.AccessModes = accessModes

	if vm, present, err := parseStringField(props, "volumeMode", "volumeMode"); err != nil {
		return PVCConfig{}, err
	} else if present {
		mode, err := parseVolumeModeValue(vm, "volumeMode")
		if err != nil {
			return PVCConfig{}, err
		}
		claim.VolumeMode = mode
	}
	return claim, nil
}

// ApplyClaimPolicy fills an unauthored claim size from the policy's storage
// default and enforces the policy's maximum. Precedence: authored > policy
// default; with neither, the size is refused. A nil policy means no default
// and no cap.
func ApplyClaimPolicy(claim *PVCConfig, p oam.Policy) error {
	if p != nil && claim.Size == "" {
		claim.Size = p.DefaultStorageSize()
	}
	if err := validateClaimSize(claim.Size); err != nil {
		return err
	}
	if p != nil {
		return enforceMaxStorageSize(claim.Size, p.MaxStorageSize())
	}
	return nil
}

// GenerateClaim builds the claim under name in namespace with labels. It
// checks the size again, so a config built without ApplyClaimPolicy cannot
// emit a claim with no size.
func GenerateClaim(claim PVCConfig, name, namespace string, labels map[string]string) ([]*client.Object, error) {
	if err := validateClaimSize(claim.Size); err != nil {
		return nil, err
	}
	claim.Name = name
	pvc, err := BuildPVC(claim, namespace, labels)
	if err != nil {
		return nil, err
	}
	obj := client.Object(pvc)
	return []*client.Object{&obj}, nil
}

// validateClaimSize refuses an unset or non-positive size.
func validateClaimSize(size string) error {
	if size == "" {
		return errors.New("size: required (author it, or set an EnvironmentPolicy storage default)")
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return errors.Errorf("size: invalid quantity %q: %w", size, err)
	}
	if qty.Sign() <= 0 {
		return errors.Errorf("size: must be positive, got %q", size)
	}
	return nil
}
