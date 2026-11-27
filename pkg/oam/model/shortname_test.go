package model_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/definition"
	"github.com/go-kure/launcher/pkg/oam/model"
)

// These tests pin what happens when a model's short name is the plain name a handler or a
// lowering rule is registered under (go-kure/launcher#1716). An identity resolves before a plain
// name, so such a model would be built in the place of the type that holds the name.

// shadowKey is the plain type name a ComponentDefinition declares in the example.
const shadowKey = "batch-job"

// plainKeyHandler is a handler a library caller registers under a plain type name.
type plainKeyHandler struct{ name string }

func (h plainKeyHandler) CanHandle(t string) bool { return t == h.name }

func (plainKeyHandler) ToApplicationConfig(*oam.Component, string) (stack.ApplicationConfig, error) {
	return nil, nil
}

// plainKeyRule is a lowering rule a library caller registers under a plain type name.
type plainKeyRule struct{ name string }

func (r plainKeyRule) ComponentType() string { return r.name }

func (plainKeyRule) LowerComponent(*oam.Component, oam.LoweringContext) (oam.LoweringResult, error) {
	return oam.LoweringResult{}, nil
}

// shadowModel is a model of the given qualified name that publishes nothing.
func shadowModel(t *testing.T, name string) *model.Model {
	t.Helper()
	m, err := model.Load([]byte(strings.Replace(modelHeader, "webapp.acme.example", name, 1) + `
    - id: p
      name: p
      type: configmap@v1
      properties: {}
`))
	if err != nil {
		t.Fatalf("Load %s: %v", name, err)
	}
	return m
}

// A model whose short name is a plain key that no declared type claims is refused, whether the
// key holds a handler or a rule, and whether or not the key has an identity of its own under
// another short name. Nothing is left behind, and the plain key still names its own type.
func TestRegistry_RefusesAShortNameThatIsAPlainKey(t *testing.T) {
	cases := []struct {
		name     string
		register func(tr *oam.Transformer)
		holds    func(tr *oam.Transformer) bool
	}{
		{"a handler with no identity", func(tr *oam.Transformer) {
			tr.RegisterComponent(shadowKey, plainKeyHandler{shadowKey})
		}, func(tr *oam.Transformer) bool { _, ok := tr.ComponentHandlerFor(shadowKey); return ok }},
		{"a handler with an identity of another short name", func(tr *oam.Transformer) {
			tr.RegisterComponent(shadowKey, plainKeyHandler{shadowKey})
			if err := tr.DeclareComponentTypes(map[string]oam.TypeID{shadowKey: {Name: "batchjob.example.gokure.dev", Version: "v1alpha1"}}); err != nil {
				t.Fatal(err)
			}
		}, func(tr *oam.Transformer) bool { _, ok := tr.ComponentHandlerFor(shadowKey); return ok }},
		{"a lowering rule", func(tr *oam.Transformer) {
			tr.RegisterComponentLowering(plainKeyRule{shadowKey})
		}, func(tr *oam.Transformer) bool { _, ok := tr.ComponentLoweringRuleFor(shadowKey); return ok }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := expandTransformer(t)
			c.register(tr)
			reg := model.NewRegistry(tr)
			m := shadowModel(t, "batch-job.example.org")
			var first string
			for i := 0; i < 2; i++ {
				err := reg.Register(m)
				if err == nil {
					t.Fatal("a model whose short name is a registered plain key was registered")
				}
				for _, want := range []string{`"batch-job"`, "batch-job.example.org@v1"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %s", err, want)
					}
				}
				if i == 0 {
					first = err.Error()
				} else if err.Error() != first {
					t.Errorf("the second refusal %q differs from the first %q", err, first)
				}
			}
			if _, ok := tr.ComponentLoweringRuleFor(m.Key()); ok {
				t.Error("a refused model left its rule on the Transformer")
			}
			if id, err := tr.ResolveComponentType(m.Key()); err == nil {
				t.Errorf("a refused model left its type declared as %s", id)
			}
			if !c.holds(tr) {
				t.Error("the plain key no longer reaches its own type")
			}
		})
	}
}

