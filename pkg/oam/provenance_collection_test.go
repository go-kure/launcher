package oam

import (
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

// A map or slice that contains itself is one leaf where the walk meets it again; the
// walk, the leaf snapshot and the origin comparison all end.
func TestProvenance_SelfReferentialValuesDoNotLoop(t *testing.T) {
	cyclicMap := map[string]any{"ok": "v"}
	cyclicMap["self"] = cyclicMap

	cyclicList := make([]any, 2)
	cyclicList[0] = "first"
	cyclicList[1] = cyclicList

	shared := map[string]any{"k": "v"}

	cases := []struct {
		name      string
		value     any
		wantPaths []string
	}{
		{"map", map[string]any{"m": cyclicMap}, []string{"m.ok", "m.self"}},
		{"slice", map[string]any{"l": cyclicList}, []string{"l[0]", "l[1]"}},
		{"shared not cyclic", map[string]any{"a": shared, "b": shared}, []string{"a.k", "b.k"}},
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
