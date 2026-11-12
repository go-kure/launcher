---
title: "Parsing and type identity"
weight: 25
---

Implementation, history and tests of the sections on the reference page
[Parsing and type identity](../../api-reference/oam/parsing-and-types.md).
Text in square brackets is added here to say what a moved sentence refers to.

## Parsing

- **Implementation:**
  - The `scaler` entry is derived from the one table that also gives the scaler its HPA target kind (`Deployment`
    for the first three, `StatefulSet` for `statefulset`), so a type cannot be admitted without
    a target kind. The engine stamps the settled component type on the trait it applies
    (`Trait.ComponentType()`, empty outside a transform), which is where the scaler reads it.
    ["The `scaler` entry" is the `scaler` entry of `traitComponentRestrictions`, the registry
    the reference names; "the first three" are `webservice`, `worker` and `deployment`.]
- **History:**
  - go-kure/launcher#1179 (a `scaler` refuses authored `replicas`).
  - go-kure/launcher#783 (tiers are declared, never derived from a type).
- **Tests:**
  - `pkg/cmd/kurel`'s `TestBuiltinComponentHandlers_AcceptedByParser` is the guard: it
    parses a minimal document for every registered built-in type through
    `ParseWithExtraTypes`, the same entry point `kurel build` uses. ["the guard" is the guard
    of the reference's rule that a component type is validated against this package's own
    allowlist and not against the caller's handler registry; the reference's "One other
    per-type registry" sentence follows it there.]

## Component type allowlist

- **Tests:**
  - `pkg/cmd/kurel`'s `TestKindLists_Complete` fails on a type of the list with no row here and
    on a row of no type of the list, and `TestKindLists_InOrder` on a row out of place.
    ["the list" is `validComponentTypes`, "here" is the table of the reference's
    "Component type allowlist"; this sentence continued the reference's "A change that adds
    a type adds its row at its position", where the sentence now ends.]

## Type identity

- **History:**
  - go-kure/launcher#1258 (each handler, lowering rule and model declares its name and
    version when it registers; the registry is keyed by that pair).
