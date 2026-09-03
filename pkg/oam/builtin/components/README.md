# OAM Built-in Component Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/components.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)

Package `components` implements `oam.ComponentHandler` for the built-in component
types. Each handler parses a typed config from a component's `properties` and
produces the corresponding Kubernetes resources via kure's builders. Handlers are
registered with the transformer in `pkg/cmd/kurel` (`newBuiltinTransformer`), each
mapping a component `type` string to a handler implementing `CanHandle` +
`ToApplicationConfig`. Every built-in component handler also implements
`oam.PropertySchemaProvider` (`PropertySchema()`), declaring a constrained schema for its
user-facing properties so the downstream runtime can validate them before invocation. Nested
Kubernetes shapes are modeled field-by-field at full fidelity — closed objects, enums where the
API has them, and the same value validation real admission applies (ADR-036 L1: one PodSpec/
Container projection shared by every kind). Only genuine escape-hatch fields (`passthrough.object`,
`manifests`/`crd` inline content) and key→value maps whose keys are data (`nodeSelector`,
`resources.requests`/`limits`) stay open by design; the remaining open objects (`probes`,
`lifecycle`, `volumes`, `initContainers`/`sidecars` entries, `affinity`) are a known gap, not
the target shape. Every property
(including nested object fields and array item
schemas at every depth) carries a `Description`, surfaced in the downstream runtime's generated Handler API
Reference.

## Component types

| `type` | Produces | Summary |
|--------|----------|---------|
| `webservice` | Deployment, Service, ServiceAccount (+PVC) | HTTP service with replicas, probes, env, volumes. |
| `worker` | Deployment, ServiceAccount (+PVC) | Background workload (no Service/port). |
| `statefulset` | StatefulSet, headless Service, SA | Stateful workload with `volumeClaimTemplates`. |
| `daemonset` | DaemonSet, SA (+Service if `port`) | Per-node daemon; honors `tolerations`. |
| `cronjob` | CronJob, SA (+PVC) | Scheduled job; cron `schedule` + history limits + CronJobSpec/JobSpec fields (see below; `podFailurePolicy` not yet projected). |
| `helmchart` | HelmRelease + Helm/OCIRepository, or rendered manifests | Helm via Flux (`native`) or client-side `template`. |
| `oci` | OCIRepository, Kustomization | Sync manifests from an OCI artifact (Flux). |
| `postgresql` | CNPG Cluster, Pooler, ObjectStore, Database | CloudNativePG database (backup/monitoring/pooling). |
| `passthrough` | any (verbatim) | Emit an arbitrary object as-declared (`clusterScoped` opt). |
| `crd` | CustomResourceDefinition(s) | CRDs from `inline`/`url`; rejects non-CRD docs. |
| `manifests` | any | Raw manifests from `inline`/`url` with namespace stamping + `scopeOverrides`. |

The five workload kinds emit their per-component ServiceAccount only when the
component does not author `serviceAccountName` (see "Pod-level properties" below).

## Common config

