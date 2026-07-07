package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func newApp(name, namespace string) *stack.Application {
	return stack.NewApplication(name, namespace, nil)
}

func newBundle() *stack.Bundle {
	return &stack.Bundle{}
}

// webConfig satisfies servicePortProvider via duck-typing at runtime.
type webConfig struct{ port int32 }

func (w *webConfig) ServicePort() int32 { return w.port }
func (w *webConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}

// namedWebConfig additionally satisfies serviceBackendNamer.
type namedWebConfig struct {
	port        int32
	serviceName string
}

func (w *namedWebConfig) ServicePort() int32         { return w.port }
func (w *namedWebConfig) BackendServiceName() string { return w.serviceName }
func (w *namedWebConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	return nil, nil
}

func newWebApp(name, namespace string) *stack.Application {
	return stack.NewApplication(name, namespace, &webConfig{port: 80})
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

func TestExposeHandler_ValidateAndApplyDefaults_Gateway_Valid(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "my-gateway",
	}
	got, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["gatewayNamespace"] != "gateway-system" {
		t.Errorf("expected gatewayNamespace defaulted to 'gateway-system', got: %v", got["gatewayNamespace"])
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
	app := newWebApp("my-app", "default")
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
			"controllerType": "foo",
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
	app := newWebApp("my-app", "default")
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
	app := newWebApp("my-app", "default")
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
							"port":     80,
						},
						map[string]any{
							"path":     "/exact",
							"pathType": "Exact",
							"port":     80,
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
	app := newWebApp("my-app", "default")
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
	app := newWebApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.Applications[0].Name != "custom-ingress" {
		t.Errorf("expected name 'custom-ingress', got %q", bundle.Applications[0].Name)
	}
}

func TestIngressHandler_Apply_Scope(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"scope": "external",
			"rules": []any{
				map[string]any{
					"host":  "example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	app := newWebApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.Applications[0].Name != "my-app-ingress-external" {
		t.Errorf("expected name 'my-app-ingress-external', got %q", bundle.Applications[0].Name)
	}
}

func TestIngressHandler_Apply_NameWinsOverScope(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"name":  "custom-ingress",
			"scope": "external",
			"rules": []any{
				map[string]any{
					"host":  "example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	app := newWebApp("my-app", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.Applications[0].Name != "custom-ingress" {
		t.Errorf("expected name to win over scope: 'custom-ingress', got %q", bundle.Applications[0].Name)
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
		"issuerRef": map[string]any{"name": "letsencrypt-prod"},
	}
	out, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ref, ok := out["issuerRef"].(map[string]any)
	if !ok {
		t.Fatalf("expected issuerRef map, got %T", out["issuerRef"])
	}
	if ref["kind"] != "ClusterIssuer" {
		t.Errorf("expected default issuerRef.kind 'ClusterIssuer', got %q", ref["kind"])
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
		"issuerRef":    map[string]any{"name": "letsencrypt-prod"},
		"unknownField": "value",
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
			"secretName": "my-tls",
			"issuerRef":  map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"},
			"dnsNames":   []any{"example.com"},
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
			"issuerRef": map[string]any{"name": "letsencrypt-prod"},
			"dnsNames":  []any{"example.com"},
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
			"secretName": "my-tls",
			"issuerRef":  map[string]any{"name": "letsencrypt-prod"},
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
			"secretName":  "my-tls",
			"issuerRef":   map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"},
			"dnsNames":    []any{"example.com"},
			"duration":    "2160h",
			"renewBefore": "360h",
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

// applyScalerPolicy applies the trait then runs ApplyPolicy, returning the
// ApplyPolicy error. minReplicas/maxReplicas are optional at parse time (a policy
// default may supply them), so effective-value violations surface here, not in Apply.
func applyScalerPolicy(t *testing.T, trait *oam.Trait, p oam.Policy) error {
	t.Helper()
	h := &traits.ScalerHandler{}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return bundle.Applications[0].Config.(oam.Enforceable).ApplyPolicy(p)
}

func TestScalerConfig_ApplyPolicy_MissingMinReplicas(t *testing.T) {
	trait := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"maxReplicas": 10},
	}
	// Omitted min with no policy default is rejected by ApplyPolicy...
	if err := applyScalerPolicy(t, trait, &oam.NoopPolicy{}); err == nil {
		t.Fatal("expected error for missing minReplicas with no policy default")
	}
	// ...but a policy default fills it.
	p := &stubScalerPolicy{defaultScalerMin: int32ptr32(2)}
	if err := applyScalerPolicy(t, trait, p); err != nil {
		t.Fatalf("expected policy default to fill minReplicas, got: %v", err)
	}
}

func TestScalerConfig_ApplyPolicy_MinReplicasLessThanOne(t *testing.T) {
	trait := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"minReplicas": 0, "maxReplicas": 10},
	}
	if err := applyScalerPolicy(t, trait, &oam.NoopPolicy{}); err == nil {
		t.Fatal("expected error for minReplicas < 1")
	}
}

func TestScalerConfig_ApplyPolicy_MaxLessThanMin(t *testing.T) {
	trait := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"minReplicas": 5, "maxReplicas": 3},
	}
	if err := applyScalerPolicy(t, trait, &oam.NoopPolicy{}); err == nil {
		t.Fatal("expected error for maxReplicas < minReplicas")
	}
}

