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
- golangci-lint 2.10.1 (managed by mise)

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

4. **Pass required CI checks**: `lint`, `test`, `build`, `rebase-check`

5. **Merge** (linear history required — rebase, no merge commits)

### Branch Protection Rules

Enforced via the `main-protection` repository ruleset:

- **Required status checks** (strict): `lint`, `test`, `build`, `rebase-check`
- **Auto-rebase**: open PRs are automatically rebased when main is updated (via `auto-rebase.yml`)
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

# Run quick checks (lint, vet, short tests)
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

## CI/CD Pipeline

The project uses GitHub Actions workflows:

### Main CI Pipeline (`.github/workflows/ci.yml`)

- **Triggers**: Push to main/develop, PRs
- **Jobs**: rebase-check, validate (lint), test, security, coverage-check, build, cross-platform, analyze-changes
- **Runner**: `autops-kube` (self-hosted)

### Auto-Rebase (`.github/workflows/auto-rebase.yml`)

- **Triggers**: Push to main
- **Purpose**: Automatically rebases all open PRs targeting main
- **Auth**: Requires `AUTO_REBASE_PAT` secret

### Release Pipeline (`.github/workflows/release.yml`)

- **Triggers**: Version tags (`v*.*.*`)
- **Jobs**: test, validate (tag + changelog), goreleaser, post-release (proxy refresh)
- **Produces**: kurel binaries for linux/darwin/windows × amd64/arm64 + checksums + SBOM + cosign signature

### Creating a Release

Releases are triggered by pushing a `vX.Y.Z` tag:

1. Update `CHANGELOG.md`: `make changelog` (or `git cliff -o CHANGELOG.md`)
2. Commit the changelog: `git commit -m "chore: update CHANGELOG for vX.Y.Z"`
3. Push to main and wait for CI to pass
4. Tag: `git tag vX.Y.Z && git push origin vX.Y.Z`

The pushed tag triggers the release pipeline which runs GoReleaser to produce binaries and publish a GitHub release.

## Dependabot Management

Use `@dependabot` commands in PR comments:

| Command | Effect |
|---------|--------|
| `@dependabot close` | Close PR, prevent recreation |
| `@dependabot ignore this dependency` | Ignore dependency permanently |
| `@dependabot rebase` | Rebase the PR |

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

The `.golangci.yml` enables these linters, aligned with the Wharf standard (`meta/standards/golangci-lint.md`):

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
