package components

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// vctProps wraps one volumeClaimTemplates entry in the props shape
// parseVolumeClaimTemplates reads.
func vctProps(entry map[string]any) map[string]any {
	return map[string]any{"volumeClaimTemplates": []any{entry}}
}

// TestVolumeClaimTemplateSchemaMatchesParser pins the published key set of one
// entry to volumeClaimTemplatePropertyKeys, and checks no rejected key is
// advertised.
func TestVolumeClaimTemplateSchemaMatchesParser(t *testing.T) {
	item := schemaVolumeClaimTemplates().Items
	if item == nil {
		t.Fatal("schemaVolumeClaimTemplates has no Items")
	}
	got := make([]string, 0, len(item.Properties))
	for k := range item.Properties {
		got = append(got, k)
	}
	slices.Sort(got)
	want := slices.Clone(volumeClaimTemplatePropertyKeys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("volumeClaimTemplates item keys = %v\nwant %v", got, want)
	}
	for k := range volumeClaimTemplateRejectedKeys {
		if _, ok := item.Properties[k]; ok {
			t.Errorf("schema publishes rejected key %q", k)
		}
	}
}

// TestVolumeClaimTemplateSchema_EveryKeyDescribed walks the fragment: every
// property, nested property and array item carries a Description.
func TestVolumeClaimTemplateSchema_EveryKeyDescribed(t *testing.T) {
	var walk func(path string, s oam.PropertySchema)
	walk = func(path string, s oam.PropertySchema) {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: missing Description", path)
		}
		for k, sub := range s.Properties {
			walk(path+"."+k, sub)
		}
		if s.Items != nil {
			walk(path+"[]", *s.Items)
		}
	}
	walk("volumeClaimTemplates", schemaVolumeClaimTemplates())
}

// TestParseVolumeClaimTemplates_SpecRoundTrip authors every claim-spec key the
// projection adds and checks the fields it lands in.
func TestParseVolumeClaimTemplates_SpecRoundTrip(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"size":      "10Gi",
		"mountPath": "/var/lib/data",
		"selector": map[string]any{
			"matchLabels": map[string]any{"tier": "fast"},
			"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In", "values": []any{"a", "b"}},
				map[string]any{"key": "legacy", "operator": "DoesNotExist"},
			},
		},
		"resources":  map[string]any{"limits": map[string]any{"storage": "20Gi"}},
		"volumeMode": "Block",
		"dataSourceRef": map[string]any{
			"apiGroup":  "snapshot.storage.k8s.io",
			"kind":      "VolumeSnapshot",
			"name":      "seed",
			"namespace": "golden",
		},
		"volumeAttributesClassName": "gold",
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if len(vcts) != 1 {
		t.Fatalf("got %d templates, want 1", len(vcts))
	}
	spec := vcts[0].Spec

	if spec.Selector == nil || spec.Selector.MatchLabels["tier"] != "fast" {
		t.Errorf("Selector.MatchLabels = %v, want tier=fast", spec.Selector)
	}
	if got := len(spec.Selector.MatchExpressions); got != 2 {
		t.Fatalf("MatchExpressions has %d entries, want 2", got)
	}
	if e := spec.Selector.MatchExpressions[0]; e.Key != "zone" ||
		e.Operator != metav1.LabelSelectorOpIn || !slices.Equal(e.Values, []string{"a", "b"}) {
		t.Errorf("MatchExpressions[0] = %+v, want zone In [a b]", e)
	}
	if e := spec.Selector.MatchExpressions[1]; e.Operator != metav1.LabelSelectorOpDoesNotExist || len(e.Values) != 0 {
		t.Errorf("MatchExpressions[1] = %+v, want DoesNotExist with no values", e)
	}
	if spec.Resources == nil {
		t.Fatal("Resources is nil")
	}
	if got := spec.Resources.Limits[corev1.ResourceStorage]; got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("Resources.Limits.storage = %v, want 20Gi", &got)
	}
	if spec.Resources.Requests != nil {
		t.Errorf("Resources.Requests = %v, want nil (size carries it)", spec.Resources.Requests)
	}
	if spec.VolumeMode == nil || *spec.VolumeMode != corev1.PersistentVolumeBlock {
		t.Errorf("VolumeMode = %v, want Block", spec.VolumeMode)
	}
	ref := spec.DataSourceRef
	if ref == nil || ref.Kind != "VolumeSnapshot" || ref.Name != "seed" ||
		ref.APIGroup == nil || *ref.APIGroup != "snapshot.storage.k8s.io" ||
		ref.Namespace == nil || *ref.Namespace != "golden" {
		t.Errorf("DataSourceRef = %+v, want the authored snapshot reference", ref)
	}
	if spec.VolumeAttributesClassName == nil || *spec.VolumeAttributesClassName != "gold" {
		t.Errorf("VolumeAttributesClassName = %v, want gold", spec.VolumeAttributesClassName)
	}
}

