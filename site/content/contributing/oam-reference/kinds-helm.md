---
title: "Kinds: Helm"
weight: 15
---

Implementation, history and tests of the entries on the reference page
[Kinds: Helm](../../api-reference/oam-components/kinds-helm.md). Every test
named here is in `pkg/oam/builtin/components/` unless a line says another
file.

## helm

- **Implementation:** `helm.go`.
- **History:**
  - go-kure/launcher#349 (the role-named `helm` component).
  - go-kure/launcher#336 (a HelmRepository, GitRepository or Bucket source
    becomes `chart.spec.sourceRef`).
  - go-kure/launcher#702 (`valuesMode: configMap`).
  - go-kure/launcher#793 (the one shortening rule, `oam.ShortenNameWithSuffix`,
    for the values ConfigMap name).
  - go-kure/launcher#741 (the `configmap` trait builds its ConfigMap through the
    `configmap` kind's own code).
  - go-kure/launcher#757 (known gap: an authored `configmap` trait on the same
    component that takes the same name is not refused).
  - go-kure/launcher#787 (`valuesConfigMapName`, `valuesSecretName`,
    `helmReleaseName`, `source.name` beside an inline source,
    `hookGroupNamePrefix` and `layoutKustomizationName`, and the **Breaking**
    note on `source.name` with `source.url`).
  - go-kure/launcher#1080 (a HelmRelease name, the default included, is held to
    63 characters with a HelmRepository, GitRepository or Bucket source).
  - go-kure/launcher#786 (`secretValues`).
  - go-kure/launcher#794, item 9 (a key named `global` below the top level of
    `secretValues` is refused).
  - go-kure/launcher#794, item 11 (`scopeOverrides`).
  - go-kure/launcher#1064 (`source.digest` pins an inline `oci://` chart to a
    manifest digest).
  - go-kure/launcher#783 (the generated source is applied with the application
    bundle, ahead of every group).
  - go-kure/launcher#785 (an authored `releaseName`, with the `helmtemplate`
    defaulting an unset one to the component name).

## helmchart

- **History:**
  - go-kure/launcher#351 (the kind-named terminal for Flux's `HelmChart`).
  - go-kure/launcher#665 (rows 3 and 16 of the migration table: no shared
    OCIRepository between `oci` and Helm-over-OCI components, and the
    `layerSelector` of a generated OCIRepository).
  - go-kure/launcher#625 (row 5 of the migration table: `targetNamespace` under
    a Flux namespace defaults to the application namespace).
  - go-kure/launcher#781 (row 8 of the migration table: launcher sets no
    automatic health check on generated sources).
  - go-kure/launcher#776 and go-kure/launcher#785 (row 11 of the migration
    table: template delivery refuses `targetNamespace`, and an unset
    `releaseName` renders under the component name).
  - go-kure/launcher#788 (row 19 of the migration table: the component label and
    a post-renderer on the HelmRelease).
  - go-kure/launcher#785 (row 20 of the migration table: every HelmRelease
    carries `spec.releaseName`).
- **Tests:**
  - `TestHelmParity` (`pkg/cmd/kurel`) pins it on three fixture pairs: the last
    `helmchart` output of each input, frozen before the removal, is diffed
    against the `helm` build, and the diffs are in
    `pkg/cmd/kurel/testdata/helm-parity/`. ("it" is the reference's "The output
    then changes only as this table says"; the test is in
    `pkg/cmd/kurel/helm_parity_test.go`.)

## helmrelease

- **Implementation:** It lives in the internal `pkg/oam/internal/fluxduration`.
  ("It" is the duration check described in the reference's **Defaults and
  checks** paragraph.)
