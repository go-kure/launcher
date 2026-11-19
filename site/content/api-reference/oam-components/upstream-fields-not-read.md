---
title: "Upstream fields a hand-parsed kind does not read"
weight: 13
---

## Upstream fields a hand-parsed kind does not read

Six kinds read their properties key by key instead of decoding them into the
Kubernetes type: `statefulset`,
`service`, `persistentvolumeclaim`, `configmap`, `secret` and `serviceaccount`. The first
builds pods and is "the pod-building kind" below. (The `deployment`, `daemonset`, `job` and
`cronjob` kinds are not among them: each decodes its properties strictly into the upstream
`DeploymentSpec`, `DaemonSetSpec`, `JobSpec` or `CronJobSpec`, so it has no refusal map and
drops no field; see the `deployment`, `daemonset`, `cronjob` and `job` sections of
[Kinds: core workloads](kinds-core-workloads.md#deployment).) For such
a kind an upstream field its parser does not know is no decode error; it is a
key the parser never looks at. The rule that closes that gap:

> Every field of the upstream type that such a kind does not read, under its
> name or in another shape, is an entry of one of the kind's refusal maps with
> its reason. No upstream field is dropped in silence.

`webservice` and `worker` lower to a `role-deployment` and refuse what it refuses;
the `pvc` trait reads a claim through the `persistentvolumeclaim` kind's
parser and refuses what that kind refuses, and the `secret` trait reads its
Secret through the `secret` kind's parser.

### The two paths

An authored key reaches a kind on one of two paths, and both give the reason:

- **A caller of the handler** (`ToApplicationConfig`, or `Transform` without
  the check below) gets the reason alone:
  `selector: not authorable; the StatefulSet selector is builder-managed (app: <component>), must equal the generated template labels and is immutable once created`.
- **A document** goes through the authored-property check first
  (`Transformer.ValidateAuthoredProperties`, which `kurel build` calls). The
  check refuses a key the kind does not declare before the parser runs, and
  appends the kind's reason to its own text:
  `component "db" (type "statefulset"): properties: unsupported field "selector" (allowed: …); selector: not authorable; the StatefulSet selector is builder-managed …`.
  The kind gives the reason through `UnsupportedFieldHint`
  (`pkg/oam`'s README, "A reason on a refused key").

### Below the top level

`UnsupportedFieldHint` answers for a kind's **top-level** keys. A refusal that
sits inside a property the kind declares reaches a caller of the handler with
its reason. In a document, the check refuses the key at that position with its
own text and appends the same reason, which the kind gives through
`NestedUnsupportedFieldHint`:
`properties.affinity: unsupported field "nodeAffinity" (allowed: …); affinity.nodeAffinity: not read — …`.
The positions are:

| Position | On | Keys |
|----------|----|------|
| a container's `resources`: the main container's, and an `initContainers` or `sidecars` entry's | every workload kind | `claims` |
| the pod's `podResources` | every workload kind | `claims` (upstream validation forbids claims at pod level: "claims may not be set for Resources at pod-level") |
| the `affinity` shorthand | `webservice`, `worker` | `nodeAffinity`, `podAffinity`, `podAntiAffinity` |
| a `volumeClaimTemplates` entry | `statefulset` | `volumeName`, `dataSource`, `volumeMount` |

A key that is **no field of the upstream type** is not in a refusal map. The
check refuses it as it refuses any undeclared key, with nothing appended; a
caller that skips the check has it dropped, as `pkg/oam`'s README states for
every handler.

### Refused, with the reason

| Key | On | Why |
|-----|----|-----|
| `selector` | `statefulset` (`webservice`, `worker`) | Builder-managed (`app: <component>`), equal to the template labels, immutable once created. |
| `restartPolicy`, `activeDeadlineSeconds` (the pod's) | `statefulset` (`webservice`, `worker`) | apps/v1 validation accepts only `Always`, and no deadline, on these pod templates. |
| `ephemeralContainers`, `priority`, `overhead`, `serviceAccount` | every workload kind | See [Pod-level properties](common-config.md#pod-level-properties). |
| `evictionResponders` | every workload kind | Alpha upstream, behind the `EvictionRequestAPI` feature gate. |
| `restartPolicyRules` (the main container's) | every workload kind | Upstream accepts a container's restart rules only with the container's own `restartPolicy`, which no kind reads on the main container. An `initContainers` entry reads both (see [Container fields](common-config.md#container-fields)). |
| `externalIPs`, `clusterIPs` | `service` | See the `service` kind. |
| `dataSource` | `persistentvolumeclaim`, the `pvc` trait | Authored through `dataSourceRef`. |
| `secrets` | `serviceaccount` | The list limits mountable Secrets only under an annotation upstream deprecates since Kubernetes 1.32; it is no way to find or create a token. |
| `resources.claims` | a container's `resources`, on every kind that reads them | An entry names one of the pod's `resourceClaims`, and a container's resources are read without them; upstream puts the field behind the `DynamicResourceAllocation` feature gate. See [Below the top level](#below-the-top-level). |
| `podResources.claims` | every workload kind | Upstream validation forbids claims in a pod's own resources ("claims may not be set for Resources at pod-level"); the pod's claims are its `resourceClaims`. See [Below the top level](#below-the-top-level). |

### Read in another shape

These upstream names are free on the kind and are refused too, with a reason
that names the properties the field is authored as:

| Upstream field | On | Authored as |
|----------------|----|-------------|
| the pod `template` | the pod-building kind | The component's own container and pod-level properties. Its metadata is not authored either: the kind's builder writes the `app: <component>` label and nothing else, the transform then adds the component label (`<domain>/component`, see [The `app` label](conventions.md#the-app-label)), and a trait may add more. |
| `containers` | the pod-building kind | The main container is the component's container properties; further ones are `sidecars` entries. |
| the main container's `name` | the pod-building kind | Not authored: the container is named after the component. |
| `livenessProbe`, `readinessProbe`, `startupProbe` | the pod-building kind | `probes.liveness`, `probes.readiness`, `probes.startup`. |
| `volumeMounts`, `volumeDevices` | the pod-building kind | A `volumes` entry's `mountPath`, or its `devicePath` for a `volumeMode: Block` claim. |
| `resources` (the claim's) | `persistentvolumeclaim`, the `pvc` trait | `size`, the storage request. A claim's limits are not read. |

Two pod fields are read under another name, because their upstream name is a
property of the kind already: `securityContext` as `podSecurityContext` and
`resources` as `podResources` (both names are the main container's).

### One level down

Inside a container, go-kure/launcher#790 reads `securityContext.windowsOptions`
on every container, and an init container's own `restartPolicy` and
`restartPolicyRules` (see [Container fields](common-config.md#container-fields)). Three upstream
fields one level down stay unread, and are refused at their position as an
unknown key:

- **`lifecycle.stopSignal`**: upstream puts it behind the `ContainerStopSignals`
  feature gate, alpha and off by default through Kubernetes 1.37, and holds the
  signal to the pod's `os.name`. A container's `lifecycle` reads `postStart`
  and `preStop`.
- **`ports[].hostPort` and `ports[].hostIP`**: they bind a port on the node.
  No environment-policy switch covers that (`AllowHostNetwork()` gates the
  node's network namespace, not a port bound from the pod's own), and whether
  the port is free is the scheduler's question, on cluster state. A `ports`
  entry reads `containerPort`, `name` and `protocol`.

### Outside the properties

The object's envelope is not a property of the kind's own schema: its
`apiVersion` and `kind` are the component's type, its `metadata` is the
component's name and the `objectName`, `labels` and `annotations` properties
every kind takes, and `status` is the cluster's. `spec` is the level whose
fields the properties are, and has no key of its own.

### An explicit null on a refused key

A null carries no content, and the kinds differ on what the parser makes of it
on a key they refuse. This is each kind's behavior from before
go-kure/launcher#790, left as it was:

- **Read as unauthored:** `service` and `persistentvolumeclaim` on every key
  they refuse.
- **Refused, as any value is:** `statefulset`,
  `serviceaccount`, and `webservice` and `worker`; and every kind on
  `resources.claims`, on `podResources.claims` and on the keys refused under
  the `affinity` shorthand.

In a document the difference does not show: the authored-property check
refuses an undeclared key whatever its value, null included.

### What holds this

The tests that hold this rule are on the
[contributor page](../../contributing/oam-reference/upstream-fields-not-read.md).
