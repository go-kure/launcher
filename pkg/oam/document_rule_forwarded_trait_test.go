package oam

import (
	stderrors "errors"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- go-kure/launcher#603: a document rule's by-value copy of an authored trait ----

// docTraitCopyRule lowers its kind to a terminal Application, forwarding the
// components it was handed. By pointer (byValue false) the output reuses the very
// component slice it was handed; by value it rebuilds every component with a new
// Traits slice copied from the authored one, the shape that needs the forwarding
// mark (go-kure/launcher#603). edit, when set, changes each copied trait before it
// is returned. mutate, when set, writes into the document the rule was handed, the
// thing LoweringContext.Document forbids, so a test can prove it cannot reach the
// authored document.
type docTraitCopyRule struct {
	byValue bool
	edit    func(*Trait)
	mutate  bool
}

func (docTraitCopyRule) Kind() string { return "DocTraitCopy" }

func (r docTraitCopyRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	comps := doc.Spec.Components
	if r.byValue {
		comps = make([]Component, len(doc.Spec.Components))
		for i, comp := range doc.Spec.Components {
			comp.Traits = append([]Trait(nil), comp.Traits...)
			if r.edit != nil {
				for k := range comp.Traits {
					r.edit(&comp.Traits[k])
				}
			}
			comps[i] = comp
		}
	}
	if r.mutate {
		doc.Spec.Components[0].Name = "rewritten"
		doc.Spec.Components[0].Traits[0] = Trait{Type: "rewritten", Properties: map[string]any{}}
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: comps},
	}}}, nil
}

// forwardingDocApp is a Forwarding document with one webservice component carrying
// one authored trait of traitType.
func forwardingDocApp(traitType string, props map[string]any) *Application {
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "DocTraitCopy",
		Metadata:   Metadata{Name: "myapp", Namespace: "test"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "webservice",
			Properties: map[string]any{"image": "nginx"},
			Traits:     []Trait{{Type: traitType, Properties: props}},
		}}},
	}
}

// renderRecordingTraitHandler is a CapabilityAware trait handler that records the
// properties applyTraits dispatched it with, capability rendering included.
type renderRecordingTraitHandler struct{ applied *map[string]any }

func (renderRecordingTraitHandler) CanHandle(t string) bool  { return t == "needs-cap" }
func (renderRecordingTraitHandler) CapabilityRequired() bool { return true }
func (h renderRecordingTraitHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.applied = trait.Properties
	return nil
}
func (renderRecordingTraitHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}

// forwardingDocTransformer registers rule and a webservice component handler, and
// returns the properties the needs-cap trait handler is applied with.
func forwardingDocTransformer(rule docTraitCopyRule) (*Transformer, *map[string]any) {
	applied := new(map[string]any)
	tr := NewTransformer(map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}}, nil)
	tr.RegisterTrait("needs-cap", renderRecordingTraitHandler{applied: applied})
	tr.RegisterDocumentLowering(rule)
	return tr, applied
}

func needsCapCapability() TransformContext {
	return TransformContext{Capabilities: map[string]CapabilityBinding{
		"needs-cap": {Rendering: map[string]any{"rendered": "from-profile"}},
	}}
}

// TestTransform_DocumentRuleTraitCopyGetsCapabilityRendering is go-kure/launcher#603:
// a document rule that forwards an authored trait by value used to have it sealed as
// its own output, so applyTraits skipped the trait's capability rendering.
func TestTransform_DocumentRuleTraitCopyGetsCapabilityRendering(t *testing.T) {
	tr, applied := forwardingDocTransformer(docTraitCopyRule{byValue: true})

	if _, err := tr.Transform(forwardingDocApp("needs-cap", map[string]any{"authored": "yes"}), needsCapCapability()); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := (*applied)["rendered"]; got != "from-profile" {
		t.Errorf("forwarded trait applied with %v; want the capability rendering merged in (rendered: from-profile)", *applied)
	}
	if got := (*applied)["authored"]; got != "yes" {
		t.Errorf("forwarded trait applied with %v; want its authored value kept", *applied)
	}
}

