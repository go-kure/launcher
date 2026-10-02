package oam

import (
	stderrors "errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- go-kure/launcher#612: a document rule renders a PlatformReserved value ---------

// capturingReservedSink is reservedSinkHandler that keeps the properties it was
// dispatched with, so a test can see the value that reached the handler.
type capturingReservedSink struct {
	reservedSinkHandler
	props map[string]any
}

func (h *capturingReservedSink) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.props = c.Properties
	return &stubAppConfig{}, nil
}

func capturingSinkTransformer() (*Transformer, *capturingReservedSink) {
	sink := &capturingReservedSink{}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("reserved-sink", sink)
	return tr, sink
}

// renderingDocRule builds one component "web" of type typ in a document of kind out
// (the terminal kind when empty), and lets fill write its properties.
type renderingDocRule struct {
	kind, out, typ string
	fill           func(comp *Component, doc *Application, lctx LoweringContext) error
}

func (r renderingDocRule) Kind() string { return r.kind }

func (r renderingDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	comp := Component{Name: "web", Type: r.typ, Properties: map[string]any{"image": "nginx"}}
	if err := r.fill(&comp, doc, lctx); err != nil {
		return LoweringResult{}, err
	}
	out := r.out
	if out == "" {
		out = terminalDocumentKind
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       out,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: []Component{comp}},
	}}}, nil
}

// renderNetpol renders the reserved networkPolicy from the capability, as a fresh
// map so a test may edit it without touching the capability.
func renderNetpol(comp *Component, _ *Application, lctx LoweringContext) error {
	return comp.RenderReserved("networkPolicy", map[string]any{
		"trafficSources": lctx.Capabilities["netpol"].Rendering["trafficSources"],
	})
}

// copyingComponentsDocRule forwards the components it was handed as by-value copies
// in a new slice: not pointer-identical, so not forwarded (isForwardedComponent).
type copyingComponentsDocRule struct{ kind string }

func (r copyingComponentsDocRule) Kind() string { return r.kind }

func (r copyingComponentsDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: slices.Clone(doc.Spec.Components)},
	}}}, nil
}

func emptyDoc(kind string) *Application {
	return &Application{APIVersion: SupportedAPIVersion, Kind: kind, Metadata: Metadata{Name: "myapp", Namespace: "test"}}
}

func expectRenderedNetpol(t *testing.T, sink *capturingReservedSink) {
	t.Helper()
	want := netpolCapability().Capabilities["netpol"].Rendering
	if got := sink.props["networkPolicy"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected the rendered networkPolicy %#v to reach the handler, got %#v", want, got)
	}
}

// TestTransform_DocumentRuleRenderedReservedIsAccepted: a document rule that renders
// the reserved value from LoweringContext.Capabilities through RenderReserved has it
// accepted end to end, and the value reaches the handler.
func TestTransform_DocumentRuleRenderedReservedIsAccepted(t *testing.T) {
	tr, sink := capturingSinkTransformer()
	tr.RegisterDocumentLowering(renderingDocRule{kind: "Rendering", typ: "reserved-sink", fill: renderNetpol})

	app := singleComponentApp("Rendering", "reserved-sink", map[string]any{"image": "nginx"})
	if _, err := tr.Transform(app, netpolCapability()); err != nil {
		t.Fatalf("a rendered reserved value must be accepted, got: %v", err)
	}
	expectRenderedNetpol(t, sink)
}

// TestTransform_EmptyDocumentRuleRenderedReservedIsAccepted: the same from a document
// with no components at all — the rule's only input is the document and capabilities.
func TestTransform_EmptyDocumentRuleRenderedReservedIsAccepted(t *testing.T) {
	tr, sink := capturingSinkTransformer()
	tr.RegisterDocumentLowering(renderingDocRule{kind: "Rendering", typ: "reserved-sink", fill: renderNetpol})

	if _, err := tr.Transform(emptyDoc("Rendering"), netpolCapability()); err != nil {
		t.Fatalf("a rendered reserved value from an empty document must be accepted, got: %v", err)
	}
	expectRenderedNetpol(t, sink)
}

