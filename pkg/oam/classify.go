package oam

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// DefaultDomain is the library default domain for derived platform keys. Callers
// embedding launcher (e.g. a downstream platform) override it via TransformContext.Domain.
const DefaultDomain = "gokure.dev"

// TierAnnotation is the OAM component annotation key that places a component in a tier.
//
// Deprecated: use TierAnnotationKey(domain). Retained for source compatibility; this is
// the library default key (== TierAnnotationKey(DefaultDomain)).
const TierAnnotation = "gokure.dev/tier"

// ComponentLabel is the default key of the component label: launcher sets it on
// what a component owns (go-kure/launcher#788), and synthesized NetworkPolicies
// select pods by it.
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

// validTiers is the set of valid tier values for annotation validation.
var validTiers = map[Tier]bool{
	TierInfra:    true,
	TierServices: true,
	TierApps:     true,
}

// ClassifyComponent is ClassifyComponentWithDomain with the library default
// domain (DefaultDomain) for the tier annotation key.
func ClassifyComponent(c *Component) (Tier, error) {
	return ClassifyComponentWithDomain(c, DefaultDomain)
}

// ClassifyComponentWithDomain returns the tier the component's "<domain>/tier"
// annotation places it in, or "" when it carries none: launcher places no
// component by its type (go-kure/launcher#783), so a component without the
// annotation is in no tier unless a placement policy puts it in one. A nil
// component is an error; an empty domain uses DefaultDomain; an invalid domain is
// an error (validated here independently, since this is an exported helper
// callable outside the transform pipeline); so is an annotation that names no
// tier.
func ClassifyComponentWithDomain(c *Component, domain string) (Tier, error) {
	if c == nil {
		return "", errors.New("nil component")
	}
	domain = domainOrDefault(domain)
	if errs := validation.IsDNS1123Subdomain(domain); len(errs) > 0 {
		return "", errors.Errorf("invalid domain %q: %s", domain, strings.Join(errs, "; "))
	}
	v, ok := c.Annotations[TierAnnotationKey(domain)]
	if !ok {
		return "", nil
	}
	tier := Tier(v)
	if !validTiers[tier] {
		return "", errors.Errorf("invalid tier annotation %q on component %q: must be one of infra, services, apps", v, c.Name)
	}
	return tier, nil
}
