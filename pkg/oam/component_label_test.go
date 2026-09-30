package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// validComponentName fails the test unless name is a valid component name (a
// DNS-1123 subdomain) of the given length, so a case cannot silently test a
// name the parser would refuse.
func validComponentName(t *testing.T, name string, wantLen int) string {
	t.Helper()
	if len(name) != wantLen {
		t.Fatalf("test name is %d characters, want %d", len(name), wantLen)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Fatalf("test name %q is not a valid component name: %v", name, errs)
	}
	return name
}

// dottedName builds a valid DNS-1123 subdomain of exactly n characters out of
// 50-character labels joined by dots, ending in an alphanumeric.
func dottedName(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for b.Len() < n {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString("component-label-projection-regression-guard-abcdef")
	}
	name := strings.TrimRight(b.String()[:n], "-.")
	for len(name) < n {
		name += "x"
	}
	return validComponentName(t, name, n)
}

func wantDigest(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:ComponentLabelDigestLength]
}

func assertValidLabelValue(t *testing.T, name, got string) {
	t.Helper()
	if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
		t.Errorf("ComponentLabelValue(%d-char name) = %q, not a valid label value: %v", len(name), got, errs)
	}
	if len(got) > validation.LabelValueMaxLength {
		t.Errorf("ComponentLabelValue(%d-char name) is %d characters, want at most %d", len(name), len(got), validation.LabelValueMaxLength)
	}
}

// TestComponentLabelValue_IdentityUpTo63 pins the no-output-change half of
// go-kure/launcher#572: every name that already was a valid label value is
// returned byte-for-byte.
func TestComponentLabelValue_IdentityUpTo63(t *testing.T) {
	for _, name := range []string{
		"a",
		"web",
		"batch.worker",
		"1api",
		validComponentName(t, "a"+strings.Repeat("b-", 30)+"cd", 63),
		dottedName(t, 63),
	} {
		if got := ComponentLabelValue(name); got != name {
			t.Errorf("ComponentLabelValue(%q) = %q, want the name unchanged", name, got)
		}
	}
}

// TestComponentLabelValue_ProjectsLongNames covers the projected half: a valid
// label value of at most 63 characters, made of a prefix of the name and the
// digest of the whole name.
func TestComponentLabelValue_ProjectsLongNames(t *testing.T) {
	for _, n := range []int{64, 100, 200, 253} {
		for _, name := range []string{
			validComponentName(t, strings.Repeat("a", n), n),
			dottedName(t, n),
		} {
			got := ComponentLabelValue(name)
			assertValidLabelValue(t, name, got)
			if !strings.HasSuffix(got, "-"+wantDigest(name)) {
				t.Errorf("ComponentLabelValue(%d-char name) = %q, want it to end in -<first %d hex of sha256(name)> %q",
					n, got, ComponentLabelDigestLength, wantDigest(name))
			}
			prefix := strings.TrimSuffix(got, "-"+wantDigest(name))
			if !strings.HasPrefix(name, prefix) {
				t.Errorf("ComponentLabelValue(%d-char name) = %q: %q is not a prefix of the name", n, got, prefix)
			}
		}
	}
	// The all-letters case fills the value exactly: nothing to trim.
	if got := ComponentLabelValue(strings.Repeat("a", 64)); len(got) != validation.LabelValueMaxLength {
		t.Errorf("ComponentLabelValue(64 x 'a') is %d characters, want exactly %d", len(got), validation.LabelValueMaxLength)
	}
}

// TestComponentLabelValue_TrimsSeparatorsBeforeDigest: when the 52-character cut
// lands on a '-' or '.', the prefix drops it (and any run of them) instead of
// emitting a doubled separator before the digest.
func TestComponentLabelValue_TrimsSeparatorsBeforeDigest(t *testing.T) {
	prefixLen := validation.LabelValueMaxLength - ComponentLabelDigestLength - 1 // 52
	for _, tc := range []struct {
		name       string
		base       string // the first prefixLen characters of the name
		wantPrefix string
	}{
		{"cut on a dash", strings.Repeat("a", prefixLen-1) + "-", strings.Repeat("a", prefixLen-1)},
		{"cut on a dot", strings.Repeat("a", prefixLen-1) + ".", strings.Repeat("a", prefixLen-1)},
		{"cut on a dash run", strings.Repeat("a", prefixLen-3) + "---", strings.Repeat("a", prefixLen-3)},
		{"cut on an alphanumeric", strings.Repeat("a", prefixLen-2) + "-a", strings.Repeat("a", prefixLen-2) + "-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := validComponentName(t, tc.base+strings.Repeat("b", 20), prefixLen+20)
			got := ComponentLabelValue(name)
			assertValidLabelValue(t, name, got)
			if want := tc.wantPrefix + "-" + wantDigest(name); got != want {
				t.Errorf("ComponentLabelValue = %q, want %q", got, want)
			}
		})
	}
}

// TestComponentLabelValue_Deterministic: the same name always projects to the
// same value, so a label and a selector computed separately agree.
func TestComponentLabelValue_Deterministic(t *testing.T) {
	name := dottedName(t, 200)
	first := ComponentLabelValue(name)
	for range 5 {
		if got := ComponentLabelValue(strings.Clone(name)); got != first {
			t.Fatalf("ComponentLabelValue is not deterministic: %q then %q", first, got)
		}
	}
}

// TestComponentLabelValue_DistinctNamesSharingThePrefix: two names that share
// the whole 52-character prefix still project to different values, which a
// plain truncation to 63 characters would not give them.
func TestComponentLabelValue_DistinctNamesSharingThePrefix(t *testing.T) {
	shared := strings.Repeat("s", 70)
	a := validComponentName(t, shared+"-alpha", 76)
	b := validComponentName(t, shared+"-bravo", 76)
	ga, gb := ComponentLabelValue(a), ComponentLabelValue(b)
	if ga == gb {
		t.Fatalf("ComponentLabelValue(%q) == ComponentLabelValue(%q) == %q, want distinct values", a, b, ga)
	}
	if ga[:40] != gb[:40] {
		t.Errorf("projected values %q and %q should share the readable prefix", ga, gb)
	}
	// Names that differ only past character 63 are the case truncation gets wrong.
	c := validComponentName(t, strings.Repeat("t", 63)+"-one", 67)
	d := validComponentName(t, strings.Repeat("t", 63)+"-two", 67)
	if ComponentLabelValue(c) == ComponentLabelValue(d) {
		t.Errorf("names differing only after character 63 project to the same value %q", ComponentLabelValue(c))
	}
}
