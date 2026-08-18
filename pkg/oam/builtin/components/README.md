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
share these fields, projected as a full-fidelity subset of `corev1.Container`/
`PodSpec` rather than a hand-rolled parallel schema: `image` (validated — no
untagged/`latest`), `env` (`value` or `valueFrom` — mutually exclusive, matching
`corev1.EnvVar`'s own doc comment ("cannot be used if value is not empty");
`valueFrom` is one of `secretKeyRef`, `configMapKeyRef` (both accept
`optional`), `fieldRef`, `resourceFieldRef` — mutually exclusive among
themselves too), `envFrom` (bulk-import a ConfigMap's or Secret's keys, with
`prefix`), `resources` (requests/limits, defaults 100m/128Mi for `cpu`/`memory`;
any other resource name, e.g. `ephemeral-storage` or `nvidia.com/gpu`,
round-trips unmodified — no policy default/max hook exists for those today),
`command`/`args`, `probes` (httpGet/tcpSocket/exec/grpc), `lifecycle`
(`postStart`/`preStop`: `exec`/`httpGet` (including `httpHeaders`)/`sleep` —
`tcpSocket` is not accepted, since corev1 documents it as broken for lifecycle
hooks), `securityContext` (per-container:
`runAsUser`/`runAsGroup`/`runAsNonRoot`, `readOnlyRootFilesystem`,
`allowPrivilegeEscalation`, `privileged`, `capabilities.{add,drop}`,
`seccompProfile`, `seLinuxOptions`, `appArmorProfile`; `windowsOptions` and
`procMount` are deliberately not covered — this project targets Linux-only
podman/distroless images (see the workspace-root `meta/CLAUDE.md`), and
`procMount` is an alpha, rarely-used field with no precedent elsewhere in this
package), `workingDir`, `volumes`, `initContainers`, `sidecars`, and `affinity`.

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

`env`, `envFrom`, `resources`, `lifecycle`, `securityContext`, and `workingDir`
are each schema fragments parameterized by a `reserved bool` (mirroring
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
