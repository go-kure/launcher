package components

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-kure/launcher/pkg/oam"
)

// helmReleaseChartSourceSamples is an authored value of each inert chart
// template field, by its path under chart.spec.
var helmReleaseChartSourceSamples = map[string]any{
	"url":                         "oci://registry.example.com/charts/podinfo",
	"ref":                         map[string]any{"tag": "6.7.0"},
	"layerSelector":               map[string]any{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "operation": "copy"},
	"provider":                    "generic",
	"secretRef":                   map[string]any{"name": "registry"},
	"serviceAccountName":          "puller",
	"certSecretRef":               map[string]any{"name": "ca"},
	"proxySecretRef":              map[string]any{"name": "proxy"},
	"timeout":                     "30s",
	"ignore":                      "*.md",
	"insecure":                    true,
	"suspend":                     true,
	"verify.matchOIDCIdentity":    []any{map[string]any{"issuer": "^https://token.actions.githubusercontent.com$", "subject": "^https://github.com/org/.*$"}},
	"verify.trustedRootSecretRef": map[string]any{"name": "root"},
}

// TestHelmReleaseChartInertFields_KindScoped: each field of the OCIRepository a
// chart template inlines that a template of kind HelmChart refuses as not read
// is taken under kind OCIRepository and carried into the HelmRelease as
// authored (go-kure/launcher#1072). Every inert field has a sample, so a new
// one fails here until it does.
func TestHelmReleaseChartInertFields_KindScoped(t *testing.T) {
	var paths []string
	for field := range helmReleaseChartInertFields {
		paths = append(paths, field)
	}
	for field := range helmReleaseChartVerifyInertFields {
		paths = append(paths, "verify."+field)
	}
	slices.Sort(paths)
	if got := slices.Sorted(maps.Keys(helmReleaseChartSourceSamples)); !slices.Equal(got, paths) {
		t.Fatalf("samples for %v, want one for each inert field %v", got, paths)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			author := func(kind string, spec map[string]any) (any, error) {
				parent := spec
				segments := strings.Split(path, ".")
				for _, s := range segments[:len(segments)-1] {
					child := map[string]any{"provider": "cosign"}
					parent[s] = child
					parent = child
				}
				parent[segments[len(segments)-1]] = helmReleaseChartSourceSamples[path]
				chart := map[string]any{"spec": spec}
				if kind != "" {
					chart["kind"] = kind
				}
				return (&HelmReleaseHandler{}).ToApplicationConfig(
					&oam.Component{Name: "web", Type: "helmrelease", Properties: map[string]any{"chart": chart}}, "demo")
			}

			_, err := author("", map[string]any{"chart": "podinfo", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"}})
			if want := "chart.spec." + path + ": not read for a chart template of kind HelmChart"; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("kind HelmChart: error = %v, want one containing %q", err, want)
			}

			cfg, err := author("OCIRepository", map[string]any{"url": helmReleaseChartSourceSamples["url"]})
			if err != nil {
				t.Fatalf("kind OCIRepository: %v", err)
			}
			objs, err := cfg.(*HelmReleaseConfig).Generate(nil)
			if err != nil {
				t.Fatalf("kind OCIRepository: Generate: %v", err)
			}
			m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(*objs[0])
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			at := m["spec"].(map[string]any)["chart"].(map[string]any)["spec"].(map[string]any)
			segments := strings.Split(path, ".")
			for _, s := range segments[:len(segments)-1] {
				at, _ = at[s].(map[string]any)
			}
			got, _ := json.Marshal(at[segments[len(segments)-1]])
			want, _ := json.Marshal(helmReleaseChartSourceSamples[path])
			if string(got) != string(want) {
				t.Errorf("kind OCIRepository: chart.spec.%s is written %s, want %s", path, got, want)
			}
		})
	}
}
