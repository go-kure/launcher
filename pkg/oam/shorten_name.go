package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ShortenNameDigestLength is the number of lowercase hex characters of the
// SHA-256 digest that ShortenName appends to a shortened name: 10 characters,
// 40 bits. It is the same for every limit but ShortenLimitHelmRelease.
const ShortenNameDigestLength = 10

// The limits ShortenName is called with, by what the generated name is for.
const (
	// ShortenLimitLabel is the limit of a label value and of a DNS-1035 or
	// DNS-1123 label: 63 characters.
	ShortenLimitLabel = validation.LabelValueMaxLength
	// ShortenLimitSubdomain is the limit of a DNS-1123 subdomain, the name of
	// most Kubernetes objects: 253 characters.
	ShortenLimitSubdomain = validation.DNS1123SubdomainMaxLength
	// ShortenLimitHelmRelease is the limit of a Helm release name: 53
	// characters. At this limit ShortenName applies Flux's rule, not its own.
	ShortenLimitHelmRelease = 53
)

// The constants of Flux helm-controller's release-name shortening, which
// ShortenName reproduces at ShortenLimitHelmRelease.
const (
	helmReleasePrefixLength = 40
	helmReleaseDigestLength = 12
)

// ShortenName is the one rule by which launcher shortens a name it generates
// (go-kure/launcher#793). A name of at most limit characters is returned
// unchanged, so no document whose names fit changes output. A longer one
// becomes its first limit-ShortenNameDigestLength-1 characters with trailing
// '-' and '.' trimmed, a "-", and the first ShortenNameDigestLength hex
// characters of the SHA-256 digest of the whole name: at most limit
// characters, beginning as name does and ending in a hex digit, so a valid
// label value or DNS-1123 subdomain whenever name is one. When trimming leaves
// no prefix, or limit leaves no room for one, the result is the digest alone,
// cut to limit.
//
// The result is deterministic. Two different names over limit share one only
// when their trimmed prefixes and their 40-bit digests both coincide; a name
// that fits is returned as written, so it can also equal the shortened form of
// a longer one.
//
// One exception: at ShortenLimitHelmRelease the result is what Flux
// helm-controller computes for a HelmRelease's release name — the first 40
// characters, kept as cut, a "-", and the first 12 hex characters of the
// digest — so a release launcher renders itself is named as Flux would name
// it. helm-controller keeps that function internal, so it is reproduced here.
//
// Only a name launcher generates by default goes through ShortenName. A name
// an author or a consumer supplies is used as written, never shortened.
func ShortenName(name string, limit int) string {
	if limit == ShortenLimitHelmRelease {
		if len(name) <= limit {
			return name
		}
		return name[:helmReleasePrefixLength] + "-" + nameDigest(name)[:helmReleaseDigestLength]
	}
	return shortenName(name, limit)
}

// ShortenNameWithSuffix returns name+suffix within limit characters for a
// generated name that ends in a fixed suffix: name is shortened by
// ShortenName's general rule to what suffix leaves of limit, and suffix is
// kept whole, so what the suffix says (a role such as "-hpa", a content
// digest) survives. The Helm release exception never applies here, whatever
// suffix leaves.
//
// A suffix that leaves fewer than ShortenNameDigestLength characters of limit
// cannot be kept whole beside a full digest. The whole name+suffix is then
// shortened by the general rule, so the result still fits limit and still
// tells two names apart; only a suffix with a part of unbounded length, such
// as an authored scope, can be that long.
func ShortenNameWithSuffix(name, suffix string, limit int) string {
	room := limit - len(suffix)
	if len(name) > room && room < ShortenNameDigestLength {
		return shortenName(name+suffix, limit)
	}
	return shortenName(name, room) + suffix
}

func shortenName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	digest := nameDigest(name)[:ShortenNameDigestLength]
	prefixLen := limit - ShortenNameDigestLength - 1 // -1 for the joining "-"
	if prefixLen < 0 {
		return digest[:max(limit, 0)]
	}
	prefix := strings.TrimRight(name[:prefixLen], "-.")
	if prefix == "" {
		return digest
	}
	return prefix + "-" + digest
}

func nameDigest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}