func TestScalerConfig_ApplyPolicy_DefaultsAndCeiling(t *testing.T) {
	// Authored value wins over the policy default.
	authored := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"minReplicas": 3, "maxReplicas": 9},
	}
	p := &stubScalerPolicy{defaultScalerMin: int32ptr32(1), defaultScalerMax: int32ptr32(4)}
	if err := applyScalerPolicy(t, authored, p); err != nil {
		t.Fatalf("authored values with policy defaults: %v", err)
	}
	// Effective maxReplicas above the policy ceiling is rejected.
	omitted := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"minReplicas": 2},
	}
	ceiling := &stubScalerPolicy{maxReplicas: int32ptr32(5), defaultScalerMax: int32ptr32(10)}
	if err := applyScalerPolicy(t, omitted, ceiling); err == nil {
		t.Fatal("expected error when effective maxReplicas exceeds MaxReplicas ceiling")
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

func TestScalerConfig_ApplyPolicy_PDB_RequiresMinReplicas2(t *testing.T) {
	trait := &oam.Trait{
		Type: "scaler",
		Properties: map[string]any{
			"minReplicas": 1,
			"maxReplicas": 5,
			"enablePDB":   true,
		},
	}
	if err := applyScalerPolicy(t, trait, &oam.NoopPolicy{}); err == nil {
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

func TestScalerConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	// Authored values + nil policy: valid, no defaults/caps applied.
	authored := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"minReplicas": 2, "maxReplicas": 6},
	}
	if err := applyScalerPolicy(t, authored, nil); err != nil {
		t.Errorf("authored values with nil policy should be valid, got: %v", err)
	}
	// Omitted min + nil policy: no default to fill it → validateEffective errors.
	omitted := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"maxReplicas": 6},
	}
	if err := applyScalerPolicy(t, omitted, nil); err == nil {
		t.Fatal("expected error for omitted minReplicas with nil policy")
	}
}

