package components

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
)

// TestHorizontalPodAutoscalerSpec_NoOmittedZeros: the horizontalpodautoscaler
// kind passes no defaulted-zero list to refuseUncarriedSpecValues, so the spec
// type may hold no field on which an authored 0 or false would be dropped: a
// non-pointer omitempty number or boolean, at any depth. At k8s.io/api v0.37.1
// it holds none: every optional number is a pointer, and maxReplicas and a
// scaling policy's value and periodSeconds are always written. A dependency
// bump that adds one fails here, naming it; the kind then needs the list.
func TestHorizontalPodAutoscalerSpec_NoOmittedZeros(t *testing.T) {
	// Vacuity guard: the walk finds such a field where the object has one, in
	// its status.
	whole := omitemptyScalarDocs(t, reflect.TypeFor[autoscalingv2.HorizontalPodAutoscaler](), "metadata")
	if _, ok := whole["status.currentReplicas"]; !ok {
		t.Fatalf("the walk found %v, want status.currentReplicas among them; the reflection walk is broken", slices.Sorted(maps.Keys(whole)))
	}
	found := omitemptyScalarDocs(t, reflect.TypeFor[autoscalingv2.HorizontalPodAutoscalerSpec]())
	for _, path := range slices.Sorted(maps.Keys(found)) {
		t.Errorf("%s is omitted when zero: an authored 0 or false would not reach the object; the kind needs a defaulted-zero list or a refusal", path)
	}
}
