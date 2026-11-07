package model

import (
	"fmt"
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
			name := n.Content[i].Value
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
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := refuseNullElements(n.Content[i+1], t.Elem(), joinPath(path, n.Content[i].Value)); err != nil {
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

// refuseDuplicateKeys fails on a mapping of free-form content that holds two keys
// which YAML resolves to one value. yaml.v3 compares the spelling of two keys to find
// a duplicate. When one key of a mapping is not a string, it decodes the mapping into
// a map keyed by the resolved value. Then `{1: a, 01: b}` and `{true: a, True: b}`
// become one key and the later entry silently replaces the earlier one. A mapping
// whose keys are all strings decodes into string keys and loses nothing.
//
// The error names the path of the outermost free-form value. For a mapping nested
// inside it, the line number in the message gives the exact place. The check
// over-refuses one absurd case: `.nan` and `.NaN` print the same, although Go keeps
// two NaN keys apart.
func refuseDuplicateKeys(n *yaml.Node, path string) error {
	if n.Kind == yaml.MappingNode && hasNonStringKey(n) {
		seen := map[string]*yaml.Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			var v any
			if err := k.Decode(&v); err != nil {
				continue
			}
			id := fmt.Sprintf("%T|%v", v, v)
			if first, ok := seen[id]; ok {
				return invalid(path, "line %d: duplicate mapping key %s (same as %s on line %d)", k.Line, k.Value, first.Value, first.Line)
			}
			seen[id] = k
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