// TestTransform_DocumentRuleRenderedTypedReservedIsAccepted: emission validation
// rewrites a rule's typed Go collection to map[string]any; the record still matches,
// since the value it serializes to is unchanged.
func TestTransform_DocumentRuleRenderedTypedReservedIsAccepted(t *testing.T) {
	tr, sink := capturingSinkTransformer()
	tr.RegisterDocumentLowering(renderingDocRule{kind: "Rendering", typ: "reserved-sink", fill: func(comp *Component, _ *Application, _ LoweringContext) error {
		return comp.RenderReserved("networkPolicy", map[string]string{"mode": "platform"})
	}})

	if _, err := tr.Transform(emptyDoc("Rendering"), TransformContext{}); err != nil {
		t.Fatalf("a rendered typed reserved value must be accepted, got: %v", err)
	}
	if got, want := sink.props["networkPolicy"], map[string]any{"mode": "platform"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected the normalized value %#v at the handler, got %#v", want, got)
	}
}

// copiedSources are the parts of a document rule's input an authored value can be
// copied from: a trait property, a policy property, metadata, and another authored
// component's property. The seed component's type has no schema, so nothing refuses
// its own value before the rule runs.
var copiedSources = []struct {
	name string
	read func(doc *Application) any
}{
	{"trait property", func(doc *Application) any { return doc.Spec.Components[0].Traits[0].Properties["networkPolicy"] }},
	{"policy property", func(doc *Application) any { return doc.Spec.Policies[0].Properties["networkPolicy"] }},
	{"metadata label", func(doc *Application) any {
		return map[string]any{"trafficSources": []any{map[string]any{"namespace": doc.Metadata.Labels["source"]}}}
	}},
	{"metadata annotation", func(doc *Application) any {
		return map[string]any{"trafficSources": []any{map[string]any{"namespace": doc.Metadata.Annotations["source"]}}}
	}},
	{"another component's property", func(doc *Application) any { return doc.Spec.Components[0].Properties["networkPolicy"] }},
}

// sourcedApp carries an authored networkPolicy, differing from the capability's, in
// every place copiedSources reads.
func sourcedApp(kind string) *Application {
	authored := func() map[string]any { return map[string]any{"trafficSources": []any{}} }
	return &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       kind,
		Metadata: Metadata{
			Name: "myapp", Namespace: "test",
			Labels:      map[string]string{"source": "from-label"},
			Annotations: map[string]string{"source": "from-annotation"},
		},
		Spec: ApplicationSpec{
			Components: []Component{{
				Name: "seed", Type: "unchecked",
				Properties: map[string]any{"networkPolicy": authored()},
				Traits:     []Trait{{Type: "anything", Properties: map[string]any{"networkPolicy": authored()}}},
			}},
			Policies: []ApplicationPolicy{{Name: "p", Type: "anything", Properties: map[string]any{"networkPolicy": authored()}}},
		},
	}
}

// TestTransform_DocumentRuleCopiedReservedIsRejected: an authored value a document
// rule copies into the reserved key is refused, from every part of its input — both
// with no record at all and over a value the rule did render, which the copy replaced.
func TestTransform_DocumentRuleCopiedReservedIsRejected(t *testing.T) {
	for _, src := range copiedSources {
		for _, renderFirst := range []bool{false, true} {
			name := src.name
			if renderFirst {
				name += " over a rendered value"
			}
			t.Run(name, func(t *testing.T) {
				tr, _ := capturingSinkTransformer()
				tr.RegisterDocumentLowering(renderingDocRule{kind: "Copying", typ: "reserved-sink", fill: func(comp *Component, doc *Application, lctx LoweringContext) error {
					if renderFirst {
						if err := renderNetpol(comp, doc, lctx); err != nil {
							return err
						}
					}
					comp.Properties["networkPolicy"] = src.read(doc)
					return nil
				}})
				_, err := tr.Transform(sourcedApp("Copying"), netpolCapability())
				expectPlatformReserved(t, err)
			})
		}
	}
}

