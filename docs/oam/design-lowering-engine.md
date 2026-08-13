# Spike findings: OAM lowering engine

Branch: `spike/oam-lowering-engine` (never merged, no PR). Validates the design set out
in this project's OAM-levels research notes (decisions D1–D7). This document reports
what running code found against each decision.

## Summary

All seven decisions survived contact with running code, with two concrete corrections:
D3's proof surfaced that "platform-reserved" was previously **documentation, not
enforcement** for a shared schema fragment (`networkPolicy`); the initial fix scoped
`PlatformReserved` per-schema-copy to unblock the spike, but the follow-up ADR decision
was to enforce it **uniformly** across all three sharing traits instead, via an explicit
`schemaNetworkPolicy(reserved bool)` parameter rather than an implicit shared default —
see the D3 section below. D5's four-input closure forced an
engine-level design choice — capability rendering is merged in by the engine, not the
rule — that was not spelled out in the original decisions doc. Everything else matched
the design as written. No production API resulted; `pkg/oam` scope widened beyond its
stated "model + parser + validator" (`pkg/oam/doc.go:1-3`) for the duration of the spike
only, and that widening does not survive un-reverted onto `main`.

## D1 — one engine for all four positions

Holds. `DocumentLoweringRule` / `ComponentLoweringRule` / `TraitLoweringRule` /
`PolicyLoweringRule` (`lowering.go:172-196`) share one `LoweringResult` type, one
fixpoint loop (`lowerDocumentBody`), and one `LoweringContext`. `loweringPositionRules`
(`lowering.go:92-97`) is the only position-specific logic — a lookup table, not a
branch per position in the engine body. The four positions did not need separate
mechanisms; they needed a shared result type wide enough to express "this rule may
touch traits, components, and policies, but not documents" declaratively.

## D2 — 1→N everywhere

Holds, at a real but bounded cost. `NameAllocator` (`lowering.go:138-170`) is the whole
naming mechanism: deterministic `<base>-<suffix>` generation plus collision detection
keyed by authored `Origin`, so two rules independently choosing the same generated name
fail loudly and name both origins rather than silently overwriting. `Origin` provenance
(`lowering.go:35-48`) rides as unexported `origin *Origin` / `sealed bool` fields on
`Trait`/`Component`/`ApplicationPolicy`/`Application` — yaml.v3 ignores unexported
fields, so this cost nothing in the wire format, and value-copy semantics at existing
call sites (`transform.go` cluster-building) preserve it without a pointer-keyed side
table. Document-level 1→N required a genuinely new API,
`TransformAll(app, ctx) ([]*stack.Cluster, error)` (`transform.go:389`), because
`Transform`/`TransformWithPolicy`'s single-`*stack.Cluster` return cannot express a
document splitting into two. `WebApplicationRule` (`pkg/oam/builtin/lowering/webapplication.go`)
exercises exactly this: 1→2 documents, one of which still carries a higher-level
component — proving the fixpoint recurses across rounds, not just once per position.

## D3 — platform-reserved precedence

Holds, but the spike's proof attempt found a real gap: the pre-existing doc comments on
`IngressHandler`/`HTTPRouteHandler` already asserted `networkPolicy` was
"platform-reserved" (`ingress.go:77`, `httproute.go:64`), but nothing enforced it —
~18 tests in `networkpolicy_auto_test.go` author `networkPolicy` directly on those
traits, bypassing any `ClusterProfile`, to exercise netpol synthesis without a
capability round-trip. Marking the shared `schemaNetworkPolicy()` fragment
(`pkg/oam/builtin/traits/schema.go`) reserved broke all of them — a pre-existing,
intentional test pattern outside this spike's scope. The spike's first fix scoped
`PlatformReserved` per-schema-copy (`ExposeRule.PropertySchema()` cloning the fragment
and overriding the flag on its own copy only), which unblocked the spike but left
`IngressHandler`/`HTTPRouteHandler`'s copies unreserved — three schema declarations of
the same property that could silently drift out of sync, with nothing forcing a future
change to touch all three.

