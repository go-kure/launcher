# Design spike: external-secret `data[]` shorthand

Status: accepted (launcher#199). Companion downstream: a downstream runtime's
external-secret migration.

## Problem

External-secret `data[]` entries are the single largest authored-OAM boilerplate in
the reference scenario — ~390 removable lines, 58% of the external-secret volume
(measured in the downstream migration review). Each entry authors 4 lines
(`secretKey` + `remoteRef.{key,property}`), but the values are near-perfectly
derivable. Across all 130 `data` entries in opsmaster's 37 external-secret blocks:

- `remoteRef.property == secretKey` — 130/130 (100%)
- `remoteRef.key == "<namespace>/<secretName>"` — 126/130 (97%)

## Decision

Extend the `data[]` entry with **derivation by absence**, keeping the entry an
**object** (no new string form, no schema-union). A fully-conforming entry collapses
to one line:

```yaml
data:
  - secretKey: DB_PASSWORD          # derives remoteRef.key + remoteRef.property
  - secretKey: TOKEN                # key override; property still derived
    remoteRef: {key: shared/token}
```

### Derivation rules

| field | required | derived when absent |
|---|---|---|
| `secretKey` | yes | — |
| `remoteRef` | no | synthesized `{}` |
| `remoteRef.key` | no | `"<app.Namespace>/<secretName>"` |
| `remoteRef.property` | no | `secretKey` |
| `remoteRef.version` | no | (none — emitted only when authored) |
| `remoteRef.decodingStrategy` | no | (none — emitted only when authored) |

`<secretName>` is the ExternalSecret name (`config.SecretName`); the namespace is the
build namespace (`app.Namespace`). Both are in scope at parse time. The pre-existing
top-level single-entry `remoteRef` object shorthand and its mutual-exclusion with
`data`/`dataFrom` are unchanged.

### Strict mode — reject unknown fields

Unknown keys in a `data` entry, or inside `remoteRef`, are **rejected** with an error
naming the offending field and listing the supported ones (`unsupported field "X"
(supported: …)`).

This is load-bearing, not house style. Because absence is now meaningful, lenient
parsing would silently *rewrite meaning*: a typo'd `remteRef:` would be ignored, the
parser would see `remoteRef` absent, derive `<namespace>/<secretName>`, and the app
would fetch the **wrong secret path** — discovered at runtime, in-cluster, on auth
material. Strict rejection is the only safe complement to defaulting-by-absence, and
it matches the downstream strict-mode charter and the #323 reject-over-ignore pins. There
are no existing users to grandfather; the opsmaster fixtures conform.

The error names the *supported* fields rather than calling the key a "typo", because
ExternalSecrets' `RemoteRef` carries fields this handler does not model
(`conversionStrategy`, `metadataPolicy` — unused by opsmaster); an author reaching for
a real-but-unhandled field gets an accurate message.

### Edge cases (the 4/130 non-conforming)

The 4 entries whose `remoteRef.key` differs from `<namespace>/<secretName>` author
`remoteRef.key` explicitly; the explicit value wins (no derivation for an authored
field). Property overrides work the same way. These are the explicit-override test
cases in `external_secret_test.go`.

## Rejected alternatives

- **String-list form** (`data: [DB_PASSWORD, …]`). The win is the derivation, not the
  syntax: `- secretKey: DB_PASSWORD` already captures essentially the whole line
  reduction. A string list buys marginal terseness at the cost of a string-or-object
  union that exists nowhere else in the trait vocabulary.
- **`OneOf`/union in `PropertySchema`** to model that union. A cross-repo vocabulary
  extension (schema + downstream validator + docgen rendering) for a single foreseeable
  user does not pay for itself; if a second union case appears, `OneOf` is an additive
  extension to add then.
- **Parser-only string form** (accept string, don't model it in the schema). The
  schema is the SSOT the downstream validator consumes; letting it under-describe accepted
  input reverses the #235 schema-SSOT direction and makes the generated handler
  reference narrower than reality.

## Schema impact

`data.Items.remoteRef` becomes **optional** with `key`/`property` optional (a
`dataRemoteRef` variant); `secretKey` stays required. The top-level `remoteRef`
shorthand keeps its key-required schema. No `PropertySchema` vocabulary change, so
the downstream validator and docgen are untouched.
