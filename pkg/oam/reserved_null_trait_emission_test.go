package oam

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- go-kure/launcher#626: an authored reserved null into an emitted trait --------
//
// go-kure/launcher#609's trait-surface twin. Validating an emitted trait, or an
// emitted component whose properties map a trait shares, normalizes an explicit null
// to absence, so the traits a rule emits and does not synthesize are checked for
// reserved keys before any of its output is validated.

// expectReservedNullRefusedIn is expectPlatformReserved plus the site the refusal must
// name.
func expectReservedNullRefusedIn(t *testing.T, err error, site string) {
	t.Helper()
	expectPlatformReserved(t, err)
	if msg := err.Error(); !strings.Contains(msg, site) {
		t.Errorf("expected the refusal to contain %q, got: %v", site, msg)
	}
}

// reservedTraitFillingDocRule rebuilds the document's first component as a reserved-sink and
// copies that component's properties into a reserved-trait it attaches to it.
type reservedTraitFillingDocRule struct{}

func (reservedTraitFillingDocRule) Kind() string { return "TraitFilling" }

func (reservedTraitFillingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "reserved-sink",
			Properties: map[string]any{"image": "nginx"},
			Traits:     []Trait{{Type: "reserved-trait", Properties: doc.Spec.Components[0].Properties}},
		}}},
	}}}, nil
}

// sharedMapTraitsRule declares no schema and emits an open-trait and then a
// reserved-trait that share the authored trait's properties map.
type sharedMapTraitsRule struct{}

func (sharedMapTraitsRule) TraitType() string { return "shared-map-traits" }

func (sharedMapTraitsRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{
		{Type: "open-trait", Properties: trait.Properties},
		{Type: "reserved-trait", Properties: trait.Properties},
	}}, nil
}

// sharedMapNestedTraitRule declares no schema and emits an open-sink carrying a
// reserved-trait, both on the authored properties map. The component is validated
// before the trait nested in it.
type sharedMapNestedTraitRule struct{}

func (sharedMapNestedTraitRule) ComponentType() string { return "shared-map-nested" }

func (sharedMapNestedTraitRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "web",
		Type:       "open-sink",
		Properties: comp.Properties,
		Traits:     []Trait{{Type: "reserved-trait", Properties: comp.Properties}},
	}}}, nil
}

// siblingMapTraitRule declares no schema and emits an open-trait whose properties map
// is the one of the first trait attached to the same component.
type siblingMapTraitRule struct{}

func (siblingMapTraitRule) TraitType() string { return "sibling-map" }

func (siblingMapTraitRule) LowerTrait(_ *Trait, lctx LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "open-trait", Properties: lctx.Component.Traits[0].Properties}}}, nil
}

// checkedTraitWrapperRule declares a schema with nothing reserved and copies the
// trait it lowers into a reserved-trait. Its input is checked, so its output is
// synthesized and the reserved key it writes is the rule's own.
type checkedTraitWrapperRule struct{}

func (checkedTraitWrapperRule) TraitType() string { return "checked-trait-wrapper" }

func (checkedTraitWrapperRule) PropertySchema() map[string]PropertySchema {
	return checkedPassThroughComponentRule{}.PropertySchema()
}

func (checkedTraitWrapperRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "reserved-trait", Properties: trait.Properties}}}, nil
}

// TestTransform_SchemaLessComponentRuleReservedNullIntoNestedTraitIsRejected: a
// schema-less component rule copies an authored reserved null into a reserved-trait
// it nests in the component it emits.
func TestTransform_SchemaLessComponentRuleReservedNullIntoNestedTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(traitEmittingComponentRule{})

	_, err := tr.Transform(singleComponentApp("Application", "trait-emitting", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" of component "web" emitted by rule component/trait-emitting:`)
}

// TestTransform_SchemaLessTraitRuleReservedNullIntoTraitIsRejected: a schema-less
// trait rule copies an authored trait's reserved null into the trait it emits.
func TestTransform_SchemaLessTraitRuleReservedNullIntoTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "trait-wrapper", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// TestTransform_DocumentRuleReservedNullIntoTraitIsRejected: a document rule copies
// an authored component's reserved null into a trait it builds.
func TestTransform_DocumentRuleReservedNullIntoTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})
	tr.RegisterDocumentLowering(reservedTraitFillingDocRule{})

	_, err := tr.Transform(singleComponentApp("TraitFilling", "pass-through", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" of component "web" emitted by rule document/TraitFilling:`)
}

// TestTransform_ReservedNullInMapSharedAcrossEmittedTraitsIsRejected: validating the
// open-trait strips the null from the map it shares with the reserved-trait, so every
// trait of the result is checked before any is validated.
func TestTransform_ReservedNullInMapSharedAcrossEmittedTraitsIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTrait("open-trait", openTraitHandler{})
	tr.RegisterTraitLowering(sharedMapTraitsRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "shared-map-traits", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/shared-map-traits:`)
}

// TestTransform_ReservedNullInMapSharedWithEmittedComponentIsRejected: validating the
// open-sink strips the null from the map the reserved-trait nested in it shares.
func TestTransform_ReservedNullInMapSharedWithEmittedComponentIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterComponentLowering(sharedMapNestedTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "shared-map-nested", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" of component "web" emitted by rule component/shared-map-nested:`)
}

