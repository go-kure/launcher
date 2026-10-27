package components_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A HelmRelease's chart template of kind OCIRepository has helm-controller
// generate an OCIRepository for the chart artifact chart.spec.url names
// (go-kure/launcher#1072). These tests hold the component to the Flux API's
// rules for that kind (helm-controller api v1.7.1 v2/helmrelease_types.go:372-380),
// to the allowed registries, to the Secrets it reads in the Flux namespace and
// to the hour its timeout must stay under.

const hrOCIURL = "oci://registry.example.com/charts/podinfo"

// hrOCIChart is a helmrelease document whose chart is of kind OCIRepository on
// hrOCIURL, with the spec keys of spec added.
func hrOCIChart(spec map[string]any) map[string]any {
	s := map[string]any{"url": hrOCIURL}
	maps.Copy(s, spec)
	return map[string]any{"chart": map[string]any{"kind": "OCIRepository", "spec": s}}
}

// TestHelmReleaseOCIChart_Admits: the template builds into the HelmRelease as
// authored, a verification without a provider gets cosign, and the Flux API's
// "*" version and ChartVersion strategy are taken.
func TestHelmReleaseOCIChart_Admits(t *testing.T) {
	hr := hrGenerate(t, hrConfig(t, "web", hrOCIChart(map[string]any{
		"ref":               map[string]any{"tag": "6.7.0"},
		"secretRef":         map[string]any{"name": "registry"},
		"verify":            map[string]any{"secretRef": map[string]any{"name": "cosign-pub"}},
		"interval":          "10m",
		"version":           "*",
		"reconcileStrategy": "ChartVersion",
	})), "")
	ch := hr.Spec.Chart
	if ch == nil {
		t.Fatal("no chart template emitted")
	}
	want := helmv2.HelmChartTemplateSpec{
		OCIRepositorySpec: sourcev1.OCIRepositorySpec{
			URL:       hrOCIURL,
			Reference: &sourcev1.OCIRepositoryRef{Tag: "6.7.0"},
			SecretRef: &meta.LocalObjectReference{Name: "registry"},
			Verify:    &sourcev1.OCIRepositoryVerification{Provider: "cosign", SecretRef: &meta.LocalObjectReference{Name: "cosign-pub"}},
		},
		Version:           "*",
		ReconcileStrategy: "ChartVersion",
	}
	want.Interval = &metav1.Duration{Duration: 10 * time.Minute}
	if ch.Kind != sourcev1.OCIRepositoryKind {
		t.Errorf("chart.kind = %q, want OCIRepository", ch.Kind)
	}
	got, _ := json.Marshal(ch.Spec)
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Errorf("chart.spec = %s\nwant %s", got, wantJSON)
	}
	if hr.Spec.Chart.Spec.SourceRef != nil || hr.Spec.ChartRef != nil {
		t.Error("an OCI chart template carries a sourceRef or a chartRef")
	}
}

