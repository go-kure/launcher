package oam

import (
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
		cases := []struct {
			name            string
			props, filled   map[string]any
			wantSourceKinds map[string]SourceKind
		}{
			{
				"authored typed map expanded by the filler",
				map[string]any{"opts": map[string]string{"mode": "mine"}},
				map[string]any{"opts": map[string]any{"mode": "mine", "class": "ssd"}},
				map[string]SourceKind{"opts.mode": SourceAuthored, "opts.class": SourceCapability},
			},
			{
				"authored typed map, value changed by the filler",
				map[string]any{"opts": map[string]string{"mode": "mine"}},
				map[string]any{"opts": map[string]any{"mode": "theirs"}},
				map[string]SourceKind{"opts.mode": SourceCapability},
			},
			{
				"authored nan struct with a nil func, no-op filler",
				map[string]any{"s": nanFunc{N: math.NaN()}},
				map[string]any{"s": nanFunc{N: math.NaN()}},
				map[string]SourceKind{"s": SourceAuthored},
			},
			{
				"authored nan scalar, no-op filler",
				map[string]any{"threshold": math.NaN()},
				map[string]any{"threshold": math.NaN()},
				map[string]SourceKind{"threshold": SourceAuthored},
			},
			{
				"authored untyped list returned typed",
				map[string]any{"tags": []any{"a", "b"}},
				map[string]any{"tags": []string{"a", "b"}},
				map[string]SourceKind{"tags": SourceAuthored},
			},
			{
				"authored typed list inside a partly normalized map",
				map[string]any{"nested": map[string][]string{"zones": {"x"}}},
				map[string]any{"nested": map[string]any{"zones": []string{"x"}, "extra": "e"}},
				map[string]SourceKind{"nested.zones": SourceAuthored, "nested.extra": SourceCapability},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := &ownerRecorder{entries: map[string]Source{}}
				rec.authored(tc.props)
				shapeFill(rec, tc.props, tc.filled, map[string]struct{}{"k": {}})
				got := map[string]SourceKind{}
				for p, s := range rec.entries {
					got[p] = s.Kind
				}
				if !reflect.DeepEqual(got, tc.wantSourceKinds) {
					t.Fatalf("sources = %v, want %v", got, tc.wantSourceKinds)
				}
			})
		}
	})
}
