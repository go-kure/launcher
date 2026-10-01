package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// helmAppHeader is an application of two helm components sharing one Helm
// repository URL, followed by an open policies list.
const helmAppHeader = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: helm
      properties:
        chart: api
        version: 1.0.0
        source:
          url: https://charts.example.com
    - name: web
      type: helm
      properties:
        chart: web
        version: 2.0.0
        source:
          url: https://charts.example.com
`

// helmSharedSource is the name the helm rule gives the source of
// https://charts.example.com in document shop.
func helmSharedSource() string {
	sum := sha256.Sum256([]byte("helm:https://charts.example.com"))
	return "shop-source-" + hex.EncodeToString(sum[:])[:10]
}

// TestBuildCommand_HelmSharesOneSource is the acceptance case end to end: two
// helm components with one URL build into exactly one HelmRepository, and both
// HelmReleases reference it by its generated name.
func TestBuildCommand_HelmSharesOneSource(t *testing.T) {
	docs, out, err := buildDocs(t, helmAppHeader)
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, out)
	}
	var repos []string
	refs := map[string]string{}
	for _, d := range docs {
		md, _ := d["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		switch d["kind"] {
		case "HelmRepository":
			repos = append(repos, name)
		case "HelmRelease":
			spec, _ := d["spec"].(map[string]any)
			chart, _ := spec["chart"].(map[string]any)
			cspec, _ := chart["spec"].(map[string]any)
			ref, _ := cspec["sourceRef"].(map[string]any)
			refs[name], _ = ref["name"].(string)
		}
	}
	if want := []string{helmSharedSource()}; !slices.Equal(repos, want) {
		t.Errorf("HelmRepositories = %v, want exactly %v\noutput:\n%s", repos, want, out)
	}
	for _, rel := range []string{"api", "web"} {
		if refs[rel] != helmSharedSource() {
			t.Errorf("HelmRelease %s references %q, want %q", rel, refs[rel], helmSharedSource())
		}
	}
}

// TestBuiltinHelm_SourceDeploysInInfra: the generated source is classified into
// the infra tier, ahead of the releases in apps, so the cluster splits into
// tier bundles with the source first.
func TestBuiltinHelm_SourceDeploysInInfra(t *testing.T) {
	cluster, _, err := transformWithBuiltins(t, helmAppHeader)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	bundles := leafBundles(cluster.Node)
	if got := bundleHolding(bundles, helmSharedSource()); got != "shop-infra" {
		t.Errorf("source is in bundle %q, want shop-infra (bundles: %v)", got, slices.Sorted(maps.Keys(bundles)))
	}
	for _, rel := range []string{"api", "web"} {
		if got := bundleHolding(bundles, rel); got != "shop-apps" {
			t.Errorf("%s is in bundle %q, want shop-apps", rel, got)
		}
	}
}

// helmDependsOn is a dependency policy ordering web after api, which makes the
// cluster dependency-aware: one bundle per component, each also depending on
// every bundle of the preceding populated tier.
const helmDependsOn = `  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
`

// assertNoConsumerPrecedesSource checks, on a dependency-aware cluster, that the
// source's bundle depends (transitively) on no release's bundle: a source
// applied only after one of its consumers became ready would never be applied,
// since that consumer waits on its own health check.
func assertNoConsumerPrecedesSource(t *testing.T, cluster *stack.Cluster) {
	t.Helper()
	bundles := leafBundles(cluster.Node)
	source := bundles["shop-"+helmSharedSource()]
	if source == nil {
		t.Fatalf("no bundle for the source, got %v", slices.Sorted(maps.Keys(bundles)))
	}
	seen := map[*stack.Bundle]bool{}
	var walk func(b *stack.Bundle)
	walk = func(b *stack.Bundle) {
		for _, dep := range b.DependsOn {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			for _, rel := range []string{"api", "web"} {
				if bundleHolding(map[string]*stack.Bundle{dep.Name: dep}, rel) != "" {
					t.Errorf("source bundle depends on %s, which holds consumer %s", dep.Name, rel)
				}
			}
			walk(dep)
		}
	}
	walk(source)
}

// TestBuiltinHelm_TierAnnotationInInfraBuilds: a helm component annotated into
// the infra tier keeps its release there, and on the dependency-aware path its
// source is not placed after it, so the cluster builds and the source waits on
// no consumer.
func TestBuiltinHelm_TierAnnotationInInfraBuilds(t *testing.T) {
	app := strings.Replace(helmAppHeader, "      type: helm\n      properties:\n        chart: api",
		"      type: helm\n      annotations:\n        "+oam.TierAnnotationKey(kurelDomain)+": infra\n      properties:\n        chart: api", 1)
	cluster, _, err := transformWithBuiltins(t, app+helmDependsOn)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	bundles := leafBundles(cluster.Node)
	if bundles["shop-api"] == nil {
		t.Fatalf("no bundle for api, got %v", slices.Sorted(maps.Keys(bundles)))
	}
	assertNoConsumerPrecedesSource(t, cluster)
}

// TestBuiltinHelm_PlacementInInfraBuilds: the same with a placement policy,
// which names the release only; the generated source still deploys no later.
func TestBuiltinHelm_PlacementInInfraBuilds(t *testing.T) {
	cluster, result, err := transformWithBuiltins(t, helmAppHeader+helmDependsOn+`    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(result.TierOverrides) == 0 {
		t.Fatalf("TierOverrides empty; the case must exercise placement")
	}
	assertNoConsumerPrecedesSource(t, cluster)
}

