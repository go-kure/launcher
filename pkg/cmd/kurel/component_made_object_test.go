package kurel

import (
	"maps"
	"slices"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// TestMadeObject_OnlyCertificateAndExternalSecret: of the built-in component
// types, only certificate and externalsecret report an object their object's
// controller makes (the engine's componentObjectMaker,
// go-kure/launcher#1086); every other type's config, built from its fixture,
// does not implement it or reports none. A type added with a controller that
// makes an object by name is caught here.
func TestMadeObject_OnlyCertificateAndExternalSecret(t *testing.T) {
	type madeObject interface {
		MadeObject(objectName string) (kind, name string, ok bool)
	}
	const component = "widget"
	handlers := builtinComponentHandlers()
	var makes []string
	for _, typ := range slices.Sorted(maps.Keys(handlers)) {
		fx, has := componentLabelFixtures[typ]
		if !has {
			t.Errorf("component type %q has no fixture in componentLabelFixtures", typ)
			continue
		}
		name, props := fx.directly(t, component)
		cfg, err := handlers[typ].ToApplicationConfig(&oam.Component{Name: name, Type: typ, Properties: maps.Clone(props)}, "default")
		if err != nil {
			t.Errorf("%s: ToApplicationConfig: %v", typ, err)
			continue
		}
		if m, ok := cfg.(madeObject); ok {
			if _, _, made := m.MadeObject(name); made {
				makes = append(makes, typ)
			}
		}
	}
	if want := []string{"certificate", "externalsecret"}; !slices.Equal(makes, want) {
		t.Errorf("types whose controller makes an object by name: %v, want %v", makes, want)
	}
}
