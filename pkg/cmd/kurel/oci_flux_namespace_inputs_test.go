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
// source several components share, or one a component names, is generated on
// its own and every consumer ordered after it: a Secret a consumer's trait
// makes and the source reads moves there too (go-kure/launcher#1016).

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

// ociObject is where a generated object landed and the component that owns
// it ("" for one the document as a whole owns).
type ociObject struct{ namespace, component string }

// ociBuild transforms comps and policies with fluxNS as the Flux namespace
// ("" for none), as kurel build does, checks the generated objects for
// in-document collisions, and returns every object keyed "Kind ns/name", as
// fluxNSBuild keys them.
func ociBuild(t *testing.T, fluxNS string, policies []oam.ApplicationPolicy, comps ...oam.Component) (map[string]ociObject, error) {
	t.Helper()
	return ociBuildWaiting(t, fluxNS, false, policies, comps...)
}

// ociBuildWaiting is ociBuild for a delivery that makes a group Ready only once
// its Flux objects are when wait is set (TransformContext.GroupsWaitForFluxObjects).
func ociBuildWaiting(t *testing.T, fluxNS string, wait bool, policies []oam.ApplicationPolicy, comps ...oam.Component) (map[string]ociObject, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps, Policies: policies},
	}
	capabilities := map[string]oam.CapabilityBinding{
		"certificate": {Rendering: map[string]any{"issuerRef": map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"}}},
	}
	cluster, err := newBuiltinTransformer().Transform(app, oam.TransformContext{FluxNamespace: fluxNS, GroupsWaitForFluxObjects: wait, Domain: kurelDomain, Capabilities: capabilities})
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
	got := map[string]ociObject{}
	for _, a := range apps {
		for _, o := range a.Objects {
			if o != nil {
				got[(*o).GetObjectKind().GroupVersionKind().Kind+" "+(*o).GetNamespace()+"/"+(*o).GetName()] = ociObject{(*o).GetNamespace(), a.Component}
			}
		}
	}
	return got, nil
}

// requireOCIObjects fails for each of want ("Kind ns/name") got lacks.
func requireOCIObjects(t *testing.T, got map[string]ociObject, want ...string) {
	t.Helper()
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("no %s among the generated objects; got %v", w, got)
		}
	}
}

// ociSources returns the OCIRepositories got holds.
func ociSources(got map[string]ociObject) []string {
	var out []string
	for k := range got {
		if strings.HasPrefix(k, "OCIRepository ") {
			out = append(out, k)
		}
	}
	return out
}

// dependsOn is a dependency policy ordering component after on.
func dependsOn(component, on string) []oam.ApplicationPolicy {
	return []oam.ApplicationPolicy{{Name: "order", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": component, "dependsOn": []any{on}}},
	}}}
}

// TestFluxNamespace_OCISharedSourceTraitSecretMoves: two components share the
// source, which reads "shared"; the trait object of the one that makes it
// lands in the Flux namespace with the source, owned by that component, and
// is claimed there (go-kure/launcher#1016).
func TestFluxNamespace_OCISharedSourceTraitSecretMoves(t *testing.T) {
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
			pair := func() []oam.Component {
				return []oam.Component{
					ociFluxNSComponent(t, "a", tc.ref, tc.trait),
					ociFluxNSComponent(t, "b", tc.ref),
				}
			}
			t.Run("moved with the source", func(t *testing.T) {
				got, err := ociBuild(t, fluxNSTarget, nil, pair()...)
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				sources := ociSources(got)
				if len(sources) != 1 {
					t.Fatalf("got OCIRepositories %v, want the one a and b share", sources)
				}
				if s := got[sources[0]]; s.namespace != fluxNSTarget || s.component != "" {
					t.Errorf("%s = %+v, want the Flux namespace, owned by the document", sources[0], s)
				}
				key := tc.moved + " " + fluxNSTarget + "/shared"
				requireOCIObjects(t, got, key)
				if o, ok := got[key]; ok && o.component != "a" {
					t.Errorf("%s owned by component %q, want %q", key, o.component, "a")
				}
			})
			t.Run("claimed in the Flux namespace", func(t *testing.T) {
				x := fluxNSComponent(t, "x", "helmrepository", map[string]any{"secretRef": secretRef("shared")}, externalSecretTrait("shared"))
				_, err := ociBuild(t, fluxNSTarget, nil, append(pair(), x)...)
				want := `name collision: Secret "` + fluxNSTarget + `/shared"`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("build error = %v, want it to contain %q", err, want)
				}
			})
			t.Run("not claimed in the application namespace", func(t *testing.T) {
				x := fluxNSComponent(t, "x", "deployment", nil, externalSecretTrait("shared"))
				got, err := ociBuild(t, fluxNSTarget, nil, append(pair(), x)...)
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				requireOCIObjects(t, got, tc.moved+" "+fluxNSTarget+"/shared", "ExternalSecret default/shared")
			})
		})
	}
}

