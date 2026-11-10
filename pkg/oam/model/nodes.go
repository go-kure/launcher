package model

import (
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// refuseNullElements walks a YAML node tree beside the Go type it decodes into and
// fails on a null element of a typed list. yaml.v3 drops such an element and keeps no
// trace of it, so `types: [string, null]` would load as `types: [string]`. A list of
// free-form items ([]any), pointers, maps or lists keeps its nulls, and so does any
// content that decodes into `any`.
//
// The walk also reaches every value of type `any` and checks it for keys that YAML
// resolves to one value (see refuseDuplicateKeys).
//
// path is the field path of n, in the form the format errors use.
func refuseNullElements(n *yaml.Node, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Interface:
		return refuseDuplicateKeys(n, path)
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return nil
		}
		fields := map[string]reflect.Type{}
		collectFields(t, fields)
		for i := 0; i+1 < len(n.Content); i += 2 {
			// yaml.v3 decodes each struct key into a string, so a key tagged !!binary
			// names the field its decoded text names. Decode it the same way.
			var name string
			if err := n.Content[i].Decode(&name); err != nil {
				continue
			}
			ft, ok := fields[name]
			if !ok {
				continue
			}
			if err := refuseNullElements(n.Content[i+1], ft, joinPath(path, name)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return nil
		}
		if t.Key().Kind() == reflect.String {
			if err := refuseDuplicateStringKeys(n, path); err != nil {
				return err
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			// yaml.v3 skips a null key of a map with string keys together with its
			// value, so the value is never decoded. A map with another key type keeps
			// the nil key, and its value is walked.
			if t.Key().Kind() == reflect.String && isNullScalar(n.Content[i]) {
				continue
			}
			// The path names the key as decoded, like the struct branch above: a key
			// tagged !!binary names the entry its decoded text names. A key that does
			// not decode into a string keeps its raw spelling.
			name := n.Content[i].Value
			var decoded string
			if t.Key().Kind() == reflect.String && n.Content[i].Decode(&decoded) == nil {
				name = decoded
			}
			if err := refuseNullElements(n.Content[i+1], t.Elem(), joinPath(path, name)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return nil
		}
		k := t.Elem().Kind()
		keepsNull := k == reflect.Interface || k == reflect.Pointer || k == reflect.Map || k == reflect.Slice
		for i, c := range n.Content {
			p := path + "[" + strconv.Itoa(i) + "]"
			if !keepsNull && c.Kind == yaml.ScalarNode && c.ShortTag() == "!!null" {
				return invalid(p, "line %d: is null, want a value", c.Line)
			}
			if err := refuseNullElements(c, t.Elem(), p); err != nil {
				return err
			}
		}
	default:
		// Scalar kinds hold no list or mapping to walk.
	}
	return nil
}

// collectFields maps the YAML key of every decoded field of struct type t to the
// field type. It follows the yaml tag the way yaml.v3 does: a name before the comma
// (the lower-cased field name when empty), `-` skips the field and `inline` merges the
// fields of an embedded struct.
func collectFields(t reflect.Type, into map[string]reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if strings.Contains(","+opts+",", ",inline,") {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectFields(ft, into)
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		into[name] = f.Type
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// isNullScalar reports whether n is a scalar that YAML resolves to null: `~`, `null`,
// `Null`, `NULL`, an empty plain scalar or an explicit `!!null` tag.
func isNullScalar(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// refuseDuplicateStringKeys fails on a mapping that decodes into a Go map with string
// keys when two of its keys decode to the same string. yaml.v3 compares the kind and
// the value of two keys to find a duplicate, and it ignores the tag. So `image` and
// `!!binary aW1hZ2U=` pass its check. It then decodes both keys into the string
// `image`, and the later entry replaces the earlier one. Keys that decode to different
// strings, such as `1` and `01`, stay apart, as in the decoded map. yaml.v3 skips a
// null key of a string map, so this function skips it too. A key that does not decode
// into a string is left to yaml.v3.
func refuseDuplicateStringKeys(n *yaml.Node, path string) error {
	seen := make(map[string]*yaml.Node, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if isNullScalar(k) {
			continue
		}
		var name string
		if err := k.Decode(&name); err != nil {
			continue
		}
		if first, ok := seen[name]; ok {
			return invalid(path, "line %d: duplicate mapping key %s, which decodes to %q (same as %s on line %d)", k.Line, k.Value, name, first.Value, first.Line)
		}
		seen[name] = k
	}
	return nil
}

// refuseDuplicateKeys fails on a mapping of free-form content that holds two keys
// which YAML resolves to one value. yaml.v3 compares the spelling of two keys to find
// a duplicate. When one key of a mapping is not a string, it decodes the mapping into
// a map keyed by the resolved value. Then `{1: a, 01: b}` and `{true: a, True: b}`
// become one key and the later entry silently replaces the earlier one. A mapping
// whose keys are all strings decodes into string keys and loses nothing.
//
// The check uses the equality of the Go map that yaml.v3 fills: it decodes each key
// into an `any` and looks it up in a map of its own. So 0.0 and -0.0 count as one key,
// and two NaN keys count as two, exactly as in the decoded value.
//
// The error names the path of the outermost free-form value. For a mapping nested
// inside it, the line number in the message gives the exact place.
func refuseDuplicateKeys(n *yaml.Node, path string) error {
	if n.Kind == yaml.MappingNode && hasNonStringKey(n) {
		seen := map[any]*yaml.Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			var v any
			if err := k.Decode(&v); err != nil {
				continue
			}
			if v != nil && !reflect.TypeOf(v).Comparable() {
				continue
			}
			if first, ok := seen[v]; ok {
				return invalid(path, "line %d: duplicate mapping key %s (same as %s on line %d)", k.Line, k.Value, first.Value, first.Line)
			}
			seen[v] = k
		}
	}
	for _, c := range n.Content {
		if err := refuseDuplicateKeys(c, path); err != nil {
			return err
		}
	}
	return nil
}

func hasNonStringKey(n *yaml.Node) bool {
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" {
			return true
		}
	}
	return false
}
