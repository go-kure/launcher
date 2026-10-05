package kurel

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin what the delivery settings of a bundle reach under per-layout
// placement, through kurel's own transformer and the base library's layout
// walker and Flux integrator (go-kure/kure#1016). Launcher sets none of them
// (go-kure/launcher#781): a consumer sets them on a bundle launcher returned.
// The Kustomization of a component's layout, and of each hook-group layout
// launcher adds below it, then takes five of them from that bundle: wait,
// timeout, retry interval, labels and annotations.

// kustomizationSettings are the five settings a per-layout Kustomization takes
// from the bundle that holds its application.
type kustomizationSettings struct {
	Wait          bool
	Timeout       string
	RetryInterval string
	Labels        map[string]string
	Annotations   map[string]string
}

// treeKustomizationSettings returns the settings of every Flux Kustomization
// in the tree under root, by name. An unset duration is "".
func treeKustomizationSettings(root *layout.ManifestLayout) map[string]kustomizationSettings {
	out := map[string]kustomizationSettings{}
	var walk func(ml *layout.ManifestLayout)
	walk = func(ml *layout.ManifestLayout) {
		for _, o := range ml.Resources {
			kz, ok := o.(*kustv1.Kustomization)
			if !ok {
				continue
			}
			s := kustomizationSettings{Wait: kz.Spec.Wait, Labels: kz.Labels, Annotations: kz.Annotations}
			if kz.Spec.Timeout != nil {
				s.Timeout = kz.Spec.Timeout.Duration.String()
			}
			if kz.Spec.RetryInterval != nil {
				s.RetryInterval = kz.Spec.RetryInterval.Duration.String()
			}
			out[kz.Name] = s
		}
		for _, child := range ml.Children {
			walk(child)
		}
	}
	walk(root)
	return out
}

// bundleSettingsTree is hookGroupTree with set called on every bundle before
// the tree is walked, as a consumer sets a delivery field on a bundle launcher
// returned. A flat application has one bundle; an ordered one has its own and
// one per group below it.
func bundleSettingsTree(t *testing.T, doc string, set func(b *stack.Bundle)) (*layout.ManifestLayout, error) {
	t.Helper()
	cluster, err := hookGroupCluster(t, doc, oam.TransformContext{})
	if err != nil {
		t.Fatalf("transforming: %v", err)
	}
	var bundles func(b *stack.Bundle)
	bundles = func(b *stack.Bundle) {
		if b == nil {
			return
		}
		set(b)
		for _, child := range b.Children {
			bundles(child)
		}
	}
	var nodes func(n *stack.Node)
	nodes = func(n *stack.Node) {
		if n == nil {
			return
		}
		bundles(n.Bundle)
		for _, child := range n.Children {
			nodes(child)
		}
	}
	nodes(cluster.Node)
	root, err := layout.WalkCluster(cluster, perLayoutRules())
	if err != nil {
		t.Fatalf("walking: %v", err)
	}
	if err := integrateHookGroupTree(root, cluster); err != nil {
		return nil, err
	}
	return root, nil
}

// setBundleSettings sets the five on b, as a consumer does, and
// bundleSettingsSet is what a Kustomization that takes them then carries.
func setBundleSettings(b *stack.Bundle) {
	wait := true
	b.Wait = &wait
	b.Timeout = "5m"
	b.RetryInterval = "1m"
	b.Labels = map[string]string{"team": "shop"}
	b.Annotations = map[string]string{"example.org/owner": "shop"}
}

var bundleSettingsSet = kustomizationSettings{
	Wait:          true,
	Timeout:       "5m0s",
	RetryInterval: "1m0s",
	Labels:        map[string]string{"team": "shop"},
	Annotations:   map[string]string{"example.org/owner": "shop"},
}

// bundleSettingsChain is the tree of a flat application with one helmtemplate
// component of three hook groups: every Kustomization, with its spec.dependsOn.
// The settings of a bundle change none of it.
var bundleSettingsChain = map[string][]string{
	"shop":                    nil,
	"shop-db":                 nil,
	"shop-db-00-pre-install":  nil,
	"shop-db-01-main":         {"shop-db-00-pre-install"},
	"shop-db-02-post-install": {"shop-db-01-main"},
}

// A bundle as launcher returns it sets none of the five, and no Kustomization
// of the tree carries one. A bundle a consumer set them on gives them to its own
// Kustomization, to the component's and to each hook group's; the tree still
// integrates, since a hook group depends on the group before it and not on the
// layout above, and spec.dependsOn chains the groups as before.
func TestBundleSettings_ReachPerLayoutKustomizations(t *testing.T) {
	doc := hookApp("shop", hookComponent("db", "helmtemplate", serveHookChart(t), ""), "")
	for _, tc := range []struct {
		name string
		set  func(b *stack.Bundle)
		want kustomizationSettings
	}{
		{name: "a bundle as launcher returns it", set: func(*stack.Bundle) {}},
		{name: "a bundle with the five settings", set: setBundleSettings, want: bundleSettingsSet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := bundleSettingsTree(t, doc, tc.set)
			if err != nil {
				t.Fatalf("integrating: %v", err)
			}
			if got := treeKustomizations(root); !reflect.DeepEqual(got, bundleSettingsChain) {
				t.Errorf("Kustomizations (name: dependsOn) = %v\nwant %v", got, bundleSettingsChain)
			}
			got := treeKustomizationSettings(root)
			if names, want := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(bundleSettingsChain)); !slices.Equal(names, want) {
				t.Fatalf("Kustomizations = %v, want %v", names, want)
			}
			for _, name := range slices.Sorted(maps.Keys(got)) {
				if !reflect.DeepEqual(got[name], tc.want) {
					t.Errorf("Kustomization %s has %+v, want %+v", name, got[name], tc.want)
				}
			}
		})
	}
}

