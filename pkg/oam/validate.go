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

// validComponentTypes is the Phase 2 set. Updated when Phase 2 handlers land.
var validComponentTypes = map[string]bool{
	"webservice":  true,
	"worker":      true,
	"postgresql":  true,
	"cronjob":     true,
	"helmchart":   true,
	"daemonset":   true,
	"statefulset": true,
}

// validTraitTypes is the set of supported trait types from design-kurel-package.md §4.3.
var validTraitTypes = map[string]bool{
	"expose":               true,
	"ingress":              true,
	"httproute":            true,
	"certificate":          true,
	"external-secret":      true,
	"configmap":            true,
	"networkpolicy":        true,
	"cilium-networkpolicy": true,
	"volsync":              true,
	"scaler":               true,
	"pvc":                  true,
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

// validateClusterProfile checks apiVersion, kind, and metadata.name on a
// decoded ClusterProfile, mirroring the semantic validation that validate()
// applies to Application documents.
func validateClusterProfile(profile *ClusterProfile) error {
	if profile.APIVersion != SupportedAPIVersion {
		return profileValidationError("apiVersion", fmt.Sprintf("unsupported apiVersion %q, expected %q",
			profile.APIVersion, SupportedAPIVersion))
	}
	if profile.Kind != "ClusterProfile" {
		return profileValidationError("kind", fmt.Sprintf("expected kind ClusterProfile, got %q", profile.Kind))
	}
	if profile.Metadata.Name == "" {
		return profileValidationError("metadata.name", "metadata.name is required")
	}
	if errs := validation.IsDNS1123Subdomain(profile.Metadata.Name); len(errs) > 0 {
		return profileValidationError("metadata.name", fmt.Sprintf("metadata.name %q is not a valid DNS-1123 subdomain",
			profile.Metadata.Name))
	}
	switch profile.Spec.GitopsEngine {
	case "", "fluxcd":
		// normalize the absent-field default
		profile.Spec.GitopsEngine = "fluxcd"
	default:
		return profileValidationError("spec.gitopsEngine",
			fmt.Sprintf("unsupported gitopsEngine %q; supported values: fluxcd", profile.Spec.GitopsEngine))
	}
	return nil
}

// validParamTypes is the set of accepted ParameterDecl.Type values.
// array and object are intentionally excluded: node substitution is not yet
// implemented, so declaring those types would produce an unusable package.
var validParamTypes = map[string]bool{
	"string":  true,
	"integer": true,
	"boolean": true,
}

// validatePackage performs semantic validation on a parsed Package.
func validatePackage(pkg *Package) error {
	if pkg.APIVersion != SupportedAPIVersion {
		return packageValidationError("apiVersion", fmt.Sprintf("unsupported apiVersion %q, expected %q",
			pkg.APIVersion, SupportedAPIVersion))
	}
	if pkg.Kind != "Package" {
		return packageValidationError("kind", fmt.Sprintf("expected kind Package, got %q", pkg.Kind))
	}
	if pkg.Metadata.Name == "" {
		return packageValidationError("metadata.name", "metadata.name is required")
	}

	seenNames := make(map[string]bool, len(pkg.Spec.Parameters))
	for i, p := range pkg.Spec.Parameters {
		if p.Name == "" {
			return packageValidationError("parameters", fmt.Sprintf("parameters[%d].name is required", i))
		}
		if seenNames[p.Name] {
			return packageValidationError("parameters", fmt.Sprintf("duplicate parameter name %q", p.Name))
		}
		seenNames[p.Name] = true
		if !validParamTypes[p.Type] {
			return packageValidationError("parameters", fmt.Sprintf(
				"parameter %q has invalid type %q; supported types: string, integer, boolean", p.Name, p.Type))
		}
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

func profileValidationError(field, message string) *errors.ValidationError {
	return &errors.ValidationError{
		Field:     field,
		Component: "clusterprofile",
		Message:   message,
	}
}

func packageValidationError(field, message string) *errors.ValidationError {
	return &errors.ValidationError{
		Field:     field,
		Component: "package",
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
