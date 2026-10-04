package oam

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
)

// namedTrait resolves its names through the trait it is handed, as a builtin
// trait does: the HorizontalPodAutoscaler name in property "object" when it has
// one, and the sub-application name in property "sub", which it then appends.
type namedTrait struct{}

func (namedTrait) CanHandle(t string) bool { return t == "named" }
func (namedTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	if object, _ := trait.Properties["object"].(string); object != "" {
		if _, err := trait.ResolveName(hpaSpec(object)); err != nil {
			return err
		}
	}
	sub, err := trait.ResolveName(NameSpec{Role: NameRoleSubApplication, Default: trait.Properties["sub"].(string)})
	if err != nil {
		return err
	}
	b.Applications = append(b.Applications, stack.NewApplication(sub, app.Namespace, &siblingStub{}))
	return nil
}

func named(object, sub string) Trait {
	return Trait{Type: "named", Properties: map[string]any{"object": object, "sub": sub}}
}

// twoNamedTraitRule lowers a "two" trait to two "named" traits generating one
// object name.
type twoNamedTraitRule struct{}

func (twoNamedTraitRule) TraitType() string { return "two" }
func (twoNamedTraitRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{named("web-hpa", "web-first"), named("web-hpa", "web-second")}}, nil
}

// namingGroupTransformer has components "a" and "b", the "named" trait, and
// rule "pair" emitting member a with onA and member b with onB. A nil onA hands
// member a the authored traits instead.
func namingGroupTransformer(onA, onB []Trait) *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0), "b": stubHandler("b", 0)},
		map[string]TraitHandler{"named": namedTrait{}})
	tr.RegisterComponentLowering(emitRule{"pair", func(c *Component) []Component {
		a := Component{Name: c.Name, Type: "a", Properties: map[string]any{}, Traits: onA}
		if onA == nil {
			a.Traits = c.Traits
		}
		return []Component{a, {Name: c.Name, Type: "b", Properties: map[string]any{}, Traits: onB}}
	}})
	tr.RegisterTraitLowering(twoNamedTraitRule{})
	return tr
}

// Two traits that generate one object are two owners wherever they stand: a
// slot does not tell them apart when a rule gave a group member both, or when a
// trait rule lowered one authored trait to both (go-kure/launcher#787).
func TestResolveName_EveryAppliedTraitIsItsOwnOwner(t *testing.T) {
	const collision = `name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named by `
	for _, tc := range []struct {
		name string
		tr   *Transformer
		doc  Component
		want string
	}{
		{
			name: "two traits a rule gave one group member",
			tr:   namingGroupTransformer([]Trait{named("web-hpa", "web-first"), named("web-hpa", "web-second")}, nil),
			doc:  Component{Name: "web", Type: "pair"},
			want: collision + `component "web" member "a" traits[0] "named" (role "hpa", its default) and by ` +
				`component "web" member "a" traits[1] "named" (role "hpa", its default); give one of them another name`,
		},
		{
			name: "one trait a rule gave two group members",
			tr:   namingGroupTransformer([]Trait{named("web-hpa", "web-first")}, []Trait{named("web-hpa", "web-second")}),
			doc:  Component{Name: "web", Type: "pair"},
			want: collision + `component "web" member "a" traits[0] "named" (role "hpa", its default) and by ` +
				`component "web" member "b" traits[0] "named" (role "hpa", its default); give one of them another name`,
		},
		{
			// Both keep the authored slot of the trait they replace, on one member:
			// only which output each is tells them apart.
			name: "two traits a trait rule lowered one forwarded trait to",
			tr:   namingGroupTransformer(nil, nil),
			doc:  Component{Name: "web", Type: "pair", Traits: []Trait{{Type: "two", Properties: map[string]any{}}}},
			want: collision + `component "web" member "a" traits[0] "named", output 1 of its lowering (role "hpa", its default) and by ` +
				`component "web" member "a" traits[0] "named", output 2 of its lowering (role "hpa", its default); give one of them another name`,
		},
		{
			// The rule's own trait stands first among the lowered component's, where
			// the forwarded one stood in the document: both are traits[0].
			name: "a trait a rule added before the forwarded one",
			tr:   prependingTransformer(named("web-hpa", "web-first")),
			doc:  Component{Name: "web", Type: "solo", Traits: []Trait{named("web-hpa", "web-second")}},
			want: collision + `component "web" traits[0] "named" after lowering (role "hpa", its default) and by ` +
				`component "web" traits[0] "named" (role "hpa", its default); give one of them another name`,
		},
		{
			name: "one trait naming two of its objects alike",
			tr:   prependingTransformer(Trait{Type: "twice", Properties: map[string]any{}}),
			doc:  Component{Name: "web", Type: "solo"},
			want: `name collision: HorizontalPodAutoscaler.autoscaling "default/web-hpa" is named twice by ` +
				`component "web" traits[0] "twice" (role "hpa", set by name); give one of them another name`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.tr.TransformWithPolicy(siblingDoc(tc.doc), TransformContext{Namespace: "default"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v\nwant one containing %q", err, tc.want)
			}
		})
	}
}

