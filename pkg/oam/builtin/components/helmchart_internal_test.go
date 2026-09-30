package components

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// TestDecodeKubeManifests_JSONTypedAndWrittenUnchanged covers each Go type
// yaml.v3 yields for a scalar a chart can render: the decoded value, at the
// top of spec, in a list and in a nested mapping, must be one
// runtime.DeepCopyJSONValue accepts (it panics on yaml.v3's int, uint64 and
// time.Time), and the object must encode to the same JSON as the yaml.v3
// decode alone — kure writes an object from its JSON encoding, so the written
// file is unchanged. .inf and .nan stay float64, which encoding/json refuses
// to write either way.
func TestDecodeKubeManifests_JSONTypedAndWrittenUnchanged(t *testing.T) {
	cases := []struct {
		scalar string
		want   any
	}{
		{"0", int64(0)},
		{"8080", int64(8080)},
		{"-1", int64(-1)},
		{"0x1F", int64(31)},
		{"9223372036854775807", int64(math.MaxInt64)},
		{"9223372036854775808", json.Number("9223372036854775808")},
		{"18446744073709551615", json.Number("18446744073709551615")},
		{"18446744073709551616", 1.8446744073709552e19},
		{"1.5", 1.5},
		{"1.0", 1.0},
		{".inf", math.Inf(1)},
		{".nan", math.NaN()},
		{"2001-12-14", "2001-12-14T00:00:00Z"},
		{"2001-12-14t21:59:43.10-05:00", "2001-12-14T21:59:43.1-05:00"},
		{"!!binary aGVsbG8=", "hello"},
		{"true", true},
		{"null", nil},
		{"text", "text"},
	}
	same := func(got, want any) bool {
		g, gok := got.(float64)
		w, wok := want.(float64)
		if gok && wok && math.IsNaN(g) && math.IsNaN(w) {
			return true
		}
		return reflect.DeepEqual(got, want)
	}
	for _, tc := range cases {
		t.Run(tc.scalar, func(t *testing.T) {
			doc := "apiVersion: example.com/v1\nkind: Thing\nmetadata:\n  name: t\nspec:\n  v: " + tc.scalar +
				"\n  list:\n  - " + tc.scalar + "\n  nested:\n    v: " + tc.scalar + "\n"
			objects, err := decodeKubeManifests([]byte(doc))
			if err != nil {
				t.Fatalf("decodeKubeManifests: %v", err)
			}
			if len(objects) != 1 {
				t.Fatalf("got %d objects, want 1", len(objects))
			}
			u := objects[0].(*unstructured.Unstructured)
			spec := u.Object["spec"].(map[string]any)
			for where, got := range map[string]any{
				"spec.v":        spec["v"],
				"spec.list[0]":  spec["list"].([]any)[0],
				"spec.nested.v": spec["nested"].(map[string]any)["v"],
			} {
				if !same(got, tc.want) {
					t.Errorf("%s = %#v (%T), want %#v (%T)", where, got, got, tc.want, tc.want)
				}
			}
			assertDeepCopyable(t, objects)

			var plain map[string]any
			if err := yaml.Unmarshal([]byte(doc), &plain); err != nil {
				t.Fatalf("yaml.v3 decode: %v", err)
			}
			wantJSON, wantErr := json.Marshal(plain)
			gotJSON, gotErr := json.Marshal(u.Object)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || string(gotJSON) != string(wantJSON) {
				t.Errorf("JSON = %s (err %v), want the yaml.v3 decode's %s (err %v)", gotJSON, gotErr, wantJSON, wantErr)
			}
		})
	}
}

