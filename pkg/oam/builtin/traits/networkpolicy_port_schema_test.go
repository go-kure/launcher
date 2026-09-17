package traits_test

import (
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/traits"
)

// TestNetworkPolicyPortSchema_UnsupportedKeyRejectedAtSchemaLayer pins the fix for a
// GitHub Codex review thread on go-kure/launcher#440 (networkpolicy.go:65, P1): the
// `port` item's schema used AdditionalProperties to admit `port` itself (undeclared,
// because PropertySchema has no int-or-string union type), which as a side effect also
// silently admitted a genuinely unknown key like `protcol` at the schema layer, leaving
// parseNPPort's own validNPPortKeys as the only thing rejecting it -- contradicting the
// schema's own AdditionalProperties: true, which docs/oam/design-gvk.md's stability
// promise treats as a real contract, not documentation.
//
// Fixed by declaring `port` explicitly (matching the `cpu`/`memory` and
// `maxUnavailable`/`maxSurge` no-declared-Type idiom used elsewhere in this codebase)
// so AdditionalProperties can default to false. This test exercises the real authored
// pipeline (Transformer.ValidateAuthoredProperties with the actual NetworkPolicyHandler
// registered, not a fixture), because that is where the schema layer's rejection -- and
// its message, different from parseNPPort's own -- actually surfaces to an author.
func TestNetworkPolicyPortSchema_UnsupportedKeyRejectedAtSchemaLayer(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{
		"networkpolicy": &traits.NetworkPolicyHandler{},
	})
	app := &oam.Application{
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name: "web",
					Type: "webservice",
					Traits: []oam.Trait{
						{
							Type: "networkpolicy",
							Properties: map[string]any{
								"ingress": []any{
									map[string]any{
										"ports": []any{
											map[string]any{"port": 53, "protcol": "UDP"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := tr.ValidateAuthoredProperties(app)
	if err == nil {
		t.Fatal("a mistyped 'protcol' key must be rejected, not silently admitted")
	}
	if !strings.Contains(err.Error(), `unsupported field "protcol"`) {
		t.Errorf("error = %v, want it to name the offending key", err)
	}
	// The allowed list is part of the contract, same as every other closed key set in
	// this codebase (property_validate_authored_test.go).
	if !strings.Contains(err.Error(), "port") || !strings.Contains(err.Error(), "protocol") {
		t.Errorf("error = %v, want the allowed-field list to name port and protocol", err)
	}
}

// TestNetworkPolicyPortSchema_KnownKeysStillAccepted is the control for the test above:
// a schema tightened to reject `protcol` must not also reject the two keys it should
// accept, `port` and `protocol` themselves -- including a bare numeric `port`, since
// declaring it with no Type is exactly what has to keep both accepted forms reachable.
func TestNetworkPolicyPortSchema_KnownKeysStillAccepted(t *testing.T) {
	tr := oam.NewTransformer(nil, map[string]oam.TraitHandler{
		"networkpolicy": &traits.NetworkPolicyHandler{},
	})
	app := &oam.Application{
		Spec: oam.ApplicationSpec{
			Components: []oam.Component{
				{
					Name: "web",
					Type: "webservice",
					Traits: []oam.Trait{
						{
							Type: "networkpolicy",
							Properties: map[string]any{
								"ingress": []any{
									map[string]any{
										"ports": []any{
											map[string]any{"port": 53, "protocol": "UDP"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := tr.ValidateAuthoredProperties(app); err != nil {
		t.Fatalf("a document using only the declared port/protocol keys must be accepted, got: %v", err)
	}
}
