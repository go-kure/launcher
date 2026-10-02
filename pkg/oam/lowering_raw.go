package oam

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/go-kure/launcher/pkg/errors"
)

// documentEnvelope is the minimal, LENIENT probe LowerRaws runs before deciding
// whether a raw input is its business at all: the two fields every authored document
// has regardless of its kind-specific spec shape. It is decoded without
// KnownFields(true) on purpose — a higher-level kind's own fields are unknown here by
// definition, and a document this pass does not claim must never be rejected by this
// pass. It mirrors the kind probe a downstream consumer runs at the same seam,
// extended to also read metadata.name.
type documentEnvelope struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
}

// rawDocKey is the LowerRaws batch-duplicate-detection key. It carries the namespace,
// so two raw inputs sharing a name and kind but authored in different namespaces are
// distinct Kubernetes-adjacent resources that must both be claimed and lowered, not
// collapsed into one "duplicate" by a key that cannot tell them apart.
//
// It deliberately has NO apiVersion component either, and neither does any other
// identity in this pass (NameAllocator keys on (namespace, name); pre-reservation
// likewise): one LowerRaws call produces one output slice for one consumer, and
// within it a (namespace, kind, name) triple names one resource whatever group each
// input was authored under. Two same-named inputs of one kind in two claimed groups
// are therefore a duplicate, not two dispatches — the conservative reading, since
// their rules' generated children would otherwise share (namespace, name) too.
type rawDocKey struct {
	namespace string
	kind      string
	name      string
}

// LowerRaws lowers every raw document whose (apiVersion, kind) pair has a
// registered RawDocumentLoweringRule — SupportedAPIVersion unless the rule
// implements RawDocumentAPIVersioner — and returns every other input, a registered
// kind under an unclaimed group included, byte-identical at its original position. It is the entry point a consumer calls BEFORE its own parse
// fan-out, which is the only placement that works for a kind the consumer's parser
// would reject: such a document carries authored fields ApplicationSpec has no home
// for, so a strict decode into *Application fails long before any rule could run.
//
// The []json.RawMessage carrier matches that consumer's own application-slice type;
// the bytes it carries are YAML, which every consumer of this seam decodes with a YAML
// decoder and of which JSON is a subset.
//
// LowerRaws runs round 0 and nothing after it (go-kure/launcher#357): each claimed
// document goes through its RawDocumentLoweringRule once, and what the rule emits is
// returned as-is — no component, trait, policy or document rule runs on it here, and
// nothing in it is sealed. The raw-rule contract follows from that: a raw rule
// rewrites authored input; it does not lower. Its output re-enters the caller's parse
// and Transform/TransformWithPolicy exactly as if a person had authored it, so every
// in-transform rule, capability merge, platform-reserved check and post-settle
// validation runs there, once, with the full MaxLoweringDepth budget. Transform does
// not shape-check authored trait properties: as for any authored document, the caller
// runs ValidateAuthoredProperties on each parsed output document, after parameter
// substitution, before Transform. A rule that copies ClusterProfile capability
// rendering into a trait it emits is therefore writing platform-reserved values into
// authored input, and Transform rejects them with ErrPlatformReserved; emit the trait
// as a person would write it and let Transform merge the capability.
//
// ctx.Capabilities reaches the rule through LoweringContext.Capability, for a rule
// whose rewrite depends on what the platform offers. It is not an invitation to
// render. A read here is not recorded in PolicyResult.ConsumedCapabilities: LowerRaws
// returns none, and the capabilities the output's traits resolve against are recorded
// when Transform runs on it.
//
// What LowerRaws still checks itself, because the caller's parser cannot: each
// claimed document's metadata, duplicate authored identities across the batch,
// generated-name collisions (pass-through Applications included), the arity and
// component/policy property schemas of what a rule emits, and that every emitted
// document carries SupportedAPIVersion or the one group its rule was matched under.
// Emitted trait properties are not among them: that is the caller's
// ValidateAuthoredProperties call, above.
//
// LowerRaws drops the rule identity of what it emitted; LowerRawsWithSteps returns it.
func (t *Transformer) LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error) {
	out, _, err := t.LowerRawsWithSteps(raws, ctx)
	return out, err
}

