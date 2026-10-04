package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// wantShortened is the one shortening rule (go-kure/launcher#793), written out
// independently of ShortenName so the tests below pin the algorithm and not the
// implementation: name+suffix when that fits limit; otherwise the first
// limit-len(suffix)-11 characters of name with trailing '-' and '.' trimmed, a
// "-", the first 10 hex digits of the SHA-256 of the whole name, and suffix.
func wantShortened(name, suffix string, limit int) string {
	limit -= len(suffix)
	if len(name) <= limit {
		return name + suffix
	}
	sum := sha256.Sum256([]byte(name))
	prefix := strings.TrimRight(name[:limit-11], "-.")
	return prefix + "-" + hex.EncodeToString(sum[:])[:10] + suffix
}

func TestShortenName(t *testing.T) {
	long := strings.Repeat("a", 300)
	sum := sha256.Sum256([]byte(long))
	digest := hex.EncodeToString(sum[:])

	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"fits a label", strings.Repeat("a", 63), ShortenLimitLabel, strings.Repeat("a", 63)},
		{"fits a subdomain", strings.Repeat("a", 253), ShortenLimitSubdomain, strings.Repeat("a", 253)},
		{"fits a Helm release name", strings.Repeat("a", 53), ShortenLimitHelmRelease, strings.Repeat("a", 53)},
		{"over a label", long, ShortenLimitLabel, strings.Repeat("a", 52) + "-" + digest[:10]},
		{"over a subdomain", long, ShortenLimitSubdomain, strings.Repeat("a", 242) + "-" + digest[:10]},
		// The one exception: Flux helm-controller's release-name rule, the first
		// 40 characters, "-", 12 hex digits.
		{"over a Helm release name", long, ShortenLimitHelmRelease, strings.Repeat("a", 40) + "-" + digest[:12]},
		{"a limit that is none of the three", long, 100, strings.Repeat("a", 89) + "-" + digest[:10]},
		{"empty", "", ShortenLimitLabel, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShortenName(tt.in, tt.limit)
			if got != tt.want {
				t.Errorf("ShortenName(%d chars, %d) = %q, want %q", len(tt.in), tt.limit, got, tt.want)
			}
			if len(got) > tt.limit {
				t.Errorf("len = %d, over the limit %d", len(got), tt.limit)
			}
			if again := ShortenName(tt.in, tt.limit); again != got {
				t.Errorf("not deterministic: %q then %q", got, again)
			}
		})
	}
}

