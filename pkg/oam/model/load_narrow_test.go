package model

import (
	"errors"
	"strings"
	"testing"
)

const (
	nullValuesOld = "    - name: full\n"
	nullPartsOld  = "  parts:\n    - id: app\n"
	nullTraitsOld = "      traits:\n        - type: expose.launcher.gokure.dev@v1\n"
)

// yaml.v3 drops a null element of a typed slice and keeps no trace of it. A null
// element is never meant, so Load refuses it with the path of the element. The
// slices below are every typed slice of a model document: values, parts, a part's
// traits, and in a schema the types, the exclusive groups and their keys. A schema
// nests through properties and items.
func TestLoad_RefusesNullInTypedSequence(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) []byte
		path string
	}{
		{"types", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      types: [string, null]\n")
		}, "spec.inputs.image.types[1]"},
		{"values", func(t *testing.T) []byte {
			return replace(t, nullValuesOld, "    - null\n"+nullValuesOld)
		}, "spec.values[0]"},
		{"parts", func(t *testing.T) []byte {
			return replace(t, nullPartsOld, "  parts:\n    - null\n    - id: app\n")
		}, "spec.parts[0]"},
		{"traits", func(t *testing.T) []byte {
			return replace(t, nullTraitsOld, "      traits:\n        - null\n        - type: expose.launcher.gokure.dev@v1\n")
		}, "spec.parts[0].traits[0]"},
		{"exclusive groups", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      exclusive: [null]\n")
		}, "spec.inputs.image.exclusive[0]"},
		{"exclusive keys", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      exclusive:\n        - keys: [a, null]\n")
		}, "spec.inputs.image.exclusive[0].keys[1]"},
		{"types of a nested property", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      properties:\n        port:\n          types: [string, null]\n")
		}, "spec.inputs.image.properties.port.types[1]"},
		{"types of an items schema", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: array\n      items:\n        types: [string, null]\n")
		}, "spec.inputs.image.items.types[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(tc.doc(t))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), "null") {
				t.Fatalf("error %q must wrap ErrInvalid, name %s and say null", err, tc.path)
			}
		})
	}
}

// A null inside free-form content decodes into `any` and keeps its place, so it is
// a legal value and Load keeps accepting it. This is the control for the refusal
// above: it passes before and after the change, so it discriminates nothing.
func TestLoad_KeepsNullInFreeFormContent(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) []byte
	}{
		{"default", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: array\n      default: [1, null]\n")
		}},
		{"enum", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: string\n      enum: [a, null]\n")
		}},
		{"part properties", func(t *testing.T) []byte {
			return replace(t, "        data: {}\n", "        data: [null, 1]\n")
		}},
		{"trait properties", func(t *testing.T) []byte {
			return replace(t, "            port: 80\n", "            port: [null]\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadNoPanic(tc.doc(t)); err != nil {
				t.Fatalf("a null in free-form content was refused: %v", err)
			}
		})
	}
}

// yaml.v3 compares the spelling of two keys to find a duplicate. In free-form content
// (a value of type any) a mapping with a key that is not a string is keyed by the
// resolved value, so 1 and 01 (or true and True) become one key and the later entry
// silently replaces the earlier one. Load refuses that mapping and wraps ErrInvalid.
func TestLoad_RefusesSameKeyWrittenTwice(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) []byte
		path string
	}{
		{"default with 1 and 01", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      default: {1: first, 01: second}\n")
		}, "spec.inputs.image.default"},
		{"default with true and True", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      default: {true: first, True: second}\n")
		}, "spec.inputs.image.default"},
		{"enum element", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: object\n      enum:\n        - {1: first, 01: second}\n")
		}, "spec.inputs.image.enum[0]"},
		{"value of a part property", func(t *testing.T) []byte {
			return replace(t, "        data: {}\n", "        data:\n          1: first\n          01: second\n")
		}, "spec.parts[1].properties.data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(tc.doc(t))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.path) ||
				!strings.Contains(err.Error(), "duplicate mapping key") || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("error %q must wrap ErrInvalid, name %s, say duplicate mapping key and name the line", err, tc.path)
			}
		})
	}
}

// Controls. Keys that stay distinct after YAML resolves them still load. They pass
// before and after the change, so they discriminate nothing against the unfixed code.
//
// The mapping {"1": first, 01: second} passes the yaml.v3 spelling check, because the
// values "1" and "01" differ. It decodes into a map[any]any with the string "1" and the
// integer 1, and both stay. This pin does not fail on the unfixed code. It fails on a
// wrong fix that compares keys by their printed value alone, because both print as 1.
//
// Under a map[string]any target, as the properties of a part are, YAML keeps 1 and 01 as
// the two strings "1" and "01", so nothing is lost there either.
func TestLoad_KeepsDistinctKeys(t *testing.T) {
	for _, def := range []string{"{1: first, 2: second}", "{a: first, A: second}"} {
		if _, err := loadNoPanic(withInputs(t, "  inputs:\n    image:\n      type: object\n      default: "+def+"\n")); err != nil {
			t.Fatalf("default: %s was refused: %v", def, err)
		}
	}
	mixed, err := loadNoPanic(withInputs(t, "  inputs:\n    image:\n      type: object\n      default: {\"1\": first, 01: second}\n"))
	if err != nil {
		t.Fatalf("a string key \"1\" and an integer key 01 were refused: %v", err)
	}
	def, ok := mixed.Spec.Inputs["image"].Default.(map[any]any)
	if !ok || len(def) != 2 || def["1"] != "first" || def[1] != "second" {
		t.Fatalf("default = %#v, want the string key \"1\" and the integer key 1, both kept", mixed.Spec.Inputs["image"].Default)
	}
	m, err := loadNoPanic(replace(t, "      properties:\n        data: {}\n", "      properties:\n        1: first\n        01: second\n"))
	if err != nil {
		t.Fatalf("1 and 01 as part properties were refused: %v", err)
	}
	props := m.Spec.Parts[1].Properties
	if len(props) != 2 || props["1"] != "first" || props["01"] != "second" {
		t.Fatalf("part properties = %v, want the two string keys 1 and 01", props)
	}
}
