package components

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	kureio "github.com/go-kure/kure/pkg/io"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/helm"
	"github.com/go-kure/kure/pkg/stack/layout"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// helmTemplateFixture parses a helmtemplate component on an HTTP Helm
// repository through the handler, then swaps its renderer for render, so a
// test drives the same config a document produces without the network.
func helmTemplateFixture(t *testing.T, render renderChartFunc) *HelmTemplateConfig {
	t.Helper()
	cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{
		Name: "myapp",
		Type: "helmtemplate",
		Properties: map[string]any{
			"chart":  "myapp",
			"source": map[string]any{"url": "https://charts.example.com"},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	tc, ok := cfg.(*HelmTemplateConfig)
	if !ok {
		t.Fatalf("config = %T, want *HelmTemplateConfig", cfg)
	}
	tc.renderChart = render
	return tc
}

// stubRender returns a renderer that ignores its inputs and yields raw.
func stubRender(raw string) renderChartFunc {
	return func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		return []byte(raw), nil
	}
}

// renderedNames runs Generate on cfg and returns the objects' names in order.
func renderedNames(t *testing.T, cfg stack.ApplicationConfig) []string {
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

// resourceNames returns the names of objs in order.
func resourceNames(objs []client.Object) []string {
	names := make([]string, len(objs))
	for i, o := range objs {
		names[i] = o.GetName()
	}
	return names
}

// helmTemplateThreeGroupChart has one object in each of three hook groups:
// pre-install, main (no hook) and post-install, deliberately listed out of
// execution order.
const helmTemplateThreeGroupChart = `apiVersion: v1
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
`

// TestHelmTemplateHandler_SchemaMatchesProperties ties the published schema to
// what the strict decode accepts: every JSON key of helmTemplateProperties, at
// every depth, with the property type its Go field encodes as, plus the values
// key split off before the decode — no more and no fewer. It also pins which
// nodes are required, the two the handler itself refuses when missing.
func TestHelmTemplateHandler_SchemaMatchesProperties(t *testing.T) {
	want := map[string]oam.PropertyType{helmTemplateValuesKey: oam.PropertyTypeObject}
	var walk func(prefix string, st reflect.Type)
	walk = func(prefix string, st reflect.Type) {
		for f := range st.Fields() {
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			path := prefix + key
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch ft.Kind() {
			case reflect.String:
				want[path] = oam.PropertyTypeString
			case reflect.Struct:
				want[path] = oam.PropertyTypeObject
				walk(path+".", ft)
			default:
				t.Fatalf("field %s: unmapped kind %s", f.Name, ft.Kind())
			}
		}
	}
	walk("", reflect.TypeFor[helmTemplateProperties]())

	got := map[string]oam.PropertyType{}
	var required []string
	var flatten func(prefix string, schema map[string]oam.PropertySchema)
	flatten = func(prefix string, schema map[string]oam.PropertySchema) {
		for k, s := range schema {
			got[prefix+k] = s.Type
			if s.Required {
				required = append(required, prefix+k)
			}
			flatten(prefix+k+".", s.Properties)
		}
	}
	flatten("", (&HelmTemplateHandler{}).PropertySchema())

	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema paths/types = %v\nwant %v", got, want)
	}
	slices.Sort(required)
	if wantRequired := []string{"source", "source.url"}; !slices.Equal(required, wantRequired) {
		t.Errorf("required schema paths = %v, want %v", required, wantRequired)
	}
}

// TestHelmTemplateHandler_EveryFieldReachable: no field of the decoded structs
// is shadowed by the split-off values key or otherwise unreachable through the
// strict decode. The exclusion list is explicit and must stay empty.
func TestHelmTemplateHandler_EveryFieldReachable(t *testing.T) {
	excluded := []string{}
	for _, typ := range []reflect.Type{reflect.TypeFor[helmTemplateProperties](), reflect.TypeFor[helmTemplateSource]()} {
		if got := builtin.UnreachableJSONFields(typ, helmTemplateValuesKey); !slices.Equal(got, excluded) {
			t.Errorf("unreachable %s fields: %v, want %v", typ.Name(), got, excluded)
		}
	}
}

// TestHelmTemplateConfig_MultiEventHookOrdersByEarliestPhase is the
// helmtemplate terminal's hook-order oracle, mirroring the composite's
// TestGenerate_MultiEventHookOrdersByEarliestPhase: an object whose
// helm.sh/hook annotation names several events ("pre-install,pre-upgrade")
// must land in the pre-install group, ahead of the hook-free main group, not in
// kure's alphabetical "unknown" bucket after post-upgrade. Changing multiHook
// to "post-install" moves the object out of the pre-install group and turns
// the order assertion red; reordering its two tokens keeps it green, since the
// earliest phase by priority, not by position, decides the group.
func TestHelmTemplateConfig_MultiEventHookOrdersByEarliestPhase(t *testing.T) {
	const multiHook = "pre-install,pre-upgrade"
	raw := `apiVersion: v1
kind: ConfigMap
metadata:
  name: multi
  annotations:
    helm.sh/hook: ` + multiHook + `
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
`
	cfg := helmTemplateFixture(t, stubRender(raw))

	got := renderedNames(t, cfg)
	want := []string{"multi", "main", "post"}
	if !slices.Equal(got, want) {
		t.Fatalf("execution order = %v, want %v", got, want)
	}

	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "team"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 3 {
		t.Fatalf("ml.Children has %d entries, want 3 hook groups", len(ml.Children))
	}
	first := ml.Children[0]
	if first.Name != "myapp-00-pre-install" {
		t.Errorf("Children[0].Name = %q, want %q (the group keyed by multi's earliest phase)", first.Name, "myapp-00-pre-install")
	}
	if names := resourceNames(first.Resources); !slices.Equal(names, []string{"multi"}) {
		t.Fatalf("Children[0] holds %v, want [multi]", names)
	}
	// The grouping key is rewritten on a copy only: the emitted object keeps
	// its authored annotation.
	if got := first.Resources[0].GetAnnotations()["helm.sh/hook"]; got != multiHook {
		t.Errorf("emitted helm.sh/hook = %q, want the authored %q", got, multiHook)
	}
}

// multiEventHookJobChart renders a pre-install,pre-upgrade hook Job with
// integer fields — backoffLimit: 3 and a container port — ahead of a hook-free
// ConfigMap: go-kure/launcher#581's reproduction. yaml.v3 decodes a rendered
// integer as a Go int, which runtime.DeepCopyJSONValue refuses with a panic,
// and a multi-event hook is the object the grouping step deep-copies
// (cloneWithHookAnnotation). Shared with the composite's
// TestAugmentLayoutTemplate_MultiEventHookJobWithIntegerFields.
const multiEventHookJobChart = `apiVersion: batch/v1
kind: Job
metadata:
  name: migrate
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
spec:
  backoffLimit: 3
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: migrate
        image: registry.example.com/migrate:1.0.0
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`

// assertDeepCopyable fails t for any object whose content holds a type
// runtime.DeepCopyJSON does not accept — the JSON-typed content an
// Unstructured promises every consumer that copies it.
func assertDeepCopyable(t *testing.T, objs []client.Object) {
	t.Helper()
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			t.Fatalf("object = %T, want *unstructured.Unstructured", o)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s %q is not deep-copyable: %v", u.GetKind(), u.GetName(), r)
				}
			}()
			runtime.DeepCopyJSON(u.Object)
		}()
	}
}

