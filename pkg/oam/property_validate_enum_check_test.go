package oam

import (
	"strings"
	"testing"
)

// TestCheckEnumMembersHoldNoNull_CleanSchemaPasses: scalar and compound enums with no
// null anywhere, at the top level and nested under Properties and Items, report
// nothing.
func TestCheckEnumMembersHoldNoNull_CleanSchemaPasses(t *testing.T) {
	schema := PropertySchema{
		Type: PropertyTypeObject,
		Enum: []any{map[string]any{"mode": "fast", "tags": []any{"a", "b"}}},
		Properties: map[string]PropertySchema{
			"mode": {Type: PropertyTypeString, Enum: []any{"fast", "slow"}},
			"ports": {Type: PropertyTypeArray, Items: &PropertySchema{
				Type: PropertyTypeObject,
				Properties: map[string]PropertySchema{
					"protocol": {Type: PropertyTypeString, Enum: []any{"TCP", "UDP"}},
				},
			}},
			// An empty collection is not a null.
			"empty": {Type: PropertyTypeObject, Enum: []any{map[string]any{}, []any{}}},
		},
	}
	if err := CheckEnumMembersHoldNoNull(schema); err != nil {
		t.Fatalf("a schema with no null in any Enum member must pass, got: %v", err)
	}
	if err := CheckEnumMembersHoldNoNull(PropertySchema{}); err != nil {
		t.Fatalf("an empty schema must pass, got: %v", err)
	}
}

// TestCheckEnumMembersHoldNoNull_FindsPlantedNull is the positive control: a member
// holding a null is found wherever it is declared and wherever inside the member the
// null sits, and is named by its schema path and index.
func TestCheckEnumMembersHoldNoNull_FindsPlantedNull(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema PropertySchema
		want   string
	}{
		{
			name:   "untyped nil member at the top level",
			schema: PropertySchema{Type: PropertyTypeString, Enum: []any{"a", nil}},
			want:   "Enum member 1",
		},
		{
			name: "null inside a member, nested via Properties",
			schema: PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
				"spec": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
					"mode": {Type: PropertyTypeObject, Enum: []any{map[string]any{"x": nil}}},
				}},
			}},
			want: "spec.mode: Enum member 0",
		},
		{
			name: "null element in a member, nested via Items",
			schema: PropertySchema{Type: PropertyTypeArray, Items: &PropertySchema{
				Type: PropertyTypeArray, Enum: []any{[]any{"a"}, []any{"b", nil}},
			}},
			want: "[]: Enum member 1",
		},
		{
			name: "via Properties then Items",
			schema: PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
				"ports": {Type: PropertyTypeArray, Items: &PropertySchema{
					Type: PropertyTypeObject, Enum: []any{map[string]any{"a": []any{map[string]any{"b": nil}}}},
				}},
			}},
			want: "ports[]: Enum member 0",
		},
		{
			name:   "typed nil map member",
			schema: PropertySchema{Type: PropertyTypeObject, Enum: []any{map[string]any(nil)}},
			want:   "Enum member 0",
		},
		{
			name:   "typed nil slice member",
			schema: PropertySchema{Type: PropertyTypeArray, Enum: []any{[]any(nil)}},
			want:   "Enum member 0",
		},
		{
			name:   "typed nil nested inside a typed collection",
			schema: PropertySchema{Type: PropertyTypeObject, Enum: []any{map[string][]string{"a": nil}}},
			want:   "Enum member 0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckEnumMembersHoldNoNull(tc.schema)
			want := "schema declares an Enum member holding a null: " + tc.want
			if err == nil || err.Error() != want {
				t.Fatalf("expected %q, got: %v", want, err)
			}
		})
	}
}

// TestCheckEnumMembersHoldNoNull_StricterThanRuntime pins the documented difference
// from the runtime Enum arm: a null under a key the object leaves to
// AdditionalProperties is matchable, so validatePropertyValue accepts the schema and a
// value equal to the member (go-kure/launcher#481), while this check still reports it.
func TestCheckEnumMembersHoldNoNull_StricterThanRuntime(t *testing.T) {
	schema := PropertySchema{
		Type:                 PropertyTypeObject,
		AdditionalProperties: true,
		Enum:                 []any{map[string]any{"opaque": nil}},
	}
	if _, err := validatePropertyValue(schema, map[string]any{"opaque": nil}, "properties.choice"); err != nil {
		t.Fatalf("precondition: the runtime arm must admit this member, got: %v", err)
	}
	err := CheckEnumMembersHoldNoNull(schema)
	if err == nil || !strings.Contains(err.Error(), "Enum member 0") {
		t.Fatalf("the static check must report a null the runtime admits, got: %v", err)
	}
}

// TestCheckEnumMembersHoldNoNull_ReportsEveryMemberInOrder: every offending member is
// reported, not only the first, with Properties keys in sorted order so the report
// does not depend on map iteration.
func TestCheckEnumMembersHoldNoNull_ReportsEveryMemberInOrder(t *testing.T) {
	schema := PropertySchema{
		Type: PropertyTypeObject,
		Enum: []any{nil},
		Properties: map[string]PropertySchema{
			"zeta":  {Type: PropertyTypeString, Enum: []any{"ok", nil}},
			"alpha": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString, Enum: []any{nil}}},
		},
	}
	want := "schema declares 3 Enum members holding a null:\n" +
		"  Enum member 0\n" +
		"  alpha[]: Enum member 0\n" +
		"  zeta: Enum member 1"
	for range 20 {
		err := CheckEnumMembersHoldNoNull(schema)
		if err == nil || err.Error() != want {
			t.Fatalf("expected %q, got: %v", want, err)
		}
	}
}

// TestCheckEnumMembersHoldNoNull_DepthBound: a member nesting past the validator's
// depth bound counts as holding a null, as it does at runtime, while one nesting within
// it and holding none passes.
func TestCheckEnumMembersHoldNoNull_DepthBound(t *testing.T) {
	nest := func(levels int) any {
		var v any = "leaf"
		for range levels {
			v = []any{v}
		}
		return v
	}
	if err := CheckEnumMembersHoldNoNull(PropertySchema{Enum: []any{nest(enumMemberMaxDepth - 1)}}); err != nil {
		t.Fatalf("a null-free member within the depth bound must pass, got: %v", err)
	}
	err := CheckEnumMembersHoldNoNull(PropertySchema{Enum: []any{nest(enumMemberMaxDepth)}})
	if err == nil || !strings.Contains(err.Error(), "Enum member 0") {
		t.Fatalf("a member past the depth bound must count as holding a null, got: %v", err)
	}
}
