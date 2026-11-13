---
title: "Names and overrides"
weight: 21
---

Implementation, history and tests of the sections on the reference page
[Names and overrides](../../api-reference/oam/names-and-overrides.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Names and overrides

- **History:**
  - go-kure/launcher#793 (launcher shortens a name only when it generated that name itself,
    and always by one rule, `ShortenName(name, limit)`).
  - go-kure/launcher#787 (a name that cannot be the name of its object fails the transform
    with the property in the error, so the cluster never has to refuse it).

## Name roles and the `Naming` hook

- **History:**
  - go-kure/launcher#787 (a consumer changes a name launcher generates through
    `TransformContext.Naming`, a `func(NameRequest) (string, bool)`).
  - go-kure/kure#1032 (kure's Flux integration refuses a name set on a layout under any
    placement but `FluxIntegratedPerLayout`).
  - go-kure/launcher#787 (the over-long name of a hook-group child fails the transform).
  - go-kure/launcher#1044 (`GeneratedName` is the name an object takes where no name is
    authored and no hook is set, so the `postgresql` rule matches another component's
    inline recovery store by it).
  - go-kure/launcher#1001 (`postgresql`'s archive check, a rule that reads another
    component of `Document` and needs the name that component's object takes).
  - go-kure/launcher#787 (a hook that names two synthesized policies apart and not their
    sub-applications leaves two applications of one name in the bundle, which is refused).
  - go-kure/launcher#787 (two sub-applications of one name in one bundle are refused: a
    behaviour change).
  - The `ingress` trait's Ingress and the `httproute` trait's HTTPRoute used to be claimed
    the same way; they are resolved under their roles now (`ingress`, `httproute`). ["the same
    way" is with
    `(*Trait).ClaimObjectName`, the claim of the sentence before it on the reference page.]

## Outputs: the names a component's objects get

- **Tests:**
  - A test pins this
    (`TestValidateAuthoredProperties_NeverWritesThroughAPointerOrStruct`). ["this" is the
    rule of the sentence before it on the reference page: validation's own code only reads
    through a pointer or a struct and never writes through either, for a value under an open
    object.]

## `labels` and `annotations`: the metadata of a kind component's object

- **History:**
  - go-kure/launcher#790 (a kind component takes two more properties the engine reads,
    `labels` and `annotations`, each a map of strings).
  - go-kure/launcher#1178 (the pod template's labels and annotations are a handler's own
    property, `podMetadata`, on `deployment`, `statefulset`, `daemonset`, `job` and
    `cronjob`).
  - go-kure/launcher#1216 (`webservice` and `worker` take `podMetadata` and hand it to their
    deployment member).
