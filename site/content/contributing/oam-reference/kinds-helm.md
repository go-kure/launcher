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