// TestDecodeKubeManifests_NonStringMappingKeyIsAnError: yaml.v3 decodes a
// mapping with a non-string key to map[any]any, which
// runtime.DeepCopyJSONValue panics on and encoding/json refuses to write. It
// is an error naming the object and the mapping, not a later panic.
func TestDecodeKubeManifests_NonStringMappingKeyIsAnError(t *testing.T) {
	_, err := decodeKubeManifests([]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\ndata:\n  1: one\n"))
	if err == nil {
		t.Fatal("expected an error for a mapping with a non-string key")
	}
	for _, want := range []string{`ConfigMap "cm"`, ".data", "not a string"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
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

// helmchartTemplateFixture returns a *HelmchartConfig configured for
// delivery: template with the given renderChart stub — the shared shape used
// by every white-box template-delivery test below.
func helmchartTemplateFixture(renderChart func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error)) *HelmchartConfig {
	return &HelmchartConfig{
		Name:        "myapp",
		Namespace:   "default",
		Delivery:    "template",
		Chart:       "myapp",
		SourceURL:   "https://charts.example.com",
		SourceKind:  "HelmRepository",
		renderChart: renderChart,
	}
}

func TestEnsureRendered_CachesRender(t *testing.T) {
	calls := 0
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		calls++
		return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
	})

	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := cfg.augmentLayoutTemplate(&layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}
	if calls != 1 {
		t.Errorf("renderChart called %d times, want 1 (Generate then AugmentLayout must render exactly once)", calls)
	}
}

func TestGenerate_FlattensHookGroupsInExecutionOrder(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	var names []string
	for _, o := range objects {
		u, ok := (*o).(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("object = %T, want *unstructured.Unstructured", *o)
		}
		names = append(names, u.GetName())
	}
	want := []string{"pre", "main", "post"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("execution order = %v, want %v", names, want)
			break
		}
	}
}

func TestAugmentLayoutTemplate_SingleGroup_NoChildren(t *testing.T) {
	raw := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n")
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := cfg.augmentLayoutTemplate(ml); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}
	if len(ml.Children) != 0 {
		t.Errorf("ml.Children has %d entries, want 0 (a single hook group is a no-op)", len(ml.Children))
	}
	if ml.ApplicationFileMode != layout.AppFileUnset {
		t.Errorf("ml.ApplicationFileMode = %v, want AppFileUnset (the directory pin applies only when partitioning)", ml.ApplicationFileMode)
	}
}

func TestAugmentLayoutTemplate_MultiGroup_PartitionsAndChains(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{
		Name:                "myapp",
		Namespace:           "team", // the enclosing layout's path, as kure's walker sets it
		Resources:           []client.Object{&unstructured.Unstructured{}},
		Mode:                layout.KustomizationExplicit,
		FluxPlacement:       layout.FluxIntegratedPerLayout,
		FileNaming:          layout.FileNamingKindName,
		FilePer:             layout.FilePerKind,
		ApplicationFileMode: layout.AppFileSingle, // must NOT propagate to children
	}
	if err := cfg.augmentLayoutTemplate(ml); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}
	if ml.Resources != nil {
		t.Errorf("ml.Resources = %v, want nil after partitioning", ml.Resources)
	}
	if ml.ApplicationFileMode != layout.AppFileSingle {
		t.Errorf("ml.ApplicationFileMode = %v, want the caller's explicit AppFileSingle kept", ml.ApplicationFileMode)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}

	wantNames := []string{"myapp-00-pre-install", "myapp-01-main", "myapp-02-post-install"}
	var prevName string
	for i, child := range ml.Children {
		if child.Name != wantNames[i] {
			t.Errorf("Children[%d].Name = %q, want %q", i, child.Name, wantNames[i])
		}
		if child.Namespace != "team/myapp" {
			t.Errorf("Children[%d].Namespace = %q, want the parent path %q", i, child.Namespace, "team/myapp")
		}
		// One level below the component's directory, never nested twice.
		if wantPath := "team/myapp/" + wantNames[i]; child.FullRepoPath() != wantPath {
			t.Errorf("Children[%d].FullRepoPath() = %q, want %q", i, child.FullRepoPath(), wantPath)
		}
		if child.Mode != ml.Mode {
			t.Errorf("Children[%d].Mode = %v, want %v", i, child.Mode, ml.Mode)
		}
		if child.FluxPlacement != ml.FluxPlacement {
			t.Errorf("Children[%d].FluxPlacement = %v, want %v", i, child.FluxPlacement, ml.FluxPlacement)
		}
		if child.FileNaming != ml.FileNaming {
			t.Errorf("Children[%d].FileNaming = %v, want %v", i, child.FileNaming, ml.FileNaming)
		}
		if child.FilePer != ml.FilePer {
			t.Errorf("Children[%d].FilePer = %v, want %v", i, child.FilePer, ml.FilePer)
		}
		if child.ApplicationFileMode != layout.AppFileUnset {
			t.Errorf("Children[%d].ApplicationFileMode = %v, want AppFileUnset (must not inherit ml's AppFileSingle)", i, child.ApplicationFileMode)
		}
		if i == 0 {
			if len(child.DependsOn) != 0 {
				t.Errorf("Children[0].DependsOn = %v, want empty", child.DependsOn)
			}
		} else if len(child.DependsOn) != 1 || child.DependsOn[0] != prevName {
			t.Errorf("Children[%d].DependsOn = %v, want [%q]", i, child.DependsOn, prevName)
		}
		prevName = child.Name
	}
}

