package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// loadNoPanic calls Load and turns a panic into an error. One input that panics then
// fails its own case and never stops the other tests of the package.
func loadNoPanic(doc []byte) (m *Model, err error) {
	defer func() {
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("load panicked: %v", r)
		}
	}()
	return Load(doc)
}

// withInputs returns validModel with its spec.inputs block replaced by block.
func withInputs(t *testing.T, block string) []byte {
	t.Helper()
	i := strings.Index(validModel, "  inputs:\n")
	j := strings.Index(validModel, "  values:\n")
	if i < 0 || j < i {
		t.Fatal("test bug: the inputs block is not in validModel")
	}
	return []byte(validModel[:i] + block + validModel[j:])
}

// The model format has no use for YAML anchors, aliases or merge keys. yaml.v3 does
// not see a duplicate key when one of the keys is an alias, and it panics on a merge
// key next to a sequence key. A walk of the node tree refuses all three before decoding.
func TestLoad_RefusesYAMLIndirection(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{"duplicate input through an alias", "  inputs:\n    &k image:\n      type: string\n    *k :\n      type: integer\n"},
		{"merge key next to a sequence key", "  inputs: {? [image]: {type: string}, <<: {}}\n"},
		{"merge key with an alias", "  inputs:\n    base: &b\n      type: string\n    image:\n      <<: *b\n"},
		{"anchor alone", "  inputs:\n    image: &t\n      type: string\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := loadNoPanic(withInputs(t, tc.block))
			if err == nil {
				t.Fatalf("Load accepted the document: %+v", m)
			}
			if m != nil {
				t.Fatalf("Load returned a model together with an error: %+v", m)
			}
			if !strings.Contains(err.Error(), "anchors, aliases and merge keys") {
				t.Fatalf("error %q does not say that anchors, aliases and merge keys are refused", err)
			}
		})
	}
}

// An empty entry in types is no type. The single type field allows an empty value
// because "unset" means "look at types"; an entry of types has no such meaning.
func TestLoad_RefusesEmptyEntryInTypes(t *testing.T) {
	for _, types := range []string{`[""]`, `[string, ""]`} {
		_, err := loadNoPanic(withInputs(t, "  inputs:\n    image:\n      types: "+types+"\n"))
		if err == nil {
			t.Fatalf("types: %s was accepted", types)
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "spec.inputs.image") {
			t.Fatalf("types: %s: error %q must wrap ErrInvalid and name spec.inputs.image", types, err)
		}
	}
	// Control: a valid list of types still loads.
	if _, err := loadNoPanic(withInputs(t, "  inputs:\n    image:\n      types: [string, integer]\n")); err != nil {
		t.Fatalf("a valid types list was refused: %v", err)
	}
}

// Input names are a Go map, which keeps no document order, so they are checked in
// alphabetical order. The README and the Load doc comment say so. This pins that
// statement. It passes before and after the change that fixed the text, so it is a
// pin of documented behavior and not evidence of a fix.
func TestLoad_ChecksInputsInAlphabeticalOrder(t *testing.T) {
	_, err := loadNoPanic(withInputs(t, "  inputs:\n    z: {}\n    a: {}\n"))
	if err == nil || !strings.Contains(err.Error(), "spec.inputs.a") {
		t.Fatalf("the alphabetical first failure is spec.inputs.a: %v", err)
	}
}

// A YAML type error names the line, which points at the field, and has no field path.
// It does not wrap ErrInvalid. The README says so. This passes before and after the
// change that fixed the text, so it is a pin and not evidence of a fix.
func TestLoad_YAMLTypeErrorNamesTheLine(t *testing.T) {
	_, err := loadNoPanic(replace(t, `        in: "${[1, 2]}"`, "        in: []"))
	if err == nil || !strings.Contains(err.Error(), "line ") {
		t.Fatalf("a YAML type error must name the line: %v", err)
	}
	if errors.Is(err, ErrInvalid) {
		t.Fatalf("a YAML type error is an envelope failure, not ErrInvalid: %v", err)
	}
}
