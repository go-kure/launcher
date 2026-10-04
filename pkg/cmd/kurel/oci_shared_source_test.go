package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// Two oci components on one artifact (go-kure/launcher#784). When their
// effective intervals are equal they share one OCIRepository, which belongs to
// the document rather than to the component that comes first: it is named
// <document>-source-<digest>, each Kustomization is ordered after it, and the
// application bundle itself holds it, ahead of the group with both
// Kustomizations. When the intervals differ each component keeps a source of its
// own, named after it and polling at its own interval, so neither component's
// interval is replaced by the other's, and nothing is ordered.

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
			// The rule orders each Kustomization after the shared source, so the
			// source is a generated one: the application bundle holds it, ahead
			// of the one group with both Kustomizations, which nothing orders.
			assertSourcesInApplicationBundle(t, cluster, shared)
			if got, want := groupNames(t, cluster), []string{"shop-00: base addons"}; !slices.Equal(got, want) {
				t.Errorf("groups = %v, want %v", got, want)
			}
		})
	}
}

// A placement of a consumer leaves the shared source where it is: with the
// application bundle, ahead of every group, so the consumer placed first and
// the one placed last both follow it.
func TestBuild_OCISharedSourceStaysAheadOfPlacedConsumers(t *testing.T) {
	app := ociSharedApp("", "") + `  policies:
    - name: base-first
      type: placement
      properties:
        component: base
        tier: infra
    - name: addons-last
      type: placement
      properties:
        component: addons
        tier: apps
`
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertSourcesInApplicationBundle(t, cluster, ociSharedSourceName("1h0m0s"))
	if got, want := groupNames(t, cluster), []string{"shop-infra: base", "shop-apps: addons"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// ociObjectsOf returns the objects the applications of bundle generate, each as
// "<kind>/<name>", in order.
func ociObjectsOf(t *testing.T, bundle *stack.Bundle) []string {
	t.Helper()
	var out []string
	for _, a := range bundle.Applications {
		objs, err := a.Config.Generate(a)
		if err != nil {
			t.Fatalf("Generate %s: %v", a.Name, err)
		}
		for _, o := range objs {
			out = append(out, (*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName())
		}
	}
	return out
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

	// Each component's two objects deploy as one unit, and neither source is a
	// generated one: nothing is ordered, so the application is one flat bundle
	// holding one application per component. A hoisted source would instead
	// give the bundle a child group.
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	root := cluster.Node.Bundle
	if root == nil || root.Name != "shop" || len(root.Children) != 0 || len(cluster.Node.Children) != 0 {
		t.Fatalf("root bundle = %v, want the one flat bundle shop", root)
	}
	wantObjects := []string{"OCIRepository/base", "Kustomization/base", "OCIRepository/addons", "Kustomization/addons"}
	if got := ociObjectsOf(t, root); !slices.Equal(got, wantObjects) {
		t.Errorf("bundle shop holds %v, want %v", got, wantObjects)
	}
}

// A component alone on its artifact deploys its source and its Kustomization as
// one unit: the source is a member of the component's same-name group, not a
// generated source, so the application bundle does not take it. A tier
// annotation or a placement policy naming the component moves both objects, and
// a dependency rule orders both after another component.
func TestBuild_OCIOwnSourceStaysWithItsComponent(t *testing.T) {
	const header = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: webservice
      annotations:
        ` + kurelDomain + `/tier: apps
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
			assertSourcesInApplicationBundle(t, cluster)
			if got, want := groupNames(t, cluster), []string{"shop-infra: manifests", "shop-apps: api"}; !slices.Equal(got, want) {
				t.Fatalf("groups = %v, want %v", got, want)
			}
			if got, want := ociObjectsOf(t, cluster.Node.Bundle.Children[0]), []string{"OCIRepository/manifests", "Kustomization/manifests"}; !slices.Equal(got, want) {
				t.Errorf("shop-infra holds %v, want %v", got, want)
			}
		})
	}

	t.Run("dependency rule", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, strings.Replace(header, "      annotations:\n        "+kurelDomain+"/tier: apps\n", "", 1)+props+`  policies:
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
		assertSourcesInApplicationBundle(t, cluster)
		if got, want := groupNames(t, cluster), []string{"shop-00: api", "shop-01: manifests"}; !slices.Equal(got, want) {
			t.Fatalf("groups = %v, want %v", got, want)
		}
		if got, want := ociObjectsOf(t, cluster.Node.Bundle.Children[1]), []string{"OCIRepository/manifests", "Kustomization/manifests"}; !slices.Equal(got, want) {
			t.Errorf("shop-01 holds %v, want %v", got, want)
		}
	})

	// Nothing declared: the pair orders nothing by itself, so the application
	// stays one flat bundle with the source in it.
	t.Run("nothing declared", func(t *testing.T) {
		cluster, _, err := transformWithBuiltins(t, strings.Replace(header, "      annotations:\n        "+kurelDomain+"/tier: apps\n", "", 1)+props)
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		root := cluster.Node.Bundle
		if root == nil || len(root.Children) != 0 || len(root.Applications) != 2 {
			t.Fatalf("root bundle = %v, want one flat bundle holding api and manifests", root)
		}
	})
}

// A trait that covers every object of its component reaches both objects of an
// oci component with a source of its own, as it did when one handler generated
// the two: every application that holds one of them takes the delivery intent
// (go-kure/launcher#782). kurel's output, which has no place for the intent,
// carries no Flux annotation for it.
func TestBuild_OCIPruneProtectionReachesBothObjects(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
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
`
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var protected, unprotected []string
	for _, b := range leafBundles(cluster.Node) {
		for _, a := range b.Applications {
			objs, err := a.Config.Generate(a)
			if err != nil {
				t.Fatalf("Generate %s: %v", a.Name, err)
			}
			for _, o := range objs {
				kind := (*o).GetObjectKind().GroupVersionKind().Kind
				if a.Delivery.PruneProtection {
					protected = append(protected, kind)
				} else {
					unprotected = append(unprotected, kind+"/"+(*o).GetName())
				}
			}
		}
	}
	slices.Sort(protected)
	if want := []string{"Kustomization", "OCIRepository"}; !slices.Equal(protected, want) {
		t.Errorf("kinds under the prune-protection intent = %v, want %v", protected, want)
	}
	if len(unprotected) != 0 {
		t.Errorf("objects of an application without the intent: %v, want none", unprotected)
	}

	docs, out := buildStdoutDocs(t, app)
	if len(docs) != 2 {
		t.Fatalf("kurel wrote %d documents, want the OCIRepository and the Kustomization\noutput:\n%s", len(docs), out)
	}
	assertNoFluxObjectKeys(t, docs...)
}
