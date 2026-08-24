package components

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestHelmchartConfig_GenerateTemplate_HTTP(t *testing.T) {
	twoConfigMaps := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: first
data:
  key: value
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
data:
  key: value`)

	cfg := &HelmchartConfig{
		Name:       "myapp",
		Namespace:  "default",
		Delivery:   "template",
		Chart:      "myapp",
		SourceURL:  "https://charts.example.com",
		SourceKind: "HelmRepository",
		renderChart: func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
			if chartURL != "https://charts.example.com/myapp" {
				t.Errorf("chartURL = %q, want https://charts.example.com/myapp", chartURL)
			}
			return twoConfigMaps, nil
		},
	}

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	var count int
	for _, g := range cfg.hookGroups {
		count += len(g.Resources)
	}
	if count != 2 {
		t.Fatalf("expected 2 objects, got %d", count)
	}
}

func TestHelmchartConfig_GenerateTemplate_OCI(t *testing.T) {
	oneConfigMap := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
data:
  key: value`)

	cfg := &HelmchartConfig{
		Name:       "myapp",
		Namespace:  "default",
		Delivery:   "template",
		SourceURL:  "oci://ghcr.io/example/charts/myapp",
		SourceKind: "OCIRepository",
		Version:    "1.2.3",
		renderChart: func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
			// OCI: chartURL must equal SourceURL as-is (no chart name appended)
			if chartURL != "oci://ghcr.io/example/charts/myapp" {
				t.Errorf("chartURL = %q, want oci://ghcr.io/example/charts/myapp", chartURL)
			}
			if version != "1.2.3" {
				t.Errorf("version = %q, want 1.2.3", version)
			}
			return oneConfigMap, nil
		},
	}

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	var count int
	for _, g := range cfg.hookGroups {
		count += len(g.Resources)
	}
	if count != 1 {
		t.Fatalf("expected 1 object, got %d", count)
	}
}

func TestDecodeKubeManifests_ErrorOnMalformedYAML(t *testing.T) {
	_, err := decodeKubeManifests([]byte("key: [unclosed"))
	if err == nil {
		t.Fatal("expected error on malformed YAML")
	}
}

func TestDecodeKubeManifests_ErrorOnMappingWithoutAPIVersion(t *testing.T) {
	_, err := decodeKubeManifests([]byte("kind: ConfigMap\nmetadata:\n  name: cm"))
	if err == nil {
		t.Fatal("expected error for map without apiVersion")
	}
}

func TestDecodeKubeManifests_SkipsNonMapDoc(t *testing.T) {
	yaml := []byte("just a string\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm")
	objects, err := decodeKubeManifests(yaml)
	if err != nil {
		t.Fatalf("decodeKubeManifests: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object (scalar doc skipped), got %d", len(objects))
	}
}

