# OAM Model, Parser & Transformer

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)

Package `oam` is launcher's core: the OAM data model, YAML parser, semantic
validator, and the transform pipeline that turns an `Application` + `ClusterProfile`
into Kubernetes manifests. Every document this package parses uses
`apiVersion: launcher.gokure.dev/v1alpha1` (`SupportedAPIVersion`); the one exception is
the raw-document lowering seam, where a `RawDocumentLoweringRule` may claim a kind under a
consumer-owned group via `RawDocumentAPIVersioner` (see "Lowering entry points" below).

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

The transform groups components into bundles in one of three shapes: one bundle for a
single-tier application; one bundle per tier under an umbrella, each depending on the
populated tier before it (`infra`, `services`, `apps`); or, once any `dependency` policy
rule exists, one bundle per component, wired with the rules plus edges to the preceding
tier's bundles. A Flux source shared by several components (`SourceDeduplicatable`) is
emitted once, by the sharing component that comes first in that deployment order (tier,
then dependencies, then document order), so its owner never waits on another consumer.

The tier umbrella bundle is named after the Application and leaves `Wait` unset: kure gives
its Kustomization one health check per child Kustomization, and Flux ignores health checks
when `wait` is enabled, so the umbrella is Ready only when every child Kustomization is.

Within a bundle, each component's application is followed by the sub-applications its traits
created, in creation order, so a trait's objects are emitted with their own component's rather
than after every component of the bundle (go-kure/launcher#712). A trait handler that moves
an application or removes a sub-application keeps the order it left (go-kure/launcher#718); one
that replaces, removes or renames a component's application fails the transform (go-kure/launcher#734,
see `TraitHandler` below). A trait whose handler
implements `SubApplicationDecorator` (the built-in `prune-protection` and `force-replace`) also
decorates those sub-applications, whatever order the traits were authored in: the last step of
the transform, after the Phase-4 synthesis below, applies it to each of them. The NetworkPolicies
that synthesis adds are no component's sub-applications and stay undecorated.

Under `TransformContext.FluxNamespace`, every config that takes it (`SetFluxNamespace`: the
`helmrelease`, `oci` and Flux source kinds) moves its Flux objects there, and a trait
sub-application of that component follows only when the Flux object reads it by name from its own
namespace (go-kure/launcher#740). The config reports what it reads (`FluxNamespaceReads`: a
HelmRelease's `valuesFrom`, `kubeConfig` and chart-template `verify` Secret, a source's
`secretRef`, `certSecretRef`, `proxySecretRef` and the Secrets under `verify` and `sts`); a
sub-application config names the ConfigMap or Secret it produces (`FluxNamespaceInput`: the
`configmap` trait's ConfigMap, the Secret an `external-secret` trait's ExternalSecret writes). Both
kind and name must match. Every other trait object — a ConfigMap or Secret the Flux object does not
name, a Certificate, a claim, a NetworkPolicy, a route — stays in the application namespace with the
workloads, where a HelmRelease installs them (`targetNamespace`). A ConfigMap or Secret the Flux
object names moves even when the chart's pods read it as well: the Flux object cannot reconcile
without it, and a reference through the chart's values is invisible here. Every built-in config
that takes the Flux namespace reports its reads, possibly none (`oci`); decorators and sibling
groups forward them.

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
  valid rule is not emitted. A component's endpoints are declared by its handler, or by
  the `ComponentLoweringRule` claiming its type, via the optional `EndpointProvider` interface and read through `Transformer.ComponentEndpoints` — the
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

The selector **value** is `ComponentLabelValue(name)`, not the raw component name, and a
platform stamping the label must use the same function. A component name is a DNS-1123
subdomain (up to 253 characters), a label value at most 63: `ComponentLabelValue` returns a
name of 63 characters or fewer unchanged and projects a longer one onto a readable prefix of at
most 52 characters (its first 52, with trailing `-`/`.` trimmed) plus `-` and the first 10 hex
characters of its sha256. The projection is deterministic, so every component-identity label and
selector that uses it gets the same value for one component. The `app` label and the `app`
selectors the built-in components and traits generate to identify a component use the same
function (go-kure/launcher#572); authored
labels and selectors (a `service` component's `selector`, a `networkpolicy` peer) are emitted as
written, and a type that emits authored objects (`passthrough`, for one) adds no `app` label.
Selectors under an operator's own label and naming contract — the `postgresql` endpoints'
`cnpg.io/cluster` and `cnpg.io/poolerName` — carry the operator's values and are not
component-identity labels.
It is a projection rather than a refusal because the label is an identifier, not the object's
name: several component types (`manifests`, `oci`,
`crd`, `passthrough`) and their traits accept a name over 63 characters. Object names are never
projected.
Because a projected value is itself a valid component name, validation rejects an Application
in which two components share a `ComponentLabelValue` — a component named exactly like another
component's projection, or two long names whose prefix and digest both coincide — since their
`app` labels and every selector built from them would match both components' pods.

One exception on the inbound side: a component whose config reports a routing target — the
`service` kind, whose Service fronts pods another component owns — gets its
`{comp}-allow-ingress-traffic` policy on the Service's `selector` pods instead of the component
label, with each routed Service port translated to its `targetPort`. A routed port that is not
one of the Service's TCP ports is dropped (the rules are TCP), and a route left with no port
synthesizes no policy. A sibling group whose `service` member fronts its own sibling's pods on
unmapped ports keeps the component label, with the ports still translated and filtered the same
way (see Same-name sibling groups below).

Every synthesized `NetworkPolicy` carries **no labels and no annotations of its own** —
only `metadata.name` and `metadata.namespace`, plus the spec. A consumer cannot select
the synthesized set by label; identify it by the `{comp}-allow-*` name. No attribution
interface is available either: these configs expose their component as a struct field
(`ComponentName string`), which is exactly what makes a `ComponentName()` method on the
same type impossible, and the external-backend policy config carries no component at all —
so none of them satisfies `ComponentNamed`. Since go-kure/launcher#361 the label absence is a
property of the object as constructed rather than a scrub: kure's release-1 builder
contract (`go-kure/kure` ≥ `v0.2.0-beta.11`) makes `CreateNetworkPolicy` return TypeMeta
and identity only, so `netpol_synthesis.go`'s `np.Labels = nil` / `np.Annotations = nil`
lines no longer strip a constructor-stamped `app:` label and annotation — they are kept
as a guard, not as a fix-up. Everything else on the policy (`spec.podSelector`,
`spec.policyTypes`, the rules) is written by the synthesizer as a direct field
assignment; nothing upstream of it supplies a default.

No synthesized policy shares a selector with its inputs or with another policy. Every
peer's `podSelector` and every policy's `spec.podSelector` is a `DeepCopy` of the traffic
source, egress peer, backend or endpoint selector it came from. The inputs are retained
trait configuration, reused by every rule and every policy built from them, so a label a
consumer stamps onto one generated policy would otherwise reach all of them
(go-kure/launcher#396).

## Parsing

| Function | Purpose |
|----------|---------|
| `Parse` / `ParseMulti` / `MustParse` | Parse one / many Application documents. |
| `ParsePackage` | Parse a `Package` (app + parameter schema). |
| `ParseClusterProfile` | Parse a `ClusterProfile`. |
| `LoadCapabilityDefinitions` | Load `CapabilityDefinition`s for capability validation. |
| `ParseWithExtraTraitTypes` | Parse allowing additional (custom) trait types. |
| `ParseWithExtraTypes` | Parse allowing custom trait types **and** a `LowerableTypes` set — the document kinds, component types and trait types claimed by a transformer's registered lowering rules. |

Standalone parsing validates each trait's `type` against this package's own allowlist
of built-in trait types (the `security-context`, `topology-spread` and `force-replace`
traits are included, matching `SecurityContextHandler`, `TopologySpreadHandler` and
`ForceReplaceHandler`);
`ParseWithExtraTraitTypes` widens that allowlist with caller-supplied custom types.
`ParseWithExtraTypes` widens it further with `Transformer.LowerableTypes()`, so a
document authored in types that only a lowering rule understands parses ahead of the
transform that will lower them away. The widening is additive and per position: an
empty `LowerableTypes` is exactly the strict behaviour, a name claimed for one position
never admits it at another, and decoding stays strict (`KnownFields(true)`), so a kind
carrying its own authored fields still belongs to the raw lowering entry point.

**Every namespace is a DNS-1123 label** (at most 63 characters, no dots), the rule the
apiserver applies. Parsing refuses any other `metadata.namespace`, `LowerRaws` refuses it
on a raw input, and `Transform` refuses a `TransformContext.Namespace` (the kurel
`--namespace` override) or `TransformContext.FluxNamespace` that breaks it, before
lowering. The message names the value, e.g.
`namespace "team.prod" is not a valid DNS-1123 label (…)`.

**Component `type` is validated against this package's own allowlist, not against
the caller's handler registry.** The two are independent lists that happen to agree:
parsing rejects an unknown component type before any handler is consulted, so
registering a `ComponentHandler` in `pkg/cmd/kurel` does *not* by itself make its type
name authorable — the name must also be added here. A type registered on one side only
is registered-but-unusable (every document naming it fails to parse) or
parseable-but-undispatchable, and in both cases a handler-level test suite stays green.
`pkg/cmd/kurel`'s `TestBuiltinComponentHandlers_AcceptedByParser` is the guard: it
parses a minimal document for every registered built-in type through
`ParseWithExtraTypes`, the same entry point `kurel build` uses. Two other per-type
registries have the same shape and the same failure mode — `traitComponentRestrictions`
(which traits a component type accepts; today only `scaler` is restricted, to `webservice`,
`worker` and `deployment`, the kinds that report a non-RWX claim to it) and `componentHealthCheckGVK` (the workload GVK
a component type's auto health check targets; an unlisted type is skipped silently, so
its bundle simply carries one health check fewer). The auto check is attached only
to the component's own application, matched by identity: a trait sub-application
that shares another component's name (a `pvc` trait's claim `<component>-<volume>`)
never takes that component's check (go-kure/launcher#702).

**Membership in `componentHealthCheckGVK` follows what kstatus can actually read, not
whether the workload has a steady ready state.** `job` is listed: kstatus's `jobConditions`
maps a Job's `Complete` condition to `CurrentStatus` and `Failed` to failed, so a Job is a
terminal signal Flux can wait on even though it never becomes `Ready`. Flux accommodates the
one shape that looked like a counter-argument — a Job deleted by `ttlSecondsAfterFinished`
before the wait finishes — by extracting TTL-bearing Jobs up front and passing them as
`JobsWithTTL` to its wait options.

`cronjob` stays absent, and for a reason that does not generalise to `job`: a CronJob owns
no pods between schedules and carries no condition that ever reports completion, so there is
nothing for a health check to read. The other unlisted types — `passthrough`, `crd`,
`manifests` and `helmtemplate` — are absent for a third reason: they emit whatever the document
carries, or for `helmtemplate` whatever the chart it renders client-side carries (and no
HelmRelease), so there is no single GVK to name. When adding a component type, decide which
group it falls in and say so; silence here reads the same either way. `helmtemplate`
(go-kure/launcher#348) sits in `defaultTierMap` at `TierApps`, like `helmrelease`.
`cnpg-cluster` is listed: it emits one CloudNativePG `Cluster`, the same object
`postgresql`'s check already targets, so it gets the same check (and, in `defaultTierMap`,
the same `services` tier). `cnpg-pooler`, `cnpg-database` and `cnpg-objectstore`
(go-kure/launcher#573) are absent for a fourth reason: their `Pooler`, `Database` and
`ObjectStore` statuses carry no condition kstatus reads, so a check would report them ready
without waiting on anything — `postgresql`, which emits the same kinds, checks only its
`Cluster`. They sit in the `services` tier with `cnpg-cluster`. `serviceaccount`,
`persistentvolumeclaim` and `configmap` (go-kure/launcher#702) are absent as well.
kstatus reports a ServiceAccount or a ConfigMap current as soon as it exists, so a check
would wait on nothing. A claim whose class binds on first consumer stays `Pending` until a
pod mounts it, so a check would hold the tier on a claim nothing mounts yet. The workload
that mounts it already carries the check that matters. Like `deployment` and `service`, the
three are not in `defaultTierMap` and fall back to `TierApps`.

`helmrelease` (go-kure/launcher#327) is listed, with the `helm.toolkit.fluxcd.io/v2`
`HelmRelease` GVK: it always emits exactly one HelmRelease, whose Ready
condition kstatus reads directly. Because the GVK is a `*.toolkit.fluxcd.io` kind and its
config accepts a Flux namespace, the check moves to that namespace with the object. It sits
in `defaultTierMap` at `TierApps`. It declines its check for
`suspend: true` (below).

The kind-named Flux source components `helmrepository`, `ocirepository`, `gitrepository` and
`bucket` (go-kure/launcher#347) are listed, each with its own `source.toolkit.fluxcd.io/v1`
GVK: each emits exactly one source CR, whose Ready condition kstatus reads, so a dependent
Kustomization waits until the source is ready. Because the GVK is a `*.toolkit.fluxcd.io` kind
and each config accepts a Flux namespace, the check moves to that namespace with the object.
They sit in `defaultTierMap` at `TierApps`, like `oci` and `helmrelease`.

`helmchart` (go-kure/launcher#351), the kind-named component for Flux's `HelmChart`, is listed
the same way, with the `source.toolkit.fluxcd.io/v1` `HelmChart` GVK, and sits at `TierApps`.
kstatus reads a HelmChart exactly as it reads a Bucket. Neither kind has a kind-specific rule:
`legacyTypes` lists only core kinds (fluxcd/cli-utils v1.2.3 `pkg/kstatus/status/core.go:22-39`,
looked up by `GetLegacyConditionsFn`, `:57-65`). Both are read by the generic rules
(`generic.go:22`, `checkGenericProperties`): `status.observedGeneration` against
`metadata.generation` (`:82-95`), then a true `Reconciling` or `Stalled` condition (`:51`,
`:54`). Both statuses carry those two fields (source-controller api v1.9.5
`helmchart_types.go:123` and `:143`, `bucket_types.go:198` and `:202`). The helm rule never
emits a HelmChart, so a `helmchart` is always authored and keeps `defaultTierMap`'s tier.

The exception is a `helmrepository`, `ocirepository`, `gitrepository` or `bucket` that a
lowering rule emitted (`Component.synthesized`): `ClassifyComponentWithDomain` places it in
`TierInfra`, after
any tier annotation and before `defaultTierMap`. The `helm` rule (go-kure/launcher#349)
emits such a source for the releases that read it. Those releases keep their own tier, and
a tier annotation or a `placement` policy may move them into `infra`. A source in a later
tier than its consumer would never be applied, because each tier waits on the health checks
of the tier before it, including the consumer's. In the earliest tier, the source never
follows a consumer, and a consumer that shares its tier is retried by helm-controller until
the source is ready. A `placement` policy naming the generated source may only keep it in
`infra`, and a `dependency` rule may not make it wait on any component (with its consumer
placed in `infra` beside it, no cycle would report the deadlock); `TransformWithPolicy`
refuses both. An authored source keeps
`defaultTierMap`'s tier.

A listed type can still decline its check per document by implementing
`EmitsAutoHealthCheck() bool`. `job` uses it for `suspend: true` — a suspended Job creates no
pods, so it reaches neither `Complete` nor `Failed` and the wait would block for exactly as
long as the document asks it to stay suspended. This is the same shape as `deployment`'s veto
for `paused: true`: the document instructs the workload not to progress, so waiting on it is
not a health signal but a guaranteed timeout. `helmrelease` declines it for `suspend: true`
too: helm-controller does not reconcile a suspended HelmRelease, and the Ready condition the
check reads is written by a reconciliation, so a newly created suspended release never acquires
one. The HelmRelease is still emitted; only the check is skipped. The five Flux source components veto for their
own `suspend: true` for the same reason — the document tells source-controller not to reconcile
— and `helmrepository` also vetoes for `type: oci`, which Flux treats as a static object with
no artifact, so there is no reconcile to wait on.

## Transform & extension

`NewTransformer(...)` builds a transformer from maps of component/trait handlers, and
`RegisterPolicy(type, handler)` adds an application policy handler; `pkg/cmd/kurel` registers
the built-ins. Extend the system by implementing:

| Interface | Role |
|-----------|------|
| `ComponentHandler` | `CanHandle(type)` + `ToApplicationConfig(...)` — see [components](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components). |
| `TraitHandler` | `CanHandle(type)` + `Apply(...)` — see [traits](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits). `Apply` mutates the application it is given and may append sub-applications to the bundle. It must not replace, remove or rename a component's application there: the transform fails, naming the trait and the component, because the automatic health check and NetworkPolicy synthesis find a component's application by its name and then its pointer, and a replaced, removed or renamed one would silently get neither (go-kure/launcher#734). |
| `PolicyHandler` | `CanHandle(type)` + `Apply(policy, components, result)` — validates one `spec.policies` entry and records its effect (tier overrides, dependency edges, extra health checks, reconciliation settings) on the shared `PolicyResult`; see [policies](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/policies). A policy type with no registered handler fails the transform. |
| `CapabilityAware` | Mark a handler as requiring a `ClusterProfile` capability. |
| `PropertySchemaProvider` | Declare a `PropertySchema` for the handler's user-facing properties (see below). |
| `ContractDescriber` | Declare `ContractMetadata` — contract family, version, required capability keys, deprecation info (see below). |
| `SourceDeduplicatable` | Collapse duplicate sources (e.g. shared OCI/Helm repos). |
| `ComponentNamed` | Expose the owning OAM component (`ComponentName() string`) on a trait/component sub-app config, so consumers can attribute each emitted resource to its component without re-deriving it from sub-app names. The value is the raw component name; a consumer writing it into a label or selector passes it through `ComponentLabelValue` first. |
| `SubApplicationDecorator` | `DecoratesSubApplications() bool` — on a `TraitHandler` whose `Apply` decorates an application's objects. When it returns `true`, the engine also calls `Apply` on every sub-application the component's traits appended to the bundle, as the last step of the transform, so trait order does not matter; a trait forwarded to several sibling-group members decorates the group's sub-applications once. `Apply` must not add, remove, replace, rename or reorder the bundle's applications there (the transform fails). Implemented by `prune-protection` and `force-replace`. |
| `ServiceAccountNamer` | `ServiceAccountName() (name string, runsPods bool)` — the ServiceAccount a workload component's pods run as: the authored `serviceAccountName`, or `""` when none is authored (no pod kind generates an account, go-kure/launcher#702; a `webservice`/`worker` hands its `deployment` member the name of the account it generates). `runsPods` reports whether the config runs pods at all; a trait decorator or sibling group that wraps no pod-running config reports `false`. Traits that bind identity to the workload (the `rbac` trait's binding subject) read this instead of assuming the component name, and `rbac` refuses a pod-running component with no name. Implemented by every built-in pod kind config. **Breaking library change**: the method gained the `runsPods` result. |
| `LayoutAugmentationCoverage` | `GenerateCoversAugmentLayout() bool` — for a config that also implements kure's `layout.LayoutAugmenter`, declare whether `Generate` alone already produces every resource `AugmentLayout` places into the layout. `kurel build` (which never walks a `layout.ManifestLayout`) uses this to fail closed: an augmenter that doesn't implement this interface, or that implements it and returns `false`, is rejected outright rather than silently dropping layout-level resources from the output. |

`PolicyResult.ConsumedCapabilities` is the sorted, deduped set of capability keys this
app's traits actually resolved against `ctx.Capabilities` during the transform — a real
`ClusterProfile` match, not every syntactically possible key a trait could name — plus
every key a component, trait, document or policy lowering rule reads through
`LoweringContext.Capability` (go-kure/launcher#686). A read by a
`RawDocumentLoweringRule` under `LowerRaws` is not among them: `LowerRaws` returns no
`PolicyResult`, and the capabilities the rewritten document's traits resolve against are
recorded when `TransformWithPolicy` runs on it. Populated
only by `TransformWithPolicy` (nil on the plain `Transform` path, which discards
`PolicyResult` entirely); a downstream consumer that needs this signal must call
`TransformWithPolicy`. It replaces a downstream consumer's own interim, purely syntactic
candidate-key derivation with the authoritative one launcher's own resolution already
computes internally.

A lowering rule reads a capability only through `lctx.Capability(key)`, which returns
the binding and whether the profile has one, and records a key it finds. The
`LoweringContext.Capabilities` map field is gone. A downstream rule author migrates
`lctx.Capabilities[k]` to `binding, ok := lctx.Capability(k)`. A test driver that calls
a rule directly, and used to set the field, uses
`lctx.WithCapabilities(m)` instead; its reads are recorded nowhere.

## Lowering

Some documents, components, traits, and policies are authored in a higher-level
vocabulary that has no direct dispatchable handler — a type a platform wants expanded
into one or more terminal types before the transform's own component/trait dispatch
runs. Where a component lowering rule places each object it emits (a same-name
sibling member, a trait on one member, or a separately named kind component), and
how a trait builds the same object as its kind twin, is set out in
`docs/oam/design-lowering-engine.md`, section "Where a lowering rule places each
object".
The lowering engine (`lowering.go`, `lowering_raw.go`) is the shared fixpoint
that performs that expansion, reachable from two entry points:

| Entry point | Reachable rules | Use when |
|---|---|---|
| `(*Transformer).lower`, invoked from `Transform`/`TransformWithPolicy` | `DocumentLoweringRule`, `ComponentLoweringRule`, `TraitLoweringRule`, `PolicyLoweringRule` | The authored document's field set already fits `ApplicationSpec` (`Components`/`Policies`, nothing else), so `ParseWithExtraTypes` can decode it before the transform lowers it away. |
| `(*Transformer).LowerRaws` — standalone, called by the caller BEFORE its own parse fan-out | `RawDocumentLoweringRule` only, for round 0 only. Nothing it emits is sealed; every other rule runs later, in `Transform`. | The authored document is a whole higher-level kind carrying its OWN fields that don't fit `ApplicationSpec`, so it can never survive a strict `ParseWithExtraTypes` decode. `LowerRaws(raws []json.RawMessage, ctx TransformContext) ([]json.RawMessage, error)` rewrites only the inputs whose `(apiVersion, kind)` pair a registered `RawDocumentLoweringRule` claims — `SupportedAPIVersion` unless the rule implements the optional `RawDocumentAPIVersioner` hook (`RawDocumentAPIVersion() string` — a plain `APIVersion()` accessor does not opt a rule in), which a consumer that owns its own API group uses so its documents are claimed rather than silently passed through (everything else passes through byte-identical) — decoding each claimed input via that rule's own `DecodeDocument` and running its `LowerDocument` once. The emitted `Application` bytes then re-enter the caller's parse and `Transform` exactly as if authored: the in-transform rules run there, under that call's `MaxLoweringDepth` budget, and ClusterProfile capability rendering is merged there. Before claiming a document, `LowerRaws` validates its `metadata.name` (required, DNS-1123 subdomain) and `metadata.namespace` (DNS-1123 label, if set) itself — the in-transform path gets the same check from `ParseWithExtraTypes`, which a raw-entered document never goes through. |

**Raw-rule contract: a raw rule rewrites authored input; it does not lower.** Emit what a
person would author — for example an `expose` trait carrying only its `hostnames` — and
let `Transform` run the in-transform rules and merge capability rendering. A raw rule
that copies capability rendering (a `PlatformReserved` field such as `controllerType`)
into what it emits is writing platform-reserved values into authored input, and
`Transform` rejects them with `ErrPlatformReserved`. `ctx TransformContext` is still
passed to the raw rule, but `LowerRaws` no longer needs an evaluated `ClusterProfile`
itself, since no `CapabilityAware` in-transform rule runs inside it. Besides metadata,
`LowerRaws` checks duplicates and generated-name collisions across the call (pass-through
identities included), rule arity, component and policy schemas of each emitted document,
and that each emitted document's apiVersion is `SupportedAPIVersion` or the group its
rule claims. Emitted traits are not checked in `LowerRaws`. A component or policy is
checked on its own copy of its properties, so checking it cannot strip a reserved
`null` from a trait or another element that shares its map before `Transform` sees it
(go-kure/launcher#626). `Transform` does not
shape-check trait properties either (it enforces only their platform-reserved keys): like
any authored document, each parsed output document must go through
`ValidateAuthoredProperties`, after parameter substitution, before `Transform` (see its
paragraph under Property schemas below). Without it, a raw rule's `enablePDB: "true"` on
a trait whose schema declares a boolean builds cleanly and is silently dropped. Before
go-kure/launcher#357 `LowerRaws` schema-checked emitted traits itself; a caller that
relied on that must now add the `ValidateAuthoredProperties` call.

The returned bytes cannot say which rule produced them: the element origin is unexported
and does not survive serialization. A caller that records provenance calls
`LowerRawsWithSteps(raws, ctx) ([]json.RawMessage, []LoweringStep, error)` instead, which
returns the same documents plus one `LoweringStep` per claimed input, in input order: `Rule`
is the rule identity (`rawdocument/<apiVersion>/<kind>`, suffixed `@<version>` when the rule
declares `ContractMetadata().Version`), `From` the authored `metadata.name` and `To` the
`metadata.name` of each document it emitted. `From` is a name, not a full identity, so two
claimed inputs of one kind and name in different namespaces are told apart only by their
position. A pass-through input has no step; on error the steps are nil and the
`LoweringError`'s `Chain` reports the failing document's steps. `LowerRaws` is
`LowerRawsWithSteps` with the steps dropped.

`NameAllocator`'s generated-name collision detection (D2) is scoped by `Origin.Namespace`,
not by name alone: two documents authored in different namespaces may generate the same
child name without colliding, since they lower to namespace-disjoint resources — the same
reason two identically-named Kubernetes objects in different namespaces coexist. Two raw
inputs sharing both a name and a namespace are rejected earlier, as a duplicate authored
document, before either reaches the allocator.

Every rule receives the run's one shared allocator as `LoweringContext.Namer`, which the
engine never leaves nil at any position (raw documents included), so a rule derives every
generated child name through `lctx.Namer.Name(base, suffix, origin)` with no nil-guard
fallback. A nil `Namer` is a contract violation by whoever built the `LoweringContext`.
Code that drives a rule directly, outside the engine — a rule's own unit test in another
module, a pre-pass, a golden-file or fixture harness — builds the `Namer` with
`NewNameAllocator()` (the zero value is not usable) and shares that one allocator across
every call belonging to the same run, so collisions between those calls are detected.
`LowerRaws` shares one allocator across its raw inputs, but only for round-0 raw-rule
claims; in-transform rules run inside each document's own `Transform` with a fresh
allocator, so a name an in-transform rule generates is never compared across documents.
A caller that transforms several documents detects that collision with
`CheckCrossDocumentCollisions`: it takes each document's identity and generated objects
(a `GeneratedDocument` per document, including any objects a layout walk adds) and reports
every object — keyed by API group, kind, namespace and name — that more than one document
generates, once per object, naming every document that generates it. Being keyed on the objects rather than on allocator claims,
it also catches two same-named authored components in one namespace, and a cluster-scoped
object generated from documents in different namespaces. An object's namespace is read from
the object, so pass objects as generated; an object with no kind is an error.

Nothing above compares the applications inside one document: a component and another
component's trait, two components, or two traits can each generate the same object, and
both pass `Transform` (go-kure/launcher#646). After transforming a document, generate it
with `GenerateApplications(cluster)` and pass the result to `CheckInDocumentCollisions`. It
returns each application's objects with its producer, generating each application once, in
the order kure's own generation uses and with the bundle labels and annotations kure adds,
so its objects replace a `Bundle.Generate` of the same cluster rather than add a second
generation, which could differ from the first. The check reports every object, keyed as
above, that more than one application generates, naming each producer as a component (a
sibling group is one) or as a trait's sub-application and its component. A trait named
after its own component reads like that component, so a second producer with the same name
is named as another application of it. A repeat within one application is not reported. `kurel build` runs both before it writes anything.

A force-applied PersistentVolume or PersistentVolumeClaim is warned about, not refused
(go-kure/launcher#720): when an update changes one of its immutable fields, Flux deletes
and recreates it instead of failing the apply, which can lose a claim's data. Pass the same
`GenerateApplications` result, after `CheckInDocumentCollisions`, to
`Transformer.WarnForcedVolumes`. It emits one warning through the warning handler
(`SetWarningHandler`) per PersistentVolume and PersistentVolumeClaim that carries
`kustomize.toolkit.fluxcd.io/force: enabled` (the `force-replace` trait sets it) or whose
application is `GeneratedApplication.Forced` (its leaf bundle sets `Force`, which a
`reconciliation` policy's `force: true` does). The warning names the kind,
`namespace/name`, the producer and every reason the object is forced, in generation order:
`PersistentVolumeClaim shop/data (component "db") is force-applied
(kustomize.toolkit.fluxcd.io/force: enabled): when an update changes an immutable field,
Flux deletes and recreates it instead of failing the apply, which can lose its data`.
An object counts as annotated as Flux's force selector matches it: the force key as a label
or an annotation, with `enabled` in any letter case. Objects are read as Flux applies them:
a list envelope still in the output stands for its members — Kustomize's build expands a
kind ending in `List` whose `items` is an array, recursively, then Flux expands any
remaining object whose `items` is an array, one level only — and a member is forced by its
own metadata, not the envelope's. An object generated more than once is warned once,
naming its first producer and every reason any copy is forced. It
covers every generated claim alike — a `webservice`/`worker` `volumes` entry (a
synthesized `pvc` trait, so named as a sub-application of its component,
go-kure/launcher#702), the `pvc` trait, the `persistentvolumeclaim` component, a `manifests` component's objects — and changes no
output. A `volumes` entry with `claimName` and the `volsync` trait's `sourcePVC` generate no
claim: an existing claim is warned where it is generated. With no warning handler it
does nothing. An embedder that does not
call it gets no warning. Objects a layout augmenter adds outside `Generate` are not in the
inventory and are not checked (`kurel build` refuses an augmenter whose `Generate` does not
cover them; see `LayoutAugmentationCoverage`).

`Reserve`/`Name` fail on every repeat claim of a name, including one from the same content.
Rules whose outputs share one derived object (two components pointing at the same chart
source, say) use the emit-or-adopt pair instead:
- `EmitOrAdopt(name, identity, origin)` claims a name for an element whose content is fully
  determined by `identity`, a string the rule builds from every input that shapes that
  element.
- `NameOrAdopt(base, suffix, identity, origin)` is the same claim through `Name`'s
  `<base>-<suffix>` construction and DNS-1123 check.

The first claim returns `adopted=false`, and the rule emits the element. A later claim with
the same identity, from any element of the same authored document and in any round, returns
`adopted=true`, and the rule must not emit the element again. Same-round sibling components
cannot see each other's output, so this is how they share one object instead of colliding. A
different identity at the same name, the same identity claimed from another authored document
(each settled document is transformed on its own, so it would lack the adopted element), an
empty identity, and a name already held by `Reserve` stay hard errors, and `Reserve` still
refuses a name claimed this way. The cross-document refusal fires only where one allocator
spans documents (`LowerRaws`' raw rules, or a caller's own shared allocator); within
`Transform` every claim comes from one document. Errors name an identity only by a short
SHA-256 digest, never its text, so an identity may include sensitive inputs.

Three caller constraints the allocator does not check. The shared element must be of a terminal
type, one no lowering rule claims: claims outlive the round, so a lowerable element could be
replaced under another name while a later adopter still references the claimed one. Adoption
covers only the shared element: the adopting rule still emits its own output, because the
engine rejects an empty `LoweringResult` as a deletion, so a rule whose whole expansion is the
shared element cannot use this pair. And the document check sees only the authored document:
when a document rule fans one authored document out into several documents, their elements
share that origin, so adoption must not be relied on across them.

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

**Same-name sibling groups.** A `ComponentLoweringRule` may emit several components
under one name, each of a distinct type that has a component handler (no lowering
rule claims it). They form one sibling group, which deploys as a single component.
Each member keeps its own config, policy defaults, traits and objects. The group has
one tier, one bundle, one `dependency` node, one auto health check (for the first
member's kind) and one layout directory. Its application generates each member's
first object in emission order, then every member's remaining objects in the same
order, as a single component generating all of them orders them. `webservice`
lowers to a deployment, a service and a serviceaccount member, which give
Deployment, Service, ServiceAccount, then the claims of the deployment member's
synthesized `pvc` traits (go-kure/launcher#702). It answers every config contract the transform reads
(Service port and port name, backend Service name, routing target, ServiceAccount,
single-pod claim) from the one member that has a value, gives the Flux namespace
to every member that takes one, and reports the group's name as its component
name (`ComponentNamed`). When a member's routing target selects another member's
pods (its non-empty `matchLabels` are a subset of that member's pod template labels,
with no `matchExpressions`) and every port of the routing member targets its own
port number, whatever its protocol, the traffic lands on the group's own pods, on
the ports it was routed to. The synthesized inbound policy then keeps the group's
component label as its pod selector, as one component deploying both would, while
its ports still go through the routing target: a routed Service port name becomes
its number and a non-TCP port is dropped. A member that remaps a port or names a
`targetPort` gets the Service `selector` policy, so it opens the port the pods
listen on.

Traits run per member, against that member's own config: the rule decides which
member carries each trait. A routing trait (ingress, Gateway API routes) belongs on
the member that owns the Service; a workload trait on the workload member. The
group applies its members' traits in authored order, not member by member: the
rule's own traits first, in member order, then the traits it forwarded, by the
slot each held among the authored traits (a forwarded trait a trait rule lowers
later keeps that slot). Trait sub-applications are therefore ordered as for one
component carrying the same traits.

The build refuses a group:
- whose members fall in different tiers, unless a placement policy places the
  group (it then deploys in the placed tier);
- in which two members answer the same contract (a member that runs pods
  answers `ServiceAccountName` even with no name, since its pods run as the
  namespace's `default` account);
- that has a member needing layout-level resources;
- in which two members generate the same Kubernetes object (API group, kind,
  namespace and name), such as two Services both named after the group;
- in which traits on two members create the same sub-application (the same trait on
  both members derives one name, such as `web-rbac`, from the shared name).

An authored duplicate name is still refused. So is a name repeated by different
rule invocations, or by a trait or document rule, including a copy of a member.

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

A trait a rule builds is sealed: its properties are final, and no
`ClusterProfile` capability rendering is merged into it later. A trait a
`ComponentLoweringRule` or `DocumentLoweringRule` merely forwards stays an ordinary
authored trait (unsealed, still capability-processed). Forwarding covers returning
`comp.Traits` itself and returning unchanged copies of its elements inside a new
slice, for example to add one trait of its own next to the authored ones or, in a
document rule, to rebuild a component around the authored traits; a copy whose type
or properties map the rule replaced counts as built by the rule. A forwarded trait's
`Origin.Index` stays its authored slot even when the rule places its own trait ahead
of it, and a trait a document rule moves to another component keeps the component it
was authored on in its `Origin`. A `DocumentLoweringRule` is handed a copy of the document whose component and
trait slices are its own, so neither the rule nor the engine's stamping of what it
forwards writes through to the authored document; properties maps are shared, and a
rule still must not mutate them.

Sealing says nothing about whether the trait's content was checked. A component or
trait a rule emits is synthesized when its properties are the rule's own output from
checked input, so a `PlatformReserved` value the rule rendered from
`LoweringContext.Capability` is accepted rather than rejected as authored. That
holds only when the rule's input was checked before it ran: a
`ComponentLoweringRule` that declares a schema (`PropertySchemaProvider`) or whose
input component is itself synthesized, or a `TraitLoweringRule` that declares a
schema over an unsealed trait or whose input trait is itself synthesized. A trait
nested in an emitted component gets the same classification as that component. A
`DocumentLoweringRule`'s output is never synthesized: the rule
sees trait and policy properties and metadata too, which nothing checks before it
runs. Such a rule writes a reserved value it renders from `LoweringContext.Capability`
with `Component.RenderReserved(path, value)`, or `Trait.RenderReserved(path, value)`
for a trait it builds, which writes a deep copy of `value` at the dot-separated
object-key `path` in `Properties` (`"networkPolicy"`,
`"tls.secretName"`; array items cannot be addressed; an object along the path that
is missing or `null`, typed nil included, is created) and records another deep copy
of it as rendered, both keeping its Go types. Changing `value` afterwards changes
nothing in `Properties`, and one value rendered at two paths is two independent
copies, so emission validation normalizing it under one key leaves the other as
written. A reserved key is then accepted only while it
holds the recorded value at that same path: either that value itself, or what
emission validation makes of it under the key's own schema, compared Go type for Go
type and a floating-point number bit for bit. So the record survives validation's
normalization exactly where validation performs it (a `[]byte` or `[]int32` under a declared array becoming `[]any`, a named
integer under a declared integer becoming `int`), and nowhere else: below a key an
object leaves to `AdditionalProperties`, or under a schema with no `Type`, the
handler receives what the rule wrote, so a rendered `[]byte` there is not matched by
an authored list of the same integers. A value the rule copied into the key from a
trait, a policy, metadata or another component, or changed after recording it, is
refused like any authored one — including a number that only prints the same, such
as an authored `1.0000000000000001e+18` over a rendered `1000000000000000100`, or one
that only compares equal, such as an authored `-0.0` over a rendered `0.0`, which a
handler tells apart with `math.Signbit` (go-kure/launcher#612). `RenderReserved`
refuses a value that is or holds a `null`, NaN or ±Inf, any Go type other than strings, booleans, numbers, slices, arrays and
string-keyed maps, or a collection that contains itself. The record follows the component
through copies and later lowering rounds; a rule that rebuilds a component from its
fields, or a document that is serialized and parsed again (what `LowerRaws` returns),
leaves it behind. A trait works the same way against its own type's schema; its
record covers the trait's own properties, checked before `applyTraits` merges a
capability rendering in, so that merge exempts nothing. Output of any other rule whose
input was not checked stays authored too (`RenderReserved` works there the same way)
and is checked like anything a user wrote, so a schema-less rule cannot pass an
authored reserved value through. Its components and traits — those it emits at trait position and those
nested in the components it emits — are checked for reserved keys as they are
emitted, before emission validation strips an explicit `null`, so a reserved key
written as `null` is refused here exactly as when authored directly
(go-kure/launcher#609, go-kure/launcher#626). Emission validation of any rule's output
normalizes the element's own copy of its properties, never the map the rule emitted,
so validating an element whose type does not reserve a key cannot strip that key's
`null` from another element sharing the map (including one the rule forwarded), which
may lower into one that does reserve it a round later. A trait such a rule only forwarded
keeps its classification, as a forwarded component does. A sealed trait that is not
synthesized is checked as it stands: as it is emitted, and again before a
`TraitLoweringRule` that declares a schema runs over it and when its handler applies
it. A schema-less rule that copies an authored reserved value into a trait it emits,
or writes one there any way other than `Trait.RenderReserved`, is therefore rejected;
a value it renders from capabilities with `Trait.RenderReserved` is accepted while the
key still holds it. A
sealed trait that passes that check is still not checked input for what the rule
emits: its schema covers the trait's own reserved keys, not those of the components
or traits the rule builds, so that rule's output stays authored. A
component a `DocumentLoweringRule` forwards — the same element of the
`doc.Spec.Components` it was handed, not a copy — keeps the classification it arrived with:
forwarding neither makes it synthesized nor resets it to authored. What a user wrote is checked
before any rule can rewrite it: before a `ComponentLoweringRule` claims the
component, and for every component of a document before its `DocumentLoweringRule`
runs, so rebuilding a component by value does not launder an authored reserved
value. Every component and every trait no rule synthesized is also checked at the
start of each lowering round, before any rule of that round runs, so a rule that
emits an element sharing a component's or trait's properties map cannot have
emission validation strip an authored `null` from it.

**Post-policy steps.** Lowering runs before the environment policy, so a rule
cannot write a value it derives from what the policy decided. It attaches a
`PostPolicyStep` to the component it emits instead, with
`Component.AfterPolicy(step)`. The transform runs the steps on the config the
component's handler built, in the order attached, right after
`Enforceable.ApplyPolicy` and before any trait of the component; an error fails the
transform, naming the component. A step is part of the component value: it survives
copies and later lowering rounds, including a trait rule rewriting the component's
traits, but not serialization, and a document cannot author one. A
`ComponentLoweringRule` that lowers a component carrying a step carries it over only
by copying that component; a component it builds anew has no step, and the engine
cannot tell one was dropped. The built-in user is the `postgresql` rule, which
attaches a step to the `cnpg-cluster` it emits to set the values postgresql derives
after the policy (`CnpgClusterConfig.ApplyPostgresqlDefaults`; go-kure/launcher#281,
go-kure/launcher#729).

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
`Types`, `Description`, `Required`, `Default`, `Enum`, nested `Properties`, `Items`, and `AdditionalProperties`
(default false; escape-hatch fields set it true). The rich fields (`Types`, `Enum`, `Properties`, `Items`,
`AdditionalProperties`) are meaningful only for handler properties: the two flat call sites (kurel
parameters, capability rendering) reject them at decode time, so unifying the type does not widen
their accepted behavior.

A kurel parameter's `Type` is `string`, `integer`, `boolean`, `array` or `object`
(go-kure/launcher#421). `ResolveParameters` replaces a whole-value `${name}` for an `array` or
`object` parameter with the value's YAML list or map. With no `items` or `properties` to declare,
the parameter checks only that shape (its default too, and a string default is refused); the
substituted value is checked by `ValidateAuthoredProperties` against the consuming handler's schema
like any authored property. Such a parameter cannot be embedded in a larger string, in the template
or in another parameter's string default.

`Types` is the union idiom (go-kure/launcher#383): a leaf that accepts more than one scalar type
lists them, and a value is accepted when any member's single `Type` accepts it — whatever order the
members are listed in — normalised exactly as the first member, in declared order, that accepts it
would (below). Members are two or more distinct scalar types (string, integer,
number, boolean); `Type` and `Types` are mutually exclusive. A schema that sets both, or a malformed
union, is a schema error reported as soon as a value reaches the leaf. Every Kubernetes
`intstr.IntOrString` leaf — the rolling-update `maxUnavailable`/`maxSurge` knobs, the networkpolicy
`port`, the `service` kind's `ports[].targetPort` — declares `integer`/`string`, and every `resource.Quantity` leaf (`cpu`, `memory`, a claim's
`storage`) declares `string`/`number`, because their parsers take a fractional number too. `Type`
stays empty on a union leaf, so a schema consumer that does not read `Types` sees an untyped leaf and
keeps accepting every member. A completeness test (`pkg/cmd/kurel`) enforces that every built-in
schema node declares exactly one of `Type` and `Types`. `Transformer.HandlerSchemas()` returns a `HandlerSchemaSet{ Components, Traits, Policies }`
of every registered handler and lowering rule that declares one, so the downstream runtime's validator can check a
component's, trait's or policy's properties before the handler is invoked. Built-in examples: the `configmap` trait and the
`passthrough` component.

`Description` is optional (`json:"description,omitempty"`) but every built-in property populates it —
including nested object fields and array item schemas at every depth — so the downstream runtime can surface prose in
its generated Handler API Reference. A completeness test (`pkg/cmd/kurel`) enforces that no built-in
schema node is left without a description.

A schema field may also be marked `PlatformReserved`: its value may arrive only via
`ClusterProfile` capability rendering, never authored inline. `enforcePlatformReserved`
(`property_validate.go`) rejects an authored value for such a field — including an
explicit `null` — before capability rendering is merged in, wrapping
`ErrPlatformReserved`; it walks declared nested object fields and the items of a
declared array of objects too (named `properties.items[0]`), so a reservation on an
inner field is enforced wherever it is declared, not only at the top level
(go-kure/launcher#635).
`createApplications` and `applyTraits` (`transform.go`) run this check on the authored
path, and the lowering engine runs it on a `TraitLoweringRule`'s input trait before
capability rendering is resolved into it (see Lowering above). A value a lowering rule
recorded with `Component.RenderReserved` or `Trait.RenderReserved` is exempt while the
key still holds it (see Lowering above).

Separately, `validateProperties` (`property_validate.go`) checks an EMITTED
component/trait/policy's properties against its TARGET handler's declared schema —
enforcing `Required`, `Type`/`Types`, `Enum`, and nested `Properties`/`Items`/
`AdditionalProperties` — immediately after a lowering rule returns it, so a rule
cannot silently produce properties its own target handler would reject.

Both this check and the authored one below write a normalised value back where the
handler's reader would not accept what was supplied: a typed Go collection becomes
`[]any`/`map[string]any`; a `string`, `boolean` or `number` value of a named Go type
(`type Mode string`) becomes its predeclared type; and an `integer` value of kind
`int8`, `int16` or any unsigned kind, or of any named integer type
(`type Replicas int32`), becomes `int` — a value outside the `int` range is rejected
instead. Values already of type `int`, `int32`, `int64` or `float64` are left as they
are, because some readers accept `int` but not `int64`; a named integer type becomes
`int` rather than its underlying type for the same reason. A `Types` union leaf is
rewritten by whichever member the value matched. A property whose schema declares
neither `Type` nor `Types`, or a key an open object leaves undeclared, is not rewritten.

`ValidateAuthoredProperties` (`property_validate_authored.go`) is that check's
authored-path counterpart, and closes go-kure/launcher#408. Parsing is strict
(`KnownFields(true)`) only down to the envelope: `Component.Properties`,
`Trait.Properties` and `ApplicationPolicy.Properties` are each `map[string]any`, so
before this every handler read the keys it knew and silently dropped the rest — a
misspelled or stale property built successfully and produced nothing. The check walks
each authored component and its traits in document order, looks up the same schema
`validateProperties` would (terminal handler first, then a `ComponentLoweringRule` /
`TraitLoweringRule` claiming the type), rejects any key the schema does not declare,
and checks the shape of every key it does. `kurel build` calls it immediately after
parsing — and, in package mode, necessarily *after* `ResolveParameters`, because a
`${...}` placeholder is a bare string until substituted.

Before any of that, it runs `Transform`'s own reservation check
(`enforcePlatformReserved`) over the document, with the same schemas and the same
error text `Transform` would report for it. It has to come first: validation drops an
explicit `null` under a nested declared object, so a reserved key authored as
`config: {locked: null}` would otherwise be gone before `Transform` could refuse it
(go-kure/launcher#635).

The shape check is also the only place an authored scalar's *type* is enforced for
every property (go-kure/launcher#325). Many built-in handlers read string properties
with a comma-ok assertion (`cfg.Delivery, _ = props["delivery"].(string)`), so on
the handler alone a `delivery: 123` becomes `""` and is defaulted as though it were
absent. Type-checking once here, against the schema each handler already declares,
is what rejects it — not a per-field check in each handler. A caller that drives
`Transform` directly must therefore call `ValidateAuthoredProperties` first (after
any parameter substitution) to get the same guarantee `kurel build` gives.

Authored policies are checked the same way, after every component, in document order:
against the `PolicyHandler` registered for the type, else the `PolicyLoweringRule`
claiming it. The built-in policy handlers each declare a `PropertySchema`, so a
misspelt `reconciliation` key such as `prunee` is a build error rather than a
setting the handler never reads.

Three positions are exempt, each because there is no schema to check against: a
component or trait type no handler and no lowering rule claims (rejected separately
by the type allowlists and by `validateSettled`); a custom trait type from a
`CapabilityDefinition`, which declares that the type *exists* but not what properties
it accepts; and a policy type nothing is registered for, which the transform rejects
with `no handler for policy type`. Top-level `Required` is also deliberately not enforced
here — `ClusterProfile` capability rendering merges into a trait's top-level property
map after this runs, so a required property the platform supplies is legitimately
absent from what the author wrote. Nested `Required`, inside an object the author did
write, still is.

One property is legal on **every** trait regardless of what its handler declares:
`scope`. It is read by the transform engine rather than by a handler —
`buildCapabilityKey` builds the `"<traitType>.<scope>"` capability key for every trait
type, so a `pvc` or `certificate` trait can select a scoped `ClusterProfile` binding
even though neither handler declares such a property. `expose`, `ingress` and
`httproute` do declare it, but for an unrelated reason (sub-application naming), and
their own declaration wins over the engine default. Engine-owned keys are checked even
on a trait whose handler or lowering rule declares no `PropertySchema` at all (for
example a custom handler registered through `RegisterTrait`): a non-string `scope`
there is rejected, while the handler's own keys stay unchecked because nothing
declares them. A handler that refuses keys it does not read (for example
`topology-spread`, which takes none) asks `IsEngineTraitProperty` and lets these
through. It cannot tell an authored `scope` from one a capability rendering merged
in, and a rendered one selects nothing (the binding was already chosen), so such a
handler also refuses rendering keys in `ValidateAndApplyDefaults`, at
`EvaluateProfile`. Add to `engineTraitProperties` if
another engine-read property is ever introduced; the merge never mutates the
handler's returned schema, so `HandlerSchemas` still advertises only what each handler
actually declares.

`validateProperties`'s null check (`isNullValue`) treats a typed-nil pointer,
slice, or map — not just a bare `nil` interface — as `null`: a Go type assertion
alone can't tell an uninitialized slice/map apart from a validly-typed empty
collection, so a lowering rule that emits an unset (rather than empty) collection
field is still caught.

`IsNullValue` is the exported form of that check, and with it the contract itself:
**a null value is absent, not present-and-empty.** The predicate is nil-ness, not
serialization, and that distinction is load-bearing rather than pedantic: whether
the two shapes serialize differently depends on the ENCODER. `encoding/json` (and
`sigs.k8s.io/yaml`, which routes through it) writes `null` for a nil map or slice
and `{}`/`[]` for an allocated empty one, but `gopkg.in/yaml.v3` — the YAML library
this package parses with — renders both as `{}`/`[]`, because its encoder
dispatches on `reflect.Kind`. Keying the contract on nil-ness rather than on
rendered output is what makes it hold under either. The predicate parts company
with every encoder only on values no document round-trips (a nil channel is
reported null though `encoding/json` cannot marshal it at all).

It exists for parsers outside this package — handler and trait property readers,
and out-of-tree lowering rules — that must classify a null the same way the
validator does. Use it rather than `value == nil`: a typed nil is a non-nil
interface holding a nil value, so `== nil` is false and a `.(map[string]any)`
assertion on it succeeds with `ok=true` and a nil map, making the key read as an
authored empty collection. Where empty and absent mean different things that
difference is a behaviour change, not a cosmetic one. The standing example is a
`metav1.LabelSelector` in a `NetworkPolicy` peer: an empty one matches everything,
while a nil one applies no constraint on that axis — which is *not* the same as
matching nothing, since what the peer then selects depends on the sibling fields it
still has (`k8s.io/api` `networking/v1/types.go:199-222`).

`IntegerValue` is the matching reader for integers. It returns a property value as
an `int64` when it is a whole number of any Go integer kind, named or not, or a
finite integral float. It refuses a fraction, NaN/±Inf, a non-number, an unsigned
value above `math.MaxInt64`, and a float outside −2^63 ≤ value < 2^63 (−2^63 itself
fits `int64` and reads; 2^63 does not). The builtin handlers
read every integer property through it, and each then checks the result against its
own target (a port is 1–65535, a replica count fits `int32`), refusing a value that
does not fit rather than converting it. So a plain `int32` or `int64` from a lowering
rule or a Go caller reads the same as the `int` the YAML decoder produces, and a
value that would only fit after wrapping, like 2^32+80 for a port, is an error
instead of port 80. An out-of-tree handler should read integers through it for the
same reason. `IntegerInRange(value, lo, hi)` adds the target check and names the true
refusal reason, so an overflow like `1e20` never reads as a type error; a capability
`integer` property is checked with it too.

### What an explicit `null` means on the emitted path

The contract, stated once because "null" has several readers here and aligning
them one at a time is what made this take six rounds:

> A value that serializes to JSON null is absent, at every depth, on every path;
> a null is never a member of any `Items` type, so a null array element is a type
> error; reservation is about the KEY being written.

Applied in two places, because a key and an array element are not the same kind
of thing:

- **A declared, optional object key holding a null is DELETED** before its value
  is checked. `Required` already classifies a null as absent, so materialising it
  as a present key contradicted the classification the file had already made — and
  a handler parser decides presence with a bare two-value map lookup, so it saw a
  key the validator had decided was not there.
- **A null ARRAY ELEMENT is rejected**, by an explicit guard rather than by the
  type check. An element cannot be absent — it is present by being in the list —
  so there is nothing to normalise it to, and dropping it would renumber its
  siblings under a schema that may constrain length and order. The guard is
  necessary rather than decorative: a *typed* nil (`map[string]any(nil)`,
  `[]any(nil)`, reachable from a rule written in Go) satisfies the plain type
  assertion the object/array coercers try first, and iterating the resulting empty
  collection rejects nothing, so the type check alone accepted it. An `Items`
  schema with no declared type checks nothing at all — and so does *no* `Items`
  schema, which is why the guard runs before the `Items` walk rather than inside
  it: declaring no element schema says nothing about the members, but it does not
  license the one member no `Items` type could ever have matched.
- **An `Enum` member holding a null where the value's null would be stripped or
  rejected is refused.** Members are compared against a value that has already been
  normalised, while the declared members are not, so such a member could never
  match anything that reaches the comparison. Refusing it names the schema defect
  instead of leaving an `Enum` that silently never matches.

  The member is walked alongside its schema, because normalisation does not reach
  everywhere: a key an object leaves to `AdditionalProperties: true`, anything inside
  an array element with no `Items` schema, and anything below a schema with no
  declared `Type` pass through as written. A value can hold a null there, so a member
  holding the same null matches it and is accepted — `{opaque: null}` against
  `Enum: [{opaque: null}]` on an open object. A member's null compares equal only to
  a null value, whatever either side's Go type, and never to an empty collection. The
  rule is one-sided: where only the *value* holds a typed nil under such a key and the
  member holds an empty `[]`/`{}`, the two still match, as they always have — an
  undeclared key is outside every normalisation rule, so this does not start
  distinguishing a typed nil from an empty collection there. A member that cannot match
  for another reason — the wrong shape for the schema's type, or a key a closed
  object refuses — is still refused for any null it holds, as before.

  Refused per *member*, not per schema type. Refusing every `Enum` declared on an
  array or object type is simpler to state and was the first shape of this rule,
  but it also refuses the null-free compound enums that match perfectly well —
  and `PropertySchema` is exported, so a handler outside this repo would have seen
  a schema this validator used to accept start failing for a reason that does not
  apply to it. No built-in schema declares an `Enum` on a non-scalar type today,
  but nothing asserts that as a rule and this check does not depend on it. What is
  asserted is the rule above: `TestBuiltinHandlerSchemaEnumMembersHoldNoNull`
  (`pkg/cmd/kurel`) walks every schema that ships for a member holding a null,
  because the runtime arm only fires once a document validates against the property
  carrying the `Enum`.

  That walk is exported as `CheckEnumMembersHoldNoNull(schema PropertySchema) error`,
  so a handler outside this package can assert the same of its own schemas. It
  reports every `Enum` member — on the schema and on every schema nested under its
  `Properties` (in sorted key order) or `Items` — that holds a null anywhere, named
  by schema path and member index, and returns `nil` when none does. It is
  deliberately **stricter** than the runtime arm: it does not read the schema around
  a member, so it also reports the matchable nulls described above (under an
  `AdditionalProperties` key, inside an array element with no `Items`, below a schema
  with no `Type`). A schema it passes is never refused by the runtime's null rule; one
  it fails may still validate. A member nesting past the validator's depth bound
  counts as holding a null, as it does at runtime.

Two things this deliberately does not do:

- **It makes no exception for `PlatformReserved` keys.** Reservation
  (`enforcePlatformReserved`) is a rule about what a user *wrote*. On the authored
  surface the two rules never meet — it runs upstream of any emission validation, and
  `ValidateAuthoredProperties` runs it before its own strip —
  so reservation keeps treating an explicit null as *present* while the strip
  treats one as absent. Exempting reserved keys here would not have preserved the
  authored rule; it would only have handed a reserved null to the type check,
  producing a loud rejection with the wrong reason. The **component** and **trait**
  surfaces keep the same separation: an authored component or trait is checked
  before any rule can rewrite it. One a rule emitted from checked input — whose
  properties are the ones the strip touches — is exempt from reservation; the output
  of a rule whose input was not checked stays authored, and its components and
  traits are checked as they are emitted, before the strip (see the rule-output
  contract above).
- **A key the schema does not declare is untouched**, including inside an object
  that sets `AdditionalProperties`. Nothing describes such a value, so nothing
  here can normalise it, and a null inside an opaque object still reaches the
  handler parser. This is the same horizon as validation itself.

Scope, because this contract is not settled repository-wide: `ValidateAuthoredProperties`
(above) *does* reach `validatePropertyValue` — it calls it directly for every
authored key, with no strip of its own first. That is why
`validatePropertyValue` carries its own top-level null check
(`if isNullValue(value) { return value, nil }`) rather than relying entirely on
`validateObjectProperties`'s strip: on the emitted path the strip runs first and
the check is usually moot, but on the authored path it is what makes an
explicit null under an optional field read as absence instead of a type error.
What the contract does *not* reach is any path that skips both validators
entirely — a component a lowering rule constructs directly in Go, or a
built-in parser's own field read (`pkg/oam/builtin/components`) — which is why
those parsers keep their explicit-null guards regardless. Aligning that
parser-level layer with this contract is a separate, lower-level concern,
tracked as `go-kure/launcher#394`, and is not closed by this.

Two surfaces used to be named here as sitting outside the contract; both are now
fixed, so the note is historical rather than a live caveat:

- **Capability rendering** (`checkCapabilityValueType`, `capability.go`) is still
  outside the contract by the scope sentence — it is a parallel implementation,
  not a call-through to `validatePropertyValue` — but it now agrees with the
  contract's own behaviour: a present null reads as absence, matching the handler
  surface, and the type switch has a `default` arm. See "Capability rendering:
  types and nulls" below for the current, authoritative description. Fixed as
  `go-kure/launcher#431`.
- **The networkpolicy peer parser** used to read a *typed* nil `namespaceSelector`
  differently from an untyped one. Fixed as `go-kure/launcher#430` — both parsers
  now route their presence check through `oam.IsNullValue`'s reflect-based
  classification, so a typed nil reads as omission on every path that reaches it,
  the same as an untyped one.

## Contract metadata

Handlers and lowering rules may implement `ContractDescriber` (`ContractMetadata()
ContractMetadata`) to declare registration metadata about the contract they
implement: `Family`, `Version`, `RequiredCapabilityKeys` (the `ClusterProfile`
capability keys an entity of this contract needs), `Deprecated`, and
`DeprecationMessage`. It is primarily a discovery/documentation surface — the engine
does not enforce any of these fields (`CapabilityAware.CapabilityRequired` is what the
engine actually enforces). Two fields are read. A lowering rule's `Version` composes
the `"@<version>"` suffix on `Origin.Rule` (see below). `Deprecated` turns every
authored component or trait of that type into one warning through the Transformer's
warning handler (`SetWarningHandler`), in document order: `component "<name>": type
<t> is deprecated[: <DeprecationMessage>]`, or `trait type <t>` for a trait. Both
`Transform` and `TransformWithPolicy` warn; `LowerRaws` does not, because its output
re-enters `Transform` as authored and warns there. The check reads the authored
document before lowering, so a component a rule synthesizes never warns. A
deprecation is never an error and never changes the output. Nothing else here is
read or enforced. Consumers otherwise: schema publication, artifact
provenance in a downstream consumer, and deprecation tooling. Metadata rides the
existing registration mechanism; there is no separate contract registry.

`Transformer.HandlerContracts()` returns a `HandlerContractSet{ Components, Traits }`
of every registered component/trait handler, and every component/trait lowering
rule, that implements `ContractDescriber` — the same four component/trait
registries `HandlerSchemas()` covers (componentHandlers, traitHandlers,
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

### Capability rendering: types and nulls

A `CapabilityDefinition` rendering property uses the **flat** vocabulary —
`string`, `integer`, `boolean`, or **no type at all**, which accepts any value.
`LoadCapabilityDefinitions` rejects anything else at load time, and the value check
rejects it too, so a definition built in Go and installed with
`Transformer.SetCapabilityDefs` — which bypasses the loader — cannot silently accept
every value for a property by declaring a type outside that set.

An explicit `null` on this surface is **absence**, matching the contract the handler
surface follows, rather than failing as a type error; the null key does not survive
into the validated rendering. What happens next depends on `required` and `default`,
checked in that order — `required` always wins, even over a declared default:

- **required** — reported as required-and-missing. A declared default does not
  rescue it; requiredness is checked first.
- **optional, with a non-null default** — the declared default is substituted.
- **optional, with no default** (or `default:` with no value, which likewise
  declares **no default**, not a null one) — the key stays absent.

This holds for a typed nil as well as an authored one, which matters precisely
because `SetCapabilityDefs` takes Go-built definitions.

A **declared default is type-checked against its own declared type**, on the same
footing as a value the document supplies. Without that, a property was validated
when the *document* supplied the value and unvalidated when the *schema* did — so a
Go-built definition declaring `integer` with a `"three"` default injected that string
into the rendering, past the check that exists to keep it out. Files were never
exposed to this (`LoadCapabilityDefinitions` checks defaults at load); `SetCapabilityDefs`
was, being the same bypass the type check above guards. A property declaring **no
type** still accepts any default, unchanged.

This is a large internal builder surface; the tables above cover the entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the full
type reference, the design notes under the Concepts section, and `examples/` for
runnable applications.
