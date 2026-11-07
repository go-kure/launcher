package model

import "testing"

// yaml.v3 does not store a null key in a map with string keys: it skips the key. So a
// document with null keys, or with a null key beside the empty string, loses nothing
// and has no duplicate. Load accepts it. The duplicate check once decoded a null key
// to the empty string and refused these documents with a false duplicate-key error.
func TestLoad_KeepsNullKeysAndEmptyStringKey(t *testing.T) {
	cases := []struct {
		name string
		keys string
	}{
		{"empty string beside a null key", "        \"\": kept\n        null: ignored\n"},
		{"two null spellings", "        ~: a\n        null: b\n"},
		{"capitalised null spellings", "        Null: a\n        NULL: b\n"},
		{"explicit null tag", "        !!null \"\": a\n        null: b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := replace(t, "        owner: acme\n", "        owner: acme\n"+tc.keys)
			if _, err := loadNoPanic(doc); err != nil {
				t.Fatalf("a document with null keys was refused: %v", err)
			}
		})
	}
}

// yaml.v3 drops the whole entry of a null key in a map with string keys, so it never
// decodes the value under that key. Load accepts a document whose only fault sits in
// such a value, because the fault is not in the loaded model. The null-element walk
// and the free-form key check once visited those values and refused the document.
func TestLoad_SkipsValuesUnderNullKeys(t *testing.T) {
	cases := []struct {
		name string
		doc  []byte
	}{
		{"null entry in a list under a null input key", withInputs(t, "  inputs:\n    null:\n      types: [string, null]\n    image:\n      type: string\n")},
		{"null entry in a list under a tilde input key", withInputs(t, "  inputs:\n    ~:\n      types: [string, null]\n    image:\n      type: string\n")},
		{"duplicate free-form keys under a null property key", replace(t, "        replicas: \"${inputs.replicas}\"\n", "        replicas: \"${inputs.replicas}\"\n        null: {1: a, 01: b}\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadNoPanic(tc.doc); err != nil {
				t.Fatalf("a value under a null key was checked and refused: %v", err)
			}
		})
	}
}