func TestValuesConfigMapName_TruncatesLongComponentName(t *testing.T) {
	// Case 1: a long name with no '.' anywhere near the 246-char truncation
	// boundary.
	plain := strings.Repeat("a", 253)
	gotPlain := valuesConfigMapName(plain)
	if len(gotPlain) > 253 {
		t.Errorf("plain: len(%q) = %d, want <= 253", gotPlain, len(gotPlain))
	}
	if errs := validation.IsDNS1123Subdomain(gotPlain); len(errs) != 0 {
		t.Errorf("plain: IsDNS1123Subdomain(%q) = %v, want no errors", gotPlain, errs)
	}
	if !strings.HasSuffix(gotPlain, "-values") {
		t.Errorf("plain: %q does not end in -values", gotPlain)
	}

	// Case 2: the actual truncation boundary (prefixLen = maxPrefix(246) -
	// hashLen(8) - 1 = 237, not 246 — the hash suffix eats into maxPrefix)
	// lands immediately after a literal '.' — dotBoundary[236] == '.', so
	// dotBoundary[:237] ends in ".", exercising the TrimRight(name, "-.")
	// cleanup that prevents a dangling '.' from being left at the end of the
	// truncated prefix.
	dotBoundary := strings.Repeat("a", 236) + "." + strings.Repeat("b", 16)
	if dotBoundary[236] != '.' {
		t.Fatalf("test setup: dotBoundary[236] = %q, want '.'", dotBoundary[236])
	}
	gotDot := valuesConfigMapName(dotBoundary)
	if len(gotDot) > 253 {
		t.Errorf("dotBoundary: len(%q) = %d, want <= 253", gotDot, len(gotDot))
	}
	if errs := validation.IsDNS1123Subdomain(gotDot); len(errs) != 0 {
		t.Errorf("dotBoundary: IsDNS1123Subdomain(%q) = %v, want no errors", gotDot, errs)
	}
	if strings.HasPrefix(gotDot, ".") || strings.Contains(gotDot, "..") {
		t.Errorf("dotBoundary: %q has a dangling '.' artifact from truncation", gotDot)
	}

	// Build a real configMap-mode config with the dot-boundary name and
	// non-empty Values: the ConfigMap name (from AugmentLayout) and the
	// HelmRelease's generated valuesFrom ref (from buildHelmRelease) must
	// both be byte-identical to each other and to the direct helper call
	// above — the same helper backs both call sites (see valuesConfigMapName's
	// doc comment).
	cfg := &HelmchartConfig{
		Name:       dotBoundary,
		Namespace:  "default",
		ValuesMode: "configMap",
		Values:     map[string]any{"replicaCount": 1},
	}
	wrapped := wrapIfHelmchartAugmenter(cfg)
	aug, ok := wrapped.(interface {
		AugmentLayout(*layout.ManifestLayout) error
	})
	if !ok {
		t.Fatal("configMap-mode config with non-empty Values does not implement LayoutAugmenter")
	}
	ml := &layout.ManifestLayout{}
	if err := aug.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Resources) != 1 {
		t.Fatalf("ml.Resources has %d entries, want 1", len(ml.Resources))
	}
	cm, ok := ml.Resources[0].(*corev1.ConfigMap)
	if !ok {
		t.Fatalf("ml.Resources[0] = %T, want *corev1.ConfigMap", ml.Resources[0])
	}

	hr := cfg.buildHelmRelease()
	if len(hr.Spec.ValuesFrom) == 0 {
		t.Fatal("buildHelmRelease produced no ValuesFrom entries")
	}

	if cm.Name != gotDot {
		t.Errorf("ConfigMap name = %q, want %q (direct helper call)", cm.Name, gotDot)
	}
	if hr.Spec.ValuesFrom[0].Name != gotDot {
		t.Errorf("HelmRelease ValuesFrom[0].Name = %q, want %q (direct helper call)", hr.Spec.ValuesFrom[0].Name, gotDot)
	}
}

// TestValuesConfigMapName_TruncationPreservesUniqueness pins that two distinct
// valid component names (each within validate.go's 253-char DNS-1123 max)
// sharing the same first 246 characters still produce distinct ConfigMap
// names. Component names are unique only in full (validate.go's
// duplicate-name check), so a plain 246-char truncation would map both to the
// identical name — silently sharing, and one clobbering, the other's values
// ConfigMap.
func TestValuesConfigMapName_TruncationPreservesUniqueness(t *testing.T) {
	shared := strings.Repeat("a", 246)
	nameA := shared + strings.Repeat("b", 7) // 253 chars total
	nameB := shared + strings.Repeat("c", 7) // 253 chars total, same 246-char prefix
	if nameA == nameB {
		t.Fatal("test setup: nameA and nameB must differ")
	}
	gotA := valuesConfigMapName(nameA)
	gotB := valuesConfigMapName(nameB)
	if gotA == gotB {
		t.Fatalf("valuesConfigMapName collided: nameA=%q nameB=%q both produced %q", nameA, nameB, gotA)
	}
	for _, got := range []string{gotA, gotB} {
		if len(got) > 253 {
			t.Errorf("len(%q) = %d, want <= 253", got, len(got))
		}
		if errs := validation.IsDNS1123Subdomain(got); len(errs) != 0 {
			t.Errorf("IsDNS1123Subdomain(%q) = %v, want no errors", got, errs)
		}
	}
}
