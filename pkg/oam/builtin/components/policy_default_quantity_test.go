package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestApplyPolicy_NegativeResourceDefaultRefused proves every kind that applies
// the policy's cpu/memory defaults refuses a negative one (go-kure/launcher#628),
// for each of the four defaults, and still accepts zero. The defaults reach a
// handler only through a caller-supplied oam.Policy: kurel itself always passes
// NoopPolicy, whose defaults are empty.
func TestApplyPolicy_NegativeResourceDefaultRefused(t *testing.T) {
	type configHandler interface {
		ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error)
	}
	image := map[string]any{"image": "ghcr.io/org/app:v1"}
	kinds := []struct {
		kind  string
		h     configHandler
		props map[string]any
	}{
		{"deployment", &components.DeploymentHandler{}, image},
		{"statefulset", &components.StatefulsetHandler{}, image},
		{"daemonset", &components.DaemonsetHandler{}, image},
		{"job", &components.JobHandler{}, image},
		{"cronjob", &components.CronjobHandler{}, map[string]any{"image": "ghcr.io/org/app:v1", "schedule": "0 2 * * *"}},
		{"webservice", webserviceViaRule{}, image},
		{"postgresql", postgresqlViaRule{}, map[string]any{}},
	}
	defaults := []struct {
		field  string
		policy *stubPolicy
		name   string
	}{
		{"cpu request", &stubPolicy{defaultCPURequest: "-500m"}, "cpu"},
		{"memory request", &stubPolicy{defaultMemoryRequest: "-128Mi"}, "memory"},
		{"cpu limit", &stubPolicy{defaultCPULimit: "-1"}, "cpu"},
		{"memory limit", &stubPolicy{defaultMemoryLimit: "-1Gi"}, "memory"},
	}
	enforceable := func(t *testing.T, k configHandler, kind string, props map[string]any) oam.Enforceable {
		t.Helper()
		cfg, err := k.ToApplicationConfig(&oam.Component{Name: "app", Type: kind, Properties: props}, "default")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		return cfg.(oam.Enforceable)
	}
	for _, k := range kinds {
		t.Run(k.kind, func(t *testing.T) {
			for _, d := range defaults {
				err := enforceable(t, k.h, k.kind, k.props).ApplyPolicy(d.policy)
				want := "policy default for " + d.name + ": quantity must not be negative"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("negative %s default: error = %v, want %q", d.field, err, want)
				}
			}
			zero := &stubPolicy{defaultCPURequest: "0", defaultMemoryRequest: "0", defaultCPULimit: "0", defaultMemoryLimit: "0"}
			if err := enforceable(t, k.h, k.kind, k.props).ApplyPolicy(zero); err != nil {
				t.Errorf("zero defaults must be accepted, got %v", err)
			}
		})
	}
}
