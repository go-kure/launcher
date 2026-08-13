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
