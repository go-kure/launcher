package policies_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/policies"
)

func TestHealthChecksHandler_CanHandle(t *testing.T) {
	h := &policies.HealthChecksHandler{}
	if !h.CanHandle("health-checks") {
		t.Error("expected CanHandle(\"health-checks\") = true")
	}
	if h.CanHandle("reconciliation") {
		t.Error("expected CanHandle(\"reconciliation\") = false")
	}
}

func TestHealthChecksHandler_ApplyAppendsChecks(t *testing.T) {
	h := &policies.HealthChecksHandler{}
	result := oam.NewPolicyResult()
	result.HealthCheckOverrides = []stack.HealthCheck{{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "existing",
		Namespace:  "default",
	}}
	policy := &oam.ApplicationPolicy{
		Name: "extra-checks",
		Type: "health-checks",
		Properties: map[string]any{
			"checks": []any{
				map[string]any{
					"apiVersion": "batch/v1",
					"kind":       "Job",
					"name":       "db-migrate",
					"namespace":  "tenant-a",
				},
				map[string]any{
					"apiVersion": "source.toolkit.fluxcd.io/v1",
					"kind":       "GitRepository",
					"name":       "application-source",
				},
			},
		},
	}

	if err := h.Apply(policy, nil, result); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	want := []stack.HealthCheck{
		{APIVersion: "apps/v1", Kind: "Deployment", Name: "existing", Namespace: "default"},
		{APIVersion: "batch/v1", Kind: "Job", Name: "db-migrate", Namespace: "tenant-a"},
		{APIVersion: "source.toolkit.fluxcd.io/v1", Kind: "GitRepository", Name: "application-source"},
	}
	if !reflect.DeepEqual(result.HealthCheckOverrides, want) {
		t.Errorf("HealthCheckOverrides = %#v, want %#v", result.HealthCheckOverrides, want)
	}
}

func TestHealthChecksHandler_ApplyRejectsInvalidChecks(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]any
		wantError  string
	}{
		{
			name:       "missing checks",
			properties: map[string]any{},
			wantError:  "missing required property 'checks'",
		},
		{
			name:       "checks is not a list",
			properties: map[string]any{"checks": "not-a-list"},
			wantError:  "property 'checks' must be a list",
		},
		{
			name:       "checks is empty",
			properties: map[string]any{"checks": []any{}},
			wantError:  "property 'checks' must not be empty",
		},
		{
			name:       "entry is not a map",
			properties: map[string]any{"checks": []any{"not-a-map"}},
			wantError:  "checks[0] must be a map",
		},
		{
			name: "apiVersion is missing",
			properties: map[string]any{"checks": []any{map[string]any{
				"kind": "Job",
				"name": "db-migrate",
			}}},
			wantError: "checks[0].apiVersion is required",
		},
		{
			name: "apiVersion is empty",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "",
				"kind":       "Job",
				"name":       "db-migrate",
			}}},
			wantError: "checks[0].apiVersion is required",
		},
		{
			name: "apiVersion has wrong type",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": 1,
				"kind":       "Job",
				"name":       "db-migrate",
			}}},
			wantError: "checks[0].apiVersion is required",
		},
		{
			name: "kind is missing",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"name":       "db-migrate",
			}}},
			wantError: "checks[0].kind is required",
		},
		{
			name: "kind is empty",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "",
				"name":       "db-migrate",
			}}},
			wantError: "checks[0].kind is required",
		},
		{
			name: "kind has wrong type",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       true,
				"name":       "db-migrate",
			}}},
			wantError: "checks[0].kind is required",
		},
		{
			name: "name is missing",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
			}}},
			wantError: "checks[0].name is required",
		},
		{
			name: "name is empty",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"name":       "",
			}}},
			wantError: "checks[0].name is required",
		},
		{
			name: "name has wrong type",
			properties: map[string]any{"checks": []any{map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"name":       []any{"db-migrate"},
			}}},
			wantError: "checks[0].name is required",
		},
	}

	h := &policies.HealthChecksHandler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &oam.ApplicationPolicy{
				Name:       "invalid-checks",
				Type:       "health-checks",
				Properties: tt.properties,
			}

			err := h.Apply(policy, nil, oam.NewPolicyResult())
			if err == nil {
				t.Fatal("Apply() error = nil, want error")
			}
			if !strings.Contains(err.Error(), `policy "invalid-checks"`) {
				t.Errorf("Apply() error = %q, want policy name", err)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Apply() error = %q, want substring %q", err, tt.wantError)
			}
		})
	}
}
