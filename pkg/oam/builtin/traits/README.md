# OAM Built-in Trait Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/traits.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)

Package `traits` implements `oam.TraitHandler` for most built-in trait types, plus one
`oam.TraitLoweringRule` (`expose`, see below). A trait decorates or augments a
component — adding networking, security, storage, scaling, or operational behavior.
Handlers are registered with the transformer in `pkg/cmd/kurel` via
`RegisterBuiltinTrait(type, handler)`; each implements `CanHandle` + `Apply`. `expose`
is registered separately, via `RegisterBuiltinTraitLowering` (`builtinTraitLoweringRules()`
in `pkg/cmd/kurel`) — it lowers into a terminal `ingress` or `httproute` trait rather
than building a resource itself, so it is never also present in the dispatchable
trait-handler map (a lowerable type and a dispatchable handler type are mutually
exclusive by construction; see the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s
Lowering section for the general mechanism). Some traits are **capability-aware**
(`CapabilityRequired`) and draw platform choices (issuer, gateway, secret store) from
the `ClusterProfile` — this applies to both dispatchable handlers and lowering rules.
Every built-in trait handler also implements `oam.PropertySchemaProvider`
(`PropertySchema()`), declaring a constrained schema for its user-facing properties so
the downstream runtime can validate them before invocation. This includes the platform-reserved keys a
handler reads from merged properties (e.g. `networkPolicy`, `allowedHostnameWildcard`,
`controllerType`). Some deeply nested or K8s-adjacent shapes are kept shallow/open
(`additionalProperties`) rather than modeled field-by-field, but strictness-sensitive traits are
**closed**: the `rbac` rule object and the `fluxcd-patches` patch item and its `target` selector
enumerate their fields and set `additionalProperties: false` (unknown keys rejected), matching the
downstream single-owner adoption of these builtins. `prune-protection` accepts no
properties and so declares an empty schema. Every property (including nested object fields and
array item schemas at every depth) carries a `Description`, surfaced in the downstream runtime's generated Handler
API Reference.

Capability-injected fields are **not** marked `Required` in a handler's schema, because
they are supplied by capability rendering (validated in `ValidateAndApplyDefaults`), not by
the OAM author — e.g. `expose.controllerType` and the parent `certificate.issuerRef` are
optional in the user-facing schema (though `issuerRef.name` stays required when `issuerRef`
is present). Marking a capability-injected field user-required would make a consumer's schema
preflight reject every valid use of the trait.

## Trait catalog

### Networking
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `ingress` | Ingress | `rules[]` (`host`, `paths[]`), `ingressClassName`, `tls[]`, `annotations` |
| `httproute` | Gateway API HTTPRoute | `rules[]` (`matches`/`backendRefs`/`filters`/`timeouts`), `hostnames[]`, `annotations`; `parentRefs[]` optional — synthesized from the `gatewayName`/`gatewayNamespace` capability when omitted |
| `expose` | Ingress **or** HTTPRoute | `rules[]`, `hostnames[]` — controller chosen by ClusterProfile (`controllerType`) |
| `networkpolicy` | NetworkPolicy | `ingress[]`/`egress[]` (`from`/`to`, `ports`) |
| `cilium-networkpolicy` | CiliumNetworkPolicy | `name`, `endpointSelector`, `ingress`/`egress` (raw Cilium rules — decoded strictly, see below) |

### Security
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `certificate` | cert-manager Certificate | `secretName`, `dnsNames[]`, `duration`, `renewBefore`, `privateKey` (`algorithm`/`size`/`encoding`/`rotationPolicy`) (issuer from ClusterProfile) |
| `rbac` | Role/RoleBinding (+ClusterRole/Binding) | `rules[]` (`apiGroups`/`resources`/`verbs`), `clusterWide`. The binding subject is the component's effective ServiceAccount via `oam.ServiceAccountNamer` (an authored `serviceAccountName`, else the per-component account); Role/binding object names stay component-derived. |
| `external-secret` | ESO ExternalSecret (+ optional envFrom / volume mount) | `secretName`, `data[]`/`dataFrom[]`, `refreshInterval`, `envFrom`, `mountPath` (store from ClusterProfile or `provider`) |
| `security-context` | (modifies PodSpec) | `psaLevel` (`restricted`\|`baseline`\|`privileged`), optional: `runAsNonRoot`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, `runAsUser`, `runAsGroup`, `fsGroup`. On a pod whose component set `os.name: windows` only the Windows-legal subset is written (see below). |

