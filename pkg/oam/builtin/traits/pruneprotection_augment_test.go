package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"github.com/go-kure/kure/pkg/stack/layout"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
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

func isPruneDisabled(o client.Object) bool {
	return o.GetAnnotations()[stack.AnnotationFluxPruneKey] == stack.AnnotationFluxPruneDisabled
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

func assertAllPruneDisabled(t *testing.T, ml *layout.ManifestLayout, wantNames ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, o := range layoutObjects(ml) {
		seen[o.GetName()] = true
		if !isPruneDisabled(o) {
			t.Errorf("%s %q: missing %s=%s", o.GetObjectKind().GroupVersionKind().Kind, o.GetName(),
				stack.AnnotationFluxPruneKey, stack.AnnotationFluxPruneDisabled)
		}
	}
	for _, n := range wantNames {
		if !seen[n] {
			t.Errorf("layout does not contain %q; the assertion above would be vacuous for it", n)
		}
	}
}

func applyPruneProtection(t *testing.T, app *stack.Application) {
	t.Helper()
	if err := (&traits.PruneProtectionHandler{}).Apply(&oam.Trait{Type: "prune-protection"}, app, &stack.Bundle{}); err != nil {
		t.Fatalf("prune-protection Apply: %v", err)
	}
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

// TestPruneProtection_AnnotatesAugmentLayoutResources is the regression test
// for go-kure/launcher#324: resources an inner LayoutAugmenter adds in
// AugmentLayout — to ml.Resources or to a child layout — must carry the
// prune-disabled annotation just like the ones Generate returns.
func TestPruneProtection_AnnotatesAugmentLayoutResources(t *testing.T) {
	app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
	applyPruneProtection(t, app)
	ml := augmentLikeWalker(t, app)
	assertAllPruneDisabled(t, ml, "generated", "augmented", "augmented-child")
}

// TestPruneProtection_AnnotatesAugmentLayoutResources_ThroughDecoratorChain
// covers prune-protection sitting at either end of a decorator chain: the
// annotation must reach augmenter-added resources whether prune-protection
// wraps another decorator or is itself wrapped by one.
func TestPruneProtection_AnnotatesAugmentLayoutResources_ThroughDecoratorChain(t *testing.T) {
	t.Run("PruneOutermost", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applySecurityContext(t, app)
		applyPruneProtection(t, app)
		assertAllPruneDisabled(t, augmentLikeWalker(t, app), "generated", "augmented", "augmented-child")
	})

	t.Run("PruneInnermost", func(t *testing.T) {
		app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
		applyPruneProtection(t, app)
		applySecurityContext(t, app)
		assertAllPruneDisabled(t, augmentLikeWalker(t, app), "generated", "augmented", "augmented-child")
	})
}

// TestNonPruneDecorator_DoesNotAnnotateAugmentLayoutResources pins that the
// post-AugmentLayout annotation is prune-protection's own, not a side effect
// of the generic augmentingDecorator forward every trait decorator shares.
func TestNonPruneDecorator_DoesNotAnnotateAugmentLayoutResources(t *testing.T) {
	app := stack.NewApplication("stub", "ns", &addingAugmenterStub{})
	applySecurityContext(t, app)
	for _, o := range layoutObjects(augmentLikeWalker(t, app)) {
		if _, ok := o.GetAnnotations()[stack.AnnotationFluxPruneKey]; ok {
			t.Errorf("%q: unexpectedly annotated without prune-protection", o.GetName())
		}
	}
}

// TestPruneProtection_HelmchartValuesConfigMap_WalkCluster exercises the
// concrete case #324 was filed for end to end through kure's real layout
// walker: a helmchart component under valuesMode: configMap emits its values
// ConfigMap only from AugmentLayout. With prune-protection on the component,
// that ConfigMap must be annotated; a sibling application in the same bundle
// (merged into the parent layout, not the component's per-app sub-layout)
// must not be.
func TestPruneProtection_HelmchartValuesConfigMap_WalkCluster(t *testing.T) {
	cfg, err := (&components.HelmchartHandler{}).ToApplicationConfig(&oam.Component{
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
	app := stack.NewApplication("metrics", "monitoring", cfg)
	applyPruneProtection(t, app)
	sibling := stack.NewApplication("sibling", "monitoring", &cmStub{name: "sibling", namespace: "monitoring"})

	cluster := &stack.Cluster{
		Name: "c",
		Node: &stack.Node{
			Name:   "apps",
			Bundle: &stack.Bundle{Name: "apps", Applications: []*stack.Application{app, sibling}},
		},
	}
	root, err := layout.WalkCluster(cluster, layout.LayoutRules{})
	if err != nil {
		t.Fatalf("WalkCluster: %v", err)
	}

	var sawValuesCM, sawSibling bool
	for _, o := range layoutObjects(root) {
		switch o.GetName() {
		case "sibling":
			sawSibling = true
			if isPruneDisabled(o) {
				t.Error("sibling application's ConfigMap was annotated; prune-protection must stay scoped to its own component")
			}
		default:
			if o.GetObjectKind().GroupVersionKind().Kind == "ConfigMap" {
				sawValuesCM = true
			}
			if !isPruneDisabled(o) {
				t.Errorf("%s %q: missing %s=%s", o.GetObjectKind().GroupVersionKind().Kind, o.GetName(),
					stack.AnnotationFluxPruneKey, stack.AnnotationFluxPruneDisabled)
			}
		}
	}
	if !sawValuesCM {
		t.Error("walked layout carries no values ConfigMap; the test no longer exercises the AugmentLayout path")
	}
	if !sawSibling {
		t.Error("walked layout carries no sibling ConfigMap; the scoping assertion is vacuous")
	}
}
