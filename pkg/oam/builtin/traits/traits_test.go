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

// --- CertificateHandler ---

func TestCertificateHandler_CanHandle(t *testing.T) {
	h := &traits.CertificateHandler{}
	if !h.CanHandle("certificate") {
		t.Error("expected true for certificate")
	}
	if h.CanHandle("scaler") {
		t.Error("expected false for scaler")
	}
}

func TestCertificateHandler_CapabilityRequired(t *testing.T) {
	h := &traits.CertificateHandler{}
	if !h.CapabilityRequired() {
		t.Error("expected true")
	}
}

func TestCertificateHandler_ValidateAndApplyDefaults_OK(t *testing.T) {
	h := &traits.CertificateHandler{}
	rendering := map[string]any{
		"issuerRefName": "letsencrypt-prod",
	}
	out, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["issuerRefKind"] != "ClusterIssuer" {
		t.Errorf("expected default issuerRefKind 'ClusterIssuer', got %q", out["issuerRefKind"])
	}
}

func TestCertificateHandler_ValidateAndApplyDefaults_MissingIssuerRefName(t *testing.T) {
	h := &traits.CertificateHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing issuerRefName")
	}
}

func TestCertificateHandler_ValidateAndApplyDefaults_UnknownField(t *testing.T) {
	h := &traits.CertificateHandler{}
	rendering := map[string]any{
		"issuerRefName": "letsencrypt-prod",
		"unknownField":  "value",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestCertificateHandler_Apply_OK(t *testing.T) {
	h := &traits.CertificateHandler{}
	trait := &oam.Trait{
		Type: "certificate",
		Properties: map[string]any{
			"secretName":    "my-tls",
			"issuerRefName": "letsencrypt-prod",
			"issuerRefKind": "ClusterIssuer",
			"dnsNames":      []any{"example.com"},
		},
	}
	app := newApp("frontend", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Errorf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
}

func TestCertificateHandler_Apply_MissingSecretName(t *testing.T) {
	h := &traits.CertificateHandler{}
	trait := &oam.Trait{
		Type: "certificate",
		Properties: map[string]any{
			"issuerRefName": "letsencrypt-prod",
			"dnsNames":      []any{"example.com"},
		},
	}
	app := newApp("frontend", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err == nil {
		t.Fatal("expected error for missing secretName")
	}
}

func TestCertificateHandler_Apply_MissingDNSNames(t *testing.T) {
	h := &traits.CertificateHandler{}
	trait := &oam.Trait{
		Type: "certificate",
		Properties: map[string]any{
			"secretName":    "my-tls",
			"issuerRefName": "letsencrypt-prod",
		},
	}
	app := newApp("frontend", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err == nil {
		t.Fatal("expected error for missing dnsNames")
	}
}

func TestCertificateConfig_Generate_OK(t *testing.T) {
	h := &traits.CertificateHandler{}
	trait := &oam.Trait{
		Type: "certificate",
		Properties: map[string]any{
			"secretName":    "my-tls",
			"issuerRefName": "letsencrypt-prod",
			"issuerRefKind": "ClusterIssuer",
			"dnsNames":      []any{"example.com"},
			"duration":      "2160h",
			"renewBefore":   "360h",
		},
	}
	app := newApp("frontend", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("expected 1 Certificate object, got %d", len(objects))
	}
}

// --- ScalerHandler ---

func TestScalerHandler_CanHandle(t *testing.T) {
	h := &traits.ScalerHandler{}
	if !h.CanHandle("scaler") {
		t.Error("expected true for scaler")
	}
	if h.CanHandle("pvc") {
		t.Error("expected false for pvc")
	}
}

func TestScalerHandler_Apply_OK(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 2,
			"maxReplicas": 10,
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Errorf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
}

func TestScalerHandler_Apply_MissingMinReplicas(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"maxReplicas": 10,
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for missing minReplicas")
	}
}

func TestScalerHandler_Apply_MinReplicasLessThanOne(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 0,
			"maxReplicas": 10,
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for minReplicas < 1")
	}
}

func TestScalerHandler_Apply_MaxLessThanMin(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 5,
			"maxReplicas": 3,
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for maxReplicas < minReplicas")
	}
}

func TestScalerHandler_Apply_WithPDB(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 2,
			"maxReplicas": 5,
			"enablePDB":   true,
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 2 {
		t.Errorf("expected 2 objects (HPA + PDB), got %d", len(objects))
	}
}

func TestScalerHandler_Apply_PDB_RequiresMinReplicas2(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 1,
			"maxReplicas": 5,
			"enablePDB":   true,
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error: enablePDB requires minReplicas >= 2")
	}
}

func TestScalerHandler_Apply_CPUUtilizationOutOfRange(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas":    1,
			"maxReplicas":    5,
			"cpuUtilization": 150,
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for cpuUtilization > 100")
	}
}

