package components_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

func TestStatefulsetHandler_CanHandle(t *testing.T) {
	h := &components.StatefulsetHandler{}
	if !h.CanHandle("statefulset") {
		t.Error("expected true for statefulset")
	}
	if h.CanHandle("webservice") {
		t.Error("expected false for webservice")
	}
}

func TestStatefulsetHandler_RequiredImage_Missing(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name:       "db",
		Type:       "statefulset",
		Properties: map[string]any{},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestStatefulsetHandler_Generate_BasicResources(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"port":  5432,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var foundSTS, foundSVC, foundSA bool
	for _, obj := range objects {
		switch (*obj).(type) {
		case *appsv1.StatefulSet:
			foundSTS = true
		case *corev1.Service:
			foundSVC = true
		case *corev1.ServiceAccount:
			foundSA = true
		}
	}
	if !foundSTS {
		t.Error("expected StatefulSet")
	}
	if !foundSVC {
		t.Error("expected headless Service")
	}
	if !foundSA {
		t.Error("expected ServiceAccount")
	}
}

func TestStatefulsetHandler_VolumeClaimTemplates(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "10Gi",
					"mountPath": "/var/lib/data",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	app := stack.NewApplication("db", "default", cfg)
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var sts *appsv1.StatefulSet
	for _, obj := range objects {
		if s, ok := (*obj).(*appsv1.StatefulSet); ok {
			sts = s
		}
	}
	if sts == nil {
		t.Fatal("expected StatefulSet in output")
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 {
		t.Errorf("expected 1 VCT, got %d", len(sts.Spec.VolumeClaimTemplates))
	}
}

func TestStatefulsetHandler_VolumeClaimTemplate_MissingName(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"size":      "10Gi",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for missing VCT name")
	}
}

func TestStatefulsetHandler_VolumeClaimTemplate_InvalidSize(t *testing.T) {
	h := &components.StatefulsetHandler{}
	_, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "not-a-quantity",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err == nil {
		t.Fatal("expected error for invalid VCT size")
	}
}

func TestStatefulsetConfig_ApplyPolicy_MaxReplicas(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image":    "ghcr.io/org/postgres:v15",
			"replicas": 5,
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxReplicas: int32ptr(3)}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when replicas exceed max")
	}
}

func TestStatefulsetConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
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

func TestStatefulsetConfig_ApplyPolicy_VCTStorageSize(t *testing.T) {
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db",
		Type: "statefulset",
		Properties: map[string]any{
			"image": "ghcr.io/org/postgres:v15",
			"volumeClaimTemplates": []any{
				map[string]any{
					"name":      "data",
					"size":      "100Gi",
					"mountPath": "/data",
				},
			},
		},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}

	enforceable := cfg.(oam.Enforceable)
	p := &stubPolicy{maxStorageSize: "10Gi"}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when VCT size exceeds max")
	}
}
