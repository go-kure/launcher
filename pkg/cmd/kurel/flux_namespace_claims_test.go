package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
)

// A trait's Secret that a component's Flux object reads moves with that object
// to the Flux namespace (moveFluxNamespaceInputs), so its name is claimed
// there, by the same test of what the object reads, and not in the
// application namespace it would otherwise be in (go-kure/launcher#787).

// fluxNSBuild builds comps under the Flux namespace, with hook as the naming
// hook, through the generated-object collision check, and returns every
// generated object keyed "Kind namespace/name".
func fluxNSBuild(t *testing.T, hook func(oam.NameRequest) (string, bool), comps ...oam.Component) (map[string]bool, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	capabilities := map[string]oam.CapabilityBinding{
		"certificate": {Rendering: map[string]any{"issuerRef": map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"}}},
	}
	cluster, err := newBuiltinTransformer().Transform(app, oam.TransformContext{
		FluxNamespace: fluxNSTarget, Domain: kurelDomain, Capabilities: capabilities, Naming: hook,
	})
	if err != nil {
		return nil, err
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		return nil, err
	}
	if err := oam.CheckInDocumentCollisions(apps); err != nil {
		return nil, err
	}
	got := map[string]bool{}
	var walk func(n *stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				objs, err := a.Config.Generate(a)
				if err != nil {
					t.Fatalf("Generate %s: %v", a.Name, err)
				}
				for _, o := range objs {
					got[(*o).GetObjectKind().GroupVersionKind().Kind+" "+(*o).GetNamespace()+"/"+(*o).GetName()] = true
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	return got, nil
}

func requireObjects(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	for _, w := range want {
		if !got[w] {
			t.Errorf("no %s among the generated objects", w)
		}
	}
}

// TestFluxNamespace_ClaimsWhereTheObjectLands: a's HelmRepository reads
// "shared" from its own trait, which moves to the Flux namespace; b's
// Deployment has a Secret "shared" made in the application namespace. The two
// Secrets land in different namespaces, so both build.
func TestFluxNamespace_ClaimsWhereTheObjectLands(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ref   string
		trait oam.Trait
		moved string
	}{
		{"external-secret read by secretRef", "secretRef", externalSecretTrait("shared"), "ExternalSecret " + fluxNSTarget + "/shared"},
		{"certificate read by certSecretRef", "certSecretRef", certificateTrait("shared"), "Certificate " + fluxNSTarget + "/shared"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := fluxNSComponent(t, "a", "helmrepository", map[string]any{tt.ref: secretRef("shared")}, tt.trait)
			b := fluxNSComponent(t, "b", "deployment", nil, externalSecretTrait("shared"))
			got, err := fluxNSBuild(t, nil, a, b)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			requireObjects(t, got, tt.moved, "ExternalSecret default/shared")
		})
	}
}

// TestFluxNamespace_SameFinalNamespaceStillCollides: two HelmRepositories each
// read "shared" from their own external-secret trait; both Secrets land in the
// Flux namespace, where their names collide.
func TestFluxNamespace_SameFinalNamespaceStillCollides(t *testing.T) {
	a := fluxNSComponent(t, "a", "helmrepository", map[string]any{"secretRef": secretRef("shared")}, externalSecretTrait("shared"))
	b := fluxNSComponent(t, "b", "helmrepository", map[string]any{"secretRef": secretRef("shared")}, externalSecretTrait("shared"))
	_, err := fluxNSBuild(t, nil, a, b)
	want := `name collision: Secret "` + fluxNSTarget + `/shared" is named by component "a"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("build error = %v, want it to contain %q", err, want)
	}
}

// TestFluxNamespace_HookRenamedSecretStays: the hook renames the Secret a's
// external-secret trait produces, so a's HelmRepository no longer reads it
// ("shared" is what it reads). It does not move: the ExternalSecret and its
// Secret stay in the application namespace and are claimed there, where a
// Secret of the same name from b collides with it.
func TestFluxNamespace_HookRenamedSecretStays(t *testing.T) {
	rename := func(r oam.NameRequest) (string, bool) {
		if r.Role == oam.NameRoleExternalSecret && r.Component == "a" {
			return "other", true
		}
		return "", false
	}
	a := fluxNSComponent(t, "a", "helmrepository", map[string]any{"secretRef": secretRef("shared")}, externalSecretTrait("shared"))

	t.Run("stays in the application namespace", func(t *testing.T) {
		got, err := fluxNSBuild(t, rename, a)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		requireObjects(t, got, "ExternalSecret default/shared", "HelmRepository "+fluxNSTarget+"/a")
		if got["ExternalSecret "+fluxNSTarget+"/shared"] {
			t.Errorf("the ExternalSecret of the renamed Secret moved to %s", fluxNSTarget)
		}
	})
	t.Run("claimed in the application namespace", func(t *testing.T) {
		b := fluxNSComponent(t, "b", "deployment", nil, externalSecretTrait("other"))
		_, err := fluxNSBuild(t, rename, a, b)
		want := `name collision: Secret "default/other" is named by component "a"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build error = %v, want it to contain %q", err, want)
		}
	})
}