// TestTransform_DocumentRuleEditedRenderedReservedIsRejected: the record vouches for
// the value it took, not for the key. Changing the rendered value afterwards, in
// place or by assignment, leaves the key authored.
func TestTransform_DocumentRuleEditedRenderedReservedIsRejected(t *testing.T) {
	tests := map[string]func(comp *Component) error{
		"edited in place": func(comp *Component) error {
			comp.Properties["networkPolicy"].(map[string]any)["mode"] = "open"
			return nil
		},
		"replaced by assignment": func(comp *Component) error {
			comp.Properties["networkPolicy"] = map[string]any{"mode": "open"}
			return nil
		},
		"changed by a later RenderReserved below it": func(comp *Component) error {
			return comp.RenderReserved("networkPolicy.mode", "open")
		},
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			tr, _ := capturingSinkTransformer()
			tr.RegisterDocumentLowering(renderingDocRule{kind: "Editing", typ: "reserved-sink", fill: func(comp *Component, doc *Application, lctx LoweringContext) error {
				if err := renderNetpol(comp, doc, lctx); err != nil {
					return err
				}
				return edit(comp)
			}})
			_, err := tr.Transform(emptyDoc("Editing"), netpolCapability())
			expectPlatformReserved(t, err)
		})
	}
}

// TestLower_RenderedReservedSurvivesIntoAComponentRule: a rendered component of a
// type a schema-declaring ComponentLoweringRule claims reaches that rule in the next
// round — through the round-start check and the rule's own pre-rule check — with the
// value intact.
func TestLower_RenderedReservedSurvivesIntoAComponentRule(t *testing.T) {
	next := &reservingComponentRule{}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(next)
	tr.RegisterDocumentLowering(renderingDocRule{kind: "Rendering", typ: next.ComponentType(), fill: renderNetpol})

	if _, err := tr.lower(emptyDoc("Rendering"), netpolCapability()); err != nil {
		t.Fatalf("a rendered reserved value must survive into the next round's rule, got: %v", err)
	}
	if next.seen["networkPolicy"] == nil {
		t.Fatalf("expected the rendered value to reach the next rule, got: %#v", next.seen)
	}
}

// TestTransform_RenderedReservedSurvivesALaterDocumentRule: a later document rule
// that forwards the component, or copies it by value, keeps its record through that
// round and into createApplications; one that rebuilds the component from its fields
// emits a new component with no record, and the value is refused.
func TestTransform_RenderedReservedSurvivesALaterDocumentRule(t *testing.T) {
	tests := []struct {
		name   string
		later  DocumentLoweringRule
		accept bool
	}{
		{"forwarded", forwardingComponentsDocRule{kind: "Later"}, true},
		{"copied by value", copyingComponentsDocRule{kind: "Later"}, true},
		{"rebuilt from its fields", forwardingDocRule{kind: "Later"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr, sink := capturingSinkTransformer()
			tr.RegisterDocumentLowering(renderingDocRule{kind: "Rendering", out: "Later", typ: "reserved-sink", fill: renderNetpol})
			tr.RegisterDocumentLowering(tc.later)

			_, err := tr.Transform(emptyDoc("Rendering"), netpolCapability())
			if !tc.accept {
				expectPlatformReserved(t, err)
				return
			}
			if err != nil {
				t.Fatalf("a rendered reserved value must survive the later rule, got: %v", err)
			}
			expectRenderedNetpol(t, sink)
		})
	}
}

// --- RenderReserved and the record, unit level ----------------------------------

func TestComponentRenderReserved_WritesAndRecords(t *testing.T) {
	var comp Component
	if err := comp.RenderReserved("tls.secretName", "shop-tls"); err != nil {
		t.Fatalf("RenderReserved: %v", err)
	}
	if want := map[string]any{"tls": map[string]any{"secretName": "shop-tls"}}; !reflect.DeepEqual(comp.Properties, want) {
		t.Fatalf("expected the properties map and the missing object to be created, got %#v", comp.Properties)
	}
	// An existing object along the path is written into, not replaced.
	if err := comp.RenderReserved("tls.issuer", "platform"); err != nil {
		t.Fatalf("RenderReserved: %v", err)
	}
	if want := map[string]any{"secretName": "shop-tls", "issuer": "platform"}; !reflect.DeepEqual(comp.Properties["tls"], want) {
		t.Fatalf("expected both values under tls, got %#v", comp.Properties["tls"])
	}
	if want := (renderedValues{"tls.secretName": "shop-tls", "tls.issuer": "platform"}); !reflect.DeepEqual(comp.rendered, want) {
		t.Fatalf("expected the record %#v, got %#v", want, comp.rendered)
	}
}

