package oam

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// renderedMode is a rule's own named string type.
type renderedMode string

// renderedFlag is a rule's own named boolean type.
type renderedFlag bool

// renderedRatio is a rule's own named floating-point type.
type renderedRatio float64

// TestSameRenderedValue: a recorded snapshot equals a current value when the value is
// the snapshot itself, or what emission validation makes of it under the reserved
// key's schema — so a coercion counts only where validation performs it, and every
// value is compared with its Go type, numbers included.
func TestSameRenderedValue(t *testing.T) {
	var (
		untyped     = PropertySchema{}
		integer     = PropertySchema{Type: PropertyTypeInteger}
		number      = PropertySchema{Type: PropertyTypeNumber}
		str         = PropertySchema{Type: PropertyTypeString}
		boolean     = PropertySchema{Type: PropertyTypeBoolean}
		union       = PropertySchema{Types: []PropertyType{PropertyTypeInteger, PropertyTypeString}}
		integers    = PropertySchema{Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeInteger}}
		texts       = PropertySchema{Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}}
		list        = PropertySchema{Type: PropertyTypeArray}
		limits      = PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{"cpu": integer}}
		openObject  = PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true}
		nestedLists = PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{"a": integers}}
		negZero     = math.Copysign(0, -1)
	)
	tests := []struct {
		name     string
		field    PropertySchema
		rendered any
		current  any
		want     bool
	}{
		// Scalars: validation unnames a named type and makes an integer of a kind no
		// reader asserts an int; it keeps int, int32, int64, float64 and float32.
		{"integer: the same int", integer, 8443, 8443, true},
		{"integer: named type equals the int validation writes", integer, renderedPort(8443), 8443, true},
		{"integer: unsigned equals the int validation writes", integer, uint16(8443), 8443, true},
		{"integer: int64 is kept, so differs from int", integer, int64(8443), 8443, false},
		{"integer: int differs from an integral float", integer, 1, 1.0, false},
		{"integer: large int differs from the float JSON prints the same", integer, int64(1000000000000000100), 1.0000000000000001e+18, false},
		{"integer: float differs from the int JSON prints the same", integer, 1.0000000000000001e+18, int64(1000000000000000100), false},
		{"number: float32 is kept, so differs from float64", number, float32(0.5), 0.5, false},
		{"number: differs from its string", number, 8443, "8443", false},
		// Signed zeros compare equal with == but a handler tells them apart
		// (math.Signbit), so a floating-point number is compared bit for bit.
		{"number: positive zero differs from negative zero", number, 0.0, negZero, false},
		{"number: negative zero differs from positive zero", number, negZero, 0.0, false},
		{"number: negative zero equals itself", number, negZero, negZero, true},
		{"number: float32 negative zero differs from positive zero", number, float32(negZero), float32(0), false},
		{"number: named negative zero equals the float validation writes", number, renderedRatio(negZero), negZero, true},
		{"number: named negative zero differs from positive zero", number, renderedRatio(negZero), 0.0, false},
		{"integer: integral negative zero differs from positive zero", integer, negZero, 0.0, false},
		{"untyped: positive zero differs from negative zero", untyped, 0.0, negZero, false},
		{"string: named type equals string", str, renderedMode("platform"), "platform", true},
		{"string: differs from bytes", str, "AQI=", []byte{1, 2}, false},
		{"boolean: named type equals bool", boolean, renderedFlag(true), true, true},
		{"boolean: differs from its string", boolean, true, "true", false},
		{"union: unsigned equals the int its integer member writes", union, uint16(80), 80, true},
		{"union: named string equals string", union, renderedMode("x"), "x", true},
		{"untyped: named string is kept", untyped, renderedMode("platform"), "platform", false},
		{"untyped: named string equals itself", untyped, renderedMode("platform"), renderedMode("platform"), true},
		{"untyped: named bool is kept", untyped, renderedFlag(true), true, false},
		{"untyped: int64 is kept", untyped, int64(7), 7, false},
		// Collections: validation makes any slice or array []any and any string-keyed
		// map map[string]any, and recurses only into what the schema declares.
		{"array of integers: bytes equal the list of ints validation writes", integers, []byte{1, 2}, []any{1, 2}, true},
		{"array of integers: bytes differ from their base64 string", integers, []byte{1, 2}, "AQI=", false},
		{"array of integers: int32 items are kept", integers, []int32{1, 2}, []any{int32(1), int32(2)}, true},
		{"array of integers: int32 items differ from ints", integers, []int32{1, 2}, []any{1, 2}, false},
		{"array of strings: array equals []any", texts, [2]string{"a", "b"}, []any{"a", "b"}, true},
		{"array of integers: another length", integers, []int{1, 2}, []any{1, 2, 3}, false},
		{"array of integers: another order", integers, []int{1, 2}, []any{2, 1}, false},
		{"array without items: bytes keep their byte items", list, []byte{1, 2}, []any{uint8(1), uint8(2)}, true},
		{"array without items: bytes differ from a list of ints", list, []byte{1, 2}, []any{1, 2}, false},
		{"untyped: bytes are kept", untyped, []byte{1, 2}, []any{1, 2}, false},
		{"untyped: bytes equal themselves", untyped, []byte{1, 2}, []byte{1, 2}, true},
		{"untyped: array is kept", untyped, [2]int{1, 2}, []any{1, 2}, false},
		{"object: typed map equals the map validation writes", limits, map[renderedMode]int{"cpu": 2}, map[string]any{"cpu": 2}, true},
		{"object: declared child differs from an integral float", limits, map[string]int{"cpu": 2}, map[string]any{"cpu": 2.0}, false},
		{"object: nested collections", nestedLists, map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{int32(1), int32(2)}}, true},
		{"object: nested value changed", nestedLists, map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{int32(1), int32(3)}}, false},
		{"object: nested null item", nestedLists, map[string][]int32{"a": {1, 2}}, map[string]any{"a": []any{int32(1), nil}}, false},
		{"open object: undeclared bytes are kept", openObject, map[string]any{"payload": []byte{1, 2}}, map[string]any{"payload": []any{1, 2}}, false},
		{"open object: undeclared bytes equal themselves", openObject, map[string]any{"payload": []byte{1, 2}}, map[string]any{"payload": []byte{1, 2}}, true},
		{"open object: undeclared named integer is kept", openObject, map[string]any{"port": renderedPort(8443)}, map[string]any{"port": 8443}, false},
		{"open object: typed map becomes map[string]any, its items kept", openObject, map[string]int32{"cpu": 2}, map[string]any{"cpu": int32(2)}, true},
		{"open object: typed map items differ from ints", openObject, map[string]int32{"cpu": 2}, map[string]any{"cpu": 2}, false},
		{"array without items: a zero item differs from a negative zero", list, []float64{0}, []any{negZero}, false},
		{"array without items: a negative zero item equals itself", list, []float64{negZero}, []any{negZero}, true},
		{"open object: an undeclared zero differs from a negative zero", openObject, map[string]any{"r": 0.0}, map[string]any{"r": negZero}, false},
		{"open object: an extra key", openObject, map[string]int{"cpu": 2}, map[string]any{"cpu": 2, "mem": 1}, false},
		{"open object: another key", openObject, map[string]int{"cpu": 2}, map[string]any{"mem": 2}, false},
		{"open object: typed nil for an empty object", openObject, map[string]any{"a": map[string]any{}}, map[string]any{"a": map[string]any(nil)}, false},
		{"open object: typed nil for an empty list", openObject, map[string]any{"a": []any{}}, map[string]any{"a": []any(nil)}, false},
		// A null is absence, and a snapshot validation refuses matches only itself.
		{"null for a value", str, "platform", nil, false},
		{"untyped: list for an object", untyped, map[string]any{}, []any{}, false},
		{"untyped: object for a list", untyped, []any{}, map[string]any{}, false},
		{"refused by validation: equals itself", integer, "8443", "8443", true},
		{"refused by validation: differs from what the type would hold", integer, "8443", 8443, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := snapshotRenderedValue(tc.rendered)
			if err != nil {
				t.Fatalf("snapshotRenderedValue: %v", err)
			}
			if got := sameRenderedValue(tc.field, snapshot, tc.current); got != tc.want {
				t.Fatalf("sameRenderedValue(%#v, %#v) = %v, want %v", snapshot, tc.current, got, tc.want)
			}
			// The value a rule rendered always matches its own snapshot.
			if !sameRenderedValue(tc.field, snapshot, tc.rendered) {
				t.Fatalf("the rendered value %#v does not match its own snapshot %#v", tc.rendered, snapshot)
			}
		})
	}
}