**ADR decision: enforce `PlatformReserved` uniformly, via an explicit parameter, not an
implicit shared default.** `schemaNetworkPolicy()` became `schemaNetworkPolicy(reserved
bool)` (`schema.go`) — every call site (`ExposeRule`, `IngressHandler`,
`HTTPRouteHandler`) now states its choice at the call, and today all three pass `true`.
This closes the divergence risk without adding a generic reservation-scoping mechanism
that only one property currently needs — the explicit argument is the whole mechanism.
The ~18 affected tests in `networkpolicy_auto_test.go` (plus 3 in
`domain_resolution_test.go`) were migrated: tests that drive the full
`TransformWithPolicy` pipeline now supply `networkPolicy` via
`ctx.Capabilities["ingress"|"httproute"].Rendering`, mirroring how a real
`ClusterProfile` would render it, instead of authoring it inline on the trait (which
`enforcePlatformReserved` now rejects). Tests that call
`IngressHandler.Apply`/`HTTPRouteHandler.Apply` directly are unaffected — they bypass
`applyTraits`/`enforcePlatformReserved` entirely — and were left authoring it inline.
This is itself a finding: **the design's "mark it declaratively" assumption is right,
and a shared schema fragment does not need a bespoke per-caller-scoping mechanism to
stay uniform — an explicit boolean parameter is enough**, so long as every caller is
required to pass one rather than relying on a default.

`enforcePlatformReserved` (`property_validate.go`) runs at every point that merges
capability rendering in: the trait-lowering branch (`lowering.go`, before
`resolveCapability`), `applyTraits` (`transform.go`, before `resolveCapability`, inside
the `!trait.sealed` guard), and symmetrically before a component handler's
`ToApplicationConfig` (`transform.go`, `createApplications`) — though no component
schema declares a reserved field today, so that call site is currently a no-op in
practice. The proof: `webservice-expose-ingress/app.yaml` loses its inline
`controllerType: ingress` line; `expected.yaml` is **byte-identical** (`git diff
--exit-code` confirms zero diff) because the capability-supplied value was already
sufficient — the authored line was redundant even before D3, and D3 makes that
redundancy an error instead of a silent no-op. `flatschema.go`'s flat allow-sets
(`kurelParamKeys`, `capabilityPropKeys`) are untouched, by design: a capability
rendering schema describes what the platform *may set*, so "platform-reserved" is
meaningless at that call site, exactly like `Enum`/`Properties`/`Items` already are.

## D4 — re-validate emitted elements, plus whole-document validation

Holds, and required writing the validator the design assumed existed —
`PropertySchema` was published (`HandlerSchemas`) but nothing in launcher enforced it
before this spike. `validateProperties`/`validateObjectProperties`
(`property_validate.go`) cover type/required/enum/nested `Properties`/`Items`/
`AdditionalProperties`. Two enforcement points, matching the design: emission-time
(`validateEmittedComponent`/`validateEmittedTrait`/`validateEmittedPolicy`, called the
moment a rule emits an element, citing the **authored** origin first per D7) and a
post-fixpoint whole-document pass with an **empty** `LowerableTypes`
(`lowering.go:380`, `validateWithExtraTypes(doc, customTraitTypes, LowerableTypes{})`)
— any kind/component/trait type still present once the fixpoint has settled is, by
construction, not claimed by any registered rule, so it is a non-terminating rule's
leftover rather than a legitimate terminal type (`lowering.go:287-290,371`).

## D5 — information closure, four inputs

Holds for `expose`, but the port surfaced a design decision the original document did
not spell out: **who performs the capability-rendering merge, the engine or the rule?**
The spike's answer is the engine — `lowerDocumentBody`'s trait branch merges capability
rendering into the trait via `resolveCapability` *before* calling `LowerTrait`
(`lowering.go`, mirroring `applyTraits`'s existing pre-merge for a dispatchable
`TraitHandler`) — so `ExposeRule.LowerTrait` reads `trait.Properties` exactly as
`ExposeHandler.Apply` always did; the port from handler to rule is close to mechanical
(`app.Name` → `lctx.Component.Name`, the two terminal handler calls become emitted
`Trait{Type:"ingress"|"httproute"}`). This closes a corollary the design implied but
did not state: **an emitted element cannot re-enter capability resolution**, because
that would introduce a fifth input (a *different* key's capability rendering, chosen
by the emitted type rather than the authored one). The fix is the `sealed` field
(`types.go`): every trait a `TraitLoweringRule` emits is marked `sealed = true`, and
`applyTraits` skips its entire capability-processing block for a sealed trait
(`transform.go`, `if !trait.sealed`). `TestExposeRule_SealedGuard_ExtraIngressCapabilityIgnored`
(`pkg/cmd/kurel`) proves the guard does something: it fails when the guard is removed
(verified by hand during C5) and passes with it in place, confirming a profile
defining *both* `expose` and `ingress` capabilities produces byte-identical output to
one defining `expose` alone.

## D7 — recursion bound and provenance-first errors

