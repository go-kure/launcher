package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func deploymentConfig(t *testing.T, name string, props map[string]any) stack.ApplicationConfig {
	t.Helper()
	h := &components.DeploymentHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       name,
		Type:       "deployment",
		Properties: props,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return cfg
}

func generateDeployment(t *testing.T, name string, props map[string]any) (*appsv1.Deployment, []*client.Object) {
	t.Helper()
	cfg := deploymentConfig(t, name, props)
	objects, err := cfg.Generate(stack.NewApplication(name, "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var dep *appsv1.Deployment
	for _, obj := range objects {
		if d, ok := (*obj).(*appsv1.Deployment); ok {
			dep = d
		}
	}
	if dep == nil {
		t.Fatal("Generate produced no Deployment")
	}
	return dep, objects
}

func TestDeploymentHandler_CanHandle(t *testing.T) {
	h := &components.DeploymentHandler{}
	if !h.CanHandle("deployment") {
		t.Error("expected true for deployment")
	}
	for _, other := range []string{"worker", "webservice", "statefulset", "daemonset", ""} {
		if h.CanHandle(other) {
			t.Errorf("expected false for %q", other)
		}
	}
}

func TestDeploymentHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.DeploymentHandler{}
	if _, err := h.ToApplicationConfig(&oam.Component{
		Name:       "app",
		Type:       "deployment",
		Properties: map[string]any{},
	}, "default"); err == nil {
		t.Fatal("expected error for missing image")
	}
}

// TestDeploymentHandler_Generate_NoService pins the deliberate omission: this
// kind has no `port` property, so unlike webservice it never emits a Service.
func TestDeploymentHandler_Generate_NoService(t *testing.T) {
	_, objects := generateDeployment(t, "backend", map[string]any{
		"image": "ghcr.io/org/backend:v1.0.0",
	})

	var foundService, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *corev1.Service:
			foundService = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if foundService {
		t.Error("deployment must not generate a Service — it publishes no port property")
	}
	if !foundSA {
		t.Error("expected a ServiceAccount")
	}
}

// TestDeploymentHandler_LabelMapsAreNotShared is the guard against the defect
// class being fixed on the sibling kinds: one `map[string]string`
// literal reused by the object metadata, the pod template and the selector.
// Sharing is invisible until something mutates one of them — a trait adding a
// label to object metadata — at which point the selector changes too, and a
// Deployment's selector is immutable once created, so the object becomes
// unpatchable and orphans its ReplicaSets. Mutating one map here must leave
// every other consumer untouched.
func TestDeploymentHandler_LabelMapsAreNotShared(t *testing.T) {
	dep, objects := generateDeployment(t, "app", map[string]any{
		"image": "ghcr.io/org/app:v1",
		"volumes": []any{
			map[string]any{
				"name":        "data",
				"type":        "pvc",
				"mountPath":   "/data",
				"size":        "1Gi",
				"accessModes": []any{"ReadWriteMany"},
			},
		},
	})

	var sa *corev1.ServiceAccount
	var pvc *corev1.PersistentVolumeClaim
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *corev1.ServiceAccount:
			sa = o
		case *corev1.PersistentVolumeClaim:
			pvc = o
		}
	}
	if sa == nil || pvc == nil {
		t.Fatalf("expected a ServiceAccount and a PVC alongside the Deployment, got sa=%v pvc=%v", sa != nil, pvc != nil)
	}
	if dep.Spec.Selector == nil {
		t.Fatal("Deployment has no selector")
	}

	// Named rather than iterated so a failure says which consumer aliased.
	consumers := []struct {
		name string
		m    map[string]string
	}{
		{"deployment.metadata.labels", dep.Labels},
		{"deployment.spec.selector.matchLabels", dep.Spec.Selector.MatchLabels},
		{"deployment.spec.template.metadata.labels", dep.Spec.Template.Labels},
		{"serviceaccount.metadata.labels", sa.Labels},
		{"pvc.metadata.labels", pvc.Labels},
	}
	// t.Fatalf, not t.Errorf: the aliasing loop below writes into every one of
	// these maps, and a nil map there panics with "assignment to entry in nil
	// map" — aborting the binary instead of reporting the failure this loop
	// already detected.
	for _, c := range consumers {
		if c.m == nil {
			t.Fatalf("%s has no label map", c.name)
		}
		if c.m["app"] != "app" {
			t.Fatalf("%s = %v, want app=app", c.name, c.m)
		}
	}

	for i, mutated := range consumers {
		mutated.m["probe.example.com/canary"] = "1"
		for j, other := range consumers {
			if i == j {
				continue
			}
			if _, aliased := other.m["probe.example.com/canary"]; aliased {
				t.Errorf("%s and %s are the same map: writing to one changed the other", mutated.name, other.name)
			}
		}
		delete(mutated.m, "probe.example.com/canary")
	}
}

