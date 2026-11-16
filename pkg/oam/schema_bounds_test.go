package oam

import (
	"reflect"
	"testing"
)

// boundsFixture is a schema with a leaf, a nested object, an array of declared
// items and an open array, built fresh for each call.
func boundsFixture() map[string]PropertySchema {
	return map[string]PropertySchema{
		"image": {Type: PropertyTypeString},
		"resources": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"requests": {Type: PropertyTypeObject, Properties: map[string]PropertySchema{
				"cpu":    {Type: PropertyTypeString},
				"memory": {Type: PropertyTypeString},
			}},
		}},
		"volumes": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject, Properties: map[string]PropertySchema{
			"type": {Type: PropertyTypeString},
		}}},
		"containers": {Type: PropertyTypeArray, Items: &PropertySchema{Type: PropertyTypeObject, AdditionalProperties: true}},
	}
}

func TestDeclareBounds(t *testing.T) {
	in := boundsFixture()
	got := DeclareBounds(in, map[string][]RefusalClass{
		"image": {RefusalRegistry},
		// Given out of order; published in RefusalClasses order.
		"resources.requests.cpu":    {RefusalResourceDefaultLimit, RefusalResourceMaximum},
		"resources.requests.memory": {RefusalResourceMaximum},
		"volumes[].type":            {RefusalHostPath},
		"containers":                {RefusalRegistry, RefusalPrivileged, RefusalRegistry},
	})

	want := boundsFixture()
	leaf := func(classes ...RefusalClass) PropertySchema {
		return PropertySchema{Type: PropertyTypeString, BoundedBy: classes}
	}
	want["image"] = leaf(RefusalRegistry)
	want["resources"].Properties["requests"].Properties["cpu"] = leaf(RefusalResourceMaximum, RefusalResourceDefaultLimit)
	want["resources"].Properties["requests"].Properties["memory"] = leaf(RefusalResourceMaximum)
	want["volumes"].Items.Properties["type"] = leaf(RefusalHostPath)
	open := want["containers"]
	open.BoundedBy = []RefusalClass{RefusalPrivileged, RefusalRegistry}
	want["containers"] = open

	if !reflect.DeepEqual(got, want) {
		t.Errorf("DeclareBounds:\n got %+v\nwant %+v", got, want)
	}
	// The schema handed in is another declaration's: it is left as it was, at
	// every depth.
	if !reflect.DeepEqual(in, boundsFixture()) {
		t.Errorf("DeclareBounds changed the schema it was given: %+v", in)
	}
}

// A bound already on a node is kept; the declared classes join it.
func TestDeclareBounds_JoinsADeclaredBound(t *testing.T) {
	in := map[string]PropertySchema{"image": {Type: PropertyTypeString, BoundedBy: []RefusalClass{RefusalRegistry}}}
	got := DeclareBounds(in, map[string][]RefusalClass{"image": {RefusalHostPath}})
	if want := []RefusalClass{RefusalHostPath, RefusalRegistry}; !reflect.DeepEqual(got["image"].BoundedBy, want) {
		t.Errorf("BoundedBy = %v, want %v", got["image"].BoundedBy, want)
	}
}

// A path the schema does not declare is declared nowhere. The guard over the
// registered schemas reports the bound as missing; nothing here invents a node.
func TestDeclareBounds_UnknownPath(t *testing.T) {
	for _, path := range []string{"nope", "image.part", "image[]", "resources.nope", "resources[].requests", "volumes.type", "volumes[].nope", "containers[].image"} {
		got := DeclareBounds(boundsFixture(), map[string][]RefusalClass{path: {RefusalRegistry}})
		if !reflect.DeepEqual(got, boundsFixture()) {
			t.Errorf("path %q: the schema changed: %+v", path, got)
		}
	}
}

// A class outside RefusalClasses is kept, after the library's own, so that the
// check on the published schemas sees it.
func TestDeclareBounds_UnknownClassIsKept(t *testing.T) {
	got := DeclareBounds(boundsFixture(), map[string][]RefusalClass{"image": {"zz-own", RefusalRegistry, "aa-own"}})
	if want := []RefusalClass{RefusalRegistry, "aa-own", "zz-own"}; !reflect.DeepEqual(got["image"].BoundedBy, want) {
		t.Errorf("BoundedBy = %v, want %v", got["image"].BoundedBy, want)
	}
}
