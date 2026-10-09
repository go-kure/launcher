# OAM Built-in Registry

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/registry.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/registry)

Package `registry` exports every built-in handler and lowering rule launcher implements,
keyed by the type each one registers under, so a consumer registers the builtins from the
library instead of keeping its own list. `kurel build` registers exactly these
(`pkg/cmd/kurel`, `newBuiltinTransformer`).

| Function | Holds | Register with |
|---|---|---|
| `ComponentHandlers()` | every kind component of `pkg/oam/builtin/components` | `oam.NewTransformer` or `RegisterComponent` |
| `ComponentLoweringRules()` | `webservice`, `worker`, `helm`, `postgresql`, `oci` | `RegisterComponentLowering` |
| `TraitHandlers()` | every trait of `pkg/oam/builtin/traits` | `RegisterBuiltinTrait` |
| `TraitLoweringRules()` | `expose` | `RegisterBuiltinTraitLowering` |
| `PolicyHandlers()` | `dependency`, `placement` | `RegisterPolicy` |

Each function returns a new map on every call: a caller may delete, replace or add
entries, and the change reaches no other caller. A type launcher adds later appears in the
map of its kind without a change on the caller's side. Adding one takes two lines in one
commit: its entry in `registry.go`, at its position, and its line in kurel's
`testdata/builtin-registries.txt`. The table names the maps, not the types: the kind
components themselves, each with what it emits and refuses, are listed in
[`pkg/oam/builtin/components`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components),
so a new kind changes nothing here but those two lines.

A type that is a lowering rule is never also a handler of the same position, and
`RegisterComponentLowering` and `RegisterTraitLowering` panic on that collision. A consumer
that replaces a handler sets its own under the same key; one that replaces a lowered type
(say `webservice`) with a handler deletes the type from the lowering-rule map.

launcher has no handler for the `app-dependency`, `reconciliation` and `health-checks`
policies or the `fluxcd-patches` and `fluxcd-postbuild` traits, so none of the maps holds
them: a consumer that implements them registers its own.

See "Registering the builtins" in [`pkg/oam`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam)
for the registration sequence.

## Tests

- `TestRegistries_FreshMapEachCall`: a caller's change to one call's map does not show in
  the next call's.
- `TestRegistries_LoweringRulesClaimTheirKey`: each lowering rule is keyed under the type it
  claims.
- `TestRegistries_RegisterTogether`: every entry registers on one transformer without a
  collision.
- In `pkg/cmd/kurel`, `TestKindLists_InOrder` holds the entries of `ComponentHandlers` in
  the order of their type, `TestBuiltinRegistries_TypesUnchanged` holds kurel's registered
  set to `testdata/builtin-registries.txt`, and the handler-schema tests cover every entry.
