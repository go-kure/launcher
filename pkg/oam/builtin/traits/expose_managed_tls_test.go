package traits_test

import (
	stderrors "errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// ingressFromBundle generates the first sub-application and returns it as an Ingress.
func ingressFromBundle(t *testing.T, bundle *stack.Bundle) *networkingv1.Ingress {
	t.Helper()
	if len(bundle.Applications) == 0 {
		t.Fatal("no sub-application in bundle")
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objs {
		if ing, ok := (*o).(*networkingv1.Ingress); ok {
			return ing
		}
	}
	t.Fatal("no Ingress object generated")
	return nil
}

func exposeIngressTrait(issuer, wildcard string, hosts ...string) *oam.Trait {
	rules := make([]any, 0, len(hosts))
	for _, h := range hosts {
		rules = append(rules, map[string]any{
			"host":  h,
			"paths": []any{map[string]any{"path": "/"}},
		})
	}
	props := map[string]any{
		"controllerType":   "ingress",
		"ingressClassName": "nginx",
		"rules":            rules,
	}
	if issuer != "" {
		props["certManagerClusterIssuer"] = issuer
	}
	if wildcard != "" {
		props["allowedHostnameWildcard"] = wildcard
	}
	return &oam.Trait{Type: "expose", Properties: props}
}

func TestExposeHandler_Apply_IngressManagedTLS(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	// Two rules, one duplicate host — synthesized TLS must dedupe.
	trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com", "a.apps.example.com", "b.apps.example.com")
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)

	if got := ing.Annotations["cert-manager.io/cluster-issuer"]; got != "letsencrypt-prod" {
		t.Errorf("cluster-issuer annotation = %q, want letsencrypt-prod", got)
	}
	if len(ing.Spec.TLS) != 1 {
		t.Fatalf("expected 1 TLS entry, got %d", len(ing.Spec.TLS))
	}
	tls := ing.Spec.TLS[0]
	if tls.SecretName != "my-app-tls" {
		t.Errorf("secretName = %q, want my-app-tls", tls.SecretName)
	}
	if len(tls.Hosts) != 2 || tls.Hosts[0] != "a.apps.example.com" || tls.Hosts[1] != "b.apps.example.com" {
		t.Errorf("TLS hosts = %v, want [a.apps.example.com b.apps.example.com]", tls.Hosts)
	}
}

func TestExposeHandler_Apply_IngressNoIssuer_NoTLS(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	if err := h.Apply(exposeIngressTrait("", "", "a.apps.example.com"), app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.TLS) != 0 {
		t.Errorf("expected no TLS, got %v", ing.Spec.TLS)
	}
	if _, ok := ing.Annotations["cert-manager.io/cluster-issuer"]; ok {
		t.Error("expected no cluster-issuer annotation")
	}
}

func TestExposeHandler_Apply_IngressUserTLSIgnored(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
	// User attempts to author TLS directly — expose must ignore it.
	trait.Properties["tls"] = []any{map[string]any{
		"hosts": []any{"evil.example.com"}, "secretName": "user-secret",
	}}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "my-app-tls" {
		t.Errorf("user TLS not overridden: %+v", ing.Spec.TLS)
	}
}

func TestExposeHandler_Apply_IngressHostnameRejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("", "*.apps.example.com", "not-allowed.example.com")
	err := h.Apply(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %v", err)
	}
}

func TestExposeHandler_Apply_IngressHostnameAllowed(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("", "*.apps.example.com", "foo.apps.example.com")
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestExposeHandler_Apply_ClusterIssuerCollision(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
	trait.Properties["annotations"] = map[string]any{
		"cert-manager.io/cluster-issuer": "self-signed", // conflicts with platform
	}
	err := h.Apply(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError for annotation collision, got %v", err)
	}
}

func TestExposeHandler_Apply_GatewayHostnameRejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType":          "gateway",
		"gatewayName":             "public",
		"allowedHostnameWildcard": "*.apps.example.com",
		"hostnames":               []any{"bad.example.com"},
		"rules":                   []any{map[string]any{}},
	}}
	err := h.Apply(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError for gateway hostname, got %v", err)
	}
}

func TestExposeHandler_Apply_GatewayNoTLS(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType":          "gateway",
		"gatewayName":             "public",
		"allowedHostnameWildcard": "*.apps.example.com",
		"hostnames":               []any{"ok.apps.example.com"},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}},
		}},
	}}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Gateway path emits an HTTPRoute, never an Ingress.
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objs {
		if _, ok := (*o).(*networkingv1.Ingress); ok {
			t.Error("gateway path must not emit an Ingress")
		}
	}
}
