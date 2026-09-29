package builtin_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"

	"github.com/go-kure/launcher/pkg/oam/builtin"
)

// TestDecodeStrictJSON_FollowsJSONTags is the reason the helper exists: Flux API
// types carry json tags only, so a camelCase key must land on its field. The yaml
// decoder keys on the lowercased Go field name and refuses the same document.
func TestDecodeStrictJSON_FollowsJSONTags(t *testing.T) {
	src := map[string]any{
		"releaseName": "podinfo",
		"interval":    "5m",
		"chartRef":    map[string]any{"kind": "OCIRepository", "name": "podinfo"},
		"kubeConfig":  map[string]any{"secretRef": map[string]any{"name": "remote"}},
	}

	spec, owned, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](src)
	if err != nil {
		t.Fatalf("DecodeStrictJSON: %v", err)
	}
	if len(owned) != 0 {
		t.Errorf("owned = %v, want empty with no owned keys named", owned)
	}
	if spec.ReleaseName != "podinfo" {
		t.Errorf("ReleaseName = %q, want podinfo", spec.ReleaseName)
	}
	if spec.Interval.Duration != 5*time.Minute {
		t.Errorf("Interval = %v, want 5m", spec.Interval.Duration)
	}
	if spec.ChartRef == nil || spec.ChartRef.Kind != "OCIRepository" || spec.ChartRef.Name != "podinfo" {
		t.Errorf("ChartRef = %+v, want OCIRepository/podinfo", spec.ChartRef)
	}
	if spec.KubeConfig == nil || spec.KubeConfig.SecretRef == nil || spec.KubeConfig.SecretRef.Name != "remote" {
		t.Errorf("KubeConfig = %+v, want secretRef remote", spec.KubeConfig)
	}

	if _, err := builtin.DecodeStrict[helmv2.HelmReleaseSpec](src); err == nil {
		t.Error("yaml DecodeStrict accepted camelCase json-tag keys; the contrast this test documents no longer holds")
	}
}

