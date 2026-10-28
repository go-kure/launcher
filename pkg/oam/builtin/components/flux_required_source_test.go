package components_test

import (
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// requiredSourceReader is pkg/oam.fluxRequiredSourceReader.
type requiredSourceReader interface {
	FluxRequiredSource() (field, kind, name, namespace string)
}

// requiredSource is what cfg reports as the source it cannot become Ready
// without, and whether it reports one at all.
func requiredSource(cfg stack.ApplicationConfig) (source [4]string, reports bool) {
	r, ok := cfg.(requiredSourceReader)
	if !ok {
		return source, false
	}
	field, kind, name, namespace := r.FluxRequiredSource()
	return [4]string{field, kind, name, namespace}, true
}

// sourceConfig is the config the handler of typ builds from props, in
// namespace "ns".
func sourceConfig(t *testing.T, h oam.ComponentHandler, typ string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "c", Type: typ, Properties: props}, "ns")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig: %v", typ, err)
	}
	return cfg
}

// TestFluxRequiredSource: each Flux config whose object names the source it
// cannot become Ready without reports it (go-kure/launcher#1088): the field
// that names it, its kind, its name and the namespace the reference writes. A
// HelmRelease's chartRef, or the sourceRef of its chart template of kind
// HelmChart (written or left empty), none for kind OCIRepository; a
// Kustomization's, a HelmChart's (always in its own namespace) and an
// ImageUpdateAutomation's sourceRef. None when suspended.
func TestFluxRequiredSource(t *testing.T) {
	chartRef := &helmv2.CrossNamespaceSourceReference{Kind: sourcev1.OCIRepositoryKind, Name: "chart"}
	chartRefElsewhere := &helmv2.CrossNamespaceSourceReference{Kind: sourcev1.HelmChartKind, Name: "chart", Namespace: "other"}
	template := func(kind, namespace string) *helmv2.HelmChartTemplate {
		return &helmv2.HelmChartTemplate{Kind: kind, Spec: helmv2.HelmChartTemplateSpec{
			Chart:     "app",
			SourceRef: &helmv2.CrossNamespaceObjectReference{Kind: sourcev1.HelmRepositoryKind, Name: "repo", Namespace: namespace},
		}}
	}
	ociChart := &helmv2.HelmChartTemplate{Kind: sourcev1.OCIRepositoryKind, Spec: helmv2.HelmChartTemplateSpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{URL: "oci://example.com/app"}}}
	kustomization := func(namespace string, suspend bool) *components.FluxcdKustomizationConfig {
		return &components.FluxcdKustomizationConfig{Spec: kustv1.KustomizationSpec{
			SourceRef: kustv1.CrossNamespaceSourceReference{Kind: sourcev1.GitRepositoryKind, Name: "fleet", Namespace: namespace},
			Suspend:   suspend,
		}}
	}
	helmChart := func(suspend bool) *components.HelmChartConfig {
		return &components.HelmChartConfig{Spec: sourcev1.HelmChartSpec{
			Chart:     "app",
			SourceRef: sourcev1.LocalHelmChartSourceReference{Kind: sourcev1.HelmRepositoryKind, Name: "repo"},
			Suspend:   suspend,
		}}
	}
	automation := &components.ImageUpdateAutomationHandler{}
	for _, tc := range []struct {
		name string
		cfg  stack.ApplicationConfig
		want [4]string
	}{
		{"helmrelease chartRef", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef}}, [4]string{"chartRef", "OCIRepository", "chart", ""}},
		{"helmrelease chartRef in another namespace", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRefElsewhere}}, [4]string{"chartRef", "HelmChart", "chart", "other"}},
		{"suspended helmrelease chartRef", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{ChartRef: chartRef, Suspend: true}}, [4]string{}},
		{"helmrelease chart template", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: template("", "")}}, [4]string{"chart.spec.sourceRef", "HelmRepository", "repo", ""}},
		{"helmrelease chart template of kind HelmChart in another namespace", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: template(sourcev1.HelmChartKind, "other")}}, [4]string{"chart.spec.sourceRef", "HelmRepository", "repo", "other"}},
		{"suspended helmrelease chart template", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: template("", ""), Suspend: true}}, [4]string{}},
		{"helmrelease chart template of kind OCIRepository", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: ociChart}}, [4]string{}},
		{"fluxcd-kustomization", kustomization("", false), [4]string{"sourceRef", "GitRepository", "fleet", ""}},
		{"fluxcd-kustomization in another namespace", kustomization("other", false), [4]string{"sourceRef", "GitRepository", "fleet", "other"}},
		{"suspended fluxcd-kustomization", kustomization("", true), [4]string{}},
		{"helmchart", helmChart(false), [4]string{"sourceRef", "HelmRepository", "repo", ""}},
		{"suspended helmchart", helmChart(true), [4]string{}},
		{"imageupdateautomation", sourceConfig(t, automation, "imageupdateautomation", withProperty(imageUpdateAutomationFull(), "suspend", false)), [4]string{"sourceRef", "GitRepository", "fleet", "platform"}},
		{"suspended imageupdateautomation", sourceConfig(t, automation, "imageupdateautomation", imageUpdateAutomationFull()), [4]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := requiredSource(tc.cfg)
			if !ok {
				t.Fatalf("%T does not report FluxRequiredSource", tc.cfg)
			}
			if got != tc.want {
				t.Errorf("FluxRequiredSource = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFluxRequiredSource_NoneForOtherFluxKinds: no other Flux config reports a
// source, not suspended: the sources name none, and the other kinds' references
// are not to a source a wait holds on (go-kure/launcher#1088).
func TestFluxRequiredSource_NoneForOtherFluxKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  stack.ApplicationConfig
	}{
		{"helmrepository", &components.HelmRepositoryConfig{}},
		{"ocirepository", &components.OCIRepositoryConfig{}},
		{"gitrepository", &components.GitRepositoryConfig{}},
		{"bucket", &components.BucketConfig{}},
		{"fluxcd-alert", sourceConfig(t, &components.FluxcdAlertHandler{}, "fluxcd-alert", withProperty(fluxAlertFull(), "suspend", false))},
		{"fluxcd-provider", sourceConfig(t, &components.FluxcdProviderHandler{}, "fluxcd-provider", withProperty(fluxProviderFull(), "suspend", false))},
		{"fluxcd-receiver", sourceConfig(t, &components.FluxcdReceiverHandler{}, "fluxcd-receiver", withProperty(fluxReceiverFull(), "suspend", false))},
		{"imagepolicy", sourceConfig(t, &components.ImagePolicyHandler{}, "imagepolicy", withProperty(imagePolicyFull(), "suspend", false))},
		{"imagerepository", sourceConfig(t, &components.ImageRepositoryHandler{}, "imagerepository", withProperty(imageRepositoryFull(), "suspend", false))},
		{"artifactgenerator", sourceConfig(t, &components.ArtifactGeneratorHandler{}, "artifactgenerator", artifactGeneratorFull())},
		{"resourcesetinputprovider", sourceConfig(t, &components.ResourceSetInputProviderHandler{}, "resourcesetinputprovider", resourceSetInputProviderFull())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := requiredSource(tc.cfg); got != ([4]string{}) {
				t.Errorf("FluxRequiredSource = %q, want none", got)
			}
		})
	}
}