// TestTransform_ReservedNullInMapSharedWithAuthoredTraitIsRejected: validating the
// open-trait would strip the null from the authored reserved-trait it shares a map
// with, so the authored traits are checked before any rule of the round runs.
func TestTransform_ReservedNullInMapSharedWithAuthoredTraitIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTrait("open-trait", openTraitHandler{})
	tr.RegisterTraitLowering(siblingMapTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{
		{Type: "reserved-trait", Properties: authoredReservedNull()},
		{Type: "sibling-map", Properties: map[string]any{}},
	}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" on component "web" in document "myapp" (kind "Application"): properties:`)
}

// rawSharedMapRule writes one open-sink carrying a reserved-trait, both on the given
// properties map. LowerRaws validates the component and leaves the trait to
// Transform.
type rawSharedMapRule struct{ rawReservedSinkRule }

func (r rawSharedMapRule) LowerDocument(doc any, _ LoweringContext) (LoweringResult, error) {
	src := doc.(*testRawDoc)
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: src.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "open-sink",
			Properties: r.props,
			Traits:     []Trait{{Type: "reserved-trait", Properties: r.props}},
		}}},
	}}}, nil
}

// TestLowerRaws_RawRuleReservedNullInMapSharedWithComponentReachesTransform: a raw
// rule's trait is checked for reserved keys by Transform, so LowerRaws must hand it
// over as written. Validating the open-sink strips the null from its properties map,
// and that must not reach the trait sharing it.
func TestLowerRaws_RawRuleReservedNullInMapSharedWithComponentReachesTransform(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterRawDocumentLowering(rawSharedMapRule{rawReservedSinkRule{props: authoredReservedNull()}})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}
	// The parser knows the built-in component types and the lowerable ones; open-sink
	// is a test handler, so it is passed the way a consumer passes its own.
	lowerable := tr.LowerableTypes()
	lowerable.ComponentTypes = append(lowerable.ComponentTypes, "open-sink")
	app, err := ParseWithExtraTypes(out[0], []string{"reserved-trait"}, lowerable)
	if err != nil {
		t.Fatalf("LowerRaws output does not parse: %v\n%s", err, out[0])
	}
	_, err = tr.Transform(app, TransformContext{Namespace: "default"})
	expectReservedNullRefusedIn(t, err, `component "web" trait "reserved-trait"`)
}

// --- A null shared with an element whose type does not reserve the key ----------
//
// Each rule below emits, or forwards, an open element and a wrapper that share one
// properties map, or a collection in it. Neither reserves the key, so the rule's
// output passes its check; but validating the open element normalizes its
// properties, and unless it normalizes its own copy, the null would be gone before
// the wrapper lowers into a reserved-trait a round later.

// sharedMapChainTraitRule declares no schema and emits an open-trait and a
// trait-wrapper on the authored trait's properties map.
type sharedMapChainTraitRule struct{}

func (sharedMapChainTraitRule) TraitType() string { return "shared-map-chain" }

func (sharedMapChainTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{
		{Type: "open-trait", Properties: trait.Properties},
		{Type: "trait-wrapper", Properties: trait.Properties},
	}}, nil
}

// sharedMapChainComponentRule declares no schema and emits an open-sink carrying a
// trait-wrapper, both on the authored component's properties map.
type sharedMapChainComponentRule struct{}

func (sharedMapChainComponentRule) ComponentType() string { return "shared-map-chain" }

func (sharedMapChainComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "web",
		Type:       "open-sink",
		Properties: comp.Properties,
		Traits:     []Trait{{Type: "trait-wrapper", Properties: comp.Properties}},
	}}}, nil
}

// sharedMapChainDocRule is sharedMapChainComponentRule at document position, on the
// document's first component's properties map.
type sharedMapChainDocRule struct{}

func (sharedMapChainDocRule) Kind() string { return "SharedMapChain" }