func TestDecodeStrictJSON_Refuses(t *testing.T) {
	cases := []struct {
		name string
		src  map[string]any
		want string
	}{
		{"unknown top-level key", map[string]any{"releaseName": "r", "relaseName": "typo"}, `unknown field "relaseName"`},
		{"unknown nested key", map[string]any{"chartRef": map[string]any{"kind": "OCIRepository", "name": "n", "chartName": "x"}}, `unknown field "chartName"`},
		{"wrong type", map[string]any{"releaseName": 5}, "releaseName"},
		{"unmarshalable value", map[string]any{"maxHistory": math.NaN()}, "marshal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](tc.src)
			if err == nil {
				t.Fatalf("DecodeStrictJSON(%v) = nil error, want one containing %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// TestDecodeStrictJSON_SplitsOwnedKeys: a launcher-owned key is handed back to the
// caller instead of reaching the strict decoder (where it would be an unknown
// field), and the caller's map is left untouched.
func TestDecodeStrictJSON_SplitsOwnedKeys(t *testing.T) {
	src := map[string]any{
		"releaseName": "r",
		"valuesMode":  "inline",
		"delivery":    map[string]any{"mode": "native"},
	}

	spec, owned, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](src, "valuesMode", "delivery", "source")
	if err != nil {
		t.Fatalf("DecodeStrictJSON: %v", err)
	}
	if spec.ReleaseName != "r" {
		t.Errorf("ReleaseName = %q, want r", spec.ReleaseName)
	}
	wantOwned := map[string]any{"valuesMode": "inline", "delivery": map[string]any{"mode": "native"}}
	if !reflect.DeepEqual(owned, wantOwned) {
		t.Errorf("owned = %v, want %v (an owned key absent from src must not appear)", owned, wantOwned)
	}
	if len(src) != 3 {
		t.Errorf("src was mutated: %v", src)
	}

	if _, _, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](src); err == nil {
		t.Error("without owned keys named, valuesMode must be an unknown field")
	}

	// encoding/json folds case, so a case variant of an owned key must be split off too.
	if spec, owned, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](map[string]any{"Values": "leaks"}, "values"); err != nil || spec.Values != nil || !reflect.DeepEqual(owned, map[string]any{"Values": "leaks"}) {
		t.Errorf("case variant of an owned key: owned = %v, err = %v; want it split off, not decoded", owned, err)
	}
}

// TestDecodeStrictJSON_KeepsExactNumbers: an interface-typed field keeps a large int exact.
func TestDecodeStrictJSON_KeepsExactNumbers(t *testing.T) {
	spec, _, err := builtin.DecodeStrictJSON[struct{ Extra map[string]any }](map[string]any{"extra": map[string]any{"n": int64(9007199254740993)}})
	if err != nil || fmt.Sprint(spec.Extra["n"]) != "9007199254740993" {
		t.Errorf("DecodeStrictJSON = %v, %v; want extra.n 9007199254740993", spec, err)
	}
}

func TestDecodeStrictJSON_NilSource(t *testing.T) {
	spec, owned, err := builtin.DecodeStrictJSON[helmv2.HelmReleaseSpec](nil, "valuesMode")
	if err != nil {
		t.Fatalf("DecodeStrictJSON(nil): %v", err)
	}
	if spec == nil || !reflect.DeepEqual(*spec, helmv2.HelmReleaseSpec{}) {
		t.Errorf("spec = %+v, want the zero value", spec)
	}
	if len(owned) != 0 {
		t.Errorf("owned = %v, want empty", owned)
	}
}

type reachInner struct {
	Promoted string `json:"promoted"`
}

type reachTarget struct {
	reachInner
	Named     string `json:"named"`
	Untagged  string
	Skipped   string `json:"-"`
	Collides  string `json:"valuesMode,omitempty"`
	unexposed string //nolint:unused // exercised through reflection only
}

func TestUnreachableJSONFields(t *testing.T) {
	got := builtin.UnreachableJSONFields(reflect.TypeFor[reachTarget](), "valuesmode", "promoted")
	want := []string{"Skipped", "promoted", "valuesMode"}
	if !slices.Equal(got, want) {
		t.Errorf("UnreachableJSONFields = %v, want %v", got, want)
	}

	if got := builtin.UnreachableJSONFields(reflect.TypeFor[*reachTarget]()); !slices.Equal(got, []string{"Skipped"}) {
		t.Errorf("pointer type: got %v, want [Skipped]", got)
	}
}

// RecursiveEmbed embeds a pointer to itself, the one shape whose embedded fields
// never run out; encoding/json stops at the repeat, and so must the check. It is
// exported so the embedded field is exported too, as it would be in a spec type.
type RecursiveEmbed struct {
	*RecursiveEmbed
	Name string `json:"name"`
}

// TestUnreachableJSONFields_RecursiveEmbedding: a type that embeds itself is
// walked once, instead of recursing until the stack overflows.
func TestUnreachableJSONFields_RecursiveEmbedding(t *testing.T) {
	if got := builtin.UnreachableJSONFields(reflect.TypeFor[RecursiveEmbed](), "name"); !slices.Equal(got, []string{"name"}) {
		t.Errorf("UnreachableJSONFields = %v, want [name]", got)
	}
}

// encoding/json drops reachDiamond's value as ambiguous and accepts reachTagged's embed.
type (
	reachLeaf struct {
		Value string `json:"value"`
	}
	reachLeft    struct{ reachLeaf }
	reachRight   struct{ reachLeaf }
	reachDiamond struct {
		reachLeft
		reachRight
	}
	reachTagged struct {
		reachLeaf `json:"values"`
	}
	// The decoder cannot set a field behind an unexported embedded pointer, and panics
	// on a tagged one. The last two carry invalid tag names.
	reachHiddenPtr struct{ *reachLeaf }
	reachTaggedPtr struct {
		*reachLeaf `json:"nested"`
	}
	reachBadTag struct {
		Value string `json:"bad\\key"` //nolint:staticcheck // SA5008: the invalid name is the fixture
	}
	reachBadEmbed struct {
		reachLeaf `json:"bad\\emb"` //nolint:staticcheck // SA5008: as above
	}
)

// TestUnreachableJSONFields_FollowsDecoder: the check agrees with what the decoder accepts.
func TestUnreachableJSONFields_FollowsDecoder(t *testing.T) {
	for _, tc := range []struct {
		typ         reflect.Type
		owned, want []string
	}{
		{reflect.TypeFor[reachDiamond](), nil, []string{"value"}},
		{reflect.TypeFor[reachTagged](), []string{"values"}, []string{"values"}},
		{reflect.TypeFor[reachTagged](), nil, nil},
		{reflect.TypeFor[reachHiddenPtr](), nil, []string{"value"}},
		{reflect.TypeFor[reachTaggedPtr](), nil, []string{"nested"}},
	} {
		if got := builtin.UnreachableJSONFields(tc.typ, tc.owned...); !slices.Equal(got, tc.want) {
			t.Errorf("UnreachableJSONFields(%v, %v) = %v, want %v", tc.typ, tc.owned, got, tc.want)
		}
	}
	// v1 encoding/json reaches these by Go name; the jsonv2-backed one refuses the field.
	agreesWithDecoder[reachBadTag](t, "Value")
	agreesWithDecoder[reachBadEmbed](t, "value")
	if _, _, err := builtin.DecodeStrictJSON[reachDiamond](map[string]any{"value": "x"}); err == nil {
		t.Error("the ambiguous key decoded; the diamond fixture no longer shows the gap")
	}
}

// agreesWithDecoder: T's only key is reported exactly when DecodeStrictJSON refuses it.
func agreesWithDecoder[T any](t *testing.T, key string) {
	t.Helper()
	var want []string
	if _, _, err := builtin.DecodeStrictJSON[T](map[string]any{key: "x"}); err != nil {
		want = []string{key}
	}
	if got := builtin.UnreachableJSONFields(reflect.TypeFor[T]()); !slices.Equal(got, want) {
		t.Errorf("UnreachableJSONFields(%T) = %v, want %v", *new(T), got, want)
	}
}

// TestUnreachableJSONFields_HelmReleaseSpec shows the intended use: a terminal that
// owns keys asserts none of them shadows a spec field, against an exclusion list
// that should stay empty.
func TestUnreachableJSONFields_HelmReleaseSpec(t *testing.T) {
	got := builtin.UnreachableJSONFields(reflect.TypeFor[helmv2.HelmReleaseSpec](), "valuesMode", "delivery", "source")
	if len(got) != 0 {
		t.Errorf("unreachable HelmReleaseSpec fields: %v", got)
	}
	if got := builtin.UnreachableJSONFields(reflect.TypeFor[helmv2.HelmReleaseSpec](), "values"); !slices.Equal(got, []string{"values"}) {
		t.Errorf("owning values: got %v, want [values]", got)
	}
}

// Fields hidden behind another field under the same key: the key decodes, but lands
// on the other field.
type (
	// The shallower promoted wins; reachInner's promoted is unreachable.
	reachShallow struct {
		reachInner
		Promoted string `json:"promoted"`
	}
	// encoding/json drops the diamond's value as ambiguous, then folds the key "value"
	// onto VALUE.
	reachFolded struct {
		reachLeft
		reachRight
		Upper string `json:"VALUE"`
	}
	// As reachShallow, behind an unexported embedded pointer the probe cannot allocate.
	reachShadowPtr struct {
		*reachLeaf
		Value string `json:"value"`
	}
	// A leaf whose type refers to itself: filling it must terminate.
	reachRecursiveLeaf struct {
		reachNode
		Next *reachNode `json:"next"`
	}
	reachNode struct {
		Next     *reachNode  `json:"next"`
		Children []reachNode `json:"children"`
	}
	// Its own MarshalJSON says nothing about field selection, so rivals are reported.
	reachCustom struct {
		reachInner
		Promoted string `json:"promoted"`
	}
	// Encoding the zero value panics (time.Time's MarshalJSON, promoted through a nil
	// embedded pointer), so the probe cannot run and both rivals are reported.
	reachPanics struct {
		reachInner
		Promoted string     `json:"promoted"`
		Stamp    reachStamp `json:"stamp,omitempty"`
	}
	reachStamp struct{ *time.Time }
	// Its own UnmarshalJSON bypasses encoding/json's field selection when decoding,
	// so an encoding says nothing about which rival a key reaches.
	reachSelfDecode struct {
		reachInner
		Promoted string `json:"promoted"`
	}
	// As reachSelfDecode, with UnmarshalJSON promoted from an embedded type: it takes
	// the whole object and sets neither rival.
	reachPromotedDecode struct {
		reachDecoder
		reachInner
		Promoted string `json:"promoted"`
	}
	reachDecoder struct{}
	// As reachSelfDecode, through UnmarshalText.
	reachTextDecode struct {
		reachInner
		Promoted string `json:"promoted"`
	}
)

func (c reachCustom) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string{c.reachInner.Promoted, c.Promoted})
}

