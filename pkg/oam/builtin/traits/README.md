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
**closed**: the `rbac` rule object
enumerates its fields and sets `additionalProperties: false` (unknown keys rejected), matching the
downstream single-owner adoption of these builtins. `prune-protection`, `topology-spread` and
`force-replace` accept no properties of their own and so declare an empty schema (the
engine-owned `scope`, legal on every trait, is still accepted); for `prune-protection` and
`force-replace` any other authored key is a build error. Every property (including nested object fields and
array item schemas at every depth) carries a `Description`, surfaced in the downstream runtime's generated Handler
API Reference.

Every built-in trait handler, and the `expose` rule, implements `oam.ContractDescriber`
(go-kure/launcher#789): family is the trait type, version is `builtin.ContractVersion`
(`v1alpha1`), and `RequiredCapabilityKeys` is the trait type for the two whose
`CapabilityRequired` returns `true` (`certificate`, `expose`) and empty for the rest. The
`expose` rule also implements `oam.LoweringTargetDeclarer`: it lowers into the `ingress` and
`httproute` traits, so `Transformer.Seal`, which every transform runs first, refuses a
registry that holds `ExposeRule` without both, with `registry incomplete: lowering rule
trait/expose@v1alpha1 lowers into trait type "httproute", which is not registered`.
**Breaking library change**: a consumer that registered `ExposeRule` with one of the two
handlers only now fails at the first transform.

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
| `networkpolicy` | NetworkPolicy | `ingress[]`/`egress[]` (`from`/`to`, `ports`), `name` (optional; the policy's name, default `<component>-allow`) |
| `cilium-networkpolicy` | CiliumNetworkPolicy | `name`, `endpointSelector` (required), `ingress`/`egress` (raw Cilium rules, at least one rule between them — decoded strictly, see below) |

### Security
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `certificate` | cert-manager Certificate | `secretName`, `dnsNames[]`, `duration`, `renewBefore`, `privateKey` (`algorithm`/`size`/`encoding`/`rotationPolicy`) (issuer from ClusterProfile) |
| `rbac` | Role/RoleBinding (+ClusterRole/Binding) | `rules[]` (`apiGroups`/`resources`/`verbs`), `clusterWide`, `name` (optional). The binding subject is the account the component's pods run as, via `oam.ServiceAccountNamer`: an authored `serviceAccountName`, or a `webservice`/`worker`'s generated account. A pod kind (`deployment`, `statefulset`, `daemonset`, `job`, `cronjob`) without `serviceAccountName` generates no account (go-kure/launcher#702), so `rbac` on it is refused (`rbac: component "x" runs as no ServiceAccount of its own; set serviceAccountName to the existing ServiceAccount the rules are granted to`) rather than bound to an account that does not exist. A component that runs no pods keeps the component name as the subject. The objects are named after the component unless `name` is authored: the one `name` names the Role, the RoleBinding and, with `clusterWide`, the ClusterRole and the ClusterRoleBinding, and is the `roleRef.name` of both bindings (go-kure/launcher#787; see Conventions). It names neither the subject nor the `app` label, which stay the component's. |
| `external-secret` | ESO ExternalSecret (+ optional envFrom / volume mount) | `secretName`, `data[]`/`dataFrom[]`, `refreshInterval`, `envFrom`, `mountPath` (store from ClusterProfile or `provider`) |
| `security-context` | (modifies PodSpec) | `psaLevel` (`restricted`\|`baseline`\|`privileged`), optional: `runAsNonRoot`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, `runAsUser`, `runAsGroup`, `fsGroup`. Applies to the pod spec of a Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob, or a `pod` component's Pod. On a pod whose component set `os.name: windows` only the Windows-legal subset is written (see below). |

### Storage
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `pvc` | PersistentVolumeClaim | The `persistentvolumeclaim` kind's twin: every claim field below is parsed, defaulted and built by the kind's own code, so both build the same claim; the trait adds only the claim's `name`, the owner's `app` label, namespace and bundle (go-kure/launcher#741). `name` (a DNS-1123 subdomain), `size` (optional; policy default `storageSize`; the effective size must be a positive quantity — zero or negative fails the build, as `ValidatePersistentVolumeClaimSpec` would refuse the claim), `storageClassName` (a DNS-1123 subdomain; an authored `""` requests no class, i.e. no dynamic provisioning, and is emitted as `storageClassName: ""`; unset or `null` takes the ClusterProfile `pvc` capability's `storageClassName`, as the kind's does (go-kure/launcher#742), else the cluster default. **Pre-GA output change** (go-kure/launcher#702): `""` used to be treated as unset), `accessModes[]` (`ReadWriteOncePod` must be the only mode), `volumeMode` (optional `Filesystem`\|`Block`; omitted leaves the claim's mode unset, which the apiserver defaults to `Filesystem`; any other value or type fails the build) (policy: `maxStorageSize`). **Pre-GA tightening** (go-kure/launcher#741): a `name` or `storageClassName` that is not a DNS-1123 subdomain, and `ReadWriteOncePod` combined with another mode, used to build and are now refused, as the kind refuses them; a zero or negative `size` is refused when the trait is applied instead of at generation |
| `volsync` | VolSync ReplicationSource | `sourcePVC`, `schedule`, `copyMethod`, `storageClassName`, `volumeSnapshotClassName`, `retain.{daily,weekly,monthly}` (class fields also supplied via capability rendering; injection is `copyMethod`-aware) |

### Configuration & scaling
| `type` | Produces | Key properties |
|--------|----------|----------------|
| `configmap` | ConfigMap (+ optional volume mount) | The `configmap` kind's twin: `data`, `binaryData` and `immutable` are parsed and the ConfigMap built by the kind's own code, so both build the same ConfigMap and refuse the same input; the trait adds only the ConfigMap's `name`, the owner's `app` label, namespace and bundle, and the mount (go-kure/launcher#741). `name`, `mountPath` (mounts into a Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob, or a `pod` component's Pod; any other component fails generation), `data` (string values only), `binaryData` (base64; a key may not also appear in `data`), `immutable`. The `data` values and decoded `binaryData` values may total at most 1,048,576 bytes, the API server's ConfigMap limit; more is refused at build time. Keys must be valid ConfigMap keys (alphanumerics, `-`, `_`, `.`, at most 253 characters, not `.` or `..` or starting with `..`); an invalid key is refused at build time, the first in sorted order. **Pre-GA tightening** (go-kure/launcher#741): a number or boolean `data` value used to be stringified and is now refused, as the kind refuses it; quote it. |
| `topology-spread` | (modifies the Deployment's PodSpec) | (no properties; an authored engine-owned `scope` is accepted; a capability rendering carries no keys). Stamps launcher's default topology spread constraints — the ones `webservice` and `worker` apply from `topologySpread` — onto every typed Deployment the component generates (one a launcher kind builds, or one decoded from a `manifests` source or a `helmtemplate` chart render), from its post-policy `spec.replicas`: none at 1 replica, a hostname spread from 2, a zone spread added from 3. Refuses a Deployment that already carries constraints or whose selector is not `matchLabels` alone, and a component with no typed Deployment (a `pod` component, for one: the trait is Deployment-only); a Deployment passed through as raw, unstructured output (`passthrough`) is not inspected (see below). |
| `scaler` | HorizontalPodAutoscaler (+ optional PDB) | `minReplicas`, `maxReplicas` (both optional; policy defaults `scalerMinReplicas`/`scalerMaxReplicas`, policy cap `maxReplicas`), `cpuUtilization`, `memoryUtilization`, `enablePDB`, `hpaName` and `pdbName` (optional; the objects' names, default `<component>-hpa` and `<component>-pdb`; `pdbName` without `enablePDB: true` names no object and is refused). Admitted on `webservice`, `worker` and `deployment` only. On any of them with a non-RWX claim (the claims that cap the component at one replica, see the components README's "Non-RWX volumes"), an effective `maxReplicas` above 1 fails the build, naming the trait and the claim: the HPA would otherwise scale the Deployment past the one pod the claim allows. |

### Operational (FluxCD)
| `type` | Effect | Key properties |
|--------|--------|----------------|
| `prune-protection` | Sets the `PruneProtection` delivery intent on the component's applications (`stack.Application.Delivery`): their objects stay in the cluster when they are removed from the source. Writes nothing on the objects; kure's Flux workflow turns the intent into `kustomize.toolkit.fluxcd.io/prune: disabled` on each of them. | (no properties) |
| `force-replace` | Sets the `ForceReplace` delivery intent: an object whose update fails on an immutable field (a `job`'s pod template) may be deleted and recreated. Writes nothing on the objects; kure's Flux workflow turns the intent into `kustomize.toolkit.fluxcd.io/force: enabled` on each of them. Replacing a Job re-runs it and stops any run in progress. Replacing a PersistentVolumeClaim or PersistentVolume can lose its data, so each one under the intent gets a build warning. Opt-in: without the trait launcher states no intent. | (no properties) |

`fluxcd-patches` and `fluxcd-postbuild` are not built in. They set `spec.patches` and
`spec.postBuild` of the Flux Kustomization that delivers a bundle, and launcher sets no Flux
delivery field on the bundles it returns (go-kure/launcher#781; see `docs/delivery-scope.md`).
A document using one fails the transform with `no handler for trait type "fluxcd-patches" (on
component "<name>"): it configures delivery, which launcher leaves to the consumer that
delivers the application; a consumer that delivers through Flux registers its own handler`. Such a consumer registers its
own trait handler (`RegisterTrait`) and applies the result to the delivery objects it generates.

`prune-protection` and `force-replace` cover every object the component owns, by a delivery
intent rather than by an annotation (go-kure/launcher#782). Each sets its field of
`stack.DeliveryIntent` on the component's application and on every sub-application its other
traits append to the bundle — the applications of `pvc`, `configmap`, `volsync`, `certificate`,
`ingress`, `httproute`, `rbac`, `scaler`, `networkpolicy`, `cilium-networkpolicy` and
`external-secret`. Trait order does not matter: both implement `oam.SubApplicationDecorator`, and
the engine applies them to the component's sub-applications in a last build step, after every
trait of every component has run. A sibling group is delivered as one application, and that
application takes each intent that any of its members has: a lowering rule that forwards the
trait to one member only still covers the whole group, its sub-applications included.
The NetworkPolicies the engine synthesizes (default-deny, inbound and egress allows) are not
covered: they belong to no component's traits and are regenerated on every build, so pruning
them stays correct. Neither is another component's sub-application.

The traits write nothing on the objects launcher returns. The workflow that delivers an
application reads its intent. kure's Flux workflow annotates everything the application's layout
holds, where it integrates a layout (`CreateLayoutWithResources`, `IntegrateWithLayout`): what
the application generates, wherever a layout augmenter places it (a `helmtemplate` component's
hook groups, which its `Generate` returns and its augmenter moves into child layouts), what an
augmenter adds outside `Generate` and, as `options.annotations`, the `configMapGenerator`s of
those layouts. kure refuses a set intent where it cannot map it: its Flux
`GenerateFromCluster`, which returns none of the application's objects, and a workflow with no
mapping. Under prune protection each content change of a
generator-built ConfigMap leaves the old hash-named ConfigMap behind: kustomize names such a
ConfigMap with a hash of its content, so a change makes a new object, and the previous one, no
longer in the source, is kept. That is the base library's behaviour (kure's `pkg/stack/fluxcd`
maps the intent, in `applyDeliveryIntents`), not launcher's; no built-in component adds a
generator. A consumer that applies launcher's objects without such a workflow reads
`Application.Delivery` itself, or the two traits have no effect.

`force-replace` covers claims too — a component's `volumes` claims and the `pvc`
sub-application's (`volsync` generates none; it backs up an existing claim): where the
delivery workflow maps the intent, a claim whose immutable field changes is deleted and
recreated, losing its data unless its volume is retained, so `Transformer.WarnForcedVolumes`
(which `kurel build` runs) warns once per PersistentVolume and PersistentVolumeClaim under
the intent: in those conditional terms when the intent is its only reason, as force-applied
when the object also carries the Flux force annotation or its bundle sets `Force`
(go-kure/launcher#720; see the `pkg/oam` README).

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
  (Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob, or a `pod` component's Pod): `envFrom` wholesale-injects the Secret into
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
  `VolumeMount.mountPath` in a container to be unique. The same applies, for both `external-secret`
  and `configmap`, to a path the workload's main container already uses as the `devicePath` of a
  raw block volume (`volumeMode: Block`): Kubernetes refuses a `devicePath` that is also a
  `mountPath` in the same container. Both traits also refuse a volume name the main container
  already attaches as a raw block device — a `statefulset` Block claim template has no pod volume
  of its own, so the ordinary volume-name check would miss it — because Kubernetes refuses a
  volume listed under both `volumeMounts` and `volumeDevices`. A filesystem claim template has no
  pod volume either, only a main-container mount, so both traits refuse a volume named like one
  too: the StatefulSet controller replaces a pod volume named like a claim template with the
  claim, and the ConfigMap or Secret would never be mounted.

## Ingress implicit backend ports

An `ingress` path with no `backend`, or with a `backend` naming the component's own
Service, routes to the component's implicit backend: the one Service port the component
exposes. A path may address it by number (`port`) or by name (`portName`), and either
must be that port. Any other number is refused (`cannot route implicit backend to port
N`), and so is any other name (`cannot route implicit backend to port "name"`), at build
time instead of building an Ingress whose backend port cannot resolve
(go-kure/launcher#545). The name each kind's Service gives that port:

| Component kind | Implicit backend port name |
|----------------|----------------------------|
| `webservice` | `http` |
| `service` | the first port's own `name` (so a later port's name is refused too) |

A `backend` naming a different Service is explicit and its `port`/`portName` is not
checked. A component that exposes no Service port — `deployment`, `statefulset` and
`daemonset` (which dropped `port` and its Service in go-kure/launcher#690), or another
kind that generates no Service such as `helmrelease` — has no implicit
backend unless the trait sets `servicePort` (and optionally `serviceName`); that
trait-level port carries no name, so a `portName` is not checked against it. A
port-less headless `service` (`clusterIP: None` with no `ports`,
go-kure/launcher#690) is the exception: its Service knows it has no port, so a
trait-level `servicePort` on it is refused (`servicePort may not be set on
component "db": its Service has no ports to route to`) rather than routed to a
port the Service lacks.

## NetworkPolicy nulls: null, empty and absent

In a `networkpolicy` peer (`ingress[].from[]` / `egress[].to[]`), `podSelector`,
`namespaceSelector` and `ipBlock` are optional objects where **absent and empty are
opposite answers**, because that is what `networking.k8s.io/v1` means by them: an
**empty** `namespaceSelector` (`{}`) matches **every** namespace, while an **absent**
one does not constrain namespaces at all — which, for a peer that *also* sets
`podSelector`, leaves it scoped to the policy's own namespace (`k8s.io/api`
`networking/v1/types.go:199-222`).

The "own namespace" reading belongs to that pairing, not to the null on its own. A
peer with **no** selector and no `ipBlock` is not a narrow peer: it names no peer
at all, and the type's own summary is that "only certain combinations of fields
are allowed" (`types.go:197-198`). The API server refuses it, so the parser does
too (see [Peer shape](#peer-shape) below). Read a null selector as absence first,
then ask what the peer has left.

An explicit `null` is **absence**, following the contract `oam.IsNullValue` carries
(see [`pkg/oam`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)) — so
`namespaceSelector:` with no value is the same as omitting the key, and never the
same as `namespaceSelector: {}`:

```yaml
from:
  - podSelector: {matchLabels: {app: web}}
    namespaceSelector:            # null -> absent: web pods in the policy's own namespace
  - podSelector: {matchLabels: {app: web}}
    namespaceSelector: {}         # empty -> web pods in EVERY namespace
  - namespaceSelector:            # populated -> every pod in namespaces carrying the label
      matchLabels: {env: prod}
```

This holds for a **typed** nil too — an uninitialized Go map from a lowering rule
that constructs peer properties directly, which a plain type assertion would accept
as an authored empty object and silently widen to every namespace
(go-kure/launcher#430). A `null` `ipBlock` is likewise absent rather than an
`ipBlock: 'cidr' is required` error; a **present** `ipBlock` still requires `cidr`.

A value of the **wrong type** is the third answer, and it is an error rather than a
silent drop — `namespaceSelector: prod`, or a `matchLabels` that is not a mapping,
is rejected as `…: expected object, got string`. Discarding it left the selector
allocated with no labels, and an empty selector matches every namespace, so a
malformed constraint used to widen the peer to the maximum at render time.

An **unrecognized key** is the same answer for the same reason, at every object
depth this parser reads: the rule (`from`/`to`, `ports`), the peer
(`podSelector`, `namespaceSelector`, `ipBlock`), the selector (`matchLabels`),
the `ipBlock` (`cidr`, `except`) and a `ports` item (`port`, `protocol`). Each is
rejected as `…: unsupported key "…"`, naming the lexicographically first offender
so the diagnostic is the same on every run:

```yaml
from:
  - namespaceSelector:
      matchExpressions: [...]     # rejected: this parser implements matchLabels only
  - ipBlock: {cidr: 10.0.0.0/8, exclude: [10.1.0.0/16]} # rejected: the key is `except`
```

`matchExpressions` is the case worth spelling out: it is a real
`metav1.LabelSelector` field, so a document written against Kubernetes' own schema
used to parse into a selector with **no** labels — which matches every namespace.
A constraint this parser cannot honour must fail, never widen. `endPort` on a
`ports` item is the same case: a real `NetworkPolicyPort` field this parser does
not implement, which accepted silently would render a single port where a **range**
was authored.

The trait's own **top-level** property map — `ingress` and `egress` — is closed one
layer further out, by the engine's schema check rather than by this parser, so a
typo'd `egres` is reported as `unsupported field "egres"` rather than dropped along
with the whole direction it carried. `kurel build` runs that check over the
authored document before transform (`pkg/cmd/kurel/build.go`), and the lowering
fixpoint runs it over every emitted trait (`pkg/oam/lowering.go`). A consumer that
calls `NetworkPolicyHandler.Apply` directly, without either, is the gap tracked in
go-kure/launcher#394.

The same applies one depth further down, to a `matchLabels` **value**. `env:` with
no value is rejected as `…matchLabels: "env" has no value`, and a **composite**
value — a mapping or a list — as `…matchLabels: "env" must be a string, number or
boolean, got …`. Both used to reach `%v` and render `<nil>`, `map[a:1]` or `[x y]`
into a label the API server then refuses, one layer away from the cause. A scalar
still renders as before — `port: 8080` is the label value `"8080"`, and booleans
and every numeric kind a YAML or JSON decoder produces render the same way — but
the result must then pass the content check described under "Label and CIDR
content" below.

The peer **envelope** itself is the one place in this section where a null is an
**error**, not absence:

```yaml
from:
  -                               # null: rejected, `ingress[0].from[0]: expected object`
```

A peer sits in a list, so "absent" has no meaning for it — dropping the element
would silently shrink the rule. This is what an untyped `nil` in that position
always did; the typed nil now agrees with it instead of being accepted as a peer
that names no source at all. An authored `- {}` is present but names no peer
either, and is rejected by the shape rule under [Peer shape](#peer-shape).

The same holds one level up, for an element of the `ingress`/`egress` **rule**
list, where it matters more: a rule with neither `from`/`to` nor `ports` matches
**all** sources on **all** ports, so a silently-accepted null rule widens the
policy to allow-all rather than narrowing it.

```yaml
ingress:
  - {}                            # a present, empty rule: allow-all, authored on purpose
  -                               # null: rejected, `ingress[1]: expected object`
```

A rule's `from`/`to` and `ports` are read the same way, and for the same reason:
each is a **constraint**, so a mistyped one used to be discarded and leave the
rule matching everything on that axis. A null is absence (an absent `from` *is*
the authored allow-all), an authored `[]` is that same value written down, and a
wrong-typed value is an error:

```yaml
ingress:
  - from:                         # null -> absent: this rule allows all sources
    ports: [{port: 8080}]
  - from: web                     # rejected: `ingress[1].from: expected array, got string`
  - frm: [...]                    # rejected: `ingress[2]: unsupported key "frm"`
```

Parsing stops at the first rule that fails, so this document reports the
`ingress[1]` error and never reaches `ingress[2]`; the index in each diagnostic is
the offending rule's own position in the list it was authored in.

`ipBlock.except` behaves the same, one level down, and is the clearest case of the
class: `except` is an **exclusion**, so a dropped one renders a block strictly
wider than the document authored. A mistyped `cidr` now reports itself as mistyped
(`…ipBlock.cidr: expected string, got int`) instead of as missing.

A `ports` item is the last depth. `port` is an int-or-string union, published
as `Types: [integer, string]` (go-kure/launcher#383) like the rolling-update
`maxUnavailable`/`maxSurge` knobs of the workload components, so a value of any
other type — `port: true` — is rejected by property validation before the
parser sees it. It is a declared property (not left to
`AdditionalProperties`), so the schema closes this key set too and a `protcol`
typo is rejected before ever reaching the parser (go-kure/launcher#440 round
3). Both of its fields carry the same rule as everything above:

```yaml
ports:
  - {port: 53, protocol: [UDP]}   # rejected: `…ports[0].protocol: expected string, got []interface {}`
  - {port: 53, protcol: UDP}      # rejected: `…ports[1]: unsupported field "protcol" (allowed: port, protocol)`
  - {port: 80.9}                  # rejected: `…ports[2]: 'port' must be a whole number, got 80.9`
  - {port: 4294967376}            # rejected: `…ports[3]: 'port' 4294967376 is out of range (1-65535)`
```

A wrong-typed `protocol` was **discarded**, and an absent protocol means TCP, so
`protocol: [UDP]` rendered a policy permitting TCP and denying the UDP the document
asked for. A numeric `port` went through a bare `int32(…)` conversion, which
truncates a fractional value and is implementation-defined outside `int32` range:
`80.9` rendered port 80, and `4294967376` also rendered port 80. A null or absent
`protocol` still means TCP, and every integer, float, or string kind a decoder
or a lowering rule can produce is accepted — including a named Go numeric or
string type, matching `matchLabels`' reach (go-kure/launcher#440 rounds 2-3):
a named-port string and a `protocol` assembled as a `corev1.Protocol` both
parse the same as their builtin-typed equivalents.

### Label and CIDR content

Everything above checks **type** and **nullity**. A well-typed value is also
checked for **content**, against the validators the API server applies to a
NetworkPolicy peer's labels and CIDRs, so a label or CIDR that renders is one
the cluster admits (go-kure/launcher#469). The combination of fields a peer
sets is a separate check, under [Peer shape](#peer-shape). The content rules:

- A `matchLabels` **key** must be a qualified name (`app.kubernetes.io/name`), and
  the rendered **value** a valid label value: empty, or at most 63 characters of
  alphanumerics, `-`, `_` and `.`, starting and ending with an alphanumeric. A
  number is checked as rendered, so `1000000.0` (`"1e+06"`), `-1` and `.inf`
  (`"+Inf"`) are rejected, and the diagnostic names the type they were rendered
  from.
- `ipBlock.cidr` and each `except` entry must be a CIDR, and each exception a
  **strict subset** of `cidr`: inside it, with a longer prefix, in the same address
  family. The check follows the pinned Kubernetes minor, where strict CIDR
  validation is on by default (it is from 1.36), so `10.0.0.1/8` (bits set past the prefix),
  `010.0.0.0/8` and an IPv4-mapped IPv6 CIDR are rejected as well. Non-canonical
  IPv6 text such as upper-case hex is still accepted, as it is upstream.

```yaml
from:
  - podSelector: {matchLabels: {size: 1000000.0}}      # rejected: `…matchLabels: "size" has invalid label value "1e+06" (rendered from float64): …`
  - podSelector: {matchLabels: {"bad key": web}}       # rejected: `…matchLabels: invalid label key "bad key": …`
  - ipBlock: {cidr: not-cidr}                          # rejected: `…ipBlock.cidr: invalid CIDR "not-cidr": …`
  - ipBlock: {cidr: 10.0.0.0/8, except: [10.0.0.0/8]}  # rejected: `…ipBlock.except[0]: "10.0.0.0/8" must be a strict subset of cidr "10.0.0.0/8"`
```

Each of these used to render, and fail only when the manifest was applied.

### Peer shape

A peer must name something, and an `ipBlock` stands alone. These are the two
structural rules the API server applies to a peer (`ValidateNetworkPolicyPeer`),
and the parser applies them in the same words, after treating a null
`podSelector`, `namespaceSelector` or `ipBlock` as absent (go-kure/launcher#470).
Each row is shown as if authored on its own:

```yaml
from:
  - {}                                            # rejected: `…: must specify a peer`
  - podSelector:                                  # null -> absent, so no peer is named: rejected the same way
  - ipBlock: {cidr: 10.0.0.0/8}
    podSelector: {matchLabels: {app: web}}        # rejected: `…: may not specify both ipBlock and another peer`
  - podSelector: {}                               # every pod in the policy's own namespace: accepted
  - podSelector: {matchLabels: {app: web}}
    namespaceSelector: {matchLabels: {env: prod}} # the one legal pair: accepted
```

An empty selector (`{}`) counts as set, so `- podSelector: {}` is a peer. A null
selector does not, so an `ipBlock` beside a `podSelector:` with no value is still
legal. Each rejected shape is one where every value is legal on its own, so the
per-field checks above could not see it; both used to render and fail only when
the manifest was applied.

### Null `ingress` / `egress`

The trait's two top-level rule keys are optional individually and **required
jointly** — `at least one of 'ingress' or 'egress' must be specified`. A null reads
as absence *before* that requirement is evaluated, so it cannot satisfy it:

```yaml
ingress:                          # null -> absent, so this document is rejected
```

```yaml
ingress:                          # null -> absent; the policy is the egress rules
egress:
  - to: [...]
```

An **authored empty list** is a different value and still satisfies the requirement:
`ingress: []` means "select this component's pods and permit no ingress", which is a
default-deny somebody asked for. A **typed** nil `ingress` — an uninitialized Go
slice from a lowering rule that builds trait properties directly — used to produce
exactly that policy without anyone asking, because it satisfied the `[]any`
assertion with `ok=true` and a nil slice. An **authored** `ingress:` with no value
never reached that point: it failed the same assertion and reported `'ingress' must
be an array`, a mistyped-key diagnostic for a key that is absent. The two shapes now
agree, and a genuinely non-null value of the wrong type keeps that
`'ingress' must be an array` diagnostic for itself.

### `policyTypes` follows key presence

The rendered `spec.policyTypes` lists a direction when its key is **present** in the
document, not when it has rules. In `networking.k8s.io/v1` a set `policyTypes` is
authoritative: a direction missing from it is not isolated at all. Per direction:

| document | `policyTypes` | rules for that direction |
|---|---|---|
| key absent | not listed | none |
| key null | not listed (null is absence, above) | none |
| key `[]` | **listed** | none — deny all for that direction |
| key with rules | listed | the rules |

```yaml
ingress: []                       # policyTypes: [Ingress, Egress]; no ingress permitted
egress:
  - to:
      - ipBlock: {cidr: 10.0.0.0/8}
```

This used to follow rule count, so the document above rendered `policyTypes:
[Egress]` and left ingress completely open — the opposite of what `ingress: []`
says. A single `ingress: []` with no `egress` hid the defect: it rendered no
`policyTypes` at all, and the API server defaults that to `Ingress`. It now renders
`policyTypes: [Ingress]` explicitly, which enforces the same thing. The same
defaulting made a single `egress: []` isolate **ingress** and leave egress open; it
now renders `policyTypes: [Egress]` (go-kure/launcher#467).

## Auto-synthesized NetworkPolicy

Every integer property is read through `oam.IntegerValue`, so any Go integer kind
(a plain `int32`/`int64` or an unsigned kind from a lowering rule or a Go caller)
renders the same as the `int` a YAML literal decodes to. The value is then
range-checked against its target and refused if it does not fit, never truncated or
wrapped. For routing ports this covers the trait-level `servicePort`, an ingress
`rules[].paths[].port`, and an httproute `rules[].backendRefs[].port`: each must be
1–65535, and an error names the field. A present-but-invalid path or backendRef
`port` used to fall back to the component's port (ingress) or render as-is
(httproute); it is now an error. `externalAuth.forwardBody.maxSize` must fit uint16.

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

The same holds for a trait-level `serviceName` (with `servicePort`) that names a Service the
routing component does not own: every path/backendRef that names no backend of its own routes there,
and synthesis treats it as an external backend. The allow is retargeted onto the component that owns
that Service, on `servicePort`, and the routing component gets none. When no component owns it, it
takes the bare-external-Service path below and — since a trait-level Service carries no
`backendSelector` — stays authored. "Own" is decided from the component alone (its
`BackendServiceName()`, else its component name), so `servicePort` without `serviceName`, or a
`serviceName` equal to the component's own Service, keeps the allow on the routing component.

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

### Null `trafficSources` and `matchLabels`

`networkPolicy.trafficSources` is required once `networkPolicy` is present, and an
authored `trafficSources: []` is the deliberate way to switch synthesis off. A `null`
is **not** `[]`: it is rejected (`expected array, got null`), whatever its Go shape. A
**typed** nil list from a lowering rule used to take the `[]` opt-out path and disable
synthesis with no diagnostic, where an authored null was already an error
(go-kure/launcher#468).

`matchLabels` is likewise required in every matchLabels-only selector parsed here — a
traffic source's `podSelector` and a routing trait's `backendSelector`. A null
`matchLabels` is rejected (`expected object, got null`). In a `podSelector` a typed nil
map used to pass the presence check and render an empty selector, which matches every
pod; a `backendSelector` already refused it, as an empty `matchLabels`.

## Extending

Custom traits implement `oam.TraitHandler` (`CanHandle` + `Apply`), optionally
`CapabilityAware` + `ValidateAndApplyDefaults` for capability validation. A trait that
needs to expand into one or more OTHER traits (or components/policies) before any
handler runs — rather than building a resource itself — implements
`oam.TraitLoweringRule` (`TraitType` + `LowerTrait`) instead, registered via
`RegisterTraitLowering`; `expose` (`expose_rule.go`, above) is the built-in example.
See the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)'s Lowering
section for the full mechanism (registration, the fixpoint, and
`PropertySchemaProvider`/`CapabilityAware` enforcement). Work a lowering rule must
do after the environment policy, which no author should write, is not a trait: the
rule attaches a post-policy step to the component it emits
(`oam.Component.AfterPolicy`, in the same Lowering section).

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

## The topology-spread trait

`topology-spread` makes launcher's default spread opinion available on a kind
that carries none of its own. `webservice` and `worker` apply that opinion from
their `topologySpread` property; `deployment` deliberately does not, and until
this trait the only way to spread a `deployment` was to author the raw
`topologySpreadConstraints`. The trait and the two role kinds share one
definition, `components.BuildTopologySpreadConstraints`, so the output is
identical. `worker` and `webservice` go further and apply their opinion through
this very trait: their component lowering rules (`components.WorkerRule` and
`components.WebserviceRule`, go-kure/launcher#280) emit a `deployment` and,
unless `topologySpread: false`, attach a synthesized `topology-spread` in front
of its authored traits, so it is the innermost decorator — where the former
handlers applied the constraints. An authored `topology-spread` on either
therefore meets the "no merging" rule below exactly as it did before.

| effective replicas | constraints |
|---|---|
| 1 (or unset) | none |
| 2 | `kubernetes.io/hostname`, `maxSkew: 1`, `DoNotSchedule` |
| 3 or more | the above, plus `topology.kubernetes.io/zone`, `maxSkew: 1`, `ScheduleAnyway` |

Each constraint's `labelSelector` is a copy of the Deployment's own
`spec.selector.matchLabels` (`app: <component>` for `deployment`, `webservice`
and `worker`; whatever the manifest declares for a `manifests` Deployment).

The replica count is the one the generated Deployment carries, which is the
count **after** the environment policy ran: the transformer applies the policy
to the component before any trait, and the decorator reads the object at
`Generate` time. A document that authors no `replicas` under a policy
defaulting to 3 therefore gets both constraints, the same as a `worker` would.
A `scaler` HPA does not change the count the trait sees, as it does not for the
role kinds.

The trait is strict in five ways, each an error at build time:

- **No properties.** Any key under the trait is refused by name, whether
  authored or merged in from a `ClusterProfile` capability rendering. The
  exception is the engine-owned keys every trait accepts when authored — today
  `scope`, which selects the `topology-spread.<scope>` capability binding: they
  are read by the transform engine, not by the trait, and are let through.
- **No rendering.** A `topology-spread` capability in the `ClusterProfile`
  carries no rendering: any key there, `scope` included, fails profile
  evaluation, naming the key and the capability. A rendered `scope` is merged
  in after the engine chose the binding, so it could select nothing.
- **No merging.** A Deployment that already has `topologySpreadConstraints` is
  refused rather than merged, whatever put them there: the raw property on
  `deployment`, or the `topologySpread` default of `webservice`/`worker` at 2
  or more replicas. Set `topologySpread: false` on a role kind to use the trait
  instead. At one replica the role kinds produce no constraints, so there is
  nothing to conflict with.
- **A typed Deployment is required.** The trait acts on the typed Deployment
  objects a component's `Generate` returns: the one `deployment`, `webservice`
  or `worker` builds, and any `apps/v1` Deployment in a `manifests` source or
  in the render of a `helmtemplate` chart, which is decoded into that type. A
  component whose output contains none (`statefulset`, `daemonset`, a
  `manifests` source or a chart without a Deployment, …) fails, rather than
  carrying a trait that does nothing. A Deployment passed through as raw,
  unstructured output — a `passthrough` object — is not inspected, as for the
  other Deployment-decorating traits, so such a component fails the same way.
- **A `matchLabels` selector is required.** A Deployment whose selector is
  missing, has no `matchLabels`, or also carries `matchExpressions` is refused,
  since the spread selector could not select exactly its pods. The check runs
  at every replica count, including 1 and unset where no constraint would be
  written, so a document does not build in one environment and fail in
  another whose policy raises the count. `deployment`, `webservice` and
  `worker` always select on `app: <component>`; a `manifests` Deployment can
  declare any selector, and is refused when it does not meet this rule.

## Decorator forwarding for layout-augmenting components

A component config that also implements kure's `layout.LayoutAugmenter` (e.g. any `helmtemplate`)
can carry any trait. `wrapIfAugmenter`
(`decorator.go`) is the shared construction-site helper every trait decorator calls: if the
wrapped inner config implements `layout.LayoutAugmenter`, it returns an `augmentingDecorator`
wrapping the trait-specific decorator instead of the plain one, so the wrapper itself also
satisfies `layout.LayoutAugmenter` and forwards `AugmentLayout` straight through to the inner
config. Without this forward, kure's layout walker — which keys a structural decision off
`layout.LayoutAugmenter`'s mere *presence* on the concrete config it walks — would never see the
capability on a decorated (trait-carrying) config, silently losing the augmenter's layout-level
effect (the hook-group repartitioning) the moment any trait is added.

An inner config that also implements kure's `layout.LayoutIntentAugmenter` keeps that too:
`wrapIfAugmenter` then returns an `intentAugmentingDecorator`, which forwards `WantsOwnLayout`.
The walker treats an absent `WantsOwnLayout` as "wants its own layout", so without the forward an
augmenter that answers `false` (stay in the parent's layout) would get a child layout as soon as a
trait decorated it. The method is present only when the inner has it, like `AugmentLayout`. No
built-in component config implements `LayoutIntentAugmenter`; this keeps a registered handler's
config, or a future built-in's, placed where it asks.

The forward is all `augmentingDecorator` does there. A decorator's own processing never sees the
resources the augmenter adds: those are created inside `AugmentLayout`, after every decorator's
`Generate` has returned. No decorator needs to: `configmap`, `external-secret`,
`security-context` and `topology-spread` rewrite the workload their inner `Generate` returns and
have nothing to do for an augmenter-added resource. `prune-protection` and `force-replace`, which
did annotate those resources through a post-augment hook, are no longer decorators: they set a
delivery intent on the application, and the delivering workflow covers what the layout holds
(go-kure/launcher#782). The hook is removed with them.

Every trait decorator also embeds `decoratorBase`, which forwards the optional
interfaces a component config may implement — `stack.Validator`,
`fluxNamespaceSettable`, `fluxNamespaceReader`, `servicePortProvider`,
`serviceBackendNamer`, `servicePortNamer`, `oam.ServiceAccountNamer`, `nonRWXClaimer`,
`serviceRoutingTargeter`, `podTemplateLabeler`, `identityPortMapper` and `oam.ComponentNamed` —
so a decorated config keeps answering them. The `oam.ComponentNamed` forward keeps a decorated
config attributed to its component (see "Component attribution" below). The NetworkPolicy
synthesis collectors of the routing traits are deliberately not forwarded: they are contracts of
trait sub-applications, which no built-in decorator wraps, and
forwarding them would make every decorated component look like a router. The `ServiceAccountNamer` forward is what keeps the
`rbac` row above true once a second trait is present: without it a workload
that authored `serviceAccountName` would stop reporting its account, and that
it runs pods, as soon as any trait wrapped it, and `rbac` would silently bind
the component name instead. A config that is not a `ServiceAccountNamer`
forwards `("", false)`: no account, and no pods. The `nonRWXClaimer` forward does the same for the `scaler` row: a
decorating trait declared before `scaler` must not hide the claim that caps
`maxReplicas` at 1. The `serviceRoutingTargeter` forward keeps a decorated `service`
component's synthesized ingress allow on its `selector` pods rather than on the component
label, which none of its pods carry. The `podTemplateLabeler` and `identityPortMapper` forwards
keep a sibling group able to tell a `service` member routing to its own `deployment` member's pods
(see the sibling group section of `pkg/oam/README.md`) when a trait wraps either member. The `servicePortNamer` forward keeps an `ingress` path's
`portName` on a decorated component held to its Service port's name (the first port, on a
`service` component), the same rule a port number is held to. A config that implements none of them gets the zero
answer (`nil`, `0`, `""`, `false`), which every reader treats as "not set".

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
carrying `security-context`), which would otherwise be wrongly rejected by `kurel build`.

## Trait objects under a Flux namespace

Under `TransformContext.FluxNamespace` a trait's objects stay in the application namespace,
except a `configmap` trait's ConfigMap or the Secret an `external-secret` trait's ExternalSecret
or a `certificate` trait's Certificate writes, when the component's own Flux object reads it by
name from the namespace that object moves to: a `helmrelease`'s `valuesFrom`, a `helmrepository`'s
`secretRef` or `certSecretRef` and the like. That one moves with it — the ExternalSecret or
Certificate moves, and with it the Secret it writes; the remote key an ExternalSecret defaults from
the application namespace does not change (go-kure/launcher#740). The default
`ClusterSecretStore` serves it there unchanged; a namespaced store (`secretStoreRef.kind:
SecretStore`) is resolved in the ExternalSecret's own namespace, so a store of that name must
exist in the Flux namespace too. Likewise a Certificate's `ClusterIssuer` serves it anywhere, while
a namespaced `Issuer` must exist in the Flux namespace. The transform cannot see which stores or
issuers a cluster has, so it does not check this. The three sub-application
configs name their object through `FluxNamespaceInput() (kind, name string)`; the component configs
report what they read through `FluxNamespaceReads()`, which every decorator forwards. See the
`pkg/oam` README for the full rule.

The `helm` component's `valuesMode: configMap` relies on this: its rule appends a synthesized
`configmap` trait holding the values to the `helmrelease` it lowers to, and names that ConfigMap
in `valuesFrom`, so it follows the HelmRelease (go-kure/launcher#702). Synthesized, the trait goes
through the same `configmap` checks as an authored one (key validity, the size limit), and its
ConfigMap is emitted after the HelmRelease.

## Component attribution

Every trait sub-app config exposes the OAM component it was emitted for via
`ComponentName() string` (the `oam.ComponentNamed` interface) — always the component
name, never the sub-app or K8s Service name. Consumers use it to stamp per-resource
provenance (the derived `<domain>/component` label) without re-deriving the component
from sub-app names, which several handlers author from properties rather than
`<component>-<suffix>`. `prune-protection` and `force-replace` leave a sub-app's config as it
is (they set a delivery intent on its application), so it answers directly. The routing traits' existing `TargetComponentName()` (used by
auto-NetworkPolicy synthesis) delegates to the same accessor; auto-synthesized
NetworkPolicies target that `<domain>/component` label by default (domain from
`TransformContext.Domain`, library default `gokure.dev`;
`TransformContext.ComponentLabelKey`-overridable). The accessor returns the raw name;
the label and selector value is `oam.ComponentLabelValue` of it, which differs from
the name only past 63 characters (see Conventions). The transform puts that label on
every object a trait's sub-application generates, with its component's value
(go-kure/launcher#788); after `Transform` the sub-application's config is the ownership
wrapper, so a caller reaches a trait config's concrete type through `oam.UnwrapConfig`.

## Raw Cilium rules are decoded strictly

`cilium-networkpolicy` passes `endpointSelector`, `ingress` and `egress` through to
Cilium's `api.Rule` as opaque shapes, so the trait schema cannot validate them. They are
decoded with `DisallowUnknownFields`, through the shared `builtin.DecodeStrictJSON`: a property the linked Cilium API version does not
recognise makes the build fail, naming the rejected field.

This is deliberate. Lenient decoding silently **widened** policies whenever Cilium removed
an API field — v1.20 dropped `kafka`, `l7proto` and `l7` from `api.L7Rules`, so a rule
carrying them would have rendered as L4-only with no error, producing a policy more
permissive than authored. Failing the build is the safe direction for a policy generator.

Practical consequence: a package pinned to rule shapes from an older Cilium release will
fail to build after a Cilium major bump rather than quietly losing its L7 constraints.
Rewrite the affected rules to the shapes the new API supports.

**Selectors are checked too.** `encoding/json` does not propagate `DisallowUnknownFields`
into a type that defines its own `UnmarshalJSON`, and Cilium's `EndpointSelector` is one: it
drops a key it does not know. A selector whose only key is misspelt (`matchLabel` for
`matchLabels`) would then build as the empty selector, which matches every endpoint of the
namespace. The trait therefore refuses an unknown key inside a selector by its path, for
example `unknown field "ingress[0].fromEndpoints[1].matchLabel"`, at every position of the
three properties the trait takes (`endpointSelector`, `ingress`, `egress`) that holds one:
`endpointSelector`, `fromEndpoints`, `toEndpoints`, `fromNodes`, `toNodes` and a CIDR
entry's `cidrGroupSelector`. The same check covers the one other such type those properties
reach, an ICMP field (`icmps[].fields[]`, which declares `family` and `type`). The
positions are found from the Cilium types by `builtin.UnknownCiliumKeyPath`, which knows
every type under a Cilium rule that decodes itself, a rule label and the deny rules
included; the trait takes neither. A test fails when a Cilium bump adds such a type and it
is not checked.

**Breaking**: a `cilium-networkpolicy` trait whose selector holds an unknown key built
before, with a selector wider than written, and is now refused.

### Null or empty `endpointSelector` / `egress` / `ingress`

A `null` is **absence**, whatever its Go shape — the same contract as the
`networkpolicy` trait above. The trait refuses at build time, by name, every document
that would render a CiliumNetworkPolicy the cluster rejects on apply
(go-kure/launcher#468):

- `egress` and `ingress` are **required jointly, and must carry at least one rule
  between them** (`at least one of 'egress' or 'ingress' must be specified with at
  least one rule`). Cilium renders both as `omitempty` lists, so a null and an empty
  list alike produce no rule key, and the CRD (anyOf
  `ingress`/`ingressDeny`/`egress`/`egressDeny`) rejects that. `egress: []` alone, or
  `egress: []` beside a null `ingress`, is therefore an error. An empty or null key
  beside a direction that does carry a rule is simply absent.
- `endpointSelector` is **required**. The trait synthesizes no default selector and
  exposes no `nodeSelector`, so an omitted selector would render a policy the CRD
  rejects (oneOf `endpointSelector`/`nodeSelector`). A null is refused the same way as
  an omitted key. To select every endpoint, author `endpointSelector: {}`. A **typed**
  nil — an uninitialized Go map from a lowering rule — used to be emitted as
  `endpointSelector: null`, which Cilium decodes to an empty selector matching
  **every endpoint**, so a null silently became select-all.

### Typed nils in the other trait parsers

A **typed** nil (`map[string]any(nil)`, `[]any(nil)`) is what an uninitialized Go map
or slice produces when a lowering rule or a Go-API caller assigns it into a property.
A bare `v.(map[string]any)` succeeds on it, so it used to read as an authored empty
value where an untyped null (`key:` with no value in a document) did not. Since
go-kure/launcher#465, the parsers of `httproute`, `ingress`, `external-secret`,
`rbac`, `certificate`, `pvc` and `expose`, and the
shared `networkPolicy.trafficSources` parser, give a typed nil the answer an untyped one
gets. The rule is the untyped answer, whatever it is:

- **Refused, with the untyped message:** a list entry (`httproute` `parentRefs[]`,
  `rules[]`, `matches[]`, `headers[]`, `backendRefs[]`, `filters[]` and header-modifier
  entries; `ingress` `rules[]`, `paths[]`, `tls[]`; `external-secret` `data[]` and
  `dataFrom[]`; `rbac` `rules[]`; `trafficSources[]`), and a required or typed-when-present block (the `httproute`
  filter blocks and their `backendRef`s, `ingress`/`httproute` `backendSelector`,
  `data[].remoteRef`, `certificate` `issuerRef`, `rbac` `apiGroups`/`resources`/`verbs`,
  `networkPolicy`, `podSelector`). Before, a typed nil there became a catch-all rule, a
  `/` path, an empty TLS block, or an empty value that let a valid sibling carry the
  document.
- **Absent:** an optional block (`httproute` `annotations`, `backendRefs`, `timeouts`,
  a match or redirect/rewrite `path`, mirror `fraction`, `externalAuth`
  `grpc`/`http`/`forwardBody`; `ingress` `annotations`; `external-secret` `remoteRef`,
  `dataFrom[].extract`/`find`/`find.tags`, `target.template` and its `data`; `pvc`
  `accessModes`, which takes the `ReadWriteOnce` default; `expose` `annotations`, which
  used to panic when `sslRedirect` wrote into it).

Each remaining comma-ok assertion in these files is safe for one of three reasons:
- a required list's `len == 0` check fires first with the same message for both
  shapes (`httproute` `parentRefs`/`rules`, `ingress` `rules`/`paths`, `rbac` `rules`,
  `certificate` `dnsNames`);
- the value is filtered through `oam.IsNullValue` first (the `networkpolicy` trait
  parser via `nonNullObject`/`nonNullArray`, `trafficSources`, `backendSelector`);
- a nil map is only read by key and a nil list only ranged, so both shapes parse the
  same (`certificate` `privateKey`, `external-secret` `target`, `expose` `rules[]`, and
  the optional string lists).

`TestTypedNilSweep` and `TestTypedNilSweepFollowUp` pin each fixed site against the
untyped answer.

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

Every `app` label and `app` selector a trait generates to identify its component — on
the `configmap`, `pvc`, `rbac`, `scaler`, `ingress`, `httproute`, `networkpolicy` and
`external-secret` objects, and the PodDisruptionBudget and NetworkPolicy `podSelector`
that pick the component's pods — is valued at `oam.ComponentLabelValue(<component>)`,
through the unexported `componentLabels` (`labels.go`); `external-secret` calls the
function directly. Authored selectors, such as a `networkpolicy` peer's `podSelector`,
are emitted as written. It is
the same function the component handlers label their pods with, so a trait's selector
matches them. A component name may be up to 253 characters and a label value at most
63; the function returns a name of 63 characters or fewer unchanged and projects a
longer one onto a prefix of at most 52 characters plus a 10-hex-character digest
(go-kure/launcher#572). A trait on a component whose type accepts a longer name (a
`passthrough`, for one) reaches the projection.

The object names a trait generates by default follow the same shortening rule at 253
characters (`oam.ShortenNameWithSuffix`, go-kure/launcher#793): the `scaler` HPA and PDB
(`<component>-hpa`, `<component>-pdb`), the `networkpolicy` trait's policy
(`<component>-allow`), the `ingress` Ingress and the `httproute` HTTPRoute
(`<component>-ingress`, `<component>-httproute`, each followed by `-<scope>` when the trait
sets one), the managed TLS Secret default (`<component>-tls`), the `volsync`
ReplicationSource (`<sourcePVC>-backup`) and the `volsync` default repository Secret name
(`<component>-volsync-secret`). A name that fits is unchanged; a longer one keeps a prefix of
the name before the suffix, a `-`, 10 hex characters of its sha256, and the suffix whole.

An authored name is used as written or refused: it is never shortened and never changed, and
one that cannot be the name of its object fails the transform with the property in the error
(go-kure/launcher#787). The check is the DNS-1123 subdomain rule every one of these objects
can be named by (at most 253 characters, lower-case alphanumerics, `-` and `.`, starting and ending
with an alphanumeric). An authored empty string is refused too. For an optional override
(`name` on a routing trait, on `rbac` and on `networkpolicy`, `hpaName`, `pdbName`,
`targetSecretName`, `repository`) it is not a way to ask for the
default: leaving the property out, or null, gets that. The `expose` `secretName` is optional
too and gets its default when left out. The other names are required.

The `rbac` `name` is held to the same subdomain rule, which is stricter than the cluster's
own rule for the RBAC kinds: the cluster also accepts a name such as `web:reader`, and
launcher refuses it. The name also becomes a file name and a `kustomization.yaml` entry in
the written tree, and a colon has not been shown to be safe there. An author who needs such
a name cannot write it today.

The names of the `scaler`, `rbac` and `networkpolicy` objects are resolved under a name role
(`pkg/oam/README.md`, "Name roles and the `Naming` hook"): the author's property, else the
consumer's `TransformContext.Naming` hook, else the default. `Transform` keeps those names
apart and refuses two that name one object, naming both: an `hpaName` equal to another
component's HPA, or two `scaler` traits on one component. Every other authored name in the
table below is checked as a name, not against the other objects of the document:
`oam.GenerateApplications` and `oam.CheckInDocumentCollisions` report those (`kurel build`
runs both; see `pkg/oam/README.md`, "Nothing above compares the applications inside one
document").

Every trait's sub-application name is resolved under the `sub-application` role, so the hook
can rename it. That name is not the object's: the `configmap`, `ingress`, `httproute` and
`volsync` objects keep their own name when their sub-application is renamed.

| Trait | Property | What it names |
|-------|----------|---------------|
| `ingress`, `httproute` | `name` | The Ingress or HTTPRoute. |
| `expose` | `name` | The Ingress or HTTPRoute the trait lowers to; checked by that trait. |
| `expose` | `secretName` | The managed TLS Secret (a `ValidationError` on the component). |
| `certificate` | `secretName` | The Certificate and the Secret it writes. |
| `external-secret` | `secretName`, `targetSecretName` | The ExternalSecret, and the Secret it produces. |
| `configmap` | `name` | The ConfigMap. |
| `cilium-networkpolicy` | `name` | The CiliumNetworkPolicy. |
| `pvc` | `name` | The PersistentVolumeClaim. |
| `volsync` | `repository`, `sourcePVC` | The repository Secret, and the claim to back up (which also starts the ReplicationSource name). |
| `scaler` | `hpaName`, `pdbName` | The HorizontalPodAutoscaler, and the PodDisruptionBudget (`pdbName` needs `enablePDB: true`). |
| `rbac` | `name` | The Role, the RoleBinding and, with `clusterWide`, the ClusterRole and the ClusterRoleBinding: one name for all four, and the `roleRef.name` of both bindings. The ClusterRole and the ClusterRoleBinding are claimed with no namespace (`oam.NameSpec.ClusterScoped`), so a lowering rule's cluster-scoped object of the same kind and name is refused with both named. |
| `networkpolicy` | `name` | The NetworkPolicy. |

A routing trait's `scope` (on `ingress`, `httproute`, and `expose`, which hands its own on) is
a part of a generated name, not a name: its length is never
refused, because the name it ends is shortened, but a character an object name cannot hold is,
checked on the name as built so the digest cannot hide it. Beside an authored `name` the scope
is in no name and is not checked. A property that only refers to an
object launcher never names (`issuerRef.name`, a `gatewayName`, an `ingress` `tls[].secretName`)
is passed through as written.

The same holds for the traffic sources a routing trait retains (`TrafficSources()`):
NetworkPolicy synthesis in `pkg/oam` gives every emitted peer, and every synthesized
policy's `spec.podSelector`, its own deep copy of the source or backend selector. So a
label a caller stamps on one generated NetworkPolicy never reaches another policy
built from the same source, or the trait configuration (go-kure/launcher#396).

Since go-kure/launcher#361 these handlers build against kure's release-1 builder
contract (`go-kure/kure` ≥ `v0.2.0-beta.11`), under which a `Create<Kind>`
constructor returns TypeMeta plus `metadata.name`/`metadata.namespace` and nothing
else — no labels, no annotations, no defaults. Every other field on a generated
object is therefore set by the trait itself, either as a direct field assignment
(`np.Spec.PodSelector`, `rb.RoleRef`, `crb.RoleRef`, `es.Spec.Target`,
`cm.Labels`) or through the remaining typed sugar kure still exposes for
appending to a list (`AddRoleRule`, `AddRoleBindingSubject`,
`AddNetworkPolicyPolicyType`, `AddConfigMapData`, `AddLabel`). Two practical
consequences: the `Annotations = nil` lines in `configmap`, `networkpolicy`,
`rbac` and `scaler` (both the HPA and the PDB) no longer strip anything a
constructor stamped and are now defensive only;
and a *new* trait that emits a selector-bearing kind must write that selector
itself, because nothing upstream of it will. `CreateIngress` is the one
constructor in this package whose signature changed with the contract — it no
longer takes an ingress class, so `ingress` writes `spec.ingressClassName` via
`SetIngressClassName` only when one was authored, leaving it nil otherwise
(unchanged output: the old call passed the class in and then nil'd the field back
out when it was empty).

kure `v0.2.0-beta.13` (release 2 of the builder contract) retired the
config-struct layer the remaining four traits still went through
(`certmanager.Certificate`, `cilium.CiliumNetworkPolicy`,
`externalsecrets.ExternalSecret`, `volsync.ReplicationSource` and their `*Config`
types). `certificate`, `cilium-networkpolicy`, `external-secret` and `volsync` now
call the generated `CreateCertificate` / `CreateCiliumNetworkPolicy` /
`CreateExternalSecret` / `CreateReplicationSource` and set the spec themselves —
through `AddCertificateDNSName`, `SetCertificateDuration`,
`SetCertificateRenewBefore` and `SetCiliumNetworkPolicySpec`, or by assigning
`es.Spec.SecretStoreRef` and the upstream `ReplicationSourceResticSpec` /
`ReplicationSourceTriggerSpec` directly. None of those layers injected a value, so
the emitted manifests are unchanged.
