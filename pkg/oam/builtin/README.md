# OAM Built-in Rendering Schemas

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin)

Package `builtin` holds the rendering-schema types shared by the built-in capability
handlers (e.g. `CertificateRendering`, `ExposeRendering`, `ExternalSecretRendering`,
`NetworkPolicyRendering`, `ConfigmapRendering`, `SecretRendering`, `TopologySpreadRendering`, `VolSyncRendering`,
`PVCRendering`) and the `DecodeStrict[T]` helper used by handlers to decode capability
properties.

`DecodeStrict` decodes through yaml.v3, which keys on yaml tags and otherwise on the lowercased
Go field name, so it suits launcher's own schema types only. An external API type that carries
json tags only (Flux, Cilium) is decoded with `DecodeStrictJSON[T](props, owned...)` instead:
the launcher-owned keys named in `owned` (matched case-insensitively, as `encoding/json` matches
fields) are split off and returned to the caller, and the rest is JSON-decoded into `T` with
`DisallowUnknownFields`, so a misspelt key at any depth or a wrongly typed value is an error
rather than a dropped field; a number in an interface-typed field stays an exact `json.Number`.
It decodes only: no defaulting, no semantic checks. It shares one limitation with
`encoding/json`: unknown keys nested inside a type that has its own `UnmarshalJSON` are still
dropped. `UnreachableJSONFields(type, owned...)` lists the spec keys an author cannot use that
way (tagged `json:"-"`, refused by `encoding/json` itself such as an ambiguously promoted key,
shadowed by an owned key, or behind an unexported embedded pointer the decoder cannot set),
including embedded ones; each key is probed against `encoding/json`, so the list agrees with
the decoder. A field whose key works but lands on another field (one a shallower field
dominates, or one dropped as ambiguous whose key folds onto a field equal ignoring case) is
reported by its Go field path, e.g. `Inner.Value`; such a field is checked by filling it alone
and confirming `encoding/json` encodes it, and one the check cannot prove reachable is reported
too, as is every such field of a type with its own (or a promoted) `MarshalJSON`, `MarshalText`,
`UnmarshalJSON` or `UnmarshalText`, or the `AppendText`, `MarshalJSONTo` or `UnmarshalJSONFrom`
that the jsonv2-backed `encoding/json` (`GOEXPERIMENT=jsonv2`) also calls. A handler that
decodes an external spec type asserts that list is empty, against an explicit exclusion list, so
an upstream field added under a name launcher already owns fails the test instead of silently
becoming unreachable. The `cilium-networkpolicy` trait decodes its raw rules this way.

`VolSyncRendering` and `PVCRendering` carry platform-supplied storage-class defaults
(`storageClassName`, plus `volumeSnapshotClassName` for volsync) that a ClusterProfile capability
can inject; both are overridable by the matching inline trait property and strict-decoded, so an
operator typo in the rendering fails at profile-load.

`ExposeRendering` additionally carries `certManagerClusterIssuer` (platform-managed TLS on
the ingress path), `allowedHostnameWildcard` (hostname constraint for both paths), the
ingress-only `sslRedirect` / `forceSslRedirect` platform defaults (author-overridable via the
matching inline expose properties), the ingress-only external-auth facts `authURL` /
`authSigninURL` / `authResponseHeaders` (consumed when an expose trait authors `allowedGroups`;
`authSigninURL` is override-able inline), and `networkPolicy` (the platform-reserved
`trafficSources` object also declared on `ingress`/`httproute`; kept untyped here since its shape
is validated later, on the merged trait properties, by `parseTrafficSources` in
`pkg/oam/builtin/traits`, not by `ExposeRendering` itself).

`ContractVersion` (`v1alpha1`) is the contract version every built-in handler and lowering
rule declares in its `oam.ContractMetadata` (go-kure/launcher#789). The family is the type
name the built-in is registered under, so the version alone is shared; a built-in rule's
identity reads `component/webservice@v1alpha1`. See Contract metadata in
[`pkg/oam`](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) for the scheme.

These are internal schema types used by [`builtin/components`](components) and
[`builtin/traits`](traits); they are not a user-facing API. The sibling
[`builtin/policies`](policies) package holds the built-in application policy handlers
(`dependency`, `placement`); it declares its property
schemas directly and uses none of these rendering types. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin) for the
full exported surface. Full reference deferred (see go-kure/launcher#145 PR-B).
