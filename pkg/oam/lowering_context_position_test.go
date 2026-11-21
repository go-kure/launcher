package oam

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// ctxSnapshot is what the LoweringContext of one lowering call holds of the fields
// the engine sets: the element being lowered, where it is, the allocator, the
// capability a rule reads and the two reference levels.
type ctxSnapshot struct {
	document, component, origin, namer bool
	capabilityFound                    bool
	image, source                      ImageReferenceLevel
}

// carrySeen records one snapshot per position. Each position reads a capability
// key of its own, so the consumed set shows which positions recorded their read.
type carrySeen map[string]ctxSnapshot

func (s carrySeen) note(position string, lctx LoweringContext) {
	_, found := lctx.Capability("cap-" + position)
	s[position] = ctxSnapshot{
		document:        lctx.Document != nil,
		component:       lctx.Component != nil,
		origin:          !reflect.ValueOf(lctx.Origin).IsZero(),
		namer:           lctx.Namer != nil,
		capabilityFound: found,
		image:           lctx.ImageLevel(),
		source:          lctx.SourceLevel(),
	}
}

type carryComponentRule struct{ seen carrySeen }

func (carryComponentRule) ComponentType() string { return "probe" }
func (r carryComponentRule) LowerComponent(_ *Component, lctx LoweringContext) (LoweringResult, error) {
	r.seen.note("component", lctx)
	return LoweringResult{Components: []Component{{Name: "w", Type: "widget"}}}, nil
}

type carryTraitRule struct{ seen carrySeen }

func (carryTraitRule) TraitType() string { return "probe" }
func (r carryTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	r.seen.note("trait", lctx)
	return LoweringResult{Traits: []Trait{{Type: "final", Properties: map[string]any{}}}}, nil
}

type carryPolicyRule struct{ seen carrySeen }

func (carryPolicyRule) PolicyType() string { return "probe" }
func (r carryPolicyRule) LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error) {
	r.seen.note("policy", lctx)
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: "final"}}}, nil
}

type carryDocumentRule struct{ seen carrySeen }

func (carryDocumentRule) Kind() string { return "Probe" }
func (r carryDocumentRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	r.seen.note("document", lctx)
	out := *doc
	out.Kind = terminalDocumentKind
	return LoweringResult{Documents: []Application{out}}, nil
}

type carryRawRule struct{ seen carrySeen }

func (carryRawRule) Kind() string                         { return "RawProbe" }
func (carryRawRule) DecodeDocument(_ []byte) (any, error) { return struct{}{}, nil }
func (r carryRawRule) LowerDocument(_ any, lctx LoweringContext) (LoweringResult, error) {
	r.seen.note("raw", lctx)
	name, err := lctx.Namer.Name("r", "1", lctx.Origin)
	if err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion, Kind: terminalDocumentKind, Metadata: Metadata{Name: name},
		Spec: ApplicationSpec{Components: []Component{{Name: "w", Type: "widget"}}},
	}}}, nil
}

// carryFillerHandler is a handler whose capability filler is asked for every
// widget. It records under the position it was given, so the two branches of the
// filler (provenance off and on) are told apart.
type carryFillerHandler struct {
	seen     carrySeen
	position *string
}

func (carryFillerHandler) CanHandle(t string) bool { return t == "widget" }
func (carryFillerHandler) ToApplicationConfig(_ *Component, _ string) (stack.ApplicationConfig, error) {
	return &imagedConfig{}, nil
}
func (h carryFillerHandler) FillCapabilityDefaults(props map[string]any, lctx LoweringContext) (map[string]any, error) {
	h.seen.note(*h.position, lctx)
	return props, nil
}

