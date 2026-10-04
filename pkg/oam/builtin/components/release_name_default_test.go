package components_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// releaseNameOf lowers a helm component named name with delivery and extra
// properties, hands the one release it emits to its terminal in application
// namespace "demo", under fluxNS when non-empty, and returns the name the
// chart is released under: spec.releaseName of the HelmRelease under delivery:
// flux, which is also what Flux's own GetReleaseName then returns, and the
// render's .Release.Name under delivery: template.
func releaseNameOf(t *testing.T, delivery, fluxNS, name string, extra map[string]any) string {
	t.Helper()
	props := map[string]any{"delivery": delivery, "chart": "podinfo", "source": map[string]any{"url": "https://charts.example.com"}}
	maps.Copy(props, extra)
	comps := lowerHelm(t, helmLowering("shop"), name, props)
	switch delivery {
	case "flux":
		release := componentByType(t, comps, "helmrelease")
		cfg, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(&release, "demo")
		if err != nil {
			t.Fatalf("helmrelease ToApplicationConfig: %v", err)
		}
		hr := hrGenerate(t, cfg, fluxNS)
		if got := hr.GetReleaseName(); got != hr.Spec.ReleaseName {
			t.Errorf("Flux reads the release name %q, spec.releaseName is %q", got, hr.Spec.ReleaseName)
		}
		return hr.Spec.ReleaseName
	case "template":
		release := componentByType(t, comps, "helmtemplate")
		cfg, err := (&components.HelmTemplateHandler{}).ToApplicationConfig(&release, "demo")
		if err != nil {
			t.Fatalf("helmtemplate ToApplicationConfig: %v", err)
		}
		// A Flux namespace moves no rendered object, so the terminal takes none.
		if _, ok := cfg.(interface{ SetFluxNamespace(string) }); ok {
			t.Fatalf("helmtemplate takes a Flux namespace; this test must pass it %q", fluxNS)
		}
		return cfg.(*components.HelmTemplateConfig).ReleaseName
	}
	t.Fatalf("unknown delivery %q", delivery)
	return ""
}

// TestReleaseNameDefault is the acceptance test of go-kure/launcher#785, over
// delivery, Flux namespace and authored name: a chart is released under the
// component's name under either delivery, with and without a Flux namespace;
// an authored releaseName is honoured under both; and a 60-character component
// name is shortened to the same name under both, as Flux shortens a release
// name. The shortened name is a fixed string computed outside the code under
// test (the first 12 hex digits of `printf %s <name> | sha256sum`).
func TestReleaseNameDefault(t *testing.T) {
	const (
		longName      = "checkout-service-payment-gateway-adapter-for-the-eu-region-1"
		longShortened = "checkout-service-payment-gateway-adapter-b56668f5fa8b"
	)
	if len(longName) != 60 || len(longShortened) != 53 {
		t.Fatalf("fixture lengths %d and %d, want 60 and 53", len(longName), len(longShortened))
	}
	for _, component := range []struct{ name, want string }{
		{"web", "web"},
		{longName, longShortened},
	} {
		for _, authored := range []string{"", "shop-podinfo"} {
			want := component.want
			var extra map[string]any
			if authored != "" {
				want, extra = authored, map[string]any{"releaseName": authored}
			}
			for _, delivery := range []string{"flux", "template"} {
				for _, fluxNS := range []string{"", "flux-system"} {
					name := fmt.Sprintf("%d-character name, releaseName %q, delivery %s, Flux namespace %q", len(component.name), authored, delivery, fluxNS)
					t.Run(name, func(t *testing.T) {
						if got := releaseNameOf(t, delivery, fluxNS, component.name, extra); got != want {
							t.Errorf("release name = %q, want %q", got, want)
						}
					})
				}
			}
		}
	}
}

