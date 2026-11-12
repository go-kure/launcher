package patch

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const mergeKeyResource = `apiVersion: v1
kind: ConfigMap
metadata:
  name: merged
data:
  <<:
    a: x
`

// A patch that sets a merge key (`<<`) to a value that is not a mapping would be written as
// `!!merge <<: new`, and the loader refuses that text. The write-back returns an error and leaves
// the document as it was, where it used to return text that the next load refuses.
func TestWriteBack_MergeKeySetToAScalarIsRefused(t *testing.T) {
	routes := map[string]func(*YAMLDocument) error{
		"patch": func(doc *YAMLDocument) error {
			return doc.ApplyPatchesToDocument([]PatchOp{{Op: "replace", Path: "data.<<", Value: "new"}})
		},
		"strategic": func(doc *YAMLDocument) error {
			lookup, err := DefaultKindLookup()
			if err != nil {
				t.Fatalf("DefaultKindLookup: %v", err)
			}
			return doc.ApplyStrategicPatchesToDocument([]StrategicPatch{{Patch: map[string]any{"data": map[string]any{"<<": "new"}}}}, lookup)
		},
		"update": func(doc *YAMLDocument) error {
			doc.Resource.Object["data"].(map[string]any)["<<"] = "new"
			return doc.UpdateDocumentFromResource()
		},
	}
	for name, route := range routes {
		t.Run(name, func(t *testing.T) {
			doc := loadOneDocument(t, mergeKeyResource)
			before, err := yaml.Marshal(doc.Node)
			if err != nil {
				t.Fatalf("marshal the authored document: %v", err)
			}
			err = route(doc)
			if err == nil {
				out, _ := yaml.Marshal(doc.Node)
				t.Fatalf("no error, and the document now reads:\n%s", out)
			}
			if !strings.Contains(err.Error(), "<<") {
				t.Errorf("the error does not name the key: %v", err)
			}
			after, mErr := yaml.Marshal(doc.Node)
			if mErr != nil {
				t.Fatalf("marshal the document after the refusal: %v", mErr)
			}
			if string(after) != string(before) {
				t.Errorf("the document changed on a refusal:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// A merge key set to a mapping loads, so it is not refused. This guards the check against a
// refusal of every merge key.
func TestWriteBack_MergeKeySetToAMappingIsKept(t *testing.T) {
	doc := loadOneDocument(t, mergeKeyResource)
	out := writeBack(t, doc, "patch", []string{"data", "<<"}, map[string]any{"b": "y"})
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A quoted `"<<"` key is an ordinary string key, and the loader accepts it. A patch to another
// field must not be refused because the marshaled resource writes that key as a merge key: the
// merge gives the key back its authored quotes, and that text loads.
func TestWriteBack_QuotedMergeLikeKeyIsKept(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: quoted\ndata:\n  \"<<\": literal\n  other: x\n")
	out := writeBack(t, doc, "patch", []string{"data", "other"}, "z")
	if !strings.Contains(out, "\"<<\": literal") {
		t.Errorf("the quoted key was not kept:\n%s", out)
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}
