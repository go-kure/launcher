package oam

import (
	"testing"
)

// targetComponentRule lowers a component of type typ into one component per
// entry of emit, each named after the authored one. It declares targets.
type targetComponentRule struct {
	typ     string
	targets LoweringTargets
	emit    []string
	traits  []string
}

func (r targetComponentRule) ComponentType() string { return r.typ }

func (r targetComponentRule) LoweringTargets() LoweringTargets { return r.targets }

func (r targetComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	var out LoweringResult
	for i, typ := range r.emit {
		c := Component{Name: comp.Name, Type: typ, Properties: map[string]any{}}
		if i == 0 {
			for _, trait := range r.traits {
				c.Traits = append(c.Traits, Trait{Type: trait, Properties: map[string]any{}})
			}
		}
		out.Components = append(out.Components, c)
	}
	return out, nil
}

// versionedTargetComponentRule is targetComponentRule with a contract version, so
// its rule identity carries the "@<version>" suffix.
type versionedTargetComponentRule struct {
	targetComponentRule
	version string
}

func (r versionedTargetComponentRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: r.typ, Version: r.version}
}

// undeclaredComponentRule is targetComponentRule without the declaration: the
// shape of every rule written before LoweringTargets existed.
type undeclaredComponentRule struct {
	typ    string
	emit   string
	traits []string
}

func (r undeclaredComponentRule) ComponentType() string { return r.typ }

func (r undeclaredComponentRule) LowerComponent(comp *Component, _ LoweringContext) (LoweringResult, error) {
	c := Component{Name: comp.Name, Type: r.emit, Properties: map[string]any{}}
	for _, trait := range r.traits {
		c.Traits = append(c.Traits, Trait{Type: trait, Properties: map[string]any{}})
	}
	return LoweringResult{Components: []Component{c}}, nil
}

// targetTraitRule lowers a trait of type typ into a trait of type emit.
type targetTraitRule struct {
	typ     string
	emit    string
	targets LoweringTargets
}

func (r targetTraitRule) TraitType() string { return r.typ }

func (r targetTraitRule) LoweringTargets() LoweringTargets { return r.targets }

func (r targetTraitRule) LowerTrait(*Trait, LoweringContext) (LoweringResult, error) {
	return LoweringResult{Traits: []Trait{{Type: r.emit, Properties: map[string]any{}}}}, nil
}

// targetPolicyRule lowers a policy of type typ into a policy of type emit.
type targetPolicyRule struct {
	typ     string
	emit    string
	targets LoweringTargets
}

func (r targetPolicyRule) PolicyType() string { return r.typ }

func (r targetPolicyRule) LoweringTargets() LoweringTargets { return r.targets }

func (r targetPolicyRule) LowerPolicy(pol *ApplicationPolicy, _ LoweringContext) (LoweringResult, error) {
	return LoweringResult{Policies: []ApplicationPolicy{{Name: pol.Name, Type: r.emit}}}, nil
}

// targetDocRule is a document rule that declares targets; it is never run here.
type targetDocRule struct {
	kind    string
	targets LoweringTargets
}

func (r targetDocRule) Kind() string { return r.kind }

func (r targetDocRule) LoweringTargets() LoweringTargets { return r.targets }

func (r targetDocRule) LowerDocument(*Application, LoweringContext) (LoweringResult, error) {
	return LoweringResult{}, nil
}

// targetRawRule is a raw document rule that declares targets; it is never run here.
type targetRawRule struct {
	testRawRule
	targets LoweringTargets
}

func (r targetRawRule) LoweringTargets() LoweringTargets { return r.targets }

// terminalApp is an authored Application of the terminal kind.
func terminalApp(components ...Component) *Application {
	app := makeApp("app", components...)
	app.APIVersion = SupportedAPIVersion
	app.Kind = terminalDocumentKind
	return app
}

