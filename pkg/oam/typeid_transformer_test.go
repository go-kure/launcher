package oam

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	kureerrors "github.com/go-kure/launcher/pkg/errors"
	"github.com/go-kure/launcher/pkg/oam/netpol"
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

// ambiguousProviders returns a transformer where the short name `widget` names two
// versions of one type and `gizmo` the same for a lowering rule, each registered
// under two plain keys, as endpoint providers.
func ambiguousProviders(t *testing.T) *Transformer {
	t.Helper()
	tr := NewTransformer(nil, nil)
	tr.RegisterComponent("widget", &stubEndpointHandler{typeName: "widget", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponent("widget-v2", &stubEndpointHandler{typeName: "widget-v2", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "gizmo", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "gizmo-v2", eps: []netpol.Endpoint{validEndpoint()}})
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"widget":    mustID(t, "widget.acme.example@v1"),
		"widget-v2": mustID(t, "widget.acme.example@v2"),
		"gizmo":     mustID(t, "gizmo.acme.example@v1"),
		"gizmo-v2":  mustID(t, "gizmo.acme.example@v2"),
	}); err != nil {
		t.Fatal(err)
	}
	return tr
}

// A reference the resolver refuses has no endpoint provider either: handler-backed
// and rule-backed, an ambiguous short name answers nothing, as Transform refuses it.
func TestComponentEndpoints_AmbiguousReferenceHasNoProvider(t *testing.T) {
	tr := ambiguousProviders(t)
	for _, ref := range []string{"widget", "gizmo"} {
		if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: ref}); eps != nil || err != nil {
			t.Errorf("ComponentEndpoints(%q) = %v, %v; want nil, nil", ref, eps, err)
		}
		if eps, err := tr.ComponentEndpointsNamed("app", &Component{Name: "x", Type: ref}, nil); eps != nil || err != nil {
			t.Errorf("ComponentEndpointsNamed(%q) = %v, %v; want nil, nil", ref, eps, err)
		}
	}
	for _, ref := range []string{"widget@v2", "gizmo@v2", "widget.acme.example@v1"} {
		if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: ref}); err != nil || len(eps) != 1 {
			t.Errorf("ComponentEndpoints(%q) = %v, %v; want one endpoint", ref, eps, err)
		}
	}
	// The ambiguous rule-backed reference has no rule either (the lookup APIs agree with Transform).
	if _, ok := tr.ComponentLoweringRuleFor("gizmo"); ok {
		t.Error("ComponentLoweringRuleFor answered for an ambiguous short name")
	}
	if _, ok := tr.ComponentLoweringRuleFor("gizmo@v2"); !ok {
		t.Error("ComponentLoweringRuleFor did not answer for gizmo@v2")
	}
}

// A handler or rule registered under a key that contains `@` is not reachable by
// that text once a reference with a version fails to resolve: Transform refuses it,
// so ComponentHandlerFor, ComponentLoweringRuleFor and the endpoint lookup agree.
func TestVersionedRefusalDoesNotFallBackToARegisteredKey(t *testing.T) {
	tr, _ := declared(t)
	tr.RegisterComponent("widget@v9", &stubEndpointHandler{typeName: "widget@v9", eps: []netpol.Endpoint{validEndpoint()}})
	tr.RegisterComponentLowering(stubEndpointRule{typeName: "gadget@v9", eps: []netpol.Endpoint{validEndpoint()}})
	for _, ref := range []string{"widget@v9", "gadget@v9"} {
		if h, ok := tr.ComponentHandlerFor(ref); ok {
			t.Errorf("ComponentHandlerFor(%q) = %v despite an unresolved version", ref, h)
		}
		if r, ok := tr.ComponentLoweringRuleFor(ref); ok {
			t.Errorf("ComponentLoweringRuleFor(%q) = %v despite an unresolved version", ref, r)
		}
		if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: ref}); eps != nil || err != nil {
			t.Errorf("ComponentEndpoints(%q) = %v, %v; want nil, nil", ref, eps, err)
		}
	}
}

// epHandler is a recording component handler that is also an endpoint provider.
type epHandler struct{ recordingComponentHandler }

func (h *epHandler) Endpoints(_ *Component) ([]netpol.Endpoint, error) {
	return []netpol.Endpoint{validEndpoint()}, nil
}

