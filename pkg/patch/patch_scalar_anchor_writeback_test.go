package patch

import (
	"strings"
	"testing"
)

// The write-back tests of go-kure/launcher#1408. A scalar value that the write-back rewrites must
// keep the anchor it carries in the document. An alias key (`*k`) that reads the anchor then still
// finds it, the merged text loads, and the comments of the document stay.

// anchorHead is keyHead as the write-back writes it, without the annotations.
const anchorHead = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n    name: keys\n"

// A rewritten scalar value keeps its anchor. The rows pin the exact merged text, because the load
// check in the write-back would otherwise hide a merge that fell back to the patched resource and
// lost the comment above data. The anchored value is written as `16` and not as `0x10`: the
// write-back does not restore the authored spelling of a scalar, and this change leaves that alone.
func TestWriteBack_KeepsTheAnchorOfARewrittenScalarValue(t *testing.T) {
	rows := []struct {
		name  string
		src   string
		exact string
	}{
		{
			name:  "annotation",
			src:   keyHead + "  annotations:\n    source: &k 0x10\n# about data\ndata:\n  *k : a\n  other: s\n",
			exact: anchorHead + "    annotations:\n        source: &k 16\n# about data\ndata:\n    *k: a\n    other: z\n",
		},
		{
			name:  "root",
			src:   keyHead + "source: &k 0x10\n# about data\ndata:\n  *k : a\n  other: s\n",
			exact: anchorHead + "source: &k 16\n# about data\ndata:\n    *k: a\n    other: z\n",
		},
	}
	for _, tt := range rows {
		for _, route := range []string{"patch", "strategic"} {
			t.Run(tt.name+"_"+route, func(t *testing.T) {
				doc := loadOneDocument(t, tt.src)
				out := writeBack(t, doc, route, []string{"data", "other"}, "z")
				requireExact(t, out, tt.exact)
				again := requireLoads(t, out)
				requireString(t, again.Documents[0].Resource.Object, "a", "data", "16")
			})
		}
	}
}

// A value that changes keeps no anchor. An alias key that reads it would otherwise read the new
// value, and the document would mean something else. The write-back finds the dangling alias key
// when it loads the merged text again, and it writes the patched resource: the text loads, the
// data is right, and the comments are gone. The row pins that fallback.
func TestWriteBack_ChangedValueUnderAnAnchorFallsBack(t *testing.T) {
	const src = keyHead + "  annotations:\n    source: &k 0x10\n# about data\ndata:\n  *k : a\n  other: s\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"metadata", "annotations", "source"}, "changed")
			again := requireLoads(t, out)
			obj := again.Documents[0].Resource.Object
			requireString(t, obj, "changed", "metadata", "annotations", "source")
			requireString(t, obj, "a", "data", "16")
			if strings.Contains(out, "# about data") || strings.Contains(out, "&k") {
				t.Errorf("the written text keeps the comment or the anchor, so it is not the fallback:\n%s", out)
			}
		})
	}
}

// A value that is an alias (`*v`) never takes the anchor of the scalar it points at. The anchor
// would then be declared twice, and a later alias would read the nearer one.
func TestWriteBack_KeepsNoAnchorOfAnAliasValue(t *testing.T) {
	const src = keyHead + "  annotations:\n    a: &v x\n    b: *v\n# about data\ndata:\n  other: s\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"data", "other"}, "z")
			requireLoads(t, out)
			if got := strings.Count(out, "&v"); got != 1 {
				t.Errorf("the written text declares the anchor &v %d times, want 1:\n%s", got, out)
			}
			if !strings.Contains(out, "# about data") {
				t.Errorf("the comment above data is gone, so the write-back fell back:\n%s", out)
			}
		})
	}
}

// An anchor on a scalar inside a list is a known limit. The list paths replace an item whole and
// the item loses its anchor, so an alias key that reads it finds nothing and the write-back falls
// back to the patched resource. The row pins that fallback, so a later change shows in this test.
func TestWriteBack_DropsNoAnchorOfAnUntouchedScalarInAList(t *testing.T) {
	const src = "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\nspec:\n  # about items\n  items:\n    - &k 0x10\n  data:\n    *k : a\n  other: s\n"
	doc := loadOneDocument(t, src)
	out := writeBack(t, doc, "patch", []string{"spec", "other"}, "z")
	again := requireLoads(t, out)
	obj := again.Documents[0].Resource.Object
	requireString(t, obj, "a", "spec", "data", "16")
	requireString(t, obj, "z", "spec", "other")
	if strings.Contains(out, "# about items") {
		t.Errorf("the written text keeps the comment, so it is not the fallback:\n%s", out)
	}
}

// An anchored timestamp that a patch sets to the string of the same text keeps its anchor. The
// document loads a plain timestamp as its text, and the write-back compares a timestamp as its
// text too, so the two read as one value, the anchor is kept and the alias key still finds it
// (go-kure/launcher#1422).
func TestWriteBack_KeepsTheAnchorOfAnAnchoredTimestamp(t *testing.T) {
	const src = keyHead + "  annotations:\n    source: &k 2026-11-07\n# about data\ndata:\n  *k : a\n  other: s\n"
	for _, route := range []string{"patch", "strategic"} {
		t.Run(route, func(t *testing.T) {
			doc := loadOneDocument(t, src)
			out := writeBack(t, doc, route, []string{"metadata", "annotations", "source"}, "2026-11-07")
			if !strings.Contains(out, "# about data") {
				t.Fatalf("the write-back lost the comment, so it fell back to the patched resource:\n%s", out)
			}
			again := requireLoads(t, out)
			requireString(t, again.Documents[0].Resource.Object, "a", "data", "2026-11-07")
		})
	}
}
