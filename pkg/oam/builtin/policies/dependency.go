// Package policies holds the spike's terminal PolicyHandler implementations —
// concrete evidence that a lowering rule's emitted policy is observable by the rest
// of the transform pipeline, not just by the lowering fixpoint itself. No policy
// handler was registered anywhere in launcher before this spike (spec.policies
// appears in zero production fixtures); "dependency" is the first.
package policies

import (
	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// DependencyHandler implements oam.PolicyHandler for policy type "dependency":
// properties.component depends on every name in properties.dependsOn. It feeds
// Transformer.applyPolicies -> PolicyResult.Dependencies ->
// buildDependencyAwareCluster (transform.go), which wires the corresponding
// stack.Bundle.DependsOn edges — so a "dependency" policy emitted by a lowering
// rule (lowering/ordered.go) is observable in the final manifest ordering, the same
// way an authored one would be.
type DependencyHandler struct{}

func (DependencyHandler) CanHandle(policyType string) bool { return policyType == "dependency" }

// PropertySchema declares the "dependency" policy's user-facing properties, so
// emitted "dependency" policies get D4's emission-time validation like any other
// lowering-rule output.
func (DependencyHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"component": {Type: oam.PropertyTypeString, Required: true, Description: "Component that depends on the others."},
		"dependsOn": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Items:       &oam.PropertySchema{Type: oam.PropertyTypeString, Description: "A component name this one depends on."},
			Description: "Component names that must be deployed before 'component'.",
		},
	}
}

func (DependencyHandler) Apply(policy *oam.ApplicationPolicy, components []string, result *oam.PolicyResult) error {
	component, _ := policy.Properties["component"].(string)
	if component == "" {
		return errors.Errorf("dependency policy %q: properties.component is required", policy.Name)
	}
	rawDeps, _ := policy.Properties["dependsOn"].([]any)
	if len(rawDeps) == 0 {
		return errors.Errorf("dependency policy %q: properties.dependsOn must be a non-empty array", policy.Name)
	}

	known := make(map[string]bool, len(components))
	for _, c := range components {
		known[c] = true
	}
	if !known[component] {
		return errors.Errorf("dependency policy %q: component %q is not in this application", policy.Name, component)
	}

	for _, v := range rawDeps {
		dep, ok := v.(string)
		if !ok {
			return errors.Errorf("dependency policy %q: dependsOn entries must be strings, got %T", policy.Name, v)
		}
		if !known[dep] {
			return errors.Errorf("dependency policy %q: dependsOn %q is not in this application", policy.Name, dep)
		}
		result.Dependencies[component] = append(result.Dependencies[component], dep)
	}
	return nil
}
