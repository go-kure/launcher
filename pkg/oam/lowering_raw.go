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
	for i, raw := range raws {
		var env documentEnvelope
		if err := yaml.Unmarshal(raw, &env); err != nil {
			// Not this pass's error to report: the caller's own parser produces
			// the canonical message for a malformed document. Pass through.
			continue
		}
		// Dispatch on the full (apiVersion, kind) pair, not kind alone: an
		// unrelated resource that happens to share a registered kind string under
		// a different apiVersion must be preserved byte-for-byte, not claimed and
		// mis-lowered (or failed) by a decoder that was never meant for it. This
		// package supports exactly one apiVersion end to end — the in-transform
		// path enforces the identical single-group gate before a document ever
		// reaches lowering (validate.go, checked before the kind allowlist) — so
		// gating on SupportedAPIVersion here, rather than adding a per-rule
		// apiVersion to RawDocumentLoweringRule, matches that existing invariant
		// instead of introducing multi-group registration this package has no
		// other use for.
		if env.APIVersion != SupportedAPIVersion {
			continue // pass-through: different apiVersion, not this pass's business
		}
		rule, ok := t.rawDocLoweringRules[env.Kind]
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
			// resource. An empty or malformed name is not this pass's error to
			// reject (env.Kind isn't even a lowerable kind here), so only register
			// a usable name.
			if env.Kind == terminalDocumentKind && env.Metadata.Name != "" {
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
	emitted := make([]*Application, len(result.Documents))
	names := make([]string, len(result.Documents))
	for i := range result.Documents {
		origin := d.origin
		result.Documents[i].origin = &origin
		emitted[i] = &result.Documents[i]
		names[i] = result.Documents[i].Metadata.Name
	}
	step := LoweringStep{
		Rule:     "rawdocument/" + d.origin.DocumentKind,
		Position: PositionDocument,
		Round:    round,
		From:     d.origin.Document,
		To:       names,
	}
	return emitted, []LoweringStep{step}, nil
}
