package oam

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
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
	// the same Origin by identity/equality (NameAllocator's (namespace, name) key and
	// its sameAuthoredLocation check) — a name collision
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
	// is "document"/"component"/"trait"/"policy" (the Position the rule occupies).
	// A RawDocumentLoweringRule never stamps an Origin: LowerRaws serializes its
	// output as authored input, and only the LoweringStep.Rule of its step in a
	// LoweringError chain names it, as "rawdocument/<apiVersion>/<kind>". ""
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
// kinds occupy is identical; only the provenance label differs). That call site
// passes "<apiVersion>/<kind>" as typeName, since the raw registry is keyed on the
// pair and two raw rules may claim one kind under different groups; a kind alone
// would not name the rule that fired. Every other call site passes
// string(PositionX) and a bare type name, so Origin.Rule and LoweringStep.Rule stay
// in lockstep by construction.
//
// rule is `any` rather than one of the five lowering-rule interfaces
// (DocumentLoweringRule, RawDocumentLoweringRule, ComponentLoweringRule,
// TraitLoweringRule, PolicyLoweringRule) because they share no common type; the only
// thing this function actually needs is the optional ContractDescriber assertion.
//
// isDocumentRuleIdentity reads the label back.
func loweringRuleIdentity(label string, typeName string, rule any) string {
	id := label + "/" + typeName
	if cd, ok := rule.(ContractDescriber); ok {
		if v := cd.ContractMetadata().Version; v != "" {
			id += "@" + v
		}
	}
	return id
}

// isDocumentRuleIdentity reports whether id, an Origin.Rule, names a
// DocumentLoweringRule: the label loweringRuleIdentity gave it is
// PositionDocument's. A raw document rule's "rawdocument" label is not, and
// never reaches an Origin.
func isDocumentRuleIdentity(id string) bool {
	return strings.HasPrefix(id, string(PositionDocument)+"/")
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

// sameAuthoredDocument reports whether o and other lie in the same authored document:
// the document-level fields only (Namespace, Document, DocumentKind), so two sibling
// components, traits or policies of one document match while elements of two different
// documents never do. NameAllocator.EmitOrAdopt uses it to keep adoption inside one
// document: each settled document is transformed on its own, so an element adopted from
// another document would be missing from the adopter's output. Only an allocator shared
// across documents — LowerRaws' round-0 raw-rule claims, or a caller's own — can see a
// claim from another document; Transform's holds one authored document's claims.
func (o Origin) sameAuthoredDocument(other Origin) bool {
	return o.Namespace == other.Namespace && o.Document == other.Document && o.DocumentKind == other.DocumentKind
}

// identityDigest renders a non-reversible short digest of an EmitOrAdopt content
// identity for error messages. An identity may be built from sensitive inputs
// (credentials, tokens), and a collision error travels through LoweringError to CLI
// output and logs, so the raw identity is never echoed.
func identityDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
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
	// Document is the enclosing document as it stands at this round (for a
	// DocumentLoweringRule, the same copy passed to LowerDocument). Read-only: a
	// rule must not mutate it or the element pointer it was handed. nil for a
	// RawDocumentLoweringRule, whose document is the decoded value passed to
	// LowerDocument and has no *Application form yet (lowering_raw.go).
	Document *Application
	// Component is the enclosing component; nil at document and policy position.
	Component *Component
	// capabilities is TransformContext.Capabilities (post-EvaluateProfile). A rule
	// reads it only through Capability, so every key it reads is recorded
	// (go-kure/launcher#686).
	capabilities map[string]CapabilityBinding
	// consumed is TransformContext.consumedCapabilities, the set Capability records a
	// read key into. nil where nothing collects them (LowerRaws, a test driver).
	consumed map[string]struct{}
	// Origin is the authored location of the element being lowered.
	Origin Origin
	// Namer allocates deterministic collision-free names (D2). Never nil when the
	// engine calls a rule: every position, including a RawDocumentLoweringRule
	// reached through LowerRaws, receives the one allocator shared across the whole
	// run. A rule may therefore call Namer.Name unconditionally and must not carry
	// a nil-guard fallback — that would be a second naming path with no collision
	// detection. A nil Namer is a contract violation by whoever built the
	// LoweringContext; code that drives a rule directly supplies NewNameAllocator().
	Namer *NameAllocator
	// application is the document name ResolveName asks the Naming hook with,
	// where no Document carries it: set by ComponentEndpointsNamed, empty on a
	// context the lowering engine builds.
	application string
}

// Capability returns the ClusterProfile capability bound to key (post-EvaluateProfile)
// and whether the profile has one. It is the only way a rule reads a capability: a key
// it finds is recorded as consumed, so TransformWithPolicy lists it in
// PolicyResult.ConsumedCapabilities exactly as it lists a key a trait resolves against.
// A key the profile does not bind is not recorded. The binding is the profile's own
// value, read-only like the rest of the context: a rule must not mutate its Rendering.
//
// A read from a RawDocumentLoweringRule is not recorded. LowerRaws returns no
// PolicyResult, and the raw rule's output re-enters Transform as authored input, where
// the capabilities its traits resolve against are recorded.
func (l LoweringContext) Capability(key string) (CapabilityBinding, bool) {
	binding, ok := l.capabilities[key]
	if ok && l.consumed != nil {
		l.consumed[key] = struct{}{}
	}
	return binding, ok
}

// WithCapabilities returns a copy of l whose Capability reads look up m. It is for a
// test driver that calls a rule directly; the engine wires the context it hands a rule
// itself. The copy records nothing: a read through it is counted nowhere.
func (l LoweringContext) WithCapabilities(m map[string]CapabilityBinding) LoweringContext {
	l.capabilities = m
	l.consumed = nil
	return l
}

// NameAllocator hands out deterministic, collision-free generated names within one
// lowering run (D2). A name already claimed by a different origin is a hard error
// naming both origins — a collision fails the build, never silently overwrites.
// The one exception is EmitOrAdopt: when a name was first claimed through it, a
// repeat EmitOrAdopt claim for the same content identity, from any element of the
// same authored document, adopts the existing element instead of colliding (for a
// terminal-type shared element only — see EmitOrAdopt).
type NameAllocator struct {
	taken map[string]nameClaim
	// round is the fixpoint round currently being processed, set by runLowering
	// before it dispatches any rule in that round (lowerRawRound, which runs round 0
	// only, sets 0). Recorded on every claim purely
	// to make Reserve's error message more specific (same round vs. an earlier
	// round) — see Reserve. It is NOT used to treat any repeat claim as a
	// legitimate no-op: Origin carries no per-sibling discriminator (two elements
	// independently emitted from one authored origin — LoweringResult's doc
	// comment — share one Origin value), so a repeat claim for the same origin in
	// a later round cannot be safely told apart from a genuinely different
	// sibling colliding with an earlier one.
	round int
	// resolved is the second claim space (go-kure/launcher#787): every object
	// and bundle name resolved through the name resolver, by what it names
	// (claimName, naming.go). It is separate from taken, which holds the names
	// lowering rules reserve by namespace and name alone. Created on first claim.
	resolved map[nameClaimKey]resolvedNameClaim
	// hook is TransformContext.Naming on the allocator of a transform, nil on any
	// other: LoweringContext.ResolveName asks it for a name a lowering rule
	// generates.
	hook func(NameRequest) (string, bool)
	// lowered holds the object names lowering rules resolved, in the order they
	// were resolved, until the transform claims them into resolved with the
	// namespace lowering settled on (claimLowered, naming_lowering.go).
	lowered []loweredName
}

