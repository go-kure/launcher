package components_test

import (
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// htBaseProps is the smallest valid helmtemplate property map: a chart in an
// HTTP Helm repository.
func htBaseProps() map[string]any {
	return map[string]any{
		"chart":  "podinfo",
		"source": map[string]any{"url": "https://charts.example.com"},
	}
}

func htParse(props map[string]any) (stack.ApplicationConfig, error) {
	return htParseNamed("web", props)
}

// htParseNamed is htParse for a component called name.
func htParseNamed(name string, props map[string]any) (stack.ApplicationConfig, error) {
	return (&components.HelmTemplateHandler{}).ToApplicationConfig(
		&oam.Component{Name: name, Type: "helmtemplate", Properties: props}, "demo")
}

// Component names whose 40-character cut, under Flux's release-name
// shortening, ends in '-' (the shortened name is still valid: "--" is) and in
// '.' (the shortened name is not: a label may not start with '-').
var (
	htDashAtCut = strings.Repeat("a", 39) + "-" + strings.Repeat("b", 20)
	htDotAtCut  = strings.Repeat("a", 39) + "." + strings.Repeat("b", 20)
)

// htAuthoredErr runs authored-property validation — the check kurel build runs
// before any handler — on a one-component Application carrying props.
func htAuthoredErr(props map[string]any) error {
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}}, nil)
	return tr.ValidateAuthoredProperties(&oam.Application{Spec: oam.ApplicationSpec{
		Components: []oam.Component{{Name: "web", Type: "helmtemplate", Properties: props}},
	}})
}

func TestHelmTemplateHandler_CanHandle(t *testing.T) {
	h := &components.HelmTemplateHandler{}
	if !h.CanHandle("helmtemplate") || h.CanHandle("helm") {
		t.Error("CanHandle must accept helmtemplate only")
	}
}