// TestSameRenderedValue_LeavesTheRecordUnchanged: emission validation rewrites what it
// normalizes in place, and the comparison runs it on a copy, so comparing never
// rewrites the snapshot it reads — nor the current value it is handed.
func TestSameRenderedValue_LeavesTheRecordUnchanged(t *testing.T) {
	field := PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
		"codes": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeInteger}},
		"cpu":   {Type: PropertyTypeInteger},
	}}
	snapshot, err := snapshotRenderedValue(map[string]any{"codes": []any{uint16(1)}, "cpu": renderedPort(2)})
	if err != nil {
		t.Fatalf("snapshotRenderedValue: %v", err)
	}
	current := map[string]any{"codes": []any{1}, "cpu": 2}
	if !sameRenderedValue(field, snapshot, current) {
		t.Fatal("the value emission validation makes of the snapshot must match it")
	}
	want := map[string]any{"codes": []any{uint16(1)}, "cpu": renderedPort(2)}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("the comparison rewrote the snapshot: %#v, want %#v", snapshot, want)
	}
	if wantCurrent := map[string]any{"codes": []any{1}, "cpu": 2}; !reflect.DeepEqual(current, wantCurrent) {
		t.Fatalf("the comparison rewrote the current value: %#v, want %#v", current, wantCurrent)
	}
}

// TestSnapshotRenderedValue_IsADeepCopy: the snapshot keeps the Go types of the value
// it was taken from and shares nothing with it, so changing that value afterwards is
// a different value.
func TestSnapshotRenderedValue_IsADeepCopy(t *testing.T) {
	value := map[string]any{"sources": []any{map[string]any{"namespace": "ingress"}}, "ports": []int{80}, "codes": [1]byte{7}}
	snapshot, err := snapshotRenderedValue(value)
	if err != nil {
		t.Fatalf("snapshotRenderedValue: %v", err)
	}
	if !reflect.DeepEqual(snapshot, value) {
		t.Fatalf("the snapshot %#v does not keep the types of %#v", snapshot, value)
	}
	open := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true}
	value["sources"].([]any)[0].(map[string]any)["namespace"] = "anywhere"
	if sameRenderedValue(open, snapshot, value) {
		t.Fatal("a value changed through a nested map still matched the snapshot")
	}
	value["sources"].([]any)[0].(map[string]any)["namespace"] = "ingress"
	value["ports"].([]int)[0] = 443
	if sameRenderedValue(open, snapshot, value) {
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
	if _, err := snapshotRenderedValue(map[string]any{"one": shared, "two": []any{shared, shared}}); err != nil {
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
			_, err := snapshotRenderedValue(tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error mentioning %q, got: %v", tc.want, err)
			}
		})
	}
}
