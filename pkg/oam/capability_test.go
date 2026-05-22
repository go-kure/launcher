package oam

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCapabilityDefinitions_ValidFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "custom.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
  description: "a custom trait"
  rendering:
    properties:
      timeout:
        type: integer
        required: true
      mode:
        type: string
        default: auto
`)
	defs, err := LoadCapabilityDefinitions(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	def, ok := defs["my-trait"]
	if !ok {
		t.Fatal("expected definition for 'my-trait'")
	}
	if def.Spec.Description != "a custom trait" {
		t.Errorf("description = %q, want %q", def.Spec.Description, "a custom trait")
	}
	if _, ok := def.Spec.Rendering.Properties["timeout"]; !ok {
		t.Error("expected property 'timeout'")
	}
}

func TestLoadCapabilityDefinitions_SkipsOtherKinds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "profile.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: staging
spec:
  capabilities: {}
`)
	defs, err := LoadCapabilityDefinitions(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected 0 definitions, got %d", len(defs))
	}
}

func TestLoadCapabilityDefinitions_MissingDirNotError(t *testing.T) {
	defs, err := LoadCapabilityDefinitions(nil, "/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("expected 0 definitions, got %d", len(defs))
	}
}

func TestLoadCapabilityDefinitions_DeduplicatesIdentical(t *testing.T) {
	dir := t.TempDir()
	body := `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
  rendering:
    properties:
      key:
        type: string
`
	writeFile(t, filepath.Join(dir, "a.yaml"), body)
	writeFile(t, filepath.Join(dir, "b.yaml"), body)

	defs, err := LoadCapabilityDefinitions(nil, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Errorf("expected 1 definition after dedup, got %d", len(defs))
	}
}

func TestLoadCapabilityDefinitions_ConflictErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
  rendering:
    properties:
      key:
        type: string
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
  rendering:
    properties:
      key:
        type: integer
`)
	_, err := LoadCapabilityDefinitions(nil, dir)
	if err == nil {
		t.Fatal("expected error for conflicting definitions")
	}
}

func TestLoadCapabilityDefinitions_WrongAPIVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.yaml"), `
apiVersion: wrong.group/v1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec: {}
`)
	_, err := LoadCapabilityDefinitions(nil, dir)
	if err == nil {
		t.Fatal("expected error for wrong apiVersion")
	}
}

func TestLoadCapabilityDefinitions_MissingName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: ""
spec: {}
`)
	_, err := LoadCapabilityDefinitions(nil, dir)
	if err == nil {
		t.Fatal("expected error for missing metadata.name")
	}
}

func TestLoadCapabilityDefinitions_UnsupportedPropertyType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.yaml"), `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: my-trait
spec:
  rendering:
    properties:
      key:
        type: number
`)
	_, err := LoadCapabilityDefinitions(nil, dir)
	if err == nil {
		t.Fatal("expected error for unsupported property type")
	}
}

func TestLoadCapabilityDefinitions_MalformedYAML_Errors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.yaml"), `
kind: CapabilityDefinition
: this is not valid yaml {[
`)
	_, err := LoadCapabilityDefinitions(nil, dir)
	if err == nil {
		t.Fatal("expected error for malformed YAML in auto-discovered file")
	}
}

func TestLoadCapabilityDefinitions_MalformedYAML_ExplicitPath_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	writeFile(t, path, `
kind: CapabilityDefinition
: this is not valid yaml {[
`)
	_, err := LoadCapabilityDefinitions([]string{path}, "")
	if err == nil {
		t.Fatal("expected error for malformed YAML in explicit path")
	}
}

func TestLoadCapabilityDefinitions_ExplicitPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	writeFile(t, path, `
apiVersion: launcher.gokure.dev/v1alpha1
kind: CapabilityDefinition
metadata:
  name: explicit-trait
spec: {}
`)
	defs, err := LoadCapabilityDefinitions([]string{path}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := defs["explicit-trait"]; !ok {
		t.Error("expected definition for 'explicit-trait' from explicit path")
	}
}

func TestApplyDefinitionSchema_AppliesDefault(t *testing.T) {
	def := &CapabilityDefinition{
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]CapabilityPropertySchema{
					"mode": {Type: "string", Default: "auto"},
				},
			},
		},
	}
	result, err := applyDefinitionSchema(map[string]any{}, def)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["mode"] != "auto" {
		t.Errorf("mode = %v, want %q", result["mode"], "auto")
	}
}

func TestApplyDefinitionSchema_RequiredMissing(t *testing.T) {
	def := &CapabilityDefinition{
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]CapabilityPropertySchema{
					"timeout": {Type: "integer", Required: true},
				},
			},
		},
	}
	_, err := applyDefinitionSchema(map[string]any{}, def)
	if err == nil {
		t.Fatal("expected error for required missing property")
	}
}

func TestApplyDefinitionSchema_TypeMismatch(t *testing.T) {
	def := &CapabilityDefinition{
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]CapabilityPropertySchema{
					"enabled": {Type: "boolean"},
				},
			},
		},
	}
	_, err := applyDefinitionSchema(map[string]any{"enabled": "yes"}, def)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
}

func TestApplyDefinitionSchema_UnknownKey(t *testing.T) {
	def := &CapabilityDefinition{
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]CapabilityPropertySchema{
					"mode": {Type: "string"},
				},
			},
		},
	}
	_, err := applyDefinitionSchema(map[string]any{"mode": "fast", "extra": "nope"}, def)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestApplyDefinitionSchema_ValidRendering(t *testing.T) {
	def := &CapabilityDefinition{
		Spec: CapabilityDefSpec{
			Rendering: CapabilityRenderingSchema{
				Properties: map[string]CapabilityPropertySchema{
					"mode":     {Type: "string"},
					"timeout":  {Type: "integer"},
					"enabled":  {Type: "boolean"},
					"optional": {Type: "string", Default: "fallback"},
				},
			},
		},
	}
	rendering := map[string]any{
		"mode":    "fast",
		"timeout": float64(30),
		"enabled": true,
	}
	result, err := applyDefinitionSchema(rendering, def)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["optional"] != "fallback" {
		t.Errorf("optional = %v, want %q", result["optional"], "fallback")
	}
	if result["mode"] != "fast" {
		t.Errorf("mode = %v, want %q", result["mode"], "fast")
	}
}

// writeFile writes content to path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}
