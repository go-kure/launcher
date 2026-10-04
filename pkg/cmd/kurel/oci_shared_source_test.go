package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// Two oci components on one artifact (go-kure/launcher#784). When their
// effective intervals are equal they share one OCIRepository, which belongs to
// the document rather than to the component that comes first: it is named
// <document>-source-<digest> and deploys in the infra tier, ahead of both
// Kustomizations. When the intervals differ each component keeps a source of its
// own, named after it and polling at its own interval, so neither component's
// interval is replaced by the other's.

// ociSharedApp is a document with two oci components on one artifact, each with
// the given interval line ("" for unset).
func ociSharedApp(baseInterval, addonsInterval string) string {
	interval := func(v string) string {
		if v == "" {
			return ""
		}
		return "        interval: " + v + "\n"
	}
	return `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: base
      type: oci
      properties:
        source:
          url: oci://registry.example.com/org/platform
        version: 1.4.0
        path: ./base
` + interval(baseInterval) + `    - name: addons
      type: oci
      properties:
        source:
          url: oci://registry.example.com/org/platform
        version: 1.4.0
        path: ./addons
` + interval(addonsInterval)
}

// ociSharedSourceName is the name of the source two oci components share, for
// the given effective interval as time.Duration prints it.
func ociSharedSourceName(interval string) string {
	identity := `oci-artifact:{"url":"oci://registry.example.com/org/platform","version":"1.4.0","interval":"` + interval + `"}`
	sum := sha256.Sum256([]byte(identity))
	return "shop-source-" + hex.EncodeToString(sum[:])[:10]
}

// ociSourcesAndRefs returns the interval of every OCIRepository in docs, keyed
// by name, and every Kustomization's source reference as
// "Kustomization/<name>-><kind>/<name>", sorted.
func ociSourcesAndRefs(docs []map[string]any) (map[string]string, []string) {
	sources := map[string]string{}
	var refs []string
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		spec, _ := d["spec"].(map[string]any)
		switch d["kind"] {
		case "OCIRepository":
			sources[fmt.Sprint(md["name"])] = fmt.Sprint(spec["interval"])
		case "Kustomization":
			ref, _ := spec["sourceRef"].(map[string]any)
			refs = append(refs, fmt.Sprintf("Kustomization/%v->%v/%v", md["name"], ref["kind"], ref["name"]))
		}
	}
	slices.Sort(refs)
	return sources, refs
}

func TestBuild_OCISameArtifact_EqualIntervalsShareOneSource(t *testing.T) {
	// Unset and 1h are the same effective interval: the 60m default.
	for name, app := range map[string]string{
		"both unset":           ociSharedApp("", ""),
		"unset and 1h":         ociSharedApp("", "1h"),
		"60m and 1h":           ociSharedApp("60m", "1h"),
		"second written first": ociSharedApp("1h", ""),
	} {
		t.Run(name, func(t *testing.T) {
			docs, out := buildStdoutDocs(t, app)
			shared := ociSharedSourceName("1h0m0s")
			sources, refs := ociSourcesAndRefs(docs)
			if len(sources) != 1 || sources[shared] != "1h0m0s" {
				t.Fatalf("OCIRepositories = %v, want the one shared source %s at 1h0m0s\noutput:\n%s", sources, shared, out)
			}
			want := []string{"Kustomization/addons->OCIRepository/" + shared, "Kustomization/base->OCIRepository/" + shared}
			if !slices.Equal(refs, want) {
				t.Errorf("source references = %v, want %v", refs, want)
			}

			cluster, _, err := transformWithBuiltins(t, app)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			bundles := leafBundles(cluster.Node)
			if got := bundleHolding(bundles, shared); got != "shop-infra" {
				t.Errorf("shared source is in bundle %q, want shop-infra (bundles: %v)", got, slices.Sorted(maps.Keys(bundles)))
			}
			for _, comp := range []string{"base", "addons"} {
				if got := bundleHolding(bundles, comp); got != "shop-apps" {
					t.Errorf("%s is in bundle %q, want shop-apps", comp, got)
				}
			}
		})
	}
}

