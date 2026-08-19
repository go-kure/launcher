# OAM Built-in Component Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/components.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)

Package `components` implements `oam.ComponentHandler` for the built-in component
types. Each handler parses a typed config from a component's `properties` and
produces the corresponding Kubernetes resources via kure's builders. Handlers are
registered with the transformer in `pkg/cmd/kurel` (`newBuiltinTransformer`), each
mapping a component `type` string to a handler implementing `CanHandle` +
`ToApplicationConfig`. Every built-in component handler also implements
`oam.PropertySchemaProvider` (`PropertySchema()`), declaring a constrained schema for its
user-facing properties so the downstream runtime can validate them before invocation. Deeply nested or
K8s-adjacent shapes are kept shallow/open (`additionalProperties`) rather than modeled
field-by-field; escape-hatch fields (e.g. `passthrough.object`, `manifests`/`crd` inline
content) stay open by design. Every property (including nested object fields and array item
schemas at every depth) carries a `Description`, surfaced in the downstream runtime's generated Handler API
Reference.

## Component types

| `type` | Produces | Summary |
|--------|----------|---------|
| `webservice` | Deployment, Service, ServiceAccount (+PVC) | HTTP service with replicas, probes, env, volumes. |
| `worker` | Deployment, ServiceAccount (+PVC) | Background workload (no Service/port). |
| `statefulset` | StatefulSet, headless Service, SA | Stateful workload with `volumeClaimTemplates`. |
| `daemonset` | DaemonSet, SA (+Service if `port`) | Per-node daemon; honors `tolerations`. |
| `cronjob` | CronJob, SA (+PVC) | Scheduled job; cron `schedule` + history limits. |
| `helmchart` | HelmRelease + Helm/OCIRepository, or rendered manifests | Helm via Flux (`native`) or client-side `template`. |
| `oci` | OCIRepository, Kustomization | Sync manifests from an OCI artifact (Flux). |
| `postgresql` | CNPG Cluster, Pooler, ObjectStore, Database | CloudNativePG database (backup/monitoring/pooling). |
| `passthrough` | any (verbatim) | Emit an arbitrary object as-declared (`clusterScoped` opt). |
| `crd` | CustomResourceDefinition(s) | CRDs from `inline`/`url`; rejects non-CRD docs. |
| `manifests` | any | Raw manifests from `inline`/`url` with namespace stamping + `scopeOverrides`. |

## Common config

