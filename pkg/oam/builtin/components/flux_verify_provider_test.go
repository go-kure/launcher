package components_test

import (
	"encoding/json"
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// fluxVerifyKind is one of the three kinds whose spec holds a verification
// with a provider the Go type always encodes.
type fluxVerifyKind struct {
	typ     string
	handler oam.ComponentHandler
	// props are the kind's properties with verify as its verification block,
	// and without one when verify is nil.
	props func(verify any) map[string]any
	// path is the verification block in the properties and in the object's
	// spec, which have the same shape.
	path []string
	// emptied returns the config with the provider of its verification set to
	// "", as a caller building the config in Go leaves it, and a read of that
	// provider afterwards.
	emptied func(t *testing.T, cfg stack.ApplicationConfig) func() string
}

func withFluxVerify(props map[string]any, verify any, path ...string) map[string]any {
	if verify == nil {
		return props
	}
	node := props
	for _, key := range path[:len(path)-1] {
		node = node[key].(map[string]any)
	}
	node[path[len(path)-1]] = verify
	return props
}

var fluxVerifyKinds = []fluxVerifyKind{
	{
		typ: "helmrelease", handler: &components.HelmReleaseHandler{}, path: []string{"chart", "spec", "verify"},
		props: func(verify any) map[string]any {
			return withFluxVerify(map[string]any{"chart": map[string]any{"spec": map[string]any{
				"chart": "app", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"},
			}}}, verify, "chart", "spec", "verify")
		},
		emptied: func(t *testing.T, cfg stack.ApplicationConfig) func() string {
			c := cfg.(*components.HelmReleaseConfig)
			c.Spec.Chart.Spec.Verify.Provider = ""
			return func() string { return c.Spec.Chart.Spec.Verify.Provider }
		},
	},
	{
		typ: "ocirepository", handler: &components.OCIRepositoryHandler{}, path: []string{"verify"},
		props: func(verify any) map[string]any {
			return withFluxVerify(map[string]any{"url": "oci://registry.example.com/charts/app"}, verify, "verify")
		},
		emptied: func(t *testing.T, cfg stack.ApplicationConfig) func() string {
			c := cfg.(*components.OCIRepositoryConfig)
			c.Spec.Verify.Provider = ""
			return func() string { return c.Spec.Verify.Provider }
		},
	},
	{
		typ: "helmchart", handler: &components.HelmChartHandler{}, path: []string{"verify"},
		props: func(verify any) map[string]any {
			return withFluxVerify(map[string]any{
				"chart": "app", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"},
			}, verify, "verify")
		},
		emptied: func(t *testing.T, cfg stack.ApplicationConfig) func() string {
			c := cfg.(*components.HelmChartConfig)
			c.Spec.Verify.Provider = ""
			return func() string { return c.Spec.Verify.Provider }
		},
	},
}

// fluxSpecBlock is the block at path under the spec of the one object cfg
// generates, read from the object's JSON, which is what reaches the API
// server; ok is false when the object has none.
func fluxSpecBlock(t *testing.T, cfg stack.ApplicationConfig, path []string) (map[string]any, bool) {
	t.Helper()
	objs, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate emitted %d objects, want one", len(objs))
	}
	raw, err := json.Marshal(*objs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	block, ok, err := unstructured.NestedMap(obj, append([]string{"spec"}, path...)...)
	if err != nil {
		t.Fatalf("spec.%s: %v", strings.Join(path, "."), err)
	}
	return block, ok
}

// TestFluxKinds_VerifyProviderDefault holds the three kinds to what they say
// about a verification's provider: unauthored it is written as the API's
// default, an authored one is kept, an authored "" is refused, and no
// verification is written where none was authored.
func TestFluxKinds_VerifyProviderDefault(t *testing.T) {
	secret := func() map[string]any { return map[string]any{"name": "keys"} }
	for _, k := range fluxVerifyKinds {
		t.Run(k.typ, func(t *testing.T) {
			path := strings.Join(k.path, ".")
			generated := func(t *testing.T, verify any) (map[string]any, bool) {
				t.Helper()
				cfg, err := kindConfig(t, k.handler, k.typ, "app", k.props(verify))
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				return fluxSpecBlock(t, cfg, k.path)
			}

			t.Run("unauthored provider is written as the default", func(t *testing.T) {
				block, ok := generated(t, map[string]any{"secretRef": secret()})
				if !ok || block["provider"] != "cosign" {
					t.Errorf("spec.%s = %v, want provider cosign", path, block)
				}
			})
			t.Run("null provider is written as the default", func(t *testing.T) {
				block, ok := generated(t, map[string]any{"provider": nil, "secretRef": secret()})
				if !ok || block["provider"] != "cosign" {
					t.Errorf("spec.%s = %v, want provider cosign", path, block)
				}
			})
			t.Run("authored provider is kept", func(t *testing.T) {
				block, ok := generated(t, map[string]any{"provider": "notation", "secretRef": secret()})
				if !ok || block["provider"] != "notation" {
					t.Errorf("spec.%s = %v, want provider notation", path, block)
				}
			})
			t.Run("no verification is written where none is authored", func(t *testing.T) {
				if block, ok := generated(t, nil); ok {
					t.Errorf("spec.%s = %v, want none", path, block)
				}
			})
			for _, key := range []string{"provider", "Provider"} {
				t.Run("authored empty "+key+" is refused", func(t *testing.T) {
					_, err := kindConfig(t, k.handler, k.typ, "app", k.props(map[string]any{key: "", "secretRef": secret()}))
					want := path + "." + key + `: "" is refused by the Flux API`
					if err == nil || !strings.HasPrefix(err.Error(), want) {
						t.Errorf("ToApplicationConfig error = %v, want it to start with %q", err, want)
					}
				})
			}
			// Properties built in Go with a typed map or a named string type
			// encode the same "" and are refused the same way.
			type provider string
			for name, verify := range map[string]any{
				"typed map":         map[string]string{"provider": ""},
				"named string type": map[string]any{"provider": provider(""), "secretRef": secret()},
			} {
				t.Run("authored empty provider in a "+name+" is refused", func(t *testing.T) {
					_, err := kindConfig(t, k.handler, k.typ, "app", k.props(verify))
					want := path + `.provider: "" is refused by the Flux API`
					if err == nil || !strings.HasPrefix(err.Error(), want) {
						t.Errorf("ToApplicationConfig error = %v, want it to start with %q", err, want)
					}
				})
			}
			// A number the decode keeps exact but a float64 cannot hold, in a
			// HelmRelease's free-form values, does not stop the check.
			if k.typ == "helmrelease" {
				t.Run("authored empty provider beside an out-of-range number is refused", func(t *testing.T) {
					props := k.props(map[string]string{"provider": ""})
					props["values"] = map[string]any{"large": json.Number("1e1000")}
					_, err := kindConfig(t, k.handler, k.typ, "app", props)
					want := path + `.provider: "" is refused by the Flux API`
					if err == nil || !strings.HasPrefix(err.Error(), want) {
						t.Errorf("ToApplicationConfig error = %v, want it to start with %q", err, want)
					}
				})
			}
			// A config built in Go cannot say whether its "" was meant: it is
			// filled, and the config's own spec is left as it was.
			t.Run("empty provider of a config built in Go is filled", func(t *testing.T) {
				cfg, err := kindConfig(t, k.handler, k.typ, "app", k.props(map[string]any{"provider": "notation", "secretRef": secret()}))
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				provider := k.emptied(t, cfg)
				block, ok := fluxSpecBlock(t, cfg, k.path)
				if !ok || block["provider"] != "cosign" {
					t.Errorf("spec.%s = %v, want provider cosign", path, block)
				}
				if got := provider(); got != "" {
					t.Errorf("Generate changed the config's provider to %q", got)
				}
			})
		})
	}
}

// TestHelmRelease_ChartSourceKindRequired: a chart's source reference without
// a kind is refused where the component is read and again in Generate, for a
// config built in Go. A chartRef is another type: the API requires its kind
// too, but the Go type always encodes it, so it is in neither of the table's
// two sets and this change does not check it.
func TestHelmRelease_ChartSourceKindRequired(t *testing.T) {
	const want = "chart.spec.sourceRef.kind: required"
	props := map[string]any{"chart": map[string]any{"spec": map[string]any{
		"chart": "app", "sourceRef": map[string]any{"name": "charts"},
	}}}
	if _, err := kindConfig(t, &components.HelmReleaseHandler{}, "helmrelease", "app", props); err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("ToApplicationConfig error = %v, want it to start with %q", err, want)
	}

	cfg := &components.HelmReleaseConfig{Name: "app", Namespace: "default", Spec: helmv2.HelmReleaseSpec{
		Chart: &helmv2.HelmChartTemplate{Spec: helmv2.HelmChartTemplateSpec{
			Chart: "app", SourceRef: helmv2.CrossNamespaceObjectReference{Name: "charts"},
		}},
	}}
	if _, err := cfg.Generate(stack.NewApplication("app", "default", cfg)); err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("Generate error = %v, want it to start with %q", err, want)
	}
	cfg.Spec.Chart.Spec.SourceRef.Kind = sourcev1.HelmRepositoryKind
	if _, err := cfg.Generate(stack.NewApplication("app", "default", cfg)); err != nil {
		t.Errorf("Generate with a kind: %v", err)
	}
}