// TestDeploymentHandler_DeploymentSpecFieldsAreProjected walks every
// DeploymentSpec-level property from the document to the generated object.
func TestDeploymentHandler_DeploymentSpecFieldsAreProjected(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "ghcr.io/org/app:v1",
		"strategy": map[string]any{
			"type": "RollingUpdate",
			"rollingUpdate": map[string]any{
				"maxUnavailable": "25%",
				"maxSurge":       2,
			},
		},
		"minReadySeconds":         15,
		"revisionHistoryLimit":    0,
		"paused":                  true,
		"progressDeadlineSeconds": 600,
	})

	if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want RollingUpdate", dep.Spec.Strategy.Type)
	}
	if ru := dep.Spec.Strategy.RollingUpdate; ru == nil {
		t.Error("Strategy.RollingUpdate is nil")
	} else {
		if got := ru.MaxUnavailable.String(); got != "25%" {
			t.Errorf("MaxUnavailable = %q, want \"25%%\"", got)
		}
		if got := ru.MaxSurge.String(); got != "2" {
			t.Errorf("MaxSurge = %q, want \"2\"", got)
		}
	}
	if dep.Spec.MinReadySeconds != 15 {
		t.Errorf("MinReadySeconds = %d, want 15", dep.Spec.MinReadySeconds)
	}
	// Zero is a meaningful revisionHistoryLimit (keep no old ReplicaSets), and
	// distinguishable from unset only because the field is a pointer.
	if dep.Spec.RevisionHistoryLimit == nil || *dep.Spec.RevisionHistoryLimit != 0 {
		t.Errorf("RevisionHistoryLimit = %v, want an explicit 0", dep.Spec.RevisionHistoryLimit)
	}
	if !dep.Spec.Paused {
		t.Error("Paused = false, want true")
	}
	if dep.Spec.ProgressDeadlineSeconds == nil || *dep.Spec.ProgressDeadlineSeconds != 600 {
		t.Errorf("ProgressDeadlineSeconds = %v, want 600", dep.Spec.ProgressDeadlineSeconds)
	}
}

// TestDeploymentHandler_UnauthoredSpecFieldsStayEmpty is the counterpart: a
// document that authors none of the new properties must produce the same
// Deployment the builder produced before this kind existed, so the apiserver's
// own defaults still apply rather than launcher freezing them into the
// manifest.
func TestDeploymentHandler_UnauthoredSpecFieldsStayEmpty(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image": "ghcr.io/org/app:v1",
	})

	if dep.Spec.Strategy.Type != "" || dep.Spec.Strategy.RollingUpdate != nil {
		t.Errorf("Strategy = %+v, want the zero value so apiserver defaulting applies", dep.Spec.Strategy)
	}
	if dep.Spec.MinReadySeconds != 0 {
		t.Errorf("MinReadySeconds = %d, want 0", dep.Spec.MinReadySeconds)
	}
	if dep.Spec.RevisionHistoryLimit != nil {
		t.Errorf("RevisionHistoryLimit = %v, want nil", dep.Spec.RevisionHistoryLimit)
	}
	if dep.Spec.Paused {
		t.Error("Paused = true, want false")
	}
	if dep.Spec.ProgressDeadlineSeconds != nil {
		t.Errorf("ProgressDeadlineSeconds = %v, want nil", dep.Spec.ProgressDeadlineSeconds)
	}
}

func nonRWXVolumeProps(extra map[string]any) map[string]any {
	props := map[string]any{
		"image": "ghcr.io/org/app:v1",
		"volumes": []any{
			map[string]any{
				"name":        "data",
				"type":        "pvc",
				"mountPath":   "/data",
				"size":        "1Gi",
				"accessModes": []any{"ReadWriteOnce"},
			},
		},
	}
	for k, v := range extra {
		props[k] = v
	}
	return props
}

