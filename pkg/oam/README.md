# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. All documents use `apiVersion: launcher.gokure.dev/v1alpha1`.

## Document kinds

| Kind | Type | Purpose |
|------|------|---------|
| `Application` | `Application` | The app: `components[]` (each with `type` + `properties`) and `traits[]`. |
| `Package` | `Package` | A parameterized, distributable unit: `app.yaml` + a `kurel.yaml` parameter schema (`ParameterDecl`). |
| `ClusterProfile` | `ClusterProfile` | Platform choices (trait implementations, capabilities) supplied at build time. |
| `CapabilityDefinition` | `CapabilityDefinition` | Declares a capability's rendering/property schema for validation. |

## Pipeline

```
parse → resolve parameters → transform (component + trait handlers) → manifests
```

1. **Parse** an Application/Package/ClusterProfile from YAML.
2. **Resolve parameters** (`ResolveParameters`) — apply `kurel.yaml` declarations,
   values files, and `--set` overrides via `${var}` substitution.
3. **Transform** (`Transformer`) — dispatch each component to its
   `ComponentHandler` and each trait to its `TraitHandler`, merging the
   `ClusterProfile`'s capability choices.

A Phase-4 post-build stage then synthesizes per-component `NetworkPolicy` resources,
each a **separate** additive resource (the authored `networkpolicy` /
`cilium-networkpolicy` traits are unaffected):

- **Inbound** (`{comp}-allow-ingress-traffic`) — routing-derived, from routing traits'
  platform-reserved `networkPolicy.trafficSources` capability rendering. When a routing trait's
  `backendRef` names a **separate** backend Service (not the exposing component's own),
  the allow is **retargeted onto that backend component's pods** + the backendRef port — resolved
  by matching the backend Service name to a sibling OAM component **cluster-wide**, and the
  retargeted policy is emitted in the **backend component's own leaf bundle**. Resolution spans
  bundles: components of one Application share a namespace but are split across leaf bundles
  (dependency-aware = one per component, hierarchical = one per tier), so a router in one bundle
  correctly retargets onto a backend in another. Two components resolving to the **same** Service
  name is ambiguous and **fails the transform**. A backendRef
  that resolves to no component (a **bare external Service**) is left authored **unless**
  it carries an explicit authored `backendSelector` (matchLabels only, on the routing trait's
  `paths[].backend` / `backendRefs[]` — the selector is not inferable from a Service name): that
  emits a separate `{service}-allow-ingress-traffic` policy in the router's namespace selecting the
  backend's pods on the backendRef ports. **Same-namespace only** (the backend is referenced by bare
  name; cross-namespace `ReferenceGrant` is out of scope). External backends are deduplicated
  **cluster-wide** (routers in different leaf bundles naming the same Service emit one merged
  policy). Two routers giving one external Service different selectors, or an external policy name
  colliding with a component's emitted inbound policy, **fails the transform** rather than emitting
  conflicting or duplicate allows.
- **Egress** (`{comp}-allow-egress-traffic`) — from `TransformContext.EgressPeers`, a
  downstream-supplied, non-authorable synthesis input (graph-derived dependency peers; never
  set from OAM YAML or capability rendering). K8s `NetworkPolicy` only. Empty when a
  caller supplies no peers (e.g. the kurel CLI), so synthesis is then a no-op. **Fail-fast**
  (aligned with the endpoint-ingress family): a peer that carries ports but a nil, empty, or
  expression-bearing pod selector is a producer bug and fails the transform with an error — it
  would otherwise emit a namespace-wide egress allow. A peer with **no ports** is the documented
  escape hatch and is silently skipped (the destination stays authored).
