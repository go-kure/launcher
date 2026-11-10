package model

import (
	"strings"
	"testing"
)

// A map key that is not written in plain form decodes to a different string than its
// spelling: `!!binary aW1hZ2U=` decodes to `image`. The path in an error names the key
// as decoded, the way the struct fields of the format are named, and never the raw
// spelling. The README says so.
func TestLoad_ErrorPathNamesDecodedKey(t *testing.T) {
	cases := []struct {
		name    string
		doc     []byte
		want    string // the path the error must hold
		notWant string // the raw spelling the error must not hold
	}{
		{
			"input key",
			withInputs(t, "  inputs:\n    !!binary aW1hZ2U=:\n      types: [string, null]\n"),
			"spec.inputs.image.types[1]", "aW1hZ2U=",
		},
		{
			"nested schema property key",
			withInputs(t, "  inputs:\n    image:\n      type: object\n      properties:\n        !!binary cG9ydA==:\n          types: [string, null]\n"),
			"spec.inputs.image.properties.port.types[1]", "cG9ydA==",
		},
		{
			"part property key with a duplicate key below it",
			replace(t, `        replicas: "${inputs.replicas}"`, "        !!binary cmVwbGljYXM=: {1: a, 01: b}"),
			"properties.replicas", "cmVwbGljYXM=",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(tc.doc)
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not hold the path %q", err, tc.want)
			}
			if strings.Contains(err.Error(), tc.notWant) {
				t.Fatalf("error %q holds the raw spelling %q of the key", err, tc.notWant)
			}
		})
	}
}
