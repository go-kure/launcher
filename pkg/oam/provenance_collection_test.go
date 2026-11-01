package oam

import (
	"math"
	"reflect"
	"testing"
)

// A profile that gives a typed collection, normalized by a handler to map[string]any
// or []any, keeps the profile as the origin of every element the handler left alone;
// only an element it added or changed is the handler's.
func TestProvenance_TypedCollectionElementsKeepOrigin(t *testing.T) {
	profile := map[string]any{
		"issuerRef": map[string]string{"name": "myissuer", "kind": "Issuer"},
		"tags":      []string{"a", "b"},
		"nested":    map[string][]string{"zones": {"x"}},
	}
	steps := defaultSteps{profile: leafValues(profile)}
	steps.schema = steps.profile

	final := map[string]any{
		"issuerRef": map[string]any{"name": "myissuer", "kind": "ClusterIssuer", "group": "g"},
		"tags":      []any{"a", "b", "c"},
		"nested":    map[string]any{"zones": []any{"x"}},
	}
	got := steps.detail(final)
	want := map[string]string{
		"issuerRef.name":  detailProfile,
		"issuerRef.kind":  detailHandlerDefault,
		"issuerRef.group": detailHandlerDefault,
		"tags[0]":         detailProfile,
		"tags[1]":         detailProfile,
		"tags[2]":         detailHandlerDefault,
		"nested.zones[0]": detailProfile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detail = %v, want %v", got, want)
	}

	// Negative control: with no snapshot of the typed value, the same final leaves are
	// all the handler's, so the test above fails if the elements are not consulted.
	bare := defaultSteps{profile: map[string]any{}, schema: map[string]any{}}
	for path, d := range bare.detail(final) {
		if d != detailHandlerDefault {
			t.Fatalf("detail[%s] = %q without a snapshot, want %q", path, d, detailHandlerDefault)
		}
	}
}

// A handler may return the outer typed collection as a map[string]any that still holds
// the inner one typed. The final leaf is then the inner collection itself, and it keeps
// the profile's origin when unchanged and is the handler's when it differs.
func TestProvenance_PartiallyNormalizedCollectionKeepsOrigin(t *testing.T) {
	profile := map[string]any{
		"nested": map[string][]string{"zones": {"x"}, "other": {"a"}},
	}
	steps := defaultSteps{profile: leafValues(profile)}
	steps.schema = steps.profile

	final := map[string]any{
		"nested": map[string]any{
			"zones": []string{"x"},
			"other": []string{"a", "b"},
		},
	}
	got := steps.detail(final)
	want := map[string]string{
		"nested.zones": detailProfile,
		"nested.other": detailHandlerDefault,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detail = %v, want %v", got, want)
	}
}

// A NaN is not equal to itself under reflect.DeepEqual, but a leaf nobody touched is
// still the same value: it keeps the origin of the step that supplied it, also when it
// sits in a typed collection, and a different value is still the handler's.
func TestProvenance_UnchangedNaNKeepsOrigin(t *testing.T) {
	profile := map[string]any{
		"threshold": math.NaN(),
		"ratios":    []float64{1, math.NaN()},
		"nested":    map[string][]float64{"w": {math.NaN()}},
		"plain":     "v",
	}
	steps := defaultSteps{profile: leafValues(profile)}
	steps.schema = steps.profile

	got := steps.detail(profile)
	for path, d := range got {
		if d != detailProfile {
			t.Errorf("detail[%s] = %q, want %q", path, d, detailProfile)
		}
	}
	if len(got) == 0 {
		t.Fatal("detail has no entries")
	}

	changed := map[string]any{
		"threshold": 1.5,
		"ratios":    []float64{1, 2},
		"nested":    map[string][]float64{"w": {math.NaN(), 3}},
		"plain":     "v",
	}
	got = steps.detail(changed)
	for _, path := range []string{"threshold", "ratios", "nested"} {
		if got[path] != detailHandlerDefault {
			t.Errorf("detail[%s] = %q for a changed value, want %q", path, got[path], detailHandlerDefault)
		}
	}
}

