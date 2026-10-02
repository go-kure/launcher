package oam

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// stampTraitHandler is a decorating trait: Apply wraps the application's config
// so that every object it generates gains one more "x" in its "stamp"
// annotation. An object decorated twice therefore reads "xx". With decorates
// false it does not opt in to SubApplicationDecorator's pass. With seal it is
// the "seal" trait instead, adding "s".
type stampTraitHandler struct {
	decorates bool
	seal      bool
}

func (h stampTraitHandler) CanHandle(t string) bool {
	if h.seal {
		return t == "seal"
	}
	return t == "stamp"
}

func (h stampTraitHandler) Apply(_ *Trait, app *stack.Application, _ *stack.Bundle) error {
	mark := "x"
	if h.seal {
		mark = "s"
	}
	app.Config = &stampConfig{inner: app.Config, mark: mark}
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

// reorderTraitHandler is a trait that does more than append to the bundle. Its
// op acts on the application named target, or on the trait's own application
// when target is empty: "move" moves it to the end of the bundle, "replace"
// swaps it for a new one named after it plus "-replaced", "rename" adds
// "-renamed" to its name in place, "remove" drops it, and "" does nothing. With appends set it then adds a sub-application of that
// name.
type reorderTraitHandler struct {
	op      string
	target  string
	appends string
}

func (reorderTraitHandler) CanHandle(t string) bool { return strings.HasPrefix(t, "reorder") }

func (h reorderTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	i := slices.Index(bundle.Applications, app)
	if h.target != "" {
		i = slices.IndexFunc(bundle.Applications, func(a *stack.Application) bool { return a.Name == h.target })
	}
	switch h.op {
	case "move":
		moved := bundle.Applications[i]
		bundle.Applications = append(slices.Delete(bundle.Applications, i, i+1), moved)
	case "replace":
		name := bundle.Applications[i].Name + "-replaced"
		bundle.Applications[i] = stack.NewApplication(name, app.Namespace, &namedConfigMapConfig{name: name, namespace: app.Namespace})
	case "rename":
		// Its own application by pointer: a sibling group member's is not in the
		// bundle.
		renamed := app
		if h.target != "" {
			renamed = bundle.Applications[i]
		}
		renamed.Name += "-renamed"
	case "remove":
		bundle.Applications = slices.Delete(bundle.Applications, i, i+1)
	}
	if h.appends != "" {
		bundle.Applications = append(bundle.Applications,
			stack.NewApplication(h.appends, app.Namespace, &namedConfigMapConfig{name: h.appends, namespace: app.Namespace}))
	}
	return nil
}

// reorderApp is "shop": a webservice web carrying one trait per handler, in
// order, registered as reorder0, reorder1, …, and a webservice other. With
// stamp, web also carries a decorating stamp trait.
func reorderApp(tr *Transformer, handlers []reorderTraitHandler, stamp bool) *Application {
	var traits []Trait
	for i, h := range handlers {
		typ := fmt.Sprintf("reorder%d", i)
		tr.RegisterTrait(typ, h)
		traits = append(traits, Trait{Type: typ})
	}
	if stamp {
		tr.RegisterTrait("stamp", stampTraitHandler{decorates: true})
		traits = append(traits, Trait{Type: "stamp"})
	}
	app := makeApp("shop",
		Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: traits},
		Component{Name: "other", Type: "webservice", Properties: map[string]any{}},
	)
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	return app
}