// LowerRawsWithSteps is LowerRaws, also returning the lowering steps of a successful
// call: one LoweringStep per claimed raw input, in input order. Each step's Rule is
// the rule identity ("rawdocument/<apiVersion>/<kind>", suffixed "@<version>" when
// the rule declares ContractMetadata().Version), From the authored document's
// metadata.name and To the metadata.name of every document it emitted, so a caller
// can attribute each output document to the rule that produced it — the identity a
// LoweringError's Chain reports on failure, and which the returned bytes cannot
// carry.
//
// From is a name, not a full identity: two claimed inputs of one kind and name in
// different namespaces yield steps that differ only by their position in the slice,
// which is the position of their inputs. A pass-through input has no step. When no
// input is claimed, the steps are nil; on error, they are nil and the
// LoweringError's Chain carries the failing document's steps.
func (t *Transformer) LowerRawsWithSteps(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, []LoweringStep, error) {
	if len(t.rawDocLoweringRules) == 0 {
		return raws, nil, nil // raw-path analogue of the pointer-identity guarantee: nothing to do, nothing touched
	}

	claimed := make([]bool, len(raws))
	seenKeys := make(map[rawDocKey]int, len(raws))
	var seed []loweringDoc
	// preReserved claims every pass-through document's own identity against the shared
	// NameAllocator before any rule runs — see lowerRawRound.
	var preReserved []reservedIdentity
	groups := t.rawClaimedGroups()
	for i, raw := range raws {
		var env documentEnvelope
		if err := yaml.Unmarshal(raw, &env); err != nil {
			// Not this pass's error to report: the caller's own parser produces
			// the canonical message for a malformed document. Pass through.
			continue
		}
		// Dispatch on the full (apiVersion, kind) pair, not kind alone: an
		// unrelated resource that happens to share a registered kind string under
		// an apiVersion no rule claims must be preserved byte-for-byte, not claimed
		// and mis-lowered (or failed) by a decoder that was never meant for it. A
		// rule claims SupportedAPIVersion unless it implements
		// RawDocumentAPIVersioner (lowering.go) — the hook a consumer that owns its
		// own API group uses so that its documents are claimed here instead of
		// silently passing through and then failing in that consumer's own parser.
		// The in-transform path stays single-group: it gates on SupportedAPIVersion
		// before a document ever reaches lowering (validate.go), and nothing there
		// consults this registry.
		rule, ok := t.rawDocLoweringRules[rawDocRuleKey{apiVersion: env.APIVersion, kind: env.Kind}]
		if !ok {
			// Pass-through: never decoded, never re-serialized — but a pass-through
			// Application's identity still has to be visible to collision detection
			// (see preReserved above), since a claimed raw document's rule could
			// otherwise generate a same-named Application. Restricted to the
			// terminal kind: NameAllocator.Reserve's key is (namespace, name) alone,
			// with no kind component (see Reserve), and lowering only ever
			// PRODUCES Application documents (terminalDocumentKind) — so a
			// same-named pass-through of any OTHER kind (e.g. a ClusterProfile
			// sharing a name with an Application) is not a real identity collision
			// and must not be pre-reserved, or it would collide with that
			// Application's own legitimate reservation despite naming a distinct
			// resource. Restricted likewise to the API groups this pass claims
			// (rawClaimedGroups — the group, not the full apiVersion, since
			// identity here is version-blind like every other key in the pass):
			// an Application under a group no rule claims is a foreign resource
			// sharing a kind string, not an identity a rule's generated child
			// could collide with. An empty or malformed name is not this pass's
			// error to reject (env.Kind isn't even a lowerable kind here), so
			// only register a usable name.
			if env.Kind == terminalDocumentKind && env.Metadata.Name != "" && groups[apiGroup(env.APIVersion)] {
				preReserved = append(preReserved, reservedIdentity{
					name:   env.Metadata.Name,
					origin: Origin{Document: env.Metadata.Name, DocumentKind: env.Kind, Namespace: env.Metadata.Namespace},
				})
			}
			continue
		}

		// Validate the two fields this pass itself relies on before claiming the
		// document: an empty or malformed name propagates uncaught into Origin
		// (rawDocKey dedup, LoweringError attribution below) and into every
		// generated child name a rule derives from it (a rule's own call to
		// NameAllocator.Name takes this name as its base), surfacing far
		// downstream as an opaque generated-name failure instead of here — the
		// same DNS-1123 gate ParseWithExtraTypes enforces for the in-transform
		// path (validate.go:107-119).
		if env.Metadata.Name == "" {
			return nil, nil, &LoweringError{Origin: Origin{DocumentKind: env.Kind}, Cause: errors.Errorf(
				"raw input %d: metadata.name is required", i)}
		}
		if errs := validation.IsDNS1123Subdomain(env.Metadata.Name); len(errs) > 0 {
			return nil, nil, &LoweringError{Origin: Origin{Document: env.Metadata.Name, DocumentKind: env.Kind}, Cause: errors.Errorf(
				"raw input %d: metadata.name %q is not a valid DNS-1123 subdomain", i, env.Metadata.Name)}
		}
		if env.Metadata.Namespace != "" {
			if errs := validation.IsDNS1123Label(env.Metadata.Namespace); len(errs) > 0 {
				return nil, nil, &LoweringError{Origin: Origin{Document: env.Metadata.Name, DocumentKind: env.Kind, Namespace: env.Metadata.Namespace}, Cause: errors.Errorf(
					"raw input %d: metadata.namespace %q is not a valid DNS-1123 label (%s)", i, env.Metadata.Namespace, namespaceLabelRule)}
			}
		}

		origin := Origin{Document: env.Metadata.Name, DocumentKind: env.Kind, Namespace: env.Metadata.Namespace}
		// Two raw inputs sharing a namespace, kind and name are the same authored
		// resource declared twice, and Origin deliberately excludes slot, so nothing
		// downstream can tell them apart. Left unchecked, a generated-name
		// collision between their children would be reported by the shared
		// NameAllocator as one document colliding with itself (Reserve's
		// same-location branch), and EmitOrAdopt would let the second adopt the
		// first's shared element as if both were one document. A duplicate whose
		// rule generates no name would pass silently. Reject it here instead,
		// naming both input positions.
		//
		// Two raw inputs sharing a name and kind but authored in different
		// namespaces are distinct resources, not duplicates of each other — see
		// rawDocKey's doc comment.
		key := rawDocKey{namespace: env.Metadata.Namespace, kind: env.Kind, name: env.Metadata.Name}
		if prior, dup := seenKeys[key]; dup {
			return nil, nil, &LoweringError{Origin: origin, Cause: errors.Errorf(
				"duplicate authored document: raw input %d and raw input %d both name %q (kind %q, namespace %q)",
				prior, i, env.Metadata.Name, env.Kind, env.Metadata.Namespace)}
		}
		seenKeys[key] = i
		claimed[i] = true
		seed = append(seed, loweringDoc{
			raw:  raw,
			rule: rule,
			// The envelope probe is what supplies the Origin. DecodeDocument
			// returns an opaque rule-specific value with no generic way to read
			// an authored name back out, so the engine cannot wait until after
			// decoding to learn the Origin it must pass INTO LowerDocument's own
			// lctx — exactly as the kind probe supplies dispatch before decoding.
			origin: origin,
			slot:   i,
			// The group every document this seed's rule emits may carry besides
			// SupportedAPIVersion — see loweringDoc.apiVersion.
			// This is the registry key that matched (env.APIVersion), NOT a second
			// call to the rule's RawDocumentAPIVersion() hook: a stateful rule could answer
			// differently now than at registration and thereby authorize a group
			// it never registered under.
			apiVersion: env.APIVersion,
		})
	}
	if len(seed) == 0 {
		return raws, nil, nil
	}

	emitted, chains, err := t.lowerRawRound(seed, ctx, preReserved)
	if err != nil {
		return nil, nil, err
	}
	// Seeds are in input order, so concatenating their chains gives the steps in
	// input order; chainBySlot attributes a re-serialization failure below.
	var steps []LoweringStep
	chainBySlot := make(map[int][]LoweringStep, len(seed))
	for i, d := range seed {
		steps = append(steps, chains[i]...)
		chainBySlot[d.slot] = chains[i]
	}

	// Splice on slot — not on Origin, and not on position within emitted. Group
	// first, preserving each slot's own emission order.
	bySlot := make(map[int][]loweringDoc, len(seed))
	for _, d := range emitted {
		bySlot[d.slot] = append(bySlot[d.slot], d)
	}
	out := make([]json.RawMessage, 0, len(raws))
	for i, raw := range raws {
		if !claimed[i] {
			out = append(out, raw)
			continue
		}
		for _, d := range bySlot[i] {
			b, err := yaml.Marshal(d.doc)
			if err != nil {
				return nil, nil, &LoweringError{Origin: d.origin, Chain: chainBySlot[i], Cause: errors.Wrapf(err, "re-serialize lowered document %q", d.doc.Metadata.Name)}
			}
			out = append(out, b)
		}
	}
	return out, steps, nil
}

