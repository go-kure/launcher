package oam

import "testing"

// IsNullValue is a public API surface — parsers outside this package classify
// nulls with it, and pkg/oam/builtin/traits now routes an entire NetworkPolicy
// peer through it — but until this file it was only exercised transitively, by
// tests whose subject was a validator or a parser rather than the predicate. That
// left the shapes those tests happen not to author unpinned: a nil pointer, a nil
// channel and a nil func are named in the implementation's reflect.Kind switch and
// were covered by nothing (go-kure/launcher#430).
//
// Both names are tested. IsNullValue must stay a pure delegation to isNullValue:
// if the wrapper ever grows behaviour of its own, the two columns diverge here
// rather than in a caller.

func TestIsNullValue(t *testing.T) {
	type namedPointer *int
	var nilChan chan int
	var nilFunc func()
	var nilPointer *string
	var nilNamed namedPointer

	// A nil INTERFACE, which is a different thing from a nil value inside one:
	// converting it to `any` carries no dynamic type, so these two reach
	// isNullValue as an untyped nil and are caught by its first line, never by the
	// reflect.Kind switch. Kept as rows because the distinction is exactly what
	// the contract is about, and because reflect.Interface appears in that switch
	// while being unreachable through an ordinary conversion — the shapes that DO
	// exercise it as a non-nil interface holding a nil value are the pointer,
	// map, slice, chan and func rows below.
	var nilError error
	var nilStringer interface{ String() string }

	for _, tc := range []struct {
		name  string
		value any
		null  bool
	}{
		// Null: every shape that serializes to null, or that a Go-constructed
		// property map can leave unset.
		{"untyped nil", nil, true},
		{"nil map", map[string]any(nil), true},
		{"nil typed map", map[string]string(nil), true},
		{"nil slice", []any(nil), true},
		{"nil typed slice", []string(nil), true},
		{"nil pointer", nilPointer, true},
		{"nil named pointer", nilNamed, true},
		{"nil channel", nilChan, true},
		{"nil func", nilFunc, true},
		{"nil interface value", nilError, true},
		{"nil interface method set", nilStringer, true},

		// Not null: present values, including every empty-but-allocated
		// collection. This half is the control — a predicate that returned true
		// for everything falsy would pass the rows above and silently turn every
		// authored empty object into an absent one.
		{"empty map", map[string]any{}, false},
		{"empty slice", []any{}, false},
		{"empty string", "", false},
		{"zero int", 0, false},
		{"false", false, false},
		{"non-nil pointer", new(string), false},
		{"populated map", map[string]any{"a": 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNullValue(tc.value); got != tc.null {
				t.Errorf("IsNullValue(%#v) = %v, want %v", tc.value, got, tc.null)
			}
			if got := isNullValue(tc.value); got != tc.null {
				t.Errorf("isNullValue(%#v) = %v, want %v", tc.value, got, tc.null)
			}
		})
	}
}

func TestIsNullValue_ArrayIsNeverNull(t *testing.T) {
	// A fixed-size array has no nil form, so it must not reach the Kind switch's
	// nil branch. Pinned because reflect.Array sits next to reflect.Slice and an
	// edit that widened the case list would silently make a zero-length array
	// read as an absent property.
	if IsNullValue([0]string{}) {
		t.Error("a zero-length array is a present value, not null")
	}
}
