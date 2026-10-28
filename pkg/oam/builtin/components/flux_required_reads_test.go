package components_test

import (
	"slices"
	"testing"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	kustv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// requiredReader is pkg/oam.fluxRequiredReader.
type requiredReader interface {
	FluxRequiredReads() (configMaps, secrets []string)
}

// requiredReadsConfig is the config the handler of typ builds from props, in namespace
// "ns".
func requiredReadsConfig(t *testing.T, h oam.ComponentHandler, typ string, props map[string]any) requiredReader {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "c", Type: typ, Properties: props}, "ns")
	if err != nil {
		t.Fatalf("%s: ToApplicationConfig: %v", typ, err)
	}
	r, ok := cfg.(requiredReader)
	if !ok {
		t.Fatalf("%s: %T does not report FluxRequiredReads", typ, cfg)
	}
	return r
}

// TestFluxRequiredReads: each Flux config reports what of its reads its
// object cannot become Ready without (go-kure/launcher#1079): none when
// suspended; a HelmRelease's valuesFrom not marked optional, its kubeConfig
// and what its chart template's generated object requires, an OCIRepository
// by the ocirepository rule and a HelmChart in the release's namespace its
// verification Secret; a Kustomization's decryption and kubeConfig, never its
// substituteFrom; a HelmRepository's Secrets, none for type oci; an
// OCIRepository's FluxSourceRequiredSecrets; and the generic kinds' Secret
// references.
func TestFluxRequiredReads(t *testing.T) {
	ref := func(name string) *meta.LocalObjectReference { return &meta.LocalObjectReference{Name: name} }
	values := []helmv2.ValuesReference{
		{Kind: "ConfigMap", Name: "vals"},
		{Kind: "Secret", Name: "creds"},
		{Kind: "ConfigMap", Name: "maybe", Optional: true},
	}
	kubeConfig := &meta.KubeConfigReference{SecretRef: &meta.SecretKeyReference{Name: "kube"}}
	ociChart := &helmv2.HelmChartTemplate{Kind: sourcev1.OCIRepositoryKind, Spec: helmv2.HelmChartTemplateSpec{OCIRepositorySpec: ociVerifiedSpec("notation")}}
	verified := helmv2.HelmChartTemplateSpec{OCIRepositorySpec: sourcev1.OCIRepositorySpec{Verify: &sourcev1.OCIRepositoryVerification{Provider: "cosign", SecretRef: ref("keys")}}}
	helmChart := &helmv2.HelmChartTemplate{Spec: verified}
	elsewhere := verified
	elsewhere.SourceRef = &helmv2.CrossNamespaceObjectReference{Kind: sourcev1.HelmRepositoryKind, Name: "repo", Namespace: "other"}
	helmChartElsewhere := &helmv2.HelmChartTemplate{Spec: elsewhere}

	for _, tc := range []struct {
		name                string
		cfg                 requiredReader
		configMaps, secrets []string
	}{
		{"helmrelease", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{ValuesFrom: values, KubeConfig: kubeConfig}}, []string{"vals"}, []string{"creds", "kube"}},
		{"suspended helmrelease", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{ValuesFrom: values, KubeConfig: kubeConfig, Suspend: true}}, nil, nil},
		{"helmrelease, OCI chart template", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: ociChart}}, nil, []string{"pull", "tls", "proxy", "keys"}},
		{"helmrelease, HelmChart template", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: helmChart}}, nil, []string{"keys"}},
		{"helmrelease, HelmChart template in another namespace", &components.HelmReleaseConfig{Namespace: "ns", Spec: helmv2.HelmReleaseSpec{Chart: helmChartElsewhere}}, nil, nil},
		{"fluxcd-kustomization", &components.FluxcdKustomizationConfig{Spec: kustv1.KustomizationSpec{
			Decryption: &kustv1.Decryption{Provider: "sops", SecretRef: ref("sops")},
			KubeConfig: &meta.KubeConfigReference{ConfigMapRef: ref("kube")},
			PostBuild:  &kustv1.PostBuild{SubstituteFrom: []kustv1.SubstituteReference{{Kind: "ConfigMap", Name: "vars"}, {Kind: "Secret", Name: "svars"}}},
		}}, []string{"kube"}, []string{"sops"}},
		{"suspended fluxcd-kustomization", &components.FluxcdKustomizationConfig{Spec: kustv1.KustomizationSpec{Decryption: &kustv1.Decryption{SecretRef: ref("sops")}, Suspend: true}}, nil, nil},
		{"helmrepository", &components.HelmRepositoryConfig{Spec: sourcev1.HelmRepositorySpec{SecretRef: ref("auth"), CertSecretRef: ref("tls")}}, nil, []string{"auth", "tls"}},
		{"helmrepository of type oci", &components.HelmRepositoryConfig{Spec: sourcev1.HelmRepositorySpec{Type: sourcev1.HelmRepositoryTypeOCI, SecretRef: ref("auth")}}, nil, nil},
		{"suspended helmrepository", &components.HelmRepositoryConfig{Spec: sourcev1.HelmRepositorySpec{SecretRef: ref("auth"), Suspend: true}}, nil, nil},
		{"ocirepository", &components.OCIRepositoryConfig{Spec: ociVerifiedSpec("notation")}, nil, []string{"pull", "tls", "proxy", "keys"}},
		{"suspended ocirepository", &components.OCIRepositoryConfig{Spec: func() sourcev1.OCIRepositorySpec { s := ociVerifiedSpec(""); s.Suspend = true; return s }()}, nil, nil},
		{"gitrepository", &components.GitRepositoryConfig{Spec: sourcev1.GitRepositorySpec{SecretRef: ref("git")}}, nil, []string{"git"}},
		{"suspended gitrepository", &components.GitRepositoryConfig{Spec: sourcev1.GitRepositorySpec{SecretRef: ref("git"), Suspend: true}}, nil, nil},
		{"bucket", &components.BucketConfig{Spec: sourcev1.BucketSpec{SecretRef: ref("s3")}}, nil, []string{"s3"}},
		{"suspended bucket", &components.BucketConfig{Spec: sourcev1.BucketSpec{SecretRef: ref("s3"), Suspend: true}}, nil, nil},
		{"helmchart", &components.HelmChartConfig{Spec: sourcev1.HelmChartSpec{Verify: &sourcev1.HelmChartVerification{Provider: "cosign", SecretRef: ref("keys")}}}, nil, []string{"keys"}},
		{"suspended helmchart", &components.HelmChartConfig{Spec: sourcev1.HelmChartSpec{Verify: &sourcev1.HelmChartVerification{Provider: "cosign", SecretRef: ref("keys")}, Suspend: true}}, nil, nil},
		{"fluxcd-receiver", requiredReadsConfig(t, &components.FluxcdReceiverHandler{}, "fluxcd-receiver", withProperty(fluxReceiverFull(), "suspend", false)), nil, []string{"webhook-token"}},
		{"suspended fluxcd-receiver", requiredReadsConfig(t, &components.FluxcdReceiverHandler{}, "fluxcd-receiver", fluxReceiverFull()), nil, nil},
		{"imagerepository", requiredReadsConfig(t, &components.ImageRepositoryHandler{}, "imagerepository", withProperty(imageRepositoryFull(), "suspend", false)), nil, []string{"registry-credentials", "registry-proxy", "registry-tls"}},
		{"suspended imagerepository", requiredReadsConfig(t, &components.ImageRepositoryHandler{}, "imagerepository", imageRepositoryFull()), nil, nil},
		{"imageupdateautomation", requiredReadsConfig(t, &components.ImageUpdateAutomationHandler{}, "imageupdateautomation", withProperty(imageUpdateAutomationFull(), "suspend", false)), nil, []string{"signing-key"}},
		{"suspended imageupdateautomation", requiredReadsConfig(t, &components.ImageUpdateAutomationHandler{}, "imageupdateautomation", imageUpdateAutomationFull()), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configMaps, secrets := tc.cfg.FluxRequiredReads()
			if !slices.Equal(configMaps, tc.configMaps) || !slices.Equal(secrets, tc.secrets) {
				t.Errorf("FluxRequiredReads = %v, %v, want %v, %v", configMaps, secrets, tc.configMaps, tc.secrets)
			}
		})
	}
}

// TestFluxRequiredReads_NoneForConditionalKinds: a resourcesetinputprovider,
// an Alert and a Provider require nothing, whatever they read: their reads
// are conditional, or a wait cannot hold on them (go-kure/launcher#1079).
func TestFluxRequiredReads_NoneForConditionalKinds(t *testing.T) {
	for _, tc := range []struct {
		typ   string
		h     oam.ComponentHandler
		props map[string]any
	}{
		{"resourcesetinputprovider", &components.ResourceSetInputProviderHandler{}, resourceSetInputProviderFull()},
		{"fluxcd-alert", &components.FluxcdAlertHandler{}, fluxAlertFull()},
		{"fluxcd-provider", &components.FluxcdProviderHandler{}, fluxProviderFull()},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			configMaps, secrets := requiredReadsConfig(t, tc.h, tc.typ, tc.props).FluxRequiredReads()
			if len(configMaps) != 0 || len(secrets) != 0 {
				t.Errorf("FluxRequiredReads = %v, %v, want none", configMaps, secrets)
			}
		})
	}
}
