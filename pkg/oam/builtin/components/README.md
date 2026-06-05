# OAM Built-in Component Handlers

[![Go Reference](https://pkg.go.dev/badge/github.com/go-kure/launcher/pkg/oam/builtin/components.svg)](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)

Package `components` implements `oam.ComponentHandler` for the built-in OAM component
types — `webservice`, `worker`, `statefulset`, `daemonset`, `cronjob`, `helmchart`,
`oci`, `postgresql`, `passthrough`, and CRD passthrough. Each handler parses its
typed config (e.g. `WebserviceConfig`, `CronjobConfig`, `HelmchartConfig`) and
produces the corresponding Kubernetes resources via kure's builders.

This is an **internal builder surface** for the `kurel` CLI and the primary extension
point for custom component types (implement `oam.ComponentHandler`). A full
per-symbol reference is deferred; see
[pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components)
and the examples in `examples/` for usage. (#145 PR-B will publish the full reference.)
