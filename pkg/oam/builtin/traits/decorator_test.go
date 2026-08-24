package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
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

// augmenterStub implements layout.LayoutAugmenter in addition to Generate.
type augmenterStub struct{ called *bool }

func (s *augmenterStub) Generate(app *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *augmenterStub) AugmentLayout(l *layout.ManifestLayout) error {
	*s.called = true
	return nil
}

func TestConfigMapDecorator_ForwardsLayoutAugmenter(t *testing.T) {
	called := false
	dec := traits.NewConfigMapDecorator(&augmenterStub{called: &called}, "c", "/etc/c")

	aug, ok := dec.(interface {
		AugmentLayout(l *layout.ManifestLayout) error
	})
	if !ok {
		t.Fatal("wrapped config does not implement LayoutAugmenter")
	}
	if err := aug.AugmentLayout(&layout.ManifestLayout{}); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	if !called {
		t.Error("inner AugmentLayout was not called")
	}
}

// TestWrapIfAugmenter_AllConstructionSites guards the three other wrapIfAugmenter
// call sites this task adds — ConfigMapDecorator's own site is already covered by
// TestConfigMapDecorator_ForwardsLayoutAugmenter above. Skipping the wrapIfAugmenter
// call at any one of these (e.g. leaving a bare struct literal assignment) would
// leave that site's decorator unable to satisfy LayoutAugmenter even when its inner
// implements it, and none of the tests above would notice.
func TestWrapIfAugmenter_AllConstructionSites(t *testing.T) {
	assertForwards := func(t *testing.T, cfg stack.ApplicationConfig, called *bool) {
		t.Helper()
		aug, ok := cfg.(interface {
			AugmentLayout(l *layout.ManifestLayout) error
		})
		if !ok {
			t.Fatal("wrapped config does not implement LayoutAugmenter")
		}
		if err := aug.AugmentLayout(&layout.ManifestLayout{}); err != nil {
			t.Fatalf("AugmentLayout: %v", err)
		}
		if !*called {
			t.Error("inner AugmentLayout was not called")
		}
	}

	t.Run("NewExternalSecretDecorator", func(t *testing.T) {
		called := false
		dec := traits.NewExternalSecretDecorator(&augmenterStub{called: &called}, "s", "", false)
		assertForwards(t, dec, &called)
	})

	t.Run("SecurityContextHandler", func(t *testing.T) {
		called := false
		app := stack.NewApplication("app", "default", &augmenterStub{called: &called})
		if err := (&traits.SecurityContextHandler{}).Apply(
			&oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "restricted"}},
			app, &stack.Bundle{}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertForwards(t, app.Config, &called)
	})

	t.Run("PruneProtectionHandler", func(t *testing.T) {
		called := false
		app := stack.NewApplication("app", "default", &augmenterStub{called: &called})
		if err := (&traits.PruneProtectionHandler{}).Apply(
			&oam.Trait{Type: "prune-protection"}, app, &stack.Bundle{}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertForwards(t, app.Config, &called)
	})

	// NewConfigMapDecorator/RealHelmchart* below is the end-to-end proof that
	// presence-based forwarding fires for a production component (a real
	// valuesMode: configMap helmchart config), not just augmenterStub. See
	// TestConfigMapDecorator_ForwardsLayoutAugmenter above for the stub-based
	// coverage of this same construction site.
	t.Run("NewConfigMapDecorator/RealHelmchartConfigMapValues", func(t *testing.T) {
		h := &components.HelmchartHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: "metrics",
			Type: "helmchart",
			Properties: map[string]any{
				"chart":      "kube-prometheus-stack",
				"valuesMode": "configMap",
				"values":     map[string]any{"replicaCount": 3},
				"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
			},
		}, "monitoring")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}

		dec := traits.NewConfigMapDecorator(cfg, "c", "/etc/c")
		aug, ok := dec.(interface {
			AugmentLayout(l *layout.ManifestLayout) error
		})
		if !ok {
			t.Fatal("decorator wrapping a real configMap-mode helmchart config does not implement LayoutAugmenter")
		}
		ml := &layout.ManifestLayout{}
		if err := aug.AugmentLayout(ml); err != nil {
			t.Fatalf("AugmentLayout: %v", err)
		}
		if len(ml.Resources) != 1 {
			t.Errorf("ml.Resources has %d entries, want 1 (the generated values ConfigMap)", len(ml.Resources))
		}
	})

	// RealHelmchartInlineDoesNotWrap mirrors the negative guard in
	// TestConfigMapDecorator_DoesNotClaimLayoutAugmenter_WhenInnerDoesNot,
	// but with a real inline-mode helmchart config instead of nakedStub: the
	// regression this guards against would silently relocate every
	// flat-bundle helmchart app into per-app sub-layout placement.
	t.Run("NewConfigMapDecorator/RealHelmchartInlineDoesNotWrap", func(t *testing.T) {
		h := &components.HelmchartHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: "metrics",
			Type: "helmchart",
			Properties: map[string]any{
				"chart":  "kube-prometheus-stack",
				"values": map[string]any{"replicaCount": 3},
				"source": map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
			},
		}, "monitoring")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}

		dec := traits.NewConfigMapDecorator(cfg, "c", "/etc/c")
		if _, ok := dec.(interface {
			AugmentLayout(l *layout.ManifestLayout) error
		}); ok {
			t.Fatal("decorator wrapping an inline-mode (no-op augmenter) helmchart config must not implement LayoutAugmenter")
		}
	})

	// The following three subtests guard augmentingDecorator's unconditional
	// GenerateCoversAugmentLayout forward (step 8): unlike AugmentLayout's own
	// forward, this one must come through regardless of what the inner
	// augmenter reports, so pkg/cmd/kurel's build guard sees the same answer
	// through a decorator as it would through the bare inner config.
	assertCoverage := func(t *testing.T, cfg stack.ApplicationConfig, want bool) {
		t.Helper()
		cov, ok := cfg.(interface{ GenerateCoversAugmentLayout() bool })
		if !ok {
			t.Fatal("wrapped config does not implement GenerateCoversAugmentLayout")
		}
		if got := cov.GenerateCoversAugmentLayout(); got != want {
			t.Errorf("GenerateCoversAugmentLayout() = %v, want %v", got, want)
		}
	}

	t.Run("NewConfigMapDecorator/RealHelmchartTemplateDelivery_CoversAugmentLayout", func(t *testing.T) {
		h := &components.HelmchartHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: "myapp",
			Type: "helmchart",
			Properties: map[string]any{
				"chart":    "myapp",
				"delivery": "template",
				"source":   map[string]any{"url": "https://charts.example.com"},
			},
		}, "default")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		dec := traits.NewConfigMapDecorator(cfg, "c", "/etc/c")
		if _, ok := dec.(interface {
			AugmentLayout(l *layout.ManifestLayout) error
		}); !ok {
			t.Fatal("decorator wrapping a real template-delivery helmchart config does not implement LayoutAugmenter")
		}
		assertCoverage(t, dec, true)
	})

	t.Run("NewConfigMapDecorator/RealHelmchartConfigMapValues_DoesNotCoverAugmentLayout", func(t *testing.T) {
		h := &components.HelmchartHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: "metrics",
			Type: "helmchart",
			Properties: map[string]any{
				"chart":      "kube-prometheus-stack",
				"valuesMode": "configMap",
				"values":     map[string]any{"replicaCount": 3},
				"source":     map[string]any{"url": "https://prometheus-community.github.io/helm-charts"},
			},
		}, "monitoring")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		dec := traits.NewConfigMapDecorator(cfg, "c", "/etc/c")
		assertCoverage(t, dec, false)
	})

	t.Run("NewConfigMapDecorator/AugmenterStubDoesNotOptIn", func(t *testing.T) {
		called := false
		dec := traits.NewConfigMapDecorator(&augmenterStub{called: &called}, "c", "/etc/c")
		assertCoverage(t, dec, false)
	})
}