// TestHelmReleaseOCIChart_Refuses holds each rule of the kind, on the parse
// path and on a config built directly, which Generate checks the same way.
func TestHelmReleaseOCIChart_Refuses(t *testing.T) {
	const notTaken = "not taken under chart.kind OCIRepository"
	ref := &helmv2.CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "charts"}
	cases := []struct {
		name   string
		spec   map[string]any // authored keys over hrOCIChart's url; a nil url removes it
		direct helmv2.HelmChartTemplateSpec
		want   string
	}{
		{"url missing", map[string]any{"url": nil}, helmv2.HelmChartTemplateSpec{},
			"helmrelease: chart.spec.url is required under chart.kind OCIRepository"},
		{"url not oci", map[string]any{"url": "https://charts.example.com/podinfo"}, helmv2.HelmChartTemplateSpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{URL: "https://charts.example.com/podinfo"}},
			"helmrelease: chart.spec.url must start with oci:// under chart.kind OCIRepository"},
		{"chart", map[string]any{"chart": "podinfo"}, helmv2.HelmChartTemplateSpec{Chart: "podinfo"},
			"chart.spec.chart: " + notTaken},
		{"sourceRef", map[string]any{"sourceRef": map[string]any{"kind": "HelmRepository", "name": "charts"}}, helmv2.HelmChartTemplateSpec{SourceRef: ref},
			"chart.spec.sourceRef: " + notTaken},
		{"valuesFiles", map[string]any{"valuesFiles": []any{"values-prod.yaml"}}, helmv2.HelmChartTemplateSpec{ValuesFiles: []string{"values-prod.yaml"}},
			"chart.spec.valuesFiles: " + notTaken},
		{"ignoreMissingValuesFiles", map[string]any{"ignoreMissingValuesFiles": true}, helmv2.HelmChartTemplateSpec{IgnoreMissingValuesFiles: true},
			"chart.spec.ignoreMissingValuesFiles: " + notTaken},
		{"version", map[string]any{"version": "6.7.0"}, helmv2.HelmChartTemplateSpec{Version: "6.7.0"},
			`chart.spec.version "6.7.0": under chart.kind OCIRepository only "*" is taken`},
		{"reconcileStrategy", map[string]any{"reconcileStrategy": "Revision"}, helmv2.HelmChartTemplateSpec{ReconcileStrategy: "Revision"},
			`chart.spec.reconcileStrategy "Revision": under chart.kind OCIRepository only ChartVersion is taken`},
		{"timeout of an hour in minutes", map[string]any{"timeout": "90m"}, helmv2.HelmChartTemplateSpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{Timeout: &metav1.Duration{Duration: 90 * time.Minute}}},
			"chart.spec.timeout 90m0s: under chart.kind OCIRepository it must be under an hour"},
		{"timeout of exactly an hour in minutes", map[string]any{"timeout": "60m"}, helmv2.HelmChartTemplateSpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{Timeout: &metav1.Duration{Duration: time.Hour}}},
			"use an ocirepository component and a chartRef to it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := hrOCIChart(nil)
			spec := doc["chart"].(map[string]any)["spec"].(map[string]any)
			for k, v := range tc.spec {
				if v == nil {
					delete(spec, k)
					continue
				}
				spec[k] = v
			}
			_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(
				&oam.Component{Name: "web", Type: "helmrelease", Properties: doc}, "demo")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parse: error = %v, want one containing %q", err, tc.want)
			}
			direct := tc.direct
			if direct.URL == "" && tc.name != "url missing" {
				direct.URL = hrOCIURL
			}
			cfg := &components.HelmReleaseConfig{Name: "web", Spec: helmv2.HelmReleaseSpec{
				Chart: &helmv2.HelmChartTemplate{Kind: sourcev1.OCIRepositoryKind, Spec: direct},
			}}
			if _, err := cfg.Generate(nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Generate: error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestHelmReleaseOCIChart_Timeout: chart.spec.timeout takes no h, as an
// OCIRepository's timeout does not, and stays under an hour, since
// helm-controller writes the OCIRepository it generates through the
// unstructured converter, which writes a duration of an hour or more with an
// h (helm-controller v1.7.1 internal/reconcile/helmchart_template.go:130,152).
// Under that, the timeout goes out as written, and so does every other
// duration of a HelmRelease.
func TestHelmReleaseOCIChart_Timeout(t *testing.T) {
	t.Run("an hour with h", func(t *testing.T) {
		_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(
			&oam.Component{Name: "web", Type: "helmrelease", Properties: hrOCIChart(map[string]any{"timeout": "1h"})}, "demo")
		if err == nil || !strings.Contains(err.Error(), "chart.spec.timeout") {
			t.Fatalf("error = %v, want the refusal of chart.spec.timeout", err)
		}
	})
	for _, text := range []string{"59m59s", "30s"} {
		t.Run(text, func(t *testing.T) {
			hr := hrGenerate(t, hrConfig(t, "web", hrOCIChart(map[string]any{"timeout": text})), "")
			m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(hr)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			spec := m["spec"].(map[string]any)["chart"].(map[string]any)["spec"].(map[string]any)
			if spec["timeout"] != text {
				t.Errorf("chart.spec.timeout is written %v, want %q", spec["timeout"], text)
			}
			// What helm-controller writes into the OCIRepository it generates:
			// the template's spec, converted.
			oci := &sourcev1.OCIRepository{Spec: hr.Spec.Chart.Spec.OCIRepositorySpec}
			om, err := runtime.DefaultUnstructuredConverter.ToUnstructured(oci)
			if err != nil {
				t.Fatalf("convert the OCIRepository: %v", err)
			}
			got, _ := om["spec"].(map[string]any)["timeout"].(string)
			if strings.Contains(got, "h") {
				t.Errorf("the generated OCIRepository's timeout is written %q, with an h", got)
			}
		})
	}
	t.Run("the probe the hour rests on", func(t *testing.T) {
		oci := &sourcev1.OCIRepository{Spec: sourcev1.OCIRepositorySpec{Timeout: &metav1.Duration{Duration: 90 * time.Minute}}}
		om, err := runtime.DefaultUnstructuredConverter.ToUnstructured(oci)
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got := om["spec"].(map[string]any)["timeout"]; got != "1h30m0s" {
			t.Errorf("90m converts to %v; the refusal of an hour or more rests on 1h30m0s", got)
		}
	})
}

// TestHelmReleaseOCIChart_ApplyPolicy: chart.spec.url is held to the allowed
// registries as an ocirepository's url is, the registry named explicitly and
// a repository path after it; no policy or an empty allowlist permits every
// url, and a chart template of kind HelmChart or a chartRef is no url of the
// component's.
func TestHelmReleaseOCIChart_ApplyPolicy(t *testing.T) {
	const (
		implicit   = "does not name its registry explicitly"
		notAllowed = "is not in allowed registries"
	)
	cases := []struct {
		name, url string
		allowed   []string
		want      string
	}{
		{"allowed", "oci://registry.example.com/charts/app", []string{"registry.example.com"}, ""},
		{"another registry", "oci://other.example.com/charts/app", []string{"registry.example.com"}, notAllowed},
		{"host only", "oci://ghcr.io", []string{"ghcr.io"}, implicit},
		{"single label", "oci://registry/app", []string{"registry"}, implicit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := hrConfig(t, "web", hrOCIChart(map[string]any{"url": tc.url})).(oam.Enforceable)
			err := cfg.ApplyPolicy(fakeOCIPolicy{allowed: tc.allowed})
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("url %q: %v, want allowed", tc.url, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("url %q: error = %v, want one containing %q", tc.url, err, tc.want)
			case err != nil && !strings.Contains(err.Error(), "helmrelease: chart.spec.url: "):
				t.Errorf("error %q does not name the component type and field", err)
			}
			if err := cfg.ApplyPolicy(nil); err != nil {
				t.Errorf("nil policy: %v", err)
			}
			if err := cfg.ApplyPolicy(fakeOCIPolicy{}); err != nil {
				t.Errorf("empty allowlist: %v", err)
			}
		})
	}
	strict := fakeOCIPolicy{allowed: []string{"registry.example.com"}}
	for name, props := range map[string]map[string]any{
		"kind HelmChart": {"chart": hrChart()},
		"chartRef":       {"chartRef": map[string]any{"kind": "OCIRepository", "name": "p"}},
	} {
		if err := hrConfig(t, "web", props).(oam.Enforceable).ApplyPolicy(strict); err != nil {
			t.Errorf("%s: %v, want no url of the component's checked", name, err)
		}
	}
}

