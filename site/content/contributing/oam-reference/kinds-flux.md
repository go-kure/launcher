---
title: "Kinds: Flux"
weight: 7
---

Implementation, history and tests of the entries on the reference page
[Kinds: Flux](../../api-reference/oam-components/kinds-flux.md). Every test
named here is in `pkg/oam/builtin/components/flux_kinds_internal_test.go`
unless a line says another file.

## fluxcd-alert, fluxcd-provider, fluxcd-receiver, imagepolicy, imagerepository, imageupdateautomation, artifactgenerator, resourcesetinputprovider

- **Implementation:** Each is built on `policyFreeKind` (see the **storageclass**
  entry) with the Flux namespace, the check of its durations and, where a field
  names a host the environment policy holds or holds a credential, a check under
  that policy (`fluxKind`), and emits that one object. ("the **storageclass**
  entry" is on the reference page
  [Kinds: core workloads](../../api-reference/oam-components/kinds-core-workloads.md).)
- **History:**
  - go-kure/launcher#790 (the kind-named projections).
  - go-kure/launcher#601 (a duration is held to the pattern its field declares).
  - go-kure/launcher#619 (a `timeout` of an hour or more is written in minutes,
    as the timeout of a Flux source is).
  - go-kure/launcher#984 (a Receiver resource's `apiVersion` is checked at build
    time).
  - go-kure/launcher#1010 (the version the refusal names for a kind outside the
    Receiver API's `kind` enum).
  - go-kure/launcher#883 (the Required lists are generated from the markers of
    the Go source, and the CRDs are vendored for the tests).
  - go-kure/launcher#874 (a kind checks an expression rule only where the check
    is held to the API server's own validator, which answers from a CRD).
  - go-kure/launcher#975 (a `pagerduty` Provider's routing key in `channel`).
  - go-kure/launcher#1079 (a trait in a later group that makes a required read is
    refused).
- **Tests:**
  - `TestFluxKinds_DefaultedZeros` holds each kind's list of such fields to the
    numbers, booleans and strings that are omitted when zero and that the API
    defaults to something else ("such fields" are the reference's fields that the
    type omits when empty and the API defaults, as `eventSeverity` and
    `filter.limit`).
  - `TestFluxKinds_MarkersClassified` fails on a field the type writes unauthored
    that neither a marker nor that rule classifies ("that rule" is the
    reference's "schema generators' rule" for a field with no marker whose json
    tag keeps it when empty).
  - A dependency bump that adds, drops or moves one fails there ("there" is
    `TestFluxKinds_MarkersClassified`, and "one" is a field the type writes
    unauthored that a marker or that rule classifies).
  - `TestKindComponents_GeneratedRequiredBothWays` (the test the reference's
    "the list of a `resourcesetinputprovider` is derived from its CRD instead, and
    held to that CRD by the API server" names; it is in
    `kind_required_internal_test.go`).
  - The other kinds here are held to the API server's own validator on the
    controllers' CRDs, vendored from each controller's repository at the tag of
    the linked API module, unmodified but for `allow-term` pragma comment lines
    (see "Vendored CRDs" under the required fields)
    (`testdata/upstream/<repository>/`, `scripts/vendor-flux-crds.sh`, each file
    checked against its git blob id by `TestVendoredCRDs_MatchSource`): of every
    type that holds a generated path, the same test creates the object with one
    such path authored, which the API server accepts, and without it, which the
    API server refuses as missing a required field; and every generated path is
    one the CRD declares ("The other kinds here" are the kinds of the reference
    entry other than `resourcesetinputprovider`; "the required fields" are the
    reference's [Required fields a kind writes unauthored and does not refuse](../../api-reference/oam-components/conventions.md#required-fields-a-kind-writes-unauthored-and-does-not-refuse);
    "the same test" is `TestVendoredCRDs_MatchSource`, which is in
    `vendored_crds_internal_test.go`).
  - `TestFluxKinds_ExpressionRules` reads every such rule from the markers of the
    linked source and holds the list (`fluxRulesLeft`) to them, and
    `TestFluxOperatorKinds_ExpressionRulesMatchCRD` holds the
    ResourceSetInputProvider's list to the `x-kubernetes-validations` of the
    shipped CRD, in both directions, so a dependency bump that adds, drops or
    rewords one fails there ("such rule", "the list" and "one" are the API
    expression rules the reference lists under "The APIs' expression rules are
    not checked"; "the ResourceSetInputProvider's list" is its seventeen rules).
  - `TestProviderAddressCredentialTypes_InUpstreamEnum` holds the list to the
    API's values of `type` ("the list" is the reference's list of Provider types
    whose `address` is the credential, or can be; the test is in
    `fluxcd_provider_internal_test.go`).

## fluxcd-kustomization

- **History:**
  - go-kure/launcher#784 (the kind-named terminal for Flux's `Kustomization`).
  - go-kure/launcher#352 (the type is `fluxcd-kustomization` rather than
    `kustomization`).
  - go-kure/launcher#883 (the Required list of the fields the type writes
    unauthored is generated from the markers of the linked kustomize-controller
    module).
  - go-kure/launcher#740 (`FluxNamespaceReads`: the ConfigMaps and Secrets it
    reads from its own namespace).
  - go-kure/launcher#1079 (a trait in a later group that makes a required read is
    refused).
- **Tests:**
  - a test ties the published schema to that struct, so a bump that adds or drops
    a spec field fails the suite until the schema follows ("that struct" is
    `KustomizationSpec`, of the reference's "Its properties are exactly the
    top-level JSON keys of `KustomizationSpec`"; the sentence names no test).

## helmrepository, ocirepository, gitrepository, bucket, helmchart

- **History:**
  - go-kure/launcher#347 (the kind-named terminals for Flux's four fetching
    source kinds, part of the Helm-family redesign go-kure/launcher#336).
  - go-kure/launcher#351 (`helmchart`, the kind that builds a chart artifact from
    one of them).
  - go-kure/launcher#1078 (a `url` must not carry a credential).
  - go-kure/launcher#601 (an `interval` is checked as on `helmrelease`).
  - go-kure/launcher#606 (the `timeout` pattern of the source CRDs has no `h`
    unit).
  - go-kure/launcher#619 (a `timeout` of an hour or more is emitted with its
    hours folded into minutes).
  - go-kure/launcher#790 (an authored `verify` that names no `provider` is
    written with `provider: cosign`, and an authored `provider: ""` is refused).
  - go-kure/launcher#883 (every other required field the type writes unauthored
    is refused, from a list generated from the markers of the linked
    source-controller module).
  - go-kure/launcher#699 (no refusal prints the value it refused).
  - go-kure/launcher#740 (`FluxNamespaceReads`: the Secrets and the ConfigMap a
    reference names move with the source).
  - go-kure/launcher#1079 (a trait in a later group that makes a required read is
    refused).
- **Tests:**
  - a test ties each published schema to its struct, so a bump that adds or drops
    a spec field fails the suite until the schema follows ("its struct" is the
    source-controller spec type of the reference's "Each component's properties
    are exactly the top-level JSON keys of its source-controller spec type"; the
    sentence names no test).
  - A reflection test asserts, per kind, that no spec field is unreachable through
    that decode, against an explicit exclusion list that is empty ("that decode"
    is the strict decode of the reference's "Decoding").