- **Endpoint ingress** (`{comp}-allow-endpoint-ingress`) — the **target side** of a
  connection, from `TransformContext.IngressPeers` (a platform-supplied, non-authorable
  graph-derived input). Each `netpol.IngressPeer` names an `Endpoint` (pod selector + ports)
  and the sources allowed to reach it; launcher emits an Ingress `NetworkPolicy` selecting the
  **endpoint's own selector** — deliberately **not** the component-label key — so it protects
  operator-created pods (e.g. a CloudNativePG cluster's `cnpg.io/cluster` instance pods) that
  carry no component-provenance label. Fail-closed: each source must carry a namespace + a
  non-empty matchLabels pod selector (namespace-wide sources are dropped), and a policy with no
  valid rule is not emitted. A component's endpoints are declared by its handler via the
  optional `EndpointProvider` interface and read through `Transformer.ComponentEndpoints` — the
  producer half a downstream platform uses to learn the real selector (no hardcoding) and build
  its dependency graph. One policy is emitted **per distinct endpoint**: a single-endpoint
  component keeps the bare `{comp}-allow-endpoint-ingress` name, while a multi-endpoint component
  (e.g. a CloudNativePG cluster plus its pooler) suffixes each policy with a short content hash of
  the endpoint, so the names are distinct and stay stable across unrelated endpoint additions. The
  suffix names the emitted **NetworkPolicy resource** itself (not just the internal layout entry),
  so a multi-endpoint component's resource ids are unique and `kustomize build` accepts them.

The inbound/egress families select the component's own pods (the ingress recipients / the egress
source pods) via a **derived `<domain>/component`** label by default — the domain comes
from `TransformContext.Domain` (empty ⇒ the library default `gokure.dev`; the kurel CLI
uses `launcher.gokure.dev`). The full key is overridable per transform through
`TransformContext.ComponentLabelKey` (precedence: `ComponentLabelKey` > `<Domain>/component`
> `gokure.dev/component`). This is a **platform contract**: `trafficSources` (inbound) and
`EgressPeers` (egress) are platform inputs, so a caller that injects them must ensure its
pods carry the derived label — a downstream platform sets its own `Domain` and stamps the
matching `<domain>/component` on every rendered workload and helm-rendered pod — or set
`ComponentLabelKey` to a label its pods do carry (e.g. `"app"`). A caller that injects
`trafficSources`/`EgressPeers` without either will synthesize a policy that selects nothing.

## Parsing

| Function | Purpose |
|----------|---------|
| `Parse` / `ParseMulti` / `MustParse` | Parse one / many Application documents. |
| `ParsePackage` | Parse a `Package` (app + parameter schema). |
| `ParseClusterProfile` | Parse a `ClusterProfile`. |
| `LoadCapabilityDefinitions` | Load `CapabilityDefinition`s for capability validation. |
| `ParseWithExtraTraitTypes` | Parse allowing additional (custom) trait types. |
| `ParseWithExtraTypes` | Parse allowing custom trait types **and** a `LowerableTypes` set — the document kinds, component types and trait types claimed by a transformer's registered lowering rules. |

Standalone parsing validates each trait's `type` against the built-in handler set
(the `security-context` trait is included, matching `SecurityContextHandler`);
`ParseWithExtraTraitTypes` widens that allowlist with caller-supplied custom types.
`ParseWithExtraTypes` widens it further with `Transformer.LowerableTypes()`, so a
document authored in types that only a lowering rule understands parses ahead of the
transform that will lower them away. The widening is additive and per position: an
empty `LowerableTypes` is exactly the strict behaviour, a name claimed for one position
never admits it at another, and decoding stays strict (`KnownFields(true)`), so a kind
carrying its own authored fields still belongs to the raw lowering entry point.

## Transform & extension

`NewTransformer(...)` builds a transformer from maps of component/trait handlers;
`pkg/cmd/kurel` registers the built-ins. Extend the system by implementing:

| Interface | Role |
|-----------|------|
| `ComponentHandler` | `CanHandle(type)` + `ToApplicationConfig(...)` — see [components](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components). |
| `TraitHandler` | `CanHandle(type)` + `Apply(...)` — see [traits](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits). |
| `PolicyHandler` | Enforce/validate policies (`Enforceable`, `PolicyResult`). |
| `CapabilityAware` | Mark a handler as requiring a `ClusterProfile` capability. |
| `PropertySchemaProvider` | Declare a `PropertySchema` for the handler's user-facing properties (see below). |
| `ContractDescriber` | Declare `ContractMetadata` — contract family, version, required capability keys, deprecation info (see below). |
| `SourceDeduplicatable` | Collapse duplicate sources (e.g. shared OCI/Helm repos). |
| `ComponentNamed` | Expose the owning OAM component (`ComponentName() string`) on a trait/component sub-app config, so consumers can attribute each emitted resource to its component without re-deriving it from sub-app names. |
| `LayoutAugmentationCoverage` | `GenerateCoversAugmentLayout() bool` — for a config that also implements kure's `layout.LayoutAugmenter`, declare whether `Generate` alone already produces every resource `AugmentLayout` places into the layout. `kurel build` (which never walks a `layout.ManifestLayout`) uses this to fail closed: an augmenter that doesn't implement this interface, or that implements it and returns `false`, is rejected outright rather than silently dropping layout-level resources from the output. |

