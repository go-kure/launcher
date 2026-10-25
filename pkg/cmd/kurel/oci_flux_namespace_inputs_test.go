package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// An oci component that keeps its own source lowers to a sibling group of the
// OCIRepository and the Kustomization, and its traits go to the Kustomization.
// Both land in the Flux namespace together, so a Secret one of the component's
// traits makes and the source reads (secretRef, certSecretRef, proxySecretRef)
// moves there with them, and is claimed there (go-kure/launcher#1012). A
// source several components share is not of the group: a consumer's trait
// Secret it reads stays (go-kure/launcher#1016).

const ociFluxNSURL = "oci://registry.example.com/manifests/app"

// ociFluxNSComponent is an oci component on ociFluxNSURL whose source reads
// "shared" under ref, with traits attached.
func ociFluxNSComponent(t *testing.T, name, ref string, traits ...oam.Trait) oam.Component {
	t.Helper()
	return fluxNSComponent(t, name, "oci", map[string]any{
		"source": map[string]any{"url": ociFluxNSURL, ref: secretRef("shared")},
	}, traits...)
}

// TestFluxNamespace_OCIKeptSourceReadsTraitSecret: the trait's object lands in
// the Flux namespace with the source that reads it, and its name is claimed
// there: a same-named Secret another component makes in the Flux namespace
// collides with it, one in the application namespace does not.
func TestFluxNamespace_OCIKeptSourceReadsTraitSecret(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ref   string
		trait oam.Trait
		moved string // the trait's object, "Kind"
	}{
		{"external-secret read by secretRef", "secretRef", externalSecretTrait("shared"), "ExternalSecret"},
		{"secret read by secretRef", "secretRef", secretTrait("shared"), "Secret"},
		{"external-secret read by certSecretRef", "certSecretRef", externalSecretTrait("shared"), "ExternalSecret"},
		{"certificate read by certSecretRef", "certSecretRef", certificateTrait("shared"), "Certificate"},
		{"external-secret read by proxySecretRef", "proxySecretRef", externalSecretTrait("shared"), "ExternalSecret"},
		{"secret read by proxySecretRef", "proxySecretRef", secretTrait("shared"), "Secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("moved with the source", func(t *testing.T) {
				got := fluxNSObjects(t, ociFluxNSComponent(t, "c", tc.ref, tc.trait))
				for _, key := range []string{"OCIRepository/c", "Kustomization/c", tc.moved + "/shared"} {
					if ns, ok := got[key]; !ok {
						t.Errorf("no %s emitted; got %v", key, got)
					} else if ns != fluxNSTarget {
						t.Errorf("%s namespace = %q, want %q", key, ns, fluxNSTarget)
					}
				}
			})
			t.Run("claimed in the Flux namespace", func(t *testing.T) {
				b := fluxNSComponent(t, "b", "helmrepository", map[string]any{"secretRef": secretRef("shared")}, externalSecretTrait("shared"))
				_, err := fluxNSBuild(t, nil, ociFluxNSComponent(t, "c", tc.ref, tc.trait), b)
				want := `name collision: Secret "` + fluxNSTarget + `/shared"`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("build error = %v, want it to contain %q", err, want)
				}
			})
			t.Run("not claimed in the application namespace", func(t *testing.T) {
				b := fluxNSComponent(t, "b", "deployment", nil, externalSecretTrait("shared"))
				got, err := fluxNSBuild(t, nil, ociFluxNSComponent(t, "c", tc.ref, tc.trait), b)
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				requireObjects(t, got, tc.moved+" "+fluxNSTarget+"/shared", "ExternalSecret default/shared")
			})
		})
	}
}

// TestFluxNamespace_OCIUnreadTraitSecretStays: a trait Secret the source does
// not read stays in the application namespace with the workloads.
func TestFluxNamespace_OCIUnreadTraitSecretStays(t *testing.T) {
	got := fluxNSObjects(t, ociFluxNSComponent(t, "c", "secretRef", externalSecretTrait("other")))
	if ns := got["ExternalSecret/other"]; ns != "default" {
		t.Errorf("ExternalSecret/other namespace = %q, want %q; got %v", ns, "default", got)
	}
}

