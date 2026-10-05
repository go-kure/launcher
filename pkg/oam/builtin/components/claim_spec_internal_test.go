package components

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestClaimSpecSchemaMatchesParser pins the published keys of the standalone
// claim fragment to claimSpecPropertyKeys at every level, and checks that the
// persistentvolumeclaim kind publishes the fragment and no rejected key.
func TestClaimSpecSchemaMatchesParser(t *testing.T) {
	fragment := schemaClaimSpec()
	if got, want := slices.Sorted(maps.Keys(fragment)), slices.Sorted(slices.Values(claimSpecPropertyKeys)); !slices.Equal(got, want) {
		t.Fatalf("schemaClaimSpec keys = %v\nwant claimSpecPropertyKeys %v", got, want)
	}
	root := oam.PropertySchema{Properties: fragment}
	assertSchemaKeysAt(t, root, "selector", volumeClaimSelectorKeys)
	assertSchemaKeysAt(t, root, "selector.matchExpressions.[]", volumeClaimSelectorExprKeys)
	assertSchemaKeysAt(t, root, "dataSourceRef", volumeClaimDataSourceRefKeys)

	kind := (&PersistentVolumeClaimHandler{}).PropertySchema()
	for _, key := range claimSpecPropertyKeys {
		if _, ok := kind[key]; !ok {
			t.Errorf("persistentvolumeclaim schema does not publish %q", key)
		}
	}
	for key := range claimRejectedKeys {
		if _, ok := kind[key]; ok {
			t.Errorf("persistentvolumeclaim schema publishes rejected key %q", key)
		}
		if slices.Contains(claimSpecPropertyKeys, key) {
			t.Errorf("%q is both read and rejected", key)
		}
	}
}

// TestClaimSpecSchema_EveryKeyDescribed walks the fragment: every property,
// nested property and array item carries a Description.
func TestClaimSpecSchema_EveryKeyDescribed(t *testing.T) {
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
	for key, s := range schemaClaimSpec() {
		walk(key, s)
	}
}

// claimSpecSamples authors each key of claimSpecPropertyKeys once.
func claimSpecSamples() map[string]any {
	return map[string]any{
		"selector": map[string]any{
			"matchLabels": map[string]any{"tier": "archive"},
			"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In", "values": []any{"a", "b"}},
			},
		},
		"dataSourceRef": map[string]any{
			"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly", "namespace": "backups",
		},
		"volumeName":                "pv-archive-0",
		"volumeAttributesClassName": "gold",
	}
}

// TestParseClaimSpec_EveryKeyIsRead authors each key alone and requires the
// parsed fields to differ from the unauthored ones, so a key that is listed and
// published but never read fails here.
func TestParseClaimSpec_EveryKeyIsRead(t *testing.T) {
	samples := claimSpecSamples()
	if got, want := slices.Sorted(maps.Keys(samples)), slices.Sorted(slices.Values(claimSpecPropertyKeys)); !slices.Equal(got, want) {
		t.Fatalf("sample keys = %v\nwant claimSpecPropertyKeys %v", got, want)
	}
	for key, value := range samples {
		t.Run(key, func(t *testing.T) {
			got, err := parseClaimSpec(map[string]any{key: value})
			if err != nil {
				t.Fatalf("parseClaimSpec: %v", err)
			}
			if reflect.DeepEqual(got, ClaimSpecFields{}) {
				t.Errorf("%s was authored and parsed to the zero value", key)
			}
		})
	}

	got, err := parseClaimSpec(samples)
	if err != nil {
		t.Fatalf("parseClaimSpec: %v", err)
	}
	want := ClaimSpecFields{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"tier": "archive"},
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "zone", Operator: metav1.LabelSelectorOpIn, Values: []string{"a", "b"}},
			},
		},
		DataSourceRef: &corev1.TypedObjectReference{
			APIGroup: new("snapshot.storage.k8s.io"), Kind: "VolumeSnapshot", Name: "nightly", Namespace: new("backups"),
		},
		VolumeName:                "pv-archive-0",
		VolumeAttributesClassName: new("gold"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseClaimSpec = %+v\nwant %+v", got, want)
	}
}

