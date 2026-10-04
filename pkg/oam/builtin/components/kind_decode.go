package components

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// This file holds the strict decode the kind components share whose properties
// are the top-level fields of an upstream spec type: the cnpg-pooler,
// cnpg-database and cnpg-objectstore kinds (cnpg_common.go) and the kinds of
// go-kure/launcher#790. cnpg-cluster predates it and keeps its own copies;
// folding them together is a separate change.

// decodeKindSpec decodes a kind component's properties strictly into the
// upstream spec type T, under the package's null contract, exactly as
// CnpgClusterHandler.ToApplicationConfig does: the properties are read as
// their JSON serialization (jsonProperties), a null key is dropped and a null
// array element refused by path, and an unknown key or a wrongly typed value
// at any depth is an error. The unstripped tree is decoded too, so a key the
// type does not declare is refused even when its value is null. kind names the
// upstream type in the error ("postgresql.cnpg.io/v1 PoolerSpec").
//
// It returns the decoded spec and the stripped property tree, which
// refuseUncarriedSpecValues and any authorship check read.
func decodeKindSpec[T any](props map[string]any, kind string) (*T, map[string]any, error) {
	stripped, raw, err := jsonProperties(props)
	if err != nil {
		return nil, nil, err
	}
	spec, _, err := builtin.DecodeStrictJSON[T](stripped)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "properties do not decode into a %s", kind)
	}
	if _, _, err := builtin.DecodeStrictJSON[T](raw); err != nil {
		return nil, nil, errors.Wrapf(err, "properties do not decode into a %s", kind)
	}
	return spec, stripped, nil
}

// defaultedZeroFields lists the fields of one upstream spec type on which an
// authored 0 or false cannot be carried: the type's encoding omits a zero
// there, and the object's defaulter applies a non-zero default in its place.
// The zero value lists none.
type defaultedZeroFields struct {
	// api names the API types in the refusal ("CloudNativePG").
	api string
	// defaulter is who applies the default to an omitted field ("operator").
	defaulter string
	// fields maps a json path, with [] for an array element, to that default.
	fields map[string]string
}

// refuseUncarriedSpecValues is refuseUncarriedValues for any spec type: it
// refuses an authored 0 or false that spec's encoding omits on a field of
// defaulted, where the defaulter would apply its non-zero default instead, and
// two spellings of one field in the same object, of which encoding/json keeps
// only one.
func refuseUncarriedSpecValues(authored map[string]any, spec any, defaulted defaultedZeroFields) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return errors.Wrap(err, "internal: encode the decoded spec")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var encoded any
	if err := dec.Decode(&encoded); err != nil {
		return errors.Wrap(err, "internal: decode the encoded spec")
	}
	return compareCarriedIn(authored, encoded, "", "", defaulted)
}

// compareCarriedIn is compareCarried with the defaulted-zero list passed in
// rather than read from cnpgClusterDefaultedZeroFields; the walk is the same.
func compareCarriedIn(authored, encoded any, path, field string, defaulted defaultedZeroFields) error {
	switch a := authored.(type) {
	case map[string]any:
		e, ok := encoded.(map[string]any)
		if !ok {
			return nil
		}
		join := func(base, k string) string {
			if base == "" {
				return k
			}
			return base + "." + k
		}
		claimed := make(map[string]string, len(a))
		var unmatched []string
		for _, k := range slices.Sorted(maps.Keys(a)) {
			child := join(path, k)
			ek, present := encodedKey(e, k)
			if !present {
				for _, prev := range unmatched {
					if strings.EqualFold(prev, k) {
						return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, join(path, prev))
					}
				}
				unmatched = append(unmatched, k)
				if def, ok := lookupFolded(defaulted.fields, join(field, k)); ok && isOmittedZero(a[k]) {
					return errors.Errorf("%s: %v cannot be carried by the %s API types (the field is omitted when zero, so the %s would apply its default %s)", child, a[k], defaulted.api, defaulted.defaulter, def)
				}
				continue
			}
			if other, dup := claimed[ek]; dup {
				return errors.Errorf("%s: sets the same field as %s (field names match case-insensitively, so one value would be dropped)", child, other)
			}
			claimed[ek] = child
			if err := compareCarriedIn(a[k], e[ek], child, join(field, ek), defaulted); err != nil {
				return err
			}
		}
	case []any:
		e, ok := encoded.([]any)
		if !ok {
			return nil
		}
		for i := range min(len(a), len(e)) {
			if err := compareCarriedIn(a[i], e[i], fmt.Sprintf("%s[%d]", path, i), field+"[]", defaulted); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookupFolded returns the value of m's key that matches field
// case-insensitively, as defaultedZeroField does for the Cluster's list.
func lookupFolded(m map[string]string, field string) (string, bool) {
	for _, known := range slices.Sorted(maps.Keys(m)) {
		if strings.EqualFold(known, field) {
			return m[known], true
		}
	}
	return "", false
}
