# OAM Fill Probes

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/probe.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/probe)

Package `probe` holds the library's provenance report to what the library does, as
observed in a fixed set of probe documents. A built-in handler fills values the author
left out, and the report names each one with its source (`policy` or `builtin`, see
`pkg/oam`). The probes run documents through a transformer and compare the report with
the generated objects. They show what these documents trigger. A fill no probe document
triggers is not seen, so a clean run says nothing about it.

A consumer passes its own `*oam.Transformer`. The probes bring their own policies:
`ProbePolicy()` (every `Default*` accessor of `oam.Policy` set to an arbitrary value) and
`NoPolicy()`. The arbitrary values never appear in what a consumer publishes.

## Surface

| Name | Use |
|------|-----|
| `Probes()` | The probe documents, each with the fills observed with and without the policy. |
| `Probe`, `Fill` | One document, and one fill it shows: path, source in each state, and the value an author can write for it. |
| `ProbePolicy()`, `NoPolicy()` | The two policy states. |
| `Run(tr, components, policy)` | Validate, transform with provenance, generate: the report and the marshalled objects. |
| `CheckDeclaredDefaults` | Check A. |
| `CheckReportedFillsAreReal` | Check B. |
| `CheckProbePolicyCoversEveryDefault` | Check E. |

Each check returns findings as strings and never calls `testing`, so a consumer runs it
from its own test and prints what it returns.

## The three checks

- **A, declared defaults.** For every default a handler schema declares for a property a
  probe document leaves out, writing the default gives the same objects as leaving it out
  and the report holds an entry at that path. A default the handler leaves to the
  cluster is listed in the probe's `LeftToCluster` with the reason. The check reads schema
  declarations only: a fill no schema declares, such as the resource requests, is not
  seen here.
- **B, reported fills are real.** In both policy states the report holds the sources each
  probe states, and no entry of source `policy` or `builtin` that the probe does not list.
  For each fill with an authored value, writing that value gives the same objects and no
  entry at that path. An entry no probe lists fails with an instruction to add a row or
  fix the report.
- **E, the probe policy sets every `Default*` accessor.** An accessor added to
  `oam.Policy` and not to the probe policy is named.

One exception is named and kept: the cronjob schema declares `concurrencyPolicy` with the
default `Allow`, and the handler leaves the field unset for the cluster to apply. Writing
`Allow` changes the generated manifest text and not the cluster's behaviour. The cronjob
probe lists it in `LeftToCluster`. It is the one place check A disagrees with a schema.

The probes cover the handler-built workload types: deployment, statefulset, daemonset,
job and cronjob, with their init containers, sidecars, claim templates and pod failure
policy. It also probes the types that lower to other components: `webservice` and `worker`,
whose entries the report names at the place of their `deployment` member (`Fill.Member`),
and `postgresql`, which the report names at the cluster it builds. For a type that
publishes a lowering key map check A reads the places of a declared default from it. For
`postgresql`, which publishes none, the probe's `DefaultPlaces` names them. A default that lands in several
places needs an entry at each. Provenance records leaves, so a default under a key the rule
builds (`affinity`) is read as "some entry below that key": weaker than its own leaf.
For `postgresql`, check B compares written values for the four resource rows only; the
`enablePDB`, `instances` and `storage.size` rows state sources and no value, because the
authored keys differ (`replicas`, `storageSize`), so check B does not show that the library
applies those three fills.

Check A also found declared defaults the report holds no entry for, observed in these
probes. Each is listed in its probe's `Unreported` with the reason, and check A fails when
an entry there stops being a gap: `topologySpread` on `webservice` and `worker` (the
document with no policy runs one replica, where the topology-spread trait it switches writes
no constraints and the report holds no entry; the document with the policy runs two and
holds it), and `provider`, `version` and, without a policy default, `replicas` on
`postgresql`.

The trait probes put one trait on a deployment, with the capability bindings the transform
runs with (`Probe.Capabilities`), and name the fills the report holds under the trait
(`Fill.Trait`): `certificate`, `external-secret`, `volsync`, `scaler`, `networkpolicy`,
`ingress`, `httproute` (also the gateway namespace of a platform gateway), `pvc` and
`topology-spread`. A
probe row writes its value at the trait at `Fill.TraitIndex`, when that trait has the row's type. Five limits, observed in these
probes:

- Check A reads declared defaults of components and not of traits, so a declared trait
  default no row names is not seen by it.
- The `issuerRef.kind` and `gatewayNamespace` rows state sources and no value: an author
  cannot write that path alone, or the platform owns it, so check B does not show that the
  library applies them.
- The computed fills (names, backends, a target secret, the repository of a backup) have
  rows that state sources only: no value an author writes is the computed one, so check B
  does not show that the library applies them.
- A fill the report does not hold yet has no row: what the `expose` rule fills. The
  `topology-spread` row states a source only: the trait takes no property and fills a field
  of the Deployment, so no value an author writes is its fill.
- The `security-context` probes cover `restricted` (three fills) and `baseline` (none).
  Only `readOnlyRootFilesystem` has a value and an `Other`: the opposite of `runAsNonRoot`
  and of `allowPrivilegeEscalation` is refused at `restricted`, so their rows state a source
  only. A workload that authors some of the three, a Windows pod and `privileged` have no
  probe; the registry tests read each of them.
- `scaler` lists `replicas` in `NotAuthorable`: the trait owns the replica count and the
  document refuses an authored one with the stated text, so check A cannot compare objects for it.
