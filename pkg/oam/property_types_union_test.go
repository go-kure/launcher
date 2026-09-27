package oam

import (
	"strings"
	"testing"
)

// intOrString is the union every Kubernetes intstr.IntOrString leaf publishes
// (go-kure/launcher#383).
var intOrString = PropertySchema{Types: []PropertyType{PropertyTypeInteger, PropertyTypeString}}

// TestValidatePropertyValue_TypesUnionAcceptsAnyMember: a value is accepted when
// it matches any one of the listed types, and each member normalizes the value
// exactly as the same single Type would.
func TestValidatePropertyValue_TypesUnionAcceptsAnyMember(t *testing.T) {
	type namedPercent string
	type namedCount uint16
	cases := []struct {
		name   string
		schema PropertySchema
		in     any
		want   any
	}{
		{"integer member takes an int", intOrString, 2, 2},
		{"integer member takes an integral float", intOrString, float64(2), float64(2)},
		{"integer member normalizes a named unsigned type to int", intOrString, namedCount(2), 2},
		{"string member takes a percentage", intOrString, "25%", "25%"},
		{"string member unnames a named string", intOrString, namedPercent("25%"), "25%"},
		{"number member takes a fraction", PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeNumber}}, 0.5, 0.5},
		{"string member of a quantity union", PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeNumber}}, "500m", "500m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validatePropertyValue(tc.schema, tc.in, "properties.leaf")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %T(%v), want %T(%v)", got, got, tc.want, tc.want)
			}
		})
	}
}

// TestValidatePropertyValue_TypesUnionRejectsNonMembers: a value matching none
// of the listed types fails at the schema layer, and the message names every
// member so an author sees both accepted forms.
func TestValidatePropertyValue_TypesUnionRejectsNonMembers(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
	}{
		{"boolean", true},
		{"fractional float", 2.5},
		{"array", []any{2}},
		{"object", map[string]any{"v": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePropertyValue(intOrString, tc.in, "properties.leaf")
			if err == nil {
				t.Fatalf("%T(%v) accepted, want a type error", tc.in, tc.in)
			}
			if !strings.Contains(err.Error(), "expected one of integer, string") {
				t.Errorf("error = %v, want it to name both members", err)
			}
		})
	}
}

// TestValidatePropertyValue_TypesUnionNullIsAbsent: the null contract is unchanged
// for a union leaf — an optional null constrains nothing.
func TestValidatePropertyValue_TypesUnionNullIsAbsent(t *testing.T) {
	if _, err := validatePropertyValue(intOrString, nil, "properties.leaf"); err != nil {
		t.Fatalf("null under an optional union leaf: %v", err)
	}
}

// TestValidatePropertyValue_TypesUnionWithEnum: Enum still applies after the
// union's type check.
func TestValidatePropertyValue_TypesUnionWithEnum(t *testing.T) {
	schema := PropertySchema{Types: []PropertyType{PropertyTypeInteger, PropertyTypeString}, Enum: []any{1, "all"}}
	for _, ok := range []any{1, "all"} {
		if _, err := validatePropertyValue(schema, ok, "properties.leaf"); err != nil {
			t.Errorf("%v: unexpected error: %v", ok, err)
		}
	}
	if _, err := validatePropertyValue(schema, 2, "properties.leaf"); err == nil {
		t.Error("2 accepted, want an Enum rejection")
	}
}

// TestValidatePropertyValue_MalformedTypesIsASchemaError: a schema that states its
// type two ways, or a union no value can be checked against unambiguously, is a
// handler bug and fails loudly, like an unsupported single Type.
func TestValidatePropertyValue_MalformedTypesIsASchemaError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema PropertySchema
		want   string
	}{
		{
			"Type and Types both set",
			PropertySchema{Type: PropertyTypeString, Types: []PropertyType{PropertyTypeInteger, PropertyTypeString}},
			"declares both type",
		},
		{
			"single-member union",
			PropertySchema{Types: []PropertyType{PropertyTypeInteger}},
			"at least two",
		},
		{
			"duplicate member",
			PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeString}},
			"more than once",
		},
		{
			"compound member",
			PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeObject}},
			"scalar",
		},
		{
			"unknown member",
			PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyType("strng")}},
			"scalar",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePropertyValue(tc.schema, "x", "properties.leaf")
			if err == nil || !strings.Contains(err.Error(), "schema declares") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a schema error containing %q", err, tc.want)
			}
		})
	}
}

// TestTypesUnion_AuthoredAndEmittedPathsAgree is the acceptance criterion of
// go-kure/launcher#383: the authored path (validateAuthoredProperties) and the
// emitted path (validateProperties) accept the same values for a union leaf —
// `2` and `"25%"` both — and reject the same non-members.
func TestTypesUnion_AuthoredAndEmittedPathsAgree(t *testing.T) {
	schema := map[string]PropertySchema{"maxUnavailable": intOrString}
	paths := map[string]func(map[string]any) error{
		"authored": func(p map[string]any) error { return validateAuthoredProperties(schema, p, "properties") },
		"emitted":  func(p map[string]any) error { return validateProperties(schema, p, "properties") },
	}
	for name, validate := range paths {
		for _, v := range []any{2, "25%"} {
			if err := validate(map[string]any{"maxUnavailable": v}); err != nil {
				t.Errorf("%s path rejected %T(%v): %v", name, v, v, err)
			}
		}
		if err := validate(map[string]any{"maxUnavailable": true}); err == nil {
			t.Errorf("%s path accepted a boolean, want a type error", name)
		}
	}
}
