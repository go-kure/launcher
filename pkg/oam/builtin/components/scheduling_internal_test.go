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

// TestSchedulingKeysAbsentFromSharedFragments is the first half of the ordering
// guard: the three raw scheduling keys must live in the deployment handler's own
// literal map and NOT in either fragment it merges. If one ever migrates into
// schemaPodSpec, this fails here rather than silently changing three other
// kinds — see TestFragmentCopyWouldClobberShorthand for what that would cost.
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
		"schemaDeploymentSpec()": schemaDeploymentSpec(),
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
				t.Errorf("%s declares %q; the raw scheduling shapes belong to the deployment kind's own map, not a shared fragment", name, key)
			}
		}
	}
}

// TestSchedulingKeysSurviveFragmentCopies is the second half: the keys are set
// in the literal map BEFORE the two maps.Copy calls, so a fragment gaining one
// of them later would overwrite the deployment kind's version rather than
// collide visibly. Asserting the merged result, not the literal, is the point —
// this test sees what an author sees.
func TestSchedulingKeysSurviveFragmentCopies(t *testing.T) {
	s := (&DeploymentHandler{}).PropertySchema()
	for _, key := range rawSchedulingKeys {
		if _, found := s[key]; !found {
			t.Errorf("PropertySchema() lost %q to a fragment copy", key)
		}
	}
	// Identity, not just presence: a clobbered `affinity` would still be
	// present, just holding the wrong shape.
	affinity, found := s["affinity"]
	if !found {
		t.Fatal(`PropertySchema() has no "affinity"`)
	}
	if _, isRaw := affinity.Properties["nodeAffinity"]; !isRaw {
		t.Error(`"affinity" is not the raw corev1.Affinity shape after the fragment copies`)
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
		"worker":      (&WorkerHandler{}).PropertySchema(),
		"statefulset": (&StatefulsetHandler{}).PropertySchema(),
		"webservice":  (&WebserviceHandler{}).PropertySchema(),
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

// TestFragmentCopyWouldClobberShorthand is the oracle for the two guards above:
// it demonstrates the failure they exist to catch actually happens, rather than
// trusting that maps.Copy behaves as claimed. Without this, both guards could
// pass against a mechanism that was never a risk.
func TestFragmentCopyWouldClobberShorthand(t *testing.T) {
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
