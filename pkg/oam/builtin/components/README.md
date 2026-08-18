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
| `cronjob` | CronJob, SA | Scheduled job; cron `schedule` + history limits. |
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
`secretKeyRef`, `configMapKeyRef` (both accept `optional`), `fieldRef`
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
matching admission's own zero-value defaulting),
`fileKeyRef` (`volumeName`/`path`/`key` required, `optional` accepted;
corev1's `EnvFiles` feature; `path` must be relative and must not contain a
`..` backstep component, per this repo's own path-safety convention) —
mutually exclusive among themselves too), `envFrom` (bulk-import a ConfigMap's
or Secret's keys, with `prefix` — any printable ASCII character except `=`,
matching `corev1.EnvFromSource.Prefix`'s own field doc comment; only the final
prefix+key concatenation need be a valid env var name, not the prefix alone;
`configMapRef.name`/`secretRef.name` must each be a valid DNS-1123 subdomain,
matching how every Kubernetes object name is validated),
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
round-tripped unmodified.
Every quantity must be non-negative; `cpu`/`memory`/`storage`/
`ephemeral-storage` may be fractional, but any other (extended) resource name
must be a whole number, matching Kubernetes' own extended-resource
constraint. No policy default/max hook exists for names other than cpu/memory
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
(httpGet/tcpSocket/exec/grpc), `lifecycle` (`postStart`/`preStop`:
`exec` (every `command` element must be a string; a non-string element is
rejected, not silently dropped)/`httpGet` (including `httpHeaders`)/`sleep` —
`tcpSocket` is not
accepted, since corev1 documents it as broken for lifecycle hooks),
`securityContext` (per-container: `runAsUser`/`runAsGroup`/`runAsNonRoot`,
`readOnlyRootFilesystem`, `allowPrivilegeEscalation`, `privileged`,
`capabilities.{add,drop}`, `seccompProfile`, `seLinuxOptions`,
`appArmorProfile`, `procMount` (`Default`|`Unmasked`); `windowsOptions` is
deliberately not covered — this project's own container images are
Linux-only (distroless base images run under podman), so a Windows-specific
security context has no target to apply to here, and `procMount` is
Linux-specific by definition, so excluding `windowsOptions` does not extend to
it), `workingDir`, `volumes`, `initContainers`, `sidecars`, and `affinity`.

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
