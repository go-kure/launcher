package traits_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// ingressWildcardApp builds a webservice + plain ingress trait that authors
// allowedHostnameWildcard inline — bypassing the "expose" trait (and its own
// PlatformReserved marking on ExposeRule.PropertySchema) entirely by targeting
// IngressHandler directly.
func ingressWildcardApp(wildcard string) *oam.Application {
	return &oam.Application{
		Metadata: oam.Metadata{Name: "myapp", Namespace: "default"},
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{{
				Name:       "web",
				Type:       "webservice",
				Properties: map[string]any{"image": "nginx:1.25", "port": 8080},
				Traits: []oam.Trait{{
					Type: "ingress",
					Properties: map[string]any{
						"rules": []any{map[string]any{
							"host":  "example.com",
							"paths": []any{map[string]any{"path": "/"}},
						}},
						"allowedHostnameWildcard": wildcard,
					},
				}},
			}},
		},
	}
}

func ingressWildcardTransformer() *oam.Transformer {
	tr := oam.NewTransformer(nil, nil)
	tr.RegisterComponent("webservice", &components.WebserviceHandler{})
	tr.RegisterBuiltinTrait("ingress", &traits.IngressHandler{})
	return tr
}

// TestIngressHandler_AllowedHostnameWildcard_InlineAuthoringRejected is the Finding-1
// regression proof: before this fix, IngressHandler.PropertySchema() described
// allowedHostnameWildcard as platform-reserved in prose only (the doc comment above
// PropertySchema), without the PropertySchema.PlatformReserved flag that actually
// wires into enforcePlatformReserved (transform.go's applyTraits, for a non-sealed,
// directly-authored trait). That let an author bypass hostname-wildcard enforcement by
// authoring the "ingress" trait directly instead of going through "expose" (whose own
// ExposeRule.PropertySchema already reserved this key). This test proves the bypass is
// now closed: authoring allowedHostnameWildcard inline on a directly-authored ingress
// trait is rejected exactly like an authored networkPolicy is (platform_reserved_test.go,
// TestLower_RejectsAuthoredPlatformReservedTraitProperty, mirrored here for the ingress
// trait handler position instead of the synthetic trait-lowering-rule position).
func TestIngressHandler_AllowedHostnameWildcard_InlineAuthoringRejected(t *testing.T) {
	tr := ingressWildcardTransformer()
	_, err := tr.Transform(ingressWildcardApp("*.apps.example.com"), oam.TransformContext{Namespace: "default"})
	if err == nil {
		t.Fatal("want error: allowedHostnameWildcard is platform-reserved and must not be authorable inline on ingress")
	}
	if !stderrors.Is(err, oam.ErrPlatformReserved) {
		t.Errorf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	if !strings.Contains(err.Error(), "allowedHostnameWildcard") {
		t.Errorf("expected the error to name allowedHostnameWildcard, got: %v", err)
	}
}

// TestIngressHandler_AllowedHostnameWildcard_ReservedAtEveryDeclaration pins that both
// schemas which read this key from capability rendering (IngressHandler directly, and
// ExposeRule which delegates to it) declare the same reservation — the mismatch this
// fix closes.
func TestIngressHandler_AllowedHostnameWildcard_ReservedAtEveryDeclaration(t *testing.T) {
	declarations := map[string]oam.PropertySchemaProvider{
		"ingress": &traits.IngressHandler{},
		"expose":  &traits.ExposeRule{},
	}
	for name, provider := range declarations {
		t.Run(name, func(t *testing.T) {
			schema, ok := provider.PropertySchema()["allowedHostnameWildcard"]
			if !ok {
				t.Fatalf("%s declares no allowedHostnameWildcard property", name)
			}
			if !schema.PlatformReserved {
				t.Errorf("%s must declare allowedHostnameWildcard platform-reserved", name)
			}
		})
	}
}
