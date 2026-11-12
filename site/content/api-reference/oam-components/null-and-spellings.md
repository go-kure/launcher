---
title: "The null contract and two spellings of one field"
weight: 15
---

## The null contract

Every table above answers "what does an explicit `null` mean here?" one field at a
time. This is the rule they are all instances of:

> A value that serializes to JSON null is absent, at every depth, on every path; a
> null is never a member of any `Items` type, so a null array element is a type
> error; reservation is about the KEY being written.

It is written down because "null" has several readers — the emission validator's
requiredness check and its optional-key strip, its array-element guard,
`enforcePlatformReserved`, this package's parsers, and the Kubernetes API itself —
and each was aligned separately, so each round of review found the reader the last
round had not touched.

What it means for a handler parser in this package: read a null under an optional
property as omission, never as a present value of the wrong type. `authoredValue` —
the presence primitive `parseStringField`, `parseObjectField`, `parseInt32Field`,
`parseInt64Field`, `parseObjectList` and `parseStringList` all go through — does
exactly that, so calling any of them directly already complies. They classify a
**typed** nil as null too — `map[string]any(nil)` or `[]any(nil)` inside an `any`,
which is what a lowering rule assembled in Go produces for an unset optional and is
not `== nil`.

This used to require a separate `optional*` wrapper family (`optionalString`,
`optionalObject`, `optionalInt32`, `optionalInt64`, `optionalObjectList`,
`optionalStringList`) layered in front of the plain parsers. `go-kure/launcher#394`
moved the handling into the shared helpers themselves, which made every wrapper an
exact no-op forward; they were removed and every caller now uses the `parse*` helper
directly.

`TestNoOptionalFieldWrappers` (`no_optional_wrappers_internal_test.go`) keeps them
removed: it fails on any function or method whose name starts with `optional`. It
parses every production `.go` file directly in this package's directory — every
file, not only `common.go`, and never a `_test.go` file or a subdirectory — and
fails if it parsed none, so a wrong working directory cannot pass it vacuously. A
new optional field is read with the helper for its type instead: `parseStringField`
(or `requiredStringField` when the field is required), `parseBoolField`,
`parseInt32Field`, `parseInt64Field`, `parseObjectField`, `parseObjectList` or
`parseStringList`. Each already reads an explicit null as absence through
`authoredValue`, so a wrapper would only repeat that check.

Two limits worth knowing before relying on the rule:

- **A parser that answers presence with a bare `v, ok := props[key]` does not comply
  on its own.** It reads a present null as present. Emission validation deletes such
  keys before a rule-emitted component is converted, so on that path those parsers
  agree with the contract *because of the strip*, not independently of it. The
  authored path has no top-level strip: `ValidateAuthoredProperties` checks shape and
  leaves a top-level null in place, so there the null reaches the parser. What it
  does next depends on how the parser consumes the lookup: one that type-checks the
  value refuses it as a wrong type, while one that discards a failed assertion treats
  it as absent without saying so. `go-kure/launcher#423`, `go-kure/launcher#394` and `go-kure/launcher#570`
  converted the parsers they covered; the last of them are listed with the `null`
  row of the `deployment` table above.
- **An empty object is not a null and is not absent.** `{}` and an absent key mean
  different things to Kubernetes wherever a `LabelSelector` is involved, so the
  contract must never be read as licence to collapse them. What each one *means* is
  per field, not a property of selectors in general — the two selectors in a single
  `corev1.PodAffinityTerm` disagree, in upstream's own words:

  | field | null | `{}` |
  |---|---|---|
  | `labelSelector` | "matches with no Pods" | matches every pod in scope |
  | `namespaceSelector` | "this pod's namespace" | "matches all namespaces" |

  So "empty matches everything, nil matches nothing" holds for `labelSelector` and is
  wrong for `namespaceSelector`, where nil is the *narrowest* answer rather than the
  emptiest one.

Both limits met on one key, and it was the second row of that table.
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
Fixed as `go-kure/launcher#430`; `networkpolicy_internal_test.go`'s
`TestParseNPPeer_NamespaceSelectorPresenceCases` pins the typed-nil case
directly.

The same typed-nil shape reached **list elements**. `parseObjectList` read a
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

## Two spellings of one field

`encoding/json` matches field names ignoring case, so a property map that writes one field in
two spellings (`suspend` and `Suspend`) would build with one value and drop the other without a
message. Every kind that decodes through `builtin.DecodeStrictJSON` refuses such a map, at any
depth, naming both keys: `<later>: sets the same field as <earlier> (field names match
case-insensitively, so one value would be dropped)`. Keys compare in byte order, at the first
byte where they differ. An upper-case ASCII letter sorts before its lower-case letter, and a
non-ASCII character sorts after every ASCII character. So in `Suspend` and `suspend` the later key
is `suspend`, and with the Kelvin sign (U+212A) in place of `k` the Kelvin spelling is the later
key. A field tagged `case:strict` is filled only by a key spelled exactly as its name, so the
refusal pairs no other spelling with it. The v1 decoder (`GOEXPERIMENT=nojsonv2`) ignores that
option. The refusal comes after the strict decode, so an unknown
key or a wrong type is still the first error, and a null never forms a pair.

The same decode refuses a key given twice in one JSON object, at any depth, with `<path> is given
more than once in the same object; give each key once`. A map cannot hold a key twice, but a
value that serializes itself, such as a `json.RawMessage`, can, and the decoder would merge its
occurrences of an object member or keep the last scalar without a message. This refusal comes
before the pair walk.

`kurel build` reaches the decode only after the authored-property check, which holds each
top-level key to the kind's schema by exact name. A top-level pair therefore stops earlier there:
the spelling the schema does not declare is refused as `unsupported field "Suspend" (allowed: ...)`,
and the "sets the same field" sentence is the refusal of a pair below the top level, inside an
object the schema leaves open (`ref.branch` and `ref.Branch`). Both refusals are build errors.
A caller that decodes without that check (a test, a library use) meets the sentence at the top
level too.

The kinds that go through `decodeKindSpec` and `cnpg-cluster` return that sentence alone, as
they did when their own check found the pair. A kind that decodes by itself keeps the wrapper it
adds to a decode error: `gitrepository: properties do not decode as a GitRepositorySpec: <sentence>`,
`helmrelease: properties do not decode as a HelmReleaseSpec: <sentence>`, and the same for
`bucket`, `helmchart`, `helmrepository`, `ocirepository`, `fluxcd-kustomization`, `helm` and
`helmtemplate` below their guarded levels. Before, those documents built. The `cilium-networkpolicy`
trait refuses a rule that writes one field in two spellings too, **even when both spellings are
complete**: it used to hold each spelling to the required list and build. The `helmrelease` verify
check still refuses a pair in which one spelling is null, which the decode lets through
(`foldedKey`).

The required-field check that `requiredfields` runs reads every spelling of a field, as before; the
decode refuses a pair before it is reached on the paths above, and a path that does not decode
through `DecodeStrictJSON` keeps its own reading.
