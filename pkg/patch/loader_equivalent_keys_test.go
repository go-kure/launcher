package patch

import (
	"fmt"
	"strings"
	"testing"
)

// The YAML decoder merges two keys that decode to the same Go value (`0x10` and `16`, `1.0`
// and `1.00`, `true` and `True`, `null` and `~`) into one entry before the conversion check
// runs, so the first value used to be lost without an error. The loaders now refuse the
// document. Every key is decoded into the key type the decoder gives its mapping. The root
// mapping decodes into a string-keyed map, so `0x10` and `16` there stay two keys, while a key
// and its alias read as one string and are refused.

const equivalentKeysHead = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: k\n"

type keyRow struct {
	name string
	body string
}

// resourceRefused holds nested mappings that must be refused by both resource loaders.
var resourceRefused = []keyRow{
	{"int_hex_and_decimal", "data:\n  0x10: a\n  16: b\n"},
	{"int_decimal_and_hex", "data:\n  16: a\n  0x10: b\n"},
	{"float_two_spellings", "data:\n  1.0: a\n  1.00: b\n"},
	{"bool_two_spellings", "data:\n  true: a\n  True: b\n"},
	{"null_two_spellings", "data:\n  null: a\n  ~: b\n"},
	{"alias_key", "k: &k 16\ndata:\n  0x10: a\n  *k : b\n"},
	{"two_levels_deep", "data:\n  x:\n    0x10: a\n    16: b\n"},
	{"list_item", "spec:\n  items:\n    - 0x10: a\n      16: b\n"},
	{"alias_of_own_anchor", "data:\n  &k 16: a\n  *k : b\n"},
	{"alias_named_like_its_key", "data:\n  &16 16: a\n  *16 : b\n"},
	{"quoted_merge_text_and_binary_key", "data:\n  \"<<\": a\n  !!binary PDw=: b\n"},
	{"merge_source_into_general_map", "data:\n  <<:\n    0x10: a\n    16: b\n  5: c\n"},
	{"merge_source_in_sequence", "data:\n  <<:\n    - 0x10: a\n      16: b\n  5: c\n"},
	{"merge_source_by_alias", "<<: &b\n  0x10: a\n  16: b\ndata:\n  <<: *b\n  5: c\n"},
	// A string-keyed map still collapses a key and its alias, because both decode to one string.
	{"root_alias_key", "&k 16: first\n*k : second\n"},
	{"string_map_alias_key", "data:\n  &k name: first\n  *k : second\n"},
	// A merge source keeps the key type of the mapping that names the first merge.
	{"nested_merge_in_general_map", "data:\n  5: c\n  <<:\n    <<:\n      0x10: a\n      16: b\n"},
	// A null key does not decode into a string, so the decoder skips it and does not hold "".
	{"null_key_does_not_hold_empty_string", "data:\n  <<:\n    - null: unused\n    - \"\": {0x10: first, 16: second}\n"},
}

// resourceAccepted holds documents that must keep loading.
var resourceAccepted = []keyRow{
	{"root_keys_keep_authored_text", "0x10: a\n16: b\n"},
	{"merge_key_override", "base: &b\n  16: a\ndata:\n  <<: *b\n  0x10: z\n"},
	{"distinct_keys", "data:\n  16: a\n  17: b\n"},
	{"single_non_string_key", "data:\n  0x10: a\n"},
	// A merge source is decoded into the map of the mapping that names it, so a source of a
	// string-keyed mapping keeps each key as written.
	{"root_merge_source_keeps_authored_keys", "<<:\n  0x10: a\n  16: b\n"},
	{"merge_source_of_string_map_keeps_authored_keys", "data:\n  <<:\n    0x10: a\n    16: b\n  z: c\n"},
	// The merge key is no entry, so it never pairs with a key that reads "<<".
	{"merge_key_beside_binary_key", "data:\n  <<:\n    x: a\n  !!binary PDw=: b\n"},
	// The decoder skips a merged entry whose key the mapping holds, before it reads the value.
	{"merge_value_overridden_by_explicit_key", "data:\n  <<:\n    x:\n      0x10: a\n      16: b\n  x: kept\n"},
}

func TestLoaders_RefuseEquivalentNonStringKeys(t *testing.T) {
	loaders := map[string]func(string) error{
		"multi": func(s string) error {
			_, err := LoadResourcesFromMultiYAML(strings.NewReader(s))
			return err
		},
		"structure": func(s string) error {
			_, err := LoadResourcesWithStructure(strings.NewReader(s))
			return err
		},
	}
	for lname, load := range loaders {
		for _, tt := range resourceRefused {
			t.Run(lname+"/refused/"+tt.name, func(t *testing.T) {
				err := load(equivalentKeysHead + tt.body)
				if err == nil {
					t.Fatalf("the loader accepted two keys that read the same:\n%s", tt.body)
				}
				if !strings.Contains(err.Error(), "both read") {
					t.Errorf("error does not name the colliding keys: %v", err)
				}
			})
		}
		for _, tt := range resourceAccepted {
			t.Run(lname+"/accepted/"+tt.name, func(t *testing.T) {
				if err := load(equivalentKeysHead + tt.body); err != nil {
					t.Errorf("the loader refused a valid document: %v\n%s", err, tt.body)
				}
			})
		}
	}
}

