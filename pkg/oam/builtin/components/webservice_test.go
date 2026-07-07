package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// stubPolicy implements oam.Policy for testing.
type stubPolicy struct {
	maxReplicas        *int32
	defaultReplicas    *int32
	allowedRegistries  []string
	maxCPU             string
	maxMemory          string
	maxStorageSize     string
	defaultCPURequest  string
	defaultStorageSize string
}

func (s *stubPolicy) MaxReplicas() *int32              { return s.maxReplicas }
func (s *stubPolicy) MaxCPU() string                   { return s.maxCPU }
func (s *stubPolicy) MaxMemory() string                { return s.maxMemory }
func (s *stubPolicy) MaxStorageSize() string           { return s.maxStorageSize }
func (s *stubPolicy) AllowedRegistries() []string      { return s.allowedRegistries }
func (s *stubPolicy) DefaultReplicas() *int32          { return s.defaultReplicas }
func (s *stubPolicy) DefaultCPURequest() string        { return s.defaultCPURequest }
func (s *stubPolicy) DefaultMemoryRequest() string     { return "" }
func (s *stubPolicy) DefaultCPULimit() string          { return "" }
func (s *stubPolicy) DefaultMemoryLimit() string       { return "" }
func (s *stubPolicy) DefaultStorageSize() string       { return s.defaultStorageSize }
func (s *stubPolicy) DefaultScalerMinReplicas() *int32 { return nil }
func (s *stubPolicy) DefaultScalerMaxReplicas() *int32 { return nil }
func (s *stubPolicy) AllowHostNetwork() bool           { return false }
func (s *stubPolicy) AllowPrivileged() bool            { return false }
func (s *stubPolicy) AllowHostPID() bool               { return false }
func (s *stubPolicy) AllowHostIPC() bool               { return false }
func (s *stubPolicy) AllowHostPathVolumes() bool       { return false }
func (s *stubPolicy) AllowedCapabilities() []string    { return nil }
func (s *stubPolicy) ForbiddenCapabilities() []string  { return nil }
func (s *stubPolicy) RequiredCapabilities() []string   { return nil }

var _ oam.Policy = (*stubPolicy)(nil)

func int32ptr(v int32) *int32 { return &v }

func TestWebserviceHandler_CanHandle(t *testing.T) {
	h := &components.WebserviceHandler{}
	if !h.CanHandle("webservice") {
		t.Error("expected true for webservice")
	}
	if h.CanHandle("worker") {
		t.Error("expected false for worker")
	}
}

func TestWebserviceHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name:       "app",
		Type:       "webservice",
		Properties: map[string]any{},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestWebserviceHandler_InvalidImage_Latest(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "nginx:latest",
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for :latest tag")
	}
}

func TestWebserviceHandler_Generate_BasicResources(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "my-app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/my-app:v1.0.0",
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("my-app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var (
		foundDeployment     bool
		foundService        bool
		foundServiceAccount bool
	)
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.Deployment:
			foundDeployment = true
		case *corev1.Service:
			foundService = true
		case *corev1.ServiceAccount:
			foundServiceAccount = true
		}
	}
	if !foundDeployment {
		t.Error("expected Deployment in output")
	}
	if !foundService {
		t.Error("expected Service in output")
	}
	if !foundServiceAccount {
		t.Error("expected ServiceAccount in output")
	}
}

func TestWebserviceConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 3,
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable, ok := cfg.(oam.Enforceable)
	if !ok {
		t.Fatal("expected WebserviceConfig to implement oam.Enforceable")
	}

	p := &stubPolicy{maxReplicas: int32ptr(2)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestWebserviceConfig_ApplyPolicy_AllowedRegistries(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "docker.io/library/nginx:v1.25.0",
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{allowedRegistries: []string{"ghcr.io"}}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error for disallowed registry")
	}
}

func TestWebserviceConfig_ApplyPolicy_DefaultReplicas(t *testing.T) {
	h := &components.WebserviceHandler{}
	// No replicas in properties → not explicit
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{defaultReplicas: int32ptr(5)}
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}

	app := stack.NewApplication("app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 5 {
				t.Errorf("expected replicas=5 from default, got %v", dep.Spec.Replicas)
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_WithResources(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"resources": map[string]any{
				"requests": map[string]any{
					"cpu":    "100m",
					"memory": "128Mi",
				},
				"limits": map[string]any{
					"cpu":    "500m",
					"memory": "512Mi",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithEnv_Simple(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{"name": "FOO", "value": "bar"},
				map[string]any{"name": "BAZ", "value": "qux"},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithEnv_SecretRef(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{
					"name": "SECRET_VAL",
					"valueFrom": map[string]any{
						"secretKeyRef": map[string]any{
							"name": "my-secret",
							"key":  "password",
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
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithEnv_ConfigMapRef(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{
					"name": "CONFIG_VAL",
					"valueFrom": map[string]any{
						"configMapKeyRef": map[string]any{
							"name": "my-config",
							"key":  "value",
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
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithCommandAndArgs(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":   "ghcr.io/org/app:v1",
			"command": []any{"/bin/sh"},
			"args":    []any{"-c", "echo hello"},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithProbes(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"livenessProbe": map[string]any{
				"httpGet": map[string]any{
					"path": "/healthz",
					"port": 8080,
				},
				"initialDelaySeconds": 10,
				"periodSeconds":       5,
			},
			"readinessProbe": map[string]any{
				"httpGet": map[string]any{
					"path": "/ready",
					"port": 8080,
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithInitContainers(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"initContainers": []any{
				map[string]any{
					"name":    "init",
					"image":   "ghcr.io/org/init:v1",
					"command": []any{"/bin/sh", "-c", "echo init"},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithSidecars(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"sidecars": []any{
				map[string]any{
					"name":  "sidecar",
					"image": "ghcr.io/org/sidecar:v1",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceConfig_ApplyPolicy_MaxCPU(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"resources": map[string]any{
				"limits": map[string]any{
					"cpu": "2000m",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{}
	p.maxCPU = "500m"
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when CPU limit exceeds max")
	}
}

func TestWebserviceConfig_ApplyPolicy_DefaultCPURequest(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{}
	p.defaultCPURequest = "100m"
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
}

func TestWebserviceHandler_WithVolumes_EmptyDir(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
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
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceConfig_ApplyPolicy_MaxStorageSize(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":    "ghcr.io/org/app:v1",
			"replicas": 1,
			"volumes": []any{
				map[string]any{
					"name":         "data",
					"type":         "pvc",
					"mountPath":    "/data",
					"size":         "20Gi",
					"storageClass": "standard",
					"accessModes":  []any{"ReadWriteOnce"},
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{}
	p.maxStorageSize = "5Gi"
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when PVC size exceeds max")
	}
}

func TestWebserviceHandler_WithProbes_NamedPort(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"livenessProbe": map[string]any{
				"httpGet": map[string]any{
					"path": "/healthz",
					"port": 8080,
				},
				"initialDelaySeconds": 10,
				"periodSeconds":       5,
			},
			"readinessProbe": map[string]any{
				"httpGet": map[string]any{
					"path": "/ready",
					"port": "http",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_WithAffinity(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"affinity": map[string]any{
				"enablePodAntiAffinity": true,
				"podAntiAffinityType":   "required",
				"topologyKey":           "kubernetes.io/hostname",
				"nodeSelector": map[string]any{
					"disktype": "ssd",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestWebserviceHandler_InvalidAffinity(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"affinity": map[string]any{
				"podAntiAffinityType": "invalid",
			},
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for invalid podAntiAffinityType")
	}
}