// TestApplyTraits_KeepsCustomOrder is go-kure/launcher#718: the bundle is
// ordered component by component only when its traits did nothing but append.
// A trait that moved an application or removed a sub-application keeps the
// order it left; one that shortened the bundle must not make the engine index
// past it, nor miss the sub-application it appended after the removal shifted
// the tail (the decorating stamp shows whether the engine saw it as one).
// TestDecorateSubApplications_AnyTraitOrder's flat shape pins the append-only
// case.
func TestApplyTraits_KeepsCustomOrder(t *testing.T) {
	cases := []struct {
		name     string
		handlers []reorderTraitHandler
		stamp    bool
		want     string
	}{
		{name: "move", handlers: []reorderTraitHandler{{op: "move"}}, want: "other=,web="},
		{name: "move-and-append", handlers: []reorderTraitHandler{{op: "move", appends: "web-sub"}}, want: "other=,web=,web-sub="},
		{
			name:     "remove a sub-application",
			handlers: []reorderTraitHandler{{appends: "web-sub"}, {op: "remove", target: "web-sub"}},
			stamp:    true,
			want:     "web=x,other=",
		},
		{
			name:     "remove a sub-application and append",
			handlers: []reorderTraitHandler{{appends: "web-sub"}, {op: "remove", target: "web-sub", appends: "web-sub2"}},
			stamp:    true,
			want:     "web=x,other=,web-sub2=x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := inDocumentTransformer()
			app := reorderApp(tr, tc.handlers, tc.stamp)
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

// TestApplyTraits_RefusesReplacedComponentApplication is
// go-kure/launcher#734: the Phase-4 passes find a component's application by
// its name and then the pointer its entry holds, so a trait that replaces,
// removes or renames one, its own or another component's in the bundle, fails
// the transform instead of silently costing that component its NetworkPolicies
// and health check.
func TestApplyTraits_RefusesReplacedComponentApplication(t *testing.T) {
	const contract = `; a TraitHandler mutates the application it is given and appends sub-applications, it must not replace, remove or rename a component's application`
	replaced := `component "web" trait "reorder0" replaced or removed the application of component %q` + contract
	for name, tc := range map[string]struct {
		handler reorderTraitHandler
		want    string
	}{
		"replace its own":             {reorderTraitHandler{op: "replace"}, fmt.Sprintf(replaced, "web")},
		"remove its own":              {reorderTraitHandler{op: "remove"}, fmt.Sprintf(replaced, "web")},
		"remove its own and append":   {reorderTraitHandler{op: "remove", appends: "web"}, fmt.Sprintf(replaced, "web")},
		"replace another component's": {reorderTraitHandler{op: "replace", target: "other"}, fmt.Sprintf(replaced, "other")},
		"rename its own": {reorderTraitHandler{op: "rename"},
			`component "web" trait "reorder0" renamed the application of component "web" from "web" to "web-renamed"` + contract},
		"rename another component's": {reorderTraitHandler{op: "rename", target: "other"},
			`component "web" trait "reorder0" renamed the application of component "other" from "other" to "other-renamed"` + contract},
	} {
		t.Run(name, func(t *testing.T) {
			tr := inDocumentTransformer()
			app := reorderApp(tr, []reorderTraitHandler{tc.handler}, false)
			_, _, err := tr.TransformWithPolicy(app, TransformContext{})
			want := tc.want
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
	}
}

// TestApplyTraits_RefusesRenameOfEarlierComponent is go-kure/launcher#747: the
// names a component's traits are checked against cover every component of the
// bundle, so a trait on a later component that renames an earlier component's
// application, whose own traits already ran, fails the transform.
func TestApplyTraits_RefusesRenameOfEarlierComponent(t *testing.T) {
	tr := inDocumentTransformer()
	tr.RegisterTrait("reorder0", reorderTraitHandler{})
	tr.RegisterTrait("reorder1", reorderTraitHandler{op: "rename", target: "web"})
	app := makeApp("shop",
		Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{{Type: "reorder0"}}},
		Component{Name: "other", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{{Type: "reorder1"}}},
	)
	app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
	_, _, err := tr.TransformWithPolicy(app, TransformContext{})
	want := `component "other" trait "reorder1" renamed the application of component "web" from "web" to "web-renamed"; ` + entryAppContract
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one containing %q", err, want)
	}
}

// policyOpConfig is a trait sub-application's config whose ApplyPolicy acts on
// the application named target, or on the trait's own application own when
// target is empty: "rename" adds "-renamed" to its name in place, "remove" drops
// it from the bundle, and "" does nothing.
type policyOpConfig struct {
	namedConfigMapConfig
	op, target string
	own        *stack.Application
	bundle     *stack.Bundle
}

func (c *policyOpConfig) ApplyPolicy(Policy) error {
	// By pointer: a sibling group member's application is not in the bundle.
	target := c.own
	if c.target != "" {
		target = c.bundle.Applications[slices.IndexFunc(c.bundle.Applications, func(a *stack.Application) bool { return a.Name == c.target })]
	}
	switch c.op {
	case "rename":
		target.Name += "-renamed"
	case "remove":
		c.bundle.Applications = slices.DeleteFunc(c.bundle.Applications, func(a *stack.Application) bool { return a == target })
	}
	return nil
}

// policyOpTraitHandler appends a sub-application named after the trait's
// application plus "-sub", whose policyOpConfig carries op and target.
type policyOpTraitHandler struct{ op, target string }

func (policyOpTraitHandler) CanHandle(t string) bool { return strings.HasPrefix(t, "policyop") }

func (h policyOpTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	name := app.Name + "-sub"
	bundle.Applications = append(bundle.Applications, stack.NewApplication(name, app.Namespace, &policyOpConfig{
		namedConfigMapConfig: namedConfigMapConfig{name: name, namespace: app.Namespace},
		op:                   h.op, target: h.target, own: app, bundle: bundle,
	}))
	return nil
}

// TestApplyTraits_RefusesPolicyReplacedComponentApplication is
// go-kure/launcher#752: a trait sub-application's ApplyPolicy runs after the
// trait's own check, so it is checked again against the same names. One that
// renames or removes a component's application, its own or an earlier one's,
// fails the transform, naming the trait, its component and the sub-application.
func TestApplyTraits_RefusesPolicyReplacedComponentApplication(t *testing.T) {
	contract := "; " + entryAppPolicyContract
	for name, tc := range map[string]struct {
		web, other policyOpTraitHandler
		want       string
	}{
		"rename its own": {web: policyOpTraitHandler{op: "rename"},
			want: `component "web" trait "policyop0": the ApplyPolicy of sub-application "web-sub" renamed the application of component "web" from "web" to "web-renamed"` + contract},
		"rename an earlier component's": {other: policyOpTraitHandler{op: "rename", target: "web"},
			want: `component "other" trait "policyop1": the ApplyPolicy of sub-application "other-sub" renamed the application of component "web" from "web" to "web-renamed"` + contract},
		// On the last component no later trait's check would see the removal.
		"remove its own": {other: policyOpTraitHandler{op: "remove"},
			want: `component "other" trait "policyop1": the ApplyPolicy of sub-application "other-sub" replaced or removed the application of component "other"` + contract},
		"no-op": {},
	} {
		t.Run(name, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait("policyop0", tc.web)
			tr.RegisterTrait("policyop1", tc.other)
			app := makeApp("shop",
				Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{{Type: "policyop0"}}},
				Component{Name: "other", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{{Type: "policyop1"}}},
			)
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			_, _, err := tr.TransformWithPolicy(app, TransformContext{})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("TransformWithPolicy: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
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

// TestApplyTraits_RefusesRenamedSiblingMember is go-kure/launcher#752: a trait
// on a sibling group runs on a member's application, which the bundle does not
// hold (the group's does), so a member is checked by its name. A trait or a
// trait sub-application's policy that renames it fails the transform: the
// member would generate its objects under a name the group's health check does
// not name.
func TestApplyTraits_RefusesRenamedSiblingMember(t *testing.T) {
	const member = `renamed the application of member "webservice" of sibling group "web" from "web" to "web-renamed"; `
	for name, tc := range map[string]struct {
		typ     string
		handler TraitHandler
		want    string
	}{
		"trait renames its member": {"reorder0", reorderTraitHandler{op: "rename"},
			`component "web" trait "reorder0" ` + member + entryAppContract},
		"policy renames a member": {"policyop0", policyOpTraitHandler{op: "rename"},
			`component "web" trait "policyop0": the ApplyPolicy of sub-application "web-sub" ` + member + entryAppPolicyContract},
		"no-op trait":  {"reorder0", reorderTraitHandler{}, ""},
		"no-op policy": {"policyop0", policyOpTraitHandler{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait(tc.typ, tc.handler)
			tr.RegisterComponentLowering(pairRule{})
			app := makeApp("shop", Component{Name: "web", Type: "pair", Properties: map[string]any{}, Traits: []Trait{{Type: tc.typ}}})
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			_, _, err := tr.TransformWithPolicy(app, TransformContext{})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("TransformWithPolicy: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
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

// bundleEditTraitHandler is a decorating trait that edits the bundle. Applied to
// its component's application in the trait pass, it appends the sub-application
// "<name>-sub". Applied to that sub-application in the decoration pass, it makes
// the change named by edit to the bundle.
type bundleEditTraitHandler struct {
	edit string // "append", "remove-and-append", "replace", "rename" or "reorder"
}

func (bundleEditTraitHandler) CanHandle(t string) bool { return t == "edit" }

func (h bundleEditTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	newApp := func(name string) *stack.Application {
		return stack.NewApplication(name, app.Namespace, &namedConfigMapConfig{name: name, namespace: app.Namespace})
	}
	if !strings.HasSuffix(app.Name, "-sub") {
		bundle.Applications = append(bundle.Applications, newApp(app.Name+"-sub"))
		return nil
	}
	i := slices.Index(bundle.Applications, app)
	switch h.edit {
	case "append":
		bundle.Applications = append(bundle.Applications, newApp(app.Name+"-extra"))
	case "remove-and-append":
		// The length is unchanged: a length check alone would pass this.
		bundle.Applications = append(slices.Delete(bundle.Applications, 0, 1), newApp(app.Name+"-extra"))
	case "replace":
		bundle.Applications[i] = newApp(app.Name)
	case "rename":
		// The component's application, after its checks and policies were named.
		owner := strings.TrimSuffix(app.Name, "-sub")
		bundle.Applications[slices.IndexFunc(bundle.Applications, func(a *stack.Application) bool { return a.Name == owner })].Name += "-renamed"
	case "reorder":
		bundle.Applications = append([]*stack.Application{app}, slices.Delete(bundle.Applications, i, i+1)...)
	}
	return nil
}

func (bundleEditTraitHandler) DecoratesSubApplications() bool { return true }

// TestDecorateSubApplications_RefusesBundleChange is go-kure/launcher#723. A
// decorating trait that changes the bundle's applications while decorating a
// sub-application is refused. The pass runs after the build's other steps, so
// they would never see an added application, and the order is already final.
// A removal followed by an append leaves the length unchanged, and is refused
// all the same. So is a rename (go-kure/launcher#734): the health check and
// NetworkPolicies already carry the name.
func TestDecorateSubApplications_RefusesBundleChange(t *testing.T) {
	for _, edit := range []string{"append", "remove-and-append", "replace", "rename", "reorder"} {
		t.Run(edit, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait("edit", bundleEditTraitHandler{edit: edit})
			app := makeApp("shop", Component{Name: "web", Type: "webservice", Properties: map[string]any{}, Traits: []Trait{
				{Type: "edit"},
			}})
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			_, _, err := tr.TransformWithPolicy(app, TransformContext{})
			want := `component "web" trait "edit" changed the bundle's applications while decorating sub-application "web-sub"; a SubApplicationDecorator must not add, remove, replace, rename or reorder applications`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to contain %s", err, want)
			}
		})
	}
}

// memberRenameTraitHandler is a decorating trait that keeps the application it
// is applied to in the trait pass, a sibling group member's, and appends the
// sub-application "<name>-sub". Applied to that sub-application in the
// decoration pass, it renames the kept application when rename is set.
type memberRenameTraitHandler struct {
	rename bool
	kept   *stack.Application
}

func (*memberRenameTraitHandler) CanHandle(t string) bool { return t == "memberrename" }

func (h *memberRenameTraitHandler) Apply(_ *Trait, app *stack.Application, bundle *stack.Bundle) error {
	if h.kept == nil {
		h.kept = app
		name := app.Name + "-sub"
		bundle.Applications = append(bundle.Applications, stack.NewApplication(name, app.Namespace, &namedConfigMapConfig{name: name, namespace: app.Namespace}))
		return nil
	}
	if h.rename {
		h.kept.Name += "-renamed"
	}
	return nil
}

func (*memberRenameTraitHandler) DecoratesSubApplications() bool { return true }

// TestDecorateSubApplications_RefusesRenamedSiblingMember is
// go-kure/launcher#763: the bundle holds a sibling group's application, not its
// members', so the decoration pass's bundle comparison cannot see a member
// renamed. A decorator that kept a member's application from the trait pass and
// renames it while decorating fails the transform, as a trait or a policy that
// renames one does (go-kure/launcher#752).
func TestDecorateSubApplications_RefusesRenamedSiblingMember(t *testing.T) {
	for name, tc := range map[string]struct {
		rename bool
		want   string
	}{
		"renames a member": {true, `component "web" trait "memberrename" while decorating sub-application "web-sub" renamed the application of member "webservice" of sibling group "web" from "web" to "web-renamed"; ` + subAppDecoratorContract},
		"no-op":            {false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			tr := inDocumentTransformer()
			tr.RegisterTrait("memberrename", &memberRenameTraitHandler{rename: tc.rename})
			tr.RegisterComponentLowering(pairRule{})
			app := makeApp("shop", Component{Name: "web", Type: "pair", Properties: map[string]any{}, Traits: []Trait{{Type: "memberrename"}}})
			app.APIVersion, app.Kind = SupportedAPIVersion, terminalDocumentKind
			_, _, err := tr.TransformWithPolicy(app, TransformContext{})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("TransformWithPolicy: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