func (sharedMapChainDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	props := doc.Spec.Components[0].Properties
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "open-sink",
			Properties: props,
			Traits:     []Trait{{Type: "trait-wrapper", Properties: props}},
		}}},
	}}}, nil
}

// TestTransform_ReservedNullSharedWithTraitWrapperByTraitRuleIsRejected: validating
// the open-trait must not strip the null from the trait-wrapper.
func TestTransform_ReservedNullSharedWithTraitWrapperByTraitRuleIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTrait("open-trait", openTraitHandler{})
	tr.RegisterTraitLowering(sharedMapChainTraitRule{})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "shared-map-chain", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// TestTransform_ReservedNullSharedWithTraitWrapperByComponentRuleIsRejected:
// validating the open-sink must not strip the null from the trait-wrapper nested in
// it.
func TestTransform_ReservedNullSharedWithTraitWrapperByComponentRuleIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterComponentLowering(sharedMapChainComponentRule{})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	_, err := tr.Transform(singleComponentApp("Application", "shared-map-chain", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// TestTransform_ReservedNullSharedWithTraitWrapperByDocumentRuleIsRejected is the
// same at document position.
func TestTransform_ReservedNullSharedWithTraitWrapperByDocumentRuleIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterComponentLowering(passThroughComponentRule{})
	tr.RegisterDocumentLowering(sharedMapChainDocRule{})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	_, err := tr.Transform(singleComponentApp("SharedMapChain", "pass-through", authoredReservedNull()), TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// checkedTraitSharingComponentRule declares a schema with nothing reserved, emits an
// open-sink on its first trait's properties map and forwards its traits.
type checkedTraitSharingComponentRule struct{}

func (checkedTraitSharingComponentRule) ComponentType() string { return "checked-trait-sharing" }

func (checkedTraitSharingComponentRule) PropertySchema() map[string]PropertySchema {
	return checkedPassThroughComponentRule{}.PropertySchema()
}

func (checkedTraitSharingComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{
		Name:       "web",
		Type:       "open-sink",
		Properties: comp.Traits[0].Properties,
		Traits:     comp.Traits,
	}}}, nil
}

// TestTransform_ReservedNullSharedWithForwardedTraitByCheckedRuleIsRejected: a rule
// whose input was checked emits an open-sink on the map of a trait-wrapper it
// forwards. Validating the open-sink must not strip the trait-wrapper's null.
func TestTransform_ReservedNullSharedWithForwardedTraitByCheckedRuleIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterComponentLowering(checkedTraitSharingComponentRule{})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	app := singleComponentApp("Application", "checked-trait-sharing", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "trait-wrapper", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// itemsTraitHandler declares an array of objects whose networkPolicy it does not
// reserve.
type itemsTraitHandler struct{ openTraitHandler }

func (itemsTraitHandler) CanHandle(t string) bool { return t == "items-trait" }

func (itemsTraitHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"items": {Type: PropertyTypeArray, Items: &PropertySchema{
		Type:       PropertyTypeObject,
		Properties: checkedPassThroughComponentRule{}.PropertySchema(),
	}}}
}

// itemsSharingTraitRule declares no schema and emits an items-trait and an
// items-wrapper sharing one typed collection that holds the authored properties map.
type itemsSharingTraitRule struct{}

func (itemsSharingTraitRule) TraitType() string { return "items-sharing" }

func (itemsSharingTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	items := []map[string]any{trait.Properties}
	return LoweringResult{Traits: []Trait{
		{Type: "items-trait", Properties: map[string]any{"items": items}},
		{Type: "items-wrapper", Properties: map[string]any{"items": items}},
	}}, nil
}

// itemsWrapperTraitRule declares no schema and lowers its first item into a
// reserved-trait.
type itemsWrapperTraitRule struct{}

func (itemsWrapperTraitRule) TraitType() string { return "items-wrapper" }

func (itemsWrapperTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "reserved-trait", Properties: trait.Properties["items"].([]map[string]any)[0]}}}, nil
}