// TestTransform_DocumentRuleTraitCopyMissingCapabilityIsRejected: the forwarded copy
// is not sealed, so a required capability absent from the profile is enforced.
func TestTransform_DocumentRuleTraitCopyMissingCapabilityIsRejected(t *testing.T) {
	tr, _ := forwardingDocTransformer(docTraitCopyRule{byValue: true})

	_, err := tr.Transform(forwardingDocApp("needs-cap", map[string]any{}), TransformContext{})
	if !stderrors.Is(err, ErrMissingCapability) {
		t.Fatalf("expected ErrMissingCapability for the forwarded copy, got: %v", err)
	}
}

// TestTransform_DocumentRulePointerForwardedTraitMissingCapabilityIsRejected is the
// pointer-forwarding half: a rule that hands back the component slice it was given
// (now the engine's copy, not the authored document's) still has its traits
// recognised as forwarded through isForwardedComponent and left unsealed.
func TestTransform_DocumentRulePointerForwardedTraitMissingCapabilityIsRejected(t *testing.T) {
	tr, _ := forwardingDocTransformer(docTraitCopyRule{})

	_, err := tr.Transform(forwardingDocApp("needs-cap", map[string]any{}), TransformContext{})
	if !stderrors.Is(err, ErrMissingCapability) {
		t.Fatalf("expected ErrMissingCapability for the pointer-forwarded trait, got: %v", err)
	}
}

// TestTransform_DocumentRuleTraitCopyReservedValueIsRejected: an authored reserved
// value in a trait a document rule forwards by value is refused.
func TestTransform_DocumentRuleTraitCopyReservedValueIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("webservice", &pipelineComponentHandler{typ: "webservice"})
	tr.RegisterDocumentLowering(docTraitCopyRule{byValue: true})

	_, err := tr.Transform(forwardingDocApp("reserved-trait", authoredNetworkPolicy()), TransformContext{})
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected ErrPlatformReserved for the forwarded authored value, got: %v", err)
	}
}

// TestLower_DocumentRuleTraitForwarding pins the classification at document
// position: an unchanged copy and a pointer-forwarded component both leave the
// trait unsealed and unstamped, no forwarding mark survives the rule invocation, and
// a copy the rule changed is sealed as its own output.
func TestLower_DocumentRuleTraitForwarding(t *testing.T) {
	cases := map[string]struct {
		rule       docTraitCopyRule
		wantSealed bool
	}{
		"pointer":             {rule: docTraitCopyRule{}},
		"unchanged copy":      {rule: docTraitCopyRule{byValue: true}},
		"replaced properties": {rule: docTraitCopyRule{byValue: true, edit: func(tr *Trait) { tr.Properties = map[string]any{"k": "v"} }}, wantSealed: true},
		"new type":            {rule: docTraitCopyRule{byValue: true, edit: func(tr *Trait) { tr.Type = "rbac" }}, wantSealed: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tr.RegisterDocumentLowering(tc.rule)

			settled, err := tr.lower(forwardingDocApp("configmap", map[string]any{"k": "v"}), TransformContext{})
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			trait := settled[0].Spec.Components[0].Traits[0]
			if trait.sealed != tc.wantSealed {
				t.Errorf("trait sealed = %v, want %v (%+v)", trait.sealed, tc.wantSealed, trait)
			}
			if _, stamped := trait.Origin(); stamped != tc.wantSealed {
				t.Errorf("trait origin stamped = %v, want %v", stamped, tc.wantSealed)
			}
			if trait.synthesized {
				t.Error("a document rule's output is never synthesized")
			}
			if trait.forwardedFrom != nil {
				t.Error("the trait still carries its forwarding mark after the rule invocation")
			}
		})
	}
}

// TestLower_DocumentRuleDoesNotWriteThroughToAuthoredDocument: the rule is handed a
// copy, so neither a rule writing into it nor the engine stamping a component the
// rule forwarded by pointer changes the authored document, and its traits are never
// marked.
func TestLower_DocumentRuleDoesNotWriteThroughToAuthoredDocument(t *testing.T) {
	cases := map[string]docTraitCopyRule{
		"pointer forward": {},
		"by-value copy":   {byValue: true},
		"mutating rule":   {byValue: true, mutate: true},
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tr.RegisterDocumentLowering(rule)

			app := forwardingDocApp("configmap", map[string]any{"k": "v"})
			if _, err := tr.lower(app, TransformContext{}); err != nil {
				t.Fatalf("lower: %v", err)
			}
			comp := app.Spec.Components[0]
			if comp.Name != "web" {
				t.Errorf("authored component renamed to %q", comp.Name)
			}
			if _, stamped := comp.Origin(); stamped {
				t.Error("the authored component was origin-stamped through the rule's output")
			}
			trait := comp.Traits[0]
			if trait.Type != "configmap" || trait.sealed || trait.forwardedFrom != nil {
				t.Errorf("authored trait changed: %+v", trait)
			}
		})
	}
}

