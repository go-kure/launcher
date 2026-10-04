package policies

import (
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// PlacementHandler processes OAM placement policies.
// A placement policy overrides the automatic tier classification for a component:
//
//	policies:
//	  - name: cache-in-infra
//	    type: placement
//	    properties:
//	      component: redis-cache
//	      tier: infra
type PlacementHandler struct{}

// CanHandle returns true for the "placement" policy type.
func (h *PlacementHandler) CanHandle(policyType string) bool {
	return policyType == "placement"
}

// Apply records the component's tier in result.TierOverrides. The tier must be
// one of oam.TierOrder, the component must exist in components, and an earlier
// placement policy on the same result may not have put it in a different tier.
func (h *PlacementHandler) Apply(policy *oam.ApplicationPolicy, components []string, result *oam.PolicyResult) error {
	component, ok := policy.Properties["component"].(string)
	if !ok || component == "" {
		return errors.Errorf("policy %q: missing required property 'component'", policy.Name)
	}

	tierStr, ok := policy.Properties["tier"].(string)
	if !ok || tierStr == "" {
		return errors.Errorf("policy %q: missing required property 'tier'", policy.Name)
	}

	tier := oam.Tier(tierStr)
	if !slices.Contains(oam.TierOrder, tier) {
		return errors.Errorf("policy %q: unknown tier %q (valid: %s)", policy.Name, tierStr, validTierList())
	}

	componentSet := toSet(components)
	if !componentSet[component] {
		return errors.Errorf("policy %q references unknown component %q", policy.Name, component)
	}

	// A second placement for the same component is an error rather than a silent
	// override; repeating the same tier is harmless and accepted.
	if prev, placed := result.TierOverrides[component]; placed && prev != tier {
		return errors.Errorf("policy %q: component %q is already placed in tier %q by an earlier placement policy, cannot also place it in %q",
			policy.Name, component, prev, tier)
	}

	result.TierOverrides[component] = tier
	return nil
}

// PropertySchema declares the placement policy's property surface.
func (h *PlacementHandler) PropertySchema() map[string]oam.PropertySchema {
	tiers := make([]any, 0, len(oam.TierOrder))
	for _, t := range oam.TierOrder {
		tiers = append(tiers, string(t))
	}
	return map[string]oam.PropertySchema{
		"component": {Type: oam.PropertyTypeString, Required: true, Description: "Name of the component whose tier classification is being overridden."},
		"tier": {
			Type:        oam.PropertyTypeString,
			Required:    true,
			Enum:        tiers,
			Description: "Deployment tier to place the component in, overriding automatic classification.",
		},
	}
}

// validTierList renders oam.TierOrder as "infra, services, apps".
func validTierList() string {
	names := make([]string, 0, len(oam.TierOrder))
	for _, t := range oam.TierOrder {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}
