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

`securityContext.privileged: true` is rejected unless the environment policy's
`AllowPrivileged()` allows it (`enforce.go`'s `enforcePrivileged`).
`securityContext.capabilities.add` is separately enforced against
`AllowedContainerCapabilities()`/`ForbiddenContainerCapabilities()` (see above,
`enforce.go`'s `enforceContainerCapabilities`); every other `securityContext`
field still has no policy hook. Both checks cover the main container and
every `initContainers`/`sidecars` entry (go-kure/launcher#312's shared
`enforceExtraContainer` helper), not just the main container.

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

## Per-type highlights

- **webservice / worker** — `image`, `replicas` (default 1), `port` (webservice).
  The `webservice` handler implements the optional `oam.EndpointProvider`: it declares its own
  pods (`app: <component-name>`) on the declared `port` (its single `port` property drives both
  the container port and the Service port), letting a downstream platform synthesize generic
  app→app connections targeting a webservice. `worker` declares no in-cluster port and emits no
  Service, so it deliberately advertises no endpoint (not an `EndpointProvider`).
- **statefulset** — `volumeClaimTemplates` (`name`, `size` — a `resource.Quantity`
  rejected if negative, same as `volumes`' `pvc.size` above — `storageClass`
  (a present-but-non-string value is rejected outright, same as `volumes`'
  `pvc.storageClass`), `accessModes`, `mountPath`), `serviceName` (headless).
- **daemonset** — `tolerations` (`key`/`operator`/`value`/`effect`); `port`
  optionally adds a Service. No `sidecars` schema key (init containers only).
- **cronjob** — `schedule` (5-field cron), `restartPolicy` (default `OnFailure`),
  `successfulJobsHistoryLimit`/`failedJobsHistoryLimit`. No `sidecars` schema
  key (init containers only).
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
  `delivery: native` is unaffected.
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
