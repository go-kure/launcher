package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

func TestRBACHandler_CanHandle(t *testing.T) {
	h := &traits.RBACHandler{}
	cases := []struct {
		typ  string
		want bool
	}{
		{"rbac", true},
		{"networkpolicy", false},
		{"pvc", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := h.CanHandle(tc.typ); got != tc.want {
			t.Errorf("CanHandle(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

// TestRBACHandler_Schema_Strict guards the strict shape of the rbac schema: a rule
// is a closed object (unknown keys rejected) and resources/verbs are required.
// Downstream consumers preflight against this schema, so a regression here would
// silently relax their strict validation.
func TestRBACHandler_Schema_Strict(t *testing.T) {
	schema := (&traits.RBACHandler{}).PropertySchema()
	rules, ok := schema["rules"]
	if !ok {
		t.Fatal("schema missing 'rules' property")
	}
	if rules.Items == nil {
		t.Fatal("rules.Items is nil")
	}
	if rules.Items.AdditionalProperties {
		t.Error("rules item must be a closed object (AdditionalProperties=false) so unknown keys in a rule are rejected")
	}
	for _, field := range []string{"resources", "verbs"} {
		p, ok := rules.Items.Properties[field]
		if !ok {
			t.Fatalf("rules item missing %q property", field)
		}
		if !p.Required {
			t.Errorf("rules item %q must be Required", field)
		}
	}
	if p, ok := rules.Items.Properties["apiGroups"]; !ok {
		t.Fatal("rules item missing \"apiGroups\" property")
	} else if p.Required {
		t.Error("rules item \"apiGroups\" must stay optional (not Required)")
	}
}

func TestRBACHandler_Apply_NamespaceOnly(t *testing.T) {
	h := &traits.RBACHandler{}
	trait := &oam.Trait{
		Type: "rbac",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"apiGroups": []any{""},
					"resources": []any{"pods"},
					"verbs":     []any{"get", "list"},
				},
			},
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()
	bundle.Applications = append(bundle.Applications, app)

	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(bundle.Applications) != 2 {
		t.Fatalf("expected 2 applications, got %d", len(bundle.Applications))
	}
	rbacApp := bundle.Applications[1]
	if rbacApp.Name != "api-rbac" {
		t.Errorf("rbac app name = %q, want %q", rbacApp.Name, "api-rbac")
	}

	objects, err := rbacApp.Config.Generate(rbacApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects (Role+RoleBinding), got %d", len(objects))
	}
	kinds := map[string]bool{}
	for _, o := range objects {
		kinds[(*o).GetObjectKind().GroupVersionKind().Kind] = true
	}
	if !kinds["Role"] {
		t.Error("expected Role")
	}
	if !kinds["RoleBinding"] {
		t.Error("expected RoleBinding")
	}
}

func TestRBACHandler_Apply_ClusterWide(t *testing.T) {
	h := &traits.RBACHandler{}
	trait := &oam.Trait{
		Type: "rbac",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"apiGroups": []any{""},
					"resources": []any{"pods"},
					"verbs":     []any{"get"},
				},
			},
			"clusterWide": true,
		},
	}
	app := newApp("api", "default")
	bundle := newBundle()

	if err := h.Apply(trait, app, bundle); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	rbacApp := bundle.Applications[0]
	objects, err := rbacApp.Config.Generate(rbacApp)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(objects) != 4 {
		t.Fatalf("expected 4 objects (Role+RoleBinding+ClusterRole+ClusterRoleBinding), got %d", len(objects))
	}
	kinds := map[string]bool{}
	for _, o := range objects {
		kinds[(*o).GetObjectKind().GroupVersionKind().Kind] = true
	}
	for _, want := range []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"} {
		if !kinds[want] {
			t.Errorf("expected kind %s in generated objects", want)
		}
	}
}

func TestRBACHandler_Apply_Errors(t *testing.T) {
	h := &traits.RBACHandler{}
	app := newApp("api", "default")

	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
	}{
		{
			name:    "missing rules",
			props:   map[string]any{},
			wantErr: "required property 'rules'",
		},
		{
			name:    "empty rules",
			props:   map[string]any{"rules": []any{}},
			wantErr: "required property 'rules'",
		},
		{
			name: "rule not object",
			props: map[string]any{
				"rules": []any{"not-a-map"},
			},
			wantErr: "rules[0]: expected object",
		},
		{
			name: "missing resources",
			props: map[string]any{
				"rules": []any{
					map[string]any{"apiGroups": []any{""}, "verbs": []any{"get"}},
				},
			},
			wantErr: "resources must not be empty",
		},
		{
			name: "missing verbs",
			props: map[string]any{
				"rules": []any{
					map[string]any{"apiGroups": []any{""}, "resources": []any{"pods"}},
				},
			},
			wantErr: "verbs must not be empty",
		},
		{
			name: "verbs not array",
			props: map[string]any{
				"rules": []any{
					map[string]any{
						"apiGroups": []any{""},
						"resources": []any{"pods"},
						"verbs":     "get",
					},
				},
			},
			wantErr: "expected array",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := h.Apply(&oam.Trait{Type: "rbac", Properties: tc.props}, app, newBundle())
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
