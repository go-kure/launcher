package components

// Tests of the scopeOverrides property on the helm rule: forwarded to the
// helmtemplate component under delivery: template, refused under delivery:
// flux (go-kure/launcher#794, item 11).

import (
	"reflect"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// helmScopeProps is the properties of a helm component on an HTTP Helm
// repository under delivery ("" for the default), with scopeOverrides under key
// when overrides is not nil.
func helmScopeProps(delivery, key string, overrides any) map[string]any {
	props := map[string]any{
		"chart":  "myapp",
		"source": map[string]any{"url": "https://charts.example.com"},
	}
	if delivery != "" {
		props["delivery"] = delivery
	}
	if overrides != nil {
		props[key] = overrides
	}
	return props
}

// lowerHelmScope lowers a helm component named "myapp" with props.
func lowerHelmScope(props map[string]any) (oam.LoweringResult, error) {
	lctx := oam.LoweringContext{
		Namer:  oam.NewNameAllocator(),
		Origin: oam.Origin{Document: "shop", DocumentKind: "Application", Namespace: "default", Component: "myapp", ComponentType: helmType},
	}
	return HelmRule{}.LowerComponent(&oam.Component{Name: "myapp", Type: helmType, Properties: props}, lctx)
}

// TestHelmRule_TemplateForwardsScopeOverrides: under delivery: template the
// authored list reaches the helmtemplate component as written, under the
// declared spelling of the key whatever spelling the author used, and the
// component the rule emits then generates what a helmtemplate authored with the
// same list generates.
func TestHelmRule_TemplateForwardsScopeOverrides(t *testing.T) {
	overrides := []any{
		scopeEntry("example.io/v1", "Gadget", "Namespaced"),
		scopeEntry("example.io/v1", "ClusterGadget", "Cluster"),
		scopeEntry("cert-manager.io/v1", "Certificate", "Cluster"),
	}
	raw := nsRender(
		nsDoc("example.io/v1", "Gadget", "stated-namespaced", "", ""),
		nsDoc("example.io/v1", "ClusterGadget", "stated-cluster", "kept", ""),
		nsDoc("example.io/v1", "Thing", "unknown", "", ""),
		nsDoc("cert-manager.io/v1", "Certificate", "table-namespaced", "", ""),
		nsDoc("v1", "ConfigMap", "api-namespaced", "", ""),
	)
	want := map[string]string{
		"Gadget/stated-namespaced":     "team",
		"ClusterGadget/stated-cluster": "kept",
		"Thing/unknown":                "",
		"Certificate/table-namespaced": "",
		"ConfigMap/api-namespaced":     "team",
	}
	nsWant(t, "authored helmtemplate", scopeGenerate(t, scopeFixture(t, overrides, raw)), want)

	for _, key := range []string{scopeOverridesKey, "ScopeOverrides"} {
		t.Run(key, func(t *testing.T) {
			res, err := lowerHelmScope(helmScopeProps("template", key, overrides))
			if err != nil {
				t.Fatalf("LowerComponent: %v", err)
			}
			if len(res.Components) != 1 || res.Components[0].Type != helmTemplateType {
				t.Fatalf("emitted %+v, want only the helmtemplate", res.Components)
			}
			emitted := res.Components[0]
			if got := emitted.Properties[scopeOverridesKey]; !reflect.DeepEqual(got, any(overrides)) {
				t.Errorf("%s = %v, want the authored %v", scopeOverridesKey, got, overrides)
			}
			if _, ok := emitted.Properties["ScopeOverrides"]; ok {
				t.Errorf("properties = %v, kept the authored spelling ScopeOverrides", emitted.Properties)
			}
			cfg, err := (&HelmTemplateHandler{}).ToApplicationConfig(&emitted, "team")
			if err != nil {
				t.Fatalf("helmtemplate ToApplicationConfig: %v", err)
			}
			tc := cfg.(*HelmTemplateConfig)
			tc.renderChart = stubRender(raw)
			nsWant(t, "lowered from helm", scopeGenerate(t, tc), want)
		})
	}
}

// TestHelmRule_TemplateScopeOverridesMalformed: a malformed entry is refused by
// the rule, before the helmtemplate component reads it, with the manifests
// component's messages under the helm prefix.
func TestHelmRule_TemplateScopeOverridesMalformed(t *testing.T) {
	cases := []struct {
		name      string
		overrides any
		want      string
	}{
		{"not a list", map[string]any{"apiVersion": "v1"}, "helm: scopeOverrides must be a list of {apiVersion, kind, scope} objects"},
		{"entry not an object", []any{"Widget"}, "helm: scopeOverrides[0]: expected an object"},
		{"apiVersion missing", []any{map[string]any{"kind": "Widget", "scope": "Cluster"}}, "helm: scopeOverrides[0]: apiVersion is required"},
		{"kind missing", []any{map[string]any{"apiVersion": "example.io/v1", "scope": "Cluster"}}, "helm: scopeOverrides[0]: kind is required"},
		{"scope missing", []any{map[string]any{"apiVersion": "example.io/v1", "kind": "Widget"}}, `helm: scopeOverrides[0]: scope "" is invalid; must be "Cluster" or "Namespaced"`},
		{"scope outside the two", []any{scopeEntry("example.io/v1", "Widget", "cluster")}, `helm: scopeOverrides[0]: scope "cluster" is invalid; must be "Cluster" or "Namespaced"`},
		{"second entry malformed", []any{scopeEntry("example.io/v1", "Widget", "Cluster"), 7}, "helm: scopeOverrides[1]: expected an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lowerHelmScope(helmScopeProps("template", scopeOverridesKey, tc.overrides))
			if err == nil || err.Error() != tc.want {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestHelmRule_FluxRefusesScopeOverrides: delivery: flux, set or defaulted,
// refuses the property by name, whatever its shape, and emits nothing.
func TestHelmRule_FluxRefusesScopeOverrides(t *testing.T) {
	const want = "helm: delivery: flux does not support scopeOverrides (only a client-side render reads it)"
	for _, delivery := range []string{"", "flux"} {
		for _, tc := range []struct {
			name      string
			key       string
			overrides any
		}{
			{"well formed", scopeOverridesKey, []any{scopeEntry("example.io/v1", "Gadget", "Namespaced")}},
			{"other spelling", "ScopeOverrides", []any{scopeEntry("example.io/v1", "Gadget", "Namespaced")}},
			{"empty list", scopeOverridesKey, []any{}},
			{"malformed", scopeOverridesKey, []any{"Widget"}},
		} {
			t.Run("delivery "+delivery+"/"+tc.name, func(t *testing.T) {
				res, err := lowerHelmScope(helmScopeProps(delivery, tc.key, tc.overrides))
				if err == nil || err.Error() != want {
					t.Errorf("error = %v, want %q", err, want)
				}
				if len(res.Components) != 0 {
					t.Errorf("emitted %+v with the refusal", res.Components)
				}
			})
		}
	}
}

// TestHelmRule_NullScopeOverridesIsAbsent: a null, typed or untyped, reads as
// omission under both deliveries: not refused under delivery: flux, and
// forwarded to neither terminal.
func TestHelmRule_NullScopeOverridesIsAbsent(t *testing.T) {
	for _, delivery := range []string{"flux", "template"} {
		for _, nv := range nullValues() {
			t.Run(delivery+"/"+nv.name, func(t *testing.T) {
				props := helmScopeProps(delivery, scopeOverridesKey, nil)
				props[scopeOverridesKey] = nv.val
				res, err := lowerHelmScope(props)
				if err != nil {
					t.Fatalf("LowerComponent: %v", err)
				}
				if len(res.Components) == 0 {
					t.Fatal("nothing was emitted")
				}
				for _, c := range res.Components {
					if _, ok := c.Properties[scopeOverridesKey]; ok {
						t.Errorf("%s %s carries the null %s: %v", c.Type, c.Name, scopeOverridesKey, c.Properties)
					}
				}
			})
		}
	}
}
