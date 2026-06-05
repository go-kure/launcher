# OAM Built-in Trait Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/traits.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)

Package `traits` implements `oam.TraitHandler` for the built-in OAM trait types,
covering networking (`ingress`, `httproute`, `expose`, network policy / Cilium),
security (`rbac`, `certificate`, `externalsecret`), storage (`pvc`, `volsync`),
config (`configmap`), scaling (`scaler`), and operational concerns (FluxCD patches,
prune protection, post-build). Each handler parses its typed config (e.g.
`IngressConfig`, `HTTPRouteConfig`, `CertificateConfig`, `ScalerConfig`) and decorates
or emits Kubernetes resources accordingly.

This is an **internal builder surface** for the `kurel` CLI and the extension point
for custom traits (implement `oam.TraitHandler`). A full per-symbol reference is
deferred; see
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits)
and `examples/` for usage. (#145 PR-B will publish the full reference.)
