package traits_test

import (
	stderrors "errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

const (
	kAuthURL      = "nginx.ingress.kubernetes.io/auth-url"
	kAuthSignin   = "nginx.ingress.kubernetes.io/auth-signin"
	kAuthRespHdrs = "nginx.ingress.kubernetes.io/auth-response-headers"
)

// capability auth-* values, as they'd arrive merged into the trait props.
func withCapAuth(props map[string]any) map[string]any {
	props["authURL"] = "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth"
	props["authSigninURL"] = "https://auth-proxy.example.net/oauth2/start?rd=$scheme://$host$escaped_request_uri"
	props["authResponseHeaders"] = "X-Auth-Request-User,X-Auth-Request-Email,X-Auth-Request-Groups"
	props["hostnames"] = []any{"a.apps.example.com"}
	return props
}

func TestExposeHandler_Ingress_ExtAuth_Default(t *testing.T) {
	h := &traits.ExposeHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngress(withCapAuth(map[string]any{"allowedGroups": []any{"ginsys-admins"}}))
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if got := ing.Annotations[kAuthURL]; got != "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth?allowed_groups=ginsys-admins" {
		t.Errorf("auth-url = %q", got)
	}
	if got := ing.Annotations[kAuthSignin]; got != "https://auth-proxy.example.net/oauth2/start?rd=$scheme://$host$escaped_request_uri" {
		t.Errorf("auth-signin = %q", got)
	}
	if got := ing.Annotations[kAuthRespHdrs]; got != "X-Auth-Request-User,X-Auth-Request-Email,X-Auth-Request-Groups" {
		t.Errorf("auth-response-headers = %q", got)
	}
}

func TestExposeHandler_Ingress_ExtAuth_CSVOrderPreserved(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	trait := exposeIngress(withCapAuth(map[string]any{"allowedGroups": []any{"home-users", "home-admins"}}))
	if err := h.Apply(trait, newWebApp("web", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if got := ing.Annotations[kAuthURL]; got != "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth?allowed_groups=home-users,home-admins" {
		t.Errorf("auth-url CSV order = %q, want home-users,home-admins", got)
	}
}

func TestExposeHandler_Ingress_ExtAuth_SigninOverride(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	props := withCapAuth(map[string]any{"allowedGroups": []any{"home-users"}})
	// inline override wins over the capability default (resolveCapability merges
	// rendering first, then trait props) — set it after the capability default.
	props["authSigninURL"] = "https://video.home.example.be/oauth2/start?rd=$scheme://$host$escaped_request_uri"
	if err := h.Apply(exposeIngress(props), newWebApp("web", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if got := ing.Annotations[kAuthSignin]; got != "https://video.home.example.be/oauth2/start?rd=$scheme://$host$escaped_request_uri" {
		t.Errorf("auth-signin override = %q", got)
	}
}

func TestExposeHandler_Ingress_ExtAuth_TypedBeatsAnnotation(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	props := withCapAuth(map[string]any{
		"allowedGroups": []any{"ginsys-admins"},
		"annotations":   map[string]any{kAuthURL: "http://evil.example/auth?allowed_groups=everyone"},
	})
	if err := h.Apply(exposeIngress(props), newWebApp("web", "default"), bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if got := ing.Annotations[kAuthURL]; got != "http://oauth2-proxy.oauth2-proxy.svc.cluster.local:4180/oauth2/auth?allowed_groups=ginsys-admins" {
		t.Errorf("typed auth-url must beat authored annotation, got %q", got)
	}
}

func TestExposeHandler_Ingress_ExtAuth_MissingURL_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	// allowedGroups but no capability authURL.
	trait := exposeIngress(map[string]any{
		"allowedGroups": []any{"ginsys-admins"},
		"hostnames":     []any{"a.apps.example.com"},
	})
	err := h.Apply(trait, newWebApp("web", "default"), bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("want *ValidationError (ext-auth not offered), got %v", err)
	}
}

func TestExposeHandler_Ingress_ExtAuth_EmptyGroups_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	trait := exposeIngress(withCapAuth(map[string]any{"allowedGroups": []any{}}))
	err := h.Apply(trait, newWebApp("web", "default"), bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("want *ValidationError for empty allowedGroups, got %v", err)
	}
}

func TestExposeHandler_ExtAuth_AuthURLWithQuery_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{
		"controllerType":   "ingress",
		"ingressClassName": "nginx",
		"authURL":          "http://oauth2-proxy/oauth2/auth?already=here",
	})
	if err == nil {
		t.Fatal("want error: authURL with a query string")
	}
}

func TestExposeHandler_ExtAuth_GatewayRendering_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	_, err := h.ValidateAndApplyDefaults(map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "public-gateway",
		"authURL":        "http://oauth2-proxy/oauth2/auth",
	})
	if err == nil {
		t.Fatal("want error: authURL not valid for gateway rendering")
	}
}

func TestExposeHandler_Gateway_InlineAllowedGroups_Rejected(t *testing.T) {
	h := &traits.ExposeHandler{}
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "public-gateway",
		"allowedGroups":  []any{"ginsys-admins"},
	}}
	err := h.Apply(trait, newWebApp("web", "default"), bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("want *ValidationError for inline allowedGroups on gateway, got %v", err)
	}
}
