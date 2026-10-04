package traits_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	pkgerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam"
)

// Authored secretName overrides the synthesized <component>-tls name; hosts and the
// cluster-issuer annotation are unchanged, and secretName does not leak downstream.
func TestExposeRule_Ingress_SecretNameOverride(t *testing.T) {
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
	trait.Properties["secretName"] = "custom-tls"
	if err := applyExpose(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if len(ing.Spec.TLS) != 1 {
		t.Fatalf("expected 1 TLS entry, got %d", len(ing.Spec.TLS))
	}
	if ing.Spec.TLS[0].SecretName != "custom-tls" {
		t.Errorf("secretName = %q, want custom-tls", ing.Spec.TLS[0].SecretName)
	}
	if got := ing.Annotations["cert-manager.io/cluster-issuer"]; got != "letsencrypt-prod" {
		t.Errorf("cluster-issuer annotation = %q, want letsencrypt-prod", got)
	}
	if len(ing.Spec.TLS[0].Hosts) != 1 || ing.Spec.TLS[0].Hosts[0] != "a.apps.example.com" {
		t.Errorf("TLS hosts = %v, want [a.apps.example.com]", ing.Spec.TLS[0].Hosts)
	}
}

// No secretName → default <component>-tls (regression guard).
func TestExposeRule_Ingress_SecretNameDefault(t *testing.T) {
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	if err := applyExpose(exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com"), app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ing := ingressFromBundle(t, bundle)
	if ing.Spec.TLS[0].SecretName != "my-app-tls" {
		t.Errorf("secretName = %q, want my-app-tls (default)", ing.Spec.TLS[0].SecretName)
	}
}

// secretName on the gateway path is rejected (ingress-only), like sslRedirect/allowedGroups.
func TestExposeRule_Gateway_SecretNameRejected(t *testing.T) {
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := &oam.Trait{Type: "expose", Properties: map[string]any{
		"controllerType": "gateway",
		"gatewayName":    "public",
		"hostnames":      []any{"a.apps.example.com"},
		"secretName":     "custom-tls",
	}}
	err := applyExpose(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError for secretName on gateway, got %v", err)
	}
	if !strings.Contains(err.Error(), "secretName") {
		t.Errorf("error = %q, want mention of secretName", err.Error())
	}
}

// secretName without a cluster-issuer capability is rejected (else silently dropped).
func TestExposeRule_Ingress_SecretNameNoIssuerRejected(t *testing.T) {
	app := newWebApp("my-app", "default")
	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("", "", "a.apps.example.com") // no issuer
	trait.Properties["secretName"] = "custom-tls"
	err := applyExpose(trait, app, bundle)
	var ve *pkgerrors.ValidationError
	if !stderrors.As(err, &ve) {
		t.Fatalf("expected *ValidationError for secretName without issuer, got %v", err)
	}
	if !strings.Contains(err.Error(), "secretName") {
		t.Errorf("error = %q, want mention of secretName", err.Error())
	}
}

// An authored secretName names the managed TLS Secret: one that cannot be a
// Secret's name is refused with the component and the property, not passed on
// for cert-manager to fail on; the longest valid name is used as written
// (go-kure/launcher#787).
func TestExposeRule_Ingress_SecretNameMustBeAnObjectName(t *testing.T) {
	fits := strings.Repeat("a", oam.ShortenLimitSubdomain)
	for why, name := range map[string]string{
		"too long":          fits + "a",
		"upper case":        "Custom-TLS",
		"underscore":        "custom_tls",
		"trailing hyphen":   "custom-",
		"path-like":         "team/custom-tls",
		"empty DNS label":   "custom..tls",
		"surrounding space": " custom-tls",
	} {
		t.Run(why, func(t *testing.T) {
			trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
			trait.Properties["secretName"] = name
			err := applyExpose(trait, newWebApp("my-app", "default"), &stack.Bundle{})
			var ve *pkgerrors.ValidationError
			if !stderrors.As(err, &ve) {
				t.Fatalf("expected *ValidationError for secretName=%q, got %v", name, err)
			}
			if ve.Field != "secretName" || ve.Component != "my-app" {
				t.Errorf("error names field %q of component %q, want secretName of my-app", ve.Field, ve.Component)
			}
			if !strings.Contains(err.Error(), "DNS-1123 subdomain") {
				t.Errorf("error = %q, want the rule the name breaks", err.Error())
			}
		})
	}

	bundle := &stack.Bundle{}
	trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
	trait.Properties["secretName"] = fits
	if err := applyExpose(trait, newWebApp("my-app", "default"), bundle); err != nil {
		t.Fatalf("Apply with a %d-character secretName: %v", len(fits), err)
	}
	if got := ingressFromBundle(t, bundle).Spec.TLS[0].SecretName; got != fits {
		t.Errorf("secretName = %q, want the authored value unchanged", got)
	}
}

// Present-but-wrong-typed / empty secretName is rejected, not silently defaulted to
// <component>-tls (the go-kure/launcher#199 lesson: absence-defaulting + lenient parse hides a typo).
func TestExposeRule_Ingress_SecretNameWrongType(t *testing.T) {
	cases := map[string]any{"int": 12345, "empty": ""}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			app := newWebApp("my-app", "default")
			bundle := &stack.Bundle{}
			trait := exposeIngressTrait("letsencrypt-prod", "", "a.apps.example.com")
			trait.Properties["secretName"] = val
			err := applyExpose(trait, app, bundle)
			var ve *pkgerrors.ValidationError
			if !stderrors.As(err, &ve) {
				t.Fatalf("expected *ValidationError for secretName=%v, got %v", val, err)
			}
		})
	}
}