// Every lookup that takes a type reference answers for a reference exactly when
// Transform accepts it: a reference that does not resolve (unknown, ambiguous, a
// literal name@vN nobody declared) answers nothing, a plain registered name and a
// resolving identity answer. The matrix is the four inputs of the review brief
// (unresolved, ambiguous short name, plain registered key, literal name@vN with no
// declaration) with the resolving forms beside them.
func TestLookupsAgreeWithTransform(t *testing.T) {
	want := map[string]bool{
		"widget":                 false, // ambiguous short name: two versions are declared
		"widget.acme.example":    false, // ambiguous: the qualified name without a version
		"widget@v1":              true,
		"widget@v2":              true,
		"widget.acme.example@v2": true,
		"widget@v9":              false, // a version nobody declared
		"plain":                  true,  // a plain registered key, no identity declared
		"plain@v1":               false, // a version on a name with no declaration
		"lit@v1":                 false, // a literal registered key with `@`, no declaration
		"nothing":                false, // unresolved, unregistered
		"Widget":                 false, // not a valid identity, not registered
		"":                       false,
	}
	// ResolveTypes refuses exactly the references that name a declared type
	// ambiguously, or carry a version nothing declares. A plain name nobody declared
	// passes through to the later refusal ("no handler") as it always did.
	resolveRefuses := map[string]bool{
		"widget": true, "widget.acme.example": true, "widget@v9": true, "plain@v1": true, "lit@v1": true,
	}
	seen := &[]string{}
	newHandler := func(typ string) *epHandler {
		return &epHandler{recordingComponentHandler{typ: typ, seen: seen}}
	}
	tr := NewTransformer(map[string]ComponentHandler{
		"widget":    newHandler("widget"),
		"widget-v2": newHandler("widget-v2"),
		"plain":     newHandler("plain"),
		"lit@v1":    newHandler("lit@v1"),
	}, nil)
	if err := tr.DeclareComponentTypes(map[string]TypeID{
		"widget":    mustID(t, "widget.acme.example@v1"),
		"widget-v2": mustID(t, "widget.acme.example@v2"),
	}); err != nil {
		t.Fatal(err)
	}
	// The rule-backed family: the same names, a rule each.
	rules := NewTransformer(nil, nil)
	for _, k := range []string{"widget", "widget-v2", "plain", "lit@v1"} {
		rules.RegisterComponentLowering(stubEndpointRule{typeName: k, eps: []netpol.Endpoint{validEndpoint()}})
	}
	if err := rules.DeclareComponentTypes(map[string]TypeID{
		"widget":    mustID(t, "widget.acme.example@v1"),
		"widget-v2": mustID(t, "widget.acme.example@v2"),
	}); err != nil {
		t.Fatal(err)
	}
	for ref, ok := range want {
		t.Run("handler/"+ref, func(t *testing.T) {
			if _, got := tr.ComponentHandlerFor(ref); got != ok {
				t.Errorf("ComponentHandlerFor(%q) = %v, want %v", ref, got, ok)
			}
			if _, got := tr.ComponentLoweringRuleFor(ref); got {
				t.Errorf("ComponentLoweringRuleFor(%q) answered for a handler type", ref)
			}
			eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: ref})
			if err != nil || (len(eps) == 1) != ok {
				t.Errorf("ComponentEndpoints(%q) = %v, %v; want an answer = %v", ref, eps, err, ok)
			}
			eps, err = tr.ComponentEndpointsNamed("app", &Component{Name: "x", Type: ref}, nil)
			if err != nil || (len(eps) == 1) != ok {
				t.Errorf("ComponentEndpointsNamed(%q) = %v, %v; want an answer = %v", ref, eps, err, ok)
			}
			if ref == "" {
				return
			}
			app := makeApp("myapp", makeComponent("w", ref))
			app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
			_, terr := tr.Transform(app, TransformContext{})
			if (terr == nil) != ok {
				t.Errorf("Transform(%q) error = %v, want success = %v", ref, terr, ok)
			}
			if _, verr := tr.ResolveTypes(app); (verr != nil) != resolveRefuses[ref] {
				t.Errorf("ResolveTypes(%q) error = %v", ref, verr)
			}
		})
		t.Run("rule/"+ref, func(t *testing.T) {
			if _, got := rules.ComponentLoweringRuleFor(ref); got != ok {
				t.Errorf("ComponentLoweringRuleFor(%q) = %v, want %v", ref, got, ok)
			}
			if _, got := rules.ComponentHandlerFor(ref); got {
				t.Errorf("ComponentHandlerFor(%q) answered for a rule type", ref)
			}
			eps, err := rules.ComponentEndpoints(&Component{Name: "x", Type: ref})
			if err != nil || (len(eps) == 1) != ok {
				t.Errorf("ComponentEndpoints(%q) = %v, %v; want an answer = %v", ref, eps, err, ok)
			}
		})
		if ref == "" {
			continue
		}
		// The authored checks resolve like ResolveTypes: they refuse what it refuses, as a
		// ValidationError, and pass a plain name nobody declared on to the later stage.
		t.Run("validate/"+ref, func(t *testing.T) {
			app := makeApp("myapp", makeComponent("w", ref))
			app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
			_, err := tr.ValidateAuthoredProperties(app)
			if (err != nil) != resolveRefuses[ref] {
				t.Fatalf("ValidateAuthoredProperties(%q) error = %v, want a refusal = %v", ref, err, resolveRefuses[ref])
			}
			var ve *kureerrors.ValidationError
			if err != nil && !errors.As(err, &ve) {
				t.Errorf("ValidateAuthoredProperties(%q) error %T is not a *ValidationError: %v", ref, err, err)
			}
		})
		// Parse accepts the reference exactly when Transform does: the rule-backed family
		// claims its keys as lowerable types, so a reference that resolves to one is accepted.
		t.Run("parse/"+ref, func(t *testing.T) {
			_, err := ParseWithExtraTypes(identityDoc(ref, ""), nil, rules.LowerableTypes())
			if (err == nil) != ok {
				t.Errorf("ParseWithExtraTypes(%q) error = %v, want success = %v", ref, err, ok)
			}
		})
	}
}

