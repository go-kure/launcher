package components

// Tests of the scopeOverrides property of the helmtemplate component: the
// scope a document states for a kind the chart renders, read by the namespace
// stamp of template delivery (stampRenderedNamespaces, go-kure/launcher#794).

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/manifest"
	"github.com/go-kure/kure/pkg/stack/helm"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// scopeEntry is one authored scopeOverrides entry.
func scopeEntry(apiVersion, kind, scope string) map[string]any {
	return map[string]any{"apiVersion": apiVersion, "kind": kind, "scope": scope}
}

// scopeProps is the properties of a helmtemplate component on an HTTP Helm
// repository, with scopeOverrides when overrides is not nil.
func scopeProps(overrides any) map[string]any {
	props := map[string]any{
		"chart":  "myapp",
		"source": map[string]any{"url": "https://charts.example.com"},
	}
	if overrides != nil {
		props[scopeOverridesKey] = overrides
	}
	return props
}

// scopeFixture parses a helmtemplate component in namespace "team" with the
// given scopeOverrides through the handler, on a stub render of raw.
func scopeFixture(t *testing.T, overrides []any, raw string) *HelmTemplateConfig {
	t.Helper()
	cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "myapp", Type: helmTemplateType, Properties: scopeProps(overrides)}, "team")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	tc := cfg.(*HelmTemplateConfig)
	tc.renderChart = stubRender(raw)
	return tc
}