func TestScalerConfig_Generate_HPAOnly(t *testing.T) {
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas":    2,
			"maxReplicas":    8,
			"cpuUtilization": 70,
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("expected 1 object (HPA only), got %d", len(objects))
	}
}

// --- PVCHandler ---

func TestPVCHandler_CanHandle(t *testing.T) {
	h := &traits.PVCHandler{}
	if !h.CanHandle("pvc") {
		t.Error("expected true for pvc")
	}
	if h.CanHandle("scaler") {
		t.Error("expected false for scaler")
	}
}

func TestPVCHandler_Apply_OK(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "shared-data",
			"size": "5Gi",
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Errorf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
}

func TestPVCHandler_Apply_MissingName(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"size": "5Gi",
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestPVCHandler_Apply_MissingSize(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "data",
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for missing size")
	}
}

func TestPVCHandler_Apply_InvalidSize(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "data",
			"size": "not-a-quantity",
		},
	}
	if err := h.Apply(trait, newApp("api", "default"), newBundle()); err == nil {
		t.Fatal("expected error for invalid size")
	}
}

func TestPVCTraitConfig_Generate_OK(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "shared-data",
			"size": "5Gi",
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("expected 1 PVC object, got %d", len(objects))
	}
}

func TestPVCTraitConfig_ApplyPolicy_ExceedsMax(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "data",
			"size": "100Gi",
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg := bundle.Applications[0].Config
	enforceable, ok := cfg.(oam.Enforceable)
	if !ok {
		t.Fatal("PVCTraitConfig must implement oam.Enforceable")
	}
	p := &stubPVCPolicy{maxStorageSize: "10Gi"}
	if err := enforceable.ApplyPolicy(p); err == nil {
		t.Error("expected error when PVC size exceeds max")
	}
}

func TestPVCTraitConfig_ApplyPolicy_WithinMax(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "data",
			"size": "5Gi",
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg := bundle.Applications[0].Config
	enforceable := cfg.(oam.Enforceable)
	p := &stubPVCPolicy{maxStorageSize: "10Gi"}
	if err := enforceable.ApplyPolicy(p); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// stubPVCPolicy implements oam.Policy for PVC trait tests.
type stubPVCPolicy struct {
	maxStorageSize string
}

func (p *stubPVCPolicy) MaxReplicas() *int32             { return nil }
func (p *stubPVCPolicy) MaxCPU() string                  { return "" }
func (p *stubPVCPolicy) MaxMemory() string               { return "" }
func (p *stubPVCPolicy) MaxStorageSize() string          { return p.maxStorageSize }
func (p *stubPVCPolicy) AllowedRegistries() []string     { return nil }
func (p *stubPVCPolicy) DefaultReplicas() *int32         { return nil }
func (p *stubPVCPolicy) DefaultCPURequest() string       { return "" }
func (p *stubPVCPolicy) DefaultMemoryRequest() string    { return "" }
func (p *stubPVCPolicy) DefaultCPULimit() string         { return "" }
func (p *stubPVCPolicy) DefaultMemoryLimit() string      { return "" }
func (p *stubPVCPolicy) AllowHostNetwork() bool          { return false }
func (p *stubPVCPolicy) AllowPrivileged() bool           { return false }
func (p *stubPVCPolicy) AllowHostPID() bool              { return false }
func (p *stubPVCPolicy) AllowHostIPC() bool              { return false }
func (p *stubPVCPolicy) AllowHostPathVolumes() bool      { return false }
func (p *stubPVCPolicy) AllowedCapabilities() []string   { return nil }
func (p *stubPVCPolicy) ForbiddenCapabilities() []string { return nil }
func (p *stubPVCPolicy) RequiredCapabilities() []string  { return nil }
