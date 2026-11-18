package oam

import (
	stderrors "errors"
	"strings"
	"testing"
)

// A map from a free key to a typed value, for example {"group": ["name", ...]}, is
// published as an object whose AdditionalPropertiesSchema types every key it leaves
// open (go-kure/launcher#1615). These tests pin what validatePropertyValue does with
// such a node; the walkers that read it elsewhere have their own tests.

// listsOfStrings is the issue's own case: a map from a group name to a list of names.
func listsOfStrings() PropertySchema {
	return PropertySchema{
		Type: PropertyTypeObject,
		AdditionalPropertiesSchema: &PropertySchema{
			Type:  PropertyTypeArray,
			Items: &PropertySchema{Type: PropertyTypeString},
		},
	}
}

// propertyErrorOf finds the PropertyError in err, failing the test where there is none.
func propertyErrorOf(t *testing.T, err error) *PropertyError {
	t.Helper()
	var pe *PropertyError
	if !stderrors.As(err, &pe) {
		t.Fatalf("expected a *PropertyError, got %T: %v", err, err)
	}
	return pe
}

func TestValidatePropertyValue_ValueSchema_AcceptsTypedValues(t *testing.T) {
	value := map[string]any{"web": []any{"a", "b"}, "db": []any{}}
	if _, err := validatePropertyValue(listsOfStrings(), value, "properties.groups"); err != nil {
		t.Fatalf("a map of string lists must be accepted, got: %v", err)
	}
}

func TestValidatePropertyValue_ValueSchema_RefusesAWrongValueType(t *testing.T) {
	_, err := validatePropertyValue(listsOfStrings(), map[string]any{"web": "a"}, "properties.groups")
	pe := propertyErrorOf(t, err)
	if pe.Kind != PropertyErrorType {
		t.Fatalf("kind = %q, want %q (an undeclared-key refusal would mean the key was never typed): %v", pe.Kind, PropertyErrorType, err)
	}
	if !strings.Contains(err.Error(), "properties.groups.web") {
		t.Fatalf("the refusal must name the value's path, got: %v", err)
	}
}

func TestValidatePropertyValue_ValueSchema_RefusesAWrongElementType(t *testing.T) {
	_, err := validatePropertyValue(listsOfStrings(), map[string]any{"web": []any{"a", 1}}, "properties.groups")
	pe := propertyErrorOf(t, err)
	if pe.Kind != PropertyErrorType {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorType, err)
	}
	if !strings.Contains(err.Error(), "properties.groups.web[1]") {
		t.Fatalf("the refusal must name the element's path, got: %v", err)
	}
}

// A value schema opens the object whatever the bool says: the bool is the shorthand
// for "any value", the schema the typed form.
func TestValidatePropertyValue_ValueSchema_OpensAnObjectTheBoolLeavesClosed(t *testing.T) {
	schema := listsOfStrings()
	schema.AdditionalProperties = false
	if _, err := validatePropertyValue(schema, map[string]any{"web": []any{"a"}}, "properties.groups"); err != nil {
		t.Fatalf("a value schema must open the object, got: %v", err)
	}
}

// A declared key keeps its own schema; every other key takes the value schema.
func TestValidatePropertyValue_ValueSchema_DeclaredKeysKeepTheirOwnSchema(t *testing.T) {
	schema := listsOfStrings()
	schema.Properties = map[string]PropertySchema{"mode": {Type: PropertyTypeString}}

	if _, err := validatePropertyValue(schema, map[string]any{"mode": "fast", "web": []any{"a"}}, "properties.groups"); err != nil {
		t.Fatalf("a declared key and a typed open key must both be accepted, got: %v", err)
	}
	_, err := validatePropertyValue(schema, map[string]any{"mode": []any{"a"}}, "properties.groups")
	if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorType {
		t.Fatalf("the declared key must keep its string schema, got kind %q: %v", pe.Kind, err)
	}
}

// A null under a key a value schema types is stripped, as a declared optional key's
// null is: it must not reach a handler as a nil in the map.
func TestValidatePropertyValue_ValueSchema_StripsANull(t *testing.T) {
	value := map[string]any{"web": nil, "db": []any{"a"}}
	got, err := validatePropertyValue(listsOfStrings(), value, "properties.groups")
	if err != nil {
		t.Fatalf("a null under a typed open key must be accepted as absent, got: %v", err)
	}
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected the object back, got %T", got)
	}
	if _, present := obj["web"]; present {
		t.Errorf("the null key must be stripped, got %v", obj)
	}
	if _, present := obj["db"]; !present {
		t.Errorf("the typed key must stay, got %v", obj)
	}
}

// A bool-only open object keeps today's behaviour: a null under an open key is left alone.
func TestValidatePropertyValue_BoolOnlyOpenObjectLeavesANullAlone(t *testing.T) {
	schema := PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true}
	got, err := validatePropertyValue(schema, map[string]any{"web": nil}, "properties.groups")
	if err != nil {
		t.Fatalf("got: %v", err)
	}
	if _, present := got.(map[string]any)["web"]; !present {
		t.Errorf("the bool-only open path must leave a null alone, got %v", got)
	}
}

// A value schema may itself hold a required key; the value of each present open key is checked.
func TestValidatePropertyValue_ValueSchema_ChecksRequiredInTheValue(t *testing.T) {
	schema := PropertySchema{
		Type: PropertyTypeObject,
		AdditionalPropertiesSchema: &PropertySchema{
			Type:       PropertyTypeObject,
			Properties: map[string]PropertySchema{"image": {Type: PropertyTypeString, Required: true}},
		},
	}
	_, err := validatePropertyValue(schema, map[string]any{"web": map[string]any{}}, "properties.apps")
	if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorRequired {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorRequired, err)
	}
}

// A value schema on a node that is not an object is a defect in the handler's schema.
func TestValidatePropertyValue_ValueSchema_OnANonObjectIsASchemaError(t *testing.T) {
	for _, typ := range []PropertyType{PropertyTypeString, PropertyTypeArray, PropertyTypeInteger} {
		schema := PropertySchema{Type: typ, AdditionalPropertiesSchema: &PropertySchema{Type: PropertyTypeString}}
		var value any = "x"
		switch typ {
		case PropertyTypeArray:
			value = []any{"x"}
		case PropertyTypeInteger:
			value = 1
		}
		_, err := validatePropertyValue(schema, value, "properties.field")
		if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorSchema {
			t.Errorf("type %s: kind = %q, want %q: %v", typ, pe.Kind, PropertyErrorSchema, err)
		}
	}
}
