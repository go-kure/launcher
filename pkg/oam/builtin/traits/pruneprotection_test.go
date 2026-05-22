package traits_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

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

func TestPruneProtectionHandler_Apply_AnnotatesResources(t *testing.T) {
	h := &traits.PruneProtectionHandler{}

	app := stack.NewApplication("topolvm", "storage", &cmStub{name: "topolvm", namespace: "storage"})
	bundle := newBundle()

	if err := h.Apply(&oam.Trait{Type: "prune-protection"}, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resources, err := app.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resources) == 0 {
		t.Fatal("Generate returned no resources")
	}

	for _, r := range resources {
		ann := (*r).GetAnnotations()
		got := ann[stack.AnnotationFluxPruneKey]
		if got != stack.AnnotationFluxPruneDisabled {
			t.Errorf("resource %q: annotation %q = %q, want %q",
				(*r).GetName(), stack.AnnotationFluxPruneKey, got, stack.AnnotationFluxPruneDisabled)
		}
	}
}

func TestPruneProtectionHandler_Apply_OnlyTargetApp(t *testing.T) {
	h := &traits.PruneProtectionHandler{}

	protected := stack.NewApplication("protected", "ns", &cmStub{name: "protected", namespace: "ns"})
	unprotected := stack.NewApplication("unprotected", "ns", &cmStub{name: "unprotected", namespace: "ns"})
	bundle := newBundle()

	if err := h.Apply(&oam.Trait{Type: "prune-protection"}, protected, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	protectedResources, err := protected.Generate()
	if err != nil {
		t.Fatalf("protected.Generate: %v", err)
	}
	unprotectedResources, err := unprotected.Generate()
	if err != nil {
		t.Fatalf("unprotected.Generate: %v", err)
	}

	for _, r := range protectedResources {
		ann := (*r).GetAnnotations()
		if ann[stack.AnnotationFluxPruneKey] != stack.AnnotationFluxPruneDisabled {
			t.Errorf("protected resource %q: missing prune annotation", (*r).GetName())
		}
	}
	for _, r := range unprotectedResources {
		ann := (*r).GetAnnotations()
		if v, exists := ann[stack.AnnotationFluxPruneKey]; exists {
			t.Errorf("unprotected resource %q: unexpectedly has prune annotation %q", (*r).GetName(), v)
		}
	}
}

// TestPruneProtectionHandler_Apply_DoesNotProtectSiblingApps documents that
// prune-protection only annotates resources produced by the component's own
// app.Config. Resources appended to bundle.Applications by other trait handlers
// (e.g. rbac) are NOT annotated — narrow scope is intentional.
func TestPruneProtectionHandler_Apply_DoesNotProtectSiblingApps(t *testing.T) {
	rbacH := &traits.RBACHandler{}
	prune := &traits.PruneProtectionHandler{}

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
	if err := rbacH.Apply(rbacTrait, app, bundle); err != nil {
		t.Fatalf("rbac.Apply: %v", err)
	}
	if err := prune.Apply(&oam.Trait{Type: "prune-protection"}, app, bundle); err != nil {
		t.Fatalf("prune.Apply: %v", err)
	}

	// Main app resources ARE annotated.
	mainResources, err := app.Generate()
	if err != nil {
		t.Fatalf("app.Generate: %v", err)
	}
	for _, r := range mainResources {
		if (*r).GetAnnotations()[stack.AnnotationFluxPruneKey] != stack.AnnotationFluxPruneDisabled {
			t.Errorf("main app resource %q: expected prune annotation", (*r).GetName())
		}
	}

	// Sibling (rbac) resources are NOT annotated — narrow scope is intentional.
	rbacApp := bundle.Applications[1]
	rbacResources, err := rbacApp.Config.Generate(rbacApp)
	if err != nil {
		t.Fatalf("rbacApp.Generate: %v", err)
	}
	for _, r := range rbacResources {
		if _, ok := (*r).GetAnnotations()[stack.AnnotationFluxPruneKey]; ok {
			t.Errorf("rbac sibling resource %q: should NOT have prune annotation (narrow scope)", (*r).GetName())
		}
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
