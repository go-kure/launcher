package policies_test

import (
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

// TestPlacementHandler_SecondPlacement: a second placement policy for the same
// component is refused when it names a different tier, leaving the first tier in
// place, and accepted when it repeats the same tier.
func TestPlacementHandler_SecondPlacement(t *testing.T) {
	place := func(name, tier string) *oam.ApplicationPolicy {
		return &oam.ApplicationPolicy{Name: name, Type: "placement", Properties: map[string]any{
			"component": "cache",
			"tier":      tier,
		}}
	}
	components := []string{"cache"}

	t.Run("different tier", func(t *testing.T) {
		h := &policies.PlacementHandler{}
		result := oam.NewPolicyResult()
		if err := h.Apply(place("first", "infra"), components, result); err != nil {
			t.Fatalf("first: %v", err)
		}
		err := h.Apply(place("second", "apps"), components, result)
		want := `component "cache" is already placed in tier "infra" by an earlier placement policy, cannot also place it in "apps"`
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
		if got := result.TierOverrides["cache"]; got != oam.Tier("infra") {
			t.Errorf("tier override for cache = %q after the refused policy, want infra", got)
		}
	})

	t.Run("same tier", func(t *testing.T) {
		h := &policies.PlacementHandler{}
		result := oam.NewPolicyResult()
		if err := h.Apply(place("first", "services"), components, result); err != nil {
			t.Fatalf("first: %v", err)
		}
		if err := h.Apply(place("second", "services"), components, result); err != nil {
			t.Fatalf("second, same tier: %v", err)
		}
		if got := result.TierOverrides["cache"]; got != oam.Tier("services") {
			t.Errorf("tier override for cache = %q, want services", got)
		}
	})
}

// TestPlacementHandler_Errors holds the handler's own message for each refusal.
// It does not name the policy: the transform does, once
// (TestRefusalsNameThePolicyOnce).
func TestPlacementHandler_Errors(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{
			name:  "unknown component",
			props: map[string]any{"component": "nonexistent", "tier": "infra"},
			want:  `references unknown component "nonexistent"`,
		},
		{
			name:  "invalid tier",
			props: map[string]any{"component": "web", "tier": "custom"},
			want:  `unknown tier "custom" (valid: infra, services, apps)`,
		},
		{
			name:  "missing component",
			props: map[string]any{"tier": "infra"},
			want:  "missing required property 'component'",
		},
		{
			name:  "component not a string",
			props: map[string]any{"component": 1, "tier": "infra"},
			want:  "missing required property 'component'",
		},
		{
			name:  "missing tier",
			props: map[string]any{"component": "web"},
			want:  "missing required property 'tier'",
		},
		{
			name:  "empty tier",
			props: map[string]any{"component": "web", "tier": ""},
			want:  "missing required property 'tier'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.PlacementHandler{}
			policy := &oam.ApplicationPolicy{Name: "place", Type: "placement", Properties: tc.props}
			err := h.Apply(policy, []string{"web"}, oam.NewPolicyResult())
			if err == nil {
				t.Fatalf("expected error %q", tc.want)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
		})
	}
}
