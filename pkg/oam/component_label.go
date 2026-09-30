package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ComponentLabelDigestLength is the number of lowercase hex characters of the
// SHA-256 digest that ComponentLabelValue appends to a shortened component name:
// 10 characters, 40 bits.
const ComponentLabelDigestLength = 10

// ComponentLabelValue returns the label value that identifies the component
// named name: the value of the `app` label every built-in handler writes, of
// every selector that picks out a component's objects or pods by that label,
// and of the `<domain>/component` selector the synthesized NetworkPolicies use.
//
// A component name is a DNS-1123 subdomain (up to 253 characters, validated in
// validate.go), but a label value is at most 63 characters
// (validation.LabelValueMaxLength). A name of 63 characters or fewer is always
// a valid label value and is returned unchanged, so no document whose names
// fit changes output. A longer name is projected onto a readable prefix of
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
// container or Service after the component (helmchart, manifests, oci, crd,
// passthrough, and the traits attached to them) legitimately accept names up
// to 253 characters: their object names allow it, and the label is an
// identifier, not an address. The projection is deterministic, so the same name
// renders the same value on every build, and two distinct names map to the same
// value only through a 40-bit digest collision on a shared 52-character
// prefix — or when one author deliberately names a component exactly like
// another's projection.
//
// name must be a valid component name (a DNS-1123 subdomain); the result is
// unspecified otherwise.
func ComponentLabelValue(name string) string {
	if len(name) <= validation.LabelValueMaxLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	digest := hex.EncodeToString(sum[:])[:ComponentLabelDigestLength]
	prefixLen := validation.LabelValueMaxLength - ComponentLabelDigestLength - 1 // -1 for the joining "-"
	prefix := strings.TrimRight(name[:prefixLen], "-.")
	if prefix == "" {
		return digest
	}
	return prefix + "-" + digest
}
