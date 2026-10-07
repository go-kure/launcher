package kurel

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// The configmap, secret and pvc traits name their objects by a required `name`,
// so no hook is asked for them, and the name is claimed (go-kure/launcher#787).
// The ConfigMap and the Secret are claimed in the namespace they land in: the
// Flux namespace when the component's Flux object reads them.

func secretTrait(name string) oam.Trait {
	return oam.Trait{Type: "secret", Properties: map[string]any{"name": name, "stringData": map[string]any{"K": "v"}}}
}

// fluxNSTransformErr transforms comps under the Flux namespace, as fluxNSObjects
// does, and returns the transform's error.
func fluxNSTransformErr(comps ...oam.Component) error {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	_, err := newBuiltinTransformer().Transform(app, oam.TransformContext{FluxNamespace: fluxNSTarget, Domain: kurelDomain})
	return err
}

// Two helmreleases that each read their own trait's object of one name move
// both objects to the Flux namespace, where they are one object: refused with
// both named. When only one of them reads it, the two land in two namespaces
// and are two objects.
func TestStorageClaims_FluxInputClaimedWhereItLands(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		trait      oam.Trait
	}{
		{"configmap", "ConfigMap", configMapTrait("vals")},
		{"secret", "Secret", secretTrait("vals")},
	} {
		reads := map[string]any{"valuesFrom": []any{map[string]any{"kind": tt.kind, "name": "vals"}}}
		t.Run(tt.name+" read by both", func(t *testing.T) {
			err := fluxNSTransformErr(
				fluxNSComponent(t, "a", "helmrelease", reads, tt.trait),
				fluxNSComponent(t, "b", "helmrelease", reads, tt.trait),
			)
			want := `name collision: ` + tt.kind + ` "` + fluxNSTarget + `/vals" is named by component "a" traits[0] "` + tt.name +
				`" (its own object, set by name) and by component "b" traits[0] "` + tt.name + `" (its own object, set by name)`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
		t.Run(tt.name+" read by one", func(t *testing.T) {
			// The two sub-applications are named after their objects, and two
			// applications of one name in one bundle are refused: the hook names
			// b's apart, and leaves both objects named vals.
			apart := func(req oam.NameRequest) (string, bool) {
				return req.Default + "-b", req.Role == oam.NameRoleSubApplication && req.Component == "b"
			}
			got := fluxNSObjectsNamed(t, apart,
				fluxNSComponent(t, "a", "helmrelease", reads, tt.trait),
				fluxNSComponent(t, "b", "helmrelease", nil, tt.trait),
			)
			// fluxNSObjects keys by kind and name, so the two objects share a key;
			// the transform accepting them is the point.
			if _, ok := got[tt.kind+"/vals"]; !ok {
				t.Fatalf("no %s/vals generated: %v", tt.kind, got)
			}
		})
	}
}

// certificateRule is a trait lowering rule that emits a certificate trait whose
// Secret is secretName, as a platform's own rule may. An emitted trait takes no
// capability rendering, so the rule names the issuer.
type certificateRule struct{ secretName string }

func (certificateRule) TraitType() string { return "issued-tls" }

func (r certificateRule) LowerTrait(_ *oam.Trait, _ oam.LoweringContext) (oam.LoweringResult, error) {
	cert := certificateTrait(r.secretName)
	cert.Properties["issuerRef"] = map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"}
	return oam.LoweringResult{Traits: []oam.Trait{cert}}, nil
}

// certificateRuleTransformErr transforms comps with certificateRule registered
// for secretName, under fluxNamespace when it is set, and returns the
// transform's error.
func certificateRuleTransformErr(secretName, fluxNamespace string, comps ...oam.Component) error {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "app", Namespace: "default"},
		Spec:       oam.ApplicationSpec{Components: comps},
	}
	tr := newBuiltinTransformer()
	tr.RegisterTraitLowering(certificateRule{secretName: secretName})
	_, err := tr.Transform(app, oam.TransformContext{FluxNamespace: fluxNamespace, Domain: kurelDomain})
	return err
}

// A certificate trait a lowering rule emitted names a Secret cert-manager
// writes, which no generated-object check sees, so its claim is held against
// every other owner of the name: an authored secret component's Secret, and
// the helm rule's values Secret on the same component, with or without a Flux
// namespace. The trait-generated values Secret and volume claims a rule names
// stay accepted (TestHelmNames_AuthoredWinsAndHookIsNotAsked, the webservice
// and worker volume builds). The authored component's case is held by the
// prior claim not being a lowered one; the helm cases by the certificate's
// Secret not being an object the trait generates.
func TestStorageClaims_RuleEmittedCertificateSecretClaimed(t *testing.T) {
	const want = `name collision: Secret "`
	issued := oam.Trait{Type: "issued-tls"}
	t.Run("an authored secret component", func(t *testing.T) {
		err := certificateRuleTransformErr("shared", "",
			fluxNSComponent(t, "shared", "secret", nil),
			fluxNSComponent(t, "web", "helmrelease", nil, issued),
		)
		if err == nil || !strings.Contains(err.Error(), want+`default/shared"`) {
			t.Fatalf("err = %v, want a name collision on default/shared", err)
		}
	})
	values := map[string]any{"secretValues": map[string]any{"password": "not-a-real-one"}, "valuesSecretName": "shared"}
	for _, fluxNamespace := range []string{"", fluxNSTarget} {
		landing := "default"
		if fluxNamespace != "" {
			landing = fluxNamespace
		}
		t.Run("the helm rule's values Secret, Flux namespace "+fluxNamespace, func(t *testing.T) {
			err := certificateRuleTransformErr("shared", fluxNamespace, fluxNSComponent(t, "chart", "helm", values, issued))
			if err == nil || !strings.Contains(err.Error(), want+landing+`/shared"`) {
				t.Fatalf("err = %v, want a name collision on %s/shared", err, landing)
			}
		})
	}
}

// Two pvc traits of one name on two components are one claim: refused with
// both named.
func TestStorageClaims_PVCTraitClaimed(t *testing.T) {
	pvc := oam.Trait{Type: "pvc", Properties: map[string]any{"name": "data", "size": "1Gi"}}
	err := fluxNSTransformErr(
		fluxNSComponent(t, "a", "helmrelease", nil, pvc),
		fluxNSComponent(t, "b", "helmrelease", nil, pvc),
	)
	want := `name collision: PersistentVolumeClaim "default/data" is named by component "a" traits[0] "pvc" (its own object, set by name) and by component "b" traits[0] "pvc" (its own object, set by name)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one containing %q", err, want)
	}
}
