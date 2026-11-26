---
title: "Fill report"
weight: 8
---

A consumer that builds an input form or an API from the published property schemas needs
two facts a schema alone does not give: who supplies a value the author leaves out, and
which policy refusal an authored value can cause. This page says where each fact is read
and what it does not cover. It links to the full text and repeats none of it.

## Who supplies a value

`Transformer.TransformWithProvenance(app, ctx)` returns, beside what `TransformWithPolicy`
returns, a `*Provenance`: for each component and trait of the document, the source of every
leaf value. The kinds are listed under "Property provenance" on
[Transform, extension and lowering](transform-and-lowering.md#transform--extension). Four of
them name a value the author did not write at that path:

| Kind | Who supplied the value |
|------|------------------------|
| `policy` | A default of the `Policy` the transform ran with. |
| `builtin` | Launcher's own code, because nothing else held a value. |
| `capability` | A `ClusterProfile` binding. |
| `rendered` | A lowering rule added, changed or moved it. The value can still be one the author wrote under another key. |

The report is about one run of one document. A path has an entry only where a value
stands, and one source only: the one whose value stands.

## A fact per property

"Left out, this property is filled by the policy" is not in the report of one document. It
is derived: run a document that leaves the property out, once under a policy that sets
every default and once under none, and read the entry at the property's path in each run.

Package `probe` ([Fill Probes](/pkg/oam/builtin/probe/README.md)) holds such documents and
the two policies. A consumer passes its own transformer:

```go
for _, p := range probe.Probes() {
	with, _, err := probe.RunWith(tr, p.Components, probe.ProbePolicy(), p.Capabilities)
	// ...
	without, _, err := probe.RunWith(tr, p.Components, probe.NoPolicy(), p.Capabilities)
	// ...
}
```

Each run returns the report and the generated objects. Each `Probe` also states what was
observed for its document (`Fills`): the path, the source in each state, and the value an
author can write for it. The two sources of a path read as follows:

| With the policy | With none | Reading |
|-----------------|-----------|---------|
| `policy` | `builtin` | The policy default where the policy sets one, launcher's value where it sets none. |
| `policy` | no entry | A policy default fills it. With none the report holds nothing, which does not show that nothing fills it. |
| `builtin` | `builtin` | Launcher fills it under any policy. |
| `builtin` | no entry | Launcher fills it for some documents only, and a policy default made this one such a document. |

The last row is `topologySpreadConstraints` of `webservice` and `worker`: the trait fills
it from two replicas, and the probe document runs two under the policy's default and one
under none. No entry in a state is therefore not "nothing ever fills it". The second row
has such a case too: `instances` of the Cluster a `postgresql` component builds. With no
policy launcher's default of 1 stands, and the report holds no entry for it.

The values `ProbePolicy()` supplies are arbitrary, and they can make a document refused in
that state (`Probe.RefusedWithPolicy`). They never belong in what a consumer publishes: the
value of a default is read from the consumer's own policy.

## Lowered types

A component type that a rule lowers has no entry of its own: the report speaks at the
members the rule emits. `webservice` and `worker` publish where each of their keys lands
(`Transformer.ComponentLoweringKeys()`, see
[Lowering keys](schemas-and-contract.md#lowering-keys)), so a fill at a member's path maps
back to the key an author writes. A probe row names the member in `Fill.Member`.

`oci` and `helm` publish the same map. `postgresql` and the `expose` trait rule publish no such map yet
(go-kure/launcher#1601). Their fills are read at the member's path. The `postgresql` probe
names the place of each declared default itself (`Probe.DefaultPlaces`).

## Which types report

Only a config that reports its fills has `policy` and `builtin` entries. These do:

- `webservice` and `worker`, through the `role-deployment` they lower to. The list is under
  [policy defaults](../oam-components/policy-and-endpoints.md#policy-defaults--enforcement-ordering).
- The Cluster a `postgresql` component builds.
- The `persistentvolumeclaim` component: the policy's default `size`.
- The traits in the table under "Property provenance", each with the paths it reports.

The raw `deployment`, `statefulset`, `daemonset`, `job` and `cronjob` kinds are not in the
list: they fill no default, so they have no `policy` or `builtin` entry (see the
`deployment`, `statefulset`, `daemonset`, `cronjob` and `job` sections of
[Kinds: core workloads](../oam-components/kinds-core-workloads.md#deployment)). A
`statefulset` claim template that authors no `spec.storageClassName` takes the class of the
ClusterProfile's `pvc` capability, and the report names that capability as the source, at
`volumeClaimTemplates[<n>].spec.storageClassName`.

A fill can depend on the document. The `topology-spread` trait reports
`topologySpreadConstraints` for a Deployment of two replicas or more, and nothing at one
replica. The `security-context` trait reports `runAsNonRoot`, `allowPrivilegeEscalation`
and `readOnlyRootFilesystem` at `psaLevel: restricted` only, and each only where neither
the trait nor the workload set it. The table states the exact condition.

A handler whose config reports nothing leaves the defaults it applies out of the report.
For such a type, no entry at a path says nothing about who supplies the value.

## The checks

The probe package carries three checks a consumer runs from its own test, against its own
transformer. Each returns its findings as strings:

- **A**, `CheckDeclaredDefaults`: a default a schema declares is applied and reported.
- **B**, `CheckReportedFillsAreReal`: a fill the report names is one the library applies,
  and the report names no fill the probe does not list.
- **E**, `CheckProbePolicyCoversEveryDefault`: the probe policy sets every `Default*`
  accessor of `oam.Policy`.

What each check compares, and the rows it cannot compare, is on
[Fill Probes](/pkg/oam/builtin/probe/README.md).

## Limits

The kinds, the list of types that report and the key maps are read from the code and from
the pages linked above. What a probe states, and what the checks hold, is observed in these
probes. Neither is a claim about every fill.

- A fill no probe document triggers is not seen, so a clean run of the checks says nothing
  about it. Not every trait in the table has a probe.
- A fill that no schema declares, that no config reports and that happens only for some
  documents is found by no check.
- The report does not name every value a trait supplies. The list is under "Not reported"
  on [Transform, extension and lowering](transform-and-lowering.md#transform--extension).
- A name the consumer's `Naming` hook gave has no entry: the report has no source kind for
  the hook.
- A component type defined by a model publishes no key map, and neither do the two rules
  named above (`postgresql` and the `expose` trait rule).

## Which refusal an authored value can cause

A policy refusal carries a class and a message, and no property path. So the library
declares, per property, the classes an authored value there can cause: `boundedBy` on the
property schema. The classes and the properties that declare them are under
[Declared bounds](policy-and-capability.md#declared-bounds).

Each declared class is held by a test that shows the refusal. The test runs one way: a
property with no entry is not shown to be free of a bound, and a built-in type that
declares none says nothing about whether the policy holds it.
