# Component definitions

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/definition.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/definition)

A `ComponentDefinition` declares a component type for one custom resource kind, with no Go code.
It names the kind it emits and a CustomResourceDefinition file, pinned by SHA-256. `kurel build`
loads the documents of `<app dir>/definitions/` (see the
[kurel CLI reference](https://github.com/go-kure/launcher/blob/main/pkg/cmd/kurel/README.md#component-definitions)); a library caller uses
this package directly.

```go
h, err := definition.Load("definitions/batchjob.yaml") // reads the CRD and checks its digest
if err != nil { /* refused: names the file and the reason */ }
err = definition.Register(transformer, h)              // the type is now authorable
```

`Load` refuses a document with a field it does not know, a `crd` path that leaves the definition's
directory (by `..`, an absolute path or a symlink), a digest that differs, a CRD that does not define the emitted kind in a served
version, and a schema that is not structural. `Register` refuses a type name the transformer
already holds, and declares the type's identity, `<kind>.<group>@<version>`, so a model can name
it. It uses `Transformer.RegisterAuthoredComponent`, so a document may author the type.

A component of the type takes the resource's `spec` as its properties. The object is checked
against the CRD's schema, unknown fields included, and emitted by the `passthrough` handler,
which applies its own per-object rules. The CRD's `spec.scope` decides whether the object is
namespaced.

## What the check is

The API server's structural schema check, run offline: types, required fields, enums, bounds,
patterns, formats, array items, `additionalProperties`, int-or-string values and
`x-kubernetes-preserve-unknown-fields`. Not done:

- CEL rules (`x-kubernetes-validations`) are not evaluated.
- Defaults are not applied.
- Only the version the document names is read, from a local file.
- A property is authored without `null`, which reads as absent, as for every property, so a
  CRD's `nullable` is not reachable.
- A CRD whose `spec` is open (`additionalProperties` or `x-kubernetes-preserve-unknown-fields`
  on `spec` itself) is refused: the published properties are the declared ones. Open
  properties further down are accepted.
- A file holds one definition; a second YAML document is refused.
- No policy reads the pod settings a resource embeds (go-kure/launcher#1705).
