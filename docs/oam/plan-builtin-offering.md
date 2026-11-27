# Built-in offering plan (draft)

**Status: draft.** Nothing on this page is scheduled, and nothing is built. Where Serge has answered, the page says "decided" with the date and, where it matters, his words. Every other "include", "combine" and "not" is a **proposal** for Serge, with its reason. What this page cannot settle is listed at the end as open questions.

This page builds on the [Conclusion of the Helm chart pattern survey](https://github.com/go-kure/launcher/blob/main/docs/oam/research-helm-chart-patterns.md#conclusion) (go-kure/launcher#1637). It does not repeat the survey's counts; follow the links there for the evidence.

## The words used

Read [Model and Layers](https://github.com/go-kure/launcher/blob/main/docs/oam/model-and-layers.md) first for components, traits and lowering. The page uses these terms.

| Term | Meaning | Where it is defined |
|---|---|---|
| Built-in type | A component or trait type that launcher ships with Go code: a handler that builds the object. | [components README](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/components/README.md), [traits README](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/traits/README.md) |
| Kind type | A built-in type that writes exactly one Kubernetes kind with no launcher opinions, such as `servicemonitor` or `cnpg-cluster`. | [Operator custom-resource components](https://github.com/go-kure/launcher/blob/main/docs/oam/design-operator-cr-components.md) |
| Go lowering rule | Go code that expands a high-level type into lower-level types, such as `webservice`, `postgresql` or the `expose` trait. | [Lowering Engine](https://github.com/go-kure/launcher/blob/main/docs/oam/design-lowering-engine.md) |
| Configuration model | A YAML document (`kind: Model`) that declares inputs and computes parts with CEL. It adds a component type with no Go. | [pkg/oam/model README](https://github.com/go-kure/launcher/blob/main/pkg/oam/model/README.md) |
| Generic route | A `ComponentDefinition` over a pinned CRD, which makes a kind available with no handler (go-kure/launcher#1712, merged). | go-kure/launcher#102 |
| Part | One entry of a model: a component or a trait that the model expands to. | model README |

A configuration model defines a **component** type. The model README says a trait on a component of a model type is refused. So a configuration-defined **trait** type does not exist today (see the open questions).

## What the page proposes, in one table

"Include" means launcher ships it built in. "By hand" means a user writes the kind through the generic route, and launcher ships nothing for it.

| Candidate | Proposal | Shape | Reason |
|---|---|---|---|
| Workload model: a workload with optional volume, exposure, database, monitoring and backup | Include | Configuration model, with one `mode` input (service, worker or job; decided 2026-11-27, see Models) | It is the most frequent combination that no single type gives (survey, Candidate models). One model with parts switched off replaces a monitored-web-service model and a stateful-web-app model. |
| Generic monitoring part: a scrape and a rule | Include | Both a part of the workload model and a Go trait (decided 2026-11-27, see Traits) | It serves the most widespread use case in the survey's charts. It lowers to VictoriaMetrics kinds only. |
| Generic backup part | Include | Lowers to `cnpg-scheduledbackup` for a database and to VolSync for a volume | Decided on 2026-11-27 (see Backups). It needs no new kind. |
| S3 storage: create a bucket, and consume one | Include, as a base offering (decided 2026-11-27) | Create: backend specific. Consume: one configuration model for every S3 | Two separate concerns, researched on 2026-11-28 (see S3 as a base offering). |
| Data store model | Include, low priority | Configuration model | An operator type is the better route where one exists, so the value is where none does. |
| Operator install model | Not for now | | `helm` already delivers most of it, so the value is low (survey, Conclusion). The per-namespace grant needs Go. |
| Many-service application | Not | | It is the worked example of what the mechanism must build. A user writes a model for it. |
| Generic dashboard part or trait (Grafana, Perses) | Not | | The content of the dashboards is too different (Serge, 2026-11-27). The kinds stay by hand. |
| Prometheus instance kinds (`Prometheus`, `Alertmanager`, `ThanosRuler`; `PrometheusAgent` has no built-in type) | The three built-in types stay as hand-written types (decided 2026-11-27) | Built-in types, as today | Launcher still has no native Prometheus backend and no part lowers to them (Serge, 2026-11-27). |
| Velero `Backup`, `Schedule` and the location kinds | By hand only | Generic route | Velero is not a default backup target (decided 2026-11-27). |
| EMQX | Not | | Serge's reason: it is no longer open source. The licence was not researched. |

## Base kinds, models and traits

### Base kinds (built-in types)

- **Proposal:** a kind type or a generic-route definition for each VictoriaMetrics kind the generic monitoring part lowers to: `VMServiceScrape`, `VMPodScrape` and `VMRule`. They have no built-in type today. A generic-route definition is enough for a model to write them (see "What a model part can write").
- **Proposal:** the other VictoriaMetrics scrape and configuration kinds (`VMProbe`, `VMNodeScrape`, `VMStaticScrape`, `VMScrapeConfig`, `VMAlertmanagerConfig`) go by hand through the generic route, because no part lowers to them.
- **Proposal:** the VictoriaMetrics instance kinds (`VMAgent`, `VMAlert`, `VMAlertmanager`, `VMSingle`, `VMCluster`, `VMAuth` and the log and trace families) run pods from settings in the resource. They wait for the pod policy of go-kure/launcher#1705, or a Go type. They are not part of the generic monitoring part (see below).
- Nothing is proposed as a built-in type for Grafana, Perses, Forecastle, Velero or the low-priority kinds of the survey's list. They are by hand.

### Models: Go lowering rules and configuration models

A configuration model expresses what CEL and the model format can: inputs and defaults, values computed from inputs, parts switched on by `when`, one part per list entry with `forEach`, names read from earlier parts, and nested models. The survey's verdicts say what stays Go: the hash behind a restart on a config change, an order between two parts of one tier and hook phases, a config file serialised from structured settings, and a grant into each watched namespace.

| Proposal | Why it fits that shape |
|---|---|
| The workload model is a configuration model with one `mode` input (service, worker or job). | Its parts are existing types switched on by inputs. Restart on a config change can use the text in a pod annotation for a ConfigMap the model builds. The hash itself stays Go, so this part of the behaviour is partial. |
| The generic monitoring part is a configuration model part, and also a Go trait. | A scrape and a rule are built from inputs (selector, port, path, interval, expression). Decided 2026-11-27: both forms exist (see Traits). |
| The generic backup part is a configuration model part. | It only maps what to back up onto two existing types, and a model part can carry the `volsync` trait (see "What a model part can write" and Backups). |
| The S3 consume part is a configuration model. | It writes a ConfigMap and an external secret or Secret from inputs, with a format switch (see S3 as a base offering). Creating a bucket is backend specific and is not a part of this model. |

**The workload model's shape, decided 2026-11-27.** Serge, typed and relayed 2026-11-27 23:42:27 CET: "(1) One workload model with a mode input / if we want the simpler model, the one model per mode can be built on top of 1". So one workload model with a `mode` input (service, worker or job) is the base. A model per mode, if one is wanted, is a thin model built on top of it.

Existing Go lowering rules (`webservice`, `worker`, `helm`, `postgresql`, `oci`) are not taken as settled by the survey, and this page does not settle them.

### Traits

A trait adds a capability to a component. Today the built-in traits are Go handlers, and `expose` is a Go lowering rule that picks an `ingress` or an `httproute` from the cluster profile.

- **Decided 2026-11-27:** the generic monitoring part (`VMServiceScrape`, `VMPodScrape`, `VMRule`) lives in both places. Serge, typed and relayed 2026-11-27 23:05 CET: "3. Both." It is a part of the workload model, and it is also a Go trait that a plain `webservice` or `worker` can take, because a model cannot carry a trait on its own component. As a trait it is Go code today.
- **Proposal:** no S3 trait. S3 is a consume model, with creation backend specific (see S3 as a base offering).
- **Proposal:** no dashboard trait (see above).
- A configuration-defined trait type is not proposed here, and Serge's answer above does not address it. It would need a mechanism that go-kure/launcher#102 has to design. See the open questions.

### What a model part can write

Read from the code and tried with a model in a local test on 2026-11-27.

- **A kind that only a generic-route definition declares: yes.** A model part names its type, and that type must be declared when the model registers. Registering a definition declares the kind's identity (`<kind>.<group>@<version>`, such as `vmrule.operator.victoriametrics.com@v1beta1`) so that a model can name it. `kurel build` loads definitions before models. A model whose part named such a kind built the object in the test, and the same model with no definition was refused at registration. So the generic monitoring part needs no built-in type for `VMServiceScrape`, `VMPodScrape` or `VMRule`.
  - The test used the CRDs of the VictoriaMetrics operator as they stand on its default branch on 2026-11-27, not a release. The loader accepts the CRDs of all three kinds, and a model with a scrape part and a rule part built a `VMServiceScrape` and a `VMRule`. It refuses `VMAuth`, because that CRD leaves its `spec` open. That agrees with the proposal that the instance kinds wait.
  - Not tried: the CRDs' own CEL rules, which the loader does not evaluate, and a CRD pinned to a release.
- **Not settled: how a definition and a model reach a user.** launcher loads definitions only from the application's `definitions/` directory, and models only from its `models/` directory and `--model` files. Nothing is embedded in the binary. So every "Include" row that is a model, and the pinned CRDs a monitoring part needs, wait for a delivery route. That is an open question (see the open questions).
- **The `volsync` trait from a model part: yes.** See Backups.

#### Sources for these answers

Read at commit `ed8e2365` on 2026-11-27; the line numbers belong to that commit.

- A part's type must be declared when the model registers: `pkg/oam/model/registry.go:66-69`.
- A definition declares `<kind>.<group>@<version>` so a model can name it: `pkg/oam/definition/definition.go:203` and `:333-371`, and `pkg/oam/definition/README.md:24`.
- Definitions load before models: `pkg/cmd/kurel/build.go:155-163`.
- Where definitions and models load from: `pkg/cmd/kurel/definitions.go:45-49`, `pkg/cmd/kurel/models.go:34-40`, `pkg/cmd/kurel/README.md:303` and `:394-398`. No Go file in the repository has a `go:embed` or an `embed.FS` (searched on 2026-11-27).
- A CRD with an open `spec` is refused: `pkg/oam/definition/definition.go:196-199`.
- A part carries traits: `pkg/oam/model/types.go:62-77`, `pkg/oam/model/expand.go:420-425`, `pkg/oam/model/registry.go:77-81`, and `pkg/oam/model/rule.go:39-43` (the trait is checked like an authored one). A part reads only an earlier part: `pkg/oam/model/README.md:65`. A trait on a component of a model type is refused: `pkg/oam/model/traits.go:8-26`.
- The `volsync` trait: `pkg/oam/builtin/traits/volsync.go:34-43` (properties), `:90-103` (`sourcePVC` and `schedule` required), `:114` (default Secret name), `:225-266` (the `ReplicationSource`). The Secret is referenced and never built: `pkg/oam/builtin/traits/README.md`, the `volsync` row of the name table.

Local experiments, run with `go test` in `pkg/oam/model` and not committed:

1. The definition `examples/custom-component/definitions/batchjob.yaml` and a model with one part of its type built a `BatchJob`. The same model with no definition was refused at registration with "no registered type is named".
2. The operator's CRD bundle was split into one file per kind and each file was pinned by digest in a generated definition. The loader accepted 24 of the 27 kinds. It refused `VMAuth`, as stated above, and two more kinds that also leave their `spec` open. A model with a scrape part and a rule part over two of the definitions built a `VMServiceScrape` and a `VMRule`.
3. A model with a `persistentvolumeclaim` part and a later `deployment.apps` part carrying `volsync` built the claim and its `ReplicationSource`. The same trait on the claim's own part was refused at registration. A claim part with its own `name` was built as `orders-orders-data`, so a `sourcePVC` of `orders-data` named no claim.

No assumption is left except the source of the CRDs: the operator's default branch, not a release.

## Monitoring in particular

### The rule

Serge, 2026-11-27: VictoriaMetrics is the supported backend. Prometheus Operator objects are considered only where the VictoriaMetrics operator accepts them, for example scrape objects it converts and that can work with VictoriaMetrics. launcher supports no native Prometheus backend and has no intention to.

### Which objects exist

| Group | VictoriaMetrics kinds | Prometheus Operator counterpart the VictoriaMetrics operator converts |
|---|---|---|
| Scrape | `VMServiceScrape`, `VMPodScrape`, `VMScrapeConfig`, `VMProbe`; `VMNodeScrape` and `VMStaticScrape` have no counterpart and are never created by conversion | `ServiceMonitor`, `PodMonitor`, `ScrapeConfig`, `Probe` |
| Rule | `VMRule` | `PrometheusRule` |
| Alerting configuration | `VMAlertmanagerConfig` | `AlertmanagerConfig` (named by the VictoriaMetrics page's intro; the page's default list does not name it) |
| Instance (runs pods) | `VMAgent`, `VMAlert`, `VMAlertmanager`, `VMSingle`, `VMCluster`, `VMAuth` | none named by the page: `Prometheus`, `Alertmanager`, `ThanosRuler` and `PrometheusAgent` are not converted |
| Dashboard | none (a Grafana or Perses kind, or a labelled ConfigMap) | none |

Source: the VictoriaMetrics operator's page on Prometheus integration, read on 2026-11-27. By default the operator converts `ServiceMonitor`, `PodMonitor`, `PrometheusRule`, `Probe` and `ScrapeConfig`; the page does not say whether `AlertmanagerConfig` conversion is on by default. The operator does not ship the Prometheus Operator CRDs, so they must be installed for the conversion to work.

### What combines and what does not (proposals)

| Combine or not | Proposal | Reason |
|---|---|---|
| Scrape and rule, in one generic part | Combine | They attach to one workload and share its selector and port. One part says "watch this, alert on that". |
| Scrape and rule with an instance kind | Do not combine | An instance kind runs pods and needs the pod policy of go-kure/launcher#1705. A workload does not own the cluster's monitoring stack. |
| The generic part with the alerting configuration | Do not combine | Alert routing is the cluster's policy, not application content. It stays a kind by hand. |
| The generic part with dashboards | Do not combine | The content is too different to share a form. |
| The generic part with the converted Prometheus Operator kinds | Do not choose between them | The part writes VictoriaMetrics kinds. A user who writes `ServiceMonitor` by hand gets conversion only if the conversion is on and the CRDs are installed. |

### What the existing Prometheus Operator types become

The components README lists 7 built-in Prometheus Operator types: `alertmanager`, `podmonitor`, `prometheus`, `prometheus-probe`, `prometheusrule`, `servicemonitor` and `thanosruler`. Under the rule, the four whose kinds the VictoriaMetrics operator converts (`podmonitor`, `prometheus-probe`, `prometheusrule`, `servicemonitor`) stay as kinds a user writes by hand. The other three (`alertmanager`, `prometheus`, `thanosruler`) are instance kinds.

**Decided 2026-11-27.** Serge, typed and relayed 2026-11-27 23:01 CET: "1. Keep all seven. Users are still able to use them if they like". So all seven stay as hand-written built-in types, the three instance kinds included. Launcher still has no native Prometheus backend, and no part lowers to any of them.

## Backups, as decided

Decided on 2026-11-27 (KL-backup-default-1, answered 2026-11-27 18:24 CET): a generic backup part lowers to CloudNativePG for a database and to VolSync for a volume. Both are built in, so no new kind is needed. Velero is not a default target, and its kinds stay by hand through the generic route. Backup of objects is not needed where every object comes from git, and a whole-cluster (etcd) backup belongs to the cluster platform. The reasons are on the survey page, section Monitoring and backup, generically.

**The volume backup can be written from a model, with no Go lowering rule.** A model part can carry traits, and a trait a model writes is checked like one written in a document. In a local test (2026-11-27), a model with a `persistentvolumeclaim` part, followed by a `deployment.apps` part that carries the `volsync` trait with `sourcePVC: ${ parts.claim.name }` and a `schedule`, built the claim and a `ReplicationSource` for it. Three rules for such a part:

1. Put the trait on a part that comes after the claim's part. A part reads only an earlier part, so a trait on the claim's own part cannot read the claim's name. The model is refused when it registers.
2. Read the claim's name from `parts.<id>.name`. The claim is named `<component>-<part name>`, so the bare part name (or a name the part sets itself) leaves out the component prefix and points at no claim. A full name typed by hand matches today and breaks with the naming.
3. The restic repository Secret is referenced and never built. The part takes its name as an input, or the model builds the Secret as a part. Its default name comes from the component that carries the trait.

The limit stated above still holds: a trait on a component of a model type is refused, so the backup is a part of the model and not a trait on the model's own component.

## S3 as a base offering

**Decided 2026-11-27** (Serge, typed and relayed: his design input at 23:21 CET, his answer at 23:35 CET). S3 is a base offering, and it is two separate concerns. His design input:

> We should make the distinction between / - creating a bucket (which makes sense for the selfhosted garage, but not for 3rd party buckets, unless there exists some operator that does that) / - consuming a bucket (define a configmap and or secret with how to use a bucket, possibly in different forms depending on what the consuming application needs); s3 is s3 and access and auth is the same regardless of which s3 it is, so a generic form does not make any more sense here / if we use some garage operator, both can probably be manged by the operator

His answer, to the reading of that input and to the research: "1. yes your reading is right / 2. yes research / creating what is needed to consume a bucket (cm, external-secret) could be a higher level model that creates those, with an optional switch of what format if should have".

So:

- **Create** is backend specific. It makes sense for a self-hosted Garage, and for a provider only where an operator creates buckets. It is not a generic part.
- **Consume** has one form for every S3: a configuration model that writes a ConfigMap and an external secret or Secret, with an optional switch for the output format.

Do not confuse the `bucket` component with either. It is Flux's `source.toolkit.fluxcd.io` Bucket, a reader of a bucket that already exists, and it creates nothing ([components README](https://github.com/go-kure/launcher/blob/main/pkg/oam/builtin/components/README.md), the `bucket` row).

### Create, self-hosted: Garage

Garage (Deuxfleurs) is the self-hosting candidate. It has no official operator. Two community operators exist, and both manage a bucket and the access to it, as Serge supposed. The facts below were read on 2026-11-28 from each repository and its README. They are not a recommendation: both are single-maintainer projects, and the choice is Serge's.

| | rajsinghtech/garage-operator | bmarinov/garage-storage-controller |
|---|---|---|
| Repository | [github.com/rajsinghtech/garage-operator](https://github.com/rajsinghtech/garage-operator) | [github.com/bmarinov/garage-storage-controller](https://github.com/bmarinov/garage-storage-controller) |
| Maintainer | one person's account | one person's account |
| Licence | Apache-2.0 | Apache-2.0 |
| Last release | v0.9.3, 2026-11-25 | v0.5.4, 2026-10-20 |
| Activity | created 2026-01-15; last push 2026-11-26; 280 stars; 3 open issues | created 2025-12-01; last push 2026-10-20; 14 stars; 21 open issues |
| Maturity as stated | not stated in the README | "beta"; its CRDs "are still alpha" |
| Bucket | `GarageBucket` (`garage.rajsingh.info/v1beta1`): quotas, website hosting | `Bucket` (`garage.getclustered.net/v1alpha1`): name, maximum size, import of an existing bucket |
| Access | `GarageKey`: per-bucket `read`, `write`, `owner`, or `allBuckets`; imports an existing key | `AccessKey` plus `AccessPolicy` (`read`, `write`, `owner`) |
| What it writes for a consumer | a Secret shaped by `secretTemplate`: key names, endpoint, region, an optional AWS credentials file | a ConfigMap (`bucket-name`, `s3-endpoint`) and a Secret (`access-key-id`, `secret-access-key`) |
| Also does | the Garage cluster itself (`GarageCluster`, `GarageNode`), a management handle over an existing Garage, an optional COSI driver | nothing else: it needs a running Garage and its admin token |
| Needs Garage | v2.0.0 or newer (it drives the admin API v2 only) | the admin API v2 (`/v2/CreateBucket`, `/v2/CreateKey`, `/v2/AllowBucketKey` and others) |

Sources: both READMEs, and the GitHub API for licence, releases, stars and issues, read 2026-11-28.

**Fallback if no operator is usable.** Garage's own admin API, version v2 as of Garage v2.6.0 (released 2026-11-24), creates the bucket (`CreateBucket`), the key (`CreateKey`, `ImportKey`), the grant (`AllowBucketKey`, `DenyBucketKey`) and the quota and website settings (`UpdateBucket`). Source: the API specification at tag v2.6.0, `doc/api/garage-admin-v2.json`, read 2026-11-28. Calling it from a Job would need code that launcher does not have today.

Whether either operator runs in production was not researched.

### Create, provider S3

Operators that create a bucket on a provider's S3 exist. One line each, with no deep dive; read 2026-11-28.

- [AWS Controllers for Kubernetes S3 controller](https://github.com/aws-controllers-k8s/s3-controller): AWS's own controller, type `Bucket`, Apache-2.0, v1.16.0 (2026-11-10).
- [Crossplane provider-upjet-aws](https://github.com/crossplane-contrib/provider-upjet-aws): AWS S3 resources through Crossplane, Apache-2.0, last push 2026-11-26.
- [Google Config Connector](https://github.com/GoogleCloudPlatform/k8s-config-connector): Google's own controller for Cloud Storage, Apache-2.0, last push 2026-11-27.
- [Azure Service Operator](https://github.com/Azure/azure-service-operator): Microsoft's own controller for Azure storage, MIT, last push 2026-11-26.
- [Rook](https://github.com/rook/rook): `ObjectBucketClaim` over a Ceph object store, Apache-2.0, last push 2026-11-27.
- [COSI](https://github.com/kubernetes-sigs/container-object-storage-interface): the Kubernetes bucket claim API (v1alpha2), Apache-2.0, release v0.2.2 on 2025-12-08. A driver provides the backend; the rajsinghtech operator offers one for Garage.

None is proposed as a built-in type here. Each can go through the generic route once a user pins its CRD.

### Consume: one model for every S3

**Tally.** Of the 27 application charts of the survey, 3 take S3 client settings in their default values (`nextcloud`, `cnpg-cluster`, `velero`), and 2 carry only a commented example (`kube-prometheus-stack` for Thanos, `flux2` for a Flux Bucket). Chart 15 (`minio`) is the S3 server itself. Source: `helm show values` of each chart at the survey's version, read 2026-11-28, nothing installed. The detector is a text match for `s3`, `bucket` and `objectstore` in the default values, so a chart that takes S3 settings in a free-form config block reads as none: treat the count as a floor.

**Shapes in which an application takes S3 settings.**

| Shape | Seen in | Count |
|---|---|---|
| Discrete values with the application's own key names (access key, secret key, endpoint, region, bucket, path style) | `nextcloud` (`nextcloud.objectStore.s3`), `cnpg-cluster` (`backups.s3`, `recovery.s3`) | 2 charts |
| A reference to an existing Secret, with the key names configurable | `nextcloud` (`secretKeys`), CloudNativePG (`s3Credentials`: `accessKeyId`, `secretAccessKey`, `region`, `sessionToken`, each `{name, key}`), `velero` (`credential: {name, key}`) | 3 charts |
| A credentials file inside one Secret key | `velero` (the Secret key its `credential` names; the format of that content is not confirmed, see below); the rajsinghtech operator can write one | 0 charts confirmed |
| AWS-style environment variables as Secret keys | the VolSync restic Secret: `RESTIC_REPOSITORY`, `RESTIC_PASSWORD`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` ([VolSync restic documentation](https://volsync.readthedocs.io/en/stable/usage/restic/index.html), read 2026-11-28) | 0 charts, 1 built-in user |
| A URL form | CloudNativePG `destinationPath` (`s3://bucket/path`) with `endpointURL`; restic `RESTIC_REPOSITORY`; `velero` `config.s3Url` | 2 charts, 1 built-in user |

**The two built-in users.** `cnpg-objectstore` references the credential keys of an existing Secret and never creates the bucket or the Secret ([PostgreSQL backup and recovery](https://github.com/go-kure/launcher/blob/main/docs/postgresql-backup-recovery.md), line 33 and the example at lines 70-72). The `volsync` trait references the restic Secret by name and never builds it (traits README, the `volsync` row). So today a user writes both Secrets by hand, in two different shapes. That is the gap a consume model closes.

**Proposal: an S3 access model.** One configuration model, whatever S3 is behind it.

- Inputs: `endpoint`, `region`, `bucket`, `prefix`, `pathStyle`, an optional CA Secret, where the credentials come from (a reference to an existing Secret, an `external-secret` reference, or a value the model writes with the `secret` trait), and `format`.
- Writes: a ConfigMap (the `configmap` trait) with the non-secret settings, and the credentials as an `external-secret` trait or a `secret` trait.
- `format` is an optional switch that defaults to `plain`:

| `format` | Keys written |
|---|---|
| `plain` | `access-key-id`, `secret-access-key`, `endpoint`, `bucket-name`: the shape both Garage operators write |
| `aws-env` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `AWS_ENDPOINT_URL_S3` |
| `credentials-file` | one key holding an AWS shared credentials file |
| `restic` | `RESTIC_REPOSITORY` plus the AWS variables, for the `volsync` trait |
| `barman` | the keys a `cnpg-objectstore` `s3Credentials` reference names |
| `custom` | the user names every key |

**Policy.** An environment policy that forbids explicit secrets (`oam.ExplicitSecretPolicy`, `AllowExplicitSecrets() == false`) refuses the `secret` trait with the class `RefusalExplicitSecret` ([`pkg/oam/policy.go`](https://github.com/go-kure/launcher/blob/main/pkg/oam/policy.go), traits README, "The secret trait"). So the plain-key route is a choice a policy can close, and the `external-secret` route stays open. That policy does not touch `configmap` or `external-secret`.

### Not confirmed

None of these is a fact on this page.

- Whether Garage's pull request 994 (a `GarageNode` CRD in Garage's own repository) was merged.
- The bucket CRD names of the Crossplane, Config Connector and Azure Service Operator routes.
- The content format of the credentials Secret that Velero reads.
- Whether the External Secrets Operator can read a Secret that a Garage operator created, so that the consume model could re-format it for another application.
- Production use, and the upgrade record, of either Garage operator.

## Open questions

None of these is decided here. Questions 1, 3 and 4 of the first draft were answered by Serge on 2026-11-27 and are recorded in the sections above (the Prometheus Operator types, S3, and the workload model). The numbers of the others are kept so that earlier references still point at the same question.

- **Question 2: a trait defined without Go.** Serge's answer on where a generic part lives ("3. Both.", see Traits) settles the monitoring part. It does not address this: does launcher want a way to define a trait without Go? A trait is Go today. This is a design question for go-kure/launcher#102, and it is not among his answers.
- **Question 5: conversion on or off.** Does launcher state in the cluster profile that the VictoriaMetrics operator's Prometheus conversion is on, so a user can rely on a hand-written `ServiceMonitor`?
- **Question 6: how definitions and models reach a user.** launcher loads definitions only from the application's `definitions/` directory, and models only from its `models/` directory and `--model` files. Nothing is embedded (see "What a model part can write"). So how does a definition with its pinned CRD, or a model that launcher offers, reach a user? The choices: ship them with the binary, ship them as a separate package or OCI artifact, or leave them to each application.
