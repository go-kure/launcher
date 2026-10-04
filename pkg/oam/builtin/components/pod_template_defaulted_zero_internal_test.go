package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

// TestPodTemplateKindsDefaultedZeros_MatchFieldDocs is
// TestPodSpecDefaultedZeros_MatchFieldDocs for the kinds that hold a pod
// template: over the whole type each one decodes, the fields on which an
// authored 0 or false would be silently replaced (a non-pointer omitempty
// number or boolean whose field comment states a default that is not zero)
// must be exactly podTemplateDefaultedZeros. So a field of that shape outside
// the template's pod spec — in the type's own fields or in the template's
// metadata — fails here, naming it, as does a dependency bump that adds one.
//
// template.spec.ephemeralContainers is not compared: the kinds refuse the
// field whole (validateAuthoredPodSpec).
func TestPodTemplateKindsDefaultedZeros_MatchFieldDocs(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[appsv1.ReplicaSetSpec](),
	} {
		t.Run(typ.String(), func(t *testing.T) {
			docs := omitemptyScalarDocs(t, typ)
			// Vacuity guard: the walk reaches the template's pod spec and reads
			// its field comments (see the PodSpec test for the counts).
			if len(docs) < 40 {
				t.Fatalf("found %d omitempty numbers and booleans under %s, want >= 40; the reflection walk is broken", len(docs), typ)
			}
			want := map[string]string{}
			for path, doc := range docs {
				if strings.HasPrefix(path, "template.spec.ephemeralContainers[]") {
					continue
				}
				m := docDefault.FindStringSubmatch(doc)
				if m == nil {
					continue
				}
				if def := strings.ToLower(m[1]); !crdDefaultIsZero(def) {
					want[path] = def
				}
			}
			if len(want) < 10 {
				t.Fatalf("found %d documented non-zero defaults under %s, want >= 10; the field comments are not being read", len(want), typ)
			}
			got := podTemplateDefaultedZeros().fields
			for _, path := range slices.Sorted(maps.Keys(want)) {
				def, ok := got[path]
				switch {
				case !ok:
					t.Errorf("missing %s (documented default %s): a non-pointer omitempty field with a non-zero default", path, want[path])
				case def != want[path]:
					t.Errorf("%s records default %s, the field comment says %s", path, def, want[path])
				}
			}
			for _, path := range slices.Sorted(maps.Keys(got)) {
				if _, ok := want[path]; !ok {
					t.Errorf("stale %s: not a non-pointer omitempty field of %s with a documented non-zero default", path, typ)
				}
			}
		})
	}
}