// TestConfigMapDecorator_DoesNotClaimLayoutAugmenter_WhenInnerDoesNot guards the
// presence-based placement decision in kure's layout walker: a decorator wrapping
// a non-augmenting inner must NOT satisfy LayoutAugmenter, or every decorated
// component would be forced into per-app sub-layout placement regardless of what
// its inner config wants.
func TestConfigMapDecorator_DoesNotClaimLayoutAugmenter_WhenInnerDoesNot(t *testing.T) {
	dec := traits.NewConfigMapDecorator(&nakedStub{}, "c", "/etc/c")
	if _, ok := dec.(interface {
		AugmentLayout(l *layout.ManifestLayout) error
	}); ok {
		t.Fatal("decorator wrapping a non-augmenting inner must not implement LayoutAugmenter")
	}
}

// augmentingSvcStub implements layout.LayoutAugmenter AND the service
// interfaces, to prove wrapIfAugmenter's wrapping does not drop the
// decoratorBase forwards it must preserve (see decoratedConfig in Step 3).
type augmentingSvcStub struct {
	port    int32
	svcName string
}

func (s *augmentingSvcStub) Generate(app *stack.Application) ([]*client.Object, error) {
	return nil, nil
}
func (s *augmentingSvcStub) AugmentLayout(l *layout.ManifestLayout) error { return nil }
func (s *augmentingSvcStub) ServicePort() int32                           { return s.port }
func (s *augmentingSvcStub) BackendServiceName() string                   { return s.svcName }