// TestDeploymentHandler_NonRWXForcesRecreate covers the build-time guard: a
// ReadWriteOnce claim cannot be held by an old and a new pod at once, so the
// unauthored strategy becomes Recreate.
func TestDeploymentHandler_NonRWXForcesRecreate(t *testing.T) {
	dep, _ := generateDeployment(t, "app", nonRWXVolumeProps(nil))
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want Recreate for a non-RWX PVC", dep.Spec.Strategy.Type)
	}
}

// TestDeploymentHandler_NonRWXWithRecreateAuthored asserts an author who wrote
// the same thing the guard would have chosen is not treated as a conflict, and
// that the later apply does not undo it.
func TestDeploymentHandler_NonRWXWithRecreateAuthored(t *testing.T) {
	dep, _ := generateDeployment(t, "app", nonRWXVolumeProps(map[string]any{
		"strategy": map[string]any{"type": "Recreate"},
	}))
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("Strategy.Type = %q, want Recreate", dep.Spec.Strategy.Type)
	}
	if dep.Spec.Strategy.RollingUpdate != nil {
		t.Errorf("Strategy.RollingUpdate = %+v, want nil under Recreate", dep.Spec.Strategy.RollingUpdate)
	}
}

// TestDeploymentHandler_NonRWXRejectsConflictingStrategy is the difference from
// the worker kind, where the Recreate substitution is silent because there is
// no strategy property to contradict. Here the author said RollingUpdate, and
// overwriting it silently would ship something they did not write.
func TestDeploymentHandler_NonRWXRejectsConflictingStrategy(t *testing.T) {
	cfg := deploymentConfig(t, "app", nonRWXVolumeProps(map[string]any{
		"strategy": map[string]any{"type": "RollingUpdate"},
	}))
	_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err == nil {
		t.Fatal("expected an error for RollingUpdate alongside a non-RWX PVC")
	}
	if !strings.Contains(err.Error(), "strategy.type must be Recreate") {
		t.Errorf("error = %q, want it to name the conflicting strategy.type", err.Error())
	}
}

// TestDeploymentHandler_RWXCapableClaimIsNotConstrained pins the read of
// `accessModes` the guard depends on. The field is a request for a volume that
// supports *every* mode listed, so a claim naming ReadWriteMany alongside
// ReadWriteOnce binds a volume many pods can mount read-write: constraining it
// would refuse a document Kubernetes accepts and serves, and force a Recreate
// rollout on a workload that never needed one. The single-mode case below is
// the control — without it this test would pass just as well if the guard
// stopped firing altogether.
func TestDeploymentHandler_RWXCapableClaimIsNotConstrained(t *testing.T) {
	rwxProps := func(modes []any, extra map[string]any) map[string]any {
		props := map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumes": []any{
				map[string]any{
					"name":        "data",
					"type":        "pvc",
					"mountPath":   "/data",
					"size":        "1Gi",
					"accessModes": modes,
				},
			},
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}

	t.Run("a claim listing ReadWriteMany alongside ReadWriteOnce takes replicas > 1", func(t *testing.T) {
		dep, _ := generateDeployment(t, "app", rwxProps([]any{"ReadWriteOnce", "ReadWriteMany"}, map[string]any{
			"replicas": 2,
		}))
		if dep.Spec.Replicas == nil {
			t.Fatal("Spec.Replicas = nil, want a value")
		}
		if got := *dep.Spec.Replicas; got != 2 {
			t.Errorf("Replicas = %d, want 2", got)
		}
		if dep.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
			t.Error("Strategy.Type = Recreate, want the strategy left alone for an RWX-capable claim")
		}
	})

	t.Run("an authored RollingUpdate is not a conflict either", func(t *testing.T) {
		dep, _ := generateDeployment(t, "app", rwxProps([]any{"ReadWriteOnce", "ReadWriteMany"}, map[string]any{
			"strategy": map[string]any{"type": "RollingUpdate"},
		}))
		if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
			t.Errorf("Strategy.Type = %q, want RollingUpdate", dep.Spec.Strategy.Type)
		}
	})

	t.Run("ReadWriteOnce alone is still constrained", func(t *testing.T) {
		cfg := deploymentConfig(t, "app", rwxProps([]any{"ReadWriteOnce"}, map[string]any{"replicas": 2}))
		_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err == nil {
			t.Fatal("expected an error for replicas=2 alongside a ReadWriteOnce-only claim")
		}
		if !strings.Contains(err.Error(), "at most one replica") {
			t.Errorf("error = %q, want it to name the replica constraint", err.Error())
		}
	})

	t.Run("one RWX-capable claim does not excuse a second, constrained one", func(t *testing.T) {
		// The skip is per claim, not a verdict for the workload: a component
		// mounting a shared claim and a private ReadWriteOnce one is still
		// pinned to a single pod by the second.
		props := map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 2,
			"volumes": []any{
				map[string]any{
					"name": "shared", "type": "pvc", "mountPath": "/shared", "size": "1Gi",
					"accessModes": []any{"ReadWriteOnce", "ReadWriteMany"},
				},
				map[string]any{
					"name": "private", "type": "pvc", "mountPath": "/private", "size": "1Gi",
					"accessModes": []any{"ReadWriteOnce"},
				},
			},
		}
		cfg := deploymentConfig(t, "app", props)
		_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err == nil {
			t.Fatal("expected an error: the second claim is ReadWriteOnce-only")
		}
		if !strings.Contains(err.Error(), "at most one replica") {
			t.Errorf("error = %q, want it to name the replica constraint", err.Error())
		}
	})

	t.Run("ReadWriteOnce with ReadOnlyMany is still constrained", func(t *testing.T) {
		// ReadOnlyMany lets many pods mount the volume read-only; it does not
		// make the read-write mount shareable, so the guard still applies.
		cfg := deploymentConfig(t, "app", rwxProps([]any{"ReadWriteOnce", "ReadOnlyMany"}, map[string]any{"replicas": 2}))
		_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err == nil {
			t.Fatal("expected an error for replicas=2 alongside a ReadWriteOnce/ReadOnlyMany claim")
		}
	})
}

