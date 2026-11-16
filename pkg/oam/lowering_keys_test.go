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

// The result is the caller's own: writing to it reaches neither the rule's map
// nor the next caller's result.
func TestComponentLoweringKeys_ResultIsTheCallersOwn(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(keyedComponentRule{
		targetComponentRule: targetComponentRule{typ: "role", emit: []string{"kind-a"}},
		keys: map[string][]LoweredKey{
			"image": {{Member: "kind-a", Path: "image", Unchanged: true}},
			"name":  nil,
		},
	})

	first := tr.ComponentLoweringKeys()
	first["role"]["image"][0].Path = "changed"
	delete(first["role"], "name")
	first["role"]["added"] = nil

	want := map[string]map[string][]LoweredKey{"role": {
		"image": {{Member: "kind-a", Path: "image", Unchanged: true}},
		"name":  nil,
	}}
	if got := tr.ComponentLoweringKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("ComponentLoweringKeys() after a caller wrote to an earlier result = %#v\nwant %#v", got, want)
	}
}