// writtenYAML is obj as kure writes it into a manifest file.
func writtenYAML(t *testing.T, obj client.Object) string {
	t.Helper()
	data, err := kureio.EncodeObjectsToYAML([]*client.Object{&obj})
	if err != nil {
		t.Fatalf("EncodeObjectsToYAML: %v", err)
	}
	return string(data)
}

// assertEmitsBackoffLimit3 checks go-kure/launcher#581's output point on the
// emitted migrate Job of multiEventHookJobChart: spec.backoffLimit is still 3,
// and kure writes it as `backoffLimit: 3` under spec.
func assertEmitsBackoffLimit3(t *testing.T, where string, obj client.Object) {
	t.Helper()
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("%s: object = %T, want *unstructured.Unstructured", where, obj)
	}
	if u.GetName() != "migrate" {
		t.Fatalf("%s: object is %q, want the migrate Job", where, u.GetName())
	}
	if got, found, err := unstructured.NestedInt64(u.Object, "spec", "backoffLimit"); err != nil || !found || got != 3 {
		t.Errorf("%s: spec.backoffLimit = %d (found %v, err %v), want 3", where, got, found, err)
	}
	if written := writtenYAML(t, obj); !strings.Contains(written, "\nspec:\n  backoffLimit: 3\n") {
		t.Errorf("%s: written Job lacks spec.backoffLimit: 3:\n%s", where, written)
	}
}

