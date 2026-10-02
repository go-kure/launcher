package oam

import (
	"math"
	"strings"
	"testing"
)

// renderedMode is a rule's own named string type.
type renderedMode string

// renderedFlag is a rule's own named boolean type.
type renderedFlag bool

// TestSameRenderedValue: a recorded snapshot equals a current value exactly when the
// value is the same under the coercions validatePropertyValue applies — and numbers
// are compared exactly, never through float64 or JSON.
func TestSameRenderedValue(t *testing.T) {
	tests := []struct {
		name     string
		rendered any
		current  any
		want     bool
	}{
		{"int equals integral float", 1, 1.0, true},
		{"int equals int of another kind", int64(7), uint8(7), true},
		{"negative int equals negative float", int32(-3), -3.0, true},
		{"zero equals negative zero", 0, math.Copysign(0, -1), true},
		{"large int differs from the float JSON prints the same", int64(1000000000000000100), 1.0000000000000001e+18, false},
		{"float differs from the int JSON prints the same", 1.0000000000000001e+18, int64(1000000000000000100), false},
		{"float equals the int it is exactly", 1.0000000000000001e+18, int64(1000000000000000128), true},
		{"max uint64 differs from 2^64", uint64(math.MaxUint64), 18446744073709551616.0, false},
		{"float32 equals its exact float64", float32(0.5), 0.5, true},
		{"float32 differs from the nearest float64", float32(0.1), 0.1, false},
		{"fraction differs from its truncation", 2.5, 2, false},
		{"number differs from its string", 8443, "8443", false},
		{"named string equals string", renderedMode("platform"), "platform", true},
		{"string differs from bytes", "AQI=", []byte{1, 2}, false},
		{"bytes equal a list of the same integers", []byte{1, 2}, []any{1, 2}, true},
		{"bytes differ from their base64 string", []byte{1, 2}, "AQI=", false},
		{"named bool equals bool", renderedFlag(true), true, true},
		{"bool differs from its string", true, "true", false},
		{"typed list equals []any", []int32{1, 2}, []any{1, int64(2)}, true},
		{"array equals []any", [2]string{"a", "b"}, []any{"a", "b"}, true},
		{"list of another length", []int{1, 2}, []any{1, 2, 3}, false},
		{"list in another order", []int{1, 2}, []any{2, 1}, false},
		{"typed map equals map[string]any", map[renderedMode]int{"cpu": 2}, map[string]any{"cpu": 2.0}, true},
		{"map with an extra key", map[string]int{"cpu": 2}, map[string]any{"cpu": 2, "mem": 1}, false},
		{"map with another key", map[string]int{"cpu": 2}, map[string]any{"mem": 2}, false},
		{"nested collections", map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{1, 2}}, true},
		{"nested value changed", map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{1, 3}}, false},
		{"nested null item", map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{1, nil}}, false},
		{"nested typed nil for an empty object", map[string]any{"a": map[string]any{}}, map[string]any{"a": map[string]any(nil)}, false},
		{"nested typed nil for an empty list", map[string]any{"a": []any{}}, map[string]any{"a": []any(nil)}, false},
		{"null for a value", "platform", nil, false},
		{"list for an object", map[string]any{}, []any{}, false},
		{"object for a list", []any{}, map[string]any{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := snapshotRenderedValue(tc.rendered, map[propertyCopyKey]bool{})
			if err != nil {
				t.Fatalf("snapshotRenderedValue: %v", err)
			}
			if got := sameRenderedValue(snapshot, tc.current); got != tc.want {
				t.Fatalf("sameRenderedValue(%#v, %#v) = %v, want %v", snapshot, tc.current, got, tc.want)
			}
			// The value a rule rendered always matches its own snapshot.
			if !sameRenderedValue(snapshot, tc.rendered) {
				t.Fatalf("the rendered value %#v does not match its own snapshot %#v", tc.rendered, snapshot)
			}
		})
	}
}

// TestSnapshotRenderedValue_IsADeepCopy: the snapshot shares nothing with the value
// it was taken from, so changing that value afterwards is a different value.
func TestSnapshotRenderedValue_IsADeepCopy(t *testing.T) {
	value := map[string]any{"sources": []any{map[string]any{"namespace": "ingress"}}, "ports": []int{80}}
	snapshot, err := snapshotRenderedValue(value, map[propertyCopyKey]bool{})
	if err != nil {
		t.Fatalf("snapshotRenderedValue: %v", err)
	}
	value["sources"].([]any)[0].(map[string]any)["namespace"] = "anywhere"
	if sameRenderedValue(snapshot, value) {
		t.Fatal("a value changed through a nested map still matched the snapshot")
	}
	value["sources"].([]any)[0].(map[string]any)["namespace"] = "ingress"
	value["ports"].([]int)[0] = 443
	if sameRenderedValue(snapshot, value) {
		t.Fatal("a value changed through a typed slice still matched the snapshot")
	}
}

// TestSnapshotRenderedValue_Refuses: what is not a property value, or not a finite,
// null-free and finite-depth one, has no snapshot.
func TestSnapshotRenderedValue_Refuses(t *testing.T) {
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	cyclicList := []any{nil}
	cyclicList[0] = cyclicList
	s := "x"
	// A map reached twice, but not inside itself, is not a cycle.
	shared := map[string]any{"a": "b"}
	if _, err := snapshotRenderedValue(map[string]any{"one": shared, "two": []any{shared, shared}}, map[propertyCopyKey]bool{}); err != nil {
		t.Fatalf("a map reached twice was refused: %v", err)
	}
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"typed nil map", map[string]any(nil), "null"},
		{"nested typed nil slice", map[string]any{"a": []string(nil)}, "null"},
		{"nested nil pointer", []any{(*string)(nil)}, "null"},
		{"NaN", math.NaN(), "not a finite number"},
		{"float32 NaN", float32(math.NaN()), "not a finite number"},
		{"+Inf", math.Inf(1), "not a finite number"},
		{"nested -Inf", []float64{1, math.Inf(-1)}, "not a finite number"},
		{"pointer", &s, "not a property value"},
		{"struct", struct{ A string }{"x"}, "not a property value"},
		{"func", func() {}, "not a property value"},
		{"complex", complex(1, 2), "not a property value"},
		{"map with integer keys", map[int]any{1: "x"}, "keys are not strings"},
		{"map containing itself", cyclicMap, "contains itself"},
		{"list containing itself", cyclicList, "contains itself"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := snapshotRenderedValue(tc.value, map[propertyCopyKey]bool{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}
