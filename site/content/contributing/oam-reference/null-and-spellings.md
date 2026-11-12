---
title: "The null contract and two spellings of one field"
weight: 20
---

Implementation, history and tests of the sections on the reference page
[The null contract and two spellings of one field](../../api-reference/oam-components/null-and-spellings.md).
Text in square brackets is added here to say what a moved sentence refers to.

## The null contract

- **Implementation:**
  - `authoredValue` — the presence primitive `parseStringField`, `parseObjectField`, `parseInt32Field`,
    `parseInt64Field`, `parseObjectList` and `parseStringList` all go through — does
    exactly that, so calling any of them directly already complies. They classify a
    **typed** nil as null too — `map[string]any(nil)` or `[]any(nil)` inside an `any`,
    which is what a lowering rule assembled in Go produces for an unset optional and is
    not `== nil`. ["that" is the reference's "read a null under an optional property as
    omission"; "They" are the six helpers named in this sentence.]
  - A new optional field is read with the helper for its type instead: `parseStringField`
    (or `requiredStringField` when the field is required), `parseBoolField`,
    `parseInt32Field`, `parseInt64Field`, `parseObjectField`, `parseObjectList` or
    `parseStringList`. Each already reads an explicit null as absence through
    `authoredValue`, so a wrapper would only repeat that check. ["instead" is instead of
    an `optional*` wrapper; "a wrapper" is one of those, listed under History.]
- **History:**
  - It is written down because "null" has several readers — the emission validator's
    requiredness check and its optional-key strip, its array-element guard,
    `enforcePlatformReserved`, this package's parsers, and the Kubernetes API itself —
    and each was aligned separately, so each round of review found the reader the last
    round had not touched. ["It" is the reference's rule on the null contract.]
  - This used to require a separate `optional*` wrapper family (`optionalString`,
    `optionalObject`, `optionalInt32`, `optionalInt64`, `optionalObjectList`,
    `optionalStringList`) layered in front of the plain parsers. `go-kure/launcher#394`
    moved the handling into the shared helpers themselves, which made every wrapper an
    exact no-op forward; they were removed and every caller now uses the `parse*` helper
    directly.
  - `go-kure/launcher#423`, `go-kure/launcher#394` and `go-kure/launcher#570`
    converted the parsers they covered; the last of them are listed with the `null`
    row of the `deployment` table above. ["the parsers they covered" are the parsers that
    answer presence with a bare `v, ok := props[key]`, the first limit of the reference; "the
    `deployment` table above" is on the reference page of the `deployment` kind.]
  - Both limits met on one key, and it was the second row of that table.
    `namespaceSelector` is parsed twice in this repository: here, for pod affinity,
    through `parseSchedulingSelector` (`parseObjectField`), where any null is
    omission — which upstream reads as "this pod's namespace"; and in the
    networkpolicy trait, through `parseNPPeer`. A *typed* nil used to satisfy the
    networkpolicy trait's own type assertion and yield an empty selector — all
    namespaces, the widest possible answer — while an untyped nil already read as
    omission there, so one input landed on opposite ends of the range depending on
    which Go nil shape produced it. Both parsers now route their presence check
    through `oam.IsNullValue`'s reflect-based classification, which treats a typed
    nil the same as an untyped one, so the two halves converge: a null
    `namespaceSelector`, however it was constructed, is omission on both paths.
    ["Both limits" are the two limits the reference lists; "that table" is its
    `labelSelector` and `namespaceSelector` table; "here" is the handler parsers.]
  - The same typed-nil shape reached **list elements**. `parseObjectList` read a
    null key as absence, but a typed-nil *element* (`[]any{map[string]any(nil)}`)
    passed its per-element assertion as an empty object, where an untyped null
    element is refused (`tolerations[0]: must be an object, got <nil>`). Most
    callers then failed on a missing required field anyway. `tolerations` did not:
    an empty entry became an `Exists` toleration with no key, which tolerates
    **every** taint. The element is now normalised to a null first, so every caller
    refuses both shapes identically (go-kure/launcher#465). The list readers that
    do not go through `parseObjectList` (`envFrom`, `httpHeaders`, `volumes`,
    `volumeMounts`, sidecar `ports`, the Job `successPolicy`/`podFailurePolicy` rules and
    `onPodConditions`, `volumeClaimTemplates`, `valuesFrom`, `scopeOverrides`) pass each
    element through `nullElem` first, for the same untyped refusal and message.
    ["The same typed-nil shape" is the one of the entry above.]
- **Tests:**
  - `TestNoOptionalFieldWrappers` (`no_optional_wrappers_internal_test.go`) keeps them
    removed: it fails on any function or method whose name starts with `optional`. It
    parses every production `.go` file directly in this package's directory — every
    file, not only `common.go`, and never a `_test.go` file or a subdirectory — and
    fails if it parsed none, so a wrong working directory cannot pass it vacuously.
    ["them" are the `optional*` wrappers of the History entry above.]
  - Fixed as `go-kure/launcher#430`; `networkpolicy_internal_test.go`'s
    `TestParseNPPeer_NamespaceSelectorPresenceCases` pins the typed-nil case
    directly. ["Fixed" is the `namespaceSelector` split of the History entry that starts
    "Both limits".]

## Two spellings of one field

- **Implementation:** `foldedKey` [the helper behind the reference's sentence on the
  `helmrelease` verify check that still refuses a pair in which one spelling is null,
  which the decode lets through].
- **History:** Before, those documents built. ["those documents" are the ones a kind
  that decodes by itself builds, listed in the reference's `gitrepository` and
  `helmrelease` sentence.]
