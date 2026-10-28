package components_test

import (
	"strings"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// helm-controller labels the source it generates for a HelmRelease's chart
// template with the HelmRelease's name (helm.toolkit.fluxcd.io/name, a label
// value of at most 63 characters) and names it `<namespace>-<name>` after the
// HelmRelease (at most 253). A HelmRelease that breaks either is refused at
// generation (go-kure/launcher#1080); a chartRef generates no source and is
// not held.

// hrChartSourceDocs are a helmrelease document per chart template kind.
var hrChartSourceDocs = map[string]func() map[string]any{
	sourcev1.HelmChartKind:     func() map[string]any { return map[string]any{"chart": hrChart()} },
	sourcev1.OCIRepositoryKind: func() map[string]any { return hrOCIChart(nil) },
}

// hrChartSourceTemplate is a chart template of kind, as a config built
// directly carries it, its HelmChart's sourceRef in sourceNamespace.
func hrChartSourceTemplate(kind, sourceNamespace string) *helmv2.HelmChartTemplate {
	if kind == sourcev1.OCIRepositoryKind {
		return &helmv2.HelmChartTemplate{Kind: kind, Spec: helmv2.HelmChartTemplateSpec{
			OCIRepositorySpec: sourcev1.OCIRepositorySpec{URL: hrOCIURL},
		}}
	}
	return &helmv2.HelmChartTemplate{Spec: helmv2.HelmChartTemplateSpec{
		Chart:     "podinfo",
		SourceRef: &helmv2.CrossNamespaceObjectReference{Kind: "HelmRepository", Name: "podinfo", Namespace: sourceNamespace},
	}}
}

// TestHelmReleaseChartSource_NameIsALabelValue: a HelmRelease named 63
// characters builds under either kind and 64 is refused, through the
// component and on a config built directly, named after the component or by
// objectName. A chartRef under the same name builds.
func TestHelmReleaseChartSource_NameIsALabelValue(t *testing.T) {
	at, over := strings.Repeat("a", 63), strings.Repeat("a", 64)
	refused := func(kind, from string) string {
		return `helmrelease "` + over + `": under chart, helm-controller labels the ` + kind +
			` it generates with the HelmRelease's name (helm.toolkit.fluxcd.io/name), which as a label value must be at most 63 characters: it has 64, from ` +
			from + `; use a shorter name, or a chartRef to an authored source`
	}
	for kind, doc := range hrChartSourceDocs {
		t.Run(kind, func(t *testing.T) {
			hr := hrGenerate(t, hrConfig(t, at, doc()), "")
			if hr.Name != at {
				t.Fatalf("HelmRelease name = %q, want %q", hr.Name, at)
			}
			cfg := hrConfig(t, over, doc())
			if _, err := cfg.Generate(stack.NewApplication("x", "demo", cfg)); err == nil || err.Error() != refused(kind, "the component name") {
				t.Errorf("component: error = %v\nwant %s", err, refused(kind, "the component name"))
			}
			direct := &components.HelmReleaseConfig{Name: "web", ObjectName: over, Namespace: "demo",
				Spec: helmv2.HelmReleaseSpec{Chart: hrChartSourceTemplate(kind, "")}}
			want := refused(kind, "the object name: objectName, a helm component's helmReleaseName, or the Naming hook's answer")
			if _, err := direct.Generate(nil); err == nil || err.Error() != want {
				t.Errorf("direct objectName: error = %v\nwant %s", err, want)
			}
			direct = &components.HelmReleaseConfig{Name: at, Namespace: "demo",
				Spec: helmv2.HelmReleaseSpec{Chart: hrChartSourceTemplate(kind, "")}}
			if _, err := direct.Generate(nil); err != nil {
				t.Errorf("direct at the limit: %v", err)
			}
		})
	}
	ref := hrGenerate(t, hrConfig(t, over, map[string]any{
		"chartRef": map[string]any{"kind": "OCIRepository", "name": "podinfo"},
	}), "")
	if ref.Name != over {
		t.Errorf("chartRef: HelmRelease name = %q, want %q", ref.Name, over)
	}
}

// TestHelmReleaseChartSource_GeneratedNameLength: `<namespace>-<name>` at 253
// characters builds and 254 is refused, the namespace being the HelmRelease's
// after the move to the Flux namespace. A HelmChart's sourceRef namespace
// moves the source, never its name, so it changes neither outcome. A
// component's namespace is a DNS label, so only a config built directly can
// reach the limit.
func TestHelmReleaseChartSource_GeneratedNameLength(t *testing.T) {
	const name = "web"
	ns := func(n int) string { return strings.Repeat("n", n-len(name)-1) }
	for kind := range hrChartSourceDocs {
		for _, sourceNS := range []string{"", "flux-sources"} {
			if kind == sourcev1.OCIRepositoryKind && sourceNS != "" {
				continue
			}
			for _, fluxNS := range []bool{false, true} {
				gen := func(n int) error {
					cfg := &components.HelmReleaseConfig{Name: name, Namespace: ns(n),
						Spec: helmv2.HelmReleaseSpec{Chart: hrChartSourceTemplate(kind, sourceNS)}}
					if fluxNS {
						cfg.Namespace = "demo"
						cfg.SetFluxNamespace(ns(n))
					}
					_, err := cfg.Generate(nil)
					return err
				}
				t.Run(kind+"/sourceRef.namespace="+sourceNS+"/fluxNamespace="+map[bool]string{false: "unset", true: "set"}[fluxNS], func(t *testing.T) {
					if err := gen(253); err != nil {
						t.Errorf("253: %v", err)
					}
					want := `helmrelease "web": under chart, helm-controller names the ` + kind + ` it generates "` + ns(254) + `-web" (<namespace>-<name>), which must be at most 253 characters: it has 254; use a shorter name, or a chartRef to an authored source`
					if err := gen(254); err == nil || err.Error() != want {
						t.Errorf("254: error = %v\nwant %s", err, want)
					}
				})
			}
		}
	}
}
