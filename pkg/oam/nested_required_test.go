package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// Nested Required is checked on a trait's properties as the capability rendering
// merges into them, not only as written (go-kure/launcher#765): by
// ValidateAuthoredPropertiesWithCapabilities, and by Transform itself at both trait
// merge sites (applyTraits and the lowering fixpoint).

// issuerSchema has a required key inside an object a rendering can supply, and one
// inside an array's elements, which a rendering never merges into.
func issuerSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"secretName": {Type: PropertyTypeString, Required: true},
		"issuerRef": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"name": {Type: PropertyTypeString, Required: true},
			"kind": {Type: PropertyTypeString},
		}},
		"sans": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"host": {Type: PropertyTypeString, Required: true},
		}}},
	}
}

// issuerTrait is a TraitHandler declaring issuerSchema; it records what Apply was
// handed.
type issuerTrait struct{ got map[string]any }

func (h *issuerTrait) CanHandle(t string) bool { return t == "issuer" }
func (h *issuerTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	h.got = trait.Properties
	return nil
}
func (h *issuerTrait) PropertySchema() map[string]PropertySchema { return issuerSchema() }

// issuerRule is issuerTrait's lowering-rule counterpart: it claims "issuer-rule" and
// emits an "issuer-done" trait a recordingTraitHandler takes.
type issuerRule struct{ got *map[string]any }

func (issuerRule) TraitType() string { return "issuer-rule" }
func (r issuerRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	*r.got = trait.Properties
	return LoweringResult{Traits: []Trait{{Type: "issuer-done", Properties: map[string]any{}}}}, nil
}
func (issuerRule) PropertySchema() map[string]PropertySchema { return issuerSchema() }

func issuerTransformer(h *issuerTrait, ruleGot *map[string]any) *Transformer {
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{
			"issuer":      h,
			"issuer-done": &recordingTraitHandler{typ: "issuer-done"},
		},
	)
	tr.RegisterTraitLowering(issuerRule{got: ruleGot})
	return tr
}

func issuerApp(traitType string, props map[string]any) *Application {
	return storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: traitType, Properties: props}}})
}

func issuerBinding(traitType string, rendering map[string]any) map[string]CapabilityBinding {
	return map[string]CapabilityBinding{traitType: {Rendering: rendering}}
}

// issuerScope is a named string type, as a library caller may author a scope.
type issuerScope string

const (
	plainRequired    = `properties.issuerRef: "name" is required`
	renderedRequired = `properties.issuerRef: "name" is required; neither the trait nor capability "issuer"'s rendering sets it`
)

func TestValidateAuthoredPropertiesWithCapabilities_NestedRequired(t *testing.T) {
	partial := func() map[string]any {
		return map[string]any{"secretName": "tls", "issuerRef": map[string]any{"kind": "Issuer"}}
	}
	tests := []struct {
		name     string
		props    map[string]any
		caps     map[string]CapabilityBinding
		plain    bool // run ValidateAuthoredProperties instead of the variant
		wantErr  string
		wantNone bool
	}{
		{
			name:    "no binding: refused as written, by the plain validator",
			props:   partial(),
			plain:   true,
			wantErr: plainRequired,
		},
		{
			name:    "no binding: refused as written, by the variant, with the same text",
			props:   partial(),
			caps:    issuerBinding("other", map[string]any{"issuerRef": map[string]any{"name": "ca"}}),
			wantErr: plainRequired,
		},
		{
			name:    "binding matches but neither side sets the key: refused naming the capability",
			props:   partial(),
			caps:    issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"kind": "ClusterIssuer"}}),
			wantErr: renderedRequired,
		},
		{
			// The plain validator refuses the same document (the first row).
			name:     "rendering supplies the nested sibling of a partial override: accepted",
			props:    partial(),
			caps:     issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"name": "ca"}}),
			wantNone: true,
		},
		{
			name:    "rendered array element missing a required key: refused naming the capability",
			props:   map[string]any{"secretName": "tls"},
			caps:    issuerBinding("issuer", map[string]any{"sans": []any{map[string]any{}}}),
			wantErr: `properties.sans[0]: "host" is required; neither the trait nor capability "issuer"'s rendering sets it`,
		},
		{
			name: "Required inside an array element is not relaxed by a binding",
			props: map[string]any{
				"secretName": "tls",
				"sans":       []any{map[string]any{}},
			},
			caps:    issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"name": "ca"}}),
			wantErr: `properties.sans[0]: "host" is required`,
		},
		{
			// The binding is looked up on the validated properties, where a scope of a
			// named string type has been normalized, so it matches as in Transform.
			name:    "scope of a named string type selects its scoped binding",
			props:   map[string]any{"secretName": "tls", "scope": issuerScope("internal")},
			caps:    issuerBinding("issuer.internal", map[string]any{"issuerRef": map[string]any{"kind": "Issuer"}}),
			wantErr: `properties.issuerRef: "name" is required; neither the trait nor capability "issuer.internal"'s rendering sets it`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
			app := issuerApp("issuer", tc.props)
			var err error
			if tc.plain {
				err = tr.ValidateAuthoredProperties(app)
			} else {
				err = tr.ValidateAuthoredPropertiesWithCapabilities(app, tc.caps)
			}
			if tc.wantNone {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.HasSuffix(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to end with %q", err, tc.wantErr)
			}
			if !tc.plain && tc.wantErr == plainRequired && strings.Contains(err.Error(), "capability") {
				t.Errorf("an unbound trait's refusal must not name a capability: %v", err)
			}
		})
	}
}

