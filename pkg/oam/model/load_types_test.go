package model

import (
	"errors"
	"strings"
	"testing"
)

// A union needs at least two distinct scalar members. The shared validator in pkg/oam
// refuses any other list as soon as a value reaches it, so such a schema accepts no
// value at all. Load refuses it as a schema-shape error and names the schema.
func TestLoad_RefusesMalformedUnions(t *testing.T) {
	cases := []struct {
		name  string
		block string
		path  string
	}{
		{"one member", "  inputs:\n    image:\n      types: [string]\n", "spec.inputs.image"},
		{"repeated member", "  inputs:\n    image:\n      types: [string, string]\n", "spec.inputs.image"},
		{"object member", "  inputs:\n    image:\n      types: [string, object]\n", "spec.inputs.image"},
		{"array member", "  inputs:\n    image:\n      types: [integer, array]\n", "spec.inputs.image"},
		{"nested property", "  inputs:\n    image:\n      type: object\n      properties:\n        port:\n          types: [string]\n", "spec.inputs.image.properties.port"},
		{"array items", "  inputs:\n    image:\n      type: array\n      items:\n        types: [string, string]\n", "spec.inputs.image.items"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(withInputs(t, tc.block))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), "types") {
				t.Fatalf("error %q must wrap ErrInvalid, name %s and say types", err, tc.path)
			}
		})
	}
}

// A union of two distinct scalar types is valid, at the top and under properties or
// items. It passes before and after the change, so it is a pin of accepted behavior and
// discriminates nothing against the unfixed code.
func TestLoad_KeepsValidUnion(t *testing.T) {
	blocks := []string{
		"  inputs:\n    image:\n      types: [string, integer]\n",
		"  inputs:\n    image:\n      types: [boolean, number, string, integer]\n",
		"  inputs:\n    image:\n      type: object\n      properties:\n        port:\n          types: [integer, string]\n",
		"  inputs:\n    image:\n      type: array\n      items:\n        types: [string, number]\n",
	}
	for _, block := range blocks {
		if _, err := loadNoPanic(withInputs(t, block)); err != nil {
			t.Fatalf("a valid union was refused: %v\n%s", err, block)
		}
	}
}