// TestParseVolumeClaimTemplates_LongSizeSpelling: resources.requests.storage
// satisfies the size requirement on its own.
func TestParseVolumeClaimTemplates_LongSizeSpelling(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"mountPath": "/var/lib/data",
		"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}},
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if vcts[0].Size != "" {
		t.Errorf("Size = %q, want empty (only the long spelling was authored)", vcts[0].Size)
	}
	got := vcts[0].Spec.Resources.Requests[corev1.ResourceStorage]
	if got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("requests.storage = %v, want 5Gi", &got)
	}
}

// TestParseVolumeClaimTemplates_UnauthoredSpecIsZero: an entry using none of the
// projected keys parses to the zero spec, so apply cannot move its output.
func TestParseVolumeClaimTemplates_UnauthoredSpecIsZero(t *testing.T) {
	vcts, err := parseVolumeClaimTemplates(vctProps(map[string]any{
		"name":      "data",
		"size":      "10Gi",
		"mountPath": "/var/lib/data",
	}))
	if err != nil {
		t.Fatalf("parseVolumeClaimTemplates: %v", err)
	}
	if vcts[0].Spec != (VolumeClaimSpecConfig{}) {
		t.Errorf("Spec = %+v, want the zero config", vcts[0].Spec)
	}

	pvc := corev1.PersistentVolumeClaim{}
	vcts[0].Spec.apply(&pvc)
	if !pvc.Spec.Resources.Requests.Storage().IsZero() || pvc.Spec.Selector != nil ||
		pvc.Spec.VolumeMode != nil || pvc.Spec.DataSourceRef != nil ||
		pvc.Spec.VolumeAttributesClassName != nil {
		t.Errorf("apply wrote a field from the zero config: %+v", pvc.Spec)
	}
}

// TestVolumeClaimSpec_ApplyMergesRequests: authoring only `limits` leaves the
// constructor's requests.storage (from `size`) in place.
func TestVolumeClaimSpec_ApplyMergesRequests(t *testing.T) {
	pvc := corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}}
	cfg := VolumeClaimSpecConfig{Resources: &corev1.VolumeResourceRequirements{
		Limits: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
	}}
	cfg.apply(&pvc)

	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Errorf("requests.storage = %v, want the constructor's 10Gi", &got)
	}
	if got := pvc.Spec.Resources.Limits[corev1.ResourceStorage]; got.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("limits.storage = %v, want 20Gi", &got)
	}
}

func TestParseVolumeClaimTemplates_SpecErrors(t *testing.T) {
	base := map[string]any{"name": "data", "size": "10Gi", "mountPath": "/data"}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name    string
		entry   map[string]any
		wantErr string
	}{
		{"unknown key", with(map[string]any{"storageClassName": "fast"}), `unrecognized key "storageClassName"`},
		{"volumeName rejected", with(map[string]any{"volumeName": "pv-0"}), "volumeName: not authorable"},
		{"dataSource rejected", with(map[string]any{"dataSource": map[string]any{}}), "dataSource: not authorable"},
		{
			"size and long spelling together",
			with(map[string]any{"resources": map[string]any{"requests": map[string]any{"storage": "5Gi"}}}),
			"size and resources.requests.storage are the same field",
		},
		{
			"neither size nor long spelling",
			map[string]any{"name": "data", "mountPath": "/data"},
			"missing required field 'size'",
		},
		{
			"non-storage resource",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": "1"}}}),
			"is not a claim resource",
		},
		{
			"negative storage limit",
			with(map[string]any{"resources": map[string]any{"limits": map[string]any{"storage": "-1Gi"}}}),
			"must not be negative",
		},
		{"volumeMode enum", with(map[string]any{"volumeMode": "Raw"}), "volumeMode: invalid value"},
		{
			"selector empty",
			with(map[string]any{"selector": map[string]any{}}),
			"at least one of matchLabels or matchExpressions",
		},
		{
			"selector unknown key",
			with(map[string]any{"selector": map[string]any{"matchFields": []any{}}}),
			`unrecognized key "matchFields"`,
		},
		{
			"In without values",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In"},
			}}}),
			"at least one value is required for operator In",
		},
		{
			"Exists with values",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "Exists", "values": []any{"a"}},
			}}}),
			"must be empty for operator Exists",
		},
		{
			"unknown operator",
			with(map[string]any{"selector": map[string]any{"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "Matches", "values": []any{"a"}},
			}}}),
			"operator: invalid value",
		},
		{
			"dataSourceRef missing kind",
			with(map[string]any{"dataSourceRef": map[string]any{"name": "seed"}}),
			"kind: required",
		},
		{
			"dataSourceRef core group with a foreign kind",
			with(map[string]any{"dataSourceRef": map[string]any{"kind": "VolumeSnapshot", "name": "seed"}}),
			"kind must be PersistentVolumeClaim when apiGroup names the core group",
		},
		{
			"dataSourceRef bad namespace",
			with(map[string]any{"dataSourceRef": map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "seed", "namespace": "Not_Valid"}}),
			"namespace: invalid name",
		},
		{
			"volumeAttributesClassName invalid",
			with(map[string]any{"volumeAttributesClassName": "Not_Valid"}),
			"volumeAttributesClassName: invalid name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseVolumeClaimTemplates(vctProps(tc.entry))
			if err == nil {
				t.Fatalf("parseVolumeClaimTemplates(%v) succeeded, want error containing %q", tc.entry, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
