# OAM Built-in Rendering Schemas

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin)

Package `builtin` holds the rendering-schema types shared by the built-in capability
handlers (e.g. `CertificateRendering`, `ExposeRendering`, `ExternalSecretRendering`,
`NetworkPolicyRendering`, `ConfigmapRendering`, `VolSyncRendering`, `PVCRendering`) and the
`DecodeStrict[T]` helper used by handlers to decode capability properties.

`DecodeStrict` decodes through yaml.v3, which keys on yaml tags and otherwise on the lowercased
Go field name, so it suits launcher's own schema types only. An external API type that carries
json tags only (Flux, Cilium) is decoded with `DecodeStrictJSON[T](props, owned...)` instead:
the launcher-owned keys named in `owned` are split off and returned to the caller, and the rest
is JSON-decoded into `T` with `DisallowUnknownFields`, so a misspelt key at any depth or a
wrongly typed value is an error rather than a dropped field. It decodes only: no defaulting, no
semantic checks. It shares one limitation with `encoding/json`: unknown keys nested inside a
type that has its own `UnmarshalJSON` are still dropped. `UnreachableJSONFields(type, owned...)`
lists the spec fields an author cannot set that way (tagged `json:"-"`, or shadowed by an owned
key), including fields promoted from embedded structs; like `encoding/json`, it visits each
embedded struct type once, so a type that embeds itself is safe to pass. A handler that
decodes an external spec type asserts that list is empty, against an
explicit exclusion list, so an upstream field added under a name launcher already owns fails
the test instead of silently becoming unreachable. The `cilium-networkpolicy` trait decodes its
raw rules this way.

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

These are internal schema types used by [`builtin/components`](components) and
[`builtin/traits`](traits); they are not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin) for the
full exported surface. Full reference deferred (see #145 PR-B).