func TestDeploymentHandler_NonRWXRejectsMultipleReplicas(t *testing.T) {
	cfg := deploymentConfig(t, "app", nonRWXVolumeProps(map[string]any{"replicas": 2}))
	_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err == nil {
		t.Fatal("expected an error for replicas=2 alongside a non-RWX PVC")
	}
	if !strings.Contains(err.Error(), "at most one replica") {
		t.Errorf("error = %q, want it to name the replica constraint", err.Error())
	}
}

// TestDeploymentHandler_ReplicasIsValidated pins this kind's checked reading of
// `replicas`. The shared parseReplicas helper the other kinds use runs the value
// through toInt32 and falls back to the default when that fails, so a string
// silently becomes 1 and a negative reaches the apiserver; deployment refuses
// both at build time instead.
func TestDeploymentHandler_ReplicasIsValidated(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"string", "3", "replicas: must be an integer, got string"},
		{"boolean", true, "replicas: must be an integer, got bool"},
		{"negative", -1, "replicas: must be >= 0, got -1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &components.DeploymentHandler{}
			_, err := h.ToApplicationConfig(&oam.Component{
				Name: "app",
				Type: "deployment",
				Properties: map[string]any{
					"image":    "nginx:1.27",
					"replicas": tc.value,
				},
			}, "default")
			if err == nil {
				t.Fatalf("ToApplicationConfig(replicas: %v) = nil error, want one containing %q", tc.value, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestDeploymentHandler_ReplicasRoundTrip covers the accepting side of the
// guard above, including the unauthored default, so a parser that rejected
// every replicas value could not pass.
func TestDeploymentHandler_ReplicasRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
		want  int32
	}{
		{"unauthored defaults to 1", map[string]any{}, 1},
		{"authored", map[string]any{"replicas": 3}, 3},
		{"scale to zero", map[string]any{"replicas": 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := map[string]any{"image": "nginx:1.27"}
			for k, v := range tc.props {
				props[k] = v
			}
			dep, _ := generateDeployment(t, "app", props)
			if dep.Spec.Replicas == nil {
				t.Fatal("Spec.Replicas = nil, want a value")
			}
			if *dep.Spec.Replicas != tc.want {
				t.Errorf("Spec.Replicas = %d, want %d", *dep.Spec.Replicas, tc.want)
			}
		})
	}
}

// TestDeploymentHandler_NonRWXAllowsZeroReplicas pins the "at most one" reading
// of the guard: scale-to-zero holds the ReadWriteOnce claim in no pod at all,
// so it is legal. Without this, tightening the guard to "exactly one" would
// leave the suite green.
func TestDeploymentHandler_NonRWXAllowsZeroReplicas(t *testing.T) {
	cfg := deploymentConfig(t, "app", nonRWXVolumeProps(map[string]any{"replicas": 0}))
	if _, err := cfg.Generate(stack.NewApplication("app", "default", cfg)); err != nil {
		t.Fatalf("Generate() error = %v, want replicas=0 accepted alongside a non-RWX PVC", err)
	}
}

// TestDeploymentHandler_RWXLeavesStrategyUntouched proves the guard is scoped
// to non-RWX claims: without it the test above would pass for the wrong reason.
func TestDeploymentHandler_RWXLeavesStrategyUntouched(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": 3,
		"volumes": []any{
			map[string]any{
				"name":        "data",
				"type":        "pvc",
				"mountPath":   "/data",
				"size":        "1Gi",
				"accessModes": []any{"ReadWriteMany"},
			},
		},
	})
	if dep.Spec.Strategy.Type != "" {
		t.Errorf("Strategy.Type = %q, want it left unset for an RWX claim", dep.Spec.Strategy.Type)
	}
}

