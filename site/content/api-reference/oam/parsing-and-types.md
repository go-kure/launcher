---
title: "Parsing and type identity"
weight: 4
---

The implementation, history and tests of each section below are on the
[contributor page](../../contributing/oam-reference/parsing-and-types.md).

## Parsing

| Function | Purpose |
|----------|---------|
| `Parse` / `ParseMulti` / `MustParse` | Parse one / many Application documents. |
| `ParsePackage` | Parse a `Package` (the `kurel.yaml` parameter declarations; the application is read separately). |
| `ParseClusterProfile` | Parse a `ClusterProfile`. `spec.kubernetesVersion`, when set, must be `1.N`, `v1.N` or `v1.N.P`; see `ValidateAuthoredPropertiesForTarget`. `spec.featureGates` (gate name to bool) overrides a gate's state; a name the gate table lacks is refused, and the map needs `spec.kubernetesVersion`; see `ValidateAuthoredPropertiesForTargetWithGates`. |
| `LoadCapabilityDefinitions` | Load `CapabilityDefinition`s for capability validation. |
| `ParseWithExtraTraitTypes` | Parse allowing additional (custom) trait types. |
| `ParseWithExtraTypes` | Parse allowing custom trait types **and** a `LowerableTypes` set — the document kinds, component types, trait types and policy types claimed by a transformer's registered lowering rules, and the policy types it has a handler for. |

Standalone parsing validates each trait's `type` against this package's own allowlist
of built-in trait types (the `security-context`, `topology-spread`, `force-replace` and
`disruption` traits are included, matching `SecurityContextHandler`,
`TopologySpreadHandler`, `ForceReplaceHandler` and `DisruptionHandler`);
`ParseWithExtraTraitTypes` widens that allowlist with caller-supplied custom types.
`ParseWithExtraTypes` widens it further with `Transformer.LowerableTypes()`, so a
document authored in types that only a lowering rule understands parses ahead of the
transform that will lower them away. The widening is additive and per position: an
empty `LowerableTypes` is exactly the strict behaviour, a name claimed for one position
never admits it at another, and decoding stays strict (`KnownFields(true)`), so a kind
carrying its own authored fields still belongs to the raw lowering entry point.

A policy `type` is checked the same way. Standalone parsing accepts `dependency` and
`placement` (the built-in handlers) and `app-dependency`, `health-checks` and
`reconciliation`, the delivery types that launcher has no handler for and that a
consumer's own handler answers; any other policy type is refused, with the supported
types listed. The last three stay accepted so that a document naming one reaches the
`no handler for policy type` refusal at build, which says why, and not a parse error.
A policy type of a consumer's own handler parses when the caller passes it as
`LowerableTypes.PolicyTypes`, or passes `Transformer.LowerableTypes()` of a transformer
that registered the handler with `RegisterPolicy`. The check after lowering reads the
registered handlers the same way. An authored identity (`name@version`) is checked as
the plain type it resolves to. A `name@version` that no declaration resolves is refused,
unless the caller claimed that literal text (in `LowerableTypes.PolicyTypes`, or as the
name of a handler it registered): where no policy identity is declared, a literal name is
kept as written.

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
"Component type allowlist" below names every type on this list, one row each; the
components README's "Kind inventory" (`pkg/oam/builtin/components/README.md`) gives the
object and the details of each kind.
`ingress`, `httproute`, `networkpolicy`, `cilium-networkpolicy` and `certificate` are
also trait types: the two lists are separate, and a component of such a type is the
authored object, not the trait.
One other per-type
registry has the same shape and the same failure mode: `traitComponentRestrictions`
(which traits a component type accepts; `scaler` is restricted to `webservice`, `worker`,
`deployment` and `statefulset`, and `disruption` to the same four).

**A `scaler` refuses authored `replicas`**. The
HorizontalPodAutoscaler owns the replica count, so the scaler builds the Deployment or
StatefulSet without `spec.replicas`, and `validateTrait` refuses a component that sets `replicas`
beside a `scaler`, naming the component and pointing to `minReplicas`/`maxReplicas` on the
scaler (`validateScalerReplicas`). An authored `null` is no value. The check runs on the
properties a lowering rule emits too: while the component type is still lowerable
`validateTrait` defers it, and `validateSettled` runs it on the lowered component. The
omission and the migration of an already-deployed object are in the components README,
"Replicas under a `scaler`".

