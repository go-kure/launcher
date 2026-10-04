package oam

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	forcedTail       = ": when an update changes an immutable field, Flux deletes and recreates it instead of failing the apply, which can lose its data"
	annotationReason = "kustomize.toolkit.fluxcd.io/force: enabled"
	intentReason     = "its application sets the force-replace delivery intent"
	bundleReason     = "its bundle sets force: true"
)

func forceAnnotated(v string) map[string]string {
	return map[string]string{fluxForceAnnotation: v}
}

func claimObject(namespace, name string, annotations map[string]string) *client.Object {
	return collisionObject(&corev1.PersistentVolumeClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Annotations: annotations},
	})
}

func volumeObject(name string, annotations map[string]string) *client.Object {
	return collisionObject(&corev1.PersistentVolume{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolume"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
	})
}

// unstructuredClaim is a PersistentVolumeClaim as a list member, annotated with
// the force annotation when force is set.
func unstructuredClaim(name string, force bool) map[string]any {
	metadata := map[string]any{"namespace": "shop", "name": name}
	if force {
		metadata["annotations"] = map[string]any{fluxForceAnnotation: "enabled"}
	}
	return map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": metadata}
}

// listObject is an unstructured list envelope of kind with items, carrying the
// force annotation itself, which Flux drops when it applies the members.
func listObject(kind string, items ...any) *client.Object {
	return collisionObject(&unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": kind,
		"metadata": map[string]any{"name": "envelope", "annotations": map[string]any{fluxForceAnnotation: "enabled"}},
		"items":    items,
	}})
}

