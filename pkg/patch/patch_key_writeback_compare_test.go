package patch

import (
	"runtime/debug"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The write-back tests of go-kure/launcher#1400 for the comparison of two list items and for the
// pairing of items that share a merge-key value. Each row checks the exact written text and that
// the text loads again.

// A merge entry (`<<`) holds a source the loader does not read when the mapping holds the merged
// entry itself. Here `unused` is held, so the loader skips the source, and its value can hold a
// cycle. The comparison of the two lists must not read it. Before the fix itemData followed the
// cycle until the stack overflowed, which no recover can stop, so the stack limit is lowered here
// to make a red run quick, and a timer stops a run that hangs instead.
func TestWriteBack_ReadsNoValueOfAMergeEntry(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - <<: {unused: &loop [*loop]}\n      unused: kept\n      0x10: x\n  other: s\n"
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	doc := loadOneDocument(t, src)
	errc := make(chan error, 1)
	go func() {
		errc <- doc.ApplyPatchesToDocument([]PatchOp{{Op: "replace", Path: "spec.other", Value: "z"}})
	}()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("ApplyPatchesToDocument: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the write-back did not return in 30 s: it reads the value of a merge entry")
	}
	out := writtenBack(t, doc.Node)
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          unused: kept\n    other: z\n")
	requireLoads(t, out)
}

// itemData reads no node twice on one path. The loader refuses every cycle it reads (an anchor
// that holds itself), and the merge entry above is skipped, so no loaded document reaches this
// guard. A node built by hand does: a list that holds an alias of itself, and a mapping that holds
// an alias of itself as a value. Each reads as data that equals nothing a written item holds, and
// the read returns.
func TestItemData_ReadsACycleAsUnreadData(t *testing.T) {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	seq.Content = []*yaml.Node{{Kind: yaml.AliasNode, Alias: seq}}
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	mapping.Content = []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "k"},
		{Kind: yaml.AliasNode, Alias: mapping},
	}
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	for name, node := range map[string]*yaml.Node{"list": seq, "mapping": mapping} {
		t.Run(name, func(t *testing.T) {
			done := make(chan any, 1)
			go func() { done <- itemData(node) }()
			select {
			case got := <-done:
				if sameItemData(node, &yaml.Node{Kind: yaml.MappingNode}) {
					t.Errorf("a cycle equals an empty mapping: %v", got)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("itemData did not return in 30 s: it follows a cycle")
			}
		})
	}
}

// A list item that the patch did not change keeps its authored key text, whatever numbers it
// holds. A NaN never equals a NaN under reflect.DeepEqual, and the float 1.0 is written as the int
// 1, so before the fix both items read as changed and their key `0x10` was written as "16".
func TestWriteBack_KeepsTheAuthoredKeyOfAnItemWithANumber(t *testing.T) {
	rows := []struct {
		name   string
		src    string
		exact  string
		routes []string
	}{
		{
			name:  "nan",
			src:   widgetHead + "spec:\n  items:\n    - 0x10: x\n      value: .nan\n  other: s\n",
			exact: widgetOut + "spec:\n    items:\n        - 0x10: x\n          value: .nan\n    other: z\n",
			// The strategic route marshals the resource to JSON, and JSON has no NaN,
			// so it fails before any write-back (go-kure/launcher#1414).
			routes: []string{"patch"},
		},
		{
			name:   "whole_float",
			src:    widgetHead + "spec:\n  items:\n    - 0x10: x\n      value: 1.0\n  other: s\n",
			exact:  widgetOut + "spec:\n    items:\n        - 0x10: x\n          value: 1\n    other: z\n",
			routes: []string{"patch", "strategic"},
		},
	}
	for _, row := range rows {
		for _, route := range row.routes {
			t.Run(row.name+"_"+route, func(t *testing.T) {
				doc := loadOneDocument(t, row.src)
				out := writeBack(t, doc, route, []string{"spec", "other"}, "z")
				requireExact(t, out, row.exact)
				requireLoads(t, out)
			})
		}
	}
}

// Two authored items of a list that share one merge-key value cannot be told apart, so neither
// pairs with the item the resource holds: the first takes no key text and no comment of the second.
// This is the same rule as for a nested list, here for a list that sits directly under the field.
// Before the fix the merge paired both written items with the last authored item.
func TestWriteBack_DoesNotPairDirectItemsThatShareAMergeKeyValue(t *testing.T) {
	const src = widgetHead + "spec:\n  items:\n    - name: shared\n      0x10: x\n    - name: shared\n      # ck\n      16: z\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	requireExact(t, out, widgetOut+"spec:\n    items:\n        - \"16\": x\n          name: shared\n        - \"16\": z\n          name: shared\n    other: z\n")
	requireLoads(t, out)
}
