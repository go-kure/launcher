package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These tests pin go-kure/launcher#728: the force warning reads a volume as Flux
// applies it, after its bundle's Kustomization patches; and go-kure/launcher#745:
// a patched volume is named by the generated object it comes from.

const patchedReason = annotationReason + ", set by its bundle's patches"

// fixedConfig generates the same objects on every call.
type fixedConfig struct{ objects []*client.Object }

func (c *fixedConfig) Generate(*stack.Application) ([]*client.Object, error) {
	return c.objects, nil
}

func fixedApp(name string, objects ...*client.Object) *stack.Application {
	return stack.NewApplication(name, "shop", &fixedConfig{objects: objects})
}

// claimMap is a claim in shop as a list envelope's member.
func claimMap(name string, annotated bool) map[string]any {
	metadata := map[string]any{"namespace": "shop", "name": name}
	if annotated {
		metadata["annotations"] = map[string]any{fluxForceAnnotation: fluxForceAnnotationEnabled}
	}
	return map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": metadata}
}

func claimWarning(name, producer, reason string) string {
	return "PersistentVolumeClaim shop/" + name + " (" + producer + ") is force-applied (" + reason + ")" + forcedTail
}

// patchedWarnings generates one document of the given leaf bundles and returns
// WarnForcedVolumes' warnings.
func patchedWarnings(t *testing.T, bundles ...*stack.Bundle) []string {
	t.Helper()
	cluster := &stack.Cluster{Name: "shop", Node: &stack.Node{Name: "shop", Bundle: &stack.Bundle{Name: "shop", Children: bundles}}}
	apps, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	return collectWarnings(apps)
}

func collectWarnings(apps []GeneratedApplication) []string {
	tr := NewTransformer(nil, nil)
	var got []string
	tr.SetWarningHandler(func(msg string) { got = append(got, msg) })
	tr.WarnForcedVolumes(apps)
	return got
}