// generatedNames returns the names of objects, Generate's output, in order.
func generatedNames(objects []*client.Object) []string {
	names := make([]string, len(objects))
	for i, o := range objects {
		names[i] = (*o).GetName()
	}
	return names
}

// TestHelmTemplateConfig_MultiEventHookJobWithIntegerFields pins the terminal
// half of go-kure/launcher#581's acceptance: a rendered pre-install,pre-upgrade
// Job with backoffLimit: 3 builds without panicking (yaml.v3's Go int used to
// panic in the grouping copy: "cannot deep copy int"), lands in the pre-install
// hook group, and emits backoffLimit: 3 unchanged — written exactly as the same
// document decoded by yaml.v3 alone would be (an int and an int64 encode
// alike). The composite half is
// TestAugmentLayoutTemplate_MultiEventHookJobWithIntegerFields.
func TestHelmTemplateConfig_MultiEventHookJobWithIntegerFields(t *testing.T) {
	cfg := helmTemplateFixture(t, stubRender(multiEventHookJobChart))

	// Builds: Generate and AugmentLayout both return, without a panic.
	objects, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "team"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
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

	var plain map[string]any
	if err := yaml.NewDecoder(strings.NewReader(multiEventHookJobChart)).Decode(&plain); err != nil {
		t.Fatalf("decoding the Job with yaml.v3: %v", err)
	}
	want := writtenYAML(t, &unstructured.Unstructured{Object: plain})
	got := writtenYAML(t, hook[0])
	if got != want {
		t.Errorf("written Job =\n%s\nwant (the yaml.v3 decode written as is)\n%s", got, want)
	}
	if !strings.Contains(got, "- containerPort: 8080\n") {
		t.Errorf("written Job lacks its containerPort 8080:\n%s", got)
	}
}

// outOfRangeOffsetChart renders a hook-free ConfigMap whose data value is an
// unquoted timestamp with a UTC offset of 24 hours. yaml.v3 decodes it to a
// time.Time that RFC 3339 cannot express, which time.Time.MarshalJSON — and so
// kure's writer, which writes an object from its JSON encoding — refuses.
// Shared with the composite's
// TestGenerateTemplate_TimestampOutsideRFC3339IsABuildError.
const outOfRangeOffsetChart = `apiVersion: v1
kind: ConfigMap
metadata:
  name: stamped
data:
  at: 2001-12-14T21:59:43+24:00
`

// TestHelmTemplateConfig_TimestampOutsideRFC3339IsABuildError: a rendered
// timestamp RFC 3339 cannot express fails the build, naming the object and
// the path, as kure's writer refused the same document decoded by yaml.v3
// alone. Converting the value must not turn it into a string that writes.
func TestHelmTemplateConfig_TimestampOutsideRFC3339IsABuildError(t *testing.T) {
	var plain map[string]any
	if err := yaml.NewDecoder(strings.NewReader(outOfRangeOffsetChart)).Decode(&plain); err != nil {
		t.Fatalf("decoding the ConfigMap with yaml.v3: %v", err)
	}
	var obj client.Object = &unstructured.Unstructured{Object: plain}
	if _, err := kureio.EncodeObjectsToYAML([]*client.Object{&obj}); err == nil {
		t.Fatal("kure writes the yaml.v3 decode as is; the chart no longer pins a refused timestamp")
	}

	cfg := helmTemplateFixture(t, stubRender(outOfRangeOffsetChart))
	_, err := cfg.Generate(nil)
	assertErrorMentions(t, err, `ConfigMap "stamped"`, ".data.at", "timezone hour outside of range")
}

