package oam

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// Position names the slot in the OAM document tree that a lowering rule occupies.
type Position string

const (
	PositionDocument  Position = "document"
	PositionComponent Position = "component"
	PositionTrait     Position = "trait"
	PositionPolicy    Position = "policy"
)

// terminalDocumentKind is the one top-level kind the pipeline dispatches downstream
// of lowering. Mirrors the hard-coded check in validate.go.
const terminalDocumentKind = "Application"

// MaxLoweringDepth bounds the fixpoint (D7): a lowering rule that keeps re-emitting
// its own (or another registered) type fails the build with the full expansion
// chain, rather than looping forever.
const MaxLoweringDepth = 8

// ErrLoweringDepthExceeded is the cause wrapped by a LoweringError when the fixpoint
// does not settle within MaxLoweringDepth rounds.
var ErrLoweringDepthExceeded = errors.New("oam: lowering exceeded max recursion depth")

// Origin is the AUTHORED location an element came from. It is stamped once, on the
// authored element, and copied verbatim onto every element it expands into at any
// depth — never re-derived from a synthesized element. Every lowering error and every
// element's Origin() accessor lead with it, so a user always sees the YAML they wrote
// (D7: authored location first, synthesized detail second).
type Origin struct {
	Document      string // authored metadata.name
	DocumentKind  string // authored kind, e.g. "WebApplication"
	Component     string // authored component name; "" at document/policy position
	ComponentType string
	TraitType     string // authored trait type; "" unless the origin is a trait
	PolicyName    string // authored policy name; "" unless the origin is a policy
	Index         int    // index in the authored parent slice
}

// String renders the origin for error messages, e.g.
// `component "shop" (type "web-and-cache") in document "app" (kind "WebApplication")`.
func (o Origin) String() string {
	var b strings.Builder
	switch {
	case o.TraitType != "":
		fmt.Fprintf(&b, "trait %q on component %q", o.TraitType, o.Component)
	case o.PolicyName != "":
		fmt.Fprintf(&b, "policy %q", o.PolicyName)
	case o.Component != "":
		fmt.Fprintf(&b, "component %q (type %q)", o.Component, o.ComponentType)
	default:
		fmt.Fprintf(&b, "document %q", o.Document)
	}
	fmt.Fprintf(&b, " in document %q (kind %q)", o.Document, o.DocumentKind)
	return b.String()
}

// LoweringResult is what a rule returns. Which fields a rule may populate is
// position-dependent — the engine enforces it (see loweringPositionRules):
//
//	document  -> Documents only
//	component -> Components, Policies
//	trait     -> Traits, Components, Policies
//	policy    -> Policies only
//
// An entirely empty result is an error: a registered rule that emits nothing is
// indistinguishable from a deletion, which D2 does not permit.
type LoweringResult struct {
	Documents  []Application
	Components []Component
	Traits     []Trait
	Policies   []ApplicationPolicy
}

func (r LoweringResult) empty() bool {
	return len(r.Documents) == 0 && len(r.Components) == 0 && len(r.Traits) == 0 && len(r.Policies) == 0
}

// loweringPositionRules is which LoweringResult fields a rule at a given position may
// populate. A field left at its zero value is always allowed; this map lists what is
// alloweded to be *non-empty*.
var loweringPositionRules = map[Position]struct{ documents, components, traits, policies bool }{
	PositionDocument:  {documents: true},
	PositionComponent: {components: true, policies: true},
	PositionTrait:     {traits: true, components: true, policies: true},
	PositionPolicy:    {policies: true},
}

func validatePositionResult(pos Position, origin Origin, result LoweringResult) error {
	if result.empty() {
		return errors.Errorf("%s: lowering rule emitted nothing (D2 does not permit deletion)", origin)
	}
	allowed := loweringPositionRules[pos]
	if len(result.Documents) > 0 && !allowed.documents {
		return errors.Errorf("%s: a %s-position rule may not emit Documents", origin, pos)
	}
	if len(result.Components) > 0 && !allowed.components {
		return errors.Errorf("%s: a %s-position rule may not emit Components", origin, pos)
	}
	if len(result.Traits) > 0 && !allowed.traits {
		return errors.Errorf("%s: a %s-position rule may not emit Traits", origin, pos)
	}
	if len(result.Policies) > 0 && !allowed.policies {
		return errors.Errorf("%s: a %s-position rule may not emit Policies", origin, pos)
	}
	return nil
}

