package oam

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- Capability defaults on components, and null over a rendering key (go-kure/launcher#742) ---

// defaultsComponentHandler takes "class" from the "store" capability and records
// the properties it was handed. Its schema leaves "class" untyped, so any property
// value passes validateCapabilityFill.
type defaultsComponentHandler struct{ got map[string]any }

func (h *defaultsComponentHandler) CanHandle(t string) bool { return t == "store" }

func (h *defaultsComponentHandler) CapabilityDefaults() (string, []string) {
	return "store", []string{"class"}
}

func (h *defaultsComponentHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"size": {Type: PropertyTypeString}, "class": {}}
}

func (h *defaultsComponentHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.got = c.Properties
	return &stubAppConfig{}, nil
}

// storeLoweringRule declares a schema, so the "store" component it emits counts as
// synthesized.
type storeLoweringRule struct{ props map[string]any }

func (storeLoweringRule) ComponentType() string { return "store-rule" }

func (storeLoweringRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (r storeLoweringRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: comp.Name, Type: "store", Properties: r.props}}}, nil
}

// storeApp is a terminal Application holding comp, as the lowering pass requires.
func storeApp(comp Component) *Application {
	app := makeApp("myapp", comp)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind
	return app
}

func storeBinding() map[string]CapabilityBinding {
	return map[string]CapabilityBinding{
		"store": {Rendering: map[string]any{"class": "platform", "extra": "never-copied"}},
	}
}

func TestComponentCapabilityDefaults(t *testing.T) {
	cases := []struct {
		name         string
		props        map[string]any
		capabilities map[string]CapabilityBinding
		want         map[string]any
		wantConsumed []string
	}{
		{name: "unset takes the rendering", props: map[string]any{"size": "1Gi"}, capabilities: storeBinding(),
			want: map[string]any{"size": "1Gi", "class": "platform"}, wantConsumed: []string{"store"}},
		{name: "nil properties take the rendering", props: nil, capabilities: storeBinding(),
			want: map[string]any{"class": "platform"}, wantConsumed: []string{"store"}},
		{name: "null takes the rendering", props: map[string]any{"class": nil}, capabilities: storeBinding(),
			want: map[string]any{"class": "platform"}, wantConsumed: []string{"store"}},
		{name: "typed nil takes the rendering", props: map[string]any{"class": map[string]any(nil)}, capabilities: storeBinding(),
			want: map[string]any{"class": "platform"}, wantConsumed: []string{"store"}},
		{name: "authored value wins", props: map[string]any{"class": "mine"}, capabilities: storeBinding(),
			want: map[string]any{"class": "mine"}, wantConsumed: []string{"store"}},
		{name: "authored empty string wins", props: map[string]any{"class": ""}, capabilities: storeBinding(),
			want: map[string]any{"class": ""}, wantConsumed: []string{"store"}},
		{name: "no binding leaves the properties", props: map[string]any{"size": "1Gi"}, capabilities: nil,
			want: map[string]any{"size": "1Gi"}, wantConsumed: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &defaultsComponentHandler{}
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			authored := tc.props
			var before map[string]any
			if authored != nil {
				before = map[string]any{}
				for k, v := range authored {
					before[k] = v
				}
			}
			app := storeApp(Component{Name: "data", Type: "store", Properties: authored})
			_, result, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: tc.capabilities})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if !reflect.DeepEqual(h.got, tc.want) {
				t.Errorf("handler got %v, want %v", h.got, tc.want)
			}
			if !slices.Equal(result.ConsumedCapabilities, tc.wantConsumed) {
				t.Errorf("ConsumedCapabilities = %v, want %v", result.ConsumedCapabilities, tc.wantConsumed)
			}
			if authored != nil && !reflect.DeepEqual(authored, before) {
				t.Errorf("the authored properties were mutated: %v, was %v", authored, before)
			}
		})
	}
}