// lowerRawRound is LowerRaws' whole engine: round 0 over every claimed seed, sharing
// one NameAllocator so generated-name collisions are detected across the batch, and
// then nothing — the emitted documents are not lowered further here (see LowerRaws).
//
// preReserved claims every pass-through document's own identity against the
// allocator before any rule runs. A pass-through document is never decoded and never
// joins seed, so it would otherwise never touch the allocator at all: a claimed raw
// document's rule could then generate a child document sharing a pass-through's exact
// (namespace, name) — the collision API used correctly — and LowerRaws would return
// two Application entries with the same identity.
//
// Each emitted document must carry SupportedAPIVersion or the group its seed was
// matched under (loweringDoc.apiVersion): a rule emitting into any other group is a
// rule bug, reported here against the authored document rather than left to a caller's
// parser that cannot say which rule produced it.
//
// On success it also returns each seed's own steps, indexed like seed
// (LowerRawsWithSteps).
func (t *Transformer) lowerRawRound(seed []loweringDoc, ctx TransformContext, preReserved []reservedIdentity) ([]loweringDoc, [][]LoweringStep, error) {
	namer := NewNameAllocator()
	for _, r := range preReserved {
		if err := namer.Reserve(r.name, r.origin); err != nil {
			return nil, nil, &LoweringError{Origin: r.origin, Cause: err}
		}
	}
	namer.round = 0

	out := make([]loweringDoc, 0, len(seed))
	chains := make([][]LoweringStep, len(seed))
	for i, d := range seed {
		// Seeds are independent authored documents, so an error's Chain is this
		// seed's own steps only — never an earlier seed's (D7).
		emitted, steps, err := t.lowerRawOnce(d, ctx, namer, 0)
		if err != nil {
			return nil, nil, &LoweringError{Origin: d.origin, Chain: steps, Cause: err}
		}
		chains[i] = steps
		for _, doc := range emitted {
			if err := checkLoweredAPIVersion(doc, d.apiVersion); err != nil {
				return nil, nil, &LoweringError{Origin: d.origin, Chain: steps, Cause: err}
			}
			out = append(out, loweringDoc{doc: doc, origin: d.origin, slot: d.slot, apiVersion: d.apiVersion})
		}
	}
	return out, chains, nil
}

