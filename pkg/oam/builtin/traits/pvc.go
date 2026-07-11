package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

var validPVCAccessModes = map[string]bool{
	string(corev1.ReadWriteOnce):    true,
	string(corev1.ReadOnlyMany):     true,
	string(corev1.ReadWriteMany):    true,
	string(corev1.ReadWriteOncePod): true,
}

// PVCHandler handles OAM pvc traits, generating a standalone PersistentVolumeClaim.
type PVCHandler struct{}

// CanHandle returns true for the pvc trait type.
func (h *PVCHandler) CanHandle(traitType string) bool {
	return traitType == "pvc"
}

// ValidateAndApplyDefaults accepts the pvc storageClassName rendering key and rejects
// any other key, turning an operator typo into a profile-load error instead of a
// silent pass-through. The class stays optional/overridable, so pvc is not
// CapabilityRequired.
func (h *PVCHandler) ValidateAndApplyDefaults(rendering map[string]any) (map[string]any, error) {
	if _, err := builtin.DecodeStrict[builtin.PVCRendering](rendering); err != nil {
		return nil, errors.Wrap(err, "pvc rendering")
	}
	return rendering, nil
}

// PropertySchema declares the pvc trait's user-facing properties.
func (h *PVCHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"name": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the PersistentVolumeClaim to create."},
		// size is not schema-required: an EnvironmentPolicy may supply it via
		// DefaultStorageSize. When neither the trait nor a policy default provides a
		// value, ApplyPolicy errors (pvc has no last-resort handler default).
		"size":             {Type: oam.PropertyTypeString, Description: "Requested storage size as a Kubernetes quantity (e.g. 10Gi); may instead come from an EnvironmentPolicy storage default."},
		"storageClassName": {Type: oam.PropertyTypeString, Description: "StorageClass backing the volume."},
		"accessModes": {
			Type:        oam.PropertyTypeArray,
			Default:     []any{"ReadWriteOnce"},
			Description: "Access modes requested for the volume.",
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: []any{"ReadWriteOnce", "ReadOnlyMany", "ReadWriteMany", "ReadWriteOncePod"}, Description: "A volume access mode (ReadWriteOnce, ReadOnlyMany, ReadWriteMany, or ReadWriteOncePod)."},
		},
	}
}

// Apply parses the trait properties and appends a standalone PVC to the bundle.
func (h *PVCHandler) Apply(trait *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	config, err := h.parseProperties(trait.Properties, app)
	if err != nil {
		return err
	}

	pvcApp := stack.NewApplication(
		config.Name,
		app.Namespace,
		config,
	)
	bundle.Applications = append(bundle.Applications, pvcApp)
	return nil
}

func (h *PVCHandler) parseProperties(props map[string]any, app *stack.Application) (*PVCTraitConfig, error) {
	name, _ := props["name"].(string)
	if name == "" {
		return nil, errors.New("required property 'name' missing or not a string")
	}

	// size is optional at parse time: a policy default may supply it. When present
	// it must be a valid quantity; the "must be set" check runs in validateEffective
	// after ApplyPolicy has had a chance to apply the policy default.
	size, _ := props["size"].(string)
	if size != "" {
		if _, err := resource.ParseQuantity(size); err != nil {
			return nil, errors.Errorf("invalid PVC size %q: %w", size, err)
		}
	}

	var storageClass string
	if s, ok := props["storageClassName"].(string); ok {
		storageClass = s
	}

	accessModes := []string{string(corev1.ReadWriteOnce)}
	if rawModes, ok := props["accessModes"].([]any); ok {
		if len(rawModes) == 0 {
			return nil, errors.New("accessModes must not be empty when specified")
		}
		accessModes = nil
		for i, m := range rawModes {
			s, ok := m.(string)
			if !ok {
				return nil, errors.Errorf("accessModes[%d]: expected string, got %T", i, m)
			}
			if !validPVCAccessModes[s] {
				return nil, errors.Errorf("invalid accessMode %q: must be one of ReadWriteOnce, ReadOnlyMany, ReadWriteMany, ReadWriteOncePod", s)
			}
			accessModes = append(accessModes, s)
		}
	}

	return &PVCTraitConfig{
		Name:          name,
		componentName: app.Name,
		Size:          size,
		StorageClass:  storageClass,
		AccessModes:   accessModes,
	}, nil
}

// PVCTraitConfig implements stack.ApplicationConfig for standalone PVC traits.
type PVCTraitConfig struct {
	Name          string
	componentName string
	Size          string
	StorageClass  string
	AccessModes   []string
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *PVCTraitConfig) ComponentName() string { return c.componentName }

// ApplyPolicy defaults the PVC size from the policy when the trait omitted it,
// validates the effective size, and enforces the policy storage-size limit.
// Precedence: authored > policy default > (error, no handler default). A nil
// policy means no default and no cap.
func (c *PVCTraitConfig) ApplyPolicy(p oam.Policy) error {
	if p != nil {
		c.Size = applyDefaultResource(c.Size, p.DefaultStorageSize())
	}
	if err := c.validateEffective(); err != nil {
		return err
	}
	if p != nil {
		if err := enforceMaxStorageSize(c.Size, p.MaxStorageSize()); err != nil {
			return errors.Errorf("PVC %q %w", c.Name, err)
		}
	}
	return nil
}

// validateEffective owns the effective-size validation for the PVC: it runs
// after the policy default is applied (from ApplyPolicy) and defensively from
// Generate so a config built without ApplyPolicy cannot emit an empty size.
func (c *PVCTraitConfig) validateEffective() error {
	if c.Size == "" {
		return errors.Errorf("PVC %q size is required (set it on the trait or via an EnvironmentPolicy storage default)", c.Name)
	}
	if _, err := resource.ParseQuantity(c.Size); err != nil {
		return errors.Errorf("invalid PVC size %q: %w", c.Size, err)
	}
	return nil
}

// Generate delegates to components.BuildPVC so the trait shares the same PVC construction path.
func (c *PVCTraitConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	// Defensive: reject an unset/invalid size even if ApplyPolicy was bypassed.
	if err := c.validateEffective(); err != nil {
		return nil, err
	}

	labels := map[string]string{"app": c.componentName}
	pvc, err := components.BuildPVC(components.PVCConfig{
		Name:         c.Name,
		Size:         c.Size,
		StorageClass: c.StorageClass,
		AccessModes:  c.AccessModes,
	}, app.Namespace, labels)
	if err != nil {
		return nil, err
	}
	obj := client.Object(pvc)
	return []*client.Object{&obj}, nil
}