// TestHelmTemplateHandler_RejectionMatrix pins, one case per key, that the
// terminal refuses outright every property only a Flux-reconciled release
// reads, a source reference, and an OCIRepository source without a version.
// Each refused key is refused twice over: by the handler's
// strict decode, and by authored-property validation against the published
// schema, which a kurel build runs first. The OCI version rule is the
// handler's alone: the schema cannot express a requirement that depends on
// another key.
func TestHelmTemplateHandler_RejectionMatrix(t *testing.T) {
	t.Run("control: base properties are accepted", func(t *testing.T) {
		if _, err := htParse(htBaseProps()); err != nil {
			t.Fatalf("handler refused the base properties: %v", err)
		}
		if err := htAuthoredErr(htBaseProps()); err != nil {
			t.Fatalf("authored validation refused the base properties: %v", err)
		}
	})

	withKey := func(key string, value any) func(map[string]any) {
		return func(p map[string]any) { p[key] = value }
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		// names is what both errors must contain: the refused key, quoted as
		// both the decoder and the schema validator quote it.
		names string
		// schemaRefuses reports whether authored validation refuses it too.
		schemaRefuses bool
	}{
		{"targetNamespace", withKey("targetNamespace", "web-ns"), `"targetNamespace"`, true},
		{"interval", withKey("interval", "10m"), `"interval"`, true},
		{"driftDetection", withKey("driftDetection", map[string]any{"mode": "enabled"}), `"driftDetection"`, true},
		{"install", withKey("install", map[string]any{"crds": "Skip"}), `"install"`, true},
		{"upgrade", withKey("upgrade", map[string]any{"crds": "Skip"}), `"upgrade"`, true},
		{"valuesFrom", withKey("valuesFrom", []any{map[string]any{"kind": "ConfigMap", "name": "extra"}}), `"valuesFrom"`, true},
		{"valuesMode", withKey("valuesMode", "inline"), `"valuesMode"`, true},
		{"source.name", withKey("source", map[string]any{"url": "https://charts.example.com", "name": "podinfo"}), `"name"`, true},
		{"OCIRepository without version", func(p map[string]any) {
			delete(p, "chart")
			p["source"] = map[string]any{"url": "oci://ghcr.io/example/charts/podinfo"}
		}, "requires version", false},
		// The helm rule's delivery switch: the terminal is always template
		// delivery.
		{"delivery", withKey("delivery", "template"), `"delivery"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := htBaseProps()
			tc.mutate(props)
			_, err := htParse(props)
			if err == nil {
				t.Fatalf("handler accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("handler error %q does not name %q", err, tc.names)
			}
			if !tc.schemaRefuses {
				return
			}
			props = htBaseProps()
			tc.mutate(props)
			err = htAuthoredErr(props)
			if err == nil {
				t.Fatalf("authored validation accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("authored validation error %q does not name %q", err, tc.names)
			}
		})
	}
}

// TestHelmTemplateHandler_SourceChecks pins the inline-source rules the
// terminal shares with the helm rule: source and source.url required, the
// kind inferred from the scheme when unset and checked against it when set,
// chart required for a HelmRepository.
func TestHelmTemplateHandler_SourceChecks(t *testing.T) {
	errCases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{"no source", map[string]any{"chart": "podinfo"}, "source is required"},
		{"null source", map[string]any{"chart": "podinfo", "source": nil}, "source is required"},
		{"no source.url", map[string]any{"chart": "podinfo", "source": map[string]any{}}, "source.url is required"},
		{"HelmRepository without chart", map[string]any{"source": map[string]any{"url": "https://charts.example.com"}}, "requires chart"},
		{"HelmRepository on an oci:// URL", map[string]any{"chart": "podinfo", "version": "1.0.0", "source": map[string]any{"url": "oci://ghcr.io/x/podinfo", "kind": "HelmRepository"}}, "incompatible with oci://"},
		{"HelmRepository on another scheme", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "ftp://charts.example.com"}}, "must start with https:// or http://"},
		{"OCIRepository on an https:// URL", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "https://charts.example.com", "kind": "OCIRepository"}}, "requires an oci:// URL"},
		{"other kind", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com", "kind": "HelmChart"}}, "not valid for inline source"},
		{"wrongly typed chart", map[string]any{"chart": 3, "source": map[string]any{"url": "https://charts.example.com"}}, "chart"},
		{"user and token in an https:// URL", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://deploy:s3cr3t@charts.example.com"}}, "helmtemplate: source.url must not carry a user or password; a client-side render takes no credentials"},
		{"user in an oci:// URL", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "oci://deploy@ghcr.io/x/podinfo"}}, "helmtemplate: source.url must not carry a user or password; a client-side render takes no credentials"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := htParse(tc.props)
			if err == nil {
				t.Fatalf("accepted %v", tc.props)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("error %q repeats the URL's credential", err)
			}
		})
	}

	kindCases := []struct {
		name     string
		props    map[string]any
		wantKind string
	}{
		{"https:// infers HelmRepository", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com"}}, "HelmRepository"},
		{"http:// infers HelmRepository", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "http://charts.example.com"}}, "HelmRepository"},
		{"oci:// infers OCIRepository", map[string]any{"version": "1.2.3", "source": map[string]any{"url": "oci://ghcr.io/x/podinfo"}}, "OCIRepository"},
		{"explicit OCIRepository", map[string]any{"version": "1.2.3", "source": map[string]any{"url": "oci://ghcr.io/x/podinfo", "kind": "OCIRepository"}}, "OCIRepository"},
		{"@ in the path is not a user", map[string]any{"chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com/team@example"}}, "HelmRepository"},
	}
	for _, tc := range kindCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := htParse(tc.props)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if got := cfg.(*components.HelmTemplateConfig).SourceKind; got != tc.wantKind {
				t.Errorf("SourceKind = %q, want %q", got, tc.wantKind)
			}
		})
	}
}

// TestHelmTemplateHandler_Values pins values handling: an object is kept as
// authored, null (typed or not) is absence, anything else is refused, and a
// value encoding/json cannot represent is a build error, never a panic.
func TestHelmTemplateHandler_Values(t *testing.T) {
	t.Run("object kept as authored", func(t *testing.T) {
		values := map[string]any{"replicaCount": 2, "image": map[string]any{"tag": "1.2"}}
		props := htBaseProps()
		props["values"] = values
		cfg, err := htParse(props)
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if got := cfg.(*components.HelmTemplateConfig).Values; !reflect.DeepEqual(got, values) {
			t.Errorf("Values = %#v, want %#v", got, values)
		}
	})
	for name, v := range map[string]any{"untyped null": nil, "typed null": map[string]any(nil)} {
		t.Run(name+" is absence", func(t *testing.T) {
			props := htBaseProps()
			props["values"] = v
			cfg, err := htParse(props)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if got := cfg.(*components.HelmTemplateConfig).Values; got != nil {
				t.Errorf("Values = %#v, want nil", got)
			}
		})
	}
	errCases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"not an object", func(p map[string]any) { p["values"] = "replicaCount: 2" }, "values: must be an object"},
		{"non-finite number", func(p map[string]any) { p["values"] = map[string]any{"ratio": math.NaN()} }, "not representable as JSON"},
		{"two spellings", func(p map[string]any) {
			p["values"] = map[string]any{"a": 1}
			p["Values"] = map[string]any{"a": 2}
		}, "given more than once"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			props := htBaseProps()
			tc.mutate(props)
			_, err := htParse(props)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestHelmTemplateConfig_DirectConfigCheckedBeforeRender: this config type is
// exported, so one built directly never went through the handler. Generate
// re-applies the handler's checks before any render — no network is touched
// here — and a non-finite value is an error, not a panic.
func TestHelmTemplateConfig_DirectConfigCheckedBeforeRender(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *components.HelmTemplateConfig
		wantErr string
	}{
		{"no URL", &components.HelmTemplateConfig{Name: "web", Chart: "podinfo"}, "source.url is required"},
		{"OCIRepository without version", &components.HelmTemplateConfig{Name: "web", SourceURL: "oci://ghcr.io/x/podinfo"}, "requires version"},
		{"kind disagrees with scheme", &components.HelmTemplateConfig{Name: "web", SourceURL: "https://charts.example.com", SourceKind: "OCIRepository", Version: "1.0.0"}, "requires an oci:// URL"},
		{"non-finite values", &components.HelmTemplateConfig{Name: "web", SourceURL: "https://charts.example.com", Chart: "podinfo", Values: map[string]any{"x": math.Inf(1)}}, "not representable as JSON"},
		{"invalid release name", &components.HelmTemplateConfig{Name: "web", SourceURL: "https://charts.example.com", Chart: "podinfo", ReleaseName: "Web"}, `releaseName "Web" must be a DNS-1123 subdomain`},
		{"invalid default release name", &components.HelmTemplateConfig{Name: htDotAtCut, SourceURL: "https://charts.example.com", Chart: "podinfo"}, "set releaseName"},
		{"neither ReleaseName nor Name", &components.HelmTemplateConfig{SourceURL: "https://charts.example.com", Chart: "podinfo"}, "set releaseName or the component name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.Generate(nil)
			if err == nil {
				t.Fatal("Generate accepted an invalid config")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
			if err := tc.cfg.AugmentLayout(&layout.ManifestLayout{Name: "web"}); err == nil {
				t.Error("AugmentLayout accepted an invalid config")
			}
		})
	}
}

// TestHelmTemplateConfig_IsCoveredLayoutAugmenter: every helmtemplate config
// is a layout.LayoutAugmenter whose Generate covers AugmentLayout, so a
// layout-walking consumer gets the hook-group partition and kurel build's
// guard lets it through.
func TestHelmTemplateConfig_IsCoveredLayoutAugmenter(t *testing.T) {
	cfg, err := htParse(htBaseProps())
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if _, ok := cfg.(layout.LayoutAugmenter); !ok {
		t.Fatal("helmtemplate config does not implement layout.LayoutAugmenter")
	}
	cov, ok := cfg.(oam.LayoutAugmentationCoverage)
	if !ok {
		t.Fatal("helmtemplate config does not implement oam.LayoutAugmentationCoverage")
	}
	if !cov.GenerateCoversAugmentLayout() {
		t.Error("GenerateCoversAugmentLayout() = false, want true")
	}
}

// htTemplateChart is a chart served over HTTP for the render tests: one
// object per hook group, a multi-event hook, a `test` hook that is dropped,
// and a template whose output depends on a values entry's type.
var htTemplateChart = map[string]string{
	"pre.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: pre\n  annotations:\n    helm.sh/hook: pre-install\n",
	"multi.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: multi\n  annotations:\n    helm.sh/hook: pre-upgrade,pre-install\n",
	"main.yaml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: main\ndata:\n  replicas: {{ if eq .Values.replicas 3 }}\"three\"{{ else }}\"other\"{{ end }}\n",
	"post.yaml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: post\n  annotations:\n    helm.sh/hook: post-install\n",
	"test.yaml":  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: smoke\n  annotations:\n    helm.sh/hook: test\nspec:\n  containers:\n    - name: c\n      image: busybox\n",
}

// htRenderTerminal parses a helmtemplate component on the chart served at
// srvURL through its handler.
func htRenderTerminal(t *testing.T, srvURL string, values map[string]any) stack.ApplicationConfig {
	t.Helper()
	terminal, err := htParse(map[string]any{
		"chart":   "testchart",
		"version": "0.1.0",
		"source":  map[string]any{"url": srvURL},
		"values":  values,
	})
	if err != nil {
		t.Fatalf("helmtemplate ToApplicationConfig: %v", err)
	}
	return terminal
}

func htGenerate(t *testing.T, cfg stack.ApplicationConfig) []client.Object {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("web", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := make([]client.Object, len(objs))
	for i, o := range objs {
		out[i] = *o
	}
	return out
}

func htAugment(t *testing.T, cfg stack.ApplicationConfig) *layout.ManifestLayout {
	t.Helper()
	aug, ok := cfg.(layout.LayoutAugmenter)
	if !ok {
		t.Fatalf("%T is not a layout.LayoutAugmenter", cfg)
	}
	ml := &layout.ManifestLayout{Name: "web", Namespace: "team"}
	if err := aug.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	return ml
}

// TestHelmTemplate_RendersRealChart renders one real chart — served locally,
// fetched and rendered by kure exactly as a build does — through the terminal
// and checks its result: hook execution order (the multi-event hook placed by
// its earliest phase, the test hook dropped), the values entry's int type
// honored by the chart's `eq`, three hook-group child layouts from
// AugmentLayout, and the children's union equal to Generate's output, which is
// what GenerateCoversAugmentLayout asserts.
func TestHelmTemplate_RendersRealChart(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htTemplateChart)
	values := map[string]any{"replicas": 3}
	terminal := htRenderTerminal(t, srvURL, values)

	termObjs := htGenerate(t, terminal)
	var names []string
	for _, o := range termObjs {
		names = append(names, o.GetName())
	}
	// multi and pre share the pre-install group; within a group, objects keep
	// kure's render order, which sorts by template file name.
	if want := []string{"multi", "pre", "main", "post"}; !slices.Equal(names, want) {
		t.Errorf("execution order = %v, want %v (the test hook dropped)", names, want)
	}
	for _, o := range termObjs {
		cm, ok := o.(*corev1.ConfigMap)
		if !ok {
			t.Fatalf("%s: %T, want *corev1.ConfigMap", o.GetName(), o)
		}
		if cm.GetName() != "main" {
			continue
		}
		if got := cm.Data["replicas"]; got != "three" {
			t.Errorf("main data.replicas = %q, want %q (values.replicas reached the chart as the int it was authored as)", got, "three")
		}
	}

	termLayout := htAugment(t, terminal)
	if len(termLayout.Children) != 3 {
		t.Fatalf("terminal layout has %d children, want 3 (pre-install, main, post-install)", len(termLayout.Children))
	}
	union := append([]client.Object(nil), termLayout.Resources...)
	for _, child := range termLayout.Children {
		union = append(union, child.Resources...)
	}
	key := func(o client.Object) string { return o.GetObjectKind().GroupVersionKind().Kind + "/" + o.GetName() }
	sortByKey := func(objs []client.Object) {
		sort.Slice(objs, func(i, j int) bool { return key(objs[i]) < key(objs[j]) })
	}
	gen := append([]client.Object(nil), termObjs...)
	sortByKey(union)
	sortByKey(gen)
	if !reflect.DeepEqual(union, gen) {
		t.Errorf("children's union differs from Generate's output:\n  union:    %#v\n  Generate: %#v", union, gen)
	}
}

// htHookGroupChildren transforms an Application named application whose one
// component db is a helmtemplate on the chart served at srvURL, carrying
// traitTypes, walks the result with kure's layout walker and returns the
// component layout's hook-group children.
func htHookGroupChildren(t *testing.T, srvURL, application string, traitTypes ...string) []*layout.ManifestLayout {
	t.Helper()
	tr := oam.NewTransformer(
		map[string]oam.ComponentHandler{"helmtemplate": &components.HelmTemplateHandler{}},
		map[string]oam.TraitHandler{"prune-protection": &traits.PruneProtectionHandler{}})
	component := oam.Component{
		Name: "db",
		Type: "helmtemplate",
		Properties: map[string]any{
			"chart":   "testchart",
			"version": "0.1.0",
			"source":  map[string]any{"url": srvURL},
			"values":  map[string]any{"replicas": 3},
		},
	}
	for _, traitType := range traitTypes {
		component.Traits = append(component.Traits, oam.Trait{Type: traitType, Properties: map[string]any{}})
	}
	cluster, err := tr.Transform(&oam.Application{
		Metadata: oam.Metadata{Name: application},
		Spec:     oam.ApplicationSpec{Components: []oam.Component{component}},
	}, oam.TransformContext{Namespace: "demo"})
	if err != nil {
		t.Fatalf("Transform %s: %v", application, err)
	}
	root, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		t.Fatalf("WalkCluster %s: %v", application, err)
	}
	var find func(ml *layout.ManifestLayout) *layout.ManifestLayout
	find = func(ml *layout.ManifestLayout) *layout.ManifestLayout {
		if ml.Name == "db" {
			return ml
		}
		for _, child := range ml.Children {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	componentLayout := find(root)
	if componentLayout == nil {
		t.Fatalf("application %s: the walked tree has no layout for component db", application)
	}
	return componentLayout.Children
}

// TestHelmTemplate_HookGroupChildNamesIncludeApplication is the acceptance
// test of go-kure/launcher#792, through the transform and kure's layout walker:
// two differently named applications that each have a helmtemplate component db
// get different hook-group child names, and a single application's children
// differ from the ones a config no transform told its application gets only by
// the application name that leads them — the same groups, objects and order. A
// trait that wraps the component's config changes none of it.
func TestHelmTemplate_HookGroupChildNamesIncludeApplication(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", htTemplateChart)
	objectNames := func(ml *layout.ManifestLayout) []string {
		names := make([]string, len(ml.Resources))
		for i, o := range ml.Resources {
			names[i] = o.GetName()
		}
		return names
	}

	// What a config built without an application gives: the names as they were.
	direct, err := (&components.HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{
		Name: "db", Type: "helmtemplate", Properties: map[string]any{
			"chart":   "testchart",
			"version": "0.1.0",
			"source":  map[string]any{"url": srvURL},
			"values":  map[string]any{"replicas": 3},
		},
	}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	before := &layout.ManifestLayout{Name: "db", Namespace: "team"}
	if err := direct.(layout.LayoutAugmenter).AugmentLayout(before); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	var beforeNames []string
	for _, child := range before.Children {
		beforeNames = append(beforeNames, child.Name)
	}
	if want := []string{"db-00-pre-install", "db-01-main", "db-02-post-install"}; !slices.Equal(beforeNames, want) {
		t.Fatalf("children without an application = %v, want %v", beforeNames, want)
	}

	seen := map[string]string{}
	for _, application := range []string{"shop", "billing"} {
		children := htHookGroupChildren(t, srvURL, application)
		if len(children) != len(before.Children) {
			t.Fatalf("application %s has %d children, want %d", application, len(children), len(before.Children))
		}
		for i, child := range children {
			if want := application + "-" + beforeNames[i]; child.Name != want {
				t.Errorf("application %s child %d is named %q, want %q", application, i, child.Name, want)
			}
			if other, taken := seen[child.Name]; taken {
				t.Errorf("applications %s and %s both have a child named %q", other, application, child.Name)
			}
			seen[child.Name] = application
			if got, want := objectNames(child), objectNames(before.Children[i]); !slices.Equal(got, want) {
				t.Errorf("application %s child %q holds %v, want %v", application, child.Name, got, want)
			}
			var wantDeps []string
			if i > 0 {
				wantDeps = []string{children[i-1].Name}
			}
			if !slices.Equal(child.DependsOn, wantDeps) {
				t.Errorf("application %s child %q depends on %v, want %v", application, child.Name, child.DependsOn, wantDeps)
			}
		}
	}

	// The trait's decorator wraps the config after it was told its application.
	decorated := htHookGroupChildren(t, srvURL, "shop", "prune-protection")
	var decoratedNames []string
	for _, child := range decorated {
		decoratedNames = append(decoratedNames, child.Name)
	}
	if want := []string{"shop-db-00-pre-install", "shop-db-01-main", "shop-db-02-post-install"}; !slices.Equal(decoratedNames, want) {
		t.Errorf("children under a prune-protection trait = %v, want %v", decoratedNames, want)
	}
}

// TestHelmTemplate_ReleaseIdentity: the render's .Release.Namespace is the
// namespace the handler is given, and its .Release.Name is the authored
// releaseName, or the component name when none is authored — rendered by kure
// from a locally served chart, as a build does.
func TestHelmTemplate_ReleaseIdentity(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", identityChart)
	for _, tc := range []struct {
		name        string
		releaseName any
		want        string
	}{
		{"unset defaults to the component name", nil, "web-cm"},
		{"authored", "shop-a", "shop-a-cm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{
				"chart":   "testchart",
				"version": "0.1.0",
				"source":  map[string]any{"url": srvURL},
			}
			if tc.releaseName != nil {
				props["releaseName"] = tc.releaseName
			}
			cfg, err := htParse(props)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			gotName, gotNamespace := renderedIdentity(t, cfg)
			if gotName != tc.want || gotNamespace != "demo" {
				t.Errorf("rendered %s/%s, want demo/%s", gotNamespace, gotName, tc.want)
			}
		})
	}
}

// TestHelmTemplateConfig_DirectConfigReleaseName: a config built directly,
// which never went through the handler, still renders under Name when
// ReleaseName is empty, and under ReleaseName when it is set, with or without
// a Name. With neither it is refused before any render
// (TestHelmTemplateConfig_DirectConfigCheckedBeforeRender).
func TestHelmTemplateConfig_DirectConfigReleaseName(t *testing.T) {
	srvURL := startMinimalHelmChartServer(t, "testchart", "0.1.0", identityChart)
	for _, tc := range []struct{ name, releaseName, want string }{
		{"web", "", "web-cm"},
		{"", "direct", "direct-cm"},
		{"web", "direct", "direct-cm"},
	} {
		cfg := &components.HelmTemplateConfig{Name: tc.name, Namespace: "demo", SourceURL: srvURL, Chart: "testchart", Version: "0.1.0", ReleaseName: tc.releaseName}
		if gotName, _ := renderedIdentity(t, cfg); gotName != tc.want {
			t.Errorf("Name %q, ReleaseName %q: rendered %s, want %s", tc.name, tc.releaseName, gotName, tc.want)
		}
	}
}

// TestHelmTemplateHandler_ReleaseName pins the release name the handler
// resolves and records: the authored releaseName, never shortened, else the
// release name Flux gives a HelmRelease named after the component with no
// targetNamespace — the component name, shortened past 53 characters as
// helm-controller's release.ShortenName does — whatever the chart or source.
// It refuses a name that is not a valid Helm release name, a default with the
// remedy to set releaseName. Both the handler and authored-property validation
// accept the key. The shortened names are fixed strings computed outside the
// code under test (the first 12 hex digits of `printf %s <name> | sha256sum`).
func TestHelmTemplateHandler_ReleaseName(t *testing.T) {
	oci := func(url string) map[string]any {
		return map[string]any{"version": "1.0.0", "source": map[string]any{"url": url}}
	}
	with := func(p map[string]any, releaseName string) map[string]any {
		p["releaseName"] = releaseName
		return p
	}
	const longName = "checkout-service-payment-gateway-adapter-for-the-eu-region" // 58 characters
	name53 := "a" + strings.Repeat("b", 51) + "c"
	okCases := []struct {
		name      string
		component string
		props     map[string]any
		want      string
	}{
		{"HelmRepository default is the component name", "web", htBaseProps(), "web"},
		{"OCIRepository default is the component name", "web", oci("oci://ghcr.io/example/charts/Pod_Info"), "web"},
		{"default keeps a 53-character component name", name53, htBaseProps(), name53},
		{"default shortens a longer component name as Flux does", longName, htBaseProps(), "checkout-service-payment-gateway-adapter-a380d4c53021"},
		{"default cut at a dash stays valid", htDashAtCut, htBaseProps(), strings.Repeat("a", 39) + "--223f6f9789ce"},
		{"authored on a HelmRepository", "web", with(htBaseProps(), "shop-podinfo"), "shop-podinfo"},
		{"authored on an OCIRepository", "web", with(oci("oci://ghcr.io/example/charts/Pod_Info"), "podinfo"), "podinfo"},
		{"authored overrides a long component name", longName, with(htBaseProps(), "shop-podinfo"), "shop-podinfo"},
		{"authored 53 characters is not shortened", "web", with(htBaseProps(), strings.Repeat("a", 53)), strings.Repeat("a", 53)},
		{"authored with dots", "web", with(htBaseProps(), "shop.podinfo"), "shop.podinfo"},
	}
	for _, tc := range okCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := htParseNamed(tc.component, tc.props)
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			if got := cfg.(*components.HelmTemplateConfig).ReleaseName; got != tc.want {
				t.Errorf("ReleaseName = %q, want %q", got, tc.want)
			}
			if err := htAuthoredErr(tc.props); err != nil {
				t.Errorf("authored validation refused %v: %v", tc.props, err)
			}
		})
	}

	const rule = "must be a DNS-1123 subdomain of at most 53 characters, as a Helm release name is"
	errCases := []struct {
		name      string
		component string
		props     map[string]any
		wantErr   string
	}{
		{"authored uppercase", "web", with(htBaseProps(), "Podinfo"), `helmtemplate: releaseName "Podinfo" ` + rule},
		{"authored underscore", "web", with(htBaseProps(), "pod_info"), `helmtemplate: releaseName "pod_info" ` + rule},
		{"authored 54 characters is refused, not shortened", "web", with(htBaseProps(), strings.Repeat("a", 54)), "helmtemplate: releaseName " + `"` + strings.Repeat("a", 54) + `" ` + rule},
		{"authored leading dash", "web", with(htBaseProps(), "-podinfo"), `helmtemplate: releaseName "-podinfo" ` + rule},
		{"default cut at a dot", htDotAtCut, htBaseProps(),
			`helmtemplate: the default release name "` + strings.Repeat("a", 39) + `.-b46d196cb11f", derived from the component name "` + htDotAtCut +
				`" as Flux derives a HelmRelease's, ` + rule + "; set releaseName"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := htParseNamed(tc.component, tc.props)
			if err == nil {
				t.Fatalf("accepted %v", tc.props)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
