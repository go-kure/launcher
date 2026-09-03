package traits_test

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestRBACHandler_Apply_BindsAuthoredServiceAccount: when the workload authors
// serviceAccountName, the rbac trait binds that account, not the
// per-component one the kind no longer generates. The Role and RoleBinding
// objects keep the component-derived names so the trait's output stays
// addressable by component.
func TestRBACHandler_Apply_BindsAuthoredServiceAccount(t *testing.T) {
	cases := []struct {
		name        string
		props       map[string]any
		wantSubject string
	}{
		{"default per-component account", map[string]any{"image": "ghcr.io/org/api:v1"}, "api"},
		{"authored account", map[string]any{"image": "ghcr.io/org/api:v1", "serviceAccountName": "shared-sa"}, "shared-sa"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := (&components.WebserviceHandler{}).ToApplicationConfig(&oam.Component{
				Name: "api", Type: "webservice", Properties: tc.props,
			}, "default")
			if err != nil {
				t.Fatalf("ToApplicationConfig: %v", err)
			}
			app := stack.NewApplication("api", "default", cfg)
			bundle := newBundle()
			bundle.Applications = append(bundle.Applications, app)

			h := &traits.RBACHandler{}
			trait := &oam.Trait{
				Type: "rbac",
				Properties: map[string]any{
					"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}, "verbs": []any{"get"}}},
				},
			}
			if err := h.Apply(trait, app, bundle); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			objects, err := bundle.Applications[1].Config.Generate(bundle.Applications[1])
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
			if rb.Name != "api" {
				t.Errorf("RoleBinding name = %q, want api (component-derived)", rb.Name)
			}
			if len(rb.Subjects) != 1 {
				t.Fatalf("RoleBinding has %d subjects, want 1", len(rb.Subjects))
			}
			if got := rb.Subjects[0]; got.Kind != "ServiceAccount" || got.Name != tc.wantSubject || got.Namespace != "default" {
				t.Errorf("RoleBinding subject = %+v, want ServiceAccount %s/default", got, tc.wantSubject)
			}
		})
	}
}
