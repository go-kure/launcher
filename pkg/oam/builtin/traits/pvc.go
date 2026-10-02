package traits

import (
	"maps"
	"strings"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// PVCHandler handles OAM pvc traits, generating a standalone PersistentVolumeClaim.
// It is the persistentvolumeclaim kind's twin (go-kure/launcher#741): every
// claim field is read, defaulted and built by the kind's own
// components.ParseClaimProperties, ApplyClaimPolicy and GenerateClaim. The
// trait adds only what ownership means: the claim's name (its `name`
// property), the owner's labels and namespace, and the owner's bundle.
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

// PropertySchema declares the pvc trait's user-facing properties: the
// persistentvolumeclaim kind's, plus the claim's `name`. Only the
// storageClassName description differs, since the trait alone takes a
// platform default from the ClusterProfile `pvc` capability
// (go-kure/launcher#742 tracks giving the kind the same).
func (h *PVCHandler) PropertySchema() map[string]oam.PropertySchema {
	schema := maps.Clone((&components.PersistentVolumeClaimHandler{}).PropertySchema())
	schema["name"] = oam.PropertySchema{Type: oam.PropertyTypeString, Required: true, Description: "Name of the PersistentVolumeClaim to create (a DNS-1123 subdomain)."}
	sc := schema["storageClassName"]
	sc.Description = "StorageClass backing the claim. Unset takes the ClusterProfile pvc capability's storageClassName, else the cluster's default class; an empty string requests no class (no dynamic provisioning)."
	schema["storageClassName"] = sc
	return schema
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
	// The kind's claim name is its component name, which document validation
	// holds to the same rule.
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return nil, errors.Errorf("PVC name %q is not a valid DNS-1123 subdomain: %s", name, strings.Join(errs, "; "))
	}
	claim, err := components.ParseClaimProperties(props)
	if err != nil {
		return nil, errors.Wrapf(err, "PVC %q", name)
	}
	return &PVCTraitConfig{Name: name, componentName: app.Name, Claim: claim}, nil
}

// PVCTraitConfig implements stack.ApplicationConfig for standalone PVC traits.
type PVCTraitConfig struct {
	// Name is the claim's name.
	Name          string
	componentName string
	// Claim carries the claim's fields as the persistentvolumeclaim kind
	// parses them. Claim.Size is "" until a policy default fills an
	// unauthored size.
	Claim components.PVCConfig
}

// ComponentName returns the OAM component this sub-app belongs to, for resource
// provenance attribution.
func (c *PVCTraitConfig) ComponentName() string { return c.componentName }

// ApplyPolicy applies the kind's storage policy: an unauthored size takes the
// policy default, and the effective size must be positive and within the
// policy maximum. A nil policy means no default and no cap.
func (c *PVCTraitConfig) ApplyPolicy(p oam.Policy) error {
	if err := components.ApplyClaimPolicy(&c.Claim, p); err != nil {
		return errors.Wrapf(err, "PVC %q", c.Name)
	}
	return nil
}

// Generate builds the claim through the kind's GenerateClaim, under the
// trait's name and with the owning component's labels.
func (c *PVCTraitConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := components.GenerateClaim(c.Claim, c.Name, app.Namespace, componentLabels(c.componentName))
	if err != nil {
		return nil, errors.Wrapf(err, "PVC %q", c.Name)
	}
	return objs, nil
}