// TestComponentCapabilityDefaults_CopiesValues: a filled value is a copy, so a
// handler changing it leaves the profile's rendering as it was.
func TestComponentCapabilityDefaults_CopiesValues(t *testing.T) {
	h := &defaultsComponentHandler{}
	tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
	caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"class": map[string]any{"tier": "fast"}}}}
	app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
	if _, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps}); err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	filled, ok := h.got["class"].(map[string]any)
	if !ok {
		t.Fatalf("handler got class %v, want a map", h.got["class"])
	}
	filled["tier"] = "changed"
	if got := caps["store"].Rendering["class"].(map[string]any)["tier"]; got != "fast" {
		t.Errorf("the profile's rendering changed to %v through the filled value", got)
	}
}

// TestComponentCapabilityDefaults_UncopyableValueRefused: a filled value that cannot
// be copied fails the transform rather than being shared with the profile. Through
// TransformWithPolicy the binding is refused before any component is built
// (go-kure/launcher#756); called directly, the fill refuses it itself.
func TestComponentCapabilityDefaults_UncopyableValueRefused(t *testing.T) {
	h := &defaultsComponentHandler{}
	tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
	caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"class": make(chan int)}}}
	app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
	_, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps})
	if err == nil {
		t.Fatal("TransformWithPolicy succeeded, want an error for an uncopyable rendering value")
	}
	if !strings.Contains(err.Error(), `capability "store" rendering key "class"`) {
		t.Errorf("error %q does not name the capability and the rendering key", err)
	}
	if h.got != nil {
		t.Errorf("handler ran with %v", h.got)
	}

	_, err = tr.applyComponentCapabilityDefaults(h, map[string]any{}, TransformContext{Capabilities: caps})
	if err == nil || !strings.Contains(err.Error(), `capability "store" defaults: rendering key "class"`) {
		t.Errorf("applyComponentCapabilityDefaults error = %v, want one naming the capability and the key", err)
	}
}

// TestComponentCapabilityDefaults_SynthesizedSkipped: a component a lowering rule
// synthesized keeps the rule's own output, as a sealed trait does.
func TestComponentCapabilityDefaults_SynthesizedSkipped(t *testing.T) {
	h := &defaultsComponentHandler{}
	tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
	tr.RegisterComponentLowering(storeLoweringRule{props: map[string]any{"size": "1Gi"}})
	app := storeApp(Component{Name: "data", Type: "store-rule", Properties: map[string]any{}})
	_, result, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: storeBinding()})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if want := map[string]any{"size": "1Gi"}; !reflect.DeepEqual(h.got, want) {
		t.Errorf("handler got %v, want the rule's output %v unchanged", h.got, want)
	}
	if len(result.ConsumedCapabilities) != 0 {
		t.Errorf("ConsumedCapabilities = %v, want none", result.ConsumedCapabilities)
	}
}

// keyedDefaultsHandler is a "store" component taking keys from the capability key,
// with no schema of its own.
type keyedDefaultsHandler struct {
	key  string
	keys []string
	got  map[string]any
}

func (h *keyedDefaultsHandler) CanHandle(t string) bool { return t == "store" }

func (h *keyedDefaultsHandler) CapabilityDefaults() (string, []string) { return h.key, h.keys }

func (h *keyedDefaultsHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.got = c.Properties
	return &stubAppConfig{}, nil
}

// schemaDefaultsHandler is keyedDefaultsHandler declaring "class" as a string.
type schemaDefaultsHandler struct{ keyedDefaultsHandler }

func (h *schemaDefaultsHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"class": {Type: PropertyTypeString}}
}

