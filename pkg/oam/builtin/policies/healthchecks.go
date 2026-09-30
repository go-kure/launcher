package policies

import (
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// HealthChecksHandler processes OAM health-checks policies.
// A health-checks policy adds explicit Flux health check entries that are
// appended to every leaf bundle after the auto-generated entries:
//
//	policies:
//	  - name: extra-checks
//	    type: health-checks
//	    properties:
//	      checks:
//	        - apiVersion: batch/v1
//	          kind: Job
//	          name: db-migrate
//	          namespace: default
//
// Flux's kustomize-controller ignores spec.healthChecks on a Kustomization
// whose spec.wait is true, so combined with a reconciliation policy setting
// wait: true these entries are never checked.
type HealthChecksHandler struct{}

// CanHandle returns true for the "health-checks" policy type.
func (h *HealthChecksHandler) CanHandle(policyType string) bool {
	return policyType == "health-checks"
}

// Apply appends the policy's entries to result.HealthCheckOverrides, after any
// recorded by an earlier health-checks policy.
func (h *HealthChecksHandler) Apply(policy *oam.ApplicationPolicy, _ []string, result *oam.PolicyResult) error {
	checks, err := parseHealthCheckEntries(policy.Name, policy.Properties)
	if err != nil {
		return err
	}
	result.HealthCheckOverrides = append(result.HealthCheckOverrides, checks...)
	return nil
}

// PropertySchema declares the health-checks policy's property surface.
func (h *HealthChecksHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"checks": {
			Type:        oam.PropertyTypeArray,
			Required:    true,
			Description: "Explicit Flux health-check entries appended to every leaf bundle, in addition to the auto-generated ones.",
			Items: &oam.PropertySchema{
				Type:        oam.PropertyTypeObject,
				Description: "One Flux health-check reference.",
				Properties: map[string]oam.PropertySchema{
					"apiVersion": {Type: oam.PropertyTypeString, Required: true, Description: "API version of the resource to health-check (e.g. \"apps/v1\")."},
					"kind":       {Type: oam.PropertyTypeString, Required: true, Description: "Kind of the resource to health-check (e.g. \"Deployment\")."},
					"name":       {Type: oam.PropertyTypeString, Required: true, Description: "Name of the resource to health-check."},
					"namespace":  {Type: oam.PropertyTypeString, Description: "Namespace of the resource; left empty when omitted (Flux resolves the namespace)."},
				},
			},
		},
	}
}

// parseHealthCheckEntries extracts health check entries from policy properties.
func parseHealthCheckEntries(policyName string, properties map[string]any) ([]stack.HealthCheck, error) {
	rawChecks, ok := properties["checks"]
	if !ok {
		return nil, errors.Errorf("policy %q: missing required property 'checks'", policyName)
	}

	checkList, ok := rawChecks.([]any)
	if !ok {
		return nil, errors.Errorf("policy %q: property 'checks' must be a list", policyName)
	}

	if len(checkList) == 0 {
		return nil, errors.Errorf("policy %q: property 'checks' must not be empty", policyName)
	}

	checks := make([]stack.HealthCheck, 0, len(checkList))
	for i, rawCheck := range checkList {
		checkMap, ok := rawCheck.(map[string]any)
		if !ok {
			return nil, errors.Errorf("policy %q: checks[%d] must be a map", policyName, i)
		}

		apiVersion, _ := checkMap["apiVersion"].(string)
		kind, _ := checkMap["kind"].(string)
		name, _ := checkMap["name"].(string)
		// namespace is optional, so an absent value is legitimately empty; a present
		// value of the wrong type is not treated as absent, or the check would
		// silently target the implicit namespace instead of the one the author meant.
		var namespace string
		if rawNamespace, present := checkMap["namespace"]; present {
			if namespace, ok = rawNamespace.(string); !ok {
				return nil, errors.Errorf("policy %q: checks[%d].namespace must be a string", policyName, i)
			}
		}

		if apiVersion == "" {
			return nil, errors.Errorf("policy %q: checks[%d].apiVersion is required", policyName, i)
		}
		if kind == "" {
			return nil, errors.Errorf("policy %q: checks[%d].kind is required", policyName, i)
		}
		if name == "" {
			return nil, errors.Errorf("policy %q: checks[%d].name is required", policyName, i)
		}

		checks = append(checks, stack.HealthCheck{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
		})
	}

	return checks, nil
}
