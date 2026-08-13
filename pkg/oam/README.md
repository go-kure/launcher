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

## Lowering engine (spike, `spike/oam-lowering-engine`, branch-only)

`lowering.go` adds an optional, opt-in recursive expansion pass ahead of dispatch:
`RegisterDocumentLowering`/`RegisterComponentLowering`/`RegisterTraitLowering`/
`RegisterPolicyLowering` let a higher-level document kind, component type, trait
type, or policy type lower into one or more lower-level elements, to a fixpoint
(`MaxLoweringDepth`), before the ordinary handler dispatch above ever runs. With no
lowering rules registered — true for every production `Transformer` today — `Transform`/
`TransformWithPolicy`/`TransformAll` behave exactly as before; `TransformAll` is new
because a document can now expand 1→N (`Transform`/`TransformWithPolicy` error if it
does). This validates a design recorded in `autops/wharf/adr` research
(`research/abstraction-model/oam-levels-design-decisions.md`, "OAM levels", adr#34) —
the branch is never merged; only what survives design review lands as a real API later.

D4 ("always re-validate emitted elements against their target schema, as if
user-authored") is enforced at two points: `validateProperties` checks each emitted
component/trait/policy's `Properties` against its target handler's `PropertySchema()`
the moment a lowering rule emits it (an unknown key, a missing required field, a
type/enum mismatch all fail immediately, citing the **authored** origin first); once
the fixpoint stops changing anything, every resulting document is re-validated with
an empty `LowerableTypes` — any kind/component/trait type still present at that point
is, by construction, not claimed by any registered rule, so it is a non-terminating
rule's leftover rather than a legitimate terminal type.

`pkg/oam/builtin/lowering` (toy rules, never registered by the CLI) and
`pkg/oam/builtin/policies` (a terminal "dependency" `PolicyHandler` — the first
policy handler in the repo) exercise all four positions end to end: a
`WebApplicationRule` document rule (1→2 documents), a `WebAndCacheRule` component
rule (1→2 components + 1 emitted policy), and an `OrderedRule` policy rule (1→N
terminal "dependency" policies). `pkg/cmd/kurel/toy_webapplication_test.go` drives
the full chain through the real pipeline (`testdata/toy-webapplication/`, marked
`SPIKE_ONLY` so the generic `TestFixtures` loop skips it) and asserts the resulting
`stack.Bundle.DependsOn` edge, not just the golden manifests.

`pkg/oam/builtin/traits.ExposeRule` re-expresses the built-in `expose` trait as a
`TraitLoweringRule` (D5 worked example, not a toy): it is no longer a dispatchable
`TraitHandler`, so `applyTraits`' capability-merge/dispatch path never sees it.
Instead the engine merges the `expose` capability's rendering into the trait
*before* calling `LowerTrait` (mirroring what `applyTraits` used to do for
`ExposeHandler.Apply`), and `ExposeRule.LowerTrait` emits a terminal `ingress` or
`httproute` trait for the fixpoint's next round to dispatch on normally. Two
consequences follow directly from D5's closed-input rule: emitted traits are
marked `sealed`, so `applyTraits` skips capability resolution for them entirely —
without this, a component that also had an unrelated `ingress`/`httproute`
capability defined would leak a second, uninvited merge into the emitted trait,
a fifth input D5 forbids. And because `ExposeRule` is never in `traitHandlers`,
`EvaluateProfile` falls back to the trait-lowering-rule registry so the `expose`
capability's `ValidateAndApplyDefaults` (the `gatewayName`/`gatewayNamespace`
checks) still runs — otherwise those checks would silently stop firing with no
failing test to catch it (see `TestExposeRule_EvaluateProfile_GatewayValidation`
and `TestExposeRule_SealedGuard_ExtraIngressCapabilityIgnored`).

`PropertySchema.PlatformReserved` (D3) marks a property as platform-supplied only:
`enforcePlatformReserved` rejects an authored value for such a key **before**
capability rendering is merged in, at every position that performs that merge
(the trait-lowering branch above, `applyTraits`) plus symmetrically before a
component handler's `ToApplicationConfig` (which performs no such merge today,
but the guard is position-agnostic). `ExposeRule.PropertySchema()` marks
`controllerType`, `certManagerClusterIssuer`, `allowedHostnameWildcard`, `authURL`,
and `authResponseHeaders` reserved — each is capability-injected-only in
`ExposeRule.LowerTrait` and never reaches the emitted `ingress`/`httproute` trait.
`networkPolicy` is reserved on `ExposeRule`'s own schema copy only, **not** on the
`schemaNetworkPolicy()` fragment `IngressHandler`/`HTTPRouteHandler` also share —
those two traits' own test suites author `networkPolicy` directly (bypassing any
`ClusterProfile`) to exercise netpol synthesis without a capability round-trip, a
pre-existing, intentional pattern D3 does not reach. `sslRedirect`,
`forceSslRedirect`, `authSigninURL`, and `ingressClassName` stay **not** reserved:
each is documented as capability-default-with-inline-override, or (for
`ingressClassName` on the plain `ingress` trait) authored directly by existing
fixtures.

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
| `ParseWithExtraTypes` (spike) | Parse allowing custom trait types **and** a `LowerableTypes` set — kinds/component types/trait types claimed by a registered lowering rule (see "Lowering engine" above). |

Standalone parsing validates each trait's `type` against the built-in handler set
(the `security-context` trait is included, matching `SecurityContextHandler`);
`ParseWithExtraTraitTypes` widens that allowlist with caller-supplied custom types.
`ParseWithExtraTypes` widens it further with `Transformer.LowerableTypes()`, so a
document a lowering rule will later expand parses today instead of failing validation
before the fixpoint ever runs.

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
| `SourceDeduplicatable` | Collapse duplicate sources (e.g. shared OCI/Helm repos). |
| `ComponentNamed` | Expose the owning OAM component (`ComponentName() string`) on a trait/component sub-app config, so consumers can attribute each emitted resource to its component without re-deriving it from sub-app names. |

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

## Policy defaults & enforcement

`Policy` is a typed accessor interface (no type assertions in handlers) that carries
per-environment **enforced limits** (`MaxReplicas`, `MaxCPU`, `MaxMemory`, `MaxStorageSize`,
`AllowedRegistries`), **defaults** (`DefaultReplicas`, the CPU/memory request/limit defaults,
and the workload-shape defaults `DefaultStorageSize`, `DefaultScalerMinReplicas`,
`DefaultScalerMaxReplicas`), security flags, and capability constraints. Handlers that
implement `Enforceable` receive it via `ApplyPolicy`; `NoopPolicy` supplies zero values when
no policy is set (so `ApplyPolicy` is always called with a non-nil value at runtime).

Handlers apply values with the precedence **authored > policy default > handler default**,
then enforce the limits on the resulting effective value. For example the `scaler` trait fills
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