Most workload types (`webservice`, `worker`, `statefulset`, `daemonset`, `cronjob`)
share these fields, projected directly onto real `corev1` types (same
structural pattern as `ProbeConfig` holding `*corev1.Probe`) rather than a
hand-rolled parallel schema: `image` (validated — no untagged/`latest`), `env`
(`value` or `valueFrom` — mutually exclusive, matching `corev1.EnvVar`'s own
doc comment ("cannot be used if value is not empty"); `valueFrom` is one of
`secretKeyRef`, `configMapKeyRef` (both accept `optional`, rejecting a
present non-boolean value rather than silently treating it as unset; a key
other than `name`/`key`/`optional` in either is rejected outright too, rather
than being silently ignored), `fieldRef`
(`apiVersion` must be `v1` if authored — the only field-label conversion
Kubernetes has ever shipped for the downward API; omitting it also defaults to
`v1`; a present-but-non-string `apiVersion` (e.g. a bare YAML number) is
rejected rather than silently treated as absent, same as every other typed
scalar field in this document; `fieldPath` is validated against the exact set real admission accepts
for an env var fieldRef — `metadata.name`/`metadata.namespace`/`metadata.uid`,
`spec.nodeName`/`spec.serviceAccountName`, `status.hostIP`/`status.hostIPs`/
`status.podIP`/`status.podIPs` — plus the `metadata.labels['KEY']`/
`metadata.annotations['KEY']` subscript forms, each with `KEY` checked as a
qualified name; a field like `status.phase` builds but is rejected by
admission, so it is rejected here too; a key other than `fieldPath`/
`apiVersion` is rejected outright too, rather than being silently ignored),
`resourceFieldRef` (`resource` must be
one of `limits.cpu`, `limits.memory`, `limits.ephemeral-storage`,
`requests.cpu`, `requests.memory`, `requests.ephemeral-storage`, or a
`requests.hugepages-<size>`/`limits.hugepages-<size>` selector — the downward
API cannot project an arbitrary extended resource such as
`limits.nvidia.com/gpu`, unlike a plain `resources` map below; an authored
`divisor` must be one of the canonical unit strings admission accepts for that
resource's family — `1m`/`1` for cpu, one of `1`/`1k`/`1M`/`1G`/`1T`/`1P`/`1E`/
`1Ki`/`1Mi`/`1Gi`/`1Ti`/`1Pi`/`1Ei` for memory/ephemeral-storage/hugepages — a
zero-valued divisor such as `"0"` is rejected outright rather than silently
treated as absent: Kubernetes' own zero-value defaulting substitutes 1 for a
zero divisor, so silently accepting one would change the emitted unit without
the author asking for it; a present-but-non-string
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
kubelet-time `CreateContainerConfigError`; a key other than
`resource`/`containerName`/`divisor` is rejected outright rather than
silently ignored, since a typo such as `divisorr` would otherwise leave the
divisor unset — Kubernetes treats a zero divisor as its default of 1,
changing the emitted unit),
`fileKeyRef` (`volumeName`/`path`/`key` required, `optional` accepted (same
non-boolean rejection as above); corev1's `EnvFiles` feature; `volumeName`
must be a valid DNS-1123 label and `key` a valid (relaxed) env var name,
matching real admission's own `validateFileKeySelector`; `path` must be
relative and must not contain a `..` backstep component, per this repo's own
path-safety convention; **deferred:** `volumeName` is not cross-checked
against the component's declared `volumes` — env parsing runs before volume
parsing in every call site — so a `fileKeyRef` naming a nonexistent volume,
or an existing but non-`emptyDir` one, builds successfully here but is
rejected at real admission: `validateFileKeyRefVolumes` requires the
referenced volume be specifically `emptyDir`, not any other source type this
schema supports; see the doc comment on `parseFileKeyRef` in `common.go`; a
key other than `volumeName`/`path`/`key`/`optional` is rejected outright too,
rather than being silently ignored) —
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
as `env`'s `secretKeyRef`/`configMapKeyRef` above; a key other than
`name`/`optional` inside either nested ref (e.g. a misspelled `optoinal`) is
rejected outright too, rather than silently leaving the ref at its required
default; a present-but-malformed
`configMapRef`/`secretRef` (e.g. a scalar instead of an object) is rejected
outright rather than silently treated as absent — the latter would let the
other, well-formed ref alone satisfy the "exactly one" check below and
quietly discard the malformed one instead of reporting it); a key other
than `prefix`/`configMapRef`/`secretRef` (e.g. a misspelled `prefx`) is
rejected outright too, rather than being silently ignored while the rest of
the entry still builds — on `prefix` specifically, that previously emitted
an unprefixed import instead of the intended one),
`resources` — a `corev1.ResourceRequirements` projection: `requests`/`limits`
accept `cpu`/`memory` (defaults 100m/128Mi — subject to the environment
policy's `MaxCPU()`/`MaxMemory()` maxima the same as an authored value; see
"Policy defaults & enforcement ordering" below) plus any other well-formed
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
today; the container-level `claims` list (Dynamic Resource Allocation) is
deliberately not covered — it only *references* pod-level claims by name, and
the pod-level `resourceClaims` property that declares them is now accepted
(see Pod-level properties below, go-kure/launcher#342), so what remains
missing is the container-side reference list alone, tracked with the rest of
DRA support, see `parseResources`'s doc comment), `command`/`args` (each element must
be a string — **note:** unlike every other array field in this schema,
`command`/`args` still silently drop a non-string element rather than
rejecting it; `lifecycle.{postStart,preStop}.exec.command` below was fixed to
reject, but the top-level `command`/`args` fix was deliberately left out of
that change to keep it self-contained to `common.go`'s `parseLifecycleHandler`
— touching `parseCommand`/`parseArgs` would ripple into all 7 call sites across
every kind component), `probes`
(rejected outright if authored with a non-object value, e.g. `probes: true`,
and likewise for each of its own `readiness`/`liveness`/`startup` keys, e.g.
`probes: {liveness: true}` — same two-level presence-then-type-check shape as
`lifecycle` below, instead of silently discarding the authored health check;
a key other than `readiness`/`liveness`/`startup` (e.g. a misspelled
`probes: {live: {...}}`) is rejected outright too, rather than matching none
of the three recognized keys and silently producing no probe at all; a key
other than the ten recognized fields inside a single probe object — the four
handlers below plus the six timing fields further down — (e.g. a misspelled
`failureTreshold`) is rejected outright too, instead of silently falling
back to Kubernetes' own default for the field the typo was meant to
override;
httpGet/tcpSocket/exec/grpc — exactly one handler may be authored; a
present-but-non-object value for any of the four (e.g. `httpGet: "invalid"`)
is rejected outright, even when paired with a well-formed sibling handler,
rather than silently discarded while the sibling wins; an authored probe
object with none of the four handlers present (e.g. `probes: {liveness:
{periodSeconds: 10}}`) is rejected too, matching real admission
(`validateHandler`'s `numHandlers == 0` check) instead of silently discarding
the whole authored probe; `exec.command` must be
a non-empty array of strings — a present-but-wrong-type `command` (e.g. a
bare string instead of an array) or an array containing a non-string element
is rejected the same way, instead of silently producing no probe at all,
mirroring `lifecycle.{postStart,preStop}.exec.command` below; a key other
than `command` inside a probe `exec` object (e.g. a misspelled `commnad`) is
rejected outright too, instead of silently ignored while the intended
command never overrides the default; a string `port` is a named container port, not
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
rejected the same way; `grpc.service`, if authored, must be a string — a
present-but-non-string value (e.g. `service: 123`) is rejected rather than
silently treated as absent (which would check the overall server instead of
the intended named service) — and must be no more than 63
characters (mirrors `validateGRPCService`'s length cap — the gRPC
health-checking service name is not DNS-1123 formatted, but admission still
bounds its length), and `grpc.port` is always numeric regardless of any
kind's named-port rules — a named `grpc.port` is rejected outright with its
own message, never resolved against a declared container port; a key other
than `port`/`service` inside a probe `grpc` object (e.g. a misspelled
`servcie`) is rejected outright too, instead of silently ignored;
`tcpSocket.host`, if authored, is preserved on the probe — a
present-but-non-string value (e.g. `host: 123`) is rejected the same as
every other typed scalar field in this document, instead of silently
discarded while the probe still dials the Pod IP; when omitted,
`corev1.TCPSocketAction.Host`'s own doc comment says it then defaults to the
Pod IP, so no explicit default needs to be authored here — the same
optional-override shape as `httpGet.host` below; a key other than
`port`/`host` inside a probe `tcpSocket` object (e.g. a misspelled `hots`)
is rejected outright too, instead of silently ignored;
`httpGet.httpHeaders` itself and its entries are validated
rather than silently dropped/coerced — an authored non-array `httpHeaders`
value (e.g. a single header object instead of a list) is rejected the same
as a non-object entry within it, a missing/empty/invalid `name`
(`validation.IsHTTPHeaderName`, matching `validateHTTPGetAction`), or a
present-but-non-string `value` are all rejected instead of quietly
disappearing or turning into `""`; a key other than `name`/`value` in a
header entry (e.g. a misspelled `vaule`) is rejected outright too, rather
than being silently ignored; an omitted `value` key still defaults to
`""` — shared by `lifecycle.{postStart,preStop}.httpGet` below via the same
parsing helper; a key other than `port`/`path`/`host`/`scheme`/`httpHeaders`
anywhere in an httpGet object (e.g. a misspelled `pth` for `path`) is
rejected outright too, instead of silently ignored while Kubernetes defaults
whatever field the typo was meant to override — the identical five-key
allow-list is checked independently in `lifecycle.{postStart,preStop}.httpGet`
below, which has its own copy of this handler; `path`/`host`/`scheme` are
all optional — an absent or empty
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
running the container with no startup/shutdown hooks; a key other than
`postStart`/`preStop` (e.g. a misspelled `lifecycle: {postStop: {...}}`) is
rejected outright too, rather than matching neither recognized key and
silently producing no hook at all;
`postStart`/`preStop`:
`exec` (every `command` element must be a string; a non-string element is
rejected, not silently dropped; a key other than `command` in the object,
e.g. a misspelled `commnad`, is rejected outright too, instead of silently
ignored)/`httpGet` (same named-port, `httpHeaders`,
unknown-key, and optional-`path`/`host`/`scheme` rules as `probes`
above)/`sleep` (`seconds` is required and must be a non-negative integer; a
key other than `seconds` in the object, e.g. a misspelled `sconds`, is
rejected outright too, instead of silently ignored) — at
most one of these three may be authored, and a present-but-non-object value
for any of them is rejected outright rather than silently discarded while a
well-formed sibling wins, same as `probes` above;
`tcpSocket` is rejected unconditionally, even when paired with another valid
handler such as `exec`, and regardless of its own value's shape (an
authored-but-malformed `tcpSocket`, e.g. a string, is rejected the same as a
well-formed one) — corev1 documents it as broken for lifecycle hooks,
and simply ignoring the extra key would silently drop the authored
`tcpSocket` while emitting only the other handler; a key on a `postStart`/
`preStop` value outside `httpGet`/`exec`/`sleep`/`tcpSocket` itself (e.g. a
misspelled `timeoutSeconds` alongside a valid `exec`) is rejected outright
too, rather than being silently ignored while the valid sibling handler
still builds; `postStart`/`preStop` are
each rejected outright if present with a non-object value, e.g. `preStop:
"flush"`, instead of silently discarding the whole hook),
`securityContext` (rejected outright if authored with a non-object value,
e.g. a scalar or array, instead of silently treating it as absent and
emitting a container with a nil security context; a key other than the
eleven recognized security-context fields (e.g. a misspelled
`readOnlyRootFileSystem`) is rejected outright too, instead of matching none
of them and silently discarding the whole hardening request;
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
authored hardening request was honored; additionally, `privileged: true`
combined with `allowPrivilegeEscalation: false` is rejected outright —
`corev1.SecurityContext.AllowPrivilegeEscalation`'s own field doc states it
is always true once a container runs privileged, so the pair would claim a
hardening guarantee the runtime cannot honor, the same contradiction shape as
`runAsUser: 0` with `runAsNonRoot: true` above),
`capabilities` (rejected if authored with a non-object value; a key other
than `add`/`drop` in the object, e.g. a misspelled `dorp`, is rejected
outright too, instead of silently ignored; `add`/`drop`
are each rejected if authored with a non-array value, e.g. `drop: ALL`
instead of a list, or with a non-string array element — an empty-string
element is silently skipped rather than rejected, since real admission places
no format constraint on a Capability string at all. Adding the literal
capability `CAP_SYS_ADMIN` alongside `allowPrivilegeEscalation: false` is
rejected outright — Kubernetes admission always treats a container holding
that capability as privilege-escalated regardless of the field's own value,
so the combination promises hardening the runtime cannot honor; the
unprefixed conventional form `SYS_ADMIN` is not rejected, matching real
admission's own exact-string scope (it checks only `CAP_SYS_ADMIN`,
literally). `add` is checked against the environment policy's
`AllowedContainerCapabilities`/`ForbiddenContainerCapabilities` accessor pair
(`enforceContainerCapabilities` in `enforce.go`) — a separate pair from
`oam.Policy`'s `AllowedCapabilities`/`ForbiddenCapabilities`/
`RequiredCapabilities`, which gate OAM trait-type usage (e.g. "ingress"), not
container Linux capability strings (e.g. "NET_ADMIN"); see `enforce.go`'s
`enforcePrivileged` doc comment for the naming-collision detail.
Default-allow, forbidden-list-first semantics: a nil/empty `Allowed` list
means no restriction and a nil/empty `Forbidden` list means no forbids, but a
capability present in both is rejected — forbidden always wins. `drop` is
never checked against policy — dropping a capability is strictly hardening.
Both the authored value and every policy-list entry are normalised
(upper-cased, `CAP_` prefix stripped) before comparison, so `NET_ADMIN`,
`CAP_NET_ADMIN` and `net_admin` are treated as the same capability on both
sides; `ALL` is special-cased symmetrically on both sides. An authored entry
that normalises to `ALL` is rejected whenever `Forbidden` is non-empty, even
if no entry normalising to `ALL` is itself listed in `Forbidden`, since `ALL`
necessarily grants every forbidden capability. Conversely, a `Forbidden`
entry that normalises to `ALL` rejects every authored `add` entry
unconditionally — `forbidden: ["ALL"]` means no capability may be added at
all, regardless of what `Allowed` says), `seccompProfile` (rejected outright if authored with
a non-object value, e.g. `seccompProfile: RuntimeDefault`, instead of silently skipping the field
and dropping the requested sandboxing entirely; a key other than
`type`/`localhostProfile` in the object, e.g. a misspelled `locahost`, is
rejected outright too, instead of silently ignored; `type` is required whenever the
`seccompProfile` object is authored at all, matching real admission's own
`field.Required` — omitting it (e.g. authoring only `localhostProfile` with
no `type` key) is rejected rather than silently discarding the whole
profile; `localhostProfile` is rejected outright when authored alongside
`type: RuntimeDefault`/`Unconfined` (only meaningful for `type: Localhost`),
including a present-but-non-string value in that position (e.g.
`localhostProfile: 123`) — the same present-but-wrong-type rejection as
every other typed scalar field in this document, not silently treated as
absent while the contradictory type is accepted as authored; when `type` is
`Localhost`, `localhostProfile` must be
relative and must not contain a `..` backstep component, matching
`corev1.SeccompProfile.LocalhostProfile`'s own doc comment — "must be a
descending path, relative to the kubelet's configured seccomp profile
location" — and this repo's own path-safety convention), `seLinuxOptions`
(also rejected outright if authored with a non-object value, same reasoning as
`seccompProfile` above; a key other than `user`/`role`/`type`/`level` in the
object, e.g. a misspelled `tpye`, is rejected outright too, instead of
silently ignored;
`user`/`role`/`type`/`level` are each rejected if authored with a
non-string value, e.g. `type: 123`, instead of silently discarding just that
sub-field — if it were the only one set, the whole SELinux context would
otherwise vanish rather than reporting the malformed input),
`appArmorProfile` (same "`type` required when authored" rule as
`seccompProfile` above, the same non-object rejection as `seccompProfile`
and `seLinuxOptions`, the same unknown-key rejection (only
`type`/`localhostProfile` are recognized) as `seccompProfile` above, and the
same `localhostProfile`
mutual-exclusivity-with-RuntimeDefault/Unconfined and present-but-non-string
rejection as `seccompProfile` above), `procMount` (`Default`|`Unmasked`; a present-but-non-string
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
`volumes` (the `volumes` property itself, if authored, must be an array — a
present-but-non-array value, e.g. `volumes: {name: data}`, is rejected
outright rather than silently treated as absent and building without the
requested volume/mount, the same presence-then-type-check shape as `probes`
above; each entry in that array must itself be an object — a non-object
entry, e.g. `volumes: [data]`, is rejected outright rather than silently
skipped while any well-formed sibling entries still build; every volume's
`name`, regardless of source type — `hostPath`,
`emptyDir`, `pvc`, `configMap`, `secret`, etc. — must be a valid DNS-1123
label, matching how real admission validates every `corev1.Volume.Name`; an
invalid name, e.g. containing `/`, builds successfully but is rejected at Pod
admission; two volumes sharing the same valid name are likewise rejected —
Pod volume names must be unique (`validateVolumes`' own duplicate check),
so the second entry is caught at parse time rather than only at admission;
two volumes with distinct names sharing the same `mountPath` are also
rejected — a container's own volume mounts must have unique mount paths
(`ValidateVolumeMounts`' own duplicate check), so only the first of two
colliding mounts would ever actually be reachable at admission; `readOnly`,
if authored, must be a boolean — a present-but-non-boolean value (e.g.
`readOnly: "true"`) is rejected rather than silently defaulting to a
writable mount (same fix applied to `initContainers`/`sidecars`' own
`volumeMounts` entries below, which had the identical gap);
`name` and `mountPath` are both required on every entry — a present-but-
non-string value (e.g. a numeric `mountPath`) collapses to the same empty
value as an absent one, so both are rejected the same way: an entry missing
either, or authoring one with the wrong type, previously built with no
volume and no mount for that entry instead of reporting what was missing;
`emptyDir.sizeLimit`, if authored, is parsed as a
`resource.Quantity` and rejected if negative (e.g. `"-1Gi"`) — syntactically
valid but a storage quantity real Kubernetes resource validation refuses,
same as `resources`' own quantity fields above; a present-but-non-string
value (e.g. `sizeLimit: 1048576`) is rejected the same way, instead of
failing the bare type assertion silently and building an emptyDir with no
size limit at all; `pvc.size` is required (the
same missing-vs-wrong-type rejection as `name`/`mountPath` above) and, once
present, is validated the same way as `emptyDir.sizeLimit`; `pvc.storageClass`,
if authored, must be a string — a present-but-non-string value (e.g. a bare
number) is rejected rather than silently building with the cluster default
class — and, once confirmed a string, a non-empty value must also be a valid
DNS-1123 subdomain, matching `ValidatePersistentVolumeClaimSpec`'s own
`ValidateClassName` check; a malformed class name (e.g. containing `_` or
`!`) previously built successfully and was rejected only at admission. An
explicitly authored `storageClass: ""` is preserved as an
opt-out request (Kubernetes distinguishes a nil `StorageClassName` — use the
cluster default — from a pointer to `""` — request no class) rather than
being collapsed to "absent" and silently provisioned through the default
class; this distinction is not available for `volumeClaimTemplates.storageClass`
below, since the underlying `go-kure/kure` `CreateVolumeClaimTemplate` helper
that path builds on only ever sets a non-empty storage class name on the
generated claim — a cross-repo limitation, out of scope here.
`pvc.accessModes`, if authored, must be a non-empty array of non-empty
strings, each one of the three real `corev1.PersistentVolumeAccessMode`
values — a present-but-non-array value (e.g. a bare string) or a non-string
element is rejected outright too, instead of silently falling through to the
`ReadWriteOnce` default while discarding the author's actual list; an
authored empty array (`accessModes: []`) is rejected outright as well —
`ValidatePersistentVolumeClaimSpec` itself requires at least one access
mode, so silently defaulting an explicit empty list to `ReadWriteOnce` would
build a claim the author never asked for rather than reporting the
malformed input; an absent `accessModes` key still defaults to
`ReadWriteOnce`, unchanged — only an authored-and-empty array is treated as
malformed; `ReadWriteOncePod`
combined with any other access mode is rejected outright — real Kubernetes
requires it be the claim's only mode; the same parser
backs `volumeClaimTemplates.accessModes` below; the generated
`PersistentVolumeClaim` object's own Kubernetes name is the component's
`<app.Name>-<pod-local volume name>`, not the bare pod-local name — two
components in the same namespace that both author a `pvc` volume named
`data` would otherwise emit two colliding `PersistentVolumeClaim/data`
objects; the pod-local `Volume.Name` and its `VolumeMount` reference stay
unqualified, only the PVC object's name and the matching
`Volume.PersistentVolumeClaim.ClaimName` are qualified (`qualifyPVCNames` in
`common.go`, called once per kind's `Generate()`) — qualification is keyed
off the stable pod-local `Volume.Name` rather than the PVC's own (mutable)
object name, so a second `Generate()` call on the same component instance
reproduces the same qualified name instead of re-qualifying an
already-qualified one (e.g. `app-data` becoming `app-app-data`); the join
itself escapes interior hyphens in each half (`escapeForPVCQualification`)
before concatenating, since plain `<appName>-<localName>` string
concatenation is not collision-free when either half itself contains a
hyphen — component `a-b` with volume `data`, and component `a` with volume
`b-data`, would otherwise both qualify to `a-b-data`; `hostPath.path`
is likewise required and must be absolute — a raw host filesystem path has
no defined root to resolve a relative value against, and real admission
(`validateHostPathVolumeSource`) rejects a relative one the same way;
`configMap.configMapName` and `secret.secretName` are each required the same
way as `pvc.size`; a fully-authored entry whose `type` matches none of the
five recognized sources — including `type` omitted entirely — is rejected
outright rather than silently producing no volume or mount for an entry the
author clearly intended to add; each recognized type's own field set is
closed the same way `securityContext` and its nested objects are elsewhere
in this file — an unrecognized key on a `hostPath`, `emptyDir`, `pvc`,
`configMap`, or `secret` entry (e.g. a typo'd `sizeLmit` instead of
`sizeLimit`) is rejected rather than silently ignored, which previously let
the author's intended value take no effect with no error explaining why),
`initContainers`, `sidecars` (each entry's own `volumeMounts[].readOnly`
must be a boolean and `volumeMounts[].subPath` must be a string when
present — same presence-then-type-check shape as `volumes.readOnly` above;
`volumeMounts[].mountPath` must also be unique within that entry's own
mount list, the identical rule as `volumes`' duplicate-mountPath check
above, since each `initContainers`/`sidecars` entry is its own container;
each entry also accepts its own `securityContext`, the identical field set
and validation as the main container's own `securityContext` described
below — see that prose for the field list rather than restating it here),
and `affinity`.

### Pod-level properties

The five kind components also share one pod-level property surface
(go-kure/launcher#342, ADR-036 L1), parsed by `parsePodSpec` (`podspec.go`)
straight into a `corev1.PodSpec` and rendered by the shared `buildPodSpec`,
which every kind assigns to its pod template. Property names are the
`corev1.PodSpec` JSON names, except three that carry a `pod` prefix because
the bare name is already a property at some kind: `podSecurityContext`
(container `securityContext` exists everywhere), `podResources` (container
`resources`), and `podActiveDeadlineSeconds` (cronjob's job-level
`activeDeadlineSeconds`). Each accepted value is validated the way real
admission validates it when that check is deterministic from the document
alone (DNS-1123 names, enums, IP literals, duplicate names, the cross-field
exclusions listed below, and the `os.name` contract: a `windows` pod may not
set the Linux-only pod and container fields, a `linux` pod may not set
`windowsOptions`); checks needing cluster state (host ports under
`hostNetwork`, feature gates, RuntimeClass existence) are left to the cluster.

| Property | Type | Effect | Kind |
|----------|------|--------|------|
| `serviceAccountName` | string | **Behavior-changing.** Pods run as the named account and the per-component ServiceAccount is *not* generated. The `rbac` trait binds its Role/ClusterRole to this account via `oam.ServiceAccountNamer` (see below). The generated account carries `automountServiceAccountToken: false`; an authored account is owned elsewhere and its own setting governs, so authors who want the pod not to mount a token set the pod-level `automountServiceAccountToken: false` explicitly — the handler does not inject it. | additive when unset |
| `automountServiceAccountToken` | bool | Pod-level token automount override. | additive |
| `terminationGracePeriodSeconds` | int ≥ 0 | Grace period before SIGKILL. | additive |
| `podActiveDeadlineSeconds` | int 1..MaxInt32 | Pod-level `activeDeadlineSeconds`. **cronjob only** — apps/v1 rejects it on Deployment/StatefulSet/DaemonSet templates, so the other kinds neither publish nor accept it. | additive |
| `dnsPolicy`, `dnsConfig` | enum, object | `ClusterFirstWithHostNet`/`ClusterFirst`/`Default`/`None`; `dnsConfig` = `nameservers` (≤3 plain IPv4/IPv6 literals — a zone-scoped address such as `fe80::1%eth0` is rejected, matching upstream's `net.ParseIP`-based check, which has no notion of a zone), `searches` (≤32 entries whose joined length, separators included, is ≤2048 characters — the `resolv.conf` search-line limit, so 32 individually valid domains can still be refused), `options[]{name,value}`. `None` requires at least one nameserver. | additive |
| `nodeSelector`, `nodeName`, `schedulerName`, `priorityClassName`, `preemptionPolicy`, `runtimeClassName`, `schedulingGates[]{name}`, `schedulingGroup{podGroupName}` | scheduling | Placement fields; gate names must be unique. `nodeName` is a DNS-1123 subdomain (a Node is an ordinary object, so an invalid value is refused at admission, not merely unmatched). `schedulerName` is deliberately *not* validated: upstream constrains its form nowhere, so an arbitrary string is a legal document and rejecting one here would refuse work a cluster accepts. | additive |
| `hostNetwork`, `hostPID`, `hostIPC` | bool | Host namespaces. **Policy-gated**: rejected by `ApplyPolicy` unless `AllowHostNetwork()`/`AllowHostPID()`/`AllowHostIPC()` allow them (`enforce.go`'s `enforceHostNamespaces`, called from all five kinds; `NoopPolicy` denies all three). | additive |
| `shareProcessNamespace` | bool | Mutually exclusive with `hostPID: true`. | additive |
| `hostname`, `subdomain`, `setHostnameAsFQDN`, `hostnameOverride`, `hostAliases[]{ip,hostnames}` | naming | `hostname`/`subdomain` are DNS-1123 labels; `hostnameOverride` is a ≤64-char subdomain and cannot combine with `hostNetwork` or `setHostnameAsFQDN`. `hostAliases[].ip` is a plain IPv4/IPv6 literal, zone suffixes rejected as for `dnsConfig.nameservers`. | additive |
| `podSecurityContext` | object | The full `corev1.PodSecurityContext` field set (`runAsUser`/`runAsGroup`/`runAsNonRoot`/`fsGroup`/`fsGroupChangePolicy`/`supplementalGroups`/`supplementalGroupsPolicy`/`sysctls`/`seLinuxOptions`/`seLinuxChangePolicy`/`seccompProfile`/`appArmorProfile`/`windowsOptions`), closed and validated like the container `securityContext`. `sysctls[].name` must match the sysctl grammar (≤253 characters of dot- or slash-separated lowercase alphanumeric segments) and be unique within the list. `windowsOptions.hostProcess: true` additionally requires `hostNetwork: true`, which upstream demands of any pod containing HostProcess containers. The `runAsUser: 0` / `runAsNonRoot: true` contradiction is judged per container on the *effective* values once the containers are assembled, not on this object alone — a container-level `runAsUser` overrides the pod-level one, so the pair is a valid document when every container names a non-root UID, and the deferred check also catches a container-level `runAsUser: 0` under a pod-level `runAsNonRoot`. **Partly policy-gated**: `windowsOptions.hostProcess: true` is rejected unless `AllowPrivileged()` allows it (a HostProcess pod runs with the node's own privileges, and upstream forces every container in it to be HostProcess too); every other field has no policy hook. | additive |
| `imagePullSecrets[]{name}`, `enableServiceLinks`, `os{name}`, `hostUsers`, `readinessGates[]{conditionType}`, `resourceClaims[]{name, resourceClaimName \| resourceClaimTemplateName}`, `podResources{requests,limits}` | misc | `podResources` accepts only `cpu`, `memory` and `hugepages-<size>` (pod-level resources have no ephemeral-storage or extended resources); claim names must be unique and name exactly one source. `hostUsers: false` cannot combine with `hostPID` or `hostIPC` (upstream forbids both outright); it stays authorable alongside `hostNetwork`, which upstream forbids only on a cluster without user-namespace host-network support, so whether that pair is accepted is a property of the target cluster rather than of the document. **`podResources` is policy-gated**: its cpu and memory requests and limits are checked against `MaxCPU()`/`MaxMemory()`, the same budget the container `resources` are checked against. | additive |

Deliberately **not** accepted — each is rejected with an error naming the
reason rather than silently ignored: `ephemeralContainers` (added to a running
pod through its `ephemeralcontainers` subresource, never on a template),
`priority` and `overhead` (the Priority and RuntimeClass admission
controllers, on by default, reject pods that set them and derive them from
`priorityClassName`/`runtimeClassName`), and `serviceAccount` (deprecated alias
of `serviceAccountName`).

Every kind config implements `oam.ServiceAccountNamer` (`pkg/oam/handler.go`),
returning the authored `serviceAccountName` or, when unset, the component name
the generated ServiceAccount carries. The `rbac` trait
(`pkg/oam/builtin/traits/rbac.go`) binds its RoleBinding/ClusterRoleBinding
subject to that name, so binding follows the authored account; the Role and
binding objects keep their component-derived names.

The unauthored fallback is the kind config's own `Name`, set by each handler's
`ToApplicationConfig` from `component.Name`. On every supported path that is
the same string as the `stack.Application`'s name, because the transform builds
the Application from that same component — but that agreement is an invariant
of the call site, not of the types, so `Generate` does not re-derive the name
from `app.Name` alongside it. Both the generated ServiceAccount's name and the
pod's `serviceAccountName` are resolved by asking the `oam.ServiceAccountNamer`
implementation itself (`generationServiceAccountName` in `podspec.go`), which
is the same method the `rbac` trait reads. The account a RoleBinding names and
the account the pods run as are therefore one value by construction, and cannot
drift apart if the two names ever differ. The shared kind test asserts the three
agree on every kind, including against a deliberately mismatched Application
name.

A config built directly rather than through `ToApplicationConfig` carries no
`Name`; its namer then returns `""` — the "fall back to the component name"
convention `decoratorBase.ServiceAccountName` documents and the `rbac` trait
implements — and the Application's own name stands in.

`securityContext.privileged: true` is rejected unless the environment policy's
`AllowPrivileged()` allows it (`enforce.go`'s `enforcePrivileged`).
`securityContext.capabilities.add` is separately enforced against
`AllowedContainerCapabilities()`/`ForbiddenContainerCapabilities()` (see above,
`enforce.go`'s `enforceContainerCapabilities`); every other `securityContext`
field still has no policy hook. Both checks cover the main container and
every `initContainers`/`sidecars` entry (go-kure/launcher#312's shared
`enforceExtraContainer` helper), not just the main container.

The pod-level surface carries two policy checks of its own, both called from
all five kinds' `ApplyPolicy` next to `enforceHostNamespaces`:
`enforcePodResources` measures `podResources`' cpu and memory requests and
limits against `MaxCPU()`/`MaxMemory()` — without it an author could keep the
container under the maximum and put the oversized request on the pod, which
the scheduler charges against the node identically — and
`enforcePodHostProcess` rejects `podSecurityContext.windowsOptions.hostProcess:
true` unless `AllowPrivileged()` allows it, the Windows spelling of
`securityContext.privileged` (`enforcePrivileged` checks the container-level
`windowsOptions.hostProcess` too, so the two spellings share one switch). An
unset `MaxCPU`/`MaxMemory` leaves pod-level resources unconstrained: pod-level
resources are written only when authored, so there is no generated default to
fall back on.

A `volumes` entry sourced from `hostPath` is rejected unless the environment
policy's `AllowHostPathVolumes()` allows it (`enforce.go`'s
`enforceHostPathVolumes`, the same reused-mechanism shape as
`enforcePrivileged` above); like `enforcePrivileged`, it is called from all
five kind components' `ApplyPolicy`, not just one — a hostPath volume mounts
an arbitrary path from the node's own filesystem into the Pod, so an
unenforced policy denial here is a container-escape-adjacent gap, not merely
a style one.

Setting any `securityContext` field makes the container's `SecurityContext`
non-nil, which opts it out of the `security-context` trait's nil-only
backfill for every *other* `SecurityContext` field too. If a component uses
both, the trait's `Generate()` pass runs later and unconditionally overwrites
`container.SecurityContext` — the trait always wins when both are applied to
the same component. This applies to a component-authored `initContainers`/
`sidecars` entry's own `securityContext` too, not just the main container's:
the trait's `applyToPodSpec` (`pkg/oam/builtin/traits/security_context.go`)
overwrites `SecurityContext` on every entry of both `podSpec.Containers`
(which includes rendered sidecars) and `podSpec.InitContainers`, so it wins
over an init container's or sidecar's own authored value the same way it
wins over the main container's. Use the trait for a safe, complete
PSA-consistent default; use this property for raw, partial, full-fidelity
authoring.

`env`, `envFrom`, `resources`, `lifecycle`, `securityContext`, `workingDir`, and
`probes` are each schema fragments parameterized by a `reserved bool` (mirroring
`pkg/oam/builtin/traits/schema.go`'s `schemaNetworkPolicy(reserved bool)`):
every built-in call site passes `false` today. Deciding which of these fields
should be platform-reserved (rejecting any authored value via
`PropertySchema.PlatformReserved`/`enforcePlatformReserved`) is a consumer-side
policy choice, not something this shared schema hardcodes.

## Policy defaults & enforcement ordering

A container's effective cpu/memory requests and limits are assembled in three
tiers, in precedence order:

1. **Authored** — whatever the component's `resources` property set explicitly.
2. **Policy default** — `ApplyPolicy` fills any request/limit the author left
   unset from `oam.Policy.DefaultCPURequest()`/`DefaultMemoryRequest()`/
   `DefaultCPULimit()`/`DefaultMemoryLimit()` (`enforce.go`'s
   `applyDefaultQuantity`).
3. **Intrinsic handler default** — `buildResourceRequirements` (`common.go`)
   fills anything still unset at `Generate()` time: 100m CPU request, 128Mi
   memory request, and a memory limit mirroring the (possibly just-defaulted)
   memory request.

`oam.Policy.MaxCPU()`/`MaxMemory()` are enforced against the *effective*
value — what `Generate()` will actually emit, after all three tiers — not
just the authored/policy-defaulted `resources` field. Prior to launcher#251
the enforcement check ran before the intrinsic tier was computed, so an
application that omitted `spec.resources` entirely could ship a Deployment
whose 100m CPU / 128Mi memory intrinsic defaults exceeded the enforced
maximum. `enforceMaxResources` (`enforce.go`) closes this by calling the same
`buildResourceRequirements` the generator calls, enforcing against its
result, and discarding it — `buildResourceRequirements` deep-copies its
maps, so this is read-only and generated output is unchanged. When a value
came from the intrinsic tier specifically (absent from the authored/policy-
defaulted value, present only after the intrinsic fallback), the error names
it as a "generated default" so the mismatch isn't mysterious.

This three-tier effective-value enforcement applies to the five kind
components that call `buildResourceRequirements` on their main container
(`webservice`, `worker`, `cronjob`, `statefulset`, `daemonset`).
**`postgresql` is exempt**: `createCluster` forwards `c.Resources` straight
into `kurecnpg.ResourceOptions` behind a `!= ""` guard and never calls
`buildResourceRequirements`, so it has no intrinsic tier for its existing
direct-form checks to diverge from.

Init containers and sidecars receive the same intrinsic defaults
(`common.go`'s `buildResourceRequirements` call sites at the init-container
and sidecar builders), and `ApplyPolicy` enforces them too (go-kure/launcher#312):
each entry's resources, image registry, `securityContext.privileged`, and
`securityContext.capabilities.add` are checked against the same policy
methods the main container uses, via the shared `enforceExtraContainer`
helper (`enforce.go`). All five kind components enforce their
`initContainers`; only `webservice`, `worker`, and `statefulset` have a
`sidecars` schema key at all (`cronjob`/`daemonset` have no sidecars support,
per "Per-type highlights" below) so only those three enforce a sidecars
loop. Errors name the authored list position and container, e.g.
`initContainers[0] "init": image "docker.io/x/y:v1" is not from an allowed
registry [...]`.

`cronjob`'s `backoffLimit`/`completions`/`parallelism`/`activeDeadlineSeconds`/
`ttlSecondsAfterFinished`/`completionMode` properties (see "Per-type highlights"
below) are parsed and applied by a dedicated `JobSpecConfig`/`parseJobSpec`/
`applyJobSpec` (`common.go`), factored out separately from the fields above
because `batchv1.CronJob.Spec.JobTemplate.Spec` and a bare `batchv1.Job.Spec`
are the same `batchv1.JobSpec` type — the future `job` component (#279) reuses
this trio verbatim rather than duplicating it.

## Per-type highlights

- **webservice / worker** — `image`, `replicas` (default 1), `port` (webservice).
  The `webservice` handler implements the optional `oam.EndpointProvider`: it declares its own
  pods (`app: <component-name>`) on the declared `port` (its single `port` property drives both
  the container port and the Service port), letting a downstream platform synthesize generic
  app→app connections targeting a webservice. `worker` declares no in-cluster port and emits no
  Service, so it deliberately advertises no endpoint (not an `EndpointProvider`).
- **statefulset** — `serviceName` (headless) and `volumeClaimTemplates`
  (`name`, `mountPath`, `size`, `storageClass`, `accessModes`, plus the rest of
  `corev1.PersistentVolumeClaimSpec`). The StatefulSetSpec-level and
  claim-template field sets are classified in "StatefulSet-level and
  claim-template properties" below.
- **daemonset** — `tolerations` (`key`/`operator`/`value`/`effect`); `port`
  optionally adds a Service. No `sidecars` schema key (init containers only).
  DaemonSetSpec-level (go-kure/launcher#340, `daemonset_spec.go`): `updateStrategy`,
  `minReadySeconds`, `revisionHistoryLimit`. `appsv1.DaemonSetSpec` has five
  fields; `template` is the pod projection above and `selector` is
  builder-managed, which leaves these three.

  | Property | Type | Effect | Compatibility |
  |----------|------|--------|---------------|
  | `updateStrategy` | object | `type` (**required**, `RollingUpdate`\|`OnDelete`) and `rollingUpdate` (`maxUnavailable`, `maxSurge`), only accepted under `type: RollingUpdate`. `type: RollingUpdate` alone is accepted — the apiserver defaults the `rollingUpdate` object that upstream validation then requires. | additive |
  | `minReadySeconds` | int ≥ 0 | Seconds a new pod must stay ready before it counts as available. | additive |
  | `revisionHistoryLimit` | int ≥ 0 | Superseded ControllerRevisions kept for rollback. | additive |
  | `selector` | — | **Rejected outright**, not silently dropped: the selector is builder-managed (`app: <component>`), must equal the generated template labels, and is immutable once created. | **Behavior-changing** |

  `maxUnavailable` and `maxSurge` each accept a non-negative integer or a `"N%"`
  string with N ≤ 100 (the integer form is a pod count and is deliberately
  uncapped, matching upstream's `IsNotMoreThan100Percent`, which inspects only
  percentages). A leading sign (`"+50%"`) is rejected — upstream's own
  `IsValidPercent` form check is used rather than a `TrimSuffix`/`Atoi` pair,
  which would accept it. Unlike the statefulset kind, whose `maxUnavailable`
  "cannot be 0", either DaemonSet knob may be zero on its own; it is the
  **pair** that must have exactly one non-zero member. That rule is enforced
  against the *effective* pair, counting the API defaults for whichever half the
  document leaves out — `maxUnavailable` 1, `maxSurge` 0
  (`k8s.io/api/apps/v1/types.go`, the `RollingUpdateDaemonSet` field docs). So
  `maxSurge: 2` on its own is rejected here rather than at apply time (the
  defaulted `maxUnavailable: 1` makes both non-zero), and using surge means
  writing `maxUnavailable: 0` alongside it. The error names which half was
  defaulted, since that is the half absent from the author's YAML.

  Both knobs are published with **no declared schema type**, the same treatment
  `schemaResources` gives cpu/memory quantities. Launcher's `PropertyType` set
  has no int-or-string union, and declaring `string` would not merely understate
  what is accepted: property validation rejects a non-string outright, so the
  integer form the parser accepts could never reach it through a
  schema-validating consumer. Leaving the type unset skips that check and keeps
  both forms reachable; the property descriptions carry the constraint instead
  (go-kure/launcher#383).

  **Two rules here are deliberately stricter than upstream.** The API accepts
  both shapes; what it does with them differs. `updateStrategy.type` is required
  here, where the API defaults it to `RollingUpdate` and acts on that — so a bare
  `updateStrategy: {}` is a legal document whose entire meaning comes from
  defaulting rather than from anything written. `updateStrategy.rollingUpdate` is
  refused under `type: OnDelete`, where `ValidateDaemonSetUpdateStrategy`'s
  `OnDelete` branch is empty and the field is accepted and never read — the
  silently-ignored knob this projection exists to remove. Both are still
  *additive*: `updateStrategy` is a new
  property, so no document that built before this change can carry either shape.
  In the other direction the parser is laxer in exactly one place — upstream
  requires a non-nil `rollingUpdate` under `RollingUpdate`, but apiserver
  defaulting satisfies that, not the author, so `type: RollingUpdate` alone is
  accepted (the same reasoning the statefulset kind applies to its own optional
  `rollingUpdate`).

  Every accepted property is presence-gated: a document authoring none of them
  produces byte-identical output to before, because `DaemonSetSpecConfig.apply`
  writes only the fields that were authored. The one behavior change is
  `selector`: authored-document validation checks type names and identity, not
  property shape (`pkg/oam/property_validate.go:22-27`), so a `selector:` on a
  daemonset used to be silently ignored and now fails the build. That is the
  point — a silently dropped selector reads as applied.

  It is a behavior change against what the code did, not against what the format
  promised. `docs/oam/design-gvk.md` § Parser Strictness already states the
  contract — "Launcher rejects unknown fields in all launcher-native documents.
  An `app.yaml`, `kurel.yaml`, or `cluster.yaml` with unrecognised keys is a
  build error" — so a daemonset carrying `selector:` was never a valid
  `launcher.gokure.dev/v1alpha1` document, and the same-version stability promise
  ("every document that was **valid** under it remains valid") never covered it.
  The authored path simply had not implemented that strictness for this key; the
  rejection implements it, with a message that says why rather than the generic
  unknown-key error. `6aed090` (`feat(format): enforce policy and support
  securityContext on init/sidecar containers`) is the in-repo precedent for
  shipping this class of tightening under an unchanged version string.
- **cronjob** — `schedule` accepts a standard 5-field cron expression (e.g.
  `0 2 * * *`; not 6-field — a seconds field is rejected), one of the fixed
  `@`-descriptors (`@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`,
  `@midnight`, `@hourly`; `@reboot` is rejected, meaningless for a CronJob),
  or `@every <duration>` (e.g. `@every 1h30m`, validated via Go's
  `time.ParseDuration` — a malformed duration is rejected, not merely
  regex-matched). `restartPolicy` (default `OnFailure`),
  `successfulJobsHistoryLimit`/`failedJobsHistoryLimit`. No `sidecars` schema
  key (init containers only).
  CronJobSpec-level: `concurrencyPolicy` (`Allow`|`Forbid`|`Replace`; the API's
  own default is `Allow`, but this is only ever written when authored —
  `ConcurrencyPolicy` has no `omitempty`, so writing it unconditionally would
  add the key to every generated CronJob), `suspend`, `startingDeadlineSeconds`
  (`>= 0`), `timeZone` (a real IANA zone name, e.g. `Europe/Brussels`; an
  authored empty string is rejected outright, `Local` is rejected
  case-insensitively even though Go's own `time.LoadLocation("Local")`
  succeeds — Kubernetes' CronJob validation rejects it as server-dependent —
  and any other value is checked via `time.LoadLocation`, which this binary
  resolves from an embedded IANA database rather than the host's zoneinfo).
  JobSpec-level (projected onto `spec.jobTemplate.spec`, shared with the
  future `job` component — see "Common config" above): `backoffLimit`,
  `completions`, `parallelism` (`<= 100000` when `completionMode` is
  `Indexed`), `activeDeadlineSeconds` (must be a **positive** integer — `0` is
  rejected, not just negative values), `ttlSecondsAfterFinished`,
  `completionMode` (`NonIndexed`|`Indexed`; `Indexed` requires `completions`
  to also be authored). Every optional field above is presence-gated: omitting
  it never adds a key to the generated output, even where the corresponding
  Kubernetes default (e.g. `concurrencyPolicy: Allow`) would otherwise appear
  to have been authored.
  Known limitation: the plain 5-field `schedule` form accepts any 5
  whitespace-separated tokens with no per-field semantic check (e.g.
  `99 99 99 99 99` builds successfully here and is only rejected later, by
  Kubernetes' own API server) — this is deliberate, not an oversight: tightening
  it would reject documents that build successfully today, which is a breaking
  change under this project's additive-compatibility rule (see
  `docs/oam/design-gvk.md`). `podFailurePolicy` is not yet projected (#279).
- **helmchart** — `chart`, `version`, `delivery` (`native`|`template`), `source`
  (inline `url` or `{name,kind}` ref), `values`/`valuesFrom`, `valuesMode`
  (`inline` default | `configMap`), `driftDetection`, `install.crds`/`upgrade.crds`.
  `valuesMode: configMap` externalizes `values` into a literal `ConfigMap` resource
  — not a kustomize `configMapGenerator` (its hash-suffixed name has no HelmRelease
  entry in kustomize's built-in name-reference table to rewrite) — referenced from
  the `HelmRelease` via `spec.valuesFrom`. Emitting that `ConfigMap` requires a
  consumer that walks kure's `ManifestLayout` (the `layout.LayoutAugmenter` path). A
  layout-walking consumer keys a structural decision off that interface's mere
  presence, not just its side effect (`pkg/stack/layout/walker.go`): switching an
  existing component from `inline` to `configMap` (with non-empty `values`) moves
  its `HelmRelease` and source CR out of the parent bundle's flat resource set and
  into their own per-app sub-layout directory with its own `kustomization.yaml` — a
  visible output-path change in a GitOps repo, not an internal detail.
  `kurel build`'s own flat output (`pkg/cmd/kurel/build.go`) never walks the layout,
  so it rejects `valuesMode: configMap` at build time (naming the component)
  when the component has non-empty `values` — with no `values` to externalize
  there is nothing to emit, so the config is not wrapped as a `LayoutAugmenter`
  and `kurel build` accepts it — rather than emit a `HelmRelease` referencing a
  `ConfigMap` that isn't in the output. Use `inline` (the default) with
  `kurel build` for a component that does have `values`, or a consumer that
  walks the layout. Known limitation: the generated `ConfigMap`'s
  name (`<component>-values`) is not checked against a user-authored `configmap`
  trait's own `name` property — a component whose `configmap` trait happens to
  produce that exact name collides silently (`pkg/oam/validate.go` has no
  cross-trait resource-name uniqueness check at all, for any trait pair, so this
  is one instance of a pre-existing gap, not one this component introduces).
  Known limitation: the `prune-protection` trait only annotates resources
  returned from a wrapped config's `Generate` (`pkg/oam/builtin/traits/pruneprotection.go`)
  — layout-added resources such as this `ConfigMap` are not covered, because the
  generic `layout.LayoutAugmenter` forwarding (`pkg/oam/builtin/traits/decorator.go`)
  calls straight through to the wrapped augmenter and bypasses every other
  decorator, including `prune-protection`'s own. Combining `prune-protection`
  with `valuesMode: configMap` today does not protect the values `ConfigMap`
  from pruning; this is a gap in the shared trait-decorator framework, not
  specific to `helmchart` — tracked in go-kure/launcher#324. When both the generated
  `configMap` values reference and a user-supplied `valuesFrom` entry are present,
  the user's entry wins on overlapping keys (Flux merges `spec.valuesFrom` in list
  order, and the generated reference is added before the user's own entries);
  under `inline` mode, `spec.values` is merged last and always wins over any
  `valuesFrom` entry on overlapping keys. Known limitation: a values-only edit
  under `valuesMode: configMap` changes only the generated `ConfigMap`'s data —
  the `HelmRelease` itself (which carries only the stable ConfigMap name and
  key) is untouched, so whether that change is picked up promptly depends on
  the installed `helm-controller`'s own watch behavior for referenced
  `valuesFrom` objects, which this repo does not control or vendor. If
  reconciliation does not pick it up immediately, trigger one explicitly:
  `flux reconcile helmrelease <name>`. Known limitation: `delivery: template`
  rejects `releaseName`/`targetNamespace`/`valuesFrom`/`valuesMode: configMap`/
  `interval`/`driftDetection`/`install.crds`/`upgrade.crds` outright (compile-time
  validation error) rather than applying them — the client-side render always uses
  kure's defaults (`.Release.Name`/`.Release.Namespace` = `release`/`default`), and
  there is no way today to override release identity for a templated chart.
  `delivery: native` is unaffected. Known limitation, over-broad wording fixed: this list is the
  set of properties `delivery: template` rejects when **explicitly** authored — an inherited
  handler default (e.g. `valuesMode` with no property-level `configMap`) falls back to `inline`
  rather than erroring (`pkg/oam/builtin/components/helmchart.go:281-289`); same over-broad-wording
  class `go-kure/launcher#319` already fixed elsewhere in this file.

  **`delivery: template` output is partitioned by Helm hook group.** Every rendered manifest
  carrying a `helm.sh/hook` annotation (or a standalone `helm.sh/hook-weight`) is grouped by
  `(phase, weight)` via kure's `helm.SplitByHookWeight`, then emitted in hook-execution order —
  `pre-install, pre-upgrade, main, post-install, post-upgrade, <unknown, alphabetical>` — instead of
  raw chart-render order; a chart with no hook annotations at all yields one group and
  byte-identical output (a correctness fix, not a behavior change, for the unannotated case).
  `pre-delete`/`post-delete`/`pre-rollback`/`post-rollback`/`test` objects are dropped outright
  (kure does not surface them as static manifests) — mostly a bug fix, since a `test` Pod rendered
  as a static GitOps object would otherwise be reconciled on every apply, but it is silent data
  loss for a chart that relies on one of those hooks; `pkg/oam` has no logging channel to flag it.
  For a layout-walking consumer (the `layout.LayoutAugmenter` path, same mechanism as the
  `valuesMode: configMap` relocation above), more than one hook group makes `AugmentLayout` clear
  the component's flat `Resources` and replace them with one child `ManifestLayout` per group,
  named `<component>-NN-<phase-slug>` and chained via `DependsOn`, listing each child's preceding
  sibling in the order kure's `helm.SplitByHookWeight` synthesizes from Helm's hook phases — a
  combined install/upgrade ordering for GitOps reconciliation, not Helm's own per-operation
  execution order (kure `pkg/stack/helm/hooks.go:28-36`). `DependsOn` is set on every child
  regardless of placement, but kure's layout integrator only translates it into `spec.dependsOn`
  on a per-child Flux Kustomization CR under `FluxIntegratedPerLayout` placement (kure
  `pkg/stack/layout/manifest.go`'s `DependsOn` field doc); under coarser placement modes the
  children's resources are aggregated instead, and reconciliation ordering between hook groups is
  not separately enforced. A single-group chart's `AugmentLayout` is a no-op. Every `delivery: template`
  component becomes a `LayoutAugmenter` regardless of hook-group count — including a hook-free
  chart, since the wrap decision happens at config-construction time, before the network render
  that would reveal there is only one group — so even a hook-free templated chart now gets its own
  sub-layout directory under a layout-walking consumer, for no behavioural benefit; this mirrors
  `valuesMode: configMap`'s relocation caveat above (same mechanism, different trigger). Known
  limitation: the child directory name's DNS-1123 truncation (mirroring `valuesConfigMapName`'s own
  `sha256`-prefixed truncation above) makes same-name collisions vanishingly unlikely *within* one
  Application, but two different Applications with a same-named component still collide — component
  names are unique only within one Application (`pkg/oam/validate.go:186-189`), while emitted
  Kustomization CRs for hook-group children share one controller namespace; a pre-existing gap
  (inherited from a downstream consumer's reference implementation) that this partitioning newly exposes, not one
  it introduces. `kurel build`'s flat output **accepts** `delivery: template` — its `Generate`
  output is already the same flat union `AugmentLayout` would otherwise repartition, so nothing is
  lost by skipping the layout walk — while still rejecting `valuesMode: configMap` when it would
  actually emit a values `ConfigMap` (`emitsValuesConfigMap()`: `configMap` mode with non-empty
  `values`), same qualification as the over-broad-wording fix above, not an unconditional rejection
  of every `LayoutAugmenter`.
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

## StatefulSet-level and claim-template properties

The `statefulset` kind projects the whole `appsv1.StatefulSetSpec` field set it
owns, and each `volumeClaimTemplates` entry projects the whole
`corev1.PersistentVolumeClaimSpec`. Every field below is written only when
authored, so an unauthored StatefulSet keeps exactly what kure's
`CreateStatefulSet` put in the spec — `OrderedReady` and an empty
`updateStrategy` — and no existing output moves. The claim entry's five
pre-existing keys (`name`, `mountPath`, `size`, `storageClass`, `accessModes`)
now sit inside a closed key set, so a typo in an entry is reported instead of
being silently dropped.

Columns match the pod-level table above: **additive** means no document that
built before builds differently now; **behavior-changing** means an existing
document's meaning or acceptance moved.

| Property | Type | Effect | Kind |
|----------|------|--------|------|
| `podManagementPolicy` | enum | `OrderedReady` (the constructor's own value) or `Parallel`. | additive |
| `updateStrategy{type, rollingUpdate{partition, maxUnavailable}}` | object | `type` is `RollingUpdate` or `OnDelete`; `rollingUpdate` is rejected under `OnDelete`, mirroring `ValidateStatefulSetSpec`. `partition` is `>= 0`. `maxUnavailable` takes a positive integer or a 1–100% string — the schema publishes it as a string because launcher's `PropertyType` set has no int-or-string leaf, but an authored integer is accepted and carried through as an integer, not converted (go-kure/launcher#383). | additive |
| `revisionHistoryLimit` | int ≥ 0 | Retained controller revisions. | additive |
| `minReadySeconds` | int ≥ 0 | Readiness settling time before a pod counts as available. | additive |
| `persistentVolumeClaimRetentionPolicy{whenDeleted, whenScaled}` | object | Each is `Retain` or `Delete`. | additive |
| `ordinals{start}` | object | `start` shifts the replica ordinal range and is required once `ordinals` is authored; `>= 0`. | additive |
| `selector` (claim) | object | `matchLabels`/`matchExpressions`, with the operator arity rule — `In`/`NotIn` need at least one value, `Exists`/`DoesNotExist` none. An entirely empty `selector` is rejected: the apiserver would accept it as matching every volume, which is never what an author who wrote the key meant. | additive |
| `resources{requests,limits}` (claim) | object | `storage` is the only accepted resource name — `ValidatePersistentVolumeClaimSpec` reads `requests[storage]` and nothing else, so any other name would be silently ignored. Quantities must be positive. `apply` *merges* `requests` onto what the constructor already wrote, so `size` survives when only `limits` is authored. `requests.storage` is the long spelling of `size`; authoring both is an error. | additive |
| `volumeMode` (claim) | enum | Only `Filesystem` is accepted. The API's other mode, `Block`, is rejected at parse time: every claim entry requires a `mountPath` and this kind renders it as a filesystem `volumeMount`, while a block volume must be consumed through `volumeDevices`/`devicePath`. The claim and the pod template are validated as separate objects, so the apiserver would accept the mismatched pair and the pods would then fail at kubelet mount time. Raw block support is go-kure/launcher#385. | additive |
| `dataSourceRef{apiGroup,kind,name,namespace}` (claim) | object | Mirrors upstream `validateDataSourceRef`: `kind` and `name` are required non-empty with no format rule (a Kind is a CamelCase identifier, not a DNS name), `apiGroup` must be a DNS-1123 subdomain when non-empty, an omitted or empty `apiGroup` pins `kind` to `PersistentVolumeClaim` (the core group holds no other populator), and `namespace`, when set, is a DNS-1123 *label* (`ValidateNamespaceName`), not a subdomain. | additive |
| `volumeAttributesClassName` (claim) | string | DNS-1123 subdomain naming a `VolumeAttributesClass`. | additive |
| `storageClass` (claim) | string | **Behavior-changing.** A non-empty value is now validated as a DNS-1123 subdomain (`ValidateClassName`, the check `ValidatePersistentVolumeClaimSpec` runs and the one the `volumes[].pvc` path already applied). An invalid class name previously built a claim and was refused later by the apiserver; it is now refused here. A present-but-non-string value is likewise rejected rather than read as absent and provisioned through the cluster default class. | behavior-changing |
| `size` (claim) | quantity | **Behavior-changing.** `size` is now the short spelling of `resources.requests.storage` rather than a key of its own, and it is no longer unconditionally required — either spelling satisfies the requirement, and authoring both is an error. It must also now be **positive**: `0` was accepted before and is rejected now, matching upstream's `ValidatePositiveQuantityValue` on `requests[storage]`. (The `volumes[].pvc.size` surface on the other kinds still accepts `0`; that is tracked separately as go-kure/launcher#384.) Because "authored" is now read through `parseStringField`, an explicitly empty `size: ""` no longer counts as authoring the short spelling, so pairing it with `resources.requests.storage` is accepted rather than rejected as a double spelling. | behavior-changing |
| `name`, `mountPath`, `size` (claim) — wrong type | string | **Behavior-changing.** All three previously read through a bare type assertion, so a present-but-non-string value (YAML parses `size: 10e9` as a float and `mountPath: 7` as an int) collapsed to the empty string and was reported as the *requiredness* error — "missing required field 'size'" for a field the author had written. Each now rejects with an explicit type error naming the field and the type received. Acceptance does not widen: every one of these documents failed before and fails now, but with a message that points at the real defect. | behavior-changing |

Deliberately **not** accepted — each is rejected with an error naming the
reason rather than silently ignored:

- `spec.selector` (StatefulSet-level) — the builder derives it from the
  component name, and a StatefulSet's selector is immutable after creation.
- `volumeName` (claim) — pre-binding a claim *template* to one named
  PersistentVolume would point every replica at the same volume.
- `volumeMount` (claim) — not a claim-spec field at all; the container mount
  is authored as `mountPath` on the same entry.
- `dataSource` (claim) — when `dataSourceRef` carries no `namespace` the
  apiserver mirrors it into the superseded `dataSource` field, so authoring
  both is redundant; when it does carry a `namespace` the apiserver does not
  mirror, and `dataSource` must stay empty. Either way the field is authored
  through `dataSourceRef` alone.

## Extending

Custom component types implement `oam.ComponentHandler` (`CanHandle` +
`ToApplicationConfig`) and are registered alongside the built-ins. Exported helpers:
`ValidateImageRef` (image policy) and `BuildPVC` (PVC from a `PVCConfig`). A custom
`Generate()` that builds standalone PVCs from a `PVCConfig` list should qualify their
names the same way every built-in kind does — see `qualifyPVCNames` (unexported,
`common.go`) — to avoid two components colliding on the same pod-local volume name.

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
for the full type/field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the handler interfaces, and `examples/` for runnable applications.

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.

Every generated object owns its label maps. `Generate` hands back objects the caller
owns and routinely edits — stamping ownership, environment or version labels onto the
workloads it emits — so no two fields and no two objects ever share one
`map[string]string`. Concretely: the object's `metadata.labels`, the pod template's
`metadata.labels`, the Service selector and the `labelSelector` of every topology
spread constraint and pod anti-affinity term are separate maps with equal contents.
Inside this package `appLabels` and `selectorFrom` (both unexported, `common.go`) are
what enforce that: call `appLabels` once per assignment rather than hoisting its result
into a variable used twice, and let `selectorFrom` copy what it is handed rather than
store it. A custom handler outside this package cannot call either and does not need to
— the rule is to build a fresh `map[string]string` at each assignment site, and to
`maps.Clone` any map before storing it in a selector.

This is a correctness rule, not tidiness. A selector that aliases the pod template's
labels follows the caller's edits, and a topology spread constraint's `labelSelector`
defines the pod set over which skew is computed — so a version label leaking into it
makes a rollout spread each version separately instead of spreading the workload.