// A position where nothing is declared behaves as it did before type identities: a
// type is its registered name, a name with `@` included, and every lookup agrees with
// Transform about it. It holds with nothing declared anywhere, and with another
// position declared (traits here), where the engine still resolves the whole document.
func TestUndeclaredPositionKeepsRegisteredNames(t *testing.T) {
	seen := &[]string{}
	handlers := func() map[string]ComponentHandler {
		return map[string]ComponentHandler{
			"plain":  &epHandler{recordingComponentHandler{typ: "plain", seen: seen}},
			"lit@v1": &epHandler{recordingComponentHandler{typ: "lit@v1", seen: seen}},
		}
	}
	bare := NewTransformer(handlers(), nil)
	traitsOnly := NewTransformer(handlers(), map[string]TraitHandler{"scaler": &stubTraitHandler{typ: "scaler"}})
	if err := traitsOnly.DeclareTraitTypes(map[string]TypeID{"scaler": mustID(t, "scaler.launcher.gokure.dev@v1")}); err != nil {
		t.Fatal(err)
	}
	for name, tr := range map[string]*Transformer{"nothing declared": bare, "traits declared": traitsOnly} {
		for _, ref := range []string{"plain", "lit@v1"} {
			t.Run(name+"/"+ref, func(t *testing.T) {
				if _, ok := tr.ComponentHandlerFor(ref); !ok {
					t.Errorf("ComponentHandlerFor(%q) found no handler", ref)
				}
				if eps, err := tr.ComponentEndpoints(&Component{Name: "x", Type: ref}); err != nil || len(eps) != 1 {
					t.Errorf("ComponentEndpoints(%q) = %v, %v; want the provider's endpoints", ref, eps, err)
				}
				app := makeApp("myapp", makeComponent("w", ref))
				app.APIVersion, app.Kind = SupportedAPIVersion, "Application"
				if _, err := tr.Transform(app, TransformContext{}); err != nil {
					t.Errorf("Transform(%q) = %v, want success", ref, err)
				}
				if _, err := tr.ResolveTypes(app); err != nil {
					t.Errorf("ResolveTypes(%q) = %v, want none", ref, err)
				}
				if _, err := tr.ValidateAuthoredProperties(app); err != nil {
					t.Errorf("ValidateAuthoredProperties(%q) = %v, want none", ref, err)
				}
			})
		}
	}
}
