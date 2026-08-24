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
- **Patch library**: standalone JSONPath-based patching (`pkg/patch`), not part of the build pipeline — explicit, user-invoked resource customization
- **OAM alignment**: Follows Open Application Model semantics

## Repository Structure

```
launcher/
├── cmd/
│   └── kurel/        # kurel CLI binary entrypoint (package main)
├── pkg/
│   ├── cmd/
│   │   ├── kurel/    # kurel command tree (build, config, completion, version)
│   │   └── shared/   # shared CLI builders + global options
│   ├── errors/       # structured error types + wrapping helpers
│   ├── oam/          # OAM model, parser, transformer (+ builtin/ handlers, netpol/)
│   └── patch/        # JSONPath-based patching (TOML/YAML, strategic merge), standalone
├── docs/             # Documentation (design.md, oam/ specs, github-workflows)
├── examples/         # Runnable example applications and cluster profiles
├── site/             # Hugo docs site (docs-map.yaml, scripts/, content/)
├── .github/
│   └── workflows/    # CI, deploy-docs, release, pr-review
├── renovate.json     # Renovate config (extends the shared go-kure/.github preset)
├── .claude/          # Claude Code configuration
├── mise.toml         # Tool versions and tasks
├── Makefile          # Build system
├── AGENTS.md         # This file
└── DEVELOPMENT.md    # Development workflow guide
```

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
- **Required CI checks** that must pass: `lint`, `test`, `build`
- **Merge queue**: merging goes through a GitHub merge queue (rebase method) that rebases and tests the merged result before landing — no manual rebasing needed
- **Linear history** enforced — rebase only, no merge commits
- **All conversations** must be resolved before merge
- Use `gh pr create` to open pull requests
- PR template: `.github/PULL_REQUEST_TEMPLATE.md`
- **Document-format changes**: a commit that changes the shape or meaning of a
  `launcher.gokure.dev/v1alpha1` document (`app.yaml`/`kurel.yaml`/`cluster.yaml`, or a
  `CapabilityDefinition`) uses the `format` scope (`feat(format):`, `fix(format):`,
  `docs(format):`) so it lands under the
  changelog's Document Format heading — see `docs/oam/design-gvk.md` § Document-Format
  Lifecycle for the additive/breaking test and the version-string discipline it implies

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

### Documentation sync (mandatory)

**Code and documentation changes must be in the same PR.** When you change a
package, update its `README.md` and any affected guides/site docs in the same PR;
removing or renaming a package or symbol must repoint every reference. This is the
go-kure organization documentation-sync standard (`go-kure/.github` →
`docs/standards.md`), which is CI-enforced.

`site/docs-map.yaml` is the single source of the code→docs mapping. The site mounts,
the reverse-mapping table below, and the api-reference nav are generated from or
validated against it — never hand-edit them. To change what's published, edit
`docs-map.yaml` and run `bash site/scripts/gen-docs-tables.sh`. Enforcement runs the
canonical scripts from `go-kure/.github` via composite actions (launcher no longer
vendors copies): `check-doc-sync` (structure, blocking), `check-links` (rendered
internal links, blocking), and the `doc-gate` job (a mapped package's source change
must touch its docs; bypass only via the maintainer-restricted `docs-skip` label).

### No downstream references (mandatory)

Launcher is an open-source go-kure project and must not name downstream, closed-source
platform consumers in tracked source, docs, comments, tests, or identifiers. Reword any
such reference to a generic role (e.g. "a downstream consumer" / "the downstream runtime");
never introduce a new one. This is the go-kure organization "No Downstream References"
standard (`go-kure/.github` → `docs/standards.md`), CI-enforced here via the shared
`go-kure/.github` `check-forbidden-terms` action, which scans `--full-tree` on **every** event
(pull request and merge queue alike) so the two never diverge; `scripts/release.sh` runs a
byte-identical vendored copy (`site/scripts/check-forbidden-terms.sh`) as a release preflight. A
legitimately unavoidable term takes an adjacent `allow-term:<word>` pragma. The remediation
runbook is `go-kure/.github` → `docs/no-downstream-references.md`.

### Shared dependencies with kure (mandatory)

Launcher imports `github.com/go-kure/kure` and shares several third-party dependencies with
it (cert-manager, fluxcd controller APIs, cloudnative-pg, external-secrets/apis, gateway-api,
…) that launcher's own code also imports directly.

**Why it matters.** Go's Minimum Version Selection makes launcher's effective version of a
shared dep `max(launcher require, kure require)`. Launcher can therefore never fall *below*
the kure it imports (MVS pulls it up), so the only manageable direction is launcher racing
*ahead* — compiling kure's own code against a dependency version kure never released against.

**The rule.** Launcher must not *newly* lead the kure release it imports on a shared **direct**
dependency. Only deps that are direct requires of **both** go.mods are in scope; deps unique
to either repo, and indirect deps (MVS-governed — they float to the max across launcher's whole
graph), are unconstrained. Existing drift is grandfathered.

**How it's enforced.** `site/scripts/check-kure-dep-sync.sh` (Go helper in
`site/scripts/kuredepsync/`) runs in CI's `validate` job: diff-scoped and blocking on PRs /
merge-queue (fails only when a change introduces or increases the lead), `--report`-only on
push/schedule. Also wired into `make check` / `make precommit`. Renovate never proposes
these deps in the first place: `renovate.json` carries an `enabled: false` rule listing the
shared-direct set — keep that list in step with go.mod changes that add or drop a shared
direct dep (a missing entry is still caught by the CI guard, just as a red PR).

