package patch

import (
	"strings"
	"testing"
)

// The write-back tests of go-kure/launcher#1400. Each row loads a document, writes one field back
// through the public route, and checks the exact written text and that the text loads again.

const keyHead = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\n"

// requireLoads fails when the written text does not load again.
func requireLoads(t *testing.T, out string) *YAMLDocumentSet {
	t.Helper()
	set, err := LoadResourcesWithStructure(strings.NewReader(out))
	if err != nil {
		t.Fatalf("the written text does not load again: %v\n%s", err, out)
	}
	return set
}

// An alias key (`*k`) is written as authored. The alias key reads like the key it points at
// (`&k 0x10` reads "16"), so it pairs with the resource key of that spelling. Before the fix it
// paired with nothing: the resource key "16" was added beside it and the document did not load.
func TestWriteBack_KeepsAnAuthoredAliasKey(t *testing.T) {
	rows := []struct {
		name string
		src  string
		want []string
		not  []string
	}{
		{
			name: "mapping",
			src:  keyHead + "  annotations:\n    &k 0x10: x\ndata:\n  *k : a\n  other: s\n",
			want: []string{"data:\n    *k: a\n    other: z\n", "&k 0x10: x\n"},
			not:  []string{"16:"},
		},
		{
			name: "root",
			src:  keyHead + "  annotations:\n    &k 0x10: x\n*k : r\ndata:\n  other: s\n",
			want: []string{"*k: r\n", "&k 0x10: x\n", "other: z\n"},
			not:  []string{"0x10: r", "16:"},
		},
		{
			name: "timestamp_under_the_alias_key",
			src:  keyHead + "  annotations:\n    &k 0x10: x\ndata:\n  *k : 2026-11-07\n  other: s\n",
			want: []string{"*k: 2026-11-07\n", "other: z\n"},
			not:  []string{"16:", `"2026-11-07"`},
		},
	}
	for _, tt := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(tt.name+"_"+route, func(t *testing.T) {
				doc := loadOneDocument(t, tt.src)
				out := writeBack(t, doc, route, []string{"data", "other"}, "z")
				for _, want := range tt.want {
					if !strings.Contains(out, want) {
						t.Errorf("the written text lacks %q:\n%s", want, out)
					}
				}
				for _, not := range tt.not {
					if strings.Contains(out, not) {
						t.Errorf("the written text holds %q:\n%s", not, out)
					}
				}
				requireLoads(t, out)
			})
		}
	}
}

// A list item keeps an alias key as authored when another field of the item is written.
func TestWriteBack_KeepsAnAuthoredAliasKeyInAListItem(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n  annotations:\n    &k 0x10: x\nspec:\n  items:\n    - name: first\n      *k : b\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	if !strings.Contains(out, "- *k: b\n") || strings.Contains(out, `"16"`) {
		t.Errorf("the item lost its alias key:\n%s", out)
	}
	requireLoads(t, out)
}

// A strategic patch can add the string key "0x10" to a mapping that holds an alias key reading
// "16". The two never repeat a text, because the alias key is not a key with the text "0x10", so
// the alias key stays and the new key is written quoted.
func TestWriteBack_AddsAKeyWhoseTextAnAliasKeyReads(t *testing.T) {
	const src = keyHead + "  annotations:\n    &k 0x10: x\ndata:\n  *k : a\n  other: s\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"data": map[string]any{"0x10": "new"}})
	out := writtenBack(t, doc.Node)
	if !strings.Contains(out, "*k: a\n") || !strings.Contains(out, `"0x10": new`) {
		t.Errorf("the alias key or the new key is not written as expected:\n%s", out)
	}
	again := requireLoads(t, out)
	requireString(t, again.Documents[0].Resource.Object, "a", "data", "16")
	requireString(t, again.Documents[0].Resource.Object, "new", "data", "0x10")
}