// LoweringContext carries exactly the D5 input set beyond the element itself (passed
// separately to each LowerXxx method) and the rule's own code: (2) the enclosing
// authored context, (3) ClusterProfile capabilities. A rule must not read anything
// else — package state, the filesystem, the clock — that would violate the
// information-closure rule.
type LoweringContext struct {
	// Document is the enclosing document as it stands at this round. Read-only: a
	// rule must not mutate it or the element pointer it was handed.
	Document *Application
	// Component is the enclosing component; nil at document and policy position.
	Component *Component
	// Capabilities is TransformContext.Capabilities (post-EvaluateProfile).
	Capabilities map[string]CapabilityBinding
	// Origin is the authored location of the element being lowered.
	Origin Origin
	// Namer allocates deterministic collision-free names (D2).
	Namer *NameAllocator
}

// NameAllocator hands out deterministic, collision-free generated names within one
// lowering run (D2). A name already claimed by a different origin is a hard error
// naming both origins — a collision fails the build, never silently overwrites.
type NameAllocator struct {
	taken map[string]Origin
}

func newNameAllocator() *NameAllocator {
	return &NameAllocator{taken: make(map[string]Origin)}
}

// Reserve claims name for origin. Reserving the same name twice for the same origin
// is a no-op; reserving it for a different origin is an error.
func (n *NameAllocator) Reserve(name string, origin Origin) error {
	if prior, ok := n.taken[name]; ok && prior != origin {
		return errors.Errorf("lowering: generated name %q collides — already used by %s, also wanted by %s", name, prior, origin)
	}
	n.taken[name] = origin
	return nil
}

// Name builds "<base>-<suffix>", validates it as a DNS-1123 subdomain, reserves it
// against origin, and returns it.
func (n *NameAllocator) Name(base, suffix string, origin Origin) (string, error) {
	name := base + "-" + suffix
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", errors.Errorf("lowering: generated name %q is not a valid DNS-1123 subdomain: %s", name, strings.Join(errs, "; "))
	}
	if err := n.Reserve(name, origin); err != nil {
		return "", err
	}
	return name, nil
}

// DocumentLoweringRule lowers a whole authored document (top-level kind, position 1).
type DocumentLoweringRule interface {
	// Kind is the authored kind this rule claims, e.g. "WebApplication". Never
	// "Application" — that is the terminal kind the rest of the pipeline dispatches.
	Kind() string
	LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error)
}

// ComponentLoweringRule lowers a component in spec.components[].
type ComponentLoweringRule interface {
	ComponentType() string
	LowerComponent(comp *Component, lctx LoweringContext) (LoweringResult, error)
}

// TraitLoweringRule lowers a trait in spec.components[].traits[].
type TraitLoweringRule interface {
	TraitType() string
	LowerTrait(trait *Trait, lctx LoweringContext) (LoweringResult, error)
}

// PolicyLoweringRule lowers a policy entry in spec.policies[].
type PolicyLoweringRule interface {
	PolicyType() string
	LowerPolicy(pol *ApplicationPolicy, lctx LoweringContext) (LoweringResult, error)
}

// LoweringStep is one edge of the expansion chain, for error reporting (D7).
type LoweringStep struct {
	Rule     string // e.g. "trait/expose"
	Position Position
	Round    int
	From     string   // element identity before lowering
	To       []string // element identities emitted
}

// LoweringError reports a failure during expansion. Error() prints the AUTHORED
// origin first, the synthesized cause second, then the expansion chain (D7).
type LoweringError struct {
	Origin Origin
	Chain  []LoweringStep
	Cause  error
}

func (e *LoweringError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "lowering %s: %v", e.Origin, e.Cause)
	for _, s := range e.Chain {
		fmt.Fprintf(&b, "\n  round %d: %s %q -> %v", s.Round, s.Rule, s.From, s.To)
	}
	return b.String()
}

func (e *LoweringError) Unwrap() error { return e.Cause }

// hasLoweringRules reports whether any rule is registered at any position.
func (t *Transformer) hasLoweringRules() bool {
	return len(t.docLoweringRules) > 0 || len(t.componentLoweringRules) > 0 ||
		len(t.traitLoweringRules) > 0 || len(t.policyLoweringRules) > 0
}

