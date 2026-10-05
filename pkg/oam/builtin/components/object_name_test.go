package components_test

import (
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// A kind handler driven directly, outside a transform, is given a component the
// engine resolved no object name for: the object keeps the component name. The
// engine reads `objectName` and removes it before the handler runs; these three
// handlers, handed the property all the same, pass over it, and their own
// schema, which the engine adds the property to, does not declare it. A handler
// that decodes its properties strictly refuses it instead; the walk over every
// kind is pkg/cmd/kurel's TestObjectName_HandlerDrivenDirectly.
func TestKindHandler_DrivenDirectly(t *testing.T) {
	for typ, tc := range map[string]struct {
		handler oam.ComponentHandler
		props   func() map[string]any
	}{
		"configmap": {&components.ConfigMapHandler{}, func() map[string]any { return map[string]any{} }},
		"service": {&components.ServiceHandler{}, func() map[string]any {
			return map[string]any{
				"selector": map[string]any{"app": "x"},
				"ports":    []any{map[string]any{"port": 80}},
			}
		}},
		"serviceaccount": {&components.ServiceAccountHandler{}, func() map[string]any { return map[string]any{} }},
	} {
		t.Run(typ, func(t *testing.T) {
			if _, declares := tc.handler.(oam.ComponentObjectProvider); !declares {
				t.Fatalf("%T declares no object", tc.handler)
			}
			cfg, err := kindConfig(t, tc.handler, typ, "settings", tc.props())
			if err != nil {
				t.Fatal(err)
			}
			// generateOne fails unless the one object is named "settings".
			generateOne(t, cfg, "settings")

			props := tc.props()
			props[oam.ObjectNameProperty] = "other"
			cfg, err = kindConfig(t, tc.handler, typ, "settings", props)
			if err != nil {
				t.Fatalf("with %s: %v", oam.ObjectNameProperty, err)
			}
			generateOne(t, cfg, "settings")

			schema := tc.handler.(oam.PropertySchemaProvider).PropertySchema()
			if _, own := schema[oam.ObjectNameProperty]; own {
				t.Errorf("%T declares %s itself; the engine adds it", tc.handler, oam.ObjectNameProperty)
			}
		})
	}
}
