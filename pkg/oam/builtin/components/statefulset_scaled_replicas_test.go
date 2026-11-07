package components_test

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	appsv1 "k8s.io/api/apps/v1"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// Under a scaler the HorizontalPodAutoscaler owns the replica count of a
// StatefulSet as of a Deployment (go-kure/launcher#1179, go-kure/launcher#1268):
// the kind is built without spec.replicas, the policy's default and ceiling have
// no count to act on, and the scaler enforces the same ceiling on its own
// maxReplicas (pkg/oam/builtin/traits). These tests run the handler directly, as
// deployment_scaled_replicas_internal_test.go does for the Deployment, so no
// validation step stands between an authored count and the kind.

func buildStatefulSet(t *testing.T, policy oam.Policy, props map[string]any, traits ...oam.Trait) (*appsv1.StatefulSet, oam.PolicyDefaultReporter, error) {
	t.Helper()
	all := map[string]any{"image": "ghcr.io/org/db:v1", "serviceName": "db-headless"}
	for k, v := range props {
		all[k] = v
	}
	h := &components.StatefulsetHandler{}
	cfg, err := h.ToApplicationConfig(&oam.Component{
		Name: "db", Type: "statefulset", Properties: all, Traits: traits,
	}, "default")
	if err != nil {
		t.Fatalf("ToApplicationConfig: %v", err)
	}
	if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
		return nil, nil, err
	}
	objects, err := cfg.Generate(stack.NewApplication("db", "default", cfg))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	reporter, _ := cfg.(oam.PolicyDefaultReporter)
	for _, obj := range objects {
		if sts, ok := (*obj).(*appsv1.StatefulSet); ok {
			return sts, reporter, nil
		}
	}
	t.Fatal("Generate produced no StatefulSet")
	return nil, nil, nil
}

func reportsReplicasDefault(r oam.PolicyDefaultReporter) bool {
	if r == nil {
		return false
	}
	for _, d := range r.PolicyDefaults() {
		if d.Path == "replicas" {
			return true
		}
	}
	return false
}

func scalerOnStatefulSet() oam.Trait {
	return oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
}

// TestStatefulsetConfig_ScaledIsBuiltWithoutReplicas: with a scaler the
// StatefulSet has no spec.replicas, the policy default is neither applied nor
// reported, and an explicit count above the policy ceiling is not refused (a
// direct handler call reaches this without the validation that refuses the
// authored count).
func TestStatefulsetConfig_ScaledIsBuiltWithoutReplicas(t *testing.T) {
	two := int32(2)
	policy := &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &two}

	t.Run("policy default", func(t *testing.T) {
		sts, reporter, err := buildStatefulSet(t, policy, nil, scalerOnStatefulSet())
		if err != nil {
			t.Fatalf("scaled StatefulSet refused: %v", err)
		}
		if sts.Spec.Replicas != nil {
			t.Errorf("spec.replicas = %d, want it unset", *sts.Spec.Replicas)
		}
		if reportsReplicasDefault(reporter) {
			t.Error("the policy replicas default is reported as applied to a count the scaler owns")
		}
	})

	t.Run("explicit count over the ceiling", func(t *testing.T) {
		sts, _, err := buildStatefulSet(t, policy, map[string]any{"replicas": 5}, scalerOnStatefulSet())
		if err != nil {
			t.Fatalf("replicas 5 beside a scaler under a ceiling of 2 was refused: %v", err)
		}
		if sts.Spec.Replicas != nil {
			t.Errorf("spec.replicas = %d, want it unset", *sts.Spec.Replicas)
		}
	})
}

// TestStatefulsetConfig_NotScaledKeepsReplicasDefaultAndCeiling is the control:
// without a scaler the kind keeps its own count, the policy default fills it and
// is reported, and the ceiling refuses an explicit count above it.
func TestStatefulsetConfig_NotScaledKeepsReplicasDefaultAndCeiling(t *testing.T) {
	two := int32(2)
	policy := &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &two}

	t.Run("default", func(t *testing.T) {
		high := int32(10)
		sts, reporter, err := buildStatefulSet(t, &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &high}, nil)
		if err != nil {
			t.Fatalf("StatefulSet refused: %v", err)
		}
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 3 {
			t.Errorf("spec.replicas = %v, want the policy default 3", sts.Spec.Replicas)
		}
		if !reportsReplicasDefault(reporter) {
			t.Error("the policy replicas default is not reported")
		}
	})

	t.Run("ceiling", func(t *testing.T) {
		_, _, err := buildStatefulSet(t, policy, map[string]any{"replicas": 5})
		if err == nil || !strings.Contains(err.Error(), "exceeds enforced maximum 2") {
			t.Errorf("replicas 5 over a ceiling of 2: error = %v, want the replica-maximum refusal", err)
		}
	})
}
