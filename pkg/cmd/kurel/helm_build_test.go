package kurel

import (
	"crypto/sha256"
	"encoding/hex"
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

// assertSourcesInApplicationBundle checks the shape a generated source gives an
// application (go-kure/launcher#783): the application bundle shop holds exactly
// the sources as its own applications, ahead of its groups, and no group holds
// one.
func assertSourcesInApplicationBundle(t *testing.T, cluster *stack.Cluster, sources ...string) {
	t.Helper()
	root := cluster.Node.Bundle
	if root == nil || root.Name != "shop" || len(cluster.Node.Children) != 0 {
		t.Fatalf("root bundle = %v with %d child nodes, want the one application bundle shop", root, len(cluster.Node.Children))
	}
	var own []string
	for _, a := range root.Applications {
		own = append(own, a.Name)
	}
	if !slices.Equal(own, sources) {
		t.Errorf("application bundle's own applications = %v, want the generated sources %v", own, sources)
	}
	for _, group := range root.Children {
		for _, a := range group.Applications {
			if slices.Contains(sources, a.Name) {
				t.Errorf("group %s holds generated source %s", group.Name, a.Name)
			}
		}
	}
}

// groupNames returns the application bundle's groups in order, each as
// "<name>: <applications>", and fails when a group does not depend on exactly
// the group before it.
func groupNames(t *testing.T, cluster *stack.Cluster) []string {
	t.Helper()
	var out []string
	var previous *stack.Bundle
	for _, group := range cluster.Node.Bundle.Children {
		var apps []string
		for _, a := range group.Applications {
			apps = append(apps, a.Name)
		}
		out = append(out, group.Name+": "+strings.Join(apps, " "))
		var want []*stack.Bundle
		if previous != nil {
			want = []*stack.Bundle{previous}
		}
		if !slices.Equal(group.DependsOn, want) {
			t.Errorf("group %s depends on %d bundle(s), want exactly the group before it", group.Name, len(group.DependsOn))
		}
		previous = group
	}
	return out
}

// TestBuiltinHelm_SourceInApplicationBundle: two helm components sharing one
// inline source give an application bundle holding the source once, and one
// group with both releases: the helm rule orders each release after the source,
// and nothing orders the releases.
func TestBuiltinHelm_SourceInApplicationBundle(t *testing.T) {
	cluster, _, err := transformWithBuiltins(t, helmAppHeader)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertSourcesInApplicationBundle(t, cluster, helmSharedSource())
	if got, want := groupNames(t, cluster), []string{"shop-00: api web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuiltinHelm_ReferencedSourceOrdersNothing: a source the author wrote is
// the author's to order. A helm component referencing it by name stays in one
// flat bundle with it.
func TestBuiltinHelm_ReferencedSourceOrdersNothing(t *testing.T) {
	const app = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: charts
      type: helmrepository
      properties:
        url: https://charts.example.com
    - name: api
      type: helm
      properties:
        chart: api
        version: 1.0.0
        source:
          kind: HelmRepository
          name: charts
`
	cluster, _, err := transformWithBuiltins(t, app)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	root := cluster.Node.Bundle
	if root == nil || len(root.Children) != 0 || len(root.Applications) != 2 {
		t.Fatalf("root bundle = %v, want one flat bundle holding the source and the release", root)
	}
}

// TestBuiltinHelm_GitAndBucketSourcesInApplicationBundle: a generated
// gitrepository and bucket sit in the application bundle like a generated
// helmrepository, ahead of the group holding their releases.
func TestBuiltinHelm_GitAndBucketSourcesInApplicationBundle(t *testing.T) {
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
	var sources []string
	for _, identity := range []string{
		`git:{"url":"https://github.com/example/charts","ref":{"branch":"main"}}`,
		`bucket:{"provider":"","endpoint":"minio.example.com","bucketName":"charts","region":"","prefix":""}`,
	} {
		sum := sha256.Sum256([]byte(identity))
		sources = append(sources, "shop-source-"+hex.EncodeToString(sum[:])[:10])
	}
	assertSourcesInApplicationBundle(t, cluster, sources...)
	if got, want := groupNames(t, cluster), []string{"shop-00: api web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// helmDependsOn is a dependency policy ordering web after api.
const helmDependsOn = `  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [api]
`

// TestBuiltinHelm_TierAnnotationKeepsSourceFirst: a helm component annotated
// into the infra tier keeps its release in that group; the shared source stays
// in the application bundle, ahead of it.
func TestBuiltinHelm_TierAnnotationKeepsSourceFirst(t *testing.T) {
	app := strings.Replace(helmAppHeader, "      type: helm\n      properties:\n        chart: api",
		"      type: helm\n      annotations:\n        "+oam.TierAnnotationKey(kurelDomain)+": infra\n      properties:\n        chart: api", 1)
	cluster, _, err := transformWithBuiltins(t, app+helmDependsOn)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertSourcesInApplicationBundle(t, cluster, helmSharedSource())
	if got, want := groupNames(t, cluster), []string{"shop-infra: api", "shop-01: web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuiltinHelm_PlacementKeepsSourceFirst: the same with a placement policy,
// which names the release only.
func TestBuiltinHelm_PlacementKeepsSourceFirst(t *testing.T) {
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
	assertSourcesInApplicationBundle(t, cluster, helmSharedSource())
	if got, want := groupNames(t, cluster), []string{"shop-infra: api", "shop-01: web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// TestBuiltinHelm_PlacementCannotPlaceGeneratedSource: a generated source is in
// no group, so a placement policy naming it is refused, whatever the tier.
func TestBuiltinHelm_PlacementCannotPlaceGeneratedSource(t *testing.T) {
	for _, tier := range []string{"infra", "apps"} {
		_, _, err := transformWithBuiltins(t, helmAppHeader+`  policies:
    - name: source-placed
      type: placement
      properties:
        component: `+helmSharedSource()+`
        tier: `+tier+"\n")
		if err == nil || !strings.Contains(err.Error(), "helmrepository "+strconv.Quote(helmSharedSource())+" cannot be placed in tier "+tier) {
			t.Errorf("tier %s: Transform error = %v, want the generated source's placement refused", tier, err)
		}
	}
}

// TestBuiltinHelm_DependencyCannotDelayGeneratedSource pins the dependency
// counterpart of the placement refusal: a rule making the source wait on one of
// its consumers could not be kept, since the application bundle's own
// applications are applied before every group.
func TestBuiltinHelm_DependencyCannotDelayGeneratedSource(t *testing.T) {
	sourceWaits := helmDependsOn + `          - component: ` + helmSharedSource() + `
            dependsOn: [api]
`
	_, _, err := transformWithBuiltins(t, helmAppHeader+sourceWaits)
	if err == nil || !strings.Contains(err.Error(), "helmrepository "+strconv.Quote(helmSharedSource())+" cannot wait on api") {
		t.Fatalf("Transform error = %v, want the generated source's dependency refused", err)
	}
}

// TestBuiltinHelm_DependencyOnGeneratedSource: a component the author makes
// wait for the generated source needs no group of its own for that; the source
// is ahead of every group already.
func TestBuiltinHelm_DependencyOnGeneratedSource(t *testing.T) {
	cluster, _, err := transformWithBuiltins(t, helmAppHeader+`  policies:
    - name: order
      type: dependency
      properties:
        rules:
          - component: web
            dependsOn: [`+helmSharedSource()+`]
`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	assertSourcesInApplicationBundle(t, cluster, helmSharedSource())
	if got, want := groupNames(t, cluster), []string{"shop-00: api web"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

// unorderedSourceRule emits a helmrepository carrying a tier annotation and
// orders nothing after it, which a custom lowering rule may do; the built-in
// helm rule orders its release after the source it emits. It declares a
// schema, so its output counts as synthesized.
type unorderedSourceRule struct{}

func (unorderedSourceRule) ComponentType() string { return "apps-source" }

func (unorderedSourceRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

func (unorderedSourceRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{Components: []oam.Component{{
		Name:        comp.Name + "-src",
		Type:        "helmrepository",
		Properties:  map[string]any{"url": "https://charts.example.com"},
		Annotations: map[string]string{oam.TierAnnotationKey(kurelDomain): string(oam.TierApps)},
	}}}, nil
}

// TestBuiltinHelm_UnorderedRuleSourceIsAComponentLikeAnyOther pins that only
// the order a rule declares takes a source out of the groups: a source a rule
// emits without ordering a component after it is placed and depended on like
// any component.
func TestBuiltinHelm_UnorderedRuleSourceIsAComponentLikeAnyOther(t *testing.T) {
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
	transformer.RegisterComponentLowering(unorderedSourceRule{})
	parsed, err := oam.ParseWithExtraTypes([]byte(app), nil, transformer.LowerableTypes())
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	cluster, _, err := transformer.TransformWithPolicy(parsed, oam.TransformContext{Domain: kurelDomain})
	if err != nil {
		t.Fatalf("Transform with the source placed in infra: %v", err)
	}
	if got := len(cluster.Node.Bundle.Applications); got != 0 {
		t.Errorf("application bundle holds %d own applications, want none", got)
	}
	if got, want := groupNames(t, cluster), []string{"shop-00: lib-src", "shop-01: api"}; !slices.Equal(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
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
	for _, b := range allBundles(cluster.Node) {
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

// waitingRule lowers a component of type typ into a webservice "<name>-first"
// and a component "<name>" of the built-in type as, ordered after the first.
// The built-in rule of that type then lowers "<name>" again, into components
// it builds from scratch. It declares a schema, so its output counts as
// synthesized.
type waitingRule struct {
	typ, as string
	props   map[string]any
}

func (r waitingRule) ComponentType() string { return r.typ }

func (waitingRule) PropertySchema() map[string]oam.PropertySchema {
	return map[string]oam.PropertySchema{}
}

func (r waitingRule) LowerComponent(comp *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	second := oam.Component{Name: comp.Name, Type: r.as, Properties: r.props}
	second.OrderAfter(comp.Name + "-first")
	return oam.LoweringResult{Components: []oam.Component{
		{Name: comp.Name + "-first", Type: "webservice", Properties: map[string]any{"image": "nginx:1.27"}},
		second,
	}}, nil
}

// TestBuiltinRules_KeepTheOrderOfTheComponentTheyLower pins that an order a
// rule declared on a component survives the built-in rule that lowers the
// component again: what a webservice or a helm component becomes waits as the
// component did. The source generated for a helm component that waits does not
// wait with it: it is the application's, and stays among the application
// bundle's own applications.
func TestBuiltinRules_KeepTheOrderOfTheComponentTheyLower(t *testing.T) {
	const header = `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: shop
  namespace: default
spec:
  components:
    - name: two
      type: `
	for _, tc := range []struct {
		rule waitingRule
		own  []string
		want []string
	}{
		{
			rule: waitingRule{typ: "waiting-web", as: "webservice", props: map[string]any{"image": "nginx:1.27"}},
			want: []string{"shop-00: two-first", "shop-01: two"},
		},
		{
			rule: waitingRule{typ: "waiting-chart", as: "helm", props: map[string]any{
				"chart":   "api",
				"version": "1.0.0",
				"source":  map[string]any{"url": "https://charts.example.com"},
			}},
			own:  []string{helmSharedSource()},
			want: []string{"shop-00: two-first", "shop-01: two"},
		},
	} {
		t.Run(tc.rule.typ, func(t *testing.T) {
			transformer := newBuiltinTransformer()
			transformer.RegisterComponentLowering(tc.rule)
			parsed, err := oam.ParseWithExtraTypes([]byte(header+tc.rule.typ+"\n"), nil, transformer.LowerableTypes())
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			cluster, _, err := transformer.TransformWithPolicy(parsed, oam.TransformContext{Domain: kurelDomain})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			assertSourcesInApplicationBundle(t, cluster, tc.own...)
			if got := groupNames(t, cluster); !slices.Equal(got, tc.want) {
				t.Errorf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}
