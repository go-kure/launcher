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

func TestCronjobHandler_HistoryLimit_Overflow_Rejected(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                      "ghcr.io/org/job:v1.0.0",
			"schedule":                   "0 2 * * *",
			"successfulJobsHistoryLimit": 3_000_000_000,
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for out-of-int32-range successfulJobsHistoryLimit")
	}
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

func TestCronjobHandler_WithSharedPodFields(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":      "ghcr.io/org/job:v1.0.0",
			"schedule":   "0 2 * * *",
			"workingDir": "/job",
			"envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "job-cfg"}},
			},
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"exec": map[string]any{"command": []any{"/bin/sh", "-c", "cleanup"}},
				},
			},
			"securityContext": map[string]any{
				"readOnlyRootFilesystem": true,
			},
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
			c := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
			if c.WorkingDir != "/job" {
				t.Errorf("expected workingDir, got %q", c.WorkingDir)
			}
			if len(c.EnvFrom) != 1 {
				t.Errorf("expected 1 envFrom entry, got %d", len(c.EnvFrom))
			}
			if c.Lifecycle == nil || c.Lifecycle.PreStop == nil {
				t.Error("expected preStop lifecycle hook")
			}
			if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
				t.Error("expected readOnlyRootFilesystem=true")
			}
			return
		}
	}
	t.Error("CronJob not found")
}

// TestCronjobHandler_NamedLifecyclePort_Error covers launcher#278 wave-11
// finding 5 — the literal example the finding cited: cronjob has no `port`
// property at all, so a named lifecycle httpGet port (`port: http`) can
// never resolve against the main container and is rejected at parse time.
// See TestWorkerHandler_NamedLifecyclePort_Error for the fuller set of cases
// (probes, numeric-still-accepted) — cronjob shares the identical portless
// shape and the same shared parsing path, so it is not repeated here.
func TestCronjobHandler_NamedLifecyclePort_Error(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/shutdown"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named lifecycle port on a portless cronjob")
	}
}

// TestCronjobHandler_WorkingDir_NonString_Error covers a bug pattern shared
// identically across all five kind handlers (cronjob, statefulset, worker,
// daemonset, webservice): a mistyped workingDir value was previously
// silently treated as absent instead of rejected. Exercised once here since
// the fix is the same one-line change (routed through parseStringField) in
// every handler.
func TestCronjobHandler_WorkingDir_NonString_Error(t *testing.T) {
	h := &components.CronjobHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":      "ghcr.io/org/job:v1.0.0",
			"schedule":   "0 2 * * *",
			"workingDir": 123,
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for non-string workingDir, got nil")
	}
}

func TestCronjobHandler_WithProbes(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"probes": map[string]any{
				"liveness": map[string]any{
					"exec": map[string]any{"command": []any{"/bin/sh", "-c", "true"}},
				},
			},
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
			c := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
			if c.LivenessProbe == nil || c.LivenessProbe.Exec == nil {
				t.Error("expected liveness probe with exec handler")
			}
			return
		}
	}
	t.Error("CronJob not found")
}

func TestCronjobConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"securityContext": map[string]any{
				"privileged": true,
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when privileged=true and policy disallows it")
	}
}

// TestCronjobConfig_ApplyPolicy_HostPathDenied regression-tests the review
// finding this fix was anchored at (launcher#284, P1): ApplyPolicy never
// checked a parsed hostPath volume against oam.Policy.AllowHostPathVolumes(),
// so the default-deny policy (including NoopPolicy) did not actually stop a
// hostPath volume from being authored on a CronJob.
func TestCronjobConfig_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"volumes": []any{
				map[string]any{
					"name":      "logs",
					"type":      "hostPath",
					"mountPath": "/var/log",
					"path":      "/var/log/app",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowHostPathVols: false}); err == nil {
		t.Error("expected error when a hostPath volume is authored and policy disallows it")
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowHostPathVols: true}); err != nil {
		t.Errorf("expected no error when policy allows hostPath volumes, got %v", err)
	}
	// oam.NoopPolicy is the real default-deny policy (not just the test
	// stub) — confirm the fix is actually reachable through it, not only
	// through a test double that happens to agree with the real default.
	if err := enforceable.ApplyPolicy(&oam.NoopPolicy{}); err == nil {
		t.Error("expected error from the real NoopPolicy default-deny for hostPath volumes")
	}
}

