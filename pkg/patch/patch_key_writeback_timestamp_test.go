package patch

import "testing"

// The write-back tests of go-kure/launcher#1422 for a list item that holds a timestamp. The loader
// hands a timestamp to the resource as its text, so the written item holds a string where the
// authored item holds a time. The comparison of the two items must read both as that text. Each row
// replaces the whole list, which keeps the timestamp style pass away from it: the written item then
// holds the string, quoted. Each row checks the exact written text and that the text loads again.
func TestWriteBack_PairsAnItemThatHoldsATimestampInATouchedList(t *testing.T) {
	rows := []struct {
		name  string
		src   string
		items []any
		exact string
	}{
		{
			// Item 0 is unchanged and keeps its authored key. Item 1 is the one the patch changes.
			name:  "date_in_the_untouched_item",
			src:   widgetHead + "spec:\n  items:\n    - 0x10: x\n      at: 2026-11-07\n    - m: a\n",
			items: []any{map[string]any{"16": "x", "at": "2026-11-07"}, map[string]any{"m": "changed"}},
			exact: widgetOut + "spec:\n    items:\n        - 0x10: x\n          at: \"2026-11-07\"\n        - m: changed\n",
		},
		{
			name:  "date_and_time_in_the_untouched_item",
			src:   widgetHead + "spec:\n  items:\n    - 0x10: x\n      at: 2026-11-07T10:00:00Z\n    - m: a\n",
			items: []any{map[string]any{"16": "x", "at": "2026-11-07T10:00:00Z"}, map[string]any{"m": "changed"}},
			exact: widgetOut + "spec:\n    items:\n        - 0x10: x\n          at: \"2026-11-07T10:00:00Z\"\n        - m: changed\n",
		},
		{
			// The patch sets another date on item 0. The changed item no longer pairs with the
			// authored one, so it takes the converted key `"16"`. This row also holds on the base.
			name:  "date_the_patch_changes",
			src:   widgetHead + "spec:\n  items:\n    - 0x10: x\n      at: 2026-11-07\n    - m: a\n",
			items: []any{map[string]any{"16": "x", "at": "2026-11-08"}, map[string]any{"m": "a"}},
			exact: widgetOut + "spec:\n    items:\n        - \"16\": x\n          at: \"2026-11-08\"\n        - m: a\n",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := loadOneDocument(t, row.src)
			out := writeBack(t, doc, "patch", []string{"spec", "items"}, row.items)
			requireExact(t, out, row.exact)
			requireLoads(t, out)
		})
	}
}
