package policies

import (
	"time"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// ReconciliationSettingsHandler processes OAM reconciliation policies.
// A reconciliation policy sets Flux Kustomization reconciliation parameters
// for all leaf bundles in the application:
//
//	policies:
//	  - name: reconciliation
//	    type: reconciliation
//	    properties:
//	      interval: 5m
//	      retryInterval: 1m
//	      prune: true
//	      force: false
//	      suspend: false
//
// At most one reconciliation policy is allowed per application.
type ReconciliationSettingsHandler struct{}

// CanHandle returns true for the "reconciliation" policy type.
func (h *ReconciliationSettingsHandler) CanHandle(policyType string) bool {
	return policyType == "reconciliation"
}

// Apply sets result.ReconciliationSettings. A second reconciliation policy on
// the same result is an error rather than a silent override.
func (h *ReconciliationSettingsHandler) Apply(policy *oam.ApplicationPolicy, _ []string, result *oam.PolicyResult) error {
	if result.ReconciliationSettings != nil {
		return errors.Errorf("policy %q: only one reconciliation policy is allowed per application", policy.Name)
	}
	settings, err := parseReconciliationSettings(policy.Name, policy.Properties)
	if err != nil {
		return err
	}
	result.ReconciliationSettings = settings
	return nil
}

// PropertySchema declares the reconciliation policy's property surface. Every key
// is individually optional, but the handler additionally requires at least one to
// be present and that interval/retryInterval/timeout parse as Go durations —
// neither constraint is expressible in this vocabulary.
func (h *ReconciliationSettingsHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"interval":      {Type: oam.PropertyTypeString, Description: "Flux reconciliation interval as a Go duration (e.g. \"5m\")."},
		"retryInterval": {Type: oam.PropertyTypeString, Description: "Interval to wait before retrying a failed reconciliation, as a Go duration."},
		"timeout":       {Type: oam.PropertyTypeString, Description: "Timeout for apply/health-check operations, as a Go duration."},
		"prune":         {Type: oam.PropertyTypeBoolean, Description: "Enable garbage collection of resources removed from the source."},
		"wait":          {Type: oam.PropertyTypeBoolean, Description: "Wait for all applied resources to become ready before reporting success. Flux ignores healthChecks when this is true."},
		"force":         {Type: oam.PropertyTypeBoolean, Description: "Force re-creation of resources that cannot be updated in place (immutable-field changes)."},
		"suspend":       {Type: oam.PropertyTypeBoolean, Description: "Suspend reconciliation of the generated Kustomizations."},
	}
}

// parseReconciliationSettings extracts reconciliation settings from policy properties.
func parseReconciliationSettings(policyName string, props map[string]any) (*oam.ReconciliationSettings, error) {
	s := &oam.ReconciliationSettings{}

	if v, ok := props["interval"].(string); ok && v != "" {
		if err := validateDuration(policyName, "interval", v); err != nil {
			return nil, err
		}
		s.Interval = v
	}
	if v, ok := props["retryInterval"].(string); ok && v != "" {
		if err := validateDuration(policyName, "retryInterval", v); err != nil {
			return nil, err
		}
		s.RetryInterval = v
	}
	if v, ok := props["timeout"].(string); ok && v != "" {
		if err := validateDuration(policyName, "timeout", v); err != nil {
			return nil, err
		}
		s.Timeout = v
	}
	if v, ok := props["prune"].(bool); ok {
		s.Prune = &v
	}
	if v, ok := props["wait"].(bool); ok {
		s.Wait = &v
	}
	if v, ok := props["force"].(bool); ok {
		s.Force = &v
	}
	if v, ok := props["suspend"].(bool); ok {
		s.Suspend = &v
	}

	if s.Interval == "" && s.RetryInterval == "" && s.Timeout == "" &&
		s.Prune == nil && s.Wait == nil && s.Force == nil && s.Suspend == nil {
		return nil, errors.Errorf("policy %q: at least one reconciliation property must be specified", policyName)
	}

	return s, nil
}

// validateDuration checks that a duration string is parseable.
func validateDuration(policyName, field, value string) error {
	if _, err := time.ParseDuration(value); err != nil {
		return errors.Wrapf(err, "policy %q: %s %q is not a valid duration", policyName, field, value)
	}
	return nil
}
