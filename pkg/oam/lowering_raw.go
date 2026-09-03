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

// rawDocKey is the LowerRaws batch-duplicate-detection key. It is deliberately NOT
// Origin: Origin (Document, DocumentKind — lowering.go) has no Namespace field, so
// two raw inputs sharing a name and kind but authored in different namespaces are
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

// LowerRaws lowers every raw document whose kind has a registered
// RawDocumentLoweringRule, and returns every other input byte-identical at its
// original position. It is the entry point a consumer calls BEFORE its own parse
// fan-out, which is the only placement that works for a kind the consumer's parser
// would reject: such a document carries authored fields ApplicationSpec has no home
// for, so a strict decode into *Application fails long before any rule could run.
//
// The []json.RawMessage carrier matches that consumer's own application-slice type;
// the bytes it carries are YAML, which every consumer of this seam decodes with a YAML
// decoder and of which JSON is a subset.
//
// ctx is threaded through because the shared fixpoint's later rounds lower components,
// traits and policies exactly as the in-transform path does and read ctx.Capabilities
// there. A caller that has not yet evaluated a ClusterProfile passes what it has; a
// trait rule declaring CapabilityRequired() then fails with ErrMissingCapability, the
// same failure the in-transform path produces for the same input. Making capability
// evaluation available at this seam is the consumer's problem, not this function's.
//
// KNOWN LIMITATION (round-9-batch-2 Codex finding, unfixed — no RawDocumentLoweringRule
// ships in this package yet, so nothing currently triggers it): Trait.sealed is
// unexported, so yaml.Marshal silently drops it from the returned bytes. If a claimed
// raw document's shared fixpoint round (lowerRawOnce, then the ordinary
// component/trait dispatch it feeds into) seals a synthesized terminal trait, that
// seal is lost the moment this function serializes its output. A caller that re-parses
// the returned bytes and then calls Transform/TransformWithPolicy on the SAME
// Transformer will have applyTraits (transform.go) treat that trait as unsealed and
// perform a second, redundant capability-rendering merge — or fail with
// ErrMissingCapability for a capability the raw-lowering rule already accounted for.
// Before registering a RawDocumentLoweringRule whose LowerDocument (directly or via a
// component/trait-position rule dispatched during the same call) can emit a sealed
// trait, this must be fixed: either preserve sealed state across the round-trip, or
// defer all trait-position capability processing for a raw-entered document until the
// caller's own post-parse Transform call.
func (t *Transformer) LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error) {
	if len(t.rawDocLoweringRules) == 0 {
		return raws, nil // raw-path analogue of the pointer-identity guarantee: nothing to do, nothing touched
	}

	claimed := make([]bool, len(raws))
	seenKeys := make(map[rawDocKey]int, len(raws))
	var seed []loweringDoc
	// preReserved claims every pass-through document's own identity against the shared
	// NameAllocator before any rule runs — see runLowering's preReserved parameter. A
	// pass-through document is never decoded and never joins seed, so without this it
	// would never touch the allocator at all: a claimed raw document's rule could then
	// generate a child document sharing a pass-through's exact (namespace, name), and
	// LowerRaws would return both, an undetected duplicate identity.
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
			// with no kind component (lowering.go:222), and lowering only ever
			// PRODUCES Application documents (terminalDocumentKind) — so a
			// same-named pass-through of any OTHER kind (e.g. a ClusterProfile
			// sharing a name with an Application) is not a real identity collision
			// and must not be pre-reserved, or it would collide with that
			// Application's own legitimate reservation despite naming a distinct
			// resource. Restricted likewise to the groups this pass claims
			// (rawClaimedGroups): an Application under an apiVersion no rule
			// claims is a foreign resource sharing a kind string, not an identity
			// a rule's generated child could collide with. An empty or malformed
			// name is not this pass's error to reject (env.Kind isn't even a
			// lowerable kind here), so only register a usable name.
			if env.Kind == terminalDocumentKind && env.Metadata.Name != "" && groups[env.APIVersion] {
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
			return nil, &LoweringError{Origin: Origin{DocumentKind: env.Kind}, Cause: errors.Errorf(
				"raw input %d: metadata.name is required", i)}
		}
		if errs := validation.IsDNS1123Subdomain(env.Metadata.Name); len(errs) > 0 {
			return nil, &LoweringError{Origin: Origin{Document: env.Metadata.Name, DocumentKind: env.Kind}, Cause: errors.Errorf(
				"raw input %d: metadata.name %q is not a valid DNS-1123 subdomain", i, env.Metadata.Name)}
		}
		if env.Metadata.Namespace != "" {
			if errs := validation.IsDNS1123Subdomain(env.Metadata.Namespace); len(errs) > 0 {
				return nil, &LoweringError{Origin: Origin{Document: env.Metadata.Name, DocumentKind: env.Kind, Namespace: env.Metadata.Namespace}, Cause: errors.Errorf(
					"raw input %d: metadata.namespace %q is not a valid DNS-1123 subdomain", i, env.Metadata.Namespace)}
			}
		}

		origin := Origin{Document: env.Metadata.Name, DocumentKind: env.Kind, Namespace: env.Metadata.Namespace}
		// NameAllocator.Reserve treats reserving a name for an EQUAL Origin as a
		// no-op — correct within one document, where an identical Origin means
		// "the same element asking twice", but wrong across raw inputs, where
		// Origin deliberately excludes slot and two DIFFERENT authored documents
		// can produce an identical Origin by sharing a name and kind. Left
		// unchecked, two such inputs would have their generated child names
		// silently treated as one shared reservation instead of a collision.
		// Reject the duplicate here, before it ever reaches the shared
		// NameAllocator, so Reserve's existing same-Origin-is-a-no-op rule never
		// has to distinguish two different documents that happen to share an
		// Origin — that case cannot reach it anymore.
		//
		// The duplicate check itself keys on rawDocKey, not Origin: Origin has no
		// Namespace field, so two raw inputs sharing a name and kind but authored
		// in different namespaces are distinct resources, not duplicates of each
		// other — see rawDocKey's doc comment.
		key := rawDocKey{namespace: env.Metadata.Namespace, kind: env.Kind, name: env.Metadata.Name}
		if prior, dup := seenKeys[key]; dup {
			return nil, &LoweringError{Origin: origin, Cause: errors.Errorf(
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
			// The group this seed and every document descending from it may
			// settle under besides SupportedAPIVersion — see loweringDoc.apiVersion.
			apiVersion: rawRuleAPIVersion(rule),
		})
	}
	if len(seed) == 0 {
		return raws, nil
	}

	settled, err := t.runLowering(seed, ctx, preReserved)
	if err != nil {
		return nil, err
	}

	// Splice on slot — not on Origin, and not on position within settled. Group
	// first, preserving each slot's own emission order.
	bySlot := make(map[int][]loweringDoc, len(seed))
	for _, d := range settled {
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
				return nil, &LoweringError{Origin: d.origin, Cause: errors.Wrapf(err, "re-serialize lowered document %q", d.doc.Metadata.Name)}
			}
			out = append(out, b)
		}
	}
	return out, nil
}