// TestAugmentLayoutTemplate_ChildKustomizationReferencesResolveOnDisk exercises
// the actual kure disk-writer, not just the in-memory ml/Children shape: every
// resources: entry of every kustomization.yaml written must resolve on disk,
// and an entry naming a directory must reach that directory's own
// kustomization.yaml. A child nested twice (its Namespace ending in its own
// name, go-kure/kure#771) leaves only an empty intermediate directory there,
// so a bare existence check would not catch it. The parent is an application
// layout as kure's walker hands it to the augmenter: Namespace its parent
// directory, ApplicationFileMode unset (directory mode). An AppFileSingle
// root would write its kustomization.yaml into its Namespace instead of its
// own directory, listing the children where they are not written, so it
// cannot host them; the children's own AppFileUnset (never inherited from the
// parent) is asserted in TestAugmentLayoutTemplate_MultiGroup_PartitionsAndChains.
func TestAugmentLayoutTemplate_ChildKustomizationReferencesResolveOnDisk(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	ml := &layout.ManifestLayout{
		Name:      "myapp",
		Namespace: "team",
	}
	if err := cfg.augmentLayoutTemplate(ml); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}
	if len(ml.Children) < 2 {
		t.Fatalf("test setup: expected multiple hook groups to produce children, got %d", len(ml.Children))
	}

	dir := t.TempDir()
	if err := ml.WriteToDisk(dir); err != nil {
		t.Fatalf("WriteToDisk: %v", err)
	}

	var kustFiles []string
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && info.Name() == "kustomization.yaml" {
			kustFiles = append(kustFiles, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(kustFiles) == 0 {
		t.Fatal("no kustomization.yaml was written")
	}

	resourceLine := regexp.MustCompile(`^  - (.+)$`)
	for _, kf := range kustFiles {
		data, err := os.ReadFile(kf)
		if err != nil {
			t.Fatalf("read %s: %v", kf, err)
		}
		kdir := filepath.Dir(kf)
		for line := range strings.SplitSeq(string(data), "\n") {
			m := resourceLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			ref := filepath.Join(kdir, m[1])
			fi, err := os.Stat(ref)
			if err != nil {
				t.Errorf("%s: resources entry %q does not resolve on disk (%v)", kf, m[1], err)
				continue
			}
			if fi.IsDir() {
				if _, err := os.Stat(filepath.Join(ref, "kustomization.yaml")); err != nil {
					t.Errorf("%s: resources entry %q is a directory without its own kustomization.yaml (%v)", kf, m[1], err)
				}
			}
		}
	}
}

// TestAugmentLayoutTemplate_WriteManifestUnderAppFileSingleDefault pins
// go-kure/launcher#563: a hook-group helmchart walked by kure's WalkCluster
// and written by WriteManifest with a Config-wide AppFileSingle default, under
// a placement other than FluxIntegratedPerLayout. The partitioned component
// layout must stay a directory (augmentLayoutTemplate pins its unset mode to
// AppFilePerResource), so kure neither refuses it as an AppFileSingle layout
// with children nor drops the children from the build: each hook group is
// written as one file there and listed by that directory's
// kustomization.yaml, and every rendered object is reachable from the root.
func TestAugmentLayoutTemplate_WriteManifestUnderAppFileSingleDefault(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: pre
  annotations:
    helm.sh/hook: pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	for _, placement := range []layout.FluxPlacement{layout.FluxSeparate, layout.FluxIntegratedPerBundle} {
		t.Run(string(placement), func(t *testing.T) {
			cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
				return raw, nil
			})
			app := stack.NewApplication("myapp", "default", wrapIfHelmchartAugmenter(cfg))
			cluster := &stack.Cluster{
				Name: "c",
				Node: &stack.Node{
					Name:   "apps",
					Bundle: &stack.Bundle{Name: "apps", Applications: []*stack.Application{app}},
				},
			}
			root, err := layout.WalkCluster(cluster, layout.LayoutRules{FluxPlacement: placement})
			if err != nil {
				t.Fatalf("WalkCluster: %v", err)
			}
			appLayout := findLayoutByName(root, "myapp")
			if appLayout == nil {
				t.Fatal("walked tree has no layout for the helmchart component")
			}
			if len(appLayout.Children) != 3 {
				t.Fatalf("component layout has %d children, want 3 hook groups", len(appLayout.Children))
			}
			if appLayout.ApplicationFileMode != layout.AppFilePerResource {
				t.Errorf("component layout ApplicationFileMode = %v, want AppFilePerResource", appLayout.ApplicationFileMode)
			}

			base := t.TempDir()
			wcfg := layout.Config{ManifestsDir: "clusters", ApplicationFileMode: layout.AppFileSingle}
			if err := layout.WriteManifest(base, wcfg, root); err != nil {
				t.Fatalf("WriteManifest: %v", err)
			}

			appDir := filepath.Join(base, wcfg.ManifestsDir, appLayout.FullRepoPath())
			listed := map[string]bool{}
			for _, e := range kustomizationResources(t, appDir) {
				listed[e] = true
			}
			for _, child := range appLayout.Children {
				entry := child.Name + ".yaml"
				if !listed[entry] {
					t.Errorf("%s/kustomization.yaml does not list hook group file %q (listed: %v)", appDir, entry, listed)
				}
			}

			got := reachableObjectNames(t, filepath.Join(base, wcfg.ManifestsDir, root.FullRepoPath()))
			for _, name := range []string{"pre", "main", "post"} {
				if !got[name] {
					t.Errorf("object %q is not reachable from the root kustomization.yaml (reachable: %v)", name, got)
				}
			}
		})
	}
}

