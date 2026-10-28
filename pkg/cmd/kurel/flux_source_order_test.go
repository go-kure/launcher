package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestFluxNamespace_FluxObjectLateSourceComponent: with groups waiting for
// their Flux objects, a Flux component whose object names the source it cannot
// become Ready without, which a component of a later group makes, is refused:
// its group waits for it, it for the source, and the source's group for its
// own (go-kure/launcher#1088). A HelmRelease's chartRef, to an OCIRepository
// or a HelmChart, and the sourceRef of its chart template; a Kustomization's,
// a HelmChart's and an ImageUpdateAutomation's sourceRef; and the HelmRelease
// a helm component lowers to for a source it names, which it leaves the author
// to order. The waiter and the source land in one namespace under every Flux
// namespace, so each is refused under all three; a reference to another
// namespace is admitted. Under the default delivery every document is
// admitted.
func TestFluxNamespace_FluxObjectLateSourceComponent(t *testing.T) {
	chartRef := func(kind string) oam.Component {
		return oam.Component{Name: "w", Type: "helmrelease", Properties: map[string]any{
			"chartRef": map[string]any{"kind": kind, "name": "src"},
		}}
	}
	sourceRef := func(typ, kind, namespace string) oam.Component {
		ref := map[string]any{"kind": kind, "name": "src"}
		if namespace != "" {
			ref["namespace"] = namespace
		}
		return fluxNSComponent(t, "w", typ, map[string]any{"sourceRef": ref})
	}
	helm := oam.Component{Name: "w", Type: "helm", Properties: map[string]any{
		"chart": "app", "source": map[string]any{"name": "src", "kind": "HelmRepository"},
	}}
	for _, tc := range []struct {
		name       string
		waiter     oam.Component
		source     string // the source component's type
		waiterKind string // the waiter's object
		sourceKind string // the source's object
		refused    string // the refusal's waiter and source; "" when admitted
	}{
		{"a helmrelease's chartRef to an ocirepository", chartRef("OCIRepository"), "ocirepository", "HelmRelease", "OCIRepository",
			`helmrelease "w" requires OCIRepository "src" (chartRef), the object of component "src"`},
		{"a helmrelease's chartRef to a helmchart", chartRef("HelmChart"), "helmchart", "HelmRelease", "HelmChart",
			`helmrelease "w" requires HelmChart "src" (chartRef), the object of component "src"`},
		{"a helmrelease's chart template sourceRef to a gitrepository", fluxNSComponent(t, "w", "helmrelease", map[string]any{
			"chart": map[string]any{"spec": map[string]any{"chart": "./charts/app", "sourceRef": map[string]any{"kind": "GitRepository", "name": "src"}}},
		}), "gitrepository", "HelmRelease", "GitRepository",
			`helmrelease "w" requires GitRepository "src" (chart.spec.sourceRef), the object of component "src"`},
		{"a fluxcd-kustomization's sourceRef to a gitrepository", sourceRef("fluxcd-kustomization", "GitRepository", ""), "gitrepository", "Kustomization", "GitRepository",
			`fluxcd-kustomization "w" requires GitRepository "src" (sourceRef), the object of component "src"`},
		{"a helmchart's sourceRef to a helmrepository", sourceRef("helmchart", "HelmRepository", ""), "helmrepository", "HelmChart", "HelmRepository",
			`helmchart "w" requires HelmRepository "src" (sourceRef), the object of component "src"`},
		{"an imageupdateautomation's sourceRef to a gitrepository", sourceRef("imageupdateautomation", "GitRepository", ""), "gitrepository", "ImageUpdateAutomation", "GitRepository",
			`imageupdateautomation "w" requires GitRepository "src" (sourceRef), the object of component "src"`},
		{"a helm component's source naming a helmrepository", helm, "helmrepository", "HelmRelease", "HelmRepository",
			`helmrelease "w" requires HelmRepository "src" (chart.spec.sourceRef), the object of component "src"`},
		{"a fluxcd-kustomization's sourceRef to another namespace", sourceRef("fluxcd-kustomization", "GitRepository", "elsewhere"), "gitrepository", "Kustomization", "GitRepository", ""},
	} {
		for _, fluxNS := range []string{"", fluxNSTarget, "default"} {
			t.Run(tc.name+"/fluxNamespace="+fluxNS, func(t *testing.T) {
				policies := dependsOn("src", "w")
				ns := "default"
				if fluxNS != "" {
					ns = fluxNS
				}
				source := fluxNSComponent(t, "src", tc.source, nil)
				_, err := ociBuildWaiting(t, fluxNS, true, policies, source, tc.waiter)
				switch {
				case tc.refused == "" && err != nil:
					t.Fatalf("build: %v", err)
				case tc.refused != "" && err == nil:
					t.Fatal("build admitted it, want the late source refused")
				case tc.refused != "":
					for _, want := range []string{tc.refused, "never converges", `place component "src" in group`} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("build error = %v, want it to contain %s", err, want)
						}
					}
				}
				got, err := ociBuildWaiting(t, fluxNS, false, policies, source, tc.waiter)
				if err != nil {
					t.Fatalf("default delivery: build: %v", err)
				}
				requireOCIObjects(t, got, tc.waiterKind+" "+ns+"/w", tc.sourceKind+" "+ns+"/src")
			})
		}
	}
}
