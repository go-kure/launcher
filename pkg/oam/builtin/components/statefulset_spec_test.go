package components_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// statefulSetFrom pulls the StatefulSet out of a generated object list.
func statefulSetFrom(t *testing.T, props map[string]any) *appsv1.StatefulSet {
	t.Helper()
	for _, obj := range generateKind(t, &components.StatefulsetHandler{}, "statefulset", props) {
		if sts, ok := (*obj).(*appsv1.StatefulSet); ok {
			return sts
		}
	}
	t.Fatal("no StatefulSet in the generated objects")
	return nil
}

// TestStatefulSetSpec_ReachesSpec checks the six kind-level properties land on
// the generated appsv1.StatefulSetSpec, not merely on the parsed config.
func TestStatefulSetSpec_ReachesSpec(t *testing.T) {
	sts := statefulSetFrom(t, map[string]any{
		"image":               "ghcr.io/org/app:v1",
		"podManagementPolicy": "Parallel",
		"updateStrategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"partition":      2,
				"maxUnavailable": "50%",
			},
		},
		"revisionHistoryLimit": 7,
		"minReadySeconds":      15,
		"persistentVolumeClaimRetentionPolicy": map[string]any{
			"whenDeleted": "Delete",
			"whenScaled":  "Retain",
		},
		"ordinals": map[string]any{"start": 4},
	})
	spec := sts.Spec

	if spec.PodManagementPolicy != appsv1.ParallelPodManagement {
		t.Errorf("PodManagementPolicy = %q, want Parallel", spec.PodManagementPolicy)
	}
	if spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType {
		t.Errorf("UpdateStrategy.Type = %q, want RollingUpdate", spec.UpdateStrategy.Type)
	}
	ru := spec.UpdateStrategy.RollingUpdate
	if ru == nil {
		t.Fatal("UpdateStrategy.RollingUpdate = nil, want partition 2 and maxUnavailable 50%")
	}
	if ru.Partition == nil || *ru.Partition != 2 {
		t.Errorf("UpdateStrategy.RollingUpdate.Partition = %v, want 2", ru.Partition)
	}
	// A separate if, not an else-if chained onto Partition: chained, a wrong
	// Partition left MaxUnavailable unasserted entirely.
	if ru.MaxUnavailable == nil || *ru.MaxUnavailable != intstr.FromString("50%") {
		t.Errorf("UpdateStrategy.RollingUpdate.MaxUnavailable = %v, want 50%%", ru.MaxUnavailable)
	}
	if spec.RevisionHistoryLimit == nil || *spec.RevisionHistoryLimit != 7 {
		t.Errorf("RevisionHistoryLimit = %v, want 7", spec.RevisionHistoryLimit)
	}
	if spec.MinReadySeconds != 15 {
		t.Errorf("MinReadySeconds = %d, want 15", spec.MinReadySeconds)
	}
	rp := spec.PersistentVolumeClaimRetentionPolicy
	if rp == nil || rp.WhenDeleted != appsv1.DeletePersistentVolumeClaimRetentionPolicyType ||
		rp.WhenScaled != appsv1.RetainPersistentVolumeClaimRetentionPolicyType {
		t.Errorf("PersistentVolumeClaimRetentionPolicy = %v, want Delete/Retain", rp)
	}
	if spec.Ordinals == nil || spec.Ordinals.Start != 4 {
		t.Errorf("Ordinals = %v, want start 4", spec.Ordinals)
	}
}

// TestStatefulSetSpec_UnauthoredKeepsConstructorDefaults pins the no-op half of
// the projection: with none of the six properties authored, the generated spec
// still carries exactly what kure's CreateStatefulSet put there, so adding this
// surface cannot move existing output.
func TestStatefulSetSpec_UnauthoredKeepsConstructorDefaults(t *testing.T) {
	spec := statefulSetFrom(t, map[string]any{"image": "ghcr.io/org/app:v1"}).Spec

	if spec.PodManagementPolicy != appsv1.OrderedReadyPodManagement {
		t.Errorf("PodManagementPolicy = %q, want the constructor's OrderedReady", spec.PodManagementPolicy)
	}
	if spec.UpdateStrategy.Type != "" || spec.UpdateStrategy.RollingUpdate != nil {
		t.Errorf("UpdateStrategy = %+v, want the constructor's empty value", spec.UpdateStrategy)
	}
	if spec.RevisionHistoryLimit != nil {
		t.Errorf("RevisionHistoryLimit = %v, want nil", spec.RevisionHistoryLimit)
	}
	if spec.MinReadySeconds != 0 {
		t.Errorf("MinReadySeconds = %d, want 0", spec.MinReadySeconds)
	}
	if spec.PersistentVolumeClaimRetentionPolicy != nil {
		t.Errorf("PersistentVolumeClaimRetentionPolicy = %v, want nil", spec.PersistentVolumeClaimRetentionPolicy)
	}
	if spec.Ordinals != nil {
		t.Errorf("Ordinals = %v, want nil", spec.Ordinals)
	}
}

// TestStatefulSetSpec_SelectorStaysBuilderManaged: `selector` is rejected as a
// property rather than ignored, and the constructor's own selector survives.
func TestStatefulSetSpec_SelectorStaysBuilderManaged(t *testing.T) {
	err := generateErr(t, &components.StatefulsetHandler{}, "statefulset", map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"selector": map[string]any{"matchLabels": map[string]any{"app": "mine"}},
	})
	if err == nil {
		t.Fatal("authoring selector succeeded, want a rejection")
	}

	sts := statefulSetFrom(t, map[string]any{"image": "ghcr.io/org/app:v1"})
	if sts.Spec.Selector == nil || sts.Spec.Selector.MatchLabels["app"] != "app" {
		t.Errorf("Selector = %v, want the constructor's app=app", sts.Spec.Selector)
	}
}
