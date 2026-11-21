package oam

import (
	"encoding/json"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// levelSeen records, by carrier, the image reference level the LoweringContext
// of each call carried.
type levelSeen map[string]ImageReferenceLevel

// seenComponentRule, seenTraitRule, seenPolicyRule, seenDocumentRule and
// seenRawRule are rules at each position that record the level of their
// context and emit nothing.
type seenComponentRule struct{ seen levelSeen }

func (seenComponentRule) ComponentType() string { return "probe" }
func (r seenComponentRule) LowerComponent(_ *Component, lctx LoweringContext) (LoweringResult, error) {
	r.seen["component rule"] = lctx.ImageLevel()
	return LoweringResult{Components: []Component{{Name: "w", Type: "widget"}}}, nil
}

type seenTraitRule struct{ seen levelSeen }

func (seenTraitRule) TraitType() string { return "probe" }
func (r seenTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	r.seen["trait rule"] = lctx.ImageLevel()
	return LoweringResult{Traits: []Trait{{Type: "final", Properties: map[string]any{}}}}, nil
}

type seenPolicyRule struct{ seen levelSeen }

func (seenPolicyRule) PolicyType() string { return "probe" }
func (r seenPolicyRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	r.seen["policy rule"] = lctx.ImageLevel()
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "final"}}}, nil
}

type seenDocumentRule struct{ seen levelSeen }

func (seenDocumentRule) Kind() string { return "Probe" }
func (r seenDocumentRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	r.seen["document rule"] = lctx.ImageLevel()
	out := *doc
	out.Kind = terminalDocumentKind
	return LoweringResult{Documents: []Application{out}}, nil
}

type seenRawRule struct{ seen levelSeen }

func (seenRawRule) Kind() string                         { return "RawProbe" }
func (seenRawRule) DecodeDocument(_ []byte) (any, error) { return struct{}{}, nil }
func (r seenRawRule) LowerDocument(_ any, lctx LoweringContext) (LoweringResult, error) {
	r.seen["raw document rule"] = lctx.ImageLevel()
	name, err := lctx.Namer.Name("r", "1", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion, Kind: terminalDocumentKind, Metadata: Metadata{Name: name},
		Spec: ApplicationSpec{Components: []Component{{Name: "w", Type: "widget"}}},
	}}}, nil
}

// seenHandler is a handler that records the level its Outputs and its
// Endpoints were asked at.
type seenHandler struct{ seen levelSeen }

func (seenHandler) CanHandle(t string) bool { return t == "widget" }
func (seenHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &imagedConfig{}, nil
}
func (h seenHandler) Outputs(_ *Component, lctx LoweringContext) ([]OutputDecl, error) {
	h.seen["handler outputs"] = lctx.ImageLevel()
	return nil, nil
}
func (h seenHandler) EndpointsNamed(_ *Component, lctx LoweringContext) ([]netpol.Endpoint, error) {
	h.seen["endpoints"] = lctx.ImageLevel()
	return nil, nil
}

// TestImageLevel_ReachesEveryCarrier: every context the Transformer builds
// carries the policy's level, whichever position builds it: a component rule,
// a trait rule, a policy rule, a document rule, a raw document rule, a
// handler's outputs. The endpoint query asks at the loosest level, as it only
// reads ports and names (go-kure/launcher#1644). A carrier that is missed reads
// the default level, so a test that asks under `any` sees it.
func TestImageLevel_ReachesEveryCarrier(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
		want   ImageReferenceLevel
	}{
		{"no policy", nil, ImageLevelTagged},
		{"any", levelPolicy{&NoopPolicy{}, ImageLevelAny}, ImageLevelAny},
		{"digest", levelPolicy{&NoopPolicy{}, ImageLevelDigest}, ImageLevelDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := levelSeen{}
			h := seenHandler{seen}
			tr := NewTransformer(map[string]ComponentHandler{"widget": h}, map[string]TraitHandler{"final": &stubTraitHandler{typ: "final"}})
			tr.RegisterPolicy("final", &stubPolicyHandler{typ: "final"})
			tr.RegisterComponentLowering(seenComponentRule{seen})
			tr.RegisterTraitLowering(seenTraitRule{seen})
			tr.RegisterPolicyLowering(seenPolicyRule{seen})
			tr.RegisterDocumentLowering(seenDocumentRule{seen})
			tr.RegisterRawDocumentLowering(seenRawRule{seen})

			app := makeApp("myapp", makeComponent("web", "probe"), makeComponent("tr", "widget"))
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			app.Spec.Components[1].Traits = []Trait{{Type: "probe"}}
			app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "probe"}}
			if _, err := tr.Transform(app, TransformContext{Policy: tc.policy}); err != nil {
				t.Fatalf("Transform: %v", err)
			}

			doc := makeApp("doc", makeComponent("w", "widget"))
			doc.APIVersion, doc.Kind = SupportedAPIVersion, "Probe"
			if _, err := tr.Transform(doc, TransformContext{Policy: tc.policy}); err != nil {
				t.Fatalf("Transform of a document the document rule lowers: %v", err)
			}

			raw, _ := json.Marshal(map[string]any{
				"apiVersion": SupportedAPIVersion, "kind": "RawProbe", "metadata": map[string]any{"name": "r"},
			})
			if _, err := tr.LowerRaws([]json.RawMessage{raw}, TransformContext{Policy: tc.policy}); err != nil {
				t.Fatalf("LowerRaws: %v", err)
			}

			if _, err := tr.ComponentEndpoints(&Component{Name: "w", Type: "widget"}); err != nil {
				t.Fatalf("ComponentEndpoints: %v", err)
			}

			for _, carrier := range []string{"component rule", "trait rule", "policy rule", "document rule", "raw document rule", "handler outputs"} {
				if got, ok := seen[carrier]; !ok {
					t.Errorf("%s was not asked", carrier)
				} else if got != tc.want {
					t.Errorf("%s carried level %q, want %q", carrier, got, tc.want)
				}
			}
			if got := seen["endpoints"]; got != ImageLevelAny {
				t.Errorf("the endpoint query carried level %q, want %q: it reads ports and names, never the image", got, ImageLevelAny)
			}
		})
	}
}
