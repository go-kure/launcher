package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func httprouteFromBundle(t *testing.T, bundle *stack.Bundle) *gatewayv1.HTTPRoute {
	t.Helper()
	if len(bundle.Applications) == 0 {
		t.Fatal("no sub-application in bundle")
	}
	objs, err := bundle.Applications[0].Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objs {
		if r, ok := (*o).(*gatewayv1.HTTPRoute); ok {
			return r
		}
	}
	t.Fatal("no HTTPRoute object generated")
	return nil
}

func httprouteTrait(extra map[string]any) *oam.Trait {
	props := map[string]any{
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{
				"path": map[string]any{"type": "PathPrefix", "value": "/"},
			}},
		}},
	}
	for k, v := range extra {
		props[k] = v
	}
	return &oam.Trait{Type: "httproute", Properties: props}
}

func TestHTTPRoute_SynthParentRefFromGateway(t *testing.T) {
	h := &traits.HTTPRouteHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	if err := h.Apply(httprouteTrait(map[string]any{"gatewayName": "public"}), app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	route := httprouteFromBundle(t, bundle)
	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("expected 1 parentRef, got %d", len(route.Spec.ParentRefs))
	}
	pr := route.Spec.ParentRefs[0]
	if string(pr.Name) != "public" {
		t.Errorf("parentRef name = %q, want public", pr.Name)
	}
	if pr.Namespace == nil || string(*pr.Namespace) != "gateway-system" {
		t.Errorf("parentRef namespace = %v, want gateway-system", pr.Namespace)
	}
}

func TestHTTPRoute_SynthParentRefRespectsNamespace(t *testing.T) {
	h := &traits.HTTPRouteHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	err := h.Apply(httprouteTrait(map[string]any{
		"gatewayName":      "public",
		"gatewayNamespace": "infra",
	}), app, bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	route := httprouteFromBundle(t, bundle)
	if pr := route.Spec.ParentRefs[0]; pr.Namespace == nil || string(*pr.Namespace) != "infra" {
		t.Errorf("parentRef namespace = %v, want infra", pr.Namespace)
	}
}

func TestHTTPRoute_UserParentRefsWin(t *testing.T) {
	h := &traits.HTTPRouteHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	err := h.Apply(httprouteTrait(map[string]any{
		"gatewayName": "public", // present, but user parentRefs take precedence
		"parentRefs":  []any{map[string]any{"name": "custom", "namespace": "other"}},
	}), app, bundle)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	route := httprouteFromBundle(t, bundle)
	if string(route.Spec.ParentRefs[0].Name) != "custom" {
		t.Errorf("user parentRef should win, got %q", route.Spec.ParentRefs[0].Name)
	}
}

func TestHTTPRoute_NoParentRefsNoGateway(t *testing.T) {
	h := &traits.HTTPRouteHandler{}
	app := newWebApp("web", "default")
	bundle := &stack.Bundle{}
	if err := h.Apply(httprouteTrait(map[string]any{}), app, bundle); err == nil {
		t.Fatal("expected error when neither parentRefs nor gatewayName is present")
	}
}
