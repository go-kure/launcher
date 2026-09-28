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

// TestValidatePropertyValue_TypesUnionDoesNotDependOnMemberOrder: a value is
// accepted when ANY member validates it, not only the first member whose kind it
// matches. 2^63 as a uint64 is integer-kinded but has no int representation, so the
// integer member refuses it; the number member takes it, exactly as Type: number
// does, whichever order the two are declared in.
func TestValidatePropertyValue_TypesUnionDoesNotDependOnMemberOrder(t *testing.T) {
	big := uint64(1) << 63
	want, err := validatePropertyValue(PropertySchema{Type: PropertyTypeNumber}, big, "properties.leaf")
	if err != nil {
		t.Fatalf("Type: number refused %d: %v", big, err)
	}
	for _, types := range [][]PropertyType{
		{PropertyTypeInteger, PropertyTypeNumber},
		{PropertyTypeNumber, PropertyTypeInteger},
	} {
		t.Run(joinPropertyTypes(types), func(t *testing.T) {
			got, err := validatePropertyValue(PropertySchema{Types: types}, big, "properties.leaf")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %T(%v), want %T(%v) as Type: number normalizes it", got, got, want, want)
			}
		})
	}
}

// TestValidatePropertyValue_TypesUnionReportsTheKindMatchingMembersError: when the
// only members whose kind the value matches all refuse it, the error is the first
// such member's own — here the integer range error — rather than the generic
// "expected one of", which would hide the real reason.
func TestValidatePropertyValue_TypesUnionReportsTheKindMatchingMembersError(t *testing.T) {
	_, err := validatePropertyValue(intOrString, uint64(1)<<63, "properties.leaf")
	if err == nil {
		t.Fatal("2^63 accepted under integer/string, want the integer member's range error")
	}
	if !strings.Contains(err.Error(), "out of range") || strings.Contains(err.Error(), "expected one of") {
		t.Errorf("error = %v, want the integer member's range error", err)
	}
}

// TestValidatePropertyValue_NumberAcceptsUintptr: uintptr is a Go integer kind like
// any other, and IntegerValue — which every quantity parser reads a bare number
// through — accepts it, so a number (and so a string/number quantity union) must
// accept it too. A named uintptr is unnamed like every other named number.
func TestValidatePropertyValue_NumberAcceptsUintptr(t *testing.T) {
	type namedPtr uintptr
	quantity := PropertySchema{Types: []PropertyType{PropertyTypeString, PropertyTypeNumber}}
	for _, tc := range []struct {
		name   string
		schema PropertySchema
		in     any
	}{
		{"number", PropertySchema{Type: PropertyTypeNumber}, uintptr(2)},
		{"named uintptr under number", PropertySchema{Type: PropertyTypeNumber}, namedPtr(2)},
		{"quantity union", quantity, uintptr(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validatePropertyValue(tc.schema, tc.in, "properties.leaf")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != uintptr(2) {
				t.Errorf("got %T(%v), want uintptr(2)", got, got)
			}
		})
	}
}

// TestValidatePropertyValue_NumberEnumComparesUintptr: an Enum on a number leaf
// compares a uintptr by value, whichever side holds it.
func TestValidatePropertyValue_NumberEnumComparesUintptr(t *testing.T) {
	for _, tc := range []struct {
		name  string
		enum  []any
		value any
	}{
		{"uintptr value, int member", []any{2}, uintptr(2)},
		{"int value, uintptr member", []any{uintptr(2)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := PropertySchema{Type: PropertyTypeNumber, Enum: tc.enum}
			if _, err := validatePropertyValue(schema, tc.value, "properties.leaf"); err != nil {
				t.Errorf("unexpected error: %v", err)
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
