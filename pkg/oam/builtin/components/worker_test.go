package components_test

import (
	"strings"
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

// TestWorkerHandler_NamedLifecyclePort_Error covers launcher#278 wave-11
// finding 5: worker's main container never declares any port (there is no
// `port` property at all — see PropertySchema above), so a named httpGet
// port in `lifecycle`/`probes` can never resolve against it and is rejected
// at parse time instead of authoring a hook guaranteed to fail at runtime.
// worker is the representative test for this fix — cronjob shares the same
// portless shape and the same shared parsing path (parseProbes/
// parseLifecycle's namedPortsAllowed parameter, common.go).
func TestWorkerHandler_NamedLifecyclePort_Error(t *testing.T) {
	h := &components.WorkerHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/shutdown"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named lifecycle port on a portless worker")
	}
}

func TestWorkerHandler_NamedProbePort_Error(t *testing.T) {
	h := &components.WorkerHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"probes": map[string]any{
				"liveness": map[string]any{
					"httpGet": map[string]any{"port": "http", "path": "/healthz"},
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for a named probe port on a portless worker")
	}
}

func TestWorkerHandler_NumericLifecyclePort_Accepted(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
			"lifecycle": map[string]any{
				"preStop": map[string]any{
					"httpGet": map[string]any{"port": 8080, "path": "/shutdown"},
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("app", "default", cfg)
	if _, err := cfg.Generate(app); err != nil {
		t.Fatalf("Generate: %v", err)
	}
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

// TestWorkerConfig_ApplyPolicy_HostPathDenied is worker's sibling of
// TestWebserviceConfig_ApplyPolicy_HostPathDenied (launcher#284, P1) — the
// same shared ApplyPolicy gap, same shared enforceHostPathVolumes fix.
func TestWorkerConfig_ApplyPolicy_HostPathDenied(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
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
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	enforceable := cfg.(oam.Enforceable)
	if err := enforceable.ApplyPolicy(&stubPolicy{allowHostPathVols: false}); err == nil {
		t.Error("expected error when a hostPath volume is authored and policy disallows it")
	}
}

// TestWorkerConfig_ApplyPolicy_CapabilityAddDenied is worker's sibling of
// TestWebserviceConfig_ApplyPolicy_CapabilityAddDenied (go-kure/launcher#305)
// — the same shared ApplyPolicy gap, same shared enforceContainerCapabilities
// fix.
func TestWorkerConfig_ApplyPolicy_CapabilityAddDenied(t *testing.T) {
	h := &components.WorkerHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "app",
		Type: "worker",
		Properties: map[string]any{
			"image": "ghcr.io/org/app:v1",
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
}

// TestWorkerConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault is
// worker's sibling of the two webservice
// TestWebserviceConfig_ApplyPolicy_Max{CPU,Memory}_AgainstIntrinsicDefault
// cases (launcher#251) — proving enforceMaxResources is actually wired into
// WorkerConfig.ApplyPolicy, not just added to enforce.go.
func TestWorkerConfig_ApplyPolicy_MaxResources_AgainstIntrinsicDefault(t *testing.T) {
	cases := []struct {
		name   string
		policy stubPolicy
	}{
		{"cpu", stubPolicy{maxCPU: "50m"}},
		{"memory", stubPolicy{maxMemory: "64Mi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
