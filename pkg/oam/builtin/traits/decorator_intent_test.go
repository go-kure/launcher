package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// intentStub is a layout.LayoutIntentAugmenter: Generate emits two ConfigMaps,
// AugmentLayout records that it ran, and WantsOwnLayout answers wants.
type intentStub struct {
	wants  bool
	called *bool
}

func (s *intentStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	a, b := newTestConfigMap("intent-a"), newTestConfigMap("intent-b")
	return []*client.Object{&a, &b}, nil
}

func (s *intentStub) AugmentLayout(_ *layout.ManifestLayout) error {
	*s.called = true
	return nil
}

func (s *intentStub) WantsOwnLayout() bool { return s.wants }

func assertIntent(t *testing.T, cfg stack.ApplicationConfig, want bool) {
	t.Helper()
	intent, ok := cfg.(layout.LayoutIntentAugmenter)
	if !ok {
		t.Fatalf("decorated config %T does not implement layout.LayoutIntentAugmenter", cfg)
	}
	if got := intent.WantsOwnLayout(); got != want {
		t.Errorf("WantsOwnLayout() = %v, want %v", got, want)
	}
}

// TestDecorators_ForwardLayoutIntent: a trait decorator keeps the inner
// config's layout.LayoutIntentAugmenter answer, and gains the method only when
// the inner has it (go-kure/launcher#718).
func TestDecorators_ForwardLayoutIntent(t *testing.T) {
	t.Run("ConfigMap/false", func(t *testing.T) {
		called := false
		assertIntent(t, traits.NewConfigMapDecorator(&intentStub{called: &called}, "c", "/etc/c"), false)
	})

	t.Run("ConfigMap/true", func(t *testing.T) {
		called := false
		assertIntent(t, traits.NewConfigMapDecorator(&intentStub{wants: true, called: &called}, "c", "/etc/c"), true)
	})

	t.Run("Chain/false", func(t *testing.T) {
		called := false
		app := stack.NewApplication("app", "ns", traits.NewConfigMapDecorator(&intentStub{called: &called}, "c", "/etc/c"))
		applyPruneProtection(t, app)
		assertIntent(t, app.Config, false)
	})

	t.Run("PlainAugmenterGainsNoIntent", func(t *testing.T) {
		called := false
		dec := traits.NewConfigMapDecorator(&augmenterStub{called: &called}, "c", "/etc/c")
		if _, ok := dec.(layout.LayoutIntentAugmenter); ok {
			t.Error("a decorator wrapping an augmenter without WantsOwnLayout gained it")
		}
		if _, ok := dec.(layout.LayoutAugmenter); !ok {
			t.Error("the decorator lost layout.LayoutAugmenter")
		}
	})
}

// TestPruneProtection_KeepsFlatPlacementOfIntentAugmenter is the issue's
// reproduction: under ApplicationGrouping flat, an augmenter that does not
// want its own layout is merged into its bundle's layout and is not augmented.
// A decorator that drops WantsOwnLayout turns that into a child layout of its
// own, augmented.
func TestPruneProtection_KeepsFlatPlacementOfIntentAugmenter(t *testing.T) {
	called := false
	app := stack.NewApplication("intent", "ns", &intentStub{called: &called})
	applyPruneProtection(t, app)
	cluster := &stack.Cluster{Name: "c", Node: &stack.Node{
		Name:   "root",
		Bundle: &stack.Bundle{Name: "root", Applications: []*stack.Application{app}},
	}}

	ml, err := layout.WalkCluster(cluster, layout.DefaultLayoutRules())
	if err != nil {
		t.Fatalf("WalkCluster: %v", err)
	}
	var holding []*layout.ManifestLayout
	var walk func(*layout.ManifestLayout)
	walk = func(l *layout.ManifestLayout) {
		if l.Name == app.Name {
			t.Errorf("the walker gave application %q a layout of its own", app.Name)
		}
		for _, o := range l.Resources {
			if o.GetName() == "intent-a" || o.GetName() == "intent-b" {
				holding = append(holding, l)
				break
			}
		}
		for _, c := range l.Children {
			walk(c)
		}
	}
	walk(ml)
	if len(holding) != 1 || len(holding[0].Resources) != 2 {
		t.Errorf("the application's two objects sit in %d layouts, want one holding both", len(holding))
	}
	if called {
		t.Error("AugmentLayout ran on an application that does not want its own layout")
	}
}

// intentSubAppHandler is a trait that appends one sub-application whose
// config is an intentStub answering false.
type intentSubAppHandler struct{ called *bool }

func (intentSubAppHandler) CanHandle(t string) bool { return t == "intent-sub" }

func (h intentSubAppHandler) Apply(_ *oam.Trait, app *stack.Application, bundle *stack.Bundle) error {
	bundle.Applications = append(bundle.Applications,
		stack.NewApplication(app.Name+"-intent", app.Namespace, &intentStub{called: h.called}))
	return nil
}

// TestPruneProtection_SubApplicationKeepsLayoutIntent covers the engine's
// sub-application pass: prune-protection decorating a trait sub-application
// whose config is a LayoutIntentAugmenter keeps its answer.
func TestPruneProtection_SubApplicationKeepsLayoutIntent(t *testing.T) {
	called := false
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{
		"intent-sub":       intentSubAppHandler{called: &called},
		"prune-protection": &traits.PruneProtectionHandler{},
	})
	tr.RegisterComponent("deployment", &components.DeploymentHandler{})
	cluster, err := tr.Transform(&oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{Components: []oam.Component{{
			Name:       "web",
			Type:       "deployment",
			Properties: map[string]any{"image": "nginx:1.25"},
			Traits:     []oam.Trait{{Type: "intent-sub"}, {Type: "prune-protection"}},
		}}},
	}, oam.TransformContext{Namespace: "default"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	var sub *stack.Application
	var walk func(*stack.Node)
	walk = func(n *stack.Node) {
		if n == nil {
			return
		}
		if n.Bundle != nil {
			for _, a := range n.Bundle.Applications {
				if a.Name == "web-intent" {
					sub = a
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(cluster.Node)
	if sub == nil {
		t.Fatal("no web-intent sub-application in the cluster")
	}
	if _, bare := sub.Config.(*intentStub); bare {
		t.Fatal("prune-protection did not decorate the sub-application; the intent assertion below would be vacuous")
	}
	assertIntent(t, sub.Config, false)
}
