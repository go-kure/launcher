package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// renderTwice builds one handler config and generates from it twice, running
// the caller's edit against the first render in between. That order is the
// point: the config is the thing under test, so the second render must be
// unaffected by anything done to the first.
func renderTwice(t *testing.T, h oam.ComponentHandler, kind string, props map[string]any, edit func(objects []*client.Object)) []*client.Object {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)

	first, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	edit(first)

	second, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	return second
}

// TestStatefulset_RenderingTwiceIsUnaffectedByEditingTheFirstRender covers the
// aliasing shape a consumer actually meets. A handler config is reusable and
// editing a generated object in place is an expected use — stamping a label,
// tightening a limit, repointing a data source — so every value projected out
// of the config must be a copy. Assigning the config's own pointer or map
// instead makes the first caller's edit reappear in the next render, and the
// symptom shows up far from the cause: a second workload silently carrying the
// first one's customization.
func TestStatefulset_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	props := map[string]any{
		"image":                "ghcr.io/org/app:v1",
		"revisionHistoryLimit": 7,
		"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"partition": 2},
		},
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenDeleted": "Retain",
			"whenScaled":  "Retain",
		},
		"ordinals": map[string]any{"start": 3},
		"volumeClaimTemplates": []any{
			map[string]any{
				"name":      "data",
				"mountPath": "/data",
				"size":      "1Gi",
				"selector": map[string]any{
					"matchLabels": map[string]any{"tier": "db"},
				},
				"resources": map[string]any{
					"limits": map[string]any{"storage": "2Gi"},
				},
				"dataSourceRef": map[string]any{
					"kind": "PersistentVolumeClaim",
					"name": "seed",
				},
				// Filesystem is the only mode this kind accepts (a Block
				// volume would need volumeDevices, not a mountPath), so the
				// edit below flips it to Block: a value the config could not
				// hold, which makes a leak unmistakable.
				"volumeMode":                "Filesystem",
				"volumeAttributesClassName": "gold",
			},
		},
	}

	second := renderTwice(t, &components.StatefulsetHandler{}, "statefulset", props, func(objects []*client.Object) {
		sts, ok := (*objects[0]).(*appsv1.StatefulSet)
		if !ok {
			t.Fatalf("first object is %T, want *appsv1.StatefulSet", *objects[0])
		}
		// Spec-level pointers.
		sts.Spec.UpdateStrategy.RollingUpdate.Partition = ptrInt32(99)
		*sts.Spec.RevisionHistoryLimit = 99
		sts.Spec.PersistentVolumeClaimRetentionPolicy.WhenDeleted = appsv1.DeletePersistentVolumeClaimRetentionPolicyType
		sts.Spec.Ordinals.Start = 99

		// Claim-template pointers and maps.
		if len(sts.Spec.VolumeClaimTemplates) != 1 {
			t.Fatalf("volumeClaimTemplates = %d, want 1", len(sts.Spec.VolumeClaimTemplates))
		}
		pvc := &sts.Spec.VolumeClaimTemplates[0]
		pvc.Spec.Selector.MatchLabels["tier"] = "hijacked"
		pvc.Spec.Resources.Limits[corev1.ResourceStorage] = resource.MustParse("99Gi")
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("99Gi")
		pvc.Spec.DataSourceRef.Name = "hijacked"
		*pvc.Spec.VolumeMode = corev1.PersistentVolumeBlock
		*pvc.Spec.VolumeAttributesClassName = "hijacked"
	})

	sts, ok := (*second[0]).(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("second render's first object is %T, want *appsv1.StatefulSet", *second[0])
	}

	if got := *sts.Spec.UpdateStrategy.RollingUpdate.Partition; got != 2 {
		t.Errorf("updateStrategy.rollingUpdate.partition = %d, want 2 — the first render's edit leaked back into the config", got)
	}
	if got := *sts.Spec.RevisionHistoryLimit; got != 7 {
		t.Errorf("revisionHistoryLimit = %d, want 7", got)
	}
	if got := sts.Spec.PersistentVolumeClaimRetentionPolicy.WhenDeleted; got != appsv1.RetainPersistentVolumeClaimRetentionPolicyType {
		t.Errorf("persistentVolumeClaimRetentionPolicy.whenDeleted = %q, want Retain", got)
	}
	if got := sts.Spec.Ordinals.Start; got != 3 {
		t.Errorf("ordinals.start = %d, want 3", got)
	}

	pvc := sts.Spec.VolumeClaimTemplates[0]
	if got := pvc.Spec.Selector.MatchLabels["tier"]; got != "db" {
		t.Errorf("selector.matchLabels[tier] = %q, want %q", got, "db")
	}
	if got := pvc.Spec.Resources.Limits[corev1.ResourceStorage]; got.String() != "2Gi" {
		t.Errorf("resources.limits.storage = %q, want 2Gi", got.String())
	}
	// Requests is the one value here the apply cannot alias at the map level —
	// the constructor builds that map fresh per render and apply merges into
	// it — so this assertion pins that property rather than the deep copy.
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "1Gi" {
		t.Errorf("resources.requests.storage = %q, want 1Gi", got.String())
	}
	if got := pvc.Spec.DataSourceRef.Name; got != "seed" {
		t.Errorf("dataSourceRef.name = %q, want %q", got, "seed")
	}
	if got := *pvc.Spec.VolumeMode; got != corev1.PersistentVolumeFilesystem {
		t.Errorf("volumeMode = %q, want Filesystem", got)
	}
	if got := *pvc.Spec.VolumeAttributesClassName; got != "gold" {
		t.Errorf("volumeAttributesClassName = %q, want %q", got, "gold")
	}
}

// TestDaemonset_RenderingTwiceIsUnaffectedByEditingTheFirstRender is the same
// property on the kind the review did not name. Its apply projects the same two
// shapes — an update strategy holding a pointer, and a bare *int32 — so it
// needs the same guarantee and the same guard.
func TestDaemonset_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	props := map[string]any{
		"image":                "ghcr.io/org/app:v1",
		"revisionHistoryLimit": 4,
		"updateStrategy": map[string]any{
			"type":          "RollingUpdate",
			"rollingUpdate": map[string]any{"maxUnavailable": 1},
		},
	}

	second := renderTwice(t, &components.DaemonsetHandler{}, "daemonset", props, func(objects []*client.Object) {
		ds, ok := (*objects[0]).(*appsv1.DaemonSet)
		if !ok {
			t.Fatalf("first object is %T, want *appsv1.DaemonSet", *objects[0])
		}
		ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable = nil
		*ds.Spec.RevisionHistoryLimit = 99
	})

	ds, ok := (*second[0]).(*appsv1.DaemonSet)
	if !ok {
		t.Fatalf("second render's first object is %T, want *appsv1.DaemonSet", *second[0])
	}
	if ds.Spec.UpdateStrategy.RollingUpdate == nil || ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable == nil {
		t.Fatal("updateStrategy.rollingUpdate.maxUnavailable is gone — the first render's edit leaked back into the config")
	}
	if got := ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.IntValue(); got != 1 {
		t.Errorf("updateStrategy.rollingUpdate.maxUnavailable = %d, want 1", got)
	}
	if got := *ds.Spec.RevisionHistoryLimit; got != 4 {
		t.Errorf("revisionHistoryLimit = %d, want 4", got)
	}
}

func ptrInt32(n int32) *int32 { return &n }