// nameClaim records which origin claimed a generated name, and in which round, so
// Reserve's error message can say whether the collision was within one round or
// across rounds. identity is the content identity an EmitOrAdopt claim was made
// for; it is empty for a Reserve claim, which is never adoptable.
type nameClaim struct {
	origin   Origin
	round    int
	identity string
}

// NewNameAllocator returns an empty NameAllocator — the same constructor the
// engine uses once per LowerRaws / Transform invocation. The zero value is NOT
// usable (its reservation table is nil), so code that drives a lowering rule
// directly, outside the engine (a rule's own unit test, a pre-pass, a golden-file
// or fixture harness), builds its LoweringContext.Namer with this. Share ONE
// allocator across every rule call that belongs to the same run, as the engine
// does, so a generated-name collision between two documents is detected; a fresh
// allocator per call detects nothing across calls.
func NewNameAllocator() *NameAllocator {
	return &NameAllocator{taken: make(map[string]nameClaim)}
}

// reservedIdentity is one pre-existing document identity lowerRawRound claims against
// its NameAllocator before any rule runs — see lowerRawRound's preReserved parameter.
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

// EmitOrAdopt claims name for an element whose content is fully determined by
// identity — for a shared derived object, a string built from every input that
// shapes it (its kind, URL, interval, …). It is the one exception to Reserve's
// "every repeat claim collides" rule, and it is sound for the reason Reserve's
// carve-out was not: equal identity means equal content, so two claimants cannot be
// two different elements.
//
// The first claim of name returns adopted=false: the caller emits the element. A
// later claim with the same identity, from any element of the same authored document
// (Origin's Namespace, Document and DocumentKind) and in any round, returns
// adopted=true: the element already exists and the caller must not emit it again.
// Sibling components of one round cannot see each other's output
// (LoweringContext.Document is read-only within a round), so this is how they share
// one object instead of colliding on its name.
//
// A claim with a different identity, a same-identity claim from a different authored
// document, a name Reserve already holds, and an empty identity are all hard errors;
// Reserve likewise still refuses a name claimed here. The cross-document case stays a
// collision because every settled document is transformed on its own: an element
// adopted from another document would be missing from the adopter's output. The engine
// can only see it where one allocator spans documents: LowerRaws shares one across its
// raw inputs for round-0 raw-rule claims, but in-transform rules run inside each
// document's own Transform with a fresh allocator, so a name they generate is never
// compared across documents — a caller detects that collision with
// CheckCrossDocumentCollisions over every document's generated objects.
// Keyed on (namespace, name), like Reserve. Errors identify an identity only by a short
// SHA-256 digest, never its text, so an identity may include sensitive inputs.
//
// Three constraints on the caller, none checked here. The element emitted under
// the claim must be of a terminal type, one no lowering rule claims: claims outlive
// the round, so a lowerable element could be replaced under another name while a
// later adopter still points at the claimed one. Adoption covers only the shared
// element: the adopting rule still emits its own output for the element it lowers,
// since the engine rejects an empty LoweringResult as a deletion (D2), so a rule
// whose whole expansion is the shared element is outside this API's contract. And
// the document check sees only the authored document: when a document rule fans one
// authored document out into several documents, their elements share that Origin, so
// adoption must not be relied on across them.
func (n *NameAllocator) EmitOrAdopt(name, identity string, origin Origin) (adopted bool, err error) {
	if identity == "" {
		return false, errors.Errorf("lowering: %s claimed generated name %q with an empty content identity", origin, name)
	}
	key := origin.Namespace + "\x00" + name
	if prior, ok := n.taken[key]; ok {
		if prior.identity == "" {
			return false, errors.Errorf("lowering: generated name %q collides — already reserved by %s, and a reserved name cannot be adopted by %s", name, prior.origin, origin)
		}
		if prior.identity != identity {
			return false, errors.Errorf("lowering: generated name %q collides — %s emitted it for different content than %s wants (identity %s vs %s)", name, prior.origin, origin, identityDigest(prior.identity), identityDigest(identity))
		}
		if !prior.origin.sameAuthoredDocument(origin) {
			return false, errors.Errorf("lowering: generated name %q collides — already emitted by %s, and an element of another document cannot be adopted by %s", name, prior.origin, origin)
		}
		return true, nil
	}
	n.taken[key] = nameClaim{origin: origin, round: n.round, identity: identity}
	return false, nil
}

// Name builds "<base>-<suffix>", validates it as a DNS-1123 subdomain, reserves it
// against origin, and returns it. A name over 253 characters is shortened, not
// refused: base is cut by the one shortening rule (ShortenNameWithSuffix at
// ShortenLimitSubdomain) and "-<suffix>" is kept whole. The shortened name is the
// one reserved, so it takes part in collision detection like any other. Its
// characters are checked before it is shortened (SubdomainSyntaxErrors), so only
// its length is forgiven: an invalid name is refused whatever its length.
func (n *NameAllocator) Name(base, suffix string, origin Origin) (string, error) {
	name, err := generatedName(base, suffix)
	if err != nil {
		return "", err
	}
	if err := n.Reserve(name, origin); err != nil {
		return "", err
	}
	return name, nil
}

// NameOrAdopt is Name for EmitOrAdopt: it builds, shortens when needed and validates
// "<base>-<suffix>", then claims it for identity. adopted reports that the element
// already exists.
func (n *NameAllocator) NameOrAdopt(base, suffix, identity string, origin Origin) (name string, adopted bool, err error) {
	name, err = generatedName(base, suffix)
	if err != nil {
		return "", false, err
	}
	adopted, err = n.EmitOrAdopt(name, identity, origin)
	if err != nil {
		return "", false, err
	}
	return name, adopted, nil
}

func generatedName(base, suffix string) (string, error) {
	// The characters are checked on the name as built: shortening replaces the
	// cut part by a digest, which would hide an invalid character in it. Only
	// the length may be over at this point.
	full := base + "-" + suffix
	if errs := SubdomainSyntaxErrors(full); len(errs) > 0 {
		return "", errors.Errorf("lowering: generated name %q is not a valid DNS-1123 subdomain: %s", full, strings.Join(errs, "; "))
	}
	name := ShortenNameWithSuffix(base, "-"+suffix, ShortenLimitSubdomain)
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", errors.Errorf("lowering: generated name %q is not a valid DNS-1123 subdomain: %s", name, strings.Join(errs, "; "))
	}
	return name, nil
}

