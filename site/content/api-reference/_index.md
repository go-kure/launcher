---
title: "API Reference"
weight: 35
---

Reference documentation for launcher's public Go packages. Each page below is
auto-synced from the package `README.md`.

For the full Go API, see
[pkg.go.dev/github.com/go-kure/launcher](https://pkg.go.dev/github.com/go-kure/launcher).

<!-- The tables below are generated from site/docs-map.yaml. Do not edit by hand;
     run: bash site/scripts/gen-docs-tables.sh -->
<!-- BEGIN GENERATED: api-reference-nav (source: site/docs-map.yaml) -->
## CLI

| Package | Description | Reference |
|---------|-------------|-----------|
| [kurel CLI](kurel-cli) | kurel command tree, flags, and usage | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/cmd/kurel) |

## OAM

| Package | Description | Reference |
|---------|-------------|-----------|
| [OAM Model](oam) | OAM data model, parser, and transform pipeline | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam) |
| [Component Handlers](oam-components) | Built-in component types (webservice, worker, cronjob, helmchart, …) | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/components) |
| [Trait Handlers](oam-traits) | Built-in traits (ingress, certificate, scaler, externalsecret, …) | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/oam/builtin/traits) |

## Libraries

| Package | Description | Reference |
|---------|-------------|-----------|
| [Errors](errors) | Structured error types and wrapping helpers | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/errors) |
| [Patch Engine](patch) | Declarative JSONPath patching (TOML/YAML, strategic merge) | [pkg.go.dev](https://pkg.go.dev/github.com/go-kure/launcher/pkg/patch) |
<!-- END GENERATED: api-reference-nav -->