// TestComponentCapabilityDefaults_ValidatedAgainstComponent: each filled value is
// checked against the component's own schema, whatever the trait side accepted, and
// a fill nothing can validate is refused (go-kure/launcher#751).
func TestComponentCapabilityDefaults_ValidatedAgainstComponent(t *testing.T) {
	cases := []struct {
		name      string
		handler   ComponentHandler
		trait     bool // register a "store" trait handler that accepts anything
		rule      bool // register the "store-trait" trait lowering rule
		vad       bool // the registered handler or rule implements ValidateAndApplyDefaults
		def       bool // load a CapabilityDefinition for "store"
		builtin   bool // register the "store" trait handler as built in
		rendering map[string]any
		want      map[string]any
		wantErr   []string
	}{
		{name: "component schema refuses what the trait accepts",
			handler: &schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}}, trait: true,
			rendering: map[string]any{"class": 5},
			wantErr:   []string{`component "data": capability "store" defaults: properties.class: expected string, got int`}},
		{name: "component schema refuses with no trait handler",
			handler:   &schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}},
			rendering: map[string]any{"class": true},
			wantErr:   []string{`component "data"`, `capability "store" defaults`, "properties.class: expected string"}},
		{name: "component schema alone accepts",
			handler:   &schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}},
			rendering: map[string]any{"class": "fast"},
			want:      map[string]any{"class": "fast"}},
		{name: "key the component schema does not declare",
			handler:   &schemaDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class", "tier"}}},
			rendering: map[string]any{"class": "fast", "tier": "gold"},
			wantErr:   []string{`component "data"`, `capability "store" defaults: rendering key "tier" is not a property the component declares`}},
		{name: "no schema and nothing registered for the type",
			handler:   &keyedDefaultsHandler{key: "store", keys: []string{"class"}},
			rendering: map[string]any{"class": "fast"},
			wantErr:   []string{`component "data"`, `capability "store" defaults: nothing validates rendering keys ["class"]`, "implement PropertySchemaProvider"}},
		// A registered trait side counts only when it validates the rendering
		// (go-kure/launcher#772).
		{name: "no schema, trait handler that does not validate",
			handler: &keyedDefaultsHandler{key: "store", keys: []string{"class"}}, trait: true,
			rendering: map[string]any{"class": "fast"},
			wantErr:   []string{`component "data"`, `capability "store" defaults: nothing validates rendering keys ["class"]`, `no trait handler or trait lowering rule for type "store" validates the rendering`}},
		{name: "no schema, trait lowering rule that does not validate",
			handler: &keyedDefaultsHandler{key: "store-trait", keys: []string{"class"}}, rule: true,
			rendering: map[string]any{"class": "fast"},
			wantErr:   []string{`component "data"`, `capability "store-trait" defaults: nothing validates rendering keys ["class"]`}},
		{name: "no schema, trait handler implementing ValidateAndApplyDefaults",
			handler: &keyedDefaultsHandler{key: "store", keys: []string{"class"}}, trait: true, vad: true,
			rendering: map[string]any{"class": "fast"},
			want:      map[string]any{"class": "fast"}},
		{name: "no schema, trait lowering rule implementing ValidateAndApplyDefaults",
			handler: &keyedDefaultsHandler{key: "store-trait", keys: []string{"class"}}, rule: true, vad: true,
			rendering: map[string]any{"class": "fast"},
			want:      map[string]any{"class": "fast"}},
		{name: "no schema, custom trait type with a CapabilityDefinition",
			handler: &keyedDefaultsHandler{key: "store", keys: []string{"class"}}, trait: true, def: true,
			rendering: map[string]any{"class": "fast"},
			want:      map[string]any{"class": "fast"}},
		{name: "no schema, built-in trait type with a CapabilityDefinition, which never applies",
			handler: &keyedDefaultsHandler{key: "store", keys: []string{"class"}}, trait: true, def: true, builtin: true,
			rendering: map[string]any{"class": "fast"},
			wantErr:   []string{`component "data"`, `capability "store" defaults: nothing validates rendering keys ["class"]`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"store": tc.handler}, nil)
			if tc.trait {
				var h TraitHandler = &recordingTraitHandler{typ: "store"}
				if tc.vad {
					h = &validatingTraitHandler{recordingTraitHandler{typ: "store"}}
				}
				if tc.builtin {
					tr.RegisterBuiltinTrait("store", h)
				} else {
					tr.RegisterTrait("store", h)
				}
			}
			if tc.rule {
				var got map[string]any
				var r TraitLoweringRule = recordingTraitRule{got: &got}
				if tc.vad {
					r = validatingTraitRule{recordingTraitRule{got: &got}}
				}
				tr.RegisterTraitLowering(r)
			}
			if tc.def {
				tr.SetCapabilityDefs(map[string]*CapabilityDefinition{"store": {Metadata: Metadata{Name: "store"}}})
			}
			key, _ := tc.handler.(ComponentCapabilityDefaults).CapabilityDefaults()
			caps := map[string]CapabilityBinding{key: {Rendering: tc.rendering}}
			app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
			_, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps})
			var got map[string]any
			switch h := tc.handler.(type) {
			case *schemaDefaultsHandler:
				got = h.got
			case *keyedDefaultsHandler:
				got = h.got
			}
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("TransformWithPolicy succeeded with %v, want a refusal", got)
				}
				for _, w := range tc.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Errorf("error %q does not contain %q", err, w)
					}
				}
				if got != nil {
					t.Errorf("handler ran with %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("handler got %v, want %v", got, tc.want)
			}
		})
	}
}

