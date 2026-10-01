package components_test

import (
	"maps"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// hcNativeConfig parses a delivery: native helmchart in namespace demo that
// references an existing source, so it emits only its HelmRelease.
func hcNativeConfig(t *testing.T, name string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	p := map[string]any{
		"chart":  "podinfo",
		"source": map[string]any{"kind": "HelmRepository", "name": "podinfo"},
	}
	maps.Copy(p, props)
	cfg, err := (&components.HelmchartHandler{}).ToApplicationConfig(
		&oam.Component{Name: name, Type: "helmchart", Properties: p}, "demo")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

// TestHelmchartNative_TargetNamespaceUnderFluxNamespace
// (go-kure/launcher#610): with a Flux namespace and no authored
// targetNamespace, the release installs into the application namespace, not
// the Flux namespace the HelmRelease sits in; an authored value wins; and
// without a Flux namespace targetNamespace stays unset.
func TestHelmchartNative_TargetNamespaceUnderFluxNamespace(t *testing.T) {
	tests := []struct {
		name            string
		props           map[string]any
		fluxNS          string
		wantTarget      string
		wantReleaseNS   string
		wantReleaseName string
	}{
		{name: "flux namespace, unset", fluxNS: "flux-system", wantTarget: "demo", wantReleaseNS: "demo", wantReleaseName: "demo-web"},
		{name: "flux namespace, authored", fluxNS: "flux-system", props: map[string]any{"targetNamespace": "elsewhere"}, wantTarget: "elsewhere", wantReleaseNS: "elsewhere", wantReleaseName: "elsewhere-web"},
		{name: "flux namespace, releaseName authored", fluxNS: "flux-system", props: map[string]any{"releaseName": "web"}, wantTarget: "demo", wantReleaseNS: "demo", wantReleaseName: "web"},
		{name: "no flux namespace", wantTarget: "", wantReleaseNS: "demo", wantReleaseName: "web"},
		{name: "no flux namespace, authored", props: map[string]any{"targetNamespace": "elsewhere"}, wantTarget: "elsewhere", wantReleaseNS: "elsewhere", wantReleaseName: "elsewhere-web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hr, _ := hrGenerate(t, hcNativeConfig(t, "web", tt.props), tt.fluxNS)
			wantHRNS := "demo"
			if tt.fluxNS != "" {
				wantHRNS = tt.fluxNS
			}
			if hr.Namespace != wantHRNS {
				t.Errorf("HelmRelease namespace = %q, want %q", hr.Namespace, wantHRNS)
			}
			if hr.Spec.TargetNamespace != tt.wantTarget {
				t.Errorf("targetNamespace = %q, want %q", hr.Spec.TargetNamespace, tt.wantTarget)
			}
			if got := hr.GetReleaseNamespace(); got != tt.wantReleaseNS {
				t.Errorf("release namespace = %q, want %q", got, tt.wantReleaseNS)
			}
			if got := hr.GetReleaseName(); got != tt.wantReleaseName {
				t.Errorf("release name = %q, want %q", got, tt.wantReleaseName)
			}
		})
	}
}

// TestHelmchartNative_ReleaseIdentityMatchesHelmRelease: the same chart
// installs under the same release namespace and name whichever component type
// authored it, with and without a Flux namespace and an authored
// targetNamespace.
func TestHelmchartNative_ReleaseIdentityMatchesHelmRelease(t *testing.T) {
	for _, fluxNS := range []string{"", "flux-system"} {
		for _, target := range []string{"", "elsewhere"} {
			hcProps, hrProps := map[string]any{}, map[string]any{"chart": hrChart()}
			if target != "" {
				hcProps["targetNamespace"] = target
				hrProps["targetNamespace"] = target
			}
			hc, _ := hrGenerate(t, hcNativeConfig(t, "web", hcProps), fluxNS)
			hr, _ := hrGenerate(t, hrConfig(t, "web", hrProps), fluxNS)
			if hc.Namespace != hr.Namespace || hc.GetReleaseNamespace() != hr.GetReleaseNamespace() || hc.GetReleaseName() != hr.GetReleaseName() {
				t.Errorf("fluxNS %q, targetNamespace %q: helmchart %s in %s releases %s/%s; helmrelease %s in %s releases %s/%s",
					fluxNS, target,
					hc.Name, hc.Namespace, hc.GetReleaseNamespace(), hc.GetReleaseName(),
					hr.Name, hr.Namespace, hr.GetReleaseNamespace(), hr.GetReleaseName())
			}
		}
	}
}
