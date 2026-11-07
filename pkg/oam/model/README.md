# OAM Component Models

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/model.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/model)

Package `model` defines the document format of a configuration-defined component model
(go-kure/launcher#1261). A model is a YAML document. It declares inputs, computes values with
CEL, and lists the parts (components and traits) that a component of the model type expands to.
A user can add a component type without writing Go.

This release step covers the format only. `model.Load` decodes a document and checks its
shape. It does not compile or run any expression. The CEL environment, the lowering rule
and the engine hooks arrive in later steps of go-kure/launcher#1261. A document that loads
today keeps its meaning then.

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
| `parts[].when` | Optional expression. The part is emitted only when it is true. |
| `parts[].forEach` | Optional `{as, in}`. `in` is an expression that gives a list. `as` is the loop variable, a CEL identifier. |
| `parts[].name`, `properties`, `annotations` | Optional. Strings may hold `${ }` expressions. |

A string that is exactly one `${ expr }` keeps the CEL type of the result. A string with `${ }`
inside is a string template. `$${` writes a literal `${`. Release one of the format does not
compile these strings, so `Load` stores them as written.

Loading is always strict. An unknown field fails with its name and line. There is no `extends`
and no `patches`. A file with more than one YAML document is refused. YAML anchors, aliases and
merge keys (`<<`) are refused with their line, because they hide duplicate keys. Each entry of
`types` must be a known type name, so `types: [""]` is refused. A union needs at least two
different scalar types (`string`, `integer`, `number` or `boolean`), at every depth of a schema.
So `types: [string]`, `types: [string, string]` and `types: [string, object]` are refused. These
are the rules of the shared validator in `pkg/oam`, which rejects every value of such a schema.
A null entry in a typed list (`types`, `values`, `parts`, `traits`, `exclusive`) is refused with
its path, because YAML would drop it. In a free-form value, such as a `default`, the keys of a mapping must differ after YAML
resolves them, so `{1: a, 01: b}`, `{true: a, True: b}` and `{0.0: a, -0.0: b}` are refused.
In a map that `Load` decodes into Go strings, two keys that decode to the same string are also
refused. These maps are the inputs, the `properties` of a schema, a part or a trait, and the
annotations. The YAML decoder refuses two keys with the same spelling. `Load` also refuses two keys
with different spelling that decode to one string, such as `image` and `!!binary aW1hZ2U=`. Keys
that decode to different strings, such as `1` and `01`, stay apart. A null key (`~`, `null` or an
empty key) is skipped with its value, as yaml.v3 skips it. It never counts as a duplicate, and
`Load` does not check the value under it.

## API

| Call | Does |
|---|---|
| `model.Load(data)` | Decodes one document and checks it. A failure of a decoded document wraps `model.ErrInvalid` and names the field, for example `spec.parts[1].id`. A YAML, `apiVersion` or `kind` failure does not wrap it. A YAML type error, such as a list where a string belongs, names the line of the field and not its path. |
| `(*Model).Identity()` | The model identity as written in `metadata`. |
| `(*Model).Key()` | The identity with the version in the canonical form (`name@v2.0.0`). The registry returns a version as it was registered, so compare and store this form (`oam.TypeID.Canonical`). |

`Load` returns the first failure. The YAML, `apiVersion` and `kind` checks come first. Then come the
null and duplicate-key checks, the metadata, the inputs in alphabetical order of their names
(a Go map keeps no document order), the values and the parts in document order.