- **History:**
  - go-kure/launcher#327 (the kind-named terminal for Flux's `HelmRelease`, part
    of the Helm-family redesign go-kure/launcher#336).
  - go-kure/launcher#702 (the launcher-owned `valuesMode: configMap` is refused
    like any other unknown key).
  - go-kure/launcher#590 and go-kure/launcher#601 (a set `interval` must be a
    duration Flux accepts).
  - go-kure/launcher#606 (the other duration fields are checked the same way, in
    the same form).
  - go-kure/launcher#790 (`chart.spec.sourceRef.kind` is required under an
    authored `chart` of kind HelmChart, and the published schema lists `chart`
    and `chartRef` as optional and says so in the handler's `exclusive` group).
  - go-kure/launcher#748 (`kind` of a `valuesFrom` entry is `Secret` or
    `ConfigMap`).
  - go-kure/launcher#762 (a set `targetPath` is at most 250 characters and
    matches the CRD's pattern).
  - go-kure/launcher#883 (every other required field the type writes unauthored
    is refused).
  - go-kure/launcher#1072 (the chart template of kind OCIRepository).
  - go-kure/launcher#1074 (the generated HelmChart).
  - go-kure/launcher#1080 (the name of the generated chart source).
  - go-kure/launcher#785 (the release name, and the breaking output change).
  - go-kure/launcher#988 (`spec.storageNamespace`, and the breaking output
    change).
  - go-kure/launcher#794, item 1 (the Kustomization `oci` emits has no
    `targetNamespace` default).
  - go-kure/launcher#740 (the ConfigMaps and Secrets the HelmRelease reads from
    its own namespace move with it).
  - go-kure/launcher#1079, go-kure/launcher#1086 and go-kure/launcher#1088 (a
    trait or a component of a later group that makes a required read, where the
    release lands, or the source of the `chartRef` or chart template, is
    refused).
- **Tests:**
  - Its properties are exactly the top-level JSON keys of `HelmReleaseSpec` in
    the helm-controller API version `go.mod` links; a test ties the published
    schema to that struct, so a helm-controller bump that adds or drops a spec
    field fails the suite until the schema follows. (The reference keeps this
    sentence. "Its" is the `helmrelease` kind's, "that struct" is
    `HelmReleaseSpec`, and the test is `TestHelmReleaseHandler_SchemaMatchesSpec`
    in `helmrelease_test.go`.)
  - `TestKindComponents_OmittedRequiredAndWrittenDefaults` holds both to the
    markers in the source of the linked Flux modules, which ship no CRD: the
    required field to a refusal shown on a document, and the written value to
    the default the marker states, so an upstream change of that default fails
    the test. (The reference keeps this sentence. "both" are the two sets the
    test derives for the `helmrelease` kind: the required
    `chart.spec.sourceRef.kind`, and the written default of
    `chart.spec.verify.provider`. The test is in
    `kind_api_sets_internal_test.go`.)

## helmtemplate

- **History:**
  - go-kure/launcher#348 (the kind-named terminal for a client-side Helm render,
    part of the Helm-family redesign go-kure/launcher#336).
  - go-kure/launcher#787 (`hookGroupNamePrefix` and `layoutKustomizationName`,
    and the `Naming` hook's answers for the `hook-group` and `layout` roles).
  - go-kure/launcher#941 (the default hook-group prefix names only the layouts
    and kure names each Kustomization; the layout's Kustomization name is left
    to kure).
  - go-kure/launcher#786 (`secretValues`).
  - go-kure/launcher#794, item 4 (a namespaced rendered object with no
    `metadata.namespace` is given the application namespace).
  - go-kure/launcher#794, item 11 (`scopeOverrides`).
  - go-kure/launcher#794, item 7 (undeclared fields of a registered kind).
  - go-kure/launcher#794, item 6 (the host of the chart archive a Helm
    repository's index points at is not checked, a decided limit).
  - go-kure/launcher#790 (`Lease`, `EndpointSlice`, `PriorityClass`,
    `RuntimeClass`, `APIService`, the `admissionregistration.k8s.io/v1` kinds
    and the image-reflector kinds are read as their Go types, and a list of a
    kind the scheme does not register is refused when a `helm.sh/hook`
    annotation is involved).
  - go-kure/kure#1014 (a list is read in one place, kure's parser).
  - go-kure/launcher#791 (the render is decoded with kure's manifest parser, and
    `ApplyPolicy` holds the chart to the environment policy).
  - go-kure/launcher#792 (the hook-group child name begins with the application
    name).
  - go-kure/kure#1030 and go-kure/kure#1036 (kure shortens the Kustomization
    name past 63 characters by its own rule).
  - go-kure/kure#1032 (kure's Flux integration refuses a hook-group child's
    `DependsOn` and `KustomizationName` under `FluxSeparate` and
    `FluxIntegratedPerBundle`, and a layout's name under any placement but
    per-layout).
  - go-kure/launcher#563 (an `AppFileSingle` layout writes no
    `kustomization.yaml`, so nothing would list its child layouts).
