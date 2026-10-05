package oam

import (
	"strconv"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/go-kure/launcher/pkg/errors"
)

// claimingTrait names an Ingress itself, under no role, and claims the name: its
// `name` property when the author wrote one, else "<component>-ingress".
type claimingTrait struct{}

func (claimingTrait) CanHandle(t string) bool { return t == "claims" }
func (claimingTrait) Apply(trait *Trait, app *stack.Application, _ *stack.Bundle) error {
	name, property := app.Name+"-ingress", ""
	if authored, ok := trait.Properties["name"].(string); ok {
		name, property = authored, "name"
	}
	return trait.ClaimObjectName(ingressKind, "default", name, property)
}

// A name collision is recognised through the wrapping the transform adds, with
// errors.Is and with errors.As, whoever the two members are: components, traits
// that resolve a name or claim their own, a member a lowering rule named. What
// errors.As finds says what was named and by whom, and prints the text the
// transform returns behind its own prefix.
func TestNameCollision_RecognisedThroughTheTransform(t *testing.T) {
	kinds := func() map[string]ComponentHandler {
		return map[string]ComponentHandler{
			"a":      stubHandler("a", 0),
			"widget": kindStub("widget", widgetKind, ObjectScopeNamespaced),
		}
	}
	traits := map[string]TraitHandler{"named": namedTrait{}, "claims": claimingTrait{}}
	with := func(name string, trait Trait) Component {
		return Component{Name: name, Type: "a", Properties: map[string]any{}, Traits: []Trait{trait}}
	}
	lowered := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization}
	twice := lowered
	twice.calls = 2
	other := widget("other", map[string]any{"objectName": "shared"})
	byOther := NameCollisionMember{
		Component: "other", Role: NameRoleObject, Property: "properties.objectName",
		Description: `component "other" (role "object", set by properties.objectName)`,
	}

	for _, tt := range []struct {
		name string
		rule *memberNamingRule
		doc  *Application
		hook map[string]string
		// prefix is what the transform puts in front of the refusal.
		prefix string
		want   NameCollisionError
	}{
		{
			name: "two components",
			doc: siblingDoc(
				widget("a", map[string]any{"objectName": "shared"}),
				widget("b", map[string]any{"objectName": "shared"})),
			prefix: `component "b": `,
			want: NameCollisionError{
				Kind: widgetKind, Namespace: "default", Name: "shared",
				First: NameCollisionMember{
					Component: "a", Role: NameRoleObject, Property: "properties.objectName",
					Description: `component "a" (role "object", set by properties.objectName)`,
				},
				Second: NameCollisionMember{
					Component: "b", Role: NameRoleObject, Property: "properties.objectName",
					Description: `component "b" (role "object", set by properties.objectName)`,
				},
			},
		},
		{
			name:   "two traits that resolve one name",
			doc:    siblingDoc(with("web", named("shared-hpa", "web-sub")), with("api", named("shared-hpa", "api-sub"))),
			prefix: `component "api" trait "named": `,
			want: NameCollisionError{
				Kind: hpaKind, Namespace: "default", Name: "shared-hpa",
				First: NameCollisionMember{
					Component: "web", Trait: "named", Role: NameRoleHPA,
					Description: `component "web" traits[0] "named" (role "hpa", its default)`,
				},
				Second: NameCollisionMember{
					Component: "api", Trait: "named", Role: NameRoleHPA,
					Description: `component "api" traits[0] "named" (role "hpa", its default)`,
				},
			},
		},
		{
			name: "two traits that claim their own object",
			doc: siblingDoc(
				with("web", Trait{Type: "claims", Properties: map[string]any{"name": "api-ingress"}}),
				with("api", Trait{Type: "claims", Properties: map[string]any{}})),
			prefix: `component "api" trait "claims": `,
			want: NameCollisionError{
				Kind: ingressKind, Namespace: "default", Name: "api-ingress",
				First: NameCollisionMember{
					Component: "web", Trait: "claims", Property: "name",
					Description: `component "web" traits[0] "claims" (its own object, set by name)`,
				},
				Second: NameCollisionMember{
					Component: "api", Trait: "claims",
					Description: `component "api" traits[0] "claims" (its own object, its default name)`,
				},
			},
		},
		{
			name:   "a lowered member and a component",
			rule:   &lowered,
			doc:    siblingDoc(probe("web", map[string]any{"memberName": "shared"}), other),
			prefix: `component "other": `,
			want: NameCollisionError{
				Kind: widgetKind, Namespace: "default", Name: "shared",
				First: NameCollisionMember{
					Component: "web", Role: NameRoleOCIKustomization, Property: "memberName",
					Description: `component "web" (role "oci-kustomization", set by memberName)`,
				},
				Second: byOther,
			},
		},
		{
			name:   "a lowered member the hook named",
			rule:   &lowered,
			doc:    siblingDoc(probe("web", nil), other),
			hook:   map[string]string{"web": "shared"},
			prefix: `component "other": `,
			want: NameCollisionError{
				Kind: widgetKind, Namespace: "default", Name: "shared",
				First: NameCollisionMember{
					Component: "web", Role: NameRoleOCIKustomization, FromHook: true,
					Description: `component "web" (role "oci-kustomization", returned by the Naming hook in place of "web")`,
				},
				Second: byOther,
			},
		},
		{
			// Refused while lowering: the namespace the object lands in is not
			// settled yet, and the text prints none.
			name:   "a lowered member named twice",
			rule:   &twice,
			doc:    siblingDoc(probe("web", nil)),
			prefix: `lowering document "app" in document "app" (kind "Application"): component "web" (type "probe") in document "app" (kind "Application"): `,
			want: func() NameCollisionError {
				web := NameCollisionMember{
					Component: "web", Role: NameRoleOCIKustomization,
					Description: `component "web" (role "oci-kustomization", its default)`,
				}
				return NameCollisionError{Kind: widgetKind, Name: "web", First: web, Second: web}
			}(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTransformer(kinds(), traits)
			if tt.rule != nil {
				tr.RegisterComponentLowering(*tt.rule)
			}
			var asked []NameRequest
			_, _, err := tr.TransformWithPolicy(tt.doc, TransformContext{Naming: roleHook(NameRoleOCIKustomization, tt.hook, &asked)})
			if err == nil {
				t.Fatal("the transform accepted two members naming one object")
			}
			if !errors.Is(err, ErrNameCollision) {
				t.Errorf("errors.Is(err, ErrNameCollision) is false for: %v", err)
			}
			var got *NameCollisionError
			if !errors.As(err, &got) {
				t.Fatalf("errors.As finds no *NameCollisionError in: %v", err)
			}
			if *got != tt.want {
				t.Errorf("the collision is\n  %+v\nwant\n  %+v", *got, tt.want)
			}
			// The transform's own prefix is in front of it: what was found is not
			// the error the transform returned.
			if want := tt.prefix + tt.want.Error(); err.Error() != want {
				t.Errorf("err = %v\nwant  %s", err, want)
			}
		})
	}
}

