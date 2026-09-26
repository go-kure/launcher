package oam

import (
	"strings"
	"testing"
)

func TestValidate_PassthroughComponent(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "my-cr",
					Type: "passthrough",
					Properties: map[string]any{
						"object": map[string]any{
							"apiVersion": "example.com/v1",
							"kind":       "Widget",
						},
					},
				},
			},
		},
	}
	if err := validate(app); err != nil {
		t.Errorf("unexpected error for passthrough component: %v", err)
	}
}

func TestValidate_ScalerTraitOnCronjob(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "batch-job",
					Type: "cronjob",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{}},
					},
				},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for scaler trait on cronjob, got nil")
	}
	if !strings.Contains(err.Error(), "not supported on component type") {
		t.Errorf("error = %q, want to contain 'not supported on component type'", err.Error())
	}
}

func TestValidate_ScalerTraitOnWebservice(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "web",
					Type: "webservice",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{}},
					},
				},
			},
		},
	}

	err := validate(app)
	if err != nil {
		t.Errorf("unexpected error for scaler trait on webservice: %v", err)
	}
}

func TestValidate_ScalerTraitOnWorker(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "bg",
					Type: "worker",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{}},
					},
				},
			},
		},
	}

	err := validate(app)
	if err != nil {
		t.Errorf("unexpected error for scaler trait on worker: %v", err)
	}
}

func TestValidate_ScalerTraitOnPostgresql(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "db",
					Type: "postgresql",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{}},
					},
				},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for scaler trait on postgresql, got nil")
	}
	if !strings.Contains(err.Error(), "not supported on component type") {
		t.Errorf("error = %q, want to contain 'not supported on component type'", err.Error())
	}
}

// TestValidate_ScalerTraitOnDeployment pins that "scaler" is admitted on
// deployment (go-kure/launcher#513). DeploymentConfig.applyNonRWXConstraint
// (builtin/components/deployment.go) reads only the authored `replicas`, so it
// cannot see an HPA that scales past 1. Admission is safe because
// DeploymentConfig reports its claim through NonRWXClaim, exactly as
// webservice and worker do, and the scaler itself refuses an effective
// maxReplicas > 1 beside a non-RWX claim (builtin/traits/scaler_nonrwx_test.go).
func TestValidate_ScalerTraitOnDeployment(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "api",
					Type: "deployment",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{"maxReplicas": 5}},
					},
				},
			},
		},
	}

	if err := validate(app); err != nil {
		t.Fatalf("expected scaler trait on deployment to validate, got: %v", err)
	}
}

// TestValidate_ScalerTraitOnStatefulset pins that statefulset stays excluded:
// its claims come from volumeClaimTemplates, one per pod, so the non-RWX
// question differs and admitting it is a separate decision (go-kure/launcher#513).
func TestValidate_ScalerTraitOnStatefulset(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name: "db",
					Type: "statefulset",
					Traits: []Trait{
						{Type: "scaler", Properties: map[string]any{"maxReplicas": 5}},
					},
				},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for scaler trait on statefulset, got nil")
	}
	if !strings.Contains(err.Error(), "not supported on component type") {
		t.Errorf("error = %q, want to contain 'not supported on component type'", err.Error())
	}
}

func TestValidate_PolicyOpenEndedType(t *testing.T) {
	// Policy types are open-ended in Phase 1 — any non-empty type is accepted.
	// Arbitrary types like env-policy, my-custom-policy, etc. must not be rejected.
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
			Policies: []ApplicationPolicy{
				{Name: "resource-limits", Type: "env-policy"},
				{Name: "my-custom", Type: "my-custom-policy-type"},
			},
		},
	}

	err := validate(app)
	if err != nil {
		t.Errorf("unexpected error for open-ended policy types: %v", err)
	}
}

func TestValidate_PolicyDuplicateName(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
			Policies: []ApplicationPolicy{
				{Name: "my-policy", Type: "env-policy"},
				{Name: "my-policy", Type: "another-type"},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for duplicate policy name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate policy name") {
		t.Errorf("error = %q, want to contain 'duplicate policy name'", err.Error())
	}
}

func TestValidate_PolicyInvalidDNS1123Name(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
			Policies: []ApplicationPolicy{
				{Name: "My Policy!", Type: "env-policy"},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for invalid DNS-1123 policy name, got nil")
	}
	if !strings.Contains(err.Error(), "not a valid DNS-1123") {
		t.Errorf("error = %q, want to contain 'not a valid DNS-1123'", err.Error())
	}
}

func TestValidate_PolicyMissingType(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
			Policies: []ApplicationPolicy{
				{Name: "my-policy", Type: ""},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for missing policy type, got nil")
	}
	if !strings.Contains(err.Error(), "missing type") {
		t.Errorf("error = %q, want to contain 'missing type'", err.Error())
	}
}

func TestValidate_NamespaceValidDNS1123(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app", Namespace: "my-namespace"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
		},
	}

	err := validate(app)
	if err != nil {
		t.Errorf("unexpected error for valid namespace: %v", err)
	}
}

func TestValidate_NamespaceInvalidDNS1123(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app", Namespace: "My Namespace!"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
		},
	}

	err := validate(app)
	if err == nil {
		t.Fatal("expected validation error for invalid namespace, got nil")
	}
	if !strings.Contains(err.Error(), "not a valid DNS-1123") {
		t.Errorf("error = %q, want to contain 'not a valid DNS-1123'", err.Error())
	}
}

func TestValidate_NamespaceEmpty(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
			},
		},
	}

	err := validate(app)
	if err != nil {
		t.Errorf("unexpected error for empty namespace: %v", err)
	}
}

// TestValidate_SecurityContextTrait guards that standalone validation accepts the
// security-context trait, which SecurityContextHandler ships (regression for the
// validTraitTypes allowlist omitting a shipped handler).
func TestValidate_SecurityContextTrait(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name:       "web",
					Type:       "webservice",
					Properties: map[string]any{"image": "nginx:1.25"},
					Traits: []Trait{
						{Type: "security-context", Properties: map[string]any{"runAsNonRoot": true}},
					},
				},
			},
		},
	}
	if err := validate(app); err != nil {
		t.Errorf("unexpected error for security-context trait: %v", err)
	}
}
