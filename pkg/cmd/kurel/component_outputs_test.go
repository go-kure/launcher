package kurel

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestOutputs_EveryObjectTypePublishesItsRecord: a registered component type
// that declares its one object (oam.ComponentObjectProvider) publishes it, under
// the key "object", with the kind it declares and the name the build gives the
// object: the component name, or the authored `objectName`
// (go-kure/launcher#1643). A new kind cannot join the registry and publish
// nothing.
func TestOutputs_EveryObjectTypePublishesItsRecord(t *testing.T) {
	tr := newBuiltinTransformer()
	handlers := builtinComponentHandlers()

	// The control: the types this test checks are every registered type that is
	// not listed in noObjectNameTypes. TestObjectName_EveryComponentTypeChooses
	// holds that a type is one or the other, so a count that differs from the
	// registry's means the loop below skipped a type.
	want := len(handlers) - len(noObjectNameTypes)
	checked := 0

	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		provider, declares := handlers[typ].(oam.ComponentObjectProvider)
		if !declares {
			continue
		}
		checked++
		kind, _ := provider.ComponentObject()
		t.Run(typ, func(t *testing.T) {
			for _, tc := range []struct {
				label string
				props map[string]any
				name  string
			}{
				{"component name", map[string]any{}, "widget"},
				{"authored objectName", map[string]any{oam.ObjectNameProperty: "renamed-widget"}, "renamed-widget"},
			} {
				comp := &oam.Component{Name: "widget", Type: typ, Properties: tc.props}
				got, err := tr.ComponentOutputs(comp)
				if err != nil {
					t.Errorf("%s: ComponentOutputs: %v", tc.label, err)
					continue
				}
				if len(got) != 1 || got[0].Key != "object" || got[0].Kind != kind || got[0].Name != tc.name {
					t.Errorf("%s: ComponentOutputs = %v; want one output, key \"object\", kind %s, name %q", tc.label, got, kind, tc.name)
				}
			}
		})
	}
	if checked != want {
		t.Errorf("the test checked %d object types; the registry holds %d handlers and noObjectNameTypes %d, so want %d", checked, len(handlers), len(noObjectNameTypes), want)
	}
}
