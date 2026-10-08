package oam

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// Exclusive groups (go-kure/launcher#790): PropertySchema.Exclusive on an object
// node, ExclusivePropertiesProvider for a handler's top level, published through
// HandlerSchemaSet.Exclusive.

// oneOfSchema has a top level whose "inline"/"url" pair oneOfTop groups, and an
// object "source" with an exactly-one group and an at-most-one group.
func oneOfSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"inline": {Type: PropertyTypeString},
		"url":    {Type: PropertyTypeString},
		"source": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"git":    {Type: PropertyTypeString},
				"oci":    {Type: PropertyTypeString},
				"tag":    {Type: PropertyTypeString},
				"digest": {Type: PropertyTypeString},
			},
			Exclusive: []ExclusiveGroup{
				{Keys: []string{"git", "oci"}, Required: true},
				{Keys: []string{"tag", "digest"}},
			},
		},
	}
}

func oneOfTop() []ExclusiveGroup {
	return []ExclusiveGroup{{Keys: []string{"inline", "url"}, Required: true}}
}

// oneOfComponent is a ComponentHandler declaring oneOfSchema and oneOfTop.
type oneOfComponent struct{ typ string }

func (h oneOfComponent) CanHandle(t string) bool { return t == h.typ }
func (h oneOfComponent) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stubAppConfig{}, nil
}
func (h oneOfComponent) PropertySchema() map[string]PropertySchema { return oneOfSchema() }
func (h oneOfComponent) ExclusiveProperties() []ExclusiveGroup     { return oneOfTop() }

// oneOfTrait is a TraitHandler declaring oneOfSchema and oneOfTop; it records what
// Apply was handed.
type oneOfTrait struct{ got map[string]any }

func (h *oneOfTrait) CanHandle(t string) bool { return t == "oneof" }
func (h *oneOfTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	h.got = trait.Properties
	return nil
}
func (h *oneOfTrait) PropertySchema() map[string]PropertySchema { return oneOfSchema() }
func (h *oneOfTrait) ExclusiveProperties() []ExclusiveGroup     { return oneOfTop() }

// oneOfRule is oneOfTrait's lowering-rule counterpart: it claims "oneof-rule" and
// emits a "oneof-done" trait a recordingTraitHandler takes.
type oneOfRule struct{}

func (oneOfRule) TraitType() string { return "oneof-rule" }
func (oneOfRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "oneof-done", Properties: map[string]any{}}}}, nil
}
func (oneOfRule) PropertySchema() map[string]PropertySchema { return oneOfSchema() }
func (oneOfRule) ExclusiveProperties() []ExclusiveGroup     { return oneOfTop() }

func oneOfTransformer(h *oneOfTrait) *Transformer {
	tr := NewTransformer(
		map[string]ComponentHandler{
			"webservice": &pipelineComponentHandler{typ: "webservice"},
			"oneof":      oneOfComponent{typ: "oneof"},
		},
		map[string]TraitHandler{
			"oneof":      h,
			"oneof-done": &recordingTraitHandler{typ: "oneof-done"},
		},
	)
	tr.RegisterTraitLowering(oneOfRule{})
	return tr
}

// wantError fails t unless err is non-nil and ends with want.
func wantError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("error = %v, want it to end with %q", err, want)
	}
}

