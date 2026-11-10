package patch

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
	// An alias value is checked where it is used. The anchor below sits in a merged entry the
	// mapping's own `y` overrides, so the decoder never reads it where it is defined, and the
	// alias is the only place that reads it.
	{"alias_value_of_anchor_in_skipped_merge_entry", "data:\n  y: q\n  <<: {y: &m {0x10: a, 16: b}}\n  z: *m\n"},
	{"alias_value_inside_sequence", "data:\n  y: q\n  <<: {y: &m {0x10: a, 16: b}}\n  z: [*m]\n"},
	{"alias_of_sequence_holding_bad_mapping", "data:\n  y: q\n  <<: {y: &s [{0x10: a, 16: b}]}\n  z: *s\n"},
	{"alias_target_holds_bad_mapping_inside", "data:\n  y: q\n  <<: {y: &n {inner: {0x10: a, 16: b}}}\n  z: *n\n"},
	{"alias_chain_through_two_skipped_anchors", "data:\n  y: q\n  w: r\n  <<: {y: &m {0x10: a, 16: b}, w: &n [*m]}\n  z: *n\n"},
	// One anchor, two readers: a string-keyed mapping merges it (its keys read as text and do not
	// collide), and another entry reads it as a value (a general map, where they collide).
	{"anchor_used_as_merge_source_and_as_value", "data:\n  y: q\n  <<: {y: &m {0x10: a, 16: b}}\nother:\n  <<: *m\n  z: *m\n"},
	// m is first reached through t while its own merge source s is being walked, so that walk of m skips s. The later
	// other: *m reads m with s not in progress, and the decoder then reads the v of s and collapses 0x10 and 16.
	{"alias_target_reached_while_its_merge_source_is_active", "data:\n  y: q\n  v: q\n  <<: &s\n    y: &m {y: kept, t: kept, <<: *s}\n    t: *m\n    v: {0x10: a, 16: b}\nother: *m\n"},
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
	// The check follows uses, never definitions: an anchor in a skipped place that nothing
	// aliases is never read, so nothing is lost and the document loads.
	{"unused_anchor_in_skipped_merge_entry", "data:\n  y: q\n  <<: {y: &m {0x10: a, 16: b}}\n"},
	{"alias_value_of_string_mapping", "data:\n  y: q\n  <<: {y: &m {a: 1, b: 2}}\n  z: *m\n"},
	{"alias_value_of_quoted_number_keys", "data:\n  y: q\n  <<: {y: &m {\"0x10\": a, \"16\": b}}\n  z: *m\n"},
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
		// A field the entry does not declare is never decoded, so an alias is the only reader.
		{"list_form_alias_of_anchor_in_unused_field", "- target: k\n  extra: &m {0x10: a, 16: b}\n  patch:\n    data.map: *m\n"},
		{"list_form_alias_of_anchor_in_overridden_merged_patch", "- {target: k, <<: {patch: {data.map: &m {0x10: a, 16: b}}}, patch: {a: *m}}\n"},
	}
	accepted := []keyRow{
		{"simple_form_root_keys_are_paths", "0x10: a\n16: b\n"},
		{"list_form_patch_keys_are_paths", "- target: k\n  patch:\n    0x10: a\n    16: b\n"},
		{"distinct_nested_keys", "data.map:\n  16: a\n  17: b\n"},
		// The entry's own patch field wins over a merged one, and the decoder never reads the latter.
		{"list_form_own_patch_beside_merged_patch", "- {target: k, patch: {a: 1}, <<: {patch: {data.map: {0x10: a, 16: b}}}}\n"},
		// An anchor in a field the entry does not use is not read while nothing aliases it.
		{"list_form_unused_extra_anchor", "- target: k\n  extra: &m {0x10: a, 16: b}\n  patch:\n    data.map: 1\n"},
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

// loadersByName holds the two resource loaders, so a test runs one document through both.
func loadersByName() map[string]func(string) error {
	return map[string]func(string) error{
		"multi": func(s string) error {
			_, err := LoadResourcesFromMultiYAML(strings.NewReader(s))
			return err
		},
		"structure": func(s string) error {
			_, err := LoadResourcesWithStructure(strings.NewReader(s))
			return err
		},
	}
}

// An alias that reaches its own anchor is the decoder's error. Following alias values must not
// turn it into a hang or a different message.
func TestLoaders_AliasValueCycleStaysTheDecodersError(t *testing.T) {
	for name, load := range loadersByName() {
		t.Run(name, func(t *testing.T) {
			err := load(equivalentKeysHead + "data: &x {a: *x}\n")
			if err == nil || !strings.Contains(err.Error(), "contains itself") {
				t.Fatalf("want the decoder's own cycle error, got %v", err)
			}
			if strings.Contains(err.Error(), "both read") {
				t.Errorf("the key check reported a collision for a cycle: %v", err)
			}
		})
	}
}

// Nested reuse of an alias value doubles at each level. The check follows an alias value once
// per node, so it ends at once and the decoder's own excessive-aliasing error is what the caller
// reads. This passes on a check that does not follow alias values at all: it is a guard for the
// memo, not a red test, and only a check that forgets the nodes it has walked fails it (by the
// deadline: 40 levels never finish without the memo).
func TestLoaders_AliasValueReuseIsLinear(t *testing.T) {
	var b strings.Builder
	b.WriteString(equivalentKeysHead + "l0: &l0 [a]\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "l%d: &l%d [*l%d, *l%d]\n", i, i, i-1, i-1)
	}
	b.WriteString("data: {z: *l40}\n")
	for name, load := range loadersByName() {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- load(b.String()) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "excessive aliasing") {
					t.Fatalf("want the decoder's excessive-aliasing error, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the key check did not end within 5s: it walks a reused alias value once per use")
			}
		})
	}
}
