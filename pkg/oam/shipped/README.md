# Shipped definitions and models

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/shipped.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/shipped)

The component definitions and models that launcher ships inside the `kurel` binary
(go-kure/launcher#1735). `kurel build` registers them before it reads an application's own
`definitions/` and `models/`, so an application authors a shipped type with no file of its own.

```go
set, err := shipped.Load(transformer) // registers the embedded set; refuses a file that does not load
err = set.Clash(h.TypeName(), h.Identity()) // refuses an application's type that is shipped
```

## What ships

| Type | Emits | CRD |
|---|---|---|
| `vm-rule` | `VMRule` (`operator.victoriametrics.com/v1beta1`) | VictoriaMetrics operator release v0.76.0 |
| `vm-service-scrape` | `VMServiceScrape` | the same release |
| `vm-pod-scrape` | `VMPodScrape` | the same release |

A model names such a kind by its identity, `<kind>.<group>@<version>`
(`vmrule.operator.victoriametrics.com@v1beta1`). No model ships yet.

## Where it lives and how it loads

`content/definitions/*.yaml` are `ComponentDefinition` documents, and the CRD each pins sits in
`content/definitions/crds/`, one YAML document per file. `content/models/*.yaml` are `Model`
documents. Definitions load first, then models, each in file name order, so a shipped model may name
a shipped definition's type. The files load through the same loaders as an application's own, so a
CRD whose SHA-256 differs from its definition's pin refuses the load, and a refusal names the file.
A missing `content/models` directory holds no model.

## A shipped type is never replaced

`Set.Clash` refuses an application's definition that has the same authored type name as a shipped
definition, an application's model whose short name is one, and either that has the same identity as
a shipped definition or model. The refusal names the
type and the shipped file, and `kurel build` adds the application's file. Another version of a
shipped kind is another type and is not a clash. There is no override.

## Where the CRDs come from

A CRD is taken from a release asset, never a branch. The three files are documents of
`https://github.com/VictoriaMetrics/operator/releases/download/v0.76.0/crd.yaml` (6101924 bytes,
sha256 `9362fcf82df73584aa6d97651090be324475345e8e48495eedf810557f00af84`, the digest the release
lists), split with `yq` 4.54.3:

```sh
yq eval 'select(.metadata.name == "vmrules.operator.victoriametrics.com")' crd.yaml > content/definitions/crds/vmrules.yaml
```

The same command, with `vmpodscrapes` and `vmservicescrapes`, makes the other two. Each definition
pins its split file's digest. To move to another release, download that release's `crd.yaml`, split
the three files again and put the new digests in the definitions.

Only a CRD the loader accepts can ship. It refuses one whose `spec` is open (`VMAuth` and two other
kinds), so the instance kinds are not shipped.

## Not done

- The CRDs' CEL rules are not evaluated (see [Component definitions](https://github.com/go-kure/launcher/blob/main/pkg/oam/definition/README.md)).
- The whole set registers on every build, used or not.
