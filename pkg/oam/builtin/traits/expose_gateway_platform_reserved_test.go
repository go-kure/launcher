package traits_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// exposeGatewayApp builds a webservice + expose trait that authors gatewayName
// inline under the gateway controllerType.
func exposeGatewayApp(gatewayName string) *oam.Application {
	return &oam.Application{
		APIVersion: oam.SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "expose",
					Properties: map[string]any{
						"hostnames":   []any{"shop.example.com"},
						"gatewayName": gatewayName,
					},
				}},
			}},
		},
	}
}

// TestExposeRule_GatewayName_InlineAuthoringRejected is the round-11-batch-2 Codex
// regression proof (pullrequestreview-4937433461, expose_rule.go:121 as reviewed):
// gatewayName/gatewayNamespace were declared on ExposeRule.PropertySchema without
// PlatformReserved, even though resolveCapability (transform.go) gives an authored
// inline trait property precedence over the platform's ClusterProfile-rendered one
// — so an application could author gatewayName inline under the gateway
// controllerType and attach its route to a DIFFERENT gateway than the platform
// selected, entirely bypassing the platform's own gateway choice. This proves the
// bypass is now closed: authoring gatewayName inline is rejected exactly like an
// authored controllerType already is (ingress_platform_reserved_test.go).
func TestExposeRule_GatewayName_InlineAuthoringRejected(t *testing.T) {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterBuiltinTraitLowering(traits.ExposeRule{})

	ctx := oam.TransformContext{
		Namespace: "default",
		Capabilities: map[string]oam.CapabilityBinding{
			"expose": {Rendering: map[string]any{
				"controllerType": "gateway",
				"gatewayName":    "platform-gw",
			}},
		},
	}

	_, err := tr.Transform(exposeGatewayApp("attacker-gw"), ctx)
	if err == nil {
		t.Fatal("want error: gatewayName is platform-reserved and must not be authorable inline on expose")
	}
	if !stderrors.Is(err, oam.ErrPlatformReserved) {
		t.Errorf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	if !strings.Contains(err.Error(), "gatewayName") {
		t.Errorf("expected the error to name gatewayName, got: %v", err)
	}
}

// TestExposeRule_Gateway_ReservedAtDeclaration pins that both gateway-path fields
// carry the reservation, matching the pattern
// TestIngressHandler_AllowedHostnameWildcard_ReservedAtEveryDeclaration and
// TestNetworkPolicy_ReservedAtEveryDeclaration already use for their own fields.
func TestExposeRule_Gateway_ReservedAtDeclaration(t *testing.T) {
	schema := (traits.ExposeRule{}).PropertySchema()
	for _, key := range []string{"gatewayName", "gatewayNamespace"} {
		field, ok := schema[key]
		if !ok {
			t.Fatalf("expose declares no %s property", key)
		}
		if !field.PlatformReserved {
			t.Errorf("expose must declare %s platform-reserved", key)
		}
	}
}
