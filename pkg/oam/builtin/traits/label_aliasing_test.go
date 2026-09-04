package traits_test

import (
	"reflect"
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// Traits that emit more than one object used to build a single
// map[string]string{"app": name} and assign it to every object's metadata
// labels, so the two objects shared one map: a caller stamping its own label
// onto one generated object silently stamped the other, and — for the scaler —
// the PodDisruptionBudget's selector along with it. Each object now gets its own
// map.
//
// Identity, not content, is the assertion: two equal maps and one map read twice
// are indistinguishable by content, and equal-but-separate is what correct output
// looks like here.

// traitLabelMaps returns every label map reachable from a trait's generated
// objects: each object's metadata labels, plus a PodDisruptionBudget's selector,
// which is the only selector these two traits emit.
func traitLabelMaps(t *testing.T, objects []*client.Object) map[uintptr]string {
	t.Helper()
	seen := make(map[uintptr]string, len(objects))
	add := func(where string, m map[string]string) {
		if m == nil {
			return
		}
		ptr := reflect.ValueOf(m).Pointer()
		if first, dup := seen[ptr]; dup {
			t.Errorf("%s and %s are the same map: editing one edits the other", first, where)
			return
		}
		seen[ptr] = where
	}
	for _, o := range objects {
		obj := *o
		prefix := reflect.TypeOf(obj).Elem().Name() + "/" + obj.GetName()
		add(prefix+".metadata.labels", obj.GetLabels())
		if pdb, ok := obj.(*policyv1.PodDisruptionBudget); ok && pdb.Spec.Selector != nil {
			add(prefix+".spec.selector.matchLabels", pdb.Spec.Selector.MatchLabels)
		}
	}
	return seen
}

func TestRBACTrait_GeneratedLabelMapsAreNotShared(t *testing.T) {
	rules := []any{
		map[string]any{
			"apiGroups": []any{""},
			"resources": []any{"pods"},
			"verbs":     []any{"get", "list"},
		},
	}

	for _, tc := range []struct {
		name  string
		props map[string]any
		// want is the object count, which is also the label-map count: each
		// rbac object carries exactly one label map and no selector.
		want int
	}{
		{"namespaced", map[string]any{"rules": rules}, 2},
		{"clusterWide", map[string]any{"rules": rules, "clusterWide": true}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := generateTrait(t, &traits.RBACHandler{}, &oam.Trait{Type: "rbac", Properties: tc.props})
			if len(objects) != tc.want {
				t.Fatalf("got %d generated objects, want %d — the fixture no longer reaches what this guards", len(objects), tc.want)
			}
			if got := len(traitLabelMaps(t, objects)); got != tc.want {
				t.Errorf("collected %d distinct label maps across %d objects, want %d", got, tc.want, tc.want)
			}
		})
	}
}

func TestScalerTrait_GeneratedLabelMapsAreNotShared(t *testing.T) {
	objects := generateTrait(t, &traits.ScalerHandler{}, &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 2,
			"maxReplicas": 5,
			"enablePDB":   true,
		},
	})
	if len(objects) != 2 {
		t.Fatalf("got %d generated objects, want 2 (HPA + PDB) — the fixture reaches nothing worth checking", len(objects))
	}

	// Three maps: the HPA's labels, the PDB's labels, and the PDB's selector.
	if got := len(traitLabelMaps(t, objects)); got != 3 {
		t.Errorf("collected %d distinct label maps, want 3 (HPA labels, PDB labels, PDB selector)", got)
	}
}

// generateTrait applies a trait to a fresh application and returns the objects
// the trait's own generated application emits.
func generateTrait(t *testing.T, h oam.TraitHandler, trait *oam.Trait) []*client.Object {
	t.Helper()
	app := newApp("api", "default")
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)

	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// A trait either augments the component's own application or appends its
	// own; the objects under test are whichever application the trait wrote to.
	generated := bundle.Applications[len(bundle.Applications)-1]
	objects, err := generated.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return objects
}