// A name that is no object's collides too: a bundle's, and a hook-group name
// prefix. The error has no Kind then, and the members' Role says which it is.
func TestNameCollision_NoObject(t *testing.T) {
	t.Run("a bundle", func(t *testing.T) {
		h := newNamingHarness(map[string]string{"shop-apps": "shop"})
		if _, err := h.resolver.resolveBundleName(NameRoleBundle, "shop"); err != nil {
			t.Fatalf("bundle: %v", err)
		}
		_, err := h.resolver.resolveBundleName(NameRoleGroup, "shop-apps")
		var got *NameCollisionError
		if !errors.Is(err, ErrNameCollision) || !errors.As(err, &got) {
			t.Fatalf("err = %v\nwant a name collision", err)
		}
		want := NameCollisionError{
			Name: "shop",
			First: NameCollisionMember{
				Role: NameRoleBundle, Description: `the application (role "bundle", its default)`,
			},
			Second: NameCollisionMember{
				Role: NameRoleGroup, FromHook: true,
				Description: `the application (role "group", returned by the Naming hook in place of "shop-apps")`,
			},
		}
		if *got != want {
			t.Errorf("the collision is\n  %+v\nwant\n  %+v", *got, want)
		}
		const text = `group name: name collision: bundle "shop" is named by the application (role "bundle", its default) and by ` +
			`the application (role "group", returned by the Naming hook in place of "shop-apps"); give one of them another name`
		if err.Error() != text {
			t.Errorf("err = %v\nwant  %s", err, text)
		}
	})

	t.Run("a hook-group name prefix", func(t *testing.T) {
		h := newNamingHarness(nil)
		prefix := func(component, authored string) error {
			spec := NameSpec{Role: NameRoleHookGroup, Default: "shop-" + component}
			if authored != "" {
				spec.Property, spec.Authored = HookGroupNamePrefixProperty, authored
			}
			_, err := h.resolver.resolve(nameOwner{component: component, role: NameRoleHookGroup, def: spec.Default}, spec)
			return err
		}
		if err := prefix("cache", ""); err != nil {
			t.Fatalf("first: %v", err)
		}
		err := prefix("db", "shop-cache")
		var got *NameCollisionError
		if !errors.Is(err, ErrNameCollision) || !errors.As(err, &got) {
			t.Fatalf("err = %v\nwant a name collision", err)
		}
		if got.Kind != (schema.GroupKind{}) || got.Namespace != "" || got.Name != "shop-cache" {
			t.Errorf("the collision names %+v %q %q, want no kind, no namespace and the prefix", got.Kind, got.Namespace, got.Name)
		}
		if got.First.Component != "cache" || got.Second.Component != "db" || got.Second.Property != HookGroupNamePrefixProperty {
			t.Errorf("the members are %+v and %+v, want component cache, and component db by its authored prefix", got.First, got.Second)
		}
		const text = `name collision: hook-group name prefix "shop-cache" is named by ` +
			`component "cache" (role "hook-group", its default) and by ` +
			`component "db" (role "hook-group", set by ` + HookGroupNamePrefixProperty + `); give one of them another name`
		if err.Error() != text {
			t.Errorf("err = %v\nwant  %s", err, text)
		}
	})
}

