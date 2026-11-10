package patch

import (
	"strings"
	"testing"
)

// A write-back restores the authored key text of each key of a list item, unless another key of
// the item holds that text. A restore frees the text the key held, so a key that waited on it can
// restore next. These tests build items whose keys wait on each other. The marshaler writes the
// keys of an item in sorted order, so the authored order does not set the order of the restore.

// restoreItem returns a Widget with one list item that holds the given keys under the name first.
func restoreItem(keys string) string {
	return "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  items:\n    - name: first\n" + keys + "  other: x\n"
}

// A plain key and a tagged key can wait on each other. The tagged key `!!binary MHgxMA==` reads
// as `0x10`, and the plain key `0x10` reads as "16". Both must be written as authored, whichever
// of the two the marshaler writes first. Before the fix the guard read the spellings the marshaler
// wrote, so the plain key stayed converted ("16") in both orders.
func TestWriteBack_RestoreDoesNotDependOnKeyOrder(t *testing.T) {
	cases := []struct {
		name       string
		keys       string
		plain      string
		tag        string
		plainFirst bool
	}{
		// The tagged key reads "0x10", which sorts before "16": the tagged key is written first.
		{"tagged_key_first", "      0x10: a\n      !!binary MHgxMA==: b\n", "0x10: a\n", "!!binary MHgxMA==: b\n", false},
		// The plain key `1.0` reads "1", which sorts before the "1.0" the tagged key reads: the plain
		// key is written first.
		{"plain_key_first", "      1.0: a\n      !!binary MS4w: b\n", "1.0: a\n", "!!binary MS4w: b\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := loadOneDocument(t, restoreItem(tc.keys))
			out := writeBack(t, doc, "patch", []string{"spec", "other"}, "y")
			// The premise of the case: the marshaler wrote the keys in the order the case names. The
			// value of the plain key is a and the value of the tagged key is b, and a restore moves no
			// key, so the position of the two values shows the order whether or not a key was restored.
			a, b := strings.Index(out, ": a\n"), strings.Index(out, ": b\n")
			if a < 0 || b < 0 || (a < b) != tc.plainFirst {
				t.Fatalf("premise: want the plain key first = %v, the values are at %d (a) and %d (b):\n%s", tc.plainFirst, a, b, out)
			}
			if !strings.Contains(out, tc.plain) {
				t.Errorf("the plain key lost its authored text:\n%s", out)
			}
			if !strings.Contains(out, tc.tag) {
				t.Errorf("the tagged key lost its authored text:\n%s", out)
			}
			if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
				t.Errorf("the written text does not load again: %v\n%s", err, out)
			}
		})
	}
}

// Three keys wait in a chain. The plain key `0x10` waits on the tagged key `MHgxMA==`, which reads
// "0x10". That key waits on the tagged key `TUhneE1BPT0=`, which reads "MHgxMA==". Only the last
// one is free at the start, so the restore needs more than one pass. A check on the spellings the
// marshaler wrote restores only the last key, and so does a single pass over the live texts.
func TestWriteBack_RestoreChainFreesTextsOneAfterAnother(t *testing.T) {
	keys := "      0x10: a\n      !!binary MHgxMA==: b\n      !!binary TUhneE1BPT0=: c\n"
	doc := loadOneDocument(t, restoreItem(keys))
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "y")
	for _, want := range []string{"0x10: a\n", "!!binary MHgxMA==: b\n", "!!binary TUhneE1BPT0=: c\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the item lost the authored key %q:\n%s", strings.TrimSpace(want), out)
		}
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}

// A key the patch adds holds a text for good, and no restore frees it. The authored key `0x10`
// stays converted, and the added key keeps its text. This guard passes before and after the fix.
func TestWriteBack_RestoreLeavesATextTheItemHolds(t *testing.T) {
	doc := loadOneDocument(t, restoreItem("      0x10: b\n"))
	op := PatchOp{Op: "replace", Path: "spec.items", Value: []any{map[string]any{"name": "first", "16": "b", "0x10": "c"}}}
	if err := doc.ApplyPatchesToDocument([]PatchOp{op}); err != nil {
		t.Fatalf("ApplyPatchesToDocument: %v", err)
	}
	out := writtenBack(t, doc.Node)
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Fatalf("the written text does not load again: %v\n%s", err, out)
	}
	for _, want := range []string{": b\n", ": c\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("a value is missing from the written text (%q):\n%s", strings.TrimSpace(want), out)
		}
	}
}

// An alias key that is restored frees the text its scalar key held, so a key that waited on that
// text restores next. The alias `*k` reads `0x10`, and the plain key `0x10` reads "16". The
// marshaler writes "0x10" for the alias and "16" for the plain key. Restoring the alias leaves
// no scalar key with the text `0x10`, so the plain key takes it too.
func TestWriteBack_RestoredAliasFreesItsText(t *testing.T) {
	doc := loadOneDocument(t, "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n  annotations:\n    &k !!binary MHgxMA==: v\nspec:\n  items:\n    - name: first\n      0x10: a\n      *k : b\n  other: x\n")
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	for _, want := range []string{"0x10: a\n", "*k: b\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the item lost the authored key %q:\n%s", strings.TrimSpace(want), out)
		}
	}
	if _, err := LoadResourcesWithStructure(strings.NewReader(out)); err != nil {
		t.Errorf("the written text does not load again: %v\n%s", err, out)
	}
}