func TestBuild_OCISameArtifact_DifferentIntervalsKeepTwoSources(t *testing.T) {
	app := ociSharedApp("5m", "")
	docs, out := buildStdoutDocs(t, app)
	sources, refs := ociSourcesAndRefs(docs)
	if want := map[string]string{"base": "5m0s", "addons": "1h0m0s"}; !maps.Equal(sources, want) {
		t.Fatalf("OCIRepositories = %v, want %v\noutput:\n%s", sources, want, out)
	}
	want := []string{"Kustomization/addons->OCIRepository/addons", "Kustomization/base->OCIRepository/base"}
	if !slices.Equal(refs, want) {
		t.Errorf("source references = %v, want %v", refs, want)
	}

	// Each component's two objects deploy as one unit, in its type's tier. Both
	// components are in the apps tier and neither source is a generated one, so
	// the document stays one bundle, named after it: a source in the infra tier
	// would split it into shop-infra and shop-apps.
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	bundles := leafBundles(cluster.Node)
	if got := slices.Sorted(maps.Keys(bundles)); !slices.Equal(got, []string{"shop"}) {
		t.Fatalf("bundles = %v, want the one bundle shop", got)
	}
	for _, comp := range []string{"base", "addons"} {
		if got := bundleHolding(bundles, comp); got != "shop" {
			t.Errorf("%s is in bundle %q, want shop", comp, got)
		}
	}
}

// A component alone on its artifact deploys its source and its Kustomization as
// one unit, so a tier annotation or a placement policy naming the component
// moves both, and a dependency rule may order the component after another.
func TestBuild_OCIOwnSourceFollowsItsComponentsTier(t *testing.T) {
	const header = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: webservice
      properties:
        image: nginx:1.27
    - name: manifests
      type: oci
`
	const props = `      properties:
        source:
          url: oci://registry.example.com/org/platform
        version: 1.4.0
`
	cases := map[string]struct {
		app          string
		wantOverride bool
	}{
		"tier annotation": {app: header + "      annotations:\n        " + oam.TierAnnotationKey(kurelDomain) + ": infra\n" + props},
		"placement policy": {app: header + props + `  policies:
    - name: manifests-first
      type: placement
      properties:
        component: manifests
        tier: infra
`, wantOverride: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cluster, result, err := transformWithBuiltins(t, tc.app)
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if tc.wantOverride && result.TierOverrides["manifests"] != oam.TierInfra {
				t.Fatalf("TierOverrides = %v, want manifests placed in infra", result.TierOverrides)
			}
			bundles := leafBundles(cluster.Node)
			if got := bundleHolding(bundles, "manifests"); got != "shop-infra" {
				t.Fatalf("manifests is in bundle %q, want shop-infra (bundles: %v)", got, slices.Sorted(maps.Keys(bundles)))
			}
			var kinds []string
			for _, a := range bundles["shop-infra"].Applications {
				objs, err := a.Config.Generate(a)
				if err != nil {
					t.Fatalf("Generate %s: %v", a.Name, err)
				}
				for _, o := range objs {
					kinds = append(kinds, (*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName())
				}
			}
			if want := []string{"OCIRepository/manifests", "Kustomization/manifests"}; !slices.Equal(kinds, want) {
				t.Errorf("shop-infra holds %v, want %v", kinds, want)
			}
		})
	}

	t.Run("dependency rule", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, header+props+`  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: manifests
            dependsOn: [api]
`)
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		bundles := leafBundles(cluster.Node)
		manifests := bundles["shop-manifests"]
		if manifests == nil {
			t.Fatalf("no bundle for manifests, got %v", slices.Sorted(maps.Keys(bundles)))
		}
		var deps []string
		for _, d := range manifests.DependsOn {
			deps = append(deps, d.Name)
		}
		if !slices.Contains(deps, "shop-api") {
			t.Errorf("shop-manifests depends on %v, want shop-api among them", deps)
		}
	})
}

// A trait that decorates every object of its component reaches both objects of
// an oci component with a source of its own, as it did when one handler
// generated the two.
func TestBuild_OCIPruneProtectionReachesBothObjects(t *testing.T) {
	docs, out := buildStdoutDocs(t, `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: manifests
      type: oci
      properties:
        source:
          url: oci://registry.example.com/org/platform
        version: 1.4.0
      traits:
        - type: prune-protection
`)
	var protected []string
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		annotations, _ := md["annotations"].(map[string]any)
		for k, v := range annotations {
			if strings.HasSuffix(k, "/prune") && v == "disabled" {
				protected = append(protected, fmt.Sprint(d["kind"]))
			}
		}
	}
	slices.Sort(protected)
	if want := []string{"Kustomization", "OCIRepository"}; !slices.Equal(protected, want) {
		t.Errorf("prune-protected kinds = %v, want %v\noutput:\n%s", protected, want, out)
	}
}
