package components

import (
	"strings"
	"testing"
)

// Four parsers report a bad map entry by key and return on the first one they
// reach. Go randomises map iteration order, so before this was fixed an object
// authoring two bad entries at once named a different one on each run: the
// diagnostic was not reproducible, and a test asserting one would flake.
//
// These tests pin the RULE, not the repetition. Running the same input a
// thousand times and checking the answers agree would pass on a build where
// the map happened to iterate in insertion order, and would say nothing about
// which key is correct. Each case below instead names the exact key the parser
// must report — the lowest in sort order — so a regression that drops the sort
// fails roughly half the time on the first run rather than never.
//
// Every input deliberately lists its keys in an order where the WRONG answer
// is the one a naive implementation would reach first if the map preserved
// insertion order.

func TestStringMapStrictReportsLowestSortedBadKey(t *testing.T) {
	// "zone" is written first and "rack" must still be the reported key.
	_, err := stringMapStrict(map[string]any{
		"zone": 1,
		"rack": true,
	}, "affinity.nodeSelector")
	if err == nil {
		t.Fatal("expected an error for two non-string values, got nil")
	}
	if !strings.Contains(err.Error(), `affinity.nodeSelector["rack"]`) {
		t.Errorf("expected the lowest sorted bad key %q to be reported, got: %v", "rack", err)
	}
}

func TestParseResourceListReportsLowestSortedBadKey(t *testing.T) {
	// Both keys are invalid container resource names. "aaa" sorts first.
	_, err := parseResourceList(map[string]any{
		"zzz": "1",
		"aaa": "1",
	})
	if err == nil {
		t.Fatal("expected an error for two invalid resource names, got nil")
	}
	if !strings.HasPrefix(err.Error(), "aaa:") {
		t.Errorf("expected the lowest sorted bad key %q to be reported, got: %v", "aaa", err)
	}
}

func TestParseResourceListReportsLowestSortedBadKeyAcrossDistinctErrors(t *testing.T) {
	// Two DIFFERENT failure modes on two valid resource names: a bad quantity
	// type and a negative quantity. Which message an author sees must be
	// decided by key order, not by map order.
	_, err := parseResourceList(map[string]any{
		"memory": "-1Gi",
		"cpu":    true,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "cpu:") {
		t.Errorf("expected %q to be reported before %q, got: %v", "cpu", "memory", err)
	}
}

func TestRejectUnknownKeysReportsLowestSortedBadKey(t *testing.T) {
	err := rejectUnknownKeys(
		map[string]any{"zzz": 1, "aaa": 1},
		[]string{"known"},
		"probe",
	)
	if err == nil {
		t.Fatal("expected an error for two unknown keys, got nil")
	}
	if !strings.Contains(err.Error(), `"aaa"`) {
		t.Errorf("expected the lowest sorted unknown key %q to be reported, got: %v", "aaa", err)
	}
}

func TestParseManifestSourceReportsLowestSortedBadKey(t *testing.T) {
	// "chart" and "zzz" take different arms and produce different messages —
	// "not yet supported" versus "unknown property". "chart" sorts first, so
	// that is the one an author must be told about, every run.
	_, err := parseManifestSource(map[string]any{
		"zzz":   1,
		"chart": "foo",
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("expected the %q arm (lowest sorted key) to be reported, got: %v", "chart", err)
	}
}

// Control: the sort must not change which inputs are ACCEPTED, only which
// error a rejected input reports. A version of the fix that sorted the keys
// and then dropped or reordered the accepted entries would pass every
// assertion above.
func TestSortedIterationPreservesAcceptedEntries(t *testing.T) {
	got, err := stringMapStrict(map[string]any{
		"zone":  "a",
		"rack":  "b",
		"aisle": "c",
	}, "affinity.nodeSelector")
	if err != nil {
		t.Fatalf("expected all-string map to be accepted, got: %v", err)
	}
	want := map[string]string{"zone": "a", "rack": "b", "aisle": "c"}
	if len(got) != len(want) {
		t.Fatalf("expected %d entries, got %d: %v", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: expected %q, got %q", k, v, got[k])
		}
	}
}