func TestExclusive_SchemaErrors(t *testing.T) {
	object := func(exclusive ...ExclusiveGroup) map[string]PropertySchema {
		return map[string]PropertySchema{"o": {
			Type: PropertyTypeObject,
			Properties: map[string]PropertySchema{
				"a":   {Type: PropertyTypeString},
				"b":   {Type: PropertyTypeString},
				"req": {Type: PropertyTypeString, Required: true},
			},
			Exclusive: exclusive,
		}}
	}
	props := func() map[string]any { return map[string]any{"o": map[string]any{"req": "x"}} }
	tests := []struct {
		name   string
		schema map[string]PropertySchema
		want   string
	}{
		{
			name:   "a group of one key",
			schema: object(ExclusiveGroup{Keys: []string{"a"}}),
			want:   `properties.o: schema declares exclusive group 0 with 1 key(s); a group needs at least two`,
		},
		{
			name:   "an undeclared key",
			schema: object(ExclusiveGroup{Keys: []string{"a", "zz"}}),
			want:   `properties.o: schema declares exclusive key "zz", which is not a declared property`,
		},
		{
			name:   "a key in two groups",
			schema: object(ExclusiveGroup{Keys: []string{"a", "b"}}, ExclusiveGroup{Keys: []string{"b", "a"}}),
			want:   `properties.o: schema lists exclusive key "b" twice`,
		},
		{
			name:   "a Required key",
			schema: object(ExclusiveGroup{Keys: []string{"a", "req"}}),
			want:   `properties.o: schema declares exclusive key "req" Required; a group's keys are optional, and the group's Required says one is set`,
		},
		{
			name: "groups on a non-object node",
			schema: map[string]PropertySchema{"o": {
				Type:      PropertyTypeString,
				Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}},
			}},
			want: `properties.o: schema declares exclusive groups on a non-object node; they name keys of an object`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := props()
			if tc.schema["o"].Type == PropertyTypeString {
				p = map[string]any{"o": "x"}
			}
			wantError(t, validateProperties(tc.schema, p, "properties"), tc.want)
		})
	}

	// A malformed group is the schema's error, so it is reported before anything the
	// document gets wrong: here a missing Required key.
	for _, tc := range tests {
		if tc.schema["o"].Type == PropertyTypeString {
			continue
		}
		t.Run(tc.name+", before a missing Required key", func(t *testing.T) {
			p := map[string]any{"o": map[string]any{}}
			wantError(t, validateProperties(tc.schema, p, "properties"), tc.want)
			wantError(t, checkNestedRequired(tc.schema, nil, p, "properties", ""), tc.want)
		})
	}

	t.Run("a malformed top-level group", func(t *testing.T) {
		top := []ExclusiveGroup{{Keys: []string{"inline", "nope"}}}
		want := `properties: schema declares exclusive key "nope", which is not a declared property`
		wantError(t, validateTopLevelProperties(oneOfSchema(), top, map[string]any{"inline": "x"}, "properties"), want)
		wantError(t, validateAuthoredTopLevel(oneOfSchema(), top, map[string]any{"inline": "x"}, "properties"), want)
	})
	t.Run("a malformed top-level group, before the document's errors", func(t *testing.T) {
		schema := oneOfSchema()
		schema["inline"] = PropertySchema{Type: PropertyTypeString, Required: true}
		want := `properties: schema declares exclusive key "inline" Required; a group's keys are optional, and the group's Required says one is set`
		wantError(t, validateTopLevelProperties(schema, oneOfTop(), map[string]any{}, "properties"), want)
		wantError(t, validateAuthoredTopLevel(schema, oneOfTop(), map[string]any{"zz": "x"}, "properties"), want)
	})
}

// TestExclusive_Emitted: emitted properties are held to both bounds at every level.
func TestExclusive_Emitted(t *testing.T) {
	gitSource := func() map[string]any { return map[string]any{"git": "repo"} }
	tests := []struct {
		name  string
		props map[string]any
		want  string // "" accepts
	}{
		{name: "one of the top-level pair", props: map[string]any{"inline": "x", "source": gitSource()}},
		{name: "a null counts as unset", props: map[string]any{"inline": "x", "url": nil, "source": gitSource()}},
		{
			name:  "both of the top-level pair",
			props: map[string]any{"inline": "x", "url": "y", "source": gitSource()},
			want:  `properties: "inline" and "url" are mutually exclusive`,
		},
		{
			name:  "neither of the top-level pair",
			props: map[string]any{"source": gitSource()},
			want:  `properties: exactly one of "inline" and "url" is required`,
		},
		{
			name:  "both of a nested required pair",
			props: map[string]any{"inline": "x", "source": map[string]any{"git": "a", "oci": "b"}},
			want:  `properties.source: "git" and "oci" are mutually exclusive`,
		},
		{
			name:  "neither of a nested required pair",
			props: map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}},
			want:  `properties.source: exactly one of "git" and "oci" is required`,
		},
		{
			name:  "neither of a nested optional pair",
			props: map[string]any{"inline": "x", "source": gitSource()},
		},
		{
			name:  "both of a nested optional pair",
			props: map[string]any{"inline": "x", "source": map[string]any{"git": "a", "tag": "v1", "digest": "sha"}},
			want:  `properties.source: "tag" and "digest" are mutually exclusive`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			props := tc.props
			err := validateEmittedProperties(oneOfComponent{typ: "oneof"}, &props, "properties")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			wantError(t, err, tc.want)
		})
	}
}

