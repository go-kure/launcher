package patch

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// The tests of go-kure/launcher#1423 for the value of a merge key. The write-back pairs two list
// items by that value, so it must read the value as the resource reads it: the authored `0x50` and
// the written `80` are one value.

// extractMergeKeyValue returns the reading of the scalar, never its raw text. A null and an empty
// scalar read as "", which pairs nothing. A timestamp reads as its text, as the loader hands it to
// the resource. A quoted `"80"` and a plain 80 read alike, as they did when the raw text was used.
func TestExtractMergeKeyValue_ReadsTheValueAsTheResourceDoes(t *testing.T) {
	rows := []struct {
		name  string
		value string
		want  string
	}{
		{"hex", "0x50", "80"},
		{"octal", "0o120", "80"},
		{"binary", "0b1010000", "80"},
		{"decimal", "80", "80"},
		{"whole_float", "80.0", "80"},
		{"fraction", "1.5", "1.5"},
		{"quoted_number", "'80'", "80"},
		{"nan", ".nan", "NaN"},
		{"bool", "true", "true"},
		{"word", "web", "web"},
		{"date", "2026-11-07", "2026-11-07"},
		{"date_and_time", "2026-11-07T10:00:00Z", "2026-11-07T10:00:00Z"},
		// A null never pairs: it reads "", not "<nil>" and not its text.
		{"tilde", "~", ""},
		{"null", "null", ""},
		{"empty", "", ""},
		{"empty_string", "''", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte("port: "+row.value+"\n"), &doc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := extractMergeKeyValue(doc.Content[0], "port"); got != row.want {
				t.Errorf("extractMergeKeyValue(port: %s) = %q, want %q", row.value, got, row.want)
			}
		})
	}
}

// An unchanged item of a nested list keeps its authored key, whatever spelling its merge-key value
// has. Before the fix the authored value read as its raw text, which the written value never equals,
// so the item paired with nothing and its key `0x10` was written as "16".
func TestWriteBack_PairsNestedItemsWhoseMergeKeyValueIsSpelledAnotherWay(t *testing.T) {
	rows := []struct{ name, value string }{
		{"hex", "0x50"},
		{"octal", "0o120"},
		{"binary", "0b1010000"},
		{"whole_float", "80.0"},
	}
	for _, row := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(row.name+"_"+route, func(t *testing.T) {
				src := widgetHead + "spec:\n  items:\n    - name: parent\n      inner:\n        - port: " + row.value + "\n          0x10: x\n  other: s\n"
				doc := loadOneDocument(t, src)
				out := writeBack(t, doc, route, []string{"spec", "other"}, "z")
				requireExact(t, out, widgetOut+"spec:\n    items:\n        - inner:\n            - 0x10: x\n              port: 80\n          name: parent\n    other: z\n")
				requireLoads(t, out)
			})
		}
	}
}

// The same holds for a list that sits directly under a field (mergeSequenceNodes).
func TestWriteBack_PairsDirectItemsWhoseMergeKeyValueIsSpelledAnotherWay(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - port: 0x50\n      0x10: x\n  other: s\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"spec", "other"}, "z")
			requireExact(t, out, widgetOut+"spec:\n    items:\n        - 0x10: x\n          port: 80\n    other: z\n")
			requireLoads(t, out)
		})
	}
}

// Two authored items whose merge-key values read alike (`0x50` and `80`) are one value that repeats,
// so neither pairs. The patch keeps one item with the value 80. Before the fix the raw texts differed,
// so the written item paired with the authored `80` item and took its key spelling `16`.
func TestWriteBack_DoesNotPairAnItemWhenTwoAuthoredValuesReadAlike(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - port: 0x50\n      0x10: x\n    - port: 80\n      16: y\n"
	doc := loadOneDocument(t, src)
	items := []any{map[string]any{"port": 80, "16": "x"}}
	out := writeBack(t, doc, "patch", []string{"spec", "items"}, items)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          port: 80\n")
	requireLoads(t, out)
}

// A value that repeats in the written list pairs nothing, whatever the spelling of the authored
// value. This row also holds on the base, where the written list repeats the raw text.
func TestWriteBack_DoesNotPairAnItemWhenTheWrittenListRepeatsAReading(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - port: 0x50\n      0x10: x\n"
	doc := loadOneDocument(t, src)
	items := []any{
		map[string]any{"port": 80, "16": "x"},
		map[string]any{"port": 80, "16": "z"},
	}
	out := writeBack(t, doc, "patch", []string{"spec", "items"}, items)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          port: 80\n        - \"16\": z\n          port: 80\n")
	requireLoads(t, out)
}
