# Built-in offering plan (draft)

**Status: draft.** Nothing on this page is decided or scheduled, and nothing is built. Every "include", "combine" and "not" is a **proposal** for Serge, with its reason. What this page cannot settle is listed at the end as open questions.

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
| Workload model: a workload with optional volume, exposure, database, monitoring and backup | Include | Configuration model | It is the most frequent combination that no single type gives (survey, Candidate models). One model with parts switched off replaces a monitored-web-service model and a stateful-web-app model. |
| Generic monitoring part: a scrape and a rule | Include | A part inside a model; as a trait, Go (see below) | It serves the most widespread use case in the survey's charts. It lowers to VictoriaMetrics kinds only. |
| Generic backup part | Include | Lowers to `cnpg-scheduledbackup` for a database and to VolSync for a volume | Decided on 2026-11-27 (see Backups). It needs no new kind. |
| S3 storage: a bucket and the access to it | Include, as a base offering, after research | Kind, model or trait: open | Serge asked for it as a base offering. Provider S3 is not researched yet. |
| Data store model | Include, low priority | Configuration model | An operator type is the better route where one exists, so the value is where none does. |
| Operator install model | Not for now | | `helm` already delivers most of it, so the value is low (survey, Conclusion). The per-namespace grant needs Go. |
| Many-service application | Not | | It is the worked example of what the mechanism must build. A user writes a model for it. |
| Generic dashboard part or trait (Grafana, Perses) | Not | | The content of the dashboards is too different (Serge, 2026-11-27). The kinds stay by hand. |
| Prometheus instance kinds (`Prometheus`, `Alertmanager`, `ThanosRuler`, `PrometheusAgent`) | Not | | launcher supports no native Prometheus backend (Serge, 2026-11-27). |
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
| The workload model is a configuration model. | Its parts are existing types switched on by inputs. Restart on a config change can use the text in a pod annotation for a ConfigMap the model builds. The hash itself stays Go, so this part of the behaviour is partial. |
| The generic monitoring part is a configuration model part. | A scrape and a rule are built from inputs (selector, port, path, interval, expression). |
| The generic backup part is a configuration model part. | It only maps what to back up onto two existing types, and a model part can carry the `volsync` trait (see "What a model part can write" and Backups). |
| The S3 storage part is a Go type or a model, depending on the route. | Garage has no official operator, so no CRD route exists for it. |

Existing Go lowering rules (`webservice`, `worker`, `helm`, `postgresql`, `oci`) are not taken as settled by the survey, and this page does not settle them.

### Traits

A trait adds a capability to a component. Today the built-in traits are Go handlers, and `expose` is a Go lowering rule that picks an `ingress` or an `httproute` from the cluster profile.

- **Proposal:** the generic monitoring part is also offered as a trait on a plain workload, because a model cannot carry a trait on its own component. As a trait it is Go today.
- **Proposal:** no S3 trait until the route is decided (see the open questions).
- **Proposal:** no dashboard trait (see above).
- A configuration-defined trait type is not proposed here. It would need a mechanism that go-kure/launcher#102 has to design. See the open questions.

### What a model part can write

Read from the code and tried with a model in a local test on 2026-11-27.

- **A kind that only a generic-route definition declares: yes.** A model part names its type, and that type must be declared when the model registers. Registering a definition declares the kind's identity (`<kind>.<group>@<version>`, such as `vmrule.operator.victoriametrics.com@v1beta1`) so that a model can name it. `kurel build` loads definitions before models. A model whose part named such a kind built the object in the test, and the same model with no definition was refused at registration. So the generic monitoring part needs no built-in type for `VMServiceScrape`, `VMPodScrape` or `VMRule`.
  - The test used the CRDs of the VictoriaMetrics operator as they stand on its default branch on 2026-11-27, not a release. The loader accepts the CRDs of all three kinds, and a model with a scrape part and a rule part built a `VMServiceScrape` and a `VMRule`. It refuses `VMAuth`, because that CRD leaves its `spec` open. That agrees with the proposal that the instance kinds wait.
  - Not tried: the CRDs' own CEL rules, which the loader does not evaluate, and a CRD pinned to a release.
