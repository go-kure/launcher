package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// addingAugmenterStub is a component config whose Generate emits one
// ConfigMap and whose AugmentLayout adds resources Generate never returned:
// one appended to ml.Resources and one inside a new child layout — the two
// places an augmenter can put a resource of its own.
type addingAugmenterStub struct{}

func newTestConfigMap(name string) client.Object {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
	}
}

func (s *addingAugmenterStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	obj := newTestConfigMap("generated")
	return []*client.Object{&obj}, nil
}

func (s *addingAugmenterStub) AugmentLayout(ml *layout.ManifestLayout) error {
	ml.Resources = append(ml.Resources, newTestConfigMap("augmented"))
	ml.Children = append(ml.Children, &layout.ManifestLayout{
		Name:      "child",
		Resources: []client.Object{newTestConfigMap("augmented-child")},
	})
	return nil
}

// layoutObjects returns every object in ml and its children, recursively.
func layoutObjects(ml *layout.ManifestLayout) []client.Object {
	if ml == nil {
		return nil
	}
	out := append([]client.Object(nil), ml.Resources...)
	for _, c := range ml.Children {
		out = append(out, layoutObjects(c)...)
	}
	return out
}

// augmentLikeWalker drives app the way kure's layout walker does for a
// LayoutAugmenter config: Generate, seed a per-app layout with the result,
// then call AugmentLayout on that layout.
func augmentLikeWalker(t *testing.T, app *stack.Application) *layout.ManifestLayout {
	t.Helper()
	objs, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ml := &layout.ManifestLayout{Name: app.Name}
	for _, o := range objs {
		ml.Resources = append(ml.Resources, *o)
	}
	aug, ok := app.Config.(layout.LayoutAugmenter)
	if !ok {
		t.Fatal("wrapped config does not implement layout.LayoutAugmenter")
	}
	if err := aug.AugmentLayout(ml); err != nil {
		t.Fatalf("AugmentLayout: %v", err)
	}
	return ml
}

// assertAugmentedUnannotated checks that the layout holds what the stub
// generates and what its AugmentLayout adds, and that none of it carries a
// kustomize-controller key.
func assertAugmentedUnannotated(t *testing.T, ml *layout.ManifestLayout) {
	t.Helper()
	objs := layoutObjects(ml)
	seen := map[string]bool{}
	for _, o := range objs {
		seen[o.GetName()] = true
	}
	for _, n := range []string{"generated", "augmented", "augmented-child"} {
		if !seen[n] {
			t.Errorf("layout does not contain %q; the assertion below would be vacuous for it", n)
		}
	}
	assertNoFluxObjectKeys(t, objs...)
}

// applySecurityContext wraps app.Config in another decoratorBase decorator;
// security-context passes a ConfigMap through untouched, so it isolates the
// wrap-chain behaviour from any decorator-specific output change.
func applySecurityContext(t *testing.T, app *stack.Application) {
	t.Helper()
	tr := &oam.Trait{Type: "security-context", Properties: map[string]any{"psaLevel": "baseline"}}
	if err := (&traits.SecurityContextHandler{}).Apply(tr, app, &stack.Bundle{}); err != nil {
		t.Fatalf("security-context Apply: %v", err)
	}
}

// TestDeliveryIntentTraits_LeaveAugmentLayoutResourcesAlone replaces the
// go-kure/launcher#324 regression test. The resources an inner LayoutAugmenter
// adds in AugmentLayout, to ml.Resources or to a child layout, used to be
// annotated by a hook around the augmenter. Since go-kure/launcher#782 the two
// traits state an intent on the application and install no hook: the augmenter
// runs as the component wrote it, alone and inside a decorator chain, and the
// workflow that delivers the application covers what it added
// (TestDeliveryIntent_FluxWorkflowAnnotatesEverythingAComponentOwns).
func TestDeliveryIntentTraits_LeaveAugmentLayoutResourcesAlone(t *testing.T) {
	want := stack.DeliveryIntent{PruneProtection: true, ForceReplace: true}
	apply := func(t *testing.T, app *stack.Application) {
		t.Helper()
		applyPruneProtection(t, app)
		applyForceReplace(t, app)
	}

	t.Run("Alone", func(t *testing.T) {
		cfg := &addingAugmenterStub{}
		app := stack.NewApplication("stub", "ns", cfg)
		apply(t, app)
		if got, ok := app.Config.(*addingAugmenterStub); !ok || got != cfg {
			t.Errorf("Config = %T, want the component's own augmenter, unwrapped", app.Config)
		}
		if app.Delivery != want {
			t.Errorf("Delivery = %+v, want %+v", app.Delivery, want)
		}
		assertAugmentedUnannotated(t, augmentLikeWalker(t, app))
	})
	t.Run("AfterADecorator", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applySecurityContext(t, app)
		wrapped := app.Config
		apply(t, app)
		if app.Config != wrapped {
			t.Errorf("Config = %T, want the decorator the earlier trait installed, untouched", app.Config)
		}
		if app.Delivery != want {
			t.Errorf("Delivery = %+v, want %+v", app.Delivery, want)
		}
		assertAugmentedUnannotated(t, augmentLikeWalker(t, app))
	})
	t.Run("BeforeADecorator", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		apply(t, app)
		applySecurityContext(t, app)
		if app.Delivery != want {
			t.Errorf("Delivery = %+v after a later decorator, want %+v", app.Delivery, want)
		}
		assertAugmentedUnannotated(t, augmentLikeWalker(t, app))
	})
}
