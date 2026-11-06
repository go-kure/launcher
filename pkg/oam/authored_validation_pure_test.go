package oam

import (
	"reflect"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// purityHandler is a component handler whose schema has an object field with two
// integer keys, so a document can carry a nested null and a value validation
// normalizes (a named integer type becomes an int). It keeps the properties Transform handed it.
type purityHandler struct {
	got *map[string]any
}

func (h *purityHandler) CanHandle(t string) bool { return t == "purity" }

func (h *purityHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"securityContext": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"runAsUser":  {Type: PropertyTypeInteger},
				"runAsGroup": {Type: PropertyTypeInteger},
			},
		},
	}
}

func (h *purityHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	*h.got = copyPropertyMap(c.Properties)
	return &stubAppConfig{}, nil
}

// purityTuningSchema is the one object field the trait and the policy declare: two
// integer keys, so each can carry a nested null and a value validation normalizes.
func purityTuningSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"tuning": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"a": {Type: PropertyTypeInteger},
				"b": {Type: PropertyTypeInteger},
			},
		},
	}
}

// purityTraitHandler and purityPolicyHandler reach copyAuthoredDocument's trait and
// policy branches. Each keeps the properties Transform handed it.
type purityTraitHandler struct{ got *map[string]any }

func (h *purityTraitHandler) CanHandle(t string) bool { return t == "purity-trait" }
func (h *purityTraitHandler) PropertySchema() map[string]PropertySchema {
	return purityTuningSchema()
}
func (h *purityTraitHandler) Apply(tr *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*h.got = copyPropertyMap(tr.Properties)
	return nil
}

type purityPolicyHandler struct{ got *map[string]any }

func (h *purityPolicyHandler) CanHandle(t string) bool { return t == "purity-policy" }
func (h *purityPolicyHandler) PropertySchema() map[string]PropertySchema {
	return purityTuningSchema()
}
func (h *purityPolicyHandler) Apply(p *ApplicationPolicy, _ []string, _ *PolicyResult) error {
	*h.got = copyPropertyMap(p.Properties)
	return nil
}

// purityGot holds what Transform handed each handler.
type purityGot struct {
	component, trait, policy map[string]any
}