**Tiers are declared, never derived from a type**.
`ClassifyComponentWithDomain` returns the tier the component's `<domain>/tier` annotation
names, or the empty tier when it carries none; a `placement` policy replaces it. A
component in no tier is ordered after no tier (see [Pipeline](pipeline.md#pipeline)). A component a lowering
rule emits is read the same way, with one exception: a Flux source the rule orders a
component after is applied with the application bundle, so a tier on it, from an
annotation or a `placement` policy, is refused.

### Component type allowlist

The component types parsing accepts without a widening (`validComponentTypes`), in the order
`sort.Strings` gives them. A change that adds a type adds its row at its position.

| Type |
|---|
| `alertmanager` |
| `apiservice` |
| `artifactgenerator` |
| `backendtlspolicy` |
| `bucket` |
| `certificate` |
| `cilium-bgpadvertisement` |
| `cilium-bgpclusterconfig` |
| `cilium-bgpnodeconfigoverride` |
| `cilium-bgppeerconfig` |
| `cilium-cidrgroup` |
| `cilium-clusterwidenetworkpolicy` |
| `cilium-egressgatewaypolicy` |
| `cilium-loadbalancerippool` |
| `cilium-localredirectpolicy` |
| `cilium-networkpolicy` |
| `cilium-nodeconfig` |
| `clusterexternalsecret` |
| `clusterissuer` |
| `clusterrole` |
| `clusterrolebinding` |
| `clustersecretstore` |
| `cnpg-backup` |
| `cnpg-cluster` |
| `cnpg-clusterimagecatalog` |
| `cnpg-database` |
| `cnpg-databaserole` |
| `cnpg-imagecatalog` |
| `cnpg-objectstore` |
| `cnpg-pooler` |
| `cnpg-publication` |
| `cnpg-scheduledbackup` |
| `cnpg-subscription` |
| `configmap` |
| `crd` |
| `cronjob` |
| `csidriver` |
| `daemonset` |
| `deployment` |
| `endpointslice` |
| `externalsecret` |
| `fluxcd-alert` |
| `fluxcd-kustomization` |
| `fluxcd-provider` |
| `fluxcd-receiver` |
| `gateway` |
| `gatewayclass` |
| `gitrepository` |
| `grpcroute` |
| `helm` |
| `helmchart` |
| `helmrelease` |
| `helmrepository` |
| `helmtemplate` |
| `horizontalpodautoscaler` |
| `httproute` |
| `imagepolicy` |
| `imagerepository` |
| `imageupdateautomation` |
| `ingress` |
| `ingressclass` |
| `issuer` |
| `job` |
| `limitrange` |
| `listenerset` |
| `manifests` |
| `metallb-bfdprofile` |
| `metallb-bgpadvertisement` |
| `metallb-bgppeer` |
| `metallb-community` |
| `metallb-ipaddresspool` |
| `metallb-l2advertisement` |
| `mutatingadmissionpolicy` |
| `mutatingadmissionpolicybinding` |
| `mutatingwebhookconfiguration` |
| `namespace` |
| `networkpolicy` |
| `oci` |
| `ocirepository` |
| `passthrough` |
| `persistentvolume` |
| `persistentvolumeclaim` |
| `pod` |
| `poddisruptionbudget` |
| `podmonitor` |
| `podtemplate` |
| `postgresql` |
| `priorityclass` |
| `prometheus` |
| `prometheus-probe` |
| `prometheusrule` |
| `referencegrant` |
| `replicaset` |
| `replicationcontroller` |
| `replicationdestination` |
| `replicationsource` |
| `resourcequota` |
| `resourcesetinputprovider` |
| `role` |
| `rolebinding` |
| `runtimeclass` |
| `secret` |
| `secretstore` |
| `service` |
| `serviceaccount` |
| `servicecidr` |
| `servicemonitor` |
| `statefulset` |
| `storageclass` |
| `tcproute` |
| `thanosruler` |
| `tlsroute` |
| `udproute` |
| `validatingadmissionpolicy` |
| `validatingadmissionpolicybinding` |
| `validatingwebhookconfiguration` |
| `volumeattributesclass` |
| `webservice` |
| `worker` |

## Type identity

A type (a component, trait or policy type, or a configuration-defined model) is identified by
`name@version`, lowercase. The registry is keyed by that pair, and each handler, lowering rule and
model declares its name and version when it registers.

### Grammar

| Part | Rule |
|---|---|
| `name` | lowercase letters, digits and hyphens, starting with a letter. A dot appears only in the domain. |
| Kubernetes kind | `kind.group@<API version>`, the core group with no group part: `deployment.apps@v1`, `service@v1`. |
| Any other type or model | `<name>.<domain>@v<semver>`: `cnpg-cluster.launcher.gokure.dev@v1`. |
| `version` | `v<major>`, `v<major>.<minor>` or `v<major>.<minor>.<patch>`. A short version is exact: `v2` is `v2.0.0`. |

`TypeRegistry.IDs` and `TypeRegistry.Resolve` return a version as it was registered. A type registered
as `webapp.acme.example@v2` comes back as `v2`, not `v2.0.0`. `TypeID.Canonical()` returns the identity
with its version as `v<major>.<minor>.<patch>`: compare or persist that form. A Kubernetes alpha or
beta API version (`v1beta1`) keeps its spelling. The grammar cannot tell a GA Kubernetes kind from a
semver type, so `Canonical` turns `deployment.apps@v1` into `deployment.apps@v1.0.0`. It is a comparison
key, not a Kubernetes `apiVersion`.

The domain of launcher's own types is `launcher.gokure.dev`. A consumer or a configuration-defined
model uses a domain it owns. The `<family>.v<N>` successor spelling (`webservice.v2`) is retired:
a successor is the same name at a new version. Every version ever published stays registered.

`ValidateTypeIdentity` checks one identity against the grammar. `ResolveTypeReference` resolves
the string an application authors to one registered identity.

### Resolution

An application may write a short name, with or without a version. The reference resolves, and the
result is the registered identity, with the version as it was registered, only when all of these hold:

1. A short name resolves only when exactly one registered type claims it. A type claims its
   leading name segment (`cnpg-cluster`) and its Kubernetes kind (`deployment`).
2. No short name may equal another type's qualified name. Registration refuses it.
3. A short name may carry a version: `webapp@v1.1`.
4. An omitted version resolves only when exactly one version of the type is registered.
   Otherwise the reference is refused and names the versions that exist, in version order:
   by major, then alpha before beta before GA, then the number after the stage, so `v1alpha1`,
   `v1beta1`, `v1`, `v2beta1`, `v2`, `v10`. `TypeRegistry.IDs` lists a name's versions in the same order.

Every short name in the type allowlists above stays valid: it is the unique alias of the built-in
that registered it.

| Call | Does |
|---|---|
| `TypeRegistry.Resolve(ref)` | Resolves `name` or `name@version` against the identities registered in that registry (rules 1 to 4), or refuses it. The result is the registered identity, so its version is the registered spelling (see `TypeID.Canonical()`). |
| `ResolveTypeReference(ref, registered)` | The same rule against a flat list of identities, such as `TypeRegistry.IDs` or a caller's own set. It refuses the list itself when two entries conflict. |

### Declaring and resolving

A handler or lowering rule stays registered under its plain name; its identity is declared beside it,
keyed by that name. `registry.DeclareTypes(t)` (`pkg/oam/builtin/registry`) declares every built-in's:
a Kubernetes kind component (`deployment`, `tlsroute`) is that kind's identity, every other built-in
(`webservice`, `cnpg-cluster`, each trait and policy) is `<name>.launcher.gokure.dev@v1`. A consumer
declares its own types with `DeclareComponentTypes`, `DeclareTraitTypes` and `DeclarePolicyTypes`.
A declaration is refused whole, leaving the transformer as it was, when a name has no handler or
rule, an identity is invalid, or two conflict (rules 1 to 4).

| Call | Does |
|---|---|
| `Transformer.ResolveComponentType(ref)` (also `ResolveTraitType`, `ResolvePolicyType`) | Resolves an authored type to its declared identity, or refuses it. |
| `Transformer.ComponentHandlerFor(ref)`, `ComponentLoweringRuleFor(ref)` | The handler or rule of the type a reference names. Use them instead of indexing by `Component.Type`. |
| `Transformer.ResolveTypes(app)` | The application with every component, trait and policy type that a declaration names written as its identity (`name@version`), as a copy; a type nobody declared stays as written. The result is valid input to `Transform`, which runs the same resolution first and dispatches on the plain registered name. |
| `ParseWithExtraTypes(..., t.LowerableTypes())` | Validates an authored identity as the type it resolves to; the parsed `Type` keeps the authored text. |

A plain name that no declared identity names, or that is not a valid identity at all (a custom trait
such as `custom_metrics`), behaves as before: it passes through to the allowlist or the "no handler"
refusal. A name that a declared identity claims resolves first, so a short name that more than one
type or version claims is refused as ambiguous even when a handler is registered under that plain
name. A reference with a version that does not resolve is refused in the resolver's words, naming
the versions that exist, before any handler runs. That refusal applies in a position (components,
traits, policies) where a type is declared; where nothing is declared, a type is its registered
name, `@` included, and the lookups and `Transform` dispatch on it as before. `Transform` returns
that refusal as a `ValidationError` (as `Parse` does) and `errors.Is` still matches `ErrTypeReferenceUnknown` and
`ErrTypeReferenceAmbiguous`. So does `ResolveTypes`, and so do the authored checks: the same
`ValidationError`, with the resolver's error as its `Cause`. `ResolveTypes` returned the resolver's
error unwrapped before; a caller that matched it with `errors.Is` is unaffected, and one that
compared it with `==` or type-asserted it must use `errors.Is` or `errors.As`. The authored checks
(`ValidateAuthoredProperties*`) do not judge whether a type exists: a plain type that nobody
declares and no handler or rule claims passes them, in every position, and `Parse`'s allowlist or
`Transform` refuses it. `ResolveTypes` writes each declared type as its identity, not as the
plain registered name, so feeding its result back to `Transform` resolves to the same type even where
a plain name is ambiguous. `Parse` and `ParseWithExtraTraitTypes` know no identity: use
`ParseWithExtraTypes` with the transformer's `LowerableTypes()`, as `kurel build` does.

