package policies

import (
	"regexp"
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
// be present and that interval/retryInterval/timeout are Flux durations —
// neither constraint is expressible in this vocabulary.
func (h *ReconciliationSettingsHandler) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{
		"interval":      {Type: oam.PropertyTypeString, Description: "Reconciliation interval as a Flux duration: unsigned, units ms, s, m, h (e.g. \"5m\")."},
		"retryInterval": {Type: oam.PropertyTypeString, Description: "Interval to wait before retrying a failed reconciliation, as a Flux duration."},
		"timeout":       {Type: oam.PropertyTypeString, Description: "Timeout for apply/health-check operations, as a Flux duration."},
		"prune":         {Type: oam.PropertyTypeBoolean, Description: "Enable garbage collection of resources removed from the source."},
		"wait":          {Type: oam.PropertyTypeBoolean, Description: "Wait for all applied resources to become ready before reporting success. Flux ignores healthChecks when this is true."},
		"force":         {Type: oam.PropertyTypeBoolean, Description: "Force re-creation of resources that cannot be updated in place (immutable-field changes)."},
		"suspend":       {Type: oam.PropertyTypeBoolean, Description: "Suspend reconciliation of the generated Kustomizations."},
	}
}

// parseReconciliationSettings extracts reconciliation settings from policy properties.
// A present property of the wrong type is an error, not skipped: the handler can be
// reached without ValidateAuthoredProperties having run (Transform called directly),
// and skipping would silently discard a setting the author wrote. A null value and
// an empty duration string still read as absent.
func parseReconciliationSettings(policyName string, props map[string]any) (*oam.ReconciliationSettings, error) {
	s := &oam.ReconciliationSettings{}

	for _, d := range []struct {
		field string
		dst   *string
	}{
		{"interval", &s.Interval},
		{"retryInterval", &s.RetryInterval},
		{"timeout", &s.Timeout},
	} {
		raw, present := props[d.field]
		if !present || oam.IsNullValue(raw) {
			continue
		}
		v, ok := raw.(string)
		if !ok {
			return nil, errors.Errorf("policy %q: %s must be a string duration, got %T", policyName, d.field, raw)
		}
		if v == "" {
			continue
		}
		if err := validateDuration(policyName, d.field, v); err != nil {
			return nil, err
		}
		*d.dst = v
	}

	for _, b := range []struct {
		field string
		dst   **bool
	}{
		{"prune", &s.Prune},
		{"wait", &s.Wait},
		{"force", &s.Force},
		{"suspend", &s.Suspend},
	} {
		raw, present := props[b.field]
		if !present || oam.IsNullValue(raw) {
			continue
		}
		v, ok := raw.(bool)
		if !ok {
			return nil, errors.Errorf("policy %q: %s must be a boolean, got %T", policyName, b.field, raw)
		}
		*b.dst = &v
	}

	if s.Interval == "" && s.RetryInterval == "" && s.Timeout == "" &&
		s.Prune == nil && s.Wait == nil && s.Force == nil && s.Suspend == nil {
		return nil, errors.Errorf("policy %q: at least one reconciliation property must be specified", policyName)
	}

	return s, nil
}

// fluxDuration is the pattern Flux's Kustomization CRD enforces on interval,
// retryInterval and timeout (kustomize-controller api/v1, +kubebuilder:validation:Pattern).
// time.ParseDuration alone is wider: it accepts a sign and the ns/us/µs units, which
// the API server would reject at apply time.
var fluxDuration = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ms|s|m|h))+$`)

// validateDuration checks that a duration string is one Flux accepts: parseable
// as a Go duration, and within Flux's CRD pattern.
func validateDuration(policyName, field, value string) error {
	if _, err := time.ParseDuration(value); err != nil {
		return errors.Wrapf(err, "policy %q: %s %q is not a valid duration", policyName, field, value)
	}
	if !fluxDuration.MatchString(value) {
		return errors.Errorf("policy %q: %s %q is not a valid Flux duration (unsigned, units ms, s, m, h)", policyName, field, value)
	}
	return nil
}
