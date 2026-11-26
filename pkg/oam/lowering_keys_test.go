package oam

import (
	"reflect"
	"testing"
)

// keyedComponentRule is targetComponentRule with a key map.
type keyedComponentRule struct {
	targetComponentRule
	keys map[string][]LoweredKey
}

func (r keyedComponentRule) LoweringKeys() map[string][]LoweredKey { return r.keys }

// A rule that declares a key map publishes it under its component type; a rule
// that declares none is left out, and the result is never nil.
func TestComponentLoweringKeys_PublishesDeclaredMapsOnly(t *testing.T) {
	tr := NewTransformer(nil, nil)
	if got := tr.ComponentLoweringKeys(); got == nil || len(got) != 0 {
		t.Fatalf("ComponentLoweringKeys() with no rule = %#v, want an empty non-nil map", got)
	}

	keys := map[string][]LoweredKey{
		"image": {{Member: "kind-a", Path: "image", Unchanged: true}},
		"port":  {{Member: "kind-a", Path: "ports[0].containerPort"}, {Member: "kind-b", Path: "ports[0].port"}},
		"mode":  {{Member: "kind-a", Path: "mode", When: []LoweredWhen{{Key: "delivery", Values: []string{"", "flux"}}}}},
		"name":  nil,
	}
	tr.RegisterComponentLowering(keyedComponentRule{
		targetComponentRule: targetComponentRule{typ: "role", emit: []string{"kind-a", "kind-b"}},
		keys:                keys,
	})
	tr.RegisterComponentLowering(undeclaredComponentRule{typ: "silent", emit: "kind-a"})

	got := tr.ComponentLoweringKeys()
	if want := map[string]map[string][]LoweredKey{"role": keys}; !reflect.DeepEqual(got, want) {
		t.Errorf("ComponentLoweringKeys() = %#v\nwant %#v", got, want)
	}
}

// The result is the caller's own: writing to it, its conditions included, reaches
// neither the rule's map nor the next caller's result.
func TestComponentLoweringKeys_ResultIsTheCallersOwn(t *testing.T) {
	keys := func() map[string][]LoweredKey {
		return map[string][]LoweredKey{
			"image": {{Member: "kind-a", Path: "image", Unchanged: true, When: []LoweredWhen{{Key: "delivery", Values: []string{"flux"}}}}},
			"name":  nil,
		}
	}
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(keyedComponentRule{
		targetComponentRule: targetComponentRule{typ: "role", emit: []string{"kind-a"}},
		keys:                keys(),
	})

	first := tr.ComponentLoweringKeys()
	first["role"]["image"][0].When[0].Values[0] = "changed"
	first["role"]["image"][0].When[0].Key = "changed"
	first["role"]["image"][0].Path = "changed"
	delete(first["role"], "name")
	first["role"]["added"] = nil

	want := map[string]map[string][]LoweredKey{"role": keys()}
	if got := tr.ComponentLoweringKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("ComponentLoweringKeys() after a caller wrote to an earlier result = %#v\nwant %#v", got, want)
	}
}

// A place applies when all of its conditions hold. An empty string in a
// condition's values matches a property left out or written null, and a key
// matches a property in any letter case.
func TestLoweredKey_LiveFor(t *testing.T) {
	delivery := LoweredWhen{Key: "delivery", Values: []string{"", "flux"}}
	mode := LoweredWhen{Key: "valuesMode", Values: []string{"configMap"}}
	var typedNil *string
	for name, tc := range map[string]struct {
		when  []LoweredWhen
		props map[string]any
		want  bool
	}{
		"no condition":          {nil, map[string]any{"delivery": "template"}, true},
		"key left out":          {[]LoweredWhen{delivery}, nil, true},
		"key null":              {[]LoweredWhen{delivery}, map[string]any{"delivery": nil}, true},
		"key typed null":        {[]LoweredWhen{delivery}, map[string]any{"delivery": typedNil}, true},
		"listed value":          {[]LoweredWhen{delivery}, map[string]any{"delivery": "flux"}, true},
		"other value":           {[]LoweredWhen{delivery}, map[string]any{"delivery": "template"}, false},
		"not a string":          {[]LoweredWhen{delivery}, map[string]any{"delivery": 1}, false},
		"left out, not listed":  {[]LoweredWhen{mode}, nil, false},
		"both hold":             {[]LoweredWhen{delivery, mode}, map[string]any{"valuesMode": "configMap"}, true},
		"only the first holds":  {[]LoweredWhen{delivery, mode}, map[string]any{"delivery": "flux"}, false},
		"only the second holds": {[]LoweredWhen{delivery, mode}, map[string]any{"delivery": "template", "valuesMode": "configMap"}, false},
		"key in another case":   {[]LoweredWhen{delivery}, map[string]any{"Delivery": "template"}, false},
		"another case, listed":  {[]LoweredWhen{mode}, map[string]any{"ValuesMode": "configMap"}, true},
	} {
		if got := (LoweredKey{When: tc.when}).LiveFor(tc.props); got != tc.want {
			t.Errorf("%s: LiveFor(%v) = %v, want %v", name, tc.props, got, tc.want)
		}
	}
}
