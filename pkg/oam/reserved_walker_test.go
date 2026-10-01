package oam

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- go-kure/launcher#635: reservations inside array items and authored nested nulls --
//
// enforcePlatformReserved walks every declared value that can hold a reservation —
// array items included — and ValidateAuthoredProperties checks reservations before
// it validates, because validation drops a nested explicit null that Transform's own
// check would otherwise have refused.

// nestedReservedTraitHandler declares a reservation only below the top level: inside
// a declared object and inside the items of a declared array of objects.
type nestedReservedTraitHandler struct{}

func (nestedReservedTraitHandler) CanHandle(t string) bool { return t == "nested-reserved" }

func (nestedReservedTraitHandler) Apply(*Trait, *stack.Application, *stack.Bundle) error { return nil }

func (nestedReservedTraitHandler) PropertySchema() map[string]PropertySchema {
	locked := map[string]PropertySchema{
		"name":   {Type: PropertyTypeString, Description: "Authored freely."},
		"locked": {Type: PropertyTypeString, PlatformReserved: true, Description: "Platform-supplied."},
	}
	return map[string]PropertySchema{
		"config": {Type: PropertyTypeObject, Properties: locked, Description: "Nested object."},
		"items": {
			Type:        PropertyTypeArray,
			Items:       &PropertySchema{Type: PropertyTypeObject, Properties: locked},
			Description: "Array of objects.",
		},
	}
}

// nestedReservedWrapperRule declares no schema and copies its authored properties
// into a nested-reserved trait, so what it emits is checked as authored.
type nestedReservedWrapperRule struct{}

func (nestedReservedWrapperRule) TraitType() string { return "nested-reserved-wrapper" }

func (nestedReservedWrapperRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "nested-reserved", Properties: trait.Properties}}}, nil
}

func nestedReservedTransformer() *Transformer {
	tr := reservedSinkTransformer()
	tr.RegisterTrait("nested-reserved", nestedReservedTraitHandler{})
	return tr
}

// expectReservedRefusal asserts err is a D3 refusal whose text contains site.
func expectReservedRefusal(t *testing.T, err error, site string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a platform-reserved property to be refused")
	}
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected the error to wrap ErrPlatformReserved, got: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, site) {
		t.Errorf("expected the refusal to contain %q, got: %v", site, msg)
	}
}

func appWithTrait(traitType string, props map[string]any) *Application {
	app := singleComponentApp("Application", "reserved-sink", map[string]any{"image": "nginx"})
	app.Spec.Components[0].Traits = []Trait{{Type: traitType, Properties: props}}
	return app
}

func TestEnforcePlatformReserved_WalksArrayItems(t *testing.T) {
	schema := nestedReservedTraitHandler{}.PropertySchema()
	deep := map[string]PropertySchema{
		"matrix": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeArray, Items: schema["items"].Items}},
	}
	tests := []struct {
		name     string
		schema   map[string]PropertySchema
		props    map[string]any
		wantPath string // "" means accepted
	}{
		{name: "unreserved item fields", schema: schema, props: map[string]any{"items": []any{map[string]any{"name": "a"}}}},
		{name: "not an array", schema: schema, props: map[string]any{"items": "nope"}},
		{name: "item that is not an object", schema: schema, props: map[string]any{"items": []any{"nope"}}},
		{
			name:     "reserved key in the second item",
			schema:   schema,
			props:    map[string]any{"items": []any{map[string]any{"name": "a"}, map[string]any{"locked": "x"}}},
			wantPath: "properties.items[1]",
		},
		{
			name:     "reserved key authored as null",
			schema:   schema,
			props:    map[string]any{"items": []any{map[string]any{"locked": nil}}},
			wantPath: "properties.items[0]",
		},
		{
			name:     "typed slice of typed maps",
			schema:   schema,
			props:    map[string]any{"items": []map[string]string{{"locked": "x"}}},
			wantPath: "properties.items[0]",
		},
		{
			name:     "array of arrays",
			schema:   deep,
			props:    map[string]any{"matrix": []any{[]any{}, []any{map[string]any{"locked": nil}}}},
			wantPath: "properties.matrix[1][0]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := enforcePlatformReserved(tc.schema, tc.props, "properties")
			if tc.wantPath == "" {
				if err != nil {
					t.Fatalf("expected acceptance, got: %v", err)
				}
				return
			}
			want := tc.wantPath + `: "locked" is platform-reserved`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected an error containing %q, got: %v", want, err)
			}
		})
	}
}

// TestTransform_ReservedNullInEmittedArrayItemIsRejected is the issue's first case: a
// schema-less trait rule copies {"items":[{"locked":null}]} into a trait whose schema
// reserves locked inside the items. Emission validation drops the null, so the walker
// has to see it first.
func TestTransform_ReservedNullInEmittedArrayItemIsRejected(t *testing.T) {
	tr := nestedReservedTransformer()
	tr.RegisterTraitLowering(nestedReservedWrapperRule{})

	app := appWithTrait("nested-reserved-wrapper", map[string]any{"items": []any{map[string]any{"locked": nil}}})
	_, err := tr.Transform(app, TransformContext{})
	expectReservedRefusal(t, err, `properties.items[0]: "locked" is platform-reserved`)
}

