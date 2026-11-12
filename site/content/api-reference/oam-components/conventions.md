---
title: "Conventions"
weight: 16
---

## Conventions

Handlers use `k8s.io/api` constants for well-known Kubernetes enum values (access
modes, restart policies, etc.) rather than string literals — never re-define values
that already exist upstream.

Every generated object owns its label maps. `Generate` hands back objects the caller
owns and routinely edits — stamping ownership, environment or version labels onto the
workloads it emits — so no two fields and no two objects ever share one
`map[string]string`. Concretely: the object's `metadata.labels`, the pod template's
`metadata.labels`, the Service selector and the `labelSelector` of every topology
spread constraint and pod anti-affinity term are separate maps with equal contents.
Inside this package `appLabels` and `selectorFrom` (both unexported, `common.go`) are
what enforce that: call `appLabels` once per assignment rather than hoisting its result
into a variable used twice, and let `selectorFrom` copy what it is handed rather than
store it. A custom handler outside this package cannot call either and does not need to
— the rule is to build a fresh `map[string]string` at each assignment site, and to
`maps.Clone` any map before storing it in a selector.

This is a correctness rule, not tidiness. A selector that aliases the pod template's
labels follows the caller's edits, and a topology spread constraint's `labelSelector`
defines the pod set over which skew is computed — so a version label leaking into it
makes a rollout spread each version separately instead of spreading the workload.

