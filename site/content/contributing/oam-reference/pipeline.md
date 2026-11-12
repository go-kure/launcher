---
title: "Pipeline"
weight: 27
---

Implementation, history and tests of the section on the reference page
[Pipeline](../../api-reference/oam/pipeline.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Pipeline

- **Implementation:**
  - Since go-kure/launcher#361 the absence of other labels is a
    property of the object as constructed rather than a scrub: kure's release-1 builder
    contract (`go-kure/kure` ≥ `v0.2.0-beta.11`) makes `CreateNetworkPolicy` return TypeMeta
    and identity only, so `netpol_synthesis.go`'s `np.Labels = nil` / `np.Annotations = nil`
    lines no longer strip a constructor-stamped `app:` label and annotation — they are kept
    as a guard, not as a fix-up. Everything else on the policy (`spec.podSelector`,
    `spec.policyTypes`, the rules) is written by the synthesizer as a direct field
    assignment; nothing upstream of it supplies a default. ["the object as constructed" is
    the synthesized `NetworkPolicy` of the reference's paragraph on a policy built with no
    labels and no annotations; "Everything else" is the spec of the policy, which the
    constructor leaves out.]
  - **Same resolution as the synthesis, not a copy.** The Service owners come from the lookups the
    NetworkPolicy synthesis builds, and a port is matched by asking the owner's
    `ServiceRoutingTarget` once per port. [This item came last in the reference's list on
    `Transformer.RoutedBackends`, after the item on ports; "the synthesis" is the
    NetworkPolicy synthesis of the Phase-4 post-build stage.]
- **History:**
  - go-kure/launcher#783 (the transform orders components only where the document declares an
    order; before it, the root node of an ordered application was named `<bundle>`).
  - go-kure/kure#979 (the root node's directory renders no bundle, the application bundle has
    a directory of its own inside it, and kure's bootstrap points Flux at
    `layout.TopDirectory`).
  - go-kure/kure#972 (`Bundle.DirName` gives that directory another name).
  - go-kure/kure#1030 and go-kure/kure#1036 (kure shortens a default Kustomization name over
    63 characters by its own rule, with the first 8 hexadecimal characters of the SHA-256 of
    the default).
  - go-kure/kure#1032 (kure's Flux integration refuses a name set on a layout, and a field set
    on a hook-group child, under any placement but `FluxIntegratedPerLayout`).
  - go-kure/launcher#941 (under `FluxIntegratedPerLayout` the Kustomization of a default
    hook-group child regains the leading `<unit name>-`, and a Kustomization name launcher
    shortened becomes the one kure shortens).
  - go-kure/launcher#787 (the leading `<unit name>-` on the Kustomization of a default
    hook-group child, which go-kure/launcher#941 restored).
  - go-kure/launcher#781 (launcher sets no Flux delivery field on any bundle it returns).
  - go-kure/kure#1016 (under `FluxIntegratedPerLayout` a bundle's `Wait`, `Timeout`,
    `RetryInterval`, `Labels` and `Annotations` reach the Kustomizations of the layouts of the
    components it holds).
  - go-kure/kure#1021 (the same for `Interval`, `Prune`, `Force`, `Suspend`, `PostBuild` and
    `Patches`).
  - go-kure/kure#1022 (a bundle with child bundles that sets `Wait` no longer carries a
    `spec.healthChecks` entry per child).
  - go-kure/kure#1020 (kure's layout writers refuse a layout that holds an object written
    without a `kind`, or without an `apiVersion`).
  - go-kure/kure#1017 (kure reads the resources a layout holds as kustomize builds them, a
    List standing for its items, by one rule in its pre-write checks and in its Flux
    integration).
  - go-kure/launcher#1338 (an object whose `config.kubernetes.io/local-config` annotation is
    present with any value but `"false"` generates nothing and collides with nothing).
  - go-kure/launcher#782 (the `prune-protection` and `force-replace` traits set
    `stack.Application.Delivery`, and launcher writes no Flux annotation for either).
  - go-kure/launcher#712 (a component's application is followed by the sub-applications its
    traits created, in creation order).
  - go-kure/launcher#718 (a trait handler that moves an application or removes a
    sub-application keeps the order it left).
  - go-kure/launcher#734 (a trait handler that replaces, removes or renames a component's
    application fails the transform).
  - go-kure/launcher#740 (under `TransformContext.FluxNamespace` a trait sub-application of a
    component follows its Flux objects only when the Flux object reads it by name from its own
    namespace).
  - go-kure/launcher#1012 (an `oci` component's source reads the Secret of a trait on its
    Kustomization).
  - go-kure/launcher#1016 (the `oci` rule's shared source reads the Secret one consumer's
    trait makes).
  - go-kure/launcher#988 (`storageNamespace` is defaulted to the same namespace as
    `targetNamespace`).
  - go-kure/launcher#1039, go-kure/launcher#1079 and go-kure/launcher#1086
    (`TransformContext.GroupsWaitForFluxObjects`; unset, launcher assumes the default delivery
    and refuses nothing).
  - go-kure/launcher#1088 (the transform refuses a source that a Flux object of a group
    cannot become Ready without, when a component of a later group makes it).
  - go-kure/launcher#788 (launcher puts the component label on every object a component
    owns and on its pod templates itself; it is the one label a synthesized `NetworkPolicy`
    gets).
  - go-kure/launcher#572 (the `app` label and the `app` selectors the built-in components and
    traits generate use `ComponentLabelValue`).
  - go-kure/launcher#790 (a `service` of type `ExternalName` selects no pods, so a route to it
    synthesizes no policy).
  - go-kure/launcher#1157 (a routing trait's ports for the Service of the component it runs
    on belong to the component that owns that Service name).
  - go-kure/launcher#396 (no synthesized policy shares a selector with its inputs or with
    another policy).
  - go-kure/launcher#1133 (`Transformer.RoutedBackends`).
