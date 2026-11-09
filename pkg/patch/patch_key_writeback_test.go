package patch

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// keyTexts returns the key text of each entry of the mapping under the top-level key parent,
// in document order.
func keyTexts(t *testing.T, text, parent string) []string {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		t.Fatalf("written text does not parse: %v\n%s", err, text)
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != parent {
			continue
		}
		var keys []string
		m := top.Content[i+1]
		for j := 0; j+1 < len(m.Content); j += 2 {
			keys = append(keys, m.Content[j].Value)
		}
		return keys
	}
	t.Fatalf("no top-level key %q in:\n%s", parent, text)
	return nil
}

// writeBack sets one field of a loaded document through a write-back route and returns the
// text the document writes. The patch route is ApplyPatchesToDocument and the strategic route
// is ApplyStrategicPatchesToDocument.
func writeBack(t *testing.T, doc *YAMLDocument, route string, path []string, value any) string {
	t.Helper()
	switch route {
	case "patch":
		op := PatchOp{Op: "replace", Path: strings.Join(path, "."), Value: value}
		if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
			t.Fatalf("ApplyPatchesToDocument: %v", err)
		}
	case "strategic":
		nested := value
		for i := len(path) - 1; i >= 0; i-- {
			nested = map[string]any{path[i]: nested}
		}
		applyStrategic(t, doc, nested.(map[string]any))
	default:
		t.Fatalf("unknown route %q", route)
	}
	return writtenBack(t, doc.Node)
}

const hexNaNFloat = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\ndata:\n  .nan: a\n  0x10: b\n  1.0: c\n  other: x\n"

// A non-string key keeps its authored spelling through a write-back, and the written text loads
// again. Before the fix the document held both the authored key and its converted spelling.
func TestWriteBack_KeepsAuthoredNonStringKeys(t *testing.T) {
	want := []string{".nan", "0x10", "1.0", "other"}
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route+"_unrelated_field", func(t *testing.T) {
			doc := loadOneDocument(t, hexNaNFloat)
			out := writeBack(t, doc, route, []string{"data", "other"}, "y")
			if got := keyTexts(t, out, "data"); !reflect.DeepEqual(got, want) {
				t.Errorf("keys after write-back = %q, want %q\n%s", got, want, out)
			}
			if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
				t.Errorf("the written text does not load again: %v\n%s", err, out)
			}
		})
		t.Run(route+"_value_change_under_key", func(t *testing.T) {
			doc := loadOneDocument(t, hexNaNFloat)
			out := writeBack(t, doc, route, []string{"data", "16"}, "z")
			if got := keyTexts(t, out, "data"); !reflect.DeepEqual(got, want) {
				t.Errorf("keys after write-back = %q, want %q\n%s", got, want, out)
			}
			if !strings.Contains(out, "0x10: z\n") {
				t.Errorf("the new value is not under the authored key 0x10:\n%s", out)
			}
		})
	}
}

// The key 1.0 reads "1" after fmt.Sprint of the float, the same spelling convertChecked gives.
// This pins that keyText and the loader cannot drift apart on a float key.
func TestWriteBack_KeepsAFloatKeySpelling(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\ndata:\n  1.0: c\n  other: x\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			requireString(t, doc.Resource.Object, "c", "data", "1")
			out := writeBack(t, doc, route, []string{"data", "other"}, "y")
			if got, want := keyTexts(t, out, "data"), []string{"1.0", "other"}; !reflect.DeepEqual(got, want) {
				t.Errorf("keys after write-back = %q, want %q\n%s", got, want, out)
			}
		})
	}
}

// A plain timestamp under an authored non-string key keeps its authored style.
func TestWriteBack_KeepsTheTimestampStyleUnderANonStringKey(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\ndata:\n  0x10: 2026-11-07\n  other: x\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"data", "other"}, "y")
			if !strings.Contains(out, "0x10: 2026-11-07\n") {
				t.Errorf("the timestamp lost its authored style or its key:\n%s", out)
			}
			if got, want := keyTexts(t, out, "data"), []string{"0x10", "other"}; !reflect.DeepEqual(got, want) {
				t.Errorf("keys after write-back = %q, want %q\n%s", got, want, out)
			}
		})
	}
}

