package model

import (
	"errors"
	"strings"
	"testing"
)

// yaml.v3 decodes each struct key into a string. A key tagged !!binary is decoded
// through base64, so `!!binary dHlwZXM=` names the field `types`. A null entry under
// that key is dropped like any other, so Load refuses it with the path of the field.
func TestLoad_RefusesNullUnderBinaryTaggedKey(t *testing.T) {
	doc := withInputs(t, "  inputs:\n    image:\n      !!binary dHlwZXM=: [string, null]\n")
	m, err := loadNoPanic(doc)
	if err == nil {
		t.Fatalf("Load accepted the document: %+v", m)
	}
	const path = "spec.inputs.image.types[1]"
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "null") {
		t.Fatalf("error %q must wrap ErrInvalid, name %s and say null", err, path)
	}
}

// Go treats the map keys 0.0 and -0.0 as one key, so the later entry of this mapping
// silently replaces the earlier one. The two spellings differ, so yaml.v3 does not see
// a duplicate. Load refuses the mapping and wraps ErrInvalid.
func TestLoad_RefusesZeroAndNegativeZeroKeys(t *testing.T) {
	doc := withInputs(t, "  inputs:\n    image:\n      type: object\n      default: {0.0: first, -0.0: second}\n")
	m, err := loadNoPanic(doc)
	if err == nil {
		t.Fatalf("Load accepted the document: %+v", m)
	}
	const path = "spec.inputs.image.default"
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), path) ||
		!strings.Contains(err.Error(), "duplicate mapping key") || !strings.Contains(err.Error(), "line ") {
		t.Fatalf("error %q must wrap ErrInvalid, name %s, say duplicate mapping key and name the line", err, path)
	}
}

// Go keeps two NaN keys apart, so the mapping {.nan: first, .NaN: second} loses
// nothing and Load accepts it. The check once refused it because both keys print the
// same. This test fails on that code and passes on the map-equality check.
//
// The second case holds keys with a null value. A null value is legal in free-form
// content and the keys differ. It passes before and after the change, so it
// discriminates nothing against the unfixed code.
func TestLoad_KeepsNaNKeysAndNullValues(t *testing.T) {
	for _, def := range []string{"{.nan: first, .NaN: second}", "{1: , 2: }"} {
		if _, err := loadNoPanic(withInputs(t, "  inputs:\n    image:\n      type: object\n      default: "+def+"\n")); err != nil {
			t.Fatalf("default: %s was refused: %v", def, err)
		}
	}
}
