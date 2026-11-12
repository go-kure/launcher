package oam

import (
	"errors"
	"slices"
	"testing"
)

// TestAuthoredPropertyRefusalsAreTyped is go-kure/launcher#1466: every authored
// property refusal is a *PropertyError, found with errors.As, carrying where in the
// document it is. perrCases holds a document for each refusal site.
func TestAuthoredPropertyRefusalsAreTyped(t *testing.T) {
	cases := perrCases()
	var kinds []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newPerrTransformer().ValidateAuthoredPropertiesWithCapabilities(tc.app, tc.caps)
			var pe *PropertyError
			if !errors.As(err, &pe) {
				t.Fatalf("error %T %v is not a *PropertyError", err, err)
			}
			got := [6]string{string(pe.Kind), pe.Position, pe.Component, pe.Trait, pe.Policy, pe.Field}
			want := [6]string{tc.kind, tc.pos, tc.comp, tc.trait, tc.pol, tc.field}
			if got != want {
				t.Errorf("kind, position, component, trait, policy, field\n got: %q\nwant: %q", got, want)
			}
		})
		if !slices.Contains(kinds, tc.kind) {
			kinds = append(kinds, tc.kind)
		}
	}
	// The control: the table reaches every kind, so a kind no case produces cannot hide.
	all := []PropertyErrorKind{
		PropertyErrorUnsupportedField, PropertyErrorRequired, PropertyErrorExclusive, PropertyErrorType,
		PropertyErrorEnum, PropertyErrorRange, PropertyErrorNullElement, PropertyErrorSchema,
		PropertyErrorPlatformReserved,
	}
	for _, k := range all {
		if !slices.Contains(kinds, string(k)) {
			t.Errorf("no case produces kind %q", k)
		}
	}
	if len(kinds) != len(all) {
		t.Errorf("the cases name %d kinds %v, the type has %d", len(kinds), kinds, len(all))
	}
}

// TestPropertyErrorMessageIsTheRefusalText: Error() is the text the refusal always had,
// so a caller that logs or compares the message sees no change.
func TestPropertyErrorMessageIsTheRefusalText(t *testing.T) {
	_, err := newPerrTransformer().ValidateAuthoredProperties(perrApp(with(map[string]any{"name": 5})))
	var pe *PropertyError
	if !errors.As(err, &pe) {
		t.Fatalf("not a *PropertyError: %v", err)
	}
	const want = `component "web" (type "perr"): properties.name: expected string, got int`
	if pe.Error() != want || err.Error() != want {
		t.Errorf("Error() = %q, top-level %q, want %q", pe.Error(), err.Error(), want)
	}
}

// A schema is wrong at a node validatePropertyValue is handed directly: the site is
// reachable from the emission path and from a caller with its own walk, where no
// boundary fills a position. The kind is set at the site; the position stays empty.
func TestPropertyErrorWithoutABoundary(t *testing.T) {
	_, err := validatePropertyValue(PropertySchema{Type: PropertyTypeString, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}}}, "x", "p.q")
	var pe *PropertyError
	if !errors.As(err, &pe) {
		t.Fatalf("not a *PropertyError: %v", err)
	}
	if pe.Kind != PropertyErrorSchema || pe.Position != "" || pe.Field != "" {
		t.Errorf("got kind %q position %q field %q, want schema, empty, empty", pe.Kind, pe.Position, pe.Field)
	}
	if want := "p.q: schema declares exclusive groups on a non-object node; they name keys of an object"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

// CheckSchemaNodeGroups is exported for a caller that owns the walk: its refusal is
// typed too, and says nothing of a document, since it reads a schema alone.
func TestCheckSchemaNodeGroupsIsTyped(t *testing.T) {
	err := CheckSchemaNodeGroups(PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{"a": {Type: PropertyTypeString}}, Exclusive: []ExclusiveGroup{{Keys: []string{"a"}}}}, "model.x")
	var pe *PropertyError
	if !errors.As(err, &pe) {
		t.Fatalf("not a *PropertyError: %v", err)
	}
	if pe.Kind != PropertyErrorSchema {
		t.Errorf("kind = %q, want %q", pe.Kind, PropertyErrorSchema)
	}
}