// findLayoutByName returns the first layout named name in ml's tree, or nil.
func findLayoutByName(ml *layout.ManifestLayout, name string) *layout.ManifestLayout {
	if ml == nil {
		return nil
	}
	if ml.Name == name {
		return ml
	}
	for _, c := range ml.Children {
		if found := findLayoutByName(c, name); found != nil {
			return found
		}
	}
	return nil
}

// kustomizationResources returns the resources entries of dir/kustomization.yaml.
func kustomizationResources(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read kustomization.yaml: %v", err)
	}
	var k struct {
		Resources []string `yaml:"resources"`
	}
	if err := yaml.Unmarshal(data, &k); err != nil {
		t.Fatalf("parse %s/kustomization.yaml: %v", dir, err)
	}
	return k.Resources
}

// reachableObjectNames follows dir's kustomization.yaml resources entries the
// way a kustomize build does — a file entry adds its objects, a directory
// entry adds that directory's own build — and returns every object name
// reached. An entry that does not resolve on disk fails the test.
func reachableObjectNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	var build func(d string)
	build = func(d string) {
		for _, e := range kustomizationResources(t, d) {
			p := filepath.Join(d, e)
			fi, err := os.Stat(p)
			if err != nil {
				t.Errorf("%s/kustomization.yaml: resources entry %q does not resolve (%v)", d, e, err)
				continue
			}
			if fi.IsDir() {
				build(p)
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			objs, err := decodeKubeManifests(data)
			if err != nil {
				t.Fatalf("decode %s: %v", p, err)
			}
			for _, o := range objs {
				names[o.GetName()] = true
			}
		}
	}
	build(dir)
	return names
}

