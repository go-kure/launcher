package oam

import (
	"strings"
	"testing"
)

// The capability rendering surface now follows the same null contract as the
// handler surface (property_validate.go): a value that serializes to JSON/YAML
// null is ABSENT. Before go-kure/launcher#431 a present null took the present
// branch and was handed to checkCapabilityValueType, which rejected it with
// "expected string, got <nil>" — a type error where the rest of the package
// reads a null as "not set".
//
// Each null case is tested in both shapes. An untyped nil is what a YAML
// `mode:` with no value decodes to; a typed nil is what Go construction
// produces, and Transformer.SetCapabilityDefs is an exported path that takes
// Go-built definitions and renderings straight past the loader.

func nullShapes() map[string]any {
	return map[string]any{
		"untyped nil": nil,
		"typed nil":   map[string]any(nil),
	}
}

func capDefWithProps(props map[string]PropertySchema) *CapabilityDefinition {
	return &CapabilityDefinition{
		Spec: CapabilityDefSpec{Rendering: CapabilityRenderingSchema{Properties: props}},
	}
}

func TestApplyDefinitionSchema_NullTakesTheDeclaredDefault(t *testing.T) {
	def := capDefWithProps(map[string]PropertySchema{
		"mode": {Type: "string", Default: "auto"},
	})

	for shape, value := range nullShapes() {
		t.Run(shape, func(t *testing.T) {
			result, err := applyDefinitionSchema(map[string]any{"mode": value}, def)
			if err != nil {
				t.Fatalf("a null must read as absent and take the default, got error: %v", err)
			}
			if result["mode"] != "auto" {
				t.Errorf("mode = %v, want %q", result["mode"], "auto")
			}
		})
	}
}

func TestApplyDefinitionSchema_NullOnRequiredReportsMissing(t *testing.T) {
	// The message matters as much as the rejection: both states are errors, but a
	// null used to be reported as a TYPE error, which sends the reader looking for
	// a wrongly-typed value that does not exist.
	def := capDefWithProps(map[string]PropertySchema{
		"timeout": {Type: "integer", Required: true},
	})

	for shape, value := range nullShapes() {
		t.Run(shape, func(t *testing.T) {
			_, err := applyDefinitionSchema(map[string]any{"timeout": value}, def)
			if err == nil {
				t.Fatal("a null on a required property must be rejected as missing")
			}
			if !strings.Contains(err.Error(), "is missing") {
				t.Errorf("error = %q, want it to report the property as missing rather than mistyped", err)
			}
		})
	}
}

func TestApplyDefinitionSchema_NullWithNoDefaultLeavesTheKeyAbsent(t *testing.T) {
	// Nothing replaces the null, so the key must not survive: a caller reading the
	// result would otherwise find the property PRESENT after validation decided it
	// was absent, which is the disagreement this contract exists to prevent.
	def := capDefWithProps(map[string]PropertySchema{
		"mode": {Type: "string"},
	})

	for shape, value := range nullShapes() {
		t.Run(shape, func(t *testing.T) {
			result, err := applyDefinitionSchema(map[string]any{"mode": value}, def)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v, present := result["mode"]; present {
				t.Errorf("mode survived as %v; a null with no default must leave the key absent", v)
			}
		})
	}
}

func TestApplyDefinitionSchema_NullDefaultIsNoDefault(t *testing.T) {
	// `default:` with no value declares no default, so an absent property stays
	// absent rather than being filled with a null.
	def := capDefWithProps(map[string]PropertySchema{
		"mode": {Type: "string", Default: map[string]any(nil)},
	})

	result, err := applyDefinitionSchema(map[string]any{}, def)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, present := result["mode"]; present {
		t.Errorf("mode = %v; a null default must not be applied", v)
	}
}

func TestApplyDefinitionSchema_NonNullValueStillTypeChecked(t *testing.T) {
	// Control for every test above. A parser that made everything absent would
	// pass all of them; this one fails unless the type check still runs on a real
	// value.
	def := capDefWithProps(map[string]PropertySchema{
		"mode": {Type: "string", Default: "auto"},
	})

	if _, err := applyDefinitionSchema(map[string]any{"mode": 5}, def); err == nil {
		t.Fatal("a wrongly-typed value must still be rejected; the null handling must not have swallowed the type check")
	}
}

func TestCheckCapabilityValueType_UnsupportedTypeIsRejected(t *testing.T) {
	// Before #431 this switch had no default arm and fell off the end returning
	// nil, so a property declaring any type outside the flat vocabulary accepted
	// EVERY value.
	for _, typeName := range []string{"array", "object", "number", "String"} {
		t.Run(typeName, func(t *testing.T) {
			if err := checkCapabilityValueType("anything at all", typeName); err == nil {
				t.Fatalf("type %q accepted a value; an unsupported type must be an error, not silent acceptance", typeName)
			}
		})
	}
}

func TestCheckCapabilityValueType_UntypedAcceptsAnything(t *testing.T) {
	// Control for the test above, and the reason the default arm cannot simply be
	// "anything not string/integer/boolean is an error": an empty type name means
	// the property declares no type, which the loader permits and both call sites
	// treat as "accept anything".
	for _, v := range []any{"s", 5, true, map[string]any{"k": "v"}, nil} {
		if err := checkCapabilityValueType(v, ""); err != nil {
			t.Errorf("untyped property rejected %v: %v", v, err)
		}
	}
}

func TestEvaluateProfile_UnsupportedCapabilityTypeIsRejected(t *testing.T) {
	// Reachability, through the exported entry point rather than the unexported
	// switch. SetCapabilityDefs replaces the definition set wholesale and bypasses
	// LoadCapabilityDefinitions' accepted-type check, so a hand-built definition is
	// the only way a bad type string reaches checkCapabilityValueType — and it is a
	// supported, exported thing for a caller to do.
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(customVADLoweringRule{typ: "custom-lowering"})
	tr.SetCapabilityDefs(map[string]*CapabilityDefinition{
		"custom-lowering": capDefWithProps(map[string]PropertySchema{
			"peers": {Type: "array"},
		}),
	})

	profile := &ClusterProfile{
		Spec: ClusterProfileSpec{
			Capabilities: map[string]CapabilityBinding{
				"custom-lowering": {Rendering: map[string]any{"peers": "not an array, and never checked before"}},
			},
		},
	}
	if _, err := tr.EvaluateProfile(profile); err == nil {
		t.Fatal("a definition declaring an unsupported property type must be rejected when its rendering is applied")
	}
}
