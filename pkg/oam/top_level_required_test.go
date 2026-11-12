package oam

import (
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// Top-level Required is checked by authored validation (go-kure/launcher#1463): as
// written for a component, a policy and a trait no binding matches, and on the merged
// properties for a trait a capability binding matches, here and in Transform. A null
// counts as absent, as it does on the emitted path.

func TestValidateAuthoredProperties_TopLevelRequired(t *testing.T) {
	policyApp := func(props map[string]any) *Application {
		return &Application{Spec: ApplicationSpec{Policies: []ApplicationPolicy{
			{Name: "order", Type: "dependency", Properties: props},
		}}}
	}
	tests := []struct {
		name    string
		app     *Application
		trait   bool   // run on issuerTransformer, which registers the issuer trait
		wantErr string // "" means accept
	}{
		{
			name:    "component: absent key is refused",
			app:     authoredApp("webservice", map[string]any{}),
			wantErr: `component "web" (type "webservice"): properties: "image" is required`,
		},
		{
			name:    "component: nil property map is refused",
			app:     authoredApp("webservice", nil),
			wantErr: `component "web" (type "webservice"): properties: "image" is required`,
		},
		{
			name:    "component: explicit null counts as absent",
			app:     authoredApp("webservice", map[string]any{"image": nil}),
			wantErr: `component "web" (type "webservice"): properties: "image" is required`,
		},
		{
			// A zero value is present. Only absence and null are refused.
			name: "component: an empty string is present",
			app:  authoredApp("webservice", map[string]any{"image": ""}),
		},
		{
			name: "component: a multiline string is present",
			app:  authoredApp("webservice", map[string]any{"image": "a\nb"}),
		},
		{
			name:    "trait with no binding: absent key is refused as written",
			app:     issuerApp("issuer", map[string]any{}),
			trait:   true,
			wantErr: `component "web": trait "issuer": properties: "secretName" is required`,
		},
		{
			name:    "trait with no binding: explicit null counts as absent",
			app:     issuerApp("issuer", map[string]any{"secretName": nil}),
			trait:   true,
			wantErr: `component "web": trait "issuer": properties: "secretName" is required`,
		},
		{
			name:  "trait with no binding: key present is accepted",
			app:   issuerApp("issuer", map[string]any{"secretName": "tls"}),
			trait: true,
		},
		{
			name:    "policy: absent key is refused as written",
			app:     policyApp(map[string]any{}),
			wantErr: `policy "order" (type "dependency"): properties: "components" is required`,
		},
		{
			name:    "policy: nil property map is refused",
			app:     policyApp(nil),
			wantErr: `policy "order" (type "dependency"): properties: "components" is required`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := newSchemaTransformer()
			if tc.trait {
				tr = issuerTransformer(&issuerTrait{}, new(map[string]any))
			}
			_, err := tr.ValidateAuthoredProperties(tc.app)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A trait a capability binding matches is checked on the merged properties: the
// rendering may supply a key the author left out, and a key neither side supplies is
// refused naming the capability.
func TestValidateAuthoredPropertiesWithCapabilities_TopLevelRequired(t *testing.T) {
	const neither = `component "web": trait "issuer": properties: "secretName" is required; neither the trait nor capability "issuer"'s rendering sets it`
	tests := []struct {
		name    string
		props   map[string]any
		caps    map[string]CapabilityBinding
		wantErr string // "" means accept
	}{
		{
			name:  "rendering supplies the key: accepted",
			props: map[string]any{},
			caps:  issuerBinding("issuer", map[string]any{"secretName": "tls"}),
		},
		{
			name:    "binding matches but neither side sets the key: refused naming the capability",
			props:   map[string]any{},
			caps:    issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"name": "ca"}}),
			wantErr: neither,
		},
		{
			name:    "author's null does not hide the rendering's absence",
			props:   map[string]any{"secretName": nil},
			caps:    issuerBinding("issuer", map[string]any{"issuerRef": map[string]any{"name": "ca"}}),
			wantErr: neither,
		},
		{
			name:    "binding for another trait: refused as written, naming no capability",
			props:   map[string]any{},
			caps:    issuerBinding("other", map[string]any{"secretName": "tls"}),
			wantErr: `component "web": trait "issuer": properties: "secretName" is required`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
			_, err := tr.ValidateAuthoredPropertiesWithCapabilities(issuerApp("issuer", tc.props), tc.caps)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to end with %q", err, tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(tc.wantErr, "capability") && strings.Contains(err.Error(), "capability"):
				t.Errorf("an unbound trait's refusal must not name a capability: %v", err)
			}
		})
	}
}