func TestExcludedHookPhasesAreDropped(t *testing.T) {
	excludedPhases := []string{"pre-delete", "post-delete", "pre-rollback", "post-rollback", "test"}
	var raw strings.Builder
	for i, phase := range excludedPhases {
		if i > 0 {
			raw.WriteString("---\n")
		}
		fmt.Fprintf(&raw, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: %s-pod\n  annotations:\n    helm.sh/hook: %s\n", phase, phase)
	}
	raw.WriteString("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kept\n")

	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return []byte(raw.String()), nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 surviving object (the 5 excluded-phase objects dropped), got %d", len(objects))
	}
	u, ok := (*objects[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[0] = %T, want *unstructured.Unstructured", *objects[0])
	}
	if u.GetName() != "kept" {
		t.Errorf("surviving object name = %q, want %q", u.GetName(), "kept")
	}
}

func TestHookGroupDir_EmptyPhaseIsMain(t *testing.T) {
	if got := hookGroupDir(helm.HookGroup{Phase: ""}); got != "main" {
		t.Errorf("hookGroupDir(empty phase) = %q, want %q", got, "main")
	}
}

func TestHookGroupDir_SanitizesUnsafePhase(t *testing.T) {
	cases := []struct{ phase, want string }{
		{"pre-install,post-install", "pre-install-post-install"},
		{"PRE-INSTALL", "pre-install"},
		{"weird/phase", "weird-phase"},
		{"../../etc", "etc"},
		{"!!!", "unknown"}, // punctuation-only phase strips to an empty slug — pins the "unknown" fallback
	}
	for _, c := range cases {
		if got := hookGroupDir(helm.HookGroup{Phase: c.phase}); got != c.want {
			t.Errorf("hookGroupDir(%q) = %q, want %q", c.phase, got, c.want)
		}
	}
}

func TestHookGroupDir_TruncatesLongPhase(t *testing.T) {
	long := strings.Repeat("a", 80)
	got := hookGroupDir(helm.HookGroup{Phase: long})
	if len(got) > 40 {
		t.Errorf("len(hookGroupDir(80-char phase)) = %d, want <= 40", len(got))
	}
	if got != strings.Repeat("a", 40) {
		t.Errorf("hookGroupDir(80-char phase) = %q, want 40 a's", got)
	}
}

// TestAugmentLayoutTemplate_ChildNameStaysWithinDNS1123Limit exercises
// hookGroupChildName directly with near-253-char ml.Names (validate.go's
// DNS-1123 subdomain max), including one whose truncation boundary lands
// right after a '.', and pins both the within-name and cross-name uniqueness
// guarantees hookGroupChildName's doc comment claims.
func TestAugmentLayoutTemplate_ChildNameStaysWithinDNS1123Limit(t *testing.T) {
	groups := []helm.HookGroup{
		{Phase: "pre-install"},
		{Phase: strings.Repeat("x", 80)}, // slugs+truncates to 40 x's via hookGroupDir
	}

	// mlNameA's truncation boundary (prefixLen=229 for group 0's suffix
	// "-00-pre-install", len 15: maxPrefix=253-15=238, prefixLen=238-8-1=229)
	// lands right after a literal '.': mlNameA[:229] ends in ".", exercising
	// the TrimRight(name, "-.") cleanup mirrored from valuesConfigMapName.
	mlNameA := strings.Repeat("a", 228) + "." + strings.Repeat("b", 24)
	if len(mlNameA) != 253 {
		t.Fatalf("test setup: len(mlNameA) = %d, want 253", len(mlNameA))
	}
	if mlNameA[228] != '.' {
		t.Fatalf("test setup: mlNameA[228] = %q, want '.'", mlNameA[228])
	}

	namesA := make([]string, len(groups))
	for i, g := range groups {
		dn := hookGroupChildName(mlNameA, i, g)
		if len(dn) > 253 {
			t.Errorf("group %d: len(%q) = %d, want <= 253", i, dn, len(dn))
		}
		if errs := validation.IsDNS1123Subdomain(dn); len(errs) != 0 {
			t.Errorf("group %d: IsDNS1123Subdomain(%q) = %v, want no errors", i, dn, errs)
		}
		if strings.HasSuffix(dn, ".") || strings.HasSuffix(dn, "-") {
			t.Errorf("group %d: %q has a dangling '-'/'.' artifact from truncation", i, dn)
		}
		namesA[i] = dn
	}
	if namesA[0] == namesA[1] {
		t.Fatalf("hookGroupChildName collided across groups for one ml.Name: both produced %q", namesA[0])
	}

	// A second near-253-char ml.Name sharing mlNameA's truncated prefix must
	// still yield a distinct dirName set — the sha256 prefix, not just the
	// group index, is what prevents cross-name collision (mirrors
	// TestValuesConfigMapName_TruncationPreservesUniqueness).
	mlNameB := strings.Repeat("a", 228) + "." + strings.Repeat("c", 24)
	if len(mlNameB) != 253 {
		t.Fatalf("test setup: len(mlNameB) = %d, want 253", len(mlNameB))
	}
	if mlNameA == mlNameB {
		t.Fatal("test setup: mlNameA and mlNameB must differ")
	}
	for i, g := range groups {
		dnA := hookGroupChildName(mlNameA, i, g)
		dnB := hookGroupChildName(mlNameB, i, g)
		if dnA == dnB {
			t.Errorf("group %d: hookGroupChildName collided across ml.Names: mlNameA=%q mlNameB=%q both produced %q", i, mlNameA, mlNameB, dnA)
		}
	}
}

// generateNames runs Generate and returns the resulting objects' names in
// order, failing the test on any error or non-unstructured object.
func generateNames(t *testing.T, cfg *HelmchartConfig) []string {
	t.Helper()
	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	names := make([]string, len(objects))
	for i, o := range objects {
		u, ok := (*o).(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("objects[%d] = %T, want *unstructured.Unstructured", i, *o)
		}
		names[i] = u.GetName()
	}
	return names
}

// TestGenerate_MultiEventHookOrdersByEarliestPhase pins the fix for the
// defect kure's SplitByHookWeight documents but does not itself correct
// (kure pkg/stack/helm/hooks.go:35-36): a comma-separated helm.sh/hook
// annotation ("pre-install,pre-upgrade") must land in the pre-install-ordered
// group, not kure's alphabetical "unknown" bucket (which sorts after
// post-upgrade — the opposite of what the annotation requests).
func TestGenerate_MultiEventHookOrdersByEarliestPhase(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"multi", "main", "post"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("execution order = %v, want %v", got, want)
		}
	}
}

// TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition covers a
// distinct branch untested by every other multi-event test: those all list
// their tokens in ascending priority order already
// ("pre-install,pre-upgrade"), so a naive
// implementation that simply picked the FIRST recognized token (rather than
// the earliest by kure's own hookPhaseOrder priority) would pass every one
// of them. "pre-upgrade,pre-install" (tokens in descending priority order)
// distinguishes the two: it must still resolve to the pre-install group.
func TestGenerate_MultiEventHookPicksEarliestByPriorityNotPosition(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-upgrade,pre-install
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "pre-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (earliest by priority, not by listed position)", got, "pre-install")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase covers a
// multi-value annotation mixing a recognized ordered phase with an excluded
// one ("pre-install,pre-delete"): the excluded token must not suppress the
// whole object, and the object must still land in the pre-install group.
func TestGenerate_MultiEventHookDropsExcludedTokenKeepsEarliestPhase(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: pre-install,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"multi", "main"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("execution order = %v, want %v (multi must survive, ordered ahead of main)", got, want)
		}
	}
}

// TestGenerate_MultiEventHookAllExcludedIsDropped covers a multi-value
// annotation whose tokens are entirely excluded phases ("test,pre-delete"):
// kure's exact-string-match excludedHookPhases lookup (hooks.go:20-26,49)
// never excludes the combined string, so without normalization this object
// would wrongly survive into the mis-sorted unknown bucket instead of being
// dropped, same as a single excluded phase is today.
func TestGenerate_MultiEventHookAllExcludedIsDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,pre-delete
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kept
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	got := generateNames(t, cfg)
	want := []string{"kept"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("expected only the non-hook object to survive, got %v", got)
	}
}

