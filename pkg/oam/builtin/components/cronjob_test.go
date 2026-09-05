package components_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// findCronJob returns the *batchv1.CronJob from objs, failing the test if none
// is present. Shared by the JobSpecConfig / CronJobSpec-level projection tests
// below to avoid repeating the same objects loop for each one.
func findCronJob(t *testing.T, objs []*client.Object) *batchv1.CronJob {
	t.Helper()
	for _, obj := range objs {
		if cj, ok := (*obj).(*batchv1.CronJob); ok {
			return cj
		}
	}
	t.Fatal("CronJob not found")
	return nil
}

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

// TestCronjobHandler_Schedule_Table covers the widened schedule grammar
// (validateCronSchedule, cronjob.go): the pre-existing 5-field form (still
// accepted, including the two nonsensical-but-syntactically-5-field cases the
// plan's "What this does NOT solve" section discloses as a deliberate, not
// tightened, gap — see plan-279-cronjob-jobspec.md), the fixed @-descriptor
// set, and "@every <duration>" with the duration validated via
// time.ParseDuration rather than a bare-suffix regex.
func TestCronjobHandler_Schedule_Table(t *testing.T) {
	cases := []struct {
		name      string
		schedule  string
		wantError bool
	}{
		{"5field_standard", "0 2 * * *", false},
		{"5field_nonsensical_out_of_range_still_accepted", "99 99 99 99 99", false},
		{"5field_nonsensical_tokens_still_accepted", "nope nope nope nope nope", false},
		{"descriptor_daily", "@daily", false},
		{"descriptor_hourly", "@hourly", false},
		{"descriptor_yearly", "@yearly", false},
		{"every_valid_duration", "@every 1h30m", false},
		{"reboot_rejected", "@reboot", true},
		{"6field_rejected", "0 0 2 * * *", true},
		{"garbage_rejected", "not a schedule", true},
		{"empty_rejected", "", true},
		{"every_malformed_duration_rejected", "@every nope", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.CronjobHandler{}
			_, err := h.ToApplicationConfig(&oam.Component{
				Name: "job",
				Type: "cronjob",
				Properties: map[string]any{
					"image":    "ghcr.io/org/job:v1.0.0",
					"schedule": tc.schedule,
				},
			}, "default")
			if tc.wantError && err == nil {
				t.Fatalf("schedule %q: expected error, got nil", tc.schedule)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("schedule %q: expected no error, got %v", tc.schedule, err)
			}
		})
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
// identically across all seven kind handlers (cronjob, job, statefulset,
// worker, daemonset, webservice, deployment): a mistyped workingDir value was previously
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

// TestCronjobConfig_ApplyPolicy_CapabilityAddDenied is cronjob's sibling of
// TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied (go-kure/launcher#305)
// — the same shared ApplyPolicy gap, same shared enforceContainerCapabilities
// fix. Also asserts against the real oam.NoopPolicy that capabilities.add
// passes with no policy configured — the mirror image of NoopPolicy's
// default-deny for privileged/hostPath, and the assertion that encodes this
// field's default-allow decision.
func TestCronjobConfig_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"securityContext": map[string]any{
				"capabilities": map[string]any{
					"add": []any{"NET_ADMIN"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when capabilities.add includes a forbidden capability")
	}
	// oam.NoopPolicy is default-allow for container capabilities (unlike its
	// default-deny for privileged/hostPath) — confirm the real NoopPolicy
	// does not reject an authored capability with no policy configured.
	if err := enforceable.ApplyPolicy(&oam.NoopPolicy{}); err != nil {
		t.Errorf("expected no error from the real NoopPolicy default-allow for container capabilities, got %v", err)
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

// TestCronjobConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault is
// cronjob's sibling of the two webservice
// TestWebserviceConfig_ApplyPolicy_Max{CPU,Memory}_AgainstIntrinsicDefault
// cases (launcher#251) — proving enforceMaxResources is actually wired into
// CronjobConfig.ApplyPolicy, not just added to enforce.go.
func TestCronjobConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault(t *testing.T) {
	cases := []struct {
		name   string
		policy stubPolicy
	}{
		{"cpu", stubPolicy{maxCPU: "50m"}},
		{"memory", stubPolicy{maxMemory: "64Mi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			err = enforceable.ApplyPolicy(&tc.policy)
			if err == nil {
				t.Fatal("expected error when the intrinsic default exceeds the enforced maximum")
			}
			if !strings.Contains(err.Error(), "generated default") {
				t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
			}
		})
	}
}

// The four tests below are cronjob's siblings of the
// TestWebserviceConfig_ApplyPolicy_InitContainer* tests (go-kure/launcher#312)
// — same shared ApplyPolicy gap, same enforceExtraContainer fix. cronjob has
// no sidecars schema key (see the field-coverage note in the plan), so only
// the init container loop applies here.

func TestCronjobConfig_ApplyPolicy_InitContainerResourcesDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":     "ghcr.io/org/job:v1.0.0",
			"schedule":  "0 2 * * *",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "ghcr.io/org/init:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the init container's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestCronjobConfig_ApplyPolicy_InitContainerRegistryDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":     "ghcr.io/org/job:v1.0.0",
			"schedule":  "0 2 * * *",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{"name": "init", "image": "docker.io/x/y:v1"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{allowedRegistries: []string{"ghcr.io"}})
	if err == nil {
		t.Fatal("expected error when the init container's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestCronjobConfig_ApplyPolicy_InitContainerPrivilegedDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":     "ghcr.io/org/job:v1.0.0",
			"schedule":  "0 2 * * *",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{"privileged": true},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when the init container is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestCronjobConfig_ApplyPolicy_InitContainerCapabilitiesDenied(t *testing.T) {
	h := &components.CronjobHandler{}
	component := &oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":     "ghcr.io/org/job:v1.0.0",
			"schedule":  "0 2 * * *",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name": "init", "image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{
						"capabilities": map[string]any{"add": []any{"NET_ADMIN"}},
					},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{forbiddenContainerCaps: []string{"NET_ADMIN"}}); err == nil {
		t.Error("expected error when the init container adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

// --- go-kure/launcher#279 PR 1: CronJobSpec/JobSpec completion ---

func TestCronjobHandler_ConcurrencyPolicy_Projected(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":             "ghcr.io/org/job:v1.0.0",
			"schedule":          "0 2 * * *",
			"concurrencyPolicy": "Forbid",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent {
		t.Errorf("ConcurrencyPolicy = %q, want %q", cj.Spec.ConcurrencyPolicy, batchv1.ForbidConcurrent)
	}
}

func TestCronjobHandler_Suspend_Projected(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"suspend":  true,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.Suspend == nil || !*cj.Spec.Suspend {
		t.Errorf("Suspend = %v, want true", cj.Spec.Suspend)
	}
}

// TestCronjobHandler_SuspendWritesCronJobSpecOnly guards the one deliberate
// asymmetry between the job and cronjob components. batchv1 carries two
// distinct Suspend fields — CronJobSpec.Suspend (suspends future executions of
// the schedule) and JobSpec.Suspend (creates a job with no pods) — and both
// components publish a key named `suspend`. The cronjob component's is the
// CronJobSpec one, so it must not reach the job template.
//
// Sharing `suspend` through schemaJobSpec/parseJobSpec would be the natural
// thing to do when the two components' shared surface is next widened, and it
// would silently write one authored value into two API fields. This test fails
// the moment that happens; TestSchemaJobSpec_HasNoSuspendKey is its schema-side
// half, and TestJobHandler_SuspendWritesJobSpec pins the job component's own.
func TestCronjobHandler_SuspendWritesCronJobSpecOnly(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"suspend":  true,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.Suspend == nil || !*cj.Spec.Suspend {
		t.Fatalf("Spec.Suspend = %v, want true", cj.Spec.Suspend)
	}
	if got := cj.Spec.JobTemplate.Spec.Suspend; got != nil {
		t.Errorf("Spec.JobTemplate.Spec.Suspend = %v, want nil — `suspend` on a cronjob is CronJobSpec.Suspend, not JobSpec.Suspend", got)
	}
}

func TestCronjobHandler_StartingDeadlineSeconds_Projected(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                   "ghcr.io/org/job:v1.0.0",
			"schedule":                "0 2 * * *",
			"startingDeadlineSeconds": 120,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.StartingDeadlineSeconds == nil || *cj.Spec.StartingDeadlineSeconds != 120 {
		t.Errorf("StartingDeadlineSeconds = %v, want 120", cj.Spec.StartingDeadlineSeconds)
	}
}

func TestCronjobHandler_TimeZone_Projected(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":    "ghcr.io/org/job:v1.0.0",
			"schedule": "0 2 * * *",
			"timeZone": "Europe/Brussels",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.TimeZone == nil || *cj.Spec.TimeZone != "Europe/Brussels" {
		t.Errorf("TimeZone = %v, want \"Europe/Brussels\"", cj.Spec.TimeZone)
	}
}

// TestCronjobHandler_JobSpec_Projected asserts all six JobSpec fields
// individually (rather than diffing the whole batchv1.JobSpec, which also
// carries Template and would need a much larger literal) — per the plan's
// oracle guidance, deleting any one of applyJobSpec's six assignments must
// fail this test, so each field gets its own explicit assertion.
func TestCronjobHandler_JobSpec_Projected(t *testing.T) {
	h := &components.CronjobHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "job",
		Type: "cronjob",
		Properties: map[string]any{
			"image":                   "ghcr.io/org/job:v1.0.0",
			"schedule":                "0 2 * * *",
			"backoffLimit":            4,
			"completions":             3,
			"parallelism":             2,
			"activeDeadlineSeconds":   600,
			"ttlSecondsAfterFinished": 3600,
			"completionMode":          "Indexed",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	spec := findCronJob(t, objs).Spec.JobTemplate.Spec
	if spec.BackoffLimit == nil || *spec.BackoffLimit != 4 {
		t.Errorf("BackoffLimit = %v, want 4", spec.BackoffLimit)
	}
	if spec.Completions == nil || *spec.Completions != 3 {
		t.Errorf("Completions = %v, want 3", spec.Completions)
	}
	if spec.Parallelism == nil || *spec.Parallelism != 2 {
		t.Errorf("Parallelism = %v, want 2", spec.Parallelism)
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != 600 {
		t.Errorf("ActiveDeadlineSeconds = %v, want 600", spec.ActiveDeadlineSeconds)
	}
	if spec.TTLSecondsAfterFinished == nil || *spec.TTLSecondsAfterFinished != 3600 {
		t.Errorf("TTLSecondsAfterFinished = %v, want 3600", spec.TTLSecondsAfterFinished)
	}
	if spec.CompletionMode == nil || *spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("CompletionMode = %v, want %q", spec.CompletionMode, batchv1.IndexedCompletion)
	}
}

// TestCronjobHandler_OptionalSpecFields_Unset_NotEmitted protects against the
// "suspend: false"-on-every-CronJob regression class: every optional field
// below must stay nil/empty when unauthored. Oracle: temporarily make one
// guarded setter in createCronJob unconditional and confirm this test fails.
func TestCronjobHandler_OptionalSpecFields_Unset_NotEmitted(t *testing.T) {
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
	app := stack.NewApplication("job", "default", cfg)
	objs, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cj := findCronJob(t, objs)
	if cj.Spec.Suspend != nil {
		t.Errorf("Suspend = %v, want nil", cj.Spec.Suspend)
	}
	if cj.Spec.StartingDeadlineSeconds != nil {
		t.Errorf("StartingDeadlineSeconds = %v, want nil", cj.Spec.StartingDeadlineSeconds)
	}
	if cj.Spec.TimeZone != nil {
		t.Errorf("TimeZone = %v, want nil", cj.Spec.TimeZone)
	}
	if cj.Spec.ConcurrencyPolicy != "" {
		t.Errorf("ConcurrencyPolicy = %q, want empty", cj.Spec.ConcurrencyPolicy)
	}
	spec := cj.Spec.JobTemplate.Spec
	if spec.BackoffLimit != nil {
		t.Errorf("BackoffLimit = %v, want nil", spec.BackoffLimit)
	}
	if spec.Completions != nil {
		t.Errorf("Completions = %v, want nil", spec.Completions)
	}
	if spec.Parallelism != nil {
		t.Errorf("Parallelism = %v, want nil", spec.Parallelism)
	}
	if spec.ActiveDeadlineSeconds != nil {
		t.Errorf("ActiveDeadlineSeconds = %v, want nil", spec.ActiveDeadlineSeconds)
	}
	if spec.TTLSecondsAfterFinished != nil {
		t.Errorf("TTLSecondsAfterFinished = %v, want nil", spec.TTLSecondsAfterFinished)
	}
	if spec.CompletionMode != nil {
		t.Errorf("CompletionMode = %v, want nil", spec.CompletionMode)
	}
}

// TestCronjobHandler_CronSpecAndJobSpec_Rejections covers the per-field bound
// checks and the cross-field/boundary cases the per-field checks alone would
// not catch (activeDeadlineSeconds==0, completionMode Indexed without
// completions, completionMode Indexed with parallelism > 100000) plus the
// timeZone guards (empty string, non-IANA name, case-insensitive "Local").
// Each case asserts the field name appears in the error (strings.Contains),
// not just err != nil, so a case rejected for the wrong reason still fails.
func TestCronjobHandler_CronSpecAndJobSpec_Rejections(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		wantSubstr string
	}{
		{"backoffLimit_negative", map[string]any{"backoffLimit": -1}, "backoffLimit"},
		{"completions_negative", map[string]any{"completions": -1}, "completions"},
		{"parallelism_negative", map[string]any{"parallelism": -1}, "parallelism"},
		{"activeDeadlineSeconds_zero_rejected", map[string]any{"activeDeadlineSeconds": 0}, "activeDeadlineSeconds"},
		{"activeDeadlineSeconds_negative", map[string]any{"activeDeadlineSeconds": -5}, "activeDeadlineSeconds"},
		{"ttlSecondsAfterFinished_negative", map[string]any{"ttlSecondsAfterFinished": -1}, "ttlSecondsAfterFinished"},
		{"completionMode_invalid_enum", map[string]any{"completionMode": "Bogus"}, "completionMode"},
		{"completionMode_indexed_without_completions", map[string]any{"completionMode": "Indexed"}, "completionMode"},
		{"completionMode_indexed_parallelism_over_max", map[string]any{"completionMode": "Indexed", "completions": 1, "parallelism": 100001}, "parallelism"},
		{"concurrencyPolicy_invalid_enum", map[string]any{"concurrencyPolicy": "Bogus"}, "concurrencyPolicy"},
		{"suspend_non_bool", map[string]any{"suspend": "yes"}, "suspend"},
		{"startingDeadlineSeconds_negative", map[string]any{"startingDeadlineSeconds": -1}, "startingDeadlineSeconds"},
		{"timeZone_empty_string_rejected", map[string]any{"timeZone": ""}, "timeZone"},
		{"timeZone_not_a_real_zone_rejected", map[string]any{"timeZone": "Not/AZone"}, "timeZone"},
		{"timeZone_local_rejected", map[string]any{"timeZone": "Local"}, "timeZone"},
		{"timeZone_local_lowercase_rejected", map[string]any{"timeZone": "local"}, "timeZone"},
		{"timeZone_non_string_rejected", map[string]any{"timeZone": 5}, "timeZone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{
				"image":    "ghcr.io/org/job:v1.0.0",
				"schedule": "0 2 * * *",
			}
			for k, v := range tc.props {
				props[k] = v
			}
			h := &components.CronjobHandler{}
			_, err := h.ToApplicationConfig(&oam.Component{
				Name: "job", Type: "cronjob", Properties: props,
			}, "default")
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("expected error to contain %q, got %q", tc.wantSubstr, err.Error())
			}
		})
	}
}

// TestCronjobHandler_PropertySchema_JobSpecAndCronSpecKeys_Present is the
// round-7 schema-shape completeness gate (see plan-279-cronjob-jobspec.md
// "Tests" item 5): PropertySchema() is a published contract for an
// out-of-process validator (property_validate.go:15) that none of the tests
// above exercise directly, so a forgotten maps.Copy call or a mistyped
// Enum/Default would pass every other test in this file silently. Asserts the
// full map's cardinality so an omitted key is caught, and each JobSpec- and
// CronJobSpec-level key's Type/Enum/Default/Description against a literal so a
// mistyped one is too.
// Oracle: delete one key from schemaJobSpec (or its maps.Copy call in
// PropertySchema()) and confirm this test fails.
func TestCronjobHandler_PropertySchema_JobSpecAndCronSpecKeys_Present(t *testing.T) {
	h := &components.CronjobHandler{}
	schema := h.PropertySchema()

	// 20 cronjob-own keys, the 11 JobSpec-level keys from schemaJobSpec (six
	// from the original cronjob work, five added with the job component in
	// go-kure/launcher#344), and the 31 shared pod-level keys from
	// schemaPodSpec (podActiveDeadlineSeconds included: cronjob pods are Job
	// pods).
	const wantTotalKeys = 62
	if len(schema) != wantTotalKeys {
		t.Fatalf("PropertySchema() returned %d keys, want %d", len(schema), wantTotalKeys)
	}

	type want struct {
		typ  oam.PropertyType
		enum []any
		def  any
	}
	cases := map[string]want{
		"concurrencyPolicy":       {typ: oam.PropertyTypeString, enum: []any{"Allow", "Forbid", "Replace"}, def: "Allow"},
		"suspend":                 {typ: oam.PropertyTypeBoolean},
		"startingDeadlineSeconds": {typ: oam.PropertyTypeInteger},
		"timeZone":                {typ: oam.PropertyTypeString},
		"backoffLimit":            {typ: oam.PropertyTypeInteger},
		"completions":             {typ: oam.PropertyTypeInteger},
		"parallelism":             {typ: oam.PropertyTypeInteger},
		"activeDeadlineSeconds":   {typ: oam.PropertyTypeInteger},
		"ttlSecondsAfterFinished": {typ: oam.PropertyTypeInteger},
		"completionMode":          {typ: oam.PropertyTypeString, enum: []any{"NonIndexed", "Indexed"}},
		"backoffLimitPerIndex":    {typ: oam.PropertyTypeInteger},
		"maxFailedIndexes":        {typ: oam.PropertyTypeInteger},
		"podReplacementPolicy":    {typ: oam.PropertyTypeString, enum: []any{"Failed", "TerminatingOrFailed"}},
		"managedBy":               {typ: oam.PropertyTypeString},
		"successPolicy":           {typ: oam.PropertyTypeObject},
	}

	for key, w := range cases {
		node, ok := schema[key]
		if !ok {
			t.Errorf("PropertySchema() missing key %q", key)
			continue
		}
		if node.Type != w.typ {
			t.Errorf("%s: Type = %q, want %q", key, node.Type, w.typ)
		}
		if node.Description == "" {
			t.Errorf("%s: Description is empty", key)
		}
		if len(w.enum) > 0 && !slices.Equal(node.Enum, w.enum) {
			t.Errorf("%s: Enum = %v, want %v", key, node.Enum, w.enum)
		}
		if w.def != nil && node.Default != w.def {
			t.Errorf("%s: Default = %v, want %v", key, node.Default, w.def)
		}
	}
}
