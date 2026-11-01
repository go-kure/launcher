package oam

import (
	"maps"
	"math"
	"reflect"
	"testing"
)

// nanFunc is a Go-built struct property holding a NaN and a nil func: reflect.DeepEqual
// finds it different from itself through the NaN, and the nil func must not make a
// comparison that forgives the NaN fail.
type nanFunc struct {
	N float64
	F func()
}

// shapeSteps builds the snapshots of a profile rendering whose schema step changed
// nothing, the way evaluateProfile does.
func shapeSteps(profile map[string]any) defaultSteps {
	steps := defaultSteps{profile: indexOf(profile)}
	steps.schema = steps.profile
	return steps
}

// shapeFill runs rec.nestedFill the way fillComponentCapabilities does.
func shapeFill(rec *ownerRecorder, props, filled map[string]any, reads map[string]struct{}) {
	rec.nestedFill(indexOf(props), filled, reads)
}

// The report compares values across steps. Every shape a Go-built input can take in
// that comparison is one case here: a typed collection a handler normalizes (wholly or
// partly), a NaN, a struct with a nil func, and the same value in the other shape. An
// untouched value keeps the origin of the step that supplied it, whatever its shape
// became; a value a step changed does not.
func TestProvenance_ValueShapesKeepOrigin(t *testing.T) {
	t.Run("handler step", func(t *testing.T) {
		cases := []struct {
			name           string
			profile, final map[string]any
			want           map[string]string
		}{
			{
				"typed map normalized, one key changed",
				map[string]any{"ref": map[string]string{"name": "a", "kind": "Issuer"}},
				map[string]any{"ref": map[string]any{"name": "a", "kind": "ClusterIssuer"}},
				map[string]string{"ref.name": detailProfile, "ref.kind": detailHandlerDefault},
			},
			{
				"typed slice nested in a partly normalized map",
				map[string]any{"nested": map[string][]string{"zones": {"x"}, "other": {"a"}}},
				map[string]any{"nested": map[string]any{"zones": []string{"x"}, "other": []string{"a", "b"}}},
				map[string]string{"nested.zones": detailProfile, "nested.other": detailHandlerDefault},
			},
			{
				"nan scalar untouched",
				map[string]any{"threshold": math.NaN()},
				map[string]any{"threshold": math.NaN()},
				map[string]string{"threshold": detailProfile},
			},
			{
				"nan scalar replaced",
				map[string]any{"threshold": math.NaN()},
				map[string]any{"threshold": 1.5},
				map[string]string{"threshold": detailHandlerDefault},
			},
			{
				"nan in a typed slice, normalized",
				map[string]any{"ratios": []float64{1, math.NaN()}},
				map[string]any{"ratios": []any{1.0, math.NaN()}},
				map[string]string{"ratios[0]": detailProfile, "ratios[1]": detailProfile},
			},
			{
				"nan struct with a nil func untouched",
				map[string]any{"s": nanFunc{N: math.NaN()}},
				map[string]any{"s": nanFunc{N: math.NaN()}},
				map[string]string{"s": detailProfile},
			},
			{
				"untyped list returned typed",
				map[string]any{"tags": []any{"a", "b"}},
				map[string]any{"tags": []string{"a", "b"}},
				map[string]string{"tags": detailProfile},
			},
			{
				"untyped map returned typed",
				map[string]any{"ref": map[string]any{"name": "a"}},
				map[string]any{"ref": map[string]string{"name": "a"}},
				map[string]string{"ref": detailProfile},
			},
			{
				"map with a nan key untouched",
				map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				map[string]string{"m": detailProfile},
			},
			{
				"map with a nan key, value changed",
				map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				map[string]any{"m": map[float64]string{math.NaN(): "w"}},
				map[string]string{"m": detailHandlerDefault},
			},
			{
				"typed list returned with an element added",
				map[string]any{"tags": []string{"a"}},
				map[string]any{"tags": []any{"a", "b"}},
				map[string]string{"tags[0]": detailProfile, "tags[1]": detailHandlerDefault},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := shapeSteps(tc.profile).detail(tc.final); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("detail = %v, want %v", got, tc.want)
				}
			})
		}
	})

	t.Run("nested filler", func(t *testing.T) {
		authored := Source{Kind: SourceAuthored}
		filler := Source{Kind: SourceCapability, Ref: "k"}
		cases := []struct {
			name          string
			props, filled map[string]any
			// seed is the recorded source of each leaf of props; nil means all authored.
			seed map[string]Source
			want map[string]Source
		}{
			{
				name:   "authored typed map expanded by the filler",
				props:  map[string]any{"opts": map[string]string{"mode": "mine"}},
				filled: map[string]any{"opts": map[string]any{"mode": "mine", "class": "ssd"}},
				want:   map[string]Source{"opts.mode": authored, "opts.class": filler},
			},
			{
				name:   "authored typed map, value changed by the filler",
				props:  map[string]any{"opts": map[string]string{"mode": "mine"}},
				filled: map[string]any{"opts": map[string]any{"mode": "theirs"}},
				want:   map[string]Source{"opts.mode": filler},
			},
			{
				name:   "authored nan struct with a nil func, no-op filler",
				props:  map[string]any{"s": nanFunc{N: math.NaN()}},
				filled: map[string]any{"s": nanFunc{N: math.NaN()}},
				want:   map[string]Source{"s": authored},
			},
			{
				name:   "authored nan scalar, no-op filler",
				props:  map[string]any{"threshold": math.NaN()},
				filled: map[string]any{"threshold": math.NaN()},
				want:   map[string]Source{"threshold": authored},
			},
			{
				name:   "authored untyped list returned typed",
				props:  map[string]any{"tags": []any{"a", "b"}},
				filled: map[string]any{"tags": []string{"a", "b"}},
				want:   map[string]Source{"tags": authored},
			},
			{
				name:   "authored typed list inside a partly normalized map",
				props:  map[string]any{"nested": map[string][]string{"zones": {"x"}}},
				filled: map[string]any{"nested": map[string]any{"zones": []string{"x"}, "extra": "e"}},
				want:   map[string]Source{"nested.zones": authored, "nested.extra": filler},
			},
			{
				// The leaves came from one capability at two steps of its evaluation; the
				// filler returns the object in another shape without changing a value. The
				// capability stays the source, the step is no longer one answer.
				name:   "capability object of mixed detail returned typed",
				props:  map[string]any{"class": map[string]any{"a": "x", "b": "y"}},
				filled: map[string]any{"class": map[string]string{"a": "x", "b": "y"}},
				seed: map[string]Source{
					"class.a": {Kind: SourceCapability, Ref: "store", Detail: detailProfile},
					"class.b": {Kind: SourceCapability, Ref: "store", Detail: detailHandlerDefault},
				},
				want: map[string]Source{"class": {Kind: SourceCapability, Ref: "store"}},
			},
			{
				name:   "capability object of two refs returned typed is the filler's",
				props:  map[string]any{"class": map[string]any{"a": "x", "b": "y"}},
				filled: map[string]any{"class": map[string]string{"a": "x", "b": "y"}},
				seed: map[string]Source{
					"class.a": {Kind: SourceCapability, Ref: "store"},
					"class.b": {Kind: SourceAuthored},
				},
				want: map[string]Source{"class": filler},
			},
			{
				name:   "authored map with a nan key, no-op filler",
				props:  map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				filled: map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				want:   map[string]Source{"m": authored},
			},
			{
				name:   "authored map with a nan key, value changed by the filler",
				props:  map[string]any{"m": map[float64]string{math.NaN(): "v"}},
				filled: map[string]any{"m": map[float64]string{math.NaN(): "w"}},
				want:   map[string]Source{"m": filler},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := &ownerRecorder{entries: map[string]Source{}}
				if tc.seed != nil {
					maps.Copy(rec.entries, tc.seed)
				} else {
					rec.authored(tc.props)
				}
				shapeFill(rec, tc.props, tc.filled, map[string]struct{}{"k": {}})
				if !reflect.DeepEqual(rec.entries, tc.want) {
					t.Fatalf("sources = %v, want %v", rec.entries, tc.want)
				}
			})
		}
	})
}