// objectDefaultsHandler is keyedDefaultsHandler declaring "class" as an open object.
type objectDefaultsHandler struct{ keyedDefaultsHandler }

func (h *objectDefaultsHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{"class": {Type: PropertyTypeObject, AdditionalProperties: true}}
}

// TestComponentCapabilityDefaults_NormalizedByComponentSchema: the copy keeps the
// rendering's Go type, and the component schema check then normalizes the fill as it
// normalizes any validated property, so a map[string]string under an object property
// reaches the handler as map[string]any; the profile's own value is left as it was
// (go-kure/launcher#751).
func TestComponentCapabilityDefaults_NormalizedByComponentSchema(t *testing.T) {
	h := &objectDefaultsHandler{keyedDefaultsHandler{key: "store", keys: []string{"class"}}}
	tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
	rendering := map[string]any{"class": map[string]string{"tier": "fast"}}
	caps := map[string]CapabilityBinding{"store": {Rendering: rendering}}
	app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
	if _, _, err := tr.TransformWithPolicy(app, TransformContext{Capabilities: caps}); err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if want := map[string]any{"class": map[string]any{"tier": "fast"}}; !reflect.DeepEqual(h.got, want) {
		t.Errorf("handler got %#v, want %#v", h.got, want)
	}
	if want := map[string]any{"class": map[string]string{"tier": "fast"}}; !reflect.DeepEqual(rendering, want) {
		t.Errorf("profile rendering = %#v, want it unchanged %#v", rendering, want)
	}
}

// recordingTraitHandler records the properties a dispatched trait of its type was
// handed.
type recordingTraitHandler struct {
	typ string
	got map[string]any
}

func (h *recordingTraitHandler) CanHandle(t string) bool { return t == h.typ }

func (h *recordingTraitHandler) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	h.got = trait.Properties
	return nil
}

// recordingTraitRule records the properties a lowered trait was handed, and emits a
// store-trait-done trait, since a rule may not emit nothing.
type recordingTraitRule struct{ got *map[string]any }

func (recordingTraitRule) TraitType() string { return "store-trait" }

func (r recordingTraitRule) LowerTrait(trait *Trait, _ LoweringContext) (LoweringResult, error) {
	*r.got = trait.Properties
	return LoweringResult{Traits: []Trait{{Type: "store-trait-done", Properties: map[string]any{}}}}, nil
}

// validatingTraitHandler and validatingTraitRule are the recording handler and rule
// implementing ValidateAndApplyDefaults, so EvaluateProfile validates their
// renderings.
type validatingTraitHandler struct{ recordingTraitHandler }

func (validatingTraitHandler) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}

type validatingTraitRule struct{ recordingTraitRule }

func (validatingTraitRule) ValidateAndApplyDefaults(r map[string]any) (map[string]any, error) {
	return r, nil
}

