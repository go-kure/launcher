# launcher

[![CI](https://github.com/go-kure/launcher/actions/workflows/ci.yml/badge.svg)](https://github.com/go-kure/launcher/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.26.2-blue)](go.mod)

OAM-native package manager for Kubernetes.

`kurel` uses a two-config-set model: a **package config** (what the application needs) and a **site config** (how your cluster is configured). At install time, `kurel` resolves them together and produces ready-to-apply Kubernetes manifests.

See [docs/design.md](docs/design.md) for the full design and vision.

## Installation

Binary releases are not yet available. Check back after the first tagged release.

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md) for setup and workflow.

## Contributing

Contributions are welcome. Please open an issue before submitting a pull request for significant changes.

This project follows [Conventional Commits](https://www.conventionalcommits.org/). Linear history is enforced — use rebase, not merge commits.