// A list item that merges by name keeps the authored key and the comment above it.
func TestWriteBack_KeepsTheAuthoredKeyInAListItem(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  items:\n    - name: first\n      # about hex\n      0x10: b\n  other: x\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "y")
	if !regexp.MustCompile(`# about hex\s+0x10: b`).MatchString(out) {
		t.Errorf("the item lost its authored key or the comment above it:\n%s", out)
	}
	if strings.Contains(out, `"16"`) {
		t.Errorf("the converted key 16 is in the written text:\n%s", out)
	}
}

// A patch can replace a list item with one that holds the string keys "16" and "0x10". The item
// matches the authored item by name, and the key "16" reads the same as the authored `0x10`. The
// item must not take the authored text while another key of the item already has it.
func TestWriteBack_DoesNotRestoreAKeyTextAnotherKeyHolds(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  items:\n    - name: first\n      0x10: b\n"
	doc := loadOneDocument(t, src)
	op := PatchOp{Op: "replace", Path: "spec.items", Value: []any{map[string]any{"name": "first", "16": "b", "0x10": "c"}}}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A strategic patch can add the string key "0x10" beside the authored key `0x10`. The new key is
// written quoted, so the written text holds both keys and loads again with both values.
func TestWriteBack_AddsAKeyWhoseTextAnAuthoredKeyHolds(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\ndata:\n  0x10: a\n  other: x\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"data": map[string]any{"0x10": "new"}})
	out := writtenBack(t, doc.Node)
	again, err := LoadResourcesWithStructure(strings.NewReader(out))
	if err != nil {
		t.Fatalf("the written text does not load again: %v\n%s", err, out)
	}
	requireString(t, again.Documents[0].Resource.Object, "a", "data", "16")
	requireString(t, again.Documents[0].Resource.Object, "new", "data", "0x10")
}

// The loader decodes the root mapping into a string-keyed map, so a root key keeps its authored
// text in the resource (`0x10` stays "0x10", not "16"). The root pairs by that text, and a
// timestamp under a root key keeps its authored style.
func TestWriteBack_KeepsAnAuthoredRootKey(t *testing.T) {
	const src = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\n0x10: 2026-11-07\n.nan: n\ndata:\n  other: x\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"data", "other"}, "y")
			if strings.Count(out, "0x10:") != 1 || strings.Count(out, ".nan:") != 1 {
				t.Errorf("a root key is missing or written twice:\n%s", out)
			}
			if !strings.Contains(out, "0x10: 2026-11-07\n") {
				t.Errorf("the timestamp lost its authored style or its root key:\n%s", out)
			}
			if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
				t.Errorf("the written text does not load again: %v\n%s", err, out)
			}
		})
	}
}

// Guards. They pass before and after the fix, and they pin what the fix must not change.
func TestWriteBack_LeavesOtherKeysAlone(t *testing.T) {
	rows := []struct {
		name string
		data string
		want []string
	}{
		{"string_keys_only", "  a: 1\n  b: 2\n", []string{"a", "b", "other"}},
		{"spelling_equals_converted", "  1: a\n  true: b\n", []string{"1", "true", "other"}},
		{"timestamp_key", "  2026-11-07: a\n", []string{"2026-11-07", "other"}},
	}
	for _, tt := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(tt.name+"_"+route, func(t *testing.T) {
				src := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\ndata:\n" + tt.data + "  other: x\n"
				doc := loadOneDocument(t, src)
				out := writeBack(t, doc, route, []string{"data", "other"}, "y")
				if got := keyTexts(t, out, "data"); !reflect.DeepEqual(got, tt.want) {
					t.Errorf("keys after write-back = %q, want %q\n%s", got, tt.want, out)
				}
			})
		}
	}
}
