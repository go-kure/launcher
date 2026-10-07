package oam

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-kure/launcher/pkg/oam/netpol"
)

// bundleNamesTransformer has component type "a" and the "named", "sub",
// "renamesub", "mixedsub", "authoredsub", "renameother", "childbundle",
// "renamesibling", "twoauthored", "policyrename" and "renamethenown" traits.
func bundleNamesTransformer() *Transformer {
	tr := siblingTransformer(stubHandler("a", 0))
	tr.RegisterTrait("named", namedTrait{})
	tr.RegisterTrait("sub", subAppTrait{})
	tr.RegisterTrait("renamesub", renamingSubTrait{})
	tr.RegisterTrait("mixedsub", mixedSubTrait{})
	tr.RegisterTrait("authoredsub", authoredSubTrait{})
	tr.RegisterTrait("renameother", renameOtherTrait{})
	tr.RegisterTrait("childbundle", childBundleTrait{})
	tr.RegisterTrait("renamesibling", renameSiblingTrait{})
	tr.RegisterTrait("twoauthored", twoAuthoredTrait{})
	tr.RegisterTrait("policyrename", policyRenameTrait{})
	tr.RegisterTrait("renamethenown", renameThenOwnTrait{})
	return tr
}

// renamingOtherStub is a sub-application whose ApplyPolicy renames another.
type renamingOtherStub struct {
	siblingStub
	other *stack.Application
	to    string
}

func (s *renamingOtherStub) ApplyPolicy(Policy) error {
	s.other.Name = s.to
	return nil
}

// renameSiblingTrait adds sub-application "first", which has no policy, and a
// second whose ApplyPolicy renames the first to "to".
type renameSiblingTrait struct{}

func (renameSiblingTrait) CanHandle(t string) bool { return t == "renamesibling" }
func (renameSiblingTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	first := stack.NewApplication(trait.Properties["first"].(string), app.Namespace, &siblingStub{})
	second := stack.NewApplication(app.Name+"-second", app.Namespace, nil)
	second.SetConfig(&renamingOtherStub{other: first, to: trait.Properties["to"].(string)})
	b.Applications = append(b.Applications, first, second)
	return nil
}

// renameThenOwnTrait adds sub-application "first", whose ApplyPolicy renames
// the second to "middle", then "second", whose own ApplyPolicy renames it to
// "to": its own policy runs after the other's.
type renameThenOwnTrait struct{}

func (renameThenOwnTrait) CanHandle(t string) bool { return t == "renamethenown" }
func (renameThenOwnTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	second := stack.NewApplication(trait.Properties["second"].(string), app.Namespace, nil)
	second.SetConfig(&renamingSubStub{own: second, to: trait.Properties["to"].(string)})
	first := stack.NewApplication(app.Name+"-first", app.Namespace, nil)
	first.SetConfig(&renamingOtherStub{other: second, to: trait.Properties["middle"].(string)})
	b.Applications = append(b.Applications, first, second)
	return nil
}

// bundleRenamingStub is a sub-application whose ApplyPolicy renames its
// bundle's application "from" to "to".
type bundleRenamingStub struct {
	siblingStub
	bundle   *stack.Bundle
	from, to string
}

func (s *bundleRenamingStub) ApplyPolicy(Policy) error {
	for _, a := range s.bundle.Applications {
		if a.Name == s.from {
			a.Name = s.to
		}
	}
	return nil
}

// policyRenameTrait adds sub-application "sub", whose ApplyPolicy renames the
// bundle's application "from" to "to": one an earlier trait added.
type policyRenameTrait struct{}

func (policyRenameTrait) CanHandle(t string) bool { return t == "policyrename" }
func (policyRenameTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	sub := stack.NewApplication(trait.Properties["sub"].(string), app.Namespace, nil)
	sub.SetConfig(&bundleRenamingStub{bundle: b, from: trait.Properties["from"].(string), to: trait.Properties["to"].(string)})
	b.Applications = append(b.Applications, sub)
	return nil
}

// twoAuthoredTrait adds two sub-applications under the name in "name", each
// resolved from a property of its own.
type twoAuthoredTrait struct{}

