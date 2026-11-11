package patch

import (
	"strings"
	"testing"
)

// The write-back tests of go-kure/launcher#1400. Each row loads a document, writes one field back
// through the public route, and checks the exact written text and that the text loads again.

const keyHead = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: keys\n"

// keyOut is keyHead and the anchored annotation of the rows below, as the write-back writes them.
const keyOut = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n    name: keys\n    annotations:\n        &k 0x10: x\n"

// requireLoads fails when the written text does not load again.
func requireLoads(t *testing.T, out string) *YAMLDocumentSet {
	t.Helper()
	set, err := LoadResourcesWithStructure(strings.NewReader(out))
	if err != nil {
		t.Fatalf("the written text does not load again: %v\n%s", err, out)
	}
	return set
}

// requireExact fails unless the written text is exactly the merged document. A test that expects the
// merged output asserts the whole text and not only that it loads, because the load check in the
// write-back would otherwise hide a merge that fell back to the patched resource.
func requireExact(t *testing.T, out, want string) {
	t.Helper()
	if out != want {
		t.Errorf("the written text is not the merged document.\nwant:\n%s\ngot:\n%s", want, out)
	}
}

// An alias key (`*k`) is written as authored. The alias key reads like the key it points at
// (`&k 0x10` reads "16"), so it pairs with the resource key of that spelling. Before the fix it
// paired with nothing: the resource key "16" was added beside it and the document did not load.
func TestWriteBack_KeepsAnAuthoredAliasKey(t *testing.T) {
	rows := []struct {
		name  string
		src   string
		exact string
	}{
		{
			name:  "mapping",
			src:   keyHead + "  annotations:\n    &k 0x10: x\ndata:\n  *k : a\n  other: s\n",
			exact: keyOut + "data:\n    *k: a\n    other: z\n",
		},
		{
			name:  "root",
			src:   keyHead + "  annotations:\n    &k 0x10: x\n*k : r\ndata:\n  other: s\n",
			exact: keyOut + "*k: r\ndata:\n    other: z\n",
		},
		{
			name:  "timestamp_under_the_alias_key",
			src:   keyHead + "  annotations:\n    &k 0x10: x\ndata:\n  *k : 2026-11-07\n  other: s\n",
			exact: keyOut + "data:\n    *k: 2026-11-07\n    other: z\n",
		},
	}
	for _, tt := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(tt.name+"_"+route, func(t *testing.T) {
				doc := loadOneDocument(t, tt.src)
				out := writeBack(t, doc, route, []string{"data", "other"}, "z")
				requireExact(t, out, tt.exact)
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
	requireExact(t, out, "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n    name: w\n    annotations:\n        &k 0x10: x\n"+
		"spec:\n    items:\n        - *k: b\n          name: first\n    other: z\n")
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
	requireExact(t, out, keyOut+"data:\n    *k: a\n    other: s\n    \"0x10\": new\n")
	again := requireLoads(t, out)
	requireString(t, again.Documents[0].Resource.Object, "a", "data", "16")
	requireString(t, again.Documents[0].Resource.Object, "new", "data", "0x10")
}

// A key with an explicit tag reads as the text it decodes to: `!!binary MHgxMA==` reads "0x10".
// A strategic patch can add the key "MHgxMA==", the raw text of that authored key. The authored key
// would then take its resource spelling "0x10", which another authored key (`0x10`) holds as its
// own text, so the written document would not load. The write-back finds that when it loads the
// merged text again, and the document is written from the patched resource: it loads, it carries the resource spelling, and
// the comments of the document are gone. The row pins that fallback, so a change cannot pass it by
// keeping the authored text or by refusing the patch.
func TestWriteBack_FallsBackWhenARenamedKeyWouldRepeatAKey(t *testing.T) {
	const src = keyHead + "# about data\ndata:\n  !!binary MHgxMA==: x\n  0x10: y\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"data": map[string]any{"MHgxMA==": "n"}})
	out := writtenBack(t, doc.Node)
	again := requireLoads(t, out)
	data := again.Documents[0].Resource.Object
	requireString(t, data, "x", "data", "0x10")
	requireString(t, data, "y", "data", "16")
	requireString(t, data, "n", "data", "MHgxMA==")
	if strings.Contains(out, "!!binary") || strings.Contains(out, "# about data") {
		t.Errorf("the written text keeps the authored tag or the comment above the mapping, so it is not the fallback:\n%s", out)
	}
}

// Where the renamed key repeats nothing, the authored key takes its resource spelling, the new key
// is added beside it, and the comment above the mapping stays: the merge ran, not the fallback.
// This is the control of the fallback row.
func TestWriteBack_RenamesATaggedKeyWhenNothingRepeats(t *testing.T) {
	const src = keyHead + "# about data\ndata:\n  !!binary MHgxMA==: x\n  other: y\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"data": map[string]any{"MHgxMA==": "n"}})
	out := writtenBack(t, doc.Node)
	requireLoads(t, out)
	requireExact(t, out, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n    name: keys\n# about data\n"+
		"data:\n    \"0x10\": x\n    other: \"y\"\n    \"MHgxMA==\": \"n\"\n")
}

const widgetHead = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n"

// widgetOut is widgetHead as the write-back writes it.
const widgetOut = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n    name: w\n"

// A key in a list item keeps its authored text when a list in the item, or a list with no merge key,
// is written back. Before the fix the key read "16" and the item was written with `"16"`.
func TestWriteBack_KeepsTheAuthoredKeyInNestedAndPlainLists(t *testing.T) {
	rows := []struct {
		name  string
		src   string
		exact string
	}{
		{
			name:  "list_nested_in_an_item_with_a_merge_key",
			src:   widgetHead + "spec:\n  items:\n    - name: first\n      inner:\n        - 0x10: b\n          m: n\n  other: s\n",
			exact: widgetOut + "spec:\n    items:\n        - inner:\n            - 0x10: b\n              m: \"n\"\n          name: first\n    other: z\n",
		},
		{
			name:  "list_with_no_merge_key",
			src:   widgetHead + "spec:\n  items:\n    - 0x10: b\n      m: n\n  other: s\n",
			exact: widgetOut + "spec:\n    items:\n        - 0x10: b\n          m: \"n\"\n    other: z\n",
		},
		{
			name:  "list_nested_in_a_list_with_no_merge_key",
			src:   widgetHead + "spec:\n  items:\n    - - 0x10: b\n        m: n\n  other: s\n",
			exact: widgetOut + "spec:\n    items:\n        - - 0x10: b\n            m: \"n\"\n    other: z\n",
		},
	}
	for _, tt := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(tt.name+"_"+route, func(t *testing.T) {
				doc := loadOneDocument(t, tt.src)
				out := writeBack(t, doc, route, []string{"spec", "other"}, "z")
				requireExact(t, out, tt.exact)
				requireLoads(t, out)
			})
		}
	}
}

