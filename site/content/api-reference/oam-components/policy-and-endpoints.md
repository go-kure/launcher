---
title: "Policy defaults, endpoints and outputs"
weight: 2
---

## Policy defaults & enforcement ordering

A container's effective cpu/memory requests and limits are assembled in three
tiers, in precedence order:

1. **Authored** — whatever the component's `resources` property set explicitly.
   The string `unlimited` under `limits.cpu` or `limits.memory` is an authored
   opt-out (go-kure/launcher#1181): the container gets no limit for that
   resource, because neither a policy default limit (tier 2) nor, for memory,
   the limit mirror (tier 3) is applied to it. It is accepted where a built-in
   default exists to opt out of: the main container of the five controller
   kinds, `webservice` and `worker`, and their `initContainers` and `sidecars`
   entries. It is refused by name under `requests`, under `podResources` and on
   `postgresql` ("no intrinsic default to opt out of"), under any resource name
   other than cpu and memory, and, with the `resource-maximum` refusal class,
   when the policy sets `MaxCPU()`/`MaxMemory()` for that resource: an absent
   limit is no bound, so it is never read as under the maximum. `null` is still
   absence, not an opt-out. A policy sub-option to accept `unlimited` as a
   policy default is not provided.
2. **Policy default** — `ApplyPolicy` fills any request/limit the author left
   unset from `oam.Policy.DefaultCPURequest()`/`DefaultMemoryRequest()`/
   `DefaultCPULimit()`/`DefaultMemoryLimit()` (`enforce.go`'s
   `applyDefaultQuantity`). A negative policy default fails the build, as a
   negative authored quantity does, since Kubernetes would refuse it at apply;
   zero is accepted. Each default that fills a value is reported to the
   provenance report as a `policy` entry (`oam.PolicyDefaultReporter`: the
   workload kinds, `cnpg-cluster`, `persistentvolumeclaim` and the `pvc` trait
   embed an `oam.PolicyDefaultLog`; the `scaler` trait implements the interface
   itself, so that its config stays comparable); a request lowered to the
   authored limit (tier 3 below) carries the detail `capped-to-limit`. What
   the postgresql defaults step fills after the policy (`enablePDB`, and the
   `1Gi` `storage.size` fallback) is reported as a `builtin` entry
   (`oam.BuiltinDefaultReporter`, embedded `oam.BuiltinDefaultLog` in
   `cnpg-cluster`), not as `policy`.
3. **Intrinsic handler default** — `buildResourceRequirements` (`common.go`)
   fills anything still unset at `Generate()` time: 100m CPU request, 128Mi
   memory request, and a memory limit mirroring the (possibly just-defaulted)
   memory request. A defaulted request never exceeds the limit authored for the
   same resource: it is the lower of the default and that limit, so
   `limits.memory: 64Mi` with no request renders a 64Mi request, not 128Mi
   (go-kure/launcher#1217; the apiserver refuses a request above its limit).
   A limit at or above the default leaves the default request in place. The
   same holds for a policy default request (tier 2): it is held to the authored
   limit for that resource (`capPolicyRequestDefaults`, `resource_policy_defaults.go`),
   and an authored request is never changed. Two
   consequences are easy to miss: a policy `DefaultMemoryRequest()` flows into
   the mirrored memory limit, and the handler never defaults a CPU limit.

`oam.Policy.MaxCPU()`/`MaxMemory()` are enforced against the *effective*
value — what `Generate()` will actually emit, after all three tiers — not
just the authored/policy-defaulted `resources` field. Prior to go-kure/launcher#251
the enforcement check ran before the intrinsic tier was computed, so an
application that omitted `spec.resources` entirely could ship a Deployment
whose 100m CPU / 128Mi memory intrinsic defaults exceeded the enforced
maximum. `enforceMaxResources` (`enforce.go`) closes this by calling the same
`buildResourceRequirements` the generator calls, enforcing against its
result, and discarding it — `buildResourceRequirements` deep-copies its
maps, so this is read-only and generated output is unchanged. When a value
came from the intrinsic tier specifically (absent from the authored/policy-
defaulted value, present only after the intrinsic fallback), the error names
it as a "generated default" so the mismatch isn't mysterious.

This three-tier effective-value enforcement applies to the seven kind
components that call `buildResourceRequirements` on their main container
(`webservice`, `worker`, `deployment`, `cronjob`, `job`, `statefulset`,
`daemonset`).
**`postgresql` is exempt**: its lowering rule copies every entry of
`c.Resources` straight onto the Cluster spec (`cnpgResourceList`) and never calls
`buildResourceRequirements`, so it has no intrinsic tier for its existing
direct-form checks to diverge from.

Init containers and sidecars receive the same intrinsic defaults
(`common.go`'s `buildResourceRequirements` call sites at the init-container
and sidecar builders), and `ApplyPolicy` enforces them too (go-kure/launcher#312):
each entry's resources, image registry, `securityContext.privileged`, and
`securityContext.capabilities.add` are checked against the same policy
methods the main container uses, via the shared `enforceExtraContainer`
helper (`enforce.go`). All seven kind components enforce their
`initContainers`; only `webservice`, `worker`, `deployment`, `statefulset`
and, since go-kure/launcher#790, `daemonset` have a
`sidecars` schema key at all (`cronjob`/`job` have no sidecars support,
per ["Per-type highlights"](https://www.gokure.dev/launcher/api-reference/oam-components/#per-type-highlights)) so only those five enforce a sidecars
loop. Errors name the authored list position and container, e.g.
`initContainers[0] "init": image "docker.io/x/y:v1" is not from an allowed
registry [...]`.

The twelve JobSpec-level properties (see ["Per-type highlights"](https://www.gokure.dev/launcher/api-reference/oam-components/#per-type-highlights)) are parsed
and applied by a dedicated `JobSpecConfig`/`parseJobSpec`/`applyJobSpec`
(`common.go`), factored out separately from the fields above because
`batchv1.CronJob.Spec.JobTemplate.Spec` and a bare `batchv1.Job.Spec` are the
same `batchv1.JobSpec` type. `cronjob` projects them onto
`spec.jobTemplate.spec` and `job` (go-kure/launcher#344) onto its own
`spec`; both publish the identical key set, pinned by
`TestJobSpecSchemaMatchesParser` against `jobSpecPropertyKeys`.

**One JobSpec field is deliberately not shared: `suspend`.** `batchv1` carries
two distinct Suspend fields — `CronJobSpec.Suspend` (stop starting new
executions of the schedule) and `JobSpec.Suspend` (create the job with no pods)
— and `cronjob` already published `suspend` for the first of them. Putting a
`suspend` in the shared fragment would write one authored value into two
different API fields, and would silently overwrite `cronjob`'s own schema entry,
since each handler merges the shared fragment with `maps.Copy` *after* its own
literal map. So `job` publishes and parses its own JobSpec-level `suspend`
instead, and `cronjob`'s stays CronJobSpec-level with no key for the job-template
one. Both halves are pinned: `TestSchemaJobSpec_HasNoSuspendKey` on the schema,
`TestCronjobHandler_SuspendWritesCronJobSpecOnly` and
`TestJobHandler_SuspendWritesJobSpec` on the generated objects.

`applyJobSpec` copies every value it writes rather than assigning the config's
own pointers (`copyPtr` for the scalars, `DeepCopy` for `successPolicy` and
`podFailurePolicy`, which each own a slice of rules). A handler config outlives one `Generate` call and
`Generate` may run more than once, so an aliased pointer would leave a mutable
path from the generated object back into the config — a writer through the
object would change what the next `Generate` emits.

## Policy refusal classes

Every refusal by the environment policy this package raises is an `oam.PolicyRefusal` with
a class, which a consumer reads from `oam.ViolationError.Class` instead of matching text
(go-kure/launcher#849). The classes, what each covers and what stays unclassified are in
`site/content/api-reference/oam/policy-and-capability.md`, "Refusal classes". What is particular to this package:

- **The class does not depend on the path.** A privileged container is
  `oam.RefusalPrivileged` on a workload kind's own `ApplyPolicy`, on an operator CR's pod
  template, on an object a chart renders, and on a `passthrough` or `manifests` object, at
  the transform and at generation: one check raises it (`enforce.go`,
  `enforcePodTemplatePolicy`, `enforceRenderedObjectPolicy`).
- **cpu and memory are one class, storage another.** The comparison with a maximum is
  shared and its text is the same for every resource, so the class comes from the caller:
  `oam.RefusalResourceMaximum` for a cpu or memory request or limit (a container's, a
  pod's, the block a CloudNativePG kind, an `alertmanager`, a `thanosruler` or a `prometheus` carries, and the pod template of an ACME HTTP01
  solver on an `issuer` or a `clusterissuer`), `oam.RefusalStorageMaximum` for a claim's
  request, a claim template's, a generic ephemeral volume's, a PersistentVolume's capacity
  and a `cnpg-cluster` volume, the `1Gi` fallback of `postgresql` included, which its
  post-policy step holds to the maximum. `oam.RefusalReplicaMaximum` covers `replicas`, a
  HorizontalPodAutoscaler's `maxReplicas` and `cnpg-cluster`'s `instances`.
- **Secret material in the document is `oam.RefusalExplicitSecret`:** a `secret`
  component, a Secret that `passthrough` or a `manifests` source carries, `helmtemplate`'s
  `secretValues`, a `certificate`'s keystore password (`keystores.jks.password`,
  `keystores.pkcs12.password`), and in a `secretstore` or a `clustersecretstore` a
  credential written as a `value` and the data of the `fake` provider.
- **A source host is `oam.RefusalRegistry`,** as an image's registry is: the `url` of a
  `manifests` or `crd` source, a `helmtemplate` chart source, the Flux source kinds, an
  `oci://` url that does not name its registry, and a `bucket` endpoint on Amazon S3.
- **An object that cannot be read is `oam.RefusalUnreadableObject`** on the three paths that
  take an object written elsewhere (template delivery, `passthrough`, `manifests`): the
  refusals under "What cannot be read is refused, not passed" in the `passthrough` and
  `manifests` entries.
- **Unclassified, though it is the component's violation:** the rules that hold with or
  without a policy and are checked at the policy step (`ephemeralContainers` and the image
  rule on an object written elsewhere, an undeclared field on a workload or a claim a chart
  renders), a policy default or maximum that does not parse, and a chart that does not
  render or whose render does not decode (an `items` array on a kind that is no list, for
  one).
- **A redirect of a `manifests` `url` source to a host outside the allowed registries** is a
  fetch failure, with the error a fetch failure has and no `oam.ViolationError`; that error
  holds the `oam.PolicyRefusal` of class `oam.RefusalRegistry`, which `errors.As` reaches.

A new policy check in this package builds its refusal with `oam.NewPolicyRefusal` and a class
constant; `TestBuiltinPolicyRefusalsCarryAClass` (`pkg/oam`) fails on one that does not.

## Pod view of a monitoring workload object (`MonitoringPodSpec`)

An object from which the Prometheus operator builds pods, the `Alertmanager`, `Prometheus` and
`ThanosRuler` of the `alertmanager`, `prometheus` and `thanosruler` kinds, has no pod template:
its pod fields sit under `spec` in the operator's own type, so a consumer that scans the pods of
the rendered output finds none to scan (go-kure/launcher#974). `MonitoringPodSpec(obj)` returns them as a `corev1.PodSpec`, with the
prefix its paths sit under in the object (`spec`) and whether the object is of such a kind:

```go
if pod, prefix, ok := components.MonitoringPodSpec(obj); ok {
	// pod.Containers[0].SecurityContext is at <prefix>.containers[0].securityContext in obj.
	scanPod(pod, prefix)
}
```

- **What it holds:** the fields the kind's policy checks read, from the same mapping: the listed
  `containers` and `initContainers`, `volumes`, `securityContext`, `hostNetwork` where the spec
  has it (a `ThanosRuler`'s has none), `dnsPolicy`, and the `nameservers` of `dnsConfig`. A
  listed container named for one the operator generates is a patch the operator merges into that
  container, and is in the view as written.
- **What it does not hold:** the containers the operator generates itself. Their images,
  arguments and security context are the operator's, and launcher holds only what the author
  wrote. The `searches` and `options` of `dnsConfig` are not in the view either.

  | Kind | Generated containers | Generated init containers |
  |---|---|---|
  | `alertmanager` | `alertmanager`, `config-reloader` | `init-config-reloader` |
  | `prometheus` | `prometheus`, `config-reloader`, and `thanos-sidecar` where `thanos` is set | `init-config-reloader` |
  | `thanosruler` | `thanos-ruler`, `config-reloader` | none |
- **A copy:** a change to the view does not reach the object.
- **Typed objects only:** an unstructured monitoring object is converted to its typed object
  first. Any other object, a nil one included, gives `nil, "", false`.

Each monitoring workload kind adds its case from its own `monitoringWorkload` mapping;
`TestMonitoringPodSpec_CoversEveryRegisteredWorkloadKind` (`pkg/oam/builtin/registry`) fails on a
registered kind of the operator's API whose spec lists containers and that has no case.

## Declared endpoints of a chart

`helm`, `helmrelease` and `helmtemplate` implement `oam.EndpointProvider` from an
`endpoints` property (go-kure/launcher#1132). A chart renders pods this package cannot see,
so the author declares the pods a consumer connects to; nothing is derived from the chart.
One shared schema and parser (`chartEndpoints`) serve the three types.

- **An entry** is `{name?, protocol?, port, selector}`. `name` is empty or an IANA service
  name, unique among the component's non-empty names. `protocol` is `TCP` (the default),
  `UDP` or `SCTP`. `port` is a number from 1 to 65535 or an IANA port name (a string of
  digits is not a name). `selector` is required and has `matchLabels` alone, not empty: an
  empty selector would match every pod of the namespace. No other key is taken.
- **Not a chart value.** The key is split off before any strict decode and never forwarded:
  `helmrelease` decodes the rest as its `HelmReleaseSpec` (the schema-parity test names
  `endpoints` as its one exception), `helmtemplate` splits it off as it does `scopeOverrides`,
  and `helm` does not put it in `helmOwnedKeys`, which are forwarded, so the lowered members
  are the same with and without it, under either delivery.
- **The type's parse runs first.** A component the type refuses has no endpoint and is
  refused in the parse's words, with or without a declaration. A refused declaration names its
  path (`endpoints[1].selector.matchLabels`) and is refused by a build, by the lowering and by
  a direct `Endpoints` call alike. What the `helm` lowering refuses after its decode is not
  seen by `Endpoints`.
- **Application namespace and cluster only.** An endpoint's NetworkPolicy is created in the
  application namespace of the application's own cluster, so the declared pods must run
  there. A declaration beside a property that places the pods elsewhere is refused, because
  launcher cannot compare that place with the application's: an authored, non-empty
  `targetNamespace` (another namespace; `helmrelease`, and `helm` under Flux delivery) and a
  `kubeConfig` (another cluster; `helmrelease` only, `helm` and `helmtemplate` take none). A
  null `kubeConfig` reads as absent. `helmtemplate` renders into the application namespace and
  takes no `targetNamespace`. The other `HelmReleaseSpec` fields do not place the pods and are
  accepted: `storageNamespace` (where Helm keeps release state), `serviceAccountName` (the
  identity Helm acts as), the namespaces of the chart source (`chart.spec.sourceRef`,
  `chartRef`) and of `dependsOn`, `install.createNamespace` (creates the namespace the release
  already targets) and `commonMetadata` (labels and annotations only). `postRenderers` can
  patch an object's namespace, but a patch is opaque to launcher, as are the chart's own
  templates; the declared selector is the author's claim about the rendered pods either way.
- **No declaration** reports none, as before. A declaration is the only source: a workload
  type derives its endpoints from its container ports and takes no `endpoints` property.

## Declared outputs

`configmap`, `secret`, `postgresql`, `webservice` and `worker` implement `oam.OutputProvider`
(go-kure/launcher#1259): the names of the objects a component generates, read through
`Transformer.ComponentOutputs` before the document is built, and held by the build against
the names it gives (see "Outputs" in `site/content/api-reference/oam/names-and-overrides.md`). A name is the author's property,
else the `Naming` hook's, else the default, resolved by the code that names the object in the
lowering, so a query and a build cannot differ. A refused component answers no outputs and is
refused in the lowering's own words. No other builtin declares any.

| Type | Key | Object | Name |
|---|---|---|---|
| `configmap`, `secret` | `object` | the ConfigMap or Secret | `objectName`, else the component name |
| `postgresql` | `cluster` | Cluster | `clusterObjectName`, else the component name |
| `postgresql` | `app_secret` | Secret `<cluster>-app` | derived from `cluster`; **absent** when `recovery.secret.name` is set, because CloudNativePG then uses that Secret and generates none |
| `postgresql` | `objectstore` | ObjectStore | present when `backup` is set; `backup.objectStoreObjectName`, else the component name |
| `webservice`, `worker` | `deployment` | Deployment | `deploymentObjectName`, else the component name |
| `webservice` | `service` | Service | `serviceObjectName`, else the component name |
| `webservice`, `worker` | `serviceaccount` | ServiceAccount | `serviceAccountObjectName`, else the component name; **absent** when `serviceAccountName` names an existing account |
| `webservice`, `worker` | `claim_<volume>` | PersistentVolumeClaim | one per `pvc` volume that sets no `claimName`: the volume's `claimObjectName`, else `<component>-<volume>` with each hyphen in either part written `--`; hyphens in the key become underscores |

`app_secret` is the one derived output: the operator creates the Secret, so it is not in the
built application and no object of the build holds it. The build still re-applies the
derivation to the held cluster name and fails if the output was answered another name. The
inline object store of `recovery.source` is not declared. A claim's name is resolved without the ClusterProfile `pvc` capability, which
chooses its storage class and no name, so the query records no consumed capability key.
