package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// The persistentvolumeclaim kind's selector, dataSourceRef, volumeName and
// volumeAttributesClassName (go-kure/launcher#790).

func TestPersistentVolumeClaimHandler_Generate_SpecFields(t *testing.T) {
	cfg, err := kindConfig(t, &components.PersistentVolumeClaimHandler{}, "persistentvolumeclaim", "data", map[string]any{
		"size": "5Gi",
		"selector": map[string]any{
			"matchLabels": map[string]any{"tier": "archive"},
			"matchExpressions": []any{
				map[string]any{"key": "zone", "operator": "In", "values": []any{"a", "b"}},
				map[string]any{"key": "retired", "operator": "DoesNotExist"},
			},
		},
		"dataSourceRef": map[string]any{
			"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly",
		},
		"volumeName":                "pv-archive-0",
		"volumeAttributesClassName": "gold",
	})
	if err != nil {
		t.Fatal(err)
	}
	pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)

	sel := pvc.Spec.Selector
	if sel == nil || sel.MatchLabels["tier"] != "archive" || len(sel.MatchExpressions) != 2 {
		t.Fatalf("selector = %+v, want tier=archive and two expressions", sel)
	}
	if e := sel.MatchExpressions[0]; e.Key != "zone" || e.Operator != metav1.LabelSelectorOpIn || len(e.Values) != 2 || e.Values[0] != "a" || e.Values[1] != "b" {
		t.Errorf("matchExpressions[0] = %+v, want zone In [a b]", e)
	}
	if e := sel.MatchExpressions[1]; e.Key != "retired" || e.Operator != metav1.LabelSelectorOpDoesNotExist || len(e.Values) != 0 {
		t.Errorf("matchExpressions[1] = %+v, want retired DoesNotExist", e)
	}

	ref := pvc.Spec.DataSourceRef
	if ref == nil || ref.Kind != "VolumeSnapshot" || ref.Name != "nightly" || ref.APIGroup == nil || *ref.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("dataSourceRef = %+v, want the VolumeSnapshot nightly", ref)
	}
	if ref != nil && ref.Namespace != nil {
		t.Errorf("dataSourceRef.namespace = %q, want unset", *ref.Namespace)
	}
	// The API server mirrors dataSourceRef into dataSource; the kind writes
	// only the one the author set.
	if pvc.Spec.DataSource != nil {
		t.Errorf("dataSource = %+v, want unset", pvc.Spec.DataSource)
	}
	if pvc.Spec.VolumeName != "pv-archive-0" {
		t.Errorf("volumeName = %q, want pv-archive-0", pvc.Spec.VolumeName)
	}
	if got := pvc.Spec.VolumeAttributesClassName; got == nil || *got != "gold" {
		t.Errorf("volumeAttributesClassName = %v, want gold", got)
	}
	// The fields the kind already had are untouched.
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "5Gi" {
		t.Errorf("requests.storage = %s, want 5Gi", got.String())
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("accessModes = %v, want [ReadWriteOnce]", pvc.Spec.AccessModes)
	}
}

// A document that authors none of the new keys keeps the claim it built before.
func TestPersistentVolumeClaimHandler_Generate_SpecFieldsUnsetByDefault(t *testing.T) {
	for name, props := range map[string]map[string]any{
		"absent": {"size": "5Gi"},
		"null": {
			"size": "5Gi", "selector": nil, "dataSourceRef": nil, "volumeName": nil, "volumeAttributesClassName": nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := kindConfig(t, &components.PersistentVolumeClaimHandler{}, "persistentvolumeclaim", "data", props)
			if err != nil {
				t.Fatal(err)
			}
			pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
			if pvc.Spec.Selector != nil || pvc.Spec.DataSourceRef != nil || pvc.Spec.DataSource != nil ||
				pvc.Spec.VolumeName != "" || pvc.Spec.VolumeAttributesClassName != nil {
				t.Errorf("claim spec = %+v, want none of selector, dataSourceRef, dataSource, volumeName, volumeAttributesClassName", pvc.Spec)
			}
		})
	}
}

