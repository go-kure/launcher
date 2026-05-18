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