// purityTransformer registers the handlers under plain names and declares the type
// identity purity.acme.example@v1 for the component.
func purityTransformer(t *testing.T) (*Transformer, *purityGot) {
	t.Helper()
	got := &purityGot{}
	tr := NewTransformer(
		map[string]ComponentHandler{"purity": &purityHandler{got: &got.component}},
		map[string]TraitHandler{"purity-trait": &purityTraitHandler{got: &got.trait}},
	)
	tr.RegisterPolicy("purity-policy", &purityPolicyHandler{got: &got.policy})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"purity": mustID(t, "purity.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	return tr, got
}

// purityUID is a named integer type: validation normalizes it to a plain int.
type purityUID int64

// purityDoc builds the fixture afresh on every call, so a second call is an
// independent deep snapshot of what the first one was authored as. Besides the
// property tree it carries the fields a copy of the document shares with the
// original (the metadata maps, Component.Annotations, the unexported origins),
// so a validator that wrote into any of them would show up in a DeepEqual.
func purityDoc(typ string) *Application {
	return purityDocWith(typ, purityUID(1000))
}

func purityDocWith(typ string, runAsUser any) *Application {
	app := singleComponentApp("Application", typ, map[string]any{
		"securityContext": map[string]any{"runAsUser": runAsUser, "runAsGroup": nil},
	})
	app.Metadata.Labels = map[string]string{"team": "a"}
	app.Metadata.Annotations = map[string]string{"note": "n"}
	app.origin = &Origin{Document: "myapp", DocumentKind: "Application", Namespace: "test"}
	comp := &app.Spec.Components[0]
	comp.Annotations = map[string]string{"component-note": "c"}
	comp.origin = &Origin{Document: "myapp", DocumentKind: "Application", Namespace: "test", Component: "web", ComponentType: typ}
	comp.Traits = []Trait{{
		Type:       "purity-trait",
		Properties: map[string]any{"tuning": map[string]any{"a": purityUID(7), "b": nil}},
		origin:     &Origin{Document: "myapp", DocumentKind: "Application", Namespace: "test", Component: "web"},
	}}
	app.Spec.Policies = []ApplicationPolicy{{
		Name:       "tune",
		Type:       "purity-policy",
		Properties: map[string]any{"tuning": map[string]any{"a": purityUID(8), "b": nil}},
		origin:     &Origin{Document: "myapp", DocumentKind: "Application", Namespace: "test"},
	}}
	return app
}

// purityShares reports whether a and b are one map value, not merely equal.
func purityShares(a, b map[string]any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// purityEntries are the three validation entry points, as one signature.
func purityEntries() map[string]func(*Transformer, *Application) (*Application, error) {
	return map[string]func(*Transformer, *Application) (*Application, error){
		"ValidateAuthoredProperties": func(tr *Transformer, app *Application) (*Application, error) {
			return tr.ValidateAuthoredProperties(app)
		},
		"ValidateAuthoredPropertiesWithCapabilities": func(tr *Transformer, app *Application) (*Application, error) {
			return tr.ValidateAuthoredPropertiesWithCapabilities(app, nil)
		},
		"ValidateAuthoredPropertiesForTarget": func(tr *Transformer, app *Application) (*Application, error) {
			return tr.ValidateAuthoredPropertiesForTarget(app, nil, &KubernetesVersion{1, 34})
		},
	}
}

// TestValidateAuthoredProperties_LeavesTheCallersMapsAlone is go-kure/launcher#1282:
// a validation call reads as a check, so it must not delete a nested null from, or
// rewrite a value in, the property maps of the document it was handed, for a
// plain-name component and for an identity-typed one, through both entry points. The
// strip still takes effect before Transform consumes the document (go-kure/launcher#635):
// the copy the validator returns is stripped and normalized, and Transform of that
// copy hands the handler no nested null.
func TestValidateAuthoredProperties_LeavesTheCallersMapsAlone(t *testing.T) {
	want := map[string]any{"securityContext": map[string]any{"runAsUser": 1000}}
	wantTrait := map[string]any{"tuning": map[string]any{"a": 7}}
	wantPolicy := map[string]any{"tuning": map[string]any{"a": 8}}
	for entry, validate := range purityEntries() {
		for _, typ := range []string{"purity", "purity.acme.example@v1"} {
			t.Run(entry+"/"+typ, func(t *testing.T) {
				tr, got := purityTransformer(t)
				app := purityDoc(typ)

				validated, err := validate(tr, app)
				if err != nil {
					t.Fatalf("the fixture must validate, got: %v", err)
				}
				// The whole document, not only the property trees: type, annotations,
				// metadata maps and the unexported origins are shared with the copy.
				if !reflect.DeepEqual(app, purityDoc(typ)) {
					t.Errorf("validation changed the caller's document:\n  now:      %#v\n  authored: %#v", app, purityDoc(typ))
				}

				if validated == nil || validated == app {
					t.Fatalf("validated = %p, want a copy distinct from the caller's %p", validated, app)
				}
				if vc := validated.Spec.Components[0]; vc.Type != typ {
					t.Errorf("the copy's component type = %q, want the authored %q", vc.Type, typ)
				} else if !reflect.DeepEqual(vc.Properties, want) {
					t.Errorf("the copy's properties = %#v, want the nested null stripped and runAsUser an int: %#v", vc.Properties, want)
				}

				if tp := validated.Spec.Components[0].Traits[0].Properties; !reflect.DeepEqual(tp, wantTrait) {
					t.Errorf("the copy's trait properties = %#v, want %#v", tp, wantTrait)
				}
				if pp := validated.Spec.Policies[0].Properties; !reflect.DeepEqual(pp, wantPolicy) {
					t.Errorf("the copy's policy properties = %#v, want %#v", pp, wantPolicy)
				}

				// The copy's maps are not the caller's.
				for name, pair := range map[string][2]map[string]any{
					"component": {app.Spec.Components[0].Properties, validated.Spec.Components[0].Properties},
					"trait":     {app.Spec.Components[0].Traits[0].Properties, validated.Spec.Components[0].Traits[0].Properties},
					"policy":    {app.Spec.Policies[0].Properties, validated.Spec.Policies[0].Properties},
				} {
					if purityShares(pair[0], pair[1]) {
						t.Errorf("the copy shares its %s property map with the caller's document", name)
					}
				}

				if _, err := tr.Transform(validated, TransformContext{}); err != nil {
					t.Fatalf("Transform of the validated copy: %v", err)
				}
				if !reflect.DeepEqual(got.component, want) {
					t.Errorf("Transform of the validated copy handed the component handler %#v, want %#v", got.component, want)
				}
				if !reflect.DeepEqual(got.trait, wantTrait) {
					t.Errorf("Transform of the validated copy handed the trait handler %#v, want %#v", got.trait, wantTrait)
				}
				if !reflect.DeepEqual(got.policy, wantPolicy) {
					t.Errorf("Transform of the validated copy handed the policy handler %#v, want %#v", got.policy, wantPolicy)
				}
			})
		}
	}
}

// A refused document gives no copy, and the whole caller's document is as authored,
// through every entry point and for both type spellings.
func TestValidateAuthoredProperties_RefusalReturnsNoCopy(t *testing.T) {
	for entry, validate := range purityEntries() {
		for _, typ := range []string{"purity", "purity.acme.example@v1"} {
			t.Run(entry+"/"+typ, func(t *testing.T) {
				tr, _ := purityTransformer(t)
				app := purityDocWith(typ, "root")
				validated, err := validate(tr, app)
				if err == nil {
					t.Fatal("expected a string runAsUser to be refused")
				}
				if validated != nil {
					t.Errorf("a refused document returned a copy: %#v", validated)
				}
				if !reflect.DeepEqual(app, purityDocWith(typ, "root")) {
					t.Errorf("a refused document was edited:\n  now:      %#v\n  authored: %#v", app, purityDocWith(typ, "root"))
				}
			})
		}
	}
}

// A nil document is nil, nil, as it was nil before.
func TestValidateAuthoredProperties_NilDocument(t *testing.T) {
	tr, _ := purityTransformer(t)
	if v, err := tr.ValidateAuthoredProperties(nil); v != nil || err != nil {
		t.Errorf("ValidateAuthoredProperties(nil) = %v, %v; want nil, nil", v, err)
	}
	if v, err := tr.ValidateAuthoredPropertiesForTarget(nil, nil, &KubernetesVersion{1, 34}); v != nil || err != nil {
		t.Errorf("ValidateAuthoredPropertiesForTarget(nil) = %v, %v; want nil, nil", v, err)
	}
}
