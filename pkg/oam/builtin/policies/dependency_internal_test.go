package policies

import (
	"strings"
	"testing"
)

func TestDetectCycles_NoCycle(t *testing.T) {
	deps := map[string][]string{
		"c": {"b"},
		"b": {"a"},
	}
	if err := detectCycles(deps); err != nil {
		t.Errorf("unexpected error for acyclic graph: %v", err)
	}
}

func TestDetectCycles_WithCycle(t *testing.T) {
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	err := detectCycles(deps)
	if err == nil {
		t.Fatal("expected error for cyclic graph")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Errorf("error = %q, want to contain 'circular dependency'", err.Error())
	}
}

// TestDetectCycles_DeterministicRotation pins why the DFS roots are sorted: the
// reported path must name the same rotation of a cycle on every run, whatever
// order the map yields its keys in. Repeated to give map iteration order the
// chance to vary.
func TestDetectCycles_DeterministicRotation(t *testing.T) {
	deps := map[string][]string{
		"c": {"a"},
		"b": {"c"},
		"a": {"b"},
	}
	const want = "circular dependency detected: a -> b -> c -> a"
	for i := range 50 {
		err := detectCycles(deps)
		if err == nil || err.Error() != want {
			t.Fatalf("run %d: error = %v, want %q", i, err, want)
		}
	}
}