// A prefix cut that ends in a separator is trimmed, so the result never
// carries "--" or ".-"; a prefix made of separators only leaves the digest.
func TestShortenName_TrimsTrailingSeparators(t *testing.T) {
	name := strings.Repeat("a", 50) + ".." + strings.Repeat("b", 40) // name[:52] ends in ".."
	got := ShortenName(name, ShortenLimitLabel)
	if want := wantShortened(name, "", ShortenLimitLabel); got != want {
		t.Fatalf("ShortenName = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 50)+"-") || len(got) != 50+1+10 {
		t.Errorf("ShortenName = %q, want the 50 a's, one '-' and 10 hex digits", got)
	}

	separators := strings.Repeat("-", 80)
	sum := sha256.Sum256([]byte(separators))
	if got, want := ShortenName(separators, ShortenLimitLabel), hex.EncodeToString(sum[:])[:10]; got != want {
		t.Errorf("ShortenName(80 dashes) = %q, want the bare digest %q", got, want)
	}
}

// Flux does not trim: the 40-character cut is kept as it is, so the name equals
// what helm-controller computes for the same HelmRelease.
func TestShortenName_HelmReleaseKeepsTheCut(t *testing.T) {
	name := strings.Repeat("a", 39) + "-" + strings.Repeat("b", 30)
	sum := sha256.Sum256([]byte(name))
	want := strings.Repeat("a", 39) + "--" + hex.EncodeToString(sum[:])[:12]
	if got := ShortenName(name, ShortenLimitHelmRelease); got != want {
		t.Errorf("ShortenName = %q, want %q", got, want)
	}
}

// A limit with no room for a prefix, the "-" and the digest yields the digest
// cut to the limit, so the result is still deterministic and within the limit.
func TestShortenName_LimitBelowTheDigest(t *testing.T) {
	name := strings.Repeat("a", 40)
	sum := sha256.Sum256([]byte(name))
	digest := hex.EncodeToString(sum[:])
	for limit, want := range map[int]string{11: digest[:10], 10: digest[:10], 4: digest[:4], 0: "", -3: ""} {
		if got := ShortenName(name, limit); got != want {
			t.Errorf("ShortenName(40 chars, %d) = %q, want %q", limit, got, want)
		}
	}
}

// ShortenNameWithSuffix keeps a suffix whole when it leaves room for a digest
// beside it, and never switches to the Helm release rule, whatever is left for
// the name.
func TestShortenNameWithSuffix(t *testing.T) {
	long := strings.Repeat("a", 300)
	for _, suffix := range []string{"", "-hpa", "-values-0123456789", strings.Repeat("s", 200)} {
		got := ShortenNameWithSuffix(long, suffix, ShortenLimitSubdomain)
		if want := wantShortened(long, suffix, ShortenLimitSubdomain); got != want {
			t.Errorf("suffix %q: got %q, want %q", suffix, got, want)
		}
		if len(got) != ShortenLimitSubdomain || !strings.HasSuffix(got, suffix) {
			t.Errorf("suffix %q: %q has length %d or lost its suffix", suffix, got, len(got))
		}
	}
	if got := ShortenNameWithSuffix("web", "-hpa", ShortenLimitSubdomain); got != "web-hpa" {
		t.Errorf("a name that fits = %q, want web-hpa", got)
	}
	// 63 less a 10-character suffix leaves 53: still the general rule.
	suffix := "-123456789"
	if got, want := ShortenNameWithSuffix(long, suffix, ShortenLimitLabel), wantShortened(long, suffix, ShortenLimitLabel); got != want {
		t.Errorf("53 characters left for the name: got %q, want %q", got, want)
	}
}

// A suffix that leaves no room for a full digest beside it cannot be kept
// whole: the whole name+suffix is shortened by the general rule, so the
// result still fits, is valid, and tells two names apart by a full digest.
func TestShortenNameWithSuffix_SuffixLeavesNoRoom(t *testing.T) {
	for _, n := range []int{235, 244, 253, 300} { // 9 characters left, none, less than none
		suffix := "-ingress-" + strings.Repeat("s", n)
		got, other := ShortenNameWithSuffix("web", suffix, ShortenLimitSubdomain), ShortenNameWithSuffix("api", suffix, ShortenLimitSubdomain)
		if want := wantShortened("web"+suffix, "", ShortenLimitSubdomain); got != want {
			t.Errorf("suffix of %d characters: got %q, want %q", len(suffix), got, want)
		}
		if len(got) > ShortenLimitSubdomain {
			t.Errorf("suffix of %d characters: length %d, over 253", len(suffix), len(got))
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
			t.Errorf("suffix of %d characters: IsDNS1123Subdomain = %v", len(suffix), errs)
		}
		if got == other {
			t.Errorf("suffix of %d characters: two names both gave %q", len(suffix), got)
		}
	}
	// Exactly a digest's room left: the suffix is kept, the name is its digest.
	suffix := "-" + strings.Repeat("s", ShortenLimitSubdomain-ShortenNameDigestLength-1)
	long := strings.Repeat("a", 20)
	sum := sha256.Sum256([]byte(long))
	if got, want := ShortenNameWithSuffix(long, suffix, ShortenLimitSubdomain), hex.EncodeToString(sum[:])[:10]+suffix; got != want {
		t.Errorf("a digest's room left: got %q, want %q", got, want)
	}
}

// TestShortenName_GeneratingSites is the acceptance test of
// go-kure/launcher#793 for this package: every site that generates a name
// shows the one rule, is deterministic, and keeps two names apart that share
// everything the shortened name keeps of them. The sites of the built-in
// components and traits have the same table in their own packages.
func TestShortenName_GeneratingSites(t *testing.T) {
	ep := validEndpoint()
	endpointSuffix := strings.TrimPrefix(endpointIngressPolicyName("pg", ep, true), "pg")
	origin := Origin{Document: "doc", Namespace: "ns"}

	sites := []struct {
		site   string
		limit  int
		suffix string
		gen    func(t *testing.T, name string) string
	}{
		{"ComponentLabelValue", ShortenLimitLabel, "", func(_ *testing.T, name string) string {
			return ComponentLabelValue(name)
		}},
		{"NameAllocator.Name", ShortenLimitSubdomain, "-pooler", func(t *testing.T, name string) string {
			got, err := NewNameAllocator().Name(name, "pooler", origin)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			return got
		}},
		{"NameAllocator.NameOrAdopt", ShortenLimitSubdomain, "-source-0123456789", func(t *testing.T, name string) string {
			got, adopted, err := NewNameAllocator().NameOrAdopt(name, "source-0123456789", "identity", origin)
			if err != nil || adopted {
				t.Fatalf("NameOrAdopt = (%v, %v), want (false, nil)", adopted, err)
			}
			return got
		}},
		{"component ingress NetworkPolicy", ShortenLimitSubdomain, "-allow-ingress-traffic", func(_ *testing.T, name string) string {
			return ingressTrafficPolicyName(name)
		}},
		{"component egress NetworkPolicy", ShortenLimitSubdomain, "-allow-egress-traffic", func(_ *testing.T, name string) string {
			return egressTrafficPolicyName(name)
		}},
		{"endpoint ingress NetworkPolicy, one endpoint", ShortenLimitSubdomain, "-allow-endpoint-ingress", func(_ *testing.T, name string) string {
			return endpointIngressPolicyName(name, ep, false)
		}},
		{"endpoint ingress NetworkPolicy, several endpoints", ShortenLimitSubdomain, endpointSuffix, func(_ *testing.T, name string) string {
			return endpointIngressPolicyName(name, ep, true)
		}},
		{"endpoint ingress NetworkPolicy, default of a directly built config", ShortenLimitSubdomain, "-allow-endpoint-ingress", func(_ *testing.T, name string) string {
			return (&componentEndpointIngressPolicyConfig{ComponentName: name}).policyName()
		}},
	}
	for _, s := range sites {
		t.Run(s.site, func(t *testing.T) {
			assertOneShorteningRule(t, s.limit, s.suffix, func(name string) string { return s.gen(t, name) })
		})
	}
}

// assertOneShorteningRule checks one generating site against the rule: a name
// that fits is kept, one that does not is shortened as wantShortened says,
// identically on every call, within the limit, valid for its target, and
// different for two names that share the whole kept prefix.
func assertOneShorteningRule(t *testing.T, limit int, suffix string, gen func(name string) string) {
	t.Helper()
	if got, want := gen("web"), "web"+suffix; got != want {
		t.Errorf("a name that fits = %q, want %q", got, want)
	}
	shared := strings.Repeat("a", 240)
	longA := shared + "." + strings.Repeat("b", 12) // 253 characters, a valid component name
	longB := shared + "." + strings.Repeat("c", 12)
	gotA, gotB := gen(longA), gen(longB)
	for name, got := range map[string]string{longA: gotA, longB: gotB} {
		if want := wantShortened(name, suffix, limit); got != want {
			t.Errorf("shortened = %q, want %q", got, want)
		}
		if len(got) > limit {
			t.Errorf("len(%q) = %d, over the limit %d", got, len(got), limit)
		}
		if again := gen(name); again != got {
			t.Errorf("not deterministic: %q then %q", got, again)
		}
		errs := validation.IsDNS1123Subdomain(got)
		if limit == ShortenLimitLabel {
			errs = validation.IsValidLabelValue(got)
		}
		if len(errs) > 0 {
			t.Errorf("%q is not valid for its target: %v", got, errs)
		}
	}
	if gotA == gotB {
		t.Errorf("two names sharing their first 240 characters both gave %q", gotA)
	}
}

// The allocator shortens an over-long name instead of refusing it, and the
// shortened name is the one it reserves: a second claim of it collides.
func TestNameAllocator_ShortenedNameTakesPartInCollisionDetection(t *testing.T) {
	origin := Origin{Document: "doc", Namespace: "ns"}
	base := strings.Repeat("a", 250)
	n := NewNameAllocator()
	name, err := n.Name(base, "pooler", origin)
	if err != nil {
		t.Fatalf("Name(250 characters, pooler): %v", err)
	}
	if want := wantShortened(base, "-pooler", ShortenLimitSubdomain); name != want {
		t.Fatalf("Name = %q, want %q", name, want)
	}
	if _, err := n.Name(base, "pooler", origin); err == nil {
		t.Error("a second Name with the same base and suffix did not collide")
	}
	if err := n.Reserve(name, origin); err == nil {
		t.Error("reserving the shortened name again did not collide")
	}
	other := strings.Repeat("a", 249) + "b"
	if _, err := n.Name(other, "pooler", origin); err != nil {
		t.Errorf("a different long base sharing the kept prefix collided: %v", err)
	}

	name, adopted, err := n.NameOrAdopt(base, "source-0123456789", "identity", origin)
	if err != nil || adopted {
		t.Fatalf("NameOrAdopt = (%q, %v, %v), want a first claim", name, adopted, err)
	}
	if _, adopted, err := n.NameOrAdopt(base, "source-0123456789", "identity", origin); err != nil || !adopted {
		t.Errorf("same identity under the shortened name = (%v, %v), want adopted", adopted, err)
	}
	if _, _, err := n.NameOrAdopt(base, "source-0123456789", "other identity", origin); err == nil {
		t.Error("different content under the shortened name did not collide")
	}
}

// Shortening does not make an invalid name valid: the allocator still refuses
// a name that is not a DNS-1123 subdomain.
func TestNameAllocator_StillRefusesAnInvalidName(t *testing.T) {
	origin := Origin{Document: "doc", Namespace: "ns"}
	for _, base := range []string{"Upper", strings.Repeat("A", 300)} {
		_, err := NewNameAllocator().Name(base, "pooler", origin)
		if err == nil || !strings.Contains(err.Error(), "not a valid DNS-1123 subdomain") {
			t.Errorf("Name(%d characters) error = %v, want the DNS-1123 refusal", len(base), err)
		}
	}
}
