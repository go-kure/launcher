package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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

// TestDeployment_RenderingTwiceIsUnaffectedByEditingTheFirstRender is the same
// property on the kind added in go-kure/launcher#343. Its apply projects the
// same two shapes as the others — a strategy whose struct holds a
// *RollingUpdateDeployment, and bare *int32s (revisionHistoryLimit,
// progressDeadlineSeconds) — so it needs the same guarantee and the same guard.
func TestDeployment_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	props := map[string]any{
		"image":                   "ghcr.io/org/app:v1",
		"revisionHistoryLimit":    4,
		"progressDeadlineSeconds": 700,
		"strategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"maxUnavailable": "25%",
				"maxSurge":       2,
			},
		},
	}

	second := renderTwice(t, &components.DeploymentHandler{}, "deployment", props, func(objects []*client.Object) {
		dep, ok := (*objects[0]).(*appsv1.Deployment)
		if !ok {
			t.Fatalf("first object is %T, want *appsv1.Deployment", *objects[0])
		}
		// Checked rather than dereferenced blind: every one of these is a
		// pointer the handler is supposed to have written, so a regression that
		// leaves one unset is exactly what this test exists to catch. Writing
		// through it would panic and take the whole test binary down with it,
		// reporting a crash where a named failure belongs.
		if dep.Spec.Strategy.RollingUpdate == nil {
			t.Fatal("first render has no strategy.rollingUpdate — nothing to alias, so this test cannot prove anything")
		}
		if dep.Spec.RevisionHistoryLimit == nil {
			t.Fatal("first render has no revisionHistoryLimit — nothing to alias, so this test cannot prove anything")
		}
		if dep.Spec.ProgressDeadlineSeconds == nil {
			t.Fatal("first render has no progressDeadlineSeconds — nothing to alias, so this test cannot prove anything")
		}
		dep.Spec.Strategy.RollingUpdate.MaxUnavailable = nil
		*dep.Spec.RevisionHistoryLimit = 99
		*dep.Spec.ProgressDeadlineSeconds = 42
	})

	dep, ok := (*second[0]).(*appsv1.Deployment)
	if !ok {
		t.Fatalf("second render's first object is %T, want *appsv1.Deployment", *second[0])
	}
	if dep.Spec.Strategy.RollingUpdate == nil || dep.Spec.Strategy.RollingUpdate.MaxUnavailable == nil {
		t.Fatal("strategy.rollingUpdate.maxUnavailable is gone — the first render's edit leaked back into the config")
	}
	if got := dep.Spec.Strategy.RollingUpdate.MaxUnavailable.StrVal; got != "25%" {
		t.Errorf("strategy.rollingUpdate.maxUnavailable = %q, want \"25%%\"", got)
	}
	if dep.Spec.RevisionHistoryLimit == nil {
		t.Fatal("revisionHistoryLimit is gone — the first render's edit leaked back into the config")
	}
	if got := *dep.Spec.RevisionHistoryLimit; got != 4 {
		t.Errorf("revisionHistoryLimit = %d, want 4", got)
	}
	if dep.Spec.ProgressDeadlineSeconds == nil {
		t.Fatal("progressDeadlineSeconds is gone — the first render's edit leaked back into the config")
	}
	if got := *dep.Spec.ProgressDeadlineSeconds; got != 700 {
		t.Errorf("progressDeadlineSeconds = %d, want 700", got)
	}
}

// TestJob_RenderingTwiceIsUnaffectedByEditingTheFirstRender is the same property
// on the kind added in go-kure/launcher#344. applyJobSpec projects ten scalar
// pointers and one struct pointer whose value owns a slice, so it is the same
// shape as the others even though nothing it projects is a map.
// TestApplyJobSpec_CopiesRatherThanAliases checks every field at the pointer
// level; this one checks the property a consumer actually meets, through the
// rendering path, the way the statefulset, daemonset and deployment cases do.
func TestJob_RenderingTwiceIsUnaffectedByEditingTheFirstRender(t *testing.T) {
	props := map[string]any{
		"image":          "ghcr.io/org/app:v1",
		"backoffLimit":   4,
		"completionMode": "Indexed",
		"completions":    6,
		"successPolicy": map[string]any{
			"rules": []any{map[string]any{"succeededCount": 3}},
		},
	}

	second := renderTwice(t, &components.JobHandler{}, "job", props, func(objects []*client.Object) {
		job, ok := (*objects[0]).(*batchv1.Job)
		if !ok {
			t.Fatalf("first object is %T, want *batchv1.Job", *objects[0])
		}
		// Checked rather than dereferenced blind, for the reason spelled out in
		// the deployment case above: a regression that leaves one of these
		// unset is what this test exists to catch, and writing through it
		// would panic instead of failing by name.
		if job.Spec.BackoffLimit == nil {
			t.Fatal("first render has no backoffLimit — nothing to alias, so this test cannot prove anything")
		}
		if job.Spec.SuccessPolicy == nil || len(job.Spec.SuccessPolicy.Rules) != 1 || job.Spec.SuccessPolicy.Rules[0].SucceededCount == nil {
			t.Fatal("first render has no successPolicy.rules[0].succeededCount — nothing to alias, so this test cannot prove anything")
		}
		*job.Spec.BackoffLimit = 99
		*job.Spec.SuccessPolicy.Rules[0].SucceededCount = 99
	})

	job, ok := (*second[0]).(*batchv1.Job)
	if !ok {
		t.Fatalf("second render's first object is %T, want *batchv1.Job", *second[0])
	}
	if got := *job.Spec.BackoffLimit; got != 4 {
		t.Errorf("backoffLimit = %d, want 4", got)
	}
	if job.Spec.SuccessPolicy == nil || len(job.Spec.SuccessPolicy.Rules) != 1 {
		t.Fatalf("successPolicy.rules = %v, want one rule", job.Spec.SuccessPolicy)
	}
	if got := *job.Spec.SuccessPolicy.Rules[0].SucceededCount; got != 3 {
		t.Errorf("successPolicy.rules[0].succeededCount = %d, want 3", got)
	}
}

func ptrInt32(n int32) *int32 { return &n }