func (s *reachSelfDecode) UnmarshalJSON(data []byte) error {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.reachInner.Promoted = m["promoted"]
	return nil
}

func (*reachDecoder) UnmarshalJSON([]byte) error { return nil }

func (*reachTextDecode) UnmarshalText([]byte) error { return nil }

// TestUnreachableJSONFields_FieldLevel: a field whose key works but reaches another
// field is reported by its Go field path.
func TestUnreachableJSONFields_FieldLevel(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		want []string
	}{
		{reflect.TypeFor[reachShallow](), []string{"reachInner.Promoted"}},
		{reflect.TypeFor[reachFolded](), []string{"reachLeft.reachLeaf.Value", "reachRight.reachLeaf.Value"}},
		{reflect.TypeFor[reachShadowPtr](), []string{"reachLeaf.Value"}},
		{reflect.TypeFor[reachRecursiveLeaf](), []string{"reachNode.Next"}},
		{reflect.TypeFor[reachCustom](), []string{"Promoted", "reachInner.Promoted"}},
		{reflect.TypeFor[reachPanics](), []string{"Promoted", "reachInner.Promoted"}},
		{reflect.TypeFor[reachSelfDecode](), []string{"Promoted", "reachInner.Promoted"}},
		{reflect.TypeFor[reachPromotedDecode](), []string{"Promoted", "reachInner.Promoted"}},
		{reflect.TypeFor[reachTextDecode](), []string{"Promoted", "reachInner.Promoted"}},
	} {
		if got := builtin.UnreachableJSONFields(tc.typ); !slices.Equal(got, tc.want) {
			t.Errorf("UnreachableJSONFields(%v) = %v, want %v", tc.typ, got, tc.want)
		}
	}

	// The fixtures show the gap: each key decodes, onto the other field.
	if s, _, err := builtin.DecodeStrictJSON[reachShallow](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "x" || s.reachInner.Promoted != "" {
		t.Errorf("reachShallow: %+v, %v; want promoted on the outer field", s, err)
	}
	if s, _, err := builtin.DecodeStrictJSON[reachFolded](map[string]any{"value": "x"}); err != nil || s.Upper != "x" {
		t.Errorf("reachFolded: %+v, %v; want value folded onto VALUE", s, err)
	}
	if s, _, err := builtin.DecodeStrictJSON[reachPanics](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "x" {
		t.Errorf("reachPanics: %+v, %v; want promoted on the outer field", s, err)
	}
	// A self-decoding root lands the key on the field its encoding does not select.
	if s, _, err := builtin.DecodeStrictJSON[reachSelfDecode](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "" || s.reachInner.Promoted != "x" {
		t.Errorf("reachSelfDecode: %+v, %v; want promoted on the inner field", s, err)
	}
	if s, _, err := builtin.DecodeStrictJSON[reachPromotedDecode](map[string]any{"promoted": "x"}); err != nil || s.Promoted != "" || s.reachInner.Promoted != "" {
		t.Errorf("reachPromotedDecode: %+v, %v; want neither rival set", s, err)
	}
}
