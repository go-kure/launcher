# Spike findings: OAM lowering engine

Branch: `spike/oam-lowering-engine` (never merged, no PR). Validates the design in
`autops/wharf/adr` research (`research/abstraction-model/oam-levels-design-decisions.md`,
"OAM levels", adr#34, decisions D1–D7). This document reports what running code found
against each decision; it is mirrored as a comment on adr#34.

## Summary

All seven decisions survived contact with running code, with two concrete corrections:
D3's proof surfaced that "platform-reserved" was previously **documentation, not
enforcement** for a shared schema fragment (`networkPolicy`), and its scope had to be
narrowed per-trait rather than per-shared-schema. D5's four-input closure forced an
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
intentional test pattern outside this spike's scope. **Correction: `PlatformReserved`
had to be scoped per-schema-copy, not per-shared-helper** — `ExposeRule.PropertySchema()`
clones the fragment and sets `PlatformReserved` on its own copy only
(`expose_rule.go`), leaving `IngressHandler`/`HTTPRouteHandler`'s copies untouched. This
is itself a finding: **the design's "mark it declaratively" assumption is right, but the
schema vocabulary needs per-declaration granularity even when the underlying property is
shared** — a real design constraint for the eventual ADR, not a spike-only workaround.

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

## What this does not resolve

- No production API decision: this is spike-grade code on a branch that is never
  merged. Whether the lowering engine becomes a real `pkg/oam` API, a separate
  package, or something else entirely is for ADR-035.
- Document-level 1→N is only proven for **disjoint** output documents (friction #1,
  open).
- No `WebApplication` field set, no crane insertion (crane#441), no Harbor-side design.
- The static type allowlists (`LowerableTypes`, `validComponentTypes`, etc.) were
  proven necessary but the spike does not decide whether they should become a real
  type-registry document — that question is unchanged from the original design doc.
