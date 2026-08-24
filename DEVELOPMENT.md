# Development Guide

This guide covers development workflows and tooling for the Launcher project.

## Quick Start

```bash
# Get help with all available commands
make help

# Quick development cycle
make check
```

## Prerequisites

- [mise](https://mise.jdx.dev) — tool version manager
- Go 1.26.2 (managed by mise)
- golangci-lint 2.13.1 (managed by mise)

```bash
# Install mise, then:
mise install
```

## Contributing Workflow

The `main` branch is protected — all changes must go through pull requests.

### Branch Workflow

1. **Create a feature branch** from `main`:
   ```bash
   git checkout -b feat/my-feature main
   ```
   Use branch prefixes: `feat/`, `fix/`, `docs/`, `chore/`

   Changing the shape or meaning of a `launcher.gokure.dev/v1alpha1` document
   (`app.yaml`/`kurel.yaml`/`cluster.yaml`, or a `CapabilityDefinition`)? Use the `format` commit scope
   (`feat(format):`, `fix(format):`, `docs(format):`) — it groups under the changelog's
   Document Format heading. See `docs/oam/design-gvk.md` § Document-Format Lifecycle.

2. **Develop and test locally**:
   ```bash
   make check       # Quick validation
   make precommit   # Full pre-commit checks
   ```

3. **Push and create a pull request**:
   ```bash
   git push -u origin feat/my-feature
   gh pr create
   ```

4. **Pass required CI checks**: `lint`, `test`, `build`

5. **Merge** via the merge queue (linear history required — rebase, no merge commits)

### Branch Protection Rules

Enforced via the `main-protection` repository ruleset:

- **Required status checks**: `lint`, `test`, `build`
- **Merge queue**: merging goes through a GitHub merge queue (rebase method) that rebases and tests the merged result before landing — no manual rebasing, no auto-rebase force-pushes
- **Pull requests required**: all changes must go through a PR
- **Conversation resolution**: all review threads must be resolved
- **Linear history**: enforced (rebase only, no merge commits)
- **Force pushes**: disabled
- **Branch deletion**: disabled
- **Bypass actors**: `kure-release-bot` (GitHub App) — allowed to push release commits directly

## Development Workflow

### 1. Initial Setup

```bash
# Install dependencies
make deps

# Install development tools
make tools
```

### 2. Development Cycle

```bash
# Format code
make fmt

# Run quick checks (lint, vet, short tests, kure dep sync, tool pins)
make check

# Run all tests
make test

# Run tests with coverage
make test-coverage
```

### 3. Building

```bash
# Build kurel
make build
```

### Validating example manifests against flux-schema

```bash
# Build examples/*.yaml and validate the output against fluxcd/flux-schema's
# default (embedded) catalog plus its ecosystem catalog (fetched from
# schemas.fluxoperator.dev — network access required). Requires the `flux` CLI
# (mise-managed, see mise.toml) — the script installs/upgrades the `schema` plugin itself.
make validate-manifests
```

This runs in CI as the non-required `validate-manifests` job (see
`docs/github-workflows.md`) — not yet in the required-checks list below while it
completes its first non-blocking cycle (go-kure/launcher#292).

### 4. Testing

```bash
# Run all tests
make test

# Run tests with verbose output
go test -v ./...

# Run tests with race detection
make test-race

# Run only short tests (good for quick feedback)
make test-short

# Run tests with coverage report
make test-coverage

# Run benchmark tests
make test-benchmark

# Run integration tests (when available)
make test-integration
```

### 5. Code Quality

```bash
# Run all linting
make lint

# Format code
make fmt

# Run go vet
make vet

# Tidy modules
make tidy
```

## Pre-commit Workflow

Before committing changes, run:

```bash
make precommit
```

This will:
- Format code with `go fmt` and `goimports`
- Tidy modules
- Run linters
- Run all tests
- Check kure dependency sync
- Check tool-version pins (golangci-lint, govulncheck) stay consistent across Makefile, CI and docs

## CI/CD Pipeline

The project uses GitHub Actions workflows:

### Main CI Pipeline (`.github/workflows/ci.yml`)

- **Triggers**: Push to main/develop, PRs, merge_group (merge queue)
- **Jobs**: validate (lint), test, security, coverage-check, build, cross-platform, analyze-changes
- **Runner**: `autops-kube` (self-hosted)

### Release Pipeline (`.github/workflows/release.yml`)

- **Triggers**: Version tags (`v*.*.*`)
- **Jobs**: test, validate (tag + changelog), goreleaser, post-release (proxy refresh)
- **Produces**: kurel binaries for linux × amd64/arm64 + checksums + SBOM + cosign signature

### Creating a Release

Releases are triggered by pushing a `vX.Y.Z` tag:

1. Update `CHANGELOG.md`: `make changelog` (or `git cliff -o CHANGELOG.md`)
2. Commit the changelog: `git commit -m "chore: update CHANGELOG for vX.Y.Z"`
3. Push to main and wait for CI to pass
4. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`

The pushed tag triggers the release pipeline which runs GoReleaser to produce binaries and publish a GitHub release.

## Renovate Management

Dependency updates come from Renovate (`renovate.json`, extending the shared
`go-kure/.github` preset). The **Dependency Dashboard issue** is the control
surface:

- **Gated updates** (every major, all Go-toolchain updates) sit under
  *Pending Approval* — tick the checkbox to let Renovate open the PR.
  Nothing gated is ever proposed on its own.
- **Deferring an update**: leave its dashboard checkbox unticked; there is
  nothing to close. To reopen a closed/ignored update, tick its checkbox on
  the dashboard.
- **Rebasing a PR**: tick the "rebase/retry" checkbox in the PR body, or the
  per-PR entry on the dashboard. Renovate also rebases automatically when the
  PR falls behind the base branch.
- **Closing a PR**: closing it normally tells Renovate not to recreate that
  version; the dashboard lists it under *Closed/Ignored*.

Direct dependencies shared with the imported kure are never proposed here at
all — launcher must not lead the kure release it imports (see `AGENTS.md`
§ shared dependencies; CI enforces it via `site/scripts/check-kure-dep-sync.sh`).
The disable list lives in `renovate.json` and must be kept in step with that
guard's shared-direct set.

## Makefile Targets Reference

### Development
- `help` - Display help message
- `info` - Display project information
- `clean` - Clean build artifacts and caches

### Dependencies
- `deps` - Download and tidy Go modules
- `deps-upgrade` - Upgrade all dependencies
- `tools` - Install development tools
- `outdated` - Check for outdated dependencies

### Building
- `build` / `build-kurel` - Build kurel executable
- `validate-manifests` - Build example manifests and validate against flux-schema

### Testing
- `test` - Run all tests
- `test-race` - Run tests with race detection
- `test-short` - Run short tests only
- `test-coverage` - Run tests with coverage report
- `test-benchmark` - Run benchmark tests
- `test-integration` - Run integration tests

### Code Quality
- `lint` - Run all linters
- `fmt` - Format Go code
- `vet` - Run go vet
- `tidy` - Tidy modules
- `vuln` - Run govulncheck

### CI/CD
- `check` - Quick code quality check
- `precommit` - Run all pre-commit checks
- `ci` - Run full CI pipeline

### Release
- `release TYPE=<type>` - Preview release (dry-run); types: alpha, beta, rc, stable
- `release-snapshot` - Test GoReleaser locally (no tag, no publish)
- `changelog` - Generate CHANGELOG.md from git history
- `changelog-preview` - Preview unreleased entries

## Active Linters

The `.golangci.yml` enables these linters (the full go-kure linter set):

| Linter | Category | Purpose |
|--------|----------|---------|
| `errcheck` | Default | Unchecked errors |
| `govet` | Default | Suspicious constructs |
| `ineffassign` | Default | Ineffectual assignments |
| `staticcheck` | Default | Comprehensive static analysis |
| `unused` | Default | Unused code |
| `bodyclose` | Required | HTTP response body closed |
| `durationcheck` | Required | time.Duration mistakes |
| `errorlint` | Required | Error wrapping issues |
| `exhaustive` | Required | Exhaustive enum switches |
| `misspell` | Required | Common misspellings |
| `nilerr` | Required | Nil error returns |
| `unconvert` | Required | Unnecessary conversions |
| `whitespace` | Required | Unnecessary whitespace |
| `gosec` | Optional | Security checks |

Formatters: `gofmt`, `goimports` (with `github.com/go-kure/launcher` as local prefix).

## Troubleshooting

### Build Issues

```bash
# Clean everything and rebuild
make clean
make build

# Check Go installation and environment
make info
```

### Test Failures

```bash
# Run tests with verbose output for more details
go test -v ./...

# Run specific test
go test -v ./pkg/specific/package -run TestSpecific
```

### Dependency Issues

```bash
# Update dependencies
make deps-upgrade

# Check for outdated or vulnerable dependencies
make outdated
make vuln
```