func (twoAuthoredTrait) CanHandle(t string) bool { return t == "twoauthored" }
func (twoAuthoredTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	name := trait.Properties["name"].(string)
	for _, property := range []string{"first", "second"} {
		sub, err := trait.ResolveName(NameSpec{Role: NameRoleSubApplication, Default: app.Name + "-" + property, Property: property, Authored: name})
		if err != nil {
			return err
		}
		b.Applications = append(b.Applications, stack.NewApplication(sub, app.Namespace, &siblingStub{}))
	}
	return nil
}

// authoredSubTrait adds one sub-application under the name the author wrote in
// its "name" property.
type authoredSubTrait struct{}

func (authoredSubTrait) CanHandle(t string) bool { return t == "authoredsub" }
func (authoredSubTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	name := trait.Properties["name"].(string)
	sub, err := trait.ResolveName(NameSpec{Role: NameRoleSubApplication, Default: app.Name + "-sub", Property: "name", Authored: name})
	if err != nil {
		return err
	}
	b.Applications = append(b.Applications, stack.NewApplication(sub, app.Namespace, &siblingStub{}))
	return nil
}

// renameOtherTrait renames the bundle's application "from" to "to": a
// sub-application an earlier trait added.
type renameOtherTrait struct{}

func (renameOtherTrait) CanHandle(t string) bool { return t == "renameother" }
func (renameOtherTrait) Apply(trait *Trait, _ *stack.Application, b *stack.Bundle) error {
	for _, a := range b.Applications {
		if a.Name == trait.Properties["from"].(string) {
			a.Name = trait.Properties["to"].(string)
		}
	}
	return nil
}

// childBundleTrait adds a child bundle holding two applications named "dup".
type childBundleTrait struct{}

func (childBundleTrait) CanHandle(t string) bool { return t == "childbundle" }
func (childBundleTrait) Apply(_ *Trait, app *stack.Application, b *stack.Bundle) error {
	child := &stack.Bundle{Name: "child"}
	child.Applications = []*stack.Application{
		stack.NewApplication("dup", app.Namespace, &siblingStub{}),
		stack.NewApplication("dup", app.Namespace, &siblingStub{}),
	}
	b.Children = append(b.Children, child)
	return nil
}

