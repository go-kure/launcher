package oam

import (
	"errors"
	"testing"
)

// capabilityEmitRule is a component-position rule whose emitted part carries a trait
// the rule wrote itself, plus the authored traits it was handed when forward is set.
type capabilityEmitRule struct {
	typ     string
	emit    string
	forward bool
}

func (r capabilityEmitRule) ComponentType() string { return r.typ }
func (r capabilityEmitRule) LowerComponent(c *Component, _ LoweringContext) (LoweringResult, error) {
	part := Component{Name: c.Name, Type: "part", Properties: map[string]any{}}
	if r.forward {
		part.Traits = append(part.Traits, c.Traits...)
	}
	if r.emit != "" {
		part.Traits = append(part.Traits, Trait{Type: r.emit, Properties: map[string]any{}})
	}
	return LoweringResult{Components: []Component{part}}, nil
}

func emittedTraitTransformer(rule capabilityEmitRule) *Transformer {
	tr := NewTransformer(
		map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
		map[string]TraitHandler{
			"expose":  &stubTraitHandler{typ: "expose"},
			"metrics": &stubTraitHandler{typ: "metrics"},
		},
	)
	tr.RegisterComponentLowering(rule)
	return tr
}

func transformEmitted(t *testing.T, rule capabilityEmitRule, authored []Trait, policy Policy) error {
	t.Helper()
	return transformEmittedWith(t, emittedTraitTransformer(rule), rule, authored, policy)
}

func transformEmittedWith(t *testing.T, tr *Transformer, rule capabilityEmitRule, authored []Trait, policy Policy) error {
	t.Helper()
	_, _, err := tr.TransformWithPolicy(siblingDoc(Component{Name: "web", Type: rule.typ, Traits: authored}), TransformContext{Policy: policy})
	return err
}

func wantTraitCapabilityRefusal(t *testing.T, err error) {
	t.Helper()
	var ve *ViolationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a ViolationError, got %T: %v", err, err)
	}
	if ve.Class != RefusalTraitCapability {
		t.Fatalf("want class %v, got %v (%v)", RefusalTraitCapability, ve.Class, err)
	}
}

func TestCapability_EmittedTraitForbidden(t *testing.T) {
	err := transformEmitted(t, capabilityEmitRule{typ: "bundle", emit: "expose"}, nil,
		&constrainedPolicy{forbidden: []string{"expose"}})
	wantTraitCapabilityRefusal(t, err)
}

func TestCapability_EmittedTraitOutsideAllowedList(t *testing.T) {
	err := transformEmitted(t, capabilityEmitRule{typ: "bundle", emit: "expose"}, nil,
		&constrainedPolicy{allowed: []string{"metrics"}})
	wantTraitCapabilityRefusal(t, err)
}

func TestCapability_EmittedTraitInsideAllowedList(t *testing.T) {
	err := transformEmitted(t, capabilityEmitRule{typ: "bundle", emit: "expose"}, nil,
		&constrainedPolicy{allowed: []string{"expose"}})
	if err != nil {
		t.Fatalf("an emitted trait inside the allowed list must pass: %v", err)
	}
}

// An emitted trait counts toward required: the policy judges the output.
func TestCapability_EmittedTraitSatisfiesRequired(t *testing.T) {
	policy := &constrainedPolicy{required: []string{"expose"}}
	if err := transformEmitted(t, capabilityEmitRule{typ: "bundle", emit: "expose"}, nil, policy); err != nil {
		t.Fatalf("a required trait carried only by an emitted part must pass: %v", err)
	}
	// The same policy refuses when nothing, authored or emitted, carries it.
	wantTraitCapabilityRefusal(t, transformEmitted(t, capabilityEmitRule{typ: "bundle"}, nil, policy))
}

// Controls: a rule that only forwards authored traits changes nothing, and a
// policy with no constraints judges nothing.
func TestCapability_ForwardedTraitsAndNoopPolicyUnchanged(t *testing.T) {
	authored := []Trait{{Type: "metrics", Properties: map[string]any{}}}
	if err := transformEmitted(t, capabilityEmitRule{typ: "bundle", forward: true}, authored,
		&constrainedPolicy{allowed: []string{"metrics"}}); err != nil {
		t.Fatalf("forwarded authored trait inside the allowed list must pass: %v", err)
	}
	if err := transformEmitted(t, capabilityEmitRule{typ: "bundle", emit: "expose"}, nil, &NoopPolicy{}); err != nil {
		t.Fatalf("NoopPolicy must not refuse an emitted trait: %v", err)
	}
}

// A rule registered as launcher's own (RegisterBuiltinComponentLowering) is
// exempt: its traits are launcher's opinions, so the same rule and policy that the
// tests above refuse pass here, and a required trait it writes does not count.
func TestCapability_BuiltinRuleTraitsAreExempt(t *testing.T) {
	rule := capabilityEmitRule{typ: "bundle", emit: "expose"}
	builtin := func() *Transformer {
		tr := NewTransformer(
			map[string]ComponentHandler{"part": &pipelineComponentHandler{typ: "part"}},
			map[string]TraitHandler{"expose": &stubTraitHandler{typ: "expose"}},
		)
		tr.RegisterBuiltinComponentLowering(rule)
		return tr
	}
	for name, policy := range map[string]*constrainedPolicy{
		"forbidden":   {forbidden: []string{"expose"}},
		"not allowed": {allowed: []string{"metrics"}},
	} {
		if err := transformEmittedWith(t, builtin(), rule, nil, policy); err != nil {
			t.Errorf("%s: a builtin rule's trait must not be judged: %v", name, err)
		}
	}
	wantTraitCapabilityRefusal(t, transformEmittedWith(t, builtin(), rule, nil, &constrainedPolicy{required: []string{"expose"}}))
}

// A rule that writes a trait as a declared identity is judged under the registered name the
// policy lists use, never under the identity string.
func TestCapability_EmittedQualifiedTraitIsJudgedByRegisteredName(t *testing.T) {
	const qualified = "expose.acme.example@v1"
	rule := capabilityEmitRule{typ: "bundle", emit: qualified}
	build := func(t *testing.T) *Transformer {
		t.Helper()
		tr := emittedTraitTransformer(rule)
		if err := tr.DeclareTraitTypes(map[string]TypeID{"expose": mustID(t, qualified)}); err != nil {
			t.Fatal(err)
		}
		return tr
	}
	t.Run("forbidden", func(t *testing.T) {
		wantTraitCapabilityRefusal(t, transformEmittedWith(t, build(t), rule, nil, &constrainedPolicy{forbidden: []string{"expose"}}))
	})
	t.Run("allowed", func(t *testing.T) {
		if err := transformEmittedWith(t, build(t), rule, nil, &constrainedPolicy{allowed: []string{"expose"}}); err != nil {
			t.Fatalf("a qualified emitted trait whose registered name is allowed must pass: %v", err)
		}
	})
	t.Run("required", func(t *testing.T) {
		if err := transformEmittedWith(t, build(t), rule, nil, &constrainedPolicy{required: []string{"expose"}}); err != nil {
			t.Fatalf("a qualified emitted trait must satisfy a required registered name: %v", err)
		}
	})
	t.Run("outside allowed", func(t *testing.T) {
		wantTraitCapabilityRefusal(t, transformEmittedWith(t, build(t), rule, nil, &constrainedPolicy{allowed: []string{"metrics"}}))
	})
}
