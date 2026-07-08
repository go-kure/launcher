package traits_test

import (
	stderrors "errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func exposeIngress(props map[string]any) *oam.Trait {
	base := map[string]any{"controllerType": "ingress", "ingressClassName": "nginx"}
	for k, v := range props {
		base[k] = v
	}
	return &oam.Trait{Type: "expose", Properties: base}
}

// (a) hostnames shorthand → one rule per host, path "/" + component service port (80).
func TestExposeHandler_Ingress_HostnamesShorthand(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(map[string]any{"hostnames": []any{"a.apps.example.com"}})
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.Rules) != 1 || ing.Spec.Rules[0].Host != "a.apps.example.com" {
		t.Fatalf("rules = %+v, want single host a.apps.example.com", ing.Spec.Rules)
	}
	paths := ing.Spec.Rules[0].HTTP.Paths
	if len(paths) != 1 || paths[0].Path != "/" {
		t.Fatalf("paths = %+v, want single path /", paths)
	}
	if got := paths[0].Backend.Service.Port.Number; got != 80 {
		t.Errorf("backend port = %d, want 80 (component service port)", got)
	}
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingressClassName = %v, want nginx", ing.Spec.IngressClassName)
	}
}

// hostnames + rules together: rules drive routing (existing behavior preserved).
func TestExposeHandler_Ingress_HostnamesAndRules(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(map[string]any{
		"allowedHostnameWildcard": "*.apps.example.com",
		"hostnames":               []any{"extra.apps.example.com"},
		"rules": []any{map[string]any{
			"host":  "main.apps.example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}},
	})
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.Rules) != 1 || ing.Spec.Rules[0].Host != "main.apps.example.com" {
		t.Fatalf("rules = %+v, want routing from rules (main.apps.example.com)", ing.Spec.Rules)
	}
}

// Reviewer pin: a shorthand hostname outside the wildcard is rejected even when the
// rules carry an allowed host.
func TestExposeHandler_Ingress_HostnameOutsideWildcard_RejectedWithAllowedRule(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(map[string]any{
		"allowedHostnameWildcard": "*.apps.example.com",
		"hostnames":               []any{"bad.other.com"},
		"rules": []any{map[string]any{
			"host":  "good.apps.example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}},
	})
	err := h.Apply(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("want *ValidationError for bad.other.com, got %v", err)
	}
}

// (b) ssl-redirect: typed property (capability default or inline) → nginx annotation.
func TestExposeHandler_Ingress_SSLRedirect(t *testing.T) {
	cases := []struct {
		name      string
		props     map[string]any
		wantSSL   string // "" = annotation absent
		wantForce string
	}{
		{"default true", map[string]any{"sslRedirect": true}, "true", ""},
		{"override false", map[string]any{"sslRedirect": false}, "false", ""},
		{"force true", map[string]any{"forceSslRedirect": true}, "", "true"},
		{
			"field beats annotation",
			map[string]any{
				"sslRedirect": true,
				"annotations": map[string]any{"nginx.ingress.kubernetes.io/ssl-redirect": "false"},
			},
			"true", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &traits.ExposeHandler{}
			app := newWebApp("web", "default")
			bundle := &stack.Bundle{}
			props := map[string]any{"hostnames": []any{"a.apps.example.com"}}
			for k, v := range tc.props {
				props[k] = v
			}
			if err := h.Apply(exposeIngress(props), app, bundle); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			ing := ingressFromBundle(t, bundle)
			if got := ing.Annotations["nginx.ingress.kubernetes.io/ssl-redirect"]; got != tc.wantSSL {
				t.Errorf("ssl-redirect = %q, want %q", got, tc.wantSSL)
			}
			if got := ing.Annotations["nginx.ingress.kubernetes.io/force-ssl-redirect"]; got != tc.wantForce {
				t.Errorf("force-ssl-redirect = %q, want %q", got, tc.wantForce)
			}
		})
	}
}

// (c) coverage: capability-rendered ingressClassName lands on spec.ingressClassName.
func TestExposeHandler_Ingress_IngressClassName(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(map[string]any{"hostnames": []any{"a.apps.example.com"}})
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingressClassName = %v, want nginx", ing.Spec.IngressClassName)
	}
}

// ssl-redirect fields are ingress-only: rejected on the gateway rendering.
func TestExposeHandler_Gateway_SSLRedirect_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "public-gateway",
		"sslRedirect":    true,
	})
	if err == nil {
		t.Fatal("want error: sslRedirect not valid for gateway")
	}
}

// Inline ssl-redirect on a gateway expose trait is rejected at Apply time (the
// rendering guard only covers the capability-supplied form).
func TestExposeHandler_Gateway_InlineSSLRedirect_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "public-gateway",
		"sslRedirect":    true,
	}}
	err := h.Apply(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("want *ValidationError for inline sslRedirect on gateway, got %v", err)
	}
}

// Managed TLS covers the effective routing hosts only: with both rules and hostnames,
// a hostnames entry that is not routed by rules must not get a synthesized cert.
func TestExposeHandler_Ingress_ManagedTLS_RoutingHostsOnly(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(map[string]any{
		"certManagerClusterIssuer": "letsencrypt",
		"allowedHostnameWildcard":  "*.apps.example.com",
		"hostnames":                []any{"extra.apps.example.com"}, // validated, not routed
		"rules": []any{map[string]any{
			"host":  "main.apps.example.com",
			"paths": []any{map[string]any{"path": "/"}},
		}},
	})
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.TLS) != 1 || len(ing.Spec.TLS[0].Hosts) != 1 || ing.Spec.TLS[0].Hosts[0] != "main.apps.example.com" {
		t.Fatalf("TLS = %+v, want a single entry for main.apps.example.com only (extra.apps.example.com not routed)", ing.Spec.TLS)
	}
}
