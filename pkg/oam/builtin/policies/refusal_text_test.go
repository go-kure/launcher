package policies_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

// TestRefusalsNameThePolicyOnce holds the whole message of every refusal of the
// two built-in policy handlers as the transform returns it: the transform names
// the policy, once, and the handler says what is wrong with it. One row per
// refusal; the last policy of a row is the refused one.
func TestRefusalsNameThePolicyOnce(t *testing.T) {
	dependency := func(name string, props map[string]any) oam.ApplicationPolicy {
		return oam.ApplicationPolicy{Name: name, Type: "dependency", Properties: props}
	}
	rules := func(rule ...any) map[string]any { return map[string]any{"rules": rule} }
	rule := func(component string, dependsOn ...any) map[string]any {
		return map[string]any{"component": component, "dependsOn": dependsOn}
	}
	placement := func(name string, props map[string]any) oam.ApplicationPolicy {
		return oam.ApplicationPolicy{Name: name, Type: "placement", Properties: props}
	}

	cases := []struct {
		name     string
		policies []oam.ApplicationPolicy
		want     string
	}{
		{
			name:     "dependency: rules missing",
			policies: []oam.ApplicationPolicy{dependency("order", map[string]any{})},
			want:     `policy "order": missing required property 'rules'`,
		},
		{
			name:     "dependency: rules not a list",
			policies: []oam.ApplicationPolicy{dependency("order", map[string]any{"rules": "web"})},
			want:     `policy "order": property 'rules' must be a list`,
		},
		{
			name:     "dependency: rules empty",
			policies: []oam.ApplicationPolicy{dependency("order", rules())},
			want:     `policy "order": property 'rules' must not be empty`,
		},
		{
			name:     "dependency: rule not a map",
			policies: []oam.ApplicationPolicy{dependency("order", rules("web"))},
			want:     `policy "order": rules[0] must be a map`,
		},
		{
			name:     "dependency: component missing",
			policies: []oam.ApplicationPolicy{dependency("order", rules(map[string]any{"dependsOn": []any{"db"}}))},
			want:     `policy "order": rules[0].component is required and must be a string`,
		},
		{
			name:     "dependency: dependsOn missing",
			policies: []oam.ApplicationPolicy{dependency("order", rules(map[string]any{"component": "web"}))},
			want:     `policy "order": rules[0].dependsOn is required`,
		},
		{
			name:     "dependency: dependsOn not a list",
			policies: []oam.ApplicationPolicy{dependency("order", rules(map[string]any{"component": "web", "dependsOn": "db"}))},
			want:     `policy "order": rules[0].dependsOn must be a list`,
		},
		{
			name:     "dependency: dependsOn empty",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("web")))},
			want:     `policy "order": rules[0].dependsOn must not be empty`,
		},
		{
			name:     "dependency: dependsOn entry not a name",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("web", "db", "")))},
			want:     `policy "order": rules[0].dependsOn[1] must be a non-empty string`,
		},
		{
			name:     "dependency: unknown component",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("nope", "db")))},
			want:     `policy "order": references unknown component "nope"`,
		},
		{
			name:     "dependency: unknown dependency",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("web", "nope")))},
			want:     `policy "order": component "web" depends on unknown component "nope"`,
		},
		{
			name:     "dependency: self dependency",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("web", "web")))},
			want:     `policy "order": component "web" cannot depend on itself`,
		},
		{
			name:     "dependency: cycle",
			policies: []oam.ApplicationPolicy{dependency("order", rules(rule("web", "db"), rule("db", "web")))},
			want:     `policy "order": circular dependency detected: db -> web -> db`,
		},
		{
			name: "dependency: cycle closed by a second policy",
			policies: []oam.ApplicationPolicy{
				dependency("first", rules(rule("web", "db"))),
				dependency("second", rules(rule("db", "web"))),
			},
			want: `policy "second": circular dependency detected: db -> web -> db`,
		},
		{
			name:     "placement: component missing",
			policies: []oam.ApplicationPolicy{placement("place", map[string]any{"tier": "infra"})},
			want:     `policy "place": missing required property 'component'`,
		},
		{
			name:     "placement: tier missing",
			policies: []oam.ApplicationPolicy{placement("place", map[string]any{"component": "web"})},
			want:     `policy "place": missing required property 'tier'`,
		},
		{
			name:     "placement: unknown tier",
			policies: []oam.ApplicationPolicy{placement("place", map[string]any{"component": "web", "tier": "edge"})},
			want:     `policy "place": unknown tier "edge" (valid: infra, services, apps)`,
		},
		{
			name:     "placement: unknown component",
			policies: []oam.ApplicationPolicy{placement("place", map[string]any{"component": "nope", "tier": "infra"})},
			want:     `policy "place": references unknown component "nope"`,
		},
		{
			name: "placement: second placement in another tier",
			policies: []oam.ApplicationPolicy{
				placement("first", map[string]any{"component": "db", "tier": "infra"}),
				placement("second", map[string]any{"component": "db", "tier": "apps"}),
			},
			want: `policy "second": component "db" is already placed in tier "infra" by an earlier placement policy, cannot also place it in "apps"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := oam.NewTransformer(map[string]oam.ComponentHandler{
				"deployment": &components.DeploymentHandler{},
			}, nil)
			tr.RegisterPolicy("dependency", &policies.DependencyHandler{})
			tr.RegisterPolicy("placement", &policies.PlacementHandler{})

			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "shop"},
				Spec: oam.ApplicationSpec{
					Components: []oam.Component{
						{Name: "web", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/web:v1"}},
						{Name: "db", Type: "deployment", Properties: map[string]any{"image": "ghcr.io/org/db:v1"}},
					},
					Policies: tc.policies,
				},
			}
			_, err := tr.Transform(app, oam.TransformContext{})
			if err == nil {
				t.Fatalf("transform succeeded, want %q", tc.want)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error = %q\n        want %q", got, tc.want)
			}
		})
	}
}
