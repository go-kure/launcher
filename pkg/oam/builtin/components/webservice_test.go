package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// stubPolicy implements oam.Policy for testing.
type stubPolicy struct {
	maxReplicas            *int32
	defaultReplicas        *int32
	allowedRegistries      []string
	maxCPU                 string
	maxMemory              string
	maxStorageSize         string
	defaultCPURequest      string
	defaultMemoryRequest   string
	defaultCPULimit        string
	defaultMemoryLimit     string
	defaultStorageSize     string
	allowPrivileged        bool
	allowHostPathVols      bool
	allowedContainerCaps   []string
	forbiddenContainerCaps []string
}

func (s *stubPolicy) MaxReplicas() *int32                      { return s.maxReplicas }
func (s *stubPolicy) MaxCPU() string                           { return s.maxCPU }
func (s *stubPolicy) MaxMemory() string                        { return s.maxMemory }
func (s *stubPolicy) MaxStorageSize() string                   { return s.maxStorageSize }
func (s *stubPolicy) AllowedRegistries() []string              { return s.allowedRegistries }
func (s *stubPolicy) DefaultReplicas() *int32                  { return s.defaultReplicas }
func (s *stubPolicy) DefaultCPURequest() string                { return s.defaultCPURequest }
func (s *stubPolicy) DefaultMemoryRequest() string             { return s.defaultMemoryRequest }
func (s *stubPolicy) DefaultCPULimit() string                  { return s.defaultCPULimit }
func (s *stubPolicy) DefaultMemoryLimit() string               { return s.defaultMemoryLimit }
func (s *stubPolicy) DefaultStorageSize() string               { return s.defaultStorageSize }
func (s *stubPolicy) DefaultScalerMinReplicas() *int32         { return nil }
func (s *stubPolicy) DefaultScalerMaxReplicas() *int32         { return nil }
func (s *stubPolicy) AllowHostNetwork() bool                   { return false }
func (s *stubPolicy) AllowPrivileged() bool                    { return s.allowPrivileged }
func (s *stubPolicy) AllowHostPID() bool                       { return false }
func (s *stubPolicy) AllowHostIPC() bool                       { return false }
func (s *stubPolicy) AllowHostPathVolumes() bool               { return s.allowHostPathVols }
func (s *stubPolicy) AllowedCapabilities() []string            { return nil }
func (s *stubPolicy) ForbiddenCapabilities() []string          { return nil }
func (s *stubPolicy) RequiredCapabilities() []string           { return nil }
func (s *stubPolicy) AllowedContainerCapabilities() []string   { return s.allowedContainerCaps }
func (s *stubPolicy) ForbiddenContainerCapabilities() []string { return s.forbiddenContainerCaps }

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

