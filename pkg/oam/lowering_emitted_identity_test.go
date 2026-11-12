package oam

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A rule that emits a component by its declared identity is dispatched like an authored one
// (go-kure/launcher#1261): the engine resolves the emitted types to the registered name after
// each round.

// identityRule emits one component of the type typ, named name-part.
type identityRule struct {
	typ, emits string
	hook       *func(NameRequest) (string, bool)
}

func (r identityRule) ComponentType() string { return r.typ }
func (r identityRule) LowerComponent(c *Component, lctx LoweringContext) (LoweringResult, error) {
	if r.hook != nil {
		*r.hook = lctx.NamingHook()
	}
	return LoweringResult{Components: []Component{{Name: c.Name + "-part", Type: r.emits, Properties: map[string]any{}}}}, nil
}

func TestLowering_EmittedIdentityResolvesToTheRegisteredName(t *testing.T) {
	tr, seen := declared(t)
	tr.RegisterComponentLowering(identityRule{typ: "maker", emits: "widget.acme.example@v1"})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"maker": mustID(t, "maker.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	app := &Application{
		APIVersion: SupportedAPIVersion, Kind: "Application",
		Metadata: Metadata{Name: "shop", Namespace: "demo"},
		Spec:     ApplicationSpec{Components: []Component{{Name: "orders", Type: "maker.acme.example@v1", Properties: map[string]any{}}}},
	}
	if _, err := tr.Transform(app, TransformContext{Namespace: "demo"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !slices.Equal(*seen, []string{"widget"}) {
		t.Fatalf("the widget handler saw the types %v, want [widget]", *seen)
	}
}

// An emitted identity that no declaration names fails the build and names the identity and the
// component whose rule emitted it.
func TestLowering_EmittedUnregisteredIdentityFailsTheBuild(t *testing.T) {
	tr, _ := declared(t)
	tr.RegisterComponentLowering(identityRule{typ: "maker", emits: "ghost.acme.example@v1"})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"maker": mustID(t, "maker.acme.example@v1")}); err != nil {
		t.Fatal(err)
	}
	app := &Application{
		APIVersion: SupportedAPIVersion, Kind: "Application",
		Metadata: Metadata{Name: "shop", Namespace: "demo"},
		Spec:     ApplicationSpec{Components: []Component{{Name: "orders", Type: "maker.acme.example@v1", Properties: map[string]any{}}}},
	}
	_, err := tr.Transform(app, TransformContext{Namespace: "demo"})
	if err == nil {
		t.Fatal("a rule emitted an identity that nothing declares, and the build passed")
	}
	// The settled check refuses an unknown type too, in other words, so the cause must be the
	// resolver's: it names the emitted component and the identity, and wraps the sentinel.
	if !errors.Is(err, ErrTypeReferenceUnknown) {
		t.Errorf("error %q does not wrap ErrTypeReferenceUnknown", err)
	}
	for _, want := range []string{`component "orders-part"`, "ghost.acme.example@v1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A rule that emits a plain name a handler is registered under is dispatched as it was, also when
// two declared identities share that short name: only an identity a rule wrote is resolved.
func TestLowering_EmittedPlainRegisteredNameIsLeftAlone(t *testing.T) {
	tr, seen := declared(t)
	tr.RegisterComponent("widget2", &recordingComponentHandler{typ: "widget2", seen: seen})
	if err := tr.DeclareComponentTypes(map[string]TypeID{"widget2": mustID(t, "widget.other.example@v1")}); err != nil {
		t.Fatal(err)
	}
	tr.RegisterComponentLowering(identityRule{typ: "maker", emits: "widget"})
	app := &Application{
		APIVersion: SupportedAPIVersion, Kind: "Application",
		Metadata: Metadata{Name: "shop", Namespace: "demo"},
		Spec:     ApplicationSpec{Components: []Component{{Name: "orders", Type: "maker", Properties: map[string]any{}}}},
	}
	if _, err := tr.Transform(app, TransformContext{Namespace: "demo"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if !slices.Equal(*seen, []string{"widget"}) {
		t.Fatalf("the widget handler saw the types %v, want [widget]", *seen)
	}
}

// LoweringContext.NamingHook hands a rule the hook of the run, and nil where none is set.
func TestLoweringContext_NamingHook(t *testing.T) {
	for _, tc := range []struct {
		name   string
		naming func(NameRequest) (string, bool)
	}{
		{"none", nil},
		{"set", func(NameRequest) (string, bool) { return "", false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, _ := declared(t)
			var got func(NameRequest) (string, bool)
			tr.RegisterComponentLowering(identityRule{typ: "maker", emits: "widget.acme.example@v1", hook: &got})
			app := &Application{
				APIVersion: SupportedAPIVersion, Kind: "Application",
				Metadata: Metadata{Name: "shop", Namespace: "demo"},
				Spec:     ApplicationSpec{Components: []Component{{Name: "orders", Type: "maker", Properties: map[string]any{}}}},
			}
			if _, err := tr.Transform(app, TransformContext{Namespace: "demo", Naming: tc.naming}); err != nil {
				t.Fatalf("Transform: %v", err)
			}
			if (got != nil) != (tc.naming != nil) {
				t.Fatalf("the rule saw a hook: %v, want %v", got != nil, tc.naming != nil)
			}
		})
	}
}

// LoweringContext.NamingHook on a context with no allocator is nil.
func TestLoweringContext_NamingHookWithoutAllocator(t *testing.T) {
	if (LoweringContext{}).NamingHook() != nil {
		t.Fatal("a context with no allocator holds a hook")
	}
}

// propsRule emits one component of the type emits, with the properties props.
type propsRule struct {
	typ, emits string
	props      map[string]any
}

func (r propsRule) ComponentType() string { return r.typ }
func (r propsRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Components: []Component{{Name: c.Name + "-part", Type: r.emits, Properties: r.props}}}, nil
}

// The checks of an emitted component read the registered name of its type, so a type the rule
// wrote as an identity gets the same checks as a plain name: a platform-reserved property is
// refused, and an unsupported one is refused.
func TestLowering_EmittedIdentityGetsTheEmissionChecks(t *testing.T) {
	for _, tc := range []struct {
		name, emits string
		props       map[string]any
		want        string
	}{
		{"reserved, plain name", "resv", map[string]any{"registry": "evil.example"}, "registry"},
		{"reserved, identity", "resv.acme.example@v1", map[string]any{"registry": "evil.example"}, "registry"},
		{"unsupported, plain name", "resv", map[string]any{"imaeg": "x"}, "imaeg"},
		{"unsupported, identity", "resv.acme.example@v1", map[string]any{"imaeg": "x"}, "imaeg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, _ := declared(t)
			tr.RegisterComponent("resv", reservedComponent{typ: "resv"})
			tr.RegisterComponentLowering(propsRule{typ: "maker", emits: tc.emits, props: tc.props})
			if err := tr.DeclareComponentTypes(map[string]TypeID{
				"resv":  mustID(t, "resv.acme.example@v1"),
				"maker": mustID(t, "maker.acme.example@v1"),
			}); err != nil {
				t.Fatal(err)
			}
			app := &Application{
				APIVersion: SupportedAPIVersion, Kind: "Application",
				Metadata: Metadata{Name: "shop", Namespace: "demo"},
				Spec:     ApplicationSpec{Components: []Component{{Name: "orders", Type: "maker.acme.example@v1", Properties: map[string]any{}}}},
			}
			_, err := tr.Transform(app, TransformContext{Namespace: "demo"})
			if err == nil {
				t.Fatal("a rule emitted a refused property and the build passed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}
