package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// deploymentApp builds a stack.Application backed by a real DeploymentConfig,
// through the handler the CLI dispatches. The existing no-service-port tests in
// traits_test.go use a bare nil config, which proves the trait's behaviour but
// says nothing about this component kind — and the kind is the point: unlike
// webservice it publishes no `port` and emits no Service, so it can never
// satisfy servicePortProvider (go-kure/launcher#343).
func deploymentApp(t *testing.T) *stack.Application {
	t.Helper()
	h := &components.DeploymentHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name:       "api",
		Type:       "deployment",
		Properties: map[string]any{"image": "nginx:1.27"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	return stack.NewApplication("api", "default", cfg)
}

// TestIngressHandler_Apply_DeploymentImplicitBackendErrors pins the first half
// of the README's "routing traits on a deployment need an explicit Service"
// rule: with no backend named on the trait, the ingress resolves the backend
// from the component's own service port, which this kind does not have.
func TestIngressHandler_Apply_DeploymentImplicitBackendErrors(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"ingressClassName": "nginx",
			"rules": []any{
				map[string]any{
					"host":  "example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	err := h.Apply(trait, deploymentApp(t), &stack.Bundle{})
	if err == nil {
		t.Fatal("expected an implicitly-backed ingress on a deployment component to be rejected")
	}
	if !strings.Contains(err.Error(), "no service port") {
		t.Errorf("expected a 'no service port' error, got: %v", err)
	}
}

// TestIngressHandler_Apply_DeploymentExplicitBackendRoutes pins the second
// half: naming the target Service with serviceName + servicePort routes to a
// Service this component does not create, which is what the README tells
// authors to do.
func TestIngressHandler_Apply_DeploymentExplicitBackendRoutes(t *testing.T) {
	h := &traits.IngressHandler{}
	trait := &oam.Trait{
		Type: "ingress",
		Properties: map[string]any{
			"ingressClassName": "nginx",
			"serviceName":      "api-svc",
			"servicePort":      8080,
			"rules": []any{
				map[string]any{
					"host":  "example.com",
					"paths": []any{map[string]any{"path": "/"}},
				},
			},
		},
	}
	app := deploymentApp(t)
	bundle := &stack.Bundle{}
	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("explicit serviceName+servicePort should route on a deployment component: %v", err)
	}

	if len(bundle.Applications) != 1 {
		t.Fatalf("expected the trait to add 1 application to the bundle, got %d", len(bundle.Applications))
	}
	cfg, ok := bundle.Applications[0].Config.(*traits.IngressConfig)
	if !ok {
		t.Fatalf("expected *traits.IngressConfig, got %T", bundle.Applications[0].Config)
	}
	objs, err := cfg.Generate(stack.NewApplication("api-ingress", "default", nil))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 generated object, got %d", len(objs))
	}
	ing, ok := (*objs[0]).(*networkingv1.Ingress)
	if !ok {
		t.Fatalf("expected *networkingv1.Ingress, got %T", *objs[0])
	}
	svc := ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	if svc == nil {
		t.Fatal("ingress backend has no Service reference")
	}
	if svc.Name != "api-svc" || svc.Port.Number != 8080 {
		t.Errorf("backend = %s:%d, want api-svc:8080", svc.Name, svc.Port.Number)
	}
}
