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