func TestComponentRenderReserved_RefusesAndLeavesTheComponentUnchanged(t *testing.T) {
	var typedNilMap map[string]any
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	secret := "s"
	tests := []struct {
		name  string
		path  string
		value any
		want  string
	}{
		{"empty path", "", "x", "empty path"},
		{"empty leading segment", ".tls", "x", "empty path segment"},
		{"empty inner segment", "tls..secretName", "x", "empty path segment"},
		{"empty trailing segment", "tls.", "x", "empty path segment"},
		{"array index", "rules[0].secret", "x", "indexes an array"},
		{"intermediate scalar", "image.tag", "x", "not an object"},
		{"intermediate typed map", "labels.app", "x", "not an object"},
		{"intermediate array", "rules.secret", "x", "not an object"},
		{"nil value", "tls.secretName", nil, "null"},
		{"typed nil value", "tls.secretName", typedNilMap, "null"},
		{"value holding a null", "tls", map[string]any{"secretName": nil}, "null"},
		{"value holding a null item", "tls", map[string]any{"hosts": []any{"a", nil}}, "null"},
		{"value holding a typed nil", "tls", map[string]any{"hosts": []string(nil)}, "null"},
		{"NaN", "tls.secretName", math.NaN(), "not a finite number"},
		{"infinity", "tls.secretName", map[string]any{"n": math.Inf(-1)}, "not a finite number"},
		{"channel", "tls.secretName", make(chan int), "not a property value"},
		{"pointer", "tls.secretName", &secret, "not a property value"},
		{"struct", "tls", struct{ SecretName string }{"s"}, "not a property value"},
		{"map with non-string keys", "tls", map[int]string{1: "s"}, "keys are not strings"},
		{"value containing itself", "tls", cyclic, "contains itself"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			comp := Component{Properties: map[string]any{
				"image":  "nginx",
				"labels": map[string]string{"tier": "web"},
				"rules":  []any{map[string]any{"secret": "s"}},
			}}
			before := copyPropertyMap(comp.Properties)
			err := comp.RenderReserved(tc.path, tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error mentioning %q, got: %v", tc.want, err)
			}
			if !reflect.DeepEqual(comp.Properties, before) {
				t.Errorf("a refused RenderReserved changed the properties: %#v", comp.Properties)
			}
			if comp.rendered != nil {
				t.Errorf("a refused RenderReserved recorded %#v", comp.rendered)
			}
		})
	}
}

// TestComponentRenderReserved_CopyOnWrite: recording on one copy of a component never
// reaches another copy, whichever was recorded first.
func TestComponentRenderReserved_CopyOnWrite(t *testing.T) {
	schema := reservedSinkHandler{}.PropertySchema()
	np := map[string]any{"mode": "platform"}

	authored := Component{Name: "web", Properties: map[string]any{"image": "nginx"}}
	rendered := authored
	if err := rendered.RenderReserved("networkPolicy", np); err != nil {
		t.Fatalf("RenderReserved: %v", err)
	}
	if authored.rendered != nil {
		t.Fatalf("recording on a copy recorded on the original: %#v", authored.rendered)
	}
	// The write is in place, so the original's shared properties map now holds the
	// value too — and without a record it is refused there.
	if err := enforcePlatformReserved(schema, authored.Properties, authored.rendered, "properties"); !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected the original, which recorded nothing, to be refused, got: %v", err)
	}
	if err := enforcePlatformReserved(schema, rendered.Properties, rendered.rendered, "properties"); err != nil {
		t.Fatalf("expected the copy that recorded the value to be accepted, got: %v", err)
	}

	later := rendered
	if err := later.RenderReserved("other", "x"); err != nil {
		t.Fatalf("RenderReserved: %v", err)
	}
	if len(rendered.rendered) != 1 || len(later.rendered) != 2 {
		t.Fatalf("recording on a later copy changed the earlier copy's record: %#v / %#v", rendered.rendered, later.rendered)
	}
}

