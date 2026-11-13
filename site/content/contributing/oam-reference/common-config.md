---
title: "Common config"
weight: 1
---

Implementation, history and tests of the entries on the reference page
[Common config](../../api-reference/oam-components/common-config.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Common config

- **History:**
  - go-kure/launcher#423 (the six properties and `command`/`args` read through
    presence-reporting helpers).
  - go-kure/launcher#447 (a top-level entry with `name` absent or null refused by schema
    validation).
  - go-kure/launcher#453 (the parser message for a non-string at those positions).
  - go-kure/launcher#546 (a dotted or longer name refused at conversion by the Service-name
    rule).
  - go-kure/launcher#690 (`daemonset` and `statefulset` stop emitting a Service, and `port`
    goes).
  - go-kure/launcher#383 (`cpu` and `memory` declared as a string-or-number union).
  - go-kure/launcher#342 (the pod-level `resourceClaims` property accepted).
  - go-kure/launcher#1228 (a `sleep` longer than the pod's `terminationGracePeriodSeconds`
    refused).
  - go-kure/launcher#790 (the container `windowsOptions` key set, its breaking rules for a
    pod-level value, and the closed key set of the `affinity` shorthand).
  - go-kure/launcher#384 (`pvc.size` must be a positive quantity).
  - go-kure/launcher#761 (`volumeClaimTemplates.storageClass` keeps the absent-versus-empty
    distinction).
  - go-kure/launcher#793 (`oam.ShortenNameWithSuffix` shortens a qualified volume name over
    253 characters).
  - go-kure/launcher#1177 (a volume entry can author its source as the upstream object under a
    key named like its `type`).
  - go-kure/launcher#1172 (a `volumeMounts` item is a closed key set).
  - go-kure/launcher#1193 (one helper reads the mount keys under the rules of upstream
    `ValidateVolumeMounts`).
  - go-kure/launcher#1199 (`subPath` and `subPathExpr` refused with a `..` element in all four
    places).
  - go-kure/launcher#1198 (the main container reads the same mount keys).
  - go-kure/launcher#1212, go-kure/launcher#1223 (the mount rules held on the typed upstream
    mounts of the other kinds, with the error naming the container and the mount's position).
  - go-kure/launcher#321 (an `initContainers` entry is a closed key set).
  - go-kure/launcher#1225 (port names unique across the pod).
  - go-kure/launcher#1226 (a pod whose containers share a name refused when the document is
    read).
  - go-kure/launcher#1254 (a container name in either list must be a DNS-1123 label).
  - go-kure/launcher#1278 (the same check again when the pod is generated).
  - go-kure/launcher#448 (the `postgresql` component's affinity block contract).
  - go-kure/launcher#452 (a non-string `nodeSelector` value, a behavior change under
    `v1alpha1`).
  - go-kure/launcher#444 later folded the dedicated
    `optionalObject`/`optionalObjectList`/`optionalStringList` wrappers those six originally
    used into the base `parseObjectField`/ `parseStringField`/etc. helpers themselves —
    callers now use the base helpers directly, and the null-as-absence behavior applies
    uniformly rather than only through the wrappers. [the six: `env`, `command`, `args`,
    `initContainers`, `sidecars` and `affinity`]
  - `job` refused the name from its introduction; the other six gained the check in
    go-kure/launcher#407. [the name is a workload component name; `job` and the other six are
    the seven workload kinds `webservice`, `worker`, `deployment`, `statefulset`, `daemonset`,
    `cronjob` and `job`]
  - Until go-kure/launcher#405 all three were read with a bare comma-ok assertion, so a
    handler called directly — without the schema validation an authored document gets first,
    which already refused these shapes — dropped the value and emitted a container with no
    requests or limits. [all three: `resources`, `requests` and `limits`]
  - Until go-kure/launcher#423 they were mishandled twice over: a mistyped `command: /bin/sh
    -c true` fell through their comma-ok guard and the container built with no command at all,
    and `command: [ls, 3]` emitted `["ls"]` and said nothing about the `3`. Closing it changed
    their signature to return an error, which is why the fix touches all 9 call sites — the
    seven workload kinds' own main containers, plus `initContainers` and `sidecars` in
    `common.go` [they: `command` and `args`]
  - go-kure/launcher#660; before that the name and protocol were taken unchecked, and a
    non-list `ports`, an unknown key, a non-string name or a non-string protocol, which then
    read as `TCP`, was dropped [the sentence is about the entries of a sidecar's `ports`]
  - before go-kure/launcher#466 it was refused as `must be a string, got <nil>` [it: a
    `nodeSelector` value that is null, such as `rack: null`]

### Container fields

- **History:**
  - go-kure/launcher#790 (the seven container fields read on every container of a hand-parsed
    workload kind, and an `initContainers` entry's own `restartPolicy` and
    `restartPolicyRules`).
  - go-kure/launcher#1180 (`restartPolicy: Always` makes an init container entry a native
    sidecar).
  - go-kure/launcher#1225 (an init container's `ports` read as a `sidecars` entry's are).
  - go-kure/launcher#1183 (`restartPolicyRules` refused below Kubernetes 1.35, naming the gate
    and the version).
  - go-kure/launcher#1230 (the schema names the gate per value (`valueGates`), so `Always` is
    accepted at every version).
- **Tests:**
  - One parser (`parseContainerFields`, `container_fields.go`) and one schema fragment
    (`schemaContainerFields`) serve the three positions, pinned to each other by
    `TestContainerFieldsSchemaMatchesParser`. [the three positions: the main container, each
    `initContainers` entry and each `sidecars` entry]

### Referencing an existing claim (`pvc.claimName`)

- **History:**
  - go-kure/launcher#702 (a `pvc` volume with `claimName` mounts an existing claim).
  - go-kure/launcher#787 (the `claimObjectName` of a volume that synthesizes a claim, role
    `workload-volume-claim`).
  - go-kure/launcher#746 (an unauthored `storageClass` takes the `storageClassName` of the
    ClusterProfile `pvc` capability).
  - go-kure/launcher#761 (a `statefulset` `volumeClaimTemplates` entry takes the same
    default).

### Raw block volumes (`volumeMode: Block`)

- **History:**
  - go-kure/launcher#385 (a container consumes a Block claim through `volumeDevices` at a
    `devicePath`).
  - go-kure/launcher#790 (the published schema publishes `mountPath` and `devicePath` as two
    optional keys plus a required `exclusive` group).

### Pod-level properties

- **History:**
  - go-kure/launcher#342 (the seven kind components share one pod-level property surface,
    `parsePodSpec`).
  - go-kure/launcher#1183 (a property whose feature gate is off at the profile's
    `kubernetesVersion` refused).
  - go-kure/launcher#1231 (a profile's `spec.featureGates` replaces the release default for a
    gate it names).
  - go-kure/launcher#1230 (the gate named per value in the schema (`valueGates`)).
  - go-kure/launcher#1321 (the `template` of `cnpg-pooler` left open like the raw-PodSpec
    kinds, its gated paths named in `gatedPaths`).
  - go-kure/launcher#1245 (a gated path in an open node refused, naming the authored path).
  - go-kure/launcher#1330 (a gated value refused, naming the authored path and the value).
  - go-kure/launcher#702 (an unset pod kind names no account and sets
    `automountServiceAccountToken: false` unless that is authored).
  - go-kure/launcher#790 (the schema publishes the `resourceClaims` pair `resourceClaimName`
    and `resourceClaimTemplateName` as an `exclusive` group).
  - go-kure/launcher#312 (the shared `enforceExtraContainer` helper covers every
    `initContainers` and `sidecars` entry).
  - go-kure/launcher#1171 (the `security-context` trait merges into what is authored).
  - go-kure/launcher#790 made the container `windowsOptions` authorable [the container
    `windowsOptions`: `securityContext.windowsOptions`]

### Deployment-level properties (`deployment`, `webservice`, `worker`)

- **History:**
  - go-kure/launcher#343 (the kind-named `deployment` component).
  - go-kure/launcher#341 (`DeploymentSpec` parsed by `parseDeploymentSpec` into real `appsv1`
    types, from one fragment all three handlers compose).
  - go-kure/launcher#1216 (the pod template's `podMetadata` is the same on all three).

#### Main container ports

- **History:**
  - go-kure/launcher#280 (`deployment` publishes `ports`, the main container's
    `corev1.ContainerPort` list).
  - go-kure/launcher#1132 (`deployment`, `statefulset` and `daemonset` implement
    `oam.EndpointProvider` from the main container's `ports`).
  - go-kure/launcher#225 (`job`, `cronjob` and `worker` report no endpoint).
  - go-kure/launcher#334 (`daemonset`, `statefulset`, `job` and `cronjob` publish the same
    `ports`, with the same entry rules).
  - go-kure/launcher#343 (`deployment` dropped `port` and the Service it drove).
  - go-kure/launcher#690 (`daemonset` and `statefulset` drop `port` and the Service it drove
    (breaking), and replace it with `ports`).
  - go-kure/launcher#660 (a sidecar port of the same name as a main container port refused).

#### Raw scheduling properties

- **History:**
  - go-kure/launcher#412 (`deployment` publishes `affinity`, `tolerations` and
    `topologySpreadConstraints` as their plain `corev1` shapes, parsed by `scheduling.go`).
  - go-kure/launcher#790 (the other kind-named workloads (`statefulset`, `daemonset`, `job`,
    `cronjob`) publish the same shapes through the same parsers and schemas, and `statefulset`
    dropped the four-key `affinity` shorthand (breaking)).
  - go-kure/launcher#1473 (`webservice` and `worker` publish `tolerations` and
    `topologySpreadConstraints` as the same shapes, from the same schemas and parsers).

##### What `tolerations` changed for `daemonset`

- **History:**
  - go-kure/launcher#790 (`statefulset`, `job` and `cronjob` gained `tolerations`).
  - go-kure/launcher#690 (`daemonset` and `statefulset` emit no Service and dropped `port`,
    which they read as no port at `0`).
  - go-kure/launcher#1268 (`scaler` is available on `statefulset`, with the HPA's
    `scaleTargetRef` naming a `StatefulSet`).
  - go-kure/launcher#1178 (`deployment` reports its pod template labels, its own and the ones
    `podMetadata.labels` adds).
  - go-kure/launcher#1216 (the `deployment` member of a `webservice` or `worker` reports the
    same, authored `podMetadata.labels` included).
  - go-kure/launcher#794 (`statefulset`, `daemonset`, `job` and `cronjob` have no
    `PodTemplateLabels` method, deliberately (item 3 of that issue)).
  - go-kure/launcher#394 (a property read through one of the shared field helpers in
    `common.go`, which test presence with `authoredValue`, reads a null as omission on every
    kind, and six parsers that read their own property with a bare lookup (`envFrom`,
    `probes`, `lifecycle`, `securityContext`, `volumes`, `accessModes`) were converted to the
    same primitive).
  - go-kure/launcher#570 (the top-level keys still answered with a bare lookup and then
    type-checked read a null as omission: `timeZone`, `successfulJobsHistoryLimit` and
    `failedJobsHistoryLimit` of `cronjob`, `completionMode` of `job` and `cronjob`,
    `clusterScoped` of `passthrough`, `inline` and `url` of `crd` and `manifests`, and
    `scopeOverrides` of `manifests`).
  - go-kure/launcher#341 (`webservice` and `worker` publish `strategy`: an authored `strategy`
    on a `webservice` was accepted and silently discarded before, and under a non-RWX claim
    those two substituted `Recreate` silently).
  - go-kure/launcher#408 (`kurel build` calls `ValidateAuthoredProperties`, so an unrecognised
    key in a component's `properties` map is a build error).
  - go-kure/launcher#393 (`replicas` is validated the same way on every kind that reads it,
    where only the `deployment` handler did before).
  - go-kure/launcher#525 (an integer read from a lowering rule or a Go caller is checked
    against its own target and refused with an error rather than truncated or wrapped).
  - go-kure/launcher#1179 (under a `scaler` the Deployment or StatefulSet is built without
    `spec.replicas`).
  - go-kure/launcher#1196 (under a `scaler` the `topology-spread` trait counts the scaler's
    `minReplicas` on a Deployment).

## StatefulSet-level and claim-template properties

- **History:**
  - go-kure/launcher#361 (kure's `CreateStatefulSet` no longer writes `podManagementPolicy:
    OrderedReady` into the object, the release-1 builder contract making that constructor
    identity-only).
  - go-kure/launcher#385 (`devicePath` joined the claim entry's key set, and `volumeMode:
    Block` is accepted and rendered as a main-container `volumeDevices` entry).
  - go-kure/launcher#383 (`maxUnavailable` takes a positive integer or a 1-100% string, the
    schema leaf being the `Types: [integer, string]` union).
  - go-kure/launcher#761 (an authored `storageClass: ""` requests no class, and an unauthored
    entry takes the `storageClassName` of the ClusterProfile `pvc` capability (a pre-GA output
    change)).
  - go-kure/launcher#384 (the `volumes[].pvc.size` surface on every kind applies the
    positivity rule).
  - go-kure/launcher#570 (a container's resource quantities under any name and the top-level
    keys once read with a bare lookup read a null as omission).
  - go-kure/launcher#790 (an authored StatefulSet-level `spec.template` is rejected with an
    error, where the parser dropped it silently before; the pod template is projected from the
    component's own container and pod-level properties, as on `deployment`).
  - go-kure/launcher#394 extended the same treatment to the shared field helpers and to the
    parsers it converted, so it reaches well beyond these fields — but not the whole package.
    [the same treatment: reading an explicit null as omission; these fields: the optional
    StatefulSet-level and claim-level fields above]

