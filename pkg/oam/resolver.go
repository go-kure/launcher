package oam

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

var (
	placeholderRE = regexp.MustCompile(`\$\{([^}]+)\}`)
	fullValueRE   = regexp.MustCompile(`^\$\{([^}]+)\}$`)
)

// ResolveParameters resolves ${name} placeholders in appYAML using the package schema
// and the merged supplied values. Runs before oam.Parse() on the raw template bytes.
//
// supplied should be pre-merged: --values file entries overlaid by --set entries
// (--set wins). String values from --set are coerced to the declared parameter type.
//
// Returns resolved YAML bytes ready for oam.Parse(), or an error if any placeholder
// cannot be resolved, a required parameter is missing, or a supplied key is undeclared.
func ResolveParameters(appYAML []byte, schema []ParameterDecl, supplied map[string]any) ([]byte, error) {
	schemaByName := make(map[string]*ParameterDecl, len(schema))
	for i := range schema {
		schemaByName[schema[i].Name] = &schema[i]
	}

	// Step 1: coerce supplied values to declared types (validates --set string inputs).
	coerced, err := coerceSuppliedValues(supplied, schemaByName)
	if err != nil {
		return nil, err
	}

	// Step 2: validate no unknown supplied keys.
	for k := range supplied {
		if _, ok := schemaByName[k]; !ok {
			return nil, errors.Errorf("parameter %q is not declared in kurel.yaml", k)
		}
	}

	// Step 3: build effective values by resolving defaults in declaration order.
	effective, err := buildEffectiveValues(schema, coerced)
	if err != nil {
		return nil, err
	}

	// Step 4: validate required parameters.
	for _, p := range schema {
		if p.Required {
			if _, ok := effective[p.Name]; !ok {
				return nil, errors.Errorf("required parameter %q is not set; provide it with --values or --set", p.Name)
			}
		}
	}

	// Step 5: parse YAML as node tree, substitute placeholders in-place.
	var root yaml.Node
	if err := yaml.Unmarshal(appYAML, &root); err != nil {
		return nil, errors.Wrap(err, "parsing application template YAML")
	}

	if err := substituteNodes(&root, schemaByName, effective); err != nil {
		return nil, err
	}

	// Step 6: re-serialize. (No post-scan of the output bytes — values substituted
	// into the template may themselves contain ${...} which must not be flagged.)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, errors.Wrap(err, "serializing resolved application YAML")
	}
	return out, nil
}

// coerceSuppliedValues coerces each supplied value to the type declared in the schema.
// String values (from --set) are coerced; typed values (from --values YAML) are validated.
func coerceSuppliedValues(supplied map[string]any, schema map[string]*ParameterDecl) (map[string]any, error) {
	result := make(map[string]any, len(supplied))
	for k, v := range supplied {
		decl, known := schema[k]
		if !known {
			result[k] = v // unknown key — caught in step 2
			continue
		}
		coerced, err := coerceValue(v, decl)
		if err != nil {
			return nil, err
		}
		result[k] = coerced
	}
	return result, nil
}

func coerceValue(v any, decl *ParameterDecl) (any, error) {
	switch string(decl.Type) {
	case "integer":
		switch tv := v.(type) {
		case int:
			return tv, nil
		case int64:
			return int(tv), nil
		case float64:
			if tv != math.Trunc(tv) {
				return nil, errors.Errorf("parameter %q (type integer): %g is not a valid integer (fractional values are not allowed)", decl.Name, tv)
			}
			return int(tv), nil
		case string:
			n, err := strconv.Atoi(tv)
			if err != nil {
				return nil, errors.Errorf("parameter %q (type integer): %q is not a valid integer", decl.Name, tv)
			}
			return n, nil
		default:
			return nil, errors.Errorf("parameter %q (type integer): cannot coerce %T to integer", decl.Name, v)
		}
	case "boolean":
		switch tv := v.(type) {
		case bool:
			return tv, nil
		case string:
			b, err := strconv.ParseBool(tv)
			if err != nil {
				return nil, errors.Errorf("parameter %q (type boolean): %q is not a valid boolean (use true or false)", decl.Name, tv)
			}
			return b, nil
		default:
			return nil, errors.Errorf("parameter %q (type boolean): cannot coerce %T to boolean", decl.Name, v)
		}
	case "string":
		switch v.(type) {
		case map[string]any, []any:
			return nil, errors.Errorf("parameter %q (type string): got %T, expected a string scalar value", decl.Name, v)
		case nil:
			return nil, errors.Errorf("parameter %q (type string): null is not a valid string value", decl.Name)
		default:
			return fmt.Sprintf("%v", v), nil
		}
	case "array", "object":
		if _, isStr := v.(string); isStr {
			return nil, errors.Errorf("parameter %q (type %s) cannot be set with --set; use --values to supply a structured value", decl.Name, decl.Type)
		}
		return v, nil
	}
	return v, nil
}

