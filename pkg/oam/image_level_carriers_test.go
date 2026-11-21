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

// Outputs is asked before the rule runs, on a copy of the component and a
// context of the build.
func (r seenComponentRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	r.seen["rule outputs, component"] = c.ImageLevel()
	r.seen["rule outputs, context"] = lctx.ImageLevel()
	return nil, nil
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
func (h seenHandler) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	h.seen["handler outputs"] = lctx.ImageLevel()
	h.seen["handler outputs, component"] = c.ImageLevel()
	return nil, nil
}
func (h seenHandler) EndpointsNamed(_ *Component, lctx LoweringContext) ([]netpol.Endpoint, error) {
	h.seen["endpoints"] = lctx.ImageLevel()
	return nil, nil
}

// levelOutputsHandler and levelOutputsRule answer an outputs query and record
// the level they read off the component and off the context.
type levelOutputsHandler struct{ seen levelSeen }

func (levelOutputsHandler) CanHandle(t string) bool { return t == "lvl-handler" }
func (levelOutputsHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &imagedConfig{}, nil
}
func (h levelOutputsHandler) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	h.seen["handler, component"] = c.ImageLevel()
	h.seen["handler, context"] = lctx.ImageLevel()
	return nil, nil
}

type levelOutputsRule struct{ seen levelSeen }

func (levelOutputsRule) ComponentType() string { return "lvl-rule" }
func (levelOutputsRule) LowerComponent(*Component, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r levelOutputsRule) Outputs(c *Component, lctx LoweringContext) ([]OutputDecl, error) {
	r.seen["rule, component"] = c.ImageLevel()
	r.seen["rule, context"] = lctx.ImageLevel()
	return nil, nil
}

// TestImageLevel_OutputsQueryAsksAtTheLevelOfItsContext: a rule asks the
// outputs of a component it emits through ComponentOutputsWithin, and the level
// of its context is the level of that query. A handler-backed provider reads
// it off the component and a rule-backed one off the context, and both read the
// same level: only the endpoint query is syntax-only (go-kure/launcher#1644).
func TestImageLevel_OutputsQueryAsksAtTheLevelOfItsContext(t *testing.T) {
	for _, level := range []ImageReferenceLevel{ImageLevelAny, ImageLevelVersion, ImageLevelDigest} {
		t.Run(string(level), func(t *testing.T) {
			seen := levelSeen{}
			tr := NewTransformer(map[string]ComponentHandler{"lvl-handler": levelOutputsHandler{seen}}, nil)
			tr.RegisterComponentLowering(levelOutputsRule{seen})
			lctx := LoweringContext{Namer: NewNameAllocator(), imageLevel: level}
			for _, typ := range []string{"lvl-handler", "lvl-rule"} {
				if _, err := tr.ComponentOutputsWithin(lctx, &Component{Name: "q", Type: typ, Properties: map[string]any{}}, nil); err != nil {
					t.Fatalf("ComponentOutputsWithin(%s): %v", typ, err)
				}
			}
			for _, reader := range []string{"handler, component", "handler, context", "rule, component", "rule, context"} {
				if got, ok := seen[reader]; !ok {
					t.Errorf("%s was not asked", reader)
				} else if got != level {
					t.Errorf("%s read level %q, want %q", reader, got, level)
				}
			}
		})
	}
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

			// A provider reads the level off its context or off the component it is
			// handed: in the outputs query of a build, of a rule and of a handler,
			// both are the policy's level.
			for _, carrier := range []string{"component rule", "trait rule", "policy rule", "document rule", "raw document rule", "handler outputs", "handler outputs, component", "rule outputs, component", "rule outputs, context"} {
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