// TestWarnForcedVolumes pins that every force-applied PersistentVolume and
// PersistentVolumeClaim gets exactly one warning naming it, its producer and every
// reason it is forced, and that nothing else warns (go-kure/launcher#720).
func TestWarnForcedVolumes(t *testing.T) {
	deployment := collisionObject(&appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", Annotations: forceAnnotated("enabled")},
	})
	// forced is an application a caller built: Forced, no reason recorded.
	forced := func(a GeneratedApplication) GeneratedApplication { a.Forced = true; return a }
	// intent and intentInForcedBundle are applications as GenerateApplications
	// returns them, for the ForceReplace intent alone and inside a bundle that
	// sets Force as well.
	intent := func(a GeneratedApplication) GeneratedApplication {
		a.Forced, a.forceReplace = true, true
		return a
	}
	intentInForcedBundle := func(a GeneratedApplication) GeneratedApplication {
		a.Forced, a.forceReplace, a.bundleForce = true, true, true
		return a
	}
	annotatedClaim := claimObject("shop", "data", forceAnnotated("enabled"))
	cases := []struct {
		name string
		apps []GeneratedApplication
		want []string
	}{
		{"annotated claim", []GeneratedApplication{generatedApp("db", "", claimObject("shop", "data", forceAnnotated("enabled")))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"annotated volume has no namespace", []GeneratedApplication{generatedApp("db", "", volumeObject("pv-data", forceAnnotated("enabled")))},
			[]string{`PersistentVolume pv-data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"claim in a forced bundle", []GeneratedApplication{forced(generatedApp("data", "db", claimObject("shop", "data", nil)))},
			[]string{`PersistentVolumeClaim shop/data (sub-application "data" of component "db") is force-applied (` + bundleReason + `)` + forcedTail}},
		{"both reasons, one warning", []GeneratedApplication{forced(generatedApp("db", "", claimObject("shop", "data", forceAnnotated("enabled"))))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `; ` + bundleReason + `)` + forcedTail}},
		{"claim under the force-replace intent, unannotated", []GeneratedApplication{intent(generatedApp("data", "db", claimObject("shop", "data", nil)))},
			[]string{`PersistentVolumeClaim shop/data (sub-application "data" of component "db") is force-applied (` + intentReason + `)` + forcedTail}},
		{"volume under the force-replace intent", []GeneratedApplication{intent(generatedApp("db", "", volumeObject("pv-data", nil)))},
			[]string{`PersistentVolume pv-data (component "db") is force-applied (` + intentReason + `)` + forcedTail}},
		{"intent in a forced bundle", []GeneratedApplication{intentInForcedBundle(generatedApp("db", "", claimObject("shop", "data", nil)))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + intentReason + `; ` + bundleReason + `)` + forcedTail}},
		{"all three reasons, one warning", []GeneratedApplication{intentInForcedBundle(generatedApp("db", "", claimObject("shop", "data", forceAnnotated("enabled"))))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `; ` + intentReason + `; ` + bundleReason + `)` + forcedTail}},
		{"Forced decides: a recorded reason without it forces nothing", []GeneratedApplication{
			func() GeneratedApplication {
				a := intentInForcedBundle(generatedApp("db", "", claimObject("shop", "data", nil)))
				a.Forced = false
				return a
			}(),
		}, nil},
		{"list members under the intent", []GeneratedApplication{intent(generatedApp("raw", "", listObject("List", unstructuredClaim("a", false))))},
			[]string{`PersistentVolumeClaim shop/a (component "raw") is force-applied (` + intentReason + `)` + forcedTail}},
		{"intent on a Deployment is not a volume", []GeneratedApplication{intent(generatedApp("web", "", deployment))}, nil},
		{"a repeat joins the intent to the first", []GeneratedApplication{
			forced(generatedApp("db", "", claimObject("shop", "data", nil))),
			intent(generatedApp("data", "db", claimObject("shop", "data", nil))),
		}, []string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + intentReason + `; ` + bundleReason + `)` + forcedTail}},
		{"generation order", []GeneratedApplication{
			generatedApp("db", "", claimObject("shop", "b", forceAnnotated("enabled")), claimObject("shop", "a", forceAnnotated("enabled"))),
			forced(generatedApp("cache", "", volumeObject("pv", nil))),
		}, []string{
			`PersistentVolumeClaim shop/b (component "db") is force-applied (` + annotationReason + `)` + forcedTail,
			`PersistentVolumeClaim shop/a (component "db") is force-applied (` + annotationReason + `)` + forcedTail,
			`PersistentVolume pv (component "cache") is force-applied (` + bundleReason + `)` + forcedTail,
		}},
		{"unforced claim", []GeneratedApplication{generatedApp("db", "", claimObject("shop", "data", nil))}, nil},
		{"annotation value other than enabled", []GeneratedApplication{generatedApp("db", "", claimObject("shop", "data", forceAnnotated("disabled")))}, nil},
		{"forced Deployment is not a volume", []GeneratedApplication{forced(generatedApp("web", "", deployment))}, nil},
		{"nil entry and nil object", []GeneratedApplication{forced(generatedApp("db", "", nil, collisionObject(nil)))}, nil},
		{"list members, by their own annotation", []GeneratedApplication{generatedApp("raw", "", listObject("List", unstructuredClaim("a", true), unstructuredClaim("b", false)))},
			[]string{`PersistentVolumeClaim shop/a (component "raw") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"list members in a forced bundle, nested", []GeneratedApplication{forced(generatedApp("raw", "", listObject("List",
			unstructuredClaim("a", false),
			map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaimList", "items": []any{unstructuredClaim("b", false), "not an object"}},
		)))}, []string{
			`PersistentVolumeClaim shop/a (component "raw") is force-applied (` + bundleReason + `)` + forcedTail,
			`PersistentVolumeClaim shop/b (component "raw") is force-applied (` + bundleReason + `)` + forcedTail,
		}},
		{"an envelope's own annotation forces no member", []GeneratedApplication{generatedApp("raw", "", listObject("List", unstructuredClaim("a", false)))}, nil},
		{"Flux expands a non-List envelope", []GeneratedApplication{generatedApp("raw", "", listObject("Widget", unstructuredClaim("a", true)))},
			[]string{`PersistentVolumeClaim shop/a (component "raw") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"Flux expands a non-List envelope one level only", []GeneratedApplication{forced(generatedApp("raw", "", listObject("Widget",
			map[string]any{"apiVersion": "v1", "kind": "Widget", "metadata": map[string]any{"name": "inner"}, "items": []any{unstructuredClaim("a", true)}},
		)))}, nil},
		{"annotation value in another case", []GeneratedApplication{generatedApp("db", "", claimObject("shop", "data", forceAnnotated("Enabled")))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"force label", []GeneratedApplication{generatedApp("db", "", collisionObject(&corev1.PersistentVolumeClaim{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "data", Labels: map[string]string{fluxForceAnnotation: "enabled"}},
		}))}, []string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"a forced repeat names the unforced first producer", []GeneratedApplication{
			generatedApp("db", "", claimObject("shop", "data", nil)),
			generatedApp("raw", "", claimObject("shop", "data", forceAnnotated("enabled"))),
		}, []string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"one claim repeated warns once", []GeneratedApplication{generatedApp("db", "", annotatedClaim, annotatedClaim, claimObject("shop", "data", forceAnnotated("enabled")))},
			[]string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `)` + forcedTail}},
		{"a repeat joins its reasons to the first", []GeneratedApplication{
			generatedApp("db", "", claimObject("shop", "data", forceAnnotated("enabled"))),
			forced(generatedApp("data", "db", claimObject("shop", "data", nil))),
		}, []string{`PersistentVolumeClaim shop/data (component "db") is force-applied (` + annotationReason + `; ` + bundleReason + `)` + forcedTail}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			var got []string
			tr.SetWarningHandler(func(msg string) { got = append(got, msg) })
			tr.WarnForcedVolumes(tc.apps)
			if !slices.Equal(got, tc.want) {
				t.Errorf("warnings =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}

	t.Run("no handler", func(t *testing.T) {
		NewTransformer(nil, nil).WarnForcedVolumes([]GeneratedApplication{generatedApp("db", "", claimObject("shop", "data", forceAnnotated("enabled")))})
	})
}

// claimConfig generates one PersistentVolumeClaim named after its application.
type claimConfig struct{}

func (claimConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	return []*client.Object{claimObject(app.Namespace, app.Name, nil)}, nil
}

// TestGenerateApplications_Forced pins that an application is Forced exactly
// when its leaf bundle sets Force to true or it carries the ForceReplace
// delivery intent (go-kure/launcher#782), that the PruneProtection intent
// forces nothing, and that WarnForcedVolumes names the reason of each.
func TestGenerateApplications_Forced(t *testing.T) {
	claim := func(name string, delivery stack.DeliveryIntent) *stack.Application {
		app := stack.NewApplication(name, "shop", claimConfig{})
		app.Delivery = delivery
		return app
	}
	none := stack.DeliveryIntent{}
	replace := stack.DeliveryIntent{ForceReplace: true}
	cluster := &stack.Cluster{Name: "shop", Node: &stack.Node{Name: "shop", Bundle: &stack.Bundle{Name: "shop", Children: []*stack.Bundle{
		{Name: "forced", Force: new(true), Applications: []*stack.Application{claim("a", none), claim("d", replace)}},
		{Name: "off", Force: new(false), Applications: []*stack.Application{claim("b", none), claim("e", replace)}},
		{Name: "unset", Applications: []*stack.Application{
			claim("c", none), claim("f", replace),
			claim("g", stack.DeliveryIntent{PruneProtection: true}),
			claim("h", stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}),
		}},
	}}}}
	apps, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	got := map[string]bool{}
	for _, a := range apps {
		got[a.Name] = a.Forced
	}
	want := map[string]bool{"a": true, "b": false, "c": false, "d": true, "e": true, "f": true, "g": false, "h": true}
	if !maps.Equal(got, want) {
		t.Errorf("Forced = %v, want %v", got, want)
	}

	tr := NewTransformer(nil, nil)
	var warnings []string
	tr.SetWarningHandler(func(msg string) { warnings = append(warnings, msg) })
	tr.WarnForcedVolumes(apps)
	warning := func(name, reasons string) string {
		return `PersistentVolumeClaim shop/` + name + ` (component "` + name + `") is force-applied (` + reasons + `)` + forcedTail
	}
	wantWarnings := []string{
		warning("a", bundleReason),
		warning("d", intentReason+"; "+bundleReason),
		warning("e", intentReason),
		warning("f", intentReason),
		warning("h", intentReason),
	}
	if !slices.Equal(warnings, wantWarnings) {
		t.Errorf("warnings =\n%q\nwant\n%q", warnings, wantWarnings)
	}
}

// TestAdoptMemberDeliveryIntents pins the sibling-group rule: the group's
// application takes each intent any member has, keeps one it already has, and a
// component that is not a group is left as its trait set it.
func TestAdoptMemberDeliveryIntents(t *testing.T) {
	app := func(d stack.DeliveryIntent) *stack.Application {
		a := stack.NewApplication("web", "shop", claimConfig{})
		a.Delivery = d
		return a
	}
	member := func(d stack.DeliveryIntent) componentEntry { return componentEntry{app: app(d)} }
	prune := stack.DeliveryIntent{PruneProtection: true}
	replace := stack.DeliveryIntent{ForceReplace: true}
	both := stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}
	cases := []struct {
		name    string
		group   stack.DeliveryIntent
		members []componentEntry
		want    stack.DeliveryIntent
	}{
		{"not a group", prune, nil, prune},
		{"no member has one", stack.DeliveryIntent{}, []componentEntry{member(stack.DeliveryIntent{}), member(stack.DeliveryIntent{})}, stack.DeliveryIntent{}},
		{"one member of two", stack.DeliveryIntent{}, []componentEntry{member(stack.DeliveryIntent{}), member(prune)}, prune},
		{"one intent per member", stack.DeliveryIntent{}, []componentEntry{member(replace), member(prune)}, both},
		{"the group's own is kept", replace, []componentEntry{member(prune)}, both},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := componentEntry{app: app(tc.group), members: tc.members}
			var before []stack.DeliveryIntent
			for _, m := range tc.members {
				before = append(before, m.app.Delivery)
			}
			adoptMemberDeliveryIntents([]componentEntry{entry})
			if entry.app.Delivery != tc.want {
				t.Errorf("group Delivery = %+v, want %+v", entry.app.Delivery, tc.want)
			}
			// A member keeps its own intent: only the group's application is delivered.
			for i, m := range tc.members {
				if m.app.Delivery != before[i] {
					t.Errorf("member %d Delivery = %+v, want it left at %+v", i, m.app.Delivery, before[i])
				}
			}
		})
	}
}
