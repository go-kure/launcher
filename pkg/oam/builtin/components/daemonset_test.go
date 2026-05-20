package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestDaemonsetHandler_CanHandle(t *testing.T) {
	h := &components.DaemonsetHandler{}
	if !h.CanHandle("daemonset") {
		t.Error("expected true for daemonset")
	}
	if h.CanHandle("worker") {
		t.Error("expected false for worker")
	}
}

func TestDaemonsetHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "agent",
		Type:       "daemonset",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestDaemonsetHandler_Generate_ResourceTypes(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDS, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.DaemonSet:
			foundDS = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDS {
		t.Error("expected DaemonSet")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestDaemonsetHandler_NoReplicas(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	// DaemonsetConfig must not implement MaxReplicas enforcement (no replicas field)
	if _, ok := cfg.(interface{ Replicas() int32 }); ok {
		t.Error("DaemonsetConfig should not expose Replicas()")
	}
}

func TestDaemonsetConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
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

func TestDaemonsetConfig_ApplyPolicy_AllowedRegistries(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "docker.io/library/agent:v1.0.0",
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

func TestDaemonsetHandler_Tolerations_NonStringKey_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":    123,
					"effect": "NoSchedule",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for non-string toleration key")
	}
}

func TestDaemonsetHandler_Tolerations_InvalidOperator_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "Contains",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid toleration operator")
	}
}

func TestDaemonsetHandler_Tolerations_InvalidEffect_Rejected(t *testing.T) {
	h := &components.DaemonsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":    "node-role.kubernetes.io/control-plane",
					"effect": "NoRun",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid toleration effect")
	}
}

func TestDaemonsetHandler_WithTolerations(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"tolerations": []any{
				map[string]any{
					"key":      "node-role.kubernetes.io/control-plane",
					"operator": "Exists",
					"effect":   "NoSchedule",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if ds, ok := (*obj).(*appsv1.DaemonSet); ok {
			if len(ds.Spec.Template.Spec.Tolerations) != 1 {
				t.Errorf("expected 1 toleration, got %d", len(ds.Spec.Template.Spec.Tolerations))
			}
			return
		}
	}
	t.Error("DaemonSet not found")
}

func TestDaemonsetConfig_WithPort(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
			"port":  9090,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	dc, ok := cfg.(*components.DaemonsetConfig)
	if !ok {
		t.Fatalf("expected *DaemonsetConfig, got %T", cfg)
	}
	if dc.Port != 9090 {
		t.Errorf("Port = %d, want 9090", dc.Port)
	}
	if dc.ServicePort() != 9090 {
		t.Errorf("ServicePort() = %d, want 9090", dc.ServicePort())
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundDS, foundSvc, foundSA bool
	for _, obj := range objects {
		switch o := (*obj).(type) {
		case *appsv1.DaemonSet:
			foundDS = true
			// Verify container port
			if len(o.Spec.Template.Spec.Containers) == 0 {
				t.Fatal("no containers in DaemonSet")
			}
			ports := o.Spec.Template.Spec.Containers[0].Ports
			if len(ports) == 0 || ports[0].ContainerPort != 9090 {
				t.Errorf("container port = %v, want [{tcp 9090}]", ports)
			}
		case *corev1.Service:
			foundSvc = true
			if len(o.Spec.Ports) == 0 || o.Spec.Ports[0].Port != 9090 {
				t.Errorf("service port = %v, want 9090", o.Spec.Ports)
			}
			if o.Spec.Type != corev1.ServiceTypeClusterIP {
				t.Errorf("service type = %q, want ClusterIP", o.Spec.Type)
			}
			if o.Name != "agent" {
				t.Errorf("service name = %q, want \"agent\"", o.Name)
			}
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundDS {
		t.Error("expected DaemonSet")
	}
	if !foundSvc {
		t.Error("expected Service when port > 0")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
	// Verify object order: DaemonSet → Service → ServiceAccount
	if len(objects) < 3 {
		t.Fatalf("expected at least 3 objects, got %d", len(objects))
	}
	if _, ok := (*objects[0]).(*appsv1.DaemonSet); !ok {
		t.Errorf("objects[0] = %T, want *DaemonSet", *objects[0])
	}
	if _, ok := (*objects[1]).(*corev1.Service); !ok {
		t.Errorf("objects[1] = %T, want *Service", *objects[1])
	}
	if _, ok := (*objects[2]).(*corev1.ServiceAccount); !ok {
		t.Errorf("objects[2] = %T, want *ServiceAccount", *objects[2])
	}
}

func TestDaemonsetConfig_WithoutPort(t *testing.T) {
	h := &components.DaemonsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "agent",
		Type: "daemonset",
		Properties: map[string]any{
			"image": "ghcr.io/org/agent:v1.0.0",
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("agent", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, obj := range objects {
		if _, ok := (*obj).(*corev1.Service); ok {
			t.Error("expected no Service when port is not set")
		}
	}
	if len(objects) != 2 {
		t.Errorf("expected 2 objects (DaemonSet + ServiceAccount), got %d", len(objects))
	}
}
