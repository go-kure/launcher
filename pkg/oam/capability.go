package oam

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

const (
	capabilityDefAPIVersion = "launcher.gokure.dev/v1alpha1"
	capabilityDefKind       = "CapabilityDefinition"
)

var acceptedPropertyTypes = map[string]bool{
	"string":  true,
	"integer": true,
	"boolean": true,
}

// kindProbe reads just the kind field from a YAML document for routing.
type kindProbe struct {
	Kind string `yaml:"kind"`
}

// LoadCapabilityDefinitions loads CapabilityDefinition documents from explicit
// paths and from *.yaml files in definitionsDir ("" = skip auto-discovery).
// Only files with kind: CapabilityDefinition are processed; other kinds are skipped.
// Each definition file is parsed with strict YAML (KnownFields) to reject unknown fields.
// Semantic validation:
//   - apiVersion must equal "launcher.gokure.dev/v1alpha1"
//   - metadata.name is required (non-empty)
//   - spec.rendering.properties entry types: accepted values are "string", "integer", "boolean"
//   - default value (if set) must be assignable to the declared type
//
// Identical definitions (same name + same spec) are deduplicated silently.
// Conflicting definitions (same name, different spec) return an error naming both files.
// A missing definitionsDir is not an error.
func LoadCapabilityDefinitions(paths []string, definitionsDir string) (map[string]*CapabilityDefinition, error) {
	allFiles := make([]string, len(paths))
	copy(allFiles, paths)

	if definitionsDir != "" {
		entries, err := os.ReadDir(definitionsDir)
		if err != nil && !os.IsNotExist(err) {
			return nil, errors.Wrapf(err, "reading definitions directory %q", definitionsDir)
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
				allFiles = append(allFiles, filepath.Join(definitionsDir, e.Name()))
			}
		}
	}

	defs := make(map[string]*CapabilityDefinition)
	sources := make(map[string]string) // name → file path (for conflict reporting)

	for _, filePath := range allFiles {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, errors.Wrapf(err, "reading capability definition file %q", filePath)
		}

		// Probe the kind field to decide whether to load this file.
		// A YAML parse failure here means the file is malformed — fail loudly so
		// the operator knows their schema file was never loaded.
		var probe kindProbe
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return nil, errors.Wrapf(err, "capability definition file %q: failed to parse YAML", filePath)
		}
		if probe.Kind != capabilityDefKind {
			continue
		}

		// Strict parse — reject unknown fields.
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		var def CapabilityDefinition
		if err := dec.Decode(&def); err != nil {
			return nil, errors.Wrapf(err, "parsing capability definition file %q", filePath)
		}

		if def.APIVersion != capabilityDefAPIVersion {
			return nil, errors.Errorf(
				"capability definition file %q: apiVersion must be %q, got %q",
				filePath, capabilityDefAPIVersion, def.APIVersion)
		}
		if def.Metadata.Name == "" {
			return nil, errors.Errorf("capability definition file %q: metadata.name is required", filePath)
		}

		for propName, propSchema := range def.Spec.Rendering.Properties {
			if propSchema.Type != "" && !acceptedPropertyTypes[string(propSchema.Type)] {
				return nil, errors.Errorf(
					"capability definition file %q: property %q has unsupported type %q (accepted: string, integer, boolean)",
					filePath, propName, propSchema.Type)
			}
			if propSchema.Default != nil {
				if err := checkCapabilityValueType(propSchema.Default, string(propSchema.Type)); err != nil {
					return nil, errors.Errorf(
						"capability definition file %q: property %q default value: %s",
						filePath, propName, err)
				}
			}
		}

		name := def.Metadata.Name
		if existing, ok := defs[name]; ok {
			if reflect.DeepEqual(existing.Spec, def.Spec) {
				continue // identical — silently deduplicate
			}
			return nil, errors.Errorf(
				"conflicting CapabilityDefinition for %q: defined in %q and %q",
				name, sources[name], filePath)
		}

		defs[name] = &def
		sources[name] = filePath
	}

	return defs, nil
}

// applyDefinitionSchema validates rendering against a CapabilityDefinition schema and
// applies declared defaults for absent optional properties.
// Enforces: required fields present; types match declared type; no unknown keys.
// Returns an updated rendering map (with defaults applied) or an error.
func applyDefinitionSchema(rendering map[string]any, def *CapabilityDefinition) (map[string]any, error) {
	props := def.Spec.Rendering.Properties

	for k := range rendering {
		if _, ok := props[k]; !ok {
			return nil, errors.Errorf("unknown rendering property %q", k)
		}
	}

	result := make(map[string]any, len(rendering))
	maps.Copy(result, rendering)

	for propName, propSchema := range props {
		v, present := result[propName]
		if !present {
			if propSchema.Required {
				return nil, errors.Errorf("required rendering property %q is missing", propName)
			}
			if propSchema.Default != nil {
				result[propName] = propSchema.Default
			}
			continue
		}
		if propSchema.Type != "" {
			if err := checkCapabilityValueType(v, string(propSchema.Type)); err != nil {
				return nil, errors.Errorf("rendering property %q: %s", propName, err)
			}
		}
	}

	return result, nil
}

// checkCapabilityValueType returns an error if v is not compatible with typeName.
func checkCapabilityValueType(v any, typeName string) error {
	switch typeName {
	case "string":
		if _, ok := v.(string); !ok {
			return errors.Errorf("expected string, got %T", v)
		}
	case "integer":
		switch n := v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			_ = n
		case float64:
			if n != float64(int64(n)) {
				return errors.Errorf("expected integer, got non-integer float %v", n)
			}
		default:
			return errors.Errorf("expected integer, got %T", v)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return errors.Errorf("expected boolean, got %T", v)
		}
	}
	return nil
}
