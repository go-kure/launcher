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
