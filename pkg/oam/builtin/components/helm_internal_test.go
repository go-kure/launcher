package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// jsonKeys returns the json tag names of struct type t's fields.
func jsonKeys(t reflect.Type) []string {
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	slices.Sort(keys)
	return keys
}

// TestHelmRule_SchemaMatchesDecode ties the published schema to what the rule
// decodes: the top-level keys are helmProperties' fields plus the keys split
// off before the decode (the passthrough keys, secretValues and
// scopeOverrides), and source's
// declared keys are helmSource's fields. Authored-property validation and the
// strict decode then admit the same set.
func TestHelmRule_SchemaMatchesDecode(t *testing.T) {
	schema := HelmRule{}.PropertySchema()
	want := append(jsonKeys(reflect.TypeFor[helmProperties]()), helmOwnedKeys...)
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(schema)); !slices.Equal(got, want) {
		t.Errorf("schema keys = %v, want %v", got, want)
	}
	source := schema["source"]
	if source.AdditionalProperties {
		t.Errorf("source schema admits additional properties; the decode refuses them")
	}
	if got, want := slices.Sorted(maps.Keys(source.Properties)), jsonKeys(reflect.TypeFor[helmSource]()); !slices.Equal(got, want) {
		t.Errorf("source schema keys = %v, want %v", got, want)
	}
	ref := source.Properties["ref"]
	if ref.AdditionalProperties {
		t.Errorf("source.ref schema admits additional properties; the decode refuses them")
	}
	if got, want := slices.Sorted(maps.Keys(ref.Properties)), jsonKeys(reflect.TypeFor[helmGitRef]()); !slices.Equal(got, want) {
		t.Errorf("source.ref schema keys = %v, want %v", got, want)
	}
	for _, key := range helmFluxOnlyKeys {
		if !slices.Contains(helmPassthroughKeys, key) {
			t.Errorf("flux-only key %q is not a passthrough key, so template delivery could never see it", key)
		}
	}
}