// topLevelNonStringKeyChart renders, after a hook-free ConfigMap, a hook-free
// ConfigMap with an unquoted 1 and true among its own top-level keys, which
// makes yaml.v3 decode that whole document to map[any]any. Shared with the
// composite's TestGenerateTemplate_TopLevelNonStringKeyIsABuildError.
const topLevelNonStringKeyChart = `apiVersion: v1
kind: ConfigMap
metadata:
  name: main
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: stray-keys
1: x
true: y
data:
  key: value
`

// TestHelmTemplateConfig_TopLevelNonStringKeyIsABuildError: a rendered
// document with a key that is not a string at its own top level fails the
// build, naming the object and the top level, as the same key in a nested
// mapping does. It is not skipped as a document that holds no object.
func TestHelmTemplateConfig_TopLevelNonStringKeyIsABuildError(t *testing.T) {
	cfg := helmTemplateFixture(t, stubRender(topLevelNonStringKeyChart))
	_, err := cfg.Generate(nil)
	assertErrorMentions(t, err, `ConfigMap "stray-keys"`, "top level", "not a string")
}

// droppedHooksUnemittableChart renders, beside a hook-free ConfigMap, a test
// hook with a non-string mapping key, a pre-delete,post-delete hook with an
// out-of-range timestamp, and a pre-rollback,test hook with non-string keys
// at its own top level. Hook grouping drops all three hooks unwritten, so
// none of those keys or values is ever emitted. Shared with the composite's
// TestGenerateTemplate_DroppedHookWithUnemittableValuesBuilds.
const droppedHooksUnemittableChart = `apiVersion: v1
kind: ConfigMap
metadata:
  name: test-hook
  annotations:
    helm.sh/hook: test
data:
  1: one
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: delete-hook
  annotations:
    helm.sh/hook: pre-delete,post-delete
data:
  at: 2001-12-14T21:59:43+24:00
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: rollback-hook
  annotations:
    helm.sh/hook: pre-rollback,test
1: one
true: yes
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: main
`

// TestHelmTemplateConfig_DroppedHookWithUnemittableValuesBuilds: a key or
// value no manifest can carry, inside a hook that grouping drops, does not
// fail the build; the hook is dropped with it and the rest is emitted.
func TestHelmTemplateConfig_DroppedHookWithUnemittableValuesBuilds(t *testing.T) {
	cfg := helmTemplateFixture(t, stubRender(droppedHooksUnemittableChart))
	if got, want := renderedNames(t, cfg), []string{"main"}; !slices.Equal(got, want) {
		t.Errorf("Generate emitted %v, want %v", got, want)
	}
}

// TestHelmTemplateConfig_RendersOnce: Generate followed by AugmentLayout —
// kure's layout walker's call order — renders the chart exactly once.
func TestHelmTemplateConfig_RendersOnce(t *testing.T) {
	calls := 0
	cfg := helmTemplateFixture(t, func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
		calls++
		return []byte(helmTemplateThreeGroupChart), nil
	})
	if _, err := cfg.Generate(nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := cfg.AugmentLayout(&layout.ManifestLayout{Name: "myapp", Namespace: "team"}); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if calls != 1 {
		t.Errorf("renderChart called %d times, want 1", calls)
	}
}