// Items of a nested list pair by their merge key, so a patch that swaps two items gives each its
// own authored key: the first item keeps `0x10` and the second keeps `16`, which the two read the
// same. Pairing by position would give the item that moves to the front the key of the other.
func TestWriteBack_PairsNestedListItemsByMergeKey(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: first\n      inner:\n        - name: a\n          0x10: x\n        - name: b\n          16: y\n"
	doc := loadOneDocument(t, src)
	swapped := []any{
		map[string]any{"name": "first", "inner": []any{
			map[string]any{"name": "b", "16": "y"},
			map[string]any{"name": "a", "16": "x"},
		}},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: swapped}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - inner:\n            - 16: \"y\"\n              name: b\n"+
		"            - 0x10: x\n              name: a\n          name: first\n")
	requireLoads(t, out)
}

// After a delete the lists differ in length, so no item pairs by position: a survivor never takes
// the key text or a comment of another item. Each survivor is written with the key text the resource
// holds, and no comment of the list is written.
func TestWriteBack_DoesNotPairItemsByPositionAfterADelete(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    # ca\n    - 0x10: x\n      m: a\n    # cb\n    - 0x10: y\n      m: b\n    # cc\n    - 16: z\n      m: c\n"
	doc := loadOneDocument(t, src)
	op := PatchOp{Op: "delete", Path: "spec.items", Selector: "0"}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": \"y\"\n          m: b\n        - \"16\": z\n          m: c\n")
	requireLoads(t, out)
}

// A delete of the last item leaves a survivor that holds the same data as the item at its position.
// The lists differ in length, so the survivor still gets no pair and takes the resource spelling.
func TestWriteBack_DoesNotPairAnEqualItemWhenTheListLengthChanged(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - 0x10: x\n      m: a\n    - 0x10: y\n      m: b\n"
	doc := loadOneDocument(t, src)
	op := PatchOp{Op: "delete", Path: "spec.items", Selector: "1"}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          m: a\n")
	requireLoads(t, out)
}