// scopeGenerate returns the objects cfg generates.
func scopeGenerate(t *testing.T, cfg *HelmTemplateConfig) []client.Object {
	t.Helper()
	ptrs, err := cfg.Generate(nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	objs := make([]client.Object, len(ptrs))
	for i, p := range ptrs {
		objs[i] = *p
	}
	return objs
}

// TestHelmTemplate_ScopeOverrides: a rendered object of a kind the document
// states Namespaced gets the application namespace when it carries none, and
// one of a kind stated Cluster is left as rendered, with the namespace the
// chart wrote on it too. An override outranks kure's own table (the
// cert-manager kinds) but not a kind the Kubernetes API scopes (ConfigMap,
// Namespace, PriorityClass), agrees with a CustomResourceDefinition the chart
// renders, and does nothing for a kind the chart does not render. A kind of
// unknown scope with no override stays as rendered.
func TestHelmTemplate_ScopeOverrides(t *testing.T) {
	raw := nsRender(
		nsDoc("example.io/v1", "Gadget", "stated-namespaced", "", ""),
		nsDoc("example.io/v1", "Gadget", "stated-namespaced-authored", "elsewhere", ""),
		nsDoc("example.io/v1", "Gadget", "hooked", "", "pre-install"),
		nsDoc("example.io/v1", "ClusterGadget", "stated-cluster", "", ""),
		nsDoc("example.io/v1", "ClusterGadget", "stated-cluster-authored", "kept", ""),
		nsDoc("example.io/v1", "Thing", "unknown", "", ""),
		nsDoc("example.io/v2", "Gadget", "other-version", "", ""),
		nsDoc("cert-manager.io/v1", "ClusterIssuer", "table-cluster", "", ""),
		nsDoc("cert-manager.io/v1", "Certificate", "table-namespaced", "", ""),
		nsDoc("v1", "ConfigMap", "api-namespaced", "", ""),
		nsDoc("v1", "Namespace", "api-cluster", "", ""),
		nsDoc("scheduling.k8s.io/v1", "PriorityClass", "api-cluster-scheduling", "", ""),
		nsFixtureCRD("Widget", "widgets", "Namespaced", ""),
		nsDoc("fixtures.example.com/v1", "Widget", "agreed", "", ""),
	)
	overrides := []any{
		scopeEntry("example.io/v1", "Gadget", "Namespaced"),
		scopeEntry("example.io/v1", "ClusterGadget", "Cluster"),
		scopeEntry("cert-manager.io/v1", "ClusterIssuer", "Namespaced"),
		scopeEntry("cert-manager.io/v1", "Certificate", "Cluster"),
		scopeEntry("v1", "ConfigMap", "Cluster"),
		scopeEntry("v1", "Namespace", "Namespaced"),
		scopeEntry("scheduling.k8s.io/v1", "PriorityClass", "Namespaced"),
		scopeEntry("fixtures.example.com/v1", "Widget", "Namespaced"),
		scopeEntry("example.io/v1", "NotRendered", "Namespaced"),
	}
	want := map[string]string{
		"Gadget/stated-namespaced":                              "team",
		"Gadget/stated-namespaced-authored":                     "elsewhere",
		"Gadget/hooked":                                         "team",
		"ClusterGadget/stated-cluster":                          "",
		"ClusterGadget/stated-cluster-authored":                 "kept",
		"Thing/unknown":                                         "",
		"Gadget/other-version":                                  "",
		"ClusterIssuer/table-cluster":                           "team",
		"Certificate/table-namespaced":                          "",
		"ConfigMap/api-namespaced":                              "team",
		"Namespace/api-cluster":                                 "",
		"PriorityClass/api-cluster-scheduling":                  "",
		"Widget/agreed":                                         "team",
		"CustomResourceDefinition/widgets.fixtures.example.com": "",
	}
	nsWant(t, "with overrides", scopeGenerate(t, scopeFixture(t, overrides, raw)), want)

	// The same render with no override: what the overrides changed.
	without := map[string]string{}
	for k, v := range want {
		without[k] = v
	}
	without["Gadget/stated-namespaced"] = ""
	without["Gadget/hooked"] = ""
	without["ClusterIssuer/table-cluster"] = ""
	without["Certificate/table-namespaced"] = "team"
	nsWant(t, "without overrides", scopeGenerate(t, scopeFixture(t, nil, raw)), without)
}

// TestHelmTemplate_ScopeOverrideContradictingRenderedCRD: an override that
// disagrees with the CustomResourceDefinition the chart renders for the kind
// is refused when the chart is rendered, under Generate, AugmentLayout and
// ApplyPolicy, naming the component, the object and both scopes; a config
// with no namespace is refused all the same. A CustomResourceDefinition the
// output drops defines no scope, so the override stands.
func TestHelmTemplate_ScopeOverrideContradictingRenderedCRD(t *testing.T) {
	cases := []struct {
		name, crdScope, override, want string
	}{
		{"cluster override on a namespaced CRD", "Namespaced", "Cluster", `helmtemplate "myapp": object Widget "w": scopeOverrides says Cluster but the CustomResourceDefinition for Widget.fixtures.example.com the chart renders declares Namespaced`},
		{"namespaced override on a cluster CRD", "Cluster", "Namespaced", `helmtemplate "myapp": object Widget "w": scopeOverrides says Namespaced but the CustomResourceDefinition for Widget.fixtures.example.com the chart renders declares Cluster`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := nsRender(nsFixtureCRD("Widget", "widgets", tc.crdScope, ""), nsDoc("fixtures.example.com/v1", "Widget", "w", "", ""))
			overrides := []any{scopeEntry("fixtures.example.com/v1", "Widget", tc.override)}
			refused := func(where string, err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("%s: error = %v, want it to contain %q", where, err, tc.want)
				}
			}
			_, err := scopeFixture(t, overrides, raw).Generate(nil)
			refused("Generate", err)
			refused("AugmentLayout", scopeFixture(t, overrides, raw).AugmentLayout(nil))
			refused("ApplyPolicy", scopeFixture(t, overrides, raw).ApplyPolicy(fakePolicy{}))

			bare := scopeFixture(t, overrides, raw)
			bare.Namespace = ""
			_, err = bare.Generate(nil)
			refused("Generate with no namespace", err)
		})
	}

	t.Run("a dropped CRD defines no scope", func(t *testing.T) {
		raw := nsRender(nsFixtureCRD("Widget", "widgets", "Cluster", "test"), nsDoc("fixtures.example.com/v1", "Widget", "w", "", ""))
		cfg := scopeFixture(t, []any{scopeEntry("fixtures.example.com/v1", "Widget", "Namespaced")}, raw)
		nsWant(t, "Generate", scopeGenerate(t, cfg), map[string]string{"Widget/w": "team"})
	})
}