func TestScalerConfig_Generate_RejectsInvalidBounds(t *testing.T) {
	// Bypass safety: a config built without ApplyPolicy must not emit invalid HPA
	// bounds. Apply leaves minReplicas at 0 when omitted; Generate must reject it.
	h := &traits.ScalerHandler{}
	trait := &oam.Trait{
		Type:       "scaler",
		Properties: map[string]any{"maxReplicas": 5},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := bundle.Applications[0].Generate(); err == nil {
		t.Fatal("expected Generate to reject minReplicas < 1 when ApplyPolicy was bypassed")
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

func TestPVCConfig_ApplyPolicy_MissingSize(t *testing.T) {
	h := &traits.PVCHandler{}
	trait := &oam.Trait{
		Type: "pvc",
		Properties: map[string]any{
			"name": "data",
		},
	}
	// size is optional at parse time; the "required" check moves to ApplyPolicy.
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	enforceable := bundle.Applications[0].Config.(oam.Enforceable)
	// No policy default → error.
	if err := enforceable.ApplyPolicy(&oam.NoopPolicy{}); err == nil {
		t.Fatal("expected error for missing size with no policy default")
	}
	// Policy default fills it.
	if err := enforceable.ApplyPolicy(&stubPVCPolicy{defaultStorageSize: "3Gi"}); err != nil {
		t.Fatalf("expected policy default to fill size, got: %v", err)
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

// pvcSizeAfterPolicy applies the pvc trait, runs ApplyPolicy, and returns the
// effective size rendered onto the generated PersistentVolumeClaim.
func pvcSizeAfterPolicy(t *testing.T, props map[string]any, p oam.Policy) string {
	t.Helper()
	h := &traits.PVCHandler{}
	trait := &oam.Trait{Type: "pvc", Properties: props}
	app := newApp("api", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := bundle.Applications[0].Config.(oam.Enforceable).ApplyPolicy(p); err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	pvc := (*objs[0]).(*corev1.PersistentVolumeClaim)
	return pvc.Spec.Resources.Requests.Storage().String()
}

func TestPVCTraitConfig_ApplyPolicy_NilPolicy(t *testing.T) {
	h := &traits.PVCHandler{}
	// Authored size + nil policy: valid.
	authored := &oam.Trait{Type: "pvc", Properties: map[string]any{"name": "data", "size": "5Gi"}}
	bundle := newBundle()
	if err := h.Apply(authored, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := bundle.Applications[0].Config.(oam.Enforceable).ApplyPolicy(nil); err != nil {
		t.Errorf("authored size with nil policy should be valid, got: %v", err)
	}
	// Omitted size + nil policy: no default → error.
	omitted := &oam.Trait{Type: "pvc", Properties: map[string]any{"name": "data"}}
	bundle2 := newBundle()
	if err := h.Apply(omitted, newApp("api", "default"), bundle2); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := bundle2.Applications[0].Config.(oam.Enforceable).ApplyPolicy(nil); err == nil {
		t.Fatal("expected error for omitted size with nil policy")
	}
}

func TestPVCTraitConfig_Generate_RejectsEmptySize(t *testing.T) {
	// Bypass safety: Generate must reject an unset size even without ApplyPolicy.
	h := &traits.PVCHandler{}
	trait := &oam.Trait{Type: "pvc", Properties: map[string]any{"name": "data"}}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := bundle.Applications[0].Generate(); err == nil {
		t.Fatal("expected Generate to reject empty size when ApplyPolicy was bypassed")
	}
}

func TestPVCTraitConfig_ApplyPolicy_DefaultStorageSize(t *testing.T) {
	// Omitted size takes the policy default.
	if got := pvcSizeAfterPolicy(t, map[string]any{"name": "data"}, &stubPVCPolicy{defaultStorageSize: "7Gi"}); got != "7Gi" {
		t.Errorf("defaulted size = %q, want 7Gi", got)
	}
	// Authored size wins over the policy default.
	if got := pvcSizeAfterPolicy(t, map[string]any{"name": "data", "size": "5Gi"}, &stubPVCPolicy{defaultStorageSize: "7Gi"}); got != "5Gi" {
		t.Errorf("authored size = %q, want 5Gi", got)
	}
}

// stubPVCPolicy implements oam.Policy for PVC trait tests.
type stubPVCPolicy struct {
	maxStorageSize     string
	defaultStorageSize string
}

func (p *stubPVCPolicy) MaxReplicas() *int32              { return nil }
func (p *stubPVCPolicy) MaxCPU() string                   { return "" }
func (p *stubPVCPolicy) MaxMemory() string                { return "" }
func (p *stubPVCPolicy) MaxStorageSize() string           { return p.maxStorageSize }
func (p *stubPVCPolicy) AllowedRegistries() []string      { return nil }
func (p *stubPVCPolicy) DefaultReplicas() *int32          { return nil }
func (p *stubPVCPolicy) DefaultCPURequest() string        { return "" }
func (p *stubPVCPolicy) DefaultMemoryRequest() string     { return "" }
func (p *stubPVCPolicy) DefaultCPULimit() string          { return "" }
func (p *stubPVCPolicy) DefaultMemoryLimit() string       { return "" }
func (p *stubPVCPolicy) DefaultStorageSize() string       { return p.defaultStorageSize }
func (p *stubPVCPolicy) DefaultScalerMinReplicas() *int32 { return nil }
func (p *stubPVCPolicy) DefaultScalerMaxReplicas() *int32 { return nil }
func (p *stubPVCPolicy) AllowHostNetwork() bool           { return false }
func (p *stubPVCPolicy) AllowPrivileged() bool            { return false }
func (p *stubPVCPolicy) AllowHostPID() bool               { return false }
func (p *stubPVCPolicy) AllowHostIPC() bool               { return false }
func (p *stubPVCPolicy) AllowHostPathVolumes() bool       { return false }
func (p *stubPVCPolicy) AllowedCapabilities() []string    { return nil }
func (p *stubPVCPolicy) ForbiddenCapabilities() []string  { return nil }
func (p *stubPVCPolicy) RequiredCapabilities() []string   { return nil }

// stubScalerPolicy implements oam.Policy for scaler trait tests, embedding
// NoopPolicy so only the scaler-relevant accessors need overriding.
type stubScalerPolicy struct {
	oam.NoopPolicy
	maxReplicas      *int32
	defaultScalerMin *int32
	defaultScalerMax *int32
}

func (p *stubScalerPolicy) MaxReplicas() *int32              { return p.maxReplicas }
func (p *stubScalerPolicy) DefaultScalerMinReplicas() *int32 { return p.defaultScalerMin }
func (p *stubScalerPolicy) DefaultScalerMaxReplicas() *int32 { return p.defaultScalerMax }

func int32ptr32(v int32) *int32 { return &v }

// --- ExternalSecretHandler ---

func TestExternalSecretHandler_CanHandle(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	if !h.CanHandle("external-secret") {
		t.Error("expected CanHandle to return true for 'external-secret'")
	}
	if h.CanHandle("configmap") {
		t.Error("expected CanHandle to return false for 'configmap'")
	}
}

func TestExternalSecretHandler_CapabilityRequired(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	if h.CapabilityRequired() {
		t.Error("expected CapabilityRequired to return false: inline provider/secretStoreRef is supported")
	}
}

func TestExternalSecretHandler_ValidateAndApplyDefaults_ValidWithName(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	rendering := map[string]any{
		"secretStoreRef": map[string]any{
			"name": "vault-store",
		},
	}
	got, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ref, _ := got["secretStoreRef"].(map[string]any)
	if ref["kind"] != "ClusterSecretStore" {
		t.Errorf("expected default kind ClusterSecretStore, got %v", ref["kind"])
	}
}

func TestExternalSecretHandler_ValidateAndApplyDefaults_MissingSecretStoreRef(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing secretStoreRef")
	}
}

func TestExternalSecretHandler_Apply_MissingSecretName(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretStoreRef": map[string]any{
				"name": "vault-store",
				"kind": "ClusterSecretStore",
			},
		},
	}
	err := h.Apply(trait, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing secretName")
	}
}

func TestExternalSecretHandler_Apply_AppendsToBundleAndNamedCorrectly(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"secretStoreRef": map[string]any{
				"name": "vault-store",
				"kind": "ClusterSecretStore",
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "api-external-secret-my-secret" {
		t.Errorf("expected name 'api-external-secret-my-secret', got %q", bundle.Applications[0].Name)
	}
}

// --- ConfigMapHandler ---

func TestConfigMapHandler_CanHandle(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	if !h.CanHandle("configmap") {
		t.Error("expected CanHandle to return true for 'configmap'")
	}
	if h.CanHandle("networkpolicy") {
		t.Error("expected CanHandle to return false for 'networkpolicy'")
	}
}

func TestConfigMapHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{"unknownKey": "value"})
	if err == nil {
		t.Fatal("expected error for unknown rendering key")
	}
}

func TestConfigMapHandler_Apply_MissingName(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	err := h.Apply(&oam.Trait{Type: "configmap", Properties: map[string]any{}}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestConfigMapHandler_Apply_NoMountPath_AppendsBundleOnly(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	trait := &oam.Trait{
		Type: "configmap",
		Properties: map[string]any{
			"name": "app-config",
			"data": map[string]any{"KEY": "value"},
		},
	}
	app := newApp("api", "default")
	originalConfig := app.Config
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application, got %d", len(bundle.Applications))
	}
	if app.Config != originalConfig {
		t.Error("expected app.Config unchanged when no mountPath")
	}
}

func TestConfigMapHandler_Apply_WithMountPath_WrapsConfig(t *testing.T) {
	h := &traits.ConfigMapHandler{}
	trait := &oam.Trait{
		Type: "configmap",
		Properties: map[string]any{
			"name":      "app-config",
			"mountPath": "/etc/config",
		},
	}
	app := newApp("api", "default")
	originalConfig := app.Config
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if app.Config == originalConfig {
		t.Error("expected app.Config to be wrapped with decorator")
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application for ConfigMap, got %d", len(bundle.Applications))
	}
}

// stubStatefulSetConfig is a stub ApplicationConfig that returns a StatefulSet.
type stubStatefulSetConfig struct{}

func (s *stubStatefulSetConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}},
				},
			},
		},
	}
	obj := client.Object(ss)
	return []*client.Object{&obj}, nil
}