// TestEnforcePlatformReserved_HonoursTheRecord covers the exemption at the walk: the
// recorded value, at the recorded path, and nothing else.
func TestEnforcePlatformReserved_HonoursTheRecord(t *testing.T) {
	record := func(path string, value any) renderedValues {
		var props map[string]any
		var r renderedValues
		if err := renderReserved(&props, &r, path, value); err != nil {
			t.Fatalf("renderReserved: %v", err)
		}
		return r
	}
	arraySchema := map[string]PropertySchema{
		"rules": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"secret": {Type: PropertyTypeString, PlatformReserved: true},
		}}},
	}
	dottedSchema := map[string]PropertySchema{
		"tls.secretName": {Type: PropertyTypeString, PlatformReserved: true},
		"tls": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"secretName": {Type: PropertyTypeString},
		}},
	}
	tests := []struct {
		name     string
		schema   map[string]PropertySchema
		props    map[string]any
		rendered renderedValues
		accept   bool
	}{
		{
			name:     "top-level value as recorded",
			props:    map[string]any{"networkPolicy": map[string]any{"mode": "platform"}},
			rendered: record("networkPolicy", map[string]any{"mode": "platform"}),
			accept:   true,
		},
		{
			name:     "nested value as recorded",
			props:    map[string]any{"tls": map[string]any{"secretName": "shop-tls"}},
			rendered: record("tls.secretName", "shop-tls"),
			accept:   true,
		},
		{
			// Emission validation's rewrite of a typed Go value keeps the value.
			name:     "same value in another Go type",
			props:    map[string]any{"networkPolicy": map[string]any{"replicas": 3, "mode": "platform"}},
			rendered: record("networkPolicy", map[string]any{"replicas": int64(3), "mode": "platform"}),
			accept:   true,
		},
		{
			name:     "different value",
			props:    map[string]any{"networkPolicy": map[string]any{"mode": "open"}},
			rendered: record("networkPolicy", map[string]any{"mode": "platform"}),
		},
		{
			name:     "recorded value at another path",
			props:    map[string]any{"networkPolicy": "shop-tls"},
			rendered: record("tls.secretName", "shop-tls"),
		},
		{
			// A reserved key nested in a recorded object needs its own record.
			name:     "reserved key inside a recorded object",
			props:    map[string]any{"tls": map[string]any{"secretName": "shop-tls"}},
			rendered: record("tls", map[string]any{"secretName": "shop-tls"}),
		},
		{
			name:     "explicit null against a record",
			props:    map[string]any{"networkPolicy": nil},
			rendered: record("networkPolicy", map[string]any{"mode": "platform"}),
		},
		{
			// An array item has no RenderReserved path; a record naming one by its
			// parent's key does not reach into the items.
			name:     "reserved key in an array item",
			schema:   arraySchema,
			props:    map[string]any{"rules": []any{map[string]any{"secret": "s"}}},
			rendered: renderedValues{"rules.secret": "s"},
		},
		{
			// A key holding a dot cannot be named by a path: the record of the nested
			// tls.secretName does not reach the top-level key spelled "tls.secretName".
			name:     "dotted key",
			schema:   dottedSchema,
			props:    map[string]any{"tls.secretName": "shop-tls"},
			rendered: record("tls.secretName", "shop-tls"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.schema
			if schema == nil {
				schema = reservedSchema()
			}
			err := enforcePlatformReserved(schema, tc.props, tc.rendered, "properties")
			if tc.accept {
				if err != nil {
					t.Fatalf("expected the recorded value to be accepted, got: %v", err)
				}
				return
			}
			if !stderrors.Is(err, ErrPlatformReserved) {
				t.Fatalf("expected ErrPlatformReserved, got: %v", err)
			}
		})
	}
}

// --- go-kure/launcher#612: the same for a trait a document rule builds -------------