// TestFluxNamespace_OCISharedSourceTraitSecretStays: two components share the
// source, which reads "shared"; the trait Secret of one of them stays in the
// application namespace, as the shared source is no member of its group
// (go-kure/launcher#1016).
func TestFluxNamespace_OCISharedSourceTraitSecretStays(t *testing.T) {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			ociFluxNSComponent(t, "a", "secretRef", externalSecretTrait("shared")),
			ociFluxNSComponent(t, "b", "secretRef"),
		}},
	}
	cluster, err := newBuiltinTransformer().Transform(app, oam.TransformContext{FluxNamespace: fluxNSTarget, Domain: kurelDomain})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	// Every generated object, keyed "Kind/name", as fluxNSObjects keys them.
	got := map[string]string{}
	var sources int
	for _, a := range apps {
		for _, o := range a.Objects {
			if o == nil {
				continue
			}
			kind := (*o).GetObjectKind().GroupVersionKind().Kind
			got[kind+"/"+(*o).GetName()] = (*o).GetNamespace()
			if kind == "OCIRepository" {
				sources++
			}
		}
	}
	if sources != 1 {
		t.Fatalf("got %d OCIRepositories, want the one a and b share; got %v", sources, got)
	}
	if ns := got["ExternalSecret/shared"]; ns != "default" {
		t.Errorf("ExternalSecret/shared namespace = %q, want %q; got %v", ns, "default", got)
	}
}

// fluxPairRule lowers a "flux-pair" component to a same-name sibling group of
// an ocirepository member carrying the component's traits and a
// fluxcd-kustomization member whose decryption reads Secret "shared": the
// shape of a kept oci source, with a Kustomization that reads a Secret
// itself, which the oci component cannot express.
type fluxPairRule struct{ t *testing.T }

func (fluxPairRule) ComponentType() string { return "flux-pair" }
func (r fluxPairRule) LowerComponent(c *oam.Component, _ oam.LoweringContext) (oam.LoweringResult, error) {
	source := fluxNSComponent(r.t, c.Name, "ocirepository", nil, c.Traits...)
	kustomization := fluxNSComponent(r.t, c.Name, "fluxcd-kustomization", map[string]any{
		"sourceRef":  map[string]any{"kind": "OCIRepository", "name": c.Name},
		"decryption": map[string]any{"provider": "sops", "secretRef": secretRef("shared")},
	})
	return oam.LoweringResult{Components: []oam.Component{source, kustomization}}, nil
}

// TestFluxNamespace_GroupMemberTraitSecretAnotherFluxMemberReads: a Secret
// the sibling group's Kustomization reads moves to the Flux namespace with it,
// though the trait that makes it is on the group's other Flux member.
func TestFluxNamespace_GroupMemberTraitSecretAnotherFluxMemberReads(t *testing.T) {
	tr := newBuiltinTransformer()
	tr.RegisterComponentLowering(fluxPairRule{t})
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{
			{Name: "c", Type: "flux-pair", Traits: []oam.Trait{externalSecretTrait("shared")}},
		}},
	}
	cluster, err := tr.Transform(app, oam.TransformContext{FluxNamespace: fluxNSTarget, Domain: kurelDomain})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	apps, err := oam.GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	got := map[string]string{}
	for _, a := range apps {
		for _, o := range a.Objects {
			if o != nil {
				got[(*o).GetObjectKind().GroupVersionKind().Kind+"/"+(*o).GetName()] = (*o).GetNamespace()
			}
		}
	}
	for _, key := range []string{"OCIRepository/c", "Kustomization/c", "ExternalSecret/shared"} {
		if ns, ok := got[key]; !ok {
			t.Errorf("no %s emitted; got %v", key, got)
		} else if ns != fluxNSTarget {
			t.Errorf("%s namespace = %q, want %q", key, ns, fluxNSTarget)
		}
	}
}