// stubUnsupportedConfig returns a Service (non-workload type).
type stubUnsupportedConfig struct{}

func (s *stubUnsupportedConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc"}}
	obj := client.Object(svc)
	return []*client.Object{&obj}, nil
}

// stubDaemonSetConfig returns a DaemonSet.
type stubDaemonSetConfig struct{}

func (s *stubDaemonSetConfig) Generate(_ *stack.Application) ([]*client.Object, error) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "default"},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "agent", Image: "agent:1"}},
				},
			},
		},
	}
	obj := client.Object(ds)
	return []*client.Object{&obj}, nil
}

func TestConfigMapDecorator_StatefulSet_MountsVolume(t *testing.T) {
	dec := &traits.ConfigMapDecorator{
		Inner:         &stubStatefulSetConfig{},
		ConfigMapName: "my-config",
		MountPath:     "/etc/config",
	}
	app := newApp("db", "default")
	objects, err := dec.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
	ss, ok := (*objects[0]).(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("expected *appsv1.StatefulSet, got %T", *objects[0])
	}
	if len(ss.Spec.Template.Spec.Volumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(ss.Spec.Template.Spec.Volumes))
	}
	if len(ss.Spec.Template.Spec.Containers[0].VolumeMounts) != 1 {
		t.Errorf("expected 1 volumeMount, got %d", len(ss.Spec.Template.Spec.Containers[0].VolumeMounts))
	}
	if ss.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath != "/etc/config" {
		t.Errorf("unexpected mountPath: %q", ss.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
	}
}

func TestConfigMapDecorator_DaemonSet_MountsVolume(t *testing.T) {
	dec := &traits.ConfigMapDecorator{
		Inner:         &stubDaemonSetConfig{},
		ConfigMapName: "agent-config",
		MountPath:     "/etc/agent",
	}
	objects, err := dec.Generate(newApp("agent", "default"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ds, ok := (*objects[0]).(*appsv1.DaemonSet)
	if !ok {
		t.Fatalf("expected *appsv1.DaemonSet, got %T", *objects[0])
	}
	if len(ds.Spec.Template.Spec.Volumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(ds.Spec.Template.Spec.Volumes))
	}
}

func TestConfigMapDecorator_UnsupportedComponent_ReturnsError(t *testing.T) {
	dec := &traits.ConfigMapDecorator{
		Inner:         &stubUnsupportedConfig{},
		ConfigMapName: "my-config",
		MountPath:     "/etc/config",
	}
	_, err := dec.Generate(newApp("svc", "default"))
	if err == nil {
		t.Fatal("expected error for unsupported workload type")
	}
	if !strings.Contains(err.Error(), "no supported workload resource was found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- NetworkPolicyHandler ---

func TestNetworkPolicyHandler_CanHandle(t *testing.T) {
	h := &traits.NetworkPolicyHandler{}
	if !h.CanHandle("networkpolicy") {
		t.Error("expected CanHandle to return true for 'networkpolicy'")
	}
	if h.CanHandle("cilium-networkpolicy") {
		t.Error("expected CanHandle to return false for 'cilium-networkpolicy'")
	}
}

func TestNetworkPolicyHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.NetworkPolicyHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{"foo": "bar"})
	if err == nil {
		t.Fatal("expected error for unknown rendering key")
	}
}

func TestNetworkPolicyHandler_Apply_MissingIngressAndEgress(t *testing.T) {
	h := &traits.NetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{Type: "networkpolicy", Properties: map[string]any{}}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error when neither ingress nor egress specified")
	}
}

func TestNetworkPolicyHandler_Apply_AppendsToBundleWithIngress(t *testing.T) {
	h := &traits.NetworkPolicyHandler{}
	trait := &oam.Trait{
		Type: "networkpolicy",
		Properties: map[string]any{
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "frontend"}}},
					},
					"ports": []any{
						map[string]any{"port": float64(8080), "protocol": "TCP"},
					},
				},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "api-networkpolicy" {
		t.Errorf("expected name 'api-networkpolicy', got %q", bundle.Applications[0].Name)
	}
}

// --- CiliumNetworkPolicyHandler ---

func TestCiliumNetworkPolicyHandler_CanHandle(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	if !h.CanHandle("cilium-networkpolicy") {
		t.Error("expected CanHandle to return true for 'cilium-networkpolicy'")
	}
	if h.CanHandle("networkpolicy") {
		t.Error("expected CanHandle to return false for 'networkpolicy'")
	}
}

func TestCiliumNetworkPolicyHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{"foo": "bar"})
	if err == nil {
		t.Fatal("expected error for unknown rendering key")
	}
}

func TestCiliumNetworkPolicyHandler_Apply_MissingName(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{
		Type:       "cilium-networkpolicy",
		Properties: map[string]any{"ingress": []any{}},
	}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestCiliumNetworkPolicyHandler_Apply_MissingIngressAndEgress(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{
		Type:       "cilium-networkpolicy",
		Properties: map[string]any{"name": "my-policy"},
	}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error when neither ingress nor egress specified")
	}
}