// Transform checks top-level Required on the merged properties at both trait merge
// sites, with no authored validation run first, whether or not a binding matched.
func TestTransform_TopLevelRequiredAfterMerge(t *testing.T) {
	for _, traitType := range []string{"issuer", "issuer-rule"} {
		t.Run(traitType, func(t *testing.T) {
			t.Run("rendering supplies the key: built", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				caps := issuerBinding(traitType, map[string]any{"secretName": "tls"})
				if _, err := tr.Transform(issuerApp(traitType, map[string]any{}), TransformContext{Capabilities: caps}); err != nil {
					t.Fatalf("Transform: %v", err)
				}
			})

			t.Run("neither side sets the key: refused naming the capability", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				caps := issuerBinding(traitType, map[string]any{"issuerRef": map[string]any{"name": "ca"}})
				_, err := tr.Transform(issuerApp(traitType, map[string]any{}), TransformContext{Capabilities: caps})
				want := `properties: "secretName" is required; neither the trait nor capability "` + traitType + `"'s rendering sets it`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})

			t.Run("no binding: refused as written", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				_, err := tr.Transform(issuerApp(traitType, map[string]any{}), TransformContext{})
				want := `properties: "secretName" is required`
				if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "capability") {
					t.Fatalf("error = %v, want %q naming no capability", err, want)
				}
			})

			t.Run("explicit null counts as absent", func(t *testing.T) {
				tr := issuerTransformer(&issuerTrait{}, new(map[string]any))
				_, err := tr.Transform(issuerApp(traitType, map[string]any{"secretName": nil}), TransformContext{})
				if err == nil || !strings.Contains(err.Error(), `"secretName" is required`) {
					t.Fatalf("error = %v, want a refusal naming secretName", err)
				}
			})
		})
	}
}

// requiredDefaultsHandler is a component whose Required key is also one the
// capability fills (ComponentCapabilityDefaults), so the author may leave it out.
type requiredDefaultsHandler struct{}

func (requiredDefaultsHandler) CanHandle(t string) bool { return t == "store" }
func (requiredDefaultsHandler) CapabilityDefaults() (string, []string) {
	return "store", []string{"class"}
}
func (requiredDefaultsHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"class": {Type: PropertyTypeString, Required: true},
		"size":  {Type: PropertyTypeString, Required: true},
	}
}
func (requiredDefaultsHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}

// A key a ComponentCapabilityDefaults handler lists is left to the capability, so
// authored validation does not refuse its absence. Every other Required key is.
func TestValidateAuthoredProperties_TopLevelRequiredSkipsCapabilityDefaultKeys(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"store": requiredDefaultsHandler{}}, nil)

	if _, err := tr.ValidateAuthoredProperties(authoredApp("store", map[string]any{"size": "1Gi"})); err != nil {
		t.Fatalf("a key the capability fills was refused: %v", err)
	}
	_, err := tr.ValidateAuthoredProperties(authoredApp("store", map[string]any{"class": "fast"}))
	if err == nil || !strings.Contains(err.Error(), `"size" is required`) {
		t.Fatalf("error = %v, want a refusal naming size", err)
	}
}

// The authored and the emitted path word the refusal alike for the same schema and
// document, so the two cannot drift.
func TestValidateAuthoredProperties_TopLevelRequiredReadsLikeEmitted(t *testing.T) {
	tr := newSchemaTransformer()
	_, authored := tr.ValidateAuthoredProperties(authoredApp("webservice", map[string]any{}))
	emitted := tr.validateEmittedComponent(&Component{Name: "web", Type: "webservice", Properties: map[string]any{}})
	if authored == nil || emitted == nil {
		t.Fatalf("both paths must refuse: authored = %v, emitted = %v", authored, emitted)
	}
	if !strings.HasSuffix(authored.Error(), `"image" is required`) || !strings.HasSuffix(emitted.Error(), `"image" is required`) {
		t.Errorf("the paths word the refusal differently: authored = %q, emitted = %q", authored, emitted)
	}
}

// relaxObjectRequired clears Required at the top level too, in a copy: the handler's
// own schema keeps its flags.
func TestRelaxObjectRequired_ClearsTopLevelWithoutMutating(t *testing.T) {
	shared := issuerSchema()
	relaxed := relaxObjectRequired(shared)
	if relaxed["secretName"].Required {
		t.Error("top-level Required was not cleared in the relaxed copy")
	}
	if !shared["secretName"].Required || !shared["issuerRef"].Properties["name"].Required {
		t.Error("relaxObjectRequired mutated the handler's own schema")
	}
}