// TestFluxRules_EmitWhatTheirKindsRequireAndFill shows what the two rules that
// emit a helmrelease or an ocirepository write, against what those kinds refuse
// or fill since go-kure/launcher#790. Under an authored chart the helm rule
// writes chart.spec.sourceRef.kind for every source form that leads to one, so
// the refusal never meets a component it emitted. Neither rule writes a
// verification, so the default is never filled into one either. No rule emits a
// helmchart; one that appeared in these outputs would be held to the same.
//
// The cases are the source forms of the two rules, not every document: a
// property that changed which keys a rule writes would need its own case.
func TestFluxRules_EmitWhatTheirKindsRequireAndFill(t *testing.T) {
	handlers := map[string]oam.ComponentHandler{
		"helmrelease":   &components.HelmReleaseHandler{},
		"ocirepository": &components.OCIRepositoryHandler{},
		"helmchart":     &components.HelmChartHandler{},
	}
	verifyPaths := map[string][]string{
		"helmrelease":   {"chart", "spec", "verify"},
		"ocirepository": {"verify"},
		"helmchart":     {"verify"},
	}

	gitRef := map[string]any{"tag": "v1.0.0"}
	helmCases := []struct {
		name  string
		props map[string]any
		// chart reports that the release takes a chart template, whose source
		// reference needs a kind; otherwise it takes a chartRef.
		chart bool
	}{
		{"inline HelmRepository", map[string]any{"chart": "app", "version": "1.0.0", "source": map[string]any{"url": "https://charts.example.com"}}, true},
		{"inline GitRepository", map[string]any{"chart": "./charts/app", "source": map[string]any{"url": "https://git.example.com/org/charts", "kind": "GitRepository", "ref": gitRef}}, true},
		{"inline Bucket", map[string]any{"chart": "charts/app", "source": map[string]any{"kind": "Bucket", "endpoint": "minio.example.com", "bucketName": "charts"}}, true},
		{"referenced HelmRepository", map[string]any{"chart": "app", "version": "1.0.0", "source": map[string]any{"kind": "HelmRepository", "name": "charts"}}, true},
		{"referenced GitRepository", map[string]any{"chart": "./charts/app", "source": map[string]any{"kind": "GitRepository", "name": "charts"}}, true},
		{"referenced Bucket", map[string]any{"chart": "charts/app", "source": map[string]any{"kind": "Bucket", "name": "charts", "namespace": "flux-system"}}, true},
		{"inline OCIRepository", map[string]any{"version": "1.0.0", "source": map[string]any{"url": "oci://registry.example.com/charts/app"}}, false},
		{"referenced OCIRepository", map[string]any{"source": map[string]any{"kind": "OCIRepository", "name": "charts"}}, false},
		{"referenced HelmChart", map[string]any{"source": map[string]any{"kind": "HelmChart", "name": "charts"}}, false},
	}

	seen := map[string]int{}
	charts := 0
	check := func(t *testing.T, emitted []oam.Component) {
		t.Helper()
		for _, comp := range emitted {
			path, ok := verifyPaths[comp.Type]
			if !ok {
				continue
			}
			seen[comp.Type]++
			if block, found, _ := unstructured.NestedMap(comp.Properties, path...); found {
				if provider, _ := block["provider"].(string); provider == "" {
					t.Errorf("%s %q is emitted with %s and no provider: %v", comp.Type, comp.Name, strings.Join(path, "."), block)
				}
			}
			if _, err := handlers[comp.Type].ToApplicationConfig(&comp, "default"); err != nil {
				t.Errorf("%s %q as emitted is refused by its kind: %v", comp.Type, comp.Name, err)
			}
		}
	}

	for _, tc := range helmCases {
		t.Run("helm/"+tc.name, func(t *testing.T) {
			emitted := lowerHelm(t, helmLowering("shop"), "app", tc.props)
			check(t, emitted)
			release := componentByType(t, emitted, "helmrelease").Properties
			_, hasChart := release["chart"]
			_, hasRef := release["chartRef"]
			if hasChart != tc.chart || hasRef == tc.chart {
				t.Fatalf("release has chart=%v chartRef=%v, want chart=%v and the other absent", hasChart, hasRef, tc.chart)
			}
			if !tc.chart {
				return
			}
			charts++
			kind, _, _ := unstructured.NestedString(release, "chart", "spec", "sourceRef", "kind")
			if want := tc.props["source"].(map[string]any)["kind"]; kind == "" || (want != nil && kind != want) {
				t.Errorf("chart.spec.sourceRef.kind = %q, want the source's kind (%v)", kind, want)
			}
		})
	}
	t.Run("oci", func(t *testing.T) {
		app := ociDocument(*ociComponent(validOCIProps()))
		check(t, lowerOCI(t, ociLowering("shop"), app, "checkout"))
	})

	// Without these the checks above could pass over nothing.
	if charts != 6 {
		t.Errorf("checked the source reference of %d chart templates, want 6", charts)
	}
	if seen["helmrelease"] != len(helmCases) || seen["ocirepository"] != 2 {
		t.Errorf("checked %d helmrelease and %d ocirepository components, want %d and 2 (the helm rule's inline OCI source and the oci rule's)", seen["helmrelease"], seen["ocirepository"], len(helmCases))
	}
	if seen["helmchart"] != 0 {
		t.Errorf("a rule emitted %d helmchart components; say so in this test's comment", seen["helmchart"])
	}
}
