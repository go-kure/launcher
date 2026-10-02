package oam

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// --- A capability rendering keeps its Go types on both merge sites (go-kure/launcher#756) ---

// aboveFloat64Precision is 2^53+1, the first integer a float64 cannot hold: a JSON
// round trip through float64 turns it into 2^53.
const aboveFloat64Precision = int64(1)<<53 + 1

// numericDefaultsHandler takes "replicas" and "big" from the "store" capability and
// records the properties it was handed.
type numericDefaultsHandler struct{ got map[string]any }

func (h *numericDefaultsHandler) CanHandle(t string) bool { return t == "store" }

func (h *numericDefaultsHandler) CapabilityDefaults() (string, []string) {
	return "store", []string{"replicas", "big"}
}

func (h *numericDefaultsHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.got = c.Properties
	return &stubAppConfig{}, nil
}

func numericBinding() map[string]CapabilityBinding {
	return map[string]CapabilityBinding{
		"store": {Rendering: map[string]any{"replicas": 3, "big": aboveFloat64Precision}},
	}
}

// TestCapabilityRendering_KeepsTypes: an int stays an int and an int64 above 2^53
// keeps its value, through the trait merge (dispatch and lowering paths) and the
// component defaults fill alike.
func TestCapabilityRendering_KeepsTypes(t *testing.T) {
	want := map[string]any{"replicas": 3, "big": aboveFloat64Precision}
	sites := map[string]func(t *testing.T) map[string]any{
		"trait dispatch": func(t *testing.T) map[string]any {
			th := &recordingTraitHandler{typ: "store"}
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"store": th},
			)
			app := storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "store", Properties: map[string]any{}}}})
			if _, err := tr.Transform(app, TransformContext{Capabilities: numericBinding()}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			return th.got
		},
		"trait lowering": func(t *testing.T) map[string]any {
			var got map[string]any
			tr := NewTransformer(
				map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
				map[string]TraitHandler{"store-trait-done": &recordingTraitHandler{typ: "store-trait-done"}},
			)
			tr.RegisterTraitLowering(recordingTraitRule{got: &got})
			caps := map[string]CapabilityBinding{"store-trait": numericBinding()["store"]}
			app := storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "store-trait", Properties: map[string]any{}}}})
			if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			return got
		},
		"component defaults": func(t *testing.T) map[string]any {
			h := &numericDefaultsHandler{}
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
			if _, err := tr.Transform(app, TransformContext{Capabilities: numericBinding()}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			return h.got
		},
	}
	for name, run := range sites {
		t.Run(name, func(t *testing.T) {
			got := run(t)
			for k, w := range want {
				if g := got[k]; reflect.TypeOf(g) != reflect.TypeOf(w) || g != w {
					t.Errorf("%s = %v (%T), want %v (%T)", k, g, g, w, w)
				}
			}
		})
	}
}

// TestCapabilityRendering_RefusedAtEntry: a rendering value that is not a property
// value is refused before anything is built, naming the capability and the
// rendering key, even for a binding no document uses. A JSON round trip accepted
// the first two rows and lost the last three, or shared them with the profile.
func TestCapabilityRendering_RefusedAtEntry(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nested null", value: map[string]any{"cpu": nil}, want: "null"},
		{name: "nested typed nil", value: []any{map[string]any(nil)}, want: "null"},
		{name: "NaN", value: math.NaN(), want: "not a finite number"},
		{name: "+Inf", value: math.Inf(1), want: "not a finite number"},
		{name: "struct", value: struct{ CPU string }{CPU: "1"}, want: "not a property value"},
		{name: "chan", value: make(chan int), want: "not a property value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &numericDefaultsHandler{}
			tr := NewTransformer(map[string]ComponentHandler{"store": h}, nil)
			caps := numericBinding()
			caps["unused"] = CapabilityBinding{Rendering: map[string]any{"limits": tc.value}}
			app := storeApp(Component{Name: "data", Type: "store", Properties: map[string]any{}})
			_, err := tr.Transform(app, TransformContext{Capabilities: caps})
			if err == nil {
				t.Fatal("Transform succeeded, want the rendering refused")
			}
			if !strings.Contains(err.Error(), `capability "unused" rendering key "limits"`) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q, want one naming the capability, the key and %q", err, tc.want)
			}
			if h.got != nil {
				t.Errorf("handler ran with %v", h.got)
			}
		})
	}
}

// TestCapabilityRendering_TopLevelNullKept: a null rendering value is not refused,
// and the trait merge carries it as before.
func TestCapabilityRendering_TopLevelNullKept(t *testing.T) {
	th := &recordingTraitHandler{typ: "store"}
	tr := NewTransformer(
		map[string]ComponentHandler{"webservice": &pipelineComponentHandler{typ: "webservice"}},
		map[string]TraitHandler{"store": th},
	)
	caps := map[string]CapabilityBinding{"store": {Rendering: map[string]any{"class": nil, "tier": "gold"}}}
	app := storeApp(Component{Name: "web", Type: "webservice", Traits: []Trait{{Type: "store", Properties: map[string]any{}}}})
	if _, err := tr.Transform(app, TransformContext{Capabilities: caps}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if want := map[string]any{"class": nil, "tier": "gold"}; !reflect.DeepEqual(th.got, want) {
		t.Errorf("handler got %v, want %v", th.got, want)
	}
}
