package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// svcStub is a component config that owns a Service, like a webservice.
type svcStub struct {
	port    int32
	svcName string
}

func (s *svcStub) Generate(app *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *svcStub) ServicePort() int32                                        { return s.port }
func (s *svcStub) BackendServiceName() string                                { return s.svcName }

func TestConfigMapDecorator_ForwardsServiceInterfaces(t *testing.T) {
	app := stack.NewApplication("web", "default", &svcStub{port: 8080, svcName: "web-svc"})
	h := &traits.ConfigMapHandler{}
	tr := &oam.Trait{Type: "configmap", Properties: map[string]any{
		"name": "web-config", "mountPath": "/etc/config",
	}}
	if err := h.Apply(tr, app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	pp, ok := app.Config.(interface{ ServicePort() int32 })
	if !ok {
		t.Fatal("wrapped config does not implement ServicePort")
	}
	if got := pp.ServicePort(); got != 8080 {
		t.Errorf("ServicePort() = %d, want 8080", got)
	}

	sn, ok := app.Config.(interface{ BackendServiceName() string })
	if !ok {
		t.Fatal("wrapped config does not implement BackendServiceName")
	}
	if got := sn.BackendServiceName(); got != "web-svc" {
		t.Errorf("BackendServiceName() = %q, want %q", got, "web-svc")
	}
}

// nakedStub implements only Generate — no optional interfaces.
type nakedStub struct{}

func (n *nakedStub) Generate(app *stack.Application) ([]*client.Object, error) { return nil, nil }

func TestDecoratorBase_DefaultsForNonImplementingInner(t *testing.T) {
	app := stack.NewApplication("worker", "default", &nakedStub{})
	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{"name": "c", "mountPath": "/etc/c"}},
		app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if v, ok := app.Config.(interface{ Validate() error }); !ok {
		t.Fatal("no Validate")
	} else if err := v.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for a non-Validator inner", err)
	}
	if p, ok := app.Config.(interface{ ServicePort() int32 }); !ok {
		t.Fatal("no ServicePort")
	} else if p.ServicePort() != 0 {
		t.Errorf("ServicePort() = %d, want 0", p.ServicePort())
	}
	if n, ok := app.Config.(interface{ BackendServiceName() string }); !ok {
		t.Fatal("no BackendServiceName")
	} else if n.BackendServiceName() != "" {
		t.Errorf("BackendServiceName() = %q, want empty", n.BackendServiceName())
	}
}

// TestConfigMapThenIngress_UsesComponentNameAsBackend is the guard for Step 5: an
// always-present BackendServiceName() returning "" must not shadow app.Name.
func TestConfigMapThenIngress_UsesComponentNameAsBackend(t *testing.T) {
	app := stack.NewApplication("web", "default", &svcStub{port: 8080})
	bundle := &stack.Bundle{}
	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{"name": "c", "mountPath": "/etc/c"}},
		app, bundle); err != nil {
		t.Fatalf("configmap Apply: %v", err)
	}
	if err := (&traits.IngressHandler{}).Apply(
		&oam.Trait{Type: "ingress", Properties: map[string]any{
			"rules": []any{map[string]any{
				"host":  "web.example.com",
				"paths": []any{map[string]any{"path": "/"}},
			}},
		}}, app, bundle); err != nil {
		t.Fatalf("ingress Apply after configmap: %v", err)
	}

	// The ingress trait appended its sub-app to bundle.Applications; find it and
	// generate its resources to inspect the emitted backend service name.
	var ingressApp *stack.Application
	for _, a := range bundle.Applications {
		if _, ok := a.Config.(*traits.IngressConfig); ok {
			ingressApp = a
		}
	}
	if ingressApp == nil {
		t.Fatal("no IngressConfig sub-app found in bundle")
	}
	objs, err := ingressApp.Config.Generate(ingressApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ingress, ok := (*objs[0]).(*networkingv1.Ingress)
	if !ok {
		t.Fatalf("expected *networkingv1.Ingress, got %T", *objs[0])
	}
	gotName := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name
	if gotName != "web" {
		t.Errorf("backend.service.name = %q, want %q", gotName, "web")
	}
}