// TestExclusive_Authored: an authored document's top level is held to at most one,
// not to exactly one, as its top-level Required is not enforced; a nested object is
// held to both bounds.
func TestExclusive_Authored(t *testing.T) {
	tr := oneOfTransformer(&oneOfTrait{})
	tests := []struct {
		name  string
		props map[string]any
		want  string
	}{
		{name: "neither of the top-level pair: accepted", props: map[string]any{}},
		{
			name:  "both of the top-level pair",
			props: map[string]any{"inline": "x", "url": "y"},
			want:  `properties: "inline" and "url" are mutually exclusive`,
		},
		{
			name:  "neither of a nested required pair",
			props: map[string]any{"source": map[string]any{}},
			want:  `properties.source: exactly one of "git" and "oci" is required`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, app := range []*Application{
				authoredApp("oneof", tc.props),
				authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: tc.props}),
				authoredApp("webservice", map[string]any{}, Trait{Type: "oneof-rule", Properties: tc.props}),
			} {
				err := tr.ValidateAuthoredProperties(app)
				if tc.want == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					continue
				}
				wantError(t, err, tc.want)
			}
		})
	}
}

// TestExclusive_AuthoredWithCapabilities: a bound trait's nested exactly-one is
// checked on the properties merged with the rendering, and at most one at every
// level.
func TestExclusive_AuthoredWithCapabilities(t *testing.T) {
	binding := func(rendering map[string]any) map[string]CapabilityBinding {
		return map[string]CapabilityBinding{"oneof": {Rendering: rendering}}
	}
	tests := []struct {
		name      string
		props     map[string]any
		rendering map[string]any
		unbound   bool
		want      string
	}{
		{
			name:      "the rendering supplies the nested required key: accepted",
			props:     map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}},
			rendering: map[string]any{"source": map[string]any{"git": "repo"}},
		},
		{
			name:      "neither side sets the nested required key",
			props:     map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}},
			rendering: map[string]any{"source": map[string]any{"digest": "sha"}},
			want:      `properties.source: exactly one of "git" and "oci" is required; neither the trait nor capability "oneof"'s rendering sets one`,
		},
		{
			name:      "the author and the rendering set one each of a nested pair",
			props:     map[string]any{"inline": "x", "source": map[string]any{"git": "a"}},
			rendering: map[string]any{"source": map[string]any{"oci": "b"}},
			want:      `properties.source: "git" and "oci" are mutually exclusive; the trait merged with capability "oneof"'s rendering sets them together`,
		},
		{
			name:      "the author and the rendering set one each of the top-level pair",
			props:     map[string]any{"inline": "x", "source": map[string]any{"git": "a"}},
			rendering: map[string]any{"url": "y"},
			want:      `properties: "inline" and "url" are mutually exclusive; the trait merged with capability "oneof"'s rendering sets them together`,
		},
		{
			name:    "no binding matches: the nested pair is checked as written",
			props:   map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}},
			unbound: true,
			want:    `properties.source: exactly one of "git" and "oci" is required`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := oneOfTransformer(&oneOfTrait{})
			caps := binding(tc.rendering)
			if tc.unbound {
				caps = map[string]CapabilityBinding{"other": {Rendering: map[string]any{"source": map[string]any{"git": "repo"}}}}
			}
			app := authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: tc.props})
			err := tr.ValidateAuthoredPropertiesWithCapabilities(app, caps)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			wantError(t, err, tc.want)
			if tc.unbound && strings.Contains(err.Error(), "capability") {
				t.Errorf("an unbound trait's refusal must not name a capability: %v", err)
			}
		})
	}
}