// A timestamp compares by its text: the loader hands it to the resource as a string, and the item
// still holds the same data, so it keeps its authored key.
func TestWriteBack_PairsAnItemThatHoldsATimestamp(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - 0x10: x\n      at: 2026-11-07\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - 0x10: x\n          at: 2026-11-07\n    other: z\n")
	requireLoads(t, out)
}

// An item added at the end leaves the first item holding the same data as the item at its position.
// The lists differ in length, so no item pairs and every item takes the resource spelling.
func TestWriteBack_DoesNotPairAnEqualItemWhenAnItemIsAdded(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - 0x10: x\n      m: a\n"
	doc := loadOneDocument(t, src)
	longer := []any{
		map[string]any{"16": "x", "m": "a"},
		map[string]any{"16": "y", "m": "b"},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: longer}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          m: a\n        - \"16\": \"y\"\n          m: b\n")
	requireLoads(t, out)
}

// An item that uses a merge entry (`<<`) differs from the written item, which holds the expanded
// entries, so it pairs with nothing although the patch does not touch it.
func TestWriteBack_DoesNotPairAnItemItCannotRead(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - <<: {m: a}\n      0x10: x\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          m: a\n    other: z\n")
	requireLoads(t, out)
}

// A list with no merge key pairs its items by position when the two lists have the same length and
// the items hold the same data, and then only the key text follows the pair, never a comment. A
// position is no proof that two items are the same item. Here a patch changes a field outside the
// list: the items pair, they keep `0x10`, and each comment stays out of the output.
func TestWriteBack_DoesNotCopyCommentsByPosition(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    # ca\n    - 0x10: x\n      m: a\n    # cb\n    - 0x10: y\n      m: b\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - 0x10: x\n          m: a\n        - 0x10: \"y\"\n          m: b\n    other: z\n")
	requireLoads(t, out)
}

// A patch that swaps two items of one length leaves them with no pair, so each item takes the
// spelling the resource gives it, and no comment of the list is written.
func TestWriteBack_SwappedItemsGetNoKeyTextAndNoComment(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    # ca\n    - 0x10: x\n      m: a\n    # cb\n    - 0x10: y\n      m: b\n"
	doc := loadOneDocument(t, src)
	swapped := []any{
		map[string]any{"16": "y", "m": "b"},
		map[string]any{"16": "x", "m": "a"},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: swapped}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": \"y\"\n          m: b\n        - \"16\": x\n          m: a\n")
	requireLoads(t, out)
}

// A list with no merge key pairs two items by position only when they hold the same data. Here the
// patch reverses two items whose keys read the same ("16"): position 0 held `0x10` and position 1
// held `16`, and pairing by position wrote `0x10` on the item that was never the first. With no
// pair, each item takes the spelling the resource gives it.
func TestWriteBack_DoesNotMoveKeyTextWhenItemsOfOneLengthReorder(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - 0x10: x\n      m: a\n    - 16: z\n      m: b\n"
	doc := loadOneDocument(t, src)
	reversed := []any{
		map[string]any{"16": "z", "m": "b"},
		map[string]any{"16": "x", "m": "a"},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: reversed}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": z\n          m: b\n        - \"16\": x\n          m: a\n")
	requireLoads(t, out)
}

// A patch that changes a field of one item leaves the item with no pair, although the list keeps
// its length: the item holds other data, so nothing shows that it is the item at that position.
func TestWriteBack_DoesNotPairAnItemThePatchChanged(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - 0x10: x\n      m: a\n"
	doc := loadOneDocument(t, src)
	op := PatchOp{Op: "replace", Path: "spec.items", Value: []any{map[string]any{"16": "x", "m": "changed"}}}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          m: changed\n")
	requireLoads(t, out)
}

// Two authored items of a list that share one merge-key value cannot be told apart, so neither pairs
// with the item the resource holds: the first takes no key text and no comment of the second. Here
// both inner items are named `shared`, and the patch changes a field elsewhere.
func TestWriteBack_DoesNotPairItemsThatShareAMergeKeyValue(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: parent\n      inner:\n        - name: shared\n          0x10: x\n" +
		"        - name: shared\n          # ck\n          16: z\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - inner:\n            - \"16\": x\n              name: shared\n"+
		"            - \"16\": z\n              name: shared\n          name: parent\n    other: z\n")
	requireLoads(t, out)
}

