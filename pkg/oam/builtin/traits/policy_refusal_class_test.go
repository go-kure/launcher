package traits_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestPolicyRefusalClass_TraitSubApplication: a refusal by the environment
// policy that a trait's sub-application raises yields the violation of the
// component the trait is on, with the class of the refusal
// (go-kure/launcher#849). The three traits whose sub-application the policy
// can refuse are the secret, the scaler and the pvc.
func TestPolicyRefusalClass_TraitSubApplication(t *testing.T) {
	cases := []struct {
		name   string
		trait  oam.Trait
		policy oam.Policy
		class  oam.RefusalClass
		text   string
	}{
		{
			name:   "secret under a policy that forbids explicit secrets",
			trait:  oam.Trait{Type: "secret", Properties: map[string]any{"name": "creds", "stringData": map[string]any{"password": secretSentinel}}},
			policy: noExplicitSecrets{},
			class:  oam.RefusalExplicitSecret,
			text:   `secret "creds": the environment policy forbids explicit secrets; reference a Secret created out of band instead`,
		},
		{
			name:   "scaler over the replica maximum",
			trait:  scalerTrait(map[string]any{"minReplicas": 1, "maxReplicas": 9}),
			policy: &stubScalerPolicy{maxReplicas: int32ptr32(3)},
			class:  oam.RefusalReplicaMaximum,
			text:   "replicas 9 exceeds enforced maximum 3",
		},
		{
			name:   "pvc over the storage maximum",
			trait:  oam.Trait{Type: "pvc", Properties: map[string]any{"name": "data", "size": "20Gi"}},
			policy: &stubPVCPolicy{maxStorageSize: "10Gi"},
			class:  oam.RefusalStorageMaximum,
			text:   `PVC "data": storageSize "20Gi" exceeds enforced maximum "10Gi"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := oam.NewTransformer(map[string]oam.ComponentHandler{"deployment": &components.DeploymentHandler{}}, nil)
			tr.RegisterBuiltinTrait("secret", &traits.SecretHandler{})
			tr.RegisterBuiltinTrait("scaler", &traits.ScalerHandler{})
			tr.RegisterBuiltinTrait("pvc", &traits.PVCHandler{})
			app := &oam.Application{
				APIVersion: oam.SupportedAPIVersion,
				Kind:       "Application",
				Metadata:   oam.Metadata{Name: "pkg", Namespace: "default"},
				Spec: oam.ApplicationSpec{Components: []oam.Component{{
					Name: "app", Type: "deployment",
					Properties: map[string]any{"image": "ghcr.io/org/app:v1", "replicas": 1},
					Traits:     []oam.Trait{tc.trait},
				}}},
			}
			_, err := tr.Transform(app, oam.TransformContext{Policy: tc.policy})
			if err == nil {
				t.Fatal("the transform succeeded, want a policy violation")
			}
			var v *oam.ViolationError
			if !errors.As(err, &v) {
				t.Fatalf("error is %T, want it to wrap *oam.ViolationError: %v", err, err)
			}
			if v.Component != "app" {
				t.Errorf("violation names component %q, want %q", v.Component, "app")
			}
			if v.Class != tc.class {
				t.Errorf("violation has class %q, want %q", v.Class, tc.class)
			}
			if want := `component "app": ` + tc.text; v.Error() != want {
				t.Errorf("violation reads %q, want %q", v.Error(), want)
			}
			if strings.Contains(err.Error(), secretSentinel) {
				t.Errorf("the refusal carries the value: %v", err)
			}
		})
	}
}
