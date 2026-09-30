package policies_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

func TestPlacementHandler_CanHandle(t *testing.T) {
	h := &policies.PlacementHandler{}
	if !h.CanHandle("placement") {
		t.Error("expected CanHandle('placement') = true")
	}
	if h.CanHandle("dependency") {
		t.Error("expected CanHandle('dependency') = false")
	}
}

func TestPlacementHandler_ValidOverride(t *testing.T) {
	for _, tier := range oam.TierOrder {
		t.Run(string(tier), func(t *testing.T) {
			h := &policies.PlacementHandler{}
			result := oam.NewPolicyResult()
			policy := &oam.ApplicationPolicy{
				Name: "place-cache",
				Type: "placement",
				Properties: map[string]any{
					"component": "cache",
					"tier":      string(tier),
				},
			}
			if err := h.Apply(policy, []string{"cache", "web"}, result); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := result.TierOverrides["cache"]; got != tier {
				t.Errorf("tier override for cache = %q, want %q", got, tier)
			}
			if _, ok := result.TierOverrides["web"]; ok {
				t.Error("placement recorded an override for a component it does not name")
			}
		})
	}
}

func TestPlacementHandler_Errors(t *testing.T) {
	cases := []struct {
		name    string
		props   map[string]any
		wantSub string
	}{
		{
			name:    "unknown component",
			props:   map[string]any{"component": "nonexistent", "tier": "infra"},
			wantSub: `references unknown component "nonexistent"`,
		},
		{
			name:    "invalid tier",
			props:   map[string]any{"component": "web", "tier": "custom"},
			wantSub: `unknown tier "custom" (valid: infra, services, apps)`,
		},
		{
			name:    "missing component",
			props:   map[string]any{"tier": "infra"},
			wantSub: "missing required property 'component'",
		},
		{
			name:    "component not a string",
			props:   map[string]any{"component": 1, "tier": "infra"},
			wantSub: "missing required property 'component'",
		},
		{
			name:    "missing tier",
			props:   map[string]any{"component": "web"},
			wantSub: "missing required property 'tier'",
		},
		{
			name:    "empty tier",
			props:   map[string]any{"component": "web", "tier": ""},
			wantSub: "missing required property 'tier'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.PlacementHandler{}
			policy := &oam.ApplicationPolicy{Name: "place", Type: "placement", Properties: tc.props}
			err := h.Apply(policy, []string{"web"}, oam.NewPolicyResult())
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), `policy "place"`) {
				t.Errorf("error = %q, want it to name the policy", err)
			}
		})
	}
}
