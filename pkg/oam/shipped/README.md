# Shipped definitions and models

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/shipped.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/shipped)

The component definitions and models that launcher ships inside the `kurel` binary
(go-kure/launcher#1735). `kurel build` registers them before it reads an application's own
`definitions/` and `models/`, so an application authors a shipped type with no file of its own.

```go
set, err := shipped.Load(transformer) // registers the embedded set; refuses a file that does not load
err = set.Clash(h.TypeName(), h.Identity()) // refuses an application's type that is shipped
```

## What ships

| Type | Emits | CRD |
|---|---|---|
| `vm-rule` | `VMRule` (`operator.victoriametrics.com/v1beta1`) | VictoriaMetrics operator release v0.76.0 |
| `vm-service-scrape` | `VMServiceScrape` | the same release |
| `vm-pod-scrape` | `VMPodScrape` | the same release |

A model names such a kind by its identity, `<kind>.<group>@<version>`
(`vmrule.operator.victoriametrics.com@v1beta1`).

| Model | Type | What it writes |
|---|---|---|
| `workload` | `workload.launcher.gokure.dev@v1`, short name `workload` | One workload in the mode `service`, `worker` or `job`, with an optional volume, exposure, database, monitoring and backup. See "The workload model". |

## Where it lives and how it loads

`content/definitions/*.yaml` are `ComponentDefinition` documents, and the CRD each pins sits in
`content/definitions/crds/`, one YAML document per file. `content/models/*.yaml` are `Model`
documents. Definitions load first, then models, each in file name order, so a shipped model may name
a shipped definition's type. The files load through the same loaders as an application's own, so a
CRD whose SHA-256 differs from its definition's pin refuses the load, and a refusal names the file.
A missing `content/models` directory holds no model.

## A shipped type is never replaced

`Set.Clash` refuses an application's definition that has the same authored type name as a shipped
definition, an application's model whose short name is one, and either that has the same identity as
a shipped definition or model. The refusal names the
type and the shipped file, and `kurel build` adds the application's file. Another version of a
shipped kind is another type and is not a clash. There is no override.

## The workload model

`content/models/workload.yaml` is one model of type `workload.launcher.gokure.dev@v1`
(go-kure/launcher#1737), written `type: workload@v1` in an application. A launcher-domain name takes
a `v<semver>` version, which `v1` is (`pkg/oam/typeid.go`). An application needs no `models/`
directory for it.

```yaml
components:
  - name: orders
    type: workload@v1
    properties:
      image: registry.example.org/orders:1.0.0
      port: 8080
      volume: {size: 1Gi, mountPath: /data}
      monitoring: {path: /metrics}
```

| Input | Type and default | What it does |
|---|---|---|
| `mode` | `service` (default), `worker` or `job` | Picks the workload: `webservice` (Deployment, Service, ServiceAccount), `worker` (Deployment, ServiceAccount) or `job` (a Job, `restartPolicy: OnFailure`). Another word is refused. |
| `image` | string, required | The container image, in every mode. |
| `command`, `args` | lists of strings | The container's command and arguments. |
| `env` | list of `{name, value, valueFrom{secretKeyRef, configMapKeyRef}}` | The container's environment. With `database` set, `DATABASE_URI` is added from the `uri` key of the database's application Secret. |
| `replicas` | integer, 1 | `service` and `worker`. |
| `port` | integer, 8080 | `service`: the container port and the Service port. |
| `volume` | `{size, mountPath, backup{schedule, repository}}` | Writes a claim named `<component>-data` and mounts it at `mountPath` in every mode. `backup` adds a `volsync` ReplicationSource for the claim; `repository` is the name of the restic Secret, which is referenced and never built. |
| `exposure` | `{hostnames}` | `service` only. Carries the `expose` trait on the Service, so the ClusterProfile's `expose` capability picks an Ingress or an HTTPRoute. |
| `database` | `{storageSize, backup}` | Writes a `postgresql` component. `backup` has the shape of that component's `backup` property (an S3 `objectStore`, `retentionPolicy`, `schedules`) and writes its object store and one ScheduledBackup for each schedule. |
| `monitoring` | `{port, path, interval, rules, scrapeName, ruleName}` | The property shape of the `monitoring` trait. Writes a `VMServiceScrape` in `service` mode, a `VMPodScrape` in `worker` and `job` mode, and a `VMRule` when `rules` is set. |

Each optional input is a part of the model that is emitted only when the input is set; a build
with `image` alone writes the workload and nothing else. `port` of `monitoring` is a port name or
a number: in `service` mode it defaults to the Service's `http` port, a number is written as
`targetPort`; in `worker` and `job` mode it is required and is a number, written as `portNumber`:
the model writes no container port in those modes, so a port name would name nothing.

**What the model format cannot express, and what the model does instead.**

- A trait cannot be conditional. The volume backup is the `volsync` trait on a part of its own, a
  `serviceaccount` that exists to carry it, so a volume backup writes one extra ServiceAccount,
  `<component>-backup`. The `expose` trait must sit on the component that owns the Service, so the
  `service` mode has two parts with one property list, one with the trait and one without.
- A part's type cannot depend on an input, so each mode is a part, and a read of a part's outputs
  repeats that part's condition. The `DATABASE_URI` environment entry is written in each of the
  workload parts, because a model value cannot read a part.
- An input object must declare every key it takes. `env`, `monitoring.rules` and `database.backup`
  declare theirs, and a key they do not declare is refused.
- A `null` is written to a generic-route object, and the CRD refuses it. So `path` and `interval`
  are written as `/metrics` and `30s` when unset. The `monitoring` trait leaves both out and lets
  VictoriaMetrics' defaults apply.
- A part is named `<component>-<part name>`, and `objectName` is refused on it. So `scrapeName` and
  `ruleName` are the suffix of the object name (defaults `<component>-scrape` and
  `<component>-rules`), where the trait uses them as the whole name.
- A model cannot refuse an input combination. `exposure` and `port` are read in `service` mode only and
  are not read in another. `monitoring.port` is required in `worker` and `job` mode; without it the
  build fails with `no such key: port` and names the part.
- The scrape selects the `app` label of the workload by the name of its Deployment (the Job's name in
  `job` mode). A `Naming` hook that renames the Deployment and not the label breaks the match.

**Not covered.** Probes, resources, a security context, sidecars and scaling are not inputs; the
`monitoring` objects are held to the object kind policy as the trait's are, so a policy that does not
allow cluster-scoped objects refuses them (go-kure/launcher#1744). Restarting the workload when a
config changes stays Go. There is no model per mode.

## Where the CRDs come from

A CRD is taken from a release asset, never a branch. The three files are documents of
`https://github.com/VictoriaMetrics/operator/releases/download/v0.76.0/crd.yaml` (6101924 bytes,
sha256 `9362fcf82df73584aa6d97651090be324475345e8e48495eedf810557f00af84`, the digest the release
lists), split with `yq` 4.54.3:

```sh
yq eval 'select(.metadata.name == "vmrules.operator.victoriametrics.com")' crd.yaml > content/definitions/crds/vmrules.yaml
```

The same command, with `vmpodscrapes` and `vmservicescrapes`, makes the other two. Each definition
pins its split file's digest. To move to another release, download that release's `crd.yaml`, split
the three files again and put the new digests in the definitions.

Only a CRD the loader accepts can ship. It refuses one whose `spec` is open (`VMAuth` and two other
kinds), so the instance kinds are not shipped.

## Not done

- The CRDs' CEL rules are not evaluated (see [Component definitions](https://github.com/go-kure/launcher/blob/main/pkg/oam/definition/README.md)).
- The whole set registers on every build, used or not.
