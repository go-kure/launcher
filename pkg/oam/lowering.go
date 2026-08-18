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
//
// The budget is MaxLoweringDepth-1 real expansion rounds plus one settling round: the
// guard in runLowering fires only once a round is entered that would exceed this
// count, so a chain performing exactly MaxLoweringDepth-1 genuine expansions still
// gets the one extra round it needs to observe that nothing changed and succeed
// (round-7 Codex finding, lowering.go — a chain doing exactly the then-budget's worth
// of real expansions failed because that observing round was never allowed to run).
// Set one higher than the intended real-expansion ceiling (8) for exactly that
// reason, rather than changing the guard's comparison itself, which the existing
// depth-limit tests below assert against symbolically (MaxLoweringDepth), not a
// hardcoded round count.
const MaxLoweringDepth = 9

// ErrLoweringDepthExceeded is the cause wrapped by a LoweringError when the fixpoint
// does not settle within MaxLoweringDepth rounds.
var ErrLoweringDepthExceeded = errors.New("oam: lowering exceeded max recursion depth")

// Origin is the AUTHORED location an element came from. It is stamped once, on the
// authored element, and copied verbatim onto every element it expands into at any
// depth — never re-derived from a synthesized element. Every lowering error and every
// element's Origin() accessor lead with it, so a user always sees the YAML they wrote
// (D7: authored location first, synthesized detail second). The one exception is the
// Rule field: unlike every other field here, it is deliberately re-derived at every
// lowering hop rather than copied verbatim — see its own doc comment below.
type Origin struct {
	Document     string // authored metadata.name
	DocumentKind string // authored kind, e.g. "WebApplication"
	// Namespace is the authored document's metadata.namespace. It exists on Origin
	// purely so two elements authored in DIFFERENT namespaces are never treated as
	// the same Origin by identity/equality (NameAllocator.Reserve's prior.origin !=
	// origin check, and LowerRaws' own duplicate-input check) — a name collision
	// within one namespace is real, the identical collision across two disjoint
	// namespaces is not. Deliberately excluded from String(): every existing
	// caller of String() already identifies a document by name+kind, and adding
	// namespace there would be a message-format change independent of this field's
	// actual purpose.
	Namespace     string
	Component     string // authored component name; "" at document/policy position
	ComponentType string
	TraitType     string // authored trait type; "" unless the origin is a trait
	PolicyName    string // authored policy name; "" unless the origin is a policy
	Index         int    // index in the authored parent slice

	// Rule identifies the lowering rule that MOST RECENTLY produced the element
	// carrying this Origin: "<label>/<type>" (e.g. "trait/expose"), suffixed with
	// "@<version>" when the rule also implements ContractDescriber (handler.go) and
	// declares a non-empty ContractMetadata().Version (e.g. "trait/expose@v1"). label
	// is "document"/"component"/"trait"/"policy" (the Position the rule occupies) for
	// every ordinary lowering rule, or "rawdocument" specifically for a
	// RawDocumentLoweringRule dispatched via LowerRaws (lowering_raw.go) — matching
	// the rule-class label LoweringStep.Rule already used for that path, so the two
	// provenance surfaces agree on which rule produced a raw-entered document. ""
	// means the element was never itself the direct output of a lowering rule
	// invocation — it is exactly as authored, or a descendant carried through
	// untouched (e.g. a component forwarded verbatim by a document rule — see
	// isForwardedComponent below for how that case is told apart from one the rule
	// actually produced).
	//
	// Unlike every other field on Origin, Rule is DELIBERATELY NOT part of the
	// "stamped once, copied verbatim" contract documented above: it is re-derived
	// every time a rule fires on an element's lineage, so it always names the
	// IMMEDIATE producer rather than the first rule in a multi-hop chain. A naive
	// verbatim copy would leave a round-1 rule's identity on an element that a
	// round-2 rule went on to actually produce — wrong, since "which rule produced
	// this element" is a per-expansion fact, not an authored-location fact. Origin's
	// other fields stay verbatim precisely because Document/Component/etc. name the
	// AUTHORED origin, which by definition never changes across rounds; Rule names
	// the most recent SYNTHESIS step, which by definition does. Every call site that
	// stamps a rule's LoweringResult onto Origin sets Rule immediately before doing
	// so (lowering.go, lowering_raw.go) via loweringRuleIdentity; a call site that
	// merely carries an existing Origin forward (no rule fired this round) leaves it
	// untouched, so it keeps whatever value — possibly "" — it already had.
	//
	// Granularity: set once per rule INVOCATION (every element one LowerXxx call
	// emits shares the same Rule string), not per emitted element's own resulting
	// type — a rule that emits several differently-typed elements in one call
	// (LoweringResult permits it) does not get a different Rule per element. This
	// matches the granularity Origin's other synthesized-detail fields already use
	// (one stamp per invocation) and avoids needing a per-element Origin copy at
	// every emission site — today every element from one invocation shares one
	// Origin pointer (see the stamping loops in lowering.go), and Rule preserves
	// that.
	Rule string
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

// loweringRuleIdentity formats the identity of the lowering rule that produced an
// emitted element, for Origin.Rule: "<label>/<type>" (e.g. "trait/expose"), matching
// the identical convention LoweringStep.Rule already uses at each of these call sites
// — suffixed with "@<version>" when rule also implements ContractDescriber
// (handler.go) and declares a non-empty ContractMetadata().Version.
//
// label is a plain string, not Position, because the raw-document call site
// (lowering_raw.go) must pass "rawdocument" — the rule-class label LoweringStep.Rule
// already used there before Origin.Rule existed, distinguishing a
// RawDocumentLoweringRule from an ordinary DocumentLoweringRule — even though both
// validate their LoweringResult against PositionDocument (the tree slot the two rule
// kinds occupy is identical; only the provenance label differs). Every other call
// site passes string(PositionX), so Origin.Rule and LoweringStep.Rule stay in
// lockstep by construction.
//
// rule is `any` rather than one of the five lowering-rule interfaces
// (DocumentLoweringRule, RawDocumentLoweringRule, ComponentLoweringRule,
// TraitLoweringRule, PolicyLoweringRule) because they share no common type; the only
// thing this function actually needs is the optional ContractDescriber assertion.
func loweringRuleIdentity(label string, typeName string, rule any) string {
	id := label + "/" + typeName
	if cd, ok := rule.(ContractDescriber); ok {
		if v := cd.ContractMetadata().Version; v != "" {
			id += "@" + v
		}
	}
	return id
}

// sameAuthoredLocation reports whether o and other name the same AUTHORED location —
// every Origin field EXCEPT Rule. Rule records synthesis provenance (which lowering
// rule most recently produced the element), not authored identity, and by design
// changes across rounds for what is otherwise the identical conceptual origin (see
// Rule's doc comment): two siblings emitted from ONE rule invocation share an Origin
// whose Rule reflects that invocation, but if one sibling is itself lowered further in
// a later round, ITS descendants' inherited origin then carries the LATER rule's
// identity too — a difference that must not, by itself, make NameAllocator.Reserve
// (the one caller that needs this) treat them as different origins. Every other field
// keeps full "stamped once, copied verbatim" identity semantics, so comparing them
// directly is correct.
func (o Origin) sameAuthoredLocation(other Origin) bool {
	other.Rule = o.Rule
	return o == other
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
	// rule must not mutate it or the element pointer it was handed. nil for a
	// RawDocumentLoweringRule, whose document is the decoded value passed to
	// LowerDocument and has no *Application form yet (lowering_raw.go).
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
	taken map[string]nameClaim
	// round is the fixpoint round currently being processed, set by runLowering
	// before it dispatches any rule in that round. Recorded on every claim purely
	// to make Reserve's error message more specific (same round vs. an earlier
	// round) — see Reserve. It is NOT used to treat any repeat claim as a
	// legitimate no-op: Origin carries no per-sibling discriminator (two elements
	// independently emitted from one authored origin — LoweringResult's doc
	// comment — share one Origin value), so a repeat claim for the same origin in
	// a later round cannot be safely told apart from a genuinely different
	// sibling colliding with an earlier one.
	round int
}

// nameClaim records which origin claimed a generated name, and in which round, so
// Reserve's error message can say whether the collision was within one round or
// across rounds.
type nameClaim struct {
	origin Origin
	round  int
}

func newNameAllocator() *NameAllocator {
	return &NameAllocator{taken: make(map[string]nameClaim)}
}

// reservedIdentity is one pre-existing document identity runLowering claims against its
// NameAllocator before any rule runs — see runLowering's preReserved parameter.
type reservedIdentity struct {
	name   string
	origin Origin
}

// Reserve claims name for origin. Reserving an already-claimed name is always an
// error, regardless of round or whether the origin matches the prior claim's.
//
// An earlier version of this function treated a repeat claim for the SAME origin in
// a LATER round as a no-op — "the same conceptual element re-affirming a name it
// already owns". That is unsound: LoweringResult carries no per-sibling
// discriminator, so when one origin's rule invocation emits two elements (e.g. a
// component-position rule emitting a Component and a Policy, or a trait rule
// emitting several), both share the identical Origin value. If those elements then
// take a different number of further rounds to settle, their own name-generating
// calls can land in different rounds — and the no-op carve-out let a genuine
// collision between two such siblings through silently whenever that happened. No
// registered rule in this codebase currently relies on re-deriving an identical name
// across rounds for the same conceptual element (verified: nothing outside this
// package's own tests calls NameAllocator.Name); a rule that legitimately needs to
// must derive a name that varies with something the engine can tell apart (e.g. fold
// a stable per-sibling index into the name itself), since the engine cannot perform
// that disambiguation on its behalf.
func (n *NameAllocator) Reserve(name string, origin Origin) error {
	// Keyed on (namespace, name), not name alone: two documents authored in
	// different namespaces (Origin.Namespace) may legitimately generate the same
	// child name — they lower to namespace-disjoint resources, exactly as two
	// same-named Kubernetes resources in different namespaces do not collide. A
	// bare-name key would reject that valid case, undermining LowerRaws's own
	// namespace-scoped duplicate-document detection (rawDocKey, lowering_raw.go).
	key := origin.Namespace + "\x00" + name
	if prior, ok := n.taken[key]; ok {
		// sameAuthoredLocation, not a raw !=: Origin.Rule deliberately differs across
		// rounds for what is still the same conceptual origin (see Rule's doc
		// comment) — a raw struct compare would misroute a legitimate cross-round
		// sibling collision (the very case this function's own doc comment above
		// exists to catch) into the "different origin entirely" message branch below.
		if !prior.origin.sameAuthoredLocation(origin) {
			return errors.Errorf("lowering: generated name %q collides — already used by %s, also wanted by %s", name, prior.origin, origin)
		}
		if prior.round == n.round {
			return errors.Errorf("lowering: generated name %q collides — %s already used it for a different emitted element in the same lowering round", name, origin)
		}
		return errors.Errorf("lowering: generated name %q collides — %s already used it in an earlier lowering round", name, origin)
	}
	n.taken[key] = nameClaim{origin: origin, round: n.round}
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

// DocumentLoweringRule lowers a whole authored document whose YAML ALREADY fits
// ApplicationSpec (types.go — Components and Policies, nothing else), so
// ParseWithExtraTypes can decode it into *Application with KnownFields(true)
// (parser.go) before any transform runs. Registered with RegisterDocumentLowering;
// reachable only from the in-transform entry point, where lowerDocumentOnce always
// supplies a genuine *Application.
type DocumentLoweringRule interface {
	// Kind is the authored kind this rule claims, e.g. "WebApplication". Never
	// the terminal kind "Application".
	Kind() string
	LowerDocument(doc *Application, lctx LoweringContext) (LoweringResult, error)
}

// RawDocumentLoweringRule lowers a whole authored document whose field set does NOT
// fit ApplicationSpec — a whole-noun higher-level kind carrying its own authored
// fields. Such a document cannot survive ParseWithExtraTypes at all, so this rule is
// reachable ONLY from LowerRaws, which hands it the authored bytes and lets it choose
// its own decode target.
//
// This interface deliberately does NOT embed DocumentLoweringRule. The two
// LowerDocument signatures differ, and Go forbids two methods of the same name on one
// concrete type, so no single type can satisfy both interfaces: passing a
// RawDocumentLoweringRule value to RegisterDocumentLowering does not compile (the
// mutual exclusion is structural, enforced at compile time rather than by a runtime
// guard). What the type system cannot catch is two DIFFERENT concrete types, one of
// each flavour, claiming the same Kind() string — that is what the registrars below
// check.
type RawDocumentLoweringRule interface {
	Kind() string
	// DecodeDocument decodes ONE authored document's raw bytes into whatever Go
	// type this rule wants LowerDocument to receive. Decode strictly —
	// yaml.NewDecoder + KnownFields(true) — so an authored typo in a
	// kind-specific field fails here instead of being silently dropped; this
	// pass is the only place that document is ever parsed against its own
	// schema.
	DecodeDocument(raw []byte) (any, error)
	// LowerDocument receives exactly what this rule's own DecodeDocument
	// returned, so the assertion doc.(*MyKind) holds as long as the SAME
	// concrete type implements both methods consistently. Go does not check
	// that across two methods of one interface, so it is a convention this
	// interface relies on, not a compiler guarantee — assert with the
	// two-value form and return an error on failure rather than panicking.
	LowerDocument(doc any, lctx LoweringContext) (LoweringResult, error)
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

// hasLoweringRules reports whether any rule registered on t can fire on the
// IN-TRANSFORM path. t.rawDocLoweringRules is excluded on purpose: nothing on that
// path ever consults it — lowerDocumentOnce reads t.docLoweringRules only — so
// counting it would make lower() copy and re-validate a document no reachable rule
// can touch, losing the pointer-identity guarantee for a Transformer that registers
// raw rules only.
func (t *Transformer) hasLoweringRules() bool {
	return len(t.docLoweringRules) > 0 || len(t.componentLoweringRules) > 0 ||
		len(t.traitLoweringRules) > 0 || len(t.policyLoweringRules) > 0
}

// RegisterDocumentLowering registers an in-transform document rule. Panics if kind is
// empty, is the terminal kind, is already registered here, or is already claimed by
// RegisterRawDocumentLowering.
func (t *Transformer) RegisterDocumentLowering(r DocumentLoweringRule) {
	kind := r.Kind()
	if kind == "" {
		panic("oam: document lowering rule may not claim an empty kind")
	}
	if kind == terminalDocumentKind {
		panic("oam: document lowering rule may not claim the terminal kind " + terminalDocumentKind)
	}
	if _, exists := t.rawDocLoweringRules[kind]; exists {
		panic("oam: kind " + kind + " is already registered via RegisterRawDocumentLowering; a kind may be claimed by at most one registrar")
	}
	if _, exists := t.docLoweringRules[kind]; exists {
		panic("oam: document lowering rule already registered for kind " + kind)
	}
	t.docLoweringRules[kind] = r
}

// RegisterRawDocumentLowering registers a raw-entry document rule. Same four guards,
// in the other direction.
func (t *Transformer) RegisterRawDocumentLowering(r RawDocumentLoweringRule) {
	kind := r.Kind()
	if kind == "" {
		panic("oam: raw document lowering rule may not claim an empty kind")
	}
	if kind == terminalDocumentKind {
		panic("oam: raw document lowering rule may not claim the terminal kind " + terminalDocumentKind)
	}
	if _, exists := t.docLoweringRules[kind]; exists {
		panic("oam: kind " + kind + " is already registered via RegisterDocumentLowering; a kind may be claimed by at most one registrar")
	}
	if _, exists := t.rawDocLoweringRules[kind]; exists {
		panic("oam: raw document lowering rule already registered for kind " + kind)
	}
	t.rawDocLoweringRules[kind] = r
}

// RegisterComponentLowering registers a component-position lowering rule. Panics if
// typeName is already registered at this position, or is already a dispatchable
// component handler type — a lowerable type must never also be terminal, or the
// handler would win the dispatch and the rule would never run.
func (t *Transformer) RegisterComponentLowering(r ComponentLoweringRule) {
	typeName := r.ComponentType()
	if typeName == "" {
		panic("oam: component lowering rule may not claim an empty type")
	}
	if _, exists := t.componentLoweringRules[typeName]; exists {
		panic("oam: component lowering rule already registered for type " + typeName)
	}
	if _, exists := t.componentHandlers[typeName]; exists {
		panic("oam: type " + typeName + " is already a dispatchable component handler; a lowerable type must not also be terminal")
	}
	t.componentLoweringRules[typeName] = r
}

// RegisterTraitLowering registers a trait-position lowering rule. Same duplicate/
// dispatchable-collision guard as RegisterComponentLowering, plus the same
// CapabilityAware⇒ValidateAndApplyDefaults guard RegisterTrait enforces for a
// dispatchable TraitHandler: EvaluateProfile's trait-lowering-rule fallback
// (transform.go) needs ValidateAndApplyDefaults to validate/default a
// capability-rendered binding before use, and silently accepts it unvalidated and
// undefaulted otherwise (transform.go's rule-registry fallback, "evaluated[key] =
// binding" with no defaulting/validation). Without this check here, a future
// TraitLoweringRule could implement CapabilityAware without ValidateAndApplyDefaults
// and skip validation with no registration-time signal — exactly the class of bug
// design-lowering-engine.md's "friction #2" records as fixed for "expose"
// specifically (ExposeRule implements both today), but not closed structurally on
// this general registration path until now.
func (t *Transformer) RegisterTraitLowering(r TraitLoweringRule) {
	typeName := r.TraitType()
	if typeName == "" {
		panic("oam: trait lowering rule may not claim an empty type")
	}
	if _, exists := t.traitLoweringRules[typeName]; exists {
		panic("oam: trait lowering rule already registered for type " + typeName)
	}
	if _, exists := t.traitHandlers[typeName]; exists {
		panic("oam: type " + typeName + " is already a dispatchable trait handler; a lowerable type must not also be terminal")
	}
	if _, ok := r.(CapabilityAware); ok {
		if _, ok := r.(ValidateAndApplyDefaults); !ok {
			panic("oam: trait lowering rule for type " + typeName + " implements CapabilityAware but not ValidateAndApplyDefaults")
		}
	}
	t.traitLoweringRules[typeName] = r
}

// RegisterBuiltinTraitLowering is RegisterTraitLowering for launcher's own built-in
// trait-position lowering rules (e.g. "expose"). It marks typeName in the same
// builtinTraitTypes set RegisterBuiltinTrait uses, so EvaluateProfile's
// traitLoweringRules branch exempts it from CapabilityDefinition schema application
// exactly like a built-in TraitHandler is exempt — a caller loading a
// CapabilityDefinition that happens to share a built-in lowering rule's type name
// (e.g. "expose") must not have it silently applied to that rule's rendering.
func (t *Transformer) RegisterBuiltinTraitLowering(r TraitLoweringRule) {
	t.RegisterTraitLowering(r)
	t.builtinTraitTypes[r.TraitType()] = true
}

// RegisterPolicyLowering registers a policy-position lowering rule. Same duplicate/
// dispatchable-collision guard as RegisterComponentLowering.
func (t *Transformer) RegisterPolicyLowering(r PolicyLoweringRule) {
	typeName := r.PolicyType()
	if typeName == "" {
		panic("oam: policy lowering rule may not claim an empty type")
	}
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
//
// It reads t.docLoweringRules and deliberately never t.rawDocLoweringRules. A
// raw-registered kind is by definition a kind whose authored field set ApplicationSpec
// cannot hold, so it is reachable only from LowerRaws, which runs BEFORE any parse.
// Admitting it here would tell ParseWithExtraTypes to accept a document its strict
// decode must then reject on the unknown fields anyway — with a worse message, and
// only after the one entry point that could have lowered it was skipped. Do not "fix"
// this by merging the two maps.
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

// loweringDoc is one in-flight document inside a fixpoint run.
//
// Exactly one of doc and raw is set. raw is non-nil only for a seed entry that
// entered through LowerRaws and has not been decoded yet: its decode + first
// LowerDocument call happens in round 0 of the loop below, the same round in
// which an already-parsed document's own non-terminal-Kind dispatch happens.
// There is deliberately no pre-round outside the loop, so a raw-entered document
// gets exactly the same MaxLoweringDepth budget as an in-transform one.
type loweringDoc struct {
	doc  *Application            // set once parsed (in-transform) or decoded (round 0)
	raw  []byte                  // set only on an undecoded LowerRaws seed entry
	rule RawDocumentLoweringRule // the rule claiming raw's kind; set iff raw != nil

	// origin is AUTHORED provenance, for error attribution only. Children
	// inherit their parent's verbatim at any depth (see Origin) — an Origin is
	// never re-derived from a synthesized element.
	origin Origin
	// slot is the index of the LowerRaws input this document descends from,
	// used ONLY to splice output back into input order. Always 0 for t.lower,
	// which has exactly one input. Distinct from origin by construction:
	// unique per raw input, whereas two raw inputs could share an Origin
	// (same authored name and kind).
	slot int
}

// runLowering is the ONE fixpoint implementation in this package. Both entry points
// call it exactly once per invocation, so one NameAllocator (D2), one expansion chain
// and one MaxLoweringDepth budget (D7) are shared by every document in the call,
// siblings from different raw inputs included. seed must be non-empty.
//
// preReserved claims every identity in it against namer before any rule runs. LowerRaws
// uses this to register each pass-through document's own (namespace, name) — a document
// it never decodes or hands to a rule, so it would otherwise never touch namer at all.
// Without this, a claimed raw document's rule could generate a child document sharing a
// pass-through document's exact identity: namer.Reserve would see no prior claim for
// that key and let it through, and LowerRaws would then return two Application entries
// with the same (namespace, name) — a real duplicate-identity output, not merely a
// missed diagnostic, even though the generating rule used the collision API correctly.
// t.lower has no pass-through concept (a single in-transform document has nothing else
// in its batch to collide with), so it always passes nil.
func (t *Transformer) runLowering(seed []loweringDoc, ctx TransformContext, preReserved []reservedIdentity) ([]loweringDoc, error) {
	namer := newNameAllocator()
	for _, r := range preReserved {
		if err := namer.Reserve(r.name, r.origin); err != nil {
			return nil, &LoweringError{Origin: r.origin, Cause: err}
		}
	}
	var chain []LoweringStep
	cur := seed
	culprit := seed[0].origin // first document still expanding in the latest round

	for round := 0; ; round++ {
		if round >= MaxLoweringDepth {
			return nil, &LoweringError{Origin: culprit, Chain: chain, Cause: ErrLoweringDepthExceeded}
		}
		// Every rule dispatched below this point belongs to this round — see the
		// NameAllocator.round doc comment for why Reserve needs to know it.
		namer.round = round
		next := make([]loweringDoc, 0, len(cur))
		changed := false
		for _, d := range cur {
			var (
				expanded   []*Application
				docChanged bool
				steps      []LoweringStep
				err        error
			)
			if d.raw != nil {
				expanded, steps, err = t.lowerRawOnce(d, ctx, namer, round)
				docChanged = true
			} else {
				expanded, docChanged, steps, err = t.lowerDocumentOnce(d.doc, ctx, namer, round)
			}
			chain = append(chain, steps...)
			if err != nil {
				// Attribute to the document whose expansion actually failed, never
				// to one closure-captured root.
				return nil, &LoweringError{Origin: d.origin, Chain: chain, Cause: err}
			}
			if docChanged {
				if !changed {
					culprit = d.origin
				}
				changed = true
			}
			for _, doc := range expanded {
				next = append(next, loweringDoc{doc: doc, origin: d.origin, slot: d.slot})
			}
		}
		cur = next
		if !changed {
			for _, d := range cur {
				if err := t.validateSettled(d.doc); err != nil {
					return nil, &LoweringError{Origin: d.origin, Chain: chain, Cause: err}
				}
			}
			return cur, nil
		}
	}
}

// validateSettled is D4's whole-document validation pass, applied to ONE settled
// document so the caller can attribute a failure to that document's own authored
// origin — a batched pass over every document returns the first failure with no index
// into them, which is adequate only while every document shares one root origin.
//
// The check runs against a LowerableTypes carrying no *Kinds* or *TraitTypes* — a
// document kind or component/trait type still present after the fixpoint settles is,
// for those two positions, by construction not claimed by any registered rule, so it
// is a non-terminating rule's leftover rather than a legitimate terminal type. Custom
// trait types from --capability-def stay accepted via customTraitTypes: they are
// terminal handler types, not lowering claims. ComponentTypes is populated below with
// every registered ComponentHandler type for the identical reason customTraitTypes
// exists on the trait side (see the loop below) — a registered component handler is
// a terminal type whether or not a CapabilityDefinition matches it.
func (t *Transformer) validateSettled(doc *Application) error {
	customTraitTypes := make(map[string]bool, len(t.capabilityDefs)+len(t.traitHandlers))
	for name := range t.capabilityDefs {
		customTraitTypes[name] = true
	}
	// A registered TraitHandler (transform.go) is a terminal type whether or not a
	// CapabilityDefinition was loaded for it — e.g. one accepted via
	// ParseWithExtraTypes/ParseWithExtraTraitTypes with no matching definition file.
	// Building this allowlist from t.capabilityDefs alone would reject such a trait
	// purely because some OTHER lowering rule is registered on the same Transformer
	// (that is what routes execution through validateSettled at all — hasLoweringRules).
	for name := range t.traitHandlers {
		customTraitTypes[name] = true
	}
	// Mirror the trait-handler allowlist above for registered ComponentHandler types —
	// same rationale: a custom component type accepted via ParseWithExtraTypes and
	// backed by a registered handler is terminal, and must not be rejected purely
	// because some OTHER lowering rule routes execution through validateSettled.
	//
	// Passed via customComponentTypes, NOT LowerableTypes{ComponentTypes: ...}: the
	// latter also tells validateTrait to defer the trait/component restriction check
	// (componentIsLowerable, validate.go), which is correct only for a component that
	// is still going to be rewritten by a ComponentLoweringRule. A componentHandlers
	// entry is the opposite — a terminal type that has already settled — so routing it
	// through LowerableTypes.ComponentTypes would wrongly let a terminal component skip
	// the restriction recheck this function exists to re-run post-settlement.
	customComponentTypes := make(map[string]bool, len(t.componentHandlers))
	for name := range t.componentHandlers {
		customComponentTypes[name] = true
	}
	return validateWithExtraTypes(doc, customTraitTypes, customComponentTypes, LowerableTypes{})
}

// lower runs the recursive fixpoint expansion over app (D1/D2): every round, every
// current document's non-terminal kind, components, traits, and policies are lowered
// once via their registered rule (if any); the loop repeats until a round changes
// nothing. With no rules registered anywhere, lower returns []*Application{app}
// unchanged — the SAME pointer, so no copy, no validation, and no allocation happen on
// any path that never uses lowering (the bit-identity guarantee). Registering raw
// rules only leaves that guarantee intact: they are unreachable from here.
func (t *Transformer) lower(app *Application, ctx TransformContext) ([]*Application, error) {
	if !t.hasLoweringRules() {
		return []*Application{app}, nil
	}

	appCopy := *app // shallow: never index-mutate a shared slice element; always rebuild via new slices
	seed := []loweringDoc{{
		doc:    &appCopy,
		origin: Origin{Document: app.Metadata.Name, DocumentKind: app.Kind, Namespace: app.Metadata.Namespace},
		slot:   0,
	}}
	settled, err := t.runLowering(seed, ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]*Application, len(settled))
	for i := range settled {
		out[i] = settled[i].doc
	}
	return out, nil
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
			origin = Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind, Namespace: doc.Metadata.Namespace}
		}
		lctx := LoweringContext{Document: doc, Capabilities: ctx.Capabilities, Origin: origin, Namer: namer}
		result, err := rule.LowerDocument(doc, lctx)
		if err != nil {
			return nil, false, nil, errors.Wrapf(err, "%s", origin)
		}
		if verr := validatePositionResult(PositionDocument, origin, result); verr != nil {
			return nil, false, nil, verr
		}
		// Rule is re-derived here, not carried over from origin's own (possibly
		// already-set) value — see Origin.Rule's doc comment. doc.Kind is still the
		// PRE-lowering kind at this point (doc itself is untouched; the new documents
		// live in result.Documents), so this correctly names the rule that just fired,
		// not whatever rule (if any) produced the input doc.
		origin.Rule = loweringRuleIdentity(string(PositionDocument), doc.Kind, rule)
		// Snapshot the components doc had BEFORE the rule ran: a rule that forwards
		// one of them unchanged into its output (rather than constructing a fresh
		// component) is forwarding its already-authored traits too, not synthesizing
		// them — same reasoning as originalTraits below for the component-position
		// case, extended to however many components the document rule forwards.
		originalComponents := doc.Spec.Components
		emitted := make([]*Application, len(result.Documents))
		names := make([]string, len(result.Documents))
		for i := range result.Documents {
			result.Documents[i].origin = &origin
			emitted[i] = &result.Documents[i]
			names[i] = result.Documents[i].Metadata.Name
			if err := t.validateEmittedDocument(emitted[i]); err != nil {
				return nil, false, nil, errors.Wrapf(err, "%s", origin)
			}
			for j := range result.Documents[i].Spec.Components {
				comp := &result.Documents[i].Spec.Components[j]
				// Round-11-batch-2 Codex finding (lowering.go:717 as reviewed): only
				// the emitted Application itself was stamped above; its nested
				// components and policies were left unstamped, falling through to
				// lowerDocumentBody's fallback derivation a round later (or, for an
				// already-terminal component/policy no later round ever touches,
				// never stamped at all — Origin() would return false on the final
				// settled output). Stamp explicitly here, the same treatment
				// component/trait-position rule output already gets
				// (result.Components[j].origin = &compOrigin below). Safe to do
				// unconditionally for every field EXCEPT Rule: origin.Document/
				// DocumentKind/Namespace are already the correct authored-root values
				// (copied from doc.Origin() above, stable across any number of
				// chained document-rule rounds), and a forwarded component's own
				// Name/Type are unchanged by construction, so recomputing those here
				// reproduces whatever value they would already carry. Rule is
				// different: it names WHICH rule most recently PRODUCED the element
				// (Origin.Rule's doc comment), and this document rule did not produce
				// a component it merely forwarded verbatim from originalComponents —
				// only a freshly synthesized component was actually output by it.
				// isForwardedComponent mirrors isForwardedTrait's pointer-identity
				// check just below (same false-negative tradeoff, documented there):
				// a forwarded component keeps whatever Rule it already carried
				// (possibly "", if never itself the direct output of an earlier
				// rule) instead of being misattributed to this document rule.
				compOrigin := Origin{Document: origin.Document, DocumentKind: origin.DocumentKind, Namespace: origin.Namespace, Component: comp.Name, ComponentType: comp.Type, Index: j, Rule: origin.Rule}
				if isForwardedComponent(comp, originalComponents) {
					if prior, ok := comp.Origin(); ok {
						compOrigin.Rule = prior.Rule
					} else {
						compOrigin.Rule = ""
					}
				}
				comp.origin = &compOrigin
				if err := t.sealNestedTraitsInDocument(comp, compOrigin, originalComponents); err != nil {
					return nil, false, nil, errors.Wrapf(err, "%s", origin)
				}
			}
			for k := range result.Documents[i].Spec.Policies {
				pol := &result.Documents[i].Spec.Policies[k]
				polOrigin := Origin{Document: origin.Document, DocumentKind: origin.DocumentKind, Namespace: origin.Namespace, PolicyName: pol.Name, Index: k, Rule: origin.Rule}
				pol.origin = &polOrigin
			}
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
	// Origin doctrine (see Origin): every element in this document inherits the
	// document's AUTHORED provenance. Once a document-position rule has renamed a
	// document, doc.Metadata.Name/doc.Kind hold a SYNTHESIZED identity while
	// doc.Origin() still holds the authored one, so the per-element origins below
	// are derived from the stamped value and fall back to the current identity only
	// for a document the fixpoint has never stamped — the same fallback
	// lowerDocumentOnce applies at document position.
	docOrigin, _ := doc.Origin()
	if docOrigin == (Origin{}) {
		docOrigin = Origin{Document: doc.Metadata.Name, DocumentKind: doc.Kind, Namespace: doc.Metadata.Namespace}
	}

	changed := false
	var steps []LoweringStep
	newComponents := make([]Component, 0, len(doc.Spec.Components))
	var pendingPolicies []ApplicationPolicy

	for i := range doc.Spec.Components {
		comp := doc.Spec.Components[i]
		// A component that was itself emitted by a round-N component/trait-position
		// rule already carries its own stamped authored origin (set at the emission
		// site below, and at the trait-position emission site further down) — consult
		// it first, exactly like the docOrigin fallback above, rather than re-deriving
		// one from the component's current (possibly already-renamed) name/type. Only
		// a component the fixpoint has never stamped falls back to the synthesized
		// form.
		compOrigin, ok := comp.Origin()
		if !ok {
			compOrigin = Origin{Document: docOrigin.Document, DocumentKind: docOrigin.DocumentKind, Namespace: docOrigin.Namespace, Component: comp.Name, ComponentType: comp.Type, Index: i}
		}

		if rule, ok := t.componentLoweringRules[comp.Type]; ok {
			// Snapshot the traits attached BEFORE the rule runs: a rule that preserves
			// them by returning Traits: comp.Traits (or listing the same elements) is
			// forwarding already-authored traits, not synthesizing new ones — see
			// sealEmittedNestedTraits's forwarded-trait carve-out below.
			originalTraits := comp.Traits
			// D3: reject an authored value for a platform-reserved property before the
			// rule runs — the same check the trait-lowering-rule dispatch below performs
			// (enforcePlatformReserved, further down this function) and applyTraits/
			// createApplications (transform.go) perform for a dispatchable handler.
			// Round-9 Codex regression: this was missing here, so a ComponentLoweringRule
			// — reachable via the same public RegisterComponentLowering extension point a
			// TraitLoweringRule uses — could accept an authored platform-reserved value
			// with no enforcement at all.
			if p, ok := rule.(PropertySchemaProvider); ok {
				if err := enforcePlatformReserved(p.PropertySchema(), comp.Properties, "properties"); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
			}
			lctx := LoweringContext{Document: doc, Component: &comp, Capabilities: ctx.Capabilities, Origin: compOrigin, Namer: namer}
			result, err := rule.LowerComponent(&comp, lctx)
			if err != nil {
				return false, steps, errors.Wrapf(err, "%s", compOrigin)
			}
			if err := validatePositionResult(PositionComponent, compOrigin, result); err != nil {
				return false, steps, err
			}
			// Rule is re-derived here — see Origin.Rule's doc comment. compOrigin was
			// captured into lctx.Origin (above) BEFORE this line runs, so the rule
			// itself still saw its INPUT's prior identity; only the OUTPUT stamped
			// below carries this invocation's own.
			compOrigin.Rule = loweringRuleIdentity(string(PositionComponent), comp.Type, rule)
			names := make([]string, len(result.Components))
			for j := range result.Components {
				result.Components[j].origin = &compOrigin
				names[j] = result.Components[j].Name
				if err := t.validateEmittedComponent(&result.Components[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
				if err := t.sealEmittedNestedTraits(&result.Components[j], compOrigin, originalTraits); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
			}
			newComponents = append(newComponents, result.Components...)
			for j := range result.Policies {
				result.Policies[j].origin = &compOrigin
				if err := t.validateEmittedPolicy(&result.Policies[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
				// A component-position rule may also emit Policies
				// (loweringPositionRules); include them in the step's To so the
				// recorded chain reflects everything this round actually emitted,
				// not only the position's primary field.
				names = append(names, result.Policies[j].Name)
				pendingPolicies = append(pendingPolicies, result.Policies[j])
			}
			steps = append(steps, LoweringStep{Rule: "component/" + comp.Type, Position: PositionComponent, Round: round, From: comp.Name, To: names})
			changed = true
			continue
		}

		newTraits := make([]Trait, 0, len(comp.Traits))
		for k := range comp.Traits {
			trait := comp.Traits[k]
			// Same fallback as compOrigin above: a trait already stamped by an earlier
			// round (e.g. a sealed trait re-claimed by a second TraitLoweringRule) keeps
			// its own authored origin instead of one re-derived from its current type.
			traitOrigin, traitOK := trait.Origin()
			if !traitOK {
				// Derive from compOrigin (already resolved above, itself falling back to
				// comp.Origin() first), not from comp.Name/comp.Type directly. Round-9
				// Codex regression: a forwarded trait (sealEmittedNestedTraits' carve-out
				// below deliberately leaves it unstamped) previously fell back to
				// doc.Metadata.Name/comp.Name/comp.Type here — the CURRENT, possibly
				// synthesized identity — instead of the already-correctly-resolved
				// authored identity compOrigin holds, losing Origin's "authored location
				// first" doctrine (see Origin's doc comment) for exactly the case
				// (a renamed component forwarding its original traits unchanged) that
				// doctrine exists to cover.
				traitOrigin = compOrigin
				traitOrigin.TraitType = trait.Type
				traitOrigin.Index = k
			}

			rule, ok := t.traitLoweringRules[trait.Type]
			if !ok {
				newTraits = append(newTraits, trait)
				continue
			}
			// A sealed trait was emitted by an earlier lowering round, which already
			// merged capability rendering into it (D5) before the fixpoint settled —
			// the information-closure rule does not allow a second, different-key
			// merge here (a fifth input), mirroring applyTraits' identical guard
			// (transform.go). Every capability-processing step below is skipped
			// entirely for a sealed trait; its Properties are final.
			resolvedTrait := trait
			if !trait.sealed {
				// CapabilityAware is engine-enforced here exactly as applyTraits
				// enforces it for a dispatchable TraitHandler: a lowering rule that
				// needs a ClusterProfile capability and finds none fails with
				// ErrMissingCapability, since the rule itself never runs through
				// applyTraits.
				if aware, ok := rule.(CapabilityAware); ok && aware.CapabilityRequired() {
					key := buildCapabilityKey(trait)
					_, foundScoped := ctx.Capabilities[key]
					_, foundBare := ctx.Capabilities[trait.Type]
					if !foundScoped && !foundBare {
						return false, steps, errors.Wrapf(ErrMissingCapability, "%s: capability %q not found in ClusterProfile", traitOrigin, key)
					}
				}
				// For a custom (non-built-in) trait type whose capability rendering
				// resolved in the profile, warn or (under SetStrictCapabilities) error
				// when no CapabilityDefinition was loaded for the type — the same check
				// applyTraits performs for a dispatchable TraitHandler (transform.go). A
				// TraitLoweringRule never runs through applyTraits, so without this it
				// silently bypasses strict mode entirely for a lowering-rule-consumed
				// capability (round-7 Codex finding, lowering.go).
				if !t.builtinTraitTypes[trait.Type] {
					key := buildCapabilityKey(trait)
					_, foundScoped := ctx.Capabilities[key]
					_, foundBare := ctx.Capabilities[trait.Type]
					if foundScoped || foundBare {
						if _, hasDef := t.capabilityDefs[trait.Type]; !hasDef {
							msg := fmt.Sprintf("no CapabilityDefinition found for custom trait %q", trait.Type)
							if t.strictCapabilities {
								return false, steps, errors.Errorf("%s: %s", traitOrigin, msg)
							}
							if t.warnHandler != nil {
								t.warnHandler(msg)
							}
						}
					}
				}
				// D3: reject an authored value for a platform-reserved property before
				// capability rendering is merged in. applyTraits (transform.go, for a
				// dispatchable handler) and createApplications (transform.go, for a
				// component handler) perform the same check at their own merge points,
				// alongside honoring Trait.sealed in applyTraits.
				if p, ok := rule.(PropertySchemaProvider); ok {
					if err := enforcePlatformReserved(p.PropertySchema(), trait.Properties, "properties"); err != nil {
						return false, steps, errors.Wrapf(err, "%s", traitOrigin)
					}
				}
				// Capability rendering is merged in before the rule runs (D5 input 3),
				// the same merge applyTraits performs for a dispatchable handler — so a
				// TraitLoweringRule sees the identical "rendering as defaults, inline
				// wins" view a TraitHandler would.
				resolvedTrait = resolveCapability(trait, ctx.Capabilities)
			}
			lctx := LoweringContext{Document: doc, Component: &comp, Capabilities: ctx.Capabilities, Origin: traitOrigin, Namer: namer}
			result, err := rule.LowerTrait(&resolvedTrait, lctx)
			if err != nil {
				return false, steps, errors.Wrapf(err, "%s", traitOrigin)
			}
			if err := validatePositionResult(PositionTrait, traitOrigin, result); err != nil {
				return false, steps, err
			}
			// Rule is re-derived here — see Origin.Rule's doc comment.
			traitOrigin.Rule = loweringRuleIdentity(string(PositionTrait), trait.Type, rule)
			names := make([]string, len(result.Traits))
			for j := range result.Traits {
				result.Traits[j].origin = &traitOrigin
				result.Traits[j].sealed = true
				names[j] = result.Traits[j].Type
				if err := t.validateEmittedTrait(&result.Traits[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
			}
			newTraits = append(newTraits, result.Traits...)
			for j := range result.Components {
				result.Components[j].origin = &traitOrigin
				if err := t.validateEmittedComponent(&result.Components[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				if err := t.sealEmittedNestedTraits(&result.Components[j], traitOrigin, nil); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				// A trait-position rule may also emit Components (loweringPositionRules);
				// include them in the step's To — see the matching comment on the
				// component-position block above.
				names = append(names, result.Components[j].Name)
				newComponents = append(newComponents, result.Components[j])
			}
			for j := range result.Policies {
				result.Policies[j].origin = &traitOrigin
				if err := t.validateEmittedPolicy(&result.Policies[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				names = append(names, result.Policies[j].Name)
				pendingPolicies = append(pendingPolicies, result.Policies[j])
			}
			steps = append(steps, LoweringStep{Rule: "trait/" + trait.Type, Position: PositionTrait, Round: round, From: trait.Type, To: names})
			changed = true
		}
		comp.Traits = newTraits
		newComponents = append(newComponents, comp)
	}

	// doc.Spec.Components is deliberately NOT updated here, before the policy loop
	// below runs. lctx.Document (== doc) is the same pointer handed to every rule
	// in this round; component/trait rules above see doc.Spec.Components as it
	// stood at the START of this round (they read from newComponents/local
	// variables, never from doc.Spec.Components directly). Assigning newComponents
	// into doc here would make a policy rule further down see THIS round's
	// component output while a component rule earlier in the SAME round saw the
	// PRE-round document — an inconsistent, traversal-order-dependent snapshot,
	// contradicting LoweringContext.Document's own doc comment ("the enclosing
	// document as it stands at this round"). Both newComponents and newPolicies
	// are committed to doc together, after every rule in this round has run.

	newPolicies := make([]ApplicationPolicy, 0, len(doc.Spec.Policies))
	for i := range doc.Spec.Policies {
		pol := doc.Spec.Policies[i]
		// Same fallback as compOrigin/traitOrigin above: a policy already stamped by an
		// earlier round (emitted from a component/trait-position rule, then re-claimed
		// by a policy-position rule) keeps its own authored origin.
		polOrigin, polOK := pol.Origin()
		if !polOK {
			polOrigin = Origin{Document: docOrigin.Document, DocumentKind: docOrigin.DocumentKind, Namespace: docOrigin.Namespace, PolicyName: pol.Name, Index: i}
		}

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
		// Rule is re-derived here — see Origin.Rule's doc comment.
		polOrigin.Rule = loweringRuleIdentity(string(PositionPolicy), pol.Type, rule)
		names := make([]string, len(result.Policies))
		for j := range result.Policies {
			result.Policies[j].origin = &polOrigin
			names[j] = result.Policies[j].Name
			if err := t.validateEmittedPolicy(&result.Policies[j]); err != nil {
				return false, steps, errors.Wrapf(err, "%s", polOrigin)
			}
		}
		newPolicies = append(newPolicies, result.Policies...)
		steps = append(steps, LoweringStep{Rule: "policy/" + pol.Type, Position: PositionPolicy, Round: round, From: pol.Name, To: names})
		changed = true
	}
	newPolicies = append(newPolicies, pendingPolicies...)
	doc.Spec.Components = newComponents
	doc.Spec.Policies = newPolicies

	return changed, steps, nil
}

// sealEmittedNestedTraits stamps origin and seals every NEWLY SYNTHESIZED trait
// nested inside an emitted component (comp.Traits), the same treatment a
// trait-position rule's directly-emitted Traits already get
// (result.Traits[j].sealed = true, above). A component/trait-position rule
// constructs comp with Go struct literals, from a different package — it cannot set
// the unexported Trait.sealed field itself — so a nested trait it hard-codes into an
// emitted component is, without this, silently indistinguishable from an authored
// one. If that nested trait's type has no registered TraitLoweringRule (most cases:
// it is a terminal, dispatchable-only type), it flows unprocessed straight through to
// the settled document and then to applyTraits (transform.go), which — with sealed
// left false — merges capability rendering into it exactly as it would for an
// authored trait: a rendering the emitting rule already accounted for once, an
// unwanted "fifth input" (D5), or an ErrMissingCapability failure for a capability
// the settled document was never authored to require. Sealing here does not stop the
// trait from being picked up by a registered TraitLoweringRule next round —
// lowerDocumentBody's own trait branch dispatches on trait.Type regardless of
// trait.sealed, exactly as it already does for a trait sealed at trait-position (the
// "second TraitLoweringRule" case its own comment documents) — it only marks the
// trait as already-final if no such rule exists to claim it.
//
// forwarded is the traits slice the component/trait had BEFORE this rule ran
// (round-7 Codex finding, lowering.go:945). A rule that preserves attached authored
// traits by returning them unchanged — e.g. `Traits: comp.Traits` — is not
// synthesizing anything for them: those traits were never touched by the rule and
// must undergo the SAME capability processing any other authored trait gets, either
// via a registered TraitLoweringRule next round or via applyTraits at settle time.
// Sealing them anyway would (per the reasoning above) skip that processing entirely,
// silently dropping capability rendering for a forwarded CapabilityAware trait such
// as expose. A trait found in forwarded, by pointer identity, is left untouched here
// — no origin stamp, no seal, no emitted-trait validation — exactly as if it still
// belonged to a component no rule had ever claimed.
func (t *Transformer) sealEmittedNestedTraits(comp *Component, parentOrigin Origin, forwarded []Trait) error {
	return t.sealNestedTraits(comp, parentOrigin, func(trait *Trait) bool {
		return isForwardedTrait(trait, forwarded)
	})
}

// sealNestedTraitsInDocument is sealEmittedNestedTraits' document-position analogue
// (round-9-batch-2 Codex finding, lowering.go:717): a DocumentLoweringRule emits a
// whole *Application, potentially with several components, each potentially
// forwarding traits from ANY of the original document's components (not just one) —
// e.g. by reorganizing doc.Spec.Components across several output documents. A single
// []Trait forwarded slice cannot express "forwarded from one of N original
// components", so this checks pointer identity against every original component's
// own Traits slice instead of one.
func (t *Transformer) sealNestedTraitsInDocument(comp *Component, parentOrigin Origin, originalComponents []Component) error {
	return t.sealNestedTraits(comp, parentOrigin, func(trait *Trait) bool {
		for i := range originalComponents {
			if isForwardedTrait(trait, originalComponents[i].Traits) {
				return true
			}
		}
		return false
	})
}

// sealNestedTraits is the shared body: stamp origin and seal every trait in
// comp.Traits that isForwarded reports false for — the "hard-coded by the emitting
// rule, not forwarded from an authored input" traits sealEmittedNestedTraits' own doc
// comment (above) describes.
func (t *Transformer) sealNestedTraits(comp *Component, parentOrigin Origin, isForwarded func(*Trait) bool) error {
	for k := range comp.Traits {
		trait := &comp.Traits[k]
		if isForwarded(trait) {
			continue
		}
		nestedOrigin := parentOrigin
		nestedOrigin.TraitType = trait.Type
		nestedOrigin.Index = k
		trait.origin = &nestedOrigin
		trait.sealed = true
		if err := t.validateEmittedTrait(trait); err != nil {
			return err
		}
	}
	return nil
}

// isForwardedTrait reports whether trait is literally one of the elements of
// original — i.e. the same Trait struct forwarded unchanged by a lowering rule,
// rather than a new value the rule constructed. Pointer identity (not a value/deep
// comparison) is deliberate: it matches exactly the `Traits: comp.Traits` idiom the
// round-7 finding describes, without risking a false match against a rule that
// legitimately constructs a NEW trait whose type and properties happen to equal an
// authored one.
func isForwardedTrait(trait *Trait, original []Trait) bool {
	for i := range original {
		if trait == &original[i] {
			return true
		}
	}
	return false
}

// isForwardedComponent is isForwardedTrait's component-position counterpart, used
// only by lowerDocumentOnce's document-rule branch above: it reports whether comp is
// literally one of the elements of original — i.e. the same Component struct
// forwarded unchanged by a DocumentLoweringRule (e.g. via `Components:
// doc.Spec.Components`, or a sub-slice of it that shares the same backing array),
// rather than a new value the rule constructed. Pointer identity is deliberate,
// matching isForwardedTrait's own tradeoff: a rule that builds a brand new slice by
// copying an original component BY VALUE (e.g. appending into a freshly allocated
// backing array) is treated as having synthesized a new component, not forwarded
// one, even though the value is byte-for-byte unchanged — the same false-negative
// isForwardedTrait already accepts, for the same reason (no risk of a false match
// against a rule that legitimately constructs a new component equal to an authored
// one).
func isForwardedComponent(comp *Component, original []Component) bool {
	for i := range original {
		if comp == &original[i] {
			return true
		}
	}
	return false
}
