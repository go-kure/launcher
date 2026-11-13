---
title: "Adding a kind component: where its lines go"
weight: 12
---

## Adding a kind component: where its lines go

A change that adds a kind component adds one entry to each of the lists below. Such changes
are written side by side, and two of them merge without a conflict only where their new
lines land between different neighbours. So a new entry goes **at the position of its
component type**, in the order `sort.Strings` gives (`cilium-nodeconfig` before
`clusterissuer`, `pod` before `poddisruptionbudget`), and never at the end of the list.

| List | Where | Held in order by |
|---|---|---|
| `ComponentHandlers` | `pkg/oam/builtin/registry/registry.go` | `TestKindLists_InOrder` (`pkg/cmd/kurel`) |
| `validComponentTypes` | `pkg/oam/validate.go` | `TestKindLists_InOrder` |
| `wantHandlers`, `componentLabelFixtures` | the tests of `pkg/cmd/kurel` | `TestKindLists_InOrder` |
| `coreKindSchemas`, `apiSetKinds` | the tests of this package | `TestKindLists_InOrder`; `TestKindLists_Complete` fails on a registered type with no row and no reason in `kindListExceptions` |
| `parityKinds`, for a kind excepted there because it reads its own properties (`ownProperties`) | `hand_parsed_parity_internal_test.go` | not in order; `TestKindLists_OwnPropertiesHaveParityRows` (`pkg/cmd/kurel`) fails on a type with that reason and no row, and on a row of a type without it |
| `policyFreeKinds` and the two maps of its tests, for a kind built on `policyFreeKind` | the tests of this package | `TestKindLists_InOrder` |
| `requiredWrittenKinds`, for a kind whose API is a CRD a linked module ships or whose markers are read; `monitoringKinds`, for a kind of the Prometheus operator's API | the tests of this package | `TestKindLists_InOrder`. `testdata/required-written-not-refused.txt` is written in the order of `requiredWrittenKinds`, so a new kind's lines land at its position when it is regenerated |
| `imageFieldTypes`, for a spec type a kind decodes that names an image | `image_fields_internal_test.go` | `TestKindLists_InOrder`, in the order of the entries' `name` |
| `kindTraitPairs`, for a kind a trait also generates | the tests of `pkg/cmd/kurel` | `TestKindLists_InOrder`, in the order of the entries' `typ` |
| `policyFreeTypes`, for a kind built on `policyFreeKind` whose type publishes its field comments | `kind_policy_free_internal_test.go` | no test: its rows name the kind's value, not its type, and a new row goes at the end. A kind with no row is not held by `TestPolicyFreeKinds_NoDefaultedZeros` |
| ["Component types"](/pkg/oam/builtin/components/README.md#component-types) | this file | `TestKindLists_InOrder` |
| "Component type allowlist" | `site/content/api-reference/oam/parsing-and-types.md` | `TestKindLists_InOrder`; `TestKindLists_Complete` fails on a type of `validComponentTypes` with no row and on a row of no type of it |
| Table 4.2 | `docs/oam/design-kurel-package.md` | `TestKindLists_InOrder` |
| The table of kind components under "`kurel build`" | `pkg/cmd/kurel/README.md` | `TestKindLists_InOrder` |
| ["Per-type highlights"](/pkg/oam/builtin/components/README.md#per-type-highlights) | this file | no test, and not yet in order: a new entry goes before the first entry whose head names a type that sorts after its own |
| The "Shipped" entries of §6.2 that ship kinds | `docs/delivery-scope.md` | no test: the same position, before the entries that are not "Shipped" |

In the Go lists an entry names its component type by a string literal, not by a constant (a
struct written with field names, in its `component` field or the one `kindListKeyFields` in
`pkg/cmd/kurel` names for its list): `TestKindLists_InOrder` reads the source file, and stops
on an entry whose type it cannot read.

What belongs to one kind goes in that kind's own files, not at the end of a shared one: the
handler, its `ContractMetadata` and `ComponentObject` methods (not `contract.go`,
`object_name.go`), its tests, and the helpers only its entries in a shared list use, which go
in its own test file rather than beside the list.

No sentence names every kind. A sentence that needs the set points at the ["Kind inventory"](/pkg/oam/builtin/components/README.md#kind-inventory),
which its tests hold complete. One table names every component type, one row each: the
"Component type allowlist" of `site/content/api-reference/oam/parsing-and-types.md`. A change that adds a type to
`validComponentTypes` adds its row there, so it changes the README that CI's documentation
check requires of a change to `pkg/oam`.

Six places still conflict, and there the change that lands second is rebased on the first
before it is published:

- `policyFreeTypes`, where two changes each add a row at the end;
- two changes that add kinds at the same position of a list, with no existing entry between
  them (two more `cilium-*` kinds, say);
- a map whose values `gofmt` aligns (`ComponentHandlers`, `validComponentTypes`,
  `kindListExceptions`), when a new type is longer than every type the map holds: every
  entry is realigned, which conflicts with any other change to that map;
- the ["Kind inventory"](/pkg/oam/builtin/components/README.md#kind-inventory), where a new kind edits its row in place (`missing` to `kind`): two
  changes that edit neighbouring rows;
- the entries of §6.2 of `docs/delivery-scope.md` that are not "Shipped" (a "Held" entry,
  "Missing kinds"), which have no order: two changes that each add one at the same place,
  or that edit the same one;
- the import block of a file both changes add an import to.
