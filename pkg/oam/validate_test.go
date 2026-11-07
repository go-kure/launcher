package oam

import (
	"fmt"
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
// (builtin/components/deployment.go) checks only the component's effective
// replica count, so it cannot see an HPA that scales past 1. Admission is
// safe because DeploymentConfig reports its claim through NonRWXClaim, exactly as
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

// TestValidate_ScalerTraitOnStatefulset pins that a statefulset takes a scaler
// (go-kure/launcher#1268): the HPA scales a StatefulSet through its scale
// subresource, and the kind's per-pod claims make the non-RWX question the
// scaler asks of a Deployment moot.
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

	if err := validate(app); err != nil {
		t.Fatalf("expected scaler trait on statefulset to validate, got: %v", err)
	}
}

// TestValidate_ScalerTraitRefusedOnKindsWithNoScaleTarget: a daemonset, job,
// cronjob or pod has no HPA-usable scale subresource, so the scaler stays
// refused there, and the refusal lists the admitted types, statefulset now
// among them.
func TestValidate_ScalerTraitRefusedOnKindsWithNoScaleTarget(t *testing.T) {
	for _, typ := range []string{"daemonset", "job", "cronjob", "pod"} {
		t.Run(typ, func(t *testing.T) {
			app := &Application{
				APIVersion: SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   Metadata{Name: "test-app"},
				Spec: ApplicationSpec{Components: []Component{{
					Name: "x", Type: typ,
					Traits: []Trait{{Type: "scaler", Properties: map[string]any{"maxReplicas": 5}}},
				}}},
			}
			err := validate(app)
			if err == nil {
				t.Fatalf("a scaler on a %s validated; want it refused", typ)
			}
			if !strings.Contains(err.Error(), "not supported on component type") {
				t.Errorf("error = %q, want to contain 'not supported on component type'", err.Error())
			}
			const wantList = "supported types: deployment, statefulset, webservice, worker"
			if !strings.Contains(err.Error(), wantList) {
				t.Errorf("error = %q, want to contain the sorted list %q", err.Error(), wantList)
			}
		})
	}
}

func TestValidate_PolicyCustomTypeNeedsAClaim(t *testing.T) {
	// A policy type outside validPolicyTypes is accepted only when the caller names it
	// (customPolicyTypes, here); with no claim the check refuses it.
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

	claimed := map[string]bool{"env-policy": true, "my-custom-policy-type": true}
	if err := validateWithExtraTypes(app, nil, nil, claimed, LowerableTypes{}); err != nil {
		t.Errorf("unexpected error for claimed policy types: %v", err)
	}
	if err := validate(app); err == nil {
		t.Error("policy types nobody claimed were accepted")
	}
	if err := validateWithExtraTypes(app, nil, nil, map[string]bool{"env-policy": true}, LowerableTypes{}); err == nil {
		t.Error("claiming one policy type opened the other")
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
				{Name: "my-policy", Type: "dependency"},
				{Name: "my-policy", Type: "placement"},
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

// TestValidate_NamespaceMustBeDNS1123Label guards go-kure/launcher#616: a Kubernetes
// namespace is a DNS-1123 label, so a dotted or over-63-character namespace — both valid
// subdomains — is refused, while a 63-character label is accepted.
func TestValidate_NamespaceMustBeDNS1123Label(t *testing.T) {
	cases := []struct {
		namespace string
		wantErr   bool
	}{
		{namespace: "team.prod", wantErr: true},
		{namespace: strings.Repeat("a", 64), wantErr: true},
		{namespace: strings.Repeat("a", 63), wantErr: false},
	}
	for _, tc := range cases {
		app := &Application{
			APIVersion: SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   Metadata{Name: "test-app", Namespace: tc.namespace},
			Spec: ApplicationSpec{
				Components: []Component{
					{Name: "web", Type: "webservice", Properties: map[string]any{"image": "nginx:1.25"}},
				},
			},
		}
		err := validate(app)
		if !tc.wantErr {
			if err != nil {
				t.Errorf("namespace %q: unexpected error: %v", tc.namespace, err)
			}
			continue
		}
		want := fmt.Sprintf("metadata.namespace %q is not a valid DNS-1123 label (%s)", tc.namespace, namespaceLabelRule)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("namespace %q: error = %v, want it to contain %q", tc.namespace, err, want)
		}
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

// TestValidate_TopologySpreadTrait guards that standalone validation accepts the
// topology-spread trait, which TopologySpreadHandler ships, on every kind that
// generates a Deployment. The trait carries no component restriction: a
// component that generates no Deployment fails at build time instead.
func TestValidate_TopologySpreadTrait(t *testing.T) {
	for _, typ := range []string{"deployment", "webservice", "worker"} {
		app := &Application{
			APIVersion: SupportedAPIVersion,
			Kind:       "Application",
			Metadata:   Metadata{Name: "test-app"},
			Spec: ApplicationSpec{
				Components: []Component{
					{
						Name:       "api",
						Type:       typ,
						Properties: map[string]any{"image": "nginx:1.25"},
						Traits:     []Trait{{Type: "topology-spread"}},
					},
				},
			},
		}
		if err := validate(app); err != nil {
			t.Errorf("%s: unexpected error for topology-spread trait: %v", typ, err)
		}
	}
}

// TestValidate_ForceReplaceTrait guards that standalone validation accepts the
// force-replace trait, which ForceReplaceHandler ships (go-kure/launcher#406), on
// a job component — the case it exists for.
func TestValidate_ForceReplaceTrait(t *testing.T) {
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "test-app"},
		Spec: ApplicationSpec{
			Components: []Component{
				{
					Name:       "migrate",
					Type:       "job",
					Properties: map[string]any{"image": "ghcr.io/example/migrate:v1.0.0"},
					Traits:     []Trait{{Type: "force-replace"}},
				},
			},
		},
	}
	if err := validate(app); err != nil {
		t.Errorf("unexpected error for force-replace trait: %v", err)
	}
}