func TestCronjobHandler_WithVolumes_EmptyDir(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"volumes": []any{
				map[string]any{
					"name":      "tmp",
					"type":      "emptyDir",
					"mountPath": "/tmp",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// TestCronjobHandler_FileKeyRef_VolumeWiring regression-tests a review finding
// (launcher#284): the cronjob handler exposed env[].valueFrom.fileKeyRef
// (shared via schemaEnv) without any way to declare the emptyDir volume its
// volumeName must reference, so real Kubernetes admission's
// validateFileKeyRefVolumes would reject every authored fileKeyRef with a
// NotFound on volumeName. Confirms the volume the ref points at is actually
// present in the generated CronJob's pod template.
func TestCronjobHandler_FileKeyRef_VolumeWiring(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"volumes": []any{
				map[string]any{
					"name":      "envfiles",
					"type":      "emptyDir",
					"mountPath": "/etc/envfiles",
				},
			},
			"env": []any{
				map[string]any{
					"name": "API_KEY",
					"valueFrom": map[string]any{
						"fileKeyRef": map[string]any{
							"volumeName": "envfiles",
							"path":       "api.env",
							"key":        "API_KEY",
						},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objs {
		cj, ok := (*obj).(*batchv1.CronJob)
		if !ok {
			continue
		}
		spec := cj.Spec.JobTemplate.Spec.Template.Spec
		found := false
		for _, v := range spec.Volumes {
			if v.Name == "envfiles" {
				if v.EmptyDir == nil {
					t.Error("expected envfiles volume to be emptyDir")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("expected pod spec to declare the envfiles volume referenced by fileKeyRef")
		}
		return
	}
	t.Error("CronJob not found")
}

// TestCronjobHandler_PVC_NamespacedByComponent regression-tests a review
// finding (launcher#284): the generated PersistentVolumeClaim object used
// the bare pod-local volume name verbatim, so two components in the same
// namespace both authoring a "data" volume would emit two
// PersistentVolumeClaim/data objects and collide. The PVC object's own name
// must be qualified by the component/application name; the pod-local Volume
// name and its VolumeMount reference must stay unqualified.
func TestCronjobHandler_PVC_NamespacedByComponent(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"volumes": []any{
				map[string]any{
					"name":      "data",
					"type":      "pvc",
					"mountPath": "/data",
					"size":      "1Gi",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var sawPVC, sawVolume, sawMount bool
	for _, obj := range objs {
		switch o := (*obj).(type) {
		case *corev1.PersistentVolumeClaim:
			if o.Name != "job-data" {
				t.Errorf("PVC name = %q, want %q", o.Name, "job-data")
			}
			sawPVC = true
		case *batchv1.CronJob:
			spec := o.Spec.JobTemplate.Spec.Template.Spec
			for _, v := range spec.Volumes {
				if v.Name != "data" {
					continue
				}
				sawVolume = true
				if v.PersistentVolumeClaim == nil {
					t.Fatal("expected volume \"data\" to reference a PersistentVolumeClaim")
				}
				if v.PersistentVolumeClaim.ClaimName != "job-data" {
					t.Errorf("Volume.PersistentVolumeClaim.ClaimName = %q, want %q", v.PersistentVolumeClaim.ClaimName, "job-data")
				}
			}
			for _, c := range spec.Containers {
				for _, m := range c.VolumeMounts {
					if m.Name == "data" {
						sawMount = true
					}
				}
			}
		}
	}
	if !sawPVC {
		t.Fatal("expected a PersistentVolumeClaim object")
	}
	if !sawVolume {
		t.Fatal("expected pod spec to declare the \"data\" volume, unqualified")
	}
	if !sawMount {
		t.Fatal("expected container to mount the \"data\" volume, unqualified")
	}
}
