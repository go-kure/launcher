# OAM Component Models

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/model.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/model)

Package `model` defines the document format of a configuration-defined component model
(go-kure/launcher#1261). A model is a YAML document. It declares inputs, computes values with
CEL, and lists the parts (components and traits) that a component of the model type expands to.
A user can add a component type without writing Go.

This release step covers the format, the CEL environment and the evaluation of one expression.
`model.Load` decodes a document and checks its shape. It does not compile or run any expression.
`model.Compile` compiles every expression of a loaded model against the environment below, and
`Compiled.Eval` runs one of them. The lowering rule and the engine hooks arrive in later steps of
go-kure/launcher#1261. A document that loads today keeps its meaning then.

## Document

```yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Model
metadata:
  name: webapp.acme.example   # <name>.<domain>
  version: v2                 # v<major>[.<minor>[.<patch>]]
spec:
  celEnv: "1"
  inputs:
    image: {type: string, required: true}
    replicas: {type: integer, default: 2}
  values:
    - name: full
      expr: "${inputs.image + ':latest'}"
  parts:
    - id: app
      type: deployment.apps@v1
      properties:
        replicas: "${inputs.replicas}"
      traits:
        - type: expose.launcher.gokure.dev@v1
          properties: {port: 80}
```

| Field | Rule |
|---|---|
| `apiVersion`, `kind` | `launcher.gokure.dev/v1alpha1` and `Model`. |
| `metadata.name` | `<name>.<domain>`. It must have a domain, so a model never takes the name of a Kubernetes kind. It follows the type identity grammar in `pkg/oam` (`oam.ValidateTypeIdentity`). |
| `metadata.version` | `v<major>`, `v<major>.<minor>` or `v<major>.<minor>.<patch>`. A short version is exact: `v2` is `v2.0.0`. |
| `spec.celEnv` | Required. It must be `"1"`. The value names a fixed set of CEL functions, so a model means the same later. |
| `spec.inputs` | Map of input name to `oam.PropertySchema`. Each name is a CEL identifier. Each schema has a `type` or `types`. |
| `spec.values` | Ordered list of `{name, expr}`. A value reads inputs and earlier values only. Each name is a CEL identifier and is unique. |
| `spec.parts` | At least one part. A model that expands to nothing is an error. |
| `parts[].id` | A CEL identifier, unique in the model. Other parts read `parts.<id>`. |
| `parts[].type`, `traits[].type` | A full identity, `name@version`. A short name and a missing version are refused. |
| `parts[].when` | Optional. Exactly one `${ }` hole that gives a bool. The part is emitted only when it is true. It does not see the loop variable. |
| `parts[].forEach` | Optional `{as, in}`. `in` is exactly one `${ }` hole that gives a list. `as` is the loop variable, a CEL identifier. A filter per element goes in `in`. |
| `parts[].name`, `properties`, `annotations` | Optional. Strings may hold `${ }` expressions. `name` is a string template. |

A string that is exactly one `${ expr }` keeps the CEL type of the result. A string with `${ }`
inside is a string template. `$${` writes a literal `${`. `values[].expr`, `when` and
`forEach.in` take exactly one hole and no other text. `Load` stores all these strings as
written and compiles none of them.

Loading is always strict. An unknown field fails with its name and line. There is no `extends`
and no `patches`. A file with more than one YAML document is refused. YAML anchors, aliases to
an anchor defined earlier and merge keys (`<<` as a mapping key) are refused with their line,
because they hide duplicate keys. The same text `<<` as a value is the string `<<` and loads.
An alias whose anchor is not defined earlier in the document is refused by the
YAML parser, with its own error and no line. Each entry of
`types` must be a known type name, so `types: [""]` is refused. A union needs at least two
different scalar types (`string`, `integer`, `number` or `boolean`), at every depth of a schema.
So `types: [string]`, `types: [string, string]` and `types: [string, object]` are refused. These
are the union rules of the shared validator in `pkg/oam`. A malformed exclusive group on any node of an
input schema is refused too, with the path of that node: a group of fewer than two keys, a key the
node does not declare, a key in two groups, a `Required` key, or groups on a node that is not an
object. These are the group rules of the same validator.
A null entry in a list of strings or of objects with fixed fields is refused with its path,
because YAML would drop it. Examples are `types`, `values`, `parts`, `traits`, `exclusive` and the
`keys` of an exclusive group. A list of free-form items keeps its nulls. Examples are `enum` and
any list inside a `default` or inside the `properties` of a part or a trait.
In a free-form value, such as a `default`, the keys of a mapping must differ after YAML
resolves them, so `{1: a, 01: b}`, `{true: a, True: b}` and `{0.0: a, -0.0: b}` are refused.
In every map whose keys `Load` decodes into Go strings, two keys that decode to the same string
are also refused. Examples are the inputs, the `properties`, `gatedPaths` and `valueGatedPaths` (at both levels) of a schema, the
`properties` of a part or a trait, and the annotations. The YAML decoder refuses two keys with
the same spelling. `Load` also refuses two keys with different spelling that decode to one
string, such as `image` and `!!binary aW1hZ2U=`. Keys that decode to different strings, such as
`1` and `01`, stay apart. In such a map, a null key is skipped with its value, as yaml.v3 skips
it. A null key is written `~`, `null`, `Null`, `NULL` or with the `!!null` tag. Two null keys
with different spelling, such as `~` and `null`, are no duplicate. Two null keys with the same
spelling are refused by the YAML decoder, like any two keys with the same spelling. The checks
of decoded values do not look at the value under a null key. The refusal of anchors, aliases
and merge keys reads the whole document before the decoding, so it applies under a null key
too.
The path in an error names a key as it decodes: `!!binary aW1hZ2U=` appears as `image`.

## CEL environment (`celEnv: "1"`)

The environment is the CEL standard library plus three extension libraries. Each library has a
pinned version, because cel-go defaults every library to its newest version and a model would then
change meaning on an upgrade. The versions are `ext.Strings` 5, `ext.Lists` 3 and `ext.Bindings` 1.
A golden file lists every function and overload of the environment. A test fails when the list
changes. No other library is available, so `math`, `sets` and the encoders are refused.

The golden file pins what the environment holds. Some names in it start with `@`:
`cel.@block`, `@in`, `@not_strictly_false` and `@sortByAssociatedKeys`. CEL source cannot spell
an identifier with `@`, so a model cannot call them. Macros reach them. The function
`__not_strictly_false__` is declared but disabled. So are `_in_` and `in`, the legacy names of
the `in` operator. The type checker refuses a call of a disabled function in source, and only the
runtime binding of a macro or an operator uses it. The `in` operator itself works through `@in`,
so `x in list` and `k in map` compile. The golden file marks each disabled function
`[disabled]`, so a change of that state fails the test.

A variable has a type from the input schema. `inputs.<name>` has the type of its schema. An
`object` input is `map(string, dyn)` and a `types` union is `dyn`. A list inside an object
input is therefore `dyn`. `self.name` and `self.application` are strings. `values.<name>` has
the type of its expression. A value reads inputs and earlier values only.

A comment in a hole runs from `//` to the end of the line, as in CEL. A `}` or a quote inside a
comment does not close the hole and does not open a string. A comment with no newline after it
runs to the end of the text, so the hole is not closed.

The same text is not a comment inside a string, a bytes literal or an escaped identifier. These
forms may hold `//`: single, double and triple quoted strings, the raw form of each (`r'...'`),
the bytes form of each (`b'...'`, `br'...'`) and an identifier in backticks, such as
`` {'a//b': 1}.`a//b` ``. An escaped identifier ends at the next backtick. It holds no quote, brace or
new line, so one that is not closed before the end of the line or text leaves the hole not closed.

An optional input has no default and is not required. It is null when absent. Test it with
`inputs.x == null`. `has(inputs.x)` is refused, because an input is a variable and not a map
field. A null input in an operator or a method call, such as `inputs.x + 'a'`, compiles and fails
at run time.

A value that is exactly one reference to an input or to an earlier value has the declared type
of that reference. If the input is optional, the value is also nullable, so `values.x == null`
compiles. Any other expression has the type that the checker reports. The checker drops the
nullable wrapper of a list or map result, so `inputs.tags + []` is not nullable. It keeps the
wrapper of a scalar result, so `cel.bind(x, inputs.nick, x)` with an optional string `nick` is
still nullable. A local name `values` in a `cel.bind` or a comprehension hides the model values
inside its scope, as in CEL.

Four rules keep an expression deterministic and bounded:

- A comprehension (`map`, `filter`, `exists`, `all` and the like) ranges over a list only. A map
  has no fixed key order. A `dyn` value has no known element type.
- The function `dyn()` is refused, because it hides a type from the rule above.
- An expression has at most 4096 code points, a parser recursion depth of 64 and 10000 nodes.
  The node limit is a defensive bound. No input was found that reaches it under the size limit,
  and that is not proven.
- A bind nests at most 8 deep. The limit counts every macro that expands to a bind, not only the
  `cel.bind` calls you write. In celEnv 1 these are `cel.bind` and `sortBy`. Chained `sortBy` calls
  nest, because the target of each call is the initializer of the bind before it. So
  `[1].sortBy(x, x)` repeated 9 times is refused, and so are 4 `cel.bind` calls around 5 `sortBy`
  calls. The `optMap` and `optFlatMap` macros expand to a bind too, and celEnv 1 does not load them.

The four numbers are start values. Raising a limit later breaks no model. Lowering one can.

## Evaluation

`model.Compile(m)` compiles every expression site of a loaded model. `Compiled.Eval(path, scope)`
runs one site and returns the Go value, the actual cost and an error. A site is named by its
path: `spec.values[0].expr`, `spec.parts[0].when`, `spec.parts[0].forEach.in`,
`spec.parts[0].name`, `spec.parts[0].properties.a.b`, `spec.parts[0].properties.list[1]`,
`spec.parts[0].traits[0].properties.a` and `spec.parts[0].annotations.k`. Every string in
`properties`, in `traits[].properties` and in `annotations` is a site, with or without a hole. An
unknown path is an error.

`Compile` returns the first failure, which wraps `ErrInvalid` and names the site. The order is the
values, then for each part: `when`, `forEach.in`, `name`, the properties (keys sorted at every
level), the trait properties and the annotations. The scope of a site is the inputs, `self`, the
earlier values and, for `name`, the properties, the traits and the annotations of a `forEach`
part, the loop variable. `when` and `forEach.in` do not see the loop variable. The loop variable
has the element type of `forEach.in`.

**Result.** A string that is exactly one hole gives the value with its type: `int64`, `uint64`,
`float64`, `bool`, `string`, `[]byte`, `[]any` (in order), `map[string]any` or `nil`. A string
with text around a hole, or with several holes, gives a string. A map key must be a string. A
map with another key type is refused when the model compiles, at every site that gives a value:
a property, a value and a `forEach.in` list (a map in its elements included).

**Text.** Inside text a hole must give a string, an int, a uint, a double or a bool. A list, a
map or a `dyn` value is refused when the model compiles ("not allowed in text"). The type of a
`dyn` value is not known when the model compiles, so even a `dyn` value that holds a string is
refused: write `string(x)` around it. A double takes
the form of CEL `string(x)`: `1.0` gives `1`, `0.1` gives `0.1`, `100000000.0` gives `1e+08`,
`1e21` gives `1e+21`, `-0.0` gives `-0`. A bool gives `true` or `false`. Text and a number or a
bool do not join with `+`: `'a' + 1.0` is a compile error, and `'a' + string(1.0)` works.

**Null.** A single hole keeps null: the result is `nil` and a property with a null value stays
null downstream, no key is dropped. An input that is absent from `Scope.Inputs` is null. Null in
mixed text is an error that names the site, never the text `null` and never an empty string.

**Numbers.** A NaN or an infinity has no place in a document. A single hole, or a list or map
inside it, that holds one fails with `ErrNonFinite`, and so does a double of that kind in text. An
explicit `string(1.0 / 0.0)` is text and is not refused: the author asked for the conversion.

**Map keys.** A key in `properties`, in `traits[].properties` and in `annotations` is literal
text. A key that holds `${` is refused when the model compiles, whether it is a hole or the
escape `$${`.

**Cost.** One hole has a cost limit of 1,000,000. An evaluation that spends more fails with
`ErrCostLimit`, which names the site and the limit. The cost of a site is the sum of its holes.
The cost also stops a `replace` or a `join` that would build a result of several megabytes (a
test pins both). A list of more than 1,000,000 elements is refused by cel-go, which has its own
cap for `lists.range`, and not by the cost limit. The cost does not bound the size of the result
of `format`: a `format` that builds a result of 2,000,000 bytes runs to the end. A size limit for
`format` is a separate change.

**Cache.** `Cache.Compile` keeps a compiled site under the model identity (`Model.Key()`), the
site path and the template text. A second compile of a model reuses the sites. The same text at
another path, or in a model of another identity, is another entry, because its types differ. An
identity names one definition: a changed model under the same identity must use a new cache. A
`Compiled` and a `Cache` are safe for concurrent use.

## API

| Call | Does |
|---|---|
| `model.Compile(m)` | Compiles every expression site of `m` with a cache of its own. |
| `(*Cache).Compile(m)` | The same, with a shared cache from `model.NewCache()`. `Len()` counts the cached sites. |
| `(*Compiled).Eval(path, scope)` | Runs one site. Returns the Go value, the actual cost and an error. |
| `model.Load(data)` | Decodes one document and checks it. A failure of a decoded document wraps `model.ErrInvalid` and names the field, for example `spec.parts[1].id`. A YAML, `apiVersion` or `kind` failure does not wrap it. A YAML type error, such as a list where a string belongs, names the line of the field and not its path. An explicit tag that does not fit its value, such as `!!bool notabool`, names its line too, when one tagged value is at fault; with two or more, the error has no line. |
| `(*Model).Identity()` | The model identity as written in `metadata`. |
| `(*Model).Key()` | The identity with the version in the canonical form (`name@v2.0.0`). The registry returns a version as it was registered, so compare and store this form (`oam.TypeID.Canonical`). |

`Load` returns the first failure. The YAML, `apiVersion` and `kind` checks come first. Then come the
null and duplicate-key checks, the metadata, `spec.celEnv`, the inputs in alphabetical order of
their names (a Go map keeps no document order), the values and the parts in document order.