### Storage
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `pvc` | PersistentVolumeClaim | `name`, `size` (optional; policy default `storageSize`), `storageClassName`, `accessModes[]` (policy: `maxStorageSize`) |
| `volsync` | VolSync ReplicationSource | `sourcePVC`, `schedule`, `copyMethod`, `storageClassName`, `volumeSnapshotClassName`, `retain.{daily,weekly,monthly}` (class fields also supplied via capability rendering; injection is `copyMethod`-aware) |

### Configuration & scaling
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `configmap` | ConfigMap (+ optional volume mount) | `name`, `data`, `mountPath` (mounts into a Deployment, StatefulSet, DaemonSet, Job, or CronJob; any other component fails generation) |
| `scaler` | HorizontalPodAutoscaler (+ optional PDB) | `minReplicas`, `maxReplicas` (both optional; policy defaults `scalerMinReplicas`/`scalerMaxReplicas`, policy cap `maxReplicas`), `cpuUtilization`, `memoryUtilization`, `enablePDB` |

### Operational (FluxCD)
| `type` | Effect | Key properties |
|--------|--------|----------------|
| `fluxcd-patches` | Appends `Kustomization.spec.patches` | `patches[]` (`patch`, `target`) |
| `fluxcd-postbuild` | Sets `Kustomization.spec.postBuild` | `substitute`, `substituteFrom[]` |
| `prune-protection` | Adds `kustomize.toolkit.fluxcd.io/prune: disabled` | (no properties) |

## Capability-aware traits

These require (or optionally use) a `ClusterProfile` capability, so the platform —
not the app — chooses the implementation:

- **expose** (a `TraitLoweringRule`, not a dispatchable handler — see above) →
  `controllerType` (ingress vs gateway) + gateway/ingress details.
  On the **ingress** path, expose is platform-managed for TLS: it derives `spec.tls[]`
  from the rule hosts under a deterministic `<component>-tls` secret and emits the
  `cert-manager.io/cluster-issuer` annotation from the `certManagerClusterIssuer`
  capability field (empty ⇒ managed TLS disabled). Users do **not** author the TLS
  block on the expose trait (use the low-level `ingress` trait for full TLS control),
  but may author `secretName` to override just the managed secret's name (still
  ingress-only, hosts stay rule-derived, and it requires the cluster-issuer capability;
  a `secretName` on the gateway path or without managed TLS is a `ValidationError`).
  This lets a component carry several expose ingress traits (distinct `name`/`scope`)
  each naming its own cert secret. Both paths
  validate user hostnames against the `allowedHostnameWildcard` capability field (empty ⇒
  no validation); a violation is a `ValidationError`.
  Both paths accept a bare `hostnames: [...]` shorthand when `rules` is absent, each
  expanding it the way its own controller expects: ingress gets one rule per host with
  `path: /` + the component service port and drops `hostnames`; gateway gets a single
  catch-all rule backed by the component service and keeps `hostnames` on the route,
  which is where a Gateway API route matches them (supply `rules` for finer control;
  both together keep `rules` for routing while all hosts are still
  wildcard-validated). Platform-default `ssl-redirect` / `force-ssl-redirect`
  come from the `sslRedirect` / `forceSslRedirect` capability fields (author-overridable via
  the same inline properties; the typed value wins over a raw same-key annotation).
  External-auth (oauth2-proxy): authoring `allowedGroups: [...]` on an ingress expose emits the
  nginx `auth-url` / `auth-signin` / `auth-response-headers` annotations from the capability's
  `authURL` / `authSigninURL` / `authResponseHeaders` (`authSigninURL` is override-able inline;
  `authURL` must be a bare base URL). `allowedGroups` must be non-empty, and the capability must
  supply `authURL` or the trait is rejected.
- **certificate** → `issuerRef` (cert-manager issuer/cluster-issuer).
- **external-secret** → `secretStoreRef` (or the inline `provider` shorthand).

  `data[]` entries derive by absence: a bare `- secretKey: FOO` defaults
  `remoteRef.key` to `"<namespace>/<secretName>"` and `remoteRef.property` to
  `secretKey`; author any `remoteRef` field to override. Because absence is meaningful,
  unknown keys in an entry or its `remoteRef` are rejected (naming the supported
  fields) rather than silently ignored. See
  [External Secret Shorthand](/concepts/oam-external-secret-shorthand/).

  The produced Secret is otherwise emit-only — nothing references it unless the trait is told
  to. Set `envFrom: true` and/or `mountPath: <path>` to inject it into the component's workload
  (Deployment, StatefulSet, DaemonSet, Job, or CronJob): `envFrom` wholesale-injects the Secret into
  the first container via `envFrom[].secretRef`, and `mountPath` mounts it as a volume on the
  first container at that path. Both may be set together. `envFrom` cannot be combined with the
  top-level `remoteRef` shorthand: the shorthand derives its single `data[]` entry's `secretKey`
  from `secretName`, which is a Secret *name*, not a valid environment variable name — author
  explicit `data[]` entries with their own `secretKey` values instead. When `envFrom` is set,
  every authored `data[].secretKey` (and any `target.template.data` key) must satisfy
  Kubernetes' `IsEnvVarName`, since it becomes an env var name in the container; `dataFrom[]`
  keys are exempt from this check because they are extract/find queries resolved by ESO at
  runtime, so the keys they ultimately produce aren't known at render time. When `mountPath` is
  set, the produced Secret's name (`secretName`, or `targetSecretName` if overridden) must be a
  valid DNS-1123 label, because it becomes the injected volume's name; a dotted or otherwise
  non-label-safe name is rejected at render time rather than producing an invalid Volume. A
  `mountPath` already used by another decorator's volume (e.g. `configmap`) is also rejected at
  render time — even when the two volumes have different names, Kubernetes requires every
  `VolumeMount.mountPath` in a container to be unique.

