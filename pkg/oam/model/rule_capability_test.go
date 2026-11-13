package model_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// A trait a model writes on a part is resolved like an authored one: the capability
// binding is merged, a missing binding and a platform-reserved value are refused, and the
// consumed capability is recorded (go-kure/launcher#1562). Each case has an authored control
// that must behave the same.

const exposeModelParts = `
    - id: app
      name: app
      type: ` + webType + `
      properties:
        image: registry.example/app:1
        port: 8080
      traits:
        - type: expose.launcher.gokure.dev@v1
          properties:
            scope: internal
            hostnames: [app.example]
`

const exposeModelReservedParts = `
    - id: app
      name: app
      type: ` + webType + `
      properties:
        image: registry.example/app:1
        port: 8080
      traits:
        - type: expose.launcher.gokure.dev@v1
          properties:
            scope: internal
            hostnames: [app.example]
            controllerType: gateway
`

func exposeCapabilities() map[string]oam.CapabilityBinding {
	return map[string]oam.CapabilityBinding{
		"expose.internal": {Rendering: map[string]any{"controllerType": "ingress", "ingressClassName": "internal"}},
	}
}

func transformModelApp(t *testing.T, tr *oam.Transformer, comps []oam.Component, caps map[string]oam.CapabilityBinding) (*oam.PolicyResult, error) {
	t.Helper()
	app := &oam.Application{
		APIVersion: oam.SupportedAPIVersion, Kind: "Application",
		Metadata: oam.Metadata{Name: "shop", Namespace: "demo"},
		Spec:     oam.ApplicationSpec{Components: comps},
	}
	_, result, err := tr.TransformWithPolicy(app, oam.TransformContext{Namespace: "demo", Capabilities: caps})
	return result, err
}

func authoredExposeControl(props map[string]any) []oam.Component {
	return []oam.Component{{
		Name: "app", Type: webType,
		Properties: map[string]any{"image": "registry.example/app:1", "port": 8080},
		Traits:     []oam.Trait{{Type: "expose.launcher.gokure.dev@v1", Properties: props}},
	}}
}

// T1 and T2: the model's trait takes its class from the capability and the capability is
// recorded as consumed, exactly as for the authored control.
func TestRule_ModelTraitResolvesItsCapability(t *testing.T) {
	trc := expandTransformer(t)
	control, err := transformModelApp(t, trc, authoredExposeControl(map[string]any{"scope": "internal", "hostnames": []any{"app.example"}}), exposeCapabilities())
	if err != nil {
		t.Fatalf("authored control: %v", err)
	}

	tr, _ := registered(t, modelHeader+exposeModelParts)
	got, err := transformModelApp(t, tr, orders(), exposeCapabilities())
	if err != nil {
		t.Fatalf("model trait: %v", err)
	}
	if want := []string{"expose.internal"}; !reflect.DeepEqual(control.ConsumedCapabilities, want) {
		t.Fatalf("authored control consumed %v, want %v", control.ConsumedCapabilities, want)
	}
	if !reflect.DeepEqual(got.ConsumedCapabilities, control.ConsumedCapabilities) {
		t.Fatalf("model trait consumed %v, want the authored control's %v", got.ConsumedCapabilities, control.ConsumedCapabilities)
	}
}

// T1b: with no binding in the profile the model's trait is refused, naming the component
// and the trait, as the authored control is.
func TestRule_ModelTraitWithoutBindingIsRefused(t *testing.T) {
	trc := expandTransformer(t)
	_, err := transformModelApp(t, trc, authoredExposeControl(map[string]any{"scope": "internal", "hostnames": []any{"app.example"}}), nil)
	if !errors.Is(err, oam.ErrMissingCapability) {
		t.Fatalf("authored control: want ErrMissingCapability, got %v", err)
	}

	tr, _ := registered(t, modelHeader+exposeModelParts)
	_, err = transformModelApp(t, tr, orders(), nil)
	if !errors.Is(err, oam.ErrMissingCapability) {
		t.Fatalf("model trait: want ErrMissingCapability, got %v", err)
	}
	if !strings.Contains(err.Error(), "expose") {
		t.Errorf("refusal must name the trait, got: %v", err)
	}
}

// T3: a model that sets a platform-reserved routing field is refused, as the authored control is.
func TestRule_ModelTraitReservedValueIsRefused(t *testing.T) {
	trc := expandTransformer(t)
	_, err := transformModelApp(t, trc, authoredExposeControl(map[string]any{"scope": "internal", "hostnames": []any{"app.example"}, "controllerType": "gateway"}), exposeCapabilities())
	if !errors.Is(err, oam.ErrPlatformReserved) {
		t.Fatalf("authored control: want ErrPlatformReserved, got %v", err)
	}

	tr, _ := registered(t, modelHeader+exposeModelReservedParts)
	_, err = transformModelApp(t, tr, orders(), exposeCapabilities())
	if !errors.Is(err, oam.ErrPlatformReserved) {
		t.Fatalf("model trait: want ErrPlatformReserved, got %v", err)
	}
}
