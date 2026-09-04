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
	for _, c := range consumers {
		if c.m["app"] != "app" {
			t.Errorf("%s = %v, want app=app", c.name, c.m)
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

func TestDeploymentHandler_NonRWXRejectsMultipleReplicas(t *testing.T) {
	cfg := deploymentConfig(t, "app", nonRWXVolumeProps(map[string]any{"replicas": 2}))
	_, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
	if err == nil {
		t.Fatal("expected an error for replicas=2 alongside a non-RWX PVC")
	}
	if !strings.Contains(err.Error(), "requires replicas=1") {
		t.Errorf("error = %q, want it to name the replica constraint", err.Error())
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
	// The omissions are as much a decision as the inclusions (#343): no port,
	// so no Service; no affinity shorthand; no default topology spread.
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
