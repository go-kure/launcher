package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// stampTraitHandler is a decorating trait: Apply wraps the application's config
// so that every object it generates gains one more "x" in its "stamp"
// annotation. An object decorated twice therefore reads "xx". With decorates
// false it does not opt in to SubApplicationDecorator's pass; with appends it
// also adds a sub-application on every Apply. With seal it is the "seal" trait
// instead, adding "s".
type stampTraitHandler struct {
	decorates bool
	appends   bool
	seal      bool
}

func (h stampTraitHandler) CanHandle(t string) bool {
	if h.seal {
		return t == "seal"
	}
	return t == "stamp"
}

func (h stampTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	mark := "x"
	if h.seal {
		mark = "s"
	}
	app.Config = &stampConfig{inner: app.Config, mark: mark}
	if h.appends {
		bundle.Applications = append(bundle.Applications,
			stack.NewApplication(app.Name+"-stamp", app.Namespace, &namedConfigMapConfig{name: app.Name + "-stamp", namespace: app.Namespace}))
	}
	return nil
}

func (h stampTraitHandler) DecoratesSubApplications() bool { return h.decorates }

type stampConfig struct {
	inner stack.ApplicationConfig
	mark  string
}

func (c *stampConfig) Generate(app *stack.Application) ([]*client.Object, error) {
	objs, err := c.inner.Generate(app)
	if err != nil {
		return nil, err
	}
	for _, p := range objs {
		ann := (*p).GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann["stamp"] += c.mark
		(*p).SetAnnotations(ann)
	}
	return objs, nil
}

// stamps returns "name=stamp" for every generated object, in generation order.
func stamps(t *testing.T, cluster *stack.Cluster) []string {
	t.Helper()
	apps, err := GenerateApplications(cluster)
	if err != nil {
		t.Fatalf("GenerateApplications: %v", err)
	}
	var got []string
	for _, a := range apps {
		for _, p := range a.Objects {
			got = append(got, (*p).GetName()+"="+(*p).GetAnnotations()["stamp"])
		}
	}
	return got
}

// TestDecorateSubApplications_AnyTraitOrder is go-kure/launcher#712: a decorating
// trait covers the sub-applications of its own component created by traits
// authored before it and after it, once each, and no other component's, on each
// of the three cluster shapes. On the flat shape it also pins the order: each
// component's sub-applications follow it, not every component.
func TestDecorateSubApplications_AnyTraitOrder(t *testing.T) {
	shapes := []struct {
		name       string
		second     string
		dependency bool
		wantOrder  []string
	}{
		{name: "flat", second: "webservice", wantOrder: []string{"web=x", "before=x", "after=x", "other=", "other-settings="}},
		{name: "hierarchical", second: "daemonset"},
		{name: "dependency-aware", second: "webservice", dependency: true},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
			web := Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{
				{Type: "settings", Properties: map[string]any{"name": "before"}},
				{Type: "stamp"},
				{Type: "settings", Properties: map[string]any{"name": "after"}},
			}}
			other := Component{Name: "other", Type: shape.second, Properties: map[string]any{}, Traits: []Trait{
				{Type: "settings", Properties: map[string]any{"name": "other-settings"}},
			}}
			app := makeApp("shop", web, other)
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			if shape.dependency {
				tr.RegisterPolicy("dependency", &depWritingPolicyHandler{from: "web", to: "other"})
				app.Spec.Policies = []ApplicationPolicy{{Name: "order", Type: "dependency"}}
			}
			cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			got := stamps(t, cluster)
			if shape.wantOrder != nil {
				if strings.Join(got, ",") != strings.Join(shape.wantOrder, ",") {
					t.Errorf("objects = %q, want %q", got, shape.wantOrder)
				}
				return
			}
			want := map[string]bool{"web=x": true, "before=x": true, "after=x": true, "other=": true, "other-settings=": true}
			if len(got) != len(want) {
				t.Fatalf("objects = %q, want %d", got, len(want))
			}
			for _, g := range got {
				if !want[g] {
					t.Errorf("unexpected object %q in %q", g, got)
				}
			}
		})
	}
}

