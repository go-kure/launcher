package oam

import (
	"fmt"
)

// ComponentLabelDigestLength is the number of lowercase hex characters of the
// SHA-256 digest that ComponentLabelValue appends to a shortened component name:
// ShortenNameDigestLength, 10 characters, 40 bits.
const ComponentLabelDigestLength = ShortenNameDigestLength

// ComponentLabelValue returns the label value that identifies the component
// named name: the value of the `app` label the built-in handlers generate, of
// every selector that picks out a component's objects or pods by that label,
// and of the `<domain>/component` selector the synthesized NetworkPolicies use.
//
// A component name is a DNS-1123 subdomain (up to 253 characters, validated in
// validate.go), but a label value is at most 63 characters
// (validation.LabelValueMaxLength). A name of 63 characters or fewer is always
// a valid label value and is returned unchanged, so no document whose names
// fit changes output. A longer name is shortened by the rule every generated
// name follows, ShortenName at ShortenLimitLabel: a readable prefix of
// itself, a "-", and the first ComponentLabelDigestLength hex characters of the
// SHA-256 digest of the whole name: at most 63 characters, beginning with the
// name's own first character and ending in a hex digit, so always a valid
// label value. Trailing '-' and '.' are trimmed from the prefix so the value
// never carries a doubled separator.
//
// The invariant this serves: a label and every selector meant to match it are
// computed by this one function from the same component name, so they always
// agree. Any code that stamps a component's identity into a label value — a
// built-in handler, a platform that stamps `<domain>/component` on the pods it
// renders (see TransformContext.ComponentLabelKey), or a consumer of
// ComponentNamed — must pass the name through this function rather than use it
// verbatim, or its selector and its label drift apart once a name exceeds 63
// characters.
//
// It is a projection, not a refusal, because the component types that name no
// container or Service after the component (helmtemplate, manifests, oci, crd,
// passthrough, and the traits attached to them) legitimately accept names up
// to 253 characters: their object names allow it, and the label is an
// identifier, not an address. The projection is deterministic, so the same name
// renders the same value on every build, and two distinct names map to the same
// value only when both have the same trimmed prefix and the same 40-bit digest
// — the untrimmed 52-character cuts may differ, e.g. only in a trailing '-'
// versus '.' — or when one author deliberately names a component exactly like
// another's projection. Validation rejects an Application in which either
// happens (validateComponentLabelValues), so within an Application that has
// passed validation distinct components carry distinct label values.
//
// name must be a valid component name (a DNS-1123 subdomain); the result is
// unspecified otherwise.
func ComponentLabelValue(name string) string {
	return ShortenName(name, ShortenLimitLabel)
}

// validateComponentLabelValues rejects an Application in which two components
// share a ComponentLabelValue. The projection's output is itself a valid name
// of 63 characters or fewer, which ComponentLabelValue returns unchanged, so a
// component named exactly like another component's projection would carry the
// same `app` label, and the selectors generated for either (a NetworkPolicy
// podSelector, a PodDisruptionBudget, a Service) would also pick out the
// other's pods. Distinct names are already enforced, so this only fails for
// such a name, or for two long names whose trimmed prefix and 40-bit digest
// both coincide. components must already have passed validateComponent.
//
// The members of one sibling group share their name, and so the label, by
// design: they deploy as one component (go-kure/launcher#280), and a selector
// picking out the group's pods is the point, not a collision.
func validateComponentLabelValues(components []Component) error {
	type labelOwner struct {
		name  string
		group *siblingGroup
	}
	owner := make(map[string]labelOwner, len(components))
	for _, c := range components {
		v := ComponentLabelValue(c.Name)
		if other, ok := owner[v]; ok {
			if other.group != nil && other.group == c.siblingGroup {
				continue
			}
			return oamValidationError("name", fmt.Sprintf("components %q and %q share the component label value %q; rename one of them", other.name, c.Name, v))
		}
		owner[v] = labelOwner{name: c.Name, group: c.siblingGroup}
	}
	return nil
}
