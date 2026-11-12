---
title: "Component label and ownership"
weight: 2
---

## Component label and ownership

Every application of a transformed document belongs to one authored component, or to the
document as a whole (go-kure/launcher#788). The transform records that as its last step, and
two things follow from it: each application reports its owner, and each object it generates
carries the owner's component label.

| Application | Owner |
|-------------|-------|
| A component's own application, or a same-name sibling group | The component. |
| A component a lowering rule emitted for it, under any name (the `postgresql` pooler `<comp>-pooler`, a database, an object store) | The authored component the rule lowered. |
| A sub-application a trait added | The trait's component. |
| A synthesized `NetworkPolicy` (`{comp}-allow-ingress-traffic`, `{comp}-allow-egress-traffic`, `{comp}-allow-endpoint-ingress`) | The authored component of the entry it was synthesized for. |
| A generated source the application bundle holds (go-kure/launcher#783), which every consumer shares | None: the document as a whole. |
| The external-backend policy (`{service}-allow-ingress-traffic`) | None. |

**Provenance.** `GeneratedApplication.Component` is the owner, and empty for an application
the document as a whole owns. After `Transform`, every `stack.Application.Config` is
launcher's ownership wrapper: `ComponentName()` (`ComponentNamed`) answers the same value on
it. The wrapper forwards every optional contract launcher reads on a config after the
transform (`Validate`, the Flux namespace, Service port and routing answers,
`ServiceAccountNamer`, the single-pod claim, pod template labels, identity target ports, the
layout contracts). A test fails when the code asserts or switches on a new type directly on
an application's config (`app.Config.(T)`, `switch app.Config.(type)`) and the wrapper's
list does not account for it. It reads syntax only: a config first copied to a variable or
passed to a function, and asserted there, is outside what it sees. A caller that needs the
concrete config, or a contract of its own, reads `UnwrapConfig(app.Config)`; the wrapper is a
`ConfigWrapper`
(`WrappedApplicationConfig()`). An application a caller adds to the cluster itself has no
wrapper and reports its `ComponentNamed` answer, else its name, as before.

**The label.** `<key>: ComponentLabelValue(<owner>)`, with the key the synthesized
NetworkPolicies select (`TransformContext.ComponentLabelKey`, else `<Domain>/component`),
goes on:

- every object the application generates, in `metadata.labels`;
- the pod template of a `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `ReplicaSet`,
  `ReplicationController` or `PodTemplate`, and the job template's pod template of a
  `CronJob`, typed or unstructured;
- a `CronJob`'s job template (`spec.jobTemplate.metadata.labels`), typed or unstructured, so
  every Job the CronJob creates carries the label;
- the same places on every object the config adds to a layout it augments
  (`layout.LayoutAugmenter`): the objects that were not on the layout, or on a layout below
  it, before the config's `AugmentLayout` ran. What was on the layout already is left as it
  is: the layout walker puts there what the application generated, which carries the label;
- nothing an application without an owner generates.

**The label is authoritative** (go-kure/launcher#790). Before it writes the label, the
wrapper holds every object the application generates to it: a value the object already holds
under the key is the owner's, or generation fails. That holds whatever wrote the value: a
`manifests` or `passthrough` document, an authored pod label, a kind's property, a chart
rendered at build time. The synthesized NetworkPolicies select by the label, so a pod that
carried another component's value would be let in wherever that component's pods are. The
kinds' `labels` property refuses the same value in the transform ("`labels` and
`annotations`: the metadata of a kind component's object" below). The key must therefore be
one nothing else writes.

The value is read, on every object and on each member of an unstructured list envelope:

| Where | On | Launcher writes the label there when the key is absent |
|-------|----|----|
| `metadata.labels` | every object | yes |
| the pod template's labels | `Deployment`, `StatefulSet`, `DaemonSet`, `Job`, `ReplicaSet`, `ReplicationController`, `PodTemplate`, and the job template's pod template of a `CronJob`, each in its own API group | yes |
| `spec.jobTemplate.metadata.labels`, the labels of each Job the CronJob creates | a `batch` `CronJob` | yes |
| `spec.inheritedMetadata.labels` | a `postgresql.cnpg.io` `Cluster` | no |
| `spec.template.metadata.labels` | a `postgresql.cnpg.io` `Pooler` | no |
| `spec.podMetadata.labels` | a `monitoring.coreos.com` `Prometheus`, `PrometheusAgent`, `Alertmanager` or `ThanosRuler` | no |
| `spec.<mover>.moverPodLabels` | a `volsync.backube` `ReplicationSource` (`rsync`, `rsyncTLS`, `rclone`, `restic`, `syncthing`) or `ReplicationDestination` (`rsync`, `rsyncTLS`, `rclone`, `restic`) | no |
| `spec.acme.solvers[].http01.ingress.podTemplate.metadata.labels`, and the same under `http01.gatewayHTTPRoute`, in every solver | a `cert-manager.io` `Issuer` or `ClusterIssuer` | no |
| `spec.infrastructure.labels` | a `gateway.networking.k8s.io` `Gateway` | no |
| `spec.commonMetadata.labels` | a `kustomize.toolkit.fluxcd.io` `Kustomization`, a `helm.toolkit.fluxcd.io` `HelmRelease` or a `fluxcd.controlplane.io` `ResourceSet` | no |

The six rows after the CronJob's are metadata an operator puts on the pods it creates (a
Gateway's `spec.infrastructure` on whatever the controller creates for it, which may be
pods). The last row is what the Flux controller puts on every object it applies or renders,
and the Flux Operator on every object a ResourceSet generates, pods among them. Launcher reads
them and writes nothing there: such pods carry the component label only where the document or
a kind puts it. A typed object of a `monitoring.coreos.com`, `volsync.backube`,
`cert-manager.io` or `gateway.networking.k8s.io` kind is recognized when it states its kind; a
typed Flux `Kustomization` or `HelmRelease` by its Go type as well. The component label and
reserved-key checks read a typed object in its JSON form with numbers kept exact, so a
`HelmRelease` whose `values` hold a number no float64 holds is not refused for it
(go-kure/launcher#1085); so does the claim a chart template makes, for a typed object other
than the Flux `HelmRelease` type, whose `spec.chart` alone it reads. A `ResourceSet` has no
component type: the row holds one that a `manifests` or `passthrough` document carries, as
every row does. A refusal names the labels by their path, with the index of the solver where
they are in the list (`spec.acme.solvers[1].http01.ingress.podTemplate.metadata.labels`). Not
read: `spec.commonMetadata` of an `ArtifactGenerator` and a `FluxInstance`, which go onto the
ExternalArtifacts it generates and onto the objects of the Flux installation, no pods, and a
`StatefulSet`'s volume claim templates (`spec.volumeClaimTemplates[].metadata`), which go onto
the PersistentVolumeClaims the StatefulSet controller creates (go-kure/launcher#944), and the
claim template of the storage of a `Prometheus`, `PrometheusAgent`, `Alertmanager` or
`ThanosRuler` (`spec.storage.volumeClaimTemplate.metadata`), which the operator copies onto
the volume claim template of the StatefulSet it creates (go-kure/launcher#957). The reserved
metadata keys are read in all four; the storage claim template only where neither
`spec.storage.emptyDir` nor `spec.storage.ephemeral` is set, since the operator uses the first
storage arm set and makes no claim from the template beside either. Launcher writes no label into a volume claim template
either: the API server refuses a change to a StatefulSet's
`spec.volumeClaimTemplates`, so a label written there would fail the apply of a StatefulSet the
cluster holds already, and the controller does not relabel a claim it created before. The
controller puts the `matchLabels` of the StatefulSet's selector on each claim it creates.

The five Flux holders of `spec.commonMetadata` put it on the top-level metadata of each object
they apply, never on a pod template. A `Kustomization`, `HelmRelease` or `ResourceSet` may
apply a `Pod`, so the label reaches pods there and both checks read it. What an
`ArtifactGenerator` or a `FluxInstance` applies is no `Pod`, so only the reserved metadata
keys are read there.

The label and the reserved metadata keys are held wherever an object holds labels that reach
pods. Metadata an operator copies onto other objects it creates (the Ingress of a solver, the
Secret of a Certificate, the Services of a Cluster) is held to the reserved keys alone
([Reserved metadata keys](#reserved-metadata-keys)): nothing selects those objects by the
component label, so another component's value there builds, and launcher writes nothing
there. `labelReachReservedOnly` in `pkg/cmd/kurel/label_reach_test.go` names each such field
with what it reaches, and `labelReachNotRead` each field neither check reads.
`TestLabelReach_EveryFieldIsHeldOrListed` derives these fields from the API types of the
registered kind components, so a field a dependency adds fails the test until it is held or
listed.

A workload whose own selector **requires** another value for the key is refused too: a
`matchLabels` entry, an `In` expression that does not hold the component's value, or the
entry of a `ReplicationController`'s label map. Its pods could not carry the component's
value.

So is a workload whose own selector **rules the label out** of a pod template that carries no
value for the key: a selector that matches the template and would stop matching it with the
label, by a `DoesNotExist` on the key or a `NotIn` holding the component's value. The cluster
refuses a workload whose selector does not match its template, so launcher could not write the
label there, and no synthesized policy would select its pods. The refusal names the selector
(`spec.selector`, a CronJob's `spec.jobTemplate.spec.selector`) and says to take the label out
of it. A selector that rules out only another value, or that does not match the template in
the first place, is not refused, nor is a `ReplicationController`'s, a plain label map that a
further label on its pods never fails.

The refusal names the object, the path and the component, and its text is the document
author's to read:

```text
Deployment "web": spec.template.metadata.labels["launcher.gokure.dev/component"]: "db" is not the component label of component "web" ("web"): launcher sets that label on everything the component generates, and the NetworkPolicies generated for the component select by it; remove the label, or write that value
```

It comes from generation (`GenerateApplications`, or `Generate` on an application), as the
reserved-key refusal does. An application the document as a whole owns has no component, and
its objects are not held to any value.

**Recognising it.** The error is a `*ComponentLabelError`, and answers to the sentinel
`ErrComponentLabelValue`, which it unwraps to:

```go
var refused *oam.ComponentLabelError
if errors.As(err, &refused) {
    // refused.Refused, refused.Component, refused.Kind, refused.Namespace,
    // refused.Name, refused.Object, refused.Path, refused.Key, refused.Value,
    // refused.Required, refused.Want, refused.Entry, refused.Other
}
```

`Refused` says which of five refusals it is, and all five answer to `ErrComponentLabelValue`:

| `Refused` | What is refused | Returned by | Fields beside `Component`, `Key` and `Want` |
|-----------|-----------------|-------------|----------------------------------------------|
| `ComponentLabelForeignValue` | a value an object holds under the key | generation | the object, `Path` (the labels, `spec.template.metadata.labels`), `Value` |
| `ComponentLabelSelectorRequiresAnother` | the values a workload's selector requires | generation | the object, `Path` (the selector, `spec.selector`), `Required` |
| `ComponentLabelSelectorRulesOut` | a workload's selector that rules the label out | generation | the object, `Path` (the selector, `spec.selector`) |
| `ComponentLabelInLabelsProperty` | a value a kind component's `labels` property holds under the key | the transform | `Path` (`labels`), `Value` |
| `ComponentLabelOfAnotherComponent` | an entry whose `app` value is another component's (below) | the transform | `Entry`, `Other`, `Value` |

`Component` is the owning component, `Key` the component label's key and `Want` the
component's value for it. The object is `Kind` (group and kind), `Namespace`, `Name` and
`Object` (the object as the text names it), read as the reserved-key refusal's are: the
namespace is empty when the object states none, and on a list envelope they are the
member's. They are empty where the transform refuses, which has no object yet: for a
property and for an entry. A reserved-key refusal is not one, and neither is metadata the
check cannot read or a label value that is no string, which fail generation with another
error. Under the key `app`, a `labels` property is held first to the `app` value the kinds
write, that of the name of the component after lowering, then to the owner's as under any key:
a refused value there is `ComponentLabelInLabelsProperty` too, with the name it fails as
`Component` and that name's value as `Want`.

**One value besides the owner's, under the key `app` only.** `app` is the label the built-in
kinds and traits write themselves, valued with the name of the component after lowering they
generate for. That is the owner's, except for an entry a lowering rule emitted under a name
of its own. With `ComponentLabelKey: "app"` such an entry's objects carry the entry's value,
which launcher wrote and no document can change, so the wrapper accepts it on that entry's
objects (and on what the entry's traits and policies generate), and nowhere else. It admits
no other component's value: under this key the transform refuses a document in which a rule
emitted, for one component, an entry whose label value is another component's (the refusal
names both components and the entry, and says to rename one of the two; it is a
`*ComponentLabelError` too, `Refused: ComponentLabelOfAnotherComponent`). The check compares
label values, not names: a name over 63 characters is projected onto a shorter value
(`ComponentLabelValue`), and an entry named as that projection carries the component's
label. Lowering's own name checks do not see either case where the other component is itself
lowered into entries under other names.
Under this key a renamed entry's pods carry the entry's value, so the policies synthesized for
that entry select it beside the owner's ("What a synthesized policy selects" below).

The label is written into a label map of the object's, or the pod template's, own. A config
that uses one map for an object's labels, its selector and its pod template keeps that map
as it is, so a selector never gains the key.

An unstructured object's labels are read as written, on the object and on a workload's pod
template. Null `metadata` or `labels` are absent ones, as is a null pod `template`, and a null
label value is the empty string the cluster reads it as. A nil map, which a config built in
Go can hold in such a place, reads as a null. A label that is not a string is
refused, with the object named, whether or not the key is there already: generation fails
rather than drop a label an author wrote. So does a workload's own selector that the check
cannot read: one that is no object, a label selector that does not decode as one (a
`matchLabels` that is no map of strings, a number among them on a typed object as on an
unstructured one, an expression whose `values` is no list), or a
`ReplicationController` label map whose entry for the key is no string. A check that read
nothing from it would pass a workload it never held to the label. A null selector is an
absent one. The kinds refuse such a selector at their strict decode, and a `passthrough`
document is refused when the environment policy reads it, as an object that cannot be read,
both before generation; a config a consumer builds in Go reaches the check with it.

An unstructured list envelope stands for its members when Flux applies it (as the force
warning reads one: Kustomize's build inlines a `List`, Flux's reader expands what is left, one
level). Each such member is labelled as an object handed out on its own is, a workload on its
pod template and a `HelmRelease` with its post-renderer, beside the envelope itself.

**Chart output.** A chart Flux installs is rendered in the cluster, where launcher cannot
label it. Its `HelmRelease` gets one kustomize post-renderer, after any authored ones, with
a strategic-merge patch per kind with a pod template (`Deployment`, `StatefulSet`,
`DaemonSet`, `Job`, `CronJob`, `ReplicaSet`, `ReplicationController`, `PodTemplate`) that
sets the label on the pod template, and one for a bare `Pod` that sets it on the Pod's own
labels. Each patch targets its kind in that kind's own API group: a custom resource of
another group whose kind has the same name is left alone. Flux applies it to whatever the
chart rendered, so here the component's value
**replaces** one the chart set. The entry is added once, however often the document is
transformed or generated.

The post-renderer reaches what Helm hands it. Whether that includes a chart's hook and test
Pods depends on the Helm version the helm-controller runs, and launcher has not verified
it: such a Pod may carry no component label.

The patch sets the pod template's label and touches no selector, and launcher does not look
into a chart. With a `ComponentLabelKey` that a Flux-installed chart's own selectors use
(`app`, `app.kubernetes.io/name`), the post-renderer replaces the value the chart set under
it. Where a chart's selector does not accept the component's value, that parts the selector
from the chart's pods and the cluster refuses the workload. Use a key no chart sets, such as
the default.

A chart rendered at build time
(`helm` under `delivery: template`, `helmtemplate`) yields objects launcher generates, which
are held to the label and labelled like any other. So one chart is treated two ways: a value
it sets under the key that is not the component's is **refused under template delivery,
overwritten when Flux installs**. Either way the key is one no chart should set.

**What a synthesized policy selects.** A synthesized inbound or egress policy selects the
value its entry's objects carry: the authored component's, also for an entry a lowering rule
emitted under a name of its own. The policy keeps the entry's name. A rule that emits several
pod-running entries from one component therefore gets policies that each select the pods of
all of them, as a sibling group's does.

With `ComponentLabelKey: "app"` the kinds write the entry's own value on a renamed entry's
pods (the paragraph on the key `app` above), and the owner's value would leave them out. So
under that key, and only where the entry's value differs from the owner's, the entry's
inbound and egress policies select both: `matchExpressions: [{key: app, operator: In,
values: [<owner>, <entry>]}]`. Each such policy then selects the pods stamped with the
owner's value and that entry's own pods, not those of a sibling entry under a third name,
which its own policies select. Under any other key the selector is `matchLabels` with the
owner's value, as before.

No built-in rule emits a pod-running entry under another name: `webservice` and `worker`
emit theirs under the component's own name, as a same-name sibling group. The `postgresql`
pooler, `<component>-pooler`, is an entry under another name, but it runs no pods launcher
generates: the operator creates the Pooler's pods, and `postgresql` writes no labels on the
Pooler's pod template, so those pods carry no component label under any key. The entry owns
no Service and so gets no synthesized inbound policy; it gets an egress policy only where
the consumer supplies egress peers under the entry's name, and under the key `app` that
policy's selector names both values and still selects none of the Pooler's pods. The
endpoint-ingress policy selects them by `cnpg.io/poolerName`. The databases run no pods. The
case of pods selected is reached only through a consumer's own lowering rule.

**What the label does not reach.**

- Pods an operator creates from a custom resource (a CloudNativePG `Cluster`'s instance pods,
  a `Pooler`'s pods): the custom resource carries the label, its pods do not. This is not a
  goal. The endpoint-ingress policy selects those pods by the operator's own labels for that
  reason. Where the custom resource does hand labels on to its pods, another component's
  value there is refused (the table above).
- Pods of a kind the post-renderer has no patch for in a Flux-installed chart.

**Breaking library changes** (go-kure/launcher#788):

- Output: every owned object and pod template gains the component label, a `HelmRelease`
  gains the post-renderer, and a component's synthesized NetworkPolicies gain the label.
- A `Job` that a cluster already holds from before this change, and that nothing recreates,
  cannot take the label on its pod template: the cluster keeps a Job's pod template
  immutable, so the apply, or the Helm upgrade, fails on it with the API's immutable-field
  error. This holds for a Job a chart renders, under Flux delivery (the post-renderer) and
  under template delivery, and for one launcher generates (the `job` component, a
  `manifests` Job). Once: delete the Job so that it is recreated with the label, or apply
  with force where the deployer supports it. A `CronJob` is not affected: its job template
  may change, and the Jobs it creates afterwards carry the label. The rule is read from
  Kubernetes 1.37's Job update validation (`validatePodTemplateUpdate` in
  `pkg/apis/batch/validation`), not run against a cluster here. Its exception is a suspended
  Job with no active pods, whose template labels may change: in 1.37 with its default
  features, one that never started or that carries the `JobSuspended` condition. Which
  suspended Jobs qualify depends on the cluster's version and feature gates.
- `GeneratedApplication.Component` changes from the application's name to empty for a
  generated source the application bundle holds and for the external-backend policy, and
  from the application's own name to the authored component for a lowered component named
  differently and for a synthesized NetworkPolicy. A collision error names those
  applications accordingly (`sub-application "db-pooler" of component "db"`).
- `stack.Application.Config` is the ownership wrapper after `Transform`: a type assertion on
  a concrete config type goes through `UnwrapConfig`.

**Breaking library changes** (go-kure/launcher#790, the authoritative label):

- A document that built before is refused at generation when an object it generates holds
  the component label's key with a value that is not its component's: on the object, on a
  pod template, in a `Cluster`'s `spec.inheritedMetadata`, a `Pooler`'s pod template,
  `spec.podMetadata` of a Prometheus operator kind, the `moverPodLabels` of a VolSync mover,
  the pod template of a cert-manager issuer's HTTP01 solver or a Gateway's
  `spec.infrastructure`, or as the value a workload's selector requires. Before, such a
  value stayed as written. The key must be one nothing else writes:
  a consumer that set `ComponentLabelKey` to a key its documents or charts also set (`app`,
  `app.kubernetes.io/name`) moves to a key of its own, such as the default.
- With `ComponentLabelKey: "app"`, what the built-in kinds and traits write still builds
  (the entry's own value, above). A chart rendered at build time, or an authored object,
  that sets `app` to anything but its component's value, or on a renamed entry that entry's,
  does not. Nor does a document in which a lowering rule emits, for one component, an entry
  whose label value is another component's: the transform refuses it.
- The reserved metadata keys are read in five more places, which the two checks share: a
  `Pooler`'s pod template, `spec.podMetadata` of a `Prometheus`, `PrometheusAgent`,
  `Alertmanager` or `ThanosRuler`, the `moverPodLabels` of each mover of a VolSync
  `ReplicationSource` or `ReplicationDestination`, the pod template of each HTTP01 solver of
  a cert-manager `Issuer` or `ClusterIssuer`, and a Gateway's `spec.infrastructure`
  ([Reserved metadata keys](#reserved-metadata-keys)). A document that set a reserved key
  there built before and is refused now.
- The reserved metadata keys are also read in the metadata an operator copies onto objects
  it creates that are no pods: a solver's Ingress template and HTTPRoute labels, a
  Certificate's `secretTemplate`, the Service, ServiceAccount and VolumeSnapshot templates of
  a CloudNativePG `Cluster`, a `Pooler`'s `serviceTemplate`, the Secret template of an
  `ExternalSecret` and a `ClusterExternalSecret`, the `externalSecretMetadata` of a
  `ClusterExternalSecret`, a `HelmRelease`'s `spec.chart.metadata`, `spec.commonMetadata` of
  an `ArtifactGenerator` and a `FluxInstance`, and the `serviceAnnotations` of a VolSync
  `ReplicationDestination`'s `rsync` and `rsyncTLS` movers
  ([Reserved metadata keys](#reserved-metadata-keys)). A document that set a reserved key
  there built before and is refused now. The component label is not read there.
- Both checks read `spec.commonMetadata` of a Flux `Kustomization` or `HelmRelease`, which
  the controller puts on every object it applies or renders, and of a Flux Operator
  `ResourceSet`, which goes onto every object it generates. A document that set a reserved
  key there, or the component label's key with a value that is not its component's, built
  before and is refused now. Launcher writes nothing there. A `ResourceSet` and a
  `FluxInstance` have no component type: the checks read them in a `manifests` or
  `passthrough` document.

**Breaking library changes** (go-kure/launcher#790, a CronJob's job template):

- Output: every `CronJob` an owned application generates, typed or unstructured, gains the
  component label on its job template (`spec.jobTemplate.metadata.labels`), and the
  post-renderer of a `HelmRelease` puts it there on a chart's CronJobs too, so each Job a
  CronJob creates carries the label. The cluster lets a CronJob's job template change: the
  apply updates a CronJob it holds already, and the Jobs it created before keep their labels.
- A document that built before is refused at generation when a CronJob's job template holds
  the component label's key with a value that is not its component's, or a reserved metadata
  key as a label or an annotation. Before, the job template's own metadata was not read.
- New exported API: `ReservedKeyInJobTemplate`, the `ReservedKeyHolder` of a CronJob's job
  template (`job template label "…"`).

**Breaking library changes** (go-kure/launcher#790, a selector that rules the label out):

- A document that built before is refused at generation when an owned workload's own selector
  rules the component label out of a pod template that carries no value for the key (a
  `DoesNotExist` on the key, or a `NotIn` holding the component's value). Before, the pod
  template stayed as written: its pods carried no component label, and no synthesized
  NetworkPolicy selected them. Take the label out of the selector. A chart Flux installs is
  not read: its post-renderer still sets the label on the pod template.
- Generation fails on an owned workload whose own selector the check cannot read (no object,
  a label selector that does not decode, a `ReplicationController` entry for the key that is
  no string), with the object and the selector's path named. Before, such a selector was
  read as requiring nothing. The error is no `*ComponentLabelError`: no value is refused. A
  `passthrough` document with such a selector is refused already, when the environment policy
  reads it; this reaches a consumer's own config.
- New exported API: `ComponentLabelSelectorRulesOut`, the `ComponentLabelRefusal` of that
  refusal.

**Breaking library changes** (go-kure/launcher#790, a renamed entry under the key `app`):

- Output: with `ComponentLabelKey: "app"`, the synthesized inbound and egress NetworkPolicies
  of an entry a lowering rule emitted under a name whose label value differs from its
  component's select `app In [<component>, <entry>]` (`matchExpressions`) instead of
  `matchLabels` with the component's value. The entry's own pods, which the kinds label
  with the entry's value, are now inside them. No other key and no other entry changes.

**New exported API** (go-kure/launcher#790, the authoritative label): the sentinel
`ErrComponentLabelValue`, the error type `ComponentLabelError`, the type
`ComponentLabelRefusal` with its values `ComponentLabelForeignValue`,
`ComponentLabelSelectorRequiresAnother`, `ComponentLabelInLabelsProperty` and
`ComponentLabelOfAnotherComponent`, five values of `ReservedKeyHolder`:
`ReservedKeyInPodMetadata`, `ReservedKeyInMoverPodLabels`,
`ReservedKeyInSolverPodTemplate`, `ReservedKeyInInfrastructure` and
`ReservedKeyInCommonMetadata`, and the field `ReservedMetadataKeyError.Path`. The refusal of
a kind component's `labels` property that holds another value under the key, which the
transform returned before as an error of no type, is a `*ComponentLabelError` now, with the
same text. Under the key `app` the kinds' own `app` check refused such a value first, with a
text of its own; that refusal is the component label's now, text included.

**New exported API** (go-kure/launcher#790, metadata that reaches no pods): nine values of
`ReservedKeyHolder`: `ReservedKeyInSolverIngressTemplate`, `ReservedKeyInSolverHTTPRoute`,
`ReservedKeyInSecretTemplate`, `ReservedKeyInServiceTemplate`,
`ReservedKeyInServiceAccountTemplate`, `ReservedKeyInVolumeSnapshot`,
`ReservedKeyInExternalSecretMetadata`, `ReservedKeyInChartTemplate` and
`ReservedKeyInMoverService`.

**Breaking library changes** (go-kure/launcher#944, a StatefulSet's volume claim templates):

- A document that built before is refused at generation when a volume claim template of a
  `StatefulSet` (`spec.volumeClaimTemplates[].metadata`) holds a reserved metadata key as a
  label or an annotation. Before, a volume claim template's metadata was not read. The
  `statefulset` kind writes nothing there but the claim's name, so this reaches a
  `passthrough` or `manifests` document, a chart rendered at build time and a consumer's own
  config.
- The component label is neither read nor written there, as before: another component's
  value builds, and the output does not change.
- New exported API: `ReservedKeyInVolumeClaimTemplate`, the `ReservedKeyHolder` of a volume
  claim template (`volume claim template label "…"`).

**Breaking library changes** (go-kure/launcher#957, a Prometheus operator kind's storage claim
template):

- A document that built before is refused at generation when the storage claim template of a
  `monitoring.coreos.com` `Prometheus`, `PrometheusAgent`, `Alertmanager` or `ThanosRuler`
  (`spec.storage.volumeClaimTemplate.metadata`) holds a reserved metadata key as a label or an
  annotation, and neither `spec.storage.emptyDir` nor `spec.storage.ephemeral` is set (beside
  either, the operator makes no claim from the template). Before, it was not read. This
  reaches the `alertmanager` kind's `storage`
  property, a `passthrough` or `manifests` document, a chart rendered at build time and a
  consumer's own config. The refusal names the holder `ReservedKeyInVolumeClaimTemplate`
  (`volume claim template label "…"`); no new API.
- The component label is neither read nor written there, as before, and the claim template of
  ephemeral storage (`spec.storage.ephemeral.volumeClaimTemplate`) stays unread.

## Reserved metadata keys

A consumer that keeps label and annotation keys to itself names them in
`TransformContext.ReservedMetadataKeys` (go-kure/launcher#790). One rule holds every
application the transform returns to the list, whatever wrote the key: a kind component, a
`passthrough` or `manifests` object, a chart rendered at build time, a trait's `annotations`,
a `postgresql` or `cnpg-cluster` component's `inheritedMetadata`.

**The list.** Each entry is one of:

- a key: a Kubernetes qualified name (`example.org/tenant`, `team`), which reserves that key;
- a prefix followed by `/`: a DNS-1123 subdomain (`platform.example/`), which reserves every
  key under that prefix. It does not reserve the prefix's own name as a key, or a key under
  another domain that ends alike (`sub.platform.example/zone`).

`Transform` refuses any other entry, naming it by index
(`invalid TransformContext.ReservedMetadataKeys[1] "not a key": ...`). A repeated entry, or a
key a prefix entry already covers, is accepted. An empty list reserves nothing, and the
transform and its output are then as without the field.

**Where it is refused.** At generation, not in `Transform`: the check sits in the ownership
wrapper ([Component label and ownership](#component-label-and-ownership)), so it reads each
object as its config generated it, in `GenerateApplications` (or `Generate` on an
application), and each object the config adds to a layout it augments
(`layout.LayoutAugmenter`): one that was not on the layout, or on a layout below it, before
the config's `AugmentLayout` ran. The error names the component, the object, the key and the
entry that reserves it. Its text is the document author's to read, so it names no Go field
and says the key is reserved for the platform:

```text
component "web": Ingress "web-ingress": annotation "platform.example/zone" may not be set: the prefix "platform.example/" is reserved for the platform: oam: metadata key is reserved
```

A key reserved by an entry of its own reads `the key is reserved for the platform`. An
application the document as a whole owns (a generated source several components share) is
checked too, and the refusal names `the document` in place of a component.

**Recognising it.** The error is a `*ReservedMetadataKeyError`, and answers to the sentinel
`ErrReservedMetadataKey`, which it unwraps to:

```go
var refused *oam.ReservedMetadataKeyError
if errors.As(err, &refused) {
    // refused.Component, refused.Kind, refused.Namespace, refused.Name,
    // refused.Holder, refused.Path, refused.Annotation, refused.Key, refused.Entry
}
```

`Component` is empty for an object the document as a whole owns. `Kind` (group and kind),
`Namespace` and `Name` are the object's as it was generated: the namespace is empty when the
object states none, and the kind is zero for a typed object that states none and that the
check reads only the metadata of, which `Object` (the object as the text names it) then names
by its Go type. On a list envelope they are the member's. `Holder` says which metadata holds
the key: `ReservedKeyInObjectMetadata`, `ReservedKeyInPodTemplate`, `ReservedKeyInJobTemplate`,
`ReservedKeyInInheritedMetadata`, `ReservedKeyInPodMetadata`, `ReservedKeyInMoverPodLabels`,
`ReservedKeyInSolverPodTemplate`, `ReservedKeyInInfrastructure` or
`ReservedKeyInCommonMetadata` (an ArtifactGenerator's as well, which reaches no pods), or, of
metadata that reaches no pods, `ReservedKeyInSolverIngressTemplate`, `ReservedKeyInSolverHTTPRoute`,
`ReservedKeyInSecretTemplate`, `ReservedKeyInServiceTemplate`,
`ReservedKeyInServiceAccountTemplate`, `ReservedKeyInVolumeSnapshot`,
`ReservedKeyInExternalSecretMetadata`, `ReservedKeyInChartTemplate`,
`ReservedKeyInMoverService` or `ReservedKeyInVolumeClaimTemplate`. `Path` is where the labels
or the annotations that hold the key are on the object (`metadata.labels`,
`spec.template.metadata.annotations`), the map itself where the field is a map of labels or
of annotations (`spec.rsync.serviceAnnotations`), with the index of the element where they
are in a list (`spec.acme.solvers[1].http01.ingress.podTemplate.metadata.labels`); the text
prints it only there, in parentheses after the key. `Annotation` is false for a label. `Entry` is the entry
that reserves `Key`: the key itself, or the prefix it is under.
Metadata the check cannot read fails generation with another error, which is neither.

**What is read.** On every object, and on each member of an unstructured list envelope as
Flux applies it (a `List`, or an envelope with `items`):

- the object's own `metadata.labels` and `metadata.annotations`;
- the pod template's labels and annotations, on a `Deployment`, `StatefulSet`, `DaemonSet`,
  `Job`, `ReplicaSet`, `ReplicationController` or `PodTemplate`, and the job template's pod
  template on a `CronJob`, each in its own API group: they become the metadata of the pods;
- the job template's labels and annotations on a `batch` `CronJob`
  (`spec.jobTemplate.metadata`), which become the metadata of each Job it creates: `job
  template label "…"`;
- `spec.inheritedMetadata` of a `postgresql.cnpg.io` `Cluster`, which the operator copies
  onto every object it creates for the cluster;
- the pod template's labels and annotations of a `postgresql.cnpg.io` `Pooler`
  (`spec.template.metadata`), named as a pod template's;
- `spec.podMetadata` of a `monitoring.coreos.com` `Prometheus`, `PrometheusAgent`,
  `Alertmanager` or `ThanosRuler`, which the operator puts on the pods it creates;
- `moverPodLabels` of each mover of a `volsync.backube` `ReplicationSource` or
  `ReplicationDestination` (`spec.restic.moverPodLabels` and its like), labels of the pods
  that move the data: `mover pod label "…"`;
- the pod template's labels and annotations of each HTTP01 solver of a `cert-manager.io`
  `Issuer` or `ClusterIssuer` (`spec.acme.solvers[].http01.ingress.podTemplate.metadata`,
  and the same under `gatewayHTTPRoute`), in every solver of the list: `solver pod template
  label "…"`;
- the labels and annotations of `spec.infrastructure` of a `gateway.networking.k8s.io`
  `Gateway`, which the controller applies to what it creates for the Gateway;
- the labels and annotations of `spec.commonMetadata` of a `kustomize.toolkit.fluxcd.io`
  `Kustomization` or a `helm.toolkit.fluxcd.io` `HelmRelease`, which the Flux controller puts
  on every object it applies or renders, and of a `fluxcd.controlplane.io` `ResourceSet`,
  which the Flux Operator puts on every object it generates: `spec.commonMetadata label
  "…"`;
- metadata an operator copies onto objects it creates that are no pods:
  - of each HTTP01 solver of an `Issuer` or `ClusterIssuer`, in every solver of the list,
    the Ingress template's labels and annotations
    (`spec.acme.solvers[].http01.ingress.ingressTemplate.metadata`: `solver ingress template
    label "…"`) and the labels of the HTTPRoute of a solver on a Gateway
    (`spec.acme.solvers[].http01.gatewayHTTPRoute.labels`: `solver HTTPRoute label "…"`);
  - the Secret template of a `cert-manager.io` `Certificate` (`spec.secretTemplate`), an
    `external-secrets.io` `ExternalSecret` (`spec.target.template.metadata`) and a
    `ClusterExternalSecret` (`spec.externalSecretSpec.target.template.metadata`): `secret
    template label "…"`;
  - `spec.externalSecretMetadata` of a `ClusterExternalSecret`, which goes onto the
    ExternalSecrets it creates;
  - of a `postgresql.cnpg.io` `Cluster`, the service template of each additional Service
    (`spec.managed.services.additional[].serviceTemplate.metadata`) and of a `Pooler`
    (`spec.serviceTemplate.metadata`): `service template label "…"`; the service account
    template (`spec.serviceAccountTemplate.metadata`) and the labels and annotations of the
    VolumeSnapshots it takes (`spec.backup.volumeSnapshot`);
  - `spec.chart.metadata` of a `helm.toolkit.fluxcd.io` `HelmRelease`, which goes onto the
    HelmChart the controller creates: `chart template label "…"`;
  - `spec.commonMetadata` of a `source.extensions.fluxcd.io` `ArtifactGenerator`, which goes
    onto the ExternalArtifacts it generates, and of a `fluxcd.controlplane.io`
    `FluxInstance`, which goes onto the objects of the Flux installation (Deployments,
    Services and the like, not their pod templates): `spec.commonMetadata label "…"`;
  - `serviceAnnotations` of the `rsync` and `rsyncTLS` movers of a `volsync.backube`
    `ReplicationDestination`, which go onto the mover's Service: `mover service annotation
    "…"`;
  - the metadata of each volume claim template of an `apps` `StatefulSet`
    (`spec.volumeClaimTemplates[].metadata`), in every template of the list, which the
    StatefulSet controller copies onto each PersistentVolumeClaim it creates: `volume claim
    template label "…"` (go-kure/launcher#944);
  - the metadata of the storage claim template of a `monitoring.coreos.com` `Prometheus`,
    `PrometheusAgent`, `Alertmanager` or `ThanosRuler`
    (`spec.storage.volumeClaimTemplate.metadata`), which the operator copies onto the volume
    claim template of its StatefulSet: `volume claim template label "…"`
    (go-kure/launcher#957), where neither `spec.storage.emptyDir` nor `spec.storage.ephemeral`
    is set, the operator using the first storage arm set. The claim template of its ephemeral storage
    (`spec.storage.ephemeral.volumeClaimTemplate`) goes onto a claim of a pod and is not read.

These are the places the component label is held to its value in
([Component label and ownership](#component-label-and-ownership)), but the metadata that
reaches no pods: one list serves both checks. A typed object of a `monitoring.coreos.com`,
`volsync.backube`, `cert-manager.io`, `gateway.networking.k8s.io` or `external-secrets.io`
kind is recognized when it states its kind. A typed Flux `Kustomization`, `HelmRelease` or
`ArtifactGenerator` is recognized by its Go type as well, whether or not it states its kind.
A `ResourceSet` and a `FluxInstance` have no component type; the checks read them in a
`manifests` or `passthrough` document by the kind it states.

A key is read whatever its value: a value the API server would refuse, or a null, does not
hide it. Metadata that cannot be read (a `labels` that is a list) fails generation with the
object named, and is not read as holding no key.

**What launcher writes itself is exempt.**

- The `app` label and the component label key
  (`TransformContext.ComponentLabelKey`, else `<Domain>/component`), as label keys, wherever
  the check reads labels. The built-in components and traits write the first on what they
  generate, and a consumer may well configure the second under a prefix it reserves. Both
  stay checked as annotation keys, and another key under the same prefix stays reserved.
  The exemption is of the key: the component label's value is held to the component's by the
  wrapper's other check.
- The component label the wrapper stamps, and a bundle's labels and annotations, are added
  after the check and never read.
- The annotations the platform sets on an Ingress: the ones the `expose` rule writes from a
  capability value or one of its typed properties (`cert-manager.io/cluster-issuer`,
  `nginx.ingress.kubernetes.io/ssl-redirect`, `force-ssl-redirect`, `auth-url`,
  `auth-signin`, `auth-response-headers`), and the ones a rendering of the `ingress`
  capability supplies (a map of strings, a `map[string]string` as well as a
  `map[string]any`). They reach the `ingress` trait in its platform-reserved
  `platformAnnotations` property, apart from the authored `annotations`, and pass as that key
  **and value**, on the Ingress's own annotations only. An authored annotation of such a key
  must hold the same value (it then says what the platform says, and passes); another value
  is refused by the trait, naming the annotation and where the platform's value comes from.
  Any other annotation under a reserved prefix is an authored one and is refused, so a
  consumer can reserve `nginx.ingress.kubernetes.io/` and keep `expose` working.

**What the check does not cover.**

- A chart Flux installs (`helmrelease`, `helm` under `flux` delivery) is rendered in the
  cluster, where launcher reads nothing. The `HelmRelease` object itself is checked.
- Metadata an object hands on to others in a field of its own: the claim template of an
  ephemeral volume, in a pod spec or a CloudNativePG `Cluster`'s
  `spec.ephemeralVolumeSource`. `labelReachNotRead` in `pkg/cmd/kurel/label_reach_test.go` is
  the full list of fields of the kinds' API types that hand metadata on and are not read.
- What a controller or an admission webhook adds in the cluster.
- An application a caller adds to the cluster itself after `Transform`: it has no ownership
  wrapper.
- What a config that a caller wraps around an application's config after `Transform` adds: a
  key on an object the application generated, or an object of its own. The check reads below
  the ownership wrapper, never above it, so a consumer can label the objects under a prefix
  it reserved, on a component that augments its layout as on any other.
- On a layout the config augments, what was there before its `AugmentLayout` ran. The layout
  walker puts there what the application generated, checked at generation. Two cases follow:
  an object already on the layout that the config's `AugmentLayout` edits in place is not read
  again (no built-in component edits one), and a caller that calls `AugmentLayout` itself on
  a layout holding objects that never came from `Generate` gets them unchecked.

**Library changes** (go-kure/launcher#790): a new exported field,
`TransformContext.ReservedMetadataKeys`, the sentinel `ErrReservedMetadataKey`, the error
type `ReservedMetadataKeyError` with its `ReservedKeyHolder` values, and the
method `PlatformAnnotations()` on the `ingress` trait's config (`traits.IngressConfig`).
The check reads that method through an unexported contract of this package, which a config
of another package can only meet with an exported method. A consumer's own config that
writes annotations from the platform's input, apart from authored ones, may implement the
same method. The check asks the application's config and every config under it that a
`ConfigWrapper` says it holds, down to what `UnwrapConfig` returns (at most 32 layers, and a
nil one ends it), and passes a pair any of them states. So a trait that wraps a
sub-application's config (none of the built-in ones does: `prune-protection` and
`force-replace` set a delivery intent and leave the config) keeps `expose` working under a
reserved prefix by being a `ConfigWrapper`, with no method to hand on. Under a wrapper that
does not say it wraps, the pairs are checked as authored.
**Breaking for documents:** an `expose` trait whose authored annotation
contradicts a value the trait writes is now refused where the trait's value used to win
silently (`pkg/oam/builtin/traits/README.md`, "Capability-aware traits").

