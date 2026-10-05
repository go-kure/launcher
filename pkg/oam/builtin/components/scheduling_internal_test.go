package components

import (
	"fmt"
	"maps"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// rawSchedulingKeys are the three properties go-kure/launcher#412 published on
// the deployment kind.
var rawSchedulingKeys = []string{"affinity", "tolerations", "topologySpreadConstraints"}

// rawSchedulingKinds names, per kind, the raw scheduling keys its own literal
// map publishes. `deployment` was the first (go-kure/launcher#412);
// go-kure/launcher#790 adds the kinds below it. A kind that publishes `affinity`
// as the four-key shorthand is not listed with that key here — see
// TestOpinionatedKindsKeepAffinityShorthand.
var rawSchedulingKinds = []struct {
	kind   string
	schema func() map[string]oam.PropertySchema
	keys   []string
}{
	{"deployment", (&DeploymentHandler{}).PropertySchema, rawSchedulingKeys},
	{"statefulset", (&StatefulsetHandler{}).PropertySchema, []string{"tolerations", "topologySpreadConstraints"}},
	{"daemonset", (&DaemonsetHandler{}).PropertySchema, rawSchedulingKeys},
}

// TestSchedulingKeysAbsentFromSharedFragments is the first half of the ordering
// guard: the three raw scheduling keys must live in each publishing kind's own
// literal map and NOT in any fragment it merges. If one ever migrates into
// schemaPodSpec, this fails here rather than silently changing the kinds that
// do not publish it — see TestDocumentsMapsCopyOverwriteMechanism for why a
// migrated key would overwrite rather than collide.
//
// All four (reserved, jobPods) combinations are walked, not the two production
// handlers actually pass. Today only jobPods can change the key set (schema.go:451
// adds podActiveDeadlineSeconds; reserved only stamps PlatformReserved onto values,
// and no production call site passes reserved=true at all), so the two extra
// combinations cannot fail — but nothing structural holds that. `reserved` is a
// plain bool parameter, and an `if reserved` branch adding a key later is exactly
// the migration this guard exists to catch. Generating the matrix rather than
// listing the reachable cases means the guard stays honest without anyone having to
// re-derive that argument.
func TestSchedulingKeysAbsentFromSharedFragments(t *testing.T) {
	fragments := map[string]map[string]oam.PropertySchema{
		"schemaDeploymentSpec()":  schemaDeploymentSpec(),
		"schemaStatefulSetSpec()": schemaStatefulSetSpec(),
		"schemaDaemonSetSpec()":   schemaDaemonSetSpec(),
		"schemaContainerFields()": schemaContainerFields(),
	}
	for _, reserved := range []bool{false, true} {
		for _, jobPods := range []bool{false, true} {
			name := fmt.Sprintf("schemaPodSpec(%t, %t)", reserved, jobPods)
			fragments[name] = schemaPodSpec(reserved, jobPods)
		}
	}
	for name, fragment := range fragments {
		for _, key := range rawSchedulingKeys {
			if _, found := fragment[key]; found {
				t.Errorf("%s declares %q; the raw scheduling shapes belong to a kind's own map, not a shared fragment", name, key)
			}
		}
	}
}

// TestSchedulingKeysSurviveFragmentCopies is the second half: the keys are set
// in the literal map BEFORE the maps.Copy calls, so a fragment gaining one of
// them later would overwrite the kind's version rather than collide visibly.
// Asserting the merged result, not the literal, is the point — this test sees
// what an author sees.
//
// Identity, not just presence: a clobbered key would still be present, just
// holding the wrong shape. Each raw shape is recognised by a member only it
// has.
func TestSchedulingKeysSurviveFragmentCopies(t *testing.T) {
	isRaw := map[string]func(oam.PropertySchema) bool{
		"affinity": func(s oam.PropertySchema) bool { _, ok := s.Properties["nodeAffinity"]; return ok },
		"tolerations": func(s oam.PropertySchema) bool {
			if s.Items == nil {
				return false
			}
			_, ok := s.Items.Properties["tolerationSeconds"]
			return ok
		},
		"topologySpreadConstraints": func(s oam.PropertySchema) bool {
			if s.Items == nil {
				return false
			}
			_, ok := s.Items.Properties["maxSkew"]
			return ok
		},
	}
	for _, k := range rawSchedulingKinds {
		s := k.schema()
		for _, key := range k.keys {
			got, found := s[key]
			if !found {
				t.Errorf("%s: PropertySchema() lost %q to a fragment copy", k.kind, key)
				continue
			}
			if !isRaw[key](got) {
				t.Errorf("%s: %q is not the raw corev1 shape after the fragment copies", k.kind, key)
			}
		}
	}
}

// TestOpinionatedKindsKeepAffinityShorthand pins the regression this ticket had
// to avoid. worker, statefulset and webservice each set the four-key shorthand
// in their own literal map and then copy schemaPodSpec over it, so publishing a
// raw `affinity` in that shared fragment would have replaced the shorthand on
// all three — and no golden fixture would have moved, because none authored
// affinity before this work.
func TestOpinionatedKindsKeepAffinityShorthand(t *testing.T) {
	cases := map[string]map[string]oam.PropertySchema{
		"worker":      WorkerRule{}.PropertySchema(),
		"statefulset": (&StatefulsetHandler{}).PropertySchema(),
		"webservice":  WebserviceRule{}.PropertySchema(),
	}
	for kind, s := range cases {
		affinity, found := s["affinity"]
		if !found {
			t.Errorf("%s: PropertySchema() no longer declares \"affinity\"", kind)
			continue
		}
		if _, isShorthand := affinity.Properties["enablePodAntiAffinity"]; !isShorthand {
			t.Errorf("%s: \"affinity\" is no longer the four-key shorthand — a shared fragment has clobbered it", kind)
		}
	}
}

// TestDocumentsMapsCopyOverwriteMechanism documents the maps.Copy behaviour the
// guards above depend on. It is NOT the regression oracle for them, despite an
// earlier name that said it was: it constrains no handler and no schema, and it
// keeps passing under every migration of the three keys into a shared fragment.
// The oracle is TestSchedulingKeysAbsentFromSharedFragments, which was proven
// to go red for all three keys across all four schemaPodSpec flag combinations.
//
// What this test is for is the "how do you know maps.Copy overwrites?" question
// the guards' comments assert an answer to. If the standard library ever stopped
// overwriting, those comments would be wrong and the guards would be defending a
// risk that does not exist — this fails first and says so.
func TestDocumentsMapsCopyOverwriteMechanism(t *testing.T) {
	// Exactly the shape worker.go and friends build: shorthand first, fragment
	// copied over it.
	m := map[string]oam.PropertySchema{"affinity": schemaAffinity()}
	if _, isShorthand := m["affinity"].Properties["enablePodAntiAffinity"]; !isShorthand {
		t.Fatal("precondition: schemaAffinity() is not the four-key shorthand")
	}

	hypotheticalFragment := map[string]oam.PropertySchema{"affinity": schemaRawAffinity()}
	maps.Copy(m, hypotheticalFragment)

	if _, stillShorthand := m["affinity"].Properties["enablePodAntiAffinity"]; stillShorthand {
		t.Fatal("maps.Copy did not overwrite the destination key — the ordering guards above are testing a risk that does not exist, and their comments are wrong")
	}
	if _, isRaw := m["affinity"].Properties["nodeAffinity"]; !isRaw {
		t.Error("expected the fragment's raw shape to have replaced the shorthand")
	}
}
