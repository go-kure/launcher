package policies_test

import (
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

func TestReconciliationSettingsHandler_CanHandle(t *testing.T) {
	h := &policies.ReconciliationSettingsHandler{}
	if !h.CanHandle("reconciliation") {
		t.Error("expected CanHandle(\"reconciliation\") = true")
	}
	if h.CanHandle("health-checks") {
		t.Error("expected CanHandle(\"health-checks\") = false")
	}
}

func TestReconciliationSettingsHandler_AllProperties(t *testing.T) {
	h := &policies.ReconciliationSettingsHandler{}
	result := oam.NewPolicyResult()
	policy := &oam.ApplicationPolicy{
		Name: "recon",
		Type: "reconciliation",
		Properties: map[string]any{
			"interval":      "5m",
			"retryInterval": "1m",
			"timeout":       "3m",
			"prune":         true,
			"wait":          false,
			"force":         true,
			"suspend":       false,
		},
	}
	if err := h.Apply(policy, nil, result); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	s := result.ReconciliationSettings
	if s == nil {
		t.Fatal("ReconciliationSettings is nil after Apply")
	}
	if s.Interval != "5m" || s.RetryInterval != "1m" || s.Timeout != "3m" {
		t.Errorf("durations = %q/%q/%q, want 5m/1m/3m", s.Interval, s.RetryInterval, s.Timeout)
	}
	for name, got := range map[string]*bool{"prune": s.Prune, "wait": s.Wait, "force": s.Force, "suspend": s.Suspend} {
		if got == nil {
			t.Errorf("%s = nil, want set", name)
		}
	}
	if !*s.Prune || *s.Wait || !*s.Force || *s.Suspend {
		t.Errorf("booleans prune/wait/force/suspend = %v/%v/%v/%v, want true/false/true/false",
			*s.Prune, *s.Wait, *s.Force, *s.Suspend)
	}
}

// TestReconciliationSettingsHandler_UnsetStaysUnset: a boolean the policy does
// not author stays nil, so applyReconciliationSettings leaves the bundle's own
// value alone rather than forcing false.
func TestReconciliationSettingsHandler_UnsetStaysUnset(t *testing.T) {
	h := &policies.ReconciliationSettingsHandler{}
	result := oam.NewPolicyResult()
	policy := &oam.ApplicationPolicy{Name: "recon", Type: "reconciliation", Properties: map[string]any{"interval": "10m"}}
	if err := h.Apply(policy, nil, result); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	s := result.ReconciliationSettings
	if s.Interval != "10m" {
		t.Errorf("Interval = %q, want 10m", s.Interval)
	}
	if s.RetryInterval != "" || s.Timeout != "" {
		t.Errorf("unset durations = %q/%q, want empty", s.RetryInterval, s.Timeout)
	}
	if s.Prune != nil || s.Wait != nil || s.Force != nil || s.Suspend != nil {
		t.Errorf("unset booleans = %v/%v/%v/%v, want all nil", s.Prune, s.Wait, s.Force, s.Suspend)
	}
}

func TestReconciliationSettingsHandler_OnlyOnePerApplication(t *testing.T) {
	h := &policies.ReconciliationSettingsHandler{}
	result := oam.NewPolicyResult()
	first := &oam.ApplicationPolicy{Name: "first", Type: "reconciliation", Properties: map[string]any{"prune": true}}
	second := &oam.ApplicationPolicy{Name: "second", Type: "reconciliation", Properties: map[string]any{"prune": false}}
	if err := h.Apply(first, nil, result); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := h.Apply(second, nil, result)
	if err == nil || !strings.Contains(err.Error(), `policy "second": only one reconciliation policy is allowed per application`) {
		t.Fatalf("error = %v, want the one-per-application refusal", err)
	}
	if !*result.ReconciliationSettings.Prune {
		t.Error("the refused second policy overwrote the first one's settings")
	}
}

func TestReconciliationSettingsHandler_Errors(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{
			name:    "no properties",
			props:   map[string]any{},
			wantSub: "at least one reconciliation property must be specified",
		},
		{
			name:    "only unreadable properties",
			props:   map[string]any{"interval": "", "prune": "true"},
			wantSub: "at least one reconciliation property must be specified",
		},
		{
			name:    "invalid interval",
			props:   map[string]any{"interval": "5 minutes"},
			wantSub: `interval "5 minutes" is not a valid duration`,
		},
		{
			name:    "invalid retryInterval",
			props:   map[string]any{"retryInterval": "soon"},
			wantSub: `retryInterval "soon" is not a valid duration`,
		},
		{
			name:    "invalid timeout",
			props:   map[string]any{"timeout": "1x"},
			wantSub: `timeout "1x" is not a valid duration`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.ReconciliationSettingsHandler{}
			result := oam.NewPolicyResult()
			policy := &oam.ApplicationPolicy{Name: "recon", Type: "reconciliation", Properties: tc.props}
			err := h.Apply(policy, nil, result)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), `policy "recon"`) {
				t.Errorf("error = %q, want it to name the policy", err)
			}
			if result.ReconciliationSettings != nil {
				t.Error("a refused policy left ReconciliationSettings set")
			}
		})
	}
}

// TestReconciliationSettingsHandler_DurationErrorWrapsCause: the duration error
// keeps time.ParseDuration's error in its chain.
func TestReconciliationSettingsHandler_DurationErrorWrapsCause(t *testing.T) {
	h := &policies.ReconciliationSettingsHandler{}
	policy := &oam.ApplicationPolicy{Name: "recon", Type: "reconciliation", Properties: map[string]any{"interval": "bogus"}}
	err := h.Apply(policy, nil, oam.NewPolicyResult())
	if err == nil {
		t.Fatal("expected error")
	}
	_, parseErr := time.ParseDuration("bogus")
	if stderrors.Unwrap(err) == nil || stderrors.Unwrap(err).Error() != parseErr.Error() {
		t.Errorf("error %q does not wrap the ParseDuration error %q", err, parseErr)
	}
}