// TestHelmTemplateConfig_RendererInputs pins what reaches the renderer: a
// HelmRepository's URL joined with the chart name (a trailing slash on the URL
// dropped), an OCIRepository's URL unchanged, the version, and the values
// tree exactly as authored — the same map, with its YAML-decoded int still an
// int rather than the strict decoder's json.Number, which a chart template
// comparing `.Values.replicaCount` to a number would treat differently.
func TestHelmTemplateConfig_RendererInputs(t *testing.T) {
	cases := []struct {
		name        string
		props       map[string]any
		wantURL     string
		wantVersion string
	}{
		{
			name: "HelmRepository",
			props: map[string]any{
				"chart":   "podinfo",
				"version": "6.1.0",
				"source":  map[string]any{"url": "https://charts.example.com/"},
			},
			wantURL:     "https://charts.example.com/podinfo",
			wantVersion: "6.1.0",
		},
		{
			name: "OCIRepository",
			props: map[string]any{
				"version": "1.2.3",
				"source":  map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"},
			},
			wantURL:     "oci://ghcr.io/example/charts/podinfo",
			wantVersion: "1.2.3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]any{"replicaCount": 3, "image": map[string]any{"tag": "1.2"}}
			tc.props["values"] = values
			cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "podinfo", Type: "helmtemplate", Properties: tc.props}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			var gotURL, gotVersion string
			var gotValues map[string]any
			ht := cfg.(*HelmTemplateConfig)
			ht.renderChart = func(chartURL, version string, v map[string]any, _ ...helm.RenderOption) ([]byte, error) {
				gotURL, gotVersion, gotValues = chartURL, version, v
				return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"), nil
			}
			if _, err := ht.Generate(nil); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Errorf("chartURL = %q, want %q", gotURL, tc.wantURL)
			}
			if gotVersion != tc.wantVersion {
				t.Errorf("version = %q, want %q", gotVersion, tc.wantVersion)
			}
			if !reflect.DeepEqual(gotValues, values) {
				t.Errorf("values = %#v, want the authored %#v", gotValues, values)
			}
			if _, ok := gotValues["replicaCount"].(int); !ok {
				t.Errorf("values.replicaCount reached the renderer as %T, want int as authored", gotValues["replicaCount"])
			}
		})
	}
}

// TestHelmTemplateConfig_AugmentLayout_PartitionsUnderParentPath pins the
// hook-group layout on the terminal: one child per group, in execution order,
// named <component>-NN-<phase>, each directly under the component's own
// directory (Namespace = the parent's path, never nested twice —
// go-kure/kure#771), inheriting the parent's layout rules except
// ApplicationFileMode, chained by DependsOn. A parent mode the caller set
// explicitly is kept.
func TestHelmTemplateConfig_AugmentLayout_PartitionsUnderParentPath(t *testing.T) {
	cfg := helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart))
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
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if ml.Resources != nil {
		t.Errorf("ml.Resources = %v, want nil after partitioning", ml.Resources)
	}
	if ml.ApplicationFileMode != layout.AppFileSingle {
		t.Errorf("ml.ApplicationFileMode = %v, want the caller's explicit AppFileSingle kept", ml.ApplicationFileMode)
	}
	wantNames := []string{"myapp-00-pre-install", "myapp-01-main", "myapp-02-post-install"}
	wantResources := [][]string{{"pre"}, {"main"}, {"post"}}
	if len(ml.Children) != len(wantNames) {
		t.Fatalf("ml.Children has %d entries, want %d", len(ml.Children), len(wantNames))
	}
	for i, child := range ml.Children {
		if child.Name != wantNames[i] {
			t.Errorf("Children[%d].Name = %q, want %q", i, child.Name, wantNames[i])
		}
		if names := resourceNames(child.Resources); !slices.Equal(names, wantResources[i]) {
			t.Errorf("Children[%d] holds %v, want %v", i, names, wantResources[i])
		}
		if child.Namespace != "team/myapp" {
			t.Errorf("Children[%d].Namespace = %q, want the parent path %q", i, child.Namespace, "team/myapp")
		}
		if wantPath := "team/myapp/" + wantNames[i]; child.FullRepoPath() != wantPath {
			t.Errorf("Children[%d].FullRepoPath() = %q, want %q", i, child.FullRepoPath(), wantPath)
		}
		if child.Mode != ml.Mode || child.FluxPlacement != ml.FluxPlacement || child.FileNaming != ml.FileNaming || child.FilePer != ml.FilePer {
			t.Errorf("Children[%d] rules = %v/%v/%v/%v, want the parent's %v/%v/%v/%v", i,
				child.Mode, child.FluxPlacement, child.FileNaming, child.FilePer, ml.Mode, ml.FluxPlacement, ml.FileNaming, ml.FilePer)
		}
		if child.ApplicationFileMode != layout.AppFileUnset {
			t.Errorf("Children[%d].ApplicationFileMode = %v, want AppFileUnset (must not inherit the parent's AppFileSingle)", i, child.ApplicationFileMode)
		}
		var wantDeps []string
		if i > 0 {
			wantDeps = []string{wantNames[i-1]}
		}
		if !slices.Equal(child.DependsOn, wantDeps) {
			t.Errorf("Children[%d].DependsOn = %v, want %v", i, child.DependsOn, wantDeps)
		}
	}
}

