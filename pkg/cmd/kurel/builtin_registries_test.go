package kurel

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestBuiltinRegistries_TypesUnchanged fails where kurel registers a type the
// golden file does not name, leaves one out, or registers it with a handler or
// rule of another Go type. testdata/builtin-registries.txt was written from the
// five lists kurel held before they moved to pkg/oam/builtin/registry, one line
// per entry: the registry, the type, the Go type of its value. A change to
// kurel's registered set changes that file in the same commit.
func TestBuiltinRegistries_TypesUnchanged(t *testing.T) {
	var got []string
	add := func(list string, entries map[string]string) {
		for typ, goType := range entries {
			got = append(got, fmt.Sprintf("%s %s %s", list, typ, goType))
		}
	}
	add("component", goTypes(builtinComponentHandlers()))
	add("component-lowering", goTypes(builtinComponentLoweringRules()))
	add("trait", goTypes(builtinTraitHandlers()))
	add("trait-lowering", goTypes(builtinTraitLoweringRules()))
	add("policy", goTypes(builtinPolicyHandlers()))
	sort.Strings(got)

	raw, err := os.ReadFile("testdata/builtin-registries.txt")
	if err != nil {
		t.Fatalf("read the golden file: %v", err)
	}
	want := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	sort.Strings(want)

	wantSet := make(map[string]bool, len(want))
	for _, line := range want {
		wantSet[line] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, line := range got {
		gotSet[line] = true
		if !wantSet[line] {
			t.Errorf("registered but not in the golden file: %s", line)
		}
	}
	for _, line := range want {
		if !gotSet[line] {
			t.Errorf("in the golden file but not registered: %s", line)
		}
	}
}

// goTypes maps each key of a registry to the Go type of its value, as %T
// prints it.
func goTypes[V any](entries map[string]V) map[string]string {
	types := make(map[string]string, len(entries))
	for key, value := range entries {
		types[key] = fmt.Sprintf("%T", value)
	}
	return types
}
