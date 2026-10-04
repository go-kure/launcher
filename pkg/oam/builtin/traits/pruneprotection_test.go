package traits_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// fluxObjectKeyPrefix is the prefix of every key kustomize-controller reads on
// an object (prune, force, ssa, reconcile). It is written out literally: the
// tests below pin that launcher writes none of them, whatever a constant says.
const fluxObjectKeyPrefix = "kustomize.toolkit.fluxcd.io/"

// assertNoFluxObjectKeys fails for every object that carries a
// kustomize-controller key as an annotation or a label: launcher states a
// delivery intent on the application and leaves the keys to the workflow that
// delivers it (go-kure/launcher#782).
func assertNoFluxObjectKeys(t *testing.T, objs ...client.Object) {
	t.Helper()
	if len(objs) == 0 {
		t.Fatal("no objects; the assertion would be vacuous")
	}
	for _, o := range objs {
		for _, m := range []map[string]string{o.GetAnnotations(), o.GetLabels()} {
			for k, v := range m {
				if strings.HasPrefix(k, fluxObjectKeyPrefix) {
					t.Errorf("%T %q carries %s=%q; launcher writes no kustomize-controller key", o, o.GetName(), k, v)
				}
			}
		}
	}
}

// generatedObjects runs app.Generate and returns the objects.
func generatedObjects(t *testing.T, app *stack.Application) []client.Object {
	t.Helper()
	ptrs, err := app.Generate()
	if err != nil {
		t.Fatalf("%s Generate: %v", app.Name, err)
	}
	out := make([]client.Object, 0, len(ptrs))
	for _, p := range ptrs {
		out = append(out, *p)
	}
	return out
}

func applyPruneProtection(t *testing.T, app *stack.Application) {
	t.Helper()
	if err := (&traits.PruneProtectionHandler{}).Apply(&oam.Trait{Type: "prune-protection"}, app, &stack.Bundle{}); err != nil {
		t.Fatalf("prune-protection Apply: %v", err)
	}
}

func TestPruneProtectionHandler_CanHandle(t *testing.T) {
	h := &traits.PruneProtectionHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"prune-protection", true},
		{"ingress", false},
		{"scaler", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

// TestPruneProtectionHandler_Apply_SetsDeliveryIntent pins what the trait is
// since go-kure/launcher#782: the PruneProtection delivery intent on the
// application, and nothing else. The config is the component's own, not a
// wrapper around it, and the objects it generates carry no Flux annotation.
func TestPruneProtectionHandler_Apply_SetsDeliveryIntent(t *testing.T) {
	cfg := &cmStub{name: "topolvm", namespace: "storage"}
	app := stack.NewApplication("topolvm", "storage", cfg)
	bundle := newBundle()

	if err := (&traits.PruneProtectionHandler{}).Apply(&oam.Trait{Type: "prune-protection"}, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got, want := app.Delivery, (stack.DeliveryIntent{PruneProtection: true}); got != want {
		t.Errorf("Delivery = %+v, want %+v", got, want)
	}
	if got, ok := app.Config.(*cmStub); !ok || got != cfg {
		t.Errorf("Config = %T, want the component's own config, unwrapped", app.Config)
	}
	if len(bundle.Applications) != 0 {
		t.Errorf("Apply added %d applications to the bundle, want none", len(bundle.Applications))
	}
	assertNoFluxObjectKeys(t, generatedObjects(t, app)...)
}

// TestPruneProtectionHandler_Apply_OnlyTargetApp pins both the opt-in default
// (an application without the trait has no intent) and the narrow scope (the
// trait on one application does not reach another).
func TestPruneProtectionHandler_Apply_OnlyTargetApp(t *testing.T) {
	protected := stack.NewApplication("protected", "ns", &cmStub{name: "protected", namespace: "ns"})
	unprotected := stack.NewApplication("unprotected", "ns", &cmStub{name: "unprotected", namespace: "ns"})
	applyPruneProtection(t, protected)

	if !protected.Delivery.PruneProtection {
		t.Error("the application with the trait has no PruneProtection intent")
	}
	if !unprotected.Delivery.IsZero() {
		t.Errorf("the application without the trait has the intent %+v, want none", unprotected.Delivery)
	}
}

// TestPruneProtectionHandler_Apply_LeavesSubApplicationsToTheEngine pins that
// Apply sets the intent only on the application it is handed. The
// sub-applications other trait handlers append to the bundle (rbac here) are
// reached by the engine instead, which calls Apply on each of them once every
// trait has run (oam.SubApplicationDecorator).
func TestPruneProtectionHandler_Apply_LeavesSubApplicationsToTheEngine(t *testing.T) {
	prune := &traits.PruneProtectionHandler{}
	if !prune.DecoratesSubApplications() {
		t.Fatal("DecoratesSubApplications() = false: the engine would not cover the component's sub-applications")
	}

	app := stack.NewApplication("api", "default", &cmStub{name: "api", namespace: "default"})
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)

	rbacTrait := &oam.Trait{Type: "rbac", Properties: map[string]any{
		"rules": []any{map[string]any{
			"apiGroups": []any{""},
			"resources": []any{"pods"},
			"verbs":     []any{"get"},
		}},
	}}
	if err := (&traits.RBACHandler{}).Apply(rbacTrait, app, bundle); err != nil {
		t.Fatalf("rbac.Apply: %v", err)
	}
	if err := prune.Apply(&oam.Trait{Type: "prune-protection"}, app, bundle); err != nil {
		t.Fatalf("prune.Apply: %v", err)
	}

	if !app.Delivery.PruneProtection {
		t.Error("the component's application has no PruneProtection intent")
	}
	if len(bundle.Applications) != 2 {
		t.Fatalf("bundle holds %d applications, want the component's and the rbac sub-application", len(bundle.Applications))
	}
	if sub := bundle.Applications[1]; !sub.Delivery.IsZero() {
		t.Errorf("rbac sub-application %q has the intent %+v from Apply alone, want none", sub.Name, sub.Delivery)
	}
}

// cmStub is a minimal ApplicationConfig that emits a single ConfigMap.
type cmStub struct {
	name      string
	namespace string
}

func (s *cmStub) Generate(_ *stack.Application) ([]*client.Object, error) {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
		},
	}
	obj := client.Object(cm)
	return []*client.Object{&obj}, nil
}