func TestCiliumNetworkPolicyHandler_Apply_AppendsToBundle(t *testing.T) {
	h := &traits.CiliumNetworkPolicyHandler{}
	trait := &oam.Trait{
		Type: "cilium-networkpolicy",
		Properties: map[string]any{
			"name": "api-allow",
			"ingress": []any{
				map[string]any{"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"app": "frontend"}}}},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "api-allow" {
		t.Errorf("expected name 'api-allow', got %q", bundle.Applications[0].Name)
	}
}

// --- VolSyncHandler ---

func TestVolSyncHandler_CanHandle(t *testing.T) {
	h := &traits.VolSyncHandler{}
	if !h.CanHandle("volsync") {
		t.Error("expected CanHandle to return true for 'volsync'")
	}
	if h.CanHandle("configmap") {
		t.Error("expected CanHandle to return false for 'configmap'")
	}
}

func TestVolSyncHandler_ValidateAndApplyDefaults_RejectsUnknownKey(t *testing.T) {
	h := &traits.VolSyncHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{"foo": "bar"})
	if err == nil {
		t.Fatal("expected error for unknown rendering key")
	}
}

func TestVolSyncHandler_Apply_MissingSourcePVC(t *testing.T) {
	h := &traits.VolSyncHandler{}
	err := h.Apply(&oam.Trait{
		Type:       "volsync",
		Properties: map[string]any{"schedule": "@daily"},
	}, newApp("db", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing sourcePVC")
	}
}

func TestVolSyncHandler_Apply_MissingSchedule(t *testing.T) {
	h := &traits.VolSyncHandler{}
	err := h.Apply(&oam.Trait{
		Type:       "volsync",
		Properties: map[string]any{"sourcePVC": "data"},
	}, newApp("db", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing schedule")
	}
}

func TestVolSyncHandler_Apply_AppendsToBundle(t *testing.T) {
	h := &traits.VolSyncHandler{}
	trait := &oam.Trait{
		Type:       "volsync",
		Properties: map[string]any{"sourcePVC": "data", "schedule": "@daily"},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("db", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 bundle application, got %d", len(bundle.Applications))
	}
	if bundle.Applications[0].Name != "data-backup" {
		t.Errorf("expected name 'data-backup', got %q", bundle.Applications[0].Name)
	}
}

func TestVolSyncHandler_Apply_DefaultRepository(t *testing.T) {
	h := &traits.VolSyncHandler{}
	trait := &oam.Trait{
		Type:       "volsync",
		Properties: map[string]any{"sourcePVC": "data", "schedule": "@daily"},
	}
	bundle := newBundle()
	app := newApp("mydb", "default")
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg, ok := bundle.Applications[0].Config.(*traits.VolsyncConfig)
	if !ok {
		t.Fatalf("expected *traits.VolsyncConfig, got %T", bundle.Applications[0].Config)
	}
	if cfg.Repository != "mydb-volsync-secret" {
		t.Errorf("expected default repository 'mydb-volsync-secret', got %q", cfg.Repository)
	}
}

func TestVolSyncHandler_Apply_InvalidCopyMethod(t *testing.T) {
	h := &traits.VolSyncHandler{}
	trait := &oam.Trait{
		Type: "volsync",
		Properties: map[string]any{
			"sourcePVC":  "data",
			"schedule":   "@daily",
			"copyMethod": "Invalid",
		},
	}
	err := h.Apply(trait, newApp("db", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for invalid copyMethod")
	}
	if !strings.Contains(err.Error(), "unsupported copyMethod") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVolSyncHandler_Apply_NonIntegerPruneIntervalDays(t *testing.T) {
	h := &traits.VolSyncHandler{}
	err := h.Apply(&oam.Trait{
		Type: "volsync",
		Properties: map[string]any{
			"sourcePVC":         "data",
			"schedule":          "@daily",
			"pruneIntervalDays": float64(1.5),
		},
	}, newApp("db", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for non-integer pruneIntervalDays")
	}
}

func TestVolSyncHandler_Apply_NegativePruneIntervalDays(t *testing.T) {
	h := &traits.VolSyncHandler{}
	err := h.Apply(&oam.Trait{
		Type: "volsync",
		Properties: map[string]any{
			"sourcePVC":         "data",
			"schedule":          "@daily",
			"pruneIntervalDays": -1,
		},
	}, newApp("db", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for negative pruneIntervalDays")
	}
}

func TestVolSyncHandler_Apply_BundleNameIsPVCScoped(t *testing.T) {
	h := &traits.VolSyncHandler{}
	bundle := newBundle()
	if err := h.Apply(&oam.Trait{
		Type:       "volsync",
		Properties: map[string]any{"sourcePVC": "data", "schedule": "@daily"},
	}, newApp("mydb", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if bundle.Applications[0].Name != "data-backup" {
		t.Errorf("expected 'data-backup', got %q", bundle.Applications[0].Name)
	}
}

// --- ExternalSecretHandler remoteRef shorthand ---

func TestExternalSecretHandler_Apply_RemoteRefShorthand(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	trait := &oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-creds",
			"secretStoreRef": map[string]any{
				"name": "vault",
				"kind": "ClusterSecretStore",
			},
			"remoteRef": map[string]any{
				"key": "prod/my-app/db",
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply with remoteRef shorthand: %v", err)
	}
	cfg := bundle.Applications[0].Config.(*traits.ExternalSecretConfig)
	if len(cfg.Data) != 1 {
		t.Fatalf("expected 1 data entry from remoteRef shorthand, got %d", len(cfg.Data))
	}
	if cfg.Data[0].SecretKey != "my-creds" {
		t.Errorf("expected secretKey %q, got %q", "my-creds", cfg.Data[0].SecretKey)
	}
	if cfg.Data[0].RemoteRef.Key != "prod/my-app/db" {
		t.Errorf("expected remoteRef.key %q, got %q", "prod/my-app/db", cfg.Data[0].RemoteRef.Key)
	}
}

func TestExternalSecretHandler_Apply_RemoteRefConflictsWithData(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	err := h.Apply(&oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "my-creds",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"remoteRef":      map[string]any{"key": "prod/x"},
			"data": []any{
				map[string]any{
					"secretKey": "K",
					"remoteRef": map[string]any{"key": "prod/y"},
				},
			},
		},
	}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error when remoteRef combined with data")
	}
}

func TestExternalSecretHandler_Apply_RemoteRefDecodingStrategy(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	bundle := newBundle()
	if err := h.Apply(&oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "my-creds",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"remoteRef":      map[string]any{"key": "prod/x", "decodingStrategy": "Base64"},
		},
	}, newApp("api", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	cfg := bundle.Applications[0].Config.(*traits.ExternalSecretConfig)
	if cfg.Data[0].RemoteRef.DecodingStrategy != "Base64" {
		t.Errorf("expected decodingStrategy 'Base64', got %q", cfg.Data[0].RemoteRef.DecodingStrategy)
	}
}

func TestExternalSecretHandler_Apply_RemoteRefMissingKey(t *testing.T) {
	h := &traits.ExternalSecretHandler{}
	err := h.Apply(&oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":     "my-creds",
			"secretStoreRef": map[string]any{"name": "vault", "kind": "ClusterSecretStore"},
			"remoteRef":      map[string]any{},
		},
	}, newApp("api", "default"), newBundle())
	if err == nil {
		t.Fatal("expected error for missing remoteRef.key")
	}
}

// --- Generate() tests for coverage ---

func TestCiliumNetworkPolicyConfig_Generate(t *testing.T) {
	bundle := newBundle()
	h := &traits.CiliumNetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{
		Type: "cilium-networkpolicy",
		Properties: map[string]any{
			"name": "test-policy",
			"ingress": []any{
				map[string]any{"fromEndpoints": []any{map[string]any{"matchLabels": map[string]any{"app": "frontend"}}}},
			},
		},
	}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestConfigMapConfig_Generate(t *testing.T) {
	cfg := &traits.ConfigMapConfig{
		Name:          "my-config",
		ComponentName: "api",
		Data:          map[string]string{"KEY": "value"},
	}
	app := newApp("my-config", "default")
	objects, err := cfg.Generate(app)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestNetworkPolicyConfig_Generate(t *testing.T) {
	bundle := newBundle()
	h := &traits.NetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{
		Type: "networkpolicy",
		Properties: map[string]any{
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "backend"}}},
					},
					"ports": []any{
						map[string]any{"port": float64(5432), "protocol": "TCP"},
					},
				},
			},
		},
	}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestNetworkPolicyConfig_Generate_BothIngressAndEgress(t *testing.T) {
	bundle := newBundle()
	h := &traits.NetworkPolicyHandler{}
	err := h.Apply(&oam.Trait{
		Type: "networkpolicy",
		Properties: map[string]any{
			"ingress": []any{
				map[string]any{
					"from": []any{
						map[string]any{
							"namespaceSelector": map[string]any{"matchLabels": map[string]any{"ns": "prod"}},
							"podSelector":       map[string]any{"matchLabels": map[string]any{"app": "web"}},
						},
					},
				},
			},
			"egress": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"ipBlock": map[string]any{
								"cidr":   "10.0.0.0/8",
								"except": []any{"10.1.0.0/16"},
							},
						},
					},
				},
			},
		},
	}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestExternalSecretConfig_Generate(t *testing.T) {
	bundle := newBundle()
	h := &traits.ExternalSecretHandler{}
	err := h.Apply(&oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName": "my-secret",
			"secretStoreRef": map[string]any{
				"name": "vault",
				"kind": "ClusterSecretStore",
			},
			"data": []any{
				map[string]any{
					"secretKey": "DB_PASS",
					"remoteRef": map[string]any{"key": "prod/db", "property": "password"},
				},
			},
		},
	}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestExternalSecretConfig_Generate_WithDataFromAndTemplate(t *testing.T) {
	bundle := newBundle()
	h := &traits.ExternalSecretHandler{}
	err := h.Apply(&oam.Trait{
		Type: "external-secret",
		Properties: map[string]any{
			"secretName":       "bulk-secret",
			"targetSecretName": "my-k8s-secret",
			"secretStoreRef": map[string]any{
				"name": "vault",
				"kind": "ClusterSecretStore",
			},
			"target": map[string]any{
				"creationPolicy": "Merge",
				"deletionPolicy": "Retain",
				"template": map[string]any{
					"type": "kubernetes.io/dockerconfigjson",
					"data": map[string]any{".dockerconfigjson": "{{ .secret }}"},
				},
			},
			"dataFrom": []any{
				map[string]any{
					"extract": map[string]any{
						"key":                "prod/my-app",
						"decodingStrategy":   "None",
						"conversionStrategy": "Default",
						"metadataPolicy":     "None",
					},
				},
				map[string]any{
					"find": map[string]any{
						"name": map[string]any{"regexp": "prod/.*"},
						"tags": map[string]any{"env": "prod"},
					},
				},
			},
		},
	}, newApp("api", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

func TestVolsyncConfig_Generate(t *testing.T) {
	bundle := newBundle()
	h := &traits.VolSyncHandler{}
	err := h.Apply(&oam.Trait{
		Type: "volsync",
		Properties: map[string]any{
			"sourcePVC":               "data",
			"schedule":                "@daily",
			"storageClassName":        "fast",
			"volumeSnapshotClassName": "csi-snapclass",
			"pruneIntervalDays":       float64(7),
			"retain": map[string]any{
				"daily":   float64(3),
				"weekly":  float64(2),
				"monthly": float64(1),
			},
		},
	}, newApp("db", "default"), bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objects, err := bundle.Applications[0].Config.Generate(bundle.Applications[0])
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
}

// --- ExposeHandler gateway validation ---

func TestExposeHandler_ValidateAndApplyDefaults_Gateway_MissingGatewayName(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "gateway",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for missing gatewayName")
	}
	if !strings.Contains(err.Error(), "gatewayName") {
		t.Errorf("expected 'gatewayName' in error, got: %v", err)
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_Gateway_DefaultNamespace(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "prod-gateway",
	}
	got, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["gatewayNamespace"] != "gateway-system" {
		t.Errorf("expected gatewayNamespace='gateway-system', got: %v", got["gatewayNamespace"])
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_Gateway_ExplicitNamespace(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType":   "gateway",
		"gatewayName":      "prod-gateway",
		"gatewayNamespace": "infra",
	}
	got, err := h.ValidateAndApplyDefaults(rendering)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["gatewayNamespace"] != "infra" {
		t.Errorf("expected gatewayNamespace='infra', got: %v", got["gatewayNamespace"])
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_Gateway_RejectsIngressClassName(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType":   "gateway",
		"gatewayName":      "prod-gateway",
		"ingressClassName": "nginx",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for ingressClassName in gateway rendering")
	}
	if !strings.Contains(err.Error(), "ingressClassName") {
		t.Errorf("expected 'ingressClassName' in error, got: %v", err)
	}
}

func TestExposeHandler_ValidateAndApplyDefaults_UnsupportedControllerType(t *testing.T) {
	h := &traits.ExposeHandler{}
	rendering := map[string]any{
		"controllerType": "foo",
	}
	_, err := h.ValidateAndApplyDefaults(rendering)
	if err == nil {
		t.Fatal("expected error for unsupported controllerType")
	}
	if !strings.Contains(err.Error(), "foo") {
		t.Errorf("expected 'foo' in error, got: %v", err)
	}
}

// --- ExposeHandler.Apply gateway dispatch ---

func TestExposeHandler_Apply_Gateway_DispatchesToHTTPRoute(t *testing.T) {
	h := &traits.ExposeHandler{}
	trait := &oam.Trait{
		Type: "expose",
		Properties: map[string]any{
			"controllerType": "gateway",
			"gatewayName":    "my-gw",
			"rules":          []any{map[string]any{}},
		},
	}
	app := newWebApp("web", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 1 {
		t.Fatalf("expected 1 sub-application, got %d", len(bundle.Applications))
	}
	subApp := bundle.Applications[0]
	objs, err := subApp.Config.Generate(subApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	route, ok := (*objs[0]).(*gatewayv1.HTTPRoute)
	if !ok {
		t.Fatalf("expected *gatewayv1.HTTPRoute, got %T", *objs[0])
	}
	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("expected 1 parentRef, got %d", len(route.Spec.ParentRefs))
	}
	pr := route.Spec.ParentRefs[0]
	if string(pr.Name) != "my-gw" {
		t.Errorf("parentRef.Name = %q, want \"my-gw\"", pr.Name)
	}
	if pr.Namespace == nil || string(*pr.Namespace) != "gateway-system" {
		t.Errorf("parentRef.Namespace = %v, want \"gateway-system\"", pr.Namespace)
	}
}

func TestExposeHandler_Apply_Gateway_ExplicitNamespace(t *testing.T) {
	h := &traits.ExposeHandler{}
	trait := &oam.Trait{
		Type: "expose",
		Properties: map[string]any{
			"controllerType":   "gateway",
			"gatewayName":      "my-gw",
			"gatewayNamespace": "infra",
			"rules":            []any{map[string]any{}},
		},
	}
	app := newWebApp("web", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	subApp := bundle.Applications[0]
	objs, err := subApp.Config.Generate(subApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	route, ok := (*objs[0]).(*gatewayv1.HTTPRoute)
	if !ok {
		t.Fatalf("expected *gatewayv1.HTTPRoute, got %T", *objs[0])
	}
	pr := route.Spec.ParentRefs[0]
	if pr.Namespace == nil || string(*pr.Namespace) != "infra" {
		t.Errorf("parentRef.Namespace = %v, want \"infra\"", pr.Namespace)
	}
}

func TestExposeHandler_Apply_Gateway_NoServicePort_Errors(t *testing.T) {
	h := &traits.ExposeHandler{}
	trait := &oam.Trait{
		Type: "expose",
		Properties: map[string]any{
			"controllerType": "gateway",
			"gatewayName":    "my-gw",
			"rules":          []any{map[string]any{}},
		},
	}
	app := newApp("wrk", "default")
	bundle := newBundle()
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected error for component with no service port")
	}
	if !strings.Contains(err.Error(), "no service port") {
		t.Errorf("expected 'no service port' in error, got: %v", err)
	}
}

func TestIngressHandler_Apply_NoServicePort_Errors(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
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
	app := newApp("wrk", "default")
	bundle := newBundle()
	err := h.Apply(trait, app, bundle)
	if err == nil {
		t.Fatal("expected error for component with no service port")
	}
	if !strings.Contains(err.Error(), "no service port") {
		t.Errorf("expected 'no service port' in error, got: %v", err)
	}
}

func TestIngressHandler_Apply_ExplicitBackend_NoPortErrors(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"host": "example.com",
					"paths": []any{
						map[string]any{
							"path":    "/admin",
							"backend": "deny-backend",
							// no "port" — must error even though component has port 80
						},
					},
				},
			},
		},
	}
	app := newWebApp("web", "default")
	err := h.Apply(trait, app, newBundle())
	if err == nil || !strings.Contains(err.Error(), "cannot determine backend port") {
		t.Errorf("expected 'cannot determine backend port', got: %v", err)
	}
}

func TestIngressHandler_Apply_ExplicitBackend(t *testing.T) {
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
							"path":    "/",
							"backend": "deny-backend",
							"port":    8080,
						},
					},
				},
			},
		},
	}
	app := newApp("wrk", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	objs, err := bundle.Applications[0].Config.(*traits.IngressConfig).Generate(
		stack.NewApplication("wrk-ingress", "default", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ingress := (*objs[0]).(*networkingv1.Ingress)
	gotName := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
	if gotName != "deny-backend" {
		t.Errorf("backend.service.name = %q, want \"deny-backend\"", gotName)
	}
}

func TestIngressHandler_Apply_CustomServiceName(t *testing.T) {
	h := &traits.IngressHandler{}
	app := stack.NewApplication("my-statefulset", "default", &namedWebConfig{
		port: 5432, serviceName: "postgres-primary",
	})
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"host":  "db.example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	subApp := bundle.Applications[0]
	objs, err := subApp.Config.Generate(stack.NewApplication("my-statefulset-ingress", "default", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ingress, ok := (*objs[0]).(*networkingv1.Ingress)
	if !ok {
		t.Fatalf("expected *networkingv1.Ingress, got %T", *objs[0])
	}
	gotName := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
	if gotName != "postgres-primary" {
		t.Errorf("backend.service.name = %q, want \"postgres-primary\"", gotName)
	}
	// Label must use the component name, not the K8s Service name
	if got := ingress.Labels["app"]; got != "my-statefulset" {
		t.Errorf("ingress label app = %q, want \"my-statefulset\"", got)
	}
}

func TestIngressHandler_Apply_ImplicitBackend_PortMismatch_Error(t *testing.T) {
	h := &traits.IngressHandler{}

	t.Run("implicit backend, port differs from component service", func(t *testing.T) {
		// webservice exposes port 80; trait routes implicit backend to 8080 — must error
		trait := &oam.Trait{
			Type: "ingress",
			Properties: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "port": 8080},
						},
					},
				},
			},
		}
		app := newWebApp("web", "default") // port 80
		err := h.Apply(trait, app, newBundle())
		if err == nil || !strings.Contains(err.Error(), "cannot route implicit backend") {
			t.Errorf("expected 'cannot route implicit backend', got: %v", err)
		}
	})

	t.Run("self-service backend name, port differs from component service", func(t *testing.T) {
		// Explicitly naming the component's own service still subject to port mismatch guard
		trait := &oam.Trait{
			Type: "ingress",
			Properties: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "backend": "web", "port": 8080},
						},
					},
				},
			},
		}
		app := newWebApp("web", "default") // port 80, service name "web"
		err := h.Apply(trait, app, newBundle())
		if err == nil || !strings.Contains(err.Error(), "cannot route implicit backend") {
			t.Errorf("expected 'cannot route implicit backend', got: %v", err)
		}
	})

	t.Run("implicit backend, port matches component service — success", func(t *testing.T) {
		// Redundant explicit port that matches is allowed
		trait := &oam.Trait{
			Type: "ingress",
			Properties: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "port": 80},
						},
					},
				},
			},
		}
		app := newWebApp("web", "default") // port 80
		bundle := newBundle()
		if err := h.Apply(trait, app, bundle); err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
	})

	t.Run("self-service backend name, port matches component service — success", func(t *testing.T) {
		// Self-reference with correct port is allowed (redundant but valid)
		trait := &oam.Trait{
			Type: "ingress",
			Properties: map[string]any{
				"rules": []any{
					map[string]any{
						"host": "example.com",
						"paths": []any{
							map[string]any{"path": "/", "backend": "web", "port": 80},
						},
					},
				},
			},
		}
		app := newWebApp("web", "default") // port 80, service name "web"
		bundle := newBundle()
		if err := h.Apply(trait, app, bundle); err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
	})
}

