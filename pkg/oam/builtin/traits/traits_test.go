package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func newApp(name, namespace string) *stack.Application {
	return stack.NewApplication(name, namespace, nil)
}

func newBundle() *stack.Bundle {
	return &stack.Bundle{}
}

// --- ExposeHandler.ValidateAndApplyDefaults ---

func TestExposeHandler_ValidateAndApplyDefaults_ValidIngress(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType":   "ingress",
		"ingressClassName": "nginx",
	}
	got, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil rendering")
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_MissingControllerType(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"ingressClassName": "nginx",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for missing controllerType")
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_GatewayRejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "my-gateway",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for gateway controllerType")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' in error, got: %v", err)
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_MissingIngressClassName(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "ingress",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for missing ingressClassName")
	}
	if !strings.Contains(err.Error(), "ingressClassName") {
		t.Errorf("expected 'ingressClassName' in error, got: %v", err)
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_IngressWithGatewayFields(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType":   "ingress",
		"ingressClassName": "nginx",
		"gatewayName":      "my-gw",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected mutual exclusivity error")
	}
	if !strings.Contains(err.Error(), "gatewayName") {
		t.Errorf("expected 'gatewayName' in error, got: %v", err)
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_UnknownField(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType":   "ingress",
		"ingressClassName": "nginx",
		"unknownField":     "value",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// --- ExposeHandler.Apply ---

func TestExposeHandler_Apply_DispatchesToIngress(t *testing.T) {
	h := &traits.ExposeHandler{}
	trait := &oam.Trait{
		Type: "expose",
		Properties: map[string]any{
			"controllerType":   "ingress",
			"ingressClassName": "nginx",
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{"path": "/"},
					},
				},
			},
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Errorf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
}

func TestExposeHandler_Apply_UnsupportedControllerType(t *testing.T) {
	h := &traits.ExposeHandler{}
	trait := &oam.Trait{
		Type: "expose",
		Properties: map[string]any{
			"controllerType": "gateway",
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected error for unsupported controllerType")
	}
}

// --- IngressHandler.Apply ---

func TestIngressHandler_Apply_MissingRules(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type:       "ingress",
		Properties: map[string]any{},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected error for missing rules")
	}
}

func TestIngressHandler_Apply_Basic(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"ingressClassName": "nginx",
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{"path": "/api"},
					},
				},
			},
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Errorf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "my-app-ingress" {
		t.Errorf("expected sub-app name 'my-app-ingress', got %q", bundle.Applications[0].Name)
	}
}

func TestIngressHandler_Apply_TLS(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{"path": "/"},
					},
				},
			},
			"tls": []any{
				map[string]any{
					"hosts":      []any{"example.com"},
					"secretName": "example-tls",
				},
			},
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) == 0 {
		t.Fatal("expected sub-application in bundle")
	}
}

// --- CanHandle and CapabilityRequired ---

func TestExposeHandler_CanHandle(t *testing.T) {
	h := &traits.ExposeHandler{}
	if !h.CanHandle("expose") {
		t.Error("expected true for expose")
	}
	if h.CanHandle("ingress") {
		t.Error("expected false for ingress")
	}
}

func TestExposeHandler_CapabilityRequired(t *testing.T) {
	h := &traits.ExposeHandler{}
	if !h.CapabilityRequired() {
		t.Error("expected true")
	}
}

func TestIngressHandler_CanHandle(t *testing.T) {
	h := &traits.IngressHandler{}
	if !h.CanHandle("ingress") {
		t.Error("expected true for ingress")
	}
	if h.CanHandle("expose") {
		t.Error("expected false for expose")
	}
}

// --- IngressConfig.Generate (via Apply → bundle → Generate) ---

func TestIngressHandler_Apply_Generate(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"ingressClassName": "nginx",
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{
							"path":     "/api",
							"pathType": "Prefix",
							"port":     8080,
						},
						map[string]any{
							"path":     "/exact",
							"pathType": "Exact",
							"port":     8080,
						},
						map[string]any{
							"path":     "/impl",
							"pathType": "ImplementationSpecific",
							"portName": "http",
						},
					},
				},
			},
			"tls": []any{
				map[string]any{
					"hosts":      []any{"example.com"},
					"secretName": "tls-secret",
				},
			},
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) == 0 {
		t.Fatal("no sub-application in bundle")
	}
	objects, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("expected 1 object, got %d", len(objects))
	}
}

func TestIngressHandler_Apply_NamedSubApp(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"name": "custom-ingress",
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{"path": "/"},
					},
				},
			},
		},
	}
	app := newApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.Applications[0].Name != "custom-ingress" {
		t.Errorf("expected name 'custom-ingress', got %q", bundle.Applications[0].Name)
	}
}