// capturingReservedTrait is reservedTraitHandler that keeps the properties it was
// applied with, so a test can see the value that reached the handler.
type capturingReservedTrait struct {
	reservedTraitHandler
	props map[string]any
}

func (h *capturingReservedTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	h.props = trait.Properties
	return nil
}

func capturingTraitTransformer() (*Transformer, *capturingReservedTrait) {
	h := &capturingReservedTrait{}
	tr := reservedSinkTransformer()
	tr.RegisterTrait("reserved-trait", h)
	return tr, h
}

// traitRenderingDocRule builds one component "web" of type compType (reserved-sink
// when empty) carrying one trait of type traitType (reserved-trait when empty), in a
// document of kind out (the terminal kind when empty), and lets fill write the
// trait's properties.
type traitRenderingDocRule struct {
	kind, out, compType, traitType string
	fill                           func(trait *Trait, doc *Application, lctx LoweringContext) error
}

func (r traitRenderingDocRule) Kind() string { return r.kind }

func (r traitRenderingDocRule) LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error) {
	compType, traitType, out := r.compType, r.traitType, r.out
	if compType == "" {
		compType = "reserved-sink"
	}
	if traitType == "" {
		traitType = "reserved-trait"
	}
	if out == "" {
		out = terminalDocumentKind
	}
	comp := Component{
		Name:       "web",
		Type:       compType,
		Properties: map[string]any{"image": "nginx"},
		Traits:     []Trait{{Type: traitType, Properties: map[string]any{}}},
	}
	if err := r.fill(&comp.Traits[0], doc, lctx); err != nil {
		return LoweringResult{}, err
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       out,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: []Component{comp}},
	}}}, nil
}

// renderTraitNetpol is renderNetpol for a trait.
func renderTraitNetpol(trait *Trait, _ *Application, lctx LoweringContext) error {
	return trait.RenderReserved("networkPolicy", map[string]any{
		"trafficSources": lctx.Capabilities["netpol"].Rendering["trafficSources"],
	})
}

// rebuildingTraitsDocRule forwards the components it was handed with every trait
// rebuilt from its type and properties: a new trait, not a forwarded one, and with no
// record.
type rebuildingTraitsDocRule struct{ kind string }

func (r rebuildingTraitsDocRule) Kind() string { return r.kind }

func (r rebuildingTraitsDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	components := slices.Clone(doc.Spec.Components)
	for i := range components {
		traits := make([]Trait, 0, len(components[i].Traits))
		for _, trait := range components[i].Traits {
			traits = append(traits, Trait{Type: trait.Type, Properties: trait.Properties})
		}
		components[i].Traits = traits
	}
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec:       ApplicationSpec{Components: components},
	}}}, nil
}

func expectRenderedTraitNetpol(t *testing.T, h *capturingReservedTrait) {
	t.Helper()
	want := netpolCapability().Capabilities["netpol"].Rendering
	if got := h.props["networkPolicy"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected the rendered networkPolicy %#v to reach the trait handler, got %#v", want, got)
	}
}

// expectReservedTraitRefused is expectReservedTraitRejected for any refusal of the
// reserved-trait, wherever it happens.
func expectReservedTraitRefused(t *testing.T, err error) {
	t.Helper()
	expectReservedTraitRejected(t, err, `trait "reserved-trait"`)
}

// TestTransform_DocumentRuleRenderedReservedTraitIsAccepted: a trait a document rule
// builds, with the reserved value rendered from LoweringContext.Capabilities through
// Trait.RenderReserved, is accepted end to end, and the value reaches the handler.
func TestTransform_DocumentRuleRenderedReservedTraitIsAccepted(t *testing.T) {
	inputs := map[string]*Application{
		"from a document with a component": singleComponentApp("Rendering", "reserved-sink", map[string]any{"image": "nginx"}),
		"from an empty document":           emptyDoc("Rendering"),
	}
	for name, app := range inputs {
		t.Run(name, func(t *testing.T) {
			tr, h := capturingTraitTransformer()
			tr.RegisterDocumentLowering(traitRenderingDocRule{kind: "Rendering", fill: renderTraitNetpol})

			if _, err := tr.Transform(app, netpolCapability()); err != nil {
				t.Fatalf("a rendered reserved trait value must be accepted, got: %v", err)
			}
			expectRenderedTraitNetpol(t, h)
		})
	}
}

