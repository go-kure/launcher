package patch

import (
	"bytes"
	"testing"
)

// A document can declare one anchor name twice, and an alias reads the nearest declaration before
// it. A patch that swaps the two items that declare `&k` moves the alias to the other declaration,
// so the alias key would read `first` where the resource holds `second`. The text still loads, so
// loading alone does not catch it: the write-back compares the data the text loads to with the
// patched resource, and writes the patched resource when they differ.
func TestWriteBack_FallsBackWhenAnAnchorNameIsReusedAndItsItemsSwap(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: a\n      &k first: x\n    - name: b\n      &k second: y\n    - name: c\n      *k : z\n"
	doc := loadOneDocument(t, src)
	swapped := []any{
		map[string]any{"name": "b", "second": "y"},
		map[string]any{"name": "a", "first": "x"},
		map[string]any{"name": "c", "second": "z"},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: swapped}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	again := requireLoads(t, out)
	want, err := marshalResource(doc.Resource.Object)
	if err != nil {
		t.Fatalf("marshalResource: %v", err)
	}
	got, err := marshalResource(again.Documents[0].Resource.Object)
	if err != nil {
		t.Fatalf("marshalResource of the loaded text: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the written text loads to other data than the patched resource.\nwant:\n%s\ngot:\n%s\nwritten:\n%s", want, got, out)
	}
}