// TestCapabilityMerge_NullIsAbsent: an authored null does not displace a rendering
// value on either trait merge site, the dispatch path (applyTraits) and the
// lowering path (lowerDocumentBody). A null under a key the rendering lacks stays.
func TestCapabilityMerge_NullIsAbsent(t *testing.T) {
	caps := map[string]CapabilityBinding{"store-trait": {Rendering: map[string]any{"class": "platform"}}}
	// "tier" is a typed nil, which isNullValue reads as null too.
	caps["store-trait"].Rendering["tier"] = "gold"
	authored := func() map[string]any {
		return map[string]any{"class": nil, "tier": []any(nil), "other": nil, "name": "x"}
	}
	want := map[string]any{"class": "platform", "tier": "gold", "other": nil, "name": "x"}

	t.Run("resolveCapability", func(t *testing.T) {
		got, key, matched := resolveCapability(Trait{Type: "store-trait", Properties: authored()}, caps)
		if !matched || key != "store-trait" {
			t.Fatalf("resolveCapability matched=%v key=%q", matched, key)
		}
		if !reflect.DeepEqual(got.Properties, want) {
			t.Errorf("merged %v, want %v", got.Properties, want)
		}
	})

	t.Run("dispatch path", func(t *testing.T) {
		th := &recordingTraitHandler{typ: "store-trait"}
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
			map[string]TraitHandler{"store-trait": th},
		)
		app := storeApp(Component{Name: "web", Type: "webservice",
			Traits: []Trait{{Type: "store-trait", Properties: authored()}}})
		if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if !reflect.DeepEqual(th.got, want) {
			t.Errorf("handler got %v, want %v", th.got, want)
		}
	})

	t.Run("lowering path", func(t *testing.T) {
		var got map[string]any
		tr := NewTransformer(
			map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
			map[string]TraitHandler{"store-trait-done": &recordingTraitHandler{typ: "store-trait-done"}},
		)
		tr.RegisterTraitLowering(recordingTraitRule{got: &got})
		app := storeApp(Component{Name: "web", Type: "webservice",
			Traits: []Trait{{Type: "store-trait", Properties: authored()}}})
		if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("rule got %v, want %v", got, want)
		}
	})
}

// sharedRequestsAndLimits is a rendering whose requests and limits are one map, as a
// rendering built in Go can be; the copy keeps them one map.
func sharedRequestsAndLimits() map[string]any {
	shared := map[string]any{"cpu": 1}
	return map[string]any{"resources": map[string]any{"requests": shared, "limits": shared}}
}