// TestGenerate_MultiEventCustomHooksStayUnknown proves the fix does not
// overcorrect: a multi-value annotation made entirely of unrecognized custom
// hook names ("crd-install,some-custom-hook" — no member of the excluded or
// four-ordered sets) has no defined ordering priority among its tokens, so it
// must be left exactly as kure's own unknown-bucket fallback already handles
// it — sorted alphabetically after post-upgrade, annotation untouched.
func TestGenerate_MultiEventCustomHooksStayUnknown(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: crd-install,some-custom-hook
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
	u, ok := (*objects[1]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[1] = %T, want *unstructured.Unstructured", *objects[1])
	}
	if u.GetName() != "multi" {
		t.Fatalf("execution order: objects[1].Name = %q, want %q (unrecognized custom hook must sort last, unchanged)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != "crd-install,some-custom-hook" {
		t.Errorf("multi's helm.sh/hook annotation = %q, want unchanged %q", got, "crd-install,some-custom-hook")
	}
	// Pin the grouping key itself, not just execution order and the emitted
	// annotation: a broken normalizer could rewrite the grouping key (e.g. to
	// just "crd-install", dropping "some-custom-hook") while still leaving
	// the emitted object's own annotation untouched and this object sorting
	// last purely by chance of the two orderings comparing equal here — the
	// Phase assertion is the one check that would catch that.
	if len(cfg.hookGroups) != 2 {
		t.Fatalf("expected 2 hook groups, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[1].Phase; got != "crd-install,some-custom-hook" {
		t.Errorf("hookGroups[1].Phase = %q, want unchanged %q (grouping key for an all-custom annotation must not be rewritten)", got, "crd-install,some-custom-hook")
	}
}

// TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks covers a
// branch not handled by an earlier version of this fix: a
// multi-value annotation mixing an excluded phase with an unrecognized
// custom hook name ("test,crd-install") has no recognized ordered phase
// among its tokens, so it does not take the earliest-phase branch — but it
// is not "every token unrecognized" either (one token, "test", IS a member
// of excludedHookPhases), so it must not take the leave-unchanged branch
// either. The excluded token must be dropped from the grouping key, leaving
// just "crd-install" — an excluded phase must never influence the unknown-
// bucket grouping decision for an object that survives (is not entirely
// excluded).
func TestGenerate_MultiEventHookDropsExcludedTokenAmongCustomHooks(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: test,crd-install
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group (multi survives, not all tokens excluded), got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "crd-install" {
		t.Errorf("hookGroups[0].Phase = %q, want %q (excluded token \"test\" dropped from grouping key)", got, "crd-install")
	}
	if len(cfg.hookGroups[0].Resources) != 1 {
		t.Fatalf("hookGroups[0].Resources has %d entries, want 1", len(cfg.hookGroups[0].Resources))
	}
	u, ok := cfg.hookGroups[0].Resources[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("hookGroups[0].Resources[0] = %T, want *unstructured.Unstructured", cfg.hookGroups[0].Resources[0])
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != "test,crd-install" {
		t.Errorf("emitted object's helm.sh/hook annotation = %q, want unchanged %q", got, "test,crd-install")
	}
}

// TestGenerate_CommaOnlyHookAnnotationIsNotDropped covers a degenerate
// annotation with no actual token content at all ("," — every split token is
// empty after trimming). Before this multi-event fix existed, kure's
// SplitByHookWeight would have treated the literal string "," as one opaque
// unknown-phase string — not excluded (excludedHookPhases has no "," entry)
// and not dropped. normalizeHookAnnotationForGrouping must reach the same
// outcome: "no non-empty token was ever excluded" is a different condition
// from "every non-empty token was excluded", and only the latter should
// route to the drop-via-"test" branch.
func TestGenerate_CommaOnlyHookAnnotationIsNotDropped(t *testing.T) {
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ","
`)
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	})

	if err := cfg.ensureRendered(); err != nil {
		t.Fatalf("ensureRendered: %v", err)
	}
	var total int
	for _, g := range cfg.hookGroups {
		total += len(g.Resources)
	}
	if total != 1 {
		t.Fatalf("expected the comma-only-hook object to survive (not all tokens excluded — there were no tokens at all), got %d resources across %d groups", total, len(cfg.hookGroups))
	}
	// Pin the grouping key itself: a broken normalizer could rewrite "," to
	// "" (or some other value) while still leaving the object present, which
	// the resource-count check above alone would not catch.
	if len(cfg.hookGroups) != 1 {
		t.Fatalf("expected 1 hook group, got %d", len(cfg.hookGroups))
	}
	if got := cfg.hookGroups[0].Phase; got != "," {
		t.Errorf("hookGroups[0].Phase = %q, want unchanged %q (a degenerate annotation must not be rewritten)", got, ",")
	}
}

// TestAugmentLayoutTemplate_MultiEventHookAnnotationUnchangedInOutput is the
// test called for by the fix's own hazard: the grouping-key rewrite
// (normalizeHookAnnotationForGrouping) must never leak into the object that
// ends up in emitted output. Exercises both Generate (flattened union) and
// AugmentLayout's template branch (augmentLayoutTemplate, repartitioned into
// child layouts) — a no-op implementation that simply left the multi-event
// annotation untouched would satisfy the "annotation unchanged" half of this
// test but fail its "correct group placement" half, and a broken
// implementation that mutated the object in place would fail the reverse —
// only a correct fix (copy-for-grouping, restore-original-for-output)
// satisfies both halves at once.
func TestAugmentLayoutTemplate_MultiEventHookAnnotationUnchangedInOutput(t *testing.T) {
	const wantHook = "pre-install,pre-upgrade"
	raw := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ` + wantHook + `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: post
  annotations:
    helm.sh/hook: post-install
`)
	renderChart := func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return raw, nil
	}

	// Generate path: correct placement (first) and unchanged annotation.
	genCfg := helmchartTemplateFixture(renderChart)
	objects, err := genCfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objects))
	}
	u, ok := (*objects[0]).(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("objects[0] = %T, want *unstructured.Unstructured", *objects[0])
	}
	if u.GetName() != "multi" {
		t.Fatalf("Generate execution order: objects[0].Name = %q, want %q (earliest-phase placement)", u.GetName(), "multi")
	}
	if got := u.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("Generate: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}

	// AugmentLayout path: correct child group placement (dirName derived from
	// the earliest phase, "pre-install") and unchanged annotation on the
	// resource inside that child.
	augCfg := helmchartTemplateFixture(renderChart)
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := augCfg.augmentLayoutTemplate(ml); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3", len(ml.Children))
	}
	if ml.Children[0].Name != "myapp-00-pre-install" {
		t.Fatalf("Children[0].Name = %q, want %q (multi's group keyed by its earliest phase)", ml.Children[0].Name, "myapp-00-pre-install")
	}
	if len(ml.Children[0].Resources) != 1 {
		t.Fatalf("Children[0].Resources has %d entries, want 1", len(ml.Children[0].Resources))
	}
	child, ok := ml.Children[0].Resources[0].(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Children[0].Resources[0] = %T, want *unstructured.Unstructured", ml.Children[0].Resources[0])
	}
	if child.GetName() != "multi" {
		t.Fatalf("Children[0].Resources[0].Name = %q, want %q", child.GetName(), "multi")
	}
	if got := child.GetAnnotations()["helm.sh/hook"]; got != wantHook {
		t.Errorf("AugmentLayout: multi's helm.sh/hook annotation = %q, want unchanged %q", got, wantHook)
	}
}

