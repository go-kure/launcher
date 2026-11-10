package oam

import (
	"strings"
	"testing"
)

// CheckSchemaNodeGroups checks the groups of one declared node and does not recurse. It
// reports the same five malformed shapes as the walk that checks a whole schema, with the
// path it is given, so a caller that walks the tree itself gets the rules from one place.
func TestCheckSchemaNodeGroups(t *testing.T) {
	two := map[string]PropertySchema{
		"a": {Type: PropertyTypeString},
		"b": {Type: PropertyTypeString},
	}
	cases := []struct {
		name  string
		field PropertySchema
		want  string // empty: the node is well formed
	}{
		{"well formed", PropertySchema{Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}}}, ""},
		{"no groups", PropertySchema{Type: PropertyTypeString}, ""},
		{"fewer than two keys", PropertySchema{Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a"}}}}, "a group needs at least two"},
		{"undeclared key", PropertySchema{Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "missing"}}}}, "not a declared property"},
		{"key twice", PropertySchema{Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}, {Keys: []string{"a", "b"}}}}, "twice"},
		{"required key", PropertySchema{
			Type:       PropertyTypeObject,
			Properties: map[string]PropertySchema{"a": {Type: PropertyTypeString, Required: true}, "b": {Type: PropertyTypeString}},
			Exclusive:  []ExclusiveGroup{{Keys: []string{"a", "b"}}},
		}, "Required"},
		{"groups on a non-object node", PropertySchema{Type: PropertyTypeString, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}}}, "non-object node"},
		// The function checks this node and no other: a malformed group one level down
		// is the business of the caller that visits that node.
		{"does not recurse into a property", PropertySchema{
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"inner": {Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "missing"}}}},
			},
		}, ""},
		{"does not recurse into items", PropertySchema{
			Type:  PropertyTypeArray,
			Items: &PropertySchema{Type: PropertyTypeObject, Properties: two, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "missing"}}}},
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSchemaNodeGroups(tc.field, "spec.inputs.cfg")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckSchemaNodeGroups = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckSchemaNodeGroups = nil, want an error holding %q", tc.want)
			}
			if !strings.HasPrefix(err.Error(), "spec.inputs.cfg: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckSchemaNodeGroups = %q, want the path and %q", err, tc.want)
			}
		})
	}
}
