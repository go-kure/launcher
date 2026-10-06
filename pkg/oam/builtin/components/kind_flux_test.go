package components_test

// The kind components of the Flux APIs beside the sources, the HelmRelease and
// the Kustomization (go-kure/launcher#790): their fixtures, and the tests of
// what they add to a policy-free kind, the Flux namespace. The kinds are rows
// of policyFreeKinds (flux), and the tests of everything else are the shared
// ones.

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
)

// fluxAlertSource is one source of events: every Kustomization of the Alert's
// namespace.
func fluxAlertSource() map[string]any {
	return map[string]any{"kind": "Kustomization", "name": "*"}
}

// fluxAlertWith is the properties of a fluxcd-alert with the provider
// reference and the sources.
func fluxAlertWith(provider map[string]any, sources ...any) map[string]any {
	return map[string]any{"providerRef": provider, "eventSources": append([]any{}, sources...)}
}

// fluxAlertMinimal is the least a fluxcd-alert may author.
func fluxAlertMinimal() map[string]any {
	return fluxAlertWith(map[string]any{"name": "slack"}, fluxAlertSource())
}

// fluxAlertFull sets every field of an AlertSpec. Its second source reaches
// into another namespace, by label.
func fluxAlertFull() map[string]any {
	return map[string]any{
		"providerRef":   map[string]any{"name": "slack"},
		"eventSeverity": "error",
		"eventSources": []any{
			map[string]any{"kind": "HelmRelease", "name": "web"},
			map[string]any{
				"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "name": "*",
				"namespace": "shop", "matchLabels": map[string]any{"team": "shop"},
			},
		},
		"inclusionList": []any{".*succeeded.*"},
		"exclusionList": []any{"^Dependencies do not meet ready condition.*"},
		"eventMetadata": map[string]any{"cluster": "east", "env": "production"},
		"summary":       "releases of the shop",
		"suspend":       true,
	}
}

// fluxKinds returns the rows of policyFreeKinds that move to the Flux
// namespace.
func fluxKinds(t *testing.T) []policyFreeKind {
	t.Helper()
	var kinds []policyFreeKind
	for _, kind := range policyFreeKinds {
		if kind.flux {
			kinds = append(kinds, kind)
		}
	}
	// Vacuity guard: the fluxcd-alert kind is one.
	if !slices.ContainsFunc(kinds, func(k policyFreeKind) bool { return k.component == "fluxcd-alert" }) {
		t.Fatalf("policyFreeKinds marks no fluxcd-alert as a Flux kind; it marks %d kinds", len(kinds))
	}
	return kinds
}

// fluxKindTransform transforms a document of the given components, all of one
// kind, for the build namespace "demo" and the Flux namespace fluxNS ("" for
// none), and returns every generated object.
func fluxKindTransform(kind policyFreeKind, fluxNS string, components ...oam.Component) ([]client.Object, error) {
	for i := range components {
		components[i].Type = kind.component
	}
	app := &oam.Application{Metadata: oam.Metadata{Name: "shop"}, Spec: oam.ApplicationSpec{Components: components}}
	tr := oam.NewTransformer(map[string]oam.ComponentHandler{kind.component: kind.handler}, nil)
	cluster, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", FluxNamespace: fluxNS})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	var out []client.Object
	for _, a := range apps {
		for _, o := range a.Objects {
			out = append(out, *o)
		}
	}
	return out, nil
}

// TestFluxKinds_LandInTheFluxNamespace: through the transform, each kind's
// object lands in the Flux namespace when one is set and in the build
// namespace when none is, and but for its namespace it is the same object.
func TestFluxKinds_LandInTheFluxNamespace(t *testing.T) {
	for _, kind := range fluxKinds(t) {
		t.Run(kind.component, func(t *testing.T) {
			build := func(fluxNS string) client.Object {
				objs, err := fluxKindTransform(kind, fluxNS, oam.Component{Name: "web", Properties: maps.Clone(kind.full)})
				if err != nil {
					t.Fatalf("transform with the Flux namespace %q: %v", fluxNS, err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects with the Flux namespace %q, want one", len(objs), fluxNS)
				}
				return objs[0]
			}
			plain, moved := build(""), build("flux-system")
			if plain.GetNamespace() != "demo" {
				t.Errorf("with no Flux namespace the object is in %q, want the build namespace demo", plain.GetNamespace())
			}
			if moved.GetNamespace() != "flux-system" {
				t.Errorf("with a Flux namespace the object is in %q, want flux-system", moved.GetNamespace())
			}
			moved.SetNamespace(plain.GetNamespace())
			if got, want := policyFreeJSON(t, moved), policyFreeJSON(t, plain); !reflect.DeepEqual(got, want) {
				t.Errorf("but for its namespace the moved object differs:\n got %v\nwant %v", got, want)
			}
		})
	}
}

// TestFluxKinds_ObjectNameIsClaimedInTheFluxNamespace: with a Flux namespace
// set, two components of one kind given one object name are refused, and the
// refusal names the object in the Flux namespace, where both would land.
func TestFluxKinds_ObjectNameIsClaimedInTheFluxNamespace(t *testing.T) {
	for _, kind := range fluxKinds(t) {
		t.Run(kind.component, func(t *testing.T) {
			named := func(name string) oam.Component {
				props := map[string]any{oam.ObjectNameProperty: "shared"}
				maps.Copy(props, kind.minimal)
				return oam.Component{Name: name, Properties: props}
			}
			_, err := fluxKindTransform(kind, "flux-system", named("a"), named("b"))
			want := fmt.Sprintf("name collision: %s %q is named by component %q", kind.gvk.GroupKind(), "flux-system/shared", "a")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %q", err, want)
			}
		})
	}
}

// TestFluxKinds_ReportReads: each kind's config reports the ConfigMaps and
// Secrets its object reads by name from the namespace it lands in, so the
// objects of a trait that are named follow it to the Flux namespace. Every
// kind has a row, and a row that names none says the kind reads none.
func TestFluxKinds_ReportReads(t *testing.T) {
	type reads struct {
		props               map[string]any
		configMaps, secrets []string
	}
	cases := map[string][]reads{
		// An Alert names a Provider, which is the object that holds the
		// address and the credentials.
		"fluxcd-alert": {{props: fluxAlertFull()}},
	}
	for _, kind := range fluxKinds(t) {
		if len(cases[kind.component]) == 0 {
			t.Errorf("%s has no case of what it reads", kind.component)
		}
		for i, tc := range cases[kind.component] {
			t.Run(fmt.Sprintf("%s/%d", kind.component, i), func(t *testing.T) {
				cfg, err := kind.handler.ToApplicationConfig(&oam.Component{Name: "web", Type: kind.component, Properties: tc.props}, coreKindNamespace)
				if err != nil {
					t.Fatalf("ToApplicationConfig: %v", err)
				}
				reader, ok := cfg.(interface {
					FluxNamespaceReads() (configMaps, secrets []string)
				})
				if !ok {
					t.Fatalf("%T reports no FluxNamespaceReads", cfg)
				}
				configMaps, secrets := reader.FluxNamespaceReads()
				if !slices.Equal(configMaps, tc.configMaps) || !slices.Equal(secrets, tc.secrets) {
					t.Errorf("reads ConfigMaps %v and Secrets %v, want %v and %v", configMaps, secrets, tc.configMaps, tc.secrets)
				}
			})
		}
	}
}