// TestParseClaimSpec_UnauthoredIsZero: an absent key, an explicit null and an
// empty string all leave the field unauthored, so the claim's output does not
// move.
func TestParseClaimSpec_UnauthoredIsZero(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"absent": {},
		"null": {
			"selector": nil, "dataSourceRef": nil, "volumeName": nil, "volumeAttributesClassName": nil, "dataSource": nil,
		},
		"empty strings": {"volumeName": "", "volumeAttributesClassName": ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseClaimSpec(props)
			if err != nil {
				t.Fatalf("parseClaimSpec: %v", err)
			}
			if !reflect.DeepEqual(got, ClaimSpecFields{}) {
				t.Errorf("parseClaimSpec = %+v, want the zero value", got)
			}
			var spec corev1.PersistentVolumeClaimSpec
			got.apply(&spec)
			if !reflect.DeepEqual(spec, corev1.PersistentVolumeClaimSpec{}) {
				t.Errorf("apply wrote %+v onto an empty spec, want nothing", spec)
			}
		})
	}
}

// TestClaimSpecFields_ApplyCopies edits the first applied spec in place and
// requires a second apply to be unchanged: nothing applied may alias the
// config.
func TestClaimSpecFields_ApplyCopies(t *testing.T) {
	f, err := parseClaimSpec(claimSpecSamples())
	if err != nil {
		t.Fatalf("parseClaimSpec: %v", err)
	}
	var first corev1.PersistentVolumeClaimSpec
	f.apply(&first)
	first.Selector.MatchLabels["tier"] = "changed"
	first.Selector.MatchExpressions[0].Values[0] = "changed"
	*first.DataSourceRef.APIGroup = "changed"
	*first.DataSourceRef.Namespace = "changed"
	first.DataSourceRef.Name = "changed"
	*first.VolumeAttributesClassName = "changed"

	var second corev1.PersistentVolumeClaimSpec
	f.apply(&second)
	want := corev1.PersistentVolumeClaimSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"tier": "archive"},
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "zone", Operator: metav1.LabelSelectorOpIn, Values: []string{"a", "b"}},
			},
		},
		DataSourceRef: &corev1.TypedObjectReference{
			APIGroup: new("snapshot.storage.k8s.io"), Kind: "VolumeSnapshot", Name: "nightly", Namespace: new("backups"),
		},
		VolumeName:                "pv-archive-0",
		VolumeAttributesClassName: new("gold"),
	}
	if !reflect.DeepEqual(second, want) {
		t.Errorf("second apply = %+v\nwant %+v", second, want)
	}
}

// TestBuildPVC_PodVolumeClaimHasNoSpecFields: a claim a workload's pvc volume
// describes never carries the standalone fields, so its output is unchanged.
func TestBuildPVC_PodVolumeClaimHasNoSpecFields(t *testing.T) {
	parsed, err := parseVolumes(map[string]any{"volumes": []any{
		map[string]any{"name": "data", "type": "pvc", "mountPath": "/data", "size": "1Gi"},
	}})
	if err != nil {
		t.Fatalf("parseVolumes: %v", err)
	}
	if len(parsed.PVCs) != 1 {
		t.Fatalf("PVCs = %d, want 1", len(parsed.PVCs))
	}
	if !reflect.DeepEqual(parsed.PVCs[0].Spec, ClaimSpecFields{}) {
		t.Errorf("a pvc volume's claim carries spec fields %+v", parsed.PVCs[0].Spec)
	}
	pvc, err := BuildPVC(parsed.PVCs[0], "default", nil)
	if err != nil {
		t.Fatalf("BuildPVC: %v", err)
	}
	if pvc.Spec.Selector != nil || pvc.Spec.DataSourceRef != nil || pvc.Spec.DataSource != nil ||
		pvc.Spec.VolumeName != "" || pvc.Spec.VolumeAttributesClassName != nil {
		t.Errorf("claim spec = %+v, want none of the standalone fields", pvc.Spec)
	}
}
