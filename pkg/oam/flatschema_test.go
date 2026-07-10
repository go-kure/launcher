package oam

import (
	stderrors "errors"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// --- kurel parameters: rich fields rejected by key presence (adr#33) ---

// packageWithParamField builds a Package document with a single string parameter
// carrying one extra field line (e.g. `enum: []`).
func packageWithParamField(extra string) string {
	return `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - name: mode
    type: string
    ` + extra + `
`
}

func TestParsePackage_RejectsRichParamFields(t *testing.T) {
	// Includes zero-value cases (enum: [], properties: {}, items: null,
	// additionalProperties: false) that a decoded-value check could not distinguish
	// from "omitted". They must be rejected by key presence at decode time.
	cases := map[string]string{
		"enum":                       `enum: ["dev", "prod"]`,
		"enum-empty":                 `enum: []`,
		"properties-empty":           `properties: {}`,
		"items-null":                 `items: null`,
		"additionalProperties-false": `additionalProperties: false`,
		"additionalProperties-true":  `additionalProperties: true`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePackage([]byte(packageWithParamField(extra)))
			if err == nil {
				t.Fatalf("expected error for kurel param with %s, got nil", name)
			}
			var parseErr *errors.ParseError
			if !stderrors.As(err, &parseErr) {
				t.Errorf("expected *errors.ParseError, got %T: %v", err, err)
			}
		})
	}
}

func TestParsePackage_RejectsUnknownParamField(t *testing.T) {
	// An unknown/misspelled field is no longer caught by KnownFields (the custom
	// unmarshaler bypasses it); the allow-set must reproduce that strictness.
	_, err := ParsePackage([]byte(packageWithParamField("bogusfield: x")))
	if err == nil {
		t.Fatal("expected error for unknown param field, got nil")
	}
	var parseErr *errors.ParseError
	if !stderrors.As(err, &parseErr) {
		t.Errorf("expected *errors.ParseError, got %T: %v", err, err)
	}
}

func TestParsePackage_NamelessParamItem_ValidationError(t *testing.T) {
	// An empty-mapping list item (`- {}`) passes the key guard (no keys) and decodes
	// to a zero ParameterDecl, which validatePackage rejects as "name is required" —
	// a ValidationError, not a ParseError. This proves the guard forwards a nameless
	// param to semantic validation rather than turning it into a parse error.
	// (A truly null item, `- null`, is dropped entirely by yaml.v3 both before and
	// after this change — preserved behavior, nothing to assert.)
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - {}
`
	_, err := ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected error for nameless parameter list item, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Fatalf("expected *errors.ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention the missing name, got: %v", err)
	}
}

func TestParsePackage_ResolvesParamAlias(t *testing.T) {
	// An aliased parameter item (`- *base`) must resolve to its mapping and decode
	// like plain yaml.v3 — reaching semantic validation (here: duplicate name),
	// not being rejected as a parse error for "not a mapping".
	input := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: Package
metadata:
  name: my-app
spec:
  parameters:
  - &base
    name: a
    type: string
  - *base
`
	_, err := ParsePackage([]byte(input))
	if err == nil {
		t.Fatal("expected duplicate-name error for aliased param, got nil")
	}
	var valErr *errors.ValidationError
	if !stderrors.As(err, &valErr) {
		t.Fatalf("expected *errors.ValidationError (proving the alias decoded), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate-name error, got: %v", err)
	}
}

// --- capability rendering: rich fields rejected at both levels ---

// loadCapDef writes a CapabilityDefinition YAML to a temp file and loads it.
func loadCapDef(t *testing.T, spec string) (map[string]*CapabilityDefinition, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.yaml")
	writeFile(t, path, `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
`+spec)
	return LoadCapabilityDefinitions([]string{path}, "")
}

func TestLoadCapabilityDefinitions_RejectsRichPropFields(t *testing.T) {
	cases := map[string]string{
		"enum":                       `        enum: ["a", "b"]`,
		"enum-empty":                 `        enum: []`,
		"properties-empty":           `        properties: {}`,
		"items-null":                 `        items: null`,
		"additionalProperties-false": `        additionalProperties: false`,
		"additionalProperties-true":  `        additionalProperties: true`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadCapDef(t, `  rendering:
    properties:
      mode:
        type: string
`+extra+"\n")
			if err == nil {
				t.Fatalf("expected load error for capability prop with %s, got nil", name)
			}
			if !strings.Contains(err.Error(), "unsupported field") {
				t.Errorf("error should name the unsupported field, got: %v", err)
			}
		})
	}
}

func TestLoadCapabilityDefinitions_RejectsUnknownRenderingField(t *testing.T) {
	_, err := loadCapDef(t, `  rendering:
    bogusfield:
      mode:
        type: string
`)
	if err == nil {
		t.Fatal("expected load error for unknown rendering field, got nil")
	}
}