// TestWebserviceHandler_NamedPort_Mismatch_Error covers launcher#278 wave-12
// finding 3: webservice always names its container port "http" — a
// syntactically valid but different name is guaranteed unresolvable by the
// kubelet and must be rejected, not accepted merely because a port exists.
func TestWebserviceHandler_NamedPort_Mismatch_Error(t *testing.T) {
	h := &components.WebserviceHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"probes": map[string]any{
				"readiness": map[string]any{
					"httpGet": map[string]any{
						"path": "/ready",
						"port": "metrics",
					},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named port that does not match webservice's declared \"http\" container port")
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

func TestWebserviceHandler_WithEnv_FieldRef(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{
					"name": "POD_NAME",
					"valueFrom": map[string]any{
						"fieldRef": map[string]any{
							"fieldPath": "metadata.name",
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

func TestWebserviceHandler_WithEnv_ResourceFieldRef(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{
					"name": "CPU_LIMIT",
					"valueFrom": map[string]any{
						"resourceFieldRef": map[string]any{
							"resource": "limits.cpu",
							"divisor":  "1m",
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

func TestWebserviceHandler_WithEnv_MultipleValueFromSources_Error(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"env": []any{
				map[string]any{
					"name": "BAD",
					"valueFrom": map[string]any{
						"fieldRef": map[string]any{
							"fieldPath": "metadata.name",
						},
						"secretKeyRef": map[string]any{
							"name": "s",
							"key":  "k",
						},
					},
				},
			},
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for multiple valueFrom sources")
	}
}

func TestWebserviceHandler_WithEnvFrom(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"envFrom": []any{
				map[string]any{
					"configMapRef": map[string]any{"name": "app-config"},
					"prefix":       "CFG_",
				},
				map[string]any{
					"secretRef": map[string]any{"name": "app-secret", "optional": true},
				},
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
			ef := dep.Spec.Template.Spec.Containers[0].EnvFrom
			if len(ef) != 2 {
				t.Fatalf("expected 2 envFrom entries, got %d", len(ef))
			}
			if ef[0].ConfigMapRef == nil || ef[0].ConfigMapRef.Name != "app-config" || ef[0].Prefix != "CFG_" {
				t.Errorf("unexpected first envFrom entry: %+v", ef[0])
			}
			if ef[1].SecretRef == nil || ef[1].SecretRef.Name != "app-secret" || ef[1].SecretRef.Optional == nil || !*ef[1].SecretRef.Optional {
				t.Errorf("unexpected second envFrom entry: %+v", ef[1])
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_WithEnvFrom_BothRefs_Error(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"envFrom": []any{
				map[string]any{
					"configMapRef": map[string]any{"name": "a"},
					"secretRef":    map[string]any{"name": "b"},
				},
			},
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error when both configMapRef and secretRef are set")
	}
}

func TestWebserviceHandler_WithResources_ExtraNamedResources(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"resources": map[string]any{
				"requests": map[string]any{
					"cpu":               "100m",
					"nvidia.com/gpu":    "1",
					"ephemeral-storage": "1Gi",
				},
				"limits": map[string]any{
					"nvidia.com/gpu": "1",
				},
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
			res := dep.Spec.Template.Spec.Containers[0].Resources
			if _, ok := res.Requests["nvidia.com/gpu"]; !ok {
				t.Error("expected nvidia.com/gpu in requests")
			}
			if _, ok := res.Requests["ephemeral-storage"]; !ok {
				t.Error("expected ephemeral-storage in requests")
			}
			if _, ok := res.Limits["nvidia.com/gpu"]; !ok {
				t.Error("expected nvidia.com/gpu in limits")
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_WithLifecycle(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"lifecycle": map[string]any{
				"postStart": map[string]any{
					"exec": map[string]any{"command": []any{"/bin/sh", "-c", "echo start"}},
				},
				"preStop": map[string]any{
					"sleep": map[string]any{"seconds": 5},
				},
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
			lc := dep.Spec.Template.Spec.Containers[0].Lifecycle
			if lc == nil || lc.PostStart == nil || lc.PostStart.Exec == nil {
				t.Error("expected postStart.exec to be set")
			}
			if lc == nil || lc.PreStop == nil || lc.PreStop.Sleep == nil || lc.PreStop.Sleep.Seconds != 5 {
				t.Error("expected preStop.sleep.seconds=5")
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_WithSecurityContext(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"runAsUser":                int64(1000),
				"runAsNonRoot":             true,
				"readOnlyRootFilesystem":   true,
				"allowPrivilegeEscalation": false,
				"capabilities": map[string]any{
					"add":  []any{"NET_BIND_SERVICE"},
					"drop": []any{"ALL"},
				},
				"seccompProfile": map[string]any{
					"type": "RuntimeDefault",
				},
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
			sc := dep.Spec.Template.Spec.Containers[0].SecurityContext
			if sc == nil {
				t.Fatal("expected securityContext to be set")
			}
			if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
				t.Errorf("expected runAsUser=1000, got %v", sc.RunAsUser)
			}
			if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
				t.Error("expected runAsNonRoot=true")
			}
			if sc.Capabilities == nil || len(sc.Capabilities.Add) != 1 || len(sc.Capabilities.Drop) != 1 {
				t.Errorf("unexpected capabilities: %+v", sc.Capabilities)
			}
			if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
				t.Errorf("unexpected seccompProfile: %+v", sc.SeccompProfile)
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_WithSecurityContext_SeccompLocalhost_MissingProfile_Error(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"seccompProfile": map[string]any{
					"type": "Localhost",
				},
			},
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for Localhost seccompProfile missing localhostProfile")
	}
}

func TestWebserviceHandler_WithWorkingDir(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":      "ghcr.io/org/app:v1",
			"workingDir": "/app",
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
			if dep.Spec.Template.Spec.Containers[0].WorkingDir != "/app" {
				t.Errorf("expected workingDir=/app, got %q", dep.Spec.Template.Spec.Containers[0].WorkingDir)
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceConfig_ApplyPolicy_PrivilegedDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"privileged": true,
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: false}); err == nil {
		t.Error("expected error when privileged=true and policy disallows it")
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

// TestWebserviceConfig_ApplyPolicy_HostPathDenied regression-tests a review
// finding (launcher#284, P1): ApplyPolicy never checked a parsed hostPath
// volume against oam.Policy.AllowHostPathVolumes(), so the default-deny
// policy (including NoopPolicy) did not actually stop a hostPath volume from
// being authored — a container-escape-adjacent gap, not merely a style one.
func TestWebserviceConfig_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"volumes": []any{
				map[string]any{
					"name":      "logs",
					"type":      "hostPath",
					"mountPath": "/var/log",
					"path":      "/var/log/app",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
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
}

// TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied regression-tests
// go-kure/launcher#305: ApplyPolicy never checked an authored
// securityContext.capabilities.add entry against
// oam.Policy.ForbiddenContainerCapabilities(), so a forbidden Linux
// capability could be added with nothing rejecting it.
func TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"securityContext": map[string]any{
				"capabilities": map[string]any{
					"add": []any{"NET_ADMIN"},
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
		t.Error("expected error when capabilities.add includes a forbidden capability")
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

// TestWebserviceConfig_ApplyPolicy_MaxCPU_AgainstIntrinsicDefault
// regression-tests launcher#251: buildResourceRequirements' intrinsic 100m
// CPU / 128Mi memory request fallback is injected at Generate() time, after
// ApplyPolicy runs — so enforcing maxima against the pre-fallback
// c.Resources let an application that omitted spec.resources ship above the
// cap. The error must name the value as a generated default so the author
// knows where the number came from.
func TestWebserviceConfig_ApplyPolicy_MaxCPU_AgainstIntrinsicDefault(t *testing.T) {
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

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
}

func TestWebserviceConfig_ApplyPolicy_MaxMemory_AgainstIntrinsicDefault(t *testing.T) {
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

	err = enforceable.ApplyPolicy(&stubPolicy{maxMemory: "64Mi"})
	if err == nil {
		t.Fatal("expected error when the intrinsic default memory request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
}

// TestWebserviceConfig_ApplyPolicy_MaxCPU_AuthoredBeatsIntrinsic proves
// precedence survives the fix: an authored value under the cap must not be
// flagged, even though the intrinsic default (which never applies here)
// would itself have exceeded it.
func TestWebserviceConfig_ApplyPolicy_MaxCPU_AuthoredBeatsIntrinsic(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"resources": map[string]any{
				"requests": map[string]any{
					"cpu": "10m",
				},
			},
		},
	}
	cfg, err := h.ToApplicationConfig(component, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)

	p := &stubPolicy{defaultCPURequest: "200m", maxCPU: "50m"}
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Errorf("expected no error: authored 10m beats the 200m policy default and is under the 50m max, got %v", err)
	}
}

// TestWebserviceConfig_ApplyPolicy_MaxCPU_PolicyDefaultEnforced proves a
// policy default (the middle tier) is still enforced, and — since it is not
// the intrinsic handler default — the error does NOT carry the "generated
// default" marker.
func TestWebserviceConfig_ApplyPolicy_MaxCPU_PolicyDefaultEnforced(t *testing.T) {
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

	p := &stubPolicy{defaultCPURequest: "200m", maxCPU: "50m"}
	err = enforceable.ApplyPolicy(p)
	if err == nil {
		t.Fatal("expected error: the 200m policy default exceeds the 50m max")
	}
	if strings.Contains(err.Error(), "generated default") {
		t.Errorf("policy default should not be marked as a generated default, got %q", err.Error())
	}
}

// TestWebserviceConfig_ApplyPolicy_MaxMemory_RequestCheckedBeforeLimit proves
// the check ordering matches the pre-fix code (cpu request, cpu limit, memory
// request, memory limit): a policy-defaulted 256Mi memory request against a
// 128Mi max fails at the memory-request check. It does NOT exercise the
// derived-memory-limit path — buildResourceRequirements mirrors the memory
// limit from the (same) memory request, so a request that already exceeds
// the max makes the limit exceed it identically, and the request check
// (earlier in the fixed order) always reports first.
func TestWebserviceConfig_ApplyPolicy_MaxMemory_RequestCheckedBeforeLimit(t *testing.T) {
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

	p := &stubPolicy{defaultMemoryRequest: "256Mi", maxMemory: "128Mi"}
	err = enforceable.ApplyPolicy(p)
	if err == nil {
		t.Fatal("expected error: the 256Mi policy-defaulted memory request exceeds the 128Mi max")
	}
	if !strings.Contains(err.Error(), "memory request") {
		t.Errorf("expected the memory request check to fail first, got %q", err.Error())
	}
}

// TestWebserviceConfig_ApplyPolicy_MaxResources_OutputUnchanged proves the
// fix is read-only: a config that passes policy still generates the exact
// same intrinsic-default Resources it did before launcher#251.
func TestWebserviceConfig_ApplyPolicy_MaxResources_OutputUnchanged(t *testing.T) {
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
	if err := enforceable.ApplyPolicy(&stubPolicy{maxCPU: "1", maxMemory: "1Gi"}); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}

	app := stack.NewApplication("app", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var dep *appsv1.Deployment
	for _, obj := range objects {
		if d, ok := (*obj).(*appsv1.Deployment); ok {
			dep = d
			break
		}
	}
	if dep == nil {
		t.Fatal("Deployment not found in output")
	}

	res := dep.Spec.Template.Spec.Containers[0].Resources
	wantCPURequest := resource.MustParse("100m")
	wantMemRequest := resource.MustParse("128Mi")
	wantMemLimit := resource.MustParse("128Mi")
	if got := res.Requests[corev1.ResourceCPU]; got.Cmp(wantCPURequest) != 0 {
		t.Errorf("cpu request = %v, want %v", got.String(), wantCPURequest.String())
	}
	if got := res.Requests[corev1.ResourceMemory]; got.Cmp(wantMemRequest) != 0 {
		t.Errorf("memory request = %v, want %v", got.String(), wantMemRequest.String())
	}
	if got := res.Limits[corev1.ResourceMemory]; got.Cmp(wantMemLimit) != 0 {
		t.Errorf("memory limit = %v, want %v", got.String(), wantMemLimit.String())
	}
	if cpuLimit, ok := res.Limits[corev1.ResourceCPU]; ok {
		t.Errorf("cpu limit = %v, want absent", cpuLimit.String())
	}
	if len(res.Requests) != 2 {
		t.Errorf("len(Requests) = %d, want 2 (cpu, memory): %v", len(res.Requests), res.Requests)
	}
	if len(res.Limits) != 1 {
		t.Errorf("len(Limits) = %d, want 1 (memory): %v", len(res.Limits), res.Limits)
	}
}

// The eight tests below (go-kure/launcher#312) regression-test ApplyPolicy
// walking c.InitContainers/c.Sidecars: before this fix, an init container or
// sidecar's own resources and image registry were parsed but never checked
// against the environment policy, and its `securityContext` (privileged
// flag, capabilities — go-kure/launcher#316) was not parsed at all, so an
// authored value that would be rejected on the main container sailed through
// unenforced on a non-main one. Every test authors the main container's own
// `resources` explicitly within policy-conformant bounds so the main
// container's own enforceMaxResources check does not trip first and mask the
// init/sidecar check under test.

func TestWebserviceConfig_ApplyPolicy_InitContainerResourcesDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "ghcr.io/org/init:v1",
				},
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

func TestWebserviceConfig_ApplyPolicy_InitContainerRegistryDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "docker.io/x/y:v1",
				},
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

func TestWebserviceConfig_ApplyPolicy_InitContainerPrivilegedDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name":            "init",
					"image":           "ghcr.io/org/init:v1",
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

func TestWebserviceConfig_ApplyPolicy_InitContainerCapabilitiesDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "ghcr.io/org/init:v1",
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

func TestWebserviceConfig_ApplyPolicy_SidecarResourcesDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
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
	enforceable := cfg.(oam.Enforceable)

	err = enforceable.ApplyPolicy(&stubPolicy{maxCPU: "50m"})
	if err == nil {
		t.Fatal("expected error when the sidecar's intrinsic default CPU request exceeds the enforced maximum")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "generated default") {
		t.Errorf("expected error to mark the value as a generated default, got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWebserviceConfig_ApplyPolicy_SidecarRegistryDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name":  "sidecar",
					"image": "docker.io/x/y:v1",
				},
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
		t.Fatal("expected error when the sidecar's image is not from an allowed registry")
	}
	if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under a permissive policy, got %v", err)
	}
}

func TestWebserviceConfig_ApplyPolicy_SidecarPrivilegedDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name":            "sidecar",
					"image":           "ghcr.io/org/sidecar:v1",
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
		t.Error("expected error when the sidecar is privileged and policy disallows it")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{allowPrivileged: true}); err != nil {
		t.Errorf("expected no error when policy allows privileged, got %v", err)
	}
}

func TestWebserviceConfig_ApplyPolicy_SidecarCapabilitiesDenied(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image":     "ghcr.io/org/app:v1",
			"resources": map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "16Mi"}},
			"sidecars": []any{
				map[string]any{
					"name":  "sidecar",
					"image": "ghcr.io/org/sidecar:v1",
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
		t.Error("expected error when the sidecar adds a forbidden capability")
	} else if !strings.Contains(err.Error(), "sidecars[0]") {
		t.Errorf("expected error to name sidecars[0], got %q", err.Error())
	}
	if err := enforceable.ApplyPolicy(&stubPolicy{}); err != nil {
		t.Errorf("expected no error under the default-allow NoopPolicy-equivalent stub, got %v", err)
	}
}

// TestWebserviceHandler_InitContainerSecurityContext_RoundTrip and
// TestWebserviceHandler_SidecarSecurityContext_RoundTrip (go-kure/launcher#316)
// regression-test parseInitContainers/parseSidecars actually reading an
// authored `securityContext` and buildInitContainer/buildSidecarContainer
// rendering it onto the corev1.Container — before this fix, the key was
// silently discarded (no rejectUnknownKeys call on the init/sidecar item
// map) and nothing was ever set on the built container.
func TestWebserviceHandler_InitContainerSecurityContext_RoundTrip(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"privileged":   false,
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
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			ics := dep.Spec.Template.Spec.InitContainers
			if len(ics) != 1 {
				t.Fatalf("expected 1 init container, got %d", len(ics))
			}
			sc := ics[0].SecurityContext
			if sc == nil {
				t.Fatal("expected init container securityContext to be set")
			}
			if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
				t.Error("expected runAsNonRoot=true on the init container")
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

func TestWebserviceHandler_SidecarSecurityContext_RoundTrip(t *testing.T) {
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
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"privileged":   false,
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
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, obj := range objects {
		if dep, ok := (*obj).(*appsv1.Deployment); ok {
			containers := dep.Spec.Template.Spec.Containers
			var sidecar *corev1.Container
			for i := range containers {
				if containers[i].Name == "sidecar" {
					sidecar = &containers[i]
				}
			}
			if sidecar == nil {
				t.Fatal("sidecar container not found")
			}
			sc := sidecar.SecurityContext
			if sc == nil {
				t.Fatal("expected sidecar securityContext to be set")
			}
			if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
				t.Error("expected runAsNonRoot=true on the sidecar")
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}

// TestWebserviceHandler_InitContainerSecurityContext_ParseErrorPropagation
// regression-tests that a malformed init container securityContext fails
// ToApplicationConfig with an initContainers[0]-prefixed error, proving the
// parser error path (not just the happy path) is wired through
// parseInitContainers.
func TestWebserviceHandler_InitContainerSecurityContext_ParseErrorPropagation(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "ghcr.io/org/init:v1",
					"securityContext": map[string]any{
						"privileged":               true,
						"allowPrivilegeEscalation": false,
					},
				},
			},
		},
	}
	_, err := h.ToApplicationConfig(component, "default")
	if err == nil {
		t.Fatal("expected error for contradictory privileged/allowPrivilegeEscalation on an init container")
	}
	if !strings.Contains(err.Error(), "initContainers[0]") {
		t.Errorf("expected error to name initContainers[0], got %q", err.Error())
	}
}

// TestWebserviceHandler_InitContainerSecurityContext_AbsentIsNoop regression-
// tests that an init container authoring no securityContext at all renders a
// corev1.Container whose SecurityContext stays nil, rather than some
// zero-value struct.
func TestWebserviceHandler_InitContainerSecurityContext_AbsentIsNoop(t *testing.T) {
	h := &components.WebserviceHandler{}
	component := &oam.Component{
		Name: "app",
		Type: "webservice",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"initContainers": []any{
				map[string]any{
					"name":  "init",
					"image": "ghcr.io/org/init:v1",
				},
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
			ics := dep.Spec.Template.Spec.InitContainers
			if len(ics) != 1 {
				t.Fatalf("expected 1 init container, got %d", len(ics))
			}
			if ics[0].SecurityContext != nil {
				t.Errorf("expected nil SecurityContext for an init container authoring none, got %+v", ics[0].SecurityContext)
			}
			return
		}
	}
	t.Error("Deployment not found in output")
}