// TestHelmReleaseOCIChart_FluxNamespaceReads: the OCIRepository helm-controller
// generates is in the HelmRelease's namespace, so every Secret it reads is read
// there, with a Flux namespace or without one.
func TestHelmReleaseOCIChart_FluxNamespaceReads(t *testing.T) {
	doc := hrOCIChart(map[string]any{
		"secretRef":      map[string]any{"name": "registry"},
		"certSecretRef":  map[string]any{"name": "ca"},
		"proxySecretRef": map[string]any{"name": "proxy"},
		"verify": map[string]any{
			"provider":             "cosign",
			"secretRef":            map[string]any{"name": "cosign-pub"},
			"trustedRootSecretRef": map[string]any{"name": "root"},
		},
	})
	want := []string{"ca", "cosign-pub", "proxy", "registry", "root"}
	for _, fluxNS := range []string{"", "flux-system"} {
		cfg := hrConfig(t, "web", doc)
		if fluxNS != "" {
			cfg.(interface{ SetFluxNamespace(string) }).SetFluxNamespace(fluxNS)
		}
		configMaps, secrets := cfg.(interface {
			FluxNamespaceReads() (configMaps, secrets []string)
		}).FluxNamespaceReads()
		slices.Sort(secrets)
		if len(configMaps) != 0 || !slices.Equal(secrets, want) {
			t.Errorf("Flux namespace %q: reads ConfigMaps %v, Secrets %v; want none and %v", fluxNS, configMaps, secrets, want)
		}
	}
}

// TestHelmReleaseOCIChart_GeneratesNoSource: the component emits the
// HelmRelease alone; the OCIRepository is helm-controller's to generate.
func TestHelmReleaseOCIChart_GeneratesNoSource(t *testing.T) {
	cfg := hrConfig(t, "web", hrOCIChart(nil))
	objs, err := cfg.Generate(stack.NewApplication("x", "demo", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("Generate emitted %d objects, want the HelmRelease alone", len(objs))
	}
}