func TestDeploymentHandler_ServiceAccountName_Authored(t *testing.T) {
	cfg := deploymentConfig(t, "app", map[string]any{
		"image":              "ghcr.io/org/app:v1",
		"serviceAccountName": "existing-sa",
	})
	objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if _, ok := (*obj).(*corev1.ServiceAccount); ok {
			t.Error("an authored serviceAccountName must not also generate a ServiceAccount")
		}
	}
	namer, ok := cfg.(oam.ServiceAccountNamer)
	if !ok {
		t.Fatal("DeploymentConfig does not implement oam.ServiceAccountNamer")
	}
	if got := namer.ServiceAccountName(); got != "existing-sa" {
		t.Errorf("ServiceAccountName() = %q, want %q", got, "existing-sa")
	}
}

func TestDeploymentHandler_SharedPodFieldsAreProjected(t *testing.T) {
	dep, _ := generateDeployment(t, "app", map[string]any{
		"image":                         "ghcr.io/org/app:v1",
		"hostNetwork":                   true,
		"terminationGracePeriodSeconds": 45,
		"nodeSelector":                  map[string]any{"disktype": "ssd"},
	})
	ps := dep.Spec.Template.Spec
	if !ps.HostNetwork {
		t.Error("HostNetwork = false, want true")
	}
	if ps.TerminationGracePeriodSeconds == nil || *ps.TerminationGracePeriodSeconds != 45 {
		t.Errorf("TerminationGracePeriodSeconds = %v, want 45", ps.TerminationGracePeriodSeconds)
	}
	if ps.NodeSelector["disktype"] != "ssd" {
		t.Errorf("NodeSelector = %v, want disktype=ssd", ps.NodeSelector)
	}
}

func TestDeploymentHandler_NamedProbePort_Error(t *testing.T) {
	h := &components.DeploymentHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "deployment",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"path": "/healthz", "port": "http"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected an error for a named probe port: this kind declares no ContainerPort to resolve it against")
	}
}

func TestDeploymentConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	cfg := deploymentConfig(t, "app", map[string]any{
		"image":    "ghcr.io/org/app:v1",
		"replicas": 3,
	})
	if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{maxReplicas: int32ptr(2)}); err == nil {
		t.Error("expected an error when replicas exceed max")
	}
}

func TestDeploymentConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	cfg := deploymentConfig(t, "app", map[string]any{"image": "ghcr.io/org/app:v1"})
	if err := cfg.(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("nil policy should be a no-op, got: %v", err)
	}
}

func TestDeploymentConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	cfg := deploymentConfig(t, "app", map[string]any{
		"image":           "ghcr.io/org/app:v1",
		"securityContext": map[string]any{"privileged": true},
	})
	if err := cfg.(oam.Enforceable).ApplyPolicy(&stubPolicy{}); err == nil {
		t.Error("expected an error for a privileged container under a policy that forbids it")
	}
}

func TestDeploymentConfig_ApplyPolicy_MaxStorageSize(t *testing.T) {
	cfg := deploymentConfig(t, "app", nonRWXVolumeProps(nil))
	p := &stubPolicy{}
	p.maxStorageSize = "512Mi"
	if err := cfg.(oam.Enforceable).ApplyPolicy(p); err == nil {
		t.Error("expected an error when the PVC size exceeds max")
	}
}