// TestSeal_RefusesARuleWhoseTargetsAreNotRegistered: a rule that declares the
// types it lowers into is refused while one of them has neither a handler nor a
// lowering rule, and the error names the rule and each missing type.
func TestSeal_RefusesARuleWhoseTargetsAreNotRegistered(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(targetComponentRule{typ: "role", targets: LoweringTargets{
		ComponentTypes: []string{"kind-a", "kind-b"},
		TraitTypes:     []string{"spread"},
		PolicyTypes:    []string{"order"},
	}})

	const want = `registry incomplete: ` +
		`lowering rule component/role lowers into component type "kind-b", which is not registered; ` +
		`lowering rule component/role lowers into policy type "order", which is not registered; ` +
		`lowering rule component/role lowers into trait type "spread", which is not registered`
	err := tr.Seal()
	if err == nil || err.Error() != want {
		t.Fatalf("Seal() = %v\nwant     %s", err, want)
	}
	// The check stores nothing: a second call says the same.
	if again := tr.Seal(); again == nil || again.Error() != want {
		t.Errorf("second Seal() = %v\nwant            %s", again, want)
	}
}

// TestTransform_RefusesAnIncompleteRegistry: Transform runs the same check
// itself, before it reads the document, so the refusal does not wait for a
// document that uses the rule.
func TestTransform_RefusesAnIncompleteRegistry(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	tr.RegisterComponentLowering(targetComponentRule{typ: "role", emit: []string{"kind-a", "kind-b"},
		targets: LoweringTargets{ComponentTypes: []string{"kind-a", "kind-b"}}})

	const want = `registry incomplete: lowering rule component/role lowers into component type "kind-b", which is not registered`
	// The document holds no "role" component at all.
	doc := terminalApp(Component{Name: "plain", Type: "kind-a", Properties: map[string]any{}})
	for name, transform := range map[string]func() error{
		"Transform": func() error { _, err := tr.Transform(doc, TransformContext{}); return err },
		"TransformWithPolicy": func() error {
			_, _, err := tr.TransformWithPolicy(doc, TransformContext{})
			return err
		},
	} {
		if err := transform(); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v\nwant %s", name, err, want)
		}
	}
}

// TestSeal_RegistrationOrderIsFree: the check runs on the registry as it stands,
// so a rule registered before its targets is accepted once they are there. This
// is the order a registry is naturally built in (component rules before trait
// handlers), which a check at registration would refuse.
func TestSeal_RegistrationOrderIsFree(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(targetComponentRule{typ: "role", emit: []string{"kind-a", "kind-b"}, traits: []string{"spread"},
		targets: LoweringTargets{
			ComponentTypes: []string{"kind-a", "kind-b"},
			TraitTypes:     []string{"spread"},
			PolicyTypes:    []string{"order"},
		}})
	if err := tr.Seal(); err == nil {
		t.Fatal("Seal() accepted a rule with no target registered")
	}
	tr.RegisterPolicy("order", &stubPolicyHandler{typ: "order"})
	tr.RegisterTrait("spread", &stubTraitHandler{typ: "spread"})
	tr.RegisterComponent("kind-b", &pipelineComponentHandler{typ: "kind-b"})
	tr.RegisterComponent("kind-a", &pipelineComponentHandler{typ: "kind-a"})
	if err := tr.Seal(); err != nil {
		t.Fatalf("Seal() with every target registered: %v", err)
	}
	if _, err := tr.Transform(terminalApp(Component{Name: "web", Type: "role", Properties: map[string]any{}}), TransformContext{}); err != nil {
		t.Errorf("Transform with every target registered: %v", err)
	}
}

// TestSeal_ATargetMayBeALoweringRule: a target another rule lowers further is
// registered, at each of the three positions.
func TestSeal_ATargetMayBeALoweringRule(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(targetComponentRule{typ: "outer", targets: LoweringTargets{
		ComponentTypes: []string{"inner"},
		TraitTypes:     []string{"inner-trait"},
		PolicyTypes:    []string{"inner-policy"},
	}})
	tr.RegisterComponentLowering(undeclaredComponentRule{typ: "inner", emit: "anything"})
	tr.RegisterTraitLowering(targetTraitRule{typ: "inner-trait", emit: "anything"})
	tr.RegisterPolicyLowering(targetPolicyRule{typ: "inner-policy", emit: "anything"})
	if err := tr.Seal(); err != nil {
		t.Errorf("Seal() = %v, want nil: each target is claimed by a lowering rule", err)
	}
}

