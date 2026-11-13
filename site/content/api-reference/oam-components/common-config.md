---
title: "Common config"
weight: 1
---

The issues behind the entries on this page, and the sentences that told how an entry used to behave, are on the
[contributor page](../../contributing/oam-reference/common-config.md).

## Common config

`env`, `command`, `args`, `initContainers`, `sidecars` and `affinity` each read
their property with a bare comma-ok type assertion and, when that failed,
returned the zero value with no error. So a property authored with the **wrong
container type** — a mapping where the schema wants a list, a list or scalar
where it wants an object — built cleanly and emitted nothing for the property,
and nothing in the output said it had been seen. Closed by routing those six through presence-reporting helpers (`common.go`), which
report presence separately from value, so each of the six now rejects a
mistyped value by name instead of discarding it. Those helpers were already how several other properties on the
same components are read — `updateStrategy` and `ordinals` on `statefulset`
(`statefulset_spec.go`), `successPolicy` and `podFailurePolicy` on `job` and
`cronjob` (`common.go`) — so the six behaved differently from properties an
author writes beside them in the same document. This covers those six parsers;
it is not a claim that every property in this package is read through the
helpers.

An `env` entry with no usable `name` is an error at every position, not only
at the top level. The published schema marks `env[].name` required, but that
check reached only a top-level `env` list: an `env` list nested inside an
`initContainers` or `sidecars` entry sat in an open object the schema did not
describe, and `parseEnv` then skipped a nameless entry, so the container was
emitted with the variable missing and nothing reported. `parseEnv` now refuses
it itself, whether or not the schema layer reaches that position —
`env[0]: name is required` for an absent, empty or null `name`, and
`env[0].name: must be a string, got int` for a non-string one — so the rule
holds for any caller, including one that hands properties to a handler without
schema validation. **Behavior-changing** under `launcher.gokure.dev/v1alpha1`:
a document authoring such an entry inside `initContainers`/`sidecars` built
before (with the variable dropped) and is now rejected, and so is a top-level
entry with `name: ""` — schema validation checks only that `name` is present
and a string, so the empty name reached `parseEnv` and was skipped the same
way. A top-level entry with `name` absent or null was already refused by
schema validation and still is.

A required string field given a value of the wrong type is reported as a type
error, not as missing. Fourteen required-string reads used to discard a failed
type assertion and then test for `""`, so `fieldPath: 123` produced
`fieldRef: fieldPath is required` and sent the author looking for a lost key.
They now read through `requiredStringField` (or `parseStringField` directly
where the existing message is kept verbatim), and each field gets its own
messages: `fieldRef.fieldPath: must be a string, got int` for a wrong type, and
`fieldRef: fieldPath is required` only for an absent, empty or null value. The
fields are `env[].valueFrom.fileKeyRef.volumeName`/`.path`/`.key`,
`valueFrom.fieldRef.fieldPath`, `valueFrom.resourceFieldRef.resource`,
`envFrom[].configMapRef.name` and `.secretRef.name`, the `name` and `image` of
an `initContainers`/`sidecars` entry and the `name`/`mountPath` of its
`volumeMounts`, `manifests`
`scopeOverrides[].apiVersion`/`.kind`, and `oci` `source.url` and `version`.
`fileKeyRef`, `volumeMounts` and `scopeOverrides` used to report their fields
together ("volumeName, path, and key are all required"), so a wrong type on
one looked like any of them might be missing; each field is now named on its
own. What is accepted is unchanged: every one of these values was rejected
before and still is, and only the message changed. For a document run through
schema validation, the schema already refuses a non-string at every one of
these positions, so the parser message is what a caller sees that hands
properties to a handler without schema validation.