// TestTransform_ReservedNullInSharedTypedCollectionIsRejected: validating the
// items-trait must not strip the null from a map nested in a typed collection the
// items-wrapper shares.
func TestTransform_ReservedNullInSharedTypedCollectionIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTrait("items-trait", itemsTraitHandler{})
	tr.RegisterTraitLowering(itemsSharingTraitRule{})
	tr.RegisterTraitLowering(itemsWrapperTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "items-sharing", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/items-wrapper:`)
}

// relabelingDocRule forwards the document's components unchanged under the terminal
// kind.
type relabelingDocRule struct{}

func (relabelingDocRule) Kind() string { return "Relabeling" }

func (relabelingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	out := *doc
	out.Kind = terminalDocumentKind
	return LoweringResult{Documents: []Application{out}}, nil
}

// TestTransform_ReservedNullSharedWithForwardedComponentIsRejected: validating an
// open-sink a document rule forwarded must not strip the null from the trait-wrapper
// sharing its map.
func TestTransform_ReservedNullSharedWithForwardedComponentIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterDocumentLowering(relabelingDocRule{})
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	props := authoredReservedNull()
	app := singleComponentApp("Relabeling", "open-sink", props)
	app.Spec.Components[0].Traits = []Trait{{Type: "trait-wrapper", Properties: props}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" emitted by rule trait/trait-wrapper:`)
}

// cyclicTraitRule declares no schema and emits an open-trait whose image is a map
// holding itself.
type cyclicTraitRule struct{}

func (cyclicTraitRule) TraitType() string { return "cyclic" }

func (cyclicTraitRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	m := map[string]any{}
	m["self"] = m
	return LoweringResult{Traits: []Trait{{Type: "open-trait", Properties: map[string]any{"image": m}}}}, nil
}

// TestTransform_CyclicEmittedPropertiesAreRefusedNotCopiedForever: the copy emission
// validation checks keeps a cycle a cycle, so the open-trait's image is refused as
// not a string, exactly as it was before the copy existed.
func TestTransform_CyclicEmittedPropertiesAreRefusedNotCopiedForever(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTrait("open-trait", openTraitHandler{})
	tr.RegisterTraitLowering(cyclicTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "cyclic", Properties: map[string]any{}}}
	_, err := tr.Transform(app, TransformContext{})
	if err == nil || !strings.Contains(err.Error(), `emitted trait "open-trait": properties.image`) {
		t.Fatalf("expected the cyclic image to be refused, got: %v", err)
	}
}

// TestCopyPropertyMap_KeepsShape: a map or slice reached twice is copied once, so
// cycles and shared values inside one properties map keep their shape, on fresh
// containers.
func TestCopyPropertyMap_KeepsShape(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	list := []any{nil}
	list[0] = list
	m["list"] = list
	m["a"], m["b"] = list, list

	c := copyPropertyMap(m)
	if sameMap(c, m) {
		t.Fatal("expected a new map")
	}
	if self, ok := c["self"].(map[string]any); !ok || !sameMap(self, c) {
		t.Errorf("expected the copy's self entry to be the copy, got %T", c["self"])
	}
	copied := c["list"].([]any)
	if &copied[0] == &list[0] {
		t.Error("expected a new slice")
	}
	if inner := copied[0].([]any); &inner[0] != &copied[0] {
		t.Error("expected the copied slice to hold itself")
	}
	if a, b := c["a"].([]any), c["b"].([]any); &a[0] != &copied[0] || &b[0] != &copied[0] {
		t.Error("expected a slice reached three times to be copied once")
	}
}

// rawSharedMapComponentsRule writes an open-sink and a trait-emitting component on
// the given properties map. LowerRaws validates both, the open-sink first.
type rawSharedMapComponentsRule struct{ rawReservedSinkRule }

func (r rawSharedMapComponentsRule) LowerDocument(doc any, _ LoweringContext) (LoweringResult, error) {
	src := doc.(*testRawDoc)
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: src.Metadata.Name + "-lowered"},
		Spec: ApplicationSpec{Components: []Component{
			{Name: "open", Type: "open-sink", Properties: r.props},
			{Name: "web", Type: "trait-emitting", Properties: r.props},
		}},
	}}}, nil
}

// TestLowerRaws_RawRuleReservedNullInMapSharedAcrossComponentsReachesTransform: the
// trait-emitting component must reach Transform with its null, where its rule copies
// it into a reserved-trait.
func TestLowerRaws_RawRuleReservedNullInMapSharedAcrossComponentsReachesTransform(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponent("open-sink", openSinkHandler{})
	tr.RegisterComponentLowering(traitEmittingComponentRule{})
	tr.RegisterRawDocumentLowering(rawSharedMapComponentsRule{rawReservedSinkRule{props: authoredReservedNull()}})

	out, err := tr.LowerRaws([]json.RawMessage{rawWebApplication("shop")}, TransformContext{})
	if err != nil {
		t.Fatalf("LowerRaws: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output document, got %d", len(out))
	}
	lowerable := tr.LowerableTypes()
	lowerable.ComponentTypes = append(lowerable.ComponentTypes, "open-sink")
	app, err := ParseWithExtraTypes(out[0], []string{"reserved-trait"}, lowerable)
	if err != nil {
		t.Fatalf("LowerRaws output does not parse: %v\n%s", err, out[0])
	}
	_, err = tr.Transform(app, TransformContext{Namespace: "default"})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" of component "web" emitted by rule component/trait-emitting:`)
}