// TestFluxNamespace_OCISharedSourceForms: the trait object the shared source
// reads moves whichever consumer makes it and whichever form shares the
// source; one it does not read stays; two consumers making it collide.
func TestFluxNamespace_OCISharedSourceForms(t *testing.T) {
	named := func(name string, traits ...oam.Trait) oam.Component {
		return fluxNSComponent(t, name, "oci", map[string]any{
			"source": map[string]any{"url": ociFluxNSURL, "name": "src", "secretRef": secretRef("shared")},
		}, traits...)
	}
	t.Run("the adopting consumer makes it", func(t *testing.T) {
		got, err := ociBuild(t, fluxNSTarget, nil,
			ociFluxNSComponent(t, "a", "secretRef"),
			ociFluxNSComponent(t, "b", "secretRef", externalSecretTrait("shared")))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		requireOCIObjects(t, got, "ExternalSecret "+fluxNSTarget+"/shared")
	})
	t.Run("a single consumer names the source", func(t *testing.T) {
		got, err := ociBuild(t, fluxNSTarget, nil, named("a", externalSecretTrait("shared")))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		requireOCIObjects(t, got, "OCIRepository "+fluxNSTarget+"/src", "ExternalSecret "+fluxNSTarget+"/shared")
	})
	t.Run("not read by the source", func(t *testing.T) {
		got, err := ociBuild(t, fluxNSTarget, nil,
			ociFluxNSComponent(t, "a", "secretRef", externalSecretTrait("other")),
			ociFluxNSComponent(t, "b", "secretRef"))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		requireOCIObjects(t, got, "ExternalSecret default/other")
	})
	t.Run("both consumers make it", func(t *testing.T) {
		_, err := ociBuild(t, fluxNSTarget, nil,
			ociFluxNSComponent(t, "a", "secretRef", externalSecretTrait("shared")),
			ociFluxNSComponent(t, "b", "secretRef", externalSecretTrait("shared")))
		want := `name collision: Secret "` + fluxNSTarget + `/shared"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("build error = %v, want it to contain %q", err, want)
		}
	})
}

// TestFluxNamespace_OCISharedSourceLateMaker: with groups waiting for their
// Flux objects, a consumer whose trait makes the shared source's Secret in a
// later group than another consumer of it is refused: the earlier consumer's
// group waits for it, it for the source, the source for the Secret, and the
// Secret's group for the earlier one (go-kure/launcher#1039). Under the default
// delivery the same document is admitted, and the Secret moves with the source
// under a Flux namespace. Also admitted: the maker in the earliest group, an
// external-secret under creationPolicy Merge or None (the Secret exists apart
// from it), and a later maker of a Secret the source does not read. Each row
// runs under a Flux namespace and without one.
func TestFluxNamespace_OCISharedSourceLateMaker(t *testing.T) {
	policyTrait := func(policy string) oam.Trait {
		tr := externalSecretTrait("shared")
		tr.Properties["target"] = map[string]any{"creationPolicy": policy}
		return tr
	}
	later, earliest := dependsOn("a", "b"), dependsOn("b", "a")
	for _, tc := range []struct {
		name     string
		ref      string
		trait    oam.Trait
		wait     bool
		policies []oam.ApplicationPolicy
		refused  bool
	}{
		{"refused: external-secret by secretRef", "secretRef", externalSecretTrait("shared"), true, later, true},
		{"refused: secret by proxySecretRef", "proxySecretRef", secretTrait("shared"), true, later, true},
		{"refused: certificate by certSecretRef", "certSecretRef", certificateTrait("shared"), true, later, true},
		{"admitted: external-secret, the default delivery", "secretRef", externalSecretTrait("shared"), false, later, false},
		{"admitted: secret, the default delivery", "proxySecretRef", secretTrait("shared"), false, later, false},
		{"admitted: certificate, the default delivery", "certSecretRef", certificateTrait("shared"), false, later, false},
		{"admitted: the maker in the earliest group", "secretRef", externalSecretTrait("shared"), true, earliest, false},
		{"admitted: creationPolicy Merge", "secretRef", policyTrait("Merge"), true, later, false},
		{"admitted: creationPolicy None", "secretRef", policyTrait("None"), true, later, false},
		{"admitted: a Secret the source does not read", "secretRef", externalSecretTrait("other"), true, later, false},
	} {
		for _, fluxNS := range []string{fluxNSTarget, ""} {
			t.Run(tc.name+"/fluxNamespace="+fluxNS, func(t *testing.T) {
				got, err := ociBuildWaiting(t, fluxNS, tc.wait, tc.policies,
					ociFluxNSComponent(t, "a", tc.ref, tc.trait),
					ociFluxNSComponent(t, "b", tc.ref))
				if !tc.refused {
					if err != nil {
						t.Fatalf("build: %v", err)
					}
					if !tc.wait && tc.trait.Type == "external-secret" {
						want := "default"
						if fluxNS != "" {
							want = fluxNS
						}
						requireOCIObjects(t, got, "ExternalSecret "+want+"/shared")
					}
					return
				}
				if err == nil {
					t.Fatal("build admitted it, want the late maker refused")
				}
				for _, want := range []string{`requires Secret "shared"`, `component "a"`, `component "b"`, "never converges"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("build error = %v, want it to contain %s", err, want)
					}
				}
			})
		}
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