// TestSeal_EveryRulePositionIsChecked: the declaration is read on a rule of any
// kind, and the identity in the message is the one Origin.Rule and
// LoweringStep.Rule carry, version included.
func TestSeal_EveryRulePositionIsChecked(t *testing.T) {
	missing := LoweringTargets{ComponentTypes: []string{"gone"}}
	for name, tc := range map[string]struct {
		register func(*Transformer)
		rule     string
	}{
		"component": {func(tr *Transformer) {
			tr.RegisterComponentLowering(targetComponentRule{typ: "role", targets: missing})
		}, "component/role"},
		"versioned component": {func(tr *Transformer) {
			tr.RegisterComponentLowering(versionedTargetComponentRule{targetComponentRule{typ: "role", targets: missing}, "v2"})
		}, "component/role@v2"},
		"trait": {func(tr *Transformer) {
			tr.RegisterTraitLowering(targetTraitRule{typ: "shape", targets: missing})
		}, "trait/shape"},
		"policy": {func(tr *Transformer) {
			tr.RegisterPolicyLowering(targetPolicyRule{typ: "rollout", targets: missing})
		}, "policy/rollout"},
		"document": {func(tr *Transformer) {
			tr.RegisterDocumentLowering(targetDocRule{kind: "WebApplication", targets: missing})
		}, "document/WebApplication"},
		"raw document": {func(tr *Transformer) {
			tr.RegisterRawDocumentLowering(targetRawRule{testRawRule{kind: "WebApplication"}, missing})
		}, "rawdocument/" + SupportedAPIVersion + "/WebApplication"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewTransformer(nil, nil)
			tc.register(tr)
			want := `registry incomplete: lowering rule ` + tc.rule + ` lowers into component type "gone", which is not registered`
			if err := tr.Seal(); err == nil || err.Error() != want {
				t.Errorf("Seal() = %v\nwant     %s", err, want)
			}
		})
	}
}

// TestSeal_ARuleThatDeclaresNothingIsNotChecked: the declaration is optional. A
// rule without it is accepted as before, and what it emits is found missing
// only when a document uses it.
func TestSeal_ARuleThatDeclaresNothingIsNotChecked(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterComponentLowering(undeclaredComponentRule{typ: "role", emit: "kind-b"})
	if err := tr.Seal(); err != nil {
		t.Fatalf("Seal() = %v, want nil for a rule that declares no targets", err)
	}
	if err := NewTransformer(nil, nil).Seal(); err != nil {
		t.Errorf("Seal() on an empty registry = %v, want nil", err)
	}
}

// TestTransform_MissingComponentHandlerNamesTheComponentAndTheRule: the error
// for a component whose type has no handler names the component, and for one a
// lowering rule emitted, the rule and the authored component it was lowered
// from.
func TestTransform_MissingComponentHandlerNamesTheComponentAndTheRule(t *testing.T) {
	t.Run("authored", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		_, err := tr.Transform(terminalApp(Component{Name: "web", Type: "kind-b", Properties: map[string]any{}}), TransformContext{})
		const want = `no handler for component type "kind-b" (component "web")`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant  %s", err, want)
		}
	})
	// A lowered document is validated before it is dispatched, and a type that is
	// neither one the package knows nor registered is refused there. So the
	// transform reaches a rule's output without a handler only for a type the
	// package knows: "service" here.
	t.Run("emitted by a rule", func(t *testing.T) {
		tr := NewTransformer(nil, nil)
		tr.RegisterComponentLowering(undeclaredComponentRule{typ: "role", emit: "service"})
		_, err := tr.Transform(terminalApp(Component{Name: "web", Type: "role", Properties: map[string]any{}}), TransformContext{})
		const want = `no handler for component type "service" (component "web", emitted by lowering rule component/role ` +
			`for component "web" (type "role") in document "app" (kind "Application"))`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant  %s", err, want)
		}
	})
}

