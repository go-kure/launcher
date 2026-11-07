package model

import (
	"fmt"
	"strings"
	"testing"
)

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

// Two null keys with different spelling, such as `~` and `null`, are no duplicate
// (the first test above). Two keys with the same spelling are a duplicate to the YAML
// decoder, a null key or not. The README says both. This test pins the second. It
// passes before and after the text change, so it is a pin and discriminates nothing
// between the two sides.
func TestLoad_RefusesSameSpellingKeys(t *testing.T) {
	cases := []struct {
		name string
		keys string
	}{
		{"plain key twice", "        owner: other\n"},
		{"null twice", "        null: a\n        null: b\n"},
		{"tilde twice", "        ~: a\n        ~: b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(replace(t, "        owner: acme\n", "        owner: acme\n"+tc.keys))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !strings.Contains(err.Error(), "already defined") {
				t.Fatalf("error %q does not come from the duplicate-key rule of the YAML decoder", err)
			}
		})
	}
}

// The refusal of anchors, aliases and merge keys reads the whole document before yaml.v3
// decodes it. So it also applies under a null key, although the checks of decoded values
// skip that value. The README says so. This test pins that sentence and the line in the
// error. It passes before and after the change that fixed the text, so it is a pin: it
// discriminates nothing between the two sides.
func TestLoad_RefusesIndirectionUnderNullKey(t *testing.T) {
	cases := []struct {
		name   string
		block  string
		marker string // the text whose line the error must name
	}{
		{"anchor under a null key", "  inputs:\n    null: &a {type: string}\n    image:\n      type: string\n", "&a"},
		{"anchor under a tilde key", "  inputs:\n    ~: &a {type: string}\n    image:\n      type: string\n", "&a"},
		{"merge key under a null key", "  inputs:\n    null:\n      <<: {}\n    image:\n      type: string\n", "<<"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := withInputs(t, tc.block)
			i := strings.Index(string(doc), tc.marker)
			if i < 0 {
				t.Fatalf("test bug: %q is not in the document", tc.marker)
			}
			wantLine := fmt.Sprintf("line %d:", strings.Count(string(doc[:i]), "\n")+1)
			m, err := loadNoPanic(doc)
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if !strings.Contains(err.Error(), "anchors, aliases and merge keys") {
				t.Fatalf("error %q does not say that anchors, aliases and merge keys are refused", err)
			}
			if !strings.Contains(err.Error(), wantLine) {
				t.Fatalf("error %q does not name %s", err, wantLine)
			}
		})
	}
}
