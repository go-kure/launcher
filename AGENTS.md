# Launcher Agent Instructions

This document provides comprehensive guidance for AI agents working on the Launcher codebase.

## Project Overview

Launcher is an OAM-native package manager for Kubernetes that ships as the `kurel` CLI. It provides a two-config-set model: a package config (what the application needs) and a site config (how the cluster is configured), resolving them at install time to produce ready-to-apply Kubernetes manifests.

See `docs/design.md` for the full vision and architecture.

### Technology Stack

- **Language**: Go 1.26.2
- **CLI Tool**: kurel (OAM-native package manager)
- **Build System**: Makefile + mise for cross-repo consistency
- **CI/CD**: GitHub Actions (autops-kube runner)
- **Release**: GoReleaser with cosign signing and SBOM generation

### Architecture

- **Two-config-set model**: Package config (app requirements) + site config (cluster capabilities)
- **Resolution**: Merge configs at install time → produce Kubernetes manifests
- **Patch system**: JSONPath-based patching for site-specific customization
- **OAM alignment**: Follows Open Application Model semantics

## Repository Structure

```
launcher/
├── cmd/
│   └── kurel/        # kurel CLI entrypoint
├── pkg/
│   ├── launcher/     # Package launcher core (to be migrated from kure)
│   ├── patch/        # JSONPath-based patching (to be migrated from kure)
│   ├── cmd/
│   │   └── kurel/    # kurel command implementations
│   ├── errors/       # Error handling
│   └── logger/       # Structured logging
├── docs/             # Documentation
│   └── design.md     # Full design document and vision
├── .github/
│   ├── workflows/    # CI, release, auto-rebase
│   └── dependabot.yml
├── .claude/          # Claude Code configuration
├── mise.toml         # Tool versions and tasks
├── Makefile          # Build system
├── AGENTS.md         # This file
└── DEVELOPMENT.md    # Development workflow guide
```

> **Note**: The `pkg/launcher/`, `pkg/patch/`, `cmd/kurel/`, and `pkg/cmd/kurel/` packages will be migrated from `go-kure/kure` in a future PR (tracked in go-kure/launcher#1 and go-kure/kure#442).

## Development Workflow

### Setup

```bash
# Install tools via mise
mise install

# Build all executables
mise run build
# or: make build

# Run tests
mise run test
# or: make test
```

### Testing

```bash
# Run all tests
make test

# Run with verbose output
go test -v ./...

# Run tests with coverage
make test-coverage

# Run race detection tests
make test-race

# Quick test (short tests only)
make test-short

# Run integration tests
make test-integration
```

### Code Quality

```bash
# Run linting
make lint

# Format code
make fmt

# Run static analysis
make vet

# Run all quality checks
make precommit
```

### Building

```bash
# Build kurel
make build
# or: make build-kurel
```

### Pre-commit Workflow

Before committing changes:

```bash
# Quick check
make check

# Or comprehensive pre-commit
make precommit
```

## Git Workflow

- **`main` is protected** — never commit directly to `main`
- Always create a feature branch from `main` before making changes:
  ```bash
  git checkout -b <type>/<description> main
  ```
- **Branch prefixes**: `feat/`, `fix/`, `docs/`, `chore/`
- **Required CI checks** that must pass: `lint`, `test`, `build`, `rebase-check`
- **Auto-rebase**: open PRs are automatically rebased when main is updated
- **Linear history** enforced — rebase only, no merge commits
- **All conversations** must be resolved before merge
- Use `gh pr create` to open pull requests
- PR template: `.github/PULL_REQUEST_TEMPLATE.md`

## Code Conventions

### Function Naming

- **Constructors**: `Create<Type>()`
- **Adders**: `Add<Type><Field>()`
- **Setters**: `Set<Type><Field>()`
- **Helpers**: Descriptive names for utilities

### Error Handling

Always use `github.com/go-kure/launcher/pkg/errors` in application code — never call `fmt.Errorf` directly outside of `pkg/errors` itself. The `pkg/errors` package wraps `fmt.Errorf` internally; this is correct and expected.

```go
import "github.com/go-kure/launcher/pkg/errors"

// Preferred: use the errors package
return errors.Wrap(err, "context about what failed")
return errors.Wrapf(err, "failed to process %s", name)
return errors.New("description of error")
return errors.Errorf("invalid value: %s", val)

// Discouraged: raw fmt.Errorf in application code
// return fmt.Errorf("context: %w", err)   // use errors.Wrap instead
```

### Logging

Always use pkg/logger for logging:

```go
import "github.com/go-kure/launcher/pkg/logger"

logger.Info("message", "key", value)
logger.Error("message", "error", err)
```

### Testing Patterns

```go
func TestCreate<Type>(t *testing.T) {
    obj := Create<Type>("test")
    if obj == nil {
        t.Fatal("expected non-nil object")
    }
    // Validate required fields...
}
```

Table-driven tests for multiple inputs:

```go
func TestResolve(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid", "input", "expected", false},
        {"invalid", "bad", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

### Documentation

- Add package documentation in `doc.go` files
- Use GoDoc conventions for function comments
- Include examples in function documentation

## Security Considerations

### Secret Management

- **Never hardcode secrets** in builders
- Always reference secrets through Kubernetes Secret objects
- Use `SecretKeySelector` and `LocalObjectReference` patterns

### CLI Safety

- Validate all user-provided paths before use
- Reject paths that escape the working directory
- Use `gosec` exclusions only where explicitly safe (see `.golangci.yml`)

## Troubleshooting

### Common Issues

1. **Import Errors**: Check `go.mod` for correct versions
2. **Test Failures**: Ensure all required fields are set in constructors
3. **golangci-lint version mismatch**: If lint fails with "Go language version used to build golangci-lint is lower than the targeted Go version", update the golangci-lint version in both `mise.toml` and `Makefile`. When bumping Go, always check that golangci-lint is built with a compatible Go version.
4. **Stale GOPATH binaries shadowing mise**: The Makefile appends (not prepends) `GOPATH/bin` to PATH so mise-managed tools take precedence. If you see unexpected tool versions, check `which <tool>` vs `mise which <tool>`.

### Debugging Tips

- Check test output for validation errors
- Verify Kubernetes API versions in dependencies

## Implementation Workflow

When implementing a GitHub issue, follow this checklist in order:

1. **Branch** — create a feature branch from latest `main` before writing any code.
2. **Validate the issue** — compare the issue description against project standards (naming conventions, error handling, package placement). Question anything that conflicts before implementing.
3. **Implement with tests** — write or update tests next to every new or changed function.
4. **Run all checks** — execute `make precommit` and fix any failures. When all checks pass, stop and ask for a user review.
5. **Iterate on review feedback** — address every comment, then return to step 4.
6. **Verify the diff** — before committing, review the full working-tree diff. If there are more changes than expected, ask the user what should be committed.
7. **Commit, push, PR** — commit with a conventional-commit message, push, and open a PR with `gh pr create`.

## Questions?

Refer to:
1. `DEVELOPMENT.md` - Detailed development workflow
2. `docs/design.md` - Full design document and vision
