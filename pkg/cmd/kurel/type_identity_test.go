package kurel

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/go-kure/launcher/pkg/oam"
)

// An application may author a built-in by its identity: the build parses it,
// resolves it to the registered type and lowers it exactly as the plain name.
func TestBuiltinTransformer_AuthoredTypeIdentity(t *testing.T) {
	for _, ref := range []string{"webservice", "webservice@v1", "webservice.launcher.gokure.dev", "webservice.launcher.gokure.dev@v1"} {
		t.Run(ref, func(t *testing.T) {
			var seen oam.Component
			tr := newBuiltinTransformer()
			tr.RegisterTraitLowering(originProbeRule{seen: &seen})

			app := webserviceApp(map[string]any{"image": "nginx:1"}, oam.Trait{Type: "origin-probe"})
			app.Spec.Components[0].Type = ref
			_, err := tr.Transform(app, oam.TransformContext{Namespace: "ns", Domain: kurelDomain})
			if !stderrors.Is(err, errOriginSeen) {
				t.Fatalf("expected the probe to run, got: %v", err)
			}
			if seen.Type != "deployment" {
				t.Errorf("the lowered component has type %q, want deployment", seen.Type)
			}
			if got, ok := seen.Origin(); !ok || got.ComponentType != "webservice" {
				t.Errorf("Origin = %+v, %v; want ComponentType webservice", got, ok)
			}
			if app.Spec.Components[0].Type != ref {
				t.Errorf("the caller's component type changed to %q", app.Spec.Components[0].Type)
			}
		})
	}
}

func TestBuiltinTransformer_ParsesAnAuthoredTypeIdentity(t *testing.T) {
	doc := `apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: app
spec:
  components:
    - name: web
      type: webservice@v1
      properties: {image: "nginx:1"}
    - name: cfg
      type: configmap@v1
      properties: {data: {a: b}}
`
	if _, err := oam.Parse([]byte(doc)); err == nil {
		t.Error("Parse, which knows no identity, accepted it")
	}
	if _, err := oam.ParseWithExtraTypes([]byte(doc), nil, newBuiltinTransformer().LowerableTypes()); err != nil {
		t.Errorf("ParseWithExtraTypes with the builtin transformer: %v", err)
	}
	refused := strings.Replace(doc, "webservice@v1", "webservice@v9", 1)
	_, err := oam.ParseWithExtraTypes([]byte(refused), nil, newBuiltinTransformer().LowerableTypes())
	if err == nil || !strings.Contains(err.Error(), "v1") {
		t.Errorf("webservice@v9: error %v, want a refusal naming the registered version v1", err)
	}
}

// The authored-property check looks a handler up by the plain type name, so an
// identity must be resolved first: an unknown property under configmap@v1 is
// refused exactly as under configmap (go-kure/launcher#1258).
func TestBuiltinTransformer_AuthoredPropertiesAreCheckedUnderAnIdentity(t *testing.T) {
	for _, ref := range []string{"configmap", "configmap@v1"} {
		t.Run(ref, func(t *testing.T) {
			app := webserviceApp(map[string]any{"data": map[string]any{"a": "b"}, "bogus": 1})
			app.Spec.Components[0].Type = ref
			tr := newBuiltinTransformer()
			for name, validate := range map[string]func() error{
				"ValidateAuthoredProperties": func() error { _, err := tr.ValidateAuthoredProperties(app); return err },
				"ForTarget":                  func() error { _, err := tr.ValidateAuthoredPropertiesForTarget(app, nil, nil); return err },
			} {
				err := validate()
				if err == nil || !strings.Contains(err.Error(), `unsupported field "bogus"`) {
					t.Errorf("%s with type %q: error %v, want an unsupported field refusal", name, ref, err)
				}
			}
			if app.Spec.Components[0].Type != ref {
				t.Errorf("the caller's component type changed to %q", app.Spec.Components[0].Type)
			}
		})
	}
}

// An identity that does not resolve is refused by the authored-property check
// with the resolver's error as the cause, on both entry points.
func TestBuiltinTransformer_AuthoredPropertiesRefuseAnUnresolvedIdentity(t *testing.T) {
	tr := newBuiltinTransformer()
	app := webserviceApp(map[string]any{"image": "nginx:1"})
	app.Spec.Components[0].Type = "webservice@v9"
	for name, validate := range map[string]func() error{
		"ValidateAuthoredProperties": func() error { _, err := tr.ValidateAuthoredProperties(app); return err },
		"ForTarget":                  func() error { _, err := tr.ValidateAuthoredPropertiesForTarget(app, nil, nil); return err },
	} {
		if err := validate(); !stderrors.Is(err, oam.ErrTypeReferenceUnknown) {
			t.Errorf("%s: error %v, want ErrTypeReferenceUnknown", name, err)
		}
	}
}

// Every built-in resolves by its identity to the type it is registered under,
// in all three positions.
func TestBuiltinTransformer_EveryTypeResolvesByItsIdentity(t *testing.T) {
	tr := newBuiltinTransformer()
	for name := range builtinComponentHandlers() {
		id, err := tr.ResolveComponentType(name)
		if err != nil {
			t.Errorf("component %q does not resolve: %v", name, err)
			continue
		}
		if _, ok := tr.ComponentHandlerFor(id.String()); !ok {
			t.Errorf("component %q: no handler for its identity %s", name, id)
		}
	}
	for name := range builtinComponentLoweringRules() {
		id, err := tr.ResolveComponentType(name)
		if err != nil {
			t.Errorf("component rule %q does not resolve: %v", name, err)
			continue
		}
		if _, ok := tr.ComponentLoweringRuleFor(id.String()); !ok {
			t.Errorf("component rule %q: no rule for its identity %s", name, id)
		}
	}
	for name := range builtinTraitHandlers() {
		if _, err := tr.ResolveTraitType(name); err != nil {
			t.Errorf("trait %q does not resolve: %v", name, err)
		}
	}
	for name := range builtinTraitLoweringRules() {
		if _, err := tr.ResolveTraitType(name); err != nil {
			t.Errorf("trait rule %q does not resolve: %v", name, err)
		}
	}
	for name := range builtinPolicyHandlers() {
		if _, err := tr.ResolvePolicyType(name); err != nil {
			t.Errorf("policy %q does not resolve: %v", name, err)
		}
	}
}