// TestExclusive_DoesNotMutateHandlerSchema pins the clone in relaxObjectRequired: a
// handler returning a shared schema map keeps its groups' Required.
func TestExclusive_DoesNotMutateHandlerSchema(t *testing.T) {
	shared := oneOfSchema()
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": schemaComponent{typ: "webservice"}},
		map[string]TraitHandler{"oneof": sharedSchemaTrait{typ: "oneof", schema: shared}},
	)
	app := authoredApp("webservice", map[string]any{"image": "nginx"},
		Trait{Type: "oneof", Properties: map[string]any{"source": map[string]any{"tag": "v1"}}})
	caps := map[string]CapabilityBinding{"oneof": {Rendering: map[string]any{"source": map[string]any{"git": "repo"}}}}
	if err := tr.ValidateAuthoredPropertiesWithCapabilities(app, caps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shared["source"].Exclusive[0].Required {
		t.Error("relaxObjectRequired cleared a group's Required in the handler's own schema map")
	}
}

// TestExclusive_TransformAfterMerge: Transform checks the groups on the merged
// properties at both trait merge sites.
func TestExclusive_TransformAfterMerge(t *testing.T) {
	for _, traitType := range []string{"oneof", "oneof-rule"} {
		t.Run(traitType, func(t *testing.T) {
			caps := func(rendering map[string]any) map[string]CapabilityBinding {
				return map[string]CapabilityBinding{traitType: {Rendering: rendering}}
			}
			app := func(props map[string]any) *Application {
				return storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: traitType, Properties: props}}})
			}

			t.Run("the rendering supplies the nested required key: built", func(t *testing.T) {
				tr := oneOfTransformer(&oneOfTrait{})
				props := map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}}
				if _, err := tr.Transform(app(props), TransformContext{Capabilities: caps(map[string]any{"source": map[string]any{"git": "repo"}})}); err != nil {
					t.Fatalf("Transform: %v", err)
				}
			})

			t.Run("the author and the rendering set one each of the top-level pair: refused", func(t *testing.T) {
				tr := oneOfTransformer(&oneOfTrait{})
				props := map[string]any{"inline": "x", "source": map[string]any{"git": "a"}}
				_, err := tr.Transform(app(props), TransformContext{Capabilities: caps(map[string]any{"url": "y"})})
				want := `properties: "inline" and "url" are mutually exclusive; the trait merged with capability "` + traitType + `"'s rendering sets them together`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})

			t.Run("neither side sets the nested required key: refused", func(t *testing.T) {
				tr := oneOfTransformer(&oneOfTrait{})
				props := map[string]any{"inline": "x", "source": map[string]any{"tag": "v1"}}
				_, err := tr.Transform(app(props), TransformContext{Capabilities: caps(map[string]any{"source": map[string]any{"digest": "sha"}})})
				want := `properties.source: exactly one of "git" and "oci" is required; neither the trait nor capability "` + traitType + `"'s rendering sets one`
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})
		})
	}
}

func TestExclusive_QuotedKeys(t *testing.T) {
	for keys, want := range map[string]string{
		"a":     `"a"`,
		"a,b":   `"a" and "b"`,
		"a,b,c": `"a", "b" and "c"`,
	} {
		if got := quotedKeys(strings.Split(keys, ",")); got != want {
			t.Errorf("quotedKeys(%s) = %s, want %s", keys, got, want)
		}
	}
}

