package oam

import (
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// sharingHandler declares one integer field and one open object. The open object
// takes any value under any key, so a component built in Go can put a pointer or a
// struct value there and still validate.
type sharingHandler struct{}

func (sharingHandler) CanHandle(t string) bool { return t == "sharing" }

func (sharingHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"n":    {Type: PropertyTypeInteger},
		"open": {Type: PropertyTypeObject, AdditionalProperties: true},
	}
}

func (sharingHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}

// sharingBox is a struct value that holds a map.
type sharingBox struct {
	M map[string]any
}

// sharingDoc builds the document afresh on every call, with its own pointers and
// maps. A second call is therefore a snapshot that shares nothing with the first,
// so a write through a shared pointer changes the first document and not the
// snapshot. Every pointee carries a nested null, an entry that a validator which
// reached the pointee could strip. Validation leaves a key under an open object as
// authored today, so the nulls are the trace a future change would leave.
func sharingDoc() *Application {
	pm := map[string]any{"k": "v", "gone": nil}
	ps := []any{map[string]any{"x": 1, "gone": nil}}
	return singleComponentApp("Application", "sharing", map[string]any{
		"n": purityUID(5),
		"open": map[string]any{
			"ptrMap":    &pm,
			"ptrSlice":  &ps,
			"structMap": sharingBox{M: map[string]any{"a": 1, "gone": nil}},
			"plain":     map[string]any{"gone": nil},
		},
	})
}

func sharingTransformer() *Transformer {
	return NewTransformer(map[string]ComponentHandler{"sharing": sharingHandler{}}, map[string]TraitHandler{})
}

// TestValidateAuthoredProperties_NeverWritesThroughAPointerOrStruct answers the
// question go-kure/launcher#1365 asks: validation's own code reads a pointer or a struct
// value under an open object and never writes through it. A formatting method that a
// value supplies (String, Error or Format) runs when an error message formats the
// value, and that method is outside this test. The copy shares both with the caller's
// Application (README, "The `app` argument is never edited"), so a write through
// either would reach the caller. The expected value is a second call of sharingDoc,
// which holds no pointer of the first, and not a shallow copy of it.
//
// Four cases: a pointer to a map, a pointer to a slice, a struct that holds a map,
// and a control. The control writes through the same pointer on the returned copy,
// and the caller's document must change. It proves that the comparison can see a
// write through a pointer, and that the copy does share it.
func TestValidateAuthoredProperties_NeverWritesThroughAPointerOrStruct(t *testing.T) {
	entries := map[string]func(*Transformer, *Application) (*Application, error){
		"ValidateAuthoredProperties": func(tr *Transformer, app *Application) (*Application, error) {
			return tr.ValidateAuthoredProperties(app)
		},
		"ValidateAuthoredPropertiesWithCapabilities": func(tr *Transformer, app *Application) (*Application, error) {
			return tr.ValidateAuthoredPropertiesWithCapabilities(app, nil)
		},
	}
	for entry, validate := range entries {
		t.Run(entry, func(t *testing.T) {
			app := sharingDoc()
			validated, err := validate(sharingTransformer(), app)
			if err != nil {
				t.Fatalf("the fixture must validate, got: %v", err)
			}
			if validated == nil || validated == app {
				t.Fatalf("validated = %p, want a copy distinct from the caller's %p", validated, app)
			}

			// Validation ran on the copy: n is a plain int there. The caller's n stays the
			// named type.
			vp := validated.Spec.Components[0].Properties
			if _, ok := vp["n"].(int); !ok {
				t.Fatalf("the copy's n = %T, want int: validation did not run on the copy", vp["n"])
			}
			if _, ok := app.Spec.Components[0].Properties["n"].(purityUID); !ok {
				t.Fatalf("the caller's n = %T, want purityUID: validation wrote into the caller's map", app.Spec.Components[0].Properties["n"])
			}

			t.Run("pointer to a map", func(t *testing.T) {
				assertSharingUntouched(t, app, "ptrMap")
			})
			t.Run("pointer to a slice", func(t *testing.T) {
				assertSharingUntouched(t, app, "ptrSlice")
			})
			t.Run("struct that holds a map", func(t *testing.T) {
				assertSharingUntouched(t, app, "structMap")
			})
			t.Run("control: a write through the copy reaches the caller", func(t *testing.T) {
				cp := vp["open"].(map[string]any)["ptrMap"].(*map[string]any)
				(*cp)["written"] = true
				want := sharingDoc()
				if reflect.DeepEqual(app, want) {
					t.Errorf("a write through the copy's pointer left the caller's document equal to a fresh one: the comparison cannot see it")
				}
				if _, ok := (*app.Spec.Components[0].Properties["open"].(map[string]any)["ptrMap"].(*map[string]any))["written"]; !ok {
					t.Errorf("the copy does not share the pointer with the caller")
				}
			})
		})
	}
}

// assertSharingUntouched compares one open key of app with a fresh sharingDoc.
// reflect.DeepEqual follows pointers, so the fresh document's own pointee is the
// reference, never the caller's.
func assertSharingUntouched(t *testing.T, app *Application, key string) {
	t.Helper()
	got := app.Spec.Components[0].Properties["open"].(map[string]any)[key]
	want := sharingDoc().Spec.Components[0].Properties["open"].(map[string]any)[key]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("validation wrote through %q:\n  now:      %#v\n  authored: %#v", key, got, want)
	}
}