The same rule runs in the other direction: **a generated object never shares a pointer
or a map with the config that produced it.** A handler config is reusable — the same one
can be rendered more than once — so every value a `*SpecConfig.apply` projects onto a
generated object is deep-copied on the way out: the claim template's `selector`,
`resources.limits`, `dataSourceRef`, `volumeMode` and `volumeAttributesClassName`, and at
the workload level `updateStrategy` (whose struct holds a pointer, so `*c.UpdateStrategy`
alone would not be enough), `revisionHistoryLimit`, `persistentVolumeClaimRetentionPolicy`
and `ordinals`. The deployment kind projects the same two shapes and follows the same rule:
`strategy` (holding a `*RollingUpdateDeployment`), `revisionHistoryLimit` and
`progressDeadlineSeconds`. `applyJobSpec` follows the same rule for the `job` and `cronjob`
kinds: its ten scalar pointers go through the generic `copyPtr`, and `successPolicy` and
`podFailurePolicy` — whose structs each own a slice of rules — through `DeepCopy`.
The pod template every workload kind shares follows it too: `buildPodSpec` starts from a
`DeepCopy` of the authored pod-level fields (`nodeSelector`, `podSecurityContext`,
`terminationGracePeriodSeconds`, `imagePullSecrets`, …) and deep-copies each volume,
toleration, topology spread constraint, the affinity, and the main, init and sidecar
containers as it adds them — an `append` alone copies the element structs but shares the
pointers, maps and slices they carry. The `postgresql` Cluster follows it for
`spec.inheritedMetadata`: its `labels` and `annotations` are clones of the config's
`InheritedLabels`/`InheritedAnnotations`, not the maps themselves
(go-kure/launcher#396). Without that, editing the first rendered object — the same in-place
customization the label rule above assumes — writes back into the config and reappears in
every later render, with the symptom surfacing on a different object than the one that was
edited.

### The `app` label

Every `app` label and `app` selector this package generates to identify a component is
valued at `oam.ComponentLabelValue(<component>)`, never the raw component name —
`appLabels` and `deploymentComponentLabels` call it, and nothing else writes the value.
Authored values are emitted as written: an authored `selector` on a `service` component
replaces the generated one and is not projected, and a type that emits authored objects
(`passthrough`, for one) adds no `app` label. The one authored value that is checked is
an `app` label on the pod template of a `replicaset` or `replicationcontroller` component, which gets the generated
label beside its authored ones: the component's own label value is kept, another is
refused. A component name
is a DNS-1123 subdomain (up to 253 characters), a label value at most 63: the function
returns a name of 63 characters or fewer unchanged, so output for those names is
byte-identical, and projects a longer one onto a readable prefix of at most 52 characters
(its first 52, with trailing `-` and `.` trimmed) plus `-` and
a 10-hex-character sha256 digest (go-kure/launcher#572). The workload kinds and `service` never reach
the projection, since their container name or Service name already refuses a name over 63
characters; the `helm` values ConfigMap and values Secret do (a values
ConfigMap that previously omitted `app` past 63 characters now carries the projected value). Object names are not projected. A custom handler
that labels its objects by component uses the same function, so its selectors and the
built-in traits' selectors (a PodDisruptionBudget, a NetworkPolicy `podSelector`) agree.

The component label (`<domain>/component`) is not this package's to write: the transform
adds it to every object a component's config generates, and to its pod templates, with the
authored component's value, where the key is absent (go-kure/launcher#788). Where the key is
there with another value, the transform's output is refused when it is generated
(go-kure/launcher#790): that holds for what a component type of this package emits from
authored objects or a rendered chart (`passthrough`, `manifests`, `helmtemplate`, `helm`
under template delivery) as for any other, so the key is one nothing else writes. A lowered part
named differently from its component (the `postgresql` pooler, a database, an object store)
carries the component's value, not its own name. A source a lowering rule generates and the
application bundle holds (the repository a `helm` component generates) carries none. A
`HelmRelease` also gets a post-renderer that sets the label on the chart's pod templates.
See "Component label and ownership" in the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam).

### The object name (`objectName`)

Every kind component takes `objectName`, which names its one object in place of the component
name (go-kure/launcher#787): every component type with a handler
(`builtinComponentHandlers`) but four, as `TestObjectName_EveryComponentTypeChooses`
(`pkg/cmd/kurel`) holds; a type that is lowered to others has none. `helmtemplate`,
`manifests`, `crd` and `passthrough` generate no
single object named after the component and refuse it. The rules for the name, the `Naming`
hook's role `object` and the references that follow it are under "`objectName`: the object of
a kind component" in the
[OAM model](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam).

The property is not in any handler's `PropertySchema`: the engine adds it to the schema of
every type whose handler declares its object (`ComponentObject`, in `object_name.go` or, for
a kind added since, in the kind's own file),
reads it, and hands `ToApplicationConfig` the resolved name as `Component.ObjectName()`. A
handler's config carries it as `ObjectName`, empty when it is the component's name, and
`Generate` names the object with it. Everything else the handler writes keeps the component
name: the `app` label and selectors, the pod template's labels, the main container's name;
the one exception is the pod selector of a `cnpg-cluster` or `cnpg-pooler` endpoint, below.
A handler's own check of the name (a Service's DNS-1123 label, a CloudNativePG Cluster's
length, a CronJob's 52 characters, a Job's 63 and the room an Indexed job's last index
needs) runs on the object name, since that is what the object carries.

What a config tells a trait or the transform about its object follows the object name: a
`service` component's `BackendServiceName`, a `serviceaccount` component's
`ServiceAccountName`, and the pod selector of a `cnpg-cluster` or `cnpg-pooler` endpoint. Two
consequences are the kind's own: a `statefulset`'s pods and claims take the StatefulSet's
name, so they follow `objectName`; a `helmrelease`'s `spec.releaseName` default stays the
component name, so the release does not.

### The object's labels and annotations (`labels`, `annotations`)

Every kind component, the same types that take `objectName`, takes two optional properties,
`labels` and `annotations`, each a map of strings (go-kure/launcher#790). They go on the
metadata of the component's one object and nowhere else. On a workload kind the pod template
keeps the labels of the kind, its `app` label and what `template.metadata` authors, and no
selector reads an authored label. `helmtemplate`, `manifests`, `crd` and `passthrough`
generate no single object named after the component and refuse both. A top-level spec
field named `labels` therefore cannot keep its name: a `thanosruler`'s Prometheus external
labels are its `externalLabels` property (see **thanosruler**); a `prometheus`'s are its
spec's own `externalLabels` field, under its upstream name.

```yaml
- name: web
  type: deployment
  properties:
    image: ghcr.io/example/web:1.0.0
    labels:
      team: payments
    annotations:
      example.com/owner: "payments@example.com"
```

Refused, naming the component, the property and the key:

- a value that is no map, and an entry that is no string (quote a number or a boolean); a
  null value or entry is an absent one;
- a key or value the API server refuses: a label key or value that is no valid label, an
  annotation key that is no valid annotation key, annotations whose keys and values hold more
  than 262144 bytes together;
- the `app` label with another value than the component's (its name, or the hashed form of a
  name over 63 characters), which is what the kinds that set `app` select by;
- the component label key with another value than the component's (see "Component label and
  ownership" above);
- a key the kind already sets on its object with another value;
- a key the consumer reserved for itself (`ReservedMetadataKeys`), as on every generated
  object.

A label that names another object is the author's literal: it does not follow that object's
`objectName` or the `Naming` hook's answer for it. An object that belongs to another through
a label (an EndpointSlice's `kubernetes.io/service-name`) is written with the name the other
object takes.

An annotation a delivery engine acts on (a Flux annotation such as
`kustomize.toolkit.fluxcd.io/force`, for one) is written as authored too: launcher does not
refuse it and does not carry out what the engine does with it. `Transformer.WarnForcedVolumes`
warns about a PersistentVolume or PersistentVolumeClaim that has the force key enabled, and
changes nothing. A consumer that keeps such keys to itself reserves them with
`TransformContext.ReservedMetadataKeys`.

As `objectName`, the two are in no handler's `PropertySchema`: the engine adds them to the
schema of every type whose handler declares its object, reads and checks them, removes them
from the properties and hands `ToApplicationConfig` the result as
`Component.ObjectMetadata()`. A handler's config carries it as `Metadata` and `Generate` puts
it on the object through `kindObject`, the one helper every kind returns its object through
(the Flux sources through `emitFluxSource`, which does the same before it writes a long
`spec.timeout`). `TestObjectMetadata_EveryKindComponentTakesIt` holds every registered kind
to the two properties, so a kind added later cannot ship without them.

### The pod template's labels and annotations (`podMetadata`)

`labels` and `annotations` above go on the object and never on a pod template. The five kinds
whose spec holds a pod template the author does not write whole (`deployment`, `statefulset`,
`daemonset`, `job` and `cronjob`) take a fourth property, `podMetadata`, for the pod template's
own metadata (go-kure/launcher#1178). On a `cronjob` it is the pod template inside the job
template, not the CronJob's or the job template's metadata. `webservice` and `worker` take it
too (go-kure/launcher#1216), with the same name and shape as `deployment`: the rule hands it to
its deployment member, so it lands on that Deployment's pod template and nowhere else. The
Service a `webservice` generates selects on the component's `app` label alone, so an authored
label is on the pods and not in the Service's selector. A document moves a workload between
`webservice`, `worker` and `deployment` without renaming it.

```yaml
properties:
  image: ghcr.io/example/web:v1.0.0
  podMetadata:
    labels:
      example.com/team: payments
    annotations:
      example.com/owner: payments team
```

- **What it does.** The kind writes its pod template labels first and the authored ones are added
  beside them. A change to either map is a pod template change, so the controller's update
  strategy decides whether it reaches the pods that exist (a rolling update replaces them,
  `OnDelete` waits for a deletion, and a `cronjob` only changes the Jobs it creates later). A
  `job` has no strategy: the API server refuses a change to the pod template of a Job that is
  not suspended.
- **What it never does.** It does not reach the object's metadata, the selector or any other
  object. The selector is a map of its own (`appLabels`, `deploymentComponentLabels`), so an
  authored label cannot change which pods a workload owns.
- **Refused where the component is read.** A key beside `labels` and `annotations`; a label key
  or value that is no valid label; a value that is no string; an annotation key that is no valid
  annotation key; annotations over the size the API server accepts (counted again with the
  annotations the kind sets); and, on `job` and `cronjob` only, the four labels the Job controller
  owns (`batch.kubernetes.io/controller-uid`, `batch.kubernetes.io/job-name`, `controller-uid`,
  `job-name`), since the API server generates the selector of a Job from them. That last refusal
  can be loosened later without invalidating a document.
- **Refused where the object is generated.** `app`, or any other key the kind sets, with another
  value than the one the kind writes; writing the kind's own value is accepted.
- **Checked as on any pod template.** A key the consumer reserves
  (`TransformContext.ReservedMetadataKeys`) is refused, and the component label is stamped and
  held, by the checks that already read every generated pod template. A kubernetes.io or
  k8s.io key is not refused here: the API server and the Job controller own what they own.
- **Traits.** The traits written so far change the pod spec only, so they leave the authored keys
  as they are.

The property is an addition to the document format: a document that wrote none is unchanged, and
the keys refused above were not expressible before.

### A label selector's match expressions

A kind component whose spec holds a Kubernetes label selector (`metav1.LabelSelector`) decodes
it strictly, and the strict decode refuses nothing of a match expression: the Go type writes
`key` and `operator` whether or not they were authored, and reads any string as an operator.
Three defects of an expression are therefore refused where the component is read, with or
without an environment policy, each named by its path (go-kure/launcher#790):

- **An expression without its `key` or its `operator`**
  (`selector.matchExpressions[0].operator: required (the operator of the expression: In, NotIn,
  Exists or DoesNotExist)`). The object would carry `key: ""` or `operator: ""` and not show
  the omission.
- **An `operator` that is none of the four**
  (`selector.matchExpressions[0].operator: "Equals" is not a label selector operator (In, NotIn,
  Exists or DoesNotExist)`).
- **An operator and `values` that do not go together**: `In` or `NotIn` without a value
  (`selector.matchExpressions[0].values: required with the operator In (at least one value)`),
  `Exists` or `DoesNotExist` with one (`…values: not allowed with the operator Exists (it takes
  no value)`).

The API server refuses each of the three on every selector listed below, through one function,
`ValidateLabelSelectorRequirement` of `k8s.io/apimachinery`, and under every option it passes
that function. `TestValidateLabelSelector_MatchesAPIMachinery` holds launcher's check to the
linked function on those three.

**That function judges more, and these kinds do not.** It also refuses an expression `key` that
is no label name, an expression value that is no label value (on the objects that still check
it), and a `matchLabels` key or value that is no label name or value. Those are value rules,
left to the API server as for every typed kind: a selector with `key: "not a label"` builds
here and is refused at apply. An authored empty `key` (`key: ""`) is one of them: it differs
from an unauthored key only in that the unauthored one is refused here, and the API server
refuses both.

**Where it holds:**

| Kind | Selectors |
|---|---|
| `networkpolicy` | `podSelector`; the `podSelector` and `namespaceSelector` of every `ingress[].from[]` and `egress[].to[]` peer |
| `poddisruptionbudget` | `selector` |
| `clusterrole` | every entry of `aggregationRule.clusterRoleSelectors` |
| `validatingwebhookconfiguration`, `mutatingwebhookconfiguration` | the `namespaceSelector` and `objectSelector` of every webhook |
| `validatingadmissionpolicy`, `mutatingadmissionpolicy` | the `namespaceSelector` and `objectSelector` of `matchConstraints` |
| `validatingadmissionpolicybinding`, `mutatingadmissionpolicybinding` | `paramRef.selector`; the `namespaceSelector` and `objectSelector` of `matchResources` |
| `pod`, and under `template.spec` `podtemplate`, `replicaset` and `replicationcontroller` | the `labelSelector` and `namespaceSelector` of every `podAffinity` and `podAntiAffinity` term, required or preferred; a topology spread constraint's `labelSelector`; a projected `clusterTrustBundle` source's `labelSelector`; the `selector` of a generic ephemeral volume's claim template |

A `replicaset`'s own `selector` is refused for the same defects by the check that compares it
with the `app` label, with apimachinery's reason and not the texts above (see its entry);
`TestReplicaSetSelector_RefusedAsAPIMachineryReadsIt` holds that to each of them. The Cilium
kinds hold the `key` and `operator` of Cilium's own selector type through the same required
list, with the same wording; their entries say what else Cilium requires.

**Presence only, where a CRD defines the object.** On the kinds below an expression without its
`key` or its `operator` is refused, by the same required list and in the same words. The CRD
requires both fields, and the Go type writes each whether or not it was authored, so the object
would not show the omission: the document as authored is one the API server refuses, and the
kind refuses the omission by presence, as it does every required field its type writes
unauthored. An authored empty string is a value, left to the API server. Nothing else of the
expression is refused: an operator that is none of the four and a mismatched `values` are left
to the API server and to the operator's own admission, since a CRD's schema requires the two
fields and says nothing of the pair.

| Kind | Selectors | Ground |
|---|---|---|
| `servicemonitor`, `podmonitor` | `selector` | the linked type, by the generator's rule (below) |
| `prometheus-probe` | `targets.ingress.selector` | the same |
| `alertmanager` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; `alertmanagerConfigSelector`; `alertmanagerConfigNamespaceSelector` | the same |
| `thanosruler` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; `ruleSelector`; `ruleNamespaceSelector` | the same |
| `prometheus` | the selectors a `pod` holds, at the top of the spec; the `selector` of the claim template of `storage.volumeClaimTemplate` and of `storage.ephemeral`; the selector and the namespace selector of the ServiceMonitors, PodMonitors, Probes, ScrapeConfigs and rules it reads (`serviceMonitorSelector`, `serviceMonitorNamespaceSelector`, `podMonitorSelector`, `podMonitorNamespaceSelector`, `probeSelector`, `probeNamespaceSelector`, `scrapeConfigSelector`, `scrapeConfigNamespaceSelector`, `ruleSelector`, `ruleNamespaceSelector`) | the same |
| `cnpg-cluster` | the `labelSelector` and `namespaceSelector` of every `affinity.additionalPodAffinity` and `affinity.additionalPodAntiAffinity` term, required or preferred; `topologySpreadConstraints[].labelSelector`; a `projectedVolumeTemplate` `clusterTrustBundle` source's `labelSelector`; the `selector` of `ephemeralVolumeSource.volumeClaimTemplate.spec` and of the `pvcTemplate` of `storage`, `walStorage` and each tablespace; `podSelectorRefs[].selector` | the Cluster CRD of the linked module |
| `cnpg-pooler` | under `template.spec`, the selectors a `pod` holds | the Pooler CRD of the linked module |
| `issuer`, `clusterissuer` | the `labelSelector` and `namespaceSelector` of every pod affinity and anti-affinity term of an HTTP01 solver's `podTemplate.spec.affinity`, under `ingress` and under `gatewayHTTPRoute` | the Issuer and ClusterIssuer CRDs of the linked module |
| `gateway`, `listenerset` | a listener's `allowedRoutes.namespaces.selector`; a Gateway's `allowedListeners.namespaces.selector` | the CRDs of both Gateway API channels |
| `cilium-nodeconfig` | `nodeSelector` | the CiliumNodeConfig CRD of the linked module |
| `secretstore`, `clustersecretstore` | `conditions[].namespaceSelector` | the linked type, by the generator's rule (below) |
| `clusterexternalsecret` | `namespaceSelector`; every entry of `namespaceSelectors` | the same |
| `imageupdateautomation` | `policySelector` | the same |
| `resourcesetinputprovider` | every entry of `selectors` | the ResourceSetInputProvider CRD of the linked module, and the linked type, by the generator's rule |
| `replicationsource`, `replicationdestination` | the `labelSelector` and `namespaceSelector` of every pod affinity and anti-affinity term of a mover's `moverAffinity`, required or preferred: `rclone`, `restic`, `rsyncTLS`, and on a source `syncthing` | the ReplicationSource and ReplicationDestination CRDs of the linked module |

The Prometheus operator's module, the External Secrets Operator's and Flux's image automation
module ship no CRD to read, so the ground of their kinds is the source of the linked
`metav1.LabelSelectorRequirement`; the Flux Operator's module ships its CRDs, and its kind is
held to both, to its CRD and, as the other Flux kinds are, to that source: the schema generators
require a field that carries no optional marker and whose json tag keeps it when empty, and
`key` and `operator` are such fields
where `values` is not. `TestExternalSecretsKinds_RequiredMatchSource` derives the two from
that source by that rule, beside the fields the operator's own types mark required, and holds
each kind's list to them. The monitoring and Flux kinds' lists are generated from the same
source by the same rule
([Required fields a kind writes unauthored and does not refuse](#required-fields-a-kind-writes-unauthored-and-does-not-refuse)),
which also requires every other field of an embedded Kubernetes type the rule requires (a
container's `name`, a Secret key reference's `key`). CloudNativePG's admission
webhook runs apimachinery's whole selector validation on `podSelectorRefs[].selector`; that
fuller check is the operator's, not launcher's.

**Not held: the metric selectors of a `horizontalpodautoscaler`**
(`metrics[].object.metric.selector`, `metrics[].pods.metric.selector`,
`metrics[].external.metric.selector`). The API server's validation of an autoscaler does not
read them, and a kind does not refuse what the API admits.

A config that is exported (`NetworkPolicyConfig`, `PodConfig`, `PodTemplateConfig`,
`ReplicaSetConfig`, `ReplicationControllerConfig`) repeats at `Generate` the two checks its
typed value can show: the operator, an empty one included, and the pair. A missing `key` shows
only in what was authored, so it is refused where the component is read.
`TestLabelSelectorKinds_CoverEverySelector` walks the type of every strictly decoded kind and
fails on a selector path that is neither held nor listed as left out with its reason, so a
selector added by a dependency bump or a new kind cannot go unread. For a presence-only kind it
also reads the CRDs named above, fails where one does not require `key` and `operator` at a
held path, and asks the API server's own validation of a custom resource at each: the object
the kind emits for a whole expression is accepted, and the same object with the `key` or the
`operator` taken out is refused for that field alone. With the field as the empty string the
type writes, the object is accepted again, which is why the omission is refused where the
component is read.

The walk covers the kinds that decode a type. The hand-parsed kinds (`deployment`, `daemonset`,
`job`, `cronjob`, `statefulset`, `persistentvolumeclaim`) hold their selectors through their own
parser (`parseLabelSelectorOpts`), which refuses the same and more, so their absence from the
test's list is not a gap.

### Required fields a kind writes unauthored and does not refuse

A field the API requires can still reach the object unauthored. Where the Go type writes the
field whatever was authored (`""`, `0`, `{}`, `null`), the object carries a value the author
did not write and does not show the omission. A kind refuses the omission of such a field only
where its required list or its own validation names it (`helmrelease` checks a `valuesFrom`
entry's `kind` and `name` by hand); every other one is written and left to the API server and to
the operator. The section above and each kind's entry list the ones that are refused. This
section states the rule that refuses them where a kind's list is generated, and the ones that
are not refused, as they are at this pin.

**Generated lists.** The required list of `issuer`, `clusterissuer`, `certificate`,
`secretstore`, `clustersecretstore`, `externalsecret`, `clusterexternalsecret`, `cnpg-cluster`,
`cnpg-pooler`, `cnpg-objectstore`, `cnpg-database`, `prometheus`, `alertmanager`,
`thanosruler`, `servicemonitor`, `podmonitor`, `prometheus-probe`, `prometheusrule`, the
fourteen Flux kinds (`helmrelease`, `fluxcd-kustomization`, `bucket`, `ocirepository`,
`gitrepository`, `helmchart`, `helmrepository`, `artifactgenerator`, `fluxcd-alert`,
`fluxcd-provider`, `fluxcd-receiver`, `imagepolicy`, `imagerepository`,
`imageupdateautomation`), `resourcesetinputprovider`, the ten Gateway API kinds
(`gatewayclass`, `gateway`, `listenerset`, `referencegrant`, `backendtlspolicy`, `httproute`,
`grpcroute`, `tcproute`, `udproute`, `tlsroute`), `replicationsource`,
`replicationdestination`, the four MetalLB kinds that require a field (`metallb-ipaddresspool`,
`metallb-l2advertisement`, `metallb-bgpadvertisement`, `metallb-bgppeer`), the seven further
cnpg kinds (`cnpg-imagecatalog`, `cnpg-clusterimagecatalog`, `cnpg-backup`,
`cnpg-scheduledbackup`, `cnpg-databaserole`, `cnpg-publication`, `cnpg-subscription`) and the
nine Cilium kinds other than the two network policies (`cilium-bgpadvertisement`,
`cilium-bgpclusterconfig`, `cilium-bgpnodeconfigoverride`, `cilium-bgppeerconfig`,
`cilium-cidrgroup`, `cilium-egressgatewaypolicy`, `cilium-loadbalancerippool`,
`cilium-localredirectpolicy`, `cilium-nodeconfig`) is generated:
every field the kind's API requires and its Go type writes unauthored is refused where the
author leaves it out under an authored parent, whatever type declares it. The paths are in
`zz_generated_kind_required.go` (`kindRequiredPaths`), read through the same derivation as the
measurement below (`deriveRequiredWritten`), so the two cannot drift.
`TestKindComponents_RequiredIsGenerated` fails where the file differs from the derivation, a
path added or gone, so a dependency bump that moves a field fails CI until the file is
regenerated with `UPDATE_KIND_REQUIRED=1` (refused where `CI` is set). Left out, each by a named
rule and never by the type that declares it:

- a field whose written value the API takes as authored (the valid values below);
- a field the schema gives a default, that is not nullable and that the type writes `null`:
  the API server drops that null and fills the default (`cnpg-cluster`'s
  `replicationSlots.synchronizeReplicas.enabled`);
- a field under a path the kind refuses whatever is authored there (see Method);
- an entry of `requiredWrittenExceptions`, the test's named exceptions, each with the API
  server's answer for the object without it, in one of two classes. **Accepted**: the API
  server accepts the omission as the kind writes it, and the operator handles it. At this pin:
  `cnpg-pooler`'s `template.spec.containers`, which the kind writes `[]` and the Pooler CRD
  accepts, the template's `spec` authored or left out; the operator adds its `pgbouncer`
  container. **Pruned**: the Go source requires the field and the kind's CRD does not declare
  it, so the API server prunes it, authored or not, and requires nothing of it. At this pin: on
  `prometheus`, `alertmanager` and `thanosruler`, the `apiVersion`, `kind`, `name` and `uid` of
  an owner reference in the metadata of an ephemeral volume's claim template
  (`storage.ephemeral` and `volumes[].ephemeral`), 8 per kind. The Go source declares the whole
  `ObjectMeta` there; the operator's CRDs declare that metadata as an object with no field, so
  the API server prunes an owner reference there (strict field validation refuses it as an
  unknown field) and requires none of its fields.

A refusal reads the kind's own text for the field where it has one, else `the <API> API
requires this field` (or `requires the name of what this refers to`, for a reference's
`name`). A field under a parent the type writes unauthored and the API does not require is
asked for only where that parent is authored; the generated file names each such field with
what the API server answers for the object with the parent left out, and the test holds every
one to an answer. A monitoring basic auth (`basicAuth`, wherever a kind holds one) is such a
parent: the API server accepts it without a `username` or `password`, and the Prometheus
operator reads both keys and refuses the basic auth without either, so the kind refuses an
authored basic auth that leaves either out; a web or cluster TLS server's `keySecret` is asked
for only where it is authored, `keyFile` being the alternative. The test shows what the kind
does with each, and what the API server answers on the operator's CRD: the object without the
parent where the kind builds one, else the object with the parent as the type writes it
unauthored (a `prometheus-probe`'s `prober` without a `url`, which the API server refuses as
the kind does). Of the stores, the fields whose API default never applies because the type
writes them (`secretStoreUnappliedDefaults`) are generated by the same test.

The Flux kinds that decode their properties themselves (`helmrelease`, `fluxcd-kustomization`,
`bucket`, `ocirepository`, `gitrepository`, `helmchart`, `helmrepository`) run the generated
refusal after their own checks, so a field one of those checks requires (a `bucket`'s
`bucketName`, a `helmrelease`'s `valuesFrom[].kind`) is refused in the words it always was;
the test names those fields (`generatedRefusedFirst`). An `httproute` and a `grpcroute` run it
after their decode checks too. A refusal of these kinds that their list did not name before
says its class's sentence. No path of a Flux or Gateway kind needs a recorded answer for a
parent left out. The Gateway API's module ships its CRDs, and so does the Flux Operator's: the
test runs a row of each Gateway kind and of `resourcesetinputprovider` through the API server.

**Vendored CRDs.** The Prometheus operator's module, the Flux controllers' API modules and the
External Secrets Operator's API module ship no CRD, so the test reads copies, vendored for the
tests only and each checked against its git blob id by `TestVendoredCRDs_MatchSource`, which
also holds each directory to the tag of the module `go.mod` links:

- the seven `monitoring.coreos.com` CRDs of the monitoring kinds, from
  `example/prometheus-operator-crd/` of prometheus-operator `v0.95.1`, beside the operator
  excerpt in `testdata/upstream/prometheus-operator/` (`scripts/vendor-prometheus-operator-gates.sh`);
- the CRDs of the fourteen Flux kinds, from `config/crd/bases/` of each controller's repository
  at the tag of its API module (`api/v1.7.1` of helm-controller, `api/v1.10.1` of
  kustomize-controller and source-controller, `api/v1.10.0` of notification-controller,
  `api/v1.3.0` of image-reflector-controller and image-automation-controller, `api/v2.3.1` of
  source-watcher), one directory per repository under `testdata/upstream/` with its `LICENSE`
  (`scripts/vendor-flux-crds.sh`). The one addition to their bytes is an `allow-term` pragma
  comment line above an upstream line that names a term the repository's forbidden-terms guard
  lists (a receiver type of the `Receiver` CRD), as a whole word or inside an identifier: the
  script reads the guard's own matcher (`scripts/forbidden-terms-lib.sh`, self-tested by `make
  test-forbidden-terms`), so it marks every line the guard would refuse, under the C locale or
  C.UTF-8. Each `file` pin in `SOURCE` is the blob id of
  the committed file; a file carrying pragma lines also has an `# upstream` line naming
  upstream's blob id, which the file must hash to with those lines stripped. A file with an
  `# upstream` line and no pragma, a pragma in a file with none, and a pragma whose next line
  does not name its term all fail the test;
- the CRDs of the four External Secrets kinds (`secretstore`, `clustersecretstore`,
  `externalsecret`, `clusterexternalsecret`), unmodified from `config/crds/bases/` of
  external-secrets `v2.14.0`, under the same path in `testdata/upstream/external-secrets/` with
  its `LICENSE` (`scripts/vendor-external-secrets-crds.sh`, `mise run
  vendor-external-secrets-crds vX.Y.Z`; go-kure/launcher#1108). `go.mod` links the API module
  at a pseudo-version of a commit, not at a tag of its own, and that commit is the release tag
  `v2.14.0`, a lightweight tag: its ref names the commit, with no tag object. So the
  `tag-object` line of `SOURCE` is the object the tag ref names, the commit, and the test
  requires its first 12 hex digits to be the pseudo-version's; the script takes the tag, checks
  with `git ls-remote` that it names that commit, and refuses an annotated tag or another
  commit. Copied unmodified, these files take no pragma: the script refuses one with a line the
  forbidden-terms guard would refuse, read with the guard's own matcher as above.

The kinds' lists stay derived from the markers of the Go source; the CRDs answer for them. Every
generated kind's CRD is read, shipped or vendored, and a kind whose CRD is not read fails the
test.
Of every type that holds a generated path, the test creates the object with one such path
authored, which the API server accepts, and without it, which the API server refuses as
missing a required field (a path whose CRD gives it a default is passed over for another of
its type: the API server fills a default only where the field is left out of the object, not
where the type writes it empty). A type whose every generated path the CRD defaults (a Vault
store's JWT authentication, whose one generated path is its mount `path`, defaulted to `jwt`)
is answered instead with the field authored and left out, both accepted, the default filled
in the second, and written `""` as the type writes it unauthored, which the API server keeps:
the default never applies, which is why the kind refuses it. Every generated path is one the
CRD declares. The rows author what the marker schema cannot: a URL, duration or enumerated
value where the filled control's would break a pattern or an enum, and, of the External
Secrets stores, one authentication method where a provider's property count or expression rule
requires exactly one (`generatedHarnessSamples`).

The VolSync, MetalLB, further cnpg and Cilium kinds are derived from the CRDs their linked
modules ship, and the test runs a row of each of their types that holds a generated field
through the API server, on those CRDs. `replicationsource` and `replicationdestination` run the
generated refusal after their own checks, as the Flux kinds do; their generated list adds the
required fields of the Kubernetes types their movers embed to what their hand-kept lists held.
The MetalLB, further cnpg and Cilium kinds' generated lists hold exactly what their hand-kept
lists held, and are read where those were, before the kinds' own checks, so no document is
refused that was not and every refusal reads as it did. None of these needs a recorded answer
for a parent left out.

**Kept by hand.** The `cilium-networkpolicy` and `cilium-clusterwidenetworkpolicy` kinds keep
their hand-kept list: it is one rule's list (`requiredfields.CiliumRule`), shared with the
`cilium-networkpolicy` trait in another package; it is read under the `spec` and `specs`
prefixes, which the generated lists' API server harness does not take; two of its fields
(`labels[].key`, `icmps[].fields[].type`) the decode refuses first, which that harness's
both-ways rows do not handle, though the derivation lists both; and it holds fields no
generated list has a place for (a listener's `priority` and Envoy configuration `kind`, which
the API does not require and refuses written). Both refuse
every field the derivation finds, and `TestCiliumNetworkPolicy_RequiredMatchCRD` and
`TestCiliumClusterwideNetworkPolicy_RequiredMatchCRD` hold the list to their CRDs.

**Families.** The test names each field by the type that holds it:

| Family | Owner type | Typical fields |
|---|---|---|
| `AFF` | the `core/v1` affinity terms: `NodeSelectorRequirement`, `PodAffinityTerm`, `PreferredSchedulingTerm`, `WeightedPodAffinityTerm` | a node selector requirement's `key` and `operator`; a pod affinity term's `topologyKey`; a weighted or preferred term's `weight`, `podAffinityTerm` and `preference` |
| `SEC` | `SeccompProfile`, `AppArmorProfile`, `Sysctl` | a profile's `type`; a sysctl's `name` and `value` |
| `POD` | any other `core/v1` type | the `key` of a secret or config map key reference; a container's `name`; a pooler template's `containers` |
| `KIND` | a type of the kind's own API, or of `metav1` other than a label selector's match expression | the `ownerReferences[]` fields of a Prometheus operator workload kind's ephemeral claim template |

A fifth family of the test, `SEL` (the `key` and `operator` of a `metav1` label selector's
match expression), has no member: those are refused on every kind
([A label selector's match expressions](#a-label-selectors-match-expressions)).

**Per kind.**

| Kind | Not refused | AFF | SEC | POD | KIND |
|---|---|---|---|---|---|
| `cnpg-pooler` | 1 | | | 1 | |
| `prometheus`, `alertmanager`, `thanosruler` | 8 each | | | | 8 |

Every remaining member is an exception. None is left on the other kinds whose list is
generated (`cnpg-pooler` keeps its accepted exception, and `prometheus`, `alertmanager` and
`thanosruler` their pruned owner reference fields), nor on the two Cilium network policy
kinds, whose list is kept by hand. `metallb-bfdprofile` and `metallb-community` are not
measured: their CRDs require no field, and `TestMetalLBKinds_RequiredMatchCRD` holds each kind's
empty list to its CRD. Nor is `apiservice`: no CRD serves it, the API server's aggregation layer
does.
`TestRequiredWrittenKinds_CoverEveryCRDKind` holds the measured kinds to the kind inventory:
every `kind` row whose API group client-go's scheme does not register, so a CRD serves it, is
measured or named as not measured with its reason, so a new kind component of such an API
fails there until it is one or the other. Most members are written
`""`; the rest are `0` (mostly a preferred term's `weight` or a `port`), `{}`, `[]`, `null` or
an object of such values.

**What the API server makes of them** is a value rule of each field, not read here. The
exceptions are shown: `TestKindComponents_GeneratedRequiredBothWays` creates the Pooler without
`template.spec.containers`, and without the template's `spec`, and the API server accepts both.
It creates each workload kind of the Prometheus operator with an owner reference in an
ephemeral claim template's metadata authored whole, and with the field taken out: the API
server names that `ownerReferences` an unknown field in both, and nothing else, and accepts
the object it prunes it from, so it drops the owner reference whatever it holds.
Before go-kure/launcher#883 generated their lists, the same was shown of `imageCatalogRef` on
`cnpg-cluster` and `pgbouncer.imageCatalogRef` on `cnpg-pooler`: a `kind` written `""` refused
by the CRD's own rule on the reference, a `name` written `""` and a `major` written `0` accepted.
All three are refused now.

**Excluded as valid values.** A member whose written value the API takes as authored is not
listed: `cnpg-cluster`'s `instances` (`1`, the CRD's default) and
`postgresql.syncReplicaElectionConstraint.enabled` (`false`, which the CRD requires and gives
no default; the Go type's zero value is a valid one); of the Flux kinds, `interval` of
`bucket`, `fluxcd-kustomization`, `gitrepository`, `helmchart` and `helmrelease`
(`"1h0m0s"`, the kind's own default), a verification's `provider` on `helmchart`,
`helmrelease` (`chart.spec.verify.provider`) and `ocirepository` (`"cosign"`, the API's
default, which the kind writes), and `fluxcd-kustomization`'s `prune` (`false`, a valid zero
value). The test names the eleven (`requiredWrittenValidDefaults`) and checks that their
omission builds and writes exactly that value. Nor is a field the schema gives a default, that
is not nullable and that the type writes `null` (`cnpg-cluster`'s
`replicationSlots.synchronizeReplicas.enabled`): the API server drops the null and fills the
default.

**Method.** For each field the kind's API requires and its Go type writes unauthored,
`TestKindComponents_RequiredWrittenNotRefused` builds the kind with the field authored, with
the required fields around it filled, then builds it again with the field taken out. The field
is a member when the second build succeeds and the object still holds the field. A
`helmrelease`'s fields of the OCIRepository its chart template inlines are measured under a
template of `chart.kind` OCIRepository, the only kind that reads them. A field under
a path the kind refuses whatever is authored there is skipped, and the test names those paths
with their reason: a `cilium-networkpolicy`'s `nodeSelector`, a `cnpg-pooler`'s
`template.spec.ephemeralContainers`, and the `generatorRef` of an `externalsecret`'s or a
`clusterexternalsecret`'s `data[].sourceRef`. A refused omission counts only where the error
names the field or one under it, by its full path; the two Cilium policy kinds refuse a label's
`key` and an ICMP field's `type` in their decode instead, with an error naming no field, and the
test names those with their reason. Any other field that cannot be measured fails the test. The
required lists are those of the CRDs of the linked modules. The Prometheus operator, Flux and
External Secrets kinds are read through the schema markers of their linked sources, by the same
reader as their own required-list tests; `resourcesetinputprovider` is read from the CRD the
flux-operator module ships. The members are pinned in
`testdata/required-written-not-refused.txt`, one line per field (kind, path, family, written
value). The test fails on any difference, a new member or one that is gone, so a dependency
bump or a kind change that moves the set fails CI; `UPDATE_REQUIRED_WRITTEN_PIN=1` rewrites
the file, and is refused where `CI` is set.

At this pin the file holds 25 members, every one an exception: the accepted
`cnpg-pooler template.spec.containers` and the 24 pruned owner reference fields. The External
Secrets kinds' generated paths are held to the API server too, on their vendored CRDs
(go-kure/launcher#1108).

### Every spec field is this package's to write

Since go-kure/launcher#361 this package builds against kure's release-1 builder
contract (`go-kure/kure` ≥ `v0.2.0-beta.11`). Under it a `Create<Kind>`
constructor returns an object carrying TypeMeta plus `metadata.name` and
`metadata.namespace` and **nothing else** — no labels, no annotations, no
selector, no injected defaults. Everything else is the handler's own literal or
an explicit field assignment. Two consequences a reader needs:

**`spec.selector` is written explicitly, and must agree with the pod template.**
Deployment, StatefulSet and DaemonSet all require `spec.selector` and get no
server-side default for it, so each handler assigns
`&metav1.LabelSelector{MatchLabels: …}` from the same helper that produced
`spec.template.metadata.labels` — `deploymentComponentLabels` for `deployment`
and for `worker`, which lowers to a `deployment` component (its affinity selector
uses `appLabels`), and `appLabels` for `webservice`, `statefulset` and `daemonset` (both
return a fresh `{"app": <label value>}` map, per the ownership rule above, valued
as [The `app` label](#the-app-label) describes). This is
the one field the compiler cannot check: a selector that disagrees with the
template labels compiles and is refused by the apiserver at apply time. The labels an
author adds with `podMetadata` go on the pod template only, so they cannot make the two
disagree. `job` and
`cronjob` are the deliberate exception — the Job controller fills `spec.selector`
and its matching pod labels itself.

**Two injected defaults are gone from the emitted manifests.** The constructors
used to write `imagePullPolicy: IfNotPresent` onto every container and
`podManagementPolicy: OrderedReady` onto every StatefulSet; neither is emitted
now. `OrderedReady` is the apiserver's own default for that field, so that one
is unchanged in effect. `imagePullPolicy` is written only when a component
authors it (see [Container fields](common-config.md#container-fields)); what follows is about
the omitted case. Kubernetes defaults an omitted
value from the image reference and keys that decision on the `latest` tag, so
what matters here is that no accepted image can carry one: `ValidateImageRef`
(`common.go`) refuses an untagged reference and an explicit `:latest` tag on
every main, init and sidecar image — including a digest reference that still
carries the tag (`repo:latest@sha256:...`), which it rejects for exactly this
reason. A digest reference with *no* tag is the case this argument does not
reach, and no golden covers that form; it is pinned by digest either way, and
this package does not assert what a cluster defaults for it. The argument is
about the *parsed* path, which is where `ValidateImageRef` runs
(`ToApplicationConfig`): a handler config a library consumer builds directly
carries whatever image it was given, so `:latest` with no pull policy remains
reachable that way. That is also the only other path with a delta here — raw
manifests, `passthrough`, Helm-rendered objects and Flux patches never went
through the constructors, so nothing was ever injected into them to lose.

A third delta from the same contract change is metadata rather than a default, but
belongs in the same inventory: under `valuesMode:
configMap` the generated values ConfigMap used to take an `app` label *and* an
`app` annotation from the constructor, both valued at the ConfigMap's own name
(`<component>-values`). It now carries the label only, valued at the component's
label value (`<component>`, projected when the name exceeds 63 characters — see
[The `app` label](#the-app-label)), matching every other object this package emits. A consumer
selecting that ConfigMap by `app=<component>-values` must be repointed.

The `obj.Annotations = nil`
assignments scattered through the handlers, which existed to strip the `app:`
annotation the constructors used to stamp, are now no-ops — kept so the field
stays at a known value regardless of what a future constructor does.

**`postgresql` writes every CNPG value itself (kure `v0.2.0-beta.13`).** kure's
release-2 builder contract retired the CNPG config-struct layer
(`cnpg.Cluster`/`Pooler`/`ObjectStore`/`Database` and their `*Options` types) that
`postgresql` used to go through. The handler then called the generated
`CreateCluster`/`CreatePooler`/`CreateObjectStore`/`CreateDatabase` and assigned the
upstream `cnpgv1` / barman-cloud structs directly. Since go-kure/launcher#281 the
`postgresql` rule builds those structs as the properties of the `cnpg-*` kind
components it lowers onto, whose `Generate` calls the same constructors, and
`enablePDB` comes from the post-policy step the rule attaches to the Cluster.
The values that layer used to inject are written explicitly, so the emitted
manifests are unchanged byte for byte: `enablePDB` (true only for more than one
instance), `primaryUpdateStrategy: unsupervised`, the `ACCESS_KEY_ID` /
`SECRET_ACCESS_KEY` key names on backup and objectStore credentials, the
barman-cloud WAL-archiver plugin entry when an `objectStore` is declared (its
plugin name and parameters were corrected afterwards by go-kure/launcher#643),
the pooler `type` (`rw` unless `ro` was authored) and its always-present
`pgbouncer` block, and `ensure: present` on every extension not authored `absent`.
The layer's guards are kept too: `inheritedMetadata`, `managed`, `bootstrap`,
`postgresql.synchronous` and the credential references are omitted when their input
is empty, a pooler `instances` of zero or less is omitted when nobody authored
it (an authored count is written as authored since go-kure/launcher#790, see
the postgresql entry), and a role's or database's `ensure` /
`databaseReclaimPolicy` is written only for `absent` / `delete`.
