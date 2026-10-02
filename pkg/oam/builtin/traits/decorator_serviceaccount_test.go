package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// saStub names a ServiceAccount and nothing else, so the assertion below can
// only succeed through decoratorBase's forward.
type saStub struct{ name string }

func (s *saStub) Generate(app *stack.Application) ([]*client.Object, error) { return nil, nil }
func (s *saStub) ServiceAccountName() (string, bool)                        { return s.name, true }

// TestDecorator_ForwardsServiceAccountNamer: a trait decorator must keep
// oam.ServiceAccountNamer reachable. Without the forward the wrapped config
// stops answering and rbac takes it for a config that runs no pods.
func TestDecorator_ForwardsServiceAccountNamer(t *testing.T) {
	app := stack.NewApplication("web", "default", &saStub{name: "shared-sa"})
	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{"name": "web-config", "mountPath": "/etc/config"}},
		app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	namer, ok := app.Config.(oam.ServiceAccountNamer)
	if !ok {
		t.Fatal("wrapped config does not implement oam.ServiceAccountNamer")
	}
	if got, pods := namer.ServiceAccountName(); got != "shared-sa" || !pods {
		t.Errorf("ServiceAccountName() = (%q, %v), want (shared-sa, true)", got, pods)
	}
}

// TestDecorator_ServiceAccountNamerDefault: an inner config that does not
// implement the interface answers "" and false — a config that runs no pods,
// for which rbac keeps the account named after the component (see
// traits/rbac.go).
func TestDecorator_ServiceAccountNamerDefault(t *testing.T) {
	app := stack.NewApplication("worker", "default", &nakedStub{})
	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{"name": "c", "mountPath": "/etc/c"}},
		app, &stack.Bundle{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	namer, ok := app.Config.(oam.ServiceAccountNamer)
	if !ok {
		t.Fatal("wrapped config does not implement oam.ServiceAccountNamer")
	}
	if got, pods := namer.ServiceAccountName(); got != "" || pods {
		t.Errorf("ServiceAccountName() = (%q, %v), want (\"\", false)", got, pods)
	}
}

// TestRBACHandler_BindsAuthoredAccountThroughDecorator is the end-to-end shape
// of the forward: a configmap trait wraps the workload first, then rbac reads
// the effective account through the decorator and still binds the authored one.
func TestRBACHandler_BindsAuthoredAccountThroughDecorator(t *testing.T) {
	cfg, err := webserviceViaRule{}.ToApplicationConfig(&oam.Component{
		Name: "api", Type: "webservice",
		Properties: map[string]any{"image": "ghcr.io/org/api:v1", "serviceAccountName": "shared-sa"},
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("api", "default", cfg)
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)

	if err := (&traits.ConfigMapHandler{}).Apply(
		&oam.Trait{Type: "configmap", Properties: map[string]any{"name": "api-config", "mountPath": "/etc/config"}},
		app, bundle); err != nil {
		t.Fatalf("configmap Apply: %v", err)
	}
	if err := (&traits.RBACHandler{}).Apply(&oam.Trait{
		Type: "rbac",
		Properties: map[string]any{
			"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}},
		},
	}, app, bundle); err != nil {
		t.Fatalf("rbac Apply: %v", err)
	}

	rbacApp := bundle.Applications[len(bundle.Applications)-1]
	objects, err := rbacApp.Config.Generate(rbacApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var rb *rbacv1.RoleBinding
	for _, o := range objects {
		if b, ok := (*o).(*rbacv1.RoleBinding); ok {
			rb = b
		}
	}
	if rb == nil {
		t.Fatal("no RoleBinding generated")
	}
	if len(rb.Subjects) != 1 || rb.Subjects[0].Name != "shared-sa" {
		t.Errorf("RoleBinding subjects = %+v, want the authored shared-sa account", rb.Subjects)
	}
}