// --- IngressHandler trait-level servicePort/serviceName (helmchart support) ---

func TestIngressHandler_TraitLevel_ServicePort_Success(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"servicePort": float64(8080),
			"rules": []any{
				map[string]any{
					"host":  "app.example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	// newApp creates a component with no servicePortProvider (simulates helmchart)
	app := newApp("myapp", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("expected success with trait-level servicePort, got: %v", err)
	}
	ic, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
	if !ok {
		t.Fatal("expected IngressConfig")
	}
	if ic.Rules[0].Paths[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", ic.Rules[0].Paths[0].Port)
	}
}

func TestIngressHandler_TraitLevel_ServiceNameAndPort_Success(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"serviceName": "my-chart-svc",
			"servicePort": float64(8080),
			"rules": []any{
				map[string]any{
					"host":  "app.example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	app := newApp("myapp", "default")
	bundle := newBundle()
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	ic, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
	if !ok {
		t.Fatal("expected IngressConfig")
	}
	if ic.ServiceName != "my-chart-svc" {
		t.Errorf("expected ServiceName 'my-chart-svc', got %q", ic.ServiceName)
	}
}

func TestIngressHandler_TraitLevel_ServiceNameWithoutPort_Errors(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"serviceName": "my-svc",
			"rules": []any{
				map[string]any{
					"host":  "app.example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	app := newApp("myapp", "default")
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error when serviceName set without servicePort")
	}
	if !strings.Contains(err.Error(), "serviceName requires a valid servicePort") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIngressHandler_TraitLevel_InvalidServicePort_Errors(t *testing.T) {
	h := &traits.IngressHandler{}
	for _, badPort := range []any{"oops", float64(70000), float64(0)} {
		trait := &oam.Trait{
			Type: "ingress",
			Properties: map[string]any{
				"servicePort": badPort,
				"rules": []any{
					map[string]any{
						"host":  "app.example.com",
						"paths": []any{map[string]any{"path": "/"}},
					},
				},
			},
		}
		app := newApp("myapp", "default")
		err := h.Apply(trait, app, newBundle())
		if err == nil {
			t.Fatalf("expected error for invalid servicePort %v", badPort)
		}
		if !strings.Contains(err.Error(), "valid port number") {
			t.Errorf("unexpected error for port %v: %v", badPort, err)
		}
	}
}

func TestIngressHandler_TraitLevel_ServicePort_RejectedOnKnownPortComponent(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"servicePort": float64(8080),
			"rules": []any{
				map[string]any{
					"host":  "app.example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	// newWebApp creates a component with servicePortProvider (port 80)
	app := newWebApp("myapp", "default")
	err := h.Apply(trait, app, newBundle())
	if err == nil {
		t.Fatal("expected error when servicePort set on component with known service port")
	}
	if !strings.Contains(err.Error(), "may not be set") {
		t.Errorf("unexpected error: %v", err)
	}
}
