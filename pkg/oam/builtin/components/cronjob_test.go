package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestCronjobHandler_CanHandle(t *testing.T) {
	h := &components.CronjobHandler{}
	if !h.CanHandle("cronjob") {
		t.Error("expected true for cronjob")
	}
	if h.CanHandle("worker") {
		t.Error("expected false for worker")
	}
}

func TestCronjobHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"schedule": "0 2 * * *",
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestCronjobHandler_RequiredSchedule_Missing(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image": "ghcr.io/org/job:v1.0.0",
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing schedule")
	}
}

func TestCronjobHandler_InvalidSchedule(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "@daily",
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for non-standard cron schedule")
	}
}

func TestCronjobHandler_InvalidRestartPolicy(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":         "ghcr.io/org/job:v1.0.0",
			"schedule":      "0 2 * * *",
			"restartPolicy": "Always",
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid restart policy")
	}
}

func TestCronjobHandler_Generate_ResourceTypes(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "cleanup",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/cleanup:v1.0.0",
			"schedule": "0 2 * * *",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("cleanup", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundCronJob, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *batchv1.CronJob:
			foundCronJob = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundCronJob {
		t.Error("expected CronJob")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestCronjobHandler_Generate_Defaults(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "cleanup",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/cleanup:v1.0.0",
			"schedule": "0 2 * * *",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("cleanup", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if cj, ok := (*obj).(*batchv1.CronJob); ok {
			if cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
				t.Errorf("expected OnFailure restart policy, got %s", cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy)
			}
			if cj.Spec.SuccessfulJobsHistoryLimit == nil || *cj.Spec.SuccessfulJobsHistoryLimit != 3 {
				t.Errorf("expected successfulJobsHistoryLimit=3, got %v", cj.Spec.SuccessfulJobsHistoryLimit)
			}
			if cj.Spec.FailedJobsHistoryLimit == nil || *cj.Spec.FailedJobsHistoryLimit != 1 {
				t.Errorf("expected failedJobsHistoryLimit=1, got %v", cj.Spec.FailedJobsHistoryLimit)
			}
			return
		}
	}
	t.Error("CronJob not found")
}

func TestCronjobHandler_RestartPolicy_Never(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":         "ghcr.io/org/job:v1.0.0",
			"schedule":      "0 2 * * *",
			"restartPolicy": "Never",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("job", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if cj, ok := (*obj).(*batchv1.CronJob); ok {
			if cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
				t.Errorf("expected Never restart policy, got %s", cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy)
			}
			return
		}
	}
	t.Error("CronJob not found")
}

func TestCronjobHandler_HistoryLimit_Fractional_Rejected(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                      "ghcr.io/org/job:v1.0.0",
			"schedule":                   "0 2 * * *",
			"successfulJobsHistoryLimit": 1.5,
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for fractional successfulJobsHistoryLimit")
	}
}

func TestCronjobHandler_HistoryLimit_Negative_Rejected(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                  "ghcr.io/org/job:v1.0.0",
			"schedule":               "0 2 * * *",
			"failedJobsHistoryLimit": -1,
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for negative failedJobsHistoryLimit")
	}
}

func TestCronjobHandler_HistoryLimit_Custom_Valid(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                      "ghcr.io/org/job:v1.0.0",
			"schedule":                   "0 2 * * *",
			"successfulJobsHistoryLimit": 5,
			"failedJobsHistoryLimit":     2,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("job", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if cj, ok := (*obj).(*batchv1.CronJob); ok {
			if cj.Spec.SuccessfulJobsHistoryLimit == nil || *cj.Spec.SuccessfulJobsHistoryLimit != 5 {
				t.Errorf("expected successfulJobsHistoryLimit=5, got %v", cj.Spec.SuccessfulJobsHistoryLimit)
			}
			if cj.Spec.FailedJobsHistoryLimit == nil || *cj.Spec.FailedJobsHistoryLimit != 2 {
				t.Errorf("expected failedJobsHistoryLimit=2, got %v", cj.Spec.FailedJobsHistoryLimit)
			}
			return
		}
	}
	t.Error("CronJob not found")
}

func TestCronjobConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy should be a no-op, got: %v", err)
	}
}

func TestCronjobConfig_ApplyPolicy_AllowedRegistries(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "docker.io/library/job:v1.0.0",
			"schedule": "0 2 * * *",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{allowedRegistries: []string{"ghcr.io"}}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error for disallowed registry")
	}
}
