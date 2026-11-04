package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
)

// An explicit `replicas` beside a scaler is refused when an application is
// validated (pkg/oam/validate.go:414-423), and a transformer with lowering
// rules registered validates after lowering, so the kind does not see one
// through it. A transformer without lowering rules skips that step
// (pkg/oam/lowering.go:1131), and the component handlers are exported through
// the builtin registry, so a library caller reaches ToApplicationConfig and
// ApplyPolicy without that validation; a direct handler call is one such route.
// The count the kind would have written is as unused as the policy default
// (go-kure/launcher#1179): the policy ceiling must not refuse a scaler-owned
// Deployment over a count no object carries. This is the ceiling's own case:
// the policy default is left out, so only the explicit count can trip it.
func TestDeploymentConfig_ScaledExplicitReplicasAreNotCheckedAgainstTheCeiling(t *testing.T) {
	two := int32(2)
	policy := &workloadPolicy{maxReplicas: &two}
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
	spread := oam.Trait{Type: "topology-spread", Properties: map[string]any{}}

	build := func(t *testing.T, traits ...oam.Trait) (*appsv1.Deployment, error) {
		t.Helper()
		h := &DeploymentHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name:       "app",
			Type:       "deployment",
			Properties: map[string]any{"image": "ghcr.io/org/app:v1", "replicas": 3},
			Traits:     traits,
		}, "default")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
			return nil, err
		}
		objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		for _, obj := range objects {
			if d, ok := (*obj).(*appsv1.Deployment); ok {
				return d, nil
			}
		}
		t.Fatal("Generate produced no Deployment")
		return nil, nil
	}

	orders := map[string][]oam.Trait{
		"scaler only":  {scaler},
		"scaler first": {scaler, spread},
		"spread first": {spread, scaler},
	}
	for name, traits := range orders {
		t.Run(name, func(t *testing.T) {
			if !slices.ContainsFunc(traits, func(tr oam.Trait) bool { return tr.Type == "scaler" }) {
				t.Fatal("test order carries no scaler")
			}
			dep, err := build(t, traits...)
			if err != nil {
				t.Fatalf("replicas 3 beside a scaler under a ceiling of 2 was refused: %v", err)
			}
			if dep.Spec.Replicas != nil {
				t.Errorf("spec.replicas = %d, want it unset", *dep.Spec.Replicas)
			}
		})
	}

	// Control: without the scaler the explicit count is the Deployment's own
	// and the ceiling refuses it.
	t.Run("no scaler", func(t *testing.T) {
		_, err := build(t)
		if err == nil || !strings.Contains(err.Error(), "exceeds enforced maximum 2") {
			t.Errorf("replicas 3 over a ceiling of 2: error = %v, want the replica-maximum refusal", err)
		}
	})
}
