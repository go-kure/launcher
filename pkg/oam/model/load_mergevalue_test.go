package model

import (
	"reflect"
	"strings"
	"testing"
)

// A plain `<<` is a merge key only as a mapping key. As a value it is the string `<<`,
// and Load accepts it. The README says so. yaml.v3 resolves the plain scalar `<<` to the
// short tag !!merge in every position, so the refusal of merge keys must test the key
// position and not the scalar. Anchors and aliases stay refused everywhere.
func TestLoad_AcceptsMergeSpellingAsValue(t *testing.T) {
	cases := []struct {
		name string
		doc  []byte
		get  func(*Model) any
		want any
	}{
		{
			"part property value",
			replace(t, `        replicas: "${inputs.replicas}"`, "        replicas: \"${inputs.replicas}\"\n        marker: <<"),
			func(m *Model) any { return m.Spec.Parts[0].Properties["marker"] },
			"<<",
		},
		{
			"list element",
			replace(t, `        replicas: "${inputs.replicas}"`, "        replicas: \"${inputs.replicas}\"\n        marker: [<<, x]"),
			func(m *Model) any { return m.Spec.Parts[0].Properties["marker"] },
			[]any{"<<", "x"},
		},
		{
			"schema default",
			withInputs(t, "  inputs:\n    tag:\n      type: string\n      default: <<\n"),
			func(m *Model) any { return m.Spec.Inputs["tag"].Default },
			"<<",
		},
		{
			"quoted spelling",
			replace(t, `        replicas: "${inputs.replicas}"`, "        replicas: \"${inputs.replicas}\"\n        marker: \"<<\""),
			func(m *Model) any { return m.Spec.Parts[0].Properties["marker"] },
			"<<",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(tc.doc)
			if err != nil {
				t.Fatalf("Load refused the document: %v", err)
			}
			if got := tc.get(m); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decoded value = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// A merge key is still refused wherever a mapping key can stand, whatever its spelling.
// The three existing cases in TestLoad_RefusesYAMLIndirection and
// TestLoad_RefusesIndirectionUnderNullKey cover the plain spelling. These two cover an
// explicit tag and a flow mapping.
func TestLoad_RefusesMergeKeySpellings(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{"explicit merge tag on a key", "  inputs:\n    image:\n      !!merge <<: {}\n"},
		{"merge key in a flow mapping", "  inputs: {image: {<<: {}}}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(withInputs(t, tc.block))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !strings.Contains(err.Error(), "anchors, aliases and merge keys") {
				t.Fatalf("error %q does not say that merge keys are refused", err)
			}
		})
	}
}
