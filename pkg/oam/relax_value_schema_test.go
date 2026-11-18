package oam

import (
	"strings"
	"testing"
)

// A capability rendering merges into an open key's object value as it merges into a
// declared object (mergeRenderedProperties recurses wherever both sides are objects),
// so the authored check of a bound trait relaxes Required inside the value schema
// too (go-kure/launcher#1615).

func groupsTraitSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"groups": {
			Type: PropertyTypeObject,
			AdditionalPropertiesSchema: &PropertySchema{
				Type: PropertyTypeObject,
				Properties: map[string]PropertySchema{
					"image": {Type: PropertyTypeString, Required: true},
					"tag":   {Type: PropertyTypeString},
				},
			},
		},
	}
}

func groupsTransformer(schema map[string]PropertySchema) *Transformer {
	return NewTransformer(
		map[string]ComponentHandler{"webservice": schemaComponent{typ: "webservice"}},
		map[string]TraitHandler{"issuer": sharedSchemaTrait{typ: "issuer", schema: schema}},
	)
}

func groupsApp() *Application {
	return authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "issuer", Properties: map[string]any{"groups": map[string]any{"web": map[string]any{"tag": "1"}}}})
}

func TestValidateAuthoredPropertiesWithCapabilities_RenderingSuppliesAKeyInAnOpenValue(t *testing.T) {
	tr := groupsTransformer(groupsTraitSchema())
	caps := issuerBinding("issuer", map[string]any{"groups": map[string]any{"web": map[string]any{"image": "nginx"}}})
	if _, err := tr.ValidateAuthoredPropertiesWithCapabilities(groupsApp(), caps); err != nil {
		t.Fatalf("a required key the rendering supplies below an open key must not be refused, got: %v", err)
	}
}

func TestValidateAuthoredPropertiesWithCapabilities_OpenValueKeyNeitherSideSets(t *testing.T) {
	tr := groupsTransformer(groupsTraitSchema())
	caps := issuerBinding("issuer", map[string]any{"groups": map[string]any{"web": map[string]any{"tag": "2"}}})
	_, err := tr.ValidateAuthoredPropertiesWithCapabilities(groupsApp(), caps)
	want := `properties.groups.web: "image" is required; neither the trait nor capability "issuer"'s rendering sets it`
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("error = %v, want it to end with %q", err, want)
	}
}

// The copy in relaxObjectRequired reaches the value schema: the handler's own schema
// keeps its Required flag after a bound trait is validated.
func TestValidateAuthoredPropertiesWithCapabilities_DoesNotMutateTheValueSchema(t *testing.T) {
	shared := groupsTraitSchema()
	tr := groupsTransformer(shared)
	caps := issuerBinding("issuer", map[string]any{"groups": map[string]any{"web": map[string]any{"image": "nginx"}}})
	if _, err := tr.ValidateAuthoredPropertiesWithCapabilities(groupsApp(), caps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shared["groups"].AdditionalPropertiesSchema.Properties["image"].Required {
		t.Error("relaxObjectRequired cleared Required in the handler's own value schema")
	}
}
