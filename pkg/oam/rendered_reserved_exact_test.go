package oam

import (
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"gopkg.in/yaml.v3"
)

// --- go-kure/launcher#612: a rendered reserved value is compared exactly ----------

// typedReservedSchema reserves an integer, an array of integers and an object with an
// integer field, so emission validation rewrites what a rule renders into them
// (normalizeIntegerValue, asArrayValue, asObjectValue) before the next D3 check, and
// an object whose children are all undeclared, which it rewrites nothing below.
func typedReservedSchema() map[string]PropertySchema {
	return map[string]PropertySchema{
		"image": {Type: PropertyTypeString, Description: "Authored freely."},
		"port":  {Type: PropertyTypeInteger, PlatformReserved: true, Description: "Platform-supplied."},
		"codes": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeInteger}, PlatformReserved: true, Description: "Platform-supplied."},
		"limits": {Type: PropertyTypeObject, PlatformReserved: true, Description: "Platform-supplied.", Properties: map[string]PropertySchema{
			"cpu": {Type: PropertyTypeInteger},
		}},
		// An object that leaves every key to AdditionalProperties, so emission
		// validation passes what a rule renders below it through untouched.
		"data": {Type: PropertyTypeObject, PlatformReserved: true, AdditionalProperties: true, Description: "Platform-supplied."},
	}
}

// typedReservedSink is a component handler over typedReservedSchema that keeps the
// properties it was dispatched with.
type typedReservedSink struct{ props map[string]any }

func (*typedReservedSink) CanHandle(t string) bool { return t == "typed-sink" }

func (*typedReservedSink) PropertySchema() map[string]PropertySchema { return typedReservedSchema() }

func (h *typedReservedSink) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	h.props = c.Properties
	return &stubAppConfig{}, nil
}

// typedReservedTrait is typedReservedSink for a trait.
type typedReservedTrait struct{ props map[string]any }

func (*typedReservedTrait) CanHandle(t string) bool { return t == "typed-trait" }

func (*typedReservedTrait) PropertySchema() map[string]PropertySchema { return typedReservedSchema() }

func (h *typedReservedTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	h.props = trait.Properties
	return nil
}

// typedElement drives one element kind — a component or a trait a document rule
// builds — through Transform: write fills the element's properties map and lets the
// test render into it, and run returns Transform's error and the properties that
// reached the element's handler.
type typedElement struct {
	name string
	run  func(t *testing.T, app *Application, write func(props *map[string]any, render func(path string, value any) error, doc *Application) error) (map[string]any, error)
}

var typedElements = []typedElement{
	{"component", func(t *testing.T, app *Application, write func(*map[string]any, func(string, any) error, *Application) error) (map[string]any, error) {
		t.Helper()
		sink := &typedReservedSink{}
		tr := NewTransformer(nil, nil)
		tr.RegisterComponent("typed-sink", sink)
		tr.RegisterDocumentLowering(renderingDocRule{kind: app.Kind, typ: "typed-sink", fill: func(comp *Component, doc *Application, _ LoweringContext) error {
			return write(&comp.Properties, comp.RenderReserved, doc)
		}})
		_, err := tr.Transform(app, TransformContext{})
		return sink.props, err
	}},
	{"trait", func(t *testing.T, app *Application, write func(*map[string]any, func(string, any) error, *Application) error) (map[string]any, error) {
		t.Helper()
		h := &typedReservedTrait{}
		tr := NewTransformer(nil, nil)
		tr.RegisterComponent("typed-sink", &typedReservedSink{})
		tr.RegisterTrait("typed-trait", h)
		tr.RegisterDocumentLowering(traitRenderingDocRule{kind: app.Kind, compType: "typed-sink", traitType: "typed-trait", fill: func(trait *Trait, doc *Application, _ LoweringContext) error {
			return write(&trait.Properties, trait.RenderReserved, doc)
		}})
		_, err := tr.Transform(app, TransformContext{})
		return h.props, err
	}},
}

// policyPortApp is a document whose policy carries an authored port, decoded from
// YAML the way a user's document is.
func policyPortApp(t *testing.T, kind, port string) *Application {
	t.Helper()
	var props map[string]any
	if err := yaml.Unmarshal([]byte("port: "+port+"\n"), &props); err != nil {
		t.Fatalf("decode policy properties: %v", err)
	}
	app := emptyDoc(kind)
	app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "anything", Properties: props}}
	return app
}

