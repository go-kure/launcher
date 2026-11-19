package model

import (
	"errors"
	"strings"
	"testing"
)

// whenInput is an input of object type with a string a, a boolean flag and a string
// b whose extra lines are the given ones (the requiredWhen rule of b, for example).
func whenInput(bLines string) string {
	return "  inputs:\n    cfg:\n      type: object\n      properties:\n        a: {type: string, enum: [x, y]}\n        flag: {type: boolean}\n        b:\n          type: string\n" + bLines
}

// A malformed requiredWhen on an input property is refused at load with the path of
// the object that holds it, by the rules of pkg/oam applied to every declared node.
func TestLoad_RefusesMalformedRequiredWhen(t *testing.T) {
	cases := []struct {
		name  string
		block string
		path  string
		want  string
	}{
		{"undeclared key", whenInput("          requiredWhen: {key: missing}\n"), "spec.inputs.cfg", "not a declared property"},
		{"the property itself", whenInput("          requiredWhen: {key: b}\n"), "spec.inputs.cfg", "the property itself"},
		{"a required property", whenInput("          required: true\n          requiredWhen: {key: a}\n"), "spec.inputs.cfg", "Required and requiredWhen"},
		{"a number equals", whenInput("          requiredWhen: {key: a, equals: 3}\n"), "spec.inputs.cfg", "equals is a string or a boolean"},
		{"an equals of the wrong type", whenInput("          requiredWhen: {key: flag, equals: yes-text}\n"), "spec.inputs.cfg", "is declared boolean"},
		{"an equals outside the enum", whenInput("          requiredWhen: {key: a, equals: z}\n"), "spec.inputs.cfg", "enum values"},
		{"on the input itself", "  inputs:\n    cfg:\n      type: string\n      requiredWhen: {key: other}\n", "spec.inputs.cfg", "not a property of an object"},
		{
			"on array items",
			"  inputs:\n    cfg:\n      type: array\n      items:\n        type: string\n        requiredWhen: {key: a}\n",
			"spec.inputs.cfg.items", "not a property of an object",
		},
		{
			"on an open object's value schema",
			"  inputs:\n    cfg:\n      type: object\n      additionalPropertiesSchema:\n        type: string\n        requiredWhen: {key: a}\n",
			"spec.inputs.cfg.additionalPropertiesSchema", "not a property of an object",
		},
		{
			"in array items",
			"  inputs:\n    cfg:\n      type: array\n      items:\n        type: object\n        properties:\n          a: {type: string}\n          b: {type: string, requiredWhen: {key: missing}}\n",
			"spec.inputs.cfg.items", "not a declared property",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(withInputs(t, tc.block))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %q does not wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.path+": ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must name %s and say %q", err, tc.path, tc.want)
			}
		})
	}

	t.Run("a well-formed rule is accepted", func(t *testing.T) {
		for _, bLines := range []string{
			"          requiredWhen: {key: a}\n",
			"          requiredWhen: {key: a, equals: x}\n",
			"          requiredWhen: {key: flag, equals: true}\n",
		} {
			if _, err := loadNoPanic(withInputs(t, whenInput(bLines))); err != nil {
				t.Errorf("rule %q refused: %v", strings.TrimSpace(bLines), err)
			}
		}
	})
}
