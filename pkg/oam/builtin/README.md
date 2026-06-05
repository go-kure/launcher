# OAM Built-in Rendering Schemas

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin)

Package `builtin` holds the rendering-schema types shared by the built-in capability
handlers (e.g. `CertificateRendering`, `ExposeRendering`, `ExternalSecretRendering`,
`NetworkPolicyRendering`, `ConfigmapRendering`, `VolSyncRendering`) and the
`DecodeStrict[T]` helper used by handlers to decode capability properties.

These are internal schema types used by [`builtin/components`](components) and
[`builtin/traits`](traits); they are not a user-facing API. See
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin) for the
full exported surface. Full reference deferred (see #145 PR-B).