func expectTypedReservedRefused(t *testing.T, err error, key string) {
	t.Helper()
	if !stderrors.Is(err, ErrPlatformReserved) {
		t.Fatalf("expected ErrPlatformReserved, got: %v", err)
	}
	if !strings.Contains(err.Error(), key) {
		t.Errorf("expected the error to name %q, got: %v", key, err)
	}
}

// TestTransform_RenderedReservedNumberReplacedByAnotherIsRefused: an authored number
// that a property reader tells apart from the rendered one is refused when the rule
// copies it over the rendered value, even where both print the same in JSON —
// 1000000000000000100 rendered, a policy's 1.0000000000000001e+18 (which IntegerValue
// reads as 1000000000000000128) copied in, and the reverse.
func TestTransform_RenderedReservedNumberReplacedByAnotherIsRefused(t *testing.T) {
	tests := []struct {
		name     string
		rendered any
		authored string
	}{
		{"authored float over a rendered integer", int64(1000000000000000100), "1.0000000000000001e+18"},
		{"authored integer over a rendered float", 1.0000000000000001e+18, "1000000000000000100"},
	}
	for _, el := range typedElements {
		for _, tc := range tests {
			t.Run(el.name+"/"+tc.name, func(t *testing.T) {
				app := policyPortApp(t, "Copying", tc.authored)
				_, err := el.run(t, app, func(props *map[string]any, render func(string, any) error, doc *Application) error {
					if err := render("port", tc.rendered); err != nil {
						return err
					}
					authored := doc.Spec.Policies[0].Properties["port"]
					if got, want := authored, tc.rendered; reflect.DeepEqual(got, want) {
						t.Fatalf("test premise: the authored %#v must differ from the rendered %#v", got, want)
					}
					(*props)["port"] = authored
					return nil
				})
				expectTypedReservedRefused(t, err, "port")
			})
		}
	}
}

// renderedPort is a rule's own named integer type, which emission validation rewrites
// to int.
type renderedPort int32

// TestTransform_RenderedReservedNumberSurvivesValidation: a rendered number emission
// validation normalizes, or leaves as it is, still matches its record, and reaches the
// handler as the same number.
func TestTransform_RenderedReservedNumberSurvivesValidation(t *testing.T) {
	tests := []struct {
		name     string
		rendered any
		want     int64
	}{
		{"large int64", int64(1000000000000000100), 1000000000000000100},
		{"int32", int32(8443), 8443},
		{"named integer type", renderedPort(8443), 8443},
		{"unsigned", uint16(8443), 8443},
		{"integral float", float64(8443), 8443},
	}
	for _, el := range typedElements {
		for _, tc := range tests {
			t.Run(el.name+"/"+tc.name, func(t *testing.T) {
				got, err := el.run(t, emptyDoc("Rendering"), func(_ *map[string]any, render func(string, any) error, _ *Application) error {
					return render("port", tc.rendered)
				})
				if err != nil {
					t.Fatalf("a rendered reserved number must be accepted, got: %v", err)
				}
				if n, ok := IntegerValue(got["port"]); !ok || n != tc.want {
					t.Fatalf("expected port %d at the handler, got %#v", tc.want, got["port"])
				}
			})
		}
	}
}

// TestTransform_RenderedReservedCollectionSurvivesValidation: a rendered typed Go
// collection — a []byte among them, which JSON encodes as a base64 string although
// validation reads it as a list of integers — matches its record after emission
// validation rewrites it to []any or map[string]any.
func TestTransform_RenderedReservedCollectionSurvivesValidation(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		rendered any
		want     any
	}{
		{"byte slice", "codes", []byte{1, 2}, []any{1, 2}},
		{"int32 slice", "codes", []int32{1, 2}, []any{1, 2}},
		{"int array", "codes", [2]int{1, 2}, []any{1, 2}},
		{"typed map", "limits", map[string]int32{"cpu": 2}, map[string]any{"cpu": 2}},
	}
	for _, el := range typedElements {
		for _, tc := range tests {
			t.Run(el.name+"/"+tc.name, func(t *testing.T) {
				got, err := el.run(t, emptyDoc("Rendering"), func(_ *map[string]any, render func(string, any) error, _ *Application) error {
					return render(tc.key, tc.rendered)
				})
				if err != nil {
					t.Fatalf("a rendered reserved collection must be accepted, got: %v", err)
				}
				// The normalized shape, holding the same integers whatever their kind
				// (validation keeps an int32 item as it is).
				if reflect.TypeOf(got[tc.key]) != reflect.TypeOf(tc.want) || !equalPropertyValues(tc.want, got[tc.key]) {
					t.Fatalf("expected %s %#v at the handler, got %#v", tc.key, tc.want, got[tc.key])
				}
			})
		}
	}
}

