package components_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestDeploymentConfig_ScaledStillCapsPolicyRequestDefaults pins that the
// scaler's ownership of the replica count (go-kure/launcher#1179) leaves the
// policy's resource handling alone: a scaled Deployment still gets the request
// cap of go-kure/launcher#1217, so a policy default request above the authored
// limit renders at the limit and the object stays admissible. The count itself
// is the scaler's: spec.replicas is unset.
func TestDeploymentConfig_ScaledStillCapsPolicyRequestDefaults(t *testing.T) {
	policy := &stubPolicy{defaultCPURequest: "200m", defaultMemoryRequest: "256Mi"}
	scaler := oam.Trait{Type: "scaler", Properties: map[string]any{"minReplicas": 1, "maxReplicas": 2}}

	build := func(t *testing.T, traits ...oam.Trait) *appsv1.Deployment {
		t.Helper()
		h := &components.DeploymentHandler{}
		cfg, err := h.ToApplicationConfig(&oam.Component{
			Name: "app",
			Type: "deployment",
			Properties: map[string]any{
				"image": "ghcr.io/org/app:v1",
				"resources": map[string]any{
					"limits": map[string]any{"cpu": "50m", "memory": "64Mi"},
				},
			},
			Traits: traits,
		}, "default")
		if err != nil {
			t.Fatalf("ToApplicationConfig: %v", err)
		}
		if err := cfg.(oam.Enforceable).ApplyPolicy(policy); err != nil {
			t.Fatalf("ApplyPolicy: %v", err)
		}
		objects, err := cfg.Generate(stack.NewApplication("app", "default", cfg))
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		for _, obj := range objects {
			if d, ok := (*obj).(*appsv1.Deployment); ok {
				return d
			}
		}
		t.Fatal("Generate produced no Deployment")
		return nil
	}

	want := map[corev1.ResourceName]string{corev1.ResourceCPU: "50m", corev1.ResourceMemory: "64Mi"}

	t.Run("scaled", func(t *testing.T) {
		dep := build(t, scaler)
		assertQuantities(t, "requests", dep.Spec.Template.Spec.Containers[0].Resources.Requests, want)
		if dep.Spec.Replicas != nil {
			t.Errorf("spec.replicas = %d, want it unset", *dep.Spec.Replicas)
		}
	})

	// Control: the same document without a scaler gets the same cap and keeps
	// its own count.
	t.Run("not scaled", func(t *testing.T) {
		dep := build(t)
		assertQuantities(t, "requests", dep.Spec.Template.Spec.Containers[0].Resources.Requests, want)
		if dep.Spec.Replicas == nil {
			t.Error("spec.replicas is unset, want the Deployment's own count")
		}
	})
}
