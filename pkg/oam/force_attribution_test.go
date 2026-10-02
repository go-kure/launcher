package oam

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These tests pin go-kure/launcher#745: the attribution build only names a
// patched volume, and is built only when a patched bundle has one to name.

// withAttributionBuild replaces attributionBuild for one test.
func withAttributionBuild(t *testing.T, build func([]*client.Object, []stack.Patch) ([]*unstructured.Unstructured, error)) {
	t.Helper()
	saved := attributionBuild
	attributionBuild = build
	t.Cleanup(func() { attributionBuild = saved })
}

// warnedVolumes returns the volumes warnings name, sorted: the verdict, without
// the producer, the reason or the order.
func warnedVolumes(warnings []string) []string {
	var out []string
	for _, w := range warnings {
		volume, _, _ := strings.Cut(w, " (")
		out = append(out, volume)
	}
	slices.Sort(out)
	return out
}

// attributionBundles are patched bundles whose volumes the attribution build
// traces: renamed, swapped, reused identities, list and envelope members.
func attributionBundles() [][]*stack.Bundle {
	envelope := func(items ...any) *client.Object {
		return collisionObject(&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "example.com/v1", "kind": "Widget",
			"metadata": map[string]any{"namespace": "shop", "name": "envelope"},
			"items":    items,
		}})
	}
	rename := func(from, to string) stack.Patch {
		return stack.Patch{Patch: "- op: replace\n  path: /metadata/name\n  value: " + to + "\n",
			Target: &stack.PatchSelector{Kind: "PersistentVolumeClaim", Name: from}}
	}
	return [][]*stack.Bundle{
		{{Name: "db", Applications: []*stack.Application{
			fixedApp("config", configMap("shop", "settings")),
			fixedApp("old", claimObject("shop", "data", nil)),
			fixedApp("new", claimObject("shop", "replacement", forceAnnotated("enabled"))),
		}, Patches: []stack.Patch{rename("data", "archive"), rename("replacement", "data")}}},
		{{Name: "raw", Applications: []*stack.Application{fixedApp("raw", envelope(claimMap("a", true), claimMap("b", false)))},
			Patches: []stack.Patch{{Patch: "- op: replace\n  path: /items/0/metadata/name\n  value: b\n" +
				"- op: replace\n  path: /items/1/metadata/name\n  value: a\n",
				Target: &stack.PatchSelector{Kind: "Widget"}}}}},
		{{Name: "db", Force: new(true), Applications: []*stack.Application{
			fixedApp("list", collisionObject(&unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "List", "items": []any{claimMap("data", false)},
			}})),
			fixedApp("tail", claimObject("shop", "tail", nil)),
		}, Patches: []stack.Patch{rename("data", "renamed")}}},
	}
}

// TestWarnForcedVolumes_AttributionNeverDecides pins that the verdict never
// depends on the attribution build: one that fails, and one whose tags lie,
// warn exactly the volumes the real one does.
func TestWarnForcedVolumes_AttributionNeverDecides(t *testing.T) {
	failing := func([]*client.Object, []stack.Patch) ([]*unstructured.Unstructured, error) {
		return nil, errors.New("attribution build failed")
	}
	// lying builds the bundle as the real attribution build does, then reverses
	// the tags among the objects that carry one, so every traced volume is
	// named by another origin.
	lying := func(objects []*client.Object, patches []stack.Patch) ([]*unstructured.Unstructured, error) {
		out, err := applyBundlePatches(objects, patches)
		if err != nil {
			return nil, err
		}
		var tagged []*unstructured.Unstructured
		var tags []string
		for _, obj := range out {
			if tag, ok := obj.GetAnnotations()[forceOriginAnnotation]; ok {
				tagged = append(tagged, obj)
				tags = append(tags, tag)
			}
		}
		slices.Reverse(tags)
		for i, obj := range tagged {
			annotations := obj.GetAnnotations()
			annotations[forceOriginAnnotation] = tags[i]
			obj.SetAnnotations(annotations)
		}
		return out, nil
	}
	for i := range attributionBundles() {
		real := patchedWarnings(t, attributionBundles()[i]...)
		want := warnedVolumes(real)
		if len(want) == 0 {
			t.Fatalf("bundle set %d warns nothing", i)
		}
		for name, build := range map[string]func([]*client.Object, []stack.Patch) ([]*unstructured.Unstructured, error){
			"failing": failing, "lying": lying,
		} {
			t.Run(name+" build, bundle set "+strconv.Itoa(i), func(t *testing.T) {
				withAttributionBuild(t, build)
				got := patchedWarnings(t, attributionBundles()[i]...)
				if slices.Equal(got, real) {
					t.Errorf("bundle set %d: the %s build named nothing differently, so it pins nothing", i, name)
				}
				if volumes := warnedVolumes(got); !slices.Equal(volumes, want) {
					t.Errorf("bundle set %d: warned volumes = %q, want %q", i, volumes, want)
				}
			})
		}
	}
}

// TestWarnForcedVolumes_AttributionIsLazy pins that the attribution build runs
// only for a patched bundle with a force-applied volume.
func TestWarnForcedVolumes_AttributionIsLazy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle *stack.Bundle
		calls  int
	}{
		{"patched, nothing forced",
			&stack.Bundle{Name: "db", Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", nil))},
				Patches: []stack.Patch{{Patch: "- op: replace\n  path: /metadata/name\n  value: renamed\n", Target: claimKind()}}},
			0},
		{"unpatched, forced",
			&stack.Bundle{Name: "db", Force: new(true), Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", forceAnnotated("enabled")))}},
			0},
		{"patched, forced",
			&stack.Bundle{Name: "db", Applications: []*stack.Application{fixedApp("db", claimObject("shop", "data", forceAnnotated("enabled")))},
				Patches: []stack.Patch{{Patch: "- op: replace\n  path: /metadata/name\n  value: renamed\n", Target: claimKind()}}},
			1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			withAttributionBuild(t, func(objects []*client.Object, patches []stack.Patch) ([]*unstructured.Unstructured, error) {
				calls++
				return applyBundlePatches(objects, patches)
			})
			patchedWarnings(t, tc.bundle)
			if calls != tc.calls {
				t.Errorf("attribution builds = %d, want %d", calls, tc.calls)
			}
		})
	}
}