// TestRenderReserved_NullIntermediateIsAbsent: an object along the path that holds a
// null — untyped, or a typed nil map or slice — is absent, so RenderReserved replaces
// it with a new object rather than writing into it (a typed nil map panicked) or
// refusing it, for a component and a trait alike.
func TestRenderReserved_NullIntermediateIsAbsent(t *testing.T) {
	nulls := map[string]any{
		"untyped nil":                   nil,
		"typed nil map":                 map[string]any(nil),
		"typed nil map of another type": map[string]string(nil),
		"typed nil slice":               []any(nil),
	}
	elements := map[string]func(props map[string]any) (func(string, any) error, func() map[string]any){
		"component": func(props map[string]any) (func(string, any) error, func() map[string]any) {
			c := &Component{Properties: props}
			return c.RenderReserved, func() map[string]any { return c.Properties }
		},
		"trait": func(props map[string]any) (func(string, any) error, func() map[string]any) {
			tr := &Trait{Properties: props}
			return tr.RenderReserved, func() map[string]any { return tr.Properties }
		},
	}
	for elName, newElement := range elements {
		for nullName, null := range nulls {
			t.Run(elName+"/"+nullName, func(t *testing.T) {
				render, props := newElement(map[string]any{"image": "nginx", "tls": null})
				if err := render("tls.secretName", "platform-secret"); err != nil {
					t.Fatalf("RenderReserved: %v", err)
				}
				want := map[string]any{"image": "nginx", "tls": map[string]any{"secretName": "platform-secret"}}
				if got := props(); !reflect.DeepEqual(got, want) {
					t.Fatalf("expected %#v, got %#v", want, got)
				}
			})
		}
	}
}

// TestRenderReserved_NilElementIsAnError: a nil receiver is an error, not a panic.
func TestRenderReserved_NilElementIsAnError(t *testing.T) {
	var comp *Component
	if err := comp.RenderReserved("tls.secretName", "x"); err == nil || !strings.Contains(err.Error(), "nil component") {
		t.Fatalf("expected a nil component to be refused, got: %v", err)
	}
	var trait *Trait
	if err := trait.RenderReserved("tls.secretName", "x"); err == nil || !strings.Contains(err.Error(), "nil trait") {
		t.Fatalf("expected a nil trait to be refused, got: %v", err)
	}
}

// undeclaredChildCases are values a rule renders under a key the reserved "data"
// object leaves to AdditionalProperties, each with an authored YAML value the round-2
// comparison read as the same through a coercion emission validation performs only
// where a schema declares a type — never below an undeclared key, so the handler is
// handed a different value.
var undeclaredChildCases = []struct {
	name     string
	rendered any
	authored string
}{
	{"byte slice against a list of integers", []byte{1, 2}, "[1, 2]"},
	{"int array against a list", [2]int{1, 2}, "[1, 2]"},
	{"named integer against an integer", renderedPort(8443), "8443"},
	{"int64 against an int", int64(8443), "8443"},
	{"named string against a string", renderedMode("platform"), "platform"},
	{"typed map against an object", map[string]int32{"cpu": 2}, "{cpu: 2}"},
}

// policyDataApp is a document whose policy carries an authored data object holding
// payload, decoded from YAML the way a user's document is.
func policyDataApp(t *testing.T, kind, payload string) *Application {
	t.Helper()
	var props map[string]any
	if err := yaml.Unmarshal([]byte("data: {payload: "+payload+"}\n"), &props); err != nil {
		t.Fatalf("decode policy properties: %v", err)
	}
	app := emptyDoc(kind)
	app.Spec.Policies = []ApplicationPolicy{{Name: "p", Type: "anything", Properties: props}}
	return app
}

// TestTransform_RenderedReservedUndeclaredChildReplacedIsRefused: a reserved object
// whose children are undeclared reaches the handler as the rule wrote it, so an
// authored object copied over it is refused unless it is that same value — a
// policy's {payload: [1, 2]} is not a rendered {payload: []byte{1, 2}}, which a
// handler encodes as "AQI=" — for a component and a trait alike.
func TestTransform_RenderedReservedUndeclaredChildReplacedIsRefused(t *testing.T) {
	for _, el := range typedElements {
		for _, tc := range undeclaredChildCases {
			t.Run(el.name+"/"+tc.name, func(t *testing.T) {
				app := policyDataApp(t, "Copying", tc.authored)
				_, err := el.run(t, app, func(props *map[string]any, render func(string, any) error, doc *Application) error {
					if err := render("data", map[string]any{"payload": tc.rendered}); err != nil {
						return err
					}
					(*props)["data"] = doc.Spec.Policies[0].Properties["data"]
					return nil
				})
				expectTypedReservedRefused(t, err, "data")
			})
		}
	}
}

