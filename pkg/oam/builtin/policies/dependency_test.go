package policies_test

import (
	"reflect"
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
	if want := "circular dependency detected: a -> c -> a"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q: a circular dependency across policies", err, want)
	}
}

// TestDependencyHandler_RejectedPolicyLeavesResultUnchanged: a policy whose
// first rule is valid but whose later rule, or the accumulated cycle check,
// fails records none of its edges, so a caller reusing the result does not
// inherit dependencies from a policy that was rejected.
func TestDependencyHandler_RejectedPolicyLeavesResultUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		rules []any
	}{
		{
			name: "unknown component after a valid rule",
			rules: []any{
				map[string]any{"component": "b", "dependsOn": []any{"c"}},
				map[string]any{"component": "missing", "dependsOn": []any{"a"}},
			},
		},
		{
			name: "self dependency after a valid rule",
			rules: []any{
				map[string]any{"component": "b", "dependsOn": []any{"c"}},
				map[string]any{"component": "c", "dependsOn": []any{"c"}},
			},
		},
		{
			name: "cycle closed with the earlier policy",
			rules: []any{
				map[string]any{"component": "b", "dependsOn": []any{"c"}},
				map[string]any{"component": "c", "dependsOn": []any{"a"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.DependencyHandler{}
			result := oam.NewPolicyResult()
			components := []string{"a", "b", "c"}

			first := &oam.ApplicationPolicy{Name: "first", Type: "dependency", Properties: map[string]any{
				"rules": []any{map[string]any{"component": "a", "dependsOn": []any{"b"}}},
			}}
			if err := h.Apply(first, components, result); err != nil {
				t.Fatalf("first: %v", err)
			}

			rejected := &oam.ApplicationPolicy{Name: "rejected", Type: "dependency", Properties: map[string]any{"rules": tc.rules}}
			if err := h.Apply(rejected, components, result); err == nil {
				t.Fatal("rejected policy: error = nil, want error")
			}

			want := map[string][]string{"a": {"b"}}
			if !reflect.DeepEqual(result.Dependencies, want) {
				t.Errorf("Dependencies = %v, want %v (only the accepted policy's edges)", result.Dependencies, want)
			}
		})
	}
}

// TestDependencyHandler_Errors holds the handler's own message for each refusal.
// It does not name the policy: the transform does, once
// (TestRefusalsNameThePolicyOnce).
func TestDependencyHandler_Errors(t *testing.T) {
	cases := []struct {
		name       string
		props      map[string]any
		components []string
		want       string
	}{
		{
			name:       "unknown component",
			props:      map[string]any{"rules": []any{map[string]any{"component": "nonexistent", "dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			want:       `references unknown component "nonexistent"`,
		},
		{
			name:       "unknown dependency",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"nonexistent"}}}},
			components: []string{"web", "db"},
			want:       `component "web" depends on unknown component "nonexistent"`,
		},
		{
			name:       "self dependency",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"web"}}}},
			components: []string{"web"},
			want:       `component "web" cannot depend on itself`,
		},
		{
			name: "cycle",
			props: map[string]any{"rules": []any{
				map[string]any{"component": "a", "dependsOn": []any{"b"}},
				map[string]any{"component": "b", "dependsOn": []any{"c"}},
				map[string]any{"component": "c", "dependsOn": []any{"a"}},
			}},
			components: []string{"a", "b", "c"},
			want:       "circular dependency detected: a -> b -> c -> a",
		},
		{
			name:       "missing rules",
			props:      map[string]any{},
			components: []string{"web"},
			want:       "missing required property 'rules'",
		},
		{
			name:       "rules not a list",
			props:      map[string]any{"rules": "web"},
			components: []string{"web"},
			want:       "property 'rules' must be a list",
		},
		{
			name:       "rules empty",
			props:      map[string]any{"rules": []any{}},
			components: []string{"web"},
			want:       "property 'rules' must not be empty",
		},
		{
			name:       "rules typed nil",
			props:      map[string]any{"rules": []any(nil)},
			components: []string{"web"},
			want:       "property 'rules' must not be empty",
		},
		{
			name:       "rule not a map",
			props:      map[string]any{"rules": []any{"web"}},
			components: []string{"web"},
			want:       "rules[0] must be a map",
		},
		{
			name:       "component missing",
			props:      map[string]any{"rules": []any{map[string]any{"dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			want:       "rules[0].component is required and must be a string",
		},
		{
			name:       "component empty",
			props:      map[string]any{"rules": []any{map[string]any{"component": "", "dependsOn": []any{"db"}}}},
			components: []string{"web", "db"},
			want:       "rules[0].component is required and must be a string",
		},
		{
			name:       "dependsOn missing",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web"}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn is required",
		},
		{
			name:       "dependsOn not a list",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": "db"}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn must be a list",
		},
		{
			name:       "dependsOn empty",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{}}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn must not be empty",
		},
		{
			name:       "dependsOn typed nil",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any(nil)}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn must not be empty",
		},
		{
			name:       "dependsOn entry empty",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{"db", ""}}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn[1] must be a non-empty string",
		},
		{
			name:       "dependsOn entry not a string",
			props:      map[string]any{"rules": []any{map[string]any{"component": "web", "dependsOn": []any{42}}}},
			components: []string{"web", "db"},
			want:       "rules[0].dependsOn[0] must be a non-empty string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &policies.DependencyHandler{}
			policy := &oam.ApplicationPolicy{Name: "deps", Type: "dependency", Properties: tc.props}
			err := h.Apply(policy, tc.components, oam.NewPolicyResult())
			if err == nil {
				t.Fatalf("expected error %q", tc.want)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
		})
	}
}