// TestLoweringContext_PositionsCarryTheirFields: every position the engine builds a
// LoweringContext at holds the fields of the transform context a rule may read, and
// the fields of its own element, as the context was built before it came from one
// constructor. A refactor that loses a field at one position fails here naming that
// position.
func TestLoweringContext_PositionsCarryTheirFields(t *testing.T) {
	seen := carrySeen{}
	filler := "filler"
	tr := NewTransformer(
		map[string]ComponentHandler{"widget": carryFillerHandler{seen, &filler}},
		map[string]TraitHandler{"final": &stubTraitHandler{typ: "final"}},
	)
	tr.RegisterPolicy("final", &stubPolicyHandler{typ: "final"})
	tr.RegisterComponentLowering(carryComponentRule{seen})
	tr.RegisterTraitLowering(carryTraitRule{seen})
	tr.RegisterPolicyLowering(carryPolicyRule{seen})
	tr.RegisterDocumentLowering(carryDocumentRule{seen})
	tr.RegisterRawDocumentLowering(carryRawRule{seen})

	positions := []string{"document", "component", "trait", "policy", "raw", "filler", "filler (provenance)"}
	caps := map[string]CapabilityBinding{}
	for _, p := range positions {
		caps["cap-"+p] = CapabilityBinding{Rendering: map[string]any{"at": p}}
	}
	// The levels differ from each other and from the defaults, so one cannot stand in
	// for the other and a lost field reads as the default.
	newCtx := func() TransformContext {
		return TransformContext{Capabilities: caps, Policy: bothLevelsPolicy{&NoopPolicy{}, ImageLevelDigest, ImageLevelVersion}}
	}

	app := makeApp("myapp", makeComponent("web", "probe"), makeComponent("tr", "widget"))
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	app.Spec.Components[1].Traits = []Trait{{Type: "probe"}}
	app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "probe"}}
	_, res, err := tr.TransformWithPolicy(app, newCtx())
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	consumed := map[string]bool{}
	for _, k := range res.ConsumedCapabilities {
		consumed[k] = true
	}

	doc := makeApp("doc", makeComponent("w", "widget"))
	doc.APIVersion, doc.Kind = SupportedAPIVersion, "Probe"
	_, docRes, err := tr.TransformWithPolicy(doc, newCtx())
	if err != nil {
		t.Fatalf("TransformWithPolicy of a document the document rule lowers: %v", err)
	}
	for _, k := range docRes.ConsumedCapabilities {
		consumed[k] = true
	}

	// The second branch of the filler runs when provenance is recorded.
	filler = "filler (provenance)"
	provApp := makeApp("provapp", makeComponent("w", "widget"))
	provApp.APIVersion, provApp.Kind = SupportedAPIVersion, terminalDocumentKind
	_, provRes, _, err := tr.TransformWithProvenance(provApp, newCtx())
	if err != nil {
		t.Fatalf("TransformWithProvenance: %v", err)
	}
	for _, k := range provRes.ConsumedCapabilities {
		consumed[k] = true
	}

	// A raw rule's read is not recorded: LowerRaws returns no PolicyResult. Hand it a
	// consumed set to see that nothing is written to it.
	rawConsumed := map[string]struct{}{}
	rawCtx := newCtx()
	rawCtx.consumedCapabilities = rawConsumed
	rawJSON, _ := json.Marshal(map[string]any{
		"apiVersion": SupportedAPIVersion, "kind": "RawProbe", "metadata": map[string]any{"name": "r"},
	})
	if _, err := tr.LowerRaws([]json.RawMessage{rawJSON}, rawCtx); err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}

	fields := func(document, component bool) ctxSnapshot {
		return ctxSnapshot{document: document, component: component, origin: true, namer: true, capabilityFound: true, image: ImageLevelDigest, source: ImageLevelVersion}
	}
	want := map[string]ctxSnapshot{
		"document":  fields(true, false),
		"component": fields(true, true),
		"trait":     fields(true, true),
		"policy":    fields(true, false),
		"raw":       fields(false, false),
		// The filler is handed a context with no element and no allocator.
		"filler":              {capabilityFound: true, image: ImageLevelDigest, source: ImageLevelVersion},
		"filler (provenance)": {capabilityFound: true, image: ImageLevelDigest, source: ImageLevelVersion},
	}
	for _, p := range positions {
		got, ok := seen[p]
		if !ok {
			t.Errorf("position %q was not asked", p)
			continue
		}
		if got != want[p] {
			t.Errorf("position %q: context holds %+v, want %+v", p, got, want[p])
		}
	}

	// Every position but the raw one records the capability it read, in the set the
	// result reports; the raw one writes nothing, whatever the transform context holds.
	for _, p := range positions {
		if p == "raw" {
			continue
		}
		if !consumed["cap-"+p] {
			t.Errorf("position %q: the capability it read is not in PolicyResult.ConsumedCapabilities", p)
		}
	}
	if len(rawConsumed) != 0 {
		t.Errorf("the raw position recorded its capability read in the consumed set: %v", rawConsumed)
	}
}
