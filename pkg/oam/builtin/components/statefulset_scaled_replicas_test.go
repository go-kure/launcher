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
// the kind is built without spec.replicas, the policy's ceiling has no count to
// act on, and the scaler enforces the same ceiling on its own maxReplicas
// (pkg/oam/builtin/traits). These tests run the handler directly, as
// TestDeploymentHandler_ScalerOwnsTheCount does for the Deployment, so no
// validation step stands between an authored count and the kind.
//
// The raw kind fills no replica default, so the policy's default is neither
// applied nor reported with or without a scaler: the former "not scaled keeps the
// default" half of this file is a "never filled" half (TestStatefulsetHandler_FillsNothing
// pins the same for the whole object).

// buildStatefulSet runs one statefulset component through the handler under
// policy, with the given extra properties over the smallest raw document and the
// given traits, and returns the StatefulSet. A policy refusal comes back as err.
func buildStatefulSet(t *testing.T, policy oam.Policy, props map[string]any, traits ...oam.Trait) (*appsv1.StatefulSet, stack.ApplicationConfig, error) {
	t.Helper()
	all := ptObject(t, stsPlain(htPlainPod))
	all["serviceName"] = "db-headless"
	for k, v := range props {
		all[k] = v
	}
	cfg, err := (&components.StatefulsetHandler{}).ToApplicationConfig(&oam.Component{
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
	for _, obj := range objects {
		if sts, ok := (*obj).(*appsv1.StatefulSet); ok {
			return sts, cfg, nil
		}
	}
	t.Fatal("Generate produced no StatefulSet")
	return nil, nil, nil
}

func scalerOnStatefulSet() oam.Trait {
	return oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}
}

// TestStatefulsetConfig_ScaledIsBuiltWithoutReplicas: with a scaler the
// StatefulSet has no spec.replicas, and an explicit count above the policy ceiling
// is not refused (a direct handler call reaches this without the validation that
// refuses the authored count).
func TestStatefulsetConfig_ScaledIsBuiltWithoutReplicas(t *testing.T) {
	two := int32(2)
	policy := &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &two}

	t.Run("no count", func(t *testing.T) {
		sts, cfg, err := buildStatefulSet(t, policy, nil, scalerOnStatefulSet())
		if err != nil {
			t.Fatalf("scaled StatefulSet refused: %v", err)
		}
		if sts.Spec.Replicas != nil {
			t.Errorf("spec.replicas = %d, want it unset", *sts.Spec.Replicas)
		}
		if _, ok := cfg.(oam.PolicyDefaultReporter); ok {
			t.Error("the config reports a policy default, though the kind applies none")
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

// TestStatefulsetConfig_NotScaledKeepsItsCountAndCeiling is the control: without a
// scaler the kind keeps the authored count, fills none when there is none, and the
// ceiling refuses an explicit count above it.
func TestStatefulsetConfig_NotScaledKeepsItsCountAndCeiling(t *testing.T) {
	two := int32(2)
	policy := &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &two}

	t.Run("no count stays unset", func(t *testing.T) {
		high := int32(10)
		sts, _, err := buildStatefulSet(t, &stubPolicy{defaultReplicas: int32ptr(3), maxReplicas: &high}, nil)
		if err != nil {
			t.Fatalf("StatefulSet refused: %v", err)
		}
		if sts.Spec.Replicas != nil {
			t.Errorf("spec.replicas = %d, want it unset: the policy default is not filled", *sts.Spec.Replicas)
		}
	})

	t.Run("authored count within the ceiling", func(t *testing.T) {
		sts, _, err := buildStatefulSet(t, policy, map[string]any{"replicas": 2})
		if err != nil {
			t.Fatalf("StatefulSet refused: %v", err)
		}
		if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 2 {
			t.Errorf("spec.replicas = %v, want the authored 2", sts.Spec.Replicas)
		}
	})

	t.Run("ceiling", func(t *testing.T) {
		_, _, err := buildStatefulSet(t, policy, map[string]any{"replicas": 5})
		if err == nil || !strings.Contains(err.Error(), "exceeds enforced maximum 2") {
			t.Errorf("replicas 5 over a ceiling of 2: error = %v, want the replica-maximum refusal", err)
		}
	})
}
