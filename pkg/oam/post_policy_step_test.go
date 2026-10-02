package oam

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-kure/kure/pkg/stack"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// stepConfig is an Enforceable config that logs when the policy is applied to it.
type stepConfig struct{ log *[]string }

func (c *stepConfig) Generate(*stack.Application) ([]*client.Object, error) { return nil, nil }

func (c *stepConfig) ApplyPolicy(Policy) error {
	*c.log = append(*c.log, "policy")
	return nil
}

// stepSinkHandler builds a stepConfig for the terminal "step-sink" type.
type stepSinkHandler struct{ log *[]string }

func (stepSinkHandler) CanHandle(t string) bool { return t == "step-sink" }

func (h stepSinkHandler) ToApplicationConfig(*Component, string) (stack.ApplicationConfig, error) {
	return &stepConfig{log: h.log}, nil
}

// loggedTraitHandler logs each time the "logged" trait is applied.
type loggedTraitHandler struct{ log *[]string }

func (loggedTraitHandler) CanHandle(t string) bool { return t == "logged" }

func (h loggedTraitHandler) Apply(*Trait, *stack.Application, *stack.Bundle) error {
	*h.log = append(*h.log, "trait")
	return nil
}

func (loggedTraitHandler) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

// stepAttachingRule lowers "attaches-step" onto a "step-sink" carrying the
// authored traits and the steps it is built with, attached in order.
type stepAttachingRule struct{ steps []PostPolicyStep }

func (stepAttachingRule) ComponentType() string { return "attaches-step" }

func (stepAttachingRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (r stepAttachingRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	out := Component{Name: comp.Name, Type: "step-sink", Properties: map[string]any{}, Traits: slices.Clone(comp.Traits)}
	for _, s := range r.steps {
		out.AfterPolicy(s)
	}
	return LoweringResult{Components: []Component{out}}, nil
}

// aliasTraitRule lowers an "alias" trait onto a "logged" one, so the component
// carrying the step goes through a trait rule before it settles.
type aliasTraitRule struct{}

func (aliasTraitRule) TraitType() string { return "alias" }

func (aliasTraitRule) PropertySchema() map[string]PropertySchema {
	return map[string]PropertySchema{}
}

func (aliasTraitRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: "logged", Properties: map[string]any{}}}}, nil
}

// loggingStep returns a step that logs name, after checking it was handed the
// component's own config.
func loggingStep(t *testing.T, log *[]string, name string) PostPolicyStep {
	return func(config stack.ApplicationConfig) error {
		if _, ok := config.(*stepConfig); !ok {
			t.Errorf("step %s was handed config %T, want the component's *stepConfig", name, config)
		}
		*log = append(*log, name)
		return nil
	}
}

func stepTransform(log *[]string, trait string, steps ...PostPolicyStep) error {
	tr := NewTransformer(map[string]ComponentHandler{"step-sink": stepSinkHandler{log: log}}, nil)
	tr.RegisterBuiltinTrait("logged", loggedTraitHandler{log: log})
	tr.RegisterBuiltinTraitLowering(aliasTraitRule{})
	tr.RegisterComponentLowering(stepAttachingRule{steps: steps})
	app := &Application{
		APIVersion: SupportedAPIVersion,
		Kind:       "Application",
		Metadata:   Metadata{Name: "app", Namespace: "default"},
		Spec: ApplicationSpec{Components: []Component{{
			Name:       "web",
			Type:       "attaches-step",
			Properties: map[string]any{},
			Traits:     []Trait{{Type: trait, Properties: map[string]any{}}},
		}}},
	}
	_, err := tr.Transform(app, TransformContext{})
	return err
}

// The steps run on the config the policy decided, in the order attached, and
// before any trait of the component.
func TestPostPolicyStep_RunsAfterPolicyBeforeTraits(t *testing.T) {
	var log []string
	if err := stepTransform(&log, "logged", loggingStep(t, &log, "step-1"), loggingStep(t, &log, "step-2")); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if want := []string{"policy", "step-1", "step-2", "trait"}; !slices.Equal(log, want) {
		t.Errorf("ran %v, want %v", log, want)
	}
}

// A trait rule rewriting the component's traits keeps the component, and with
// it the step.
func TestPostPolicyStep_SurvivesATraitRule(t *testing.T) {
	var log []string
	if err := stepTransform(&log, "alias", loggingStep(t, &log, "step")); err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if want := []string{"policy", "step", "trait"}; !slices.Equal(log, want) {
		t.Errorf("ran %v, want %v", log, want)
	}
}

// A failing step fails the transform, naming the component, and nothing after
// it runs.
func TestPostPolicyStep_ErrorNamesTheComponent(t *testing.T) {
	var log []string
	failing := func(stack.ApplicationConfig) error { return errors.New("boom") }
	err := stepTransform(&log, "logged", failing, loggingStep(t, &log, "after"))
	if err == nil || !strings.Contains(err.Error(), `component "web": boom`) {
		t.Fatalf("err = %v, want one containing %q", err, `component "web": boom`)
	}
	if want := []string{"policy"}; !slices.Equal(log, want) {
		t.Errorf("ran %v, want %v", log, want)
	}
}

// Attaching to a copy never reaches another copy, even when they share the
// steps attached before the copy was taken; a nil step and a nil component are
// ignored.
func TestComponentAfterPolicy_CopiesStayApart(t *testing.T) {
	var log []string
	var c Component
	c.AfterPolicy(func(stack.ApplicationConfig) error { log = append(log, "shared"); return nil })
	c.AfterPolicy(nil)
	d, e := c, c
	d.AfterPolicy(func(stack.ApplicationConfig) error { log = append(log, "d"); return nil })
	e.AfterPolicy(func(stack.ApplicationConfig) error { log = append(log, "e"); return nil })
	for _, comp := range []Component{c, d, e} {
		for _, step := range comp.afterPolicy {
			if err := step(nil); err != nil {
				t.Fatal(err)
			}
		}
		log = append(log, "|")
	}
	if want := []string{"shared", "|", "shared", "d", "|", "shared", "e", "|"}; !slices.Equal(log, want) {
		t.Errorf("ran %v, want %v", log, want)
	}
	var nilComp *Component
	nilComp.AfterPolicy(func(stack.ApplicationConfig) error { return nil })
}