// Two items of the written list that share one merge-key value cannot be told apart either, so
// neither pairs with the authored item that holds the same value: the added item takes neither the
// key text nor the comment of the authored one. Here the patch replaces the list and adds a second
// inner item named `shared`.
func TestWriteBack_DoesNotPairAnItemWhenTheWrittenListRepeatsAMergeKey(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: parent\n      inner:\n        - name: shared\n          # ck\n          0x10: x\n"
	doc := loadOneDocument(t, src)
	items := []any{map[string]any{"name": "parent", "inner": []any{
		map[string]any{"name": "shared", "16": "x"},
		map[string]any{"name": "shared", "16": "z"},
	}}}
	out := writeBack(t, doc, "patch", []string{"spec", "items"}, items)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - inner:\n            - \"16\": x\n              name: shared\n"+
		"            - \"16\": z\n              name: shared\n          name: parent\n")
	requireLoads(t, out)
}

// An alias key in one list item keeps the anchor that another item declares on its key. Before the
// anchor was kept, the item with `*k` was written and the item with `&k 0x10` was written without
// its anchor, so the document did not load (unknown anchor).
func TestWriteBack_KeepsTheAnchorOfAKeyInAListItem(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: a\n      &k 0x10: x\n    - name: b\n      *k : z\n  other: s\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"spec", "other"}, "z")
			requireExact(t, out, widgetOut+"spec:\n    items:\n        - &k 0x10: x\n          name: a\n        - *k: z\n          name: b\n    other: z\n")
			requireLoads(t, out)
		})
	}
}

// The fallback of a tagged-key collision writes the patched resource as it stands. An earlier key
// of the document (here a list with an alias key and a comment) is restored in the patched node
// before the collision is found, and the fallback must not carry that: it holds no alias key, no
// comment, and it loads.
func TestWriteBack_FallbackKeepsNothingOfTheDocument(t *testing.T) {
	const src = keyHead + "  annotations:\n    &k 0x10: x\nspec:\n  items:\n    # ci\n    - name: a\n      *k : b\ndata:\n  !!binary MHgxMA==: x\n  0x10: y\n"
	doc := loadOneDocument(t, src)
	applyStrategic(t, doc, map[string]any{"data": map[string]any{"MHgxMA==": "n"}})
	out := writtenBack(t, doc.Node)
	requireLoads(t, out)
	if strings.Contains(out, "# ci") || strings.Contains(out, "*k") || strings.Contains(out, "!!binary") {
		t.Errorf("the fallback carries the alias key, the comment or the tag of the document:\n%s", out)
	}
}

// A list item that pairs with another by position copies no comment, and neither does a list below
// it, even when the items of that list pair by their merge key: a merge key names the child and
// cannot show that the parent is the same item. Here the patch changes a field outside the list, so
// the two outer items pair by position.
func TestWriteBack_NestedKeyedListCopiesNoCommentUnderAPositionalParent(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - tag: one\n      inner:\n        # c1\n        - name: shared\n          v: a\n    - tag: two\n      inner:\n        # c2\n        - name: shared\n          v: b\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - inner:\n            - name: shared\n              v: a\n          tag: one\n"+
		"        - inner:\n            - name: shared\n              v: b\n          tag: two\n    other: z\n")
	requireLoads(t, out)
}

// A patch can move an item with an alias key before the item that declares its anchor. The merged
// text would then name an anchor that comes later and would not load. The write-back checks that the
// merged text loads, and where it does not, writes the patched resource as it stands (the comments
// of the document are lost), so the output loads.
func TestWriteBack_FallsBackWhenAnAliasKeyComesBeforeItsAnchor(t *testing.T) {
	const src = widgetHead + "spec:\n  # about items\n  items:\n    - name: a\n      &k 0x10: x\n    - name: b\n      *k : z\n"
	doc := loadOneDocument(t, src)
	swapped := []any{
		map[string]any{"name": "b", "16": "z"},
		map[string]any{"name": "a", "16": "x"},
	}
	op := PatchOp{Op: "replace", Path: "spec.items", Value: swapped}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	requireLoads(t, out)
	if strings.Contains(out, "# about items") || strings.Contains(out, "*k") {
		t.Errorf("the written text keeps the alias key or the comment, so it is not the fallback:\n%s", out)
	}
}
