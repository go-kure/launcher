package oam

import "fmt"

// TierAnnotation is the OAM component annotation key for overriding the default tier.
// allow-term:wharf tracked by #215 (domain becomes a parameter; this literal is interim).
const TierAnnotation = "wharf.zone/tier"

// ComponentLabel is the pod label key the downstream runtime stamps on every rendered
// workload pod and helm-rendered pod (webservice via its mutator, helm-rendered pods via a
// mandatory post-renderer). Synthesized NetworkPolicies target it by default so a single
// deterministic selector covers both builtin and helm-backed component pods.
// allow-term:wharf tracked by #215 (domain becomes a parameter; this literal is interim).
const ComponentLabel = "wharf.zone/component"

// defaultTierMap maps OAM component types to their deployment tier.
var defaultTierMap = map[string]Tier{
	"postgresql":  TierServices,
	"webservice":  TierApps,
	"worker":      TierApps,
	"cronjob":     TierApps,
	"helmchart":   TierApps,
	"daemonset":   TierInfra,
	"statefulset": TierApps,
	"crd":         TierApps,
	"manifests":   TierApps,
	"oci":         TierApps,
}

// validTiers is the set of valid tier values for annotation validation.
var validTiers = map[Tier]bool{
	TierInfra:    true,
	TierServices: true,
	TierApps:     true,
}

// ClassifyComponent returns the deployment tier for the given component.
// It checks the TierAnnotation first, then the defaultTierMap, and falls back to TierApps.
func ClassifyComponent(c *Component) (Tier, error) {
	if v, ok := c.Annotations[TierAnnotation]; ok {
		tier := Tier(v)
		if !validTiers[tier] {
			return "", fmt.Errorf("invalid tier annotation %q on component %q: must be one of infra, services, apps", v, c.Name)
		}
		return tier, nil
	}
	if tier, ok := defaultTierMap[c.Type]; ok {
		return tier, nil
	}
	return TierApps, nil
}

// groupByTier groups component entries by their deployment tier.
func groupByTier(entries []componentEntry) map[Tier][]componentEntry {
	groups := make(map[Tier][]componentEntry)
	for _, e := range entries {
		groups[e.tier] = append(groups[e.tier], e)
	}
	return groups
}