// reorderTraitHandler is a trait that does more than append to the bundle:
// with replace it swaps its application for a new one named after it plus
// "-replaced", with remove it drops its application, otherwise it moves its
// application to the end of the bundle; with appends it then adds a
// sub-application.
type reorderTraitHandler struct {
	replace bool
	remove  bool
	appends bool
}

func (reorderTraitHandler) CanHandle(t string) bool { return t == "reorder" }

func (h reorderTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	i := slices.Index(bundle.Applications, app)
	switch {
	case h.replace:
		name := app.Name + "-replaced"
		bundle.Applications[i] = stack.NewApplication(name, app.Namespace, &namedConfigMapConfig{name: name, namespace: app.Namespace})
	case h.remove:
		bundle.Applications = slices.Delete(bundle.Applications, i, i+1)
	default:
		bundle.Applications = append(slices.Delete(bundle.Applications, i, i+1), app)
	}
	if h.appends {
		bundle.Applications = append(bundle.Applications,
			stack.NewApplication(app.Name+"-sub", app.Namespace, &namedConfigMapConfig{name: app.Name + "-sub", namespace: app.Namespace}))
	}
	return nil
}

// TestApplyTraits_KeepsCustomOrder is go-kure/launcher#718: the bundle is
// ordered component by component only when its traits did nothing but append.
// A trait that moved, replaced or removed an application keeps the order it
// left; one that shortened the bundle must not make the engine index past it,
// nor miss the sub-application it appended after the removal shifted the tail
// (the decorating stamp shows whether the engine saw it as one).
// TestDecorateSubApplications_AnyTraitOrder's flat shape pins the append-only
// case.
func TestApplyTraits_KeepsCustomOrder(t *testing.T) {
	cases := []struct {
		name    string
		handler reorderTraitHandler
		stamp   bool
		want    string
	}{
		{name: "move", want: "other=,web="},
		{name: "move-and-append", handler: reorderTraitHandler{appends: true}, want: "other=,web=,web-sub="},
		{name: "replace", handler: reorderTraitHandler{replace: true}, want: "web-replaced=,other="},
		{name: "remove", handler: reorderTraitHandler{remove: true}, want: "other="},
		{name: "remove-and-append", handler: reorderTraitHandler{remove: true, appends: true}, stamp: true, want: "other=,web-sub=x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait("reorder", tc.handler)
			traits := []Trait{{Type: "reorder"}}
			if tc.stamp {
				tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
				traits = append(traits, Trait{Type: "stamp"})
			}
			app := makeApp("shop",
				Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: traits},
				Component{Name: "other", Type: "webservice", Properties: map[string]any{}},
			)
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
			if err != nil {
				t.Fatalf("TransformWithPolicy: %v", err)
			}
			if got := strings.Join(stamps(t, cluster), ","); got != tc.want {
				t.Errorf("objects = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDecorateSubApplications_OptIn pins that a trait handler which does not
// answer true to DecoratesSubApplications keeps the narrow scope.
func TestDecorateSubApplications_OptIn(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("stamp", stampTraitHandler{})
	app := makeApp("shop", Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{
		{Type: "settings", Properties: map[string]any{"name": "before"}},
		{Type: "stamp"},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if got, want := strings.Join(stamps(t, cluster), ","), "web=x,before="; got != want {
		t.Errorf("objects = %s, want %s", got, want)
	}
}

// pairRule lowers a "pair" component into a same-name sibling group of a
// webservice and a statefulset. It forwards every authored trait to the
// webservice and only the stamp and split traits to the statefulset too, as the
// builtin webservice rule forwards prune-protection and force-replace to both
// members.
type pairRule struct{}

func (pairRule) ComponentType() string { return "pair" }

func (pairRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	var stampsOnly []Trait
	for _, tr := range comp.Traits {
		if tr.Type == "stamp" || tr.Type == "split" {
			stampsOnly = append(stampsOnly, tr)
		}
	}
	return LoweringResult{Components: []Component{
		{Name: comp.Name, Type: "webservice", Properties: map[string]any{"configMap": comp.Name + "-a"}, Traits: append([]Trait(nil), comp.Traits...)},
		{Name: comp.Name, Type: "statefulset", Properties: map[string]any{"configMap": comp.Name + "-b"}, Traits: stampsOnly},
	}}, nil
}

// TestDecorateSubApplications_SiblingGroupOnce pins that a decorating trait a
// rule forwarded to both members of a sibling group decorates the group's
// sub-applications once, not once per member.
func TestDecorateSubApplications_SiblingGroupOnce(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
	tr.RegisterComponentLowering(pairRule{})
	app := makeApp("shop", Component{Name: "web", Type: "pair", Properties: map[string]any{}, Traits: []Trait{
		{Type: "settings", Properties: map[string]any{"name": "settings"}},
		{Type: "stamp"},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if got, want := strings.Join(stamps(t, cluster), ","), "web-a=x,web-b=x,settings=x"; got != want {
		t.Errorf("objects = %s, want %s", got, want)
	}
}

// guardRule lowers a "guard" trait into a stamp and a seal trait, which both keep
// the guard's authored slot.
type guardRule struct{}

func (guardRule) TraitType() string { return "guard" }

func (guardRule) LowerTrait(_ *Trait, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "stamp"}, {Type: "seal"}}}, nil
}

// TestDecorateSubApplications_TwoDecoratorsOneSlot pins that two decorating
// traits a trait rule lowered from one forwarded trait both decorate the
// group's sub-applications: they share an authored slot but are different
// traits.
func TestDecorateSubApplications_TwoDecoratorsOneSlot(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
	tr.RegisterTrait("seal", stampTraitHandler{decorates: true, seal: true})
	tr.RegisterTraitLowering(guardRule{})
	tr.RegisterComponentLowering(pairRule{})
	app := makeApp("shop", Component{Name: "web", Type: "pair", Properties: map[string]any{}, Traits: []Trait{
		{Type: "settings", Properties: map[string]any{"name": "settings"}},
		{Type: "guard"},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if got, want := strings.Join(stamps(t, cluster), ","), "web-a=xs,web-b=,settings=xs"; got != want {
		t.Errorf("objects = %s, want %s", got, want)
	}
}

// splitRule lowers a "split" trait into a stamp on a webservice member and a
// seal on any other member, so one forwarded trait becomes two decorating
// traits of different types on two members, both in the split's authored slot.
type splitRule struct{}

func (splitRule) TraitType() string { return "split" }

func (splitRule) LowerTrait(_ *Trait, ctx LoweringContext) (LoweringResult, error) {
	if ctx.Component != nil && ctx.Component.Type == "webservice" {
		return LoweringResult{Traits: []Trait{{Type: "stamp"}}}, nil
	}
	return LoweringResult{Traits: []Trait{{Type: "seal"}}}, nil
}

// TestDecorateSubApplications_TwoTypesTwoMembers pins that the sub-application
// dedupe key names the trait type (go-kure/launcher#718): the seal on the
// statefulset member shares the stamp's authored slot but is a different
// trait, so it is not skipped as the webservice member's copy.
func TestDecorateSubApplications_TwoTypesTwoMembers(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
	tr.RegisterTrait("seal", stampTraitHandler{decorates: true, seal: true})
	tr.RegisterTraitLowering(splitRule{})
	tr.RegisterComponentLowering(pairRule{})
	app := makeApp("shop", Component{Name: "web", Type: "pair", Properties: map[string]any{}, Traits: []Trait{
		{Type: "settings", Properties: map[string]any{"name": "settings"}},
		{Type: "split"},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	cluster, _, err := tr.TransformWithPolicy(app, TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if got, want := strings.Join(stamps(t, cluster), ","), "web-a=x,web-b=s,settings=xs"; got != want {
		t.Errorf("objects = %s, want %s", got, want)
	}
}

// TestDecorateSubApplications_RefusesAppend pins that a decorating trait which
// adds an application while decorating a sub-application is refused: the pass
// runs after the build's other steps, which would never see that application.
func TestDecorateSubApplications_RefusesAppend(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("stamp", stampTraitHandler{decorates: true, appends: true})
	app := makeApp("shop", Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{
		{Type: "stamp"},
	}})
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	_, _, err := tr.TransformWithPolicy(app, TransformContext{})
	want := `component "web" trait "stamp" added an application while decorating sub-application "web-stamp"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %s", err, want)
	}
}
