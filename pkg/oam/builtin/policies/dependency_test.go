package policies_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

func TestDependencyHandler_CanHandle(t *testing.T) {
	h := &policies.DependencyHandler{}
	if !h.CanHandle("dependency") {
		t.Error("expected CanHandle('dependency') = true")
	}
	if h.CanHandle("placement") {
		t.Error("expected CanHandle('placement') = false")
	}
	if h.CanHandle("app-dependency") {
		t.Error("expected CanHandle('app-dependency') = false")
	}
}

func TestDependencyHandler_ValidRules(t *testing.T) {
	h := &policies.DependencyHandler{}
	result := oam.NewPolicyResult()

	policy := &oam.ApplicationPolicy{
		Name: "deploy-order",
		Type: "dependency",
		Properties: map[string]any{
			"rules": []any{
				map[string]any{
					"component": "web",
					"dependsOn": []any{"db"},
				},
				map[string]any{
					"component": "api",
					"dependsOn": []any{"db", "cache"},
				},
			},
		},
	}

	if err := h.Apply(policy, []string{"web", "api", "db", "cache"}, result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string][]string{
		"web": {"db"},
		"api": {"db", "cache"},
	}
	if !reflect.DeepEqual(result.Dependencies, want) {
		t.Errorf("Dependencies = %v, want %v", result.Dependencies, want)
	}
	if !result.HasDependencies() {
		t.Error("HasDependencies() = false after recording edges")
	}
}

// TestDependencyHandler_AccumulatesAcrossPolicies: a second dependency policy
// appends to the edges the first recorded, and the cycle check runs over the
// accumulated graph, so a cycle split across two policies is still rejected.
func TestDependencyHandler_AccumulatesAcrossPolicies(t *testing.T) {
	h := &policies.DependencyHandler{}
	result := oam.NewPolicyResult()
	components := []string{"a", "b", "c"}

	first := &oam.ApplicationPolicy{Name: "first", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": "a", "dependsOn": []any{"b"}}},
	}}
	second := &oam.ApplicationPolicy{Name: "second", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": "a", "dependsOn": []any{"c"}}},
	}}
	if err := h.Apply(first, components, result); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := h.Apply(second, components, result); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got, want := result.Dependencies["a"], []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a deps = %v, want %v", got, want)
	}

	third := &oam.ApplicationPolicy{Name: "third", Type: "dependency", Properties: map[string]any{
		"rules": []any{map[string]any{"component": "c", "dependsOn": []any{"a"}}},
	}}
	err := h.Apply(third, components, result)
	if err == nil || !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("error = %v, want a circular dependency across policies", err)
	}
	if !strings.Contains(err.Error(), `policy "third"`) {
		t.Errorf("error = %q, want it to name the policy that closed the cycle", err)
	}
}

func TestDependencyHandler_Errors(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		components []string
		wantSub    string
	}{
		{
			name:       "unknown component",
			props:      map[string]any{"rules": []any{map[string]any{"component": "nonexistent", "dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			wantSub:    `references unknown component "nonexistent"`,
		},
		{
			name:       "unknown dependency",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"nonexistent"}}}},
			components: []string{"web", "db"},
			wantSub:    `component "web" depends on unknown component "nonexistent"`,
		},
		{
			name:       "self dependency",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"web"}}}},
			components: []string{"web"},
			wantSub:    `component "web" cannot depend on itself`,
		},
		{
			name: "cycle",
			props: map[string]any{"rules": []any{
				map[string]any{"component": "a", "dependsOn": []any{"b"}},
				map[string]any{"component": "b", "dependsOn": []any{"c"}},
				map[string]any{"component": "c", "dependsOn": []any{"a"}},
			}},
			components: []string{"a", "b", "c"},
			wantSub:    "circular dependency detected: a -> b -> c -> a",
		},
		{
			name:       "missing rules",
			props:      map[string]any{},
			components: []string{"web"},
			wantSub:    "missing required property 'rules'",
		},
		{
			name:       "rules not a list",
			props:      map[string]any{"rules": "web"},
			components: []string{"web"},
			wantSub:    "property 'rules' must be a list",
		},
		{
			name:       "rule not a map",
			props:      map[string]any{"rules": []any{"web"}},
			components: []string{"web"},
			wantSub:    "rules[0] must be a map",
		},
		{
			name:       "component missing",
			props:      map[string]any{"rules": []any{map[string]any{"dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].component is required and must be a string",
		},
		{
			name:       "component empty",
			props:      map[string]any{"rules": []any{map[string]any{"component": "", "dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].component is required and must be a string",
		},
		{
			name:       "dependsOn missing",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web"}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].dependsOn is required",
		},
		{
			name:       "dependsOn not a list",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": "db"}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].dependsOn must be a list",
		},
		{
			name:       "dependsOn entry empty",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"db", ""}}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].dependsOn[1] must be a non-empty string",
		},
		{
			name:       "dependsOn entry not a string",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{42}}}},
			components: []string{"web", "db"},
			wantSub:    "rules[0].dependsOn[0] must be a non-empty string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.DependencyHandler{}
			policy := &oam.ApplicationPolicy{Name: "deps", Type: "dependency", Properties: tc.props}
			err := h.Apply(policy, tc.components, oam.NewPolicyResult())
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), `policy "deps"`) {
				t.Errorf("error = %q, want it to name the policy", err)
			}
		})
	}
}
