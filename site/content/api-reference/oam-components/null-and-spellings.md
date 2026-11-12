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

What it means for a handler parser in this package: read a null under an optional
property as omission, never as a present value of the wrong type.

Two limits worth knowing before relying on the rule:

- **A parser that answers presence with a bare `v, ok := props[key]` does not comply
  on its own.** It reads a present null as present. Emission validation deletes such
  keys before a rule-emitted component is converted, so on that path those parsers
  agree with the contract *because of the strip*, not independently of it. The
  authored path has no top-level strip: `ValidateAuthoredProperties` checks shape and
  leaves a top-level null in place, so there the null reaches the parser. What it
  does next depends on how the parser consumes the lookup: one that type-checks the
  value refuses it as a wrong type, while one that discards a failed assertion treats
  it as absent without saying so.
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
`helmtemplate` below their guarded levels. The `cilium-networkpolicy`
trait refuses a rule that writes one field in two spellings too, **even when both spellings are
complete**: it used to hold each spelling to the required list and build. The `helmrelease` verify
check still refuses a pair in which one spelling is null, which the decode lets through.

The required-field check that `requiredfields` runs reads every spelling of a field, as before; the
decode refuses a pair before it is reached on the paths above, and a path that does not decode
through `DecodeStrictJSON` keeps its own reading.