// Two names lowering rules resolved are refused while lowering with no
// namespace. One pair is left until the namespace is settled, a Flux-scoped name
// and one that is not. Where both land in one namespace (no Flux namespace, or
// the document's own), that refusal carries the namespace.
func TestNameCollision_LoweredNamesAndTheNamespace(t *testing.T) {
	configMapKind := schema.GroupKind{Kind: "ConfigMap"}
	plain := NameSpec{Role: NameRoleObject, Kind: configMapKind, Property: "objectName", Authored: "web-values"}
	collision := func(t *testing.T, err error) *NameCollisionError {
		t.Helper()
		var got *NameCollisionError
		if !errors.Is(err, ErrNameCollision) || !errors.As(err, &got) {
			t.Fatalf("err = %v\nwant a name collision", err)
		}
		if got.Kind != configMapKind || got.Name != "web-values" || got.First.Component != "web" || got.Second.Component != "api" {
			t.Fatalf("the collision is %+v, want ConfigMap web-values named by web and by api", *got)
		}
		return got
	}

	t.Run("refused while lowering", func(t *testing.T) {
		h := newLoweringHarness(nil)
		if _, err := h.lctx("web").ResolveName("web", "values", NameSpec{Role: NameRoleValuesConfigMap, Kind: configMapKind}); err != nil {
			t.Fatalf("first: %v", err)
		}
		_, err := h.lctx("api").ResolveName("api", "config", plain)
		got := collision(t, err)
		const text = `name collision: ConfigMap "web-values" is named by `
		if got.Namespace != "" || !strings.HasPrefix(got.Error(), text) {
			t.Errorf("namespace %q, text %s\nwant no namespace, and %s…", got.Namespace, got.Error(), text)
		}
	})

	for _, fluxNamespace := range []string{"", "prod"} {
		t.Run("refused once the namespace is settled, Flux namespace "+strconv.Quote(fluxNamespace), func(t *testing.T) {
			h := newLoweringHarness(nil)
			if _, err := h.lctx("web").ResolveName("web", "values", NameSpec{Role: NameRoleValuesConfigMap, Kind: configMapKind, FluxScoped: true}); err != nil {
				t.Fatalf("the Flux-scoped name: %v", err)
			}
			if _, err := h.lctx("api").ResolveName("api", "config", plain); err != nil {
				t.Fatalf("the same name, not Flux-scoped: %v", err)
			}
			got := collision(t, h.namer.claimLowered("prod", fluxNamespace))
			const text = `name collision: ConfigMap "prod/web-values" is named by `
			if got.Namespace != "prod" || !strings.HasPrefix(got.Error(), text) {
				t.Errorf("namespace %q, text %s\nwant prod, and %s…", got.Namespace, got.Error(), text)
			}
		})
	}
}