`PolicyResult.ConsumedCapabilities` is the sorted, deduped set of capability keys this
app's traits actually resolved against `ctx.Capabilities` during the transform — a real
`ClusterProfile` match, not every syntactically possible key a trait could name. Populated
only by `TransformWithPolicy` (nil on the plain `Transform` path, which discards
`PolicyResult` entirely); a downstream consumer that needs this signal must call
`TransformWithPolicy`. It replaces a downstream consumer's own interim, purely syntactic
candidate-key derivation with the authoritative one launcher's own resolution already
computes internally.

## Lowering

Some documents, components, traits, and policies are authored in a higher-level
vocabulary that has no direct dispatchable handler — a type a platform wants expanded
into one or more terminal types before the transform's own component/trait dispatch
runs. The lowering engine (`lowering.go`, `lowering_raw.go`) is the shared fixpoint
that performs that expansion, reachable from two entry points:

| Entry point | Reachable rules | Use when |
|---|---|---|
| `(*Transformer).lower`, invoked from `Transform`/`TransformWithPolicy` | `DocumentLoweringRule`, `ComponentLoweringRule`, `TraitLoweringRule`, `PolicyLoweringRule` | The authored document's field set already fits `ApplicationSpec` (`Components`/`Policies`, nothing else), so `ParseWithExtraTypes` can decode it before the transform lowers it away. |
| `(*Transformer).LowerRaws` — standalone, called by the caller BEFORE its own parse fan-out | Round 0: `RawDocumentLoweringRule` only. Round 1 onward: the SAME shared fixpoint as the in-transform path, so also `DocumentLoweringRule`, `ComponentLoweringRule`, `TraitLoweringRule`, `PolicyLoweringRule` — applied to whatever the raw seed's round-0 lowering emitted. | The authored document is a whole higher-level kind carrying its OWN fields that don't fit `ApplicationSpec`, so it can never survive a strict `ParseWithExtraTypes` decode. `LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error)` lowers only the inputs whose `kind` a registered `RawDocumentLoweringRule` claims (everything else passes through byte-identical), decoding each claimed input via that rule's own `DecodeDocument`, then runs the claimed inputs through the same round-1-onward fixpoint `Transform`/`TransformWithPolicy` use. Because of that shared fixpoint, a `CapabilityAware` rule reachable from a raw-entered document needs `ctx.Capabilities` populated (from an already-evaluated `ClusterProfile`) or it fails with `ErrMissingCapability`, exactly as it would from the in-transform path — evaluating the profile ahead of this call is the caller's responsibility, not `LowerRaws`'s. Before claiming a document, `LowerRaws` validates its `metadata.name` (required, DNS-1123 subdomain) and `metadata.namespace` (DNS-1123 subdomain, if set) itself — the in-transform path gets the same check from `ParseWithExtraTypes`, which a raw-entered document never goes through. |

`NameAllocator`'s generated-name collision detection (D2) is scoped by `Origin.Namespace`,
not by name alone: two documents authored in different namespaces may generate the same
child name without colliding, since they lower to namespace-disjoint resources — the same
reason two identically-named Kubernetes objects in different namespaces coexist. Two raw
inputs sharing both a name and a namespace are rejected earlier, as a duplicate authored
document, before either reaches the allocator.

Four registration interfaces, one per position in the document tree, each with its own
registrar on `*Transformer` and a duplicate/dispatchable-collision guard (a type
claimed by a lowering rule must not also be a dispatchable handler type, and a
document kind must not be claimed by both document registrars):

| Interface | Registrar | Position | May emit |
|---|---|---|---|
| `DocumentLoweringRule` | `RegisterDocumentLowering` | whole document (`ApplicationSpec`-shaped only) | `Documents` |
| `RawDocumentLoweringRule` | `RegisterRawDocumentLowering` | whole document (any shape; own `DecodeDocument`) | `Documents` |
| `ComponentLoweringRule` | `RegisterComponentLowering` | `spec.components[]` | `Components`, `Policies` |
| `TraitLoweringRule` | `RegisterTraitLowering` | `spec.components[].traits[]` | `Traits`, `Components`, `Policies` |
| `PolicyLoweringRule` | `RegisterPolicyLowering` | `spec.policies[]` | `Policies` |

Every rule returns a `LoweringResult`; an entirely empty result is rejected — a
registered rule that emits nothing is indistinguishable from deleting the authored
element, which is not permitted. `Transformer.LowerableTypes()` reports every
kind/component-type/trait-type/policy-type claimed by rules registered on a
transformer (excluding raw-only rules, which are reachable only via `LowerRaws`), for
a caller to pass into `ParseWithExtraTypes` ahead of a transform that will lower them
(see Parsing above).

