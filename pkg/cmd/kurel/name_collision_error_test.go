package kurel

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// A name collision is recognised behind every prefix the builtin handlers and
// the transform put in front of it (go-kure/launcher#787): errors.Is answers to
// oam.ErrNameCollision, and errors.As finds the *oam.NameCollisionError with the
// object and the two members that named it. The text is the one the transform
// returned before the refusal had a type.
func TestNameCollisionError_ThroughTheBuiltinHandlers(t *testing.T) {
	kustomizationKind := schema.GroupKind{Group: "kustomize.toolkit.fluxcd.io", Kind: "Kustomization"}
	delivery := `    - name: delivery
      type: fluxcd-kustomization
      properties:
        objectName: web
        path: ./
        prune: true
        sourceRef:
          kind: OCIRepository
          name: web
`
	for _, tc := range []struct {
		name, doc string
		ctx       oam.TransformContext
		// want is Transform's error, whole: the prefix and the refusal, and after
		// them what lowering adds on a line of its own, the rounds it had run.
		want, rounds string
		collision    oam.NameCollisionError
	}{
		{
			// A component's object named as the member a lowering rule emitted for
			// another component.
			name: "a kind component and the Kustomization of an oci component",
			doc:  helmNamesApp(ociNamesComponent("web", "web", "", "") + delivery),
			ctx:  oam.TransformContext{FluxNamespace: fluxNSTarget},
			want: `component "delivery": name collision: ` + ociKustomizationKindName + ` "` + fluxNSTarget + `/web" is named by ` +
				`component "web" (role "oci-kustomization", its default) and by ` +
				`component "delivery" (role "object", set by properties.objectName); give one of them another name`,
			collision: oam.NameCollisionError{
				Kind: kustomizationKind, Namespace: fluxNSTarget, Name: "web",
				First: oam.NameCollisionMember{
					Component: "web", Role: oam.NameRoleOCIKustomization,
					Description: `component "web" (role "oci-kustomization", its default)`,
				},
				Second: oam.NameCollisionMember{
					Component: "delivery", Role: oam.NameRoleObject, Property: "properties.objectName",
					Description: `component "delivery" (role "object", set by properties.objectName)`,
				},
			},
		},
		{
			// Two members two calls of one lowering rule named, refused while
			// lowering: behind the rule's own prefix, and with no namespace yet.
			name: "the Kustomizations of two oci components",
			doc: helmNamesApp(ociNamesComponent("manifests", "manifests", "", "        kustomizationName: delivery\n") +
				ociNamesComponent("other", "other", "", "        kustomizationName: delivery\n")),
			want: `lowering document "shop" in document "shop" (kind "Application"): component "other" (type "oci") in document "shop" (kind "Application"): ` +
				`oci: naming the Kustomization: name collision: ` + ociKustomizationKindName + ` "delivery" is named by ` +
				`component "manifests" (role "oci-kustomization", set by kustomizationName) and by ` +
				`component "other" (role "oci-kustomization", set by kustomizationName); give one of them another name`,
			rounds: "\n  round 0: component/oci@v1alpha1 \"manifests\" -> [manifests manifests]",
			collision: oam.NameCollisionError{
				Kind: kustomizationKind, Name: "delivery",
				First: oam.NameCollisionMember{
					Component: "manifests", Role: oam.NameRoleOCIKustomization, Property: "kustomizationName",
					Description: `component "manifests" (role "oci-kustomization", set by kustomizationName)`,
				},
				Second: oam.NameCollisionMember{
					Component: "other", Role: oam.NameRoleOCIKustomization, Property: "kustomizationName",
					Description: `component "other" (role "oci-kustomization", set by kustomizationName)`,
				},
			},
		},
		{
			name: "the HorizontalPodAutoscalers of two scaler traits",
			doc:  webBesideAPI(scalerTrait+"            hpaName: api-hpa\n", scalerTrait),
			want: `component "api" trait "scaler": name collision: HorizontalPodAutoscaler.autoscaling "default/api-hpa" is named by ` +
				`component "web" traits[0] "scaler" (role "hpa", set by hpaName) and by ` +
				`component "api" traits[0] "scaler" (role "hpa", its default); give one of them another name`,
			collision: oam.NameCollisionError{
				Kind: schema.GroupKind{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}, Namespace: "default", Name: "api-hpa",
				First: oam.NameCollisionMember{
					Component: "web", Trait: "scaler", Role: oam.NameRoleHPA, Property: "hpaName",
					Description: `component "web" traits[0] "scaler" (role "hpa", set by hpaName)`,
				},
				Second: oam.NameCollisionMember{
					Component: "api", Trait: "scaler", Role: oam.NameRoleHPA,
					Description: `component "api" traits[0] "scaler" (role "hpa", its default)`,
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := transformErr(t, tc.doc, tc.ctx)
			if err == nil {
				t.Fatal("the transform accepted two members naming one object")
			}
			if want := tc.want + tc.rounds; err.Error() != want {
				t.Errorf("err = %v\nwant  %s", err, want)
			}
			if !errors.Is(err, oam.ErrNameCollision) {
				t.Errorf("errors.Is(err, oam.ErrNameCollision) is false for: %v", err)
			}
			var got *oam.NameCollisionError
			if !errors.As(err, &got) {
				t.Fatalf("errors.As finds no *oam.NameCollisionError in: %v", err)
			}
			if *got != tc.collision {
				t.Errorf("the collision is\n  %+v\nwant\n  %+v", *got, tc.collision)
			}
		})
	}

	// Another refusal of a name is not one.
	err := transformErr(t, helmNamesApp(ociNamesComponent("manifests", "manifests", "", "        kustomizationName: Not_A_Name\n")), oam.TransformContext{})
	var got *oam.NameCollisionError
	if err == nil || errors.Is(err, oam.ErrNameCollision) || errors.As(err, &got) {
		t.Errorf("a name that is no subdomain: err = %v\nwant a refusal that is no name collision", err)
	}
}
