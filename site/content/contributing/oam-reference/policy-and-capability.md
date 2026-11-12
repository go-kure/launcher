---
title: "Policy defaults and capability system"
weight: 26
---

Implementation, history and tests of the sections on the reference page
[Policy defaults and capability system](../../api-reference/oam/policy-and-capability.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Policy defaults & enforcement

- **History:**
  - go-kure/launcher#752 (a trait's sub-application runs its `ApplyPolicy` after the trait's
    `Apply`, held to the `TraitHandler` rule).
  - go-kure/launcher#786 (`ExplicitSecretPolicy` and `AllowExplicitSecrets() bool`).
  - go-kure/launcher#790 (the `secret` kind component, one of the places the explicit-secret
    check reaches).
  - go-kure/launcher#794 (a core Secret that the `passthrough` or `manifests` component
    carries, another of them).
  - go-kure/launcher#922 (`ObjectKindPolicy`).

### Refusal classes

- **History:**
  - go-kure/launcher#849 (the `Class` of a `ViolationError`, so that a consumer does not match
    error text).

## Capability system

- **Implementation:**
  - `EvaluateProfile` and this method resolve the validators through the same
    unexported function, so they cannot disagree. ["this method" is
    `Transformer.CapabilityValidated`; "the validators" are the trait handler, trait
    lowering rule or `CapabilityDefinition` schema that the reference names before it.]

### Rendering merge: authored values and nulls

- **History:**
  - go-kure/launcher#742 (an authored `null` is absence in the merge of a rendering).
  - go-kure/launcher#750 (where both the authored value and the rendering hold an object, the
    merge recurses).
  - go-kure/launcher#765 (`ValidateAuthoredPropertiesWithCapabilities` checks nested `Required`
    on the merged properties).
  - go-kure/launcher#751 (each value a `ComponentCapabilityDefaults` fill supplies is
    validated against the component's own `PropertySchema`).
  - go-kure/launcher#772 (a fill is refused when no trait handler, lowering rule or
    `CapabilityDefinition` validates the rendering and the component has no schema).
  - go-kure/launcher#756 (both merges copy the rendering value and keep its Go type).
