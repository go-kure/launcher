# OAM Built-in Rendering Schemas

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin)

Package `builtin` holds the rendering-schema types shared by the built-in capability
handlers (e.g. `CertificateRendering`, `ExposeRendering`, `ExternalSecretRendering`,
`NetworkPolicyRendering`, `ConfigmapRendering`, `VolSyncRendering`, `PVCRendering`) and the
`DecodeStrict[T]` helper used by handlers to decode capability properties.

`VolSyncRendering` and `PVCRendering` carry platform-supplied storage-class defaults
(`storageClassName`, plus `volumeSnapshotClassName` for volsync) that a ClusterProfile capability
can inject; both are overridable by the matching inline trait property and strict-decoded, so an
operator typo in the rendering fails at profile-load.

`ExposeRendering` additionally carries `certManagerClusterIssuer` (platform-managed TLS on
the ingress path), `allowedHostnameWildcard` (hostname constraint for both paths), the
ingress-only `sslRedirect` / `forceSslRedirect` platform defaults (author-overridable via the
matching inline expose properties), and the ingress-only external-auth facts `authURL` /
`authSigninURL` / `authResponseHeaders` (consumed when an expose trait authors `allowedGroups`;
`authSigninURL` is override-able inline).

These are internal schema types used by [`builtin/components`](components) and
[`builtin/traits`](traits); they are not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin) for the
full exported surface. Full reference deferred (see #145 PR-B).
