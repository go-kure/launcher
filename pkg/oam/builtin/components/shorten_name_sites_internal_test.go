package components

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/oam"
)

// wantShortened is the one shortening rule (go-kure/launcher#793), written out
// independently of oam.ShortenName: name+suffix when that fits limit; otherwise
// the first limit-len(suffix)-11 characters of name with trailing '-' and '.'
// trimmed, a "-", the first 10 hex digits of the SHA-256 of the whole name,
// and suffix.
func wantShortened(name, suffix string, limit int) string {
	limit -= len(suffix)
	if len(name) <= limit {
		return name + suffix
	}
	sum := sha256.Sum256([]byte(name))
	prefix := strings.TrimRight(name[:limit-11], "-.")
	return prefix + "-" + hex.EncodeToString(sum[:])[:10] + suffix
}

// TestShortenName_GeneratingSites is the acceptance test of
// go-kure/launcher#793 for the built-in components: each site that generates a
// name shows the one rule, is deterministic, stays a valid object name, and
// keeps two names apart that share everything the shortened name keeps of
// them. Component names are unique only in full, so a plain truncation would
// give both the same object, one silently clobbering the other's.
func TestShortenName_GeneratingSites(t *testing.T) {
	valuesDigest := strings.Repeat("0123456789abcdef", 4)
	sites := []struct {
		site   string
		suffix string
		gen    func(name string) string
	}{
		{"helm values ConfigMap", "-values-0123456789", func(name string) string {
			return helmValuesConfigMapName(name, valuesDigest)
		}},
		{"hook-group child layout", "-00-pre-install", func(name string) string {
			return hookGroupChildName(name, 0, helm.HookGroup{Phase: "pre-install"})
		}},
		{"hook-group child layout, a three-digit index and a capped phase", "-100-" + strings.Repeat("x", 40), func(name string) string {
			return hookGroupChildName(name, 100, helm.HookGroup{Phase: strings.Repeat("x", 80)})
		}},
	}
	shared := strings.Repeat("a", 240)
	longA := shared + "." + strings.Repeat("b", 12) // 253 characters, a valid component name
	longB := shared + "." + strings.Repeat("c", 12)
	for _, s := range sites {
		t.Run(s.site, func(t *testing.T) {
			if got, want := s.gen("web"), "web"+s.suffix; got != want {
				t.Errorf("a name that fits = %q, want %q", got, want)
			}
			gotA, gotB := s.gen(longA), s.gen(longB)
			for name, got := range map[string]string{longA: gotA, longB: gotB} {
				if want := wantShortened(name, s.suffix, oam.ShortenLimitSubdomain); got != want {
					t.Errorf("shortened = %q, want %q", got, want)
				}
				if len(got) > oam.ShortenLimitSubdomain {
					t.Errorf("len(%q) = %d, over 253", got, len(got))
				}
				if !strings.HasSuffix(got, s.suffix) {
					t.Errorf("%q lost its suffix %q", got, s.suffix)
				}
				if again := s.gen(name); again != got {
					t.Errorf("not deterministic: %q then %q", got, again)
				}
				if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
					t.Errorf("IsDNS1123Subdomain(%q) = %v, want no errors", got, errs)
				}
			}
			if gotA == gotB {
				t.Errorf("two names sharing their first 240 characters both gave %q", gotA)
			}
		})
	}
}