// TestBuildCommand_HelmSourceNameTakenByAuthoredComponent pins what happens
// when an authored component already carries the name the rule generates for a
// source (go-kure/launcher#349, Q5): the build fails, naming the duplicate,
// instead of silently merging two objects.
func TestBuildCommand_HelmSourceNameTakenByAuthoredComponent(t *testing.T) {
	app := helmAppHeader + `    - name: ` + helmSharedSource() + `
      type: helmrepository
      properties:
        url: https://other.example.com
`
	_, out, err := buildDocs(t, app)
	if err == nil {
		t.Fatalf("build accepted an authored component named like the generated source\noutput:\n%s", out)
	}
	if !strings.Contains(err.Error(), helmSharedSource()) {
		t.Errorf("error = %q, want it to name %s", err, helmSharedSource())
	}
	if !strings.Contains(err.Error(), "duplicate component name") {
		t.Errorf("error = %q, want the duplicate-name refusal", err)
	}
}

// TestBuiltinHelm_FluxNamespace: under a Flux namespace the generated source
// and the releases land in it, a release's sourceRef (no namespace) resolves to
// the source there, and the release targets the application namespace.
func TestBuiltinHelm_FluxNamespace(t *testing.T) {
	transformer := newBuiltinTransformer()
	app, err := oam.ParseWithExtraTypes([]byte(helmAppHeader), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	cluster, _, err := transformer.TransformWithPolicy(app, oam.TransformContext{Domain: kurelDomain, FluxNamespace: "flux-system"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	var objs []client.Object
	for _, b := range leafBundles(cluster.Node) {
		for _, a := range b.Applications {
			generated, err := a.Config.Generate(a)
			if err != nil {
				t.Fatalf("Generate(%s): %v", a.Name, err)
			}
			for _, o := range generated {
				objs = append(objs, *o)
			}
		}
	}
	var sawSource bool
	for _, o := range objs {
		kind := o.GetObjectKind().GroupVersionKind().Kind
		if kind != "HelmRepository" && kind != "HelmRelease" {
			continue
		}
		if o.GetNamespace() != "flux-system" {
			t.Errorf("%s %s is in namespace %q, want flux-system", kind, o.GetName(), o.GetNamespace())
		}
		sawSource = sawSource || kind == "HelmRepository"
	}
	if !sawSource {
		t.Fatalf("no HelmRepository generated")
	}
}
