package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// assertSchemaKeysAt pins the Properties key set of the schema reached by walking
// `path` (dot-separated; the step "[]" descends into Items) to want — the same
// slice the parser hands rejectUnknownKeys at that level. Without this, a
// nested key published but never parsed, or parsed but never published, leaves
// both halves internally consistent and every other test green.
func assertSchemaKeysAt(t *testing.T, root oam.PropertySchema, path string, want []string) {
	t.Helper()
	cur := root
	for _, step := range strings.Split(path, ".") {
		if step == "[]" {
			if cur.Items == nil {
				t.Fatalf("%s: no Items to descend into", path)
			}
			cur = *cur.Items
			continue
		}
		next, ok := cur.Properties[step]
		if !ok {
			t.Fatalf("%s: schema has no property %q", path, step)
		}
		cur = next
	}
	got := make([]string, 0, len(cur.Properties))
	for k := range cur.Properties {
		got = append(got, k)
	}
	slices.Sort(got)
	w := slices.Clone(want)
	slices.Sort(w)
	if !slices.Equal(got, w) {
		t.Errorf("%s: schema keys = %v, parser accepts %v", path, got, w)
	}
}