// The error names what was named as the claim key prints it, for every class of
// name and every role that is claimed. Error rebuilds the key from the exported
// fields (a role tells a bundle from a hook-group name prefix), so a change to
// the key's text, or a role given another class, shows here and not in a text
// that drifted.
func TestNameCollision_TextNamesTheClaimKey(t *testing.T) {
	shapes := map[nameClass][]struct {
		name     string
		identity objectIdentity
	}{
		nameClassObject: {
			{"a namespaced object", objectIdentity{group: "apps", kind: "Deployment", namespace: "shop", name: "web"}},
			{"a namespaced object of the core group", objectIdentity{kind: "ConfigMap", namespace: "shop", name: "web"}},
			{"a cluster-scoped object", objectIdentity{group: "rbac.authorization.k8s.io", kind: "ClusterRole", name: "web"}},
			{"an object named while lowering, without a namespace", objectIdentity{group: "apps", kind: "Deployment", name: "web"}},
		},
		nameClassBundle:          {{"a bundle", objectIdentity{name: "web"}}},
		nameClassHookGroupPrefix: {{"a hook-group name prefix", objectIdentity{name: "web"}}},
	}
	claimed := 0
	for _, r := range nameRoles {
		if r.class == nameClassSubApplication {
			// Not claimed, so never a collision (resolveFrom).
			continue
		}
		if len(shapes[r.class]) == 0 {
			t.Fatalf("role %q has a class this test has no name for", r.role)
		}
		for _, shape := range shapes[r.class] {
			claimed++
			t.Run(string(r.role)+"/"+shape.name, func(t *testing.T) {
				key := nameClaimKey{class: r.class, objectIdentity: shape.identity}
				first := resolvedNameClaim{owner: nameOwner{component: "a", role: r.role, def: "web"}}
				second := resolvedNameClaim{owner: nameOwner{component: "b", role: r.role, def: "web"}}

				var got *NameCollisionError
				if err := nameCollision(key, first, second); !errors.As(err, &got) {
					t.Fatalf("err = %v\nwant a name collision", err)
				}
				want := "name collision: " + key.String() + " is named by " + got.First.Description +
					" and by " + got.Second.Description + "; give one of them another name"
				if got.Error() != want {
					t.Errorf("err = %v\nwant  %s", got, want)
				}

				if err := nameCollision(key, second, second); !errors.As(err, &got) {
					t.Fatalf("err = %v\nwant a name collision", err)
				}
				want = "name collision: " + key.String() + " is named twice by " + got.First.Description +
					"; give one of them another name"
				if got.Error() != want {
					t.Errorf("err = %v\nwant  %s", got, want)
				}
			})
		}
	}
	if claimed == 0 {
		t.Fatal("no role was checked")
	}
}

// Only a name collision answers to ErrNameCollision: another refusal of a name
// does not, and neither does a refusal of the name allocator.
func TestNameCollision_OtherRefusalsAreNotOne(t *testing.T) {
	rule := memberNamingRule{memberType: "widget", role: NameRoleOCIKustomization}
	tr, _ := memberTransformer(rule)
	_, _, err := tr.TransformWithPolicy(siblingDoc(probe("web", map[string]any{"memberName": "Not_A_Name"})), TransformContext{})
	if err == nil {
		t.Fatal("the transform accepted a name that is no DNS-1123 subdomain")
	}
	var got *NameCollisionError
	if errors.Is(err, ErrNameCollision) || errors.As(err, &got) {
		t.Errorf("a refused name answers as a name collision: %v", err)
	}

	names := NewNameAllocator()
	origin := Origin{Document: "shop"}
	if err := names.Reserve("web", origin); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	err = names.Reserve("web", origin)
	if err == nil {
		t.Fatal("the allocator reserved one name twice")
	}
	if errors.Is(err, ErrNameCollision) || errors.As(err, &got) {
		t.Errorf("a generated-name refusal answers as a name collision: %v", err)
	}
}