// prependingTransformer has component "a", the "named" and "twice" traits, and
// rule "solo" lowering a component to one "a" component carrying first before
// the authored traits.
func prependingTransformer(first Trait) *Transformer {
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0)},
		map[string]TraitHandler{"named": namedTrait{}, "twice": twiceNamedTrait{}})
	tr.RegisterComponentLowering(emitRule{"solo", func(c *Component) []Component {
		return []Component{{Name: c.Name, Type: "a", Properties: map[string]any{}, Traits: append([]Trait{first}, c.Traits...)}}
	}})
	return tr
}

// twiceNamedTrait resolves one authored name for two objects of one kind, whose
// defaults differ.
type twiceNamedTrait struct{}

func (twiceNamedTrait) CanHandle(t string) bool { return t == "twice" }
func (twiceNamedTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	for _, def := range []string{"web-one", "web-two"} {
		spec := hpaSpec(def)
		spec.Property, spec.Authored = "name", "web-hpa"
		if _, err := trait.ResolveName(spec); err != nil {
			return err
		}
	}
	return nil
}

// A sub-application's name is not unique, in a sibling group as outside one:
// the Naming hook may give two different sub-applications of two members one
// name. The same trait on two members is still refused, whatever the hook
// answers, since it is asked the same question for both. A name the hook gave
// that meets one it did not give is refused by name (go-kure/launcher#787).
func TestSiblingGroup_TraitSubApplicationsNamedByTheHook(t *testing.T) {
	shared := func(req NameRequest) (string, bool) {
		return "shared", req.Role == NameRoleSubApplication
	}
	t.Run("two sub-applications given one name are accepted", func(t *testing.T) {
		tr := namingGroupTransformer([]Trait{named("", "web-config")}, []Trait{named("", "web-route")})
		cluster, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: shared})
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		var names []string
		for _, a := range cluster.Node.Bundle.Applications[1:] {
			names = append(names, a.Name)
		}
		if want := []string{"shared", "shared"}; !slices.Equal(names, want) {
			t.Errorf("trait sub-applications = %v, want %v", names, want)
		}
	})
	t.Run("a name the hook gave that is one's default is still the hook's", func(t *testing.T) {
		// The hook answers "web-config" for both: member a's default, and member
		// b's "web-route". Both are names the hook gave, of two sub-applications.
		tr := namingGroupTransformer([]Trait{named("", "web-config")}, []Trait{named("", "web-route")})
		cluster, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{
			Naming: func(req NameRequest) (string, bool) {
				return "web-config", req.Role == NameRoleSubApplication
			},
		})
		if err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
		var names []string
		for _, a := range cluster.Node.Bundle.Applications[1:] {
			names = append(names, a.Name)
		}
		if want := []string{"web-config", "web-config"}; !slices.Equal(names, want) {
			t.Errorf("trait sub-applications = %v, want %v", names, want)
		}
	})
	t.Run("the same trait on two members is refused", func(t *testing.T) {
		tr := namingGroupTransformer([]Trait{named("", "web-sub")}, []Trait{named("", "web-sub")})
		_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: shared})
		want := `sibling group "web": traits on members "a" and "b" both create sub-application "web-sub" (named "shared" by the Naming hook); carry the trait on one member`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %q", err, want)
		}
	})
	// Only two names the hook gave are compared by their defaults. A name it gave
	// that meets one it did not give is refused by name, as before the hook.
	onlyConfig := func(name string) func(NameRequest) (string, bool) {
		return func(req NameRequest) (string, bool) {
			return name, req.Role == NameRoleSubApplication && req.Default == "web-config"
		}
	}
	for _, tc := range []struct {
		name string
		onB  Trait
		hook func(NameRequest) (string, bool)
	}{
		{
			// Member b's trait resolves nothing and names its sub-application itself.
			name: "a name the hook gave meets one a trait gave",
			onB:  Trait{Type: "sub", Properties: map[string]any{}},
			hook: onlyConfig("web-sub"),
		},
		{
			name: "a name the hook gave meets one a policy renamed onto it",
			onB:  Trait{Type: "renamesub", Properties: map[string]any{"name": "web-other", "to": "web-sub"}},
			hook: onlyConfig("web-sub"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := namingGroupTransformer([]Trait{named("", "web-config")}, []Trait{tc.onB})
			tr.RegisterTrait("sub", subAppTrait{})
			tr.RegisterTrait("renamesub", renamingSubTrait{})
			_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: tc.hook})
			want := `sibling group "web": traits on members "a" and "b" both create sub-application "web-sub" (the Naming hook's name for "web-config" on member "a"); carry the trait on one member, or return another name from the hook`
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v\nwant one containing %q", err, want)
			}
		})
	}
	t.Run("a policy renaming one of a trait's two sub-applications leaves the other its default", func(t *testing.T) {
		// Member a's trait is given "shared" for both its sub-applications, and a
		// policy renames the first. The second is still the hook's name for
		// "web-y", which member b's trait creates too.
		tr := namingGroupTransformer(
			[]Trait{{Type: "twosub", Properties: map[string]any{"first": "web-x", "second": "web-y"}}},
			[]Trait{named("", "web-y")})
		tr.RegisterTrait("twosub", twoSubTrait{})
		_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: shared})
		want := `sibling group "web": traits on members "a" and "b" both create sub-application "web-y" (named "shared" by the Naming hook); carry the trait on one member`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %q", err, want)
		}
	})
}