// lowerRawOnce is round 0 for a raw-entered document, and the only round LowerRaws
// runs: it decodes the bytes with the registered rule's OWN target type, then calls
// that rule's LowerDocument — the same two calls lowerDocumentOnce's document-rule
// branch makes, differing only in where the decode target comes from.
//
// Nothing it returns is sealed or origin-stamped. Both are unexported and would not
// survive LowerRaws' serialization, and neither is wanted: a raw rule rewrites
// authored input, so its output is authored input to the caller's Transform, which
// stamps provenance and applies capability rendering itself.
//
// lctx.Document is nil here, and a RawDocumentLoweringRule must not read it: the
// document IS the decoded value passed as LowerDocument's first argument, and no
// *Application form of it exists yet.
func (t *Transformer) lowerRawOnce(d loweringDoc, ctx TransformContext, namer *NameAllocator, round int) ([]*Application, []LoweringStep, error) {
	decoded, err := d.rule.DecodeDocument(d.raw)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "%s: decode", d.origin)
	}
	lctx := LoweringContext{capabilities: ctx.Capabilities, Origin: d.origin, Namer: namer}
	result, err := d.rule.LowerDocument(decoded, lctx)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "%s", d.origin)
	}
	// Emitting nothing is an error here exactly as at every other position: D2 does
	// not permit deletion, so a claimed raw input always yields at least one output
	// document.
	if err := validatePositionResult(PositionDocument, d.origin, result); err != nil {
		return nil, nil, err
	}
	// The chain names a raw step "rawdocument/<apiVersion>/<kind>": label
	// "rawdocument", not string(PositionDocument), because d.rule is a
	// RawDocumentLoweringRule, not a DocumentLoweringRule (both are validated as
	// PositionDocument regardless); and the registry pair, not the kind alone,
	// because two raw rules may claim one kind under different groups.
	ruleID := loweringRuleIdentity("rawdocument", d.apiVersion+"/"+d.origin.DocumentKind, d.rule)
	// Every component the rule wrote is authored input, and its reserved keys are
	// checked first, in every emitted document: validateEmittedDocument drops an
	// explicit null before the document is serialized, and Transform would then
	// never see it (go-kure/launcher#609).
	for i := range result.Documents {
		if err := t.enforceEmittedComponentReservations(result.Documents[i].Spec.Components, ruleID, nil); err != nil {
			return nil, nil, errors.Wrapf(err, "%s", d.origin)
		}
	}
	emitted := make([]*Application, len(result.Documents))
	names := make([]string, len(result.Documents))
	for i := range result.Documents {
		emitted[i] = &result.Documents[i]
		names[i] = result.Documents[i].Metadata.Name
		// Component and policy properties are checked against their target's schema
		// now, so a malformed emission is attributed to the authored raw document.
		// Traits are not: a trait the rule wrote is authored input, so its property
		// shapes are checked by the caller's ValidateAuthoredProperties (after
		// parameter substitution) and its platform-reserved keys by Transform.
		if err := t.validateEmittedDocument(emitted[i]); err != nil {
			return nil, nil, errors.Wrapf(err, "%s", d.origin)
		}
	}
	step := LoweringStep{
		Rule:     ruleID,
		Position: PositionDocument,
		Round:    round,
		From:     d.origin.Document,
		To:       names,
	}
	return emitted, []LoweringStep{step}, nil
}