// TestValidateAuthoredPropertiesWithCapabilities_DoesNotMutateHandlerSchema pins the
// copy in relaxObjectRequired: a handler returning a shared schema map keeps its
// nested Required flags after a bound trait is validated.
func TestValidateAuthoredPropertiesWithCapabilities_DoesNotMutateHandlerSchema(t *testing.T) {
	shared := issuerSchema()
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": schemaComponent{typ: "webservice"}},
		map[string]TraitHandler{"issuer": sharedSchemaTrait{typ: "issuer", schema: shared}},
	)
	app := authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "issuer", Properties: map[string]any{"secretName": "tls"}})
	caps := issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"name": "ca"}})
	if err := tr.ValidateAuthoredPropertiesWithCapabilities(app, caps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shared["issuerRef"].Properties["name"].Required {
		t.Error("relaxObjectRequired cleared Required in the handler's own schema map")
	}
}

// TestTransform_NestedRequiredAfterMerge: Transform checks nested Required on the
// merged properties at both trait merge sites, with no authored validation run
// first — whether or not a binding matched.
func TestTransform_NestedRequiredAfterMerge(t *testing.T) {
	sites := []struct {
		name, traitType string
	}{
		{name: "dispatch", traitType: "issuer"},
		{name: "lowering", traitType: "issuer-rule"},
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			partial := map[string]any{"secretName": "tls", "issuerRef": map[string]any{"kind": "Issuer"}}

			t.Run("rendering supplies the nested sibling: built, and the handler sees both", func(t *testing.T) {
				h, got := &issuerTrait{}, map[string]any{}
				tr := issuerTransformer(h, &got)
				caps := issuerBinding(site.traitType, map[string]any{"issuerRef": map[string]any{"name": "ca"}})
				if _, err := tr.Transform(issuerApp(site.traitType, partial), TransformContext{Capabilities: caps}); err != nil {
					t.Fatalf("Transform: %v", err)
				}
				seen := h.got
				if site.traitType != "issuer" {
					seen = got
				}
				if ref, _ := seen["issuerRef"].(map[string]any); ref["name"] != "ca" || ref["kind"] != "Issuer" {
					t.Errorf("handler got %v, want issuerRef {name: ca, kind: Issuer}", seen)
				}
			})

			t.Run("array element missing a required key, after a null element: refused", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				props := map[string]any{"secretName": "tls", "sans": []any{nil, map[string]any{"host": nil}}}
				_, err := tr.Transform(issuerApp(site.traitType, props), TransformContext{})
				want := `properties.sans[1]: "host" is required`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})

			t.Run("rendered array element missing a required key: refused", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				caps := issuerBinding(site.traitType, map[string]any{"sans": []any{map[string]any{}}})
				_, err := tr.Transform(issuerApp(site.traitType, map[string]any{"secretName": "tls"}), TransformContext{Capabilities: caps})
				want := `properties.sans[0]: "host" is required; neither the trait nor capability "` + site.traitType + `"'s rendering sets it`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})

			t.Run("neither side sets the key: refused naming the capability", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				caps := issuerBinding(site.traitType, map[string]any{"issuerRef": map[string]any{"kind": "ClusterIssuer"}})
				_, err := tr.Transform(issuerApp(site.traitType, partial), TransformContext{Capabilities: caps})
				want := `properties.issuerRef: "name" is required; neither the trait nor capability "` + site.traitType + `"'s rendering sets it`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
				if !strings.Contains(err.Error(), `"web"`) || !strings.Contains(err.Error(), site.traitType) {
					t.Errorf("error %q must name the component and the trait", err)
				}
			})

			t.Run("no binding: refused as written", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				_, err := tr.Transform(issuerApp(site.traitType, partial), TransformContext{})
				if err == nil || !strings.Contains(err.Error(), plainRequired) || strings.Contains(err.Error(), "capability") {
					t.Fatalf("error = %v, want %q naming no capability", err, plainRequired)
				}
			})
		})
	}
}