// twoSubTrait resolves the sub-application names in properties "first" and
// "second" and appends both, in that order. The first's ApplyPolicy renames it
// to "moved".
type twoSubTrait struct{}

func (twoSubTrait) CanHandle(t string) bool { return t == "twosub" }
func (twoSubTrait) Apply(trait *Trait, app *stack.Application, b *stack.Bundle) error {
	var names [2]string
	for i, property := range []string{"first", "second"} {
		name, err := trait.ResolveName(NameSpec{Role: NameRoleSubApplication, Default: trait.Properties[property].(string)})
		if err != nil {
			return err
		}
		names[i] = name
	}
	moved := stack.NewApplication(names[0], app.Namespace, nil)
	moved.SetConfig(&renamingSubStub{own: moved, to: "moved"})
	b.Applications = append(b.Applications, moved, stack.NewApplication(names[1], app.Namespace, &siblingStub{}))
	return nil
}

// decoratingNamedTrait resolves one object name on every Apply: on its
// component's application, and again on each sub-application it decorates.
type decoratingNamedTrait struct{ applied *int }

func (decoratingNamedTrait) CanHandle(t string) bool        { return t == "decor" }
func (decoratingNamedTrait) DecoratesSubApplications() bool { return true }
func (d decoratingNamedTrait) Apply(trait *Trait, _ *stack.Application, _ *stack.Bundle) error {
	*d.applied++
	_, err := trait.ResolveName(hpaSpec("web-hpa"))
	return err
}

// A decorating trait the engine applies again on a sub-application resolves its
// name for the same owner: it does not collide with itself.
func TestResolveName_DecoratingTraitAppliedAgainIsOneOwner(t *testing.T) {
	applied := 0
	tr := NewTransformer(map[string]ComponentHandler{"a": stubHandler("a", 0)},
		map[string]TraitHandler{"named": namedTrait{}, "decor": decoratingNamedTrait{&applied}})
	doc := siblingDoc(Component{Name: "web", Type: "a", Properties: map[string]any{}, Traits: []Trait{
		{Type: "decor", Properties: map[string]any{}}, named("", "web-sub"),
	}})
	if _, _, err := tr.TransformWithPolicy(doc, TransformContext{Namespace: "default"}); err != nil {
		t.Fatalf("TransformWithPolicy: %v", err)
	}
	if applied != 2 {
		t.Fatalf("the decorating trait was applied %d times, want 2: on its component and on the sub-application", applied)
	}
}