// TestTransform_DocumentRuleCopiedReservedTraitIsRejected is
// TestTransform_DocumentRuleCopiedReservedIsRejected for a trait: an authored value a
// document rule copies into the trait's reserved key is refused, from every part of
// its input, with no record and over a value the rule did render.
func TestTransform_DocumentRuleCopiedReservedTraitIsRejected(t *testing.T) {
	for _, src := range copiedSources {
		for _, renderFirst := range []bool{false, true} {
			name := src.name
			if renderFirst {
				name += " over a rendered value"
			}
			t.Run(name, func(t *testing.T) {
				tr, _ := capturingTraitTransformer()
				tr.RegisterDocumentLowering(traitRenderingDocRule{kind: "Copying", fill: func(trait *Trait, doc *Application, lctx LoweringContext) error {
					if renderFirst {
						if err := renderTraitNetpol(trait, doc, lctx); err != nil {
							return err
						}
					}
					trait.Properties["networkPolicy"] = src.read(doc)
					return nil
				}})
				_, err := tr.Transform(sourcedApp("Copying"), netpolCapability())
				expectReservedTraitRefused(t, err)
			})
		}
	}
}

// TestTransform_DocumentRuleEditedRenderedReservedTraitIsRejected: changing a trait's
// rendered value after recording it leaves the key authored.
func TestTransform_DocumentRuleEditedRenderedReservedTraitIsRejected(t *testing.T) {
	tests := map[string]func(trait *Trait) error{
		"edited in place": func(trait *Trait) error {
			trait.Properties["networkPolicy"].(map[string]any)["mode"] = "open"
			return nil
		},
		"replaced by assignment": func(trait *Trait) error {
			trait.Properties["networkPolicy"] = map[string]any{"mode": "open"}
			return nil
		},
		"changed by a later RenderReserved below it": func(trait *Trait) error {
			return trait.RenderReserved("networkPolicy.mode", "open")
		},
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			tr, _ := capturingTraitTransformer()
			tr.RegisterDocumentLowering(traitRenderingDocRule{kind: "Editing", fill: func(trait *Trait, doc *Application, lctx LoweringContext) error {
				if err := renderTraitNetpol(trait, doc, lctx); err != nil {
					return err
				}
				return edit(trait)
			}})
			_, err := tr.Transform(emptyDoc("Editing"), netpolCapability())
			expectReservedTraitRefused(t, err)
		})
	}
}

// TestLower_RenderedReservedTraitSurvivesIntoATraitRule: a rendered trait of a type a
// schema-declaring TraitLoweringRule claims reaches that rule in the next round, past
// the rule's own pre-rule check, with the value intact.
func TestLower_RenderedReservedTraitSurvivesIntoATraitRule(t *testing.T) {
	next := &reservingRule{}
	tr := reservedSinkTransformer()
	tr.RegisterTraitLowering(next)
	tr.RegisterDocumentLowering(traitRenderingDocRule{kind: "Rendering", traitType: next.TraitType(), fill: renderTraitNetpol})

	if _, err := tr.lower(emptyDoc("Rendering"), netpolCapability()); err != nil {
		t.Fatalf("a rendered reserved trait value must survive into the next round's rule, got: %v", err)
	}
	if next.seen["networkPolicy"] == nil {
		t.Fatalf("expected the rendered value to reach the next rule, got: %#v", next.seen)
	}
}