// sharedTraitCopyDocRule copies the first component's traits once and attaches that
// one new slice to two components it emits.
type sharedTraitCopyDocRule struct{}

func (sharedTraitCopyDocRule) Kind() string { return "SharedTraitCopy" }

func (sharedTraitCopyDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	shared := append([]Trait(nil), doc.Spec.Components[0].Traits...)
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{
			{Name: "web", Type: "webservice", Traits: shared},
			{Name: "web2", Type: "webservice", Traits: shared},
		}},
	}}}, nil
}

// sharedTraitCopyComponentRule is the component-position counterpart.
type sharedTraitCopyComponentRule struct{}

func (sharedTraitCopyComponentRule) ComponentType() string { return "shared-trait-copy" }

func (sharedTraitCopyComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	shared := append([]Trait(nil), comp.Traits...)
	return LoweringResult{Components: []Component{
		{Name: "web", Type: "webservice", Traits: shared},
		{Name: "web2", Type: "webservice", Traits: shared},
	}}, nil
}

// TestLower_SharedForwardedTraitCopyStaysUnsealed: a rule may attach one slice of
// forwarded copies to several components. The forwarding mark must survive until
// the rule's whole output is classified, or every component after the first seals
// the shared trait as the rule's own.
func TestLower_SharedForwardedTraitCopyStaysUnsealed(t *testing.T) {
	cases := map[string]struct {
		register func(*Transformer)
		app      *Application
	}{
		"document rule": {
			register: func(tr *Transformer) { tr.RegisterDocumentLowering(sharedTraitCopyDocRule{}) },
			app: func() *Application {
				app := forwardingDocApp("configmap", map[string]any{"k": "v"})
				app.Kind = "SharedTraitCopy"
				return app
			}(),
		},
		"component rule": {
			register: func(tr *Transformer) { tr.RegisterComponentLowering(sharedTraitCopyComponentRule{}) },
			app: func() *Application {
				app := forwardingDocApp("configmap", map[string]any{"k": "v"})
				app.Kind = terminalDocumentKind
				app.Spec.Components[0].Type = "shared-trait-copy"
				return app
			}(),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tc.register(tr)

			settled, err := tr.lower(tc.app, TransformContext{})
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			comps := settled[0].Spec.Components
			if len(comps) != 2 {
				t.Fatalf("settled components = %d, want 2", len(comps))
			}
			for _, comp := range comps {
				trait := comp.Traits[0]
				if trait.sealed || trait.forwardedFrom != nil {
					t.Errorf("component %q: shared forwarded trait = %+v, want unsealed with no mark left", comp.Name, trait)
				}
			}
		})
	}
}

// appendingDocRule appends a trait to the first component of the document it was
// handed (a mutation LoweringContext.Document forbids) and forwards the components.
type appendingDocRule struct{}

func (appendingDocRule) Kind() string { return "Appending" }

func (appendingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	doc.Spec.Components[0].Traits = append(doc.Spec.Components[0].Traits, Trait{Type: "configmap", Properties: map[string]any{"k": "appended"}})
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: doc.Spec.Components},
	}}}, nil
}

// TestLower_DocumentRuleCopyDetachesEmptyTraitSlices: an empty authored trait slice
// may share spare capacity with another component's traits. The copy the rule is
// handed gives it its own storage, so appending through it cannot overwrite the
// other component's authored trait.
func TestLower_DocumentRuleCopyDetachesEmptyTraitSlices(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterDocumentLowering(appendingDocRule{})

	backing := []Trait{{Type: "configmap", Properties: map[string]any{"k": "v"}}}
	app := forwardingDocApp("configmap", map[string]any{"k": "v"})
	app.Kind = "Appending"
	app.Spec.Components = []Component{
		{Name: "web", Type: "webservice", Traits: backing[:0]},
		{Name: "web2", Type: "webservice", Traits: backing},
	}
	if _, err := tr.lower(app, TransformContext{}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if got := app.Spec.Components[1].Traits[0].Properties["k"]; got != "v" {
		t.Errorf("the second authored component's trait was overwritten through the first one's spare capacity: k = %v", got)
	}
}