func TestLoadCapabilityDefinitions_RenderingLevelErrorNamesCorrectKeys(t *testing.T) {
	// A flat-vocabulary field placed at the rendering level (not inside a property)
	// must report the rendering allow-set ("properties"), not the property-level
	// vocabulary — otherwise the message self-contradicts ("unsupported field
	// \"type\" … accept only type, …").
	_, err := loadCapDef(t, "  rendering:\n    type: string\n")
	if err == nil {
		t.Fatal("expected load error for flat field at rendering level, got nil")
	}
	if !strings.Contains(err.Error(), "allowed: properties") {
		t.Errorf("rendering-level error should name 'properties' as allowed, got: %v", err)
	}
	if strings.Contains(err.Error(), "required, default") {
		t.Errorf("rendering-level error must not claim the property vocabulary, got: %v", err)
	}
}

func TestLoadCapabilityDefinitions_RejectsUnknownPropertyField(t *testing.T) {
	_, err := loadCapDef(t, `  rendering:
    properties:
      mode:
        type: string
        bogusfield: auto
`)
	if err == nil {
		t.Fatal("expected load error for unknown property field, got nil")
	}
}

func TestLoadCapabilityDefinitions_RejectsMalformedProperties(t *testing.T) {
	cases := map[string]string{
		"sequence": `  rendering:
    properties: []
`,
		"scalar-property": `  rendering:
    properties:
      mode: hello
`,
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadCapDef(t, spec); err == nil {
				t.Fatalf("expected load error for %s, got nil", name)
			}
		})
	}
}

func TestLoadCapabilityDefinitions_ResolvesPropertyAlias(t *testing.T) {
	// A YAML alias (`*scalar`) must resolve like plain yaml.v3 decode would, not be
	// rejected as "not a mapping".
	defs, err := loadCapDef(t, `  rendering:
    properties:
      base: &scalar
        type: string
      copy: *scalar
`)
	if err != nil {
		t.Fatalf("unexpected error resolving alias: %v", err)
	}
	props := defs["my-trait"].Spec.Rendering.Properties
	if props["base"].Type != PropertyTypeString || props["copy"].Type != PropertyTypeString {
		t.Errorf("alias not resolved: %+v", props)
	}
}

func TestLoadCapabilityDefinitions_RejectsMergeKey(t *testing.T) {
	// Intentional tightening: a YAML merge key (`<<`) is not expanded — it is a key
	// outside the flat allow-set, so it is rejected. (Plain yaml.v3 would merge it.)
	_, err := loadCapDef(t, `  rendering:
    properties:
      base: &b
        type: string
      merged:
        <<: *b
        required: true
`)
	if err == nil {
		t.Fatal("expected load error for merge key, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported field") {
		t.Errorf("expected unsupported-field error for merge key, got: %v", err)
	}
}

func TestLoadCapabilityDefinitions_RejectsComplexPropertyKey(t *testing.T) {
	// A non-scalar property key would silently read as "" via Content[i].Value;
	// map[string]... decode would have errored, so reject it.
	_, err := loadCapDef(t, `  rendering:
    properties:
      ? [a, b]
      : {type: string}
`)
	if err == nil {
		t.Fatal("expected load error for non-scalar property key, got nil")
	}
}

// --- capability rendering: null/empty tolerance preserved ---

func TestLoadCapabilityDefinitions_NullEmptyRenderingTolerated(t *testing.T) {
	cases := map[string]string{
		"rendering-null":   `  rendering:` + "\n",
		"rendering-empty":  `  rendering: {}` + "\n",
		"properties-null":  "  rendering:\n    properties:\n",
		"properties-empty": "  rendering:\n    properties: {}\n",
		"no-rendering":     "  description: x\n",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			defs, err := loadCapDef(t, spec)
			if err != nil {
				t.Fatalf("expected no error for %s, got: %v", name, err)
			}
			if len(defs["my-trait"].Spec.Rendering.Properties) != 0 {
				t.Errorf("expected empty Properties for %s", name)
			}
		})
	}
}

func TestLoadCapabilityDefinitions_NullPropertyRetainedAsKnown(t *testing.T) {
	// `foo:` (null value) must remain a KNOWN, unconstrained property — decoded to
	// an empty PropertySchema whose key is retained. If it were skipped,
	// applyDefinitionSchema would treat a supplied `foo` as an unknown property.
	defs, err := loadCapDef(t, "  rendering:\n    properties:\n      foo:\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	def := defs["my-trait"]
	if _, ok := def.Spec.Rendering.Properties["foo"]; !ok {
		t.Fatal("expected 'foo' to be retained as a known property")
	}
	if _, err := applyDefinitionSchema(map[string]any{"foo": "anything"}, def); err != nil {
		t.Errorf("supplying a value for the null-schema property 'foo' should be accepted, got: %v", err)
	}
}

// --- PropertySchema is now YAML-decodable ---

func TestPropertySchema_YAMLDecode(t *testing.T) {
	const doc = `
type: string
description: a mode
required: true
default: dev
`
	var ps PropertySchema
	if err := yaml.Unmarshal([]byte(doc), &ps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ps.Type != PropertyTypeString {
		t.Errorf("Type = %q, want string", ps.Type)
	}
	if ps.Description != "a mode" || !ps.Required || ps.Default != "dev" {
		t.Errorf("fields not preserved: %+v", ps)
	}
}