const (
	addForceSMP = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
  namespace: shop
  annotations:
    kustomize.toolkit.fluxcd.io/force: enabled
`
	addForceJSON = `- op: add
  path: /metadata/annotations/kustomize.toolkit.fluxcd.io~1force
  value: Enabled
`
	removeForceJSON = `- op: remove
  path: /metadata/annotations/kustomize.toolkit.fluxcd.io~1force
`
)

func claimKind() *stack.PatchSelector { return &stack.PatchSelector{Kind: "PersistentVolumeClaim"} }

func TestWarnForcedVolumes_BundlePatches(t *testing.T) {
	plain := func() *client.Object { return claimObject("shop", "data", map[string]string{"owner": "db"}) }
	annotated := func() *client.Object { return claimObject("shop", "data", forceAnnotated("enabled")) }
	bundle := func(force bool, patches []stack.Patch, apps ...*stack.Application) *stack.Bundle {
		b := &stack.Bundle{Name: "db", Applications: apps, Patches: patches}
		if force {
			b.Force = new(true)
		}
		return b
	}
	cases := []struct {
		name   string
		bundle *stack.Bundle
		want   []string
	}{
		{"strategic merge patch adds the annotation",
			bundle(false, []stack.Patch{{Patch: addForceSMP}}, fixedApp("db", plain())),
			[]string{claimWarning("data", `component "db"`, patchedReason)}},
		{"JSON6902 patch adds it in another case, by kind",
			bundle(false, []stack.Patch{{Patch: addForceJSON, Target: claimKind()}}, fixedApp("db", plain())),
			[]string{claimWarning("data", `component "db"`, patchedReason)}},
		{"strategic merge patch adds the label",
			bundle(false, []stack.Patch{{Patch: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\n  namespace: shop\n  labels:\n    kustomize.toolkit.fluxcd.io/force: Enabled\n"}}, fixedApp("db", plain())),
			[]string{claimWarning("data", `component "db"`, patchedReason)}},
		{"target name does not match",
			bundle(false, []stack.Patch{{Patch: addForceJSON, Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "other"}}}, fixedApp("db", plain())),
			nil},
		{"target label selector does not match",
			bundle(false, []stack.Patch{{Patch: addForceJSON, Target: &stack.PatchSelector{LabelSelector: "tier=db"}}}, fixedApp("db", plain())),
			nil},
		{"a later patch selects a label an earlier one set",
			bundle(false, []stack.Patch{
				{Patch: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\n  namespace: shop\n  labels:\n    tier: db\n"},
				{Patch: addForceJSON, Target: &stack.PatchSelector{LabelSelector: "tier=db"}},
			}, fixedApp("db", plain())),
			[]string{claimWarning("data", `component "db"`, patchedReason)}},
		{"a patch removes the annotation",
			bundle(false, []stack.Patch{{Patch: removeForceJSON, Target: claimKind()}}, fixedApp("db", annotated())),
			nil},
		{"a patch removes the annotation of a claim the bundle forces",
			bundle(true, []stack.Patch{{Patch: removeForceJSON, Target: claimKind()}}, fixedApp("db", annotated())),
			[]string{claimWarning("data", `component "db"`, bundleReason)}},
		{"a patch disables it",
			bundle(false, []stack.Patch{{Patch: strings.Replace(addForceSMP, "enabled", "disabled", 1)}}, fixedApp("db", annotated())),
			nil},
		{"a patch deletes the claim",
			bundle(false, []stack.Patch{{Patch: "$patch: delete\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\n  namespace: shop\n"}}, fixedApp("db", annotated())),
			nil},
		{"a generated annotation the patch keeps is not the patch's",
			bundle(false, []stack.Patch{{Patch: addForceSMP}}, fixedApp("db", annotated())),
			[]string{claimWarning("data", `component "db"`, annotationReason)}},
		{"a patch renames the claim",
			bundle(false, []stack.Patch{{Patch: "- op: replace\n  path: /metadata/name\n  value: renamed\n", Target: claimKind()}}, fixedApp("db", annotated())),
			[]string{claimWarning("renamed", `component "db"`, annotationReason)}},
		{"a patch reaches another application's claim of the bundle",
			bundle(false, []stack.Patch{{Patch: addForceSMP}},
				fixedApp("web", claimObject("shop", "web", nil)), fixedApp("cache", plain())),
			[]string{claimWarning("data", `component "cache"`, patchedReason)}},
		{"a list member is patched",
			bundle(false, []stack.Patch{{Patch: addForceJSON, Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "b"}}},
				fixedApp("raw", listObject("List", unstructuredClaim("a", false), unstructuredClaim("b", false)))),
			[]string{claimWarning("b", `component "raw"`, patchedReason)}},
		// The objects are read from the build's YAML, as Flux reads them: a date
		// there is a string, so the annotations beside it stay readable.
		{"a patch adds a date annotation beside the force key",
			bundle(false, []stack.Patch{{Patch: strings.Replace(addForceSMP, "  annotations:\n", "  annotations:\n    reviewed-at: 2026-10-02\n", 1)}},
				fixedApp("db", plain())),
			[]string{claimWarning("data", `component "db"`, patchedReason)}},
		{"a patch adds a list member with a date annotation",
			bundle(false, []stack.Patch{{Patch: "apiVersion: v1\nkind: Widget\nmetadata:\n  name: envelope\nitems:\n" +
				"- apiVersion: v1\n  kind: PersistentVolumeClaim\n  metadata:\n    name: added\n    namespace: shop\n" +
				"    annotations:\n      reviewed-at: 2026-10-02\n      kustomize.toolkit.fluxcd.io/force: enabled\n"}},
				fixedApp("raw", listObject("Widget", unstructuredClaim("a", false)))),
			[]string{claimWarning("added", `the bundle of component "raw"`, patchedReason)}},
		// Flux's read skips a document that is not a Kubernetes object, so it is
		// never applied.
		{"a patch removes the claim's apiVersion",
			bundle(false, []stack.Patch{{Patch: "- op: remove\n  path: /apiVersion\n", Target: claimKind()}}, fixedApp("db", annotated())),
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := patchedWarnings(t, tc.bundle)
			if !slices.Equal(got, tc.want) {
				t.Errorf("warnings =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestWarnForcedVolumes_BundlePatchScope pins that a bundle's patches reach only
// its own objects.
func TestWarnForcedVolumes_BundlePatchScope(t *testing.T) {
	got := patchedWarnings(t,
		&stack.Bundle{Name: "a", Applications: []*stack.Application{fixedApp("a", claimObject("shop", "data", nil))},
			Patches: []stack.Patch{{Patch: addForceJSON, Target: claimKind()}}},
		&stack.Bundle{Name: "b", Applications: []*stack.Application{fixedApp("b", claimObject("shop", "other", nil))}},
	)
	want := []string{claimWarning("data", `component "a"`, patchedReason)}
	if !slices.Equal(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
}

// TestWarnForcedVolumes_BundlePatchProvenance pins that each patched volume is
// forced exactly as Flux's build forces it, whatever the patches do to names,
// lists and annotations, and how it is named: by the generated object the
// attribution build traces it to, in generation order, else by the first
// application that generates a volume of its final identity, else by its bundle,
// after the traced ones in kustomize's build order, with a force key named as the
// patches' unless the volume it is named by carried it.
func TestWarnForcedVolumes_BundlePatchProvenance(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		// Kinds mixed, so kustomize's legacy sort would reorder them: a
		// ConfigMap, then a claim and a volume, which that sort puts first. The
		// patch replaces the claim's annotations, so the build cannot trace it.
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{
			fixedApp("config", configMap("shop", "settings")),
			fixedApp("web", claimObject("shop", "web", forceAnnotated("enabled"))),
			fixedApp("cache", claimObject("shop", "data", nil)),
			fixedApp("later", volumeObject("pv-later", forceAnnotated("enabled"))),
		}, Patches: []stack.Patch{{Patch: "- op: replace\n  path: /metadata/name\n  value: renamed\n" +
			"- op: add\n  path: /metadata/annotations\n  value:\n    kustomize.toolkit.fluxcd.io/force: enabled\n",
			Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "data"}}}})
		want := []string{
			claimWarning("web", `component "web"`, annotationReason),
			`PersistentVolume pv-later (component "later") is force-applied (` + annotationReason + `)` + forcedTail,
			claimWarning("renamed", `the bundle of component "config"`, patchedReason),
		}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("envelope gains a volume", func(t *testing.T) {
		envelope := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    []any{map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"namespace": "shop", "name": "cm"}}},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("config", configMap("shop", "settings")), fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: add\n  path: /items/-\n  value:\n    apiVersion: v1\n    kind: PersistentVolumeClaim\n" +
				"    metadata:\n      namespace: shop\n      name: added\n      annotations:\n        kustomize.toolkit.fluxcd.io/force: enabled\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("added", `the bundle of component "config"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("list member renamed", func(t *testing.T) {
		// kustomize builds a List's members after the bundle's other objects; the
		// warnings follow generation order.
		list := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "List", "items": []any{claimMap("data", true)},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{
			fixedApp("config", configMap("shop", "settings")),
			fixedApp("list", list),
			fixedApp("tail", configMap("shop", "other"), claimObject("shop", "tail", forceAnnotated("enabled"))),
		}, Patches: []stack.Patch{{Patch: "- op: replace\n  path: /metadata/name\n  value: renamed\n",
			Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "data"}}}})
		want := []string{
			claimWarning("renamed", `component "list"`, annotationReason),
			claimWarning("tail", `component "tail"`, annotationReason),
		}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("identity reused by another rename", func(t *testing.T) {
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{
			fixedApp("config", configMap("shop", "settings")),
			fixedApp("old", claimObject("shop", "data", nil)),
			fixedApp("new", claimObject("shop", "replacement", forceAnnotated("enabled"))),
		}, Patches: []stack.Patch{
			{Patch: "- op: replace\n  path: /metadata/name\n  value: archive\n",
				Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "data"}},
			{Patch: "- op: replace\n  path: /metadata/name\n  value: data\n",
				Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "replacement"}},
		}})
		want := []string{claimWarning("data", `component "new"`, annotationReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("envelope member renamed", func(t *testing.T) {
		envelope := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    []any{claimMap("data", true)},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: replace\n  path: /items/0/metadata/name\n  value: renamed\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("renamed", `component "raw"`, annotationReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("whole annotations tested", func(t *testing.T) {
		// The patch sees the annotations exactly as Flux's build does: kustomize's
		// own entries and nothing more.
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", nil))},
			Patches: []stack.Patch{{Patch: "- op: test\n  path: /metadata/annotations\n  value:\n" +
				"    internal.config.kubernetes.io/previousNames: data\n" +
				"    internal.config.kubernetes.io/previousNamespaces: shop\n" +
				"    internal.config.kubernetes.io/previousKinds: PersistentVolumeClaim\n" +
				"- op: add\n  path: /metadata/annotations/kustomize.toolkit.fluxcd.io~1force\n  value: enabled\n",
				Target: claimKind()}}})
		want := []string{claimWarning("data", `component "db"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("envelope member's annotations tested", func(t *testing.T) {
		member := claimMap("data", false)
		member["metadata"].(map[string]any)["annotations"] = map[string]any{}
		envelope := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    []any{member},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: test\n  path: /items/0/metadata/annotations\n  value: {}\n" +
				"- op: add\n  path: /items/0/metadata/annotations/kustomize.toolkit.fluxcd.io~1force\n  value: enabled\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("data", `component "raw"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("envelope member's annotations copied", func(t *testing.T) {
		envelope := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    []any{claimMap("a", true), claimMap("b", false)},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: copy\n  from: /items/0/metadata/annotations\n  path: /items/1/metadata/annotations\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("a", `component "raw"`, annotationReason), claimWarning("b", `component "raw"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("annotations replaced, then identity reused", func(t *testing.T) {
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{
			fixedApp("old", claimObject("shop", "data", nil)),
			fixedApp("new", claimObject("shop", "replacement", forceAnnotated("enabled"))),
		}, Patches: []stack.Patch{
			{Patch: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: replacement\n  namespace: shop\n" +
				"  annotations:\n    $patch: replace\n    kustomize.toolkit.fluxcd.io/force: enabled\n"},
			{Patch: "- op: replace\n  path: /metadata/name\n  value: archive\n",
				Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "data"}},
			{Patch: "- op: replace\n  path: /metadata/name\n  value: data\n",
				Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: "replacement"}},
		}})
		want := []string{claimWarning("data", `component "old"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("annotations replaced", func(t *testing.T) {
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{
			fixedApp("config", configMap("shop", "settings")),
			fixedApp("db", claimObject("shop", "data", nil)),
		}, Patches: []stack.Patch{{Patch: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\n  namespace: shop\n" +
			"  annotations:\n    $patch: replace\n    kustomize.toolkit.fluxcd.io/force: enabled\n"}}})
		want := []string{claimWarning("data", `component "db"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("envelope members' names swapped", func(t *testing.T) {
		envelope := collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    []any{claimMap("a", true), claimMap("b", false)},
		}})
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: replace\n  path: /items/0/metadata/name\n  value: b\n" +
				"- op: replace\n  path: /items/1/metadata/name\n  value: a\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("b", `component "raw"`, annotationReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("the tag moved into labels", func(t *testing.T) {
		// The attribution build differs from Flux's, so nothing is traced.
		got := patchedWarnings(t, &stack.Bundle{Name: "db", Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", forceAnnotated("enabled")))},
			Patches: []stack.Patch{{Patch: "- op: copy\n  from: /metadata/annotations\n  path: /metadata/labels\n" +
				"- op: replace\n  path: /metadata/name\n  value: renamed\n",
				Target: claimKind()}}})
		want := []string{claimWarning("renamed", `the bundle of component "db"`, patchedReason)}
		if !slices.Equal(got, want) {
			t.Errorf("warnings =\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("a volume-less bundle's patches that do not build", func(t *testing.T) {
		got := patchedWarnings(t, &stack.Bundle{Name: "c", Applications: []*stack.Application{fixedApp("c", configMap("shop", "web"))},
			Patches: []stack.Patch{{Patch: "not: [valid"}}})
		if len(got) != 1 || !strings.HasPrefix(got[0], `the patches of the bundle of component "c" could not be applied`) {
			t.Errorf("warnings = %q, want one build warning", got)
		}
	})
}

// TestWarnForcedVolumes_BundlePatchesFailToBuild pins the fallback: a patch set
// Flux cannot build, or whose build it cannot read, is warned once and the bundle
// is checked unpatched.
func TestWarnForcedVolumes_BundlePatchesFailToBuild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		patches []stack.Patch
	}{
		{"a patch does not parse", []stack.Patch{{Patch: removeForceJSON}, {Patch: "not: [valid"}}},
		// Kustomize builds it, but the result cannot be serialized, which fails
		// Flux's build too.
		{"the result does not serialize", []stack.Patch{
			{Patch: "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\n  namespace: shop\n  annotations:\n    123: hello\n"}}},
		// Kustomize builds it, but Flux's read fails on a list member that is not
		// an object.
		{"a list member is not an object", []stack.Patch{
			{Patch: "- op: add\n  path: /items\n  value: [broken]\n", Target: claimKind()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := patchedWarnings(t, &stack.Bundle{Name: "db",
				Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", forceAnnotated("enabled")))},
				Patches:      tc.patches})
			if len(got) != 2 {
				t.Fatalf("warnings = %q, want a build warning and the unpatched claim's", got)
			}
			if !strings.HasPrefix(got[0], `the patches of the bundle of component "db" could not be applied, so its force-applied volumes are checked as generated, without them: `) {
				t.Errorf("build warning = %q", got[0])
			}
			if want := claimWarning("data", `component "db"`, annotationReason); got[1] != want {
				t.Errorf("claim warning = %q, want %q", got[1], want)
			}
		})
	}
}

// TestWarnForcedVolumes_CallerBuiltPatches pins that an application a caller
// built carries its own patches, applied to its objects alone.
func TestWarnForcedVolumes_CallerBuiltPatches(t *testing.T) {
	app := generatedApp("db", "", claimObject("shop", "data", nil))
	app.Patches = []stack.Patch{{Patch: addForceSMP}}
	got := collectWarnings([]GeneratedApplication{app, generatedApp("cache", "", claimObject("shop", "cache", nil))})
	want := []string{claimWarning("data", `component "db"`, patchedReason)}
	if !slices.Equal(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
}