// RegisterDocumentLowering registers a document-position lowering rule. Panics if
// kind is already registered at this position, or is the terminal kind.
func (t *Transformer) RegisterDocumentLowering(r DocumentLoweringRule) {
	kind := r.Kind()
	if kind == terminalDocumentKind {
		panic("oam: document lowering rule may not claim the terminal kind " + terminalDocumentKind)
	}
	if _, exists := t.docLoweringRules[kind]; exists {
		panic("oam: document lowering rule already registered for kind " + kind)
	}
	t.docLoweringRules[kind] = r
}

// RegisterComponentLowering registers a component-position lowering rule. Panics if
// typeName is already registered at this position, or is already a dispatchable
// component handler type — a lowerable type must never also be terminal, or the
// handler would win the dispatch and the rule would never run.
func (t *Transformer) RegisterComponentLowering(r ComponentLoweringRule) {
	typeName := r.ComponentType()
	if _, exists := t.componentLoweringRules[typeName]; exists {
		panic("oam: component lowering rule already registered for type " + typeName)
	}
	if _, exists := t.componentHandlers[typeName]; exists {
		panic("oam: type " + typeName + " is already a dispatchable component handler; a lowerable type must not also be terminal")
	}
	t.componentLoweringRules[typeName] = r
}

// RegisterTraitLowering registers a trait-position lowering rule. Same duplicate/
// dispatchable-collision guard as RegisterComponentLowering.
func (t *Transformer) RegisterTraitLowering(r TraitLoweringRule) {
	typeName := r.TraitType()
	if _, exists := t.traitLoweringRules[typeName]; exists {
		panic("oam: trait lowering rule already registered for type " + typeName)
	}
	if _, exists := t.traitHandlers[typeName]; exists {
		panic("oam: type " + typeName + " is already a dispatchable trait handler; a lowerable type must not also be terminal")
	}
	t.traitLoweringRules[typeName] = r
}

// RegisterPolicyLowering registers a policy-position lowering rule. Same duplicate/
// dispatchable-collision guard as RegisterComponentLowering.
func (t *Transformer) RegisterPolicyLowering(r PolicyLoweringRule) {
	typeName := r.PolicyType()
	if _, exists := t.policyLoweringRules[typeName]; exists {
		panic("oam: policy lowering rule already registered for type " + typeName)
	}
	if _, exists := t.policyHandlers[typeName]; exists {
		panic("oam: type " + typeName + " is already a dispatchable policy handler; a lowerable type must not also be terminal")
	}
	t.policyLoweringRules[typeName] = r
}

// LowerableTypes is the set of type names accepted during parsing/validation purely
// because a registered lowering rule claims them (validateWithExtraTypes). Every name
// in it MUST disappear before the fixpoint settles: the post-fixpoint whole-document
// validation pass (D4) checks the result against an EMPTY LowerableTypes, so a type
// still present at that point is by definition a non-terminating rule.
type LowerableTypes struct {
	Kinds          []string
	ComponentTypes []string
	TraitTypes     []string
	PolicyTypes    []string
}

// LowerableTypes reports the type names claimed by rules registered on t, for callers
// that parse with ParseWithExtraTypes ahead of a transform that will use t to lower.
func (t *Transformer) LowerableTypes() LowerableTypes {
	lt := LowerableTypes{
		Kinds:          make([]string, 0, len(t.docLoweringRules)),
		ComponentTypes: make([]string, 0, len(t.componentLoweringRules)),
		TraitTypes:     make([]string, 0, len(t.traitLoweringRules)),
		PolicyTypes:    make([]string, 0, len(t.policyLoweringRules)),
	}
	for k := range t.docLoweringRules {
		lt.Kinds = append(lt.Kinds, k)
	}
	for k := range t.componentLoweringRules {
		lt.ComponentTypes = append(lt.ComponentTypes, k)
	}
	for k := range t.traitLoweringRules {
		lt.TraitTypes = append(lt.TraitTypes, k)
	}
	for k := range t.policyLoweringRules {
		lt.PolicyTypes = append(lt.PolicyTypes, k)
	}
	return lt
}