// lowerRawOnce is round 0 for a raw-entered document: it decodes the bytes with the
// registered rule's OWN target type, then calls that rule's LowerDocument — the same
// two calls lowerDocumentOnce's document-rule branch makes, differing only in where
// the decode target comes from. It always reports "changed": a raw seed entry is by
// definition unfinished.
//
// lctx.Document is nil here, and a RawDocumentLoweringRule must not read it: the
// document IS the decoded value passed as LowerDocument's first argument, and no
// *Application form of it exists yet. From round 1 on, every descendant is an ordinary
// *Application and lowerDocumentOnce populates lctx.Document as usual.
func (t *Transformer) lowerRawOnce(d loweringDoc, ctx TransformContext, namer *NameAllocator, round int) ([]*Application, []LoweringStep, error) {
	decoded, err := d.rule.DecodeDocument(d.raw)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "%s: decode", d.origin)
	}
	lctx := LoweringContext{Capabilities: ctx.Capabilities, Origin: d.origin, Namer: namer}
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
	// Rule is re-derived here — see Origin.Rule's doc comment. d.origin is the raw
	// seed's own authored origin (lowerRawOnce is always round 0 for a raw entry — see
	// the doc comment above — so d.origin never itself carries a prior Rule value).
	// label is "rawdocument", not string(PositionDocument): d.rule is a
	// RawDocumentLoweringRule, not a DocumentLoweringRule, and the LoweringStep this
	// same function builds below (for the error-path chain) already used
	// "rawdocument/"+d.origin.DocumentKind for exactly this distinction before
	// Origin.Rule existed — the two provenance surfaces must agree on which rule
	// produced a raw-entered document, not just on which POSITION-shaped
	// LoweringResult it returned (both are validated as PositionDocument regardless).
	// The type name is the registry pair "<apiVersion>/<kind>", not the kind alone:
	// two raw rules may claim one kind under different groups, and the kind by itself
	// would then not say which rule fired.
	ruleID := loweringRuleIdentity("rawdocument", d.apiVersion+"/"+d.origin.DocumentKind, d.rule)
	emitted := make([]*Application, len(result.Documents))
	names := make([]string, len(result.Documents))
	for i := range result.Documents {
		origin := d.origin
		origin.Rule = ruleID
		result.Documents[i].origin = &origin
		emitted[i] = &result.Documents[i]
		names[i] = result.Documents[i].Metadata.Name
		if err := t.validateEmittedDocument(emitted[i]); err != nil {
			return nil, nil, errors.Wrapf(err, "%s", d.origin)
		}
		// Round-12-batch-2 Codex finding (lowering_raw.go, "Seal traits emitted
		// directly by raw rules"): lowerDocumentOnce's document-rule branch seals
		// every freshly synthesized nested trait via sealNestedTraitsInDocument, but
		// this raw-entry counterpart never did — a terminal trait nested inside a
		// RawDocumentLoweringRule's emitted Application flowed through unsealed, so
		// applyTraits later treated it as authored and merged capability rendering
		// into it a second time (or rejected it for a capability it was never meant
		// to require). Pass forwarded=nil (sealEmittedNestedTraits' documented "no
		// such trait" case) to seal and validate every one, and stamp per-component/
		// per-policy Origin at the same time — the identical Origin-doctrine gap
		// round-12-batch-1 fixed for lowerDocumentOnce's own document-rule branch
		// (lowering.go:729-757), safe unconditionally here for the same reason:
		// origin.Document/DocumentKind/Namespace are the stable authored-root values
		// already computed above.
		//
		// KNOWN LIMITATION (round-14 Codex finding "Preserve authored traits exposed
		// by raw decoders", deferred — no RawDocumentLoweringRule ships in this
		// package yet, so nothing currently triggers it): forwarded=nil is wrong for
		// a rule whose DecodeDocument target embeds real, authored oam.Trait values
		// (e.g. its own Traits field, decoded straight from YAML) that LowerDocument
		// then copies unchanged into the emitted component. Unlike
		// sealNestedTraitsInDocument's DocumentLoweringRule case, this function has
		// no typed "original components" to pointer-compare against — decoded is
		// `any`, defined entirely by the rule — so there is no forwarded slice to
		// pass today. A rule that legitimately forwards an authored, capability-aware
		// trait (e.g. expose) this way would have it wrongly sealed here, skipping
		// the capability merge it still needs. Before registering a
		// RawDocumentLoweringRule that forwards authored traits, this needs either an
		// optional interface the decode target can implement to expose its forwarded
		// traits (mirroring isForwardedTrait's pointer-identity check), or an
		// equivalent mechanism — not a blanket forwarded=nil.
		for j := range result.Documents[i].Spec.Components {
			comp := &result.Documents[i].Spec.Components[j]
			compOrigin := Origin{Document: origin.Document, DocumentKind: origin.DocumentKind, Namespace: origin.Namespace, Component: comp.Name, ComponentType: comp.Type, Index: j, Rule: origin.Rule}
			comp.origin = &compOrigin
			if err := t.sealEmittedNestedTraits(comp, compOrigin, nil); err != nil {
				return nil, nil, errors.Wrapf(err, "%s", d.origin)
			}
		}
		for k := range result.Documents[i].Spec.Policies {
			pol := &result.Documents[i].Spec.Policies[k]
			polOrigin := Origin{Document: origin.Document, DocumentKind: origin.DocumentKind, Namespace: origin.Namespace, PolicyName: pol.Name, Index: k, Rule: origin.Rule}
			pol.origin = &polOrigin
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