// TestHelmTemplateConfig_AugmentLayout_DirectoryPin pins go-kure/launcher#563's
// fix on the terminal: partitioning pins an unset component layout to
// AppFilePerResource so it stays a directory listing its hook-group children,
// while a single-group chart leaves the layout exactly as it was.
func TestHelmTemplateConfig_AugmentLayout_DirectoryPin(t *testing.T) {
	multi := helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart))
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "team"}
	if err := multi.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if ml.ApplicationFileMode != layout.AppFilePerResource {
		t.Errorf("partitioned ml.ApplicationFileMode = %v, want AppFilePerResource", ml.ApplicationFileMode)
	}

	single := helmTemplateFixture(t, stubRender("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"))
	kept := []client.Object{&unstructured.Unstructured{}}
	ml = &layout.ManifestLayout{Name: "myapp", Namespace: "team", Resources: kept}
	if err := single.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) != 0 {
		t.Errorf("ml.Children has %d entries, want 0 (one hook group is a no-op)", len(ml.Children))
	}
	if ml.ApplicationFileMode != layout.AppFileUnset {
		t.Errorf("ml.ApplicationFileMode = %v, want AppFileUnset (the pin applies only when partitioning)", ml.ApplicationFileMode)
	}
	if len(ml.Resources) != 1 || ml.Resources[0] != kept[0] {
		t.Errorf("ml.Resources = %v, want the walker's flat resources untouched", ml.Resources)
	}
}

// TestHelmTemplateConfig_WriteManifestUnderAppFileSingleDefault is
// go-kure/launcher#563 end to end on the terminal: walked by kure's
// WalkCluster and written by WriteManifest with a Config-wide AppFileSingle
// default, under a placement other than FluxIntegratedPerLayout, the component
// stays a directory that lists one file per hook group, and every rendered
// object is reachable from the root.
func TestHelmTemplateConfig_WriteManifestUnderAppFileSingleDefault(t *testing.T) {
	for _, placement := range []layout.FluxPlacement{layout.FluxSeparate, layout.FluxIntegratedPerBundle} {
		t.Run(string(placement), func(t *testing.T) {
			cfg := helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart))
			app := stack.NewApplication("myapp", "default", cfg)
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
				t.Fatal("walked tree has no layout for the helmtemplate component")
			}
			if len(appLayout.Children) != 3 {
				t.Fatalf("component layout has %d children, want 3 hook groups", len(appLayout.Children))
			}

			base := t.TempDir()
			wcfg := layout.Config{ManifestsDir: "clusters", ApplicationFileMode: layout.AppFileSingle}
			if err := layout.WriteManifest(base, wcfg, root); err != nil {
				t.Fatalf("WriteManifest: %v", err)
			}
			appDir := filepath.Join(base, wcfg.ManifestsDir, appLayout.FullRepoPath())
			listed := kustomizationResources(t, appDir)
			for _, child := range appLayout.Children {
				if entry := child.Name + ".yaml"; !slices.Contains(listed, entry) {
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

// TestHelmTemplateConfig_ChildKustomizationReferencesResolveOnDisk writes the
// partitioned layout with kure's disk writer: every resources entry of every
// kustomization.yaml must resolve, and one naming a directory must reach that
// directory's own kustomization.yaml — a child nested twice (go-kure/kure#771)
// leaves only an empty intermediate directory there.
func TestHelmTemplateConfig_ChildKustomizationReferencesResolveOnDisk(t *testing.T) {
	cfg := helmTemplateFixture(t, stubRender(helmTemplateThreeGroupChart))
	ml := &layout.ManifestLayout{Name: "myapp", Namespace: "team"}
	if err := cfg.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if len(ml.Children) < 2 {
		t.Fatalf("test setup: expected several hook groups to produce children, got %d", len(ml.Children))
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
		for line := range strings.SplitSeq(string(data), "\n") {
			m := resourceLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			ref := filepath.Join(filepath.Dir(kf), m[1])
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
