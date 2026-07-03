package traits

import (
	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
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

// PropertySchema declares the pvc trait's user-facing properties.
func (h *PVCHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"name":             {Type: oam.PropertyTypeString, Required: true},
		"size":             {Type: oam.PropertyTypeString, Required: true},
		"storageClassName": {Type: oam.PropertyTypeString},
		"accessModes": {
			Type:    oam.PropertyTypeArray,
			Default: []any{"ReadWriteOnce"},
			Items:   &oam.PropertySchema{Type: oam.PropertyTypeString, Enum: []any{"ReadWriteOnce", "ReadOnlyMany", "ReadWriteMany", "ReadWriteOncePod"}},
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

	size, _ := props["size"].(string)
	if size == "" {
		return nil, errors.New("required property 'size' missing or not a string")
	}
	if _, err := resource.ParseQuantity(size); err != nil {
		return nil, errors.Errorf("invalid PVC size %q: %w", size, err)
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
		ComponentName: app.Name,
		Size:          size,
		StorageClass:  storageClass,
		AccessModes:   accessModes,
	}, nil
}

// PVCTraitConfig implements stack.ApplicationConfig for standalone PVC traits.
type PVCTraitConfig struct {
	Name          string
	ComponentName string
	Size          string
	StorageClass  string
	AccessModes   []string
}

// ApplyPolicy enforces the policy storage-size limit against the requested PVC size.
func (c *PVCTraitConfig) ApplyPolicy(p oam.Policy) error {
	if p == nil || p.MaxStorageSize() == "" {
		return nil
	}
	current, err := resource.ParseQuantity(c.Size)
	if err != nil {
		return errors.Errorf("invalid PVC size %q: %w", c.Size, err)
	}
	max, err := resource.ParseQuantity(p.MaxStorageSize())
	if err != nil {
		return errors.Errorf("invalid maxStorageSize %q in policy: %w", p.MaxStorageSize(), err)
	}
	if current.Cmp(max) > 0 {
		return errors.Errorf("PVC %q size %q exceeds maximum storage size %q", c.Name, c.Size, p.MaxStorageSize())
	}
	return nil
}

// Generate delegates to components.BuildPVC so the trait shares the same PVC construction path.
func (c *PVCTraitConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	labels := map[string]string{"app": c.ComponentName}
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