**Exception path.** Never lead unilaterally. To adopt a newer shared dep: land the matching
kure bump → cut a kure release → bump launcher's `go-kure/kure` require to it and take the
shared-dep bump together (yields no lead → guard passes).

### Reverse Mapping: Code to Docs

This table is generated from `site/docs-map.yaml`. Do not edit it by hand — edit the
map and run `bash site/scripts/gen-docs-tables.sh`.

<!-- BEGIN GENERATED: reverse-mapping (source: site/docs-map.yaml) -->
| Package Changed | Auto-Synced (README) | Guides to Review |
|-----------------|---------------------|------------------|
| `pkg/cmd/kurel/` | `api-reference/kurel-cli` | — |
| `pkg/errors/` | `api-reference/errors` | — |
| `pkg/patch/` | `api-reference/patch` | — |
| `pkg/oam/` | `api-reference/oam` | — |
| `pkg/oam/builtin/components/` | `api-reference/oam-components` | — |
| `pkg/oam/builtin/traits/` | `api-reference/oam-traits` | — |
| `.github/workflows/` | — | `contributing/github-workflows` |
<!-- END GENERATED: reverse-mapping -->

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
4. **Stale GOPATH binaries shadowing mise**: The `lint` target prepends `GOPATH/bin` to PATH so the golangci-lint version it just verified or reinstalled against `GOLANGCI_LINT_VERSION` is always the one that runs, even when a differently-versioned binary (e.g. mise-managed) is earlier on `PATH`. If you see unexpected tool versions elsewhere, check `which <tool>` vs `mise which <tool>`.

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

## Agent gates (A1–A7)

Process rules for AI agents (Claude Code, Codex, and any other). They constrain *how* work is
done and are independent of any particular tool, harness or machine — everything below is
checkable from a clone of this repository.

| Gate | Rule | Relation to the workflow above |
|---|---|---|
| **A1** | Every factual claim in a plan or review carries a `file:line` actually read this session; anything uncitable goes in an explicit `ASSUMPTIONS` list. Recompute every number from source. Never cite your own uncommitted change as evidence of existing convention — check the base branch. | Sharpens step 2 — validate the issue against real code, not against its own description |
| **A2** | Destructive operations require a *proven* dry-run, not an asserted one: print the exact command, then show the dry-run ran and what it output. A tool *accepting* `--dry-run` is not proof it honoured it. If it cannot be proven, say so and stop. | Rarely applies here (no live cluster in this repo's own scope), but binds any tooling change that touches one |
| **A3** | "Stop" / "wait" means make no further edits and no further tool calls. One-line acknowledgement only. | Step 4 already stops for review — honour it literally |
| **A4** | No merge-ready claim without per-item evidence. For launcher: `mise run verify` (tidy, lint, test, kure dep sync) or `make precommit`, plus code-and-docs in the same PR per `docs/standards.md`. | Step 4, with output quoted rather than asserted |
| **A5** | Re-read your own diff for the recurring defect classes: `t.Cleanup` LIFO ordering, `$?` misuse around `if ! cmd`, partial `sed` block removals, cross-test contamination from undisposed resources, and `check-kure-dep-sync` drift (this repo must not newly lead its imported kure dependency on shared direct deps). | Step 6, with a named checklist instead of a general look |
| **A6** | Any time you touch a changeset with an open PR, check for new comments or reviews since you last looked, before calling a round done. Enumerate every review thread, not just the top-level review list — a forge can carry several independent reviews over time, and an inline thread carries separate resolved/unresolved state from the review it belongs to. Per comment: push a fix commit, or state why not — silence is not a response. Mark the thread resolved once addressed. | Sharpens step 5 — "iterate on review feedback" means every thread, checked again immediately before declaring the round done, not just what was open the first time you looked |
| **A7** | No bare internal identifier (gate ID, plan step, finding, round, run ID) in a plan, review, or comment without a short subject on first use. Every issue/PR reference is the full project path plus its sigil (`owner/repo#123`), never a bare number — except inside a comment posted on that same forge project, where the surrounding page already supplies the repo. **In this repo**, per the No Downstream References standard (`docs/standards.md`), a reference to any of the forbidden downstream repo names is never qualified even to resolve ambiguity — the qualified form is still a forbidden term. Reword to a generic functional reason and drop the number instead. | Applies wherever the workflow above cites an issue or PR — step 2's issue review through step 5's feedback thread |

## Organization Resources

The go-kure org governance, design documents, and community files are maintained in
[go-kure/.github](https://github.com/go-kure/.github).

- **Design documents** (`docs/design/`):
  - [OAM Runtime](https://github.com/go-kure/.github/blob/main/docs/design/oam-runtime.md) — kurel design and architecture
  - [Package Structure](https://github.com/go-kure/.github/blob/main/docs/design/package-structure.md) — kure + launcher organization
  - [API Stability Contract](https://github.com/go-kure/.github/blob/main/docs/design/api-stability.md) — versioning, deprecation policy
  - [OCI Artifact Layout](https://github.com/go-kure/.github/blob/main/docs/design/oci-layout.md) — layout tree conventions
- **Standards**: [docs/standards.md](https://github.com/go-kure/.github/blob/main/docs/standards.md)
- **Contributing**: [CONTRIBUTING.md](https://github.com/go-kure/.github/blob/main/CONTRIBUTING.md)
- **Reusable workflows**: release, pr-review, claude — all hosted in go-kure/.github
- **Reusable workflow reference**: [go-kure/.github AGENTS.md](https://github.com/go-kure/.github/blob/main/AGENTS.md)

## Questions?

Refer to:
1. `DEVELOPMENT.md` - Detailed development workflow
2. `docs/design.md` - Full design document and vision
3. `docs/github-workflows.md` - GitHub Actions workflow reference
