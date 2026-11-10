package patch

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mappingOf returns the mapping a node holds, through a document node.
func mappingOf(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// valueOf returns the value of the entry whose key node holds the text, or nil.
func valueOf(m *yaml.Node, text string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == text {
			return m.Content[i+1]
		}
	}
	return nil
}

// entryText renders the entry whose key node holds the text, as the marshaler writes it alone.
func entryText(t *testing.T, m *yaml.Node, text string) string {
	t.Helper()
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == text {
			out, err := yaml.Marshal(&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{m.Content[i], m.Content[i+1]}})
			if err != nil {
				t.Fatalf("render the entry %q: %v", text, err)
			}
			return string(out)
		}
	}
	return "<no entry>"
}

// Write-back keeps the text of an entry it does not touch, whatever the shape of its key, at the
// root of the resource and nested in it. This states the property over key shapes: every place
// that pairs a key of the document with a key of the patched resource has to pair by one reading,
// the text the loader gives the key, and not by the text the marshaler writes.
func TestWriteBack_KeepsAnUntouchedEntryOfEveryKeyShape(t *testing.T) {
	shapes := []struct {
		name  string
		key   string // the Value of the key node
		entry string // the authored entry, at the margin
	}{
		{"binary_key_not_utf8", "/w==", "!!binary /w==: a\n"},
		{"binary_key_not_utf8_timestamp_value", "/w==", "!!binary /w==: 2026-11-07\n"},
		{"binary_key_text", "YWJj", "!!binary YWJj: a\n"},
		{"timestamp_key", "2026-11-07", "2026-11-07: a\n"},
		{"timestamp_value", "stamp", "stamp: 2026-11-07\n"},
		{"merge_key", "<<", "<<:\n  a: x\n"},
		{"quoted_merge_like_key", "<<", "\"<<\": literal\n"},
		{"quoted_key", "0x10", "\"0x10\": a\n"},
		{"plain_non_string_key", "0x10", "0x10: a\n"},
		{"empty_key", "", "\"\": a\n"},
	}
	const head = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shapes\n"
	for _, shape := range shapes {
		for _, place := range []string{"root", "nested"} {
			for _, route := range []string{"patch", "strategic"} {
				t.Run(shape.name+"_"+place+"_"+route, func(t *testing.T) {
					var src string
					if place == "root" {
						src = head + "data:\n  other: x\n" + shape.entry
					} else {
						src = head + "data:\n  other: x\n  " + strings.ReplaceAll(strings.TrimSuffix(shape.entry, "\n"), "\n", "\n  ") + "\n"
					}
					doc := loadOneDocument(t, src)
					scope := func(n *yaml.Node) *yaml.Node {
						if place == "root" {
							return mappingOf(n)
						}
						return valueOf(mappingOf(n), "data")
					}
					before := entryText(t, scope(doc.Node), shape.key)
					if before == "<no entry>" {
						t.Fatalf("the authored document has no entry %q", shape.key)
					}
					out := writeBack(t, doc, route, []string{"data", "other"}, "z")
					var written yaml.Node
					if err := yaml.Unmarshal([]byte(out), &written); err != nil {
						t.Fatalf("the written text does not parse: %v\n%s", err, out)
					}
					if after := entryText(t, scope(&written), shape.key); after != before {
						t.Errorf("the untouched entry changed:\nbefore:\n%s\nafter:\n%s\nwritten:\n%s", before, after, out)
					}
					if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
						t.Errorf("the written text does not load again: %v\n%s", err, out)
					}
				})
			}
		}
	}
}