Expansion runs to a **fixpoint**: every round, every current document's non-terminal
kind, components, traits, and policies are lowered once via their registered rule (if
any); the loop repeats until a round changes nothing, bounded by `MaxLoweringDepth`
(9) — a rule that keeps re-emitting its own (or another registered) type fails the
build with the full expansion chain rather than looping forever. A transformer with no
lowering rules registered anywhere returns the input `*Application` unchanged (the
same pointer — no copy, no allocation); registering rules only on the raw entry point
leaves that guarantee intact for the in-transform path.

Every emitted element carries an `Origin` — the AUTHORED location it descends from,
stamped once and copied verbatim onto every element expanded from it at any depth —
so a `LoweringError` always leads with the YAML the user actually wrote, then the
synthesized cause, then the expansion chain (`LoweringStep`s) that produced it.

`Origin.Rule` is the one field on `Origin` that is NOT copied verbatim: it identifies
the lowering rule that most recently produced the element — `"<position>/<type>"`
(e.g. `"trait/expose"`), suffixed with `"@<version>"` when the rule also implements
`ContractDescriber` (see Contract metadata below) — and is re-derived at every hop, so
it always names the immediate producer rather than the first rule in a multi-hop
chain. `""` means the element was never itself the direct output of a lowering rule
(authored as-is, or carried through untouched).

A trait-position rule that implements `CapabilityAware` is enforced by the engine
exactly as `applyTraits` enforces it for a dispatchable `TraitHandler`: missing the
required `ClusterProfile` capability fails with `ErrMissingCapability`. A rule that
also implements `PropertySchemaProvider` has an authored value for one of its
platform-reserved properties rejected before capability rendering is merged into the
trait it receives. Independently of that — every lowering rule, whether or not it
declares its own schema — has each element it emits (component, trait, or policy)
validated against the TARGET handler's declared schema before the emitted element is
accepted into the next round (see Property schemas below).

## Property schemas

Handlers may implement `PropertySchemaProvider` (`PropertySchema() map[string]PropertySchema`)
to declare a constrained schema for their user-facing properties. `PropertySchema` is launcher's
single schema vocabulary — the same type also backs `kurel.yaml` parameters (`ParameterDecl`) and
`CapabilityDefinition` rendering properties. It has `Type` (string/integer/boolean/number/array/object),
`Description`, `Required`, `Default`, `Enum`, nested `Properties`, `Items`, and `AdditionalProperties`
(default false; escape-hatch fields set it true). The rich fields (`Enum`, `Properties`, `Items`,
`AdditionalProperties`) are meaningful only for handler properties: the two flat call sites (kurel
parameters, capability rendering) reject them at decode time, so unifying the type does not widen
their accepted behavior. `Transformer.HandlerSchemas()` returns a `HandlerSchemaSet{ Components, Traits }`
of every registered handler that declares one, so the downstream runtime's validator can check a component/trait's
properties before the handler is invoked. Built-in examples: the `configmap` trait and the
`passthrough` component.

`Description` is optional (`json:"description,omitempty"`) but every built-in property populates it —
including nested object fields and array item schemas at every depth — so the downstream runtime can surface prose in
its generated Handler API Reference. A completeness test (`pkg/cmd/kurel`) enforces that no built-in
schema node is left without a description.

A schema field may also be marked `PlatformReserved`: its value may arrive only via
`ClusterProfile` capability rendering, never authored inline. `enforcePlatformReserved`
(`property_validate.go`) rejects an authored value for such a field — including an
explicit `null` — before capability rendering is merged in, wrapping
`ErrPlatformReserved`; it walks declared nested object fields too, so a reservation on
an inner field is enforced wherever it is declared, not only at the top level.
`createApplications` and `applyTraits` (`transform.go`) run this check on the authored
path, and the lowering engine runs it on a `TraitLoweringRule`'s input trait before
capability rendering is resolved into it (see Lowering above).

Separately, `validateProperties` (`property_validate.go`) checks an EMITTED
component/trait/policy's properties against its TARGET handler's declared schema —
enforcing `Required`, `Type`, `Enum`, and nested `Properties`/`Items`/
`AdditionalProperties` — immediately after a lowering rule returns it, so a rule
cannot silently produce properties its own target handler would reject. This is
in-process enforcement of what `HandlerSchemas()` only publishes for authored
documents; an authored document's own property shape is still validated only by
`ValidateAndApplyDefaults`/handler-specific logic, not by this path.