// TestTransform_RenderedReservedTraitSurvivesALaterRule: a later rule that forwards
// the trait — a document rule forwarding, copying or rebuilding the component around
// it, or a component rule forwarding the component's traits — keeps the trait's
// record through that round and into applyTraits; a document rule that rebuilds the
// trait itself emits a new trait with no record, and the value is refused.
func TestTransform_RenderedReservedTraitSurvivesALaterRule(t *testing.T) {
	tests := []struct {
		name     string
		compType string
		register func(tr *Transformer)
		accept   bool
	}{
		{"document rule forwarding the component", "", func(tr *Transformer) {
			tr.RegisterDocumentLowering(forwardingComponentsDocRule{kind: "Later"})
		}, true},
		{"document rule copying the component by value", "", func(tr *Transformer) {
			tr.RegisterDocumentLowering(copyingComponentsDocRule{kind: "Later"})
		}, true},
		{"document rule rebuilding the component around its traits", "", func(tr *Transformer) {
			tr.RegisterDocumentLowering(forwardingDocRule{kind: "Later"})
		}, true},
		{"component rule forwarding the traits", "forwards", func(tr *Transformer) {
			tr.RegisterComponentLowering(forwardingComponentRule{fromType: "forwards", toType: "reserved-sink"})
		}, true},
		{"document rule rebuilding the trait", "", func(tr *Transformer) {
			tr.RegisterDocumentLowering(rebuildingTraitsDocRule{kind: "Later"})
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := "Later"
			if tc.compType != "" {
				out = ""
			}
			tr, h := capturingTraitTransformer()
			tr.RegisterDocumentLowering(traitRenderingDocRule{kind: "Rendering", out: out, compType: tc.compType, fill: renderTraitNetpol})
			tc.register(tr)

			_, err := tr.Transform(emptyDoc("Rendering"), netpolCapability())
			if !tc.accept {
				expectReservedTraitRefused(t, err)
				return
			}
			if err != nil {
				t.Fatalf("a rendered reserved trait value must survive the later rule, got: %v", err)
			}
			expectRenderedTraitNetpol(t, h)
		})
	}
}

// TestTransform_CapabilityMergeDoesNotExemptAnAuthoredTraitValue: applyTraits checks
// an unsealed trait before it merges the capability rendering in, against the trait's
// own record. An authored reserved value equal to what the capability renders for
// the same key is still authored, and refused.
func TestTransform_CapabilityMergeDoesNotExemptAnAuthoredTraitValue(t *testing.T) {
	rendering := map[string]any{"networkPolicy": map[string]any{"mode": "platform"}}
	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "reserved-trait", Properties: map[string]any{
		"networkPolicy": map[string]any{"mode": "platform"},
	}}}
	tr, _ := capturingTraitTransformer()

	_, err := tr.Transform(app, TransformContext{Capabilities: map[string]CapabilityBinding{
		"reserved-trait": {Rendering: rendering},
	}})
	expectReservedTraitRefused(t, err)
}

// TestTraitRenderReserved_WritesRecordsAndCopiesOnWrite: Trait.RenderReserved shares
// Component.RenderReserved's core — it writes, records, refuses as that does, and
// recording on one copy never reaches another.
func TestTraitRenderReserved_WritesRecordsAndCopiesOnWrite(t *testing.T) {
	schema := reservedTraitHandler{}.PropertySchema()
	var authored Trait
	if err := authored.RenderReserved("", "x"); err == nil || authored.Properties != nil || authored.rendered != nil {
		t.Fatalf("expected an empty path to be refused with the trait unchanged, got %v / %#v / %#v", err, authored.Properties, authored.rendered)
	}
	authored.Properties = map[string]any{"image": "envoy"}

	rendered := authored
	if err := rendered.RenderReserved("networkPolicy", map[string]any{"mode": "platform"}); err != nil {
		t.Fatalf("RenderReserved: %v", err)
	}
	if want := (renderedValues{"networkPolicy": map[string]any{"mode": "platform"}}); !reflect.DeepEqual(rendered.rendered, want) {
		t.Fatalf("expected the record %#v, got %#v", want, rendered.rendered)
	}
	if authored.rendered != nil {
		t.Fatalf("recording on a copy recorded on the original: %#v", authored.rendered)
	}
	if err := enforcePlatformReserved(schema, authored.Properties, authored.rendered, "properties"); !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected the original, which shares the written map but recorded nothing, to be refused, got: %v", err)
	}
	if err := enforcePlatformReserved(schema, rendered.Properties, rendered.rendered, "properties"); err != nil {
		t.Fatalf("expected the copy that recorded the value to be accepted, got: %v", err)
	}
}
