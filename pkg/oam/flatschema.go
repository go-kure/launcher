package oam

import (
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go-kure/launcher/pkg/errors"
)

// The two flat call sites (kurel parameters, capability rendering properties)
// share the PropertySchema vocabulary but support only its flat subset. Since
// PropertySchema is now YAML-decodable, its rich fields (enum/properties/items/
// additionalProperties) would decode silently at these sites — where strict
// KnownFields(true) parsing previously rejected them as unknown. A custom
// UnmarshalYAML bypasses the parent decoder's KnownFields, so these allow-sets
// restore that strictness by key presence (not by decoded value, which cannot
// distinguish an omitted field from a zero value like `additionalProperties: false`).
// See adr#33.
var (
	kurelParamKeys = map[string]struct{}{
		"name": {}, "type": {}, "required": {}, "default": {}, "description": {},
	}
	capabilityPropKeys = map[string]struct{}{
		"type": {}, "required": {}, "default": {}, "description": {},
	}
	renderingKeys = map[string]struct{}{
		"properties": {},
	}
)

// allowedFields renders an allow-set as a sorted, comma-joined list for error
// messages, so each call site names its own accepted keys (e.g. "properties" at
// the rendering level vs. the flat vocabulary at the property level).
func allowedFields(allowed map[string]struct{}) string {
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

// isYAMLNull reports whether node is a YAML null scalar (e.g. `foo:` with no
// value, or an explicit `null`). There is no yaml.NullNode; a null resolves to a
// scalar node tagged !!null. Callers treat null as "absent" to preserve the
// pre-unification tolerance of empty/zero values.
func isYAMLNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

// resolveAlias follows a YAML alias (`*anchor`) to the node it references, so alias
// nodes are handled like the value they point at. Plain yaml.v3 struct/map decoding
// resolves aliases transparently; the custom unmarshalers must do the same before
// inspecting node kind so `*anchor` inputs are not rejected as "not a mapping".
func resolveAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

// rejectUnsupportedSchemaKeys enforces allowed as the exact accepted key-set on a
// schema mapping node. Node-shape handling preserves prior decoding behavior:
//   - alias node → resolved to its target first
//   - null node → nil (pass through to a zero value; tolerated / caught later)
//   - mapping node → error on any key outside allowed
//   - any other node (non-null scalar, sequence) → error (this already failed
//     before as a yaml TypeError when decoded into a struct; only the message changes)
func rejectUnsupportedSchemaKeys(node *yaml.Node, allowed map[string]struct{}, ctx string) error {
	node = resolveAlias(node)
	if isYAMLNull(node) {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return errors.Errorf("%s must be a mapping", ctx)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			return errors.Errorf("%s: keys must be scalars", ctx)
		}
		if _, ok := allowed[keyNode.Value]; !ok {
			return errors.Errorf("%s: unsupported field %q (allowed: %s)", ctx, keyNode.Value, allowedFields(allowed))
		}
	}
	return nil
}

// UnmarshalYAML decodes a kurel parameter, rejecting any field outside the flat
// vocabulary before delegating to the embedded PropertySchema. A null list item
// passes through to a zero value and is caught by validatePackage ("name is
// required"), preserving prior behavior.
func (p *ParameterDecl) UnmarshalYAML(node *yaml.Node) error {
	node = resolveAlias(node)
	if err := rejectUnsupportedSchemaKeys(node, kurelParamKeys, "kurel parameter"); err != nil {
		return err
	}
	// Named local (not a `type raw ParameterDecl` alias) to keep the inline embed
	// unambiguous and avoid recursing back into this method. Decode the resolved
	// node so an aliased parameter item (`- *anchor`) decodes like plain yaml.v3.
	var y struct {
		Name           string `yaml:"name"`
		PropertySchema `yaml:",inline"`
	}
	if err := node.Decode(&y); err != nil {
		return err
	}
	p.Name = y.Name
	p.PropertySchema = y.PropertySchema
	return nil
}

// UnmarshalYAML decodes a capability rendering schema, validating both levels: the
// rendering mapping accepts only `properties`, and each property accepts only the
// flat vocabulary. Absent/empty/null rendering and `properties: null` yield an
// empty schema; a null property value (`foo: null`) is retained as an empty
// PropertySchema so the property stays known to applyDefinitionSchema.
func (r *CapabilityRenderingSchema) UnmarshalYAML(node *yaml.Node) error {
	r.Properties = nil // reset so decoding into a reused value is correct
	node = resolveAlias(node)
	if isYAMLNull(node) {
		return nil
	}
	if err := rejectUnsupportedSchemaKeys(node, renderingKeys, "rendering"); err != nil {
		return err
	}

	var propsNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "properties" {
			propsNode = node.Content[i+1]
			break
		}
	}
	propsNode = resolveAlias(propsNode)
	if propsNode == nil || isYAMLNull(propsNode) {
		return nil
	}
	if propsNode.Kind != yaml.MappingNode {
		return errors.Errorf("rendering.properties must be a mapping")
	}

	props := make(map[string]PropertySchema, len(propsNode.Content)/2)
	for i := 0; i+1 < len(propsNode.Content); i += 2 {
		keyNode := propsNode.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			return errors.Errorf("rendering.properties keys must be scalars")
		}
		name := keyNode.Value
		valNode := resolveAlias(propsNode.Content[i+1])
		ctx := "rendering property " + strconv.Quote(name)
		if err := rejectUnsupportedSchemaKeys(valNode, capabilityPropKeys, ctx); err != nil {
			return err
		}
		var ps PropertySchema
		if !isYAMLNull(valNode) {
			if err := valNode.Decode(&ps); err != nil {
				return err
			}
		}
		props[name] = ps
	}
	r.Properties = props
	return nil
}
