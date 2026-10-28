package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestHelmReleaseChartSource_NameLengthBuild: from app.yaml, a helmrelease
// with a chart template named 63 characters builds and 64 is refused, since
// helm-controller labels the source it generates with the HelmRelease's name
// (go-kure/launcher#1080), with and without a Flux namespace. A chartRef under
// the same name builds. A helm component with a HelmRepository source delivers
// through such a HelmRelease, so its name is held too, and a shorter
// helmReleaseName lifts the refusal.
func TestHelmReleaseChartSource_NameLengthBuild(t *testing.T) {
	at, over := strings.Repeat("a", 63), strings.Repeat("a", 64)
	chart := map[string]any{"chart": map[string]any{"spec": map[string]any{
		"chart": "app", "sourceRef": map[string]any{"kind": "HelmRepository", "name": "example"},
	}}}
	const refused = `under chart, helm-controller labels the HelmChart it generates with the HelmRelease's name (helm.toolkit.fluxcd.io/name), which as a label value must be at most 63 characters: it has 64`
	for _, fluxNS := range []string{"", fluxNSTarget} {
		for _, tc := range []struct {
			name string
			comp oam.Component
			want string // a substring of the error; "" means it builds
		}{
			{"63", fluxNSComponent(t, at, "helmrelease", chart), ""},
			{"64", fluxNSComponent(t, over, "helmrelease", chart), refused},
			{"64 under chartRef", oam.Component{Name: over, Type: "helmrelease", Properties: map[string]any{
				"chartRef": map[string]any{"kind": "OCIRepository", "name": "example"},
			}}, ""},
			{"helm 63", fluxNSComponent(t, at, "helm", nil), ""},
			{"helm 64", fluxNSComponent(t, over, "helm", nil), refused + ", from the component name"},
			{"helm 64 with a shorter helmReleaseName", fluxNSComponent(t, over, "helm", map[string]any{"helmReleaseName": "web"}), ""},
		} {
			t.Run("fluxNamespace="+fluxNS+"/"+tc.name, func(t *testing.T) {
				_, err := ociBuild(t, fluxNS, nil, tc.comp)
				switch {
				case tc.want == "" && err != nil:
					t.Fatalf("build: %v", err)
				case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
					t.Fatalf("build error = %v\nwant one containing %s", err, tc.want)
				}
			})
		}
	}
}
