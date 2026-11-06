package oam

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	kureerrors "github.com/go-kure/launcher/pkg/errors"
)

// recordingComponentHandler records the type of every component it is handed.
type recordingComponentHandler struct {
	typ  string
	seen *[]string
}

func (h *recordingComponentHandler) CanHandle(t string) bool { return t == h.typ }
func (h *recordingComponentHandler) ToApplicationConfig(c *Component, _ string) (stack.ApplicationConfig, error) {
	*h.seen = append(*h.seen, c.Type)
	return &stubAppConfig{}, nil
}

func mustID(t *testing.T, s string) TypeID {
	t.Helper()
	id, err := ValidateTypeIdentity(s)
	if err != nil {
		t.Fatalf("fixture %q: %v", s, err)
	}
	return id
}

// declared returns a transformer with a component handler `widget`, a component
// lowering rule `gadget`, a trait handler `sticky` and a policy handler
// `placement`, each declared under an identity, and the types the handler saw.
func declared(t *testing.T) (*Transformer, *[]string) {
	t.Helper()
	seen := &[]string{}
	tr := NewTransformer(map[string]ComponentHandler{
		"widget": &recordingComponentHandler{typ: "widget", seen: seen},
	}, map[string]TraitHandler{
		"sticky": &stubTraitHandler{typ: "sticky"},
	})
	tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "gadget"})
	tr.RegisterPolicy("placement", &stubPolicyHandler{typ: "placement"})
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"widget": mustID(t, "widget.acme.example@v1"),
		"gadget": mustID(t, "gadget.acme.example@v1.2"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.DeclareTraitTypes(map[string]TypeID{"sticky": mustID(t, "sticky.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	if err := tr.DeclarePolicyTypes(map[string]TypeID{"placement": mustID(t, "placement.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	return tr, seen
}

func TestDeclareComponentTypes_Refuses(t *testing.T) {
	for name, ids := range map[string]map[string]TypeID{
		"a name with no handler or rule": {"nothing": {Name: "nothing.acme.example", Version: "v1"}},
		"an invalid identity":            {"widget": {Name: "Widget.acme.example", Version: "v1"}},
		"two names, one identity": {
			"widget": {Name: "widget.acme.example", Version: "v1"},
			"gadget": {Name: "widget.acme.example", Version: "v1.0.0"},
		},
		"a short name that equals another qualified name": {
			"widget": {Name: "gadget", Version: "v1"},
			"gadget": {Name: "gadget.acme.example", Version: "v1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(map[string]ComponentHandler{"widget": &stubComponentHandler{typ: "widget"}}, nil)
			tr.RegisterComponentLowering(stubComponentLoweringRule{typ: "gadget"})
			err := tr.DeclareComponentTypes(ids)
			if !errors.Is(err, ErrTypeRegistration) {
				t.Fatalf("DeclareComponentTypes error = %v, want ErrTypeRegistration", err)
			}
			if _, err := tr.ResolveComponentType("widget"); err == nil {
				t.Error("a refused declaration left a type declared")
			}
		})
	}
}

func TestDeclareComponentTypes_RefusesARedeclaredName(t *testing.T) {
	tr, _ := declared(t)
	err := tr.DeclareComponentTypes(map[string]TypeID{"widget": mustID(t, "widget.acme.example@v2")})
	if !errors.Is(err, ErrTypeRegistration) {
		t.Fatalf("redeclaring widget: %v, want ErrTypeRegistration", err)
	}
	if id, err := tr.ResolveComponentType("widget"); err != nil || id.String() != "widget.acme.example@v1" {
		t.Errorf("after the refusal widget resolves to %v, %v; want widget.acme.example@v1", id, err)
	}
}

func TestDeclareComponentTypes_AddsToWhatIsDeclared(t *testing.T) {
	tr, _ := declared(t)
	tr.RegisterComponent("later", &stubComponentHandler{typ: "later"})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"later": mustID(t, "later.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]string{
		"widget": "widget.acme.example@v1",
		"later":  "later.acme.example@v1",
	} {
		if id, err := tr.ResolveComponentType(ref); err != nil || id.String() != want {
			t.Errorf("ResolveComponentType(%q) = %v, %v; want %s", ref, id, err, want)
		}
	}
}

func TestResolveComponentType(t *testing.T) {
	tr, _ := declared(t)
	for ref, want := range map[string]string{
		"widget":                   "widget.acme.example@v1",
		"widget@v1":                "widget.acme.example@v1",
		"widget@v1.0.0":            "widget.acme.example@v1",
		"widget.acme.example":      "widget.acme.example@v1",
		"widget.acme.example@v1":   "widget.acme.example@v1",
		"gadget@v1.2":              "gadget.acme.example@v1.2",
		"gadget@v1.2.0":            "gadget.acme.example@v1.2",
		"gadget.acme.example@v1.2": "gadget.acme.example@v1.2",
	} {
		id, err := tr.ResolveComponentType(ref)
		if err != nil || id.String() != want {
			t.Errorf("ResolveComponentType(%q) = %v, %v; want %s", ref, id, err, want)
		}
	}
	for ref, want := range map[string]error{
		"widget@v2":               ErrTypeReferenceUnknown,
		"gadget@v1":               ErrTypeReferenceUnknown,
		"nothing":                 ErrTypeReferenceUnknown,
		"widget.other.example@v1": ErrTypeReferenceUnknown,
		"Widget@v1":               ErrTypeIdentityInvalid,
	} {
		if _, err := tr.ResolveComponentType(ref); !errors.Is(err, want) {
			t.Errorf("ResolveComponentType(%q) error = %v, want %v", ref, err, want)
		}
	}
	if _, err := NewTransformer(nil, nil).ResolveComponentType("widget"); !errors.Is(err, ErrTypeReferenceUnknown) {
		t.Errorf("a transformer with no declaration resolved: %v", err)
	}
}

func TestComponentHandlerAndRuleFor(t *testing.T) {
	tr, _ := declared(t)
	for _, ref := range []string{"widget", "widget@v1", "widget.acme.example@v1"} {
		if h, ok := tr.ComponentHandlerFor(ref); !ok || h == nil {
			t.Errorf("ComponentHandlerFor(%q) = %v, %v; want the widget handler", ref, h, ok)
		}
		if r, ok := tr.ComponentLoweringRuleFor(ref); ok {
			t.Errorf("ComponentLoweringRuleFor(%q) = %v; widget is a handler, not a rule", ref, r)
		}
	}
	for _, ref := range []string{"gadget", "gadget@v1.2", "gadget.acme.example"} {
		if r, ok := tr.ComponentLoweringRuleFor(ref); !ok || r.ComponentType() != "gadget" {
			t.Errorf("ComponentLoweringRuleFor(%q) = %v, %v; want the gadget rule", ref, r, ok)
		}
		if h, ok := tr.ComponentHandlerFor(ref); ok {
			t.Errorf("ComponentHandlerFor(%q) = %v; gadget is a rule, not a handler", ref, h)
		}
	}
	for _, ref := range []string{"nothing", "widget@v9", ""} {
		if _, ok := tr.ComponentHandlerFor(ref); ok {
			t.Errorf("ComponentHandlerFor(%q) answered", ref)
		}
		if _, ok := tr.ComponentLoweringRuleFor(ref); ok {
			t.Errorf("ComponentLoweringRuleFor(%q) answered", ref)
		}
	}
}

// A type nobody declared an identity for is looked up by its plain name, as it
// always was.
func TestComponentHandlerFor_UndeclaredTypeKeepsItsPlainName(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"widget": &stubComponentHandler{typ: "widget"}}, nil)
	if _, ok := tr.ComponentHandlerFor("widget"); !ok {
		t.Error("an undeclared type is not found by its plain name")
	}
	if _, ok := tr.ComponentHandlerFor("widget@v1"); ok {
		t.Error("an undeclared type was found by an identity")
	}
}

func TestResolveTypes_RewritesOnACopy(t *testing.T) {
	tr, _ := declared(t)
	app := &Application{Spec: ApplicationSpec{
		Components: []Component{
			{Name: "a", Type: "widget.acme.example@v1", Traits: []Trait{{Type: "sticky@v1"}, {Type: "sticky"}}},
			{Name: "b", Type: "gadget@v1.2"},
			{Name: "c", Type: "widget"},
		},
		Policies: []ApplicationPolicy{{Name: "p", Type: "placement.acme.example"}},
	}}
	got, err := tr.ResolveTypes(app)
	if err != nil {
		t.Fatal(err)
	}
	if got == app {
		t.Fatal("ResolveTypes returned the caller's application after a rewrite")
	}
	if app.Spec.Components[0].Type != "widget.acme.example@v1" || app.Spec.Components[0].Traits[0].Type != "sticky@v1" ||
		app.Spec.Components[1].Type != "gadget@v1.2" || app.Spec.Policies[0].Type != "placement.acme.example" {
		t.Errorf("ResolveTypes changed the caller's application: %+v", app.Spec)
	}
	var types []string
	for _, c := range got.Spec.Components {
		types = append(types, c.Type)
		for _, trait := range c.Traits {
			types = append(types, "trait:"+trait.Type)
		}
	}
	types = append(types, "policy:"+got.Spec.Policies[0].Type)
	if want := "widget.acme.example@v1 trait:sticky.acme.example@v1 trait:sticky.acme.example@v1 gadget.acme.example@v1.2 widget.acme.example@v1 policy:placement.acme.example@v1"; strings.Join(types, " ") != want {
		t.Errorf("resolved types = %q, want %q", strings.Join(types, " "), want)
	}
}

func TestResolveTypes_ReturnsTheSameApplicationWhenNothingChanges(t *testing.T) {
	tr, _ := declared(t)
	plain := &Application{Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget.acme.example@v1", Traits: []Trait{{Type: "sticky.acme.example@v1"}}}, {Name: "x", Type: "unknown"}}}}
	if got, err := tr.ResolveTypes(plain); err != nil || got != plain {
		t.Errorf("a document of plain names was copied or refused: %v", err)
	}
	none := NewTransformer(map[string]ComponentHandler{"widget": &stubComponentHandler{typ: "widget"}}, nil)
	withRef := &Application{Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget@v1"}}}}
	if got, err := none.ResolveTypes(withRef); err != nil || got != withRef {
		t.Errorf("a transformer with no declaration changed the document: %v", err)
	}
}

func TestResolveTypes_Refuses(t *testing.T) {
	tr, _ := declared(t)
	for name, app := range map[string]*Application{
		"a component version that does not exist": {Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget@v9"}}}},
		"a trait version that does not exist":     {Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget", Traits: []Trait{{Type: "sticky@v9"}}}}}},
		"a policy version that does not exist":    {Spec: ApplicationSpec{Policies: []ApplicationPolicy{{Name: "p", Type: "placement@v9"}}}},
		"a malformed version":                     {Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "widget@1"}}}},
		"an unknown qualified name with version":  {Spec: ApplicationSpec{Components: []Component{{Name: "a", Type: "nothing.acme.example@v1"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tr.ResolveTypes(app)
			if err == nil {
				t.Fatalf("ResolveTypes accepted it: %+v", got)
			}
			if got != nil {
				t.Errorf("a refusal returned an application")
			}
		})
	}
}

// Transform dispatches on the plain name: the handler is handed the component
// under the name it is registered under, whatever identity the author wrote.
func TestTransform_ResolvesAnAuthoredIdentity(t *testing.T) {
	for _, ref := range []string{"widget", "widget@v1", "widget.acme.example", "widget.acme.example@v1.0"} {
		t.Run(ref, func(t *testing.T) {
			tr, seen := declared(t)
			app := makeApp("myapp", makeComponent("w", ref))
			// declared() registers a lowering rule, so Transform validates the
			// document as a lowered one: it needs its TypeMeta.
			app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
			if _, err := tr.Transform(app, TransformContext{}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if len(*seen) != 1 || (*seen)[0] != "widget" {
				t.Errorf("the handler saw types %v, want [widget]", *seen)
			}
			if app.Spec.Components[0].Type != ref {
				t.Errorf("Transform changed the caller's component type to %q", app.Spec.Components[0].Type)
			}
		})
	}
}

func TestTransform_RefusesAnUnresolvableIdentity(t *testing.T) {
	tr, _ := declared(t)
	app := makeApp("myapp", makeComponent("w", "widget@v9"))
	app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
	_, err := tr.Transform(app, TransformContext{})
	if !errors.Is(err, ErrTypeReferenceUnknown) {
		t.Fatalf("Transform error = %v, want ErrTypeReferenceUnknown", err)
	}
	if !strings.Contains(err.Error(), `component "w"`) || !strings.Contains(err.Error(), "v1") {
		t.Errorf("error %q does not name the component and the registered versions", err)
	}
}

// twoVersions returns a transformer where one type is registered under two plain
// names, one per version, so the short name `widget` names both versions and a
// handler is registered under that plain name too.
func twoVersions(t *testing.T) *Transformer {
	t.Helper()
	tr := NewTransformer(map[string]ComponentHandler{
		"widget":    &stubComponentHandler{typ: "widget"},
		"widget-v2": &stubComponentHandler{typ: "widget-v2"},
	}, nil)
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"widget":    mustID(t, "widget.acme.example@v1"),
		"widget-v2": mustID(t, "widget.acme.example@v2"),
	}); err != nil {
		t.Fatal(err)
	}
	return tr
}

// A name more than one version claims is refused even when a handler is registered
// under that plain name: the declared identities are read first, so the handler of
// one version never answers for an author who did not choose it.
func TestAmbiguousShortNameIsNotDispatchedByPlainKey(t *testing.T) {
	tr := twoVersions(t)
	if _, ok := tr.ComponentHandlerFor("widget"); ok {
		t.Error("ComponentHandlerFor answered for an ambiguous short name")
	}
	if _, ok := tr.ComponentLoweringRuleFor("widget"); ok {
		t.Error("ComponentLoweringRuleFor answered for an ambiguous short name")
	}
	if _, ok := tr.ComponentHandlerFor("widget@v2"); !ok {
		t.Error("ComponentHandlerFor did not answer for widget@v2")
	}
	app := makeApp("myapp", makeComponent("w", "widget"))
	app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
	if _, err := tr.ResolveTypes(app); !errors.Is(err, ErrTypeReferenceAmbiguous) {
		t.Errorf("ResolveTypes error = %v, want ErrTypeReferenceAmbiguous", err)
	}
	if _, err := tr.Transform(app, TransformContext{}); !errors.Is(err, ErrTypeReferenceAmbiguous) {
		t.Errorf("Transform error = %v, want ErrTypeReferenceAmbiguous", err)
	}
}

// A plain name that is not a declared identity, or not a valid one at all (a custom
// trait such as custom_metrics), passes through to the later refusal, the allowlist
// or "no handler", instead of failing as a type reference.
func TestResolveTypes_PlainNamesNobodyDeclaredPassThrough(t *testing.T) {
	tr, _ := declared(t)
	app := &Application{Spec: ApplicationSpec{Components: []Component{
		{Name: "a", Type: "unknown", Traits: []Trait{{Type: "custom_metrics"}, {Type: "Unknown-Trait"}, {Type: "nothing"}}},
	}, Policies: []ApplicationPolicy{{Name: "p", Type: "custom_policy"}}}}
	got, err := tr.ResolveTypes(app)
	if err != nil || got != app {
		t.Fatalf("ResolveTypes = %v, %v; want the same application and no error", got, err)
	}
}

// A refused type reference in Transform is a ValidationError, as Parse gives it,
// and the resolver's sentinel is still reachable through it.
func TestTransform_RefusalIsAValidationError(t *testing.T) {
	tr, _ := declared(t)
	app := makeApp("myapp", makeComponent("w", "widget@v9"))
	app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
	_, err := tr.Transform(app, TransformContext{})
	var ve *kureerrors.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Transform error %T is not a *ValidationError: %v", err, err)
	}
	if !errors.Is(err, ErrTypeReferenceUnknown) {
		t.Errorf("errors.Is(err, ErrTypeReferenceUnknown) is false for %v", err)
	}
}

// ResolveTypes writes a declared type as its identity, so its result is valid input
// to Transform even where the plain key behind the identity is an ambiguous short
// name (widget-v2 is the key of widget.acme.example@v2, and widget names two versions).
func TestResolveTypes_ResultIsValidTransformInput(t *testing.T) {
	seen := &[]string{}
	tr := NewTransformer(map[string]ComponentHandler{
		"widget":    &recordingComponentHandler{typ: "widget", seen: seen},
		"widget-v2": &recordingComponentHandler{typ: "widget-v2", seen: seen},
	}, nil)
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"widget":    mustID(t, "widget.acme.example@v1"),
		"widget-v2": mustID(t, "widget.acme.example@v2"),
	}); err != nil {
		t.Fatal(err)
	}
	app := makeApp("myapp", makeComponent("a", "widget@v1"), makeComponent("b", "widget@v2"))
	app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
	got, err := tr.ResolveTypes(app)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := got.Spec.Components[0].Type, got.Spec.Components[1].Type; a != "widget.acme.example@v1" || b != "widget.acme.example@v2" {
		t.Fatalf("ResolveTypes types = %q, %q; want the two identities", a, b)
	}
	if _, err := tr.Transform(got, TransformContext{}); err != nil {
		t.Fatalf("Transform of the ResolveTypes result: %v", err)
	}
	if strings.Join(*seen, " ") != "widget widget-v2" {
		t.Errorf("handlers saw types %v, want [widget widget-v2]", *seen)
	}
}
