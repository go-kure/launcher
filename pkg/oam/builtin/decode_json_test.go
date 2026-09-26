package builtin_test

import (
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