// TestReleaseNameDefault_TargetNamespace: the default is the component's name
// whatever spec.targetNamespace is, authored or defaulted under a Flux
// namespace. Flux alone would name the release "<targetNamespace>-<name>"
// there, which is what the HelmRelease carried no releaseName for before
// go-kure/launcher#785.
func TestReleaseNameDefault_TargetNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, fluxNS string
		extra        map[string]any
		wantTarget   string
	}{
		{"defaulted under a Flux namespace", "flux-system", nil, "demo"},
		{"authored, no Flux namespace", "", map[string]any{"targetNamespace": "elsewhere"}, "elsewhere"},
		{"authored under a Flux namespace", "flux-system", map[string]any{"targetNamespace": "elsewhere"}, "elsewhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"chart": hrChart()}
			maps.Copy(props, tc.extra)
			hr := hrGenerate(t, hrConfig(t, "web", props), tc.fluxNS)
			if hr.Spec.TargetNamespace != tc.wantTarget {
				t.Fatalf("targetNamespace = %q, want %q", hr.Spec.TargetNamespace, tc.wantTarget)
			}
			if got := hr.GetReleaseName(); got != "web" {
				t.Errorf("release name = %q, want web", got)
			}
		})
	}
}

// TestHelmReleaseHandler_ReleaseNameDefault: the helmrelease terminal writes
// the default on a component authored directly, leaves an authored releaseName
// as it is (its limits are the HelmRelease CRD's to check), and refuses a
// default Helm would not install under, at parse time and again in Generate
// for a config built directly, with the remedy the helmtemplate terminal
// names. A config built directly with neither a Name nor a releaseName has
// nothing to derive a default from and is refused.
func TestHelmReleaseHandler_ReleaseNameDefault(t *testing.T) {
	if hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart()}), ""); hr.Spec.ReleaseName != "web" {
		t.Errorf("default spec.releaseName = %q, want web", hr.Spec.ReleaseName)
	}
	authored := strings.Repeat("a", 54)
	if hr := hrGenerate(t, hrConfig(t, "web", map[string]any{"chart": hrChart(), "releaseName": authored}), ""); hr.Spec.ReleaseName != authored {
		t.Errorf("authored spec.releaseName = %q, want it unchanged", hr.Spec.ReleaseName)
	}
	if hr := hrGenerate(t, hrConfig(t, htDashAtCut, map[string]any{"chart": hrChart()}), ""); hr.Spec.ReleaseName != strings.Repeat("a", 39)+"--223f6f9789ce" {
		t.Errorf("default cut at a dash = %q", hr.Spec.ReleaseName)
	}

	wantErr := `helmrelease: the default release name "` + strings.Repeat("a", 39) + `.-b46d196cb11f", derived from the component name "` + htDotAtCut +
		`" as Flux derives a HelmRelease's, must be a DNS-1123 subdomain of at most 53 characters, as a Helm release name is; set releaseName`
	_, err := (&components.HelmReleaseHandler{}).ToApplicationConfig(
		&oam.Component{Name: htDotAtCut, Type: "helmrelease", Properties: map[string]any{"chart": hrChart()}}, "demo")
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("parse error = %v, want one containing %q", err, wantErr)
	}
	direct := hrConfig(t, "web", map[string]any{"chart": hrChart()}).(*components.HelmReleaseConfig)
	direct.Name = htDotAtCut
	if _, err := direct.Generate(nil); err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Errorf("Generate error = %v, want one containing %q", err, wantErr)
	}
	// The remedy works.
	hrGenerate(t, hrConfig(t, htDotAtCut, map[string]any{"chart": hrChart(), "releaseName": "web"}), "")

	direct.Name = ""
	wantErr = "helmrelease: no release name: releaseName is unset and the component has no name to derive it from; set ReleaseName or Name"
	if _, err := direct.Generate(nil); err == nil || err.Error() != wantErr {
		t.Errorf("Generate without a Name: error = %v, want %q", err, wantErr)
	}
	direct.Spec.ReleaseName = "web"
	if got := hrGenerate(t, direct, "").Spec.ReleaseName; got != "web" {
		t.Errorf("spec.releaseName without a Name = %q, want the authored web", got)
	}
}