// TestTransform_MissingTraitHandlerNamesTheComponentAndTheRule is the trait
// counterpart: the component the trait is on, and the rule that emitted it.
func TestTransform_MissingTraitHandlerNamesTheComponentAndTheRule(t *testing.T) {
	handlers := map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}
	t.Run("authored", func(t *testing.T) {
		tr := NewTransformer(handlers, nil)
		_, err := tr.Transform(terminalApp(Component{Name: "web", Type: "kind-a", Properties: map[string]any{},
			Traits: []Trait{{Type: "spread", Properties: map[string]any{}}}}), TransformContext{})
		const want = `no handler for trait type "spread" (on component "web")`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant  %s", err, want)
		}
	})
	t.Run("emitted by a component rule", func(t *testing.T) {
		tr := NewTransformer(handlers, nil)
		tr.RegisterComponentLowering(undeclaredComponentRule{typ: "role", emit: "kind-a", traits: []string{"topology-spread"}})
		_, err := tr.Transform(terminalApp(Component{Name: "web", Type: "role", Properties: map[string]any{}}), TransformContext{})
		const want = `no handler for trait type "topology-spread" (on component "web", emitted by lowering rule component/role ` +
			`for component "web" (type "role") in document "app" (kind "Application"))`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant  %s", err, want)
		}
	})
	t.Run("delivery type keeps its hint", func(t *testing.T) {
		tr := NewTransformer(handlers, nil)
		_, err := tr.Transform(terminalApp(Component{Name: "web", Type: "kind-a", Properties: map[string]any{},
			Traits: []Trait{{Type: "fluxcd-patches", Properties: map[string]any{}}}}), TransformContext{})
		const want = `no handler for trait type "fluxcd-patches" (on component "web"): it configures delivery, ` +
			`which launcher leaves to the consumer that delivers the application; ` +
			`a consumer that delivers through Flux registers its own handler`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v\nwant  %s", err, want)
		}
	})
}

// TestTransform_MissingPolicyHandlerNamesThePolicy: the policy's own name, which
// is what tells two policies of one type apart.
func TestTransform_MissingPolicyHandlerNamesThePolicy(t *testing.T) {
	tr := NewTransformer(map[string]ComponentHandler{"kind-a": &pipelineComponentHandler{typ: "kind-a"}}, nil)
	app := terminalApp(Component{Name: "web", Type: "kind-a", Properties: map[string]any{}})
	app.Spec.Policies = []ApplicationPolicy{{Name: "first", Type: "order"}}
	_, err := tr.Transform(app, TransformContext{})
	const want = `no handler for policy type "order" (policy "first")`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant  %s", err, want)
	}
}

// contractPolicy is a stub PolicyHandler that declares contract metadata.
type contractPolicy struct{ stubPolicyHandler }

func (h *contractPolicy) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: h.typ, Version: "v1"}
}

// contractPolicyRule is a policy lowering rule that declares contract metadata.
type contractPolicyRule struct{ targetPolicyRule }

func (r contractPolicyRule) ContractMetadata() ContractMetadata {
	return ContractMetadata{Family: r.typ, Version: "v2"}
}

// TestHandlerContracts_IncludesPolicies: policies publish from both of their
// registries, as they do in HandlerSchemas, and a policy does not leak into the
// component or trait map.
func TestHandlerContracts_IncludesPolicies(t *testing.T) {
	tr := NewTransformer(nil, nil)
	tr.RegisterPolicy("order", &contractPolicy{stubPolicyHandler{typ: "order"}})
	tr.RegisterPolicy("plain", &stubPolicyHandler{typ: "plain"})
	tr.RegisterPolicyLowering(contractPolicyRule{targetPolicyRule{typ: "rollout", emit: "order"}})

	set := tr.HandlerContracts()
	if got := set.Policies["order"]; got.Family != "order" || got.Version != "v1" {
		t.Errorf(`Policies["order"] = %+v, want family order, version v1`, got)
	}
	if got := set.Policies["rollout"]; got.Family != "rollout" || got.Version != "v2" {
		t.Errorf(`Policies["rollout"] = %+v, want family rollout, version v2`, got)
	}
	if _, ok := set.Policies["plain"]; ok {
		t.Error("a policy handler without ContractMetadata must be omitted")
	}
	if len(set.Policies) != 2 || len(set.Components) != 0 || len(set.Traits) != 0 {
		t.Errorf("HandlerContracts() = %+v, want two policies and nothing else", set)
	}
}

// TestHandlerContracts_MapsNonNil: every map is non-nil on an empty registry, so
// a caller can range or index without a nil check.
func TestHandlerContracts_MapsNonNil(t *testing.T) {
	set := NewTransformer(nil, nil).HandlerContracts()
	if set.Components == nil || set.Traits == nil || set.Policies == nil {
		t.Errorf("HandlerContracts() on an empty transformer = %+v, want every map non-nil", set)
	}
}