// TestHandlerSchemas_Exclusive: top-level groups are published by position and type
// name, and a set with none keeps its JSON exactly as before the field existed.
func TestHandlerSchemas_Exclusive(t *testing.T) {
	t.Run("none declared: nil, and absent from the JSON", func(t *testing.T) {
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": schemaComponent{typ: "webservice"}},
			map[string]TraitHandler{"pvc": schemaTrait{typ: "pvc"}},
		)
		set := tr.HandlerSchemas()
		if set.Exclusive != nil {
			t.Fatalf("Exclusive = %+v, want nil", set.Exclusive)
		}
		out, err := json.Marshal(set)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(out, &keys); err != nil {
			t.Fatal(err)
		}
		if len(keys) != 3 || keys["Components"] == nil || keys["Traits"] == nil || keys["Policies"] == nil {
			t.Errorf("JSON keys = %v, want exactly Components, Traits and Policies", keys)
		}
		if strings.Contains(string(out), "exclusive") || strings.Contains(string(out), "Exclusive") {
			t.Errorf("JSON carries an exclusive key: %s", out)
		}
	})

	t.Run("declared: published by position", func(t *testing.T) {
		tr := oneOfTransformer(&oneOfTrait{})
		set := tr.HandlerSchemas()
		if set.Exclusive == nil {
			t.Fatal("Exclusive is nil")
		}
		for name, groups := range map[string][]ExclusiveGroup{
			"component oneof":  set.Exclusive.Components["oneof"],
			"trait oneof":      set.Exclusive.Traits["oneof"],
			"trait oneof-rule": set.Exclusive.Traits["oneof-rule"],
		} {
			if len(groups) != 1 || !groups[0].Required || strings.Join(groups[0].Keys, ",") != "inline,url" {
				t.Errorf("%s groups = %+v, want oneOfTop", name, groups)
			}
		}
		if _, ok := set.Exclusive.Components["webservice"]; ok {
			t.Error("a handler declaring no group must not appear")
		}
		if set.Exclusive.Policies != nil {
			t.Errorf("Policies = %v, want nil", set.Exclusive.Policies)
		}
		if got := set.Components["oneof"]["source"].Exclusive; len(got) != 2 {
			t.Errorf("nested groups = %+v, want the schema's two", got)
		}
		out, err := json.Marshal(set.Exclusive)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"Components":{"oneof":[{"keys":["inline","url"],"required":true}]},"Traits":{"oneof":[{"keys":["inline","url"],"required":true}],"oneof-rule":[{"keys":["inline","url"],"required":true}]}}`; string(out) != want {
			t.Errorf("JSON = %s, want %s", out, want)
		}
	})
}

// oneOfDefaultsComponent is oneOfComponent taking "url" from the "store"
// capability as a default; it records the properties it was handed.
type oneOfDefaultsComponent struct {
	oneOfComponent
	got map[string]any
}

func (h *oneOfDefaultsComponent) CapabilityDefaults() (string, []string) {
	return "store", []string{"url"}
}

func (h *oneOfDefaultsComponent) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.got = c.Properties
	return &stubAppConfig{}, nil
}

// TestExclusive_ComponentCapabilityDefaults: a capability default that fills one
// key of a top-level group beside an authored one is refused, naming the
// capability; a default that fills the only key set, or that an authored key
// overrides, passes.
func TestExclusive_ComponentCapabilityDefaults(t *testing.T) {
	caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"url": "https://example.com/x.yaml"}}}
	cases := []struct {
		name    string
		props   map[string]any
		wantErr string
		wantURL any
	}{
		{name: "fill beside an authored key", props: map[string]any{"inline": "x"},
			wantErr: `capability "store" defaults: properties: "inline" and "url" are mutually exclusive`},
		{name: "fill alone", props: map[string]any{}, wantURL: "https://example.com/x.yaml"},
		{name: "authored key wins", props: map[string]any{"url": "https://example.com/mine.yaml"}, wantURL: "https://example.com/mine.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &oneOfDefaultsComponent{oneOfComponent: oneOfComponent{typ: "store"}}
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			app := storeApp(Component{Name: "data", Type: "store", Properties: tc.props})
			if err := tr.ValidateAuthoredProperties(app); err != nil {
				t.Fatalf("ValidateAuthoredProperties: %v", err)
			}
			_, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("TransformWithPolicy error = %v, want it to contain %q", err, tc.wantErr)
				}
				if h.got != nil {
					t.Errorf("handler ran with %v", h.got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if h.got["url"] != tc.wantURL {
				t.Errorf("handler got url %v, want %v", h.got["url"], tc.wantURL)
			}
		})
	}
}

// requiredInGroupComponent is oneOfDefaultsComponent with "inline" declared Required
// inside its inline/url group: a malformed schema.
type requiredInGroupComponent struct{ oneOfDefaultsComponent }

func (h *requiredInGroupComponent) PropertySchema() map[string]PropertySchema {
	s := oneOfSchema()
	s["inline"] = PropertySchema{Type: PropertyTypeString, Required: true}
	return s
}

// requiredInNestedGroupTrait is oneOfTrait with "source.git" declared Required
// inside the source's git/oci group.
type requiredInNestedGroupTrait struct{ oneOfTrait }

func (h *requiredInNestedGroupTrait) PropertySchema() map[string]PropertySchema {
	s := oneOfSchema()
	s["source"].Properties["git"] = PropertySchema{Type: PropertyTypeString, Required: true}
	return s
}

// emptyObjectGroupTrait is oneOfTrait whose "source" declares no properties but a
// git/oci group: both of its keys are undeclared.
type emptyObjectGroupTrait struct{ oneOfTrait }

func (h *emptyObjectGroupTrait) PropertySchema() map[string]PropertySchema {
	s := oneOfSchema()
	s["source"] = PropertySchema{Type: PropertyTypeObject, Exclusive: []ExclusiveGroup{{Keys: []string{"git", "oci"}}}}
	return s
}

// requiredInNestedGroupRule is oneOfRule with requiredInNestedGroupTrait's schema.
type requiredInNestedGroupRule struct{ oneOfRule }

func (requiredInNestedGroupRule) PropertySchema() map[string]PropertySchema {
	return (&requiredInNestedGroupTrait{}).PropertySchema()
}

// scalarItemsGroupTrait is oneOfTrait with a "list" whose string Items declare a
// group: groups on a non-object node.
type scalarItemsGroupTrait struct{ oneOfTrait }

func (h *scalarItemsGroupTrait) PropertySchema() map[string]PropertySchema {
	s := oneOfSchema()
	s["list"] = PropertySchema{Type: PropertyTypeArray, Items: &PropertySchema{
		Type: PropertyTypeString, Exclusive: []ExclusiveGroup{{Keys: []string{"a", "b"}}},
	}}
	return s
}

// unusedGroupSchema is oneOfSchema with a malformed group no value reaches unless the
// document supplies it: on an object "o" when node is "o", else on the objects of
// "list". The group names an undeclared key.
func unusedGroupSchema(node string) map[string]PropertySchema {
	s := oneOfSchema()
	malformed := PropertySchema{
		Type:       PropertyTypeObject,
		Properties: map[string]PropertySchema{"a": {Type: PropertyTypeString}, "b": {Type: PropertyTypeString}},
		Exclusive:  []ExclusiveGroup{{Keys: []string{"a", "missing"}}},
	}
	if node == "o" {
		s["o"] = malformed
	} else {
		s["list"] = PropertySchema{Type: PropertyTypeArray, Items: &malformed}
	}
	return s
}

// TestExclusive_SchemaErrorsOnUnusedNodes: property validation refuses a malformed
// group whatever the document supplies, an object or array it leaves out included,
// on the authored and the emitted path.
func TestExclusive_SchemaErrorsOnUnusedNodes(t *testing.T) {
	for _, tc := range []struct {
		name, node, want string
		props            map[string]any
	}{
		{name: "an omitted object", node: "o", props: map[string]any{"inline": "x"}, want: `properties.o: schema declares exclusive key "missing", which is not a declared property`},
		{name: "a null object", node: "o", props: map[string]any{"inline": "x", "o": nil}, want: `properties.o: schema declares exclusive key "missing", which is not a declared property`},
		{name: "an empty array", node: "list", props: map[string]any{"inline": "x", "list": []any{}}, want: `properties.list[]: schema declares exclusive key "missing", which is not a declared property`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := unusedGroupSchema(tc.node)
			h := &schemaOverrideComponent{oneOfComponent: oneOfComponent{typ: "store"}, schema: schema}
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			props := map[string]any{}
			maps.Copy(props, tc.props)
			wantError(t, tr.ValidateAuthoredProperties(authoredApp("store", props)), tc.want)
			emitted := map[string]any{}
			maps.Copy(emitted, tc.props)
			wantError(t, validateTopLevelProperties(schema, oneOfTop(), emitted, "properties"), tc.want)
		})
	}
}

// schemaOverrideComponent is oneOfComponent declaring schema instead of oneOfSchema.
type schemaOverrideComponent struct {
	oneOfComponent
	schema map[string]PropertySchema
}

func (h *schemaOverrideComponent) PropertySchema() map[string]PropertySchema { return h.schema }

// TestExclusive_SchemaErrorsBeforeCapabilities: a malformed group is reported as the
// schema's error where capabilities fill, relax or merge, before any value is
// checked: a component's capability defaults, a capability-bound trait's relaxed
// nested Required, an object only the rendering supplies, both Transform trait merge
// sites, and array items only the rendering supplies.
func TestExclusive_SchemaErrorsBeforeCapabilities(t *testing.T) {
	t.Run("a component's capability default", func(t *testing.T) {
		h := &requiredInGroupComponent{oneOfDefaultsComponent{oneOfComponent: oneOfComponent{typ: "store"}}}
		tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
		caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"url": 123}}}
		_, _, err := tr.TransformWithPolicy(storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}}), TransformContext{Capabilities: caps})
		want := `capability "store" defaults: properties: schema declares exclusive key "inline" Required; a group's keys are optional, and the group's Required says one is set`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("TransformWithPolicy error = %v, want it to contain %q", err, want)
		}
	})

	caps := map[string]CapabilityBinding{"oneof": {Rendering: map[string]any{"source": map[string]any{"tag": "v1"}}}}
	for _, tc := range []struct {
		name   string
		source map[string]any
	}{
		{name: "a relaxed Required key, beside a value of the wrong type", source: map[string]any{"git": 123}},
		{name: "a relaxed Required key, beside two keys of the group", source: map[string]any{"git": "a", "oci": "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"oneof": &requiredInNestedGroupTrait{}},
			)
			app := authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: map[string]any{"inline": "x", "source": tc.source}})
			want := `properties.source: schema declares exclusive key "git" Required; a group's keys are optional, and the group's Required says one is set`
			wantError(t, tr.ValidateAuthoredPropertiesWithCapabilities(app, caps), want)
		})
	}

	t.Run("an object only the rendering supplies", func(t *testing.T) {
		want := `properties.source: schema declares exclusive key "git", which is not a declared property`
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
			map[string]TraitHandler{"oneof": &emptyObjectGroupTrait{}},
		)
		app := authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: map[string]any{"inline": "x"}})
		rendered := map[string]CapabilityBinding{"oneof": {Rendering: map[string]any{"source": map[string]any{}}}}
		wantError(t, tr.ValidateAuthoredPropertiesWithCapabilities(app, rendered), want)
		// The merged properties' own check, which applyTraits and the lowering
		// fixpoint run, reports it too.
		schema := (&emptyObjectGroupTrait{}).PropertySchema()
		wantError(t, checkNestedRequired(schema, nil, map[string]any{"source": map[string]any{}}, "properties", ""), want)
	})

	// Transform with no prior authored check: the top-level pair the merge sets
	// together is the document's error, and the nested group's shape comes first.
	for _, traitType := range []string{"oneof", "oneof-rule"} {
		t.Run("Transform, "+traitType+", before the merge's top-level pair", func(t *testing.T) {
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{
					"oneof":      &requiredInNestedGroupTrait{},
					"oneof-done": &recordingTraitHandler{typ: "oneof-done"},
				},
			)
			tr.RegisterTraitLowering(requiredInNestedGroupRule{})
			app := storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{
				Type: traitType, Properties: map[string]any{"inline": "x", "source": map[string]any{"git": "a"}},
			}}})
			caps := map[string]CapabilityBinding{traitType: {Rendering: map[string]any{"url": "y"}}}
			_, err := tr.Transform(app, TransformContext{Capabilities: caps})
			want := `properties.source: schema declares exclusive key "git" Required; a group's keys are optional, and the group's Required says one is set`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Transform error = %v, want it to contain %q", err, want)
			}
		})
	}

	t.Run("array items only the rendering supplies", func(t *testing.T) {
		want := `properties.list[]: schema declares exclusive groups on a non-object node; they name keys of an object`
		h := &scalarItemsGroupTrait{}
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
			map[string]TraitHandler{"oneof": h},
		)
		props := map[string]any{"inline": "x"}
		rendered := map[string]CapabilityBinding{"oneof": {Rendering: map[string]any{"list": []any{"x"}}}}
		app := authoredApp("webservice", map[string]any{}, Trait{Type: "oneof", Properties: props})
		wantError(t, tr.ValidateAuthoredPropertiesWithCapabilities(app, rendered), want)
		built := storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "oneof", Properties: props}}})
		if _, err := tr.Transform(built, TransformContext{Capabilities: rendered}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Transform error = %v, want it to contain %q", err, want)
		}
		if h.got != nil {
			t.Errorf("the handler was applied with %v", h.got)
		}
	})
}
