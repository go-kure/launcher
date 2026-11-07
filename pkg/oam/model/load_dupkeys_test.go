package model

import (
	"errors"
	"strings"
	"testing"
)

// yaml.v3 decodes a key into a string, and a !!binary key goes through base64. So
// `!!binary aW1hZ2U=` is the key `image`. yaml.v3 sees two different scalars and no
// duplicate, but a Go map keeps one entry, so the later entry replaces the earlier one.
// Load refuses the pair in every typed map of the format: the inputs, a schema's
// properties, the properties of a part and of a trait, and the annotations.
func TestLoad_RefusesDuplicateDecodedStringKeys(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) []byte
		path string
	}{
		{"inputs", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    image:\n      type: string\n    !!binary aW1hZ2U=:\n      type: integer\n")
		}, "spec.inputs"},
		{"schema properties", func(t *testing.T) []byte {
			return withInputs(t, "  inputs:\n    cfg:\n      type: object\n      properties:\n        image: {type: string}\n        !!binary aW1hZ2U=: {type: integer}\n")
		}, "spec.inputs.cfg.properties"},
		{"part properties", func(t *testing.T) []byte {
			return replace(t, "        replicas: \"${inputs.replicas}\"\n", "        replicas: \"${inputs.replicas}\"\n        !!binary cmVwbGljYXM=: 3\n")
		}, "spec.parts[0].properties"},
		{"trait properties", func(t *testing.T) []byte {
			return replace(t, "            port: 80\n", "            port: 80\n            !!binary cG9ydA==: 81\n")
		}, "spec.parts[0].traits[0].properties"},
		{"annotations", func(t *testing.T) []byte {
			return replace(t, "        owner: acme\n", "        owner: acme\n        !!binary b3duZXI=: other\n")
		}, "spec.parts[0].annotations"},
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

// Keys that decode to different strings stay apart: `1` and `01` are two strings in a
// string-keyed map, and so are `true` and `True`. Load keeps them. This passes before
// and after the change, so it is a pin and discriminates nothing against the unfixed code.
func TestLoad_KeepsDistinctStringKeys(t *testing.T) {
	docs := [][]byte{
		replace(t, "        owner: acme\n", "        owner: acme\n        1: a\n        01: b\n"),
		replace(t, "        owner: acme\n", "        owner: acme\n        true: a\n        True: b\n"),
		replace(t, "        replicas: \"${inputs.replicas}\"\n", "        replicas: \"${inputs.replicas}\"\n        1: a\n        01: b\n"),
	}
	for i, doc := range docs {
		if _, err := loadNoPanic(doc); err != nil {
			t.Fatalf("document %d with distinct string keys was refused: %v", i, err)
		}
	}
}
