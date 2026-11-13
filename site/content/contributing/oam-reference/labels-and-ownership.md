---
title: "Component label and ownership"
weight: 28
---

Implementation, history and tests of the sections on the reference page
[Component label and ownership](../../api-reference/oam/labels-and-ownership.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Component label and ownership

- **History:**
  - go-kure/launcher#788 (every application of a transformed document belongs to one authored
    component, or to the document as a whole).
  - go-kure/launcher#783 (a generated source the application bundle holds, which every
    consumer shares, belongs to none: the document as a whole).
  - go-kure/launcher#790 (the label is authoritative: before it writes the label, the wrapper
    holds every object the application generates to it).
  - go-kure/launcher#1085 (the component label and reserved-key checks read a typed object in
    its JSON form with numbers kept exact, so a `HelmRelease` whose `values` hold a number no
    float64 holds is not refused for it).
  - go-kure/launcher#944 (the `StatefulSet` volume claim templates,
    `spec.volumeClaimTemplates[].metadata`, are not read for the component label).
  - go-kure/launcher#957 (the claim template of the storage of a `Prometheus`,
    `PrometheusAgent`, `Alertmanager` or `ThanosRuler`,
    `spec.storage.volumeClaimTemplate.metadata`, is not read for the component label).
  - go-kure/launcher#788 (breaking library changes: every owned object and pod template gains
    the component label, a `HelmRelease` gains the post-renderer, and a component's
    synthesized NetworkPolicies gain the label; a `Job` a cluster holds from before cannot take
    the label on its pod template; `GeneratedApplication.Component` changes; and
    `stack.Application.Config` is the ownership wrapper after `Transform`).
  - go-kure/launcher#790 (the authoritative label, breaking library changes: a document that
    built before is refused at generation when an object it generates holds the component
    label's key with a value that is not its component's; under `ComponentLabelKey: "app"`
    what the built-in kinds and traits write still builds; and the reserved metadata keys are
    read in five more places, and in the metadata an operator copies onto objects it creates
    that are no pods, and in `spec.commonMetadata` of a Flux `Kustomization`, `HelmRelease`
    or `ResourceSet`).
  - go-kure/launcher#790 (a `CronJob`'s job template, breaking library changes: every `CronJob`
    an owned application generates gains the component label on its job template, and a job
    template that holds a foreign value or a reserved metadata key is refused; new exported
    API `ReservedKeyInJobTemplate`).
  - go-kure/launcher#790 (a selector that rules the label out, breaking library changes: an
    owned workload whose own selector rules the component label out of a pod template that
    carries no value for the key is refused, and so is one whose selector the check cannot
    read; new exported API `ComponentLabelSelectorRulesOut`).
  - go-kure/launcher#790 (a renamed entry under the key `app`, breaking library changes: with
    `ComponentLabelKey: "app"` the synthesized inbound and egress NetworkPolicies of an entry
    a lowering rule emitted under a name whose label value differs from its component's select
    `app In [<component>, <entry>]`).
  - go-kure/launcher#790 (the authoritative label, new exported API: the sentinel
    `ErrComponentLabelValue`, the error type `ComponentLabelError`, the type
    `ComponentLabelRefusal` with four of its values, five values of `ReservedKeyHolder`, and
    the field `ReservedMetadataKeyError.Path`).
  - go-kure/launcher#790 (metadata that reaches no pods, new exported API: nine values of
    `ReservedKeyHolder`).
  - go-kure/launcher#944 (a `StatefulSet`'s volume claim templates, breaking library changes:
    a volume claim template that holds a reserved metadata key as a label or an annotation is
    refused at generation; new exported API `ReservedKeyInVolumeClaimTemplate`).
  - go-kure/launcher#957 (a Prometheus operator kind's storage claim template, breaking
    library changes: a storage claim template that holds a reserved metadata key is refused at
    generation, where neither `spec.storage.emptyDir` nor `spec.storage.ephemeral` is set; no
    new API).
- **Tests:**
  - A test fails when the code asserts or switches on a new type directly on
    an application's config (`app.Config.(T)`, `switch app.Config.(type)`) and the wrapper's
    list does not account for it. It reads syntax only: a config first copied to a variable or
    passed to a function, and asserted there, is outside what it sees. ["the wrapper" is
    launcher's ownership wrapper, and "list" is `configContracts` in
    `pkg/oam/component_ownership_test.go`, which names each contract the wrapper forwards and,
    for each one it does not, why not.]
  - `labelReachReservedOnly` in `pkg/cmd/kurel/label_reach_test.go` names each such field
    with what it reaches, and `labelReachNotRead` each field neither check reads.
    `TestLabelReach_EveryFieldIsHeldOrListed` derives these fields from the API types of the
    registered kind components, so a field a dependency adds fails the test until it is held or
    listed. ["such field" is a field of metadata that an operator copies onto other objects it
    creates, held to the reserved keys alone; "these fields" are the fields through which
    labels and annotations reach what the cluster creates from an object: those the two checks
    hold, and those the two names above list.]

## Reserved metadata keys

- **History:**
  - go-kure/launcher#790 (`TransformContext.ReservedMetadataKeys`: one rule holds every
    application the transform returns to the list of reserved keys, whatever wrote the key).
  - go-kure/launcher#944 (the metadata of each volume claim template of an `apps`
    `StatefulSet` is read for the reserved keys).
  - go-kure/launcher#957 (the metadata of the storage claim template of a `Prometheus`,
    `PrometheusAgent`, `Alertmanager` or `ThanosRuler` is read for the reserved keys, where
    neither `spec.storage.emptyDir` nor `spec.storage.ephemeral` is set).
  - go-kure/launcher#790 (library changes: the new exported field
    `TransformContext.ReservedMetadataKeys`, the sentinel `ErrReservedMetadataKey`, the error
    type `ReservedMetadataKeyError` with its `ReservedKeyHolder` values, and the method
    `PlatformAnnotations()` on the `ingress` trait's config; breaking for documents: an
    `expose` trait whose authored annotation contradicts a value the trait writes is refused).
- **Tests:**
  - `labelReachNotRead` in `pkg/cmd/kurel/label_reach_test.go` is
    the full list of fields of the kinds' API types that hand metadata on and are not read.
    ["not read" is not read by either check.]