// DocumentLoweringRule lowers a whole authored document whose YAML ALREADY fits
// ApplicationSpec (types.go — Components and Policies, nothing else), so
// ParseWithExtraTypes can decode it into *Application with KnownFields(true)
// (parser.go) before any transform runs. Registered with RegisterDocumentLowering;
// reachable only from the in-transform entry point, where lowerDocumentOnce always
// supplies a genuine *Application.
//
// Every component and trait the rule builds is checked as authored, since nothing
// checks the whole document it reads. A PlatformReserved value it renders from
// LoweringContext.Capability is written with Component.RenderReserved or
// Trait.RenderReserved, which records it as the rule's output; written any other way,
// it is refused.
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
// Contract: a raw rule rewrites authored input; it does not lower. LowerRaws runs it
// once (round 0) and returns what it emits unsealed and otherwise untouched, and the
// caller's parse and Transform then treat that output exactly as if a person had
// authored it. Emit the Application a person would write — for example an expose
// trait with only its hostnames — and let Transform run the in-transform rules and
// merge ClusterProfile capability rendering. A rule that copies capability rendering
// into what it emits is writing platform-reserved values into authored input, and
// Transform rejects them with ErrPlatformReserved.
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
	//
	// lctx.Namer is never nil when LowerRaws calls this (unlike lctx.Document,
	// which always is here — see LoweringContext): derive every generated child name through
	// lctx.Namer.Name, with no nil fallback. A nil Namer is a contract violation
	// by whoever built lctx; a caller driving the rule outside LowerRaws (e.g.
	// the rule's own unit test in another module) passes NewNameAllocator(),
	// shared across every call that belongs to the same run.
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

// LoweringStep is one edge of the expansion chain. A LoweringError carries the
// failing element's chain (D7); LowerRawsWithSteps returns the steps of a
// successful raw lowering, so a caller can attribute each emitted document to the
// rule that produced it.
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
	if t.rawRuleClaimsKind(kind) {
		panic("oam: kind " + kind + " is already registered via RegisterRawDocumentLowering; a kind may be claimed by at most one registrar")
	}
	if _, exists := t.docLoweringRules[kind]; exists {
		panic("oam: document lowering rule already registered for kind " + kind)
	}
	t.docLoweringRules[kind] = r
}

// RawDocumentAPIVersioner is the optional hook a RawDocumentLoweringRule implements
// when the documents it claims are authored under an apiVersion other than
// SupportedAPIVersion — the case of a consumer that owns its own API group and hands
// LowerRaws documents declared under it. A rule that does not implement it claims
// SupportedAPIVersion, so every rule written before this hook existed keeps its
// exact dispatch. LowerRaws dispatches on the full (apiVersion, kind) pair: a
// document sharing a registered kind string under an apiVersion no rule claims still
// passes through byte-identical, exactly as before.
//
// The hook is deliberately raw-entry only. The in-transform path decodes through
// ParseWithExtraTypes, which gates on SupportedAPIVersion before any rule can run
// (validate.go), so a DocumentLoweringRule never sees another group's document and
// has nothing to declare.
//
// The method is named for the hook, not the value: Go interfaces are satisfied
// structurally, so a plain APIVersion() string — a common accessor a rule may carry
// for unrelated reasons (an embedded document type, a Kubernetes-style envelope) —
// would silently opt an existing rule into the hook and re-key it away from
// SupportedAPIVersion. Only a method spelled RawDocumentAPIVersion is an opt-in.
type RawDocumentAPIVersioner interface {
	RawDocumentAPIVersion() string
}

// rawDocRuleKey is the raw-rule registry key: the (apiVersion, kind) pair a
// RawDocumentLoweringRule claims. See RawDocumentAPIVersioner.
type rawDocRuleKey struct {
	apiVersion string
	kind       string
}

// rawRuleAPIVersion is the apiVersion r claims: its RawDocumentAPIVersioner value
// when it implements the hook, SupportedAPIVersion otherwise.
func rawRuleAPIVersion(r RawDocumentLoweringRule) string {
	if v, ok := r.(RawDocumentAPIVersioner); ok {
		return v.RawDocumentAPIVersion()
	}
	return SupportedAPIVersion
}

// rawRuleClaimsKind reports whether a registered raw rule, under any apiVersion,
// claims kind. The cross-registrar guard is kind-wide on purpose: a kind string is
// claimed by at most one registrar regardless of group, so the two rule flavours can
// never disagree about what the same kind means.
func (t *Transformer) rawRuleClaimsKind(kind string) bool {
	for key := range t.rawDocLoweringRules {
		if key.kind == kind {
			return true
		}
	}
	return false
}

// rawClaimedGroups is the set of API groups LowerRaws treats as its own: the group
// of every apiVersion a registered raw rule claims, plus SupportedAPIVersion's group
// always — the one this package's own parser accepts and the one a rule emits into
// by default. Keyed by group, not by the full apiVersion (apiGroup): the identity
// model of the raw pass is version-blind, so an Application authored at another
// version of a claimed group names the same collidable resource as one at the
// claimed version. Used only to decide which pass-through Application identities
// LowerRaws pre-reserves; settled-document validation deliberately does NOT consult
// it (see loweringDoc.apiVersion).
func (t *Transformer) rawClaimedGroups() map[string]bool {
	groups := map[string]bool{apiGroup(SupportedAPIVersion): true}
	for key := range t.rawDocLoweringRules {
		groups[apiGroup(key.apiVersion)] = true
	}
	return groups
}

// apiGroup is the group half of a Kubernetes-style "group/version" apiVersion. A
// bare version with no group segment (the core group, "v1") maps to "".
func apiGroup(apiVersion string) string {
	group, _, found := strings.Cut(apiVersion, "/")
	if !found {
		return ""
	}
	return group
}

