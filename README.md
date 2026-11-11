# launcher

[![CI](https://github.com/go-kure/launcher/actions/workflows/ci.yml/badge.svg)](https://github.com/go-kure/launcher/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.27.4-blue)](go.mod)

launcher is a Go library that translates OAM-style application documents into Kubernetes objects (through [kure](https://github.com/go-kure/kure)), and `kurel` is the supported command-line tool built on it. A downstream consumer can call the library directly and add its own components, traits and policies.

`kurel` uses a two-config-set model: a **package config** (what the application needs) and a **site config** (how your cluster is configured). At build time, `kurel` resolves them together and produces ready-to-apply Kubernetes manifests.

## What is supported

launcher is pre-release: output, names and the library contract may change.

| Surface | Status |
|---|---|
| Go library (`pkg/oam` and `pkg/oam/builtin/registry`) | Supported. Translates the components of one application into kure's model and writes no files. |
| `kurel build` | Supported. Builds one `app.yaml` or package directory into static manifests. |
| `pkg/patch` | A standalone library for callers that patch Kubernetes resources in Go. `kurel build` does not use it. |
| Delivery (Flux Kustomizations, health checks, reconciliation settings) | Not launcher's job. A downstream consumer that calls the library owns it. |
| A `--policy` flag on `kurel build`, a `kurel` patch command | Not implemented. |

See [docs/delivery-scope.md](docs/delivery-scope.md) for the layer roles and [docs/design.md](docs/design.md) for the full design and vision.

## Installation

Each [release](https://github.com/go-kure/launcher/releases) carries signed `kurel` binaries for
Linux (amd64, arm64). The [install guide](site/content/getting-started/install.md) shows how to
verify and install one, how to build from a checkout, and what a module that imports launcher as a
library must copy into its `go.mod`.

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md) for setup and workflow.

## Contributing

Contributions are welcome. Please open an issue before submitting a pull request for significant changes.

This project follows [Conventional Commits](https://www.conventionalcommits.org/). Linear history is enforced — use rebase, not merge commits.