- **Tests:**
  - A test pins both, each way round, on the process's standard log and its
    default `slog` logger. ("both" are the two cases of the sentence before it:
    a top-level `global`, where the dropped value is the dependency's `global`
    entry from `values`, and a dependency's ordinary key, where it is the
    dependency's default; the test is
    `TestHelmSecretValues_AllowedShapesPrintNoValue` in
    `pkg/cmd/kurel/helm_secret_values_test.go`.)
  - A test holds the two components to the same answer for the same objects and
    overrides. ("the two components" are `helmtemplate` and `manifests`; the
    test is `TestScopeOverrides_ManifestsAndTemplateDeliveryResolveAlike` in
    `helmtemplate_scope_overrides_internal_test.go`.)
  - No type under a workload or a claim does this beyond scalar leaves
    (`Quantity`, `IntOrString`, `Time`) and the raw field set of `managedFields`;
    a test walks those types and fails when one starts to. (The reference keeps
    this sentence. "this" is decoding its own keys, as the known limit above
    states it, "those types" are the types under a workload or a claim, and "one"
    is a type that unmarshals itself beyond the scalar leaves and the field set
    of `managedFields`; the test is
    `TestUndeclaredFields_NoSelfUnmarshallingStructUnderAWorkload` in
    `undeclared_fields_internal_test.go`.)
  - The schema declares the same keys, with `source` closed to `url` and `kind`,
    and a test ties it to the struct. (The reference keeps this sentence. "it"
    is the schema and "the struct" is `helmTemplateProperties`; the test is
    `TestHelmTemplateHandler_SchemaMatchesProperties` in
    `helmtemplate_internal_test.go`.)

## oci

- **History:**
  - go-kure/launcher#784 (`oci` is lowered by `OCIRule` to an `ocirepository`
    and a `fluxcd-kustomization`).
  - go-kure/launcher#999 (the source's registry access: `source.secretRef`,
    `source.provider`, `source.insecure`, `source.certSecretRef` and
    `source.proxySecretRef`).
  - go-kure/launcher#1009 (`source.serviceAccountName`).
  - go-kure/launcher#1012 (the Secret a trait makes moves to the Flux namespace
    with the source the component keeps to itself).
  - go-kure/launcher#1016 (the same for a source generated on its own, which
    every consumer is ordered after).
  - go-kure/launcher#1039 (the transform refuses a Secret-making trait on a
    consumer in a later group under
    `TransformContext.GroupsWaitForFluxObjects`).
  - go-kure/launcher#1079 (any Flux object that requires a ConfigMap or Secret a
    trait makes in a later group).
  - go-kure/launcher#1086 (a component of a later group that makes a ConfigMap
    or Secret where it lands with the source).
  - go-kure/launcher#787 (the `Naming` hook's `helm-source` role, `source.name`,
    `kustomizationName` and `source.objectName`).
  - go-kure/launcher#622 (`targetNamespace` has no default).
  - go-kure/launcher#432 (`wait` and `healthChecks`).
  - go-kure/launcher#580 (under a non-empty registry allowlist `source.url` must
    name its registry explicitly).
  - go-kure/launcher#590 (`interval` is checked exactly as for `helmrelease`).
