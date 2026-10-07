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
		{"ingress Ingress", "-ingress", func(t *testing.T, name string) string {
			return appliedObjectName(t, &IngressHandler{}, name, scopedIngressProps(""))
		}},
		{"ingress Ingress with a scope", "-ingress-external", func(t *testing.T, name string) string {
			return appliedObjectName(t, &IngressHandler{}, name, scopedIngressProps("external"))
		}},
		{"httproute HTTPRoute", "-httproute", func(t *testing.T, name string) string {
			return appliedObjectName(t, &HTTPRouteHandler{}, name, scopedHTTPRouteProps(""))
		}},
		{"httproute HTTPRoute with a scope", "-httproute-external", func(t *testing.T, name string) string {
			return appliedObjectName(t, &HTTPRouteHandler{}, name, scopedHTTPRouteProps("external"))
		}},
		{"volsync ReplicationSource", "-backup", func(t *testing.T, name string) string {
			// The ReplicationSource is named after the source PVC, not the component.
			return appliedObjectName(t, &VolSyncHandler{}, "web",
				map[string]any{"sourcePVC": name, "schedule": "0 3 * * *"})
		}},
		{"managed TLS Secret default", "-tls", func(t *testing.T, name string) string {
			return managedTLSSecretName(name)
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

// A scope is authored and can be so long that <component>-<kind>-<scope> has
// no room for a shortened component beside it. The whole name is shortened
// then, so the object name still fits, is valid, and differs per component.
func TestRoutingObjectName_OversizedScope(t *testing.T) {
	scope := strings.Repeat("s", 245)
	handlers := map[string]struct {
		h     oam.TraitHandler
		props map[string]any
	}{
		"ingress":   {&IngressHandler{}, scopedIngressProps(scope)},
		"httproute": {&HTTPRouteHandler{}, scopedHTTPRouteProps(scope)},
	}
	for kind, tc := range handlers {
		got := appliedObjectName(t, tc.h, "web", tc.props)
		if want := wantShortened("web-"+kind+"-"+scope, "", oam.ShortenLimitSubdomain); got != want {
			t.Errorf("%s: got %q, want %q", kind, got, want)
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 {
			t.Errorf("%s: IsDNS1123Subdomain(%q) = %v, want no errors", kind, got, errs)
		}
		if other := appliedObjectName(t, tc.h, "api", tc.props); other == got {
			t.Errorf("%s: two components both gave %q", kind, got)
		}
	}
}

// appliedObjectName applies a trait to a component named component and returns
// the name of the one object the trait's sub-application generates.
func appliedObjectName(t *testing.T, h oam.TraitHandler, component string, props map[string]any) string {
	t.Helper()
	bundle := &stack.Bundle{}
	app := stack.NewApplication(component, "ns", &mockServicePortConfig{port: 80})
	if err := h.Apply(&oam.Trait{Properties: props}, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("Apply added %d sub-applications, want 1", len(bundle.Applications))
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil || len(objs) != 1 {
		t.Fatalf("Generate = (%d objects, %v), want one object", len(objs), err)
	}
	return (*objs[0]).GetName()
}

func scopedIngressProps(scope string) map[string]any {
	props := map[string]any{
		"ingressClassName": "nginx",
		"rules": []any{map[string]any{
			"host":  "example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}},
	}
	if scope != "" {
		props["scope"] = scope
	}
	return props
}

func scopedHTTPRouteProps(scope string) map[string]any {
	props := map[string]any{
		"parentRefs": []any{map[string]any{"name": "gw"}},
		"rules":      []any{map[string]any{}},
	}
	if scope != "" {
		props["scope"] = scope
	}
	return props
}

// An authored repository is an override: it is used as written, never
// shortened. At the longest name an object can have it is still kept whole,
// where the default for the same component would be shortened; one character
// more is refused (TestAuthoredObjectName_UsedAsWrittenOrRefused).
func TestVolsyncRepository_AuthoredIsNeverShortened(t *testing.T) {
	authored := strings.Repeat("r", oam.ShortenLimitSubdomain)
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
