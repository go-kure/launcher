package oam

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// DefaultDomain is the library default domain for derived platform keys. Callers
// embedding launcher (e.g. a downstream platform) override it via TransformContext.Domain.
const DefaultDomain = "gokure.dev"

// TierAnnotation is the OAM component annotation key for overriding the default tier.
//
// Deprecated: use TierAnnotationKey(domain). Retained for source compatibility; this is
// the library default key (== TierAnnotationKey(DefaultDomain)).
const TierAnnotation = "gokure.dev/tier"

// ComponentLabel is the default pod-selector key synthesized NetworkPolicies target.
//
// Deprecated: use ComponentLabelKeyForDomain(domain). The library default key
// (== ComponentLabelKeyForDomain(DefaultDomain)).
const ComponentLabel = "gokure.dev/component"

// domainOrDefault returns domain, or DefaultDomain when empty.
func domainOrDefault(domain string) string {
	if domain == "" {
		return DefaultDomain
	}
	return domain
}

// TierAnnotationKey returns the "<domain>/tier" annotation key; empty domain uses
// DefaultDomain. Pure derivation with no validation — the transform/classification paths
// validate the domain.
func TierAnnotationKey(domain string) string { return domainOrDefault(domain) + "/tier" }

// ComponentLabelKeyForDomain returns the "<domain>/component" label key; empty domain uses
// DefaultDomain. Pure derivation with no validation.
func ComponentLabelKeyForDomain(domain string) string {
	return domainOrDefault(domain) + "/component"
}

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

// ClassifyComponent returns the deployment tier for the given component, using the
// library default domain (DefaultDomain) for the tier annotation key.
func ClassifyComponent(c *Component) (Tier, error) {
	return ClassifyComponentWithDomain(c, DefaultDomain)
}

// ClassifyComponentWithDomain returns the deployment tier for the given component, reading
// the "<domain>/tier" override annotation. It checks that annotation first, then the
// defaultTierMap, and falls back to TierApps. A nil component is an error; an empty domain
// uses DefaultDomain; an invalid domain is an error (validated here independently, since
// this is an exported helper callable outside the transform pipeline).
func ClassifyComponentWithDomain(c *Component, domain string) (Tier, error) {
	if c == nil {
		return "", errors.New("nil component")
	}
	domain = domainOrDefault(domain)
	if errs := validation.IsDNS1123Subdomain(domain); len(errs) > 0 {
		return "", errors.Errorf("invalid domain %q: %s", domain, strings.Join(errs, "; "))
	}
	if v, ok := c.Annotations[TierAnnotationKey(domain)]; ok {
		tier := Tier(v)
		if !validTiers[tier] {
			return "", errors.Errorf("invalid tier annotation %q on component %q: must be one of infra, services, apps", v, c.Name)
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
