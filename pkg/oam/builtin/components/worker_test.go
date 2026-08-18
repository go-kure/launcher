package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestWorkerHandler_CanHandle(t *testing.T) {
	h := &components.WorkerHandler{}
	if !h.CanHandle("worker") {
		t.Error("expected true for worker")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

func TestWorkerHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.WorkerHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "app",
		Type:       "worker",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestWorkerHandler_Generate_NoService(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "backend",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/backend:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("backend", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDeployment, foundService, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.Deployment:
			foundDeployment = true
		case *corev1.Service:
			foundService = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDeployment {
		t.Error("expected Deployment")
	}
	if foundService {
		t.Error("worker must not generate a Service")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestWorkerConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 3,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxReplicas: int32ptr(2)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestWorkerConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
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

func TestWorkerHandler_WithSharedPodFields(t *testing.T) {
	h := &components.WorkerHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image":      "ghcr.io/org/app:v1",
			"workingDir": "/app",
			"envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "cfg"}},
			},
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"exec": map[string]any{"command": []any{"/bin/sh", "-c", "sleep 1"}},
				},
			},
			"securityContext": map[string]any{
				"readOnlyRootFilesystem": true,
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			c := dep.Spec.Template.Spec.Containers[0]
			if c.WorkingDir != "/app" {
				t.Errorf("expected workingDir=/app, got %q", c.WorkingDir)
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
	t.Error("Deployment not found in output")
}

func TestWorkerConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
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
