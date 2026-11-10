package model

import (
	"errors"
	"strings"
	"testing"
)

// groupInput is an input of object type with two string properties, a and b, and the given
// extra lines at the end of the input (the lines of an exclusive group, for example).
func groupInput(extra string) string {
	return "  inputs:\n    cfg:\n      type: object\n      properties:\n        a: {type: string}\n        b: {type: string}\n" + extra
}

// A malformed exclusive group in an input schema is refused at load, with the path of
// the node that holds it. The rules are the ones of pkg/oam, applied to every node of
// the declared schema: fewer than two keys, a key the node does not declare, a key twice
// across the groups, a Required key, and groups on a node that is not an object. The
// README says so.
func TestLoad_RefusesMalformedExclusiveGroups(t *testing.T) {
	cases := []struct {
		name  string
		block string
		path  string // the path the error must name
		want  string // the rule the error must state
	}{
		{
			"undeclared key",
			groupInput("      exclusive:\n        - keys: [a, missing]\n"),
			"spec.inputs.cfg", "not a declared property",
		},
		{
			"fewer than two keys",
			groupInput("      exclusive:\n        - keys: [a]\n"),
			"spec.inputs.cfg", "a group needs at least two",
		},
		{
			"key listed twice",
			groupInput("      exclusive:\n        - keys: [a, b]\n        - keys: [a, b]\n"),
			"spec.inputs.cfg", "twice",
		},
		{
			"required key",
			"  inputs:\n    cfg:\n      type: object\n      properties:\n        a: {type: string, required: true}\n        b: {type: string}\n      exclusive:\n        - keys: [a, b]\n",
			"spec.inputs.cfg", "Required",
		},
		{
			"groups on a string node",
			"  inputs:\n    cfg:\n      type: string\n      exclusive:\n        - keys: [a, b]\n",
			"spec.inputs.cfg", "non-object node",
		},
		{
			"undeclared key in a nested property",
			"  inputs:\n    cfg:\n      type: object\n      properties:\n        inner:\n          type: object\n          properties:\n            a: {type: string}\n            b: {type: string}\n          exclusive:\n            - keys: [a, missing]\n",
			"spec.inputs.cfg.properties.inner", "not a declared property",
		},
		{
			"undeclared key in array items",
			"  inputs:\n    cfg:\n      type: array\n      items:\n        type: object\n        properties:\n          a: {type: string}\n          b: {type: string}\n        exclusive:\n          - keys: [a, missing]\n",
			"spec.inputs.cfg.items", "not a declared property",
		},
		{
			"groups on a non-object nested property",
			"  inputs:\n    cfg:\n      type: object\n      properties:\n        inner:\n          type: string\n          exclusive:\n            - keys: [a, b]\n",
			"spec.inputs.cfg.properties.inner", "non-object node",
		},
		{
			"groups on an array node",
			"  inputs:\n    cfg:\n      type: array\n      items: {type: string}\n      exclusive:\n        - keys: [a, b]\n",
			"spec.inputs.cfg", "non-object node",
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
			if !strings.Contains(err.Error(), tc.path+":") {
				t.Fatalf("error %q does not name the path %q", err, tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not state %q", err, tc.want)
			}
		})
	}
}

// A well-formed group still loads, at the top of an input and in a nested node.
func TestLoad_AcceptsWellFormedExclusiveGroups(t *testing.T) {
	block := "  inputs:\n    cfg:\n      type: object\n      properties:\n        a: {type: string}\n        b: {type: string}\n        inner:\n          type: object\n          properties:\n            c: {type: string}\n            d: {type: string}\n          exclusive:\n            - keys: [c, d]\n              required: true\n      exclusive:\n        - keys: [a, b]\n"
	if m, err := loadNoPanic(withInputs(t, block)); err != nil {
		t.Fatalf("Load refused a well-formed group: %v (model %+v)", err, m)
	}
}