// TestCapabilityMerge_Nested: an authored nested key overrides only that key and the
// rendering's sibling keys are kept, on resolveCapability and both trait merge sites
// (go-kure/launcher#750). A null stays absent at any depth; a list, a value of
// another kind, and a rendered object of another Go type are replaced whole.
func TestCapabilityMerge_Nested(t *testing.T) {
	limits := func() map[string]any {
		return map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": 1, "memory": "1Gi"}}}
	}
	cases := []struct {
		name      string
		rendering func() map[string]any
		authored  func() map[string]any
		want      map[string]any
	}{
		{name: "nested override keeps siblings", rendering: limits,
			authored: func() map[string]any {
				return map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": 2}}}
			},
			want: map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": 2, "memory": "1Gi"}}}},
		{name: "nested null keeps the rendered value", rendering: limits,
			authored: func() map[string]any {
				return map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": nil, "memory": []any(nil)}}}
			},
			want: limits()},
		{name: "nested null under a key the rendering lacks stays", rendering: limits,
			authored: func() map[string]any {
				return map[string]any{"resources": map[string]any{"limits": map[string]any{"pods": nil}}}
			},
			want: map[string]any{"resources": map[string]any{"limits": map[string]any{"cpu": 1, "memory": "1Gi", "pods": nil}}}},
		{name: "empty authored object keeps the rendered one", rendering: limits,
			authored: func() map[string]any { return map[string]any{"resources": map[string]any{}} },
			want:     limits()},
		{name: "list replaced whole",
			rendering: func() map[string]any { return map[string]any{"hosts": []any{"a", "b"}} },
			authored:  func() map[string]any { return map[string]any{"hosts": []any{"c"}} },
			want:      map[string]any{"hosts": []any{"c"}}},
		{name: "scalar over object replaced whole", rendering: limits,
			authored: func() map[string]any { return map[string]any{"resources": "none"} },
			want:     map[string]any{"resources": "none"}},
		{name: "object over scalar replaced whole",
			rendering: func() map[string]any { return map[string]any{"mode": "x"} },
			authored:  func() map[string]any { return map[string]any{"mode": map[string]any{"a": "1"}} },
			want:      map[string]any{"mode": map[string]any{"a": "1"}}},
		{name: "object over a typed nil rendered map replaced whole",
			rendering: func() map[string]any { return map[string]any{"resources": map[string]any(nil)} },
			authored:  func() map[string]any { return map[string]any{"resources": map[string]any{"cpu": 2}} },
			want:      map[string]any{"resources": map[string]any{"cpu": 2}}},
		{name: "override of a shared rendered object leaves its alias",
			rendering: sharedRequestsAndLimits,
			authored: func() map[string]any {
				return map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": 2}}}
			},
			want: map[string]any{"resources": map[string]any{
				"requests": map[string]any{"cpu": 2}, "limits": map[string]any{"cpu": 1}}}},
		{name: "distinct overrides of a shared rendered object",
			rendering: sharedRequestsAndLimits,
			authored: func() map[string]any {
				return map[string]any{"resources": map[string]any{
					"requests": map[string]any{"cpu": 2}, "limits": map[string]any{"cpu": 3}}}
			},
			want: map[string]any{"resources": map[string]any{
				"requests": map[string]any{"cpu": 2}, "limits": map[string]any{"cpu": 3}}}},
		{name: "object over a typed rendered map replaced whole",
			rendering: func() map[string]any { return map[string]any{"labels": map[string]string{"a": "1", "b": "2"}} },
			authored:  func() map[string]any { return map[string]any{"labels": map[string]any{"a": "3"}} },
			want:      map[string]any{"labels": map[string]any{"a": "3"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := map[string]CapabilityBinding{"store-trait": {Rendering: tc.rendering()}}

			authored := tc.authored()
			got, _, matched := resolveCapability(Trait{Type: "store-trait", Properties: authored}, caps)
			if !matched || !reflect.DeepEqual(got.Properties, tc.want) {
				t.Errorf("resolveCapability merged %v (matched %v), want %v", got.Properties, matched, tc.want)
			}
			if !reflect.DeepEqual(authored, tc.authored()) {
				t.Errorf("the authored properties were mutated: %v", authored)
			}
			if !reflect.DeepEqual(caps["store-trait"].Rendering, tc.rendering()) {
				t.Errorf("the profile's rendering was mutated: %v", caps["store-trait"].Rendering)
			}

			th := &recordingTraitHandler{typ: "store-trait"}
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"store-trait": th},
			)
			app := storeApp(Component{Name: "web", Type: "webservice",
				Traits: []Trait{{Type: "store-trait", Properties: tc.authored()}}})
			if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
				t.Fatalf("Transform (dispatch): %v", err)
			}
			if !reflect.DeepEqual(th.got, tc.want) {
				t.Errorf("dispatch: handler got %v, want %v", th.got, tc.want)
			}

			var lowered map[string]any
			tr = NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"store-trait-done": &recordingTraitHandler{typ: "store-trait-done"}},
			)
			tr.RegisterTraitLowering(recordingTraitRule{got: &lowered})
			app = storeApp(Component{Name: "web", Type: "webservice",
				Traits: []Trait{{Type: "store-trait", Properties: tc.authored()}}})
			if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
				t.Fatalf("Transform (lowering): %v", err)
			}
			if !reflect.DeepEqual(lowered, tc.want) {
				t.Errorf("lowering: rule got %v, want %v", lowered, tc.want)
			}
		})
	}
}