## Auto-synthesized NetworkPolicy

Routing traits (`ingress`/`httproute`/`expose`) can surface platform-reserved
`networkPolicy.trafficSources`, which the OAM layer collects to synthesize a
matching `NetworkPolicy` (see [`pkg/oam/netpol`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/netpol)).

When a routing trait's `backendRefs` (httproute) or path `backend` (ingress) names a **separate**
in-bundle backend Service rather than the exposing component's own, the synthesized
`{comp}-allow-ingress-traffic` allow is **retargeted onto the backend component's pods** + the
backendRef port — so router→backend traffic is allowed under a namespace default-deny. The backend
Service name is resolved to a sibling OAM component in the same bundle (a component's Service name
is its `BackendServiceName()` when it declares one, else its component name); a backendRef that
resolves to no in-bundle component is **left authored**. Resolution assumes the sibling's Service
port equals its container port, which holds for all builtin components (e.g. webservice sets
`TargetPort == Port`).

A backend that names a **bare external Service** (no owning OAM component in the bundle) cannot be
resolved to a selector by name. To synthesize an allow for it, add an explicit authorable
`backendSelector` (matchLabels only) beside the backend reference —
`rules[].paths[].backendSelector` (ingress/expose) or `rules[].backendRefs[].backendSelector`
(httproute):

```yaml
paths:
  - path: /
    backend: external-svc      # a Service with no OAM component
    port: 8081
    backendSelector:
      matchLabels:
        app.kubernetes.io/name: external
```

This emits a `{service}-allow-ingress-traffic` policy in the router's namespace selecting the
backend's pods on the backend ports. Without a `backendSelector`, an external backend stays authored
(no selector is ever inferred from the Service name). A `backendSelector` on a self/implicit backend
is rejected (it could never take effect), and a `backendSelector` on a ref that resolves to a
sibling component is ignored (component-label retargeting wins). Same-namespace only.

## Extending

Custom traits implement `oam.TraitHandler` (`CanHandle` + `Apply`), optionally
`CapabilityAware` + `ValidateAndApplyDefaults` for capability validation. A trait that
needs to expand into one or more OTHER traits (or components/policies) before any
handler runs — rather than building a resource itself — implements
`oam.TraitLoweringRule` (`TraitType` + `LowerTrait`) instead, registered via
`RegisterTraitLowering`; `expose` (`expose_rule.go`, above) is the built-in example.
See the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s Lowering
section for the full mechanism (registration, the fixpoint, and
`PropertySchemaProvider`/`CapabilityAware` enforcement).

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
for the full config-field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the interfaces, and `examples/` for runnable applications.

## The security-context trait on a Windows pod

`security-context` writes its profile at `Generate` time, after the component
has already produced the pod template, so it is the last writer of every
`SecurityContext` in the pod. When that template declares `os.name: windows`
(the pod-level `os` property on any workload kind), Kubernetes rejects a pod
carrying `seccompProfile`, `capabilities`, `allowPrivilegeEscalation`,
`readOnlyRootFilesystem`, `runAsUser`, `runAsGroup` or `fsGroup`, so the trait
writes only what Windows accepts: a non-nil pod and container context carrying
`runAsNonRoot` alone. The context stays non-nil so a downstream nil-only
backfill still skips it. The component's own `os` validation cannot cover this
— it runs before the decorator — and upstream Pod Security Admission skips the
same Linux-only controls for Windows pods. An override the author set
*explicitly* that Windows forbids is an error rather than a silent drop, so a
Windows workload asking for `fsGroup` fails at `Generate` instead of emitting a
manifest the API server refuses.

## Decorator forwarding for layout-augmenting components

