package components_test

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// statefulSetFrom builds the plain raw statefulset with the given top-level
// properties added, and returns the generated StatefulSet.
func statefulSetFrom(t *testing.T, extra map[string]any) *appsv1.StatefulSet {
	t.Helper()
	props := ptObject(t, stsPlain(htPlainPod))
	for k, v := range extra {
		props[k] = v
	}
	return stsGenerate(t, "db", props)
}

// TestStatefulSetSpec_ReachesSpec checks the kind-level properties land on the
// generated appsv1.StatefulSetSpec, not merely on the parsed config.
func TestStatefulSetSpec_ReachesSpec(t *testing.T) {
	sts := statefulSetFrom(t, map[string]any{
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
		"ordinals":    map[string]any{"start": 4},
		"serviceName": "db-headless",
		"replicas":    2,
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
	if spec.ServiceName != "db-headless" {
		t.Errorf("ServiceName = %q, want db-headless", spec.ServiceName)
	}
	if spec.Replicas == nil || *spec.Replicas != 2 {
		t.Errorf("Replicas = %v, want 2", spec.Replicas)
	}
}

// TestStatefulSetSpec_UnauthoredStaysUnset pins the no-op half of the projection:
// with none of the optional properties authored, the generated spec carries
// nothing the author did not write, since the constructor is identity-only
// (go-kure/launcher#361) and the kind fills no default (go-kure/launcher#1629):
// the API server defaults them at admission.
func TestStatefulSetSpec_UnauthoredStaysUnset(t *testing.T) {
	spec := statefulSetFrom(t, nil).Spec

	if spec.Replicas != nil {
		t.Errorf("Replicas = %d, want nil", *spec.Replicas)
	}
	if spec.ServiceName != "" {
		t.Errorf("ServiceName = %q, want empty", spec.ServiceName)
	}
	if spec.PodManagementPolicy != "" {
		t.Errorf("PodManagementPolicy = %q, want empty (constructor is identity-only)", spec.PodManagementPolicy)
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
	if len(spec.VolumeClaimTemplates) != 0 {
		t.Errorf("VolumeClaimTemplates = %v, want none", spec.VolumeClaimTemplates)
	}
}

// TestStatefulSetSpec_SelectorIsAuthored: `selector` is a required property and
// the emitted one is the authored one, untouched: launcher derives none and adds no
// `app` entry to it. Before go-kure/launcher#1629 it was refused as a property and
// the handler wrote `app=<name>` itself.
func TestStatefulSetSpec_SelectorIsAuthored(t *testing.T) {
	sts := statefulSetFrom(t, map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"tier": "db", "role": "primary"}},
		"template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"tier": "db", "role": "primary"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "registry.example/team/db:1.2.3"}}},
		},
	})
	got := sts.Spec.Selector
	if got == nil || len(got.MatchLabels) != 2 || got.MatchLabels["tier"] != "db" || got.MatchLabels["role"] != "primary" || len(got.MatchExpressions) != 0 {
		t.Errorf("Selector = %v, want the authored tier and role labels only", got)
	}
	if sts.Spec.Template.Labels["app"] != "db" {
		t.Errorf("template labels = %v, want the app label added", sts.Spec.Template.Labels)
	}

	err := generateErr(t, &components.StatefulsetHandler{}, "statefulset", map[string]any{"template": ptObject(t, stsPlain(htPlainPod))["template"]})
	if err == nil || !strings.Contains(err.Error(), "selector: required") {
		t.Errorf("no selector: err = %v, want the required refusal", err)
	}
}
