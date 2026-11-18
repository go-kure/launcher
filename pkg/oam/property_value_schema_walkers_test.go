package oam

import (
	"strings"
	"testing"
)

// Every walker that recurses into Items recurses into the value schema of an open
// object too (go-kure/launcher#1615). Each test below puts the fact the walker reads
// below a typed open key and checks the walker finds it there.

// appsOf is an object property mapping a free key to a value of the given schema.
func appsOf(value PropertySchema) map[string]PropertySchema {
	return map[string]PropertySchema{
		"apps": {Type: PropertyTypeObject, AdditionalPropertiesSchema: &value},
	}
}

func objectWithRequiredImage() PropertySchema {
	return PropertySchema{
		Type:       PropertyTypeObject,
		Properties: map[string]PropertySchema{"image": {Type: PropertyTypeString, Required: true}},
	}
}

func TestCheckNestedRequired_ReachesTheValueOfAnOpenKey(t *testing.T) {
	schema := appsOf(objectWithRequiredImage())

	err := checkNestedRequired(schema, nil, map[string]any{"apps": map[string]any{"web": map[string]any{}}}, "properties", "")
	pe := propertyErrorOf(t, err)
	if pe.Kind != PropertyErrorRequired {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorRequired, err)
	}
	if !strings.Contains(err.Error(), "properties.apps.web") {
		t.Errorf("the refusal must name the value's path, got: %v", err)
	}

	ok := map[string]any{"apps": map[string]any{"web": map[string]any{"image": "x"}}}
	if err := checkNestedRequired(schema, nil, ok, "properties", ""); err != nil {
		t.Errorf("a value that sets its required key must pass, got: %v", err)
	}
}

// The early return for an object with no declared Properties must not skip the value schema.
func TestCheckNestedRequired_ValueSchemaWithNoDeclaredProperties(t *testing.T) {
	inner := appsOf(PropertySchema{
		Type: PropertyTypeObject,
		AdditionalPropertiesSchema: &PropertySchema{
			Type:       PropertyTypeObject,
			Properties: map[string]PropertySchema{"image": {Type: PropertyTypeString, Required: true}},
		},
	})
	value := map[string]any{"apps": map[string]any{"web": map[string]any{"blue": map[string]any{}}}}
	err := checkNestedRequired(inner, nil, value, "properties", "")
	if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorRequired {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorRequired, err)
	}
}

func TestCheckNestedRequired_ElementsOfAnArrayUnderAnOpenKey(t *testing.T) {
	schema := appsOf(PropertySchema{Type: PropertyTypeArray, Items: ptrSchema(objectWithRequiredImage())})
	err := checkNestedRequired(schema, nil, map[string]any{"apps": map[string]any{"web": []any{map[string]any{}}}}, "properties", "")
	if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorRequired {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorRequired, err)
	}
}

// A null under an open key is absent, as a null array element is.
func TestCheckNestedRequired_NullUnderAnOpenKeyIsSkipped(t *testing.T) {
	schema := appsOf(objectWithRequiredImage())
	if err := checkNestedRequired(schema, nil, map[string]any{"apps": map[string]any{"web": nil}}, "properties", ""); err != nil {
		t.Errorf("a null value is absent, got: %v", err)
	}
}

// A declared key keeps its own schema and is not read as an open key.
func TestCheckNestedRequired_DeclaredKeyIsNotAnOpenKey(t *testing.T) {
	schema := appsOf(objectWithRequiredImage())
	apps := schema["apps"]
	apps.Properties = map[string]PropertySchema{"mode": {Type: PropertyTypeObject}}
	schema["apps"] = apps
	value := map[string]any{"apps": map[string]any{"mode": map[string]any{}}}
	if err := checkNestedRequired(schema, nil, value, "properties", ""); err != nil {
		t.Errorf("the declared key has its own schema, got: %v", err)
	}
}

func TestCheckFieldGroups_ReachesTheValueSchema(t *testing.T) {
	bad := PropertySchema{
		Type:       PropertyTypeObject,
		Properties: map[string]PropertySchema{"a": {Type: PropertyTypeString}},
		Exclusive:  []ExclusiveGroup{{Keys: []string{"a", "missing"}}},
	}
	err := checkFieldGroups(appsOf(bad)["apps"], "properties.apps")
	if pe := propertyErrorOf(t, err); pe.Kind != PropertyErrorSchema {
		t.Fatalf("kind = %q, want %q: %v", pe.Kind, PropertyErrorSchema, err)
	}
}

func ptrSchema(s PropertySchema) *PropertySchema { return &s }