// TestAugmentLayoutTemplate_MultiEventHookJobWithIntegerFields pins
// go-kure/launcher#581's acceptance on the helmchart composite's
// delivery: template: a rendered pre-install,pre-upgrade Job with
// backoffLimit: 3 builds without panicking (yaml.v3's Go int used to panic in
// the grouping copy: "cannot deep copy int"), lands in the pre-install hook
// group, and emits backoffLimit: 3 unchanged. The terminal half, on the same
// chart, is TestHelmTemplateConfig_MultiEventHookJobWithIntegerFields.
func TestAugmentLayoutTemplate_MultiEventHookJobWithIntegerFields(t *testing.T) {
	cfg := helmchartTemplateFixture(func(chartURL, version string, values map[string]any, opts ...helm.RenderOption) ([]byte, error) {
		return []byte(multiEventHookJobChart), nil
	})

	// Builds: Generate and AugmentLayout's template branch both return,
	// without a panic.
	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "default/myapp"}
	if err := cfg.augmentLayoutTemplate(ml); err != nil {
		t.Fatalf("augmentLayoutTemplate: %v", err)
	}

	// Lands in the pre-install hook group: first in execution order, and the
	// sole object of the first child layout, the pre-install group.
	if got, want := generatedNames(objects), []string{"migrate", "main"}; !slices.Equal(got, want) {
		t.Fatalf("execution order = %v, want %v", got, want)
	}
	if len(ml.Children) != 2 {
		t.Fatalf("ml.Children has %d entries, want 2 hook groups", len(ml.Children))
	}
	if name := ml.Children[0].Name; name != "myapp-00-pre-install" {
		t.Fatalf("Children[0].Name = %q, want %q", name, "myapp-00-pre-install")
	}
	hook := ml.Children[0].Resources
	if names := resourceNames(hook); !slices.Equal(names, []string{"migrate"}) {
		t.Fatalf("Children[0] holds %v, want [migrate]", names)
	}
	assertDeepCopyable(t, hook)

	// Emits backoffLimit: 3 unchanged, from Generate and from the hook group.
	assertEmitsBackoffLimit3(t, "Generate", *objects[0])
	assertEmitsBackoffLimit3(t, "pre-install group", hook[0])
}