`validateProperties`'s null check (`isNullValue`) treats a typed-nil pointer,
slice, or map — not just a bare `nil` interface — as JSON `null`: a Go type
assertion alone can't tell an uninitialized slice/map apart from a validly-typed
empty collection, even though both serialize the same way, so a lowering rule
that emits an unset (rather than empty) collection field is still caught.

## Contract metadata

Handlers and lowering rules may implement `ContractDescriber` (`ContractMetadata()
ContractMetadata`) to declare registration metadata about the contract they
implement: `Family`, `Version`, `RequiredCapabilityKeys` (the `ClusterProfile`
capability keys an entity of this contract needs), `Deprecated`, and
`DeprecationMessage`. It is primarily a discovery/documentation surface — the engine
does not enforce any of these fields (`CapabilityAware.CapabilityRequired` is what the
engine actually enforces). The one exception is `Version`: a lowering rule's `Version`
is read to compose the `"@<version>"` suffix on `Origin.Rule` (see below); nothing
else here is read or enforced. Consumers otherwise: schema publication, artifact
provenance in a downstream consumer, and deprecation tooling. Metadata rides the
existing registration mechanism; there is no separate contract registry.

`Transformer.HandlerContracts()` returns a `HandlerContractSet{ Components, Traits }`
of every registered component/trait handler, and every component/trait lowering
rule, that implements `ContractDescriber` — the same four-registry coverage
`HandlerSchemas()` provides (componentHandlers, traitHandlers,
componentLoweringRules, traitLoweringRules), for the identical reason: a type
reachable only through a lowering rule must still publish its metadata.

A lowering rule that implements `ContractDescriber` also has its `Version` folded
into the lowering-rule identity recorded on `Origin.Rule` (see Lowering above), e.g.
`"trait/expose@v1"`.

## Policy defaults & enforcement

`Policy` is a typed accessor interface (no type assertions in handlers) that carries
per-environment **enforced limits** (`MaxReplicas`, `MaxCPU`, `MaxMemory`, `MaxStorageSize`,
`AllowedRegistries`), **defaults** (`DefaultReplicas`, the CPU/memory request/limit defaults,
and the workload-shape defaults `DefaultStorageSize`, `DefaultScalerMinReplicas`,
`DefaultScalerMaxReplicas`), security flags, and two distinct capability-constraint families:
`AllowedCapabilities`/`ForbiddenCapabilities`/`RequiredCapabilities` gate **OAM trait-type**
strings (e.g. "ingress", "autoscaling"), while `AllowedContainerCapabilities`/
`ForbiddenContainerCapabilities` gate Linux capabilities on a container's
`securityContext.capabilities.add` (e.g. "NET_ADMIN"). Both families share the same nil/empty
constraint-list convention under `NoopPolicy` — no `Allowed`/`Forbidden`/`Required` entries means
unconstrained — but only the boolean security flags (`AllowPrivileged`, `AllowHostPathVolumes`,
etc.) are default-deny; a container capability appearing in both an explicit `Allowed` list and
the `Forbidden` list is rejected, since forbidden always wins, and a nil/empty
`Allowed`/`Forbidden` list means no restriction/no forbids respectively. Handlers that implement
`Enforceable` receive it via `ApplyPolicy`; `NoopPolicy` supplies zero values when no policy is
set (so `ApplyPolicy` is always called with a non-nil value at runtime).

Handlers apply values with the precedence **authored > policy default > handler default**,
then enforce the limits on the resulting effective value — for cpu/memory this explicitly
includes the `pkg/oam/builtin/components` intrinsic handler-default tier (100m CPU / 128Mi
memory request, applied by `buildResourceRequirements` at `Generate()` time, after
`ApplyPolicy` runs), not just the authored/policy-defaulted value `ApplyPolicy` sees; see
that package's README, "Policy defaults & enforcement ordering".

For example the `scaler` trait fills
`minReplicas`/`maxReplicas` from the scaler defaults when omitted (erroring if neither the trait
nor a policy default supplies them), and the `pvc`/`postgresql` handlers default the storage
size from `DefaultStorageSize`. See the Policy Interface design note under the Concepts
section for the full accessor list and rationale.

## Capability system

Capability-aware traits (e.g. `expose`, `certificate`, `external-secret`) declare
required platform inputs; the `ClusterProfile` provides them, and
`CapabilityDefinition` rendering/property schemas validate custom capabilities
(`--strict-capabilities` turns warnings into errors).

This is a large internal builder surface; the tables above cover the entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the full
type reference, the design notes under the Concepts section, and `examples/` for
runnable applications.