- **Not settled: how a definition and a model reach a user.** launcher loads definitions only from the application's `definitions/` directory, and models only from its `models/` directory and `--model` files. Nothing is embedded in the binary. So every "Include" row that is a model, and the pinned CRDs a monitoring part needs, wait for a delivery route. That is an open question (see the open questions).
- **The `volsync` trait from a model part: yes.** See Backups.

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

The components README lists 7 built-in Prometheus Operator types: `alertmanager`, `podmonitor`, `prometheus`, `prometheus-probe`, `prometheusrule`, `servicemonitor` and `thanosruler`. Under the rule, the four whose kinds the VictoriaMetrics operator converts (`podmonitor`, `prometheus-probe`, `prometheusrule`, `servicemonitor`) can stay as kinds a user writes by hand. The other three (`alertmanager`, `prometheus`, `thanosruler`) are instance kinds. This page asks what they become and decides nothing (see the open questions).

## Backups, as decided

Decided on 2026-11-27 (KL-backup-default-1, answered 2026-11-27 18:24 CET): a generic backup part lowers to CloudNativePG for a database and to VolSync for a volume. Both are built in, so no new kind is needed. Velero is not a default target, and its kinds stay by hand through the generic route. Backup of objects is not needed where every object comes from git, and a whole-cluster (etcd) backup belongs to the cluster platform. The reasons are on the survey page, section Monitoring and backup, generically.

**The volume backup can be written from a model, with no Go lowering rule.** A model part can carry traits, and a trait a model writes is checked like one written in a document. In a local test (2026-11-27), a model with a `persistentvolumeclaim` part, followed by a `deployment.apps` part that carries the `volsync` trait with `sourcePVC: ${ parts.claim.name }` and a `schedule`, built the claim and a `ReplicationSource` for it. Three rules for such a part:

1. Put the trait on a part that comes after the claim's part. A part reads only an earlier part, so a trait on the claim's own part cannot read the claim's name. The model is refused when it registers.
2. Read the claim's name from `parts.<id>.name`. The claim is named `<component>-<part name>`, so the bare part name (or a name the part sets itself) leaves out the component prefix and points at no claim. A full name typed by hand matches today and breaks with the naming.
3. The restic repository Secret is referenced and never built. The part takes its name as an input, or the model builds the Secret as a part. Its default name comes from the component that carries the trait.

The limit stated above still holds: a trait on a component of a model type is refused, so the backup is a part of the model and not a trait on the model's own component.

## S3 as a base offering

Serge asked for a kind, model or trait that provisions S3 buckets and the access to them, so that a generic S3 part is possible in the end.

- **Proposal:** treat S3 storage as a base offering to include, with a generic S3 part as the end state. It says which bucket, who may read and write it, and where the credentials go, and it lowers to whichever S3 service the cluster uses.
- **Self-hosting candidate:** Garage from Deuxfleurs. It has no official operator, so there is no CRD for the generic route. The way in is a workload model, or a Go type.
- **Not researched:** what exists to provision buckets and access on a provider's S3, and what Garage needs to run and to hand out keys. Those are separate pieces of work, and this page proposes nothing about them.

## Open questions

None of these is decided here.

1. **The 7 Prometheus Operator types.** Do the four converted ones stay as hand-written kinds, and do `alertmanager`, `prometheus` and `thanosruler` stay, get marked unsupported, or go?
2. **Where a generic part lives.** Is the generic monitoring part (and an S3 part) a trait, a model part, or both? A trait is Go today. Does launcher want a way to define a trait without Go? That is a design question for go-kure/launcher#102.
3. **One S3 part or two.** Is S3 on a self-hosted server and S3 from a provider one generic part with two lowerings, or two parts?
4. **The workload model's shape.** Is it one part that a mode value turns into a service, a worker or a job, as some of the survey's charts do? The survey leaves this open.
5. **Conversion on or off.** Does launcher state in the cluster profile that the VictoriaMetrics operator's Prometheus conversion is on, so a user can rely on a hand-written `ServiceMonitor`?
6. **How definitions and models reach a user.** launcher loads definitions only from the application's `definitions/` directory, and models only from its `models/` directory and `--model` files. Nothing is embedded (see "What a model part can write"). So how does a definition with its pinned CRD, or a model that launcher offers, reach a user? The choices: ship them with the binary, ship them as a separate package or OCI artifact, or leave them to each application.