**The main container is named after the component, so a workload component
name must be a DNS-1123 *label*, not merely a subdomain.** A component name is
validated as a DNS-1123 subdomain (`pkg/oam/validate.go`), which permits dots,
and that name reaches `metadata.name` unchanged and the `app:` label through
`oam.ComponentLabelValue` (the name itself at 63 characters or fewer) — both
accept it. It also becomes the name of the pod's main container, and a
container name is a DNS-1123 label, which forbids dots and allows at most 63
characters where a subdomain allows 253. `batch.worker` would therefore build
a workload the API server rejects at admission, naming
`spec.template.spec.containers[0].name`, a field the author never wrote; an
undotted name longer than 63 characters is refused by the same check. All
seven workload kinds (`webservice`, `worker`, `deployment`, `statefulset`,
`daemonset`, `cronjob`, `job`) now refuse such a name at generation, in the one
builder they share. The name is refused rather than rewritten to `batch-worker`:
a derived name would silently rename the container and could collide with an
init container or sidecar of that name. Component types that name no container
after the component (`helmrelease`, `manifests`, `passthrough`, …) keep accepting
a dotted or longer name, so the component-name rule itself is unchanged. Nothing that previously produced an applyable manifest is
affected — such a document never did. A kind that also names a Service after
the component (`webservice`) refuses a dotted or longer name earlier, at
conversion, by the stricter Service-name rule described under each kind in
["Per-type highlights"](https://www.gokure.dev/launcher/api-reference/oam-components/#per-type-highlights). `daemonset` and `statefulset`
named one too until they stopped emitting a Service.
`cronjob` and `job` refuse a name past the length of their own object earlier
too, at conversion: a CronJob's name is at most 52 characters, a Job's at most
63 (see each kind in ["Per-type highlights"](https://www.gokure.dev/launcher/api-reference/oam-components/#per-type-highlights)).

Most workload types (`webservice`, `worker`, `deployment`, `statefulset`,
`daemonset`, `cronjob`, `job`)
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
rejected rather than silently treated as absent (`parseStringField`);
`fieldPath` is validated against the exact set real admission accepts
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
absent (`parseStringField`);
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
non-boolean rejection as above); corev1's `EnvFiles` feature (refused when the
`ClusterProfile`'s `kubernetesVersion` is below 1.35, where the gate turns on by
default, unless its `spec.featureGates` sets `EnvFiles` on, and refused at any
version when it sets the gate off); `volumeName`
must be a valid DNS-1123 label and `key` a valid (relaxed) env var name,
matching real admission's own `validateFileKeySelector`; `path` must be
relative and must not contain a `..` backstep component, per this repo's own
path-safety convention; once the component's `volumes` are parsed,
`volumeName` must name one of them of type `emptyDir` — a `fileKeyRef` in
the main container, an init container or a sidecar naming an undeclared
volume (a statefulset claim template included) or a non-`emptyDir` one is
refused, as real admission's `validateFileKeyRefVolumes` refuses it (no
trait the build evaluates adds an `emptyDir` volume; a patch a consumer
applies at delivery is outside the build's view, so a volume only a patch supplies is not
seen — declare the `emptyDir` volume on the component itself; see
`checkFileKeyRefVolumes` in `common.go`); a
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
`resources` — a `corev1.ResourceRequirements` projection read with
`parseObjectField`, as are its `requests` and `limits`: a present value of the
wrong type (`resources: "big"`, `requests: 3`) is **rejected by name**
(`resources: must be an object, got string`) by each of the eight kinds that accept `resources` (webservice, worker,
deployment, statefulset, daemonset, cronjob, job, postgresql) and by each
`initContainers`/`sidecars` entry, and a null reads as absence. `requests`/`limits`
accept `cpu`/`memory` (defaults 100m/128Mi — subject to the environment
policy's `MaxCPU()`/`MaxMemory()` maxima the same as an authored value; see
["Policy defaults & enforcement ordering"](policy-and-endpoints.md#policy-defaults--enforcement-ordering)) plus any other well-formed
resource name (e.g. `ephemeral-storage`, `nvidia.com/gpu`) in the same map.
`limits.cpu` and `limits.memory` also accept the string `unlimited`, which
omits that limit (see ["Policy defaults & enforcement ordering"](policy-and-endpoints.md#policy-defaults--enforcement-ordering)). Each
other value is authored as either a quantity string (`"500m"`, `"2Gi"`) or a bare
YAML/JSON number (`1`, `0.5`) — both are valid `resource.Quantity` input
(`Quantity.UnmarshalJSON` parses a bare numeric literal the same way it parses
a quoted one), and both forms are also accepted by the published property
schema itself: `cpu`/`memory` are declared as the `Types: [string, number]`
union, so a value of any other type is rejected by
property validation before the parser sees it — parsed as `resource.Quantity` and
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
non-overcommitable set. Admission's "HugePages require cpu or memory" rule
(a block naming a `hugepages-<size>` resource must also name cpu or memory,
on either side) is not checked here, because a container's final resources
always carry the cpu/memory requests `buildResourceRequirements` defaults in;
it is checked where a block reaches the cluster without those defaults —
`podResources` at parse time, and the postgresql Cluster's `resources` at
generation time. No policy
default/max hook exists for names other than cpu/memory
today; the container-level `claims` list (Dynamic Resource Allocation) is
deliberately not covered — it only *references* pod-level claims by name, and
the pod-level `resourceClaims` property that declares them is now accepted
(see Pod-level properties below), so what remains
missing is the container-side reference list alone, tracked with the rest of
DRA support, see `parseResources`'s doc comment. An authored `claims` is
refused by name, whatever its value, on the main container and on an
`initContainers` or `sidecars` entry — `resources.claims: not read by this
component — …` — and no longer dropped for a caller that skips the document
check; see
[Upstream fields a hand-parsed kind does not read](upstream-fields-not-read.md#upstream-fields-a-hand-parsed-kind-does-not-read)),
`command`/`args` (must be an
array, and each element must be a string — both are rejected outright rather
than silently discarded), `probes`
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
(Those rules predate `ports`, and the `port` they describe on `daemonset` and
`statefulset` is gone. Today `deployment`, `daemonset`,
`statefulset`, `job` and `cronjob` resolve a named port against the main
container's `ports` list, refusing a name that list does not declare and any
named port when it is empty; see "Main container ports".)
Where a named port is allowed at all, it is further checked against the
exact names the main container actually declares — `"http"` for
`webservice`, the authored `ports` names on the kinds above — not merely accepted as
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
present-but-non-string value (e.g. `host: 123`) is rejected
(`parseStringField`), instead of silently
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
above)/`sleep` (`seconds` is required and must be a non-negative integer, and
at most the pod's `terminationGracePeriodSeconds`, 30 when it sets none —
checked on the assembled pod for the main container, every `sidecars` entry
and every native sidecar, so a longer sleep is refused at `Generate` instead
of by the API server; a
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
container Linux capability strings (e.g. "NET_ADMIN"), and which judge both the
traits an author wrote and the traits a non-builtin component-position lowering
rule wrote onto a part it emitted (launcher's own rules are exempt); see `enforce.go`'s
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
`localhostProfile: 123`) — the present-but-wrong-type value is rejected
(`parseStringField`), not silently treated as
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
value, e.g. `procMount: false`, is rejected rather than silently omitted
(`parseStringField`); an explicit empty
string is still treated as absent, not an error, which is `parseStringField`'s
own convention), `windowsOptions` (`gmsaCredentialSpecName`,
`gmsaCredentialSpec`, `runAsUserName` and `hostProcess`, the closed key set the
pod-level `podSecurityContext.windowsOptions` reads; an
`os.name: linux` pod refuses it, as it refuses the pod-level one. Its strings
are held to upstream's `validateWindowsSecurityContextOptions`, at both
levels: `gmsaCredentialSpecName` is a DNS-1123 subdomain, `gmsaCredentialSpec`
is at most 64 KiB, and `runAsUserName` is `USER` or `DOMAIN\USER` with no
control character, a domain under 256 characters in the NetBIOS or the DNS
format, and a user of at most 104 characters that is not only periods and
spaces and holds none of `"/\:;|=,+*?<>@[]`; an empty string is absent, by the
same convention (**breaking**: a pod-level value that
breaks these rules built before and is refused now). Upstream's `validateWindowsHostProcessPod` rules are held on the assembled pod, so they
are reported at `Generate`: a container's `hostProcess` must equal the pod's
when both are set; a pod with one HostProcess container — its own
`hostProcess`, or the pod's when it sets none — must have only HostProcess
containers, init containers included; and such a pod must set
`hostNetwork: true`. `hostProcess: true` is policy-gated, see below)),
`workingDir` (a bare pass-through — `corev1.Container.WorkingDir`'s own
doc comment states only that the container runtime's default applies when
unset, and real admission enforces no path shape for it, so this schema does
not invent a stricter constraint upstream itself does not have; a
present-but-non-string value, e.g. `workingDir: 123`, is rejected rather than
silently treated as absent, in all seven kind handlers — this is a type check,
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
`name` and `mountPath` are both required on every entry — the one exception
is a `pvc` entry with `volumeMode: Block`, which authors `devicePath` instead
(see "Raw block volumes" below) — a present-but-
non-string value (e.g. a numeric `mountPath`) is a type error; an entry missing
either, or authoring one with the wrong type, previously built with no
volume and no mount for that entry instead of reporting what was missing;
`emptyDir.sizeLimit`, if authored, is parsed as a
`resource.Quantity` and rejected if negative (e.g. `"-1Gi"`) — syntactically
valid but a storage quantity real Kubernetes resource validation refuses,
same as `resources`' own quantity fields above; a present-but-non-string
value (e.g. `sizeLimit: 1048576`) is rejected the same way, instead of
failing the bare type assertion silently and building an emptyDir with no
size limit at all. A zero `sizeLimit` is accepted on purpose: upstream
`validateVolumeSource` rejects only a negative value, and the kubelet applies
the limit only when it is greater than zero, so `"0"` means "no volume-level
limit" rather than an inadmissible quantity; `pvc.size` is required (the
same missing-vs-wrong-type rejection as `name`/`mountPath` above) and, once
present, must be a **positive** quantity. **Behavior-changing**: `size: "0"` used to build and is now rejected, because
`ValidatePersistentVolumeClaimSpec` runs `ValidatePositiveQuantityValue` over
`requests[storage]`, which refuses zero as well as negative values — the
claim built before was never admissible. `BuildPVC` applies the same rule,
and the `pvc` trait, which parses its claim with the `persistentvolumeclaim`
kind's `ParseClaimProperties`, refuses a zero or negative `size` too; `pvc.storageClass`,
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
class; `volumeClaimTemplates.storageClass` keeps the same distinction (see "StatefulSet-level and claim-template properties"
below).
`pvc.accessModes`, if authored, must be a non-empty array of non-empty
strings, each one of the four real `corev1.PersistentVolumeAccessMode`
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
backs `volumeClaimTemplates.accessModes` below; on `webservice` and
`worker`, the claim a `pvc` volume describes is named
`<component name>-<pod-local volume name>`, not the bare pod-local name — two
components in the same namespace that both author a `pvc` volume named
`data` would otherwise emit two colliding `PersistentVolumeClaim/data`
objects; the pod-local `Volume.Name` and its `VolumeMount` reference stay
unqualified, only the claim's name and the matching
`Volume.PersistentVolumeClaim.ClaimName` are qualified (`roleClaims` in
`role_members.go`, once, when the rule lowers the component); the join
itself escapes interior hyphens in each half (`escapeForPVCQualification`)
before concatenating, since plain `<appName>-<localName>` string
concatenation is not collision-free when either half itself contains a
hyphen — component `a-b` with volume `data`, and component `a` with volume
`b-data`, would otherwise both qualify to `a-b-data`; a qualified name over
253 characters is shortened by the one rule (`oam.ShortenNameWithSuffix`), the escaped component cut and the escaped volume kept
whole, after its characters are checked on the unshortened name, so only the
length is forgiven; `hostPath.path`
is likewise required and must be absolute — a raw host filesystem path has
no defined root to resolve a relative value against, and real admission
(`validateHostPathVolumeSource`) rejects a relative one the same way;
`configMap.configMapName` and `secret.secretName` are each required the same
way as `pvc.size`; an entry can also author its
source as the upstream object under a key named like its `type`: `projected`,
`downwardAPI`, `csi`, `ephemeral` and `image` take only that nested form
(`- {name: tok, type: projected, mountPath: /var/run/tok, projected: {sources: [...]}}`),
and `configMap`, `secret` and `emptyDir` take it as well, for the fields their
flat keys have no name for (`items`, `defaultMode`, `optional`, `medium`). The
nested object is decoded strictly into the Kubernetes `VolumeSource`: an unknown
field or a wrongly typed value at any depth is refused, a `null` key reads as
absent, and a `0`, `false` or `""` that the API server would default is refused
rather than dropped. A flat source key (`configMapName`, `secretName`,
`sizeLimit`) and the nested object on one entry are refused, a nested
`configMap` needs its `name` and a nested `secret` its `secretName` (as the flat
keys do), a `csi` source needs its `driver` and a `projected` `configMap` or
`secret` source its `name`, an `image`
reference is checked by `ValidateImageRef`, and the label selectors of a
`projected` cluster trust bundle and of an `ephemeral` claim template are held as
the `pod` kind holds them. `hostPath` and `persistentVolumeClaim` stay on their
flat routes (`type: hostPath`, `type: pvc`), which own the hostPath gate and the
claim generation. Every other Kubernetes volume source (`nfs` and the removed
in-tree plugins such as `gitRepo`, `iscsi` or `rbd`) is refused by name, because
Pod Security `restricted` does not admit it. The other fields the API server
requires inside a nested source (a `serviceAccountToken` `path`, a
`clusterTrustBundle` `path`, a `downwardAPI` item's `path`) are not checked.
A fully-authored entry whose `type`
matches none of the recognized sources — including `type` omitted entirely — is
rejected outright rather than silently producing no volume or mount for an entry
the author clearly intended to add. Every controller kind (`deployment`,
`statefulset`, `daemonset`, `job`, `cronjob`, and so `webservice` and `worker`)
applies the volume-source policy gates that `enforcePodTemplatePolicy` applies to
an operator pod template (`enforceVolumeSourcePolicy`): the hostPath gate, the
storage maximum on an `ephemeral` claim, and the registry allowlist on an
`image` reference; each recognized type's own field set is
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
a `volumeMounts` item is a **closed key set**: it accepts
`name`, `mountPath`, `readOnly`, `subPath`, `subPathExpr`, `mountPropagation`
and `recursiveReadOnly`. Any other key is refused by name. Earlier versions
dropped `subPathExpr`, `mountPropagation` and `recursiveReadOnly` without an
error. One helper reads them and applies the rules
that upstream `ValidateVolumeMounts` applies to one mount: `mountPropagation`
is `None`, `HostToContainer` or `Bidirectional`, and `Bidirectional` needs
`securityContext.privileged: true` on the same entry; `recursiveReadOnly` is
`Disabled`, `IfPossible` or `Enabled`, and the last two need `readOnly: true`
and a `mountPropagation` of `None` or none at all; `subPath` and `subPathExpr`
are mutually exclusive, and each is a relative path with no `..` element
(an empty `subPath` is absent, as
upstream skips it). The main container reads the same keys: a `volumes` entry of any type and a
`volumeClaimTemplates` entry with a `mountPath` take `subPath`, `subPathExpr`, `mountPropagation` and `recursiveReadOnly`, and a
claim template takes `readOnly` too. A `volumes` entry keeps its own per-type
`readOnly`, and `recursiveReadOnly` is checked against it. A `devicePath` entry
is a raw block device with no mount, so it refuses the four keys. The two
places differ on `readOnly`: a `volumes` entry with `devicePath` still accepts
it, because it sets the claim source and did so before, and a claim template
with `devicePath` refuses it, because it never had that key. `Bidirectional` needs
`securityContext.privileged: true` on the main container, and every workload
kind checks it. A `subPath` that is absolute or holds a `..` element is refused
in all four places (`initContainers`, `sidecars`, `volumes` and
`volumeClaimTemplates`) by the helper that refuses it for `subPathExpr`, with
the same wording, where the API server would refuse it later;
the `pod`, `podtemplate`, `replicaset` and `replicationcontroller` kinds, the
workload objects of a `manifests` source (`inline` or `url`), the object of a
`passthrough` component and the workloads a `helmtemplate` chart renders read
`volumeMounts` as typed upstream mounts and hold those of every container and
init container to the same rules, whatever the environment policy: `subPath`
and `subPathExpr` are exclusive and relative with no `..` element,
`mountPropagation` and `recursiveReadOnly` are one of their modes with the
same preconditions, and `Bidirectional` needs `securityContext.privileged:
true` on that container, with the error naming the container and the mount's
position. A `passthrough`
object is refused when the component is read; a chart is refused when it is
rendered, and the refusal quotes the mount value even with `secretValues` set,
as the environment policy check quotes what it refuses. A workload in an API
version kure's scheme does not register is not read by this rule: the
environment policy check refuses it as unreadable. `bindMountOptions`, the
mount path and the volume a mount names are not checked by this rule;
each entry also accepts its own `securityContext`, the identical field set
and validation as the main container's own `securityContext` described
below — see that prose for the field list rather than restating it here;
each entry is a **closed key set**: an
`initContainers` entry accepts `name`, `image`, `command`, `args`, `env`,
`envFrom`, `resources`, `volumeMounts`, `volumeDevices` (see "Raw block
volumes" below), `securityContext`, `workingDir` and the seven keys of
"Container fields" below,
and a `sidecars` entry those plus `ports`, `probes` and `lifecycle`, each
parsed by the same parser as the main container's field of that name. Any
other key is an error naming the entry — before that the parsers read the
keys they knew and dropped the rest, so an authored `workingDir`, `envFrom`,
`probes` or `lifecycle` built cleanly and reached no container. An
`initContainers` entry also takes `ports`, `probes` and `lifecycle`, but only with
`restartPolicy: Always` (a native sidecar, see below): Kubernetes forbids
probes and lifecycle hooks on an init container that runs to completion before
the app containers start, and launcher refuses `ports` there too, since that
container has exited before anything could reach a port it declares. On any
other entry they are refused with their own message, which points at
`restartPolicy: Always` or a `sidecars` entry. A sidecar's probe or hook may address a port by
name only when that sidecar itself declares a `ports[]` entry of that name —
the kubelet resolves a named port against the container's own ports, so an
undeclared name would build and never resolve; unlike the main container,
whose single named port is fixed by its kind, a sidecar may declare several
and any of them is accepted. A sidecar's `ports` list is read exactly as
`deployment` reads its main container's (see "Main container ports" below):
closed entries, an IANA service `name`, a `protocol` from `TCP`/`UDP`/`SCTP`
(an empty string refused), and no repeated name or `containerPort`/`protocol`
pair within the sidecar. A port name must also be unique across the
pod: the main container's ports (webservice's `http`, the `ports` list of
statefulset, daemonset, deployment, job and cronjob), every
sidecar's and every native sidecar's. The
API server checks names only per container and merely warns across them,
while a Service selecting the name reaches only the first container that
declares it, so a repeated name is refused, naming both containers. A regular
init container declares no ports, so it takes no part. A container name must be
unique across the pod too: the main container (named after the component),
every `initContainers` entry and every `sidecars` entry, and within each list.
The API server refuses a pod whose containers share a name, so the document is
refused when it is read, naming both entries. A name in
either list must also be a DNS-1123 label, as the main container's name is:
lowercase alphanumerics and `-`, starting and ending with an alphanumeric, no
dots, at most 63 characters. Any other is refused, naming the entry, and again when the pod is generated, so a config built
by hand or changed after it was read cannot emit one. `job` and
`cronjob` take no `sidecars`, so their pod holds the main container and the
`initContainers` entries. Closing the entry is behavior-changing under an
unchanged `launcher.gokure.dev/v1alpha1`: a document that authored any other
key on an entry built before and errors now. It is taken on the same
reasoning as the `volumeClaimTemplates` entry below — the key never reached
the output its author intended — and is signalled by the `format` commit
scope),
and `affinity` (four keys, parsed by `parseAffinity`: `enablePodAntiAffinity`
(boolean), `topologyKey` (string, default `kubernetes.io/hostname`),
`podAntiAffinityType` (string, `preferred`|`required`, default `preferred`)
and `nodeSelector` (a string→string map). Pod anti-affinity is emitted only on
an explicit `enablePodAntiAffinity: true`; authoring the block without it just
supplies the two defaults above, and omitting the block entirely leaves
`topologyKey`/`podAntiAffinityType` empty rather than defaulted (the defaults
are applied by `parseAffinity` only once the block is present, unlike the
`postgresql` component, which also tracks whether the block was authored at all).
The key set is closed. The shorthand is not a
Kubernetes `Affinity`: `nodeAffinity`, `podAffinity` and `podAntiAffinity`
under it are each refused with what the shorthand has for the field and where
an affinity in the Kubernetes shape is authored (`deployment`, `statefulset`,
`daemonset`, `job`, `cronjob`), and any other key is refused as
`affinity: unrecognized key "<key>"`, each whatever its value. The parser read
none of them before, so a caller that skips the document check had them
dropped; see
[Upstream fields a hand-parsed kind does not read](upstream-fields-not-read.md#upstream-fields-a-hand-parsed-kind-does-not-read).
Each of the four sub-fields is read with a presence-reporting helper, so a
sub-field authored with the wrong type is **rejected by name**
(`affinity.topologyKey: must be a string, got float64`) rather than
discarded while the default above is emitted as though the key had never
been written, and a non-string `nodeSelector` **value** is refused by key
(`affinity.nodeSelector["rack"]: must be a string, got int`), the
alphabetically first offending key when several are wrong. A null sub-field
is absence and takes the default an omitted one takes, and a null
`nodeSelector` value is absence too: `nodeSelector: {zone: a, rack: null}`
builds the selector `{zone: a}`. `podAntiAffinityType`
must be `preferred` or `required`: a well-formed string outside the enum is
refused by name, and an explicitly authored `podAntiAffinityType: ""` is an
error rather than a fall back to `preferred` — the empty string reaches the
enum check instead of being read as an absent key. An authored
`topologyKey: ""`, by contrast, *does* fall back to the default. This is the
same contract the `postgresql` component's own affinity block has. **Behavior-changing** under
`launcher.gokure.dev/v1alpha1` for one authored shape:
the published schema leaves `nodeSelector` values untyped, so a document
with a non-string value (`rack: 3`) passed schema validation, built with that
entry dropped, and is now rejected. A wrongly typed sub-field itself was
already refused by schema validation for an authored document; the parser's
own check now covers a caller that hands properties to a handler without it,
which previously got the default. The out-of-enum error now reads
`affinity.podAntiAffinityType: invalid value …` rather than
`invalid podAntiAffinityType …`).
On `worker`, the affinity the shorthand evaluates to is also validated the way the
`deployment` component validates a raw `affinity`, so a `topologyKey`, or a
`nodeSelector` key or value, that is not valid label syntax is refused
(`affinity: the shorthand evaluates to an affinity the API server would
refuse: …`). **Behavior-changing** under `launcher.gokure.dev/v1alpha1`: such
a worker document built before, into a manifest the API server rejects, and
is now refused at build time — a pre-GA format change (`docs/oam/design-gvk.md`
§ Document-Format Lifecycle), signalled by the `fix(format)` commit scope. The
check runs after every other worker property is parsed, so an earlier refusal
keeps its place. A component name longer than 63 characters stays refused;
when the shorthand enables pod anti-affinity, whose label selector carries
the name, this check now refuses it before the container-name check does.
`webservice` validates the evaluated shorthand the same way, with the same
message, as the last step of its parse, as a pre-GA format change too
(a `fix(format)` change of its own): such a webservice document also built
before, into a Deployment the API server rejects. A webservice's name is
checked against the Service-name rule first, so an over-long name is refused
there, before this check.

### Container fields

Seven `corev1.Container` fields are read on every container of a hand-parsed
workload kind: as top-level keys for the main container
of `deployment`, `statefulset`, `daemonset`, `job` and `cronjob` — and of
`webservice` and `worker`, which forward them to their `deployment` member — and
as keys of each `initContainers` and `sidecars` entry.

| Property | Type | Effect |
|---|---|---|
| `imagePullPolicy` | string: `Always`, `IfNotPresent`, `Never` | `imagePullPolicy` of the container. |
| `terminationMessagePath` | string | `terminationMessagePath`, passed through: upstream validates no path shape for it, so none is invented here. |
| `terminationMessagePolicy` | string: `File`, `FallbackToLogsOnError` | `terminationMessagePolicy`. |
| `stdin`, `stdinOnce`, `tty` | boolean | The field of that name. Upstream has no rule relating the three, so none is applied. |
| `resizePolicy` | list of `{resourceName, restartPolicy}` | `resizePolicy`. Both keys are required; `resourceName` is `cpu` or `memory`, `restartPolicy` is `NotRequired` or `RestartContainer`; a resource is named once. |

- **Nothing is defaulted.** A field that is not authored is not emitted, which
  leaves the API server's own defaults in force (for `imagePullPolicy` see
  [Every spec field is this package's to write](conventions.md#every-spec-field-is-this-packages-to-write)).
  An authored `false` on a boolean is the field's zero value and renders as
  absent too.
- **No policy check.** `imagePullPolicy: Never` and `IfNotPresent` are accepted
  with no capability gate and no `Policy` method: a platform that requires
  `Always` has to enforce it at admission.
- An explicit null is absence (see [The null contract](null-and-spellings.md#the-null-contract)), and
  so is an empty string on `terminationMessagePath` (`parseStringField`'s
  convention) and an empty `resizePolicy` list. An empty string on
  `imagePullPolicy` or `terminationMessagePolicy` is refused, as any value
  outside the enum is: by property validation in a document, and by the parser
  with the message below. The enum values are case-sensitive, as upstream:
  `imagePullPolicy: always` is refused with
  `imagePullPolicy: invalid value "always", must be one of Always, IfNotPresent, Never`.
- The `resizePolicy` rules are upstream's (`validateResizePolicy`,
  `validateInitContainers` in `k8s.io/kubernetes`
  `pkg/apis/core/validation/validation.go`), checked at build time so the author
  gets the entry named instead of an admission error:
  - an unknown key, a missing key, a resource other than `cpu`/`memory`, an
    unknown restart policy and a repeated resource are each refused by entry
    (`resizePolicy[1].resourceName: "cpu" is named by an earlier entry; …`);
  - `RestartContainer` is refused on an `initContainers` entry unless that
    entry has `restartPolicy: Always`, as Kubernetes allows it only on a
    restartable init container. `NotRequired` is accepted on any entry;
  - on a pod whose `restartPolicy` is `Never` — which only `job` and `cronjob`
    can author — every container's restart policy must be `NotRequired`. That
    check runs on the assembled pod spec (`checkResizePolicyRestart`), so it is
    reported at `Generate`, not at `ToApplicationConfig`.
- On an `initContainers` or `sidecars` entry every message carries the entry
  label first (`sidecars[0] "proxy": tty: must be a boolean, got string`).

An `initContainers` entry also reads its own `restartPolicy` and
`restartPolicyRules`, held to upstream's
`validateInitContainerRestartPolicy` and `validateContainerRestartPolicy`:

- `restartPolicy` is `Never` or `OnFailure`, overriding the pod's restart
  policy for that container, or `Always`, which makes the entry a **native
  sidecar**: it starts in list order like any init
  container, but stays running for the life of the pod, alongside the main
  container. Only such an entry takes `ports`, `probes`, `lifecycle` and a
  `RestartContainer` resize policy (the same parsers as a `sidecars` entry);
  `restartPolicyRules` is refused beside `Always`. Its `ports` are read as a
  `sidecars` entry's are, so a probe or hook may address
  one by name and a consumer that generates Services can resolve a named
  `targetPort` to it; a port name is unique across the whole pod, main
  container, `sidecars` and native sidecars together. `ports` on any other
  init entry is refused with the reason, since that container has exited before
  anything could reach it. It needs a cluster with sidecar container support. Built-in resource defaults apply to it; a
  `policy`'s defaults are the main container's only;
- `restartPolicyRules` needs `restartPolicy` (not `Always`) and holds at most 20 rules. A rule
  is a closed object: `action` is required and is `Restart` (upstream's
  `RestartAllContainers` needs a separate feature gate, and is refused), and
  `exitCodes` is required: a closed object whose `operator` is required and is
  `In` or `NotIn`, and whose optional `values` is a list of at most 255 int32
  exit codes, each once (the field is a set);
- Kubernetes keeps `Never`, `OnFailure` and `restartPolicyRules` on an init
  container only with the `ContainerRestartRules` feature gate, on by default
  from Kubernetes 1.35. A cluster with the gate off drops them when it creates
  the pod, without an error, so the init container falls back to the pod's
  restart policy. `Always` is not tied to that gate: upstream validation
  accepts it with the gate on or off. A build for a `ClusterProfile` that
  states `spec.kubernetesVersion` below 1.35 refuses `restartPolicyRules`,
  naming the gate and the version. The same build
  refuses the `Never` and `OnFailure` values of `restartPolicy` below 1.35,
  naming the value, the gate and the version; the schema names the gate per
  value (`valueGates`), so `Always` is accepted at every version. A profile whose `spec.featureGates` sets
  `ContainerRestartRules` on accepts them below 1.35, and one that sets it off
  refuses them at any version. With no version stated nothing is checked.

On a `sidecars` entry neither key is read, and both are refused as unknown
keys. At the top level neither is a container key either.
`restartPolicyRules` is refused there on every workload kind, with the reason
that upstream accepts a container's rules only together with the container's
own `restartPolicy`. `restartPolicy` there is the *pod's* restart policy: a
key of `job` and `cronjob`, where the main container's own `restartPolicy`
therefore has no name to be authored or refused under, and refused with its
reason on the other kinds, whose pod template apps/v1 validation holds to
`Always`. See
[Upstream fields a hand-parsed kind does not read](upstream-fields-not-read.md#upstream-fields-a-hand-parsed-kind-does-not-read)
for these and for the container fields read under another name (`name`, the
three probe fields, `volumeMounts`, `volumeDevices`).

### Referencing an existing claim (`pvc.claimName`)

A `pvc` volume with `claimName` mounts an existing claim instead of generating
one. The claim usually comes from a
`persistentvolumeclaim` component or a `pvc` trait, but it can be any claim in
the namespace. The volume generates no object, and its claim name is used
exactly as written: it is not qualified with the component name the way a
role kind's claim is.

**On the five pod kinds `claimName` is required.** `deployment`,
`statefulset`, `daemonset`, `job` and `cronjob` generate no claim, so a `pvc`
volume without `claimName` is refused (`volume "data": a pvc volume must set
claimName to an existing claim; the component generates none, so declare it
with a persistentvolumeclaim component or a pvc trait`). This is a breaking
pre-GA format change. To migrate, declare the claim with a
`persistentvolumeclaim` component (or a `pvc` trait) carrying the volume's
`size`, its `storageClass` as `storageClassName`, and its `accessModes` and
`volumeMode`. Then reference it from the volume with `claimName`. Keep
`accessModes` on the volume, and keep `volumeMode: Block` there too: a
`devicePath` mount needs it on the volume. The environment's storage-size
limit and default now apply to that claim, not to the volume.

`webservice` and `worker` still accept a `pvc` volume that describes its
claim. The rule turns each one into a synthesized `pvc` trait on its
`deployment` member, named `<component>-<volume>` as before, and rewrites the
volume to reference it by `claimName`. The output is unchanged. The volume's
`claimObjectName` names that claim instead (role
`workload-volume-claim`; see the `deploymentObjectName` entry of
[Kinds: core workloads](kinds-core-workloads.md)).

Such a volume that leaves `storageClass` unauthored (absent or `null`) takes
the ClusterProfile `pvc` capability's `storageClassName`, as an authored `pvc`
trait and the `persistentvolumeclaim` kind do. The
synthesized trait is sealed, so the engine merges no rendering into it; the
rule reads the binding through `LoweringContext.Capability`, which records
`pvc` in `ConsumedCapabilities`, and fills the value itself. An authored
class, `""` included, wins. **Pre-GA output change**: such a volume used to
ignore the binding, so an existing claim built from it got the cluster's
default class when it was created, if the cluster had one. A claim's
assigned `storageClassName` cannot change: if the binding's class differs
from it, the claim fails to apply until it is recreated or the volume
authors the assigned class. A claim created with no class may take one
later, so it applies. A `statefulset` `volumeClaimTemplates` entry takes the
same default (see "StatefulSet-level and claim-template
properties").

- `claimName` must be a DNS-1123 subdomain.
- `size` and `storageClass` are refused alongside it, because the referenced
  claim states its own (`volume "data": size cannot be set with claimName; …`).
- `claimName: ""` is refused, not read as absent.
- `claimObjectName` is refused alongside it: it names the claim a
  `webservice` or `worker` volume generates, and this volume generates none.
- `accessModes` stays: it states the referenced claim's modes, defaulting to
  `ReadWriteOnce` as on a claim a role kind describes. The workload still reads them. A
  non-RWX claim still forces `strategy: Recreate` on a Deployment, still
  refuses `replicas` above 1, and still limits a `scaler` to one replica. To
  scale against a `ReadWriteMany` claim, state `accessModes: [ReadWriteMany]`
  on the volume.
- `volumeMode`, `readOnly` and the mount keys work as on a described claim.

Every workload kind that accepts `volumes` accepts `claimName`. `BuildPVC`
refuses a `PVCConfig` that carries a `ClaimName`: there is no object to build.

### Raw block volumes (`volumeMode: Block`)

A claim with `volumeMode: Block` has no filesystem. A container consumes it
through `volumeDevices` at a `devicePath`, never through `volumeMounts`. The claim and the pod template are validated as
separate objects, so the apiserver accepts a Block claim paired with a
filesystem mount and the pods then fail at kubelet mount time; every rule
below reports that mismatch at build time instead. The rules are the same on
all seven kinds (`webservice`, `worker`, `deployment`, `statefulset`,
`daemonset`, `job`, `cronjob`), because they live in the shared parsers.

- **`volumes[]` pvc entry.** Adds `volumeMode` (`Filesystem`|`Block`) and
  `devicePath`. The entry takes exactly one of `mountPath` and `devicePath`,
  and `devicePath` is authored if and only if `volumeMode: Block` is — in both
  directions, with nothing inferred: `mountPath` with `Block`, `devicePath`
  with `Filesystem` or with no mode, and both paths at once are each an error.
  A Block entry reaches the main container as a `corev1.VolumeDevice` and has
  no `volumeMount`. The claim carries the authored mode; an unauthored mode
  stays unset (the apiserver defaults it to `Filesystem`), so an existing
  document's claim is byte-identical. `devicePath` on any other volume type is
  an error.
- **`volumeClaimTemplates[]` entry (`statefulset`).** The same exactly-one
  rule with `devicePath`, and `volumeMode: Block` is now accepted (it was
  rejected before). A Block template renders a claim template with
  `volumeMode: Block` and a main-container `volumeDevices` entry.
- **The published schema says "exactly one of".** A `volumes[]` entry and a
  `volumeClaimTemplates[]` entry publish `mountPath` and `devicePath` as two
  optional keys plus a required `exclusive` group of the two, so a validator built from the schema refuses both
  and neither, as the build does. A key counts as authored when it is present
  and not null, an empty string included: `mountPath: ""` beside a
  `devicePath` is both, through the transform and through a direct handler
  call alike.
- **`initContainers[]` / `sidecars[]` entry.** Adds `volumeDevices:
  [{name, devicePath}]`, a closed key set with both keys required. A
  `volumeDevices` name must be a Block volume this component declares (a
  `volumes` pvc entry or a claim template); a `volumeMounts` name must **not**
  be one. A `volumeMounts` name the component does not declare at all stays
  accepted, because the `configmap` and `external-secret` traits add their
  volumes after the component is generated — and neither adds a Block volume.
- **Name and path rules** mirror `ValidateVolumeDevices`, per container: a
  volume is named at most once in `volumeDevices` and not in both
  `volumeMounts` and `volumeDevices`; a `devicePath` is unique, contains no
  `..` element, and is not also a `mountPath`. On `statefulset` this spans the
  claim templates and `volumes` together, so a Block claim template cannot
  share its name with a `volumes` entry or another claim template. The
  `configmap` and `external-secret` traits likewise refuse a volume name the
  main container already attaches as a device — including a Block claim
  template, which has no pod volume of its own — and a mount at a path it
  already uses as a `devicePath`.

**Compatibility.** Additive: every new rejection concerns a Block volume,
and no document could declare one before (a `volumes` pvc entry had no
`volumeMode`, and a claim template's `Block` was rejected). The one new rule
that reads existing keys — a `volumeMounts` entry naming a Block volume is an
error — can therefore only fire on a document that uses the new surface. The
published schemas drop `Required` from `mountPath` on `volumes` items and
claim templates; the parsers still require one of the two paths.

### Pod-level properties

The seven kind components also share one pod-level property surface
(ADR-036 L1), parsed by `parsePodSpec` (`podspec.go`)
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
`hostNetwork`, RuntimeClass existence) are left to the cluster, and so are feature
gates, with one exception: a `ClusterProfile` that states `spec.kubernetesVersion` has
a property whose gate is off by default at that release (or set off in `spec.featureGates`) refused:
`hostnameOverride` below 1.35 (`HostnameOverride`) and `podResources` below 1.34
(`PodLevelResources`), and a container's `env[].valueFrom.fileKeyRef` below 1.35
(`EnvFiles`) and an init container's `restartPolicyRules` below 1.35
(`ContainerRestartRules`), on the kinds whose schema declares them. The gate and the
release are in the property's published schema (`gate`). With no version stated nothing
is checked, and a profile's `spec.featureGates` replaces the release default for a gate it
names: set on, the property is accepted below its release; set off, it is refused at any
release. The `Never` and `OnFailure` values of an init container's `restartPolicy`
are refused below 1.35 by the same gate, and `service`'s `trafficDistribution` values
`PreferSameZone` and `PreferSameNode` below 1.34 (`PreferSameTrafficDistribution`); the
other values are accepted at every version, and the gate is named per value in the
schema (`valueGates`). The raw-PodSpec kinds (`pod` and the kinds that take a pod
template: `podtemplate`, `replicaset`, `replicationcontroller`) leave their containers and
template spec open, as does the `template` of `cnpg-pooler`, and the check reads declared schema, so their open nodes name the
gated paths in `gatedPaths` instead: `env[].valueFrom.fileKeyRef` and `restartPolicyRules` of `containers[]` and
`initContainers[]`, the `restartPolicy` of `containers[]` (Kubernetes forbids the field on a regular
container with the gate off, `Always` included), and `hostnameOverride` and
`resources` of the pod spec (`template.spec.*` on a pod template and on the `cnpg-pooler` template). A path is refused the
same way, naming the authored path. Field names match
case-insensitively, as in the decode, so a key spelled in another case is refused too and
is named as written; property validation does not read `gatedPaths`, so what a document is
accepted as does not change. A path that gates a value, not the field, sits in
`valueGatedPaths`: a map from a gate-relative path to a map from a value to its gate. The
`restartPolicy` of `initContainers[]` is there, so `Never` and `OnFailure` are refused
below 1.35 on these kinds as on the workload kinds, and `Always` is accepted at every
version. The match is exact and only a string is looked up, so a null, a number or a
string that the table does not name is not refused. The workload kinds and the raw-PodSpec
kinds share one table, so the two cannot differ. The refusal names the authored path and
the value. This check does not read whether a value such as
`never` reaches a cluster. It only refuses the values the table names.

| Property | Type | Effect | Kind |
|----------|------|--------|------|
| `serviceAccountName` | string | **Behavior-changing.** Pods run as the named account; on `webservice` and `worker` the per-component ServiceAccount is *not* generated. The `rbac` trait binds its Role/ClusterRole to this account via `oam.ServiceAccountNamer` (see below). Unset on a pod kind, the pod names no account (the namespace's `default`), and the kind sets the pod-level `automountServiceAccountToken: false` unless that is authored; a role kind's generated account carries `automountServiceAccountToken: false` itself. An authored account is owned elsewhere and its own setting governs, so authors who want the pod not to mount a token set the pod-level `automountServiceAccountToken: false` explicitly — the handler does not inject it. | additive when unset |
| `automountServiceAccountToken` | bool | Pod-level token automount override. | additive |
| `terminationGracePeriodSeconds` | int ≥ 0 | Grace period before SIGKILL. A `lifecycle` `sleep` hook may not exceed it (30 when unset). | additive |
| `podActiveDeadlineSeconds` | int 1..MaxInt32 | Pod-level `activeDeadlineSeconds`. **cronjob and job only** — apps/v1 rejects it on Deployment/StatefulSet/DaemonSet templates, so the other kinds neither publish nor accept it, and refuse the upstream name `activeDeadlineSeconds` with that reason (on `job` and `cronjob` that name is the JobSpec's). Distinct from the JobSpec-level `activeDeadlineSeconds` below: this one bounds a single pod, that one the whole job. | additive |
| `dnsPolicy`, `dnsConfig` | enum, object | `ClusterFirstWithHostNet`/`ClusterFirst`/`Default`/`None`; `dnsConfig` = `nameservers` (≤3 plain IPv4/IPv6 literals — a zone-scoped address such as `fe80::1%eth0` is rejected, matching upstream's `net.ParseIP`-based check, which has no notion of a zone), `searches` (≤32 entries whose joined length, separators included, is ≤2048 characters — the `resolv.conf` search-line limit, so 32 individually valid domains can still be refused), `options[]{name,value}`. `None` requires at least one nameserver. | additive |
| `nodeSelector`, `nodeName`, `schedulerName`, `priorityClassName`, `preemptionPolicy`, `runtimeClassName`, `schedulingGates[]{name}`, `schedulingGroup{podGroupName}` | scheduling | Placement fields; gate names must be unique. `nodeName` is a DNS-1123 subdomain (a Node is an ordinary object, so an invalid value is refused at admission, not merely unmatched). `schedulerName` is deliberately *not* validated: upstream constrains its form nowhere, so an arbitrary string is a legal document and rejecting one here would refuse work a cluster accepts. | additive |
| `hostNetwork`, `hostPID`, `hostIPC` | bool | Host namespaces. **Policy-gated**: rejected by `ApplyPolicy` unless `AllowHostNetwork()`/`AllowHostPID()`/`AllowHostIPC()` allow them (`enforce.go`'s `enforceHostNamespaces`, called from all seven kinds; `NoopPolicy` denies all three). | additive |
| `shareProcessNamespace` | bool | Mutually exclusive with `hostPID: true`. | additive |
| `hostname`, `subdomain`, `setHostnameAsFQDN`, `hostnameOverride`, `hostAliases[]{ip,hostnames}` | naming | `hostname`/`subdomain` are DNS-1123 labels; `hostnameOverride` is a ≤64-char subdomain and cannot combine with `hostNetwork` or `setHostnameAsFQDN`. `hostAliases[].ip` is a plain IPv4/IPv6 literal, zone suffixes rejected as for `dnsConfig.nameservers`. | additive |
| `podSecurityContext` | object | The full `corev1.PodSecurityContext` field set (`runAsUser`/`runAsGroup`/`runAsNonRoot`/`fsGroup`/`fsGroupChangePolicy`/`supplementalGroups`/`supplementalGroupsPolicy`/`sysctls`/`seLinuxOptions`/`seLinuxChangePolicy`/`seccompProfile`/`appArmorProfile`/`windowsOptions`), closed and validated like the container `securityContext`; the `windowsOptions` strings are held to upstream's rules as on a container (see "Container fields"). `sysctls[].name` must match the sysctl grammar (≤253 characters of dot- or slash-separated lowercase alphanumeric segments) and be unique within the list. `windowsOptions.hostProcess: true` additionally requires `hostNetwork: true`, which upstream demands of any pod containing HostProcess containers. The `runAsUser: 0` / `runAsNonRoot: true` contradiction is judged per container on the *effective* values once the containers are assembled, not on this object alone — a container-level `runAsUser` overrides the pod-level one, so the pair is a valid document when every container names a non-root UID, and the deferred check also catches a container-level `runAsUser: 0` under a pod-level `runAsNonRoot`. **Partly policy-gated**: `windowsOptions.hostProcess: true` is rejected unless `AllowPrivileged()` allows it (a HostProcess pod runs with the node's own privileges, and upstream forces every container in it to be HostProcess too); every other field has no policy hook. | additive |
| `imagePullSecrets[]{name}`, `enableServiceLinks`, `os{name}`, `hostUsers`, `readinessGates[]{conditionType}`, `resourceClaims[]{name, resourceClaimName \| resourceClaimTemplateName}`, `podResources{requests,limits}` | misc | `podResources` accepts only `cpu`, `memory` and `hugepages-<size>` (pod-level resources have no ephemeral-storage or extended resources), and a `hugepages-<size>` entry needs `cpu` or `memory` in `requests` or `limits` (admission's "HugePages require cpu or memory"; pod-level resources get no defaults); claim names must be unique and name exactly one source (an empty name beside the other counts as a second; the schema publishes the pair as an `exclusive` group). `hostUsers: false` cannot combine with `hostPID` or `hostIPC` (upstream forbids both outright); it stays authorable alongside `hostNetwork`, which upstream forbids only on a cluster without user-namespace host-network support, so whether that pair is accepted is a property of the target cluster rather than of the document. **`podResources` is policy-gated**: its cpu and memory requests and limits are checked against `MaxCPU()`/`MaxMemory()`, the same budget the container `resources` are checked against. | additive |

Deliberately **not** accepted — each is rejected with an error naming the
reason rather than silently ignored: `ephemeralContainers` (added to a running
pod through its `ephemeralcontainers` subresource, never on a template),
`priority` and `overhead` (the Priority and RuntimeClass admission
controllers, on by default, derive them from
`priorityClassName`/`runtimeClassName` and reject a pod whose authored value
differs, so authoring one can at best repeat the derived value),
`serviceAccount` (deprecated alias of `serviceAccountName`),
`evictionResponders` (alpha upstream, behind the `EvictionRequestAPI` feature
gate) and `containers` (the pod's one main container is the component's own
container properties, and further ones are `sidecars` entries where the kind
has them).

Every pod kind config implements `oam.ServiceAccountNamer` (`pkg/oam/handler.go`),
returning `(name, runsPods)`: the authored `serviceAccountName` (or `""`) and
`true`. A role rule hands its `deployment` member the name of the account its
`serviceaccount` member generates (the component name, or the one
`serviceAccountObjectName` or the `Naming` hook gave that object) unless
`serviceAccountName` is authored, so a `webservice` or `worker` answers the
account it generates. The `rbac` trait
(`pkg/oam/builtin/traits/rbac.go`) binds its RoleBinding/ClusterRoleBinding
subject to that name, so binding follows the account the pods run as; the Role
and binding objects keep their component-derived names. A pod-running
component with no account name is refused by `rbac` rather than bound to an
account that does not exist; see the traits README.

The pod's `serviceAccountName` is the namer's own answer, so the account a
RoleBinding names and the account the pods run as are one value by
construction. The shared kind test asserts they agree on every kind, including
against a deliberately mismatched Application name, and that a role kind's
generated ServiceAccount carries the same name.

`securityContext.privileged: true` is rejected unless the environment policy's
`AllowPrivileged()` allows it (`enforce.go`'s `enforcePrivileged`).
`securityContext.capabilities.add` is separately enforced against
`AllowedContainerCapabilities()`/`ForbiddenContainerCapabilities()` (see above,
`enforce.go`'s `enforceContainerCapabilities`). `enforcePrivileged` gates a
second field besides `privileged`: `securityContext.windowsOptions.hostProcess`
is rejected under the same `AllowPrivileged()`, on any container that authors
it.
Those three fields are the whole container-level policy surface:
`enforcePrivileged` and `enforceContainerCapabilities` are the only enforcers
taking a `*corev1.SecurityContext`, and every policy call outside `enforce.go`
that touches a container `securityContext` is a call site of one of them. The
package has other enforcers reached from the same `ApplyPolicy` bodies — the
pod-level pair described below, plus `enforceHostNamespaces`,
`enforceHostPathVolumes`, the `enforceMax*` family and
`enforceAllowedRegistries`/`enforceAllowedURLHosts` — but none of them reads a
container `securityContext`. Both checks cover the main container and
every `initContainers`/`sidecars` entry (the shared `enforceExtraContainer` helper), not just the main container.

The pod-level `podSecurityContext` has a **separate** hook —
`enforcePodHostProcess` (`enforce.go`) rejects
`podSecurityContext.windowsOptions.hostProcess` under `AllowPrivileged()`. It is
a different object, not an exception to the container-level list above:
`PodSpecConfig` embeds `corev1.PodSpec`, so its `SecurityContext` is a
`*corev1.PodSecurityContext`, which `enforcePrivileged` never sees. Both
spellings are authorable — see the `podSecurityContext` row under "Pod-level
properties".

The pod-level surface carries two policy checks of its own, both called from
all seven kinds' `ApplyPolicy` next to `enforceHostNamespaces`:
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
seven kind components' `ApplyPolicy`, not just one — a hostPath volume mounts
an arbitrary path from the node's own filesystem into the Pod, so an
unenforced policy denial here is a container-escape-adjacent gap, not merely
a style one. The same switch gates a PersistentVolume's `hostPath` and `local`
sources, on every path that produces the object (see **persistentvolume**
below): both name a path on the node, which a pod reaches by binding the
volume through a claim.

Setting any `securityContext` field makes the container's `SecurityContext`
non-nil, which opts it out of the downstream runtime's nil-only backfill for
every *other* `SecurityContext` field too. If a component uses both this and
the `security-context` trait, the trait's `Generate()` pass runs later and
**merges into** what is authored, field by field, on
the pod's `securityContext` and on every entry of `podSpec.Containers` (which
includes rendered sidecars) and `podSpec.InitContainers`; an init container's
or sidecar's own `securityContext` is merged the same way as the main
container's. Precedence, highest first:

1. A trait property: `runAsNonRoot`, `runAsUser`, `runAsGroup`, `fsGroup`,
   `allowPrivilegeEscalation`, `readOnlyRootFilesystem`. A different authored
   value for the same field, or a container-level `runAsUser`/`runAsGroup`
   that differs from the trait's pod-level one (it would override it at run
   time), fails the build with both values and the workload named. An equal
   value is fine.
2. The authored value. Everything the trait has no property for
   (`capabilities.add`, `privileged`, a `Localhost` seccomp profile,
   `seLinuxOptions`, `appArmorProfile`, `windowsOptions`, `procMount`,
   `sysctls`, `supplementalGroups`) is kept as authored.
3. The `psaLevel` profile, for a field still unset. At `restricted`, `ALL` is
   added to an authored `capabilities.drop` that lacks it.

The merged result is then checked against the declared `psaLevel`
(`baseline` and `restricted`; `privileged` has no check): a field the Pod
Security Standards forbid at that level, such as `privileged: true` or an
unlisted `capabilities.add` at `baseline`, fails the build rather than the
pod's admission at deploy time. Only `securityContext` fields are checked;
host namespaces, `hostPath` and volume types are not. Use the trait for a
safe, complete PSA-consistent default; use this property for the fields the
trait has no property for.

At every `psaLevel`, the merged result must also be a context Kubernetes
accepts and the kubelet can start. The build refuses `allowPrivilegeEscalation:
false` (from the trait property, the `restricted` default or the workload)
together with `privileged: true` or an added `CAP_SYS_ADMIN`, and
`runAsNonRoot: true` together with an effective `runAsUser` of 0, whether the
container or the pod sets it. The error names where each side came from. The
component's own checks run before the trait, so they cannot see a conflict the
trait introduces. A sysctl name is checked as authored, as Pod Security
Admission checks it: the component accepts `/` as a separator, but at
`baseline` and `restricted` only the dotted spelling of a safe sysctl is
allowed, so `net/ipv4/tcp_syncookies` and `net.ipv4/tcp_syncookies` are
refused and `net.ipv4.tcp_syncookies` is not. `privileged` has no check.

`env`, `envFrom`, `resources`, `lifecycle`, `securityContext`, `workingDir`, and
`probes` are each schema fragments parameterized by a `reserved bool` (mirroring
`pkg/oam/builtin/traits/schema.go`'s `schemaNetworkPolicy(reserved bool)`):
every built-in call site passes `false` today. Deciding which of these fields
should be platform-reserved (rejecting any authored value via
`PropertySchema.PlatformReserved`/`enforcePlatformReserved`) is a consumer-side
policy choice, not something this shared schema hardcodes.

### Deployment-level properties (`deployment`, `webservice`, `worker`)

Three components produce an `appsv1.Deployment`: the kind-named `deployment`
(ADR-036 L1, the stratified-levels decision: one
PodSpec/Container projection shared by every kind, with kind-named components
projecting their own API kind), and the two role-named kinds `webservice`
(Deployment + Service) and `worker` (Deployment, no Service). Everything above
— the container-level surface and the pod-level surface — applies to all three
unchanged, and so does the rest of `DeploymentSpec` below: it is parsed by
`parseDeploymentSpec` (`deployment_spec.go`) into real `appsv1` types and
written by its `apply` onto the built object, from one fragment all three
handlers compose. The pod template's `podMetadata` is the
same on all three (see ["The pod template's labels and
annotations"](conventions.md#the-pod-templates-labels-and-annotations-podmetadata)).

Sharing the fragment is the point rather than a convenience. These fields are
properties of the API kind, not of the role a component plays, so a document
that moves a workload from `webservice` to `deployment` — or the reverse —
should not lose a `strategy` on the way. Every rule the table below states,
including the refusals, therefore holds identically on all three.

The three kinds are still not layered, and one is not a superset of the others.
`deployment` deliberately publishes **no `port`** (and so emits no Service),
**no `affinity` shorthand** and **no default topology-spread constraint** —
none of those is a `DeploymentSpec` field, and a kind named after the API kind
should project the API kind rather than launcher's opinions about it. A
workload that wants launcher to create its Service uses `webservice`. This is
the reversible direction: adding a property later is additive, removing one is
breaking. Its `ports` list (below) is that kind of addition: container ports
are a `PodSpec` field, and declaring them emits no Service.

What `deployment` *does* publish, and the role kinds do not, is the raw
`corev1` form of the same three scheduling concerns — see "Raw scheduling
properties" immediately below. The two are not alternatives: the shorthand is
an opinion, the raw shapes are the API.

The default topology-spread opinion is still available on `deployment`, as an
explicit opt-in rather than a kind default: the `topology-spread` trait (see
the trait handlers README) applies the same
`BuildTopologySpreadConstraints` the role kinds use, exported for that reason,
from the Deployment's post-policy replica count, or from the matching `scaler`'s
`minReplicas` when the component carries one (see below). It refuses a Deployment that
already carries raw `topologySpreadConstraints`, so the two never merge.

#### Main container ports

`deployment` publishes `ports`, the main container's `corev1.ContainerPort` list. It declares container ports only: the kind still emits
no Service (use `webservice`, or a `service` component, for one).

**Endpoints.** `deployment`, `statefulset` and
`daemonset` implement `oam.EndpointProvider` from the main container's `ports`:
one endpoint per entry, in the authored order. An endpoint's `Name` is the port's
`name` (empty when unnamed), its `Ports` hold the numeric `containerPort` alone,
and its `Protocol` is the entry's (`TCP` when unset). Its selector is
`app: <value>`, the label the pods carry, where the value is the component name
as ["The `app` label"](conventions.md#the-app-label) derives it (a name over 63 characters is shortened), whatever
`objectName` or a Naming hook names the object. The type's own parse runs first, so a component it
refuses has no endpoint and is refused in the parse's words. Sidecar ports are
not the component's service surface and declare none. `job` and `cronjob`
publish `ports` but report no endpoint, and `worker` lowers into a `deployment`
without ports, so it reports none. Only TCP endpoints
synthesize a NetworkPolicy allow; a UDP or SCTP endpoint is collected and
validated, and synthesizes nothing.

`daemonset`, `statefulset`, `job` and `cronjob` publish the same `ports`, with
the same entry rules: container ports are a PodSpec
field, so every kind-named workload component projects them. `ports` emits no
Service on any of them. A named probe or hook port resolves against the
main container's `ports`; with none declared, it is refused.

**Breaking: `daemonset` and `statefulset` dropped `port`
and the Service it drove**, as `deployment` did. `port`
declared the main container's first port and a Service in front of it: a
ClusterIP Service named after the component on `daemonset`, a headless one
named `serviceName` (default: the component name) on `statefulset`. An authored
`port` is now refused as an unknown property. To migrate:

- Move the number into `ports` under the name `port` gave it, so named probe
  and hook ports keep resolving: `port: N` becomes
  `ports: [{name: http, containerPort: N}]` on `daemonset`,
  `ports: [{name: tcp, containerPort: N}]` on `statefulset`.
- Author the Service as a `service` component that selects the pods
  (`selector: {app: <component>}`). For a `statefulset`'s governing Service,
  set `clusterIP: None` and name it in `serviceName`.
- `serviceName` no longer defaults to the component name: unset, the
  StatefulSet carries `serviceName: ""`. The API server refuses a change to
  `spec.serviceName` on an existing StatefulSet, so one that relied on the
  default and now names the authored Service must be recreated: delete it with
  `--cascade=orphan` so its pods survive, then apply. The adopted pods keep the
  old `spec.subdomain`, which is set only when a pod is created, so their DNS
  names under the new Service do not resolve until they are replaced: run
  `kubectl rollout restart statefulset/<name>` (or, under `updateStrategy:
  OnDelete`, delete the pods one at a time). A `rollingUpdate.partition`
  excludes the pods below it from the restart: set it to `0` for the restart,
  or delete those pods one at a time. (The authored Service
  cannot keep the old name: it is the component's own, and component names are
  unique within an Application.)
- A trait that routed to the component's own Service (an implicit `ingress`,
  `httproute` or `expose` backend, or a `networkPolicy` ingress allow) now
  belongs on the authored `service`; on the workload it is refused with
  `component "<name>" has no service port`.

`examples/13-statefulset.yaml` shows the result.

| property | type | notes | compat |
|---|---|---|---|
| `ports` | array | Each entry is `containerPort` (required, 1–65535), `name` (an IANA service name, as the API server checks a container port name) and `protocol` (`TCP`/`UDP`/`SCTP`, default `TCP`; an empty string is refused, as the published enum refuses it); any other key, `hostPort` and `hostIP` included, is refused. Names must be unique, as the API server requires, and also unique across the pod: a sidecar port of the same name is refused. A repeated `containerPort`/`protocol` pair is refused too, which the API server only warns about: the second entry declares nothing new. The same number on two protocols is two ports. An absent, null or empty list declares no ports. A probe or lifecycle hook may address a declared port by name; a name the main container does not declare is refused, since the kubelet resolves it only against that container's own ports. Without `ports`, a named probe or hook port is refused. | additive (`daemonset`/`statefulset`: replaces `port`) |

#### Raw scheduling properties

`deployment` publishes `affinity`, `tolerations` and
`topologySpreadConstraints` as their plain `corev1` shapes, parsed by `scheduling.go`. Nothing is inferred from the
component: every selector, weight and topology key is authored.

The other kind-named workloads publish the same shapes through the same parsers
and schemas, so every rule in the table below holds wherever the key is
published:

| kind | `affinity` | `tolerations` | `topologySpreadConstraints` |
|---|---|---|---|
| `deployment` | raw | yes | yes |
| `statefulset` | raw | yes | yes |
| `daemonset` | raw | yes | yes |
| `job` | raw | yes | yes |
| `cronjob` | raw | yes | yes |
| `webservice`, `worker` | four-key shorthand | yes | yes |

`webservice` and `worker` publish `tolerations` and `topologySpreadConstraints`
as these same shapes, from the same schemas and parsers.
Their `affinity` stays the four-key shorthand. The constraints cost one more
property: both roles apply the default `topologySpread` through a synthesized
`topology-spread` trait, which refuses a Deployment that already carries
constraints, so authored `topologySpreadConstraints` need `topologySpread: false`.
Without it the build is refused with the trait's message, which names that
property. Each role's own parse checks both keys, so a bad value is refused with
the role's text before lowering.

**Breaking: `statefulset` dropped the four-key `affinity`
shorthand.** Its `affinity` is the raw `corev1` shape, as on the other kinds in
this table, so `enablePodAntiAffinity`, `topologyKey`, `podAntiAffinityType` and
`nodeSelector` under it are refused as unrecognized keys. To migrate, author
what the shorthand built: a `podAntiAffinity` term selecting `app: <component>`
(see [The `app` label](conventions.md#the-app-label)) over the topology key, `preferred` with
weight 100 or `required`; and, for `nodeSelector`, a required `nodeAffinity`
with one `In` requirement per label.

- Nothing is defaulted on any of them: an unauthored key emits nothing, and a
  constraint or an affinity term selects only the pods its authored
  `labelSelector` names. The pods a kind builds carry `app: <component>` (see
  [The `app` label](conventions.md#the-app-label)).
- **No policy check.** `tolerations` is accepted with no capability gate and no
  `Policy` method on every kind that publishes it, so a component can tolerate
  any taint, a control-plane node's included; a platform that reserves nodes
  by taint has to enforce that at admission.
- The `topology-spread` trait stays Deployment-only. It is refused on a
  `statefulset`, a `daemonset`, a `job` and a `cronjob`, with or without
  authored constraints (`component "<name>" generates no Deployment the trait
  can act on`).
- On a `cronjob` the three keys are written onto the pod template of the Job
  template (`spec.jobTemplate.spec.template.spec`), so they apply to the pods of
  every Job the CronJob creates.
- On a `daemonset` the three keys reach the DaemonSet's pod template as
  authored. The DaemonSet controller creates one pod for each node the
  template's node affinity, node selector and tolerations admit, and pins the
  pod to that node; a pod affinity term or a `DoNotSchedule` spread constraint
  the node cannot satisfy leaves that pod pending instead of placing it
  elsewhere. Nothing here checks for that.

| property | type | notes | compat |
|---|---|---|---|
| `affinity` | object | `nodeAffinity`, `podAffinity`, `podAntiAffinity`, each with the `requiredDuringSchedulingIgnoredDuringExecution` / `preferredDuringSchedulingIgnoredDuringExecution` arms. Node requirement operators are `In`/`NotIn`/`Exists`/`DoesNotExist`/`Gt`/`Lt`, with upstream's arity rule — `In`/`NotIn` need at least one value, `Exists`/`DoesNotExist` none, `Gt`/`Lt` exactly one integer. `matchExpressions` keys are node label keys (qualified names). `matchFields` keys are *not* qualified names and are not free-form field paths either: `metadata.name` is the only key Kubernetes accepts, and a field requirement takes only `In` or `NotIn` with exactly one value — the arity rule above belongs to `matchExpressions`, and a `matchFields` entry it would accept can still be refused at admission. That single value is a node name and is validated as a DNS-1123 subdomain: the key resolving is what *arms* the value rule upstream rather than ending it, so a well-formed key and operator with a malformed value is still refused. `matchFields` therefore publishes its own item schema rather than sharing `matchExpressions`', with `key` and `operator` enums matching the parser — an emitted property is checked against the schema's enums before conversion, so a looser schema would advertise operators to a lowering rule that the parser then rejects. Weights must be 1–100. An `affinity` with no arm set is rejected, as is a node selector term with neither `matchExpressions` nor `matchFields` — upstream documents such a term as matching no nodes, so it can only be a mistake. The published schema marks `nodeSelectorTerms`, and a weighted arm's `preference` and `podAffinityTerm`, as required — the parser already refused each of them being absent, so this changes what schema introspection reports, not what builds. | additive |
| `tolerations` | array | The same property `daemonset` already publishes, from the same parser, now covering the complete `corev1.Toleration`: `key`, `operator` (`Exists`/`Equal`/`Lt`/`Gt`), `value`, `effect`, `tolerationSeconds`. `tolerationSeconds` is a pointer upstream, so unset (tolerate forever) and `0` (evict immediately) are different documents. A non-empty `key` must be a qualified name; the empty key stays legal and is governed by the rule below. Cross-field rules: an empty `key` requires `Exists`; a `value` under `Exists` is refused; under `Equal` the `value` must be a valid label value, since it is matched against a taint's; `Lt`/`Gt` need a canonical decimal integer `value` (no leading zeros, no plus sign, no `-0` — `Toleration.ToleratesTaint` runs `content.IsDecimalInteger` before parsing and silently matches nothing otherwise) and the cluster's `TaintTolerationComparisonOperators` gate; `tolerationSeconds` requires `effect: NoExecute` spelled out, an omitted effect not counting as a wildcard for this rule. An unrecognised key is reported rather than dropped, and so is `tolerations` authored as something other than an array. | additive for `deployment`; **narrowing for `daemonset`** — see below |
| `topologySpreadConstraints` | array | `maxSkew` (required, > 0), `topologyKey` (required), `whenUnsatisfiable` (required, `DoNotSchedule`/`ScheduleAnyway`), `labelSelector`, `minDomains` (> 0, and only with `DoNotSchedule`), `nodeAffinityPolicy`/`nodeTaintsPolicy` (`Honor`/`Ignore`), `matchLabelKeys`. Two constraints may not repeat the same `(topologyKey, whenUnsatisfiable)` **pair**; sharing a `topologyKey` with different `whenUnsatisfiable` values is legal and stays accepted. The three required fields carry no `omitempty` upstream, so an unset one would emit `maxSkew: 0` / `topologyKey: ""` / `whenUnsatisfiable: ""` rather than an API default — hence required here rather than defaulted. | additive |

##### What `tolerations` changed for `daemonset`

`affinity` and `topologySpreadConstraints` are new properties on a kind that did
not have them, so they are additive outright. `tolerations` is not: `deployment`
reaches it through the *same* `parseTolerations`/`schemaTolerations` pair that
`daemonset` has always used, and those two kinds were its only callers then, so
completing the projection changed `daemonset` too. (A kind that gained
`tolerations` later, `statefulset`, `job` and `cronjob`, had no earlier
behaviour to change: for it the property is additive.) Stating that plainly, per
rule, because "additive" on its own would be false:

| change | effect on `daemonset` | why it is kept rather than gated to `deployment` |
|---|---|---|
| A well-formed `tolerationSeconds` is now read | **Additive in capability, and the one change that alters emitted bytes.** The key was previously accepted and silently dropped, so a `daemonset` that already authored it emitted a toleration without an eviction deadline; it now emits the field it asked for. That is a deliberate output change, not a preservation. | It is the fix, not a side effect. |
| A **malformed** `tolerationSeconds` is now an error | **New errors only.** A non-integer, fractional or out-of-int64-range value was previously ignored along with the rest of the key; it is now type-checked like any other integer property. | The key cannot be both read and unvalidated. A document carrying one was already not getting the deadline it asked for. |
| `operator: Lt` and `operator: Gt` are now accepted | **Purely additive.** Both were previously refused as invalid operators; they are valid `corev1` values (`core/v1/types.go:4097-4100`). Their `value` must be a canonical decimal integer — no leading zeros, no plus sign, no `-0` — because the scheduler's own matcher silently refuses to match any other form. | A "raw" projection whose operator set is narrower than the API's is the wrong shape for either kind. |
| An unrecognised key inside a toleration entry is now an error | **New errors only, and here output really is byte-identical.** A key the parser never read contributed nothing to the emitted object, so every document that still builds emits exactly what it emitted before. | `docs/oam/design-gvk.md` already states that an unrecognised key is a build error; this moves `daemonset` toward the documented contract rather than away from it. Gating it to `deployment` would leave `daemonset` permanently accepting shapes that do no work. |
| An empty `key` with a non-`Exists` operator is now an error | **New errors only, and only on documents the apiserver would have refused.** Upstream states it as a hard "must" (`k8s.io/api@v0.36.3` `core/v1/types.go:4093`). | Nothing appliable is lost. |
| `tolerations` authored as a mapping rather than an array is now an error | **New errors only, and output is byte-identical.** The whole property was previously discarded without a word — the parser's `[]any` assertion failed and it returned "absent", so a mistyped block emitted no tolerations at all and the build succeeded. Its two sibling parsers on the adjacent lines (`affinity`, `topologySpreadConstraints`) already rejected the same mistake, so this removes an inconsistency rather than adding a rule. | A silently discarded property is the failure mode this whole projection exists to remove; leaving `daemonset` on the old behaviour would keep the one parser that swallows a typo. |
| An explicit null on a toleration's `key`, `operator`, `value` or `effect` now reads as omission | **Fewer errors, and output is byte-identical for everything that already built.** Such an entry previously failed conversion with `must be a string, got <nil>`; it now behaves as if the key were absent, which is this package's null-as-omission convention. Only `null` is affected — an empty string is unchanged, so `operator: ""` is still an error rather than a silent default. | The null cannot be filtered before it arrives: `withoutExplicitNulls` strips only top-level properties, so an author writing a nested null reaches the parser with it intact. `ValidateAuthoredProperties` (`pkg/oam/property_validate_authored.go`) does shape-check authored documents, but its reach stops at any path that skips it entirely — a component a lowering rule constructs directly in Go, or a built-in parser's own field read, this file among them (the "Scope" paragraph of `site/content/api-reference/oam/schemas-and-contract.md`) — which is why the parser has to answer for itself and why these guards stay. A lowering rule's emitted null under an optional declared key is now normalised to absence before conversion (same file, `validateObjectProperties`), so the two paths agree on what a null means rather than one accepting what the other refuses. Leaving `daemonset` out would keep one kind refusing documents the schema declares valid. |
| A `value` under `operator: Exists` is now an error | **New errors only, and only on documents the apiserver would have refused** — upstream's `ValidateTolerations` rejects the pair outright, notwithstanding the field doc's softer "should" (the citation, and its second-hand provenance, are at the check itself in `common.go`). | Same: nothing appliable is lost. |
| A non-empty `key` that is not a qualified name is now an error | **New errors only, and only on documents the apiserver would have refused.** `ValidateTolerations` applies `ValidateLabelName` to every non-empty key, unconditionally and behind no feature gate. The empty key is untouched — it remains legal and keeps its own rule. | Nothing appliable is lost, and it is the same rule the affinity and topology-key paths already apply to their own label keys; `daemonset` accepting a malformed key there and not here was an inconsistency, not a policy. |
| Under `operator: Equal`, a `value` that is not a valid label value is now an error | **New errors only, and only on documents the apiserver would have refused.** The value is matched against a taint's value, so upstream runs `IsValidLabelValue` on it for the `Equal` arm. `Exists` and `Lt`/`Gt` reach their own value rules first, so this fires on `Equal` alone. | Same: nothing appliable is lost. |
| `tolerationSeconds` without `effect: NoExecute` is now an error | **New errors, and *not* only on documents the apiserver would have refused** — this row is reason 2 below, not reason 1. Upstream rejects the pair outright, and an omitted `effect` does not satisfy the rule (it has to be spelled out), but the apiserver never saw the pair: `tolerationSeconds` was previously dropped along with the rest of the key (first row of this table), so the emitted toleration carried no deadline and applied cleanly. A document hitting this rule may well have built and been accepted by a cluster before. This one was deliberately left unenforced until now, on the stated grounds that the only reachable citation was a second-hand copy carried by a dependency; the comment named a first-hand citation as the condition for adding it, and that condition is now met. | A `tolerationSeconds` on any other effect is inert at best and refused at apply at worst, which is the failure class this projection exists to remove. Leaving it would also contradict the rule's own recorded condition. |

Net, stated by cause rather than by a count or a partition. A count here went
stale twice; the replacement then asserted that the classes were exclusive and
that exactly one of them contained previously-accepted documents, and both of
those were wrong too. Three failed summaries of the same table is a sign the
summary wants a different shape, so this one describes what the rows have in
common and makes no claim about which row a document lands in. A `daemonset`
document stops building for one of these reasons, and one document can hit
several at once:

1. **It was refused on apply anyway.** An empty `key` with a non-`Exists`
   operator, a `value` under `Exists`, a non-qualified non-empty `key`, an
   invalid label value under `Equal`. Nothing appliable is lost.
2. **Part of it was being discarded unread.** An unrecognised member inside an
   entry, `tolerations` authored as a mapping rather than an array, or a
   `tolerationSeconds` the parser never looked at — malformed, or well-formed
   but carrying an `effect` other than `NoExecute`.

Reason 2 is the one that matters, and it is not confined to documents that were
doing nothing: an entry can carry an unread member alongside tolerations that
worked, and a `tolerations` block authored as a mapping left the rest of the
workload building and being accepted. So a document rejected under reason 2 may
well have built **and been accepted by a cluster** before — it simply was not
getting what it asked for, and the apiserver never saw the part that was
dropped. Reading those fields, which is the fix, is what makes them visible.
There is no version of the fix that keeps these documents building and also
emits what they asked for.

Output is byte-identical for everything that still builds, **unless it authored a
well-formed `tolerationSeconds` with `effect: NoExecute`**, in which case the
field it wrote now appears — the defect being fixed, not a regression.

The table is not uniformly a tightening: some
rows go the other way, accepting or emitting what `daemonset` previously refused
or dropped — a well-formed `tolerationSeconds`, `operator: Lt`/`Gt`, and a nested
explicit null in a toleration entry. Read the middle column per row rather than
assuming a direction. This is pre-release `v1alpha1`; the change is
taken deliberately rather than hidden behind a `deployment`-only option whose
removal would depend on unrelated work landing.

One rule applies to both `affinity` terms and topology-spread constraints:
`matchLabelKeys` (and `mismatchLabelKeys` on a pod affinity term) cannot be set
without a `labelSelector`.

A second rule applies to `matchLabelKeys` only: none of its keys may already be
constrained by that `labelSelector`, in either `matchLabels` or
`matchExpressions`. `mismatchLabelKeys` is deliberately *not* held to it, even
though its upstream field doc reads as a mirror of `matchLabelKeys`': upstream's
validation only ever builds its forbidden-key set from `matchLabelKeys`, because
a `mismatchLabelKeys` entry is merged as a `NotIn` requirement and filtering
further on the same key is a legitimate thing to want. Enforcing the doc's
wording would refuse a document the API server accepts.

A third rule applies to the *entries* of both lists, and to a pod affinity term's
`namespaces`: every `matchLabelKeys` / `mismatchLabelKeys` entry must be a valid
label key (a qualified name), and every `namespaces` entry a valid DNS-1123
label. These are shape rules the API server enforces on apply, so a document
that fails them was never going to reach the cluster — accepting it here only
moves the failure from build time to admission time. That is the opposite
direction from the `mismatchLabelKeys` overlap rule declined just above: there,
upstream does not validate, so enforcing would be *stricter* than the API; here
it does, so accepting is *looser*. Both errors are worth avoiding and they are
not in tension.

An **empty** `labelSelector: {}` is accepted here, unlike on a volume claim's
`selector` where launcher refuses it. Upstream distinguishes the two: a null
`labelSelector` on a `PodAffinityTerm` matches no pods, while an empty one
matches every pod in scope. Refusing the empty form would make a real API shape
unexpressible.

These three keys live in the `deployment` handler's own property map, above the
`maps.Copy` calls that merge the shared pod-level and `DeploymentSpec`
fragments, and they must stay there. `maps.Copy` overwrites the destination, and
`worker` and `webservice` each set the four-key `affinity` shorthand in their
own map and *then* copy the shared pod-level fragment over it — so moving a raw
`affinity` into that fragment would silently replace the shorthand on both,
with no fixture moving to reveal it.

**Routing traits on a `deployment` need an explicit Service.** `expose`,
`ingress` and `httproute` are accepted on this kind — nothing restricts them —
but they are not self-sufficient here. `expose` lowers into `ingress` or
`httproute`, and both resolve an *implicit* backend through the component's own
service port. A `deployment` has no service port and emits no Service, so an
implicitly-backed route fails the build with a "has no service port" error.
Route to it by naming the target Service on the trait, with `serviceName` **and**
`servicePort` (`serviceName` requires `servicePort`; setting `servicePort` alone
routes to a Service named after the component). That Service must exist
independently — authored as a `manifests` component, or belonging to another
component in the package. Nothing in this kind creates it.

With automatic NetworkPolicy synthesis, a trait-level `serviceName` that differs
from the component name is an external backend: a `networkPolicy.trafficSources`
entry on such a trait lands its allow on the pods of the component that owns the
named Service (on `servicePort`), not on this component's. When no component in
the package owns that Service, no allow is synthesized for it and it stays
authored. A `serviceName` equal to the component name — like `servicePort` alone
— is treated as this component's own Service and keeps the allow here. `worker`
behaves the same way, and so do `daemonset` and `statefulset`, which emit no
Service either: a `statefulset` routing to its governing
Service names that authored `service`, which owns it, so the allow lands on that
Service's selector.

**`scaler` is available on `deployment`**, as on `webservice` and `worker`. An HPA
scales the Deployment past its replica count, and the non-RWX guard below checks
only the component's effective replica count (authored, or the policy default),
so it cannot account for later HPA scaling. The `scaler` trait covers
that gap itself: `deployment` reports its first non-RWX claim to the trait
(`NonRWXClaim`), and the trait refuses an effective `maxReplicas` above 1 when
one is present (see "Non-RWX volumes"). `scaler` is available on `statefulset`
too, with the HPA's `scaleTargetRef` naming a `StatefulSet`.
That kind reports no non-RWX claim and the scaler adds no such check for it: its
claims come from `volumeClaimTemplates`, one per pod, so scaling does not make pods
contend for them, and `statefulset` applies no non-RWX limit to an authored replica
count either. A claim shared through `volumes` stays as unguarded under a scaler as
it is under authored replicas.

**`deployment` reports its pod template labels** (`PodTemplateLabels`), so a
same-name sibling group can tell that a `service` member selects this
Deployment's own pods, which the group's component-label policy already
covers (see `pkg/oam` "Same-name sibling groups"). The labels it reports are the
component's own and the ones `podMetadata.labels` adds, as the pod template carries them. The deployment member of a
`webservice` or `worker` reports the same, authored `podMetadata.labels` included. `statefulset`, `daemonset`,
`job` and `cronjob` do not have the method, deliberately: its one reader is that group check, and `deployment` is the only pod
kind a lowering rule emits into a group (`webservice`, `worker`). A rule that
emits another pod kind into a group must add the method to that kind.

| Property | Type | Effect | Compatibility |
|----------|------|--------|---------------|
| `strategy` | object | `type` (**required when `strategy` is authored**) is `Recreate` or `RollingUpdate`; `rollingUpdate` may only accompany `RollingUpdate`, matching `ValidateDeploymentStrategy`, which marks it Forbidden under `Recreate`. Omitting `strategy` entirely writes nothing, leaving apiserver defaulting to supply `RollingUpdate` with 25%/25%. | additive |
| `strategy.rollingUpdate.maxUnavailable` | int ≥ 0 or percentage string | Same form `ValidatePositiveIntOrPercent` accepts: a non-negative integer, or a `^[0-9]+%$` string — so `+50%` is rejected, as upstream rejects it. Capped at 100%, matching the `IsNotMoreThan100Percent` call upstream makes on this field, and additionally required to be convertible by the rollout controller (see `maxSurge`). | additive |
| `strategy.rollingUpdate.maxSurge` | int ≥ 0 or percentage string | Same form, **not** capped at 100%: `ValidateRollingUpdateDeployment` calls `IsNotMoreThan100Percent` on `maxUnavailable` only, so a surge above 100% is a legal Deployment and is accepted here. This is one of two places the Deployment contract differs from the DaemonSet one, which is why the two parsers are separate functions rather than one shared helper. Uncapped is not unbounded: the `^[0-9]+%$` form check accepts any run of digits, so a percentage too long for the rollout controller's own `strconv.Atoi` (`getIntOrPercentValueSafely`, which `ResolveFenceposts` calls) is rejected here, naming the field, rather than carried verbatim into the object to fail during a rollout with this component out of the message. The boundary is that conversion's, so nothing upstream accepts is refused here. | additive |
| `strategy.rollingUpdate` (both knobs zero) | — | Rejected: `ValidateRollingUpdateDeployment` refuses `maxUnavailable: 0` together with `maxSurge: 0`, a pair that can make no progress. It rejects **only** that pair — unlike the DaemonSet rule, which also rejects both being non-zero. `SetDefaults_Deployment` guards each field with its own `== nil` check, so authoring one knob does not suppress the other's 25% default and a lone authored zero is always legal. | additive |
| `minReadySeconds` | int ≥ 0 | Seconds a new pod must be ready before it counts as available. Must stay **below** the effective `progressDeadlineSeconds`, whose own API default is 600 — so `minReadySeconds: 600` alone is rejected here, exactly as the apiserver would reject the defaulted pair. | additive |
| `revisionHistoryLimit` | int ≥ 0 | Old ReplicaSets retained. `0` is meaningful (retain none) and distinguishable from unset. | additive |
| `paused` | bool | Pauses rollouts of the Deployment. | additive |
| `progressDeadlineSeconds` | int ≥ 0 | Must be **greater than** the effective `minReadySeconds`, the cross-field rule `ValidateDeploymentSpec` applies. Both halves are compared as *effective* values, because both have an API default a document may be leaving it to: `minReadySeconds` defaults to 0 (a non-pointer `int32`, so it has no unset state) and `progressDeadlineSeconds` defaults to 600. So the rule fires in both directions — `progressDeadlineSeconds: 0` alone is rejected against the defaulted 0, and `minReadySeconds: 600` alone is rejected against the defaulted 600 — and the error names, for each half, whether the value was authored or defaulted, since either can be a field the document never mentions. | additive |
| `selector`, `template` | — | Not authorable, and rejected with a message saying so rather than silently ignored. The selector is builder-managed (`app: <component>`) and immutable once the object exists; the pod template is projected from the component's own container and pod-level properties. | additive |
| any of the five `DeploymentSpec` properties above, authored as `null` (on `deployment`, `webservice` and `worker` alike); **and, on `deployment` only, any optional property of the kind** | — | Read as omission, not as a present-but-wrong type — including a typed nil, which is what a Go-constructed lowering rule produces when it assigns a nil map into an `any`. `pkg/oam`'s property validator already treats a null under an optional property as absent, so without this a component could satisfy the published schema and then fail during handler conversion. `parseDeploymentSpec` strips nulls itself, so the five fields above behave this way on `webservice` and `worker` too. The wider guarantee is the `deployment` kind's alone: there it covers the kind's whole **top-level** surface, so `replicas: null`, `workingDir: null`, `env: null` and the rest all read as unauthored and `replicas: null` takes the default 1. This is no longer specific to `deployment` for a property read through one of the shared field helpers in `common.go`, which test presence with `authoredValue`, nor for the six parsers that used to read their own property with a bare map lookup rather than a helper — `envFrom`, `probes`, `lifecycle`, `securityContext`, `volumes`, `accessModes` — which were converted to the same primitive. It also covers the top-level keys that were still answered with a bare lookup and then type-checked — `cronjob`'s `timeZone`, `successfulJobsHistoryLimit` and `failedJobsHistoryLimit`; `completionMode` on `job` and `cronjob`; `passthrough`'s `clusterScoped`; `inline` and `url` on `crd` and `manifests`; and `manifests`' `scopeOverrides` — so a null there, typed or untyped, now reads as omission instead of a wrong type. That mattered because `ValidateAuthoredProperties` does not strip a top-level null, so `kurel build` used to reach the refusal. **One deliberate exception, and it is not an oversight:** the `postStart`/`preStop` handler keys (`httpGet`, `exec`, `sleep`, `tcpSocket`) still answer presence with a bare lookup, because there the rule being enforced *is* presence — `lifecycle: {preStop: {tcpSocket: null}}` must stay a refusal, since reading it as absence would let an authored-but-empty `tcpSocket` vanish and a valid sibling handler win silently, which is the exact silent-drop the surrounding paragraph rejects. The exception covers a probe's handler keys (`httpGet`, `tcpSocket`, `exec`, `grpc`) as well: `countProbeHandlers` reads them the same way, for the same reason, so a null one is refused too. Absence and authored-null are genuinely different documents for those handler keys. A `null` on a field that is required once its parent is authored (`strategy.type`) surfaces as the requiredness error, not a type error; `selector`/`template` are not optional properties, so naming either as `null` still earns the refusal above. **It now reaches nested keys too** — `securityContext: {runAsUser: null}` builds as if `runAsUser` were omitted. That fix deliberately sits *inside* each nested parser, after its unknown-key rejection, rather than in a recursive pre-strip over the property map: a recursive strip would remove `securityContext: {bogusKey: null}` before `parseSecurityContext`'s own rejection ever saw it, turning a named refusal into silence — trading one silent-drop bug for another. So a recognized nested key read through the helpers treats a null as absence, and an unrecognized one is still named and refused; both sides of that `securityContext` boundary are pinned in `deployment_nested_null_test.go`. The two nested map readers that used to read each entry raw now skip a null entry too, as `stringMapStrict` already did: `parseResourceList` (a `resources.requests`/`limits` quantity, where property validation deletes a null first only under the declared `cpu` and `memory` keys) and `parseLabelMap` (a `nodeSelector`, `service` `selector` or `matchLabels` value). As with `securityContext`, the skip comes after the key check: an invalid resource name or label key is still refused whatever its value, and a wrongly typed entry is still refused. A map left empty by the skip meets the empty-map refusal where one exists — `service`'s `selector: {app: null}` is refused as `selector: {}` is, rather than emitting a selector-less Service. | additive |

Every field above is presence-gated: a document that authors none of them
produces the same object the builder produced before, so the apiserver's own
defaults still apply rather than being frozen into the manifest.

An existing `webservice` or `worker` document that authors none of the five
builds byte-identically, and the two rules that look like new restrictions are
reachable only through them: a `strategy` contradicting the non-RWX guard, and
the `selector`/`template` refusals. The one visible change to a document
authoring none of them is the wording of the non-RWX replica refusal, now the
shared message ("a non-RWX PVC allows at most one replica") rather than each
kind's own; the documents it refuses are exactly the ones it refused before.

**A document that *was* authoring one of the five keys is a different case, and
it is not covered by "additive".** These kinds did not previously refuse those
keys as unknown — they ignored them. A handler reads the properties it knows
and drops the rest, and at the time this paragraph was first written the
authored path had no shape check of its own: `validateProperties` runs on
*emitted* elements only (`pkg/oam/property_validate.go`), and `validate.go`
checks type names and identity but not property shape. So `strategy` on a
`webservice` was accepted and silently discarded, and is honoured now. Such a document kept building but compiled to a
different Deployment, and one authoring a malformed value now fails the build
where it previously succeeded.

Under the additive test in `docs/oam/design-gvk.md` — every previously valid
document stays valid *and* compiles to the same output — that was not
additive. The gap was not this change's: it applied to every property ever
added to an existing kind, because the authored path did not enforce the
Parser Strictness that section promises. `docs/oam/design-gvk.md` § Parser
Strictness states that an unrecognised key is a build error, which was true of
the document envelope and not of a component's `properties` map. **It is true of both:** `kurel build` now calls
`ValidateAuthoredProperties` (`pkg/cmd/kurel/build.go:149`, after parameter
resolution so a package-mode `${...}` placeholder is not checked against its
own bare-string type), which walks declared keys and shapes on the authored
document itself. An authored `strategy` malformed enough to fail that check now
fails there, before the handler ever sees it.

**`replicas` is validated the same way on every kind that reads it** —
`deployment`, `webservice`, `worker`, `statefulset` and `postgresql` share one
checked reading (`parseReplicas`, `common.go`). A value that is not an integer
is an error naming its type (`replicas: must be an integer, got string`), and a
negative is an error naming the value (`replicas: must be >= 0, got -1`).
Absent or `null` takes the default 1. `replicas: 0` is accepted everywhere.
An authored count, including 0 and 1, wins over a policy `DefaultReplicas`;
only an absent or `null` one takes it.
Previously only the `deployment` handler did this. The
other handlers fell back to the default when the value would not convert, and
carried `replicas: -1` through to an object the apiserver then refused
(`ValidateDeploymentSpec` and `ValidateStatefulSetSpec` run
`ValidateNonnegativeField` on it). What that changes in practice differs by
entry point:

- **`kurel build`** already refused a non-integer on every kind through the
  authored-property check (see above). The change it sees is the negative
  count, which is now a build error on those kinds too.
- **A library caller that runs the handlers without
  `ValidateAuthoredProperties`** used to get one replica for
  `replicas: "3"` without any error. It now gets the handler's own error.

**Every integer property accepts every Go integer kind.** The shared readers
(`toInt32`/`toInt64`, `parsePort`, the history limits, quantity and whole-number
rendering in `common.go`) read through `oam.IntegerValue`. So a `uint16` port or an
`int8` history limit from a lowering rule or a Go caller reads like the `int` a YAML
literal decodes to. Each reader then checks the value against its own target (a port
is 1–65535, a count fits `int32`) and refuses it with an error rather than truncating
or wrapping. That includes the main and sidecar container
ports. (`daemonset`/`statefulset` read `port: 0` as "no port" until they
dropped `port`.)

**Non-RWX volumes.** A `ReadWriteOnce` (or `ReadWriteOncePod`) claim cannot be
held by an outgoing and an incoming pod at once, so the handler allows **at
most one replica** (`replicas: 0` is a deliberate scale-to-zero and is accepted
rather than coerced to 1) and forces `strategy.type: Recreate`. The
scale-to-zero exemption covers the replica count only: the strategy is still
forced, and an authored `RollingUpdate` still refused, at zero replicas —
scale-to-zero is a state a later edit can leave by changing one number, and a
`RollingUpdate` that survived the guard while at zero would be wrong the moment
the first pod starts. Kubernetes rejects neither
combination — the second pod simply hangs unschedulable or stuck attaching — so
this is a build-time guard, not a mirror of an apiserver rule. An authored
`strategy.type: RollingUpdate` in that situation is **rejected** rather than
overwritten, so a document never ships a strategy its author did not write.
That refusal reaches `webservice` and `worker` only now that they publish
`strategy` at all; previously those two substituted `Recreate`
silently, because there was no authored value to contradict. `deployment`,
`webservice` and `worker` all run the one `applyNonRWXConstraint`.

That guard checks the component's effective replica count, but a `scaler`
trait's HPA scales the same Deployment up to `maxReplicas`, which the guard
cannot see. So on `deployment`, `webservice` and
`worker` the trait is held to the same limit: with a non-RWX claim attached, an effective
`maxReplicas` above 1 fails the build with an error naming the `scaler` trait
and the claim's volume. "Effective" means after an EnvironmentPolicy
`scalerMaxReplicas` default is applied. `maxReplicas: 1` still builds. On
`webservice` and `worker`, a document that combined the two used to build and
then left pods 2 and up unschedulable or stuck attaching; it is now refused
as a pre-GA format change (`docs/oam/design-gvk.md` § Document-Format
Lifecycle). On
`deployment` the combination was never accepted, because `scaler` was not
admitted there: the trait is newly admitted with this safeguard in place.

**Replicas under a `scaler`**. The HorizontalPodAutoscaler
owns the replica count of the Deployment or StatefulSet it scales, so with a
`scaler` on a `deployment`, `webservice`, `worker` or `statefulset` the object is
built **without `spec.replicas`**. That holds for the kind's default of 1 and for an
EnvironmentPolicy `DefaultReplicas`, which no longer reach that object; the
policy's `MaxReplicas` ceiling is not checked against that unused count either,
and holds on the scaler's own `maxReplicas`. An authored `replicas` beside a `scaler` is refused at validation, naming the
component, since the count would be accepted and applied to nothing; set
`minReplicas` and `maxReplicas` on the trait instead. A property still counts
when a component lowering rule emits it, and an authored `null` is no value. The
omission is part of how the kind builds the Deployment, so it does not depend on
the order the traits are declared in; the component's other objects are
unchanged. The non-RWX replica check reads one pod under a `scaler`, since the
held count is unused (the HPA-bound check on `maxReplicas` is the guard), and the
claim's `Recreate` strategy still applies. The `topology-spread` trait finds no count on
the Deployment here, so under a `scaler` it counts the scaler's `minReplicas`
instead. That applies to a Deployment only: `topology-spread`
acts on Deployments, so on a `statefulset` it is refused with or without a `scaler`,
and a scaler whose target is a StatefulSet never supplies the count of a Deployment
that happens to share its name. A `webservice` or `worker` already gets this
spread from its own `topologySpread` default, so an authored `topology-spread`
trait beside a `scaler` with a `minReplicas` of 2 or more is now refused, as it
is without a scaler at 2 or more replicas. It was admitted before, because the
default applied nothing under a `scaler`. Why: Kubernetes advises removing `spec.replicas` when an HPA manages a
Deployment, because a manifest that keeps it can reset the count to that value
on an apply (a server-side apply without force reports a conflict instead once
the HPA owns the field), and the count flaps (Kubernetes documentation, "Horizontal Pod
Autoscaling", section "Migrating Deployments and StatefulSets to horizontal
autoscaling").

Migrating an object that is already deployed with `spec.replicas`: the next apply
omits the field. With a client-side `kubectl apply`, a field present in the
last-applied configuration and absent from the new one is deleted from the live
object, which resets the count to the default of 1 even after the HPA has scaled
the workload; remove `replicas` from the last-applied configuration first (or
move to server-side apply). With server-side apply, what the omission does depends
on who owns the field at that moment
(Kubernetes documentation, server-side apply, "Transferring ownership"). If the
HPA has already written the count and so owns the field, omitting it only gives
up the applier's share and the count is kept. If the HPA has not yet written it,
for example because the scaler is added in the same change, the omission leaves
no owner and the API server resets the field to its default of 1, which can
briefly degrade a running workload. The documented ways around that are to keep
the old `replicas` in the applied configuration until the HPA has written the
count (the applier then gets a conflict, and the field is safe to remove), or
to hand the field over first with a one-off apply under a temporary field
manager. Where the applier cannot do either, apply the change once the HPA has
scaled the workload.

The guard reads a claim's **whole** access-mode set, not each mode on its own.
`accessModes` requests a volume supporting *every* mode listed, so
`[ReadWriteOnce, ReadWriteMany]` binds a volume many pods can mount read-write
and is left unconstrained — replicas above one and a rolling update are both
fine. `[ReadWriteOnce]` or `[ReadWriteOnce, ReadOnlyMany]` still constrain the
workload: neither makes the read-write mount shareable. The test is per claim,
so one shareable claim does not excuse a second `ReadWriteOnce`-only one on the
same component. `ReadWriteOncePod` never appears beside another mode — the API
forbids the combination and the parser rejects it — so it is always constraining.

That guard is one helper shared with `webservice` and `worker`, so reading the
whole set **changes what those two kinds build** for a pre-existing document
whose claim declares `[ReadWriteOnce, ReadWriteMany]`: `replicas: 2` used to be
a build error and now succeeds, and a single-replica document used to ship
`strategy.type: Recreate` and now ships no strategy at all, leaving the
apiserver's `RollingUpdate` default to apply. This is the one place in this
change where an existing document's output moves. It is deliberate and
one-directional — the correction only accepts documents that were being
refused, and only stops forcing a strategy the author never wrote — and it is
not scoped to the new kind, because scoping it would leave `webservice` and
`worker` refusing volumes Kubernetes shares happily. Both halves are pinned per
kind (`rwx_shared_helper_test.go`).

## StatefulSet-level and claim-template properties

The `statefulset` kind projects the whole `appsv1.StatefulSetSpec` field set it
owns, and each `volumeClaimTemplates` entry projects the whole
`corev1.PersistentVolumeClaimSpec`. Every field below is written only when
authored, so an unauthored StatefulSet emits no `podManagementPolicy` at all and
an empty `updateStrategy: {}` (a non-pointer struct, which `omitempty` does not
suppress), leaving the apiserver to default both. Previously kure's `CreateStatefulSet` wrote `podManagementPolicy: OrderedReady` into the
object itself; the release-1 builder contract makes that constructor
identity-only, so the line is gone from the emitted manifest. The *effective*
policy is unchanged — `OrderedReady` is also the API default — but the manifest
text moved, which is why three goldens lost a `podManagementPolicy:
OrderedReady` line. The claim entry's five
pre-existing keys (`name`, `mountPath`, `size`, `storageClass`, `accessModes`)
now sit inside a closed key set, so a typo in an entry is reported instead of
being silently dropped. `devicePath` joined that set.

Columns match the pod-level table above: **additive** means no document that
built before builds differently now; **behavior-changing** means an existing
document's meaning or acceptance moved.

| Property | Type | Effect | Kind |
|----------|------|--------|------|
| `podManagementPolicy` | enum | `OrderedReady` (also the apiserver's default for an unauthored StatefulSet, so authoring it is a no-op that only pins the value in the manifest) or `Parallel`. | additive |
| `updateStrategy{type, rollingUpdate{partition, maxUnavailable}}` | object | `type` is `RollingUpdate` or `OnDelete`; `rollingUpdate` is rejected under `OnDelete`, mirroring `ValidateStatefulSetSpec`. `type` is required only for an otherwise empty `updateStrategy: {}`, whose whole meaning would come from apiserver defaulting; when `rollingUpdate` is authored, `RollingUpdate` is inferred, since it is both the API default and the only type that reads the field. `partition` is `>= 0`. `maxUnavailable` takes a positive integer or a 1–100% string; the schema leaf is the `Types: [integer, string]` union, so a schema-validating consumer accepts both forms. | additive |
| `revisionHistoryLimit` | int ≥ 0 | Retained controller revisions. | additive |
| `minReadySeconds` | int ≥ 0 | Readiness settling time before a pod counts as available. | additive |
| `persistentVolumeClaimRetentionPolicy{whenDeleted, whenScaled}` | object | Each is `Retain` or `Delete`. | additive |
| `ordinals{start}` | object | `start` shifts the replica ordinal range and is required once `ordinals` is authored; `>= 0`. | additive |
| `selector` (claim) | object | `matchLabels`/`matchExpressions`, with the operator arity rule — `In`/`NotIn` need at least one value, `Exists`/`DoesNotExist` none — and every `values` entry validated as a label value, the check `ValidateLabelSelectorRequirement` runs on a newly created claim template. An entirely empty `selector` is rejected: the apiserver would accept it as matching every volume, which is never what an author who wrote the key meant. **Authoring a selector opts the claim out of dynamic provisioning**: a claim with a non-empty selector is never provisioned from its `StorageClass` and stays `Pending` until a pre-provisioned PV matches ([Kubernetes: persistent volumes — Selector](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#selector)). | additive |
| `resources{requests,limits}` (claim) | object | `storage` is the only accepted resource name — `ValidatePersistentVolumeClaimSpec` reads `requests[storage]` and nothing else, so any other name would be silently ignored. Quantities must be positive. `apply` *merges* `requests` onto the claim-template literal `createStatefulSet` already built (`statefulset.go`, which writes `requests.storage` from `size`), so `size` survives when only `limits` is authored. `requests.storage` is the long spelling of `size`; authoring both is an error. | additive |
| `volumeMode` (claim) | enum | `Filesystem` or `Block`. `Filesystem` pairs with `mountPath`; `Block` pairs with `devicePath` and renders a main-container `volumeDevices` entry instead of a `volumeMount` (see "Raw block volumes"). Previously `Block` was rejected, because every claim was mounted as a filesystem and the apiserver would have accepted the mismatched claim/pod pair. | additive |
| `devicePath` (claim) | string | Where a `volumeMode: Block` claim appears in the main container as a raw block device. Not a claim-spec field. Exactly one of `mountPath` and `devicePath` is required, and `devicePath` only with `volumeMode: Block`. | additive |
| `dataSourceRef{apiGroup,kind,name,namespace}` (claim) | object | Mirrors upstream `validateDataSourceRef`: `kind` and `name` are required non-empty with no format rule (a Kind is a CamelCase identifier, not a DNS name), `apiGroup` must be a DNS-1123 subdomain when non-empty, an omitted or empty `apiGroup` pins `kind` to `PersistentVolumeClaim` (the core group holds no other populator), and `namespace`, when set, is a DNS-1123 *label* (`ValidateNamespaceName`), not a subdomain. | additive |
| `volumeAttributesClassName` (claim) | string | DNS-1123 subdomain naming a `VolumeAttributesClass`. | additive |
| `storageClass` (claim) | string | **Behavior-changing.** A non-empty value is now validated as a DNS-1123 subdomain (`ValidateClassName`, the check `ValidatePersistentVolumeClaimSpec` runs and the one the `volumes[].pvc` path already applied). An invalid class name previously built a claim and was refused later by the apiserver; it is now refused here. A present-but-non-string value is likewise rejected rather than read as absent and provisioned through the cluster default class. | behavior-changing |
| `storageClass` (claim) — `""` and the `pvc` capability | string | **Pre-GA output change**. An authored `storageClass: ""` now requests no class: the claim template carries `storageClassName: ""`, as a `volumes[].pvc` claim and the `persistentvolumeclaim` kind do. It used to read as absent, so the template carried no class and the cluster's default applied. An unauthored entry (absent or `null`) now takes the ClusterProfile `pvc` capability's `storageClassName`, as an authored `pvc` trait does: the handler implements `ComponentCapabilityFiller`, reads the binding through `LoweringContext.Capability` once the component has an entry, so `pvc` is in `ConsumedCapabilities` exactly then, and fills each unauthored entry. An authored class, `""` included, wins; a non-string rendering value is refused. Either change alters an existing StatefulSet's `volumeClaimTemplates`, which the apiserver refuses to update: the StatefulSet fails to apply until it is recreated (the `force-replace` trait recreates it on apply, restarting its pods). The claims it already made keep their class, unless the retention policy deletes them with it. | behavior-changing |
| `size` (claim) | quantity | **Behavior-changing.** `size` is now the short spelling of `resources.requests.storage` rather than a key of its own, and it is no longer unconditionally required — either spelling satisfies the requirement, and authoring both is an error. It must also now be **positive**: `0` was accepted before and is rejected now, matching upstream's `ValidatePositiveQuantityValue` on `requests[storage]`. (The `volumes[].pvc.size` surface on every kind applies the same positivity rule.) Because "authored" is now read through `parseStringField`, an explicitly empty `size: ""` no longer counts as authoring the short spelling, so pairing it with `resources.requests.storage` is accepted rather than rejected as a double spelling. | behavior-changing |
| any unrecognized key on a claim entry | — | **Behavior-changing.** A `volumeClaimTemplates` entry is now a closed key set. The previous parser called no `rejectUnknownKeys` at all on the entry map, so a misspelled `storageClassName` was dropped and the claim was provisioned through the cluster default class with nothing said. Such a document built before and errors now, which is a break of the additive test in `docs/oam/design-gvk.md` under an unchanged `launcher.gokure.dev/v1alpha1`. It is taken deliberately and on precedent: `6aed090` (`feat(format): enforce policy and support securityContext on init/sidecar containers`) shipped the same tightening on init/sidecar container entries. The `format` commit scope is what signals it — `cliff.toml` groups those commits under a **Document Format** changelog heading. | behavior-changing |
| any optional field above, authored as null | — | **Not a wrong type — absence.** Every optional StatefulSet-level and claim-level field added here reads an explicit null as omission, so `updateStrategy:` with nothing after it builds exactly as omitting the key does. This matters beyond hand-written YAML: a lowering rule may emit a nil, `pkg/oam`'s emission validation accepts it ("a nil under an optional field constrains nothing"), and a parser that answered "must be an object, got `<nil>`" would let a component satisfy the published schema and then fail conversion. Where the null lands on a field that is *required* once its parent is authored (`ordinals.start`, a claim's `name`), it surfaces as the requiredness error, never as a type error. This covers the optional lists as well as the scalars and objects (`selector.matchExpressions`, a match expression's `values`), and it covers a **typed** nil — `map[string]any(nil)` or `[]any(nil)` inside an `any`, which is what a lowering rule assembled in Go produces for an unset optional and is not `== nil` — because `pkg/oam`'s `isNullValue` classifies those as null too. A key the schema rejects outright is unaffected: authoring it as null still earns the explanatory refusal rather than silence. The lifecycle and probe handler keys, where presence itself is the rule being enforced, refuse a null; a container's resource quantities under any name and the top-level keys once read with a bare lookup (listed with the `null` row of the `deployment` table above) read it as omission. | additive |
| `volumeClaimTemplates` itself — wrong type | list | **Behavior-changing.** The previous parser answered "is it present" and "is it a list" with one type assertion, so `volumeClaimTemplates: {name: data}` or a stray scalar was indistinguishable from an absent key: the StatefulSet was built with no claim templates and nothing was reported. A present-but-non-list value is now rejected naming the type received. **An explicit null is absence, not a wrong type** — `volumeClaimTemplates:` with no value decodes to nil, the key is optional in the schema, and `pkg/oam`'s own requiredness check (`isNullValue`) already reads nil as absent, so a document written that way is valid `launcher.gokure.dev/v1alpha1` today and keeps building. | behavior-changing |
| `name`, `mountPath` (claim) — collisions | string | **Behavior-changing.** A claim template's `name` must differ from every other claim template's and every `volumes` entry's, and its `mountPath` from every other main-container `mountPath`, claim templates and `volumes` together. Each parser used to check only its own entries, and the apiserver does not catch the overlap: StatefulSet validation skips the pod template's volumes. The controller keys claim templates by name, so of two sharing one only one claim was created, and it replaced a pod volume named like a template with that template's claim, so the authored volume was never mounted. A repeated `mountPath` built and then failed pod creation. Such documents now fail at build time. The `configmap` and `external-secret` traits refuse a volume named like a claim template for the same reason. | behavior-changing |
| `name`, `mountPath`, `size` (claim) — wrong type | string | **Behavior-changing.** All three previously read through a bare type assertion, so a present-but-non-string value (YAML parses `size: 10e9` as a float and `mountPath: 7` as an int) collapsed to the empty string and was reported as the *requiredness* error — "missing required field 'size'" for a field the author had written. Each now rejects with an explicit type error naming the field and the type received. Acceptance does not widen: every one of these documents failed before and fails now, but with a message that points at the real defect. | behavior-changing |

Deliberately **not** accepted — each is rejected with an error naming the
reason rather than silently ignored:

- `spec.selector` (StatefulSet-level) — the builder derives it from the
  component name, and a StatefulSet's selector is immutable after creation.
- `spec.template` (StatefulSet-level) — the pod template
  is projected from the component's own container and pod-level properties, as
  on `deployment`. The parser dropped an authored one before.
- `volumeName` (claim) — pre-binding a claim *template* to one named
  PersistentVolume would point every replica at the same volume.
- `volumeMount` (claim) — not a claim-spec field at all; the container mount
  is authored as `mountPath` (or, for a `volumeMode: Block` claim,
  `devicePath`) on the same entry.
- `dataSource` (claim) — when `dataSourceRef` carries no `namespace` the
  apiserver mirrors it into the superseded `dataSource` field, so authoring
  both is redundant; when it does carry a `namespace` the apiserver does not
  mirror, and `dataSource` must stay empty. Either way the field is authored
  through `dataSourceRef` alone.

The three claim-level refusals sit inside a `volumeClaimTemplates` entry, so
their reason reaches a caller of the handler; in a document the
authored-property check refuses the key first, with its own text and no reason
(see ["Upstream fields a hand-parsed kind does not read"](upstream-fields-not-read.md#upstream-fields-a-hand-parsed-kind-does-not-read)).