Both proposed defaults survived unchanged. `MaxLoweringDepth = 8` (`lowering.go:29`)
was never approached by any toy or real rule in this spike — the deepest chain
exercised is `WebApplicationRule` → `WebAndCacheRule` → terminal, three rounds. The
bound exists for the pathological case (a rule that keeps re-emitting a type another
rule also claims) and `LoweringError` prints the **authored** `Origin` first, the
`Cause` second, then the full `Chain` of `LoweringStep`s (`lowering.go:207-222`) — one
of the negative tests in C4 specifically asserts a depth-limit failure prints all 8
chain steps, not just the last one. No change to either default was needed.

## Frictions

Three were recorded as findings in the plan rather than designed around; two of the
three resolved themselves once C5 actually implemented `expose` as a rule, and are
recorded here as *resolved* rather than *open*.

1. **Document-level splitting fragments cluster-wide passes (open).** Netpol synthesis
   (`synthesizeNetworkPolicies` et al., post-fixpoint in `TransformWithPolicy`) and
   source dedup both operate per-cluster, after a document has already been split into
   N `*stack.Cluster`s by `TransformAll`. The toy `WebApplicationRule` splits into
   deliberately **disjoint** documents to sidestep this; a real document-splitting rule
   whose outputs need to share a netpol or dedup pass is not exercised here. This stays
   open — the spike did not attempt a fix, since no such rule exists yet to design
   against.
2. **Moving `expose` out of `traitHandlers` silently drops its capability validation
   (resolved).** `EvaluateProfile` (`transform.go`) only ever looked up
   `t.traitHandlers[typeName]`; once `ExposeRule` replaced `ExposeHandler` there
   (C5), the `gatewayName`-required and `gatewayNamespace`-default checks would have
   stopped running with no failing test to catch it. Fixed by extending
   `EvaluateProfile` to fall back to `t.traitLoweringRules[typeName]` and its optional
   `ValidateAndApplyDefaults` (`transform.go`). `TestExposeRule_EvaluateProfile_GatewayValidation`
   (`pkg/oam/builtin/traits`) is the regression guard.
3. **An emitted trait re-entering capability resolution is a fifth D5 input
   (resolved).** Covered under D5 above — the `sealed` field and the `!trait.sealed`
   guard in `applyTraits`.

## Byte-identity cost

The C1 no-op guarantee — zero lowering rules registered, `Transform`/
`TransformWithPolicy`/`TransformAll` behave exactly as before — was verified twice:
`TestLower_EmptyRegistry_ReturnsSamePointer` (`pkg/oam/lowering_test.go`) asserts
pointer identity on the returned document when no rule is registered, and
`UPDATE_GOLDEN=1 go test ./pkg/cmd/kurel -run TestFixtures` followed by
`git diff --exit-code pkg/cmd/kurel/testdata` produced **zero diff** across every
existing golden. The cost of the engine's presence, with zero rules registered, is
one extra pointer-identity check per `Transform` call — not a measurable runtime cost,
and not a single behavioral difference. C5's expose-as-a-rule migration re-ran the
same proof at the feature level: both expose goldens (`webservice-expose-ingress`,
the gateway fixture) pass **without** `UPDATE_GOLDEN`, and all five
`expose_*_test.go` files keep their original assertions, re-pointed through a shared
`applyExpose` helper that feeds `ExposeRule.LowerTrait`'s emitted trait to the real
`IngressHandler`/`HTTPRouteHandler.Apply` — reproducing byte-for-byte what the engine's
fixpoint plus `applyTraits` does end to end.

## Entry-point contract: in-transform and raw-document

The spike above proved the engine mechanics (D1–D7) for documents that already
unmarshal into the base `Application`/`ApplicationSpec` shape, entered from inside the
existing transform pipeline. Follow-on integration work against a real downstream
parser surfaced a case the spike did not cover, and it changes what "the engine" has to
be reachable from. This section specifies both entry points the production API must
expose; it is the contract the next implementation phase builds against, not new
spike-proven code itself.

### The finding

A consumer embedding this engine does not necessarily hand it an already-decoded
`Application`. A consumer's own parser may gate on `kind`/`apiVersion` *before* any
transform runs, rejecting a higher-level kind outright — a document the engine was
meant to lower never reaches a transform call at all, because it never survives the
consumer's own parse step. Separately, a consumer may parse the same raw document bytes
independently at several call sites in its own codebase (validation, export, one or
more command entry points, pre-generation passes, …), not through one funnel — so an
insertion point reachable from only one of those sites still leaves the others exposed
to the same rejection. A production lowering engine therefore has to be reachable
directly from raw, undecoded document bytes, ahead of a consumer's own parsing, not
only from a point downstream of it.

