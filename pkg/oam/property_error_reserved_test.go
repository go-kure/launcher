package oam

import (
	stderrors "errors"
	"strings"
	"testing"
)

// go-kure/launcher#1469: the platform-reservation refusal is a *PropertyError of its
// own kind on every route that raises it. perrCases holds the message and the
// location for the public entry point. This file holds the routes: the entry point,
// the wrapper the refusal comes back in, and the sites that make it.

const reservedRefusalText = `is platform-reserved and may only be set via ClusterProfile capability rendering: oam: property is platform-reserved`

type reservedRoute struct {
	name    string
	tr      func() *Transformer
	app     func() *Application
	run     func(*Transformer, *Application) error
	wrapper string // "transform" or "lowering"
	pos     string // "component", "trait", or "" for a rule-emitted element
	comp    string
	trait   string
	path    string // the refusal's own text before the key, "properties" or "properties.config"
	key     string
	field   string
}

func reservedValidate(tr *Transformer, app *Application) error {
	_, err := tr.ValidateAuthoredProperties(app)
	return err
}

func reservedTransform(tr *Transformer, app *Application) error {
	_, err := tr.Transform(app, netpolCapability())
	return err
}

func reservedLower(tr *Transformer, app *Application) error {
	_, err := tr.lower(app, TransformContext{})
	return err
}

func reservedWithRule() *Transformer {
	tr := nestedReservedTransformer()
	tr.RegisterTraitLowering(nestedReservedWrapperRule{})
	return tr
}

func reservedComponentDoc() *Application {
	return singleComponentApp("Application", "reserved-sink", authoredReservedNull())
}

func reservedTraitDoc() *Application {
	return appWithTrait("nested-reserved", map[string]any{"config": map[string]any{"locked": "x"}})
}

func reservedArrayDoc() *Application {
	return appWithTrait("nested-reserved", map[string]any{"items": []any{map[string]any{"locked": "x"}}})
}

// reservedSealedDoc is a trait that is sealed and not synthesized: a rule emitted it
// and nothing proved the platform wrote it, so Transform checks it as it stands.
func reservedSealedDoc() *Application {
	app := reservedTraitDoc()
	app.Spec.Components[0].Traits[0].sealed = true
	return app
}

func reservedRoutes() []reservedRoute {
	var rows []reservedRoute
	entries := map[string]func(*Transformer, *Application) error{
		"ValidateAuthoredProperties": reservedValidate,
		"Transform":                  reservedTransform,
	}
	trs := map[string]struct {
		tr      func() *Transformer
		wrapper string
	}{
		"without lowering rules": {nestedReservedTransformer, "transform"},
		"with a lowering rule":   {reservedWithRule, "lowering"},
	}
	docs := []reservedRoute{
		{name: "component", app: reservedComponentDoc, pos: "component", comp: "web", path: "properties", key: "networkPolicy", field: "networkPolicy"},
		{name: "trait", app: reservedTraitDoc, pos: "trait", comp: "web", trait: "nested-reserved", path: "properties.config", key: "locked", field: "config.locked"},
		{name: "trait in an array item", app: reservedArrayDoc, pos: "trait", comp: "web", trait: "nested-reserved", path: "properties.items[0]", key: "locked", field: "items[0].locked"},
	}
	for entryName, run := range entries {
		for trName, v := range trs {
			for _, d := range docs {
				r := d
				r.name = entryName + "/" + trName + "/" + d.name
				r.tr, r.wrapper, r.run = v.tr, v.wrapper, run
				rows = append(rows, r)
			}
		}
	}
	// The sites Transform reaches with no lowering round before them.
	rows = append(rows, reservedRoute{
		name: "Transform/sealed trait no rule emitted", tr: nestedReservedTransformer, app: reservedSealedDoc, run: reservedTransform,
		wrapper: "transform", pos: "trait", comp: "web", trait: "nested-reserved", path: "properties.config", key: "locked", field: "config.locked",
	})
	// lower alone, with a rule that declares the schema. The authored pre-check refuses first
	// (enforceAuthoredReservations); the rule's own dispatch check sees only what the pre-check
	// passed, so it never refuses and no row can reach it.
	rows = append(rows,
		reservedRoute{
			name: "lower/component rule, authored pre-check",
			tr: func() *Transformer {
				tr := NewTransformer(nil, nil)
				tr.RegisterComponentLowering(&reservingComponentRule{})
				return tr
			},
			app: func() *Application {
				return singleComponentApp("Application", "reserving-component", map[string]any{"image": "nginx", "networkPolicy": map[string]any{"trafficSources": []any{}}})
			},
			run: reservedLower, wrapper: "lowering", pos: "component", comp: "web", path: "properties", key: "networkPolicy", field: "networkPolicy",
		},
		reservedRoute{
			name: "lower/trait rule, authored pre-check",
			tr: func() *Transformer {
				tr := NewTransformer(nil, nil)
				tr.RegisterTraitLowering(&reservingRule{})
				return tr
			},
			app: func() *Application {
				return reservingApp(map[string]any{"networkPolicy": map[string]any{"trafficSources": []any{}}})
			},
			run:     reservedLower,
			wrapper: "lowering", pos: "trait", comp: "web", trait: "reserving-trait", path: "properties", key: "networkPolicy", field: "networkPolicy",
		},
	)
	return rows
}

