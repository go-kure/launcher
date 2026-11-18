package oam

import (
	"strings"
	"testing"
)

// The gate walk reads the value schema of an open object as it reads Items
// (go-kure/launcher#1615): a gate declared below a typed open key refuses the value
// authored there, and fieldGateOff sees it when it decides whether a Go-built value
// needs reading.

func gatedValue() PropertySchema {
	return PropertySchema{
		Type: PropertyTypeObject,
		Properties: map[string]PropertySchema{
			"policy": {Type: PropertyTypeString, Gate: "ContainerRestartRules"},
		},
	}
}

func TestCheckGatedProperties_ReachesTheValueOfAnOpenKey(t *testing.T) {
	below := gateTarget{version: KubernetesVersion{Major: 1, Minor: 34}}
	at := gateTarget{version: KubernetesVersion{Major: 1, Minor: 35}}
	schema := appsOf(gatedValue())
	props := map[string]any{"apps": map[string]any{"web": map[string]any{"policy": "x"}}}

	err := checkGatedProperties(schema, nil, props, "properties", below)
	if err == nil || !strings.Contains(err.Error(), "properties.apps.web.policy") {
		t.Fatalf("want the gated field below the open key refused at its path, got: %v", err)
	}
	if err := checkGatedProperties(schema, nil, props, "properties", at); err != nil {
		t.Errorf("the gate is on at the release, got: %v", err)
	}
}

// A value schema on an object with no declared Properties still reaches the walk.
func TestCheckGatedProperties_ValueSchemaOnAnObjectWithNoProperties(t *testing.T) {
	below := gateTarget{version: KubernetesVersion{Major: 1, Minor: 34}}
	schema := appsOf(PropertySchema{Type: PropertyTypeObject, AdditionalPropertiesSchema: ptrSchema(gatedValue())})
	props := map[string]any{"apps": map[string]any{"web": map[string]any{"blue": map[string]any{"policy": "x"}}}}
	if err := checkGatedProperties(schema, nil, props, "properties", below); err == nil {
		t.Fatal("want the gated field two open keys down refused, got none")
	}
}

func TestFieldGateOff_ReachesTheValueSchema(t *testing.T) {
	below := gateTarget{version: KubernetesVersion{Major: 1, Minor: 34}}
	field := appsOf(gatedValue())["apps"]
	off, err := fieldGateOff(field, below)
	if err != nil {
		t.Fatalf("fieldGateOff: %v", err)
	}
	if !off {
		t.Error("a gate declared in the value schema is off below its release")
	}
}
