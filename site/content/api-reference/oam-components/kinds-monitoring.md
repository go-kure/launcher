---
title: "Kinds: monitoring"
weight: 8
---

- **alertmanager** (go-kure/launcher#790) is the kind-named projection of an
  Alertmanager of the Prometheus operator's `monitoring.coreos.com/v1` API. It
  is built on `policyHeldKind` (`policyFreeKind` with a policy check, see
  **storageclass**, below) and emits that one object in the build namespace,
  named after the component unless `objectName` names it; the handler adds no
  label, no annotation and no default, and declares the object as namespaced. Launcher
  emits no pod, no StatefulSet, no Service and no Secret for it: the operator
  builds a StatefulSet from the object and runs the pods. What the spec says
  of those pods is held to the environment policy as a workload kind's own
  fields are.

  **No capability is required, and none gates the kind.** As for the four
  kinds under **servicemonitor**, below, launcher does not ask
  whether the cluster serves `monitoring.coreos.com/v1`: where the operator's
  CRDs are not installed the
  component builds, and the object is refused at apply. Whoever may author a
  component may author an Alertmanager, and so make the operator run pods in
  the namespace; the policy below is what holds them. A policy can keep the
  kind out of a build through the object kind policy (`oam.ObjectKindPolicy`,
  go-kure/launcher#922).

  **Authored.** The properties are the top-level json fields of
  `AlertmanagerSpec`, decoded strictly at every depth (an unknown key is
  refused wherever it sits: a container, a claim template, a web setting),
  less the three under "Not authorable":
  - the image: `image`, `version`, `imagePullPolicy`, `imagePullSecrets`;
  - the pods: `replicas`, `resources`, `storage`, `volumes`, `volumeMounts`,
    `containers`, `initContainers`, `securityContext`, `podMetadata`, where
    they are scheduled (`nodeSelector`, `affinity`, `tolerations`,
    `topologySpreadConstraints`, `schedulerName`, `priorityClassName`) and
    their settings (`serviceAccountName`, `automountServiceAccountToken`,
    `hostNetwork`, `hostUsers`, `hostAliases`, `dnsPolicy`, `dnsConfig`,
    `enableServiceLinks`, `terminationGracePeriodSeconds`);
  - the StatefulSet: `serviceName`, `podManagementPolicy`, `updateStrategy`,
    `minReadySeconds`, `persistentVolumeClaimRetentionPolicy`;
  - Alertmanager itself: `configSecret`, `alertmanagerConfiguration`,
    `alertmanagerConfigSelector`, `alertmanagerConfigNamespaceSelector`,
    `alertmanagerConfigMatcherStrategy`, `alertmanagerConfigFileSystemMode`,
    `secrets`, `configMaps`, `retention`, `logLevel`, `logFormat`,
    `externalUrl`, `routePrefix`, `listenLocal`, `portName`, `web`,
    `limits`, `enableFeatures`, `additionalArgs`, `paused`;
  - `alertmanagerConfigFileSystemMode` takes `DenyAll` or
    `FromAlertmanagerConfigMatcherStrategy`. `AllowAll`, which lets every
    AlertmanagerConfig object read the files of the alertmanager container,
    is refused with an error naming the field, because it loosens the
    operator's default; whether an app.yaml may set it is open
    (go-kure/launcher#1300). The operator applies the field at every
    Alertmanager version;
  - its cluster: `additionalPeers`, `clusterAdvertiseAddress`,
    `clusterGossipInterval`, `clusterLabel`, `clusterPushpullInterval`,
    `clusterPeerTimeout`, `clusterPeerName`, `clusterTLS`,
    `forceEnableClusterMode`.

  The type's comments say that an entry sharing its name with a container the
  operator generates is merged into it. For an Alertmanager the operator
  generates `alertmanager` and `config-reloader` under `containers`, and
  `init-config-reloader` under `initContainers` (`makeStatefulSetSpec` in
  `pkg/alertmanager/statefulset.go` of prometheus-operator v0.94.1, the
  version the linked module is cut from); it generates no `thanos-sidecar`,
  so an entry of that name is a container of its own. Of the three, only
  `alertmanager` takes its image from the spec; the two reloaders run the
  image of the operator's own configuration unless an entry patches them.
  Launcher holds every entry the same way, patch or not. `additionalArgs` is
  passed to the alertmanager container as written; only an argument's name is
  read, to refuse one that names a flag the operator generates (below). An
  argument can still change what the other fields configure.

  **Not authorable: `baseImage`, `tag` and `sha`.** Each is refused when not
  empty, beside an `image` too, with or without a policy (`tag: not
  authorable: the Prometheus operator deprecates the field, and composes the
  image it yields outside what the object states; use image`). The three are
  deprecated upstream, and the image they yield is composed in operator code
  outside the linked module, so the kind cannot say which image runs and has
  nothing to hold to the allowed registries or the tag rule. They are not in
  the schema, so a caller that validates the properties against it first, as
  `kurel build` does, refuses each as an unsupported field whatever its value,
  an empty one included. A caller that converts the component without that
  validation gets the text above, and there an authored empty string is the
  object an absent one is, and builds.

  **The kind's required list is generated** (go-kure/launcher#883; see
  ["Required fields a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)): every
  field the API requires that the Go type writes whether or not it was
  authored is refused when unauthored, each where its parent is authored
  (`hostAliases[1].hostnames: required (…)`), the fields of the Kubernetes
  types the spec embeds included. No top-level field is one. Among them are
  the fields the kind refused before, each with its text:
  - an additional argument's `name`, a DNS option's `name`, a host alias's
    `ip` and `hostnames`;
  - the `type` of an `updateStrategy`;
  - under `alertmanagerConfiguration.global`: the `host` and the `port` of
    `smtp.smartHost`, and the `clientId`, `clientSecret` and `tokenUrl` of
    `httpConfig.oauth2`;
  - the `key` and `operator` of a match expression, in every label selector
    the spec holds (`affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution[0].labelSelector.matchExpressions[0].operator:
    required (…)`; the selectors are listed under ["A label selector's match
    expressions"](conventions.md#a-label-selectors-match-expressions)). Presence only, as on the monitors.

  And, refused since go-kure/launcher#883 (**breaking** for a document that
  leaves one out under an authored parent): the required fields of the
  Kubernetes types the spec embeds (a container's or a volume's `name`, the
  `key` of a Secret or ConfigMap key reference, a pod affinity term's
  `topologyKey`, a sysctl's `name` and `value`, a seccomp profile's `type`),
  and the `username` and the `password` of
  `alertmanagerConfiguration.global.httpConfig.basicAuth`, which the
  operator refuses left out (the API server accepts them left out, shown on the
  operator's CRD). A `clusterTLS` without
  its `server` or its `client` stays refused, by the check of the TLS blocks
  below, which comes first. The `keySecret` of `web.tlsConfig` and of
  `clusterTLS.server` is asked for only where it is authored: the key may
  be read from `keyFile` instead.

  The list is derived from the markers of the linked module's source, which
  ships no CRD; the API server answers for it on the operator's CRD, vendored
  unmodified from the operator's repository at the linked release
  (`testdata/upstream/prometheus-operator/`). **Not refused:** the `apiVersion`,
  `kind`, `name` and `uid` of an owner reference in an ephemeral claim
  template's metadata (`storage.ephemeral`, `volumes[].ephemeral`): the CRD
  declares that metadata as an object with no field, so the API server prunes
  an owner reference there, authored whole or not, and requires none of its
  fields; and
  two fields of a listed
  container or init container and two of a listed volume that upstream marks
  required and the type leaves out when empty (the `action` of a container
  restart rule and the `operator` of its exit codes, the `signerName` and
  `keyType` of a pod certificate source), as on the **pod** kind.

  **An authored `0` or `""` the type cannot carry is refused.** As on the pod
  kinds, on a listed container, init container or volume: a
  `timeoutSeconds`, `periodSeconds`, `successThreshold` or `failureThreshold`
  written as `0` on a liveness, readiness or startup probe
  (`containers[0].readinessProbe.periodSeconds: 0 cannot be carried by the
  Prometheus operator API types (…)`); an empty string the API server
  defaults, such as a container's `imagePullPolicy`, a port's `protocol`, an
  `httpGet` `path` or `scheme`, or a volume source's default; and, under
  `hostNetwork: true`, a port's `hostPort` written as `0`, which the API
  server would set to the port's `containerPort`. The refusal holds for an
  entry that patches one of the operator's own containers too: the zero is
  left out there as anywhere, and what then applies is the operator's value
  for that container, not the authored one. As on the other kinds of the
  operator's API: an empty `portName`, `retention` or
  `alertmanagerConfigMatcherStrategy.type`, which the CRD defaults to `web`,
  `120h` and `OnNamespace` (`portName: "" cannot be carried by the
  Prometheus operator API types (…)`). An empty `schedulerName` or
  `imagePullPolicy` is refused too: the operator copies each one unchanged
  to the pods, the second to its own containers, where the API server
  defaults an empty one. `TestMonitoringWorkloadKinds_DefaultedZeros` derives
  the list. It reads the type, the default markers of the operator's source,
  the field comments of the Kubernetes types, and the API server's defaulting
  code for their strings. It then shows each field refused on a document.
  `TestMonitoringWorkloadKinds_HostNetworkHostPort` shows the `hostPort`
  refusal. An authored `hostNetwork: false` is left out, and the API reads an
  absent one as `false`.

  **What the operator builds from the spec is checked where the API or the
  operator would break it.** The kind leaves to the API what the CRD's own
  schema refuses when the Alertmanager is applied (a `retention` of `1.5h` or
  `1d`, for one), which shows at once; it refuses
  what the CRD admits but the operator or the API then refuses on the
  StatefulSet or the pods built from it, which would fail late and out of
  sight. Each is refused, under any policy and none, at
  `pkg/alertmanager/statefulset.go` of prometheus-operator v0.94.1:
  - `version` unset where `image`, or a listed `alertmanager` entry, names the
    image: the operator chooses the container's flags by `version`, and by the
    deployed operator's default where none is, which a build cannot know and
    need not be the version the image runs (`version: required
    where image, or an entry of containers named alertmanager, names the
    image`); and a `version` the operator fails the reconcile on: one
    `semver.ParseTolerant` cannot parse, one under 0.15.0, or one of a major
    version above 0 (operator.go:902-909).
  - a field the operator reads only from some Alertmanager version on, where
    `version` is below that version. An unset `version` (which is required
    only where the image is named) is judged against the operator release
    launcher vendors (v0.95.1, default v0.34.2): the deployed operator's
    default is unknown at build time, and at v0.34.2 no field below is
    refused. Below its version the operator leaves the flag off the
    container, the TLS or HTTP configuration out of the web server, the gossip
    TLS configuration off the cluster, or the field out of the global
    configuration, or refuses the AlertmanagerConfig object `name` names at
    reconcile (`clusterPeerName: read by the Prometheus operator only for
    Alertmanager 0.30.0 and later; at version v0.28.1 it does not pass it to
    Alertmanager, and the pods run without it; raise version to 0.30.0 or
    later, or leave clusterPeerName unset`). Several such fields are
    refused together, each named with its version and no consequence
    claimed for any, as on `prometheus` (go-kure/launcher#1022). Pods: `logFormat` other than `logfmt` (0.16.0),
    `web.getConcurrency` and `web.timeout` (0.17.0), `web.tlsConfig` and
    `web.httpConfig` (0.22.0), `clusterTLS` (0.24.0), `clusterLabel` (0.26.0),
    `enableFeatures` (0.27.0), `limits.maxSilences` and
    `limits.maxPerSilenceBytes` (0.28.0), `clusterPeerName` (0.30.0). Under `alertmanagerConfiguration.global`:
    `httpConfig.authorization`, `httpConfig.oauth2` and
    `httpConfig.followRedirects` (0.22.0); `telegram` (0.24.0); `webex`,
    `httpConfig.enableHttp2`, `httpConfig.tlsConfig.minVersion` and `maxVersion`,
    and `httpConfig.oauth2.proxyUrl`, `noProxy` and `proxyConnectHeader`
    (0.25.0); `httpConfig.proxyFromEnvironment` set to true,
    `httpConfig.noProxy` and `httpConfig.proxyConnectHeader` (0.26.0); `jira`,
    `rocketChat` and `smtp.tlsConfig` (0.28.0); `smtp.forceImplicitTLS`,
    `telegram.botToken` and `telegram.botTokenFile` (0.31.0); `mattermost`
    (0.32.0).
    `TestAlertmanagerVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's package `pkg/alertmanager` (every Go file of
    it, its tests aside; at v0.95.1 the comparisons are in `statefulset.go`,
    `amcfg.go` and `operator.go`) and holds each to this list, or to the
    reason it gates no field of this kind (the configuration of a receiver, a
    route or a configSecret Secret); the files, with the operator's
    `pkg/operator/defaults.go` and `pkg/operator/validations.go`, are
    vendored unmodified under
    `testdata/upstream/prometheus-operator`, with the project's LICENSE and
    NOTICE and a SOURCE file naming each file's git blob id, and
    `mise run vendor-prometheus-operator-gates <tag>` re-fetches them for the
    tag `go.mod` links.
  - an `enableFeatures` Alertmanager exits on at startup, at `version` (unset,
    the operator's default v0.34.2). The operator passes the list from 0.27.0
    on, joined with `,`, as `--enable-feature` (statefulset.go:311-315), and
    Alertmanager splits it on `,` and exits on an element that is not a feature
    flag of its version (`featurecontrol/featurecontrol.go:135-180`,
    `cmd/alertmanager/main.go:104-108` at v0.34.0). So an element with a `,`
    in it is two, an empty element is refused (`["classic-mode", ""]`), and
    `classic-mode` with `utf8-strict-mode` is refused. A list of one empty
    element is refused at any version: the operator writes its empty value as
    `--enable-feature` alone (`pkg/operator/argument.go:69-75` at v0.94.1),
    which Alertmanager's flag parser refuses (kingpin v2.4.0
    `flags.go:126-130`) before it exits; leave `enableFeatures` unset for no
    feature. The joined list is read as the kubelet passes it, which reads
    `$$` as `$` and leaves `$(` without its closing parenthesis as written; an
    element that refers to an environment variable, as `$(AM_FEATURE)` or
    `x$(AM_FEATURE)`, is not refused, since the kubelet expands it to a value
    the component cannot see, and the other elements are judged
    (`["no-such-feature", "$(AM_FEATURE)"]` is refused). `classic-mode` with
    `utf8-strict-mode` is refused where neither refers to one. A reference is
    found in the joined list before it is split, so one whose name holds a `,`
    spans elements. A reference runs from `$(` to the first `)` after it; one whose name no
    variable can have, an empty one or one with `=`, a character beyond ASCII
    or one not printable, as `$()`, is passed as written and judged as such. The
    names, by minor: `classic-mode`, `receiver-name-in-metrics`
    and `utf8-strict-mode` from 0.27;
    `auto-gomaxprocs` and `auto-gomemlimit` from 0.28; `alert-names-in-metrics`
    from 0.31; `event-recorder` and `group-key-in-metrics` from 0.33, which
    drops `auto-gomaxprocs` (`enableFeatures: not a feature flag of
    Alertmanager 0.33, at version v0.33.0: …; it takes alert-names-in-metrics,
    …`). A prerelease is judged at its minor. Above 0.34 the names are not
    known, and none is refused. `TestAlertmanagerFeatureFlags_MatchVendoredSource`
    holds the names, and `NewFlags` statement by statement, to
    `featurecontrol.go` of each minor's latest release,
    vendored unmodified under `testdata/upstream/alertmanager` with the
    project's LICENSE, each release's NOTICE and a SOURCE file naming each
    file's git blob id; `mise run vendor-alertmanager-features 0.27 0.34`
    re-fetches them.
  - a `portName` the API refuses where the operator writes it: not an IANA
    service name (at most 15 characters, lower-case letters, digits and `-`);
    unless `listenLocal` is set, `mesh-tcp`/`mesh-udp`, the container ports the
    operator adds beside the web port; and unless `serviceName` names a Service
    of the author's, `tcp-mesh`/`udp-mesh`, the ports of the governing Service
    the operator creates (statefulset.go:223-250, :483-502).
  - a negative `replicas`, which the operator runs as 0.
  - `storage.volumeClaimTemplate.metadata.name` beside `storage.emptyDir` or
    `storage.ephemeral`, other than `alertmanager-<name>-db`: the operator
    mounts the data volume under that name and creates it as
    `alertmanager-<name>-db`, so the pods mount a volume that does not exist.
  - a storage arm in use whose claim the API refuses: the claim template arm
    (which an unset arm selects, an empty `storage` included) without
    `spec.resources.requests.storage` (unset or empty access modes, which are
    not serialized, the operator writes as `ReadWriteOnce`); the ephemeral arm
    without a claim template, its access modes or its storage request, which
    the operator uses as written; and on either, a storage request of `0` or
    less, as the API requires a positive one.
  - on the claim template arm, a claim template name that is not a DNS-1123
    label, which the operator names the data volume with, or that names a
    volume the operator adds, which the StatefulSet controller then replaces
    with the claim.
  - an entry of `volumes` named as a volume the operator adds: `config-volume`,
    `tls-assets`, `config-out`, `web-config`, `cluster-tls-config`,
    `notification-templates` where
    `alertmanagerConfiguration.templates` is set, the name the operator derives
    for each of `secrets` and `configMaps` (`secret-<name>`,
    `configmap-<name>`), and the data volume's: beside `storage.emptyDir` or
    `storage.ephemeral` a second volume of one name, and on the claim template
    arm one the StatefulSet controller replaces with the claim
    (`volumes[0] "config-volume": the name is a volume the Prometheus operator
    adds to every Alertmanager's pods; name the volume otherwise`).
    `web-config` and `cluster-tls-config` are refused whatever `version`
    names, though the operator adds them only for Alertmanager 0.22.0 and
    0.24.0 on, so that raising `version` does not turn a volume of the
    author's into a clash. The volumes of the web and cluster TLS credentials
    are not checked: the operator names each after the credential's source
    with a hash appended, which the kind does not derive, so an entry under one
    of those names is left to the API to refuse. Two entries of `secrets`, or of `configMaps`,
    that the operator's naming gives one volume name (`alerts.config` and
    `alerts-config` both name `secret-alerts-config`) are refused as well:
    the operator adds a volume for each (statefulset.go:638-690). So is an
    entry whose volume name, cut to 63 characters, ends in `-`: the operator
    checks the name after the cut and fails to build the pods
    (`ResourceNamer.DNS1123Label`, statefulset.go:640-643).
  - an entry of `volumeMounts` at a path the operator mounts a volume at in
    the alertmanager container, as the API refuses two mounts at one path:
    `/alertmanager`, `/etc/alertmanager/config`, `config_out` and `certs`, the
    web and cluster TLS configuration files whatever `version` names,
    `/etc/alertmanager/templates` where `alertmanagerConfiguration.templates`
    is set, and `/etc/alertmanager/secrets/<name>` and
    `/etc/alertmanager/configmaps/<name>` for each entry of `secrets` and
    `configMaps` (:531-556, :575-692). The mounts of the web and cluster TLS
    credentials are left to the API, as their volumes are.
  - an entry of `volumeMounts` that breaks a rule of a mount
    (go-kure/launcher#1211, go-kure/launcher#1227): a `subPath` beside a
    `subPathExpr`, either one absolute or holding a `..` element, a
    `mountPropagation` that is not `None`, `HostToContainer` or
    `Bidirectional`, a `recursiveReadOnly` that is not `Disabled`,
    `IfPossible` or `Enabled`, `Enabled` or `IfPossible` without
    `readOnly: true` or beside a propagation other than `None`, and
    `Bidirectional` where the alertmanager container is not privileged. The
    operator copies the entry as written and merges into it the mount of a
    listed `alertmanager` container at the same `mountPath`, so the entry is
    judged as merged: that mount can supply `readOnly` or a propagation, and
    only `securityContext.privileged: true` on that listed container makes the
    container privileged. Checked before the path.
  - an entry of `containers` or `initContainers` with a `volumeMounts` entry
    that breaks the same rules, naming the container and the mount. An entry
    named for a container the operator generates is merged into it, and the
    merge can take `readOnly: true` from the operator's own mount at the path,
    so such an entry is not refused for `recursiveReadOnly` without
    `readOnly: true`; every other rule holds, and `Bidirectional` needs that
    entry's own `securityContext.privileged: true`.
  - an entry of `volumeMounts` at the path of an earlier one written alike: the
    API compares mount paths as written and refuses two mounts at one path
    (`pkg/apis/core/validation/validation.go:3177-3227` at Kubernetes
    v1.37.1). The message names both entries.

    Admitted: a path written otherwise than another entry's, or than a path
    the operator mounts at, as `/data/` or `data` beside `/data`, or
    `/alertmanager/`: both mounts are made, and one may hide the other, the
    operator's included; where the kubelet makes the two paths equal (a
    relative path and its absolute form), CRI-O makes only one of them
    (go-kure/launcher#1050). The kubelet makes a relative path absolute and
    keeps the mounts in order (`makeMounts`,
    `pkg/kubelet/kubelet_pods.go:280-441`); containerd keeps that order at one
    cleaned path, the later mount on top
    (`internal/cri/opts/spec_linux_opts.go:69` at containerd v2.4.2); CRI-O
    sorts without keeping it, and keeps one of two mounts the kubelet made
    equal (`server/container_create.go:1290-1306` at CRI-O v1.37.2); runc
    makes every mount it is given. A hidden volume is not judged.
  - an entry of `additionalArgs` whose name, or that name with `no-` added or
    taken away, is a flag the operator generates for the spec: it then fails
    to build the pods (`BuildArgs`, `pkg/operator/argument.go:26-79`). The
    flags are `config.file`, `storage.path`, `data.retention`,
    `web.listen-address`, `web.route-prefix`, `cluster.reconnect-timeout`,
    `cluster.listen-address` (written `cluster.listen-address=` on one
    replica without `forceEnableClusterMode`), `cluster.peer` where a replica
    or `additionalPeers` is, and the flag of each field set among
    `externalUrl`, `enableFeatures`, `web.getConcurrency`, `web.timeout`,
    `limits`, `logLevel` other than `info`, `logFormat` other than `logfmt`,
    `clusterAdvertiseAddress`, the three cluster durations and `clusterTLS`
    (statefulset.go:289-508, :700-748), and `cluster.peer-name`,
    `cluster.label` and `web.config.file`. A flag the operator generates only
    from some version on is refused from that version on (`cluster.peer-name`
    0.30.0, `enable-feature` 0.27.0, `cluster.label` 0.26.0,
    `cluster.tls-config` 0.24.0, `web.config.file` 0.22.0, `web.get-concurrency`
    and `web.timeout` 0.17.0, `log.format` 0.16.0, the `limits` flags 0.28.0),
    and always where `version` is unset: the operator then runs its default
    version, v0.34.2 (`DefaultAlertmanagerVersion`, pkg/operator/defaults.go:25),
    above every one of them, and no flag is generated only up to a version. Not
    `dispatch.start-delay`: the operator leaves its own out where an argument
    names it.
  - an entry of `alertmanagerConfiguration.templates` whose key an earlier
    entry names, `configMap` or `secret`: the operator projects each key at
    the path of its name and skips a later entry of a key it has projected
    (statefulset.go:575-620), so that template would not be loaded.
  - a `web.tlsConfig`, `clusterTLS.server` or `clusterTLS.client` the
    operator's own validation refuses (a certificate and a key, each named
    once, and a certificate for the client; webconfig.New,
    clustertlsconfig.New): it then builds no pods.
  - `dnsPolicy: None` without `dnsConfig.nameservers`, and a pod-level
    `securityContext.windowsOptions.hostProcess: true` without
    `hostNetwork: true`: the API refuses the pods the operator copies them
    into.
  - a negative request or limit in `resources`, as merged, or in a listed
    container: the CRD's quantity pattern admits a sign, and the API refuses
    the container the operator builds with it.
  - a name the operator's objects cannot be named after: the data volume
    `alertmanager-<name>-db`, unless a claim template's name names it, and the
    hostname `alertmanager-<name>-<replicas-1>` of the last pod must each be a
    DNS-1123 label; and the StatefulSet `alertmanager-<name>` must be at most
    52 characters: the StatefulSet controller labels each of its pods
    `controller-revision-hash` with that name, a `-` and a hash of up to 10
    characters, a label value the API refuses beyond 63 characters. So a name
    has no dot and at most 39 characters. The hostname and the label do not
    apply where `replicas` is 0, with no pod to name or label. The refusal
    names the component, or `objectName` where that set the name.

  **The API's expression rules are not checked.** The types the spec reaches
  state one: an `updateStrategy` with a `rollingUpdate` must have the type
  `RollingUpdate`. The build reads no CRD (the linked module ships none; the
  operator's CRD vendored for the tests, go-kure/launcher#883, is test data),
  so the rule is not checked at build: it is listed
  (`alertmanagerRulesLeft`) and left to the API server, which refuses the
  object at apply, and
  `TestMonitoringWorkloadKinds_RulesListed` fails when a dependency bump adds,
  drops or rewords a rule on a reached type. The API's other value rules
  (formats, enumerations, minima) are left to it as well.

  **What the type writes unauthored.** Every Alertmanager carries
  `resources: {}` and `alertmanagerConfigMatcherStrategy: {}`; the API
  defaults the `type` of the second to `OnNamespace`. An authored `storage`
  carries a `volumeClaimTemplate` whether or not one was authored, with
  `metadata: {}` and `status: {}` beside its `spec`; upstream documents that
  an `emptyDir` or an `ephemeral` takes precedence over it.

  **With or without a policy,** an authored `image` (unless a patch of
  `alertmanager` names an image, which replaces it in the container the
  operator builds, so only the patch's is run and held), the authored image
  of a listed container or init container, and the `reference` of an image
  volume are held to the tag rule (`ValidateImageRef`: `image: image "…" rejected:
  :latest tag not allowed`), and a resource block's request may not exceed
  its limit (`resources: cpu: request 2 must not exceed limit 1`,
  `containers[0] "proxy": resources: memory: request 2Gi must not exceed
  limit 1Gi`). The alertmanager container runs with `resources` as the
  operator fills it, an unset memory request as 200Mi whatever the limit,
  and with the requests and limits of a listed `alertmanager` entry merged
  over that block key by key (`makeStatefulSetSpec`, same source); the
  checks hold that merged block with the 200Mi filled where it names no
  memory request, and not the patch's block alone. So hugepages need no
  authored cpu or memory beside them, the filled request naming memory, and
  an extended resource the patch requests meets a limit named in
  `resources`. A memory limit under 200Mi with no memory
  request in either is refused, as the API would refuse the pods
  (`resources: memory: the unset request the Prometheus operator fills as
  200Mi must not exceed limit 100Mi; …`; with a patch, the path is
  `resources with containers[0] "alertmanager" merged over it`), while a
  request the patch names replaces the 200Mi and is held with the limit of
  `resources`. An entry named for a container the operator generates, in
  the list the operator generates it in, is merged into it, so such a patch
  may name no image; any other listed entry that names none is refused, as
  no pod could run it (`containers[1] "proxy": names no image, and the
  Prometheus operator generates no container of that name to merge it into;
  …`). A name listed twice in `containers` or `initContainers` is refused
  (`containers[1] "config-reloader": the name is listed already at
  containers[0], …`): the operator keeps only the last entry of a name
  (`MergePatchContainers`, `pkg/k8s/merge.go` at v0.94.1), so the policy
  would hold an entry that never runs. A name shared by an init container and
  a container of the pods, generated or listed, is refused, as the API
  requires the two lists' names to be unique together
  (`containers[0] "init-config-reloader": the name is also that of the init
  container the Prometheus operator generates, …`). A patch's ports are
  merged into the ports the operator gives the generated container by
  number, as a strategic merge does (`MergePatchContainers`, same source):
  in the patch's order, each port is merged into the first port of its
  number, the operator's or one an earlier port of the patch added, and
  replaces the name and protocol it names; a port of a number no port has is
  added. Where the operator gives the container no port (config-reloader
  under `listenLocal`), the merge takes the patch's ports as listed. The
  operator's ports, in its order, are the web port under
  `portName` at 9093/TCP and the config-reloader's `reloader-web` at
  8080/TCP unless `listenLocal` is set, `mesh-tcp` at 9094/TCP then
  `mesh-udp` at 9094/UDP, and the init-config-reloader's `reloader-init` at
  8081/TCP. A container whose ports, merged so, or as a listed container
  that is not a patch lists them, name two ports alike is refused, as the
  API refuses two ports of one name (`containers[0] "alertmanager":
  ports[1] "mesh-udp" at 9000/UDP: the Prometheus operator merges the
  patch's ports into the container's by number, which leaves another port
  of that name, the Prometheus operator's port at 9094/UDP, …`; a port at
  9094 renames `mesh-tcp`, never `mesh-udp`). A patch may rename a
  generated port and reuse its name, and a later port of the patch merges
  into one an earlier port added. A patch that renames the web port (a port
  at 9093 of another name than `portName`) is refused while a liveness or
  readiness probe the operator writes still names `portName`: the probe then
  names a port the container does not have, and fails (`containers[0]
  "alertmanager": ports[0] "http" at 9093/TCP renames the web port, whose
  name portName "web" the Prometheus operator's liveness and readiness
  probes name; …`; go-kure/launcher#963). The operator writes both probes
  only where `listenLocal` is unset (`pkg/alertmanager/statefulset.go:409-444`
  at prometheus-operator v0.94.1). A probe of the patch that sets no handler
  type keeps the operator's handler; one that sets any gives the patch's
  handler (`MergePatchContainers` and `sanitizeProbeHandlers`,
  `pkg/k8s/merge.go:27-121`), so a patch that gives each probe a handler
  that does not name `portName` builds, and so does one that adds a port of
  `portName`'s name at another number, as a probe names its port by name. Two
  `volumes` of one name are refused,
  as the API refuses the pods (`volumes[1] "scratch": the name is listed
  already at volumes[0], …`), and so is a `serviceName` that is not a
  DNS-1123 label, the rule of every Service name in this package: the API
  refuses a Service of another name, and the operator fails the reconcile
  where it finds no governing Service of the name (`serviceName:
  "alerts.v1" is not a valid Service name, which must be a DNS-1123 label:
  …`). A name starting with a digit (`1alerts`) is accepted by that rule and
  refused when `spec.kubernetesVersion` is below 1.36, where
  RelaxedServiceNameValidation is off by default (go-kure/launcher#959,
  go-kure/launcher#1230). `retention`, `clusterGossipInterval`, `clusterPushpullInterval` and
  `clusterPeerTimeout` are refused where they parse as a duration of 0 or
  less (`retention: "0s" is not a positive duration: …`): the operator
  empties such a value before it builds the StatefulSet and runs the pods as
  if the field were unset (`discardZeroDurations`, same source). A value
  that does not parse is left to the API's pattern.
  A nonempty `externalUrl` that Alertmanager exits on at startup is refused
  too, though the operator and the API accept it: the operator passes it
  unchanged as `--web.external-url`, and Alertmanager exits where Go's
  `net/url` cannot parse it, and, from v0.19.0 on, where its scheme is not
  `http` or `https` (`externalUrl: not a URL of scheme http or https: …`;
  `cmd/alertmanager/main.go` of v0.28.1, `app/url.go` of v0.34.0). The scheme
  is held where `version` is unset or names v0.19.0 or later, its
  prerelease `v0.19.0-rc.0` included; an earlier version takes any scheme. Neither message names the value.
  A nonempty `clusterAdvertiseAddress` that is not an IP address and a
  numeric port written as `host:port` is refused where the cluster runs, at
  `replicas` other than 1 (0 included: the object configures the cluster for
  the pods a scale-up starts) or with `forceEnableClusterMode`
  (`clusterAdvertiseAddress: not an IP address and a numeric port, …`): the
  operator passes it unchanged as `--cluster.advertise-address`, and
  Alertmanager, from v0.15.0 to v0.34.0, splits it with `net.SplitHostPort`,
  reads the port with `strconv.Atoi` and gives the host to memberlist, which
  takes only an IP address; any failure ends the cluster's creation, and
  Alertmanager exits (`cluster/cluster.go:162-171` and `app/app.go:214-234` of
  v0.34.0). The host is judged an IP address in full only where
  Alertmanager is built with Go 1.23 or later, whose
  `net.ParseIP` the check uses: from v0.28.0 on, its prerelease
  `v0.28.0-rc.0` included (`.promu.yml`), and where `version` is unset. This
  is a limit: older Alertmanager versions parse IP addresses with older Go
  rules, which this check does not model (they take some leading zeros Go
  1.23 refuses, as `010.0.0.1` or `fd00::00001`), so before v0.28.0 only
  `host:port`, a numeric port and a host of the characters an IP address has
  (hex digits, `.` and `:`) are held. A host with any other character, as the
  host name `alerts.example.com`, is no IP address for the `net.ParseIP` of
  any Go and is refused at every version; one of those characters only, as
  `cafe.dead`, is not refused before v0.28.0. An empty host, as in `:9094`, is
  not refused: Alertmanager then works the address out itself; nor is a port
  number no socket has, which memberlist truncates. The port is read as
  `strconv.Atoi` reads it on a 32-bit image (the armv7 one), so one above
  2147483647 is refused whatever the architecture. The value is read as the
  kubelet passes it, which reads `$$` as `$` and leaves `$(` without its
  closing parenthesis as written; one that refers to an environment variable,
  as `[$(POD_IP)]:9094`, is not refused, since the kubelet expands it to a
  value the component cannot see (`expand.go` of Kubernetes v1.37.1). A
  reference runs from `$(` to the first `)` after it. One whose name no
  variable can have, an empty one or one with `=`, a character beyond ASCII
  or one not printable (the API refuses each as a variable's name), as
  `[$()]:9094`, `[$(a=b)]:9094` or `[$(é)]:9094`, is passed as written, and
  the value is judged as a literal one, so those three are refused. With the
  cluster off the value is not read. The message does not name the value.

  **Policy.** `ApplyPolicy` refuses or passes; it writes nothing, and without
  a policy the same component builds. Refused, each with the class a workload
  kind's refusal has:
  - `image` where no patch of `alertmanager` replaces it, the image of a
    listed container or init container (a patch of `alertmanager` included),
    and an image volume's reference, outside the allowed registries
    (`oam.RefusalRegistry`);
  - under a policy that lists allowed registries, an image of a container
    the operator generates that the spec leaves to it (`oam.RefusalRegistry`):
    the `alertmanager` container's where neither `image` nor a patch of it
    names one, and `config-reloader`'s or `init-config-reloader`'s where no
    patch names one. See below;
  - `replicas` over the replica maximum, an unset one counted as the 1 the
    operator runs (`replicas 1 exceeds enforced maximum 0`,
    `oam.RefusalReplicaMaximum`);
  - the cpu or memory of `resources`, and of a listed container, over the
    maxima (`oam.RefusalResourceMaximum`), on a patch of one of the
    operator's containers as on any other; for the alertmanager container,
    the block it runs with, a patch merged over `resources`. Where that block
    names no memory request, the 200Mi the operator requests is held in its
    place (`resources, whose unset memory request the Prometheus operator
    fills as 200Mi: memory request "200Mi" exceeds enforced maximum
    "128Mi"`);
  - the storage a claim requests over the storage maximum
    (`oam.RefusalStorageMaximum`): of `storage`, the arm the operator uses,
    reading `emptyDir`, then `ephemeral`, then `volumeClaimTemplate` and
    taking the first that is set, so a claim template after an arm in use
    is not held, as no claim is made from it; and a generic ephemeral
    volume under `volumes`;
  - `hostNetwork: true` under a policy that does not allow the host network
    (`oam.RefusalHostNamespace`); the spec has no field for the host's
    process or IPC namespace;
  - a hostPath volume under `volumes` (`oam.RefusalHostPath`);
  - a privileged listed container, and a pod-level
    `securityContext.windowsOptions.hostProcess`, under a policy that does
    not allow privileged containers (`oam.RefusalPrivileged`), and a
    capability the policy does not allow (`oam.RefusalContainerCapability`).

  **An image the operator chooses is refused under a registry allowlist.**
  Where the spec names no image for a container the operator generates, the
  operator chooses the one it runs, and no registry allowlist reaches that
  choice, so a policy with a non-empty `AllowedRegistries` refuses it
  (`image: unset, so the Prometheus operator chooses the image the pods run,
  …`; `the image of the config-reloader container (containers): unset, …`)
  and the author names an image from one of the listed registries: `image`
  or a patch of `alertmanager` for the first, a patch of each reloader for
  the other two. Without such a list, it builds.

  **The operator's own values are held, not written.** Where the spec leaves
  `replicas` or the alertmanager container's memory request unset, the
  operator fills 1 and 200Mi before it builds the StatefulSet
  (`makeStatefulSet`, same source); the policy holds those values as if
  authored, and the emitted object still leaves both unset.

  `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` derives, from the type,
  the fields of the spec that carry the name of a field of a pod spec, a
  container or a StatefulSet's spec, the storage block, and any field that
  holds object metadata, and holds each to one of three answers: held to the
  policy, with a refusal shown on a document (the nine above: `image`,
  `replicas`, `resources`, `storage`, `volumes`, `containers`,
  `initContainers`, `securityContext`, `hostNetwork`); read by the wrapper
  (`podMetadata`, below); or stated with the reason it is neither. A
  dependency bump that adds such a field fails there, naming it. A field that
  shapes the pods under a name of the operator's own is not derived: `secrets`
  and `configMaps` mount the named Secrets and ConfigMaps of the namespace,
  read-only, into the alertmanager and config-reloader containers
  (`makeStatefulSetSpec`, same source), and fields such as `retention` and
  `logLevel` become arguments. The policy has no dimension for a Secret or
  ConfigMap volume or for an argument, so none of them is held. **Not
  held:**
  - **An image the operator chooses, under a policy without allowed
    registries,** as stated above. `version` does not name an image.
  - **What the operator adds on its own, beyond the images, the replica
    count and the memory request above:** the resources of a reloader no
    entry patches, the arguments it derives, the governing Service it
    creates where `serviceName` is unset. None of it is in the object.
  - **No policy default is filled.** An unauthored `replicas`, `resources`
    or `storage` stays unauthored under a policy that states a replica, a
    resource or a storage default: the operator, not launcher, decides what
    the omission means, and its own value is what the policy holds.
  - **The size limit of an `emptyDir`,** under `storage` or `volumes`, here
    as on every kind.
  - **Fields the environment policy has no dimension for,** on a pod kind
    either: where and in which order the pods are scheduled, how the
    StatefulSet rolls and what becomes of its claims, `serviceAccountName`
    (launcher creates no ServiceAccount for it and does not check that one
    exists), `automountServiceAccountToken`, the DNS settings, `hostAliases`,
    `hostUsers`, `terminationGracePeriodSeconds`, `imagePullPolicy`,
    `imagePullSecrets` and `volumeMounts`.

  **`podMetadata` is the author's, and nothing is added to it.** The operator
  copies its labels and annotations onto the pods. The wrapper every
  component's objects pass reads it as it reads a workload's pod template: a
  key the consumer reserved (`ReservedMetadataKeys`) is refused, and so is
  the component label key with another value than the component's. It writes
  nothing there. So the operator's pods carry the component label only if the
  author writes the component's own value into `podMetadata.labels`;
  otherwise the NetworkPolicies generated for the component do not select
  them. The Alertmanager object itself carries the component label, as every
  object a component owns. The operator sets five labels of its own on the
  pods (`alertmanager`, `app.kubernetes.io/instance`,
  `app.kubernetes.io/managed-by`, `app.kubernetes.io/name`,
  `app.kubernetes.io/version`) and the annotation
  `kubectl.kubernetes.io/default-container`. A value authored for
  `app.kubernetes.io/version` replaces the operator's, which it copies
  `podMetadata.labels` over; the other four labels and the annotation it
  writes after them, so an authored value does not replace those
  (statefulset.go:446-460). Launcher does not refuse such a key.

  **The metadata of a claim template takes no component label.** The labels
  and annotations of `storage.volumeClaimTemplate` are held to the reserved
  metadata keys: the operator copies them onto the volume claim template of
  its StatefulSet, and so onto every claim the StatefulSet controller creates
  (`volume claim template label "…"`, go-kure/launcher#957). Beside
  `storage.emptyDir` or `storage.ephemeral` the operator makes no claim from
  it, and they are not read. Those of
  `storage.ephemeral.volumeClaimTemplate` and of a generic ephemeral volume
  under `volumes` go onto a claim of a pod and are not read. All three are
  written as authored.

  **No field holds a credential in the clear, and none is checked.** Every
  credential of the spec is the key of a Secret (a `web` or `clusterTLS` key,
  an SMTP password, an OAuth2 client secret), and `configSecret`, `secrets`
  and `imagePullSecrets` name Secrets whose content is not in the object.
  `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields of
  the operator's types whose name suggests a credential and holds each to a
  stated reason it holds none. Free text that could hold one is written as
  authored, under a policy that forbids explicit secrets too:
  `additionalArgs`, `externalUrl`, and the `env` of a listed container, as on
  a pod kind.

  **Not covered.** Whether what the object refers to exists (the
  configuration Secret, a mounted Secret or ConfigMap, the ServiceAccount, a
  governing Service named by `serviceName`, the AlertmanagerConfig objects
  the selectors match). Upstream documents that, without the configuration
  Secret or its `alertmanager.yaml` key, the operator provisions a
  configuration that drops alert notifications. Launcher points no Prometheus
  at the Alertmanager. The object's status is the operator's and is not
  written.
- **thanosruler** (go-kure/launcher#790) is the kind-named projection of a
  ThanosRuler of the same API. It is built as `alertmanager` is, on
  `policyHeldKind` with the same policy check (`monitoring_workload.go`), and
  emits that one object in the build namespace, named after the component
  unless `objectName` names it; the handler declares the object as
  namespaced and adds no label and no annotation. Launcher emits no pod, no
  StatefulSet and no Service for it: the operator builds a StatefulSet from
  the object and runs the pods. What is said of `alertmanager` above holds
  here unless this entry says otherwise: no capability is required and
  nothing gates the kind; an authored `0` the type cannot carry is refused on
  a listed container, and so is an empty `portName`, `evaluationInterval`,
  `retention` or `action` of a `remoteWrite` entry's `writeRelabelConfigs`
  rule, which the CRD defaults to `web`, `15s`, `24h` and `replace`
  (`evaluationInterval: "" cannot be carried by the Prometheus operator API
  types (…)`), while a string default of the Kubernetes pod types is not
  refused; the tag rule and the request-over-limit check hold with
  or without a policy, and so does the refusal of a memory limit under the
  200Mi the operator requests where `resources` names none; no policy
  default is filled; an image the operator chooses is refused under a
  registry allowlist, and the operator's own replica count and memory
  request are held, not written (both below); `podMetadata` is read for
  reserved keys and takes no label; a claim template's metadata is not read.
  The operator's behaviour is read from its source at prometheus-operator
  v0.94.1 (`pkg/thanos/statefulset.go`). The zero-duration rule of
  `alertmanager` has no counterpart: the operator passes a set duration
  through as written.

  **One field is renamed: the spec's `labels` is the `externalLabels`
  property.** Every kind component's `labels` property is its object's own
  labels (see "The object's labels and annotations" below): the engine reads
  it before the handler sees it, so the spec's field cannot be authored
  under its own name. These are the ThanosRuler's Prometheus external
  labels, a map of label names to values (upstream: the operator always adds
  a `thanos_ruler_replica` label with the pod's name), not Kubernetes
  labels: the reserved-key and component-label checks of `labels` do not
  apply to them, and nothing is added to them. Authored together, `labels`
  lands on the object's metadata and `externalLabels` on its `spec.labels`.
  `externalLabels` is decoded as strictly as the spec, under the same null
  contract, and an error names `externalLabels`, not the spec field. A key that names the spec's field in another
  spelling (`Labels`) is refused (`Labels: not a thanosruler property: the
  ThanosRuler's external labels are the externalLabels property, …`): the
  strict decode matches field names case-insensitively and would otherwise
  carry it into the spec unpublished. The rename is the kind's alone; the
  engine's `labels` keeps its meaning. `TestCoreKindSchemas_CoverSpec`
  records it as a mapping (`externalLabels` for `labels`), so a second
  renamed field fails there until it is listed.

  **Authored.** The properties are the top-level json fields of
  `ThanosRulerSpec`, decoded strictly at every depth, `labels` renamed as
  above: the image (`image`, `version`, `imagePullPolicy`,
  `imagePullSecrets`); the pods, as on `alertmanager` less `hostNetwork`,
  which this spec does not have; the StatefulSet (`serviceName`,
  `podManagementPolicy`, `updateStrategy`, `minReadySeconds`); and the ruler
  itself: what it queries and where it sends alerts (`queryEndpoints`,
  `queryConfig`, `alertmanagersUrl`, `alertmanagersConfig`,
  `alertQueryUrl`, `alertDropLabels`, `alertRelabelConfigs`,
  `alertRelabelConfigFile`), which rules it evaluates and how
  (`ruleSelector`, `ruleNamespaceSelector`, `enforcedNamespaceLabel`,
  `excludedFromEnforcement`, `prometheusRulesExcludedFromEnforce`,
  `evaluationInterval`, `resendDelay`, `ruleOutageTolerance`,
  `ruleQueryOffset`, `ruleConcurrentEval`, `ruleGracePeriod`), where its
  data goes (`retention`, `objectStorageConfig`, `objectStorageConfigFile`,
  `remoteWrite`), its server (`listenLocal`, `portName`, `routePrefix`,
  `externalPrefix`, `grpcServerTlsConfig`, `web`), `tracingConfig`,
  `tracingConfigFile`, `logLevel`, `logFormat`, `enableFeatures`,
  `additionalArgs` and `paused`. `additionalArgs` is passed to the
  thanos-ruler container as written and is not read; upstream documents that
  an argument the operator already sets, or an invalid one, fails the
  reconciliation. An entry of `containers` named
  `thanos-ruler` or `config-reloader` is a patch of the container the
  operator generates under that name (`makeStatefulSetSpec`, merged by
  `k8s.MergePatchContainers`), so such a patch may name no image; any other
  entry of `containers` that names none is refused (`containers[0] "proxy":
  names no image, and the Prometheus operator generates no container of
  that name to merge it into; name an image, or the container it patches
  (thanos-ruler, config-reloader)`). The operator generates no init
  container and adds each entry of `initContainers` as written, so every
  one must name an image (`initContainers[0] "prepare": names no image, and
  the Prometheus operator merges no entry of initContainers into a container
  of its own; name an image`). The thanos-ruler container runs with
  `resources` as the operator fills it, an unset memory request as 200Mi,
  and with the requests and limits of a listed `thanos-ruler` entry merged
  over that block key by key (`makeStatefulSet` and `makeStatefulSetSpec`,
  `pkg/thanos/statefulset.go:62-67`, `:481` and `:494`); the checks hold that
  merged block with the 200Mi filled where it names no memory request, and
  not the patch's block alone, as on `alertmanager`. A memory limit under
  200Mi with no memory request in either is refused, as the API would refuse
  the pods (`resources: memory: the unset request the Prometheus operator
  fills as 200Mi must not exceed limit 100Mi; …`; with a patch, the path is
  `resources with containers[0] "thanos-ruler" merged over it`), while a
  request the patch names replaces the 200Mi and is held with the limit of
  `resources`. An image a `thanos-ruler` entry names replaces `image`, so
  `image` is then not held and the entry's is.
  Nothing is refused as not authorable.

  **Required: the kind's required list is generated** (go-kure/launcher#883),
  as on **alertmanager**. No top-level field is one. Among the fields it
  refuses unauthored under an authored parent, as before: an additional
  argument's `name`, a DNS option's `name`, a host alias's `ip` and
  `hostnames`, the `type` of an `updateStrategy`; the `namespace` and
  `resource` of an `excludedFromEnforcement` entry, the `ruleNamespace` and
  `ruleName` of a `prometheusRulesExcludedFromEnforce` entry; and of a
  `remoteWrite` entry its `url`, the `clientId`, `clientSecret` and
  `tokenUrl` of its `oauth2`, and the `clientId`, `tenantId` (and, under
  `azureAd.oauth`, `clientSecret`) of its Azure AD settings; the `key` and
  `operator` of a match expression, in every label selector the spec holds,
  presence only. Refused since go-kure/launcher#883 (**breaking** for a
  document that leaves one out under an authored parent): the required
  fields of the Kubernetes types the spec embeds (a container's or a
  volume's `name`, the `key` of a Secret key reference, a node selector
  requirement's `key`, a sysctl's `name` and `value`), and the `username`
  and the `password` of a `remoteWrite` entry's `basicAuth`, which the
  operator refuses left out. The `keySecret` of `web.tlsConfig` is asked for
  only where it is authored, as on **prometheus**. **Not refused:** the owner
  reference fields of an ephemeral claim template's metadata, which the API
  server prunes, as on **alertmanager**, and the restart rule and pod
  certificate fields the type leaves out when empty, as on **alertmanager**.

  **What the operator builds from the spec is checked where the API would
  break it**, as on `alertmanager`, under any policy and none, at
  `pkg/thanos/statefulset.go` of prometheus-operator v0.94.1:
  - a `portName` the API refuses where the operator writes it: not an IANA
    service name; and `grpc`, the port the operator adds beside the web port
    on the thanos-ruler container unless `listenLocal` is set, and on the
    governing Service it creates unless `serviceName` names one of the
    author's (:216-232, :545-583; `portName: "grpc" is the name of a port the
    Prometheus operator adds to the thanos-ruler container, …`). The
    operator writes `portName` nowhere else, as it gives the ruler no probe,
    so on the container it is judged as the pods run the web port: a
    `thanos-ruler` patch that names port 10902 otherwise leaves it out of the
    container, and one that renames `grpc` frees that name. The Service's
    half is held whatever a patch says. A patch of
    `thanos-ruler` or `config-reloader` is merged by port number into the
    ports the operator gives them (`grpc` at 10901/TCP, the web port at
    10902/TCP and `reloader-web` at 8080/TCP, the last two unless
    `listenLocal` is set), and a port name that leaves two of one name is
    refused, as on `alertmanager`. A `serviceName` must be a DNS-1123 label,
    the package's Service-name rule; a leading digit is refused when
    `spec.kubernetesVersion` is below 1.36 (RelaxedServiceNameValidation;
    go-kure/launcher#959, go-kure/launcher#1230, `docs/delivery-scope.md` §3.5).
  - a negative `replicas`: the operator copies the count into the
    StatefulSet as written (:507), which the API refuses.
  - the storage rules of `alertmanager` (:92-133, :441-450): a claim of the
    arm in use the API refuses, a claim template name beside `emptyDir` or
    `ephemeral` other than `thanos-ruler-<name>-data`, and on the claim
    template arm one that is not a DNS-1123 label or that names a volume the
    operator adds.
  - an entry of `volumes` named as a volume the operator adds: `tls-assets`,
    `web-config` (whatever `version` names, though the operator adds it only
    for Thanos 0.21.0 on, so that raising `version` does not turn a volume
    of the author's into a clash, as on `alertmanager`),
    `remote-write-config`, the volume of each Secret key the spec sets
    (`query-config`, `alertmanager-config`, and `objstorage-config`,
    `tracing-config` and `alertrelabel-config` where the file field that
    takes precedence is unset), `thanos-ruler-<name>-rulefiles-<n>` of the
    rule ConfigMaps, of which the operator makes as many as the selected
    rules need (`pkg/thanos/rules.go:86-104`,
    `pkg/operator/rules.go:363-365`), and the data volume's (:135, :243-297,
    :377-398, :452-469; `volumes[0] "tls-assets": the name is a volume the
    Prometheus operator adds to every ThanosRuler's pods; …`). The web TLS
    credentials' volumes are left to the API.
  - an entry of `volumeMounts` at a path the operator mounts a volume at in
    the thanos-ruler container: `/thanos/data`, `/etc/thanos/certs`,
    `/etc/thanos/web_config/web-config.yaml`, `/etc/thanos/config/<volume>`
    of each Secret key's volume above, and
    `/etc/thanos/rules/thanos-ruler-<name>-rulefiles-<n>` (:297-302, :394,
    :441-471, :601-626).
  - an entry of `volumeMounts` that breaks a rule of a mount
    (go-kure/launcher#1211, go-kure/launcher#1227): a `subPath` beside a
    `subPathExpr`, either one absolute or holding a `..` element, a
    `mountPropagation` that is not `None`, `HostToContainer` or
    `Bidirectional`, a `recursiveReadOnly` that is not `Disabled`,
    `IfPossible` or `Enabled`, `Enabled` or `IfPossible` without
    `readOnly: true` or beside a propagation other than `None`, and
    `Bidirectional` where the thanos-ruler container is not privileged. The
    operator copies the entry as written and merges into it the mount of a
    listed `thanos-ruler` container at the same `mountPath`, so the entry is
    judged as merged: that mount can supply `readOnly` or a propagation, and
    only `securityContext.privileged: true` on that listed container makes the
    container privileged. Checked before the path.
  - an entry of `containers` or `initContainers` with a `volumeMounts` entry
    that breaks the same rules, naming the container and the mount. An entry
    named for a container the operator generates is merged into it, and the
    merge can take `readOnly: true` from the operator's own mount at the path,
    so such an entry is not refused for `recursiveReadOnly` without
    `readOnly: true`; every other rule holds, and `Bidirectional` needs that
    entry's own `securityContext.privileged: true`.
  - an entry of `volumeMounts` at the path of an earlier one written alike:
    the API refuses two mounts at one path. The message names both entries.
    As on `alertmanager`, a path written otherwise than another entry's, or
    than a path the operator mounts at, as `/thanos/data/`, is admitted: both
    mounts are made, and one may hide the other, the operator's included;
    where the kubelet makes the two paths equal (a relative path and its
    absolute form), CRI-O makes only one of them (go-kure/launcher#1050).
  - `dnsPolicy: None` without `dnsConfig.nameservers`: the API refuses the
    pods the operator copies them into (:539-540).
  - a name the operator's objects cannot be named after: the data volume
    `thanos-ruler-<name>-data`, unless a claim template's name names it, and
    the first rule ConfigMap's volume `thanos-ruler-<name>-rulefiles-0` must
    each be a DNS-1123 label, so a name has no dot and at most 38
    characters. Each pod's hostname, `thanos-ruler-<name>-<ordinal>`, is no
    longer than that volume's name at any replica count. The refusal names
    the component, or `objectName` where that set the name.
  - a field the operator reads only from some Thanos version on, where
    `version` is below that version (go-kure/launcher#976). An unset
    `version` is judged against the operator release launcher vendors
    (v0.95.1, default v0.42.5), at which no field below is refused; one the
    operator cannot parse is left to it, as below. Below its version the
    operator leaves the flag off the thanos-ruler container, serves the web
    endpoint over plain HTTP without the web configuration, or refuses the
    object at reconcile. Of a remote write entry it logs every such field as
    ignored and drops it from the configuration: `tlsConfig.minVersion`,
    `tlsConfig.maxVersion`, `queueConfig.sampleAgeLimit`, the entry's own
    fields (`sendNativeHistograms`, `noProxy`, `proxyFromEnvironment`,
    `proxyConnectHeader`, `messageVersion`, `roundRobinDNS`), `azureAd` as a
    whole, and its methods `sdk` and `oauth`. (Up to v0.94.1 the operator
    reset a copy of the entry, and wrote the entry's own fields all the
    same.) An `azureAd` left with no method by a dropped `sdk` or `oauth` is
    refused at reconcile, and the refusal names it
    (`ruleQueryOffset: read by the Prometheus operator only for
    Thanos 0.38.0 and later; at version v0.37.0 it does not pass it to
    Thanos Ruler, and the ruler runs without it; raise version to 0.38.0 or
    later, or leave ruleQueryOffset unset`). Several such fields, refused
    at reconcile or dropped, are refused together ahead of the checks
    below, each named with its version and no consequence claimed for any,
    as on `prometheus` (go-kure/launcher#1022). Flags (`makeStatefulSetSpec`):
    `web.tlsConfig` and `web.httpConfig` (0.21.0), `ruleOutageTolerance` and
    `ruleGracePeriod` (0.30.0), `ruleConcurrentEval` above 0 and
    `grpcServerTlsConfig.minVersion` (0.37.0), `ruleQueryOffset` (0.38.0),
    `enableFeatures` (0.39.0), `grpcServerTlsConfig.cipherSuites` and
    `curves` (0.42.0). Remote write (`createOrUpdateRulerConfigSecret`):
    `remoteWrite` itself (0.24.0, refused at reconcile); of an entry,
    `tlsConfig.minVersion` (0.28.0), `tlsConfig.maxVersion` and
    `sendNativeHistograms` (0.30.0), `azureAd` and `azureAd.oauth` (0.31.0),
    `queueConfig.sampleAgeLimit`, `noProxy`, `proxyFromEnvironment` and
    `proxyConnectHeader` (0.32.0), `azureAd.sdk` (0.36.0), `messageVersion`
    (0.37.0), `roundRobinDNS` (0.38.0), and an `azureAd.managedIdentity`
    without `clientId` (0.40.0, refused at reconcile).
    `TestThanosRulerVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's package `pkg/thanos` (every Go file of it,
    its tests aside), vendored as for `alertmanager`, and holds each to this
    list.
  - a remote write entry's `azureAd` authored with no authentication
    method (none of `managedIdentity`, `oauth`, `sdk` and
    `workloadIdentity`), as on `prometheus` (go-kure/launcher#1023): the
    operator refuses it at reconcile whatever it drops
    (`validateRemoteWriteSpec`, which it applies to the ruler's entries
    too). The gates above of `remoteWrite` and of `azureAd`, which refuse
    any `azureAd` below 0.31.0, are refused ahead of it, as are several
    gated fields together; it is refused ahead of a single other gate and
    of the fields below, naming the methods
    the operator passes to Thanos at the version: from 0.40.0 on
    `remoteWrite[0].azureAd: no authentication method: … at every version,
    wherever it generates the remote write configuration; set
    managedIdentity, oauth or sdk (the operator does not pass
    workloadIdentity to Thanos), or leave azureAd unset`, below it a
    `managedIdentity with a clientId`, and below 0.36.0 no `sdk`.
  - a remote write entry's `azureAd.workloadIdentity`, `azureAd.scope`, or
    `sigv4.externalId` with a `roleArn` (go-kure/launcher#1020), at every
    `version` the operator can parse, after the gates above of `remoteWrite`
    and of `azureAd`, whose miss leads the operator to refuse the object or
    may (`remoteWrite` below 0.24.0, a managed identity without a `clientId`
    below 0.40.0, a method it drops), and ahead of the other gates, so that
    a `workloadIdentity` whose drop leaves no method, and the object
    refused, is not refused as a flag the ruler runs without. An
    `externalId` without a `roleArn` is left to the API server, which
    refuses it (below).
    The operator passes them to Thanos at no version: it logs each as ignored,
    with minimum version `none`, and drops it from the remote write
    configuration, so the ruler writes without it. An `azureAd` whose only
    method was `workloadIdentity` is left with none, and the operator
    refuses the object at reconcile (`remoteWrite[].azureAd.scope: the
    Prometheus operator does not pass it to Thanos at any version: it drops
    it from the remote write configuration, and the ruler writes without it;
    leave remoteWrite[].azureAd.scope unset`).
    `TestThanosRulerRemoteWriteTexts_MatchVendoredSource` holds the list to
    every field `createOrUpdateRulerConfigSecret` ignores with minimum
    version `none`.

  **Not checked of what the operator refuses:** a ThanosRuler with neither
  `queryConfig` nor `queryEndpoints` (:141-143), a `version` it cannot parse
  (:145-149), an entry of `additionalArgs` naming a flag it generates
  (`BuildArgs`, :352), and a `web.tlsConfig` its validation refuses
  (`webconfig.New`, :383-386). Each fails the reconcile, and is left to the
  operator. So is a remote write entry that `validateRemoteWriteSpec`
  (`pkg/prometheus/operator.go`) refuses for more than its authentication
  method being absent: two of `basicAuth`, `oauth2`, `authorization`,
  `sigv4` and `azureAd`, two `azureAd` methods, or an `oauth` `clientId`
  that is not a UUID. Where one applies, a refusal above that says the ruler
  runs or writes without a field is given all the same.

  **The group of an excluded object is written.** An `excludedFromEnforcement`
  entry that leaves `group` out (or writes it `null`) is written with
  `monitoring.coreos.com`. The Go type writes the field whether or not it was
  authored, and the API allows only that one value, which it defaults only
  where the field is absent: an empty group would be refused at apply. An
  authored empty group is refused by the entry's index
  (`excludedFromEnforcement[1].group: empty: the API admits only
  monitoring.coreos.com; …`), not repaired. An authored
  `monitoring.coreos.com` is kept; another group is written as authored and
  left to the API server. `TestMonitoringExcludedGroup_MatchesMarkers` holds
  the written value to the default and the enum of the linked module's
  source.

  **The API's expression rules are not checked.** Two are listed
  (`thanosRulerRulesLeft`) and left to the API server: an `updateStrategy`
  with a `rollingUpdate` must have the type `RollingUpdate`, and the
  `externalId` of a remote write entry's `sigv4` needs a `roleArn`. The kind
  refuses an `externalId` only with a `roleArn`, as one the operator passes
  to Thanos at no version (go-kure/launcher#1020), so the second rule still
  decides one without.

  **What the type writes unauthored:** `resources: {}`, on every
  ThanosRuler; and, on an authored `storage`, a `volumeClaimTemplate` with
  `metadata: {}` and `status: {}`, as on `alertmanager`.

  **Policy.** Refused, with a workload kind's classes: `image`, where no
  patch of `thanos-ruler` names one in its place, the image of
  a listed container or init container (a patch of `thanos-ruler` or
  `config-reloader` included) and an image volume's reference outside the
  allowed registries; under a policy that lists allowed registries, the
  image of the `thanos-ruler` container where neither `image` nor a patch of
  it names one, and that of `config-reloader` where no patch names one;
  `replicas` over the replica maximum, an unset one counted as the 1 the
  StatefulSet runs; the cpu or memory of `resources` and of a listed
  container over the maxima, `resources` with a `thanos-ruler` entry's
  requests and limits merged over it, and the 200Mi the operator requests
  held where neither names a memory request (`resources, whose unset memory
  request the Prometheus operator fills as 200Mi: memory request "200Mi"
  exceeds enforced maximum "128Mi"`); the storage a claim requests over the
  storage maximum, of the `storage` arm the operator uses (`emptyDir`, then
  `ephemeral`, then `volumeClaimTemplate`, as on `alertmanager`) and of a
  generic ephemeral volume; a hostPath volume; a privileged listed
  container, a pod-level `securityContext.windowsOptions.hostProcess` and a
  capability the policy does not allow. The spec has no field for the
  host's network, process or IPC namespace.
  `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` holds every field of
  the spec that shapes the pods to a refusal shown on a document or a stated
  reason, as for `alertmanager`.

  **An image the operator chooses is refused under a registry allowlist.**
  Where the spec names no image for a container the operator generates, the
  operator chooses the one it runs, and no registry allowlist reaches that
  choice, so a policy with a non-empty `AllowedRegistries` refuses it
  (`image: unset, so the Prometheus operator chooses the image the pods run,
  …`; `the image of the config-reloader container (containers): unset, …`)
  and the author names an image from one of the listed registries: `image`
  or a patch of `thanos-ruler` for the first, a patch of `config-reloader`
  for the second. The operator generates `config-reloader` on every
  ThanosRuler, as it always writes at least one rule ConfigMap
  (`makeConfigMapsFromRules`, `pkg/operator/rules.go`). Without such a list,
  it builds.

  **The operator's own values are held, not written.** Where the spec leaves
  the thanos-ruler container's memory request unset, the operator fills
  200Mi before it builds the StatefulSet (`makeStatefulSet`); it copies an
  unset `replicas` into the StatefulSet as unset, which the API server
  defaults to 1. The policy holds those values as if authored, and the
  emitted object still leaves both unset.

  **Credentials: one is held, the rest are stated.** The deprecated
  `remoteWrite[].bearerToken` holds a credential in the object; under a
  policy that forbids explicit secrets it is refused
  (`remoteWrite[1].bearerToken: holds a credential in the object, and the
  environment policy forbids explicit secrets…`,
  `oam.RefusalExplicitSecret`), and without one it is written as authored.
  Every other credential of the spec is the key of a Secret (a remote write
  entry's basic auth, authorization, OAuth2, SigV4 and Azure AD secrets) or
  the path of a file in the container (`bearerTokenFile`, a `keyFile`).
  **Not held:** a credential under a name that does not say so. A value of
  `remoteWrite[].headers`, the userinfo of a URL (`remoteWrite[].url`,
  `queryEndpoints`, `alertmanagersUrl`), and free text (`additionalArgs`,
  the `env` of a listed container) are written as authored, under a policy
  that forbids explicit secrets too.
  `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields
  whose name suggests a credential and holds each to the policy or a stated
  reason.

  **`podMetadata`.** As on `alertmanager`: the pods carry the component label
  only if the author writes the component's own value into
  `podMetadata.labels`; otherwise the NetworkPolicies generated for the
  component do not select them. The operator reserves four labels of its own
  on the pods (`app.kubernetes.io/name`, `app.kubernetes.io/managed-by`,
  `app.kubernetes.io/instance`, `thanos-ruler`) and the annotation
  `kubectl.kubernetes.io/default-container`; upstream documents that an
  authored value does not override them. Launcher does not refuse such a key.

  **Not covered.** Whether what the object refers to exists (a Secret key, the
  ServiceAccount, a governing Service named by `serviceName`, the
  PrometheusRule objects the selectors match, the query and Alertmanager
  endpoints). What the operator adds on its own, beyond the images, the
  replica count and the memory request above (the resources of a
  config-reloader no entry patches, the arguments it derives, the
  `thanos-ruler-operated` Service), is not in the object. The object's
  status is the operator's and is not written.
- **prometheus** (go-kure/launcher#790) is the kind-named projection of a
  Prometheus of the same API. It is built as `alertmanager` is, on
  `policyHeldKind` with the same policy check (`monitoring_workload.go`), and
  emits that one object in the build namespace, named after the component
  unless `objectName` names it; the handler declares the object as
  namespaced and adds no label and no annotation. Launcher emits no pod, no
  StatefulSet and no Service for it: the operator builds one StatefulSet per
  shard from the object and runs the pods. Unlike on `alertmanager`, an image
  named without `version` is not refused, nor is a `version` the operator
  cannot parse. A parseable `version` of a major other than 2 or 3 is
  refused, and so is a field the operator reads only from some Prometheus
  version on, or, under `thanos`, from some Thanos version on, where
  `version` or `thanos.version` is below it; both are listed below. What is said of
  `alertmanager` above holds here unless this entry says otherwise: no
  capability is required and nothing gates the kind; on a listed container, init container
  or volume, an authored `0` or `""` the type cannot carry is refused, a
  `hostPort` of `0` under `hostNetwork: true` included, and so is an empty
  `schedulerName` or `imagePullPolicy`, which the operator copies unchanged
  to the pods and to its own containers (prometheus, thanos-sidecar and both
  reloaders), where the API server defaults an empty one; so is an empty `portName`,
  `scrapeInterval`, `evaluationInterval` or `thanos.blockSize`, which the CRD
  defaults to `web`, `30s`, `30s` and `2h`, and an empty `action` of a
  relabeling rule (`replace`) in a `remoteWrite` entry's
  `writeRelabelConfigs`, an Alertmanager endpoint's `relabelings` or
  `alertRelabelings`, or a scrape class's `relabelings` or
  `metricRelabelings` (`scrapeInterval: "" cannot be carried by the
  Prometheus operator API types (…)`); the tag rule and the request-over-limit
  check hold with or without a policy; no policy default is filled; an image
  the operator chooses is refused under a registry allowlist, and the
  operator's own replica and shard counts are held, not written (both below);
  the labels and annotations of `storage.volumeClaimTemplate` are held to the
  reserved metadata keys where it is the arm in use, and no claim template
  takes a component label. An empty `retention` is not
  refused: its 24h default is the operator's code (where `retentionSize` and
  `retentionPercentage` are empty too), not a CRD default, so the derived list
  cannot hold it, and the object then gets the operator's default. The
  operator's behaviour is read from its source at prometheus-operator v0.94.1
  (`pkg/prometheus/server/statefulset.go`, `pkg/prometheus/common.go`). The
  operator fills no memory request of its own for the prometheus container
  or the sidecar, so the memory-limit refusal of `alertmanager` has no
  counterpart.

  **No field is renamed.** The spec has no top-level `labels` or
  `annotations` field, so every property keeps its upstream name:
  `externalLabels` is the spec's own field, the Prometheus external labels,
  not Kubernetes labels, and the reserved-key and component-label checks of
  `labels` do not apply to it.

  **Authored.** The properties are the top-level json fields of
  `PrometheusSpec`, decoded strictly at every depth, less three: the image
  (`image`, `version`, `imagePullPolicy`, `imagePullSecrets`); the pods, as
  on `alertmanager`; the StatefulSets (`replicas`, `shards`,
  `shardingStrategy`, `serviceName`, `podManagementPolicy`,
  `updateStrategy`, `minReadySeconds`, `persistentVolumeClaimRetentionPolicy`,
  `shardRetentionPolicy`); what it scrapes (the four selector and namespace
  selector pairs, the scrape settings and limits, `scrapeClasses`), the rules
  it evaluates and where alerts go (`ruleSelector`,
  `ruleNamespaceSelector`, `rules`, `alerting`), where its data goes
  (`storage`, `retention`, `retentionSize`, `retentionPercentage`, `tsdb`,
  `remoteWrite`, `remoteRead`), the Thanos sidecar (`thanos`), its server
  (`web`, `listenLocal`, `enableAdminAPI`), and the rest of the spec's
  fields. `baseImage`, `tag` and `sha` are refused when not empty; an empty
  one writes nothing, as on `alertmanager`. The same three of `thanos` are
  refused whenever set, the empty string included, since the type writes a
  set pointer; a `null` one sets none (`tag: not authorable: the Prometheus
  operator deprecates the field, …; use image`).
  `additionalArgs` and `thanos.additionalArgs` are passed as written and are
  not read. An entry of `containers` named `prometheus` or `config-reloader`,
  or `thanos-sidecar` where `thanos` is set, is a patch of the container the
  operator generates under that name (`makeStatefulSetSpec`, merged by
  `k8s.MergePatchContainers`); of `initContainers`, `init-config-reloader`.
  A patch may name no image; any other entry that names none is refused
  (`containers[0] "proxy": names no image, and the Prometheus operator
  generates no container of that name to merge it into; name an image, or
  the container it patches (prometheus, config-reloader)`), and so is a
  `thanos-sidecar` entry without an image where `thanos` is unset, since no
  sidecar is generated then. As on `alertmanager`, a name listed twice in one
  list is refused, and so is a name shared by an init container and a
  container of the pods, generated or listed (`containers[0]
  "init-config-reloader": the name is also that of the init container the
  Prometheus operator generates, …`); and `dnsPolicy: None` without
  `dnsConfig.nameservers`, and a pod-level
  `securityContext.windowsOptions.hostProcess: true` without
  `hostNetwork: true`, are refused: the API refuses the pods the operator
  copies them into. A patch's ports are merged into those the operator gives
  the container by number, as on `alertmanager`: the `prometheus` container's
  web port under `portName` at 9090/TCP and `config-reloader`'s `reloader-web`
  at 8080/TCP, none of either under `listenLocal` (`config-reloader` keeps its
  port under the `ProcessSignal` `reloadStrategy`, to which the operator does
  not hand `listenLocal` on), `init-config-reloader`'s `reloader-init` at
  8081/TCP, and `thanos-sidecar`'s `http` at 10902 and `grpc` at 10901, which
  `thanos.listenLocal` does not take away. A container whose ports, as the
  pods run them, name two ports alike is refused (`containers[0]
  "prometheus": ports[0] "web" at 8080/TCP: the Prometheus operator merges
  the patch's ports into the container's by number, which leaves another port
  of that name, the Prometheus operator's port at 9090/TCP, …`). A patch that
  renames the web port (a port at 9090 of another name than `portName`) is
  refused while a startup, liveness or readiness probe the operator writes
  still names `portName`, as on `alertmanager` (`containers[0] "prometheus":
  ports[0] "http" at 9090/TCP renames the web port, whose name portName
  "web" the Prometheus operator's startup, liveness and readiness probes
  name; …`; go-kure/launcher#963). The operator's probes name `portName`
  unless `listenLocal` is set, under which each is an exec and no web port is
  given (`BuildProbes` and `buildProbeHandler`,
  `pkg/prometheus/promcfg.go:1398-1451` at prometheus-operator v0.94.1). Two
  `volumes` of one name are refused, and so is a `serviceName` that is not a DNS-1123 label (a
  leading digit is refused when `spec.kubernetesVersion` is below 1.36,
  RelaxedServiceNameValidation; go-kure/launcher#959, go-kure/launcher#1230,
  `docs/delivery-scope.md` §3.5).
  A nonempty `externalUrl` that Prometheus exits on at
  startup is refused too, though the operator and the API accept it: the
  operator passes it unchanged as `--web.external-url`
  (`pkg/prometheus/promcfg.go`), and Prometheus exits where it begins or ends
  with a quote (`externalUrl: begins or ends with a quote: …`) or where Go's
  `net/url` cannot parse it (`externalUrl: not a URL Go's net/url can parse:
  …`; `computeExternalURL`, `cmd/prometheus/main.go` of v3.14.0). Prometheus
  checks no scheme, so any scheme builds. Neither message names the value.

  **Required: the kind's required list is generated** (go-kure/launcher#883),
  as on **alertmanager**. No top-level field is one. Among the fields it
  refuses unauthored under an authored parent, as before: the `name` of an
  additional argument (the spec's and the sidecar's), a DNS option's `name`,
  a host alias's `ip` and `hostnames`, the `type` of an `updateStrategy`; the
  `namespace` and `resource` of an `excludedFromEnforcement` entry, the
  `ruleNamespace` and `ruleName` of a `prometheusRulesExcludedFromEnforce`
  entry; `alerting.alertmanagers`, and an endpoint's `name` and `port`;
  `apiserverConfig.host`; a scrape class's `name`;
  `shardRetentionPolicy.retain.retentionPeriod`; `tracingConfig.endpoint`;
  the `url` of a `remoteWrite` and a `remoteRead` entry and the `clientId`,
  `clientSecret` and `tokenUrl` of its `oauth2`; the `clientId`, `tenantId`
  (and, under `azureAd.oauth`, `clientSecret`) of a remote write entry's
  Azure AD settings; and the `key` and `operator` of a match expression, in
  every label selector the spec holds, presence only. Refused since
  go-kure/launcher#883 (**breaking** for a document that leaves one out under
  an authored parent): the required fields of the Kubernetes types the spec
  embeds (a container's or a volume's `name`, the `key` of a Secret key
  reference, a topology spread constraint's or a pod affinity term's
  `topologyKey`, a seccomp profile's `type`), and the `username` and the
  `password` of the `basicAuth` of `alerting.alertmanagers[]`,
  `apiserverConfig`, and a `remoteRead` and a `remoteWrite` entry, which the
  operator refuses left out. The `keySecret` of `web.tlsConfig` is asked for
  only where it is authored, the key being readable from `keyFile` instead.
  **Not refused:** the owner reference fields of an ephemeral claim
  template's metadata, which the API server prunes, as on **alertmanager**,
  and the restart rule and pod certificate fields the type leaves out
  when empty, as on **alertmanager**.

  **The group of an excluded object is written.** An
  `excludedFromEnforcement` entry that leaves `group` out (or writes it
  `null`) is written with `monitoring.coreos.com`, and an authored empty
  group is refused by the entry's index: the Go type writes the field even
  when empty, and the API admits only that one group, which it defaults
  where the field is absent. `TestMonitoringExcludedGroup_MatchesMarkers`
  holds the written value to the module's default and enum.

  **What the operator builds from the spec is checked where the API or the
  operator would break it,** as on `alertmanager` and by the same shared
  checks, under any policy and none, at `pkg/prometheus/common.go` and
  `pkg/prometheus/server/` of prometheus-operator v0.94.1:
  - a `portName` the API refuses where the operator writes it: not an IANA
    service name; and unless `serviceName` names a Service of the author's,
    `grpc` where `thanos` is set, the port the operator adds for the sidecar
    to the governing Service it creates (`MakeContainerPorts`,
    `BuildStatefulSetService`; server/operator.go:1006-1030).
  - a negative `replicas`, which the operator runs as 1 (`ReplicasNumberPtr`),
    and a `shards` below 1, for which it runs 1 shard (`shardsNumber`).
  - the storage arms as on `alertmanager`: a claim template named otherwise
    than `prometheus-<name>-db` beside `storage.emptyDir` or
    `storage.ephemeral`, a claim without a positive storage request, an
    ephemeral claim without access modes, and on the claim template arm a
    claim template name that is not a DNS-1123 label or names a volume the
    operator adds (`VolumeClaimName`, server/statefulset.go:105-138).
  - an entry of `volumes` named as a volume the operator adds: `config`,
    `tls-assets`, `config-out` and `web-config` (the last whatever `version`
    names, though the operator adds it only for Prometheus 2.24.0 on, so
    that raising `version` does not turn a volume of the author's into a
    clash, as on `alertmanager`);
    `log-file` where `scrapeFailureLogFile` is a file name without a
    directory, or `queryLogFile` is one and `scrapeFailureLogFile` is unset;
    `thanos-prometheus-http-client-file` where `thanos` is set, whatever
    `thanos.version` names; any `prometheus-<name>-rulefiles-<n>`, the
    volumes of the rule ConfigMaps, of which the operator mounts at least
    three whatever the rules (`<n>` a decimal without a leading zero, as the
    operator writes it: `rulefiles-01` is not reserved); the name the operator derives for each of
    `secrets` and `configMaps` (`secret-<name>`, `configmap-<name>`); and the
    data volume's. Two entries of `secrets`, or of `configMaps`, that the
    operator's naming gives one volume name are refused as well. So is an
    entry whose volume name, cut to 63 characters, ends in `-`: the operator
    checks the name after the cut and fails to build the pods
    (`ResourceNamer.DNS1123Label`, common.go:289-292 and :311-314, returned
    at server/statefulset.go:181-184). The volumes of the web TLS
    credentials are not checked, as on `alertmanager`: the operator names
    each after the credential's source with a hash appended, which the kind
    does not derive, so an entry under one of those names is left to the API
    to refuse.
  - an entry of `volumeMounts` at a path the operator mounts a volume at in
    the prometheus container: `/prometheus`, `/etc/prometheus/config_out`,
    `/etc/prometheus/certs`, `/etc/prometheus/web_config/web-config.yaml`,
    `/var/log/prometheus` where `log-file` is added,
    `/etc/prometheus/rules/prometheus-<name>-rulefiles-<n>`, and
    `/etc/prometheus/secrets/<name>` and `/etc/prometheus/configmaps/<name>`
    for each entry of `secrets` and `configMaps`. The mounts of the web TLS
    credentials, under `/etc/prometheus/web_config/`, are left to the API,
    as their volumes are.
  - an entry of `volumeMounts` that breaks a rule of a mount
    (go-kure/launcher#1211, go-kure/launcher#1227): a `subPath` beside a
    `subPathExpr`, either one absolute or holding a `..` element, a
    `mountPropagation` that is not `None`, `HostToContainer` or
    `Bidirectional`, a `recursiveReadOnly` that is not `Disabled`,
    `IfPossible` or `Enabled`, `Enabled` or `IfPossible` without
    `readOnly: true` or beside a propagation other than `None`, and
    `Bidirectional` where the prometheus container is not privileged. The
    operator copies the entry as written and merges into it the mount of a
    listed `prometheus` container at the same `mountPath`, so the entry is
    judged as merged: that mount can supply `readOnly` or a propagation, and
    only `securityContext.privileged: true` on that listed container makes the
    container privileged. Checked before the path. An entry of
    `thanos.volumeMounts` with a nonempty `subPath` or `subPathExpr` is
    refused below, as not carried.
  - an entry of `containers` or `initContainers` with a `volumeMounts` entry
    that breaks the same rules, naming the container and the mount. An entry
    named for a container the operator generates is merged into it, and the
    merge can take `readOnly: true` from the operator's own mount at the path,
    so such an entry is not refused for `recursiveReadOnly` without
    `readOnly: true`; every other rule holds, and `Bidirectional` needs that
    entry's own `securityContext.privileged: true`.
  - an entry of `thanos.volumeMounts` at a path the operator mounts a
    volume at in the thanos-sidecar container: `/prometheus` where
    `objectStorageConfig` or `objectStorageConfigFile` is set, and
    `/etc/thanos/config`, which it mounts from Thanos 0.24.0, reserved
    whatever `thanos.version` names, as `web-config` is, so that raising
    `thanos.version` does not turn a mount of the author's into a clash
    (`createThanosContainer`, server/statefulset.go:642-671 and :730-738);
    and any field of an entry but `name` and `mountPath`
    (`readOnly`, `recursiveReadOnly`, `subPath`, `subPathExpr`,
    `mountPropagation`, `bindMountOptions`), which the operator drops: it
    copies only those two into the sidecar's mount (:642-647).
    `TestThanosDroppedMountFields` holds the list to every field of the type.
  - an entry of `volumeMounts`, or of `thanos.volumeMounts`, at the path of
    an earlier one of the same field written alike: the API refuses two
    mounts at one path. The message names both entries. As on
    `alertmanager`, a path written otherwise than another entry's, or than a
    path the operator mounts at, as `/etc/prometheus/config_out/`, is
    admitted: both mounts are made, and one may hide the other, the
    operator's included; where the kubelet makes the two paths equal (a
    relative path and its absolute form), CRI-O makes only one of them
    (go-kure/launcher#1050).
  - a `queryLogFile` named without a directory beside a
    `scrapeFailureLogFile` named with one, where the mount that covers
    `/var/log/prometheus/<name>` most closely is read-only, or binds a
    regular file above it, or binds one at it with `readOnly` set: the
    operator writes the query log under
    `/var/log/prometheus` but adds the `log-file` volume there only for the
    scrape failure log file (server/statefulset.go:510, :537;
    common.go:334-347). Where no mount covers it, the file is on the root
    filesystem, which is not judged (next entry).
  - a `queryLogFile` or `scrapeFailureLogFile` Prometheus cannot write. This
    check follows what runs at the version named, unlike the reserved paths
    above.

    Where the file is:
    - An absolute path whose last element as written is empty, `.` or `..`,
      as `/etc/prometheus/config_out/query.log/`, `/`,
      `/etc/prometheus/config_out/query.log/.` or `…/config_out/logs/..`, is
      refused before any other judging and before it is cleaned, whatever it
      expands to or links through: Prometheus opens it as written
      (prometheus/common `JoinDir` keeps an absolute path) with `O_CREAT`,
      which the kernel refuses on a trailing slash (`fs/namei.c:3620-3622` at
      Linux v6.12) and on a directory (`:3750-3751`), which `/`, `.` and `..`
      resolve to where they resolve at all.
    - A name alone is in `/var/log/prometheus`; the operator passes any other
      path unchanged (common.go:230-236).
    - A relative path with a directory, as `logs/query.log`, is not judged.
      Prometheus joins it to the directory of its configuration,
      `/etc/prometheus/config_out` (`--config.file`, promcfg.go:1258), a
      `queryLogFile` from v2.21.0 only, cleaning a trailing slash away;
      before that it opens one from its working directory, as written. `config_out` is an emptyDir (common.go:253-259): an
      `initContainer` may mount it writable and leave a link there to a
      writable volume, which Prometheus follows when it opens the file
      (Prometheus `util/logging/file.go:43`).
    - A path with a reference `$(NAME)`, a name of letters, digits and `_`,
      is not judged: the config reloader expands it from variables the kind
      does not see (`--config-envsubst-file`, common.go:378; Thanos
      `pkg/reloader/reloader.go:703`). Any other `$(` reaches Prometheus as
      written and is judged so.
    - An absolute path with a `..` element before its last, as
      `/var/log/prometheus/../query.log`, is not judged: the kernel follows a
      link on the way before it takes the `..` (`fs/namei.c:2418-2426`), so
      the cleaned path may name another file than the one opened.
    - A path at a directory above a mount of the `prometheus` container, the
      operator's or the author's, as `/etc/prometheus`, or `/var/log` where
      the operator mounts its `log-file` volume, is refused
      (go-kure/launcher#1055): the container runtime mounts the deeper mount
      through it, so it is a directory wherever the container runs
      (`fs/namei.c:2418-2433`), and Prometheus cannot open it as a file
      (`:3750-3751`).

    Which mount decides: the one that covers the file most closely, at the
    file itself (as a `subPath` mount of an existing file, which Prometheus
    opens for appending without writing to its directory), at its directory
    or at the nearest directory above it (paths compared cleaned, a relative
    `mountPath` taken from `/` as the kubelet does).
    - Writable: the data volume at `/prometheus`, the `log-file` volume at
      `/var/log/prometheus` where the operator adds it, and `config_out`. The
      operator mounts `config_out` read-only, but it is an emptyDir the config
      reloader writes, which an `initContainer` can reach too, so a link in it
      may lead to a writable volume.
    - Read-only, the operator's: the certificate, secret and configMap mounts; each rule ConfigMap's at
      `/etc/prometheus/rules/prometheus-<name>-rulefiles-<n>`, of which the
      operator mounts at least three (server/statefulset.go:514-534); and,
      from Prometheus v2.24.0 (server/statefulset.go:196), the web
      configuration and the web TLS credentials' directories under
      `/etc/prometheus/web_config/` (`secret/<keySecret>-key`, and
      `secret/` or `configmap/` `<name>-cert` and `<name>-ca`,
      `pkg/webconfig/tls_credentials.go:54-97`).
    - A mount of the author's, in `volumeMounts` or a patch of the
      `prometheus` container, is read-only only where its volume is one the
      kubelet both writes and mounts read-only: a `configMap`, `secret`,
      `downwardAPI` or `projected` volume, or one of the operator's own
      volumes, which the spec cannot define: `config`, `tls-assets`,
      `web-config`, `thanos-prometheus-http-client-file`, a rule ConfigMap's,
      and `secret-<name>`/`configmap-<name>`. The kubelet's only links in
      those point inside the volume (`AtomicWriter`,
      `pkg/volume/util/atomic_writer.go:217`, `:230`, `:482` at Kubernetes
      v1.37.1), so no file in one leads to a writable volume.
    - Admitted as writable, `readOnly` set or not: an author's mount of any
      other volume, as an `emptyDir`, `hostPath`, `image`,
      `persistentVolumeClaim` or inline `csi` volume, or the operator's
      `config-out`, data or `log-file` volume. Its content is not seen here,
      an `initContainer` may write it, and a link in it may lead to a
      writable volume, which Prometheus follows, opening the file for
      appending (`util/logging/file.go:43` at v3.7.0)
      (go-kure/launcher#1051). The web TLS credentials' volumes are named
      with a hash the kind does not derive, so an author's mount of one
      counts as writable.
    - A regular file: a mount of a `hostPath` volume of `type: File` or
      `FileOrCreate`. The kubelet checks the path is a regular file,
      following links (`pkg/volume/hostpath/host_path.go:488-493`,
      `pkg/volume/util/hostutil/hostutil.go:82`); a `subPath` binds the
      same file or, naming a path below it, fails to resolve and the
      container does not start
      (`pkg/volume/util/subpath/subpath_linux.go:199-202`). No file can be
      created under it, so a file below it is refused, `readOnly` set or
      not; set `readOnly`, it binds the file read-only
      (`kubelet_pods.go:425`), leaving no link to follow, so a file at its
      own path is refused too.
    - A directory: the path of any mount the operator makes but the web
      configuration file, which is a key of its Secret mounted by `subPath`
      (`pkg/webconfig/config.go:272-278`). `/prometheus`,
      `/etc/prometheus/config_out` and `/var/log/prometheus` are emptyDir
      or claim directories (`common.go:252-283`, `:334-347`), the rest
      Secret, ConfigMap or projected volumes mounted whole. So is the path of
      an author's mount, in `volumeMounts` or a patch, without `subPath` or
      `subPathExpr`, of an `emptyDir`, including a volume with no source,
      which the API server defaults to one, `configMap`, `secret`, `downwardAPI`,
      `projected`, `gitRepo`, `nfs`, `iscsi` or `fc` volume, or a `hostPath`
      of `type: Directory` or `DirectoryOrCreate`, or of one of the
      operator's volumes of those types (`config-out` is an emptyDir; not
      the data volume `prometheus-<name>-db`, whose type `storage` decides,
      nor the web TLS credentials' volumes, whose names are hashed): each
      is set up as a directory at its path (`emptyDir` `SetUpAt`, the
      kubelet-written types on a wrapped emptyDir, `nfs`, `iscsi` and `fc`
      on a directory they make, `hostPath` checked or made one). Prometheus
      cannot open a directory as a file, so a log file at one's own path is
      refused, writable or not, before a read-only one is refused as
      read-only; a file under a writable one is admitted. Not judged a
      directory: a mount with a `subPath` (the kubelet makes a missing one a
      directory, but an existing one may be a file), and a
      `persistentVolumeClaim`, `ephemeral`, `csi`, `flexVolume` or `image`
      volume, a `hostPath` of another type or of none, and the in-tree
      types with no kubelet plugin of their own, whose content at the path
      is the claim's, the driver's or the runtime's. A `hostPath` of
      `type: Socket` is admitted too, though opening a socket fails.

    A patch mount at a path written as an earlier mount's is merged into it
    by the operator's strategic merge: it takes the patch's volume, and keeps
    the earlier mount's `readOnly`, `subPath` and `subPathExpr` where it
    leaves them unset. The operator's data mount at `/prometheus` carries
    `subPath: prometheus-db` where `storage` is set and
    `disableMountSubPath` is not (`SubPathForStorage`, common.go:216-224,
    :278-282), so a patch there is not judged a directory then; the
    operator's own data mount is. Of mounts at one path written differently,
    as `/data` and `data/`, in `volumeMounts` or a patch, one writable makes
    it writable, and they are a directory only where every one is, since
    which one the container runtime mounts over the other is not derived.
    Paths are compared as written: the path to a mount resolves to that
    mount; a link redirected by a later, deeper mount is not modelled (a
    refusal there may be false, and an admit there may fail at runtime, as an
    admit of unseen content may). The check refuses only where it knows
    Prometheus cannot open the file for writing: the file or its directory
    is read-only, its directory is a regular file, its path is a directory
    mounted there or above a mount, or it is absolute and ends in a slash,
    `/.` or `/..`. A directory below a writable mount counts as
    writable, since it may exist on the volume or be made by an init
    container: `/prometheus/wal` is made by Prometheus opening its TSDB
    before its first configuration load opens the query log
    (`cmd/prometheus/main.go:1438` at v3.14.0). Where no mount covers the
    file, it is on the image's root filesystem, whose content is not judged
    (go-kure/launcher#1055): the stock image builds on `busybox:latest`,
    unpinned, and an authored image's content is not derivable; a link in
    it may lead to a writable volume, which Prometheus follows, and
    containerd mounts an image `VOLUME` writable. The operator makes the
    root filesystem read-only (server/statefulset.go:349), so such a file
    may still fail at runtime. No mount the container runtime or the
    kubelet adds is modelled, as `/dev`, `/proc`, `/sys`, the service
    account token, `/etc/hosts`, `/etc/resolv.conf` or the `/etc/mtab`
    link: each is made only under conditions the kind does not see, or may
    be hidden by a mount of the pod's. So a path under `/dev` or `/proc`,
    as `/dev/stdout`, is judged by the pod's mounts alone, like any other:
    a read-only or directory mount the pod makes there is refused as
    anywhere. Not judged here, or in the previous entry, is a
    field the operator drops at the version it compares, `queryLogFile`
    below Prometheus v2.16.0 and `scrapeFailureLogFile` below v2.55.0: the
    version gate refuses it instead. Where the read-only mount that decides
    is at a path the operator mounts a volume at, which `volumeMounts` may not
    name as the operator writes it, the refusal advises a file under a writable volume rather than a
    mount there.
  - a negative request or limit in `resources`, `thanos.resources` or a
    listed container.
  - a parseable `version` of a major other than 2 or 3: the operator
    builds no configuration for it (`NewConfigGenerator`,
    `pkg/prometheus/promcfg.go`).
  - a remote write entry's `azureAd` with no authentication method:
    none of `managedIdentity`, `oauth`, `sdk` and `workloadIdentity`
    (go-kure/launcher#1023). The operator refuses the object at reconcile
    without one, at every version, wherever it generates the configuration
    (`validateRemoteWriteSpec`, `pkg/prometheus/operator.go`): not with
    every monitor selector unset, unless it is run with unmanaged
    configuration disabled. The CRD states no rule for it. It is refused
    wherever the operator may reach the entries, the kind not seeing how the
    operator is run: at an unset `version` or a parseable one of major 2 or
    3 (on another major the major is refused, and an unparsable one is left
    to the operator, which fails before it reads them), ahead of the version
    gates below, so the gate of `azureAd`, which says the operator drops it
    below 2.45.0, fires only on one with a method
    (`remoteWrite[0].azureAd: no authentication method: the Prometheus
    operator requires one of managedIdentity, oauth, sdk and
    workloadIdentity, and refuses the object at reconcile without one, at
    every version, wherever it generates the remote write configuration;
    set one, or leave azureAd unset`).
  - a field the operator reads only from some Prometheus version on, where
    `version` is below that version. An unset `version` is judged against
    the operator release launcher vendors (v0.95.1, default v3.14.0), at
    which no field below is refused; an unparsable one is left to the
    operator. Below its version the operator leaves the flag off the
    container, the setting out of the configuration or of the scrape
    configurations of the monitor objects, serves the web endpoints over
    plain HTTP (with `web.tlsConfig`, while the config reloader and the
    Thanos sidecar it configures speak HTTPS, so their requests fail, and
    without `listenLocal` so do the probes and the pods never become
    ready), refuses the object at reconcile, or, for an `authorization`
    other than an Alertmanager endpoint's, panics where it generates the
    configuration that reads it, and exits, to panic again on each restart,
    reconciling only between crashes (`retentionPercentage:
    read by the Prometheus operator only for Prometheus 3.11.0 and later; at
    version v3.10.0 it drops it from the configuration it generates, and
    Prometheus runs without it; raise version to 3.11.0 or later, or leave
    retentionPercentage unset`). Where a field holds several gates, the
    highest it misses is named. Where the spec authors several fields
    outside their versions, the three below read only under some version
    included, the operator may reach any one's consequence first (a refusal
    at reconcile, before the configuration a drop is from; a panic
    generating one part, before another), so none is claimed: the refusal
    names each field with the versions it is read at (go-kure/launcher#1022;
    `version: refused: fields outside their versions at version v2.50.0:
    remoteWrite[].messageVersion (read from Prometheus 2.54.0),
    remoteWrite[].azureAd.sdk (read from Prometheus 2.52.0); the Prometheus
    operator does not run the object as authored there; raise version to
    2.54.0 or later, or leave those fields unset`). The version a refusal
    suggests, of one field or of several, is one at which every gated field
    authored is read, a minimum or a ceiling not missed at `version`
    included (`allowOverlappingBlocks` bounds it from 2.11.0 on and below
    2.39.0); where there is none, it suggests only leaving the fields refused
    unset (go-kure/launcher#1047). A field
    authored with the parent or the field it needs, both below their
    versions, is one of several. The gates under v2.45.0, the oldest
    Prometheus in the operator's compatibility matrix, are below the range
    the operator tests, and are kept.
    - flags: `rules.alert.forOutageTolerance`, `forGracePeriod` and
      `resendDelay` (2.4.0), `query.maxSamples` (2.5.0), `web.pageTitle` and
      `logFormat` other than `logfmt` (2.6.0), `retentionSize` (2.7.0),
      `allowOverlappingBlocks` and `walCompression` (2.11.0),
      `web.tlsConfig` and `web.httpConfig` (2.24.0), `enableFeatures`
      (2.25.0), `extraScrapeMetrics: true` (2.32.0; the operator passes it
      as a feature flag up to 3.9 and writes it to the configuration from
      3.10.0), `enableRemoteWriteReceiver` (2.33.0), `enableOTLPReceiver`
      (2.47.0), `remoteWriteReceiverMessageVersions` (2.54.0).
    - the configuration: `queryLogFile` (2.16.0), `exemplars`
      (2.29.0), `tsdb.outOfOrderTimeWindow` (2.39.0), `bodySizeLimit`,
      `sampleLimit`, `targetLimit`, `labelLimit`, `labelNameLengthLimit` and
      `labelValueLengthLimit` (2.45.0), `keepDroppedTargets` (2.47.0),
      `scrapeProtocols` (2.49.0), `ruleQueryOffset` (2.53.0),
      `scrapeFailureLogFile` (2.55.0), `nameValidationScheme` (3.0.0),
      `nameEscapingScheme` and `convertClassicHistogramsToNHCB` (3.4.0),
      `scrapeClassicHistograms` (3.5.0), `scrapeNativeHistograms` (3.8.0),
      `tsdb.staleSeriesCompactionThreshold` (3.10.0), `retentionPercentage`
      (3.11.0), `tsdb.chunkEncoding` (3.13.0); `otlp` (2.55.0, refused at
      reconcile) with `promoteResourceAttributes` (2.55.0),
      `translationStrategy` (3.0.0; `NoTranslation` 3.4.0 and
      `UnderscoreEscapingWithoutSuffixes` 3.6.0, refused at reconcile),
      `keepIdentifyingResourceAttributes` (3.1.0), `convertHistogramsToNHCB`
      (3.4.0), `promoteAllResourceAttributes` and `ignoreResourceAttributes`
      (3.5.0), `promoteScopeMetadata` (3.6.0),
      `labelNameUnderscoreSanitization` and
      `labelNamePreserveMultipleUnderscores` (3.8.0).
    - the scrape configurations: `overrideHonorTimestamps` (2.9.0),
      `serviceDiscoveryRole: EndpointSlice` (2.21.0), `enforcedTargetLimit`
      (2.21.0), `enforcedLabelLimit`, `enforcedLabelNameLengthLimit` and
      `enforcedLabelValueLengthLimit` (2.27.0), `enforcedBodySizeLimit`
      (2.28.0), `enforcedKeepDroppedTargets` (2.47.0); a scrape class's
      `authorization` (2.26.0), `attachMetadata` (2.35.0 for PodMonitors,
      2.37.0 for ServiceMonitors) and `fallbackScrapeProtocol` (3.0.0); and
      `scrapeProtocols: [PrometheusText1.0.0]` (3.0.0-rc.0).

      A count or length limit, enforced or not, of 0 is written at no
      version, and is not refused at any; a body size limit is written
      whatever its value, `0` included.
    - an Alertmanager endpoint: `apiVersion` (2.11.0), `basicAuth` and
      `authorization` (2.26.0), `enableHttp2` (2.35.0), `sigv4` (2.48.0),
      `alertRelabelings` (2.51.0), `sigv4.useFIPSSTSEndpoint` (2.54.0),
      `sigv4.externalId` (3.11.0).
    - a remote read entry: `name` (2.15.0), `headers`, `authorization` and
      `followRedirects` (2.26.0), `oauth2` (2.27.0), `filterExternalLabels`
      (2.34.0).
    - a remote write entry: `headers` and `name` (2.15.0), `metadataConfig`
      (2.23.0), `queueConfig.minShards` (2.6.0), `authorization`, `sigv4`,
      `followRedirects` and `queueConfig.retryOnRateLimit` (2.26.0),
      `oauth2` and `sendExemplars` (2.27.0),
      `metadataConfig.maxSamplesPerSend` (2.29.0), `enableHTTP2` (2.35.0),
      `sendNativeHistograms` (2.40.0), `azureAd` (2.45.0),
      `queueConfig.sampleAgeLimit` (2.50.0), `messageVersion` and
      `sigv4.useFIPSSTSEndpoint` (2.54.0), `roundRobinDNS` (3.1.0),
      `sigv4.externalId` (3.11.0); under `azureAd`, refused at reconcile:
      `oauth` (2.48.0), `sdk` (2.52.0), a `managedIdentity` without
      `clientId` (3.5.0), `workloadIdentity` (3.7.0); and `azureAd.scope`
      (3.9.0).
    - `apiserverConfig.authorization` (2.26.0); the `minVersion` (2.35.0)
      and `maxVersion` (2.41.0) of a TLS configuration (under
      `apiserverConfig`, `tracingConfig`, a scrape class, an Alertmanager
      endpoint, a remote read or write entry and its `oauth2`); the proxy
      settings `noProxy`, `proxyFromEnvironment` and `proxyConnectHeader`
      (2.43.0, under `apiserverConfig`, an Alertmanager endpoint and a remote
      read or write entry), and an `oauth2`'s `proxyUrl`, its proxy settings
      and its `tlsConfig` (2.43.0); a relabeling `action` of `lowercase` or
      `uppercase` (2.36.0) or of `keepequal` or `dropequal` (2.41.0), refused
      at reconcile, in a scrape class's `relabelings` and
      `metricRelabelings` and an Alertmanager endpoint's `relabelings` and
      `alertRelabelings`.

    The operator reads three fields only below some version, refused at it
    and above, and so with `version` unset: an Alertmanager endpoint's
    `apiVersion: v1` (Prometheus 2.x only; refused at every 3.x version, prereleases included),
    a remote write entry's `queueConfig.maxRetries` (below 2.11.0), and
    `allowOverlappingBlocks` (from 2.11.0 and below 2.39.0).
  - a relabeling rule's `targetLabel` that is not a legacy label name
    (`[a-zA-Z_][a-zA-Z0-9_]*`), such as `service.name` or a `${1}`
    template, below Prometheus 3.0.0, from which the operator judges label
    names by the UTF-8 scheme instead (`ValidationSchemeForPrometheus`,
    `pkg/operator/validations.go`): it refuses the object at reconcile
    (go-kure/launcher#1018). The operator checks the `targetLabel` of the
    actions `replace` (an empty action included), `hashmod`, `lowercase`,
    `uppercase`, `keepequal` and `dropequal`, in the four lists whose
    actions are gated above. Unset, `version` is the operator's default
    v3.14.0, at which none is refused; an unparsable `version` is left to
    the operator, and an action refused below its version is refused first.
    `TestPrometheusRelabelTargetLabels_MatchVendoredSource` holds the
    threshold, the actions and the lists to the operator's source.
  - a `labelmap` rule's `replacement`, where set, that is not a legacy label
    name in which `$var` and `${var}` references may stand for any part
    (`relabelTargetLegacy`, `pkg/prometheus/validation/validator.go`), such
    as `service.$1`, below Prometheus 3.0.0, in the same four lists and on
    the same terms: the operator judges it by the UTF-8 scheme from 3.0.0,
    and refuses the object at reconcile below it (go-kure/launcher#1038).
    An empty `replacement` is left to the operator, which refuses it at every
    version. `TestPrometheusRelabelLabelmapReplacement_MatchVendoredSource`
    holds the pattern and the action to the operator's source. Like the
    refusals of fields above, a refusal of a `targetLabel` or a `replacement`
    suggests a version from 3.0.0 on only where every gated field authored is
    read there too: with `allowOverlappingBlocks` (below 2.39.0) or an
    Alertmanager endpoint's `apiVersion: v1` (Prometheus 2.x), it suggests
    only the name. The other way round, a refusal of a field above suggests no
    version below 3.0.0 where a label name outside the legacy scheme is
    authored: at `version: v3.0.0`, `allowOverlappingBlocks` beside a
    `replacement` of `service.$1` is only to be left unset.
  - a field of `thanos` the operator reads only from some Thanos version on,
    where `thanos.version` is below it; unset, the operator's default
    v0.42.5, at which none is refused (an empty `thanos.version` is not
    unset to the operator, which cannot parse it, and is left to it): `getConfigTimeout` and
    `getConfigInterval` (0.29.0), `grpcServerTlsConfig.minVersion` (0.37.0),
    `grpcServerTlsConfig.cipherSuites` and `curves` (0.42.0). Below it the
    operator leaves the flag off the sidecar.
  - `thanos.blockSize`, other than the CRD's default `2h` that the API
    server writes where it is unset, where the operator reads it at neither
    version: with
    object storage configured and `disableCompaction` unset, from
    Prometheus 3.9.0 and Thanos 0.42.0 on it keeps local compaction on in
    step with the sidecar and passes no block duration
    (`compactionModeFor`), so it is refused there, with both versions unset
    too. Below either
    version, or with `disableCompaction`, it disables compaction and passes
    `blockSize` as the block duration. An unparsable `version` or
    `thanos.version` is left to the operator. Without object storage and
    without `disableCompaction` the operator reads `blockSize` at no
    version; that is not held here.

    `TestPrometheusVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's packages `pkg/prometheus`,
    `pkg/prometheus/server` and `pkg/prometheus/validation` (every Go file,
    its tests aside) and holds each to these lists, or to the reason it
    gates no field of this kind (a field of a ServiceMonitor, PodMonitor,
    Probe or ScrapeConfig object, the configuration of a PrometheusAgent, a
    choice of how the operator spells a flag), and the two defaults to the
    operator's `pkg/operator/defaults.go`; the files are vendored as for
    `alertmanager`.
  - a name the operator's objects cannot be named after: the data volume
    `prometheus-<name>-db`, the volume of the third rule ConfigMap
    `prometheus-<name>-rulefiles-2`, and the hostname of the last pod of the
    last shard, `prometheus-<name>-<replicas-1>` or
    `prometheus-<name>-shard-<shards-1>-<replicas-1>`, must each be a
    DNS-1123 label; and the name of the last shard's StatefulSet,
    `prometheus-<name>` or `prometheus-<name>-shard-<shards-1>`, must be at
    most 52 characters: the StatefulSet controller labels each of its pods
    `controller-revision-hash` with that name, a `-` and a hash of up to 10
    characters, a label value the API refuses beyond 63 characters. So a
    name has no dot and at most 40 characters, 33 with 2 to 10 shards, and
    fewer with more. Neither applies where `replicas` is 0, with no pod to
    name or label. The operator mounts three rule
    ConfigMaps whatever the rules (server/rules.go:119), and more only where
    the PrometheusRules it selects outgrow three of half a MiB each; the
    name of an eleventh, `rulefiles-10`, depends on the cluster's rules and
    is not checked. The refusal names the component, or `objectName` where
    that set the name.

  **The API's expression rules are not checked.** Five are listed
  (`prometheusRulesLeft`) and left to the API server: with the sharding
  strategy mode `Topology`, `shards` must be at least the number of topology
  values, and `topology` needs that mode; an `updateStrategy` with a
  `rollingUpdate` must have the type `RollingUpdate`; and the `externalId`
  of a `sigv4`, a remote write entry's or an Alertmanager endpoint's, needs
  a `roleArn`.

  **Topology sharding below the node metadata versions is not refused**
  (go-kure/launcher#1019). With `shardingStrategy.mode: Topology`, the
  operator assigns each target to the shards of its zone: the zone of its
  EndpointSlice, else the pod's zone label where the cluster sets one
  (Kubernetes 1.35 and later, `PodTopologyLabelsAdmission`), else its
  node's zone label. For the node's, the operator writes
  `attach_metadata: {node: true}` into the service discovery of every
  PodMonitor from Prometheus 2.35.0 and every ServiceMonitor from 2.37.0,
  and below them drops it (`mergeAttachMetadataForTopology`,
  `pkg/prometheus/promcfg.go`). There a target with no EndpointSlice zone,
  as every PodMonitor's, has no zone, and no shard scrapes it. Whether
  topology sharding is active at all depends on the operator's
  `PrometheusTopologySharding` feature gate, and the fallback on the
  cluster's pod labels; neither is visible at build, so the kind refuses
  nothing and documents the limit. A cluster without pod topology labels
  that shards by topology runs Prometheus 2.37.0 or later.
  `TestPrometheusTopologyNodeZone_MatchVendoredSource` holds both versions
  to the vendored `promcfg.go`.

  **What the type writes unauthored:** `arbitraryFSAccessThroughSMs: {}`,
  `resources: {}` and `rules: {alert: {}}`, on every Prometheus; on an
  authored `thanos`, its `resources: {}`; and, on an authored `storage`, a
  `volumeClaimTemplate` with `metadata: {}` and `status: {}`.

  **Policy.** Refused, with a workload kind's classes: `image`,
  `thanos.image`, the image of a listed container or init container (a patch
  included) and an image volume's reference outside the allowed registries;
  under a policy that lists allowed registries, an image the operator
  chooses (below); `replicas` times `shards` over the replica maximum, as the
  operator counts them (below; `replicas times shards 4 exceeds enforced
  maximum 3`); the cpu or memory of `resources`,
  of `thanos.resources` and of a listed container over the maxima; the
  storage a claim requests over the storage maximum; a hostPath volume;
  `hostNetwork` where the policy does not allow the host network; a
  privileged listed container, a pod-level
  `securityContext.windowsOptions.hostProcess` and a capability the policy
  does not allow. `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` holds
  every top-level field of the spec that shapes the pods to a refusal shown
  on a document or a stated reason; `shards` and the sidecar's fields are
  not top-level pod fields of the derivation, and `shards`, `thanos.image`
  and `thanos.resources` are held by `TestPrometheus_ReplicasTimesShards` and
  `TestPrometheus_PolicyRefusals` instead.

  **An image the operator chooses is refused under a registry allowlist.**
  Where the spec names no image for a container the operator generates, the
  operator chooses the one it runs, and no registry allowlist reaches that
  choice, so a policy with a non-empty `AllowedRegistries` refuses it
  (`image: unset, so the Prometheus operator chooses the image the pods run,
  …`; `thanos.image: unset, …`; `the image of the config-reloader container
  (containers): unset, …`) and the author names an image from one of the
  listed registries: `image` or a patch of `prometheus`; where `thanos` is
  set, `thanos.image` or a patch of `thanos-sidecar`; a patch of
  `config-reloader`; and a patch of `init-config-reloader` in
  `initContainers`. The operator generates the prometheus container, both
  reloaders and, where `thanos` is set, the sidecar (`makeStatefulSetSpec`,
  `createThanosContainer`, `BuildConfigReloader`). Without such a list, it
  builds.

  **The operator's own counts are held, not written.** The operator runs one
  StatefulSet a shard with the replica count each, and reads an unset or
  negative `replicas` as 1 and an unset `shards` as 1 (`ReplicasNumberPtr`,
  `shardsNumber`; a `shards` below 1 is refused). The replica maximum holds that
  product whether or not either is authored, and the emitted object still
  leaves both as authored.

  **Credentials: three are held, the rest are stated.** The deprecated
  `bearerToken` of a `remoteWrite` entry, of a `remoteRead` entry and of
  `apiserverConfig` holds a credential in the object; under a policy that
  forbids explicit secrets each is refused (`oam.RefusalExplicitSecret`), and
  without one it is written as authored. Every other credential of the spec
  is the key of a Secret or the path of a file in the container.
  **Not held:** a value of a `headers` map (`remoteWrite`, `remoteRead`,
  `tracingConfig`), the userinfo of a URL, and free text (`additionalArgs`,
  the `env` of a listed container) are written as authored, under a policy
  that forbids explicit secrets too.
  `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields
  whose name suggests a credential and holds each to the policy or a stated
  reason.

  **`podMetadata`.** As on `alertmanager`: the pods carry the component label
  only if the author writes the component's own value into
  `podMetadata.labels`. The operator reserves seven labels of its own on the
  pods (`prometheus`, `app.kubernetes.io/instance`,
  `app.kubernetes.io/managed-by`, `app.kubernetes.io/name`,
  `app.kubernetes.io/version`, `operator.prometheus.io/name`,
  `operator.prometheus.io/shard`) and the annotation
  `kubectl.kubernetes.io/default-container`. The operator copies the
  authored labels and annotations over `app.kubernetes.io/version` and the
  annotation, so an authored value replaces those two, and then sets the
  other six labels over the authored ones, so an authored value of those
  does not hold (`BuildPodMetadata`, promcfg.go:1367-1384;
  server/statefulset.go:219-230). Launcher does not refuse such a key.

  **Not covered.** Whether what the object refers to exists (a Secret key, a
  mounted Secret or ConfigMap, the ServiceAccount and the RBAC Kubernetes
  service discovery needs, a governing Service named by `serviceName`, the
  objects the selectors match, the Alertmanager endpoints). What the operator
  adds on its own beyond the containers above (the `prometheus-operated`
  Service, the configuration Secret) is not in the object; its code is not in
  the linked module, and was read at the version above for the containers,
  their images and the pod count only. The object's status is the operator's
  and is not written.

- **servicemonitor**, **podmonitor**, **prometheus-probe**, **prometheusrule**
  (go-kure/launcher#790) are the kind-named projections of four objects of the
  Prometheus operator's `monitoring.coreos.com/v1` API: a ServiceMonitor, a
  PodMonitor, a Probe and a PrometheusRule. Each is built on `policyFreeKind`
  and emits that one object in the build namespace, named after the component
  unless `objectName` names it; the handler adds no label, no annotation and no
  default of its own. Each declares its object as namespaced, so the object name is
  claimed in the object's namespace. The Probe's type name carries a prefix
  because a probe, in this package, is a container's.

  **No capability is required, and none gates these kinds.** Launcher does
  not ask whether the cluster serves `monitoring.coreos.com/v1`: where the
  operator's CRDs are not installed the component builds, and the object is
  refused at apply. Whoever may author a component may author these. A policy
  can keep the kinds out of a build through the object kind policy
  (`oam.ObjectKindPolicy`, go-kure/launcher#922).

  **Authored.** The properties are the top-level json fields of the spec type,
  decoded strictly at every depth: an unknown key is refused wherever it sits
  (an endpoint, a relabeling rule, a rule of a group).
  - `servicemonitor` (`ServiceMonitorSpec`): `endpoints`, `selector`,
    `namespaceSelector`, `selectorMechanism`, `jobLabel`, `targetLabels`,
    `podTargetLabels`, `attachMetadata`, `bodySizeLimit`,
    `serviceDiscoveryRole`, and the scrape settings below.
  - `podmonitor` (`PodMonitorSpec`): `podMetricsEndpoints`, `selector`,
    `namespaceSelector`, `selectorMechanism`, `jobLabel`, `podTargetLabels`,
    `attachMetadata`, `bodySizeLimit`, and the scrape settings below.
  - `prometheus-probe` (`ProbeSpec`): `prober`, `targets`, `module`,
    `jobName`, `interval`, `scrapeTimeout`, `metricRelabelings`, `params`,
    the HTTP client settings the type embeds (`authorization`, `basicAuth`,
    `oauth2`, `bearerTokenSecret`, `followRedirects`, `enableHttp2`,
    `tlsConfig`), and the scrape settings below.
  - `prometheusrule` (`PrometheusRuleSpec`): `groups`, each with its `name`,
    `rules` and evaluation settings. A rule's `expr` is a string or a number
    and is emitted as the one authored.
  - The scrape settings the three scrape kinds share: `sampleLimit`,
    `targetLimit`, `labelLimit`, `labelNameLengthLimit`,
    `labelValueLengthLimit`, `keepDroppedTargets`, `scrapeProtocols`,
    `fallbackScrapeProtocol`, `scrapeClass`, `scrapeNativeHistograms`,
    `scrapeClassicHistograms`, `extraScrapeMetrics` (Prometheus v3.10.0 or
    later; the operator drops it from the configuration of an older one,
    which these kinds cannot see), `nativeHistogramBucketLimit`,
    `nativeHistogramMinBucketFactor` and `convertClassicHistogramsToNHCB`.
  - An authored `0` or `false` is kept where the API tells it from an unset
    field (`sampleLimit: 0`, `filterRunning: false`, a group's `limit: 0`), and
    left out where the type omits a zero that means the same
    (`honorLabels: false`). A quantity written as a number is emitted in its
    canonical string form (`nativeHistogramMinBucketFactor: 1.1` as `1100m`).
    The API's two defaults are strings (a prober's `path`, `/probe`, and a
    relabeling rule's `action`, `replace`): the type omits an empty string
    there, so an authored `""` would be defaulted, and it is refused
    (`prober.path: "" cannot be carried by the Prometheus operator API types
    (…)`). `TestMonitoringKinds_DefaultedZeros` holds each kind's list of
    such fields to the numbers, booleans and strings that are omitted when
    zero and defaulted to something else, from the default markers of the
    linked module's source.

  **Required** is a field the API requires that the Go type writes whether or
  not it was authored, so that the object would not show the omission. It is
  the rule every kind follows, not a wider one: sent as it was authored, the
  document is one the API server refuses, and only the Go type's zero value
  hides that. Each must be authored (`selector: required (…)`,
  `endpoints[1].oauth2.tokenUrl: required (…)`):
  - a `servicemonitor`'s `endpoints` and `selector`, and a `podmonitor`'s
    `selector`. No default selector is filled: unauthored, the type would
    write `selector: {}`, which selects every Service or pod of the selected
    namespaces. `selector: {}` and `endpoints: []` are authored values and
    build.
  - the `clientId`, `clientSecret` and `tokenUrl` of an `oauth2`, on an
    endpoint of either monitor and on a Probe;
  - a rule group's `name` and a rule's `expr` (unauthored, the type would
    write `expr: 0`);
  - the `key` and `operator` of a match expression, in a monitor's `selector`
    and in a Probe's `targets.ingress.selector`
    (`selector.matchExpressions[0].operator: required (…)`). The selector is
    a Kubernetes type, which carries no `+required` marker: the generator
    derives the two fields from that type's source by the schema generators'
    rule, that a field with no optional marker whose json tag keeps it when
    empty is required. Presence only: the operator's value and its `values`
    are left to the API server (see ["A label selector's match expressions"](conventions.md#a-label-selectors-match-expressions)).
  - since go-kure/launcher#883 (**breaking** for a document that leaves one
    out under an authored parent): the `key` of a Secret or ConfigMap key
    reference (`endpoints[0].tlsConfig.ca.secret.key: required (…)`), the
    `key` of a proxy connect header's Secret key reference, and the
    `username` and the `password` of a `basicAuth`, on an endpoint of either
    monitor and on a Probe, which the API server accepts left out and the
    operator refuses.

  **A required field the type leaves out when it is empty is refused too:**
  the `name` of a Probe parameter (`params[1].name: required (…)`), unauthored
  or empty. The object would show the omission and the API server refuse it.
  `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives these fields
  for every kind in its table, from the CRD the linked module ships or, where
  it ships none, from the markers of its source. It holds each to a refusal,
  shown on a document, or to a stated reason. The same test derives the
  fields the API defaults and the type writes unauthored, where the default
  would never apply, and holds each to one of three answers: refused, filled
  by the kind with the API's default, or harmless with the reason.

  The table holds the kind components that decode their properties into an
  upstream type, but for two groups. The `httproute` kind is not in it: the
  experimental-channel CRD requires the `protocol` of an `externalAuth`
  filter, of a rule and of a backend reference, the type leaves an empty one
  out, and the kind does not refuse it. The Flux kinds (`helmrelease`,
  `fluxcd-kustomization`, `helmrepository`, `ocirepository`, `gitrepository`,
  `bucket`, `helmchart`) are not in it either: no row is written for them.

  **A Probe needs `prober.url` here, because the object always carries a
  prober.** This is a limit of the Go type, not a rule of the API: the API
  does not require `prober`, but the type writes one whether or not it was
  authored, and the API server refuses a prober without a `url`. A Probe
  without a prober cannot be emitted. An empty `url` is refused as an
  unauthored one is.

  **These lists are generated** (go-kure/launcher#883; see ["Required fields
  a kind writes unauthored and does not refuse"](conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse)) from the fields the source
  marks required and the type writes unauthored, the linked module's and
  those of the Kubernetes types the specs embed, so a dependency bump that
  adds, drops or moves one fails `TestKindComponents_RequiredIsGenerated`.
  `TestKindComponents_GeneratedRequiredBothWays` holds each field both ways
  on the kind, and runs a path of every type that holds one through the
  operator's CRD, vendored from its repository at the linked release (the
  module ships none). **Not refused:**
  - an authored empty string in a required field (a group's `name: ""`),
    `prober.url` and a parameter's `name` excepted. It is a value, and the
    API server's to refuse;
  - every other value rule of the API (formats, enumerations, lengths, that a
    rule is a recording or an alerting one), and the operator's own checks of
    an object the API server has admitted: that a Probe has targets, that a
    scrape timeout is no longer than its interval, that an endpoint or a
    Probe authenticates one way. Whether a rule's expression is valid PromQL
    is not checked either.

  **What the type writes unauthored.** A `servicemonitor` or `podmonitor`
  carries `namespaceSelector: {}`, which selects the object's own namespace.
  A `podmonitor` without endpoints carries `podMetricsEndpoints: null`, which
  the API server drops as it drops every null of a field that is not
  nullable. A `prometheus-probe` carries `targets: {}`, and the operator
  rejects a Probe with no target. A `tlsConfig` carries `ca: {}` and
  `cert: {}` where they were not authored.

  **The selector, the prober and the targets are the author's.** Launcher
  points none at a component and looks for none in the document. To scrape a
  workload component's pods, select their `app` label
  (`matchLabels: {app: <component>}`, valued as ["The `app` label"](conventions.md#the-app-label)
  describes); to scrape a `service` component's
  Service, select a label its object carries (the component label, see
  "Component label and ownership" in the OAM model). A Secret or ConfigMap an
  object refers to is read by the operator in the object's namespace.

  **Selected by a Prometheus through the object's labels.** A Prometheus
  picks these objects up through its label selectors
  (`serviceMonitorSelector`, `podMonitorSelector`, `probeSelector`,
  `ruleSelector`). The handler writes no label, so the object carries the
  component label, whose value is the component's, and what `labels` authors:
  for a Prometheus that selects on a fixed label (`release: <name>`, say),
  write that label there.

  **Policy.** No field of these specs is one an `oam.Policy` method speaks to,
  so `ApplyPolicy` enforces nothing and fills nothing, and each builds the
  same under every policy and under none.
  - **Hosts are not checked.** A host these objects name is one Prometheus
    reaches, not an artifact source, and none is held to the policy's allowed
    registries: `prober.url`, a `proxyUrl` (of an endpoint, a prober or an
    `oauth2`), an `oauth2`'s `tokenUrl`, and the static targets of a Probe.
  - **No field holds a literal secret by design, and none is checked.** A
    credential is a reference to a key of a Secret: `authorization.credentials`,
    `basicAuth`, `bearerTokenSecret`, an `oauth2`'s `clientSecret`, a
    `tlsConfig`'s `keySecret`, the values of `proxyConnectHeader`. Free text
    that could hold one is written to the object as authored, under a policy
    that forbids explicit secrets too: `params`, an `oauth2`'s
    `endpointParams`, and a `proxyUrl` with credentials in it.
  - **File paths are not checked.** A `servicemonitor` endpoint may name a
    file in the Prometheus container (`bearerTokenFile`, and `caFile`,
    `certFile` and `keyFile` under `tlsConfig`).

  **Not covered.** Whether what is selected or referred to exists (a
  Service, a pod, an Ingress, a named port, a Secret key), and whether a
  Prometheus of the cluster selects the object. The object's status is the
  operator's and is not written.
