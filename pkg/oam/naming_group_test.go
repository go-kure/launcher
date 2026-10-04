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
			// Both keep the authored slot of the trait they replace, on one member.
			name: "two traits a trait rule lowered one forwarded trait to",
			tr:   namingGroupTransformer(nil, nil),
			doc:  Component{Name: "web", Type: "pair", Traits: []Trait{{Type: "two", Properties: map[string]any{}}}},
			want: collision + `component "web" member "a" traits[0] "named" (role "hpa", its default) and by ` +
				`component "web" member "a" traits[0] "named" (role "hpa", its default); give one of them another name`,
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

// A sub-application's name is not unique, in a sibling group as outside one:
// the Naming hook may give two different sub-applications of two members one
// name. The same trait on two members is still refused, whatever the hook
// answers, since it is asked the same question for both (go-kure/launcher#787).
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
	t.Run("the same trait on two members is refused", func(t *testing.T) {
		tr := namingGroupTransformer([]Trait{named("", "web-sub")}, []Trait{named("", "web-sub")})
		_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: shared})
		want := `sibling group "web": traits on members "a" and "b" both create sub-application "web-sub" (named "shared" by the Naming hook); carry the trait on one member`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v\nwant one containing %q", err, want)
		}
	})
	t.Run("a name the hook gave is not compared with one it did not give", func(t *testing.T) {
		// Member a's sub-application is named by the hook, member b's by its own
		// trait, which resolves nothing.
		tr := namingGroupTransformer([]Trait{named("", "web-config")}, []Trait{{Type: "sub", Properties: map[string]any{}}})
		tr.RegisterTrait("sub", subAppTrait{})
		hook := func(req NameRequest) (string, bool) {
			return "web-sub", req.Role == NameRoleSubApplication
		}
		if _, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: "pair"}), TransformContext{Naming: hook}); err != nil {
			t.Fatalf("TransformWithPolicy: %v", err)
		}
	})
}