func TestReservationRefusalIsTypedOnEveryRoute(t *testing.T) {
	for _, r := range reservedRoutes() {
		t.Run(r.name, func(t *testing.T) {
			err := r.run(r.tr(), r.app())
			if err == nil {
				t.Fatal("the document was accepted, want a refusal")
			}
			if !stderrors.Is(err, ErrPlatformReserved) {
				t.Errorf("errors.Is(err, ErrPlatformReserved) is false for %T %v", err, err)
			}
			var pe *PropertyError
			if !stderrors.As(err, &pe) {
				t.Fatalf("error %T %v holds no *PropertyError", err, err)
			}
			got := [5]string{string(pe.Kind), pe.Position, pe.Component, pe.Trait, pe.Field}
			want := [5]string{"platform-reserved", r.pos, r.comp, r.trait, r.field}
			if got != want {
				t.Errorf("kind, position, component, trait, field\n got: %q\nwant: %q", got, want)
			}
			wantText := r.path + ": " + `"` + r.key + `" ` + reservedRefusalText
			if pe.Error() != wantText {
				t.Errorf("PropertyError text\n got: %s\nwant: %s", pe.Error(), wantText)
			}
			if !strings.HasSuffix(err.Error(), wantText) {
				t.Errorf("the wrapped text does not end with the refusal text\n got: %s\nwant suffix: %s", err.Error(), wantText)
			}
			var te *TransformError
			var le *LoweringError
			switch r.wrapper {
			case "transform":
				if !stderrors.As(err, &te) {
					t.Errorf("want a *TransformError in the chain, got %T", err)
				}
			case "lowering":
				if !stderrors.As(err, &le) {
					t.Errorf("want a *LoweringError in the chain, got %T", err)
				}
			}
		})
	}
}

// A refusal made on an element a rule emitted carries the kind and no position, as
// every emission refusal does: it is not the author's element.
func TestReservationRefusalOnAnEmittedElementHasNoPosition(t *testing.T) {
	routes := map[string]func() error{
		"component rule copies into a trait": func() error {
			tr := reservedTraitTransformer()
			tr.RegisterComponentLowering(traitEmittingComponentRule{})
			_, err := tr.Transform(singleComponentApp("Application", "trait-emitting", authoredNetworkPolicy()), TransformContext{})
			return err
		},
		"document rule builds a trait": func() error {
			tr := reservedTraitTransformer()
			tr.RegisterDocumentLowering(rendersTraitDocRule{})
			app := &Application{APIVersion: SupportedAPIVersion, Kind: "TraitRendering", Metadata: Metadata{Name: "myapp", Namespace: "test"}}
			_, err := tr.Transform(app, netpolCapability())
			return err
		},
	}
	for name, run := range routes {
		t.Run(name, func(t *testing.T) {
			err := run()
			if !stderrors.Is(err, ErrPlatformReserved) {
				t.Fatalf("errors.Is(err, ErrPlatformReserved) is false for %T %v", err, err)
			}
			var pe *PropertyError
			if !stderrors.As(err, &pe) {
				t.Fatalf("error %T %v holds no *PropertyError", err, err)
			}
			if string(pe.Kind) != "platform-reserved" || pe.Position != "" || pe.Component != "" || pe.Trait != "" || pe.Field != "" {
				t.Errorf("kind %q position %q component %q trait %q field %q, want platform-reserved and all else empty", pe.Kind, pe.Position, pe.Component, pe.Trait, pe.Field)
			}
			var le *LoweringError
			if !stderrors.As(err, &le) {
				t.Errorf("want a *LoweringError in the chain, got %T", err)
			}
		})
	}
}
