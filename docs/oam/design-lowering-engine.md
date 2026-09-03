# Design rationale: OAM lowering engine (ADR-035)

Validates the design set out in this project's OAM-levels research notes (decisions
D1–D7). The mechanics below were first proven on a throwaway spike branch
(`spike/oam-lowering-engine`, never merged, no PR); this document originally reported
what running code found against each decision there. The production implementation
this document now describes ships on `feat/oam-lowering-engine` (draft PR #274):
`pkg/oam/lowering.go`, `pkg/oam/lowering_raw.go`, the `PlatformReserved`/
`enforcePlatformReserved` enforcement, and `expose` as a registered `TraitLoweringRule`
are all real code on this branch today, not spike-only prototypes. Where a spike-time
question was left open, the section below states how it was actually resolved, with a
citation to the resolving code.

## Summary

All seven decisions survived contact with running code, with two concrete corrections:
D3's proof surfaced that "platform-reserved" was previously **documentation, not
enforcement** for a shared schema fragment (`networkPolicy`); the fix that shipped
enforces it **uniformly** across all three sharing traits, via an explicit
`schemaNetworkPolicy(reserved bool)` parameter rather than an implicit shared default —
see the D3 section below. D5's four-input closure forced an
engine-level design choice — capability rendering is merged in by the engine, not the
rule — that was not spelled out in the original decisions doc. Everything else matched
the design as written. `pkg/oam`'s scope genuinely widened beyond its doc-comment's
stated "model + parser + validator" — the lowering engine is now part of the
package's production surface, not a spike-only detour that reverts before merge.
`pkg/oam/doc.go`'s package comment was updated in this PR to say so.

## D1 — one engine for all four positions

Holds. `DocumentLoweringRule` / `ComponentLoweringRule` / `TraitLoweringRule` /
`PolicyLoweringRule` (`lowering.go:180-235`) share one `LoweringResult` type, one
fixpoint loop (`lowerDocumentBody`), and one `LoweringContext`. `loweringPositionRules`
(`lowering.go:92-97`) is the only position-specific logic — a lookup table, not a
branch per position in the engine body. The four positions did not need separate
mechanisms; they needed a shared result type wide enough to express "this rule may
touch traits, components, and policies, but not documents" declaratively.

## D2 — 1→N everywhere

Holds, at a real but bounded cost. `NameAllocator` (`lowering.go:140-172`) is the whole
naming mechanism: deterministic `<base>-<suffix>` generation plus collision detection
keyed by authored `Origin`, so two rules independently choosing the same generated name
fail loudly and name both origins rather than silently overwriting. `Origin` provenance
(`lowering.go:46-52`) rides as unexported `origin *Origin` / `sealed bool` fields on
`Trait`/`Component`/`ApplicationPolicy`/`Application` — yaml.v3 ignores unexported
fields, so this cost nothing in the wire format, and value-copy semantics at existing
call sites (`transform.go` cluster-building) preserve it without a pointer-keyed side
table.

`Origin` carries one field, `Rule` (`lowering.go:72-111`), that breaks its own
"stamped once, copied verbatim" rule: every other field names the AUTHORED location
and is fixed forever once stamped, but `Rule` names whichever lowering rule MOST
RECENTLY produced the element, so it is deliberately re-derived at every hop rather
than copied — a verbatim copy would leave a round-1 rule's identity on an element a
round-2 rule actually went on to produce. A component, policy, or trait a rule only
*forwards* verbatim (rather than constructs) is exempted from that re-stamp via
pointer-identity checks (`isForwardedComponent`, `isForwardedPolicy`,
`isForwardedTrait`) and keeps whatever `Rule` it already carried — possibly `""`, if
never itself the direct output of a rule invocation.

Document-level 1→N (one authored document lowering into several) ships only at the
**raw** entry point today: `testRawRule` (`pkg/oam/lowering_raw_test.go:41-139`) emits
`n` sibling `Application` documents from one raw document when its `emit` field is set,
proving `LoweringResult.Documents` with more than one entry round-trips through the
shared fixpoint (`runLowering`, `lowering.go:439`) correctly — each sibling gets its own
generated name via the shared `NameAllocator`, and the `slot`-keyed splice
(`lowering_raw.go:101-121`) puts every emitted document back at its raw input's
position. This is proven with a test-only rule, not a shipped built-in — no concrete
production rule in this repo emits more than one document yet. The in-transform
document position (`DocumentLoweringRule`) is exercised for 1→1 renaming
(`testDocRule`, `lowering_test.go:144-157`) but not for 1→N on this branch; see
"Document-level splitting fragments cluster-wide passes" under Frictions below for what
still blocks a real multi-cluster-output rule, and `TransformAll` under "What this does
not resolve" for the still-unshipped API that would consume such a split.

## D3 — platform-reserved precedence

Holds, but the proof attempt found a real gap: the pre-existing doc comments on
`IngressHandler`/`HTTPRouteHandler` already asserted `networkPolicy` was
"platform-reserved" (`ingress.go:79-81`, `httproute.go:64`), but nothing enforced it —
~18 tests in `networkpolicy_auto_test.go` author `networkPolicy` directly on those
traits, bypassing any `ClusterProfile`, to exercise netpol synthesis without a
capability round-trip. Marking the shared `schemaNetworkPolicy()` fragment
(`pkg/oam/builtin/traits/schema.go`) reserved broke all of them — a pre-existing,
intentional test pattern this needed to preserve, not route around. An initial fix
scoped `PlatformReserved` per-schema-copy (`ExposeRule.PropertySchema()` cloning the
fragment and overriding the flag on its own copy only), which unblocked forward
progress but left `IngressHandler`/`HTTPRouteHandler`'s copies unreserved — three schema
declarations of the same property that could silently drift out of sync, with nothing
forcing a future change to touch all three.

**Decision: enforce `PlatformReserved` uniformly, via an explicit parameter, not an
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
`applyTraits`/`enforcePlatformReserved` entirely — and were left authoring it inline
(see `TestHandlers_ReservedNetworkPolicy_StillAppliesDirectly`,
`pkg/oam/builtin/traits/platform_reserved_apply_test.go`). This is itself a finding:
**the design's "mark it declaratively" assumption is right, and a shared schema
fragment does not need a bespoke per-caller-scoping mechanism to stay uniform — an
explicit boolean parameter is enough**, so long as every caller is required to pass one
rather than relying on a default.

The same class of gap recurred once more before this branch was ready for review:
`IngressHandler`'s own `allowedHostnameWildcard` property carried the identical
"platform-reserved" doc-comment claim (`ingress.go:79-81`) without the
`PlatformReserved: true` flag on its own schema entry, even after `ExposeRule`'s copy of
the same key was correctly reserved — an author could bypass hostname-wildcard
enforcement entirely by authoring the `ingress` trait directly instead of going through
`expose`. Fixed the same way: `PropertySchema.PlatformReserved: true` added to
`IngressHandler`'s `allowedHostnameWildcard` entry (`ingress.go:131`), with a regression
test proving the bypass is closed
(`TestIngressHandler_AllowedHostnameWildcard_InlineAuthoringRejected`,
`pkg/oam/builtin/traits/ingress_platform_reserved_test.go`).

`enforcePlatformReserved` (`property_validate.go:174`) runs at every point that merges
capability rendering in: the trait-lowering branch (`lowering.go:661-670`, before
`resolveCapability`), `applyTraits` (`transform.go:760-769`, before
`resolveCapability`, inside the `!trait.sealed` guard), and symmetrically before a
component handler's `ToApplicationConfig` (`transform.go:517`, `createApplications`) —
though no component schema declares a reserved field today, so that call site is
currently a no-op in practice. The proof: `webservice-expose-ingress/app.yaml` loses its
inline `controllerType: ingress` line; `expected.yaml` is **byte-identical** because the
capability-supplied value was already sufficient — the authored line was redundant even
before D3, and D3 makes that redundancy an error instead of a silent no-op. A third
fixture, `pkg/cmd/kurel/testdata/app.yaml`, carried the same now-redundant inline
`controllerType: ingress` line and was missed in that earlier pass; it was found and
fixed the same way — `pkg/cmd/kurel/testdata/cluster.yaml` already supplies
`controllerType` via capability rendering, so deleting the inline line is the whole fix.
`flatschema.go`'s flat allow-sets (`kurelParamKeys`, `capabilityPropKeys`) are untouched,
by design: a capability rendering schema describes what the platform *may set*, so
"platform-reserved" is meaningless at that call site, exactly like
`Enum`/`Properties`/`Items` already are.

`RegisterTrait` (`transform.go:152-167`) has always panicked at registration time if a
dispatchable `TraitHandler` implements `CapabilityAware` without also implementing
`ValidateAndApplyDefaults`, because `EvaluateProfile`'s dispatch needs
`ValidateAndApplyDefaults` to validate/default a capability-rendered binding before use.
`RegisterTraitLowering` (`lowering.go:324-351`) lacked the equivalent assertion:
`EvaluateProfile`'s trait-lowering-rule fallback (`transform.go`) has the identical
need — a `TraitLoweringRule` implementing `CapabilityAware` without
`ValidateAndApplyDefaults` would have its capability rendering accepted unvalidated and
undefaulted, with no signal at registration time. `ExposeRule` implements both
interfaces today, so this was not a live bug, but nothing structurally prevented a
future rule from reintroducing the exact class of gap Friction #2 below records as
fixed specifically for `expose`. Closed by adding the same guard to
`RegisterTraitLowering` that `RegisterTrait` already has, with a registration-time panic
and regression tests proving both the accept and reject paths
(`TestRegisterTraitLowering_PanicsIfCapabilityAwareWithoutVAD`,
`TestRegisterTraitLowering_CapabilityAwareWithVAD_OK`, `pkg/oam/lowering_test.go`).

## D4 — re-validate emitted elements, plus whole-document validation

Holds, and required writing the validator the design assumed existed —
`PropertySchema` was published (`HandlerSchemas`) but nothing in launcher enforced it
before this work. `validateProperties`/`validateObjectProperties`
(`property_validate.go`) cover type/required/enum/nested `Properties`/`Items`/
`AdditionalProperties`. Two enforcement points, matching the design: emission-time
(`validateEmittedComponent`/`validateEmittedTrait`/`validateEmittedPolicy`, called the
moment a rule emits an element, citing the **authored** origin first per D7) and a
post-fixpoint whole-document pass with an **empty** `LowerableTypes`
(call site `lowering.go:483`, `validateSettled` itself at `lowering.go:502-508`, which
calls `validateWithExtraTypes(doc, customTraitTypes, LowerableTypes{})`) — any
kind/component/trait type still present once the fixpoint has settled is, by
construction, not claimed by any registered rule (`LowerableTypes`'s own doc comment,
`lowering.go:366-370`), so it is a non-terminating rule's leftover rather than a
legitimate terminal type.

## D5 — information closure, four inputs

Holds for `expose`, and the port surfaced a design decision the original document did
not spell out: **who performs the capability-rendering merge, the engine or the rule?**
The answer that shipped is the engine — `lowerDocumentBody`'s trait branch merges
capability rendering into the trait via `resolveCapability` *before* calling
`LowerTrait` (`lowering.go:671-677`, mirroring `applyTraits`'s existing pre-merge for a
dispatchable `TraitHandler`) — so `ExposeRule.LowerTrait` reads `trait.Properties`
exactly as the former `ExposeHandler.Apply` always did; the port from handler to rule is
close to mechanical (`app.Name` → `lctx.Component.Name`, the two terminal handler calls
become emitted `Trait{Type:"ingress"|"httproute"}`). This closes a corollary the design
implied but did not state: **an emitted element cannot re-enter capability resolution**,
because that would introduce a fifth input (a *different* key's capability rendering,
chosen by the emitted type rather than the authored one). The fix is the `sealed` field
(`types.go`): every trait a `TraitLoweringRule` emits is marked `sealed = true`
(`lowering.go:687`), and `applyTraits` skips its entire capability-processing block for
a sealed trait (`transform.go:721-727`, `if !trait.sealed`).
`TestExposeRule_SealedGuard_ExtraIngressCapabilityIgnored`
(`pkg/cmd/kurel/expose_sealed_test.go`) proves the guard does something: it fails when
the guard is removed (verified by hand) and passes with it in place, confirming a
profile defining *both* `expose` and `ingress` capabilities produces byte-identical
output to one defining `expose` alone.

## D7 — recursion bound and provenance-first errors

Both proposed defaults survived unchanged and ship as written. `MaxLoweringDepth = 9`
(`lowering.go:40`) was never approached by any rule exercised so far — the deepest
chain any test drives is three rounds. The bound exists for the pathological case (a
rule that keeps re-emitting a type another rule also claims) and `LoweringError` prints
the **authored** `Origin` first, the `Cause` second, then the full `Chain` of
`LoweringStep`s (`LoweringError` struct at `lowering.go:248-263`) — one of the negative
tests specifically asserts a depth-limit failure prints all `MaxLoweringDepth` chain
steps, not just the last one (`TestLower_DepthLimit_PrintsFullChain`,
`pkg/oam/lowering_negative_test.go`). No change to either default was needed.

## Frictions

Three were recorded as findings in the plan rather than designed around; two of the
three resolved themselves once `expose` was actually implemented as a rule, and are
recorded here as *resolved* rather than *open*.

1. **Document-level splitting fragments cluster-wide passes (still open).** Netpol
   synthesis (`synthesizeNetworkPolicies` et al., post-fixpoint in
   `TransformWithPolicy`) and source dedup both operate per-cluster. A document-position
   rule producing more than one output document has no shipped API to turn those
   multiple documents into multiple `*stack.Cluster`s in the first place — see
   `TransformAll` under "What this does not resolve" below — so this friction has not
   yet been exercised against real multi-cluster output, only against the raw entry
   point's `testRawRule` fixture (D2 above), whose siblings are deliberately
   **disjoint** and never reach a cluster-wide pass. This stays open: no fix was
   attempted, since no consumer of split output exists yet to design against.
2. **Moving `expose` out of `traitHandlers` silently drops its capability validation
   (resolved).** `EvaluateProfile` (`transform.go`) only ever looked up
   `t.traitHandlers[typeName]`; once `ExposeRule` replaced `ExposeHandler` there, the
   `gatewayName`-required and `gatewayNamespace`-default checks would have stopped
   running with no failing test to catch it. Fixed by extending `EvaluateProfile` to
   fall back to `t.traitLoweringRules[typeName]` and its optional
   `ValidateAndApplyDefaults` (`transform.go`). `TestExposeRule_EvaluateProfile_GatewayValidation`
   (`pkg/oam/builtin/traits/expose_rule_evaluate_profile_test.go`) is the regression
   guard. The D3 section above records the one place this fallback's own guard was
   still incomplete: `RegisterTraitLowering` did not enforce
   `CapabilityAware`⇒`ValidateAndApplyDefaults` the way `RegisterTrait` does — now fixed
   structurally, not just for `expose` specifically.
3. **An emitted trait re-entering capability resolution is a fifth D5 input
   (resolved).** Covered under D5 above — the `sealed` field and the `!trait.sealed`
   guard in `applyTraits`.

## Byte-identity cost

The C1 no-op guarantee — zero lowering rules registered, `Transform`/
`TransformWithPolicy` behave exactly as before — was verified twice:
`TestLower_EmptyRegistry_ReturnsSamePointer` (`pkg/oam/lowering_test.go:12`) asserts
pointer identity on the returned document when no rule is registered, and
`UPDATE_GOLDEN=1 go test ./pkg/cmd/kurel -run TestFixtures` followed by
`git diff --exit-code pkg/cmd/kurel/testdata` produces **zero diff** across every
existing golden. The cost of the engine's presence, with zero rules registered, is
one extra pointer-identity check per `Transform` call — not a measurable runtime cost,
and not a single behavioral difference. The expose-as-a-rule migration re-ran the
same proof at the feature level: both expose goldens (`webservice-expose-ingress`,
the gateway fixture) pass **without** `UPDATE_GOLDEN`, and all five
`expose_*_test.go` files keep their original assertions, re-pointed through a shared
`applyExpose` helper (`pkg/oam/builtin/traits/traits_test.go`) that feeds
`ExposeRule.LowerTrait`'s emitted trait to the real
`IngressHandler`/`HTTPRouteHandler.Apply` — reproducing byte-for-byte what the engine's
fixpoint plus `applyTraits` does end to end.

## Entry-point contract: in-transform and raw-document

The engine mechanics above (D1–D7) were first proven for documents that already
unmarshal into the base `Application`/`ApplicationSpec` shape, entered from inside the
existing transform pipeline. Follow-on integration work against a real downstream
parser surfaced a case that first pass did not cover, and it changes what "the engine"
has to be reachable from. This section specifies both entry points the shipped API
exposes.

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

### Entry point 1 — in-transform (shipped)

For a document whose authored YAML already unmarshals into the base
`Application`/`ApplicationSpec` shape, the engine runs downstream of `Parse`, reached
through `Transform` (`pkg/oam/transform.go:369`) / `TransformWithPolicy`
(`pkg/oam/transform.go:377`). `DocumentLoweringRule`/`ComponentLoweringRule`/
`TraitLoweringRule`/`PolicyLoweringRule` operate on a decoded `Application`, with
`LoweringContext`, `Origin` provenance, `NameAllocator`, and the `MaxLoweringDepth`
fixpoint bound all proven against it (D1–D7 above) and shipping on this branch. The
document-splitting-to-multiple-clusters case (`TransformAll`) is the one piece of the
original mechanics that is **not** implemented here — see "What this does not resolve"
below; its absence does not affect this entry point's correctness for the
single-document case it does handle.

### Entry point 2 — raw-document (shipped)

For a whole-noun higher-level kind that cannot survive the base parse, the engine is
also reachable *before* parsing: `(*Transformer).LowerRaws` operates on undecoded
document bytes, run once per raw document ahead of a consumer's own parse fan-out,
producing raw bytes that a standard `Application` parse can then accept. Shipped shape
(`pkg/oam/lowering_raw.go:42`, differing from the pre-implementation proposal by being a
`*Transformer` method rather than a package function, and by taking a
`TransformContext` — see below for why):

```go
func (t *Transformer) LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error)
```

backed by `RawDocumentLoweringRule` (`lowering.go:201-217`), analogous in spirit to
`DocumentLoweringRule` but operating on `kind`-sniffed raw bytes rather than a decoded
`Application` — it cannot assume the input unmarshals into any type this package
already knows, since the whole point is that it may not. A consumer calls `LowerRaws`
once, ahead of every one of its own parse call sites, rather than calling it per site;
a document it rewrites then flows through the ordinary parse path and, if applicable,
into entry point 1 above unchanged.

### How the open questions were resolved

Three questions were left open when this contract was first specified, ahead of
implementation. All three are answered by the shipped code:

- **Can `RawDocumentLoweringRule` share `LoweringContext`/`Origin`
  provenance/`NameAllocator`/`MaxLoweringDepth` with the decoded-document rule types, or
  does it need its own bounded fixpoint over raw bytes?** It shares them fully.
  `runLowering` (`lowering.go:439`) is, by its own doc comment, "the ONE fixpoint
  implementation in this package" — both entry points call it exactly once per
  invocation, so one `NameAllocator`, one expansion chain, and one `MaxLoweringDepth`
  budget are shared across every document in the call, siblings from different raw
  inputs included. Round 0 for a raw-entered document (`lowerRawOnce`,
  `lowering_raw.go:134-150`) decodes the bytes and calls the rule's `LowerDocument`
  exactly as `lowerDocumentOnce`'s document-rule branch would; from round 1 on, every
  descendant is an ordinary `*Application` and follows the identical path an
  in-transform document does. `ctx TransformContext` is threaded through `LowerRaws`
  precisely because of this sharing: rounds after round 0 can reach an ordinary
  `TraitLoweringRule`/`ComponentLoweringRule`/`PolicyLoweringRule`, and a
  `CapabilityAware` one among them needs `ctx.Capabilities` populated from an
  already-evaluated `ClusterProfile` or it fails with `ErrMissingCapability` — a caller
  that has not evaluated a profile yet passes what it has, and gets the same failure
  the in-transform path would produce for the same input (`lowering_raw.go:36-41`).
- **Should a document a raw-document rule emits be eligible to re-enter `LowerRaws`
  itself (a raw-to-raw fixpoint), or must it always land in base shape in one step?**
  It must land in base shape in one step; there is no raw-to-raw re-entry. Only the
  original seed entries built directly from `raws` carry a non-nil `raw` field
  (`loweringDoc.raw`, `lowering.go:420`); every document `LowerDocument` emits at round
  0 becomes an ordinary `*Application` in `next` (`runLowering`'s loop,
  `lowering.go:449-479`), which subsequent rounds process via `lowerDocumentOnce`, never
  `lowerRawOnce`, so `t.rawDocLoweringRules` is never consulted again for it. This
  mirrors the `sealed` guard's constraint on entry point 1 (D5 above): an emitted
  element does not re-enter the resolution mechanism it came from.
- **What is the exact registration and dispatch shape for `RawDocumentLoweringRule`?**
  A lookup keyed on the sniffed `(apiVersion, kind)` pair. Registration is
  `t.rawDocLoweringRules map[rawDocRuleKey]RawDocumentLoweringRule` (`transform.go`,
  populated by `RegisterRawDocumentLowering` in `lowering.go`, with the same
  duplicate/cross-registrar collision guards `RegisterDocumentLowering` has in the other
  direction — the cross-registrar guard stays kind-wide, so one kind string is claimed by
  at most one registrar regardless of group). A rule claims `SupportedAPIVersion` unless
  it implements the optional `RawDocumentAPIVersioner` hook (`APIVersion() string`); a
  consumer that owns its own API group implements it so `LowerRaws` claims that group's
  documents instead of silently passing them through to the consumer's own parser, which
  would reject them (the gap the original single-group gate left open). Dispatch decodes
  just enough of each raw input — `apiVersion`, `kind` and `metadata.name`/`namespace`,
  via the lenient `documentEnvelope` probe (`lowering_raw.go`) — to look up the pair; an
  input whose pair matches no registered rule passes through byte-identical, never
  decoded or re-serialized. Pass-through `Application` identities are pre-reserved
  against generated child names only for groups some rule claims (plus
  `SupportedAPIVersion`); an `Application` under an unclaimed group is a foreign
  resource, not a collidable identity. A settled document may carry
  `SupportedAPIVersion` or any group a registered raw rule claims (`validateSettled`
  validates it under `SupportedAPIVersion` otherwise unchanged); a rule emitting into an
  unclaimed group fails with a `LoweringError` against the authored document. The
  in-transform path is unaffected: it gates on `SupportedAPIVersion` before any rule
  runs and never consults this registry.

## What this does not resolve

- **`TransformAll` (document-level 1→N producing multiple `*stack.Cluster`s) is not
  implemented on this branch.** It remains the one piece of the original mechanics that
  stayed unshipped: `Transform`/`TransformWithPolicy`'s single-`*stack.Cluster` return
  cannot express a document splitting into two, and no equivalent multi-cluster API
  exists in `pkg/oam/transform.go` today. Whether the lowering engine's
  document-splitting support ever needs a production `TransformAll` (or some other
  shape) is still undecided and unimplemented; D2's 1→N proof above rests on the raw
  entry point's `testRawRule` fixture alone (test-only), not on a shipped mechanism for
  consuming split output.
- Document-level 1→N is, correspondingly, only proven for **disjoint** output documents
  (Friction #1 above, still open) — no rule shipped or tested here produces documents
  whose outputs need to share a cluster-wide pass (netpol synthesis, source dedup).
- No `WebApplication`-style higher-level-kind builtin ships in this repo. The
  entry-point contract above specifies, and the shipped code implements, the mechanism
  (`LowerRaws`/`RawDocumentLoweringRule`); it does not include a concrete production
  rule built on top of it, and it does not design how any particular downstream
  consumer wires its own parse fan-out through entry point 2 — that remains a
  consumer-side integration decision, out of scope for this library.
- The static type allowlists (`LowerableTypes`, `validComponentTypes`, etc.) were
  proven necessary but whether they should become a real type-registry document is
  still undecided — unchanged from the original design doc, and extends to whatever
  raw-`kind` registry a future higher-level-kind rule ends up needing.
