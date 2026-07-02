# GitHub Workflows Documentation

This document provides an overview of all GitHub Actions workflows used in the launcher project.

**Last Updated:** 2026-06-02

---

## Workflow Summary

| Workflow | File | Triggers | Purpose |
|----------|------|----------|---------|
| [CI](#ci-workflow) | `ci.yml` | push, PR, merge_group, schedule, manual | Testing, linting, building, cross-platform binaries |
| [Deploy Docs](#deploy-docs-workflow) | `deploy-docs.yml` | push to main (docs paths), `workflow_dispatch` | Multi-version docs deployment |
| [Release](#release-workflow) | `release.yml` | version tags | Release with GoReleaser, SBOM, docs deploy |
| [Create Release](#create-release-workflow) | `release-create.yml` | `workflow_dispatch` | Pre-release test gate + tag creation |
| [PR Review](#pr-review-workflow) | `pr-review.yml` | pull_request | Two-pass AI code review via claude-max-proxy |
| [Claude](#claude-workflow) | `claude.yml` | PR/issue/comment events | @claude AI assistant |

The last five workflows are thin callers that delegate to reusable workflows in
[go-kure/.github](https://github.com/go-kure/.github). See
[go-kure/.github AGENTS.md](https://github.com/go-kure/.github/blob/main/AGENTS.md)
for their full documentation.

---

## CI Workflow

**File:** `.github/workflows/ci.yml`
**Name:** `CI`

### Triggers

- Push to: `main`, `develop`, `release/*`
- Pull requests to: `main`, `develop`
- Merge group (merge queue's temporary branch — required checks must report here)
- Schedule: 4am UTC daily (catch external changes)
- Manual dispatch

### Concurrency

Uses `github.ref` to cancel superseded runs on the same branch or PR:

```yaml
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
```

### Job Dependency Graph

```
┌────────────────────┐
│   lint             │  ← Fast checks: go-version, fmt, tidy, vet, lint
└─────────┬──────────┘
          │
     ┌────┴────┐
     ▼         ▼
┌────────┐ ┌──────────┐
│  test  │ │ security │  ← Tests + govulncheck (parallel)
└───┬────┘ └──────────┘
    │
    ▼
┌──────────────────┐
│  coverage-check  │  ← 80% threshold enforcement
└──────┬───────────┘
       │
  ┌────┴────┐
  ▼         ▼
┌───────┐  ┌────────────────┐
│ build │  │ build-binaries │  ← kurel binary (linux/amd64)
└───────┘  └────────┬───────┘
                    │
                    ▼ (main / release/* only)
           ┌─────────────────┐
           │ cross-platform  │  ← linux amd64/arm64 matrix build
           └─────────────────┘

PR-only jobs (parallel, non-blocking):
┌─────────────────┐  ┌────────────┐
│ analyze-changes │  │ docs-build │
└─────────────────┘  └────────────┘
```

On `merge_group` events (merge queue), `lint`/`test`/`build` run against the queue's
temporary branch — the merged result — before the PR is allowed to land.

### Jobs Detail

| Job | Check Name | Timeout | Dependencies | Purpose |
|-----|------------|---------|--------------|---------|
| `changes` | `detect-changes` | 2 min | — | Path filter: `go:` and `docs:` outputs control downstream jobs |
| `validate` | `lint` | 20 min | changes | go-version, fmt, tidy, vet, lint; diff-based lint on PRs |
| `test` | `test` | 25 min | changes | Unit tests with race detection and coverage (`-race`); CGO enabled |
| `security` | `Security` | 10 min | changes | govulncheck (symbol scan, allowlist-gated), outdated deps check, sensitive file scan |
| `coverage-check` | `Coverage Check` | 5 min | test | 80% threshold, Codecov upload, PR sticky comment |
| `build-binaries` | `Build kurel` | 10 min | changes, test | Build `kurel` linux/amd64 binary; uploaded as artifact |
| `docs-build` | `docs-build` | 15 min | changes | Hugo site build for docs; go + Hugo caches |
| `build` | `build` | 1 min | validate, test, build-binaries, docs-build, coverage-check | Aggregation gate |
| `cross-platform` | `Cross-Platform Build` | 15 min | build-binaries | Matrix: linux × amd64/arm64 (main + release/* only) |
| `analyze-changes` | `Analyze Changes` | 5 min | — | Changed files summary, breaking change warning for pkg/ (PR only) |

### Cross-Platform Matrix

Runs on main and `release/*` branches only (not PRs):

| OS | amd64 | arm64 |
|----|-------|-------|
| linux | ✅ | ✅ |

### Configuration

- Go Version: read from `mise.toml` (single source of truth)
- Golangci-lint Version: `v2.10.1`
- Coverage Threshold: `80%`
- Test Timeout: `5m` (longer than kure; builds include CGO)

### Features

- **Path filtering** — `dorny/paths-filter` skips jobs when unrelated files change
- **Diff-based lint** — on PRs, lint only checks new/changed lines (`--new-from-rev`)
- **CGO enabled** — test job installs `build-essential` for cgo-dependent packages
- **Binary artifact** — `kurel` linux/amd64 binary uploaded per run (7-day retention)
- **Cross-platform artifacts** — 5 binaries uploaded per main push (30-day retention)
- **Skip draft PRs** — `if: github.event.pull_request.draft == false`
- **make install guard** — every job that calls `make` installs it first (runner image lacks it)
- **govulncheck allowlist** — the `Security` job runs `govulncheck -scan symbol -format json` and gates
  on OSV IDs with a reachable symbol trace. A `jq` filter is the sole enforcement point (JSON mode can
  exit 0 despite reachable findings), and accepted-risk advisories are listed in the step's
  `VULN_ALLOWLIST` env with a justification per entry. The step summary reports allowed vs unallowed
  reachable advisories separately, so accepted risk stays visible rather than looking clean.
  - Currently allowlisted: `GO-2026-5377` (external-secrets controller privilege escalation). Launcher
    only imports `external-secrets/apis` to generate CRD manifests; reachable traces are generated
    deepcopy boilerplate and package init, never a reconciler. The apis module is untagged and the Go
    vuln DB records no fixed version (`Fixed in: N/A`), so no dependency bump can clear it.

---

## Deploy Docs Workflow

**File:** `.github/workflows/deploy-docs.yml`
**Name:** `Deploy Docs`

### Triggers

- **Push to main** (paths: `site/**`, `docs/**`, `*.md`, `CHANGELOG.md`, `DEVELOPMENT.md`,
  `scripts/gen-versions-toml.sh`)
- **Manual dispatch** with inputs: `version_slot`, `version_label`, `set_latest`

### How It Works

1. Determines version parameters (dev for push to main, explicit slot for manual dispatch)
2. Reads Hugo and Go versions from `mise.toml`
3. Runs `scripts/gen-versions-toml.sh` to generate versioned Hugo config overlay
4. Builds the Hugo site targeting `https://www.gokure.dev/launcher/<slot>/`
5. If `set_latest=true`, also builds at `https://www.gokure.dev/launcher/`
6. Checks out `go-kure/go-kure.github.io` and deploys to the `launcher/` subdirectory

### Trigger Matrix

| Event | Deploys To | BaseURL |
|-------|-----------|---------|
| Push to `main` (docs paths) | `launcher/dev/` | `www.gokure.dev/launcher/dev/` |
| `workflow_dispatch` | `launcher/<slot>/` | `www.gokure.dev/launcher/<slot>/` |
| `workflow_dispatch` + `set_latest=true` | `launcher/<slot>/` + `launcher/` | both |

### Concurrency

Per-slot group (`deploy-docs-<slot>`) with `cancel-in-progress: false` — deploys queue rather
than cancel, so a race between two slot deployments doesn't corrupt the site.

### Preservation

Only the target slot is replaced. Other `launcher/v*/`, `launcher/dev/`, `CNAME`, and `.nojekyll`
are preserved. The root `launcher/` files are only overwritten when `set_latest=true`.

### Authentication

Requires `DEPLOY_TOKEN` secret — a PAT with write access to `go-kure/go-kure.github.io`.

---

## Merge Queue

launcher merges through GitHub's native **merge queue** (configured in the `main-protection`
ruleset, not a workflow file). This replaced the former `rebase-check` job and `auto-rebase.yml`
workflow — it is the native equivalent of GitLab's merged-results pipelines.

### How It Works

1. A reviewed PR is added to the queue ("Merge when ready").
2. The queue creates a temporary branch combining `main` + the PR and fires a `merge_group`
   event; `lint`/`test`/`build` run against that **merged result**.
3. If green, the PR lands on `main` with the **rebase** merge method (linear history preserved).
   If the merged result fails, the PR is dropped from the queue and `main` stays green.

### Why

- Tests the actual merged result, which `rebase-check` (ancestry-only) could not.
- No force-pushing contributor branches and no per-merge auto-rebase storm — the queue rebases
  once, at merge time.

### Configuration (ruleset `merge_queue` rule)

- **Merge method:** `REBASE` (linear history)
- **Grouping:** `ALLGREEN` (a failing entry is dropped from the group)
- **Batch size:** 1 (conservative; tune after observing runner load)
- **Required checks on the queue:** `lint`, `test`, `build` (must also trigger on `merge_group`)

Auto-merge is **not** enabled — every PR is reviewed and queued manually. The merge queue rule is
managed centrally in `go-kure/.github` (`governance/repository-settings-policy.yaml`).

---

## Release Workflow

**File:** `.github/workflows/release.yml`
**Reusable source:** `go-kure/.github/.github/workflows/release.yml@main`

### Triggers

- Push of version tags: `v*` (triggered by `release-create.yml`)

### Job Sequence

```
tag push
  → test (go test -race ./...)
    → validate (tag format, CHANGELOG entry, version progression)
      → goreleaser (GoReleaser v2, cosign signing, syft SBOM)
        → deploy-docs (triggers deploy-docs.yml, stable tags only)
        → post-release (Go proxy refresh with retries)
```

### Key Input

```yaml
with:
  go_module: github.com/go-kure/launcher
```

### Requirements

Secrets: `RELEASE_APP_ID`, `RELEASE_APP_PRIVATE_KEY` (kure-release-bot GitHub App)

---

## Create Release Workflow

**File:** `.github/workflows/release-create.yml`
**Reusable source:** `go-kure/.github/.github/workflows/release-create.yml@main`

### Triggers

- Manual dispatch with inputs: `type` (alpha/beta/rc/stable/bump), `scope` (minor/major/prerelease),
  `dry_run` (default: false)

### Purpose

Pre-release test gate + tag creation. Runs full tests before pushing any tag, so a failing test
suite never results in a published release.

```
workflow_dispatch
  → test job (go test -race ./...)
    → release job (needs: test)
      → scripts/release.sh → git-cliff changelog → tag → push
        → triggers release.yml (via tag push)
```

### Requirements

Secrets: `RELEASE_APP_ID`, `RELEASE_APP_PRIVATE_KEY` (GitHub App token, so tag push triggers
subsequent workflows — `GITHUB_TOKEN` pushes do not trigger workflows).

---

## PR Review Workflow

**File:** `.github/workflows/pr-review.yml`
**Reusable source:** `go-kure/.github/.github/workflows/pr-review.yml@main`

### Triggers

- Pull requests: `opened`, `synchronize`, `ready_for_review`, `reopened`
- Skips draft PRs and fork PRs

### How It Works

Two-pass AI review via the cluster-local claude-max-proxy sidecar:

1. **Pass 1 — Review**: Sends PR diff + `AGENTS.md` + `.claude/CLAUDE.md` to the review model.
   Posts up to 3 findings in a structured table as a PR comment.
2. **Pass 2 — Assessment**: If the review found issues, an assessment model fact-checks each
   finding against the actual diff and the provided standards. Posts a verification comment.

Non-blocking: uses `continue-on-error: true` so review failures never prevent merging.

### Context Input

```yaml
with:
  pr_review_context: "OAM-native package manager for Kubernetes, shipped as the kurel CLI.
    Implements a two-config-set model: package config (app requirements) + site config (cluster
    capabilities), resolved at install time to produce Kubernetes manifests."
```

---

## Claude Workflow

**File:** `.github/workflows/claude.yml`
**Reusable source:** `go-kure/.github/.github/workflows/claude.yml@main`

### Triggers

- PR events (opened, synchronize, ready_for_review, reopened)
- Issue comments and PR review comments (when `@claude` is mentioned)
- Issues opened or assigned
- PR reviews submitted

### Purpose

Runs the `anthropics/claude-code-action@v1` agent on any PR or issue that mentions `@claude`.
The agent has full repo access via checkout and can read code, answer questions, or suggest
changes.

### Requirements

Secret: `CLAUDE_CODE_OAUTH_TOKEN`

---

## Configuration Standards

### Go Version

All jobs read `go-version` from `mise.toml` dynamically:

```bash
GO_VER=$(grep '^go = ' mise.toml | sed 's/go = "\(.*\)"/\1/')
```

`mise.toml` is the single source of truth (kept in sync via `make check-go-version`).

### Caching

Module and build caches use explicit `actions/cache@v5` steps with `cache: false` on `setup-go`:

```yaml
- name: Cache Go modules
  uses: actions/cache@v5
  with:
    path: ~/go/pkg/mod
    key: ${{ runner.os }}-gomod-${{ hashFiles('**/go.sum') }}
    restore-keys: |
      ${{ runner.os }}-gomod-
```

Cache and artifact traffic routes through an in-cluster cache server. Setting
`ACTIONS_RESULTS_URL` in the workflow `env:` block ensures upload/download-artifact and
`actions/cache` see the correct in-cluster URL (the runner binary patch renames the env var
injected into step processes as a side effect).

### Self-Hosted Runner

All jobs run on the `autops-kube-kure` GitHub ARC scale-set. The runner image lacks `make`,
so every job that calls `make` installs it first:

```yaml
- name: Install build tools
  run: command -v make &>/dev/null || (sudo apt-get update -qq && sudo apt-get install -y -qq --no-install-recommends make)
```

---

## Maintenance Notes

- **When adding/modifying workflows:** Update this document
- **Version updates:** Run `make sync-go-version` to update Go version across all files
- **Version check:** Run `make check-go-version` to verify consistency
- **New jobs using `make`:** Include the install guard step above
- **Reusable workflows:** Changes in `go-kure/.github` take effect immediately for all callers

---

## See Also

- [Makefile](https://github.com/go-kure/launcher/blob/main/Makefile) — Local development commands
- [mise.toml](https://github.com/go-kure/launcher/blob/main/mise.toml) — Local tool versions
- [go-kure/.github AGENTS.md](https://github.com/go-kure/.github/blob/main/AGENTS.md) — Reusable workflow reference
- [scripts/gen-versions-toml.sh](https://github.com/go-kure/launcher/blob/main/scripts/gen-versions-toml.sh) — Versioned docs config generator