// TestDeploymentHandler_PropertySchemaComposition asserts the handler publishes
// all three layers — its own container-level keys, the shared pod-level
// fragment, and the DeploymentSpec fragment. Composition is easy to break by
// dropping one maps.Copy, and every individual key would still validate.
func TestDeploymentHandler_PropertySchemaComposition(t *testing.T) {
	s := (&components.DeploymentHandler{}).PropertySchema()
	for _, k := range []string{
		"image", "replicas", "probes", "volumes", // own keys
		"nodeSelector", "hostNetwork", "serviceAccountName", // schemaPodSpec
		"strategy", "minReadySeconds", "revisionHistoryLimit", "paused", "progressDeadlineSeconds", // schemaDeploymentSpec
	} {
		if _, ok := s[k]; !ok {
			t.Errorf("PropertySchema is missing %q", k)
		}
	}
	// The omissions are as much a decision as the inclusions
	// (go-kure/launcher#343): no port, so no Service; no affinity shorthand; no
	// default topology spread.
	for _, k := range []string{"port", "affinity"} {
		if _, ok := s[k]; ok {
			t.Errorf("PropertySchema declares %q, which this kind deliberately does not accept", k)
		}
	}
	for k, v := range s {
		if v.Description == "" {
			t.Errorf("property %q has no Description", k)
		}
	}
}

// TestDeploymentHandler_TopLevelNullIsOmission pins null-as-absence across this
// kind's whole published top-level surface rather than the one field a review
// named. pkg/oam's property validator reads an explicit null under an optional
// property as absent, so each of these documents is schema-valid; the typed
// parse helpers answer "present?" with a bare map lookup, so without the strip
// the nil reaches a type check and the component fails during conversion after
// the schema accepted it.
//
// The case list is derived from PropertySchema rather than written out, so a
// property added later is covered without anyone remembering to add it here.
func TestDeploymentHandler_TopLevelNullIsOmission(t *testing.T) {
	h := &components.DeploymentHandler{}
	schema := h.PropertySchema()

	optional := make([]string, 0, len(schema))
	for key, ps := range schema {
		if !ps.Required {
			optional = append(optional, key)
		}
	}
	if len(optional) < 10 {
		t.Fatalf("PropertySchema yielded only %d optional properties, want the kind's full surface — the derivation above is broken", len(optional))
	}

	for _, key := range optional {
		t.Run(key, func(t *testing.T) {
			_, err := h.ToApplicationConfig(&oam.Component{
				Name:       "app",
				Type:       "deployment",
				Properties: map[string]any{"image": "nginx:1.27", key: nil},
			}, "default")
			if err != nil {
				t.Fatalf("%s: null was refused: %v", key, err)
			}
		})
	}

	t.Run("every optional property null at once", func(t *testing.T) {
		props := map[string]any{"image": "nginx:1.27"}
		for _, key := range optional {
			props[key] = nil
		}
		dep, _ := generateDeployment(t, "app", props)
		if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
			t.Errorf("Spec.Replicas = %v, want the unauthored default 1 — `replicas: null` must read as absent, not as an authored value", dep.Spec.Replicas)
		}
	})

	// A typed nil is what a Go-constructed lowering rule produces when it
	// assigns a nil map or slice into an `any`. It is not `== nil`, so it needs
	// the reflection arm the property validator also uses.
	t.Run("typed nil", func(t *testing.T) {
		props := map[string]any{
			"image":           "nginx:1.27",
			"env":             []any(nil),
			"resources":       map[string]any(nil),
			"securityContext": map[string]any(nil),
			"volumes":         []any(nil),
		}
		if _, err := h.ToApplicationConfig(&oam.Component{Name: "app", Type: "deployment", Properties: props}, "default"); err != nil {
			t.Fatalf("typed nils were refused: %v", err)
		}
	})

	// The stripping must not reach the keys that may not appear at all: those
	// are refused before it runs, so naming one — even as a null — still earns
	// the explanatory refusal. Passing the stripped copy into
	// parseDeploymentSpec would make this silent, and every parser-level test
	// would stay green.
	t.Run("a rejected key named as null is still rejected", func(t *testing.T) {
		for _, key := range []string{"selector", "template"} {
			_, err := h.ToApplicationConfig(&oam.Component{
				Name:       "app",
				Type:       "deployment",
				Properties: map[string]any{"image": "nginx:1.27", key: nil},
			}, "default")
			if err == nil {
				t.Errorf("%s: null was accepted, want the not-authorable refusal", key)
				continue
			}
			if !strings.Contains(err.Error(), "not authorable") {
				t.Errorf("%s: null produced %q, want the not-authorable refusal", key, err.Error())
			}
		}
	})
}