// RegisterRawDocumentLowering registers a raw-entry document rule under the
// (apiVersion, kind) pair it claims — SupportedAPIVersion unless the rule implements
// RawDocumentAPIVersioner. Same four guards as RegisterDocumentLowering, in the other
// direction, plus two of its own: an implemented APIVersion may not be empty, and one
// pair may be registered once. Two rules claiming the same kind under different
// apiVersions are distinct registrations; the cross-registrar guard against
// RegisterDocumentLowering stays kind-wide (see rawRuleClaimsKind).
func (t *Transformer) RegisterRawDocumentLowering(r RawDocumentLoweringRule) {
	kind := r.Kind()
	if kind == "" {
		panic("oam: raw document lowering rule may not claim an empty kind")
	}
	if kind == terminalDocumentKind {
		panic("oam: raw document lowering rule may not claim the terminal kind " + terminalDocumentKind)
	}
	apiVersion := rawRuleAPIVersion(r)
	if apiVersion == "" {
		panic("oam: raw document lowering rule for kind " + kind + " may not claim an empty apiVersion")
	}
	if _, exists := t.docLoweringRules[kind]; exists {
		panic("oam: kind " + kind + " is already registered via RegisterDocumentLowering; a kind may be claimed by at most one registrar")
	}
	key := rawDocRuleKey{apiVersion: apiVersion, kind: kind}
	if _, exists := t.rawDocLoweringRules[key]; exists {
		panic("oam: raw document lowering rule already registered for kind " + kind + " under apiVersion " + apiVersion)
	}
	t.rawDocLoweringRules[key] = r
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

// loweringDoc is one in-flight document inside a lowering run.
//
// Exactly one of doc and raw is set. raw is non-nil only for a LowerRaws seed entry
// that has not been decoded yet; lowerRawRound decodes and lowers it in round 0, the
// only round LowerRaws runs, and its output is a doc-carrying loweringDoc that is
// serialized, never handed to runLowering. runLowering therefore only ever sees
// doc-carrying entries.
type loweringDoc struct {
	doc  *Application            // set once parsed (in-transform) or emitted by a raw rule
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
	// apiVersion is the ONE API group, besides SupportedAPIVersion, a document a raw
	// rule emits may carry: the registry key its raw seed was dispatched under (the
	// envelope's apiVersion, never a re-evaluation of the rule's hook — that is
	// evaluated exactly once, at registration). Empty on the in-transform path,
	// which is single-group by construction. Carried per document rather than read
	// from the transformer's raw registry so a DocumentLoweringRule dispatched during
	// Transform can never settle under a group that merely happens to be registered
	// on the same Transformer for the raw entry point (see checkLoweredAPIVersion).
	apiVersion string
}

// runLowering is the in-transform fixpoint: t.lower calls it exactly once per
// Transform, so one NameAllocator (D2), one expansion chain and one MaxLoweringDepth
// budget (D7) are shared by every document the authored one expands into. seed must be
// non-empty. LowerRaws does not use it: a raw rule's output is authored input to a
// later Transform, which runs this fixpoint over it with a full budget of its own
// (go-kure/launcher#357).
func (t *Transformer) runLowering(seed []loweringDoc, ctx TransformContext) ([]loweringDoc, error) {
	// A transform hands in its own allocator, which carries the Naming hook and
	// goes on to hold the names resolved after lowering.
	namer := ctx.nameClaims
	if namer == nil {
		namer = NewNameAllocator()
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
			expanded, docChanged, steps, err := t.lowerDocumentOnce(d.doc, ctx, namer, round)
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
				next = append(next, loweringDoc{doc: doc, origin: d.origin, slot: d.slot, apiVersion: d.apiVersion})
			}
		}
		cur = next
		if !changed {
			for _, d := range cur {
				if err := t.validateSettled(d.doc, d.apiVersion); err != nil {
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
//
// allowedAPIVersion is the one API group besides SupportedAPIVersion doc may settle
// under (loweringDoc.apiVersion). runLowering serves only the in-transform path, whose
// seed carries none, so it is "" there; LowerRaws applies the same group rule to what
// a raw rule emits through checkLoweredAPIVersion directly.
func (t *Transformer) validateSettled(doc *Application, allowedAPIVersion string) error {
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
	// Accept allowedAPIVersion as well as SupportedAPIVersion (checkLoweredAPIVersion),
	// validating a shallow copy under SupportedAPIVersion so every other check runs
	// unchanged — validateWithExtraTypes enforces SupportedAPIVersion because it also
	// serves authored input.
	if err := checkLoweredAPIVersion(doc, allowedAPIVersion); err != nil {
		return err
	}
	if doc.APIVersion != SupportedAPIVersion {
		normalized := *doc
		normalized.APIVersion = SupportedAPIVersion
		doc = &normalized
	}
	return validateWithExtraTypes(doc, customTraitTypes, customComponentTypes, LowerableTypes{})
}

// checkLoweredAPIVersion accepts a lowered document under SupportedAPIVersion or
// allowedAPIVersion (loweringDoc.apiVersion) and rejects any other group. A document a
// RawDocumentLoweringRule emits legitimately carries the API group that rule claims
// (RawDocumentAPIVersioner): the consumer that owns that group is the one parsing
// LowerRaws' output, and it would reject SupportedAPIVersion exactly as this package
// rejects the consumer's group. The allowed group travels with the document, never
// from t.rawDocLoweringRules: the in-transform path passes "" and so stays exactly as
// strict as before — a DocumentLoweringRule dispatched during Transform that emits a
// foreign group is rejected even when a raw rule for that group is registered on the
// same Transformer, and a raw rule claiming group G may emit only G or
// SupportedAPIVersion, never some other rule's group.
func checkLoweredAPIVersion(doc *Application, allowedAPIVersion string) error {
	if doc.APIVersion == SupportedAPIVersion {
		return nil
	}
	if allowedAPIVersion != "" && doc.APIVersion == allowedAPIVersion {
		return nil
	}
	want := fmt.Sprintf("expected %q", SupportedAPIVersion)
	if allowedAPIVersion != "" {
		want = fmt.Sprintf("expected %q or %q (the group the claiming RawDocumentLoweringRule declares)", SupportedAPIVersion, allowedAPIVersion)
	}
	return oamValidationError("apiVersion", fmt.Sprintf("unsupported apiVersion %q on lowered document, %s", doc.APIVersion, want))
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
	settled, err := t.runLowering(seed, ctx)
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
		if err := t.enforceAuthoredReservations(doc, origin); err != nil {
			return nil, false, nil, err
		}
		// The rule gets its own copy of doc: a new component slice whose traits are
		// marked with where they came from (forwardableTraits), as the component-rule
		// branch does, so a rule that rebuilds a component with a new Traits slice
		// copied from the authored one still has those traits recognised as forwarded
		// (go-kure/launcher#603). The copy also keeps a rule that writes through
		// Spec.Components or a component's Traits from reaching the authored document.
		input := documentRuleInput(doc)
		// Snapshot the components the rule was handed BEFORE it ran: a rule that
		// forwards one of them unchanged into its output (rather than constructing a
		// fresh component) is forwarding its already-authored traits too, not
		// synthesizing them — same reasoning as originalTraits below for the
		// component-position case, extended to however many components the document
		// rule forwards. Taken before LowerDocument, as this says; a rule must not
		// mutate its input (LoweringContext.Document), so for a conforming rule the
		// order is moot.
		originalComponents := input.Spec.Components
		// Same snapshot, for the identical reason, on the policy side (C1 finding on
		// PR go-kure/launcher#283: the policy loop below stamped Rule unconditionally while the
		// component loop already guarded against exactly this).
		originalPolicies := input.Spec.Policies
		lctx := LoweringContext{Document: input, capabilities: ctx.Capabilities, consumed: ctx.consumedCapabilities, Origin: origin, Namer: namer}
		result, err := rule.LowerDocument(input, lctx)
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
		// A document rule's output is authored (the component loop below), except a
		// component it forwarded that was already synthesized and a reserved value it
		// recorded with Component.RenderReserved or Trait.RenderReserved. Check the
		// rest of every emitted document for reserved keys before
		// validateEmittedDocument drops an explicit null from any of them. Traits the
		// same way, before sealNestedTraitsInDocument validates them.
		forwardedSynthesized := func(comp *Component) bool {
			return comp.synthesized && isForwardedComponent(comp, originalComponents)
		}
		forwardedSynthesizedTrait := func(trait *Trait) bool {
			if !trait.synthesized {
				return false
			}
			for i := range originalComponents {
				if isForwardedTrait(trait, originalComponents[i].Traits) {
					return true
				}
			}
			return false
		}
		for i := range result.Documents {
			if err := t.enforceEmittedComponentReservations(result.Documents[i].Spec.Components, origin.Rule, forwardedSynthesized); err != nil {
				return nil, false, nil, errors.Wrapf(err, "%s", origin)
			}
			if err := t.enforceEmittedTraitReservations(result.Documents[i].Spec.Components, nil, origin.Rule, forwardedSynthesizedTrait); err != nil {
				return nil, false, nil, errors.Wrapf(err, "%s", origin)
			}
		}
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
				// The same check decides Component.synthesized: a forwarded component
				// keeps whatever it already was; anything else is this rule's output
				// and is authored. A document rule sees the whole document — trait and
				// policy properties and metadata included — and nothing checks all of
				// that before it runs, so its output is never synthesized: a by-value
				// copy that carried the marker is reset, and every component it built
				// is checked downstream as if a user wrote it. Only the reserved values
				// the rule recorded (Component.rendered, kept here) are exempt, each
				// while it holds the recorded value.
				if isForwardedComponent(comp, originalComponents) {
					if prior, ok := comp.Origin(); ok {
						compOrigin.Rule = prior.Rule
					} else {
						compOrigin.Rule = ""
					}
				} else {
					comp.synthesized = false
				}
				// Nor does a document rule form or carry a sibling group (stampSiblingGroups).
				comp.siblingGroup = nil
				comp.origin = &compOrigin
				// The traits it built are never synthesized either, for the same reason.
				if err := t.sealNestedTraitsInDocument(comp, compOrigin, originalComponents); err != nil {
					return nil, false, nil, errors.Wrapf(err, "%s", origin)
				}
			}
			for k := range result.Documents[i].Spec.Policies {
				pol := &result.Documents[i].Spec.Policies[k]
				// Same Rule-attribution guard as the component loop above (isForwardedComponent):
				// a policy this document rule only forwarded verbatim from originalPolicies keeps
				// whatever Rule it already carried instead of being misattributed to this rule.
				polOrigin := Origin{Document: origin.Document, DocumentKind: origin.DocumentKind, Namespace: origin.Namespace, PolicyName: pol.Name, Index: k, Rule: origin.Rule}
				if isForwardedPolicy(pol, originalPolicies) {
					if prior, ok := pol.Origin(); ok {
						polOrigin.Rule = prior.Rule
					} else {
						polOrigin.Rule = ""
					}
				}
				pol.origin = &polOrigin
			}
		}
		for i := range result.Documents {
			clearForwardingMarks(result.Documents[i].Spec.Components)
		}
		step := LoweringStep{Rule: origin.Rule, Position: PositionDocument, Round: round, From: doc.Metadata.Name, To: names}
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

	// Every component and trait no rule synthesized is checked for reserved keys
	// before any rule of this round runs: a rule may emit an element whose properties
	// map is that of a component or trait it was handed (LoweringContext.Component),
	// and validating the emitted element strips an explicit null from both
	// (go-kure/launcher#609, go-kure/launcher#626).
	// The schema and the exemption are the ones the later checks use, so this
	// refuses nothing they would accept, with one exception: an authored trait a
	// component rule consumes instead of forwarding, which no later check sees. Its
	// reserved key is an authored one all the same, as for a component a document
	// rule drops (enforceAuthoredReservations).
	if err := t.enforceAuthoredReservations(doc, docOrigin); err != nil {
		return false, nil, err
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
			//
			// The rule gets its own copy, each element marked with where it came from
			// (forwardableTraits), so a rule that forwards the authored traits AND adds
			// one of its own — which needs a new slice, and so new element addresses —
			// still has them recognised as forwarded. Copying also means no rule can
			// write through comp.Traits into the authored document's backing array.
			comp.Traits = forwardableTraits(comp.Traits)
			originalTraits := comp.Traits
			// D3: reject an authored value for a platform-reserved property before the
			// rule runs — the same check the trait-lowering-rule dispatch below performs
			// (enforcePlatformReserved, further down this function) and applyTraits/
			// createApplications (transform.go) perform for a dispatchable handler.
			// Round-9 Codex regression: this was missing here, so a ComponentLoweringRule
			// — reachable via the same public RegisterComponentLowering extension point a
			// TraitLoweringRule uses — could accept an authored platform-reserved value
			// with no enforcement at all. A component an earlier rule synthesized is
			// exempt (Component.synthesized): its properties are that rule's output, and
			// what was authored in that rule's input was checked before it ran.
			//
			// This rule's own output is synthesized only when its input was checked:
			// the rule declares a schema (checked just here), or its input was itself
			// synthesized. A schema-less rule may copy an authored value through
			// unchecked, so its output stays authored and is checked downstream.
			p, declaresSchema := rule.(PropertySchemaProvider)
			if declaresSchema && !comp.synthesized {
				if err := enforcePlatformReserved(p.PropertySchema(), comp.Properties, comp.rendered, "properties"); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
			}
			inputChecked := declaresSchema || comp.synthesized
			// The order an earlier rule gave the component (Component.OrderAfter),
			// read before this rule can touch its copy.
			inheritedOrder := comp.orderAfter
			lctx := LoweringContext{Document: doc, Component: &comp, capabilities: ctx.Capabilities, consumed: ctx.consumedCapabilities, Origin: compOrigin, Namer: namer}
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
			if !inputChecked {
				if err := t.enforceEmittedComponentReservations(result.Components, compOrigin.Rule, nil); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
				// A trait the rule forwarded keeps what it was (sealEmittedNestedTraits),
				// so one that arrived synthesized stays exempt.
				forwardedSynthesized := func(trait *Trait) bool {
					return trait.synthesized && isForwardedTrait(trait, originalTraits)
				}
				if err := t.enforceEmittedTraitReservations(result.Components, nil, compOrigin.Rule, forwardedSynthesized); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
			}
			names := make([]string, len(result.Components))
			for j := range result.Components {
				result.Components[j].origin = &compOrigin
				result.Components[j].synthesized = inputChecked
				names[j] = result.Components[j].Name
				if err := t.validateEmittedComponent(&result.Components[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
				if err := t.sealEmittedNestedTraits(&result.Components[j], compOrigin, originalTraits, inputChecked); err != nil {
					return false, steps, errors.Wrapf(err, "%s", compOrigin)
				}
			}
			// What the component became waits as the component did, whether or not
			// the rule built its output from the component it was handed.
			inheritOrder(result.Components, inheritedOrder)
			clearForwardingMarks(result.Components)
			// Only a component-position rule may emit a same-name sibling group: the
			// one invocation is the group's boundary, so a name a different
			// invocation, a trait or document rule, or an author repeats is still a
			// duplicate (validateComponent).
			if err := t.stampSiblingGroups(result.Components); err != nil {
				return false, steps, errors.Wrapf(err, "%s", compOrigin)
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
			steps = append(steps, LoweringStep{Rule: compOrigin.Rule, Position: PositionComponent, Round: round, From: comp.Name, To: names})
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
				// A trait a component rule forwarded behind one of its own sits at a
				// shifted k; its slot in the slice the rule was handed is the authored one.
				if trait.authoredIndex != nil {
					traitOrigin.Index = *trait.authoredIndex
				}
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
			p, declaresSchema := rule.(PropertySchemaProvider)
			resolvedTrait := trait
			matched := false
			matchedKey := ""
			if !trait.sealed {
				// Capability rendering is merged in before the rule runs (D5 input 3),
				// the same merge applyTraits performs for a dispatchable handler — so a
				// TraitLoweringRule sees the identical "rendering as defaults, inline
				// wins" view a TraitHandler would.
				resolvedTrait, matchedKey, matched = resolveCapability(trait, ctx.Capabilities)

				// CapabilityAware is engine-enforced here exactly as applyTraits
				// enforces it for a dispatchable TraitHandler: a lowering rule that
				// needs a ClusterProfile capability and finds none fails with
				// ErrMissingCapability, since the rule itself never runs through
				// applyTraits.
				if aware, ok := rule.(CapabilityAware); ok && aware.CapabilityRequired() && !matched {
					return false, steps, errors.Wrapf(ErrMissingCapability, "%s: capability %q not found in ClusterProfile", traitOrigin, buildCapabilityKey(trait))
				}
				// For a custom (non-built-in) trait type whose capability rendering
				// resolved in the profile, warn or (under SetStrictCapabilities) error
				// when no CapabilityDefinition was loaded for the type — the same check
				// applyTraits performs for a dispatchable TraitHandler (transform.go). A
				// TraitLoweringRule never runs through applyTraits, so without this it
				// silently bypasses strict mode entirely for a lowering-rule-consumed
				// capability (round-7 Codex finding, lowering.go).
				if !t.builtinTraitTypes[trait.Type] && matched {
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
				// D3: reject an authored value for a platform-reserved property before
				// capability rendering is merged in. applyTraits (transform.go, for a
				// dispatchable handler) and createApplications (transform.go, for a
				// component handler) perform the same check at their own merge points,
				// alongside honoring Trait.synthesized in applyTraits. Checked against
				// trait.Properties (the pre-merge original), not resolvedTrait, with only
				// the trait's own record (Trait.RenderReserved) exempt.
				if declaresSchema {
					if err := enforcePlatformReserved(p.PropertySchema(), trait.Properties, trait.rendered, "properties"); err != nil {
						return false, steps, errors.Wrapf(err, "%s", traitOrigin)
					}
					// Nested Required on the merged properties, as applyTraits checks it
					// (go-kure/launcher#765).
					if err := checkNestedRequired(p.PropertySchema(), resolvedTrait.Properties, "properties", boundCapability(matched, matchedKey)); err != nil {
						return false, steps, errors.Wrapf(err, "%s", traitOrigin)
					}
				}

				if matched && ctx.consumedCapabilities != nil {
					ctx.consumedCapabilities[matchedKey] = struct{}{}
				}
			} else if declaresSchema && !trait.synthesized {
				// D3 on a sealed trait no checked rule emitted (Trait.synthesized): a
				// schema-less rule may have copied an authored reserved value into it.
				// Its Properties are final, so they are checked as they stand, a value
				// the rule recorded with Trait.RenderReserved exempt.
				if err := enforcePlatformReserved(p.PropertySchema(), trait.Properties, trait.rendered, "properties"); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
			}
			// This rule's output is synthesized only when its input was checked: the
			// trait is itself synthesized, or the rule declares a schema and the trait
			// is unsealed. A sealed trait the check above just passed stays
			// unpromoted: it holds only what the rule's own schema reserves, not what
			// the components or traits the rule emits reserve.
			inputChecked := trait.synthesized || (declaresSchema && !trait.sealed)
			lctx := LoweringContext{Document: doc, Component: &comp, capabilities: ctx.Capabilities, consumed: ctx.consumedCapabilities, Origin: traitOrigin, Namer: namer}
			result, err := rule.LowerTrait(&resolvedTrait, lctx)
			if err != nil {
				return false, steps, errors.Wrapf(err, "%s", traitOrigin)
			}
			if err := validatePositionResult(PositionTrait, traitOrigin, result); err != nil {
				return false, steps, err
			}
			// Rule is re-derived here — see Origin.Rule's doc comment.
			traitOrigin.Rule = loweringRuleIdentity(string(PositionTrait), trait.Type, rule)
			// Before the emitted traits are validated too: they may share a
			// properties map with an emitted component.
			if !inputChecked {
				if err := t.enforceEmittedComponentReservations(result.Components, traitOrigin.Rule, nil); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				if err := t.enforceEmittedTraitReservations(result.Components, result.Traits, traitOrigin.Rule, nil); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
			}
			names := make([]string, len(result.Traits))
			for j := range result.Traits {
				result.Traits[j].origin = &traitOrigin
				result.Traits[j].sealed = true
				result.Traits[j].synthesized = inputChecked
				// A trait lowered from a forwarded one keeps its authored slot, so a
				// sibling group still applies it in authored order (applyEntryTraits).
				result.Traits[j].authoredIndex = trait.authoredIndex
				names[j] = result.Traits[j].Type
				if err := t.validateEmittedTrait(&result.Traits[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
			}
			newTraits = append(newTraits, result.Traits...)
			for j := range result.Components {
				result.Components[j].origin = &traitOrigin
				result.Components[j].synthesized = inputChecked
				// A trait rule never forms a sibling group; a by-value copy of the
				// group member it decorates must not join that group (stampSiblingGroups).
				result.Components[j].siblingGroup = nil
				if err := t.validateEmittedComponent(&result.Components[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				if err := t.sealEmittedNestedTraits(&result.Components[j], traitOrigin, nil, inputChecked); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				// A trait-position rule may also emit Components (loweringPositionRules);
				// include them in the step's To — see the matching comment on the
				// component-position block above.
				names = append(names, result.Components[j].Name)
			}
			clearForwardingMarks(result.Components)
			newComponents = append(newComponents, result.Components...)
			for j := range result.Policies {
				result.Policies[j].origin = &traitOrigin
				if err := t.validateEmittedPolicy(&result.Policies[j]); err != nil {
					return false, steps, errors.Wrapf(err, "%s", traitOrigin)
				}
				names = append(names, result.Policies[j].Name)
				pendingPolicies = append(pendingPolicies, result.Policies[j])
			}
			steps = append(steps, LoweringStep{Rule: traitOrigin.Rule, Position: PositionTrait, Round: round, From: trait.Type, To: names})
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
		lctx := LoweringContext{Document: doc, capabilities: ctx.Capabilities, consumed: ctx.consumedCapabilities, Origin: polOrigin, Namer: namer}
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
		steps = append(steps, LoweringStep{Rule: polOrigin.Rule, Position: PositionPolicy, Round: round, From: pol.Name, To: names})
		changed = true
	}
	newPolicies = append(newPolicies, pendingPolicies...)
	doc.Spec.Components = newComponents
	doc.Spec.Policies = newPolicies

	return changed, steps, nil
}

// sealEmittedNestedTraits stamps origin and seals every NEWLY BUILT trait
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
// traits by returning them unchanged — e.g. `Traits: comp.Traits`, or
// `append([]Trait{synthesized}, comp.Traits...)` — is not synthesizing anything for
// them: those traits were never touched by the rule and must undergo the SAME
// capability processing any other authored trait gets, either via a registered
// TraitLoweringRule next round or via applyTraits at settle time. Sealing them
// anyway would (per the reasoning above) skip that processing entirely, silently
// dropping capability rendering for a forwarded CapabilityAware trait such as expose.
// A trait isForwardedTrait finds in forwarded is left untouched here — no origin
// stamp, no seal, no emitted-trait validation — exactly as if it still belonged to a
// component no rule had ever claimed.
//
// A forwarded trait also records the index it held in forwarded (Trait.authoredIndex)
// unless an earlier forwarding already did, so a rule that places its own trait
// ahead of the forwarded ones does not shift the Origin.Index they are later given.
//
// synthesized is the emitting rule's inputChecked: each trait sealed here gets it
// as Trait.synthesized, the same marker the component it sits in carries.
func (t *Transformer) sealEmittedNestedTraits(comp *Component, parentOrigin Origin, forwarded []Trait, synthesized bool) error {
	return t.sealNestedTraits(comp, parentOrigin, synthesized, func(trait *Trait) bool {
		i := forwardedIndex(trait, forwarded)
		if i < 0 {
			return false
		}
		if trait.authoredIndex == nil {
			trait.authoredIndex = &i
		}
		return true
	})
}

// sealNestedTraitsInDocument is sealEmittedNestedTraits' document-position analogue
// (round-9-batch-2 Codex finding, lowering.go:717): a DocumentLoweringRule emits a
// whole *Application, potentially with several components, each potentially
// forwarding traits from ANY of the original document's components (not just one) —
// e.g. by reorganizing doc.Spec.Components across several output documents. A single
// []Trait forwarded slice cannot express "forwarded from one of N original
// components", so this checks pointer identity against every original component's
// own Traits slice instead of one. A trait sealed here is never synthesized: a
// document rule's output stays authored (see the component loop in lowerDocumentOnce),
// and a reserved value the rule rendered into it is exempt only through its record
// (Trait.RenderReserved), which sealing keeps.
// A forwarded trait that carries no origin yet is stamped with its authored location:
// the component it came from and its slot in that component's Traits. The next
// round's fallback would derive it from the emitting component instead, which names
// the wrong component when the rule moved the trait to another one, and the wrong
// slot when the rule placed a trait of its own ahead of it. It stays unsealed, with an
// empty Rule, like any authored trait.
func (t *Transformer) sealNestedTraitsInDocument(comp *Component, parentOrigin Origin, originalComponents []Component) error {
	return t.sealNestedTraits(comp, parentOrigin, false, func(trait *Trait) bool {
		for i := range originalComponents {
			src := &originalComponents[i]
			k := forwardedIndex(trait, src.Traits)
			if k < 0 {
				continue
			}
			if trait.origin == nil {
				authored, ok := src.Origin()
				if !ok {
					authored = Origin{Document: parentOrigin.Document, DocumentKind: parentOrigin.DocumentKind, Namespace: parentOrigin.Namespace, Component: src.Name, ComponentType: src.Type, Index: i}
				}
				authored.TraitType = trait.Type
				authored.Index = k
				authored.Rule = ""
				trait.origin = &authored
			}
			return true
		}
		return false
	})
}

// sealNestedTraits is the shared body: stamp origin and seal every trait in
// comp.Traits that isForwarded reports false for — the "hard-coded by the emitting
// rule, not forwarded from an authored input" traits sealEmittedNestedTraits' own doc
// comment (above) describes. Each one's Trait.synthesized is set to synthesized,
// which also resets a by-value copy of a synthesized trait that the rule changed.
func (t *Transformer) sealNestedTraits(comp *Component, parentOrigin Origin, synthesized bool, isForwarded func(*Trait) bool) error {
	for k := range comp.Traits {
		trait := &comp.Traits[k]
		// The forwarding mark (forwardableTraits) stays until the caller has
		// classified the rule's whole output (clearForwardingMarks): a rule may attach
		// one slice of forwarded copies to several components, and every one of them
		// must still see the mark.
		if isForwarded(trait) {
			continue
		}
		nestedOrigin := parentOrigin
		nestedOrigin.TraitType = trait.Type
		nestedOrigin.Index = k
		trait.origin = &nestedOrigin
		trait.sealed = true
		trait.synthesized = synthesized
		if err := t.validateEmittedTrait(trait); err != nil {
			return err
		}
	}
	return nil
}

// isForwardedTrait reports whether trait is one of the elements of original
// forwarded unchanged by a lowering rule, rather than a new value the rule
// constructed. Two shapes count:
//
//   - pointer identity: trait IS an element of original — the `Traits: comp.Traits`
//     idiom the round-7 finding describes;
//   - an unchanged by-value copy of one: trait carries the forwarding mark
//     forwardableTraits put on that element, and still has its type and the very
//     same properties map. This is what a rule produces when it forwards the
//     authored traits and adds one of its own, since the extra element needs a new
//     slice (`append([]Trait{synthesized}, comp.Traits...)`).
//
// Neither is a value/deep comparison, deliberately: a rule that constructs a NEW
// trait whose type and properties happen to equal an authored one carries no mark
// (the field is unexported, so a rule in another package cannot set it) and is
// never mistaken for a forwarded one. A copy whose Type or Properties the rule
// replaced is the rule's own output, and is sealed like any other.
func isForwardedTrait(trait *Trait, original []Trait) bool {
	return forwardedIndex(trait, original) >= 0
}

// forwardedIndex is isForwardedTrait returning which element of original trait was
// forwarded from, or -1 when it is not forwarded.
func forwardedIndex(trait *Trait, original []Trait) int {
	for i := range original {
		o := &original[i]
		if trait == o {
			return i
		}
		if trait.forwardedFrom == o && trait.Type == o.Type && sameMap(trait.Properties, o.Properties) {
			return i
		}
	}
	return -1
}

// forwardableTraits returns a copy of traits with every element marked as the
// origin of any by-value copy a component rule makes of it (Trait.forwardedFrom),
// for isForwardedTrait. A copy, never the caller's slice: marking in place would
// leave the mark on the authored document's own trait elements, outside the one
// rule invocation it describes. nil is returned as is; an empty slice gets its own
// zero-capacity storage, so appending through the copy cannot reach spare capacity
// the authored slice shares with another.
func forwardableTraits(traits []Trait) []Trait {
	if traits == nil {
		return nil
	}
	out := make([]Trait, len(traits))
	copy(out, traits)
	for i := range out {
		out[i].forwardedFrom = &out[i]
	}
	return out
}

// documentRuleInput returns the copy of doc a document rule is handed: the same
// document with a new component slice, each component's traits passed through
// forwardableTraits. Properties maps and policies are shared with doc, not copied:
// a by-value forwarded trait is recognised by its very properties map (sameMap), and
// a rule must not mutate its input in any case (LoweringContext.Document).
func documentRuleInput(doc *Application) *Application {
	input := *doc
	if doc.Spec.Components != nil {
		input.Spec.Components = make([]Component, len(doc.Spec.Components))
		for i, comp := range doc.Spec.Components {
			comp.Traits = forwardableTraits(comp.Traits)
			input.Spec.Components[i] = comp
		}
	}
	return &input
}

// clearForwardingMarks drops the forwarding mark from every trait of comps once a
// rule's whole output has been classified, so it never outlives the one rule
// invocation it describes.
func clearForwardingMarks(comps []Component) {
	for i := range comps {
		for k := range comps[i].Traits {
			comps[i].Traits[k].forwardedFrom = nil
		}
	}
}

// sameMap reports whether a and b are the same map value (both nil, or one map
// reached through two references) — identity, not equality of contents.
func sameMap(a, b map[string]any) bool {
	return reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
}

// enforceAuthoredReservations runs the D3 check (enforcePlatformReserved) on every
// component of doc that no lowering rule synthesized, and on every trait no rule
// synthesized whatever component it sits in, against the schema each would later be
// checked against: a ComponentLoweringRule's or TraitLoweringRule's when one is
// registered for its type (lowerDocumentBody's pre-rule checks), else its dispatchable
// handler's (createApplications, applyTraits). It runs before a DocumentLoweringRule,
// which may drop or retype an element and so take it out of reach of those later
// checks. A type with no schema reserves nothing here, and an unknown type is the
// validator's business. A document rule's output is authored either way
// (lowerDocumentOnce) and is checked downstream too.
func (t *Transformer) enforceAuthoredReservations(doc *Application, docOrigin Origin) error {
	for i := range doc.Spec.Components {
		comp := &doc.Spec.Components[i]
		compOrigin, stamped := comp.Origin()
		if !stamped {
			compOrigin = Origin{Document: docOrigin.Document, DocumentKind: docOrigin.DocumentKind, Namespace: docOrigin.Namespace, Component: comp.Name, ComponentType: comp.Type, Index: i}
		}
		if !comp.synthesized {
			if err := t.enforceComponentReservations(comp, "properties"); err != nil {
				return errors.Wrapf(err, "%s", compOrigin)
			}
		}
		// A synthesized component may still carry an authored trait a checked rule
		// forwarded, so its traits are checked one by one.
		for k := range comp.Traits {
			trait := &comp.Traits[k]
			if trait.synthesized {
				continue
			}
			if err := t.enforceTraitReservations(trait, "properties"); err != nil {
				traitOrigin, stamped := trait.Origin()
				if !stamped {
					traitOrigin = compOrigin
					traitOrigin.TraitType = trait.Type
					traitOrigin.Index = k
				}
				return errors.Wrapf(err, "%s", traitOrigin)
			}
		}
	}
	return nil
}

// enforceComponentReservations runs the D3 check (enforcePlatformReserved) on one
// component, against the schema it would later be checked against: its
// ComponentLoweringRule's when one is registered, else its dispatchable handler's. A
// type with no schema reserves nothing, and an unknown type is the validator's
// business. The caller decides whether the component is exempt (synthesized); a
// reserved value it records as rendered (Component.RenderReserved) is exempt here, as
// is a trait's (Trait.RenderReserved) in enforceTraitReservations.
func (t *Transformer) enforceComponentReservations(comp *Component, path string) error {
	var provider any
	if rule, ok := t.componentLoweringRules[comp.Type]; ok {
		provider = rule
	} else if h := t.findComponentHandler(comp.Type); h != nil {
		provider = h
	}
	p, ok := provider.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return enforcePlatformReserved(p.PropertySchema(), comp.Properties, comp.rendered, path)
}

// enforceEmittedComponentReservations is the D3 check on the components one lowering
// rule invocation just emitted and did not synthesize; exempt, when set, names the
// ones it did. It must run on the whole result before any of it is validated,
// because emission validation normalizes an explicit null to absence in place:
// checked afterwards, an authored reserved key written as null that the rule copied
// through would no longer be there to refuse (go-kure/launcher#609). That includes a
// properties map the rule shares between two emitted elements, which validating the
// first would strip for the second. A non-null value is refused here too, earlier
// than the downstream check that would otherwise catch it. rule is the emitting
// rule's identity (loweringRuleIdentity), which Origin's string form omits.
func (t *Transformer) enforceEmittedComponentReservations(comps []Component, rule string, exempt func(*Component) bool) error {
	for i := range comps {
		comp := &comps[i]
		if exempt != nil && exempt(comp) {
			continue
		}
		path := fmt.Sprintf("component %q (type %q) emitted by rule %s: properties", comp.Name, comp.Type, rule)
		if err := t.enforceComponentReservations(comp, path); err != nil {
			return err
		}
	}
	return nil
}

// enforceTraitReservations is enforceComponentReservations for a trait: its schema is
// its TraitLoweringRule's when one is registered, else its dispatchable handler's.
func (t *Transformer) enforceTraitReservations(trait *Trait, path string) error {
	var provider any
	if rule, ok := t.traitLoweringRules[trait.Type]; ok {
		provider = rule
	} else if h := t.findTraitHandler(trait.Type); h != nil {
		provider = h
	}
	p, ok := provider.(PropertySchemaProvider)
	if !ok {
		return nil
	}
	return enforcePlatformReserved(p.PropertySchema(), trait.Properties, trait.rendered, path)
}

// enforceEmittedTraitReservations is enforceEmittedComponentReservations for the
// traits one lowering rule invocation emitted and did not synthesize: those it
// returned at trait position (traits) and those nested in the components it emitted
// (comps). exempt, when set, names the ones it did not synthesize but forwarded
// already synthesized. Like its component counterpart it must run on the whole result
// before any of it is validated: validating an emitted trait strips an explicit null
// from its properties, and validating an emitted component strips it from any trait
// sharing that component's map (go-kure/launcher#626).
func (t *Transformer) enforceEmittedTraitReservations(comps []Component, traits []Trait, rule string, exempt func(*Trait) bool) error {
	for i := range traits {
		trait := &traits[i]
		if exempt != nil && exempt(trait) {
			continue
		}
		path := fmt.Sprintf("trait %q emitted by rule %s: properties", trait.Type, rule)
		if err := t.enforceTraitReservations(trait, path); err != nil {
			return err
		}
	}
	for i := range comps {
		for k := range comps[i].Traits {
			trait := &comps[i].Traits[k]
			if exempt != nil && exempt(trait) {
				continue
			}
			path := fmt.Sprintf("trait %q of component %q emitted by rule %s: properties", trait.Type, comps[i].Name, rule)
			if err := t.enforceTraitReservations(trait, path); err != nil {
				return err
			}
		}
	}
	return nil
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

// isForwardedPolicy is isForwardedComponent's policy-position counterpart, used only
// by lowerDocumentOnce's document-rule branch above: it reports whether pol is
// literally one of the elements of original — i.e. the same ApplicationPolicy struct
// forwarded unchanged by a DocumentLoweringRule, rather than a new value the rule
// constructed. Pointer identity is deliberate, matching isForwardedComponent's own
// tradeoff and false-negative risk.
func isForwardedPolicy(pol *ApplicationPolicy, original []ApplicationPolicy) bool {
	for i := range original {
		if pol == &original[i] {
			return true
		}
	}
	return false
}
