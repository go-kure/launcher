package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestExposeRule_EvaluateProfile_GatewayValidation is the C6 regression test:
// ExposeRule is registered as a TraitLoweringRule, not a TraitHandler, so
// EvaluateProfile's rule-registry fallback (transform.go) is the only place its
// ValidateAndApplyDefaults still runs. This must keep behaving exactly as
// ExposeHandler.ValidateAndApplyDefaults did when it was reachable through
// traitHandlers directly.
func TestExposeRule_EvaluateProfile_GatewayValidation(t *testing.T) {
	transformer := oam.NewTransformer(nil, nil)
	transformer.RegisterTraitLowering(traits.ExposeRule{})

	t.Run("gateway without gatewayName is rejected", func(t *testing.T) {
		profile, err := oam.ParseClusterProfile([]byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
`))
		if err != nil {
			t.Fatalf("ParseClusterProfile: %v", err)
		}
		_, err = transformer.EvaluateProfile(profile)
		if err == nil {
			t.Fatal("want error: gatewayName is required when controllerType is \"gateway\"")
		}
		if !strings.Contains(err.Error(), "gatewayName") {
			t.Errorf("error = %q, want mention of gatewayName", err.Error())
		}
	})

	t.Run("gateway without gatewayNamespace defaults to gateway-system", func(t *testing.T) {
		profile, err := oam.ParseClusterProfile([]byte(`apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: test-cluster
spec:
  capabilities:
    expose:
      rendering:
        controllerType: gateway
        gatewayName: public
`))
		if err != nil {
			t.Fatalf("ParseClusterProfile: %v", err)
		}
		evaluated, err := transformer.EvaluateProfile(profile)
		if err != nil {
			t.Fatalf("EvaluateProfile: %v", err)
		}
		got := evaluated.Spec.Capabilities["expose"].Rendering["gatewayNamespace"]
		if got != "gateway-system" {
			t.Errorf("gatewayNamespace = %v, want gateway-system", got)
		}
	})
}
