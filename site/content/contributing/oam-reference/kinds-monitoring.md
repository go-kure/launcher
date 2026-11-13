---
title: "Kinds: monitoring"
weight: 9
---

Implementation, history and tests of the entries on the reference page [Kinds:
monitoring](../../api-reference/oam-components/kinds-monitoring.md). Every test
named here is in `pkg/oam/builtin/components/`, in the file the note beside it
names.

## alertmanager

- **History:**
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "**alertmanager**" in its introduction.]
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`) [The
    reference cited it after "build through the object kind policy" in its
    Validation section.]
  - go-kure/launcher#1300 (whether an app.yaml may set
    `alertmanagerConfigFileSystemMode: AllowAll`) [The reference cited it after
    "app.yaml may set it is open" in its Properties section.]
  - go-kure/launcher#883 (the generated required lists, refusing an omitted
    CRD-required field the Go type writes empty) [The reference cited it after
    "**The kind's required list is generated**" in its Properties section; and
    after "operator's CRD vendored for the tests" in its Validation section.]
  - go-kure/launcher#1022 (the refusal that names each field when several are
    below their versions) [The reference cited it after "claimed for any, as on
    `prometheus`" in its Validation section.]
  - go-kure/launcher#1211 (refusing an absolute or `..` `subPath` in
    `volumeMounts`) [The reference cited it after "breaks a rule of a mount" in
    its Validation section.]
  - go-kure/launcher#1227 (the typed `volumeMount` rules) [The reference cited
    it after "breaks a rule of a mount" in its Validation section.]
  - go-kure/launcher#1050 (the mount-path spelling rule of the monitoring kinds)
    [The reference cited it after "CRI-O makes only one of them" in its
    Validation section.]
  - go-kure/launcher#959 (the Service-name check and
    RelaxedServiceNameValidation) [The reference cited it after "unless
    `spec.featureGates` sets that gate on" in its Validation section.]
  - go-kure/launcher#1230 (version-gated values: tolerations, the Service name
    rule and `trafficDistribution`) [The reference cited it after "unless
    `spec.featureGates` sets that gate on" in its Validation section.]
  - go-kure/launcher#963 (`portName` against the `containers` patch) [The
    reference cited it in its Validation section.]
- **Tests:**
  - `TestMonitoringWorkloadKinds_DefaultedZeros` derives the list. It reads the
    type, the default markers of the operator's source, the field comments of
    the Kubernetes types, and the API server's defaulting code for their
    strings. It then shows each field refused on a document. ["The list" is the
    list of fields the type cannot carry, which the reference page gives, "It"
    is the test, and the test is in `monitoring_workload_internal_test.go`.]
  - `TestMonitoringWorkloadKinds_HostNetworkHostPort` shows the `hostPort`
    refusal. ["The `hostPort` refusal" is the refusal of a `hostPort` written as
    `0` under `hostNetwork: true`, which the reference page gives, and the test
    is in `monitoring_workload_internal_test.go`.]
  - `TestAlertmanagerVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's package `pkg/alertmanager` (every Go file of
    it, its tests aside; at v0.95.1 the comparisons are in `statefulset.go`,
    `amcfg.go` and `operator.go`) and holds each to this list, or to the reason
    it gates no field of this kind (the configuration of a receiver, a route or
    a configSecret Secret); the files, with the operator's
    `pkg/operator/defaults.go` and `pkg/operator/validations.go`, are vendored
    unmodified under `testdata/upstream/prometheus-operator`, with the project's
    LICENSE and NOTICE and a SOURCE file naming each file's git blob id, and
    `mise run vendor-prometheus-operator-gates <tag>` re-fetches them for the
    tag `go.mod` links. ["This list" is the list of fields the operator reads
    only from some Alertmanager version on, which the reference page gives, and
    the test is in `alertmanager_version_gates_internal_test.go`.]
  - `TestAlertmanagerFeatureFlags_MatchVendoredSource` holds the names, and
    `NewFlags` statement by statement, to `featurecontrol.go` of each minor's
    latest release, vendored unmodified under `testdata/upstream/alertmanager`
    with the project's LICENSE, each release's NOTICE and a SOURCE file naming
    each file's git blob id; `mise run vendor-alertmanager-features 0.27 0.34`
    re-fetches them. ["The names" are the feature flag names the reference page
    lists, and the test is in `alertmanager_features_internal_test.go`.]
  - `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields of
    the operator's types whose name suggests a credential and holds each to a
    stated reason it holds none. ["One" is a credential, and the test is in
    `monitoring_workload_internal_test.go`.]

## thanosruler

- **History:**
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "**thanosruler**" in its introduction.]
  - go-kure/launcher#883 (the generated required lists, refusing an omitted
    CRD-required field the Go type writes empty) [The reference cited it in its
    Properties section.]
  - go-kure/launcher#959 (the Service-name check and
    RelaxedServiceNameValidation) [The reference cited it after "unless
    `spec.featureGates` sets RelaxedServiceNameValidation on " in its Validation
    section.]
  - go-kure/launcher#1230 (version-gated values: tolerations, the Service name
    rule and `trafficDistribution`) [The reference cited it after "unless
    `spec.featureGates` sets RelaxedServiceNameValidation on " in its Validation
    section.]
  - go-kure/launcher#1211 (refusing an absolute or `..` `subPath` in
    `volumeMounts`) [The reference cited it after "breaks a rule of a mount" in
    its Validation section.]
  - go-kure/launcher#1227 (the typed `volumeMount` rules) [The reference cited
    it after "breaks a rule of a mount" in its Validation section.]
  - go-kure/launcher#1050 (the mount-path spelling rule of the monitoring kinds)
    [The reference cited it after "CRI-O makes only one of them" in its
    Validation section.]
  - go-kure/launcher#976 (refusing a field the operator drops below the
    ThanosRuler version) [The reference cited it after "where `version` is below
    that version" in its Validation section.]
  - go-kure/launcher#1022 (the refusal that names each field when several are
    below their versions) [The reference cited it after "claimed for any, as on
    `prometheus`" in its Validation section.]
  - go-kure/launcher#1023 (refusing a remote write `azureAd` with no
    authentication method) [The reference cited it after "`sdk` and
    `workloadIdentity`), as on `prometheus`" in its Validation section.]
  - go-kure/launcher#1020 (refusing the remote write fields the operator drops
    at every Thanos version) [The reference cited it after "`azureAd.scope`, or
    `sigv4.externalId` with a `roleArn`" in its Validation section; and after
    "passes to Thanos at no version" in its Validation section.]
- **Tests:**
  - `TestCoreKindSchemas_CoverSpec` records it as a mapping (`externalLabels`
    for `labels`), so a second renamed field fails there until it is listed.
    ["It" is the rename of the spec's `labels` to the `externalLabels` property,
    which the reference page gives, "there" is that test, and the test is in
    `core_kinds_test.go`.]
  - `TestThanosRulerVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's package `pkg/thanos` (every Go file of it, its
    tests aside), vendored as for `alertmanager`, and holds each to this list.
    ["This list" is the list of fields the operator reads only from some Thanos
    version on, which the reference page gives, "as for `alertmanager`" refers
    to the vendoring of the test of the `alertmanager` entry above, and the test
    is in `thanosruler_version_gates_internal_test.go`.]
  - `TestThanosRulerRemoteWriteTexts_MatchVendoredSource` holds the list to
    every field `createOrUpdateRulerConfigSecret` ignores with minimum version
    `none`. ["The list" is the list of remote write fields the operator drops at
    every Thanos version, which the reference page gives, and the test is in
    `thanosruler_version_gates_internal_test.go`.]
  - `TestMonitoringExcludedGroup_MatchesMarkers` holds the written value to the
    default and the enum of the linked module's source. ["The written value" is
    `monitoring.coreos.com`, written for the group of an excluded object, and
    the test is in `monitoring_excluded_groups_internal_test.go`.]
  - `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` holds every field of the
    spec that shapes the pods to a refusal shown on a document or a stated
    reason, as for `alertmanager`. ["As for `alertmanager`" refers to the
    description of the test of the `alertmanager` entry, which stays on the
    reference page, and the test is in `monitoring_workload_internal_test.go`.]
  - `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields
    whose name suggests a credential and holds each to the policy or a stated
    reason. [The test is in `monitoring_workload_internal_test.go`.]

## prometheus

- **History:**
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "**prometheus**" in its introduction.]
  - go-kure/launcher#959 (the Service-name check and
    RelaxedServiceNameValidation) [The reference cited it after "unless
    `spec.featureGates` sets that gate on" in its Properties section.]
  - go-kure/launcher#1230 (version-gated values: tolerations, the Service name
    rule and `trafficDistribution`) [The reference cited it after "unless
    `spec.featureGates` sets that gate on" in its Properties section.]
  - go-kure/launcher#963 (`portName` against the `containers` patch) [The
    reference cited it in its Properties section.]
  - go-kure/launcher#883 (the generated required lists, refusing an omitted
    CRD-required field the Go type writes empty) [The reference cited it in its
    Properties section.]
  - go-kure/launcher#1211 (refusing an absolute or `..` `subPath` in
    `volumeMounts`) [The reference cited it after "breaks a rule of a mount" in
    its Validation section.]
  - go-kure/launcher#1227 (the typed `volumeMount` rules) [The reference cited
    it after "breaks a rule of a mount" in its Validation section.]
  - go-kure/launcher#1050 (the mount-path spelling rule of the monitoring kinds)
    [The reference cited it after "CRI-O makes only one of them" in its
    Validation section.]
  - go-kure/launcher#1055 (a log file on the read-only root filesystem) [The
    reference cited it after "mounts its `log-file` volume, is refused" in its
    Validation section; and after "filesystem, whose content is not judged" in
    its Validation section.]
  - go-kure/launcher#1051 (a log file under a `readOnly` mount whose content may
    link to a writable volume) [The reference cited it after "file for appending
    (`util/logging/file.go:43` at v3.7.0)" in its Validation section.]
  - go-kure/launcher#1023 (refusing a remote write `azureAd` with no
    authentication method) [The reference cited it after "of `managedIdentity`,
    `oauth`, `sdk` and `workloadIdentity`" in its Validation section.]
  - go-kure/launcher#1047 (a version-gate refusal that suggests a version which
    drops another authored field) [The reference cited it after "only leaving
    the fields refused unset" in its Validation section.]
  - go-kure/launcher#1022 (the refusal that names each field when several are
    below their versions) [The reference cited it after "the versions it is read
    at" in its Validation section.]
  - go-kure/launcher#1018 (an Alertmanager endpoint relabeling `targetLabel`
    outside the label-name scheme of the Prometheus version) [The reference
    cited it after "it refuses the object at reconcile" in its Validation
    section.]
  - go-kure/launcher#1038 (a `labelmap` replacement outside the label-name
    scheme below Prometheus 3.0.0) [The reference cited it after "the object at
    reconcile below it" in its Validation section.]
  - go-kure/launcher#1019 (topology sharding below the `attach_metadata`
    versions) [The reference cited it in its Validation section.]
- **Tests:**
  - `TestMonitoringExcludedGroup_MatchesMarkers` holds the written value to the
    module's default and enum. ["The module" is the linked module, and the test
    is in `monitoring_excluded_groups_internal_test.go`.]
  - `TestThanosDroppedMountFields` holds the list to every field of the type.
    ["The list" is the list of fields of a `thanos.volumeMounts` entry that the
    operator drops, which the reference page gives, and the test is in
    `prometheus_internal_test.go`.]
  - `TestPrometheusRelabelTargetLabels_MatchVendoredSource` holds the threshold,
    the actions and the lists to the operator's source. ["The threshold" is
    Prometheus 3.0.0, "the actions" and "the lists" are the relabeling actions
    and the four lists the reference page names, and the test is in
    `prometheus_relabel_target_label_internal_test.go`.]
  - `TestPrometheusRelabelLabelmapReplacement_MatchVendoredSource` holds the
    pattern and the action to the operator's source. ["The pattern" and "the
    action" are the `labelmap` replacement pattern and the `labelmap` action the
    reference page names, and the test is in
    `prometheus_relabel_target_label_internal_test.go`.]
  - `TestPrometheusVersionGates_MatchVendoredSource` parses every version
    comparison of the operator's packages `pkg/prometheus`,
    `pkg/prometheus/server` and `pkg/prometheus/validation` (every Go file, its
    tests aside) and holds each to these lists, or to the reason it gates no
    field of this kind (a field of a ServiceMonitor, PodMonitor, Probe or
    ScrapeConfig object, the configuration of a PrometheusAgent, a choice of how
    the operator spells a flag), and the two defaults to the operator's
    `pkg/operator/defaults.go`; the files are vendored as for `alertmanager`.
    ["These lists" are the lists of fields the operator reads only from some
    Prometheus or Thanos version on, which the reference page gives, "as for
    `alertmanager`" refers to the vendoring of the test of the `alertmanager`
    entry above, and the test is in
    `prometheus_version_gates_internal_test.go`.]
  - `TestPrometheusTopologyNodeZone_MatchVendoredSource` holds both versions to
    the vendored `promcfg.go`. ["Both versions" are Prometheus 2.35.0 for a
    PodMonitor and 2.37.0 for a ServiceMonitor, which the reference page names,
    and the test is in `prometheus_topology_internal_test.go`.]
  - `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` holds every top-level
    field of the spec that shapes the pods to a refusal shown on a document or a
    stated reason; `shards` and the sidecar's fields are not top-level pod
    fields of the derivation, and `shards`, `thanos.image` and
    `thanos.resources` are held by `TestPrometheus_ReplicasTimesShards` and
    `TestPrometheus_PolicyRefusals` instead. ["The derivation" is the derivation
    of the fields by the test of the `alertmanager` entry, which the reference
    page describes, `TestMonitoringWorkloadKinds_PodFieldsHeldOrListed` is in
    `monitoring_workload_internal_test.go`, and
    `TestPrometheus_ReplicasTimesShards` and `TestPrometheus_PolicyRefusals` are
    in `prometheus_test.go`.]
  - `TestMonitoringWorkloadKinds_CredentialsHeldOrListed` derives the fields
    whose name suggests a credential and holds each to the policy or a stated
    reason. [The test is in `monitoring_workload_internal_test.go`.]

## servicemonitor, podmonitor, prometheus-probe, prometheusrule

- **History:**
  - go-kure/launcher#790 (the kind-named projections) [The reference cited it
    after "**servicemonitor**, **podmonitor**, **prometheus-probe**,
    **prometheusrule**" in its introduction.]
  - go-kure/launcher#922 (the object kind policy, `oam.ObjectKindPolicy`) [The
    reference cited it after "build through the object kind policy" in its
    Validation section.]
  - go-kure/launcher#883 (the generated required lists, refusing an omitted
    CRD-required field the Go type writes empty) [The reference cited it after
    "**These lists are generated**" in its Properties section.]
- **Tests:**
  - `TestMonitoringKinds_DefaultedZeros` holds each kind's list of such fields
    to the numbers, booleans and strings that are omitted when zero and
    defaulted to something else, from the default markers of the linked module's
    source. ["Such fields" are the fields the type omits when zero and the API
    defaults to something else, which the reference page names, and the test is
    in `monitoring_kinds_internal_test.go`.]
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` derives these fields
    for every kind in its table, from the CRD the linked module ships or, where
    it ships none, from the markers of its source. It holds each to a refusal,
    shown on a document, or to a stated reason. The same test derives the fields
    the API defaults and the type writes unauthored, where the default would
    never apply, and holds each to one of three answers: refused, filled by the
    kind with the API's default, or harmless with the reason. ["These fields"
    are the required fields the type leaves out when empty, which the reference
    page names, "its table" is the table of kind components in that test, and
    the test is in `kind_api_sets_internal_test.go`.]
  - The table holds the kind components that decode their properties into an
    upstream type, but for two groups. The `httproute` kind is not in it: the
    experimental-channel CRD requires the `protocol` of an `externalAuth`
    filter, of a rule and of a backend reference, the type leaves an empty one
    out, and the kind does not refuse it. The Flux kinds (`helmrelease`,
    `fluxcd-kustomization`, `helmrepository`, `ocirepository`, `gitrepository`,
    `bucket`, `helmchart`) are not in it either: no row is written for them.
    ["The table" is the table of kind components in the test of the previous
    entry.]
  - `TestKindComponents_GeneratedRequiredBothWays` holds each field both ways on
    the kind, and runs a path of every type that holds one through the
    operator's CRD, vendored from its repository at the linked release (the
    module ships none). ["Each field" is a field the generated lists in the
    reference name, and the test is in `kind_required_internal_test.go`.]