// Two applications of one name in one bundle are refused once the bundle's
// traits and their sub-applications' policies have run, naming both and where
// each name came from (go-kure/launcher#787).
func TestBundleApplicationNames(t *testing.T) {
	const prefix = `bundle "app": name collision: application `
	for _, tc := range []struct {
		name  string
		comps []Component
		want  string
	}{
		{
			name: "a sub-application named after another component",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{named("", "api")}},
				{Name: "api", Type: "a"},
			},
			want: prefix + `"api" is named by component "api" (its application) and by ` +
				`component "web" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			name: "a sub-application its trait named without resolving it",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "sub", Properties: map[string]any{}}}},
				{Name: "web-sub", Type: "a"},
			},
			want: prefix + `"web-sub" is named by component "web-sub" (its application) and by ` +
				`component "web" traits[0] "sub" (role "sub-application", set by the trait without resolving it)`,
		},
		{
			name: "two traits' sub-applications of two components",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{named("", "dup")}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "dup")}},
			},
			want: prefix + `"dup" is named by component "web" traits[0] "named" (role "sub-application", its default) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			name: "two traits' sub-applications of one component",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{named("", "dup"), named("", "dup")}},
			},
			want: prefix + `"dup" is named by component "web" traits[0] "named" (role "sub-application", its default) and by ` +
				`component "web" traits[1] "named" (role "sub-application", its default)`,
		},
		{
			name: "an authored sub-application name",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "authoredsub", Properties: map[string]any{"name": "web-config"}}}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "web-config")}},
			},
			want: prefix + `"web-config" is named by component "web" traits[0] "authoredsub" (role "sub-application", set by name) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			// The trait resolved "web-config" once and added two of that name:
			// which of the two the resolution named is not known.
			name: "a sub-application of a name its trait resolved for one of two",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "mixedsub", Properties: map[string]any{"first": "unresolved", "name": "web-config"}}}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "web-config")}},
			},
			want: prefix + `"web-config" is named by component "web" traits[0] "mixedsub" (role "sub-application", set by the trait, ` +
				`which resolved that name in more than one way: which one named this is not known) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			// The trait resolved "web-config" twice, authored and by default:
			// which of its two sub-applications got which is not known.
			name: "a sub-application of a name its trait resolved two ways",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "mixedsub", Properties: map[string]any{"first": "authored", "name": "web-config"}}}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "web-config")}},
			},
			want: prefix + `"web-config" is named by component "web" traits[0] "mixedsub" (role "sub-application", set by the trait, ` +
				`which resolved that name in more than one way: which one named this is not known) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			name: "a later trait renaming an earlier one's sub-application",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{named("", "web-original"), {Type: "renameother", Properties: map[string]any{"from": "web-original", "to": "api"}}}},
				{Name: "api", Type: "a"},
			},
			want: prefix + `"api" is named by component "api" (its application) and by ` +
				`component "web" traits[0] "named" (role "sub-application", renamed from "web-original" after its trait named it)`,
		},
		{
			// Its policy renamed it away, and a later trait back: the policy's
			// name is the last it is known to have had.
			name: "a later trait renaming a sub-application back to its first name",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{
					{Type: "renamesub", Properties: map[string]any{"name": "api", "to": "web-moved"}},
					{Type: "renameother", Properties: map[string]any{"from": "web-moved", "to": "api"}},
				}},
				{Name: "api", Type: "a"},
			},
			want: prefix + `"api" is named by component "api" (its application) and by ` +
				`component "web" traits[0] "renamesub" (role "sub-application", renamed from "web-moved" after its trait named it)`,
		},
		{
			// The second sub-application's policy renames the first, which has
			// no policy of its own.
			name: "another sub-application's policy renaming one",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "renamesibling", Properties: map[string]any{"first": "web-first", "to": "api"}}}},
				{Name: "api", Type: "a"},
			},
			want: prefix + `"api" is named by component "api" (its application) and by ` +
				`component "web" traits[0] "renamesibling" (role "sub-application", renamed from "web-first" after its trait named it)`,
		},
		{
			// Two authored properties gave one name: which property named which
			// sub-application is not known.
			name: "a sub-application of a name two of its trait's properties set",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "twoauthored", Properties: map[string]any{"name": "dup"}}}},
			},
			want: prefix + `"dup" is named twice by component "web" traits[0] "twoauthored" (role "sub-application", set by the trait, ` +
				`which resolved that name in more than one way: which one named this is not known)`,
		},
		{
			name: "a policy renaming a sub-application onto another's name",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{{Type: "renamesub", Properties: map[string]any{"name": "web-other", "to": "api-sub"}}}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "api-sub")}},
			},
			want: prefix + `"api-sub" is named by component "web" traits[0] "renamesub" (role "sub-application", renamed from "web-other" by its ApplyPolicy) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			// Its own policy renames it; two later traits' policies rename it
			// away and back to that name: not its own policy's rename any more.
			name: "later traits' policies renaming a sub-application back to its policy's name",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{
					{Type: "renamesub", Properties: map[string]any{"name": "web-other", "to": "api-sub"}},
					{Type: "policyrename", Properties: map[string]any{"sub": "web-p1", "from": "api-sub", "to": "web-mid"}},
					{Type: "policyrename", Properties: map[string]any{"sub": "web-p2", "from": "web-mid", "to": "api-sub"}},
				}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "api-sub")}},
			},
			want: prefix + `"api-sub" is named by component "web" traits[0] "renamesub" (role "sub-application", renamed away and back to the name its trait or policy gave it) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			// The same, by two later traits' Apply instead of their policies.
			name: "later traits renaming a sub-application back to its policy's name",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{
					{Type: "renamesub", Properties: map[string]any{"name": "web-other", "to": "api-sub"}},
					{Type: "renameother", Properties: map[string]any{"from": "api-sub", "to": "web-mid"}},
					{Type: "renameother", Properties: map[string]any{"from": "web-mid", "to": "api-sub"}},
				}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "api-sub")}},
			},
			want: prefix + `"api-sub" is named by component "web" traits[0] "renamesub" (role "sub-application", renamed away and back to the name its trait or policy gave it) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
		{
			// Another's policy renames it, then its own policy, then a later
			// trait: reported from the name its own policy gave it.
			name: "a later trait renaming a sub-application its own policy renamed after another's",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{
					{Type: "renamethenown", Properties: map[string]any{"second": "web-original", "middle": "web-middle", "to": "web-own"}},
					{Type: "renameother", Properties: map[string]any{"from": "web-own", "to": "api"}},
				}},
				{Name: "api", Type: "a"},
			},
			want: prefix + `"api" is named by component "api" (its application) and by ` +
				`component "web" traits[0] "renamethenown" (role "sub-application", renamed from "web-own" after its trait named it)`,
		},
		{
			// Renamed away and back to the name its trait gave it: no earlier
			// name to report.
			name: "later traits renaming a sub-application away and back",
			comps: []Component{
				{Name: "web", Type: "a", Traits: []Trait{
					named("", "api-sub"),
					{Type: "renameother", Properties: map[string]any{"from": "api-sub", "to": "web-mid"}},
					{Type: "renameother", Properties: map[string]any{"from": "web-mid", "to": "api-sub"}},
				}},
				{Name: "api", Type: "a", Traits: []Trait{named("", "api-sub")}},
			},
			want: prefix + `"api-sub" is named by component "web" traits[0] "named" (role "sub-application", renamed away and back to the name its trait or policy gave it) and by ` +
				`component "api" traits[0] "named" (role "sub-application", its default)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := bundleNamesTransformer().TransformWithPolicy(siblingDoc(tc.comps...), TransformContext{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
			if !errors.Is(err, ErrNameCollision) {
				t.Errorf("errors.Is(err, ErrNameCollision) = false for %v", err)
			}
		})
	}
}

// The refusal is a *NameCollisionError naming no kind: the members say which
// is a sub-application, and a component's own application has no role.
func TestBundleApplicationNames_CollisionError(t *testing.T) {
	_, _, err := bundleNamesTransformer().TransformWithPolicy(siblingDoc(
		Component{Name: "web", Type: "a", Traits: []Trait{named("", "api")}},
		Component{Name: "api", Type: "a"},
	), TransformContext{})
	var got *NameCollisionError
	if !errors.As(err, &got) {
		t.Fatalf("err = %v, want a *NameCollisionError", err)
	}
	if got.Kind.Kind != "" || got.Name != "api" {
		t.Errorf("Kind, Name = %q, %q, want no kind and %q", got.Kind, got.Name, "api")
	}
	if got.First.Component != "api" || got.First.Role != "" || got.First.Trait != "" {
		t.Errorf("First = %+v, want component api's application", got.First)
	}
	if got.Second.Component != "web" || got.Second.Role != NameRoleSubApplication || got.Second.Trait != "named" {
		t.Errorf("Second = %+v, want component web's named trait sub-application", got.Second)
	}
}

// Two sub-applications of one name in two bundles are two applications: each
// tier is its own bundle.
func TestBundleApplicationNames_TwoBundlesAccepted(t *testing.T) {
	_, _, err := bundleNamesTransformer().TransformWithPolicy(siblingDoc(
		inTier(Component{Name: "web", Type: "a", Traits: []Trait{named("", "dup")}}, TierApps),
		inTier(Component{Name: "api", Type: "a", Traits: []Trait{named("", "dup")}}, TierInfra),
	), TransformContext{})
	if err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
}

// A synthesized NetworkPolicy's sub-application is named after the bundle's
// traits have run: it is held to the bundle's other applications all the same.
func TestBundleApplicationNames_SynthesizedPolicy(t *testing.T) {
	ctx := TransformContext{EgressPeers: map[string][]netpol.EgressPeer{"web": {{
		Namespace:   "data",
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "pg"}},
		Ports:       []intstr.IntOrString{intstr.FromInt32(5432)},
	}}}}
	_, _, err := bundleNamesTransformer().TransformWithPolicy(siblingDoc(
		Component{Name: "web", Type: "a", Traits: []Trait{named("", "web-allow-egress-traffic")}},
	), ctx)
	const want = `name collision: application "web-allow-egress-traffic" is named by ` +
		`component "web" traits[0] "named" (role "sub-application", its default) and by ` +
		`component "web" (role "sub-application", its default)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %q", err, want)
	}
	if !errors.Is(err, ErrNameCollision) {
		t.Errorf("errors.Is(err, ErrNameCollision) = false for %v", err)
	}
}

// A bundle a trait adds as a child is a bundle of the transform: two
// applications of one name in it are refused.
func TestBundleApplicationNames_ChildBundle(t *testing.T) {
	_, _, err := bundleNamesTransformer().TransformWithPolicy(siblingDoc(
		Component{Name: "web", Type: "a", Traits: []Trait{{Type: "childbundle", Properties: map[string]any{}}}},
	), TransformContext{})
	const want = `bundle "child": name collision: application "dup" is named twice by ` +
		`the application (role "sub-application", named where the transform records no namer)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v\nwant one containing %q", err, want)
	}
}
