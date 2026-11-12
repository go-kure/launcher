# OAM Component Models

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/model.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/model)

Package `model` defines the document format of a configuration-defined component model
(go-kure/launcher#1261). A model is a YAML document. It declares inputs, computes values with
CEL, and lists the parts (components and traits) that a component of the model type expands to.
A user can add a component type without writing Go.

This release step covers the format, the CEL environment and the evaluation of one expression.
`model.Load` decodes a document and checks its shape. It does not compile or run any expression.
`model.Compile` compiles every expression of a loaded model against the environment below, and
`Compiled.Eval` runs one of them. `Compiled.Expand` builds the components of the parts and binds the
outputs that a later part reads. The lowering rule, the engine hooks and the capability check on the
traits of a model arrive in later steps of go-kure/launcher#1261. A document that loads today keeps its meaning then.

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
| `parts[].id` | A CEL identifier, unique in the model. A later part reads `parts.<id>.<key>`, the object name of one output that the part declares. A part reads only an earlier part, and never a part with `when` or `forEach`. A read of any other part, or of the part itself, fails when the model compiles, and the error names the site and the part. A key the part does not declare fails when `Expand` runs, and the error names the site, the part and the key. |
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

Two sites never share a path. A key that holds a dot or a bracket can write the path of another
property, such as `a.b` beside `a: {b: ...}`, or `a[0]` beside a list `a`. The model is refused
when it compiles, and the error names the path. A free-form map whose keys are not all strings,
which YAML gives for a key such as `1` or `true`, is read like any other map. The text of the key is
the path segment, so the key `1` gives `.1`. The integer key `1` and the string key `"1"` give one
path, so a map that holds both is refused.

`Compile` returns the first failure, which wraps `ErrInvalid` and names the site. The order is the
values, then for each part: `when`, `forEach.in`, `name`, the properties (keys sorted at every
level), the trait properties and the annotations. The scope of a site is the inputs, `self`, the
earlier values, the earlier parts that have no `when` and no `forEach` (`parts.<id>`) and, for `name`, the properties, the traits and the annotations of a `forEach`
part, the loop variable. `when` and `forEach.in` do not see the loop variable. The loop variable
has the element type of `forEach.in`.

**Parts.** `Compiled.Expand(tr, scope, naming)` evaluates the values, then each part in document
order. A part whose `when` is false emits nothing. A part with `forEach` emits one component for
each element. A part without a `name` takes its id. After it builds a part that has no `when` and
no `forEach`, `Expand` asks the Transformer for the outputs of that component
(`ComponentOutputsNamed`, with `naming` as the Naming hook) and stores the object name of each under
the part id. A later part reads it as `parts.<id>.<key>`, so the name follows an authored object
name and the Naming hook, as the name of the built object does. `tr` must declare the type
identities of the parts (`registry.DeclareTypes`): a type that does not resolve declares no output,
so a read of it fails as an undeclared key. `Eval` takes the same outputs in `Scope.Parts`, and it
refuses a key that is not there with the same error. `Expand` reads the inputs and `self` from the
scope and sets the other fields itself. It checks no trait against a capability and runs no
transform.

**Result.** A string that is exactly one hole gives the value with its type: `int64`, `uint64`,
`float64`, `bool`, `string`, `[]byte`, `[]any` (in order), `map[string]any` or `nil`. A string
with text around a hole, or with several holes, gives a string. A `[]byte` result is a copy: a
write to it does not change the next result of the site. A map key must be a string. The compile
check reads the type of the result and refuses a map whose key type is not a string, at every site
that gives a value: a property, a value and a `forEach.in` list (a map in its elements included).
A map literal with a key of another type, such as `{1: 'a', 'b': 'c'}`, has the key type `dyn`,
which the check cannot read. Such a map compiles and fails when the site is evaluated, with the
same words: map keys must be strings.

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
The charge by size stops a `replace`, a `join` or a `format` that would build a result of more than
1,000,000 bytes before it builds it (a test pins each). `lists.range(1000000)` is stopped by the cost limit. A list of more than
1,000,000 elements is refused by cel-go, which has its own cap for `lists.range`, and not by the
cost limit. The charge by size (see "Charge by size") also bounds every value.

**Format.** A `format` is refused before it runs when the most its arguments can print passes 1,000,000
bytes. The bound is the length of the format string, plus the length of each string or bytes argument
(twice it when the format string holds an `x`, for `%x`), 420 for a double, 64 for a timestamp, a
duration or a type, 20 for every other scalar (130 when the format string has a clause with a
precision, `%f`, `%b` or `%o`, which print a number in more digits), 4 for each list element and 6 for each
map entry. A list
or map argument is walked as a tree. The walk spends one step of the comparison budget (1,000,000
values for one evaluation) for each value it reads, so a shared nest is never walked in full and a
loop of calls cannot walk without limit. The bound is above the real
size, so a `format` whose real result is within that slack of 1,000,000 bytes can be refused though
it would fit. The result and the errors are those of cel-go.

**Comparisons and flatten.** The cost does not bound a comparison of nested lists that share their elements:
cel-go compares them element by element at every depth and charges the outer length only, so `d == d`
on four levels of shared lists of 200 elements reports a cost of 8,739 and runs for about a minute
(go-kure/launcher#1451). The `flatten` of the lists extension (version 3) has the same flaw: it copies
every element it reaches and charges the outer length times the depth. So the evaluation replaces five
calls, `==`, `!=`, `in`, `distinct` and `flatten`, with versions that count the values they visit: a list,
a map, a list element and a map value are one step each, and a string or bytes value costs one more step for
each 1,024 bytes it holds, because cel-go compares it by its content. A map comparison reads every key,
also after one differs, because cel-go gives the keys in Go map order and an early stop would make a stop by the budget
depend on that order. All of them in one site evaluation
share a budget of 1,000,000 steps with the charge by size. It also replaces `join`, only for its error: the `join` of a list that
holds a non-string fails in cel-go with an error built from the text of the whole list, which prints every
shared child and reached 7 GB, so the replacement names the type of the element and prints no value. That
`join` does not spend the budget. A site that needs more fails with `ErrCostLimit`, which names the site. The stop holds
even where CEL would drop the error, as in `(a == b) || true`: the evaluation checks the budget when the
program ends. **A comparison or flatten of more than 1,000,000 values is refused even where stock cel-go
finishes it**: `d == d` on four levels of shared lists of 50 elements holds 6.25 million values, and cel-go
answers it in about 0.2 s. Every other call gives what cel-go gives, with the cost cel-go reports: a test
compares 52 expressions with cel-go's own result and cost. The first four calls are every caller of `Equal`
in cel-go v0.31.0 and in the pinned extension versions, `flatten` is the one list function that copies
nested elements under the legacy cost tracker, and `join` is the one call whose error prints a nested value. A test reads the cel-go version and fails on another one,
so that the calls are read again when go-kure/launcher#1413 moves cel-go.

**Charge by size.** cel-go charges a call by the length of its operands and reads the cost after the call
returns, so a value that many places share, or a call that builds a big output from small operands, ran
below the cost limit (go-kure/launcher#1461). The evaluation now charges every call result and every
identifier read by size, against the same budget of 1,000,000 steps as the comparisons. A string or bytes
value costs one step for each 1,024 bytes. No string or bytes value may hold more than 1,000,000 bytes,
and no list or map more than 1,000,000 elements: a value at the cap is allowed, and a value over it stops
the evaluation with `ErrCostLimit`. A list or a map costs nothing more, because cel-go charges a call by
the element count of its operands. The keys of `sort` and `sortBy` cost the bytes of the string and
bytes elements they hold, because a sort compares them by content. The charge comes before the sort runs,
so a sort that fails after it compared, in `xs.sort() == [] || true`, is paid for too. `join` checks that every element is a
string before it writes the first byte, so a join that fails copies nothing, and it checks its buffer
before each write, against the bytes that the next write adds (the first element has no separator before it).
`replace` is replaced by a version that computes the size of its result from the number of matches
and stops before it builds a result over the cap. `indexOf` and `lastIndexOf` are replaced by a version that refuses a search
before it runs when cel-go would charge it over the cost limit: cel-go runs two nested loops and charges one tenth of a step for each
pair of characters, but only after the call returns, so a string of 1,000,000 bytes and a search text of 500,000 ran 250 billion
comparisons first. `matches` has the same replacement, in its member and its global form: cel-go charges the length of the string
times a quarter of the length of the pattern, only after the match ran, so a string of 100,000 bytes and a pattern of 40,000 that an
input holds ran for minutes. Each replacement gives the result and the cost that cel-go gives for every call under that limit. An identifier that is the key of an index, as in
`m[k]`, is charged by its size at every lookup, because cel-go reads it through the attribute and not
as a value. So is every other key attribute, such as `m[c ? k : k]` and `m[(xs + [])[0]]`: the key is
resolved once, and the cost that the evaluation reports does not change. A map comparison charges the length of each key. `&&`, `||`, the conditional and the
comprehensions are not calls, so they stay lazy: a branch that does not run is not charged. **A value over
the cap is refused even where stock cel-go finishes it.** A test names a class for every function of the
pinned environment, so that a new cel-go version fails two lists and not one. The wrapper of an
identifier read skips a node that cel-go already watches, and it finds that node by the name of an
unexported cel-go type. Without the skip, cel-go reports one more step of cost for each index or select
on a computed value. Three rows of the comparison test guard the name: `[1, 1.0][0] == [1, 1.0][1]`,
`[1u, 1][0] == [1u, 1][1]` and `[[1], {'a': 1}][0] == [[1], {'a': 1}][1]` must report the cost of cel-go
(25, 25 and 105, and 27, 27 and 107 without the skip). A cel-go version that renames the type fails them.

**Known limits: calls that work and then fail, or that cel-go cannot cost.** Three members of this class
are open.
- A `format` that copies a long string argument and then fails on a later clause is charged nothing for
  the copy, because the charge reads the result and a failure has none. The argument list must be
  bound outside the loop, because the compiler rejects a literal list that does not fit the format:
  `cel.bind(args, [inputs.image, 'x'], lists.range(2000).all(i, '%s%d'.format(args) == '' || true))`
  with a 1,000,000-byte `inputs.image` gives `true` and allocates about 5.7 GB in 1.6 s below both
  budgets.
- A `sort` or a `reverse` whose type is not fully known when the model compiles has an ambiguous
  overload, so cel-go charges it one step and the lists extension never reads its elements. For `sort`
  the cause is an unknown element type (a `dyn` element); for `reverse` it is a receiver of type `dyn`,
  because a string has a `reverse` too. A `sort` of `[0, 'bad'] + lists.range(10000)`: 2,000 sorts give
  `true` at a cost of 22,044, allocating about 1 GB in 3 s. A `reverse` of a list read from a mixed map,
  `cel.bind(xs, {'xs': lists.range(10000), 'other': 0}.xs, lists.range(2000).all(i, xs.reverse().size() == 10000))`:
  `true` at a cost of 26,065, allocating about 1.4 GB in 0.8 s. A `sort` or `reverse` of a typed list
  is stopped, and so is `reverse` of the `list(dyn)` in the `sort` example. A `dyn` receiver of `slice`
  (one overload, which charges its output), `distinct`, `flatten` or `==` is stopped by the cost limit
  or the budget in the same loop, and `size`, `in`, an index and `+` copy nothing.

Every other member of this class found so far is bounded (`sortBy`, `join`, `replace`, `format`, comparisons,
`flatten`, the keys of a typed `sort` and the keys of a result copy), and a test pins each. These
limits are not reachable today, because no code registers a model's CEL for evaluation by default.
The list may not be complete; go-kure/launcher#1461 holds the current set. It stays open for them: the fix is to charge the bytes and the elements of a
container on every read, so that no call needs its own charge. **go-kure/launcher#1461 must close
before step 4a of go-kure/launcher#1261 or any public wiring of the evaluator.** The size of a
successful `format` result is bounded by the check above.

**Result size.** The Go copy of a single-hole result holds at most 1,000,000 units, counted at every
depth. A list element or a map entry is one unit, and so is each byte of a bytes value. A map key
costs a further unit for each 1,024 bytes, twice, because the copy hashes it to read the entry and to insert it. The copy is
made after the evaluation. An expression can share one list or one bytes value in many places at a
small cost, and the copy counts it once for each place. So the cost limit does not bound the copy. A result over the limit fails with `ErrCostLimit`, which names the
site. A string or a map key in the result, at any depth, holds at most 1,000,000 bytes, as a string read by name
does: an input that nests a longer one fails when the result exports it. A comparison that only reads such an
input is charged by its size against the budget and exports nothing.

**Cache.** `Cache.Compile` keeps a compiled site under the model identity (`Model.Key()`), the
site path and the template text. A second compile of a model reuses the sites. The same text at
another path, or in a model of another identity, is another entry, because its types differ. An
identity names one definition: a changed model under the same identity must use a new cache. A
`Compiled` and a `Cache` are safe for concurrent use.

## API

| Call | Does |
|---|---|
| `model.Compile(m)` | Compiles every expression site of `m` with a cache of its own. |
| `(*Cache).Compile(m)` | The same, with a shared cache from `model.CreateCache()`. `Len()` counts the cached sites. |
| `(*Compiled).Eval(path, scope)` | Runs one site. Returns the Go value, the actual cost and an error. |
| `(*Compiled).Expand(tr, scope, naming)` | Builds the components of the parts in document order and binds `parts.<id>.<key>` from `Transformer.ComponentOutputs`. It runs no transform. |
| `model.Load(data)` | Decodes one document and checks it. A failure of a decoded document wraps `model.ErrInvalid` and names the field, for example `spec.parts[1].id`. A YAML, `apiVersion` or `kind` failure does not wrap it. A YAML type error, such as a list where a string belongs, names the line of the field and not its path. An explicit tag that does not fit its value, such as `!!bool notabool`, names its line too, when one tagged value is at fault; with two or more, the error has no line. |
| `(*Model).Identity()` | The model identity as written in `metadata`. |
| `(*Model).Key()` | The identity with the version in the canonical form (`name@v2.0.0`). The registry returns a version as it was registered, so compare and store this form (`oam.TypeID.Canonical`). |

`Load` returns the first failure. The YAML, `apiVersion` and `kind` checks come first. Then come the
null and duplicate-key checks, the metadata, `spec.celEnv`, the inputs in alphabetical order of
their names (a Go map keeps no document order), the values and the parts in document order.