This matters only for a **whole-noun higher-level kind**: a document authored under a
`kind` and `apiVersion` of its own, carrying fields that do not fit the base
`ApplicationSpec` shape at all, so it cannot be unmarshalled into an `Application` in
the first place — not for a document that already fits the base shape and merely needs
a trait/component/policy lowered out of it. The latter case is exactly what D1–D7 above
validated and needs no new machinery.

### Entry point 1 — in-transform (already proven)

For a document whose authored YAML already unmarshals into the base
`Application`/`ApplicationSpec` shape, the engine runs where the spike put it: inside
the transform pipeline, downstream of `Parse`, reached through `Transform`/
`TransformWithPolicy` (`pkg/oam/transform.go:333`) and `TransformAll`
(`transform.go:389`) for the document-splitting case. This is the mechanism D1–D7
exercised — `DocumentLoweringRule`/`ComponentLoweringRule`/`TraitLoweringRule`/
`PolicyLoweringRule` operating on a decoded `Application`, with `LoweringContext`,
`Origin` provenance, `NameAllocator`, and the `MaxLoweringDepth` fixpoint bound all
already proven against it. No change to this entry point is implied by the finding
above — it remains correct and sufficient for every document that reaches it.

### Entry point 2 — raw-document (new, required by the finding)

For a whole-noun higher-level kind that cannot survive the base parse, the engine must
also be reachable *before* parsing: an exported function operating on undecoded
document bytes, run once per raw document ahead of a consumer's own parse fan-out,
producing raw bytes that a standard `Application` parse can then accept. Proposed
shape:

```go
// LowerRaws rewrites a set of raw OAM documents, replacing any document whose
// kind/apiVersion is claimed by a registered RawDocumentLoweringRule with the
// one or more base-shaped documents it lowers to. A document not claimed by any
// rule passes through unchanged. Call this before parsing, not after.
func LowerRaws(docs []json.RawMessage) ([]json.RawMessage, error)
```

backed by a new rule type, `RawDocumentLoweringRule`, analogous in spirit to
`DocumentLoweringRule` but operating on `kind`/`apiVersion`-sniffed raw bytes rather
than a decoded `Application` — it cannot assume the input unmarshals into any type
this package already knows, since the whole point is that it may not. A consumer calls
`LowerRaws` once, ahead of every one of its own parse call sites, rather than calling it
per site; a document it rewrites then flows through the ordinary parse path and,
if applicable, into entry point 1 above unchanged.

### Open questions left for the implementation phase

- Whether `RawDocumentLoweringRule` can share `LoweringContext`/`Origin`
  provenance/`NameAllocator`/`MaxLoweringDepth` with the decoded-document rule types, or
  needs its own bounded fixpoint over raw bytes — provenance tracking assumes a decoded
  document today, and raw bytes may not have one until a rule lowers them into base
  shape.
- Whether a document a raw-document rule emits should be eligible to re-enter
  `LowerRaws` itself (a raw-to-raw fixpoint) or must always land in base shape in one
  step, mirroring the `sealed` guard's constraint on entry point 1 (D5 above) that an
  emitted element cannot re-enter the same resolution it came from.
- Exact registration and dispatch shape for `RawDocumentLoweringRule` (a lookup keyed
  on sniffed `kind`/`apiVersion`, mirroring `loweringPositionRules`, or something
  narrower) is unspecified here; the interface signature above is the contract, not the
  registry mechanics.

## What this does not resolve

- No production API decision for entry point 1: this remains spike-grade code on a
  branch that is never merged. Whether the lowering engine becomes a real `pkg/oam`
  API, a separate package, or something else entirely is for the implementation phase.
- Document-level 1→N is only proven for **disjoint** output documents (friction #1,
  open).
- No `WebApplication` field set. The entry-point contract above specifies the shape of
  `LowerRaws`/`RawDocumentLoweringRule`; it does not implement them, and it does not
  design how any particular downstream consumer wires its own parse fan-out through
  entry point 2 — that is a consumer-side integration decision, out of scope for this
  library.
- The static type allowlists (`LowerableTypes`, `validComponentTypes`, etc.) were
  proven necessary but the spike does not decide whether they should become a real
  type-registry document — that question is unchanged from the original design doc, and
  extends to whatever raw-`kind`/`apiVersion` registry entry point 2 ends up needing.