// TestConfigMapDecorator_PreservesServiceForwardsWhenAugmented guards against
// augmentingDecorator embedding the narrower stack.ApplicationConfig instead
// of decoratedConfig: that mistake would make wrapIfAugmenter's wrapped
// result implement AugmentLayout while silently losing ServicePort and
// BackendServiceName (and Validate, SetFluxNamespace, EmitsAutoHealthCheck),
// reintroducing Task 1's bug for exactly the components this task adds
// LayoutAugmenter support for.
func TestConfigMapDecorator_PreservesServiceForwardsWhenAugmented(t *testing.T) {
	dec := traits.NewConfigMapDecorator(&augmentingSvcStub{port: 8080, svcName: "web-svc"}, "c", "/etc/c")

	if _, ok := dec.(interface {
		AugmentLayout(l *layout.ManifestLayout) error
	}); !ok {
		t.Fatal("wrapped config does not implement LayoutAugmenter")
	}
	pp, ok := dec.(interface{ ServicePort() int32 })
	if !ok {
		t.Fatal("wrapped config lost ServicePort after augmenting wrap")
	}
	if got := pp.ServicePort(); got != 8080 {
		t.Errorf("ServicePort() = %d, want 8080", got)
	}
	sn, ok := dec.(interface{ BackendServiceName() string })
	if !ok {
		t.Fatal("wrapped config lost BackendServiceName after augmenting wrap")
	}
	if got := sn.BackendServiceName(); got != "web-svc" {
		t.Errorf("BackendServiceName() = %q, want %q", got, "web-svc")
	}
	// augmentingSvcStub implements none of Validator/fluxNamespaceSettable/
	// autoHealthCheckEmitter, so these three assert only that decoratedConfig
	// still declares them (the type assertion succeeds) and that decoratorBase's
	// defaults still come through — not that augmentingSvcStub itself implements
	// them. A decoratedConfig missing any of the five would fail its assertion.
	if v, ok := dec.(interface{ Validate() error }); !ok {
		t.Fatal("wrapped config lost Validate after augmenting wrap")
	} else if err := v.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for a non-Validator inner", err)
	}
	if s, ok := dec.(interface{ SetFluxNamespace(string) }); !ok {
		t.Fatal("wrapped config lost SetFluxNamespace after augmenting wrap")
	} else {
		s.SetFluxNamespace("ns") // must not panic for a non-settable inner
	}
	if e, ok := dec.(interface{ EmitsAutoHealthCheck() bool }); !ok {
		t.Fatal("wrapped config lost EmitsAutoHealthCheck after augmenting wrap")
	} else if !e.EmitsAutoHealthCheck() {
		t.Error("EmitsAutoHealthCheck() = false, want true (default for a non-implementing inner)")
	}
}
