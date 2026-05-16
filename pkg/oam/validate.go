package oam

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// SupportedAPIVersion is the only accepted apiVersion for Application documents.
const SupportedAPIVersion = "launcher.gokure.dev/v1alpha1"

// validComponentTypes is the Phase 1 set. Updated when Phase 2 handlers land.
var validComponentTypes = map[string]bool{
	"webservice":  true,
	"worker":      true,
	"postgresql":  true,
	"cronjob":     true,
	"helmrelease": true,
	"daemonset":   true,
	"statefulset": true,
}

// validTraitTypes is the Phase 1 set from design-kurel-package.md §4.3.
// Deferred until Phase 2 (#49, #50): pvc, networkpolicy, cilium-networkpolicy, volsync.
// Updated when Phase 2 handlers land.
var validTraitTypes = map[string]bool{
	"expose":          true,
	"ingress":         true,
	"httproute":       true,
	"certificate":     true,
	"external-secret": true,
	"configmap":       true,
	"scaler":          true,
}

// traitComponentRestrictions maps trait types to the component types they support.
// Traits not listed here are allowed on any component type.
var traitComponentRestrictions = map[string]map[string]bool{
	"scaler": {"webservice": true, "worker": true},
}

// validate performs semantic validation on a parsed Application.
func validate(app *Application) error {
	if app.APIVersion != SupportedAPIVersion {
		return oamValidationError("apiVersion", fmt.Sprintf("unsupported apiVersion %q, expected %q",
			app.APIVersion, SupportedAPIVersion))
	}

	if app.Kind != "Application" {
		return oamValidationError("kind", fmt.Sprintf("expected kind Application, got %q", app.Kind))
	}

	if app.Metadata.Name == "" {
		return oamValidationError("metadata.name", "metadata.name is required")
	}

	if errs := validation.IsDNS1123Subdomain(app.Metadata.Name); len(errs) > 0 {
		return oamValidationError("metadata.name", fmt.Sprintf("metadata.name %q is not a valid DNS-1123 subdomain",
			app.Metadata.Name))
	}

	if app.Metadata.Namespace != "" {
		if errs := validation.IsDNS1123Subdomain(app.Metadata.Namespace); len(errs) > 0 {
			return oamValidationError("metadata.namespace", fmt.Sprintf("metadata.namespace %q is not a valid DNS-1123 subdomain",
				app.Metadata.Namespace))
		}
	}

	if len(app.Spec.Components) == 0 {
		return oamValidationError("spec.components", "spec.components must contain at least one component")
	}

	seenNames := make(map[string]bool)
	for i, c := range app.Spec.Components {
		if err := validateComponent(&c, i, seenNames); err != nil {
			return err
		}
	}

	seenPolicyNames := make(map[string]bool)
	for i, p := range app.Spec.Policies {
		if err := validateApplicationPolicy(&p, i, seenPolicyNames); err != nil {
			return err
		}
	}

	return nil
}

func validateComponent(c *Component, index int, seenNames map[string]bool) error {
	if c.Name == "" {
		return oamValidationError("name", fmt.Sprintf("spec.components[%d].name is required", index))
	}

	if errs := validation.IsDNS1123Subdomain(c.Name); len(errs) > 0 {
		return oamValidationError("name", fmt.Sprintf("component name %q is not a valid DNS-1123 subdomain", c.Name))
	}

	if seenNames[c.Name] {
		return oamValidationError("name", fmt.Sprintf("duplicate component name %q", c.Name))
	}
	seenNames[c.Name] = true

	if c.Type == "" {
		return oamValidationError("type", fmt.Sprintf("component %q missing type", c.Name))
	}

	if !validComponentTypes[c.Type] {
		return errors.NewValidationError("type", c.Type, c.Name, supportedComponentTypes())
	}

	for j, t := range c.Traits {
		if err := validateTrait(&t, c.Name, c.Type, j); err != nil {
			return err
		}
	}

	return nil
}

func validateTrait(t *Trait, componentName, componentType string, index int) error {
	if t.Type == "" {
		return oamValidationError("type", fmt.Sprintf("component %q trait[%d] missing type",
			componentName, index))
	}

	if !validTraitTypes[t.Type] {
		return errors.NewValidationError("type", t.Type, componentName, supportedTraitTypes())
	}

	if allowed, restricted := traitComponentRestrictions[t.Type]; restricted {
		if !allowed[componentType] {
			supportedTypes := make([]string, 0, len(allowed))
			for ct := range allowed {
				supportedTypes = append(supportedTypes, ct)
			}
			slices.Sort(supportedTypes)
			return oamValidationError("type", fmt.Sprintf(
				"trait %q is not supported on component type %q (component %q); supported types: %s",
				t.Type, componentType, componentName, strings.Join(supportedTypes, ", ")))
		}
	}

	return nil
}

// validateApplicationPolicy checks that an application policy entry has a non-empty name and type.
// Policy types are open-ended in Phase 1 — no allowlist, no semantic interpretation
// of policy properties. See design-kurel-package.md §4.4.
func validateApplicationPolicy(p *ApplicationPolicy, index int, seenNames map[string]bool) error {
	if p.Name == "" {
		return oamValidationError("name", fmt.Sprintf("spec.policies[%d].name is required", index))
	}

	if errs := validation.IsDNS1123Subdomain(p.Name); len(errs) > 0 {
		return oamValidationError("name", fmt.Sprintf("policy name %q is not a valid DNS-1123 subdomain", p.Name))
	}

	if seenNames[p.Name] {
		return oamValidationError("name", fmt.Sprintf("duplicate policy name %q", p.Name))
	}
	seenNames[p.Name] = true

	if p.Type == "" {
		return oamValidationError("type", fmt.Sprintf("policy %q missing type", p.Name))
	}

	return nil
}

// oamValidationError creates a ValidationError with a custom message.
func oamValidationError(field, message string) *errors.ValidationError {
	return &errors.ValidationError{
		Field:     field,
		Component: "application",
		Message:   message,
	}
}

func supportedComponentTypes() []string {
	types := make([]string, 0, len(validComponentTypes))
	for t := range validComponentTypes {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

func supportedTraitTypes() []string {
	types := make([]string, 0, len(validTraitTypes))
	for t := range validTraitTypes {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}