// A claim in another namespace is a legal data source where the cluster allows
// it; the kind writes the namespace and leaves the grant to the cluster.
func TestPersistentVolumeClaimHandler_Generate_CrossNamespaceDataSourceRef(t *testing.T) {
	cfg, err := kindConfig(t, &components.PersistentVolumeClaimHandler{}, "persistentvolumeclaim", "data", map[string]any{
		"size":          "5Gi",
		"dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden", "namespace": "images"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pvc := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	ref := pvc.Spec.DataSourceRef
	if ref == nil || ref.Kind != "PersistentVolumeClaim" || ref.Name != "golden" || ref.Namespace == nil || *ref.Namespace != "images" {
		t.Fatalf("dataSourceRef = %+v, want the claim images/golden", ref)
	}
	if ref.APIGroup != nil {
		t.Errorf("dataSourceRef.apiGroup = %q, want unset for the core group", *ref.APIGroup)
	}
}

// One config renders twice to equal claims that share no state: an edit of the
// first claim reaches neither the config nor the second claim.
func TestPersistentVolumeClaimHandler_Generate_SpecFieldsNotAliased(t *testing.T) {
	cfg, err := kindConfig(t, &components.PersistentVolumeClaimHandler{}, "persistentvolumeclaim", "data", map[string]any{
		"size":                      "5Gi",
		"selector":                  map[string]any{"matchLabels": map[string]any{"tier": "archive"}},
		"dataSourceRef":             map[string]any{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly"},
		"volumeAttributesClassName": "gold",
	})
	if err != nil {
		t.Fatal(err)
	}
	first := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	first.Spec.Selector.MatchLabels["tier"] = "changed"
	*first.Spec.DataSourceRef.APIGroup = "changed"
	first.Spec.DataSourceRef.Name = "changed"
	*first.Spec.VolumeAttributesClassName = "changed"

	second := generateOne(t, cfg, "data").(*corev1.PersistentVolumeClaim)
	if got := second.Spec.Selector.MatchLabels["tier"]; got != "archive" {
		t.Errorf("second render selector tier = %q, want archive", got)
	}
	if ref := second.Spec.DataSourceRef; *ref.APIGroup != "snapshot.storage.k8s.io" || ref.Name != "nightly" {
		t.Errorf("second render dataSourceRef = %+v, want the authored one", ref)
	}
	if got := *second.Spec.VolumeAttributesClassName; got != "gold" {
		t.Errorf("second render volumeAttributesClassName = %q, want gold", got)
	}
}

func TestPersistentVolumeClaimHandler_RejectsSpecFields(t *testing.T) {
	h := &components.PersistentVolumeClaimHandler{}
	for name, tc := range map[string]struct {
		props map[string]any
		want  string
	}{
		"dataSource": {
			map[string]any{"dataSource": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden"}},
			"dataSource: not authorable — superseded by dataSourceRef",
		},
		"volumeName not a string":   {map[string]any{"volumeName": 7}, "volumeName"},
		"volumeName not a PV name":  {map[string]any{"volumeName": "PV_Archive"}, `volumeName: invalid name "PV_Archive"`},
		"selector not an object":    {map[string]any{"selector": "tier=archive"}, "selector"},
		"selector empty":            {map[string]any{"selector": map[string]any{}}, "selector: empty selector"},
		"selector empty labels":     {map[string]any{"selector": map[string]any{"matchLabels": map[string]any{}}}, "selector: empty selector"},
		"selector unknown key":      {map[string]any{"selector": map[string]any{"matchFields": map[string]any{}}}, `selector: unrecognized key "matchFields"`},
		"selector bad label value":  {map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"tier": "not a value"}}}, "selector.matchLabels"},
		"selector In without value": {map[string]any{"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "In"}}}}, "selector.matchExpressions[0].values: at least one value is required for operator In"},
		"selector Exists and value": {map[string]any{"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "Exists", "values": []any{"a"}}}}}, "selector.matchExpressions[0].values: must be empty for operator Exists"},
		"selector bad operator":     {map[string]any{"selector": map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "Gt", "values": []any{"1"}}}}}, `selector.matchExpressions[0].operator: invalid value "Gt"`},
		"dataSourceRef no kind":     {map[string]any{"dataSourceRef": map[string]any{"name": "nightly"}}, "dataSourceRef.kind: required"},
		"dataSourceRef no name":     {map[string]any{"dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim"}}, "dataSourceRef.name: required"},
		"dataSourceRef core group":  {map[string]any{"dataSourceRef": map[string]any{"kind": "VolumeSnapshot", "name": "nightly"}}, "dataSourceRef: kind must be PersistentVolumeClaim when apiGroup names the core group"},
		"dataSourceRef bad group":   {map[string]any{"dataSourceRef": map[string]any{"apiGroup": "Snapshot_Group", "kind": "VolumeSnapshot", "name": "nightly"}}, `dataSourceRef.apiGroup: invalid group "Snapshot_Group"`},
		"dataSourceRef bad ns":      {map[string]any{"dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden", "namespace": "a.b"}}, `dataSourceRef.namespace: invalid name "a.b"`},
		"dataSourceRef unknown key": {map[string]any{"dataSourceRef": map[string]any{"kind": "PersistentVolumeClaim", "name": "golden", "uid": "1"}}, `dataSourceRef: unrecognized key "uid"`},
		"bad attributes class":      {map[string]any{"volumeAttributesClassName": "Gold_Class"}, `volumeAttributesClassName: invalid name "Gold_Class"`},
		"attributes class a number": {map[string]any{"volumeAttributesClassName": 3}, "volumeAttributesClassName"},
	} {
		t.Run(name, func(t *testing.T) {
			props := map[string]any{"size": "1Gi"}
			for k, v := range tc.props {
				props[k] = v
			}
			_, err := kindConfig(t, h, "persistentvolumeclaim", "data", props)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// volumeName does not replace the claim's own requirements: size, the access
// modes and the policy maximum still apply to a pre-bound claim.
func TestPersistentVolumeClaimHandler_VolumeNameKeepsPolicy(t *testing.T) {
	h := &components.PersistentVolumeClaimHandler{}

	cfg, err := kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{"volumeName": "pv-archive-0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.(policyApplier).ApplyPolicy(nil); err == nil || !strings.Contains(err.Error(), "size: required") {
		t.Errorf("no size: err = %v, want a size refusal", err)
	}
	if _, err := cfg.Generate(stack.NewApplication("data", "default", cfg)); err == nil {
		t.Error("Generate without a size succeeded, want a refusal")
	}

	cfg, err = kindConfig(t, h, "persistentvolumeclaim", "data", map[string]any{"size": "20Gi", "volumeName": "pv-archive-0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.(policyApplier).ApplyPolicy(&stubPolicy{maxStorageSize: "10Gi"}); err == nil {
		t.Error("20Gi under a 10Gi maximum: ApplyPolicy succeeded, want a refusal")
	}
}
