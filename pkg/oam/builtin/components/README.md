# OAM Built-in Component Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/components.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)

Package `components` implements `oam.ComponentHandler` for the built-in component
types. Each handler parses a typed config from a component's `properties` and
produces the corresponding Kubernetes resources via kure's builders. Handlers are
registered with the transformer in `pkg/cmd/kurel` (`newBuiltinTransformer`), each
mapping a component `type` string to a handler implementing `CanHandle` +
`ToApplicationConfig`. Handlers may also implement `oam.PropertySchemaProvider`
(`PropertySchema()`) to declare a constrained schema for their properties so crane can
validate them before invocation — the `passthrough` handler is a worked example.

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
share these fields: `image` (validated — no untagged/`latest`), `env` (with
`valueFrom` secret/configMap refs), `resources` (requests/limits, defaults
100m/128Mi), `command`/`args`, `probes` (httpGet/tcpSocket/exec/grpc),
`volumes`, `initContainers`, `sidecars`, and `affinity`.

## Per-type highlights

- **webservice / worker** — `image`, `replicas` (default 1), `port` (webservice).
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
- **postgresql** — `provider: cnpg`, `version` (default `16`), `storageSize`,
  `replicas`, `backup.*`, `monitoring.enabled`, `pooler.enabled`, `managedRoles`,
  `databases`.
- **passthrough** — `object` (full apiVersion/kind/metadata/spec), `clusterScoped`.
- **crd / manifests** — `inline` xor `url`; `manifests` adds `scopeOverrides`
  (`apiVersion`/`kind`/`scope`) for unknown kinds.

## Extending

Custom component types implement `oam.ComponentHandler` (`CanHandle` +
`ToApplicationConfig`) and are registered alongside the built-ins. Exported helpers:
`ValidateImageRef` (image policy) and `BuildPVC` (PVC from a `PVCConfig`).

See [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
for the full type/field reference, the [OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the handler interfaces, and `examples/` for runnable applications.