// lower runs the recursive fixpoint expansion over app (D1/D2): every round, every
// current document's non-terminal kind, components, traits, and policies are lowered
// once via their registered rule (if any); the loop repeats until a round changes
// nothing. With no rules registered anywhere, lower returns []*Application{app}
// unchanged — the SAME pointer, so no copy, no validation, and no allocation happen on
// any path that never uses lowering (the bit-identity guarantee).
func (t *Transformer) lower(app *Application, ctx TransformContext) ([]*Application, error) {
	if !t.hasLoweringRules() {
		return []*Application{app}, nil
	}

	rootOrigin := Origin{Document: app.Metadata.Name, DocumentKind: app.Kind}
	appCopy := *app // shallow: never index-mutate a shared slice element; always rebuild via new slices
	docs := []*Application{&appCopy}
	namer := newNameAllocator()
	var chain []LoweringStep

	for round := 0; ; round++ {
		if round >= MaxLoweringDepth {
			return nil, &LoweringError{Origin: rootOrigin, Chain: chain, Cause: ErrLoweringDepthExceeded}
		}
		nextDocs := make([]*Application, 0, len(docs))
		changed := false
		for _, doc := range docs {
			expanded, docChanged, steps, err := t.lowerDocumentOnce(doc, ctx, namer, round)
			chain = append(chain, steps...)
			if err != nil {
				return nil, &LoweringError{Origin: rootOrigin, Chain: chain, Cause: err}
			}
			if docChanged {
				changed = true
			}
			nextDocs = append(nextDocs, expanded...)
		}
		docs = nextDocs
		if !changed {
			return docs, nil
		}
	}
}

// lowerDocumentOnce performs ONE round's worth of lowering for a single document: if
// its kind is non-terminal, it is replaced wholesale via its document rule (its
// contents get their own turn next round); otherwise its components, their traits,
// and its policies are each lowered once in place.
func (t *Transformer) lowerDocumentOnce(doc *Application, ctx TransformContext, namer *NameAllocator, round int) ([]*Application, bool, []LoweringStep, error) {
	if doc.Kind != terminalDocumentKind {
		rule, ok := t.docLoweringRules[doc.Kind]
		if !ok {
			// No rule claims this kind. The whole-document validator (validate.go)
			// is the backstop that rejects an unknown non-terminal kind with a
			// proper, origin-carrying error; lower() itself passes it through.
			return []*Application{doc}, false, nil, nil
		}
		origin, _ := doc.Origin()
		if origin == (Origin{}) {
			origin = Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind}
		}
		lctx := LoweringContext{Document: doc, Capabilities: ctx.Capabilities, Origin: origin, Namer: namer}
		result, err := rule.LowerDocument(doc, lctx)
		if err != nil {
			return nil, false, nil, errors.Wrapf(err, "%s", origin)
		}
		if verr := validatePositionResult(PositionDocument, origin, result); verr != nil {
			return nil, false, nil, verr
		}
		emitted := make([]*Application, len(result.Documents))
		names := make([]string, len(result.Documents))
		for i := range result.Documents {
			result.Documents[i].origin = &origin
			emitted[i] = &result.Documents[i]
			names[i] = result.Documents[i].Metadata.Name
		}
		step := LoweringStep{Rule: "document/" + doc.Kind, Position: PositionDocument, Round: round, From: doc.Metadata.Name, To: names}
		return emitted, true, []LoweringStep{step}, nil
	}

	changed, steps, err := t.lowerDocumentBody(doc, ctx, namer, round)
	if err != nil {
		return nil, false, steps, err
	}
	return []*Application{doc}, changed, steps, nil
}