// buildEffectiveValues resolves parameter defaults in declaration order.
// Defaults are resolved against effective values already set (earlier params only).
// A default that references a parameter with no effective value is a build error.
func buildEffectiveValues(schema []ParameterDecl, coerced map[string]any) (map[string]any, error) {
	effective := make(map[string]any, len(schema))
	maps.Copy(effective, coerced)

	for _, p := range schema {
		if _, ok := effective[p.Name]; ok {
			continue // supplied value takes precedence
		}
		if p.Default == nil {
			continue // no default; optional param left unset
		}

		defStr, isStr := p.Default.(string)
		if !isStr {
			// Non-string default (e.g. integer 1): use directly without substitution.
			effective[p.Name] = p.Default
			continue
		}

		if !placeholderRE.MatchString(defStr) {
			// Plain string default, no placeholders — coerce to the declared type.
			val, err := coerceStringToType(defStr, string(p.Type), p.Name)
			if err != nil {
				return nil, err
			}
			effective[p.Name] = val
			continue
		}

		// Default contains ${…} references — resolve against current effective values.
		var resolveErr error
		resolved := placeholderRE.ReplaceAllStringFunc(defStr, func(match string) string {
			if resolveErr != nil {
				return match
			}
			name := placeholderRE.FindStringSubmatch(match)[1]
			val, ok := effective[name]
			if !ok {
				resolveErr = errors.Errorf(
					"default for parameter %q references %q which has no value yet; "+
						"only earlier parameters may be referenced in defaults", p.Name, name)
				return match
			}
			return fmt.Sprintf("%v", val)
		})
		if resolveErr != nil {
			return nil, resolveErr
		}
		// Catch any surviving placeholder (e.g. forward reference that silently returned match).
		if placeholderRE.MatchString(resolved) {
			return nil, errors.Errorf(
				"default for parameter %q contains unresolved placeholder after substitution: %q", p.Name, resolved)
		}

		val, err := coerceStringToType(resolved, string(p.Type), p.Name)
		if err != nil {
			return nil, err
		}
		effective[p.Name] = val
	}

	return effective, nil
}

func coerceStringToType(s, paramType, paramName string) (any, error) {
	switch paramType {
	case "integer":
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, errors.Errorf("default for parameter %q resolved to %q which is not a valid integer", paramName, s)
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, errors.Errorf("default for parameter %q resolved to %q which is not a valid boolean", paramName, s)
		}
		return b, nil
	default:
		return s, nil
	}
}

// substituteNodes walks a yaml.Node tree and substitutes ${name} placeholders in scalar nodes.
func substituteNodes(node *yaml.Node, schema map[string]*ParameterDecl, effective map[string]any) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := substituteNodes(child, schema, effective); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		// Content alternates: key, value, key, value, ...
		// Substitute only values (odd-indexed entries); keys are never parameters.
		for i := 1; i < len(node.Content); i += 2 {
			if err := substituteNodes(node.Content[i], schema, effective); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := substituteNodes(child, schema, effective); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		// Alias nodes (YAML anchors) are not expected in OAM templates; skip.
	case yaml.ScalarNode:
		if !placeholderRE.MatchString(node.Value) {
			return nil
		}
		return substituteScalar(node, schema, effective)
	}
	return nil
}

// substituteScalar handles placeholder substitution for a single scalar yaml.Node.
// Full-value scalars (entire value is ${name}) are type-promoted to integer/boolean.
// Inline substitutions (${name} embedded in a larger string) always produce a string.
// String values are always emitted with DoubleQuotedStyle for safe YAML serialization —
// this correctly handles characters like :, #, ", \, and newlines.
func substituteScalar(node *yaml.Node, schema map[string]*ParameterDecl, effective map[string]any) error {
	// Full-value: the entire scalar value is exactly ${name}.
	if m := fullValueRE.FindStringSubmatch(node.Value); m != nil {
		name := m[1]
		decl, declared := schema[name]
		if !declared {
			return errors.Errorf("undeclared parameter %q referenced in application template", name)
		}
		val, hasVal := effective[name]
		if !hasVal {
			return errors.Errorf("parameter %q has no value; set it with --values or --set", name)
		}
		switch decl.Type {
		case "array", "object":
			return errors.Errorf(
				"parameter %q has type %s; node substitution is not yet implemented "+
					"(use --values to supply structured values)", name, decl.Type)
		case "integer":
			node.Value = fmt.Sprintf("%v", val)
			node.Tag = "!!int"
			node.Style = 0
		case "boolean":
			node.Value = fmt.Sprintf("%v", val)
			node.Tag = "!!bool"
			node.Style = 0
		default: // "string"
			node.Value = fmt.Sprintf("%v", val)
			node.Tag = "!!str"
			node.Style = yaml.DoubleQuotedStyle
		}
		return nil
	}

	// Inline: ${name} embedded within a larger string.
	var inlineErr error
	result := placeholderRE.ReplaceAllStringFunc(node.Value, func(match string) string {
		if inlineErr != nil {
			return match
		}
		name := placeholderRE.FindStringSubmatch(match)[1]
		decl, declared := schema[name]
		if !declared {
			inlineErr = errors.Errorf("undeclared parameter %q referenced in application template", name)
			return match
		}
		val, hasVal := effective[name]
		if !hasVal {
			inlineErr = errors.Errorf("parameter %q has no value; set it with --values or --set", name)
			return match
		}
		if decl.Type == "array" || decl.Type == "object" {
			inlineErr = errors.Errorf(
				"parameter %q (type %s) cannot be used in inline string substitution", name, decl.Type)
			return match
		}
		return fmt.Sprintf("%v", val)
	})
	if inlineErr != nil {
		return inlineErr
	}
	// Inline substitution always produces a string.
	// Use DoubleQuotedStyle to safely encode values containing :, #, ", \, newlines, etc.
	node.Value = result
	node.Tag = "!!str"
	node.Style = yaml.DoubleQuotedStyle
	return nil
}
