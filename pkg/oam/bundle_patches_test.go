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
// applies it, after its bundle's Kustomization patches.

const patchedReason = annotationReason + ", set by its bundle's patches"

// fixedConfig generates the same objects on every call.
type fixedConfig struct{ objects []*client.Object }

func (c *fixedConfig) Generate(*stack.Application) ([]*client.Object, error) {
	return c.objects, nil
}

func fixedApp(name string, objects ...*client.Object) *stack.Application {
	return stack.NewApplication(name, "shop", &fixedConfig{objects: objects})
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

// TestWarnForcedVolumes_BundlePatchProvenance pins that a patched volume is named
// by the application that generated it, in generation order, even when a patch
// renames it, and that a volume a patch adds to a list envelope is found in a
// bundle that generated none.
func TestWarnForcedVolumes_BundlePatchProvenance(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		// Kinds mixed, so kustomize's legacy sort would reorder them: a
		// ConfigMap, then a claim and a volume, which that sort puts first.
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
			claimWarning("renamed", `component "cache"`, patchedReason),
			`PersistentVolume pv-later (component "later") is force-applied (` + annotationReason + `)` + forcedTail,
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
		got := patchedWarnings(t, &stack.Bundle{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope)},
			Patches: []stack.Patch{{Patch: "- op: add\n  path: /items/-\n  value:\n    apiVersion: v1\n    kind: PersistentVolumeClaim\n" +
				"    metadata:\n      namespace: shop\n      name: added\n      annotations:\n        kustomize.toolkit.fluxcd.io/force: enabled\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}})
		want := []string{claimWarning("added", `component "raw"`, patchedReason)}
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
// kustomize cannot build is warned once and the bundle is checked unpatched.
func TestWarnForcedVolumes_BundlePatchesFailToBuild(t *testing.T) {
	got := patchedWarnings(t, &stack.Bundle{Name: "db",
		Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", forceAnnotated("enabled")))},
		Patches:      []stack.Patch{{Patch: removeForceJSON}, {Patch: "not: [valid"}}})
	if len(got) != 2 {
		t.Fatalf("warnings = %q, want a build warning and the unpatched claim's", got)
	}
	if !strings.HasPrefix(got[0], `the patches of the bundle of component "db" could not be applied, so its force-applied volumes are checked as generated, without them: `) {
		t.Errorf("build warning = %q", got[0])
	}
	if want := claimWarning("data", `component "db"`, annotationReason); got[1] != want {
		t.Errorf("claim warning = %q, want %q", got[1], want)
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
