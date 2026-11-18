package model

import (
	"errors"
	"strings"
	"testing"
)

// A model input may type the values of an open object with additionalPropertiesSchema
// (go-kure/launcher#1615). The loader checks that schema as it checks every other node.

func TestLoad_InputValueSchema(t *testing.T) {
	tests := []struct {
		name    string
		body    []string
		wantErr string // "" means accepted
	}{
		{
			name: "a typed value is accepted",
			body: []string{"type: object", "additionalPropertiesSchema: {type: array, items: {type: string}}"},
		},
		{
			name:    "an unknown type in the value schema is refused at its path",
			body:    []string{"type: object", "additionalPropertiesSchema: {type: strng}"},
			wantErr: `spec.inputs.image.additionalPropertiesSchema: type "strng"`,
		},
		{
			name:    "a value schema with no type is refused at its path",
			body:    []string{"type: object", "additionalPropertiesSchema: {description: x}"},
			wantErr: "spec.inputs.image.additionalPropertiesSchema: type is required",
		},
		{
			name:    "boundedBy inside the value schema is refused at its path",
			body:    []string{"type: object", "additionalPropertiesSchema: {type: string, boundedBy: [registry]}"},
			wantErr: "spec.inputs.image.additionalPropertiesSchema: boundedBy",
		},
		{
			name:    "a nested property of the value schema is checked",
			body:    []string{"type: object", "additionalPropertiesSchema: {type: object, properties: {a: {type: strng}}}"},
			wantErr: `spec.inputs.image.additionalPropertiesSchema.properties.a: type "strng"`,
		},
		{
			name:    "a value schema on a string input is refused",
			body:    []string{"type: string", "additionalPropertiesSchema: {type: string}"},
			wantErr: "spec.inputs.image: additionalPropertiesSchema",
		},
		{
			name:    "a value schema on an array input is refused",
			body:    []string{"type: array", "items: {type: string}", "additionalPropertiesSchema: {type: string}"},
			wantErr: "spec.inputs.image: additionalPropertiesSchema",
		},
		{
			name:    "a value schema on a union input is refused",
			body:    []string{"types: [string, integer]", "additionalPropertiesSchema: {type: string}"},
			wantErr: "spec.inputs.image: additionalPropertiesSchema",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadNoPanic(withInputs(t, inputBlock("", tc.body)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want the input accepted, got: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to wrap ErrInvalid and contain %q", err, tc.wantErr)
			}
		})
	}
}
