package oam

import (
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// schemaComponent is a stub ComponentHandler that declares a property schema.
type schemaComponent struct{ typ string }

func (h schemaComponent) CanHandle(t string) bool { return t == h.typ }
func (h schemaComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (h schemaComponent) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"image": {Type: PropertyTypeString, Required: true}}
}

// plainComponent is a stub ComponentHandler with no schema.
type plainComponent struct{ typ string }

func (h plainComponent) CanHandle(t string) bool { return t == h.typ }
func (h plainComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}

// schemaTrait is a stub TraitHandler that declares a property schema.
type schemaTrait struct{ typ string }

func (h schemaTrait) CanHandle(t string) bool                               { return t == h.typ }
func (h schemaTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }
func (h schemaTrait) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"size":        {Type: PropertyTypeString, Required: true},
		"accessModes": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeString}},
	}
}

func TestHandlerSchemas(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": schemaComponent{typ: "webservice"},
			"plain":      plainComponent{typ: "plain"},
		},
		map[string]TraitHandler{
			"pvc": schemaTrait{typ: "pvc"},
		},
	)

	set := tr.HandlerSchemas()

	// Providers are included, split by kind; non-providers are omitted.
	if _, ok := set.Components["webservice"]; !ok {
		t.Errorf("expected component 'webservice' schema, got %v", set.Components)
	}
	if _, ok := set.Components["plain"]; ok {
		t.Error("plain component has no schema and must be omitted")
	}
	if _, ok := set.Traits["pvc"]; !ok {
		t.Errorf("expected trait 'pvc' schema, got %v", set.Traits)
	}

	// Component and trait maps are distinct (no cross-registry collision).
	if _, ok := set.Traits["webservice"]; ok {
		t.Error("component schema leaked into Traits map")
	}

	// Schema content round-trips.
	if got := set.Components["webservice"]["image"]; got.Type != PropertyTypeString || !got.Required {
		t.Errorf("webservice.image schema = %+v", got)
	}
	if got := set.Traits["pvc"]["accessModes"]; got.Type != PropertyTypeArray || got.Items == nil || got.Items.Type != PropertyTypeString {
		t.Errorf("pvc.accessModes schema = %+v", got)
	}
}

// schemaLoweringRule is a stub TraitLoweringRule (D5, trait position) that also
// declares a property schema, modeling ExposeRule: a trait type reachable only
// through RegisterTraitLowering, never RegisterTrait/RegisterBuiltinTrait.
type schemaLoweringRule struct{ typ string }

func (r schemaLoweringRule) TraitType() string { return r.typ }
func (r schemaLoweringRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r schemaLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"controllerType": {Type: PropertyTypeString, Description: "dispatch target"},
	}
}

// TestHandlerSchemas_IncludesTraitLoweringRules is the regression guard for the C6
// review Important #2 finding: HandlerSchemas() originally iterated only
// t.traitHandlers, so a trait claimed exclusively by a TraitLoweringRule (like
// ExposeRule claims "expose") silently disappeared from the published schema set
// the moment it stopped being a dispatchable TraitHandler. A rule-claimed trait must
// appear in set.Traits exactly like a handler-claimed one does.
func TestHandlerSchemas_IncludesTraitLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(schemaLoweringRule{typ: "expose"})

	set := tr.HandlerSchemas()

	schema, ok := set.Traits["expose"]
	if !ok {
		t.Fatalf("expected trait-lowering-rule 'expose' schema in set.Traits, got %v", set.Traits)
	}
	if got := schema["controllerType"]; got.Type != PropertyTypeString || got.Description == "" {
		t.Errorf("expose.controllerType schema = %+v", got)
	}
}

// schemaComponentLoweringRule is a stub ComponentLoweringRule (D5, component position)
// that also declares a property schema — the component-position counterpart of
// schemaLoweringRule above.
type schemaComponentLoweringRule struct{ typ string }

func (r schemaComponentLoweringRule) ComponentType() string { return r.typ }
func (r schemaComponentLoweringRule) LowerComponent(*Component, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r schemaComponentLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"replicas": {Type: PropertyTypeInteger, Description: "replica count"},
	}
}

// TestHandlerSchemas_IncludesComponentLoweringRules is the component-position mirror
// of TestHandlerSchemas_IncludesTraitLoweringRules above, for a second-round Codex
// review finding: HandlerSchemas() folded t.traitLoweringRules implementing
// PropertySchemaProvider into set.Traits but had no matching loop for
// t.componentLoweringRules into set.Components — the identical C6-class bug, just
// unaddressed on the component side. A downstream validator could not check a
// higher-level component's user-facing properties before its lowering rule ran.
func TestHandlerSchemas_IncludesComponentLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(schemaComponentLoweringRule{typ: "widget"})

	set := tr.HandlerSchemas()

	schema, ok := set.Components["widget"]
	if !ok {
		t.Fatalf("expected component-lowering-rule 'widget' schema in set.Components, got %v", set.Components)
	}
	if got := schema["replicas"]; got.Type != PropertyTypeInteger || got.Description == "" {
		t.Errorf("widget.replicas schema = %+v", got)
	}
}

