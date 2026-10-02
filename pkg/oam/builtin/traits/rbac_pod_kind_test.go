package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func rbacPodsTrait() *oam.Trait {
	return &oam.Trait{
		Type: "rbac",
		Properties: map[string]any{
			"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}},
		},
	}
}

// rbacApp builds the component's application, optionally wraps it in the
// force-replace decorator, and applies rbac to it.
func rbacApp(t *testing.T, h oam.ComponentHandler, typ string, props map[string]any, decorate bool) (*stack.Bundle, error) {
	t.Helper()
	cfg, err := h.ToApplicationConfig(&oam.Component{Name: "api", Type: typ, Properties: props}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	app := stack.NewApplication("api", "default", cfg)
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)
	if decorate {
		if err := (&traits.ForceReplaceHandler{}).Apply(&oam.Trait{Type: "force-replace"}, app, bundle); err != nil {
			t.Fatalf("force-replace Apply: %v", err)
		}
	}
	return bundle, (&traits.RBACHandler{}).Apply(rbacPodsTrait(), app, bundle)
}

func rbacSubject(t *testing.T, bundle *stack.Bundle) string {
	t.Helper()
	rbacApp := bundle.Applications[len(bundle.Applications)-1]
	objects, err := rbacApp.Config.Generate(rbacApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, o := range objects {
		if b, ok := (*o).(*rbacv1.RoleBinding); ok && len(b.Subjects) == 1 {
			return b.Subjects[0].Name
		}
	}
	t.Fatal("no RoleBinding with one subject generated")
	return ""
}

// TestRBACHandler_Apply_PodKindWithoutServiceAccountIsRefused: a pod kind
// generates no ServiceAccount (go-kure/launcher#702), so its pods run as the
// namespace's default account. rbac refuses rather than binding rules to an
// account named after the component that nothing creates, directly and
// through a decorator that forwards the namer.
func TestRBACHandler_Apply_PodKindWithoutServiceAccountIsRefused(t *testing.T) {
	for _, decorate := range []bool{false, true} {
		_, err := rbacApp(t, &components.DeploymentHandler{}, "deployment",
			map[string]any{"image": "ghcr.io/org/api:v1"}, decorate)
		if err == nil || !strings.Contains(err.Error(), "runs as no ServiceAccount") {
			t.Errorf("decorated=%v: err = %v, want a refusal naming the missing ServiceAccount", decorate, err)
		}
	}
}

// TestRBACHandler_Apply_PodKindBindsAuthoredServiceAccount: with
// serviceAccountName authored, the pod kind binds it, directly and through a
// decorator.
func TestRBACHandler_Apply_PodKindBindsAuthoredServiceAccount(t *testing.T) {
	for _, decorate := range []bool{false, true} {
		bundle, err := rbacApp(t, &components.DeploymentHandler{}, "deployment",
			map[string]any{"image": "ghcr.io/org/api:v1", "serviceAccountName": "api-sa"}, decorate)
		if err != nil {
			t.Fatalf("decorated=%v: Apply: %v", decorate, err)
		}
		if got := rbacSubject(t, bundle); got != "api-sa" {
			t.Errorf("decorated=%v: subject = %q, want api-sa", decorate, got)
		}
	}
}

// TestRBACHandler_Apply_NonPodConfigKeepsComponentName: a config that runs no
// pods keeps binding the account named after the component, including when a
// decorator wraps it and answers ("", false).
func TestRBACHandler_Apply_NonPodConfigKeepsComponentName(t *testing.T) {
	for _, decorate := range []bool{false, true} {
		bundle, err := rbacApp(t, &components.ServiceAccountHandler{}, "serviceaccount", map[string]any{}, decorate)
		if err != nil {
			t.Fatalf("decorated=%v: Apply: %v", decorate, err)
		}
		if got := rbacSubject(t, bundle); got != "api" {
			t.Errorf("decorated=%v: subject = %q, want api", decorate, got)
		}
	}
}
