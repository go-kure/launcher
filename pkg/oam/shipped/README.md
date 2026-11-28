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

### Definitions

| Type | Emits | CRD |
|---|---|---|
| `vm-rule` | `VMRule` (`operator.victoriametrics.com/v1beta1`) | VictoriaMetrics operator release v0.76.0 |
| `vm-service-scrape` | `VMServiceScrape` | the same release |
| `vm-pod-scrape` | `VMPodScrape` | the same release |

A model names such a kind by its identity, `<kind>.<group>@<version>`
(`vmrule.operator.victoriametrics.com@v1beta1`).

### Models

| Model | Writes | Detail |
|---|---|---|
| `s3-access.launcher.gokure.dev@v1` | the settings of an S3 bucket as a ConfigMap and, by the credentials route, the credentials as a Secret or an ExternalSecret | [The s3-access model](#the-s3-access-model) |

An application authors a shipped model by its identity, with no `models/` directory
(`examples/s3-access`).

## Where it lives and how it loads

`content/definitions/*.yaml` are `ComponentDefinition` documents, and the CRD each pins sits in
`content/definitions/crds/`, one YAML document per file. `content/models/*.yaml` are `Model`
documents. Definitions load first, then models, each in file name order, so a shipped model may name
a shipped definition's type. The files load through the same loaders as an application's own, so a
CRD whose SHA-256 differs from its definition's pin refuses the load, and a refusal names the file.
A missing `content/models` directory holds no model. A shipped model registers with
`Registry.RegisterLazy`: its expressions compile at the first build that uses it, not at every load, because
every `kurel build` loads this set and compiling one model cost about 4 ms in a loaded process and about 22 ms
more in a fresh `kurel build` (measured on one host, go-kure/launcher#1748). A test compiles each embedded model, so a model that
does not compile fails CI, and a first use that finds one names the shipped file. A shipped model's file is
also decoded once for the process, because a decoded model is read-only.

## A shipped type is never replaced

`Set.Clash` refuses an application's definition that has the same authored type name as a shipped
definition, an application's model whose short name is one, and either that has the same identity as
a shipped definition or model. The refusal names the
type and the shipped file, and `kurel build` adds the application's file. Another version of a
shipped kind is another type and is not a clash. There is no override.

## The s3-access model

`s3-access.launcher.gokure.dev@v1` (go-kure/launcher#1738) says how to reach an S3 bucket, whatever
S3 is behind it: a ConfigMap of the settings and, by the credentials route, the credentials in the
keys a consumer reads. It creates no bucket (that is backend specific) and mounts nothing. A component
of its type takes these inputs:

| Input | Type | Default | Meaning |
|---|---|---|---|
| `endpoint` | string | required | The endpoint URL, for example `https://s3.example:9000` |
| `region` | string | `us-east-1` | |
| `bucket` | string | required | |
| `prefix` | string | `""` | A path inside the bucket |
| `pathStyle` | boolean | `true` | Path-style addressing, because a custom endpoint rarely has wildcard DNS |
| `caSecret` | `{name, key}` | none | A Secret that holds the endpoint's CA; `key` defaults to `ca.crt`. Only named in the ConfigMap |
| `credentials` | object | required | Exactly one of the three routes below |

**Credentials routes.** `credentials` holds exactly one of these:

| Route | Writes | Under a policy that forbids explicit secrets |
|---|---|---|
| `existingSecret: <name>` | the ConfigMap only; the credentials stay in the Secret you made | allowed |
| `externalSecret: {storeRef, accessKeyId, secretAccessKey, format, resticPassword, keys}` | the ConfigMap and an ExternalSecret whose template gives the keys of the format. `storeRef` is `{name, kind}` (`kind` defaults to `ClusterSecretStore`); each credential is a remote reference `{key, property}` | allowed |
| `values: {accessKeyId, secretAccessKey, format, resticPassword, keys}` | the ConfigMap and a Secret holding the values | refused with class `RefusalExplicitSecret` (the `secret` kind's refusal) |

**`format`** names the keys the credentials take. It defaults to `plain`. It is an input of `values` and
of `externalSecret` only, never of `existingSecret`: the model cannot re-key a Secret it did not write
(whether the External Secrets Operator could do it is not confirmed), so a `format` beside
`existingSecret` is refused as an unknown field, not ignored. The design page proposed a top-level
`format`; it sits inside the two routes so that it cannot be set where it does nothing.

| `format` | Keys written | Source |
|---|---|---|
| `plain` | `access-key-id`, `secret-access-key`, `endpoint`, `bucket-name` | the model's own names, the first two as both Garage operators write them |
| `aws-env` | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `AWS_ENDPOINT_URL_S3` | the AWS SDK reference guide. `AWS_ENDPOINT_URL_S3` is derived from the `AWS_ENDPOINT_URL_<SERVICE>` pattern, not printed there |
| `credentials-file` | one key, `credentials`, holding an AWS shared credentials file (`[default]`, `aws_access_key_id`, `aws_secret_access_key`) | the AWS SDK reference guide, shared credentials file |
| `restic` | `RESTIC_REPOSITORY` (`s3:<endpoint>/<bucket>[/<prefix>]`), `RESTIC_PASSWORD`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | the VolSync restic documentation, for the `volsync` trait's repository Secret |
| `barman` | `ACCESS_KEY_ID`, `ACCESS_SECRET_KEY` | the Barman Cloud plugin's object store page, for a `cnpg-objectstore`'s `s3Credentials` |
| `custom` | the two key names you give in `keys` (`accessKeyId`, `secretAccessKey`) | none |

`resticPassword` is required when `format` is `restic`: it is the repository's encryption password and
not an S3 setting, but VolSync reads it from the same Secret, which you cannot add a key to. On the
`values` route it is a string; on the `externalSecret` route a remote reference. `keys` is required when
`format` is `custom`.

**The ConfigMap** `<component>-settings` always holds `endpoint`, `region`, `bucket-name`, `prefix`,
`path-style` (`true` or `false`), `ca-secret-name`, `ca-secret-key` (both empty without `caSecret`) and
`credentials-secret`, the name of the Secret to read: yours, the one the model writes, or the one the
ExternalSecret creates (it takes the ExternalSecret's name). The model publishes the ConfigMap's name as
its output `settings`.

**Not shipped:** a session token; the Velero credentials format (not confirmed); a bucket; a mount of the
CA. A nested input's schema default is not applied by the loader, so the model reads an absent `format`
as `plain` itself.

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