// --- ContractDescriber / HandlerContracts (R9): mirrors the PropertySchemaProvider /
// HandlerSchemas tests above, including their two lowering-rule-registry regression
// guards — HandlerContracts must cover the identical four registries HandlerSchemas
// does (transform.go), for the identical reason.

// contractComponent is a stub ComponentHandler that declares contract metadata.
type contractComponent struct{ typ string }

func (h contractComponent) CanHandle(t string) bool { return t == h.typ }
func (h contractComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}
func (h contractComponent) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: "webservice", Version: "v1"}
}

// contractTrait is a stub TraitHandler that declares contract metadata.
type contractTrait struct{ typ string }

func (h contractTrait) CanHandle(t string) bool                               { return t == h.typ }
func (h contractTrait) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }
func (h contractTrait) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: "pvc", Version: "v2", RequiredCapabilityKeys: []string{"storage"}}
}

func TestHandlerContracts(t *testing.T) {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": contractComponent{typ: "webservice"},
			"plain":      plainComponent{typ: "plain"}, // no ContractMetadata method
		},
		map[string]TraitHandler{
			"pvc": contractTrait{typ: "pvc"},
		},
	)

	set := tr.HandlerContracts()

	// Providers are included, split by kind; non-providers are omitted.
	got, ok := set.Components["webservice"]
	if !ok {
		t.Fatalf("expected component 'webservice' contract metadata, got %v", set.Components)
	}
	if got.Family != "webservice" || got.Version != "v1" {
		t.Errorf("webservice contract metadata = %+v", got)
	}
	if _, ok := set.Components["plain"]; ok {
		t.Error("plain component has no ContractMetadata method and must be omitted")
	}

	traitGot, ok := set.Traits["pvc"]
	if !ok {
		t.Fatalf("expected trait 'pvc' contract metadata, got %v", set.Traits)
	}
	if traitGot.Family != "pvc" || traitGot.Version != "v2" || len(traitGot.RequiredCapabilityKeys) != 1 || traitGot.RequiredCapabilityKeys[0] != "storage" {
		t.Errorf("pvc contract metadata = %+v", traitGot)
	}

	// Component and trait maps are distinct (no cross-registry collision).
	if _, ok := set.Traits["webservice"]; ok {
		t.Error("component contract metadata leaked into Traits map")
	}
}

// contractTraitLoweringRule mirrors schemaLoweringRule above (D5, trait position),
// modeling a versioned, deprecated ExposeRule-like rule.
type contractTraitLoweringRule struct{ typ string }

func (r contractTraitLoweringRule) TraitType() string { return r.typ }
func (r contractTraitLoweringRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r contractTraitLoweringRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{
		Family:             "expose",
		Version:            "v1",
		Deprecated:         true,
		DeprecationMessage: "use httproute directly",
	}
}

// TestHandlerContracts_IncludesTraitLoweringRules is the ContractDescriber/
// HandlerContracts mirror of TestHandlerSchemas_IncludesTraitLoweringRules: a trait
// type claimed exclusively by a TraitLoweringRule (like ExposeRule claims "expose")
// must still appear in set.Traits, not just a dispatchable TraitHandler.
func TestHandlerContracts_IncludesTraitLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterTraitLowering(contractTraitLoweringRule{typ: "expose"})

	set := tr.HandlerContracts()

	got, ok := set.Traits["expose"]
	if !ok {
		t.Fatalf("expected trait-lowering-rule 'expose' contract metadata in set.Traits, got %v", set.Traits)
	}
	if !got.Deprecated || got.DeprecationMessage == "" {
		t.Errorf("expose contract metadata = %+v, want Deprecated=true with a non-empty message", got)
	}
}

// contractComponentLoweringRule is the component-position mirror of
// contractTraitLoweringRule above.
type contractComponentLoweringRule struct{ typ string }

func (r contractComponentLoweringRule) ComponentType() string { return r.typ }
func (r contractComponentLoweringRule) LowerComponent(*Component, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}
func (r contractComponentLoweringRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: "widget", Version: "v1", RequiredCapabilityKeys: []string{"widget-backend"}}
}

// TestHandlerContracts_IncludesComponentLoweringRules is the component-position
// mirror of TestHandlerSchemas_IncludesComponentLoweringRules above: a component type
// claimed exclusively by a ComponentLoweringRule must still appear in set.Components.
func TestHandlerContracts_IncludesComponentLoweringRules(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(contractComponentLoweringRule{typ: "widget"})

	set := tr.HandlerContracts()

	got, ok := set.Components["widget"]
	if !ok {
		t.Fatalf("expected component-lowering-rule 'widget' contract metadata in set.Components, got %v", set.Components)
	}
	if len(got.RequiredCapabilityKeys) != 1 || got.RequiredCapabilityKeys[0] != "widget-backend" {
		t.Errorf("widget contract metadata = %+v, want RequiredCapabilityKeys=[widget-backend]", got)
	}
}