Most workload types (`webservice`, `worker`, `statefulset`, `daemonset`, `cronjob`)
share these fields, projected directly onto real `corev1` types (same
structural pattern as `ProbeConfig` holding `*corev1.Probe`) rather than a
hand-rolled parallel schema: `image` (validated — no untagged/`latest`), `env`
(`value` or `valueFrom` — mutually exclusive, matching `corev1.EnvVar`'s own
doc comment ("cannot be used if value is not empty"); `valueFrom` is one of
`secretKeyRef`, `configMapKeyRef` (both accept `optional`, rejecting a
present non-boolean value rather than silently treating it as unset), `fieldRef`
(`apiVersion` must be `v1` if authored — the only field-label conversion
Kubernetes has ever shipped for the downward API; omitting it also defaults to
`v1`; `fieldPath` is validated against the exact set real admission accepts
for an env var fieldRef — `metadata.name`/`metadata.namespace`/`metadata.uid`,
`spec.nodeName`/`spec.serviceAccountName`, `status.hostIP`/`status.hostIPs`/
`status.podIP`/`status.podIPs` — plus the `metadata.labels['KEY']`/
`metadata.annotations['KEY']` subscript forms, each with `KEY` checked as a
qualified name; a field like `status.phase` builds but is rejected by
admission, so it is rejected here too), `resourceFieldRef` (`resource` must be
one of `limits.cpu`, `limits.memory`, `limits.ephemeral-storage`,
`requests.cpu`, `requests.memory`, `requests.ephemeral-storage`, or a
`requests.hugepages-<size>`/`limits.hugepages-<size>` selector — the downward
API cannot project an arbitrary extended resource such as
`limits.nvidia.com/gpu`, unlike a plain `resources` map below; an authored
`divisor` must be one of the canonical unit strings admission accepts for that
resource's family — `1m`/`1` for cpu, one of `1`/`1k`/`1M`/`1G`/`1T`/`1P`/`1E`/
`1Ki`/`1Mi`/`1Gi`/`1Ti`/`1Pi`/`1Ei` for memory/ephemeral-storage/hugepages — a
zero-valued divisor such as `"0"` is treated as absent rather than rejected,
matching admission's own zero-value defaulting; a present-but-non-string
divisor (e.g. a bare YAML number) is rejected rather than silently treated as
absent, same as every other typed scalar field in this document;
`containerName`, if authored,
must be a syntactically valid container name (`ValidateDNS1123Label`) —
**note:** whether it actually names a container present in the generated pod
is deliberately not checked, since that needs the full sibling
container/initContainer/sidecar name set, which isn't available at this
single-env-var parsing depth; real admission doesn't check it either (only
the downward API *volume* form of `resourceFieldRef` requires
`containerName` at all), so an unresolvable target only surfaces later, as a
kubelet-time `CreateContainerConfigError`),
`fileKeyRef` (`volumeName`/`path`/`key` required, `optional` accepted (same
non-boolean rejection as above); corev1's `EnvFiles` feature; `volumeName`
must be a valid DNS-1123 label and `key` a valid (relaxed) env var name,
matching real admission's own `validateFileKeySelector`; `path` must be
relative and must not contain a `..` backstep component, per this repo's own
path-safety convention; **deferred:** `volumeName` is not cross-checked
against the component's declared `volumes` — env parsing runs before volume
parsing in every call site, and this schema has no `image`-type volume yet
(the only volume source real `FileKeySelector` semantics resolve against), so
a name-only match would be misleadingly incomplete; see the doc comment on
`parseFileKeyRef` in `common.go`) —
mutually exclusive among themselves too), `envFrom` (an authored non-array
value, e.g. a single ConfigMap/Secret object instead of a list of them, is
rejected rather than silently treated as absent; bulk-import a ConfigMap's
or Secret's keys, with `prefix` — any printable ASCII character except `=`,
matching `corev1.EnvFromSource.Prefix`'s own field doc comment; only the final
prefix+key concatenation need be a valid env var name, not the prefix alone;
a present-but-non-string `prefix` (e.g. a bare YAML number) is rejected
rather than silently omitted while still emitting the rest of the source —
an unprefixed import can collide with existing names and leaves the names
the application expects unset;
`configMapRef.name`/`secretRef.name` must each be a valid DNS-1123 subdomain,
matching how every Kubernetes object name is validated; both `configMapRef`
and `secretRef` also accept `optional`, with the same non-boolean rejection
as `env`'s `secretKeyRef`/`configMapKeyRef` above; a present-but-malformed
`configMapRef`/`secretRef` (e.g. a scalar instead of an object) is rejected
outright rather than silently treated as absent — the latter would let the
other, well-formed ref alone satisfy the "exactly one" check below and
quietly discard the malformed one instead of reporting it),
`resources` — a `corev1.ResourceRequirements` projection: `requests`/`limits`
accept `cpu`/`memory` (defaults 100m/128Mi) plus any other well-formed
resource name (e.g. `ephemeral-storage`, `nvidia.com/gpu`) in the same map,
each value authored as either a quantity string (`"500m"`, `"2Gi"`) or a bare
YAML/JSON number (`1`, `0.5`) — both are valid `resource.Quantity` input
(`Quantity.UnmarshalJSON` parses a bare numeric literal the same way it parses
a quoted one), and both forms are also accepted by the published property
schema itself: `cpu`/`memory` are declared with no `Type`, since the schema
vocabulary has no string-or-number union and a `string`-only declaration would
reject the numeric form the parser accepts — parsed as `resource.Quantity` and
round-tripped unmodified. A resource name is validated the same way real
admission validates `corev1.Container.Resources` (mirrors
`ValidateContainerResourceName`): an unqualified name (no `/`) must be
`cpu`/`memory`/`ephemeral-storage` or a `hugepages-<size>` name — an arbitrary
unqualified token such as `foo` builds but is reserved for Kubernetes' own
native resources and is rejected here too; a qualified name (has `/`) is
accepted as an extended resource (e.g. `nvidia.com/gpu`) unless it either
contains `kubernetes.io/` (which claims to be a native resource, not an
extended one, mirroring `IsNativeResource`) or is `requests.`-prefixed (which
collides with the `ResourceQuota` `requests.<name>` alias form) — the two
conditions are checked independently, so e.g. `kubernetes.io/foo` is rejected
even though it is not `requests.`-prefixed.
Every quantity must be non-negative; `cpu`/`memory`/`storage`/
`ephemeral-storage` may be fractional, but any other (extended) resource name
must be a whole number, matching Kubernetes' own extended-resource
constraint. A `hugepages-<size>` quantity must additionally be an integer
multiple of that page size (e.g. `hugepages-2Mi: 3Mi` is a whole number of
bytes but not a whole number of 2Mi pages, and is rejected; `hugepages-2Mi:
4Mi` is accepted), matching `IsHugePageResourceValueDivisible`. A hugepages
or extended resource cannot be overcommitted (mirrors
`validateResourceRequirements`'s `IsOvercommitAllowed` check): if `requests`
sets one, `limits` must set the identical value for that same name — a
request with no matching limit is rejected outright (nothing defaults a
missing limit from a request, unlike cpu/memory), and a request/limit pair
that merely differs is rejected too. A `limits`-only entry with no matching
`requests` entry is deliberately not rejected here: the real apiserver's
defaulter copies `limits` into `requests` before validation runs, so that
shape is admission-valid. `cpu`/`memory`/`ephemeral-storage` stay
overcommittable — a lower request than limit is fine, and either may be set
without the other — but when both are present the request still must not
*exceed* the limit, matching `validateResourceRequirements`'s
request-vs-limit comparison for every resource name, not just the
non-overcommitable set. No policy
default/max hook exists for names other than cpu/memory
today; `claims` (Dynamic Resource Allocation) is deliberately not covered —
genuinely feature-gated in the pinned `k8s.io/api` version and meaningless
without pod-level `PodSpec.ResourceClaims` wiring this schema doesn't have
yet, see `parseResources`'s doc comment), `command`/`args` (each element must
be a string — **note:** unlike every other array field in this schema,
`command`/`args` still silently drop a non-string element rather than
rejecting it; `lifecycle.{postStart,preStop}.exec.command` below was fixed to
reject, but the top-level `command`/`args` fix was deliberately left out of
that change to keep it self-contained to `common.go`'s `parseLifecycleHandler`
— touching `parseCommand`/`parseArgs` would ripple into all 7 call sites across
every kind component), `probes`
(httpGet/tcpSocket/exec/grpc — a string `port` is a named container port, not
an arbitrary label, and is validated against `validation.IsValidPortName`
just as real admission's `ValidatePortNumOrName` does: lowercase
`[-a-z0-9]` only, at least one letter, no leading/trailing/adjacent hyphen,
max 15 characters — a purely numeric string like `"8080"` is rejected, since
it has no letter; beyond syntax, a named port is rejected outright on a
component kind whose main container never declares any port for the kubelet
to resolve the name against — `worker` and `cronjob` unconditionally (neither
exposes a `port` property at all), `daemonset`/`statefulset` only when their
own optional `port` was not set on that component instance (their main
container is named `"http"`/`"tcp"` respectively, but only when `port > 0`),
and `webservice` never (its `port` always defaults to 80, so the main
container is always named `"http"`) — a numeric port is unaffected either way,
since it dials the kubelet directly rather than resolving a declared name.
Where a named port is allowed at all, it is further checked against the
exact name the kind's builder actually declares — `"http"` for
`webservice`/`daemonset`, `"tcp"` for `statefulset` — not merely accepted as
any syntactically valid name: a syntactically valid but different name (e.g.
`httpGet.port: metrics` on a `webservice`, whose only declared container
port is `"http"`) builds successfully but is exactly as unresolvable by the
kubelet at runtime as a named port on a portless component, so it is
rejected the same way; `grpc.service`, if authored, must be no more than 63
characters (mirrors `validateGRPCService`'s length cap — the gRPC
health-checking service name is not DNS-1123 formatted, but admission still
bounds its length), and `grpc.port` is always numeric regardless of any
kind's named-port rules — a named `grpc.port` is rejected outright with its
own message, never resolved against a declared container port;
`httpGet.httpHeaders` itself and its entries are validated
rather than silently dropped/coerced — an authored non-array `httpHeaders`
value (e.g. a single header object instead of a list) is rejected the same
as a non-object entry within it, a missing/empty/invalid `name`
(`validation.IsHTTPHeaderName`, matching `validateHTTPGetAction`), or a
present-but-non-string `value` are all rejected instead of quietly
disappearing or turning into `""`; an omitted `value` key still defaults to
`""` — shared by `lifecycle.{postStart,preStop}.httpGet` below via the same
parsing helper; `path`/`host`/`scheme` are all optional — an absent or empty
`path` matches real Kubernetes' own defaulting (`SetDefaults_HTTPGetAction`
fills it with `"/"` before validation ever runs, wired for both probe and
lifecycle httpGet handlers), so this schema does not require what upstream
itself fills in; `host` has no format validation in real admission either
(only rejected in combination with an HTTP2 protocol, which this schema does
not expose), so an unusual-looking host string is accepted verbatim, same as
real admission; `scheme`, if authored, must be `HTTP` or `HTTPS`
(case-insensitive); all three, plus every probe numeric field below, reject a
present-but-wrong-type value instead of silently treating it as absent;
`initialDelaySeconds`/`periodSeconds`/`timeoutSeconds`/`successThreshold`/
`failureThreshold`/`terminationGracePeriodSeconds` are typed integers, each
bounds-checked to match real Kubernetes admission (`validateProbeTimeouts`
plus the liveness/startup `successThreshold` rule) rather than accepting any
integer at face value: `initialDelaySeconds` must not be negative;
`periodSeconds`/`timeoutSeconds`/`successThreshold`/`failureThreshold` must
each be at least 1 (a `periodSeconds: 0`, for example, would otherwise author
a probe Kubernetes itself would reject); `successThreshold` must additionally
be exactly 1 on a liveness or startup probe — only a readiness probe may set
it above 1, since liveness/startup have only two outcomes (still healthy,
or restart) and "N consecutive successes to reset that state" has no defined
meaning for either —
`terminationGracePeriodSeconds` is additionally rejected outright on a
readiness probe (a failed readiness check only pulls the pod from Service
endpoints, it never terminates the container, so the field has nothing to
apply to there) and must be at least 1 when set on a liveness or startup
probe), `lifecycle` (rejected outright if authored with a non-object value,
e.g. a scalar or array, instead of silently treating it as absent and
running the container with no startup/shutdown hooks;
`postStart`/`preStop`:
`exec` (every `command` element must be a string; a non-string element is
rejected, not silently dropped)/`httpGet` (same named-port, `httpHeaders`,
and optional-`path`/`host`/`scheme` rules as `probes` above)/`sleep` —
`tcpSocket` is rejected unconditionally, even when paired with another valid
handler such as `exec` — corev1 documents it as broken for lifecycle hooks,
and simply ignoring the extra key would silently drop the authored
`tcpSocket` while emitting only the other handler; `postStart`/`preStop` are
each rejected outright if present with a non-object value, e.g. `preStop:
"flush"`, instead of silently discarding the whole hook),
`securityContext` (rejected outright if authored with a non-object value,
e.g. a scalar or array, instead of silently treating it as absent and
emitting a container with a nil security context;
per-container: `runAsUser`/`runAsGroup`/`runAsNonRoot`
(`runAsUser: 0` combined with `runAsNonRoot: true` builds and is admitted by
the API server, but the kubelet's `verifyRunAsNonRoot` check deterministically
fails it at container-start time — a `CreateContainerConfigError`, every
time — so this contradictory combination is rejected here instead of
shipping a workload guaranteed never to start; `runAsUser`/`runAsGroup` are
each rejected if authored with a non-integer value, e.g. a quoted `"1000"`,
rather than silently omitting the UID/GID — since the container would then
fall back to the image's own default, which may be root, while looking like
the authored value was honored; both must also be non-negative),
`readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `privileged` (these
three, plus `runAsNonRoot` above, are each rejected outright if authored with
a non-boolean value, e.g. a quoted `"false"`, instead of being silently
skipped: since Kubernetes' default for each is permissive, silently dropping
a mistyped value would leave the container permissive while looking like the
authored hardening request was honored),
`capabilities` (rejected if authored with a non-object value; `add`/`drop`
are each rejected if authored with a non-array value, e.g. `drop: ALL`
instead of a list, or with a non-string array element — an empty-string
element is silently skipped rather than rejected, since real admission places
no format constraint on a Capability string at all. No environment-policy
enforcement hook exists for these two fields — `oam.Policy` separately
declares `AllowedCapabilities`/`ForbiddenCapabilities`/`RequiredCapabilities`,
but those gate OAM trait-type usage (e.g. "ingress"), not container Linux
capability strings (e.g. "NET_ADMIN"); see `enforce.go`'s `enforcePrivileged`
doc comment for the naming-collision detail. Enforcing these would need a new
`Policy` method, which is out of scope here), `seccompProfile` (rejected outright if authored with
a non-object value, e.g. `seccompProfile: RuntimeDefault`, instead of silently skipping the field
and dropping the requested sandboxing entirely; `type` is required whenever the
`seccompProfile` object is authored at all, matching real admission's own
`field.Required` — omitting it (e.g. authoring only `localhostProfile` with
no `type` key) is rejected rather than silently discarding the whole
profile; `localhostProfile` must be
relative and must not contain a `..` backstep component, matching
`corev1.SeccompProfile.LocalhostProfile`'s own doc comment — "must be a
descending path, relative to the kubelet's configured seccomp profile
location" — and this repo's own path-safety convention), `seLinuxOptions`
(also rejected outright if authored with a non-object value, same reasoning as
`seccompProfile` above;
`user`/`role`/`type`/`level` are each rejected if authored with a
non-string value, e.g. `type: 123`, instead of silently discarding just that
sub-field — if it were the only one set, the whole SELinux context would
otherwise vanish rather than reporting the malformed input),
`appArmorProfile` (same "`type` required when authored" rule as
`seccompProfile` above, and the same non-object rejection as `seccompProfile`
and `seLinuxOptions`), `procMount` (`Default`|`Unmasked`; a present-but-non-string
value, e.g. `procMount: false`, is rejected rather than silently omitted —
same as every other typed scalar field in this document; an explicit empty
string is still treated as absent, not an error, matching `parseStringField`'s
own convention for every other string field here); `windowsOptions` is
deliberately not covered — this project's own container images are
Linux-only (distroless base images run under podman), so a Windows-specific
security context has no target to apply to here, and `procMount` is
Linux-specific by definition, so excluding `windowsOptions` does not extend to
it), `workingDir` (a bare pass-through — `corev1.Container.WorkingDir`'s own
doc comment states only that the container runtime's default applies when
unset, and real admission enforces no path shape for it, so this schema does
not invent a stricter constraint upstream itself does not have; a
present-but-non-string value, e.g. `workingDir: 123`, is rejected rather than
silently treated as absent, in all five kind handlers — this is a type check,
distinct from the content-validation question the previous paragraph answers),
`volumes` (every volume's `name`, regardless of source type — `hostPath`,
`emptyDir`, `pvc`, `configMap`, `secret`, etc. — must be a valid DNS-1123
label, matching how real admission validates every `corev1.Volume.Name`; an
invalid name, e.g. containing `/`, builds successfully but is rejected at Pod
admission),
`initContainers`, `sidecars`, and `affinity`.

`securityContext.privileged: true` is rejected unless the environment policy's
`AllowPrivileged()` allows it — the one `securityContext` field enforced today
(`enforce.go`'s `enforcePrivileged`); the others have no policy hook yet.

Setting any `securityContext` field makes the container's `SecurityContext`
non-nil, which opts it out of the `security-context` trait's nil-only
backfill for every *other* `SecurityContext` field too. If a component uses
both, the trait's `Generate()` pass runs later and unconditionally overwrites
`container.SecurityContext` — the trait always wins when both are applied to
the same component. Use the trait for a safe, complete PSA-consistent
default; use this property for raw, partial, full-fidelity authoring.

`env`, `envFrom`, `resources`, `lifecycle`, `securityContext`, `workingDir`, and
`probes` are each schema fragments parameterized by a `reserved bool` (mirroring
`pkg/oam/builtin/traits/schema.go`'s `schemaNetworkPolicy(reserved bool)`):
every built-in call site passes `false` today. Deciding which of these fields
should be platform-reserved (rejecting any authored value via
`PropertySchema.PlatformReserved`/`enforcePlatformReserved`) is a consumer-side
policy choice, not something this shared schema hardcodes.

## Per-type highlights

- **webservice / worker** — `image`, `replicas` (default 1), `port` (webservice).
  The `webservice` handler implements the optional `oam.EndpointProvider`: it declares its own
  pods (`app: <component-name>`) on the declared `port` (its single `port` property drives both
  the container port and the Service port), letting a downstream platform synthesize generic
  app→app connections targeting a webservice. `worker` declares no in-cluster port and emits no
  Service, so it deliberately advertises no endpoint (not an `EndpointProvider`).
- **statefulset** — `volumeClaimTemplates` (`name`, `size`, `storageClass`,
  `accessModes`, `mountPath`), `serviceName` (headless).
- **daemonset** — `tolerations` (`key`/`operator`/`value`/`effect`); `port`
  optionally adds a Service.
- **cronjob** — `schedule` (5-field cron), `restartPolicy` (default `OnFailure`),
  `successfulJobsHistoryLimit`/`failedJobsHistoryLimit`.
- **helmchart** — `chart`, `version`, `delivery` (`native`|`template`), `source`
  (inline `url` or `{name,kind}` ref), `values`/`valuesFrom`, `driftDetection`,
  `install.crds`/`upgrade.crds`.
- **oci** — `source.url` (`oci://…`), `version` (tag or `sha256:…`), `path`,
  `prune`, `interval`, `targetNamespace`.
- **postgresql** — `provider: cnpg`, `version` (default `16`), `storageSize`
  (precedence: authored > policy default `storageSize` > `1Gi`), `replicas`,
  `backup.*`, `monitoring.enabled`, `pooler.enabled`, `managedRoles`, `databases`.
  `resources` forwards `cpu`/`memory` only — the underlying CNPG builder
  (`kurecnpg.ResourceOptions`, an external `go-kure/kure` type) has no fields
  for other resource names, so any other name authored under `requests`/
  `limits` (e.g. `ephemeral-storage`, `nvidia.com/gpu`) is rejected with an
  explicit error rather than silently dropped; the other 5 workload kinds
  forward every resource name directly onto the real `corev1.Container` and
  have no such restriction.
  Its handler implements the optional `oam.EndpointProvider`: it declares the CNPG cluster's
  data-plane endpoint (`cnpg.io/cluster: <component-name>` on port `5432`) so a downstream
  platform can synthesize the target-side ingress allow (`{comp}-allow-endpoint-ingress`)
  without hardcoding the operator selector. When `pooler.enabled` is set it declares a **second**
  endpoint for the pooler (PgBouncer) pods (`cnpg.io/poolerName: <component-name>-pooler` on port
  `5432`), so a consumer that dials the pooler — whose pods carry a different label set and are not
  matched by the direct-cluster selector — also gets its connection synthesized.
- **passthrough** — `object` (full apiVersion/kind/metadata/spec), `clusterScoped`.
  Its config exposes `ComponentName() string` (the `oam.ComponentNamed` interface) so
  consumers can attribute the emitted resource to its owning OAM component.
- **crd / manifests** — `inline` xor `url`; `manifests` adds `scopeOverrides`
  (`apiVersion`/`kind`/`scope`) for unknown kinds.

## Extending

Custom component types implement `oam.ComponentHandler` (`CanHandle` +
`ToApplicationConfig`) and are registered alongside the built-ins. Exported helpers:
`ValidateImageRef` (image policy) and `BuildPVC` (PVC from a `PVCConfig`).

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
for the full type/field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the handler interfaces, and `examples/` for runnable applications.

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.