// TestTransform_RenderedReservedUndeclaredChildSurvivesValidation: the same rendered
// objects left as they are match their record, and reach the handler with the
// undeclared child exactly as the rule wrote it.
func TestTransform_RenderedReservedUndeclaredChildSurvivesValidation(t *testing.T) {
	for _, el := range typedElements {
		for _, tc := range undeclaredChildCases {
			t.Run(el.name+"/"+tc.name, func(t *testing.T) {
				got, err := el.run(t, emptyDoc("Rendering"), func(_ *map[string]any, render func(string, any) error, _ *Application) error {
					return render("data", map[string]any{"payload": tc.rendered})
				})
				if err != nil {
					t.Fatalf("a rendered reserved object must be accepted, got: %v", err)
				}
				want := map[string]any{"payload": tc.rendered}
				if !reflect.DeepEqual(got["data"], want) {
					t.Fatalf("expected data %#v at the handler, got %#v", want, got["data"])
				}
			})
		}
	}
}

// TestTransform_RenderedReservedSharedValueIsAccepted: one map a rule renders into two
// reserved keys reaches each as its own copy, so emission validation normalizing it
// under one key ("limits" declares cpu an integer) does not rewrite it under the other
// ("data" leaves cpu undeclared), and both still match their records. When the two
// keys shared the map, validating limits.cpu turned data's renderedPort(2) into int(2)
// and the next check refused the unchanged rule output.
func TestTransform_RenderedReservedSharedValueIsAccepted(t *testing.T) {
	for _, el := range typedElements {
		t.Run(el.name, func(t *testing.T) {
			got, err := el.run(t, emptyDoc("Rendering"), func(_ *map[string]any, render func(string, any) error, _ *Application) error {
				shared := map[string]any{"cpu": renderedPort(2)}
				if err := render("data", shared); err != nil {
					return err
				}
				return render("limits", shared)
			})
			if err != nil {
				t.Fatalf("one value rendered into two reserved keys must be accepted, got: %v", err)
			}
			if want := map[string]any{"cpu": renderedPort(2)}; !reflect.DeepEqual(got["data"], want) {
				t.Fatalf("expected data %#v at the handler, got %#v", want, got["data"])
			}
			if want := map[string]any{"cpu": 2}; !reflect.DeepEqual(got["limits"], want) {
				t.Fatalf("expected limits %#v at the handler, got %#v", want, got["limits"])
			}
		})
	}
}

// TestRenderReserved_WritesADetachedCopy: RenderReserved writes a deep copy of the
// value, so changing the caller's value afterwards, at any depth, changes nothing in
// the properties, for a component and a trait alike.
func TestRenderReserved_WritesADetachedCopy(t *testing.T) {
	elements := map[string]func() (func(string, any) error, func() map[string]any){
		"component": func() (func(string, any) error, func() map[string]any) {
			c := &Component{}
			return c.RenderReserved, func() map[string]any { return c.Properties }
		},
		"trait": func() (func(string, any) error, func() map[string]any) {
			tr := &Trait{}
			return tr.RenderReserved, func() map[string]any { return tr.Properties }
		},
	}
	for name, newElement := range elements {
		t.Run(name, func(t *testing.T) {
			render, props := newElement()
			value := map[string]any{"mode": "platform", "sources": []any{map[string]any{"namespace": "ingress"}}, "ports": []int32{80}}
			if err := render("networkPolicy", value); err != nil {
				t.Fatalf("RenderReserved: %v", err)
			}
			value["mode"] = "open"
			value["sources"].([]any)[0].(map[string]any)["namespace"] = "anywhere"
			value["ports"].([]int32)[0] = 443
			want := map[string]any{"networkPolicy": map[string]any{"mode": "platform", "sources": []any{map[string]any{"namespace": "ingress"}}, "ports": []int32{80}}}
			if got := props(); !reflect.DeepEqual(got, want) {
				t.Fatalf("changing the caller's value changed the properties: %#v, want %#v", got, want)
			}
		})
	}
}