// In an ordered application the components are in the group bundles below the
// application's bundle, and a per-layout Kustomization takes the five from the
// bundle that holds its application: the group's. Set on the application's
// bundle alone they reach its own Kustomization and no other; set on a group's
// bundle they reach that group's Kustomization, its component's and the
// component's hook groups', and none of another group. Each group here holds a
// helmtemplate component, so each has per-layout Kustomizations to keep bare.
func TestBundleSettings_OrderedApplicationTakesTheGroupBundle(t *testing.T) {
	const placed = `  policies:
    - name: db-first
      type: placement
      properties:
        component: db
        tier: infra
    - name: web-last
      type: placement
      properties:
        component: web
        tier: apps
`
	url := serveHookChart(t)
	doc := hookApp("shop", hookComponent("db", "helmtemplate", url, "")+hookComponent("web", "helmtemplate", url, ""), placed)
	infra := []string{"shop-db-00-pre-install", "shop-db-01-main", "shop-db-02-post-install", "shop-infra", "shop-infra-db"}
	apps := []string{"shop-apps", "shop-apps-web", "shop-web-00-pre-install", "shop-web-01-main", "shop-web-02-post-install"}
	all := slices.Sorted(slices.Values(slices.Concat([]string{"shop"}, infra, apps)))
	for _, tc := range []struct {
		bundle string
		want   []string
	}{
		{bundle: "shop", want: []string{"shop"}},
		{bundle: "shop-infra", want: infra},
		{bundle: "shop-apps", want: apps},
	} {
		t.Run("set on bundle "+tc.bundle, func(t *testing.T) {
			found := false
			root, err := bundleSettingsTree(t, doc, func(b *stack.Bundle) {
				if b.Name == tc.bundle {
					found = true
					setBundleSettings(b)
				}
			})
			if err != nil {
				t.Fatalf("integrating: %v", err)
			}
			if !found {
				t.Fatalf("the cluster has no bundle %q", tc.bundle)
			}
			got := treeKustomizationSettings(root)
			if names := slices.Sorted(maps.Keys(got)); !slices.Equal(names, all) {
				t.Fatalf("Kustomizations = %v, want %v", names, all)
			}
			for _, name := range all {
				want := kustomizationSettings{}
				if slices.Contains(tc.want, name) {
					want = bundleSettingsSet
				}
				if !reflect.DeepEqual(got[name], want) {
					t.Errorf("Kustomization %s has %+v, want %+v", name, got[name], want)
				}
			}
		})
	}
}

// Two settings the base library's Flux integration refuses on a bundle
// launcher returned. A label the Kubernetes API does not accept is refused
// where a per-layout Kustomization inherits it, naming the component's layout
// and the bundle; the bundle's own Kustomization carries its labels unchecked.
// A duration of a microsecond, which the Flux API does not take, is refused
// where the bundle's own Kustomization is written. Launcher sets neither: the
// refusal reaches a consumer that set one. The texts are the base library's,
// held here so that a pin move that changes one is seen.
func TestBundleSettings_Refused(t *testing.T) {
	doc := hookApp("shop", hookComponent("db", "helmtemplate", serveHookChart(t), ""), "")
	const durationRefusal = `validation failed for Bundle 'shop' field 'timeout': timeout "1us" is written as "1µs", which the Flux API does not take: it takes digits with a unit of ms, s, m or h, so no negative duration and none under a millisecond`
	for _, tc := range []struct {
		name string
		set  func(b *stack.Bundle)
		// want is the whole text, or its beginning when rest is set: what
		// follows is then the Kubernetes validator's own wording.
		want string
		rest bool
	}{
		{
			name: "a label value the Kubernetes API does not accept",
			set:  func(b *stack.Bundle) { b.Labels = map[string]string{"team": "the shop"} },
			want: `validation failed for ManifestLayout 'cluster/shop/db' field 'labels': labels entry "team" (inherited from bundle "shop") is not one the Kubernetes API accepts on the layout's Flux Kustomization: metadata.labels: Invalid value: "the shop": `,
			rest: true,
		},
		{
			name: "a timeout of a microsecond",
			set:  func(b *stack.Bundle) { b.Timeout = "1us" },
			// The workflow wraps the generator's refusal and states it twice.
			want: `validation failed for Bundle 'shop' field 'flux-resources': failed to generate Flux resources: ` + durationRefusal + `: ` + durationRefusal,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bundleSettingsTree(t, doc, tc.set)
			if err == nil {
				t.Fatal("the tree integrated, want a refusal")
			}
			got := err.Error()
			if tc.rest && strings.HasPrefix(got, tc.want) && len(got) > len(tc.want) {
				return
			}
			if got != tc.want {
				t.Errorf("refused with\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