// TestHelmTemplateHandler_ScopeOverridesDecode: the property is read as the
// manifests component reads its own. A malformed entry is refused at decode,
// before any render, with that component's messages under the helmtemplate
// prefix; null is omission; and another spelling of the key is an unknown
// property.
func TestHelmTemplateHandler_ScopeOverridesDecode(t *testing.T) {
	decode := func(props map[string]any) (*HelmTemplateConfig, error) {
		cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&oam.Component{Name: "myapp", Type: helmTemplateType, Properties: props}, "team")
		if err != nil {
			return nil, err
		}
		return cfg.(*HelmTemplateConfig), nil
	}

	refusals := []struct {
		name      string
		overrides any
		want      string
	}{
		{"not a list", map[string]any{"apiVersion": "v1"}, "helmtemplate: scopeOverrides must be a list of {apiVersion, kind, scope} objects"},
		{"entry not an object", []any{"Widget"}, "helmtemplate: scopeOverrides[0]: expected an object"},
		{"apiVersion missing", []any{map[string]any{"kind": "Widget", "scope": "Cluster"}}, "helmtemplate: scopeOverrides[0]: apiVersion is required"},
		{"kind missing", []any{map[string]any{"apiVersion": "example.io/v1", "scope": "Cluster"}}, "helmtemplate: scopeOverrides[0]: kind is required"},
		{"scope missing", []any{map[string]any{"apiVersion": "example.io/v1", "kind": "Widget"}}, `helmtemplate: scopeOverrides[0]: scope "" is invalid; must be "Cluster" or "Namespaced"`},
		{"scope outside the two", []any{scopeEntry("example.io/v1", "Widget", "cluster")}, `helmtemplate: scopeOverrides[0]: scope "cluster" is invalid; must be "Cluster" or "Namespaced"`},
		{"second entry malformed", []any{scopeEntry("example.io/v1", "Widget", "Cluster"), 7}, "helmtemplate: scopeOverrides[1]: expected an object"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decode(scopeProps(tc.overrides))
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("accepted entries", func(t *testing.T) {
		cfg, err := decode(scopeProps([]any{
			scopeEntry("example.io/v1", "Gadget", "Namespaced"),
			scopeEntry("example.io/v1", "ClusterGadget", "Cluster"),
		}))
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		want := map[schema.GroupVersionKind]manifest.ScopeResult{
			{Group: "example.io", Version: "v1", Kind: "Gadget"}:        manifest.ScopeNamespaced,
			{Group: "example.io", Version: "v1", Kind: "ClusterGadget"}: manifest.ScopeCluster,
		}
		if len(cfg.ScopeOverrides) != len(want) {
			t.Fatalf("ScopeOverrides = %v, want %v", cfg.ScopeOverrides, want)
		}
		for gvk, scope := range want {
			if got, ok := cfg.ScopeOverrides[gvk]; !ok || got != scope {
				t.Errorf("ScopeOverrides[%s] = %v (present %t), want %v", gvk, got, ok, scope)
			}
		}
	})

	t.Run("null is omission", func(t *testing.T) {
		for _, nv := range nullValues() {
			props := scopeProps(nil)
			props[scopeOverridesKey] = nv.val
			cfg, err := decode(props)
			if err != nil {
				t.Errorf("%s: %v", nv.name, err)
				continue
			}
			if len(cfg.ScopeOverrides) != 0 {
				t.Errorf("%s: ScopeOverrides = %v, want none", nv.name, cfg.ScopeOverrides)
			}
		}
	})

	t.Run("another spelling is an unknown property", func(t *testing.T) {
		props := scopeProps(nil)
		props["ScopeOverrides"] = []any{scopeEntry("example.io/v1", "Gadget", "Namespaced")}
		_, err := decode(props)
		if err == nil || !strings.Contains(err.Error(), "helmtemplate: properties do not decode") {
			t.Errorf("error = %v, want the strict decode to refuse the key", err)
		}
	})
}

// TestHelmTemplateConfig_DirectScopeOverrideValue: a config built directly
// can hold a scope the property cannot express. It is refused before any
// render.
func TestHelmTemplateConfig_DirectScopeOverrideValue(t *testing.T) {
	rendered := false
	cfg := &HelmTemplateConfig{
		Name:      "myapp",
		Namespace: "team",
		SourceURL: "https://charts.example.com",
		Chart:     "myapp",
		ScopeOverrides: map[schema.GroupVersionKind]manifest.ScopeResult{
			{Group: "example.io", Version: "v1", Kind: "Gadget"}: manifest.ScopeUnknown,
		},
		renderChart: func(string, string, map[string]any, ...helm.RenderOption) ([]byte, error) {
			rendered = true
			return nil, nil
		},
	}
	const want = "helmtemplate: the scope override for example.io/v1 Gadget is neither Namespaced nor Cluster"
	if _, err := cfg.Generate(nil); err == nil || err.Error() != want {
		t.Errorf("Generate error = %v, want %q", err, want)
	}
	if rendered {
		t.Error("the chart was rendered; the override must be refused first")
	}
}

// TestScopeOverrides_ManifestsAndTemplateDeliveryResolveAlike holds the two
// readers of scopeOverrides to one resolution (resolveObjectScope): for the
// same object, among the same CustomResourceDefinitions and under the same
// overrides, the manifests component and template delivery agree on whether
// the object is namespaced (both stamp the namespace, or neither does) and on
// whether the override contradicts a CustomResourceDefinition (both refuse it,
// or neither does). What each does beyond that differs and is pinned by its
// own tests: the manifests component refuses an object of unknown scope
// without a namespace, which template delivery leaves as rendered.
func TestScopeOverrides_ManifestsAndTemplateDeliveryResolveAlike(t *testing.T) {
	crds := nsRender(
		nsFixtureCRD("Widget", "widgets", "Namespaced", ""),
		nsFixtureCRD("ClusterWidget", "clusterwidgets", "Cluster", ""),
	)
	objects := []struct{ apiVersion, kind string }{
		{"v1", "ConfigMap"},
		{"v1", "Namespace"},
		{"scheduling.k8s.io/v1", "PriorityClass"},
		{"cert-manager.io/v1", "ClusterIssuer"},
		{"cert-manager.io/v1", "Certificate"},
		{"fixtures.example.com/v1", "Widget"},
		{"fixtures.example.com/v1", "ClusterWidget"},
		{"example.io/v1", "Gadget"},
	}
	const conflictText = "scopeOverrides says"
	for _, obj := range objects {
		gvk := schema.FromAPIVersionAndKind(obj.apiVersion, obj.kind)
		for _, tc := range []struct {
			name      string
			overrides map[schema.GroupVersionKind]manifest.ScopeResult
		}{
			{"no override", nil},
			{"Namespaced", map[schema.GroupVersionKind]manifest.ScopeResult{gvk: manifest.ScopeNamespaced}},
			{"Cluster", map[schema.GroupVersionKind]manifest.ScopeResult{gvk: manifest.ScopeCluster}},
		} {
			t.Run(obj.kind+"/"+tc.name, func(t *testing.T) {
				raw := nsRender(crds, nsDoc(obj.apiVersion, obj.kind, "o", "", ""))
				parse := func() []helm.HookGroup {
					groups, err := parseChartManifests([]byte(raw))
					if err != nil {
						t.Fatalf("parseChartManifests: %v", err)
					}
					return groups
				}
				namespaceOf := func(objs []client.Object) string {
					for _, o := range objs {
						if o.GetName() == "o" {
							return o.GetNamespace()
						}
					}
					t.Fatal("the object is not among the parsed ones")
					return ""
				}
				flat := func(groups []helm.HookGroup) []client.Object {
					var objs []client.Object
					for _, g := range groups {
						objs = append(objs, g.Resources...)
					}
					return objs
				}

				templateGroups := parse()
				templateErr := stampRenderedNamespaces("team", tc.overrides, templateGroups)
				templateConflict := templateErr != nil && strings.Contains(templateErr.Error(), conflictText)
				if templateErr != nil && !templateConflict {
					t.Fatalf("template delivery: unexpected error %v", templateErr)
				}

				manifestObjs := flat(parse())
				_, manifestsErr := stampManifestNamespaces(tc.overrides)("team", manifestObjs)
				manifestsConflict := manifestsErr != nil && strings.Contains(manifestsErr.Error(), conflictText)
				manifestsUnknown := manifestsErr != nil && strings.Contains(manifestsErr.Error(), "has unknown scope")
				if manifestsErr != nil && !manifestsConflict && !manifestsUnknown {
					t.Fatalf("manifests: unexpected error %v", manifestsErr)
				}

				if templateConflict != manifestsConflict {
					t.Fatalf("contradiction: template delivery %t (%v), manifests %t (%v)", templateConflict, templateErr, manifestsConflict, manifestsErr)
				}
				if templateConflict {
					return
				}
				templateStamped := namespaceOf(flat(templateGroups)) == "team"
				manifestsStamped := namespaceOf(manifestObjs) == "team"
				if templateStamped != manifestsStamped {
					t.Errorf("namespaced: template delivery %t, manifests %t", templateStamped, manifestsStamped)
				}
				if manifestsUnknown && templateStamped {
					t.Error("the manifests component finds the scope unknown; template delivery stamped the object")
				}
			})
		}
	}
}
