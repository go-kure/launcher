package traits

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
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
// go-kure/launcher#793 for the built-in traits: each object name a trait
// generates by default shows the one rule, is deterministic, stays a valid
// object name, and keeps two names apart that share everything the shortened
// name keeps of them.
func TestShortenName_GeneratingSites(t *testing.T) {
	sites := []struct {
		site   string
		suffix string
		gen    func(t *testing.T, name string) string
	}{
		{"scaler HorizontalPodAutoscaler", "-hpa", func(_ *testing.T, name string) string {
			c := &ScalerConfig{componentName: name, MinReplicas: 1, MaxReplicas: 2}
			return c.buildHPA(stack.NewApplication(name, "ns", c), nil).Name
		}},
		{"scaler PodDisruptionBudget", "-pdb", func(_ *testing.T, name string) string {
			c := &ScalerConfig{componentName: name, MinReplicas: 2, MaxReplicas: 3, EnablePDB: true}
			return c.buildPDB(stack.NewApplication(name, "ns", c), nil).Name
		}},
		{"networkpolicy NetworkPolicy", "-allow", func(t *testing.T, name string) string {
			c := &NetworkPolicyConfig{componentName: name}
			objs, err := c.Generate(stack.NewApplication(name, "ns", c))
			if err != nil || len(objs) != 1 {
				t.Fatalf("Generate = (%d objects, %v), want one object", len(objs), err)
			}
			return (*objs[0]).GetName()
		}},
		{"volsync repository Secret default", "-volsync-secret", func(t *testing.T, name string) string {
			c, err := (&VolSyncHandler{}).parseProperties(
				map[string]any{"sourcePVC": "data", "schedule": "0 3 * * *"},
				stack.NewApplication(name, "ns", nil))
			if err != nil {
				t.Fatalf("parseProperties: %v", err)
			}
			return c.Repository
		}},
	}
	shared := strings.Repeat("a", 240)
	longA := shared + "." + strings.Repeat("b", 12) // 253 characters, a valid component name
	longB := shared + "." + strings.Repeat("c", 12)
	for _, s := range sites {
		t.Run(s.site, func(t *testing.T) {
			if got, want := s.gen(t, "web"), "web"+s.suffix; got != want {
				t.Errorf("a name that fits = %q, want %q", got, want)
			}
			gotA, gotB := s.gen(t, longA), s.gen(t, longB)
			for name, got := range map[string]string{longA: gotA, longB: gotB} {
				if want := wantShortened(name, s.suffix, oam.ShortenLimitSubdomain); got != want {
					t.Errorf("shortened = %q, want %q", got, want)
				}
				if len(got) > oam.ShortenLimitSubdomain {
					t.Errorf("len(%q) = %d, over 253", got, len(got))
				}
				if again := s.gen(t, name); again != got {
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

// An authored repository is an override: it is used as written, never
// shortened.
func TestVolsyncRepository_AuthoredIsNeverShortened(t *testing.T) {
	authored := strings.Repeat("r", 300)
	c, err := (&VolSyncHandler{}).parseProperties(
		map[string]any{"sourcePVC": "data", "schedule": "0 3 * * *", "repository": authored},
		stack.NewApplication("web", "ns", nil))
	if err != nil {
		t.Fatalf("parseProperties: %v", err)
	}
	if c.Repository != authored {
		t.Errorf("Repository = %q, want the authored value unchanged", c.Repository)
	}
}