// A short name that a declared type claims is not a plain key nobody claims: the reference
// resolves, or is reported ambiguous, and never silently shadows. These registrations stay accepted.
func TestRegistry_AcceptsAShortNameADeclaredTypeClaims(t *testing.T) {
	tr, reg := registered(t, modelHeader+dbThenWorker)
	for _, name := range []string{"webapp.other.example", "deployment.acme.example"} {
		if err := reg.Register(shadowModel(t, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, short := range []string{"webapp", "deployment"} {
		if _, err := tr.ResolveComponentType(short); !errors.Is(err, oam.ErrTypeReferenceAmbiguous) {
			t.Errorf("%s resolves to %v, want it ambiguous", short, err)
		}
	}
}

// The other order, through a ComponentDefinition: a model whose short name is batch-job is
// registered first, and the definition of that name is refused.
func TestRegistry_ADefinitionAfterTheModelIsRefused(t *testing.T) {
	tr := expandTransformer(t)
	if err := model.NewRegistry(tr).Register(shadowModel(t, "batch-job.example.org")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h, err := definition.Load("../../../examples/custom-component/definitions/batchjob.yaml")
	if err != nil {
		t.Fatal(err)
	}
	err = definition.Register(tr, h)
	if err == nil {
		t.Fatal("a definition whose type name a declared model answers to was registered")
	}
	for _, want := range []string{`"batch-job" is already declared`, "batch-job.example.org@v1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not hold %s", err, want)
		}
	}
	if _, ok := tr.ComponentHandlerFor(shadowKey); ok {
		t.Error("the refused definition left a handler under the plain name")
	}
}

// A model is also known by its qualified name, so a plain key equal to it is taken over the same
// way, and the registry refuses that model as well.
func TestRegistry_RefusesAQualifiedNameThatIsAPlainKey(t *testing.T) {
	tr := expandTransformer(t)
	tr.RegisterComponent("batch-job.example.org", plainKeyHandler{"batch-job.example.org"})
	m := shadowModel(t, "batch-job.example.org")
	err := model.NewRegistry(tr).Register(m)
	if err == nil {
		t.Fatal("a model whose qualified name is a registered plain key was registered")
	}
	for _, want := range []string{`"batch-job.example.org"`, m.Key()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if _, ok := tr.ComponentLoweringRuleFor(m.Key()); ok {
		t.Error("a refused model left its rule on the Transformer")
	}
}

// The other order, through the Transformer: a handler or a rule registered under a plain name
// that a declared model type already claims would never be reached, so the registration panics,
// as the Transformer's other collisions do, and names both.
func TestRegistry_APlainKeyAfterTheModelPanics(t *testing.T) {
	cases := []struct {
		name     string
		register func(tr *oam.Transformer)
	}{
		{"RegisterComponent", func(tr *oam.Transformer) { tr.RegisterComponent(shadowKey, plainKeyHandler{shadowKey}) }},
		{"RegisterAuthoredComponent", func(tr *oam.Transformer) { tr.RegisterAuthoredComponent(shadowKey, plainKeyHandler{shadowKey}) }},
		{"RegisterComponentLowering", func(tr *oam.Transformer) { tr.RegisterComponentLowering(plainKeyRule{shadowKey}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := expandTransformer(t)
			m := shadowModel(t, "batch-job.example.org")
			if err := model.NewRegistry(tr).Register(m); err != nil {
				t.Fatalf("Register: %v", err)
			}
			msg := func() (msg string) {
				defer func() {
					if p := recover(); p != nil {
						msg = fmt.Sprint(p)
					}
				}()
				c.register(tr)
				return ""
			}()
			if msg == "" {
				t.Fatal("a plain key that a declared model type claims was registered")
			}
			for _, want := range []string{`"batch-job"`, "batch-job.example.org@v1"} {
				if !strings.Contains(msg, want) {
					t.Errorf("panic %q does not name %s", msg, want)
				}
			}
		})
	}
}

// A plain key that no declared type claims, and a type whose own identity has its short name,
// still register: the new refusal reaches only a name another type already holds.
func TestRegistry_APlainKeyNobodyClaimsStillRegisters(t *testing.T) {
	tr := expandTransformer(t)
	if err := model.NewRegistry(tr).Register(shadowModel(t, "batch-job.example.org")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tr.RegisterComponent("other-job", plainKeyHandler{"other-job"})
	if _, ok := tr.ComponentHandlerFor("other-job"); !ok {
		t.Error("a plain key nobody claims no longer registers")
	}
}