A component config that also implements kure's `layout.LayoutAugmenter` (e.g. `helmchart` under
`valuesMode: configMap` or `delivery: template`) can carry any trait. `wrapIfAugmenter`
(`decorator.go`) is the shared construction-site helper every trait decorator calls: if the
wrapped inner config implements `layout.LayoutAugmenter`, it returns an `augmentingDecorator`
wrapping the trait-specific decorator instead of the plain one, so the wrapper itself also
satisfies `layout.LayoutAugmenter` and forwards `AugmentLayout` straight through to the inner
config. Without this forward, kure's layout walker — which keys a structural decision off
`layout.LayoutAugmenter`'s mere *presence* on the concrete config it walks — would never see the
capability on a decorated (trait-carrying) config, silently losing the augmenter's layout-level
effect (the values `ConfigMap`, or the hook-group repartitioning) the moment any trait is added.

Every trait decorator also embeds `decoratorBase`, which forwards the optional
interfaces a component config may implement — `stack.Validator`,
`fluxNamespaceSettable`, `autoHealthCheckEmitter`, `servicePortProvider`,
`serviceBackendNamer` and `oam.ServiceAccountNamer` — so a decorated config
keeps answering them. The `ServiceAccountNamer` forward is what keeps the
`rbac` row above true once a second trait is present: without it a workload
that authored `serviceAccountName` would stop reporting its account as soon as
any trait wrapped it, and `rbac` would silently bind the per-component name
instead. A config that implements none of them gets the zero answer (`nil`,
`0`, `""`), which every reader treats as "not set".

`augmentingDecorator` also forwards `oam.LayoutAugmentationCoverage`'s
`GenerateCoversAugmentLayout() bool` — the interface `kurel build`'s guard consults before
rejecting a `LayoutAugmenter` config it cannot walk (see the [OAM model
README](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)). Unlike the `AugmentLayout`
forward, this one is **unconditional**: it type-asserts the inner augmenter to
`oam.LayoutAugmentationCoverage` and returns `false` if that assertion fails, rather than omitting
the method. `AugmentLayout`'s forward is conditional because kure's walker treats the method's
*presence* as a structural signal; `GenerateCoversAugmentLayout`'s presence carries no such
meaning, and the guard already treats "method absent" and "method returns `false`" identically —
so a conditional forward here would only reintroduce the exact silent-loss failure mode the method
exists to prevent, this time for any trait-decorated `delivery: template` component (e.g. one
carrying `prune-protection`), which would otherwise be wrongly rejected by `kurel build`.

## Component attribution

Every trait sub-app config exposes the OAM component it was emitted for via
`ComponentName() string` (the `oam.ComponentNamed` interface) — always the component
name, never the sub-app or K8s Service name. Consumers use it to stamp per-resource
provenance (the derived `<domain>/component` label) without re-deriving the component
from sub-app names, which several handlers author from properties rather than
`<component>-<suffix>`. The routing traits' existing `TargetComponentName()` (used by
auto-NetworkPolicy synthesis) delegates to the same accessor; auto-synthesized
NetworkPolicies target that `<domain>/component` label by default (domain from
`TransformContext.Domain`, library default `gokure.dev`;
`TransformContext.ComponentLabelKey`-overridable).

## Raw Cilium rules are decoded strictly

`cilium-networkpolicy` passes `endpointSelector`, `ingress` and `egress` through to
Cilium's `api.Rule` as opaque shapes, so the trait schema cannot validate them. They are
decoded with `DisallowUnknownFields`: a property the linked Cilium API version does not
recognise makes the build fail, naming the rejected field.

This is deliberate. Lenient decoding silently **widened** policies whenever Cilium removed
an API field — v1.20 dropped `kafka`, `l7proto` and `l7` from `api.L7Rules`, so a rule
carrying them would have rendered as L4-only with no error, producing a policy more
permissive than authored. Failing the build is the safe direction for a policy generator.

Practical consequence: a package pinned to rule shapes from an older Cilium release will
fail to build after a Cilium major bump rather than quietly losing its L7 constraints.
Rewrite the affected rules to the shapes the new API supports.

**Known gap.** `encoding/json` does not propagate `DisallowUnknownFields` into types that
define their own `UnmarshalJSON`. In this API those are `EndpointSelector` and `ICMPField`,
so unknown keys nested inside `endpointSelector` or `icmps` are still dropped silently. The
`toPorts.rules.*` shapes that motivated the guard are covered.

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.

A trait that generates several objects gives each one its own label map, never one map
shared between them. These maps leave the package on objects the caller owns and edits,
so a shared map turns a label added to the Role into a label on the RoleBinding, or one
added to the HPA into a label on the PDB. The same rule and the reason behind it are in
the Conventions section of the component handlers' README
(`pkg/oam/builtin/components/README.md`).
