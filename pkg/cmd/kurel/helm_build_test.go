package kurel

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
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

// TestBuiltinHelm_GitAndBucketSourcesDeployInInfra: a generated gitrepository
// and bucket are classified into the infra tier like a generated
// helmrepository, ahead of their releases in apps.
func TestBuiltinHelm_GitAndBucketSourcesDeployInInfra(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: api
      type: helm
      properties:
        chart: ./charts/api
        source:
          kind: GitRepository
          url: https://github.com/example/charts
          ref:
            branch: main
    - name: web
      type: helm
      properties:
        chart: charts/web
        source:
          kind: Bucket
          endpoint: minio.example.com
          bucketName: charts
`
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	bundles := leafBundles(cluster.Node)
	for _, identity := range []string{
		`git:{"url":"https://github.com/example/charts","ref":{"branch":"main"}}`,
		`bucket:{"provider":"","endpoint":"minio.example.com","bucketName":"charts","region":"","prefix":""}`,
	} {
		sum := sha256.Sum256([]byte(identity))
		source := "shop-source-" + hex.EncodeToString(sum[:])[:10]
		if got := bundleHolding(bundles, source); got != "shop-infra" {
			t.Errorf("source %s (%s) is in bundle %q, want shop-infra (bundles: %v)", source, identity, got, slices.Sorted(maps.Keys(bundles)))
		}
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

// TestBuiltinHelm_PlacementCannotMoveGeneratedSource: a placement policy naming
// the generated source may keep it in infra, but moving it to a later tier is
// refused. Its consumer, placed in infra, would otherwise never become ready.
func TestBuiltinHelm_PlacementCannotMoveGeneratedSource(t *testing.T) {
	placement := func(tier string) string {
		return `    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
    - name: source-late
      type: placement
      properties:
        component: ` + helmSharedSource() + `
        tier: ` + tier + "\n"
	}
	_, _, err := transformWithBuiltins(t, helmAppHeader+helmDependsOn+placement("apps"))
	if err == nil || !strings.Contains(err.Error(), "placement cannot move helmrepository "+strconv.Quote(helmSharedSource())+" to tier apps") {
		t.Fatalf("Transform error = %v, want the generated source's placement refused", err)
	}
	cluster, result, err := transformWithBuiltins(t, helmAppHeader+helmDependsOn+placement("infra"))
	if err != nil {
		t.Fatalf("Transform with the source placed in infra: %v", err)
	}
	if result.TierOverrides[helmSharedSource()] != oam.TierInfra {
		t.Fatalf("TierOverrides = %v, want the source placed in infra", result.TierOverrides)
	}
	assertNoConsumerPrecedesSource(t, cluster)
}

// TestBuiltinHelm_DependencyCannotDelayGeneratedSource pins the dependency
// counterpart of the placement refusal. With the consumer placed in infra
// beside the source, no cross-tier edge closes a cycle, so a rule making the
// source wait on that consumer would deadlock silently rather than fail.
func TestBuiltinHelm_DependencyCannotDelayGeneratedSource(t *testing.T) {
	apiInInfra := `    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
`
	sourceWaits := helmDependsOn + `          - component: ` + helmSharedSource() + `
            dependsOn: [api]
` + apiInInfra
	_, _, err := transformWithBuiltins(t, helmAppHeader+sourceWaits)
	if err == nil || !strings.Contains(err.Error(), "dependency cannot make helmrepository "+strconv.Quote(helmSharedSource())+" wait on api") {
		t.Fatalf("Transform error = %v, want the generated source's dependency refused", err)
	}
	cluster, _, err := transformWithBuiltins(t, helmAppHeader+helmDependsOn+apiInInfra)
	if err != nil {
		t.Fatalf("Transform with only a consumer depending: %v", err)
	}
	assertNoConsumerPrecedesSource(t, cluster)
}

// appsSourceRule emits a helmrepository annotated into the apps tier, which a
// custom lowering rule may do; the built-in helm rule's sources carry no
// annotation. It declares a schema, so its output counts as synthesized.
type appsSourceRule struct{}

func (appsSourceRule) ComponentType() string { return "apps-source" }

func (appsSourceRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

func (appsSourceRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{
		Name:        comp.Name + "-src",
		Type:        "helmrepository",
		Properties:  map[string]any{"url": "https://charts.example.com"},
		Annotations: map[string]string{oam.TierAnnotationKey(kurelDomain): string(oam.TierApps)},
	}}}, nil
}

// TestBuiltinHelm_PlacementKeepsGeneratedSourceInInfra pins that a placement the
// refusal accepts still applies: placing an annotated generated source in infra
// moves it there, so an infra consumer depending on it builds without a
// cross-tier cycle.
func TestBuiltinHelm_PlacementKeepsGeneratedSourceInInfra(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: lib
      type: apps-source
    - name: api
      type: webservice
      properties:
        image: nginx:1.27
  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: api
            dependsOn: [lib-src]
    - name: api-first
      type: placement
      properties:
        component: api
        tier: infra
    - name: source-first
      type: placement
      properties:
        component: lib-src
        tier: infra
`
	transformer := newBuiltinTransformer()
	transformer.RegisterComponentLowering(appsSourceRule{})
	parsed, err := oam.ParseWithExtraTypes([]byte(app), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if _, _, err := transformer.TransformWithPolicy(parsed, oam.TransformContext{Domain: kurelDomain}); err != nil {
		t.Fatalf("Transform with the generated source placed in infra: %v", err)
	}
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

// TestBuiltinHelm_ValuesFromKindRefused: a helm component's valuesFrom reaches
// the helmrelease it lowers to, whose kind check refuses a kind Flux does not
// admit at build time (go-kure/launcher#748). Under valuesMode: configMap the
// values ConfigMap's entry comes first, so the authored entry is index 1.
func TestBuiltinHelm_ValuesFromKindRefused(t *testing.T) {
	for mode, want := range map[string]string{
		"inline":    `helmrelease: valuesFrom[0].kind "secret" is not one of Secret, ConfigMap`,
		"configMap": `helmrelease: valuesFrom[1].kind "secret" is not one of Secret, ConfigMap`,
	} {
		t.Run(mode, func(t *testing.T) {
			comp := helmValuesComponent(map[string]any{"replicaCount": 2})
			comp.Properties["valuesMode"] = mode
			comp.Properties["valuesFrom"] = []any{map[string]any{"kind": "secret", "name": "creds"}}
			_, err := helmValuesTransform(t, "", comp)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want one containing %q", err, want)
			}
		})
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
	var sources []string
	releases := map[string]*helmv2.HelmRelease{}
	for _, o := range objs {
		kind := o.GetObjectKind().GroupVersionKind().Kind
		if kind != "HelmRepository" && kind != "HelmRelease" {
			continue
		}
		if o.GetNamespace() != "flux-system" {
			t.Errorf("%s %s is in namespace %q, want flux-system", kind, o.GetName(), o.GetNamespace())
		}
		switch obj := o.(type) {
		case *sourcev1.HelmRepository:
			sources = append(sources, obj.Name)
		case *helmv2.HelmRelease:
			releases[obj.Name] = obj
		default:
			t.Fatalf("%s %s has Go type %T", kind, o.GetName(), o)
		}
	}
	if want := []string{helmSharedSource()}; !slices.Equal(sources, want) {
		t.Fatalf("HelmRepositories = %v, want %v", sources, want)
	}
	for _, name := range []string{"api", "web"} {
		hr, ok := releases[name]
		if !ok {
			t.Errorf("no HelmRelease %s generated", name)
			continue
		}
		// The release is installed into the application namespace, and reads
		// the source beside it in the Flux namespace.
		if hr.Spec.TargetNamespace != "default" {
			t.Errorf("HelmRelease %s targetNamespace = %q, want default", name, hr.Spec.TargetNamespace)
		}
		if hr.Spec.Chart == nil {
			t.Errorf("HelmRelease %s has no chart template", name)
			continue
		}
		ref := hr.Spec.Chart.Spec.SourceRef
		if ref.Kind != "HelmRepository" || ref.Name != helmSharedSource() || (ref.Namespace != "" && ref.Namespace != "flux-system") {
			t.Errorf("HelmRelease %s sourceRef = %+v, want HelmRepository %s in flux-system", name, ref, helmSharedSource())
		}
	}
}