// lowerDocumentBody lowers, in place on doc, one round's worth of component-type
// lowering (or else trait lowering on components left unmatched), and one round's
// worth of policy lowering. Elements emitted this round are appended unprocessed —
// they get their own turn next round, which is what makes recursion (e.g. a component
// rule emitting a still-higher-level component) work.
func (t *Transformer) lowerDocumentBody(doc *Application, ctx TransformContext, namer *NameAllocator, round int) (bool, []LoweringStep, error) {
	changed := false
	var steps []LoweringStep
	newComponents := make([]Component, 0, len(doc.Spec.Components))
	var pendingPolicies []ApplicationPolicy

	for i := range doc.Spec.Components {
		comp := doc.Spec.Components[i]
		compOrigin := Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind, Component: comp.Name, ComponentType: comp.Type, Index: i}

		if rule, ok := t.componentLoweringRules[comp.Type]; ok {
			lctx := LoweringContext{Document: doc, Component: &comp, Capabilities: ctx.Capabilities, Origin: compOrigin, Namer: namer}
			result, err := rule.LowerComponent(&comp, lctx)
			if err != nil {
				return false, steps, errors.Wrapf(err, "%s", compOrigin)
			}
			if err := validatePositionResult(PositionComponent, compOrigin, result); err != nil {
				return false, steps, err
			}
			names := make([]string, len(result.Components))
			for j := range result.Components {
				result.Components[j].origin = &compOrigin
				names[j] = result.Components[j].Name
			}
			newComponents = append(newComponents, result.Components...)
			for j := range result.Policies {
				result.Policies[j].origin = &compOrigin
				pendingPolicies = append(pendingPolicies, result.Policies[j])
			}
			steps = append(steps, LoweringStep{Rule: "component/" + comp.Type, Position: PositionComponent, Round: round, From: comp.Name, To: names})
			changed = true
			continue
		}

		newTraits := make([]Trait, 0, len(comp.Traits))
		for k := range comp.Traits {
			trait := comp.Traits[k]
			traitOrigin := Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind, Component: comp.Name, ComponentType: comp.Type, TraitType: trait.Type, Index: k}

			rule, ok := t.traitLoweringRules[trait.Type]
			if !ok {
				newTraits = append(newTraits, trait)
				continue
			}
			lctx := LoweringContext{Document: doc, Component: &comp, Capabilities: ctx.Capabilities, Origin: traitOrigin, Namer: namer}
			result, err := rule.LowerTrait(&trait, lctx)
			if err != nil {
				return false, steps, errors.Wrapf(err, "%s", traitOrigin)
			}
			if err := validatePositionResult(PositionTrait, traitOrigin, result); err != nil {
				return false, steps, err
			}
			names := make([]string, len(result.Traits))
			for j := range result.Traits {
				result.Traits[j].origin = &traitOrigin
				result.Traits[j].sealed = true
				names[j] = result.Traits[j].Type
			}
			newTraits = append(newTraits, result.Traits...)
			for j := range result.Components {
				result.Components[j].origin = &traitOrigin
				newComponents = append(newComponents, result.Components[j])
			}
			for j := range result.Policies {
				result.Policies[j].origin = &traitOrigin
				pendingPolicies = append(pendingPolicies, result.Policies[j])
			}
			steps = append(steps, LoweringStep{Rule: "trait/" + trait.Type, Position: PositionTrait, Round: round, From: trait.Type, To: names})
			changed = true
		}
		comp.Traits = newTraits
		newComponents = append(newComponents, comp)
	}
	doc.Spec.Components = newComponents

	newPolicies := make([]ApplicationPolicy, 0, len(doc.Spec.Policies))
	for i := range doc.Spec.Policies {
		pol := doc.Spec.Policies[i]
		polOrigin := Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind, PolicyName: pol.Name, Index: i}

		rule, ok := t.policyLoweringRules[pol.Type]
		if !ok {
			newPolicies = append(newPolicies, pol)
			continue
		}
		lctx := LoweringContext{Document: doc, Capabilities: ctx.Capabilities, Origin: polOrigin, Namer: namer}
		result, err := rule.LowerPolicy(&pol, lctx)
		if err != nil {
			return false, steps, errors.Wrapf(err, "%s", polOrigin)
		}
		if err := validatePositionResult(PositionPolicy, polOrigin, result); err != nil {
			return false, steps, err
		}
		names := make([]string, len(result.Policies))
		for j := range result.Policies {
			result.Policies[j].origin = &polOrigin
			names[j] = result.Policies[j].Name
		}
		newPolicies = append(newPolicies, result.Policies...)
		steps = append(steps, LoweringStep{Rule: "policy/" + pol.Type, Position: PositionPolicy, Round: round, From: pol.Name, To: names})
		changed = true
	}
	newPolicies = append(newPolicies, pendingPolicies...)
	doc.Spec.Policies = newPolicies

	return changed, steps, nil
}