// The comparison used for leaves ends on a self-referential value and treats NaN as
// equal to NaN only where DeepEqual would otherwise find the values equal.
func TestProvenance_SameLeafValue(t *testing.T) {
	cyc := make([]any, 2)
	cyc[0] = math.NaN()
	cyc[1] = cyc
	other := make([]any, 2)
	other[0] = math.NaN()
	other[1] = other

	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"nan scalars", math.NaN(), math.NaN(), true},
		{"nan float32", float32(math.NaN()), float32(math.NaN()), true},
		{"nan versus number", math.NaN(), 1.0, false},
		{"nan versus other type", math.NaN(), float32(math.NaN()), false},
		{"slice with nan", []float64{math.NaN(), 1}, []float64{math.NaN(), 1}, true},
		{"slice length differs", []float64{math.NaN()}, []float64{math.NaN(), 1}, false},
		{"map with nan", map[string]float64{"a": math.NaN()}, map[string]float64{"a": math.NaN()}, true},
		{"map key differs", map[string]float64{"a": math.NaN()}, map[string]float64{"b": math.NaN()}, false},
		{"any holding nan", []any{math.NaN()}, []any{math.NaN()}, true},
		{"cyclic with nan", cyc, other, true},
		{"cyclic differs", cyc, []any{math.NaN(), 1}, false},
		{"plain equal", "x", "x", true},
		{"plain differs", "x", "y", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameLeafValue(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameLeafValue = %v, want %v", got, tc.want)
			}
		})
	}
}

// The leaf snapshot copies a sub-slice of an acyclic slice instead of sharing it: a
// later change to the original must not show through the copy.
func TestProvenance_SnapshotCopiesOverlappingSubSlice(t *testing.T) {
	s := make([]any, 2)
	s[0] = "v"
	s[1] = s[:1]

	c, ok := snapshotLeaf(s).([]any)
	if !ok || len(c) != 2 {
		t.Fatalf("snapshotLeaf = %#v, want a two-element []any", snapshotLeaf(s))
	}
	s[0] = "changed"
	sub, ok := c[1].([]any)
	if !ok || len(sub) != 1 || sub[0] != "v" || c[0] != "v" {
		t.Fatalf("snapshot after a change to the original = %#v, want [v [v]]", c)
	}
}

// A map or slice that contains itself is one leaf where the walk meets it again; the
// walk, the leaf snapshot and the origin comparison all end.
func TestProvenance_SelfReferentialValuesDoNotLoop(t *testing.T) {
	cyclicMap := map[string]any{"ok": "v"}
	cyclicMap["self"] = cyclicMap

	cyclicList := make([]any, 2)
	cyclicList[0] = "first"
	cyclicList[1] = cyclicList

	shared := map[string]any{"k": "v"}

	// Acyclic: the second element is a shorter slice of the same backing array, so both
	// have one address but are different values, and every leaf is reported.
	overlap := make([]any, 2)
	overlap[0] = "v"
	overlap[1] = overlap[:1]

	// Cyclic: the first element is a one-element slice of the array that holds it, so
	// the walk meets the same (address, length) again and stops there.
	selfSub := make([]any, 2)
	selfSub[0] = selfSub[:1]
	selfSub[1] = "w"

	cases := []struct {
		name      string
		value     any
		wantPaths []string
	}{
		{"map", map[string]any{"m": cyclicMap}, []string{"m.ok", "m.self"}},
		{"slice", map[string]any{"l": cyclicList}, []string{"l[0]", "l[1]"}},
		{"shared not cyclic", map[string]any{"a": shared, "b": shared}, []string{"a.k", "b.k"}},
		{"overlapping sub-slice is not a cycle", map[string]any{"l": overlap}, []string{"l[0]", "l[1][0]"}},
		{"self-containing sub-slice is a cycle", map[string]any{"l": selfSub}, []string{"l[0][0]", "l[1]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := tc.value.(map[string]any)
			var seen []string
			eachLeaf("", props, func(path string, _ any) { seen = append(seen, path) })
			if !reflect.DeepEqual(seen, tc.wantPaths) {
				t.Fatalf("eachLeaf paths = %v, want %v", seen, tc.wantPaths)
			}

			steps := defaultSteps{profile: leafValues(props)}
			steps.schema = steps.profile
			got := steps.detail(props)
			if len(got) != len(tc.wantPaths) {
				t.Fatalf("detail = %v, want %d entries", got, len(tc.wantPaths))
			}
			for _, p := range tc.wantPaths {
				if got[p] != detailProfile {
					t.Fatalf("detail[%s] = %q, want %q", p, got[p], detailProfile)
				}
			}
		})
	}
}