// TestValidateAuthoredProperties_RejectsNestedReservedNull is the issue's second case:
// validation would delete the nested null, after which Transform accepts the document.
func TestValidateAuthoredProperties_RejectsNestedReservedNull(t *testing.T) {
	tr := nestedReservedTransformer()

	app := appWithTrait("nested-reserved", map[string]any{"config": map[string]any{"locked": nil}})
	err := tr.ValidateAuthoredProperties(app)
	expectReservedRefusal(t, err, `properties.config: "locked" is platform-reserved`)
	if _, present := app.Spec.Components[0].Traits[0].Properties["config"].(map[string]any)["locked"]; !present {
		t.Error("the refused document must be left as authored, with its null still present")
	}
}

// TestValidateAuthoredProperties_KeepsTransformsExemptions: a synthesized component and
// a sealed trait a checked rule emitted carry the platform's own values; Transform
// accepts them, so the authored check must too.
func TestValidateAuthoredProperties_KeepsTransformsExemptions(t *testing.T) {
	newDoc := func() *Application {
		app := appWithTrait("nested-reserved", map[string]any{"config": map[string]any{"locked": "x"}})
		comp := &app.Spec.Components[0]
		comp.Properties["networkPolicy"] = map[string]any{"rendered": true}
		comp.synthesized = true
		comp.Traits[0].sealed = true
		comp.Traits[0].synthesized = true
		return app
	}
	tr := nestedReservedTransformer()
	if err := tr.ValidateAuthoredProperties(newDoc()); err != nil {
		t.Fatalf("expected synthesized elements to be exempt, got: %v", err)
	}
	if _, err := tr.Transform(newDoc(), TransformContext{}); err != nil {
		t.Fatalf("expected Transform to accept the same document, got: %v", err)
	}
}

// TestValidateAuthoredProperties_ReservationErrorNamesStampedOrigin: a document the
// lowering engine already stamped is named by its authored origin, as Transform's own
// check names it, not by its current metadata.
func TestValidateAuthoredProperties_ReservationErrorNamesStampedOrigin(t *testing.T) {
	newDoc := func() *Application {
		app := appWithTrait("nested-reserved", map[string]any{"config": map[string]any{"locked": "x"}})
		app.Metadata.Name = "myapp-lowered"
		app.origin = &Origin{Document: "myapp", DocumentKind: "Wrapper", Namespace: "test"}
		return app
	}
	tr := nestedReservedTransformer()
	tr.RegisterTraitLowering(nestedReservedWrapperRule{})
	authoredErr := tr.ValidateAuthoredProperties(newDoc())
	_, transformErr := tr.Transform(newDoc(), TransformContext{})
	expectReservedRefusal(t, authoredErr, `in document "myapp" (kind "Wrapper")`)
	expectReservedRefusal(t, transformErr, `in document "myapp" (kind "Wrapper")`)
	if authoredErr.Error() != transformErr.Error() {
		t.Errorf("error text differs:\n  ValidateAuthoredProperties: %v\n  Transform:                  %v", authoredErr, transformErr)
	}
}

// TestValidateAuthoredProperties_ReservationErrorMatchesTransform: one document fails
// with the same text on both paths, with and without lowering rules registered (the
// two are reported by different Transform stages).
func TestValidateAuthoredProperties_ReservationErrorMatchesTransform(t *testing.T) {
	docs := map[string]func() *Application{
		"component": func() *Application {
			return singleComponentApp("Application", "reserved-sink", authoredReservedNull())
		},
		"trait": func() *Application {
			return appWithTrait("nested-reserved", map[string]any{"config": map[string]any{"locked": "x"}})
		},
		"trait in an array item": func() *Application {
			return appWithTrait("nested-reserved", map[string]any{"items": []any{map[string]any{"locked": "x"}}})
		},
	}
	transformers := map[string]func() *Transformer{
		"without lowering rules": nestedReservedTransformer,
		"with a lowering rule": func() *Transformer {
			tr := nestedReservedTransformer()
			tr.RegisterTraitLowering(nestedReservedWrapperRule{})
			return tr
		},
	}
	for trName, newTr := range transformers {
		for docName, newDoc := range docs {
			t.Run(trName+"/"+docName, func(t *testing.T) {
				tr := newTr()
				authoredErr := tr.ValidateAuthoredProperties(newDoc())
				_, transformErr := tr.Transform(newDoc(), TransformContext{})
				expectReservedRefusal(t, authoredErr, `component "web"`)
				expectReservedRefusal(t, transformErr, `component "web"`)
				if authoredErr.Error() != transformErr.Error() {
					t.Errorf("error text differs:\n  ValidateAuthoredProperties: %v\n  Transform:                  %v", authoredErr, transformErr)
				}
			})
		}
	}
}