// TestTransform_AuthoredReservedTraitConsumedByComponentRuleIsRejected: the check at
// the start of the round also refuses an authored reserved key on a trait the
// component rule then consumes rather than forwards, which no later check would see.
// It is an authored reserved value all the same.
func TestTransform_AuthoredReservedTraitConsumedByComponentRuleIsRejected(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(passThroughComponentRule{})

	app := singleComponentApp("Application", "pass-through", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "reserved-trait", Properties: authoredReservedNull()}}
	_, err := tr.Transform(app, TransformContext{})
	expectReservedNullRefusedIn(t, err, `trait "reserved-trait" on component "web" in document "myapp" (kind "Application"): properties:`)
}

// TestTransform_SynthesizedTraitReservedNullIsAccepted pins the exemption: a trait
// rule that declares a schema had its input checked, so the reserved null it writes
// into its emitted trait is its own output.
func TestTransform_SynthesizedTraitReservedNullIsAccepted(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(checkedTraitWrapperRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "checked-trait-wrapper", Properties: authoredReservedNull()}}
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("a synthesized trait's reserved null must be accepted, got: %v", err)
	}
}

// TestTransform_SchemaLessTraitRuleWithoutReservedKeyIsAccepted: the check refuses
// the reserved key only, not every trait a schema-less rule passes through.
func TestTransform_SchemaLessTraitRuleWithoutReservedKeyIsAccepted(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterTraitLowering(traitEmittingTraitRule{})

	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: "trait-wrapper", Properties: map[string]any{"image": "nginx"}}}
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("an unreserved pass-through must be accepted, got: %v", err)
	}
}

// traitForwardingComponentRule declares no schema and forwards the traits of the
// component it lowers onto a reserved-sink it emits.
type traitForwardingComponentRule struct{}

func (traitForwardingComponentRule) ComponentType() string { return "trait-forwarding" }

func (traitForwardingComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: "web", Type: "reserved-sink", Properties: map[string]any{"image": "nginx"}, Traits: comp.Traits}}}, nil
}

// traitForwardingDocRule builds a new reserved-sink and forwards onto it the traits
// of the document's first component.
type traitForwardingDocRule struct{}

func (traitForwardingDocRule) Kind() string { return "TraitForwarding" }

func (traitForwardingDocRule) LowerDocument(doc *Application, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Documents: []Application{{
		APIVersion: SupportedAPIVersion,
		Kind:       terminalDocumentKind,
		Metadata:   Metadata{Name: doc.Metadata.Name, Namespace: doc.Metadata.Namespace},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "reserved-sink",
			Properties: map[string]any{"image": "nginx"},
			Traits:     doc.Spec.Components[0].Traits,
		}}},
	}}}, nil
}

// synthesizedReservedTrait is a reserved-trait as a checked rule would have emitted
// it: sealed and synthesized, so the reserved key in it is that rule's own. No public
// input carries one into a rule that forwards it unchecked today; the in-package
// marker stands in for that.
func synthesizedReservedTrait() Trait {
	return Trait{Type: "reserved-trait", Properties: authoredReservedNull(), sealed: true, synthesized: true}
}

// TestTransform_ForwardedSynthesizedTraitIsExemptAtComponentRule: a schema-less
// component rule's own output is checked, but a synthesized trait it only forwarded
// keeps its exemption.
func TestTransform_ForwardedSynthesizedTraitIsExemptAtComponentRule(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterComponentLowering(traitForwardingComponentRule{})

	app := singleComponentApp("Application", "trait-forwarding", map[string]any{})
	app.Spec.Components[0].Traits = []Trait{synthesizedReservedTrait()}
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("a forwarded synthesized trait must stay exempt, got: %v", err)
	}
}

// TestTransform_ForwardedSynthesizedTraitIsExemptAtDocumentRule is the same at
// document position.
func TestTransform_ForwardedSynthesizedTraitIsExemptAtDocumentRule(t *testing.T) {
	tr := reservedTraitTransformer()
	tr.RegisterDocumentLowering(traitForwardingDocRule{})

	app := singleComponentApp("TraitForwarding", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{synthesizedReservedTrait()}
	if _, err := tr.Transform(app, TransformContext{}); err != nil {
		t.Fatalf("a forwarded synthesized trait must stay exempt, got: %v", err)
	}
}
