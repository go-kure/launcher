package model_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// These tests pin the emit hook (go-kure/launcher#1261): the consumer's TransformContext.EmitHook is
// called once for each component that a model emits, in part order, and for nothing else.

const emitModelParts = `
    - id: first
      name: first
      type: configmap@v1
      properties:
        data:
          k: one
    - id: skipped
      name: skipped
      type: configmap@v1
      when: "${ false }"
      properties: {}
    - id: many
      name: "cfg-${ item }"
      type: configmap@v1
      forEach:
        as: item
        in: "${ ['a', 'b'] }"
      properties:
        data:
          k: "${ item }"
`

// transformWith runs one Transform of comps under hook and returns the Transform error.
func transformWith(tr *oam.Transformer, comps []oam.Component, hook func(oam.ModelEmit) error) error {
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: comps},
	}
	_, err := tr.Transform(app, oam.TransformContext{Namespace: "demo", EmitHook: hook})
	return err
}

// Case A1: the hook sees each emitted component once, in part order, with the model, the part, the
// type as the model wrote it, the name the build gives and the evaluated properties. A part whose
// when is false emits nothing and so gets no call.
func TestRule_EmitHookSeesEachEmittedComponent(t *testing.T) {
	tr, reg := registered(t, modelHeader+emitModelParts)
	var got []oam.ModelEmit
	if err := transformWith(tr, orders(), func(e oam.ModelEmit) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	key := ""
	for _, e := range got {
		key = e.Model
	}
	if want := reg.Rule(key); want == nil {
		t.Fatalf("the hook named the model %q, which is no registered key", key)
	}
	type row struct{ Model, Part, Type, Name string }
	var rows []row
	for _, e := range got {
		rows = append(rows, row{e.Model, e.Part, e.Type, e.Name})
	}
	m := "webapp.acme.example@v1.0.0"
	want := []row{
		{m, "first", "configmap@v1", "orders-first"},
		{m, "many", "configmap@v1", "orders-cfg-a"},
		{m, "many", "configmap@v1", "orders-cfg-b"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("the hook saw %+v, want %+v", rows, want)
	}
	if !reflect.DeepEqual(got[0].Properties, map[string]any{"data": map[string]any{"k": "one"}}) {
		t.Errorf("first properties = %#v", got[0].Properties)
	}
	if !reflect.DeepEqual(got[2].Properties, map[string]any{"data": map[string]any{"k": "b"}}) {
		t.Errorf("third properties = %#v", got[2].Properties)
	}
}

// Case A2: a change the hook makes to the properties it was handed does not reach the build it ran
// in, for the first part or for any component of a forEach part.
func TestRule_EmitHookPropertiesAreACopy(t *testing.T) {
	tr, _ := registered(t, modelHeader+emitModelParts)
	var seen int
	objs, err := objectsWith(t, tr, orders(), nil, func(e oam.ModelEmit) error {
		seen++
		if d, ok := e.Properties["data"].(map[string]any); ok {
			d["k"] = "changed"
		}
		e.Properties["extra"] = true
		return nil
	})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if seen != 3 {
		t.Fatalf("the hook ran %d times, want 3", seen)
	}
	for name, want := range map[string]string{"orders-first": "one", "orders-cfg-a": "a", "orders-cfg-b": "b"} {
		cm, ok := objs[[2]string{"ConfigMap", name}]
		if !ok {
			t.Fatalf("the build made no ConfigMap %s: %v", name, names(objs, "ConfigMap"))
		}
		if got := prop(t, cm, "data", "k"); got != want {
			t.Errorf("%s: the build read data.k = %v after the hook changed its copy, want %s", name, got, want)
		}
	}
}

// Case A3: a hook error fails the build, names the model and the part, and wraps the hook's error.
func TestRule_EmitHookErrorFailsTheBuild(t *testing.T) {
	tr, _ := registered(t, modelHeader+emitModelParts)
	refused := errors.New("secret property refused")
	err := transformWith(tr, orders(), func(e oam.ModelEmit) error {
		if e.Part == "many" {
			return refused
		}
		return nil
	})
	if err == nil {
		t.Fatal("a hook error did not fail the build")
	}
	if !errors.Is(err, refused) {
		t.Errorf("error %q does not wrap the hook's error", err)
	}
	for _, s := range []string{"webapp.acme.example", `part "many"`} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not name %s", err, s)
		}
	}
}

// Case A4: a Go rule never calls the hook, and a nil hook asks nothing.
func TestRule_EmitHookIsForModelPartsOnly(t *testing.T) {
	tr, _ := registered(t, modelHeader+emitModelParts)
	comps := append(orders(),
		oam.Component{Name: "plain", Type: "configmap@v1", Properties: map[string]any{"data": map[string]any{"k": "v"}}})
	var parts []string
	if err := transformWith(tr, comps, func(e oam.ModelEmit) error {
		parts = append(parts, e.Part)
		return nil
	}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("the hook ran for %v, want the three parts of the model and nothing for the plain component", parts)
	}
	if err := transformWith(tr, comps, nil); err != nil {
		t.Fatalf("Transform with no hook: %v", err)
	}
}

// Case A5: a render that fails after some parts built calls the hook for none of them.
func TestRule_EmitHookNotCalledForAFailedRender(t *testing.T) {
	tr, _ := registered(t, modelHeader+emitModelParts+`
    - id: bad
      name: bad
      type: configmap@v1
      properties:
        data:
          k: "${ 1 / 0 }"
`)
	var calls int
	err := transformWith(tr, orders(), func(oam.ModelEmit) error { calls++; return nil })
	if err == nil {
		t.Fatal("a part that divides by zero built")
	}
	if calls != 0 {
		t.Fatalf("the hook ran %d times for a render that failed", calls)
	}
}