// A patch file is decoded the same way, so the pair used to collapse in a patch value. The
// list form decodes the patch field of each entry into a string-keyed map, like a root.
func TestPatchLoaders_RefuseEquivalentNonStringKeys(t *testing.T) {
	refused := []keyRow{
		{"simple_form", "data.map:\n  0x10: a\n  16: b\n"},
		{"simple_form_two_levels", "data.map:\n  x:\n    0x10: a\n    16: b\n"},
		{"list_form_field_level", "- target: k\n  patch:\n    data.map:\n      0x10: a\n      16: b\n"},
		{"list_form_strategic", "- target: k\n  type: strategic\n  patch:\n    data:\n      0x10: a\n      16: b\n"},
		{"list_form_second_entry", "- target: k\n  patch:\n    a.b: 1\n- target: k\n  type: strategic\n  patch:\n    data:\n      1.0: a\n      1.00: b\n"},
		{"list_form_patch_from_merge_source", "- target: k\n  <<:\n    patch:\n      data.map:\n        0x10: a\n        16: b\n"},
		{"list_form_patch_by_alias", "- target: k\n  x: &p\n    data.map:\n      0x10: a\n      16: b\n  patch: *p\n"},
		{"simple_form_root_alias_key", "&k a.b: 1\n*k : 2\n"},
		{"list_form_binary_field_name", "- {target: k, !!binary cGF0Y2g=: {data.map: {0x10: a, 16: b}}}\n"},
		{"list_form_alias_field_name", "- {target: k, name: &p patch, *p : {data.map: {0x10: a, 16: b}}}\n"},
		{"list_form_entry_by_alias", "- target: k\n  patch:\n    a.b: 1\n  x: &e\n    target: j\n    patch:\n      data.map:\n        0x10: a\n        16: b\n- *e\n"},
	}
	accepted := []keyRow{
		{"simple_form_root_keys_are_paths", "0x10: a\n16: b\n"},
		{"list_form_patch_keys_are_paths", "- target: k\n  patch:\n    0x10: a\n    16: b\n"},
		{"distinct_nested_keys", "data.map:\n  16: a\n  17: b\n"},
		// The entry's own patch field wins over a merged one, and the decoder never reads the latter.
		{"list_form_own_patch_beside_merged_patch", "- {target: k, patch: {a: 1}, <<: {patch: {data.map: {0x10: a, 16: b}}}}\n"},
	}
	for _, tt := range refused {
		t.Run("refused/"+tt.name, func(t *testing.T) {
			_, err := LoadYAMLPatchFile(strings.NewReader(tt.body), nil)
			if err == nil {
				t.Fatalf("the patch loader accepted two keys that read the same:\n%s", tt.body)
			}
			if !strings.Contains(err.Error(), "both read") {
				t.Errorf("error does not name the colliding keys: %v", err)
			}
		})
	}
	for _, tt := range accepted {
		t.Run("accepted/"+tt.name, func(t *testing.T) {
			if _, err := LoadYAMLPatchFile(strings.NewReader(tt.body), nil); err != nil {
				t.Errorf("the patch loader refused a valid patch file: %v\n%s", err, tt.body)
			}
		})
	}
}

// A literal repeat of a key is the decoder's own duplicate error, so the check leaves it alone
// and the message stays the decoder's.
func TestLoaders_LiteralRepeatStaysTheDecodersError(t *testing.T) {
	_, err := LoadResourcesFromMultiYAML(strings.NewReader(equivalentKeysHead + "data:\n  16: a\n  16: b\n"))
	if err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Fatalf("want the decoder's duplicate-key error, got %v", err)
	}
}

// Many distinct inline merge sources are valid and the parent loads them, so the check must not
// refuse them for their number.
func TestLoaders_ManyInlineMergeSourcesLoad(t *testing.T) {
	doc := equivalentKeysHead + "data:\n  <<:\n" + strings.Repeat("    - {}\n", 100001)
	for name, load := range map[string]func(string) error{
		"multi": func(s string) error {
			_, err := LoadResourcesFromMultiYAML(strings.NewReader(s))
			return err
		},
		"structure": func(s string) error {
			_, err := LoadResourcesWithStructure(strings.NewReader(s))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := load(doc); err != nil {
				t.Errorf("the loader refused a valid document with many inline merge sources: %v", err)
			}
		})
	}
}

// Nested reuse of a merge source through aliases grows exponentially. The check stops it with
// its own error before the walk gets long, so the decoder's later alias check never sees it.
func TestLoaders_AliasReuseOfMergeSourcesStopsEarly(t *testing.T) {
	var b strings.Builder
	b.WriteString(equivalentKeysHead + "l0: &l0 {x: 1}\n")
	for i := 1; i <= 22; i++ {
		fmt.Fprintf(&b, "l%d: &l%d {a: {<<: *l%d}, b: {<<: *l%d}}\n", i, i, i-1, i-1)
	}
	b.WriteString("data: {<<: *l22}\n")
	_, err := LoadResourcesFromMultiYAML(strings.NewReader(b.String()))
	if err == nil || !strings.Contains(err.Error(), "too often") {
		t.Fatalf("want the check's own reuse error, got %v", err)
	}
}
