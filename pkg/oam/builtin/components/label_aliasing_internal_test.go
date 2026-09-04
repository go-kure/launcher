package components

import (
	"reflect"
	"testing"
)

// TestSelectorBuilders_DoNotAliasTheCallerMap pins the boundary rule directly on
// the two builders that turn a label map into a selector: whatever a caller
// hands them, the selector that comes back owns its own copy.
//
// The end-to-end tests in label_aliasing_test.go cannot see this on their own —
// every call site now passes a freshly built map, so a builder that stored the
// caller's map would still produce output nothing else can reach. This is the
// test that keeps the guarantee true for the next call site, which may not be so
// careful.
func TestSelectorBuilders_DoNotAliasTheCallerMap(t *testing.T) {
	const key = "example.test/added-after"

	t.Run("buildTopologySpreadConstraints", func(t *testing.T) {
		callerMap := map[string]string{"app": "shop"}
		constraints := buildTopologySpreadConstraints(3, callerMap)
		if len(constraints) != 2 {
			t.Fatalf("got %d constraints at replicas=3, want 2", len(constraints))
		}
		callerMap[key] = "yes"
		for i, c := range constraints {
			if c.LabelSelector == nil {
				t.Fatalf("constraint %d has no label selector", i)
			}
			if _, leaked := c.LabelSelector.MatchLabels[key]; leaked {
				t.Errorf("constraint %d selector followed the caller's map: %v", i, c.LabelSelector.MatchLabels)
			}
		}
		if constraints[0].LabelSelector == constraints[1].LabelSelector {
			t.Error("both constraints point at one *metav1.LabelSelector")
		}
	})

	t.Run("buildAffinity", func(t *testing.T) {
		callerMap := map[string]string{"app": "shop"}
		affinity := buildAffinity(AffinityConfig{EnablePodAntiAffinity: true, TopologyKey: "kubernetes.io/hostname", PodAntiAffinityType: "preferred"}, callerMap)
		if affinity == nil || affinity.PodAntiAffinity == nil {
			t.Fatal("buildAffinity returned no pod anti-affinity")
		}
		terms := affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
		if len(terms) != 1 {
			t.Fatalf("got %d preferred terms, want 1", len(terms))
		}
		selector := terms[0].PodAffinityTerm.LabelSelector
		if selector == nil {
			t.Fatal("anti-affinity term has no label selector")
		}
		callerMap[key] = "yes"
		if _, leaked := selector.MatchLabels[key]; leaked {
			t.Errorf("anti-affinity selector followed the caller's map: %v", selector.MatchLabels)
		}
	})

	t.Run("appLabels", func(t *testing.T) {
		first := appLabels("shop")
		second := appLabels("shop")
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("appLabels is not deterministic: %v vs %v", first, second)
		}
		if reflect.ValueOf(first).Pointer() == reflect.ValueOf(second).Pointer() {
			t.Error("appLabels returned the same map twice, so two call sites would share it")
		}
	})
}
