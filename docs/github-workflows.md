# GitHub Workflows Documentation

This document provides an overview of all GitHub Actions workflows used in the launcher project.

**Last Updated:** 2026-08-24

---

## Workflow Summary

| Workflow | File | Triggers | Purpose |
|----------|------|----------|---------|
| [CI](#ci-workflow) | `ci.yml` | push, PR, merge_group, schedule, manual | Testing, linting, building, cross-platform binaries |
| [Deploy Docs](#deploy-docs-workflow) | `deploy-docs.yml` | push to main (docs paths), `workflow_dispatch` | Multi-version docs deployment |
| [Release](#release-workflow) | `release-publish.yml` | version tags, `workflow_dispatch` | Release with GoReleaser, SBOM, docs deploy |
| [Create Release](#create-release-workflow) | `release-create.yml` | `workflow_dispatch` | Pre-release test gate + tag creation |
| [PR Review](#pr-review-workflow) | `pr-review.yml` | pull_request, merge_group | Two-pass AI code review via claude-max-proxy |
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

validate-manifests  ← kurel build + flux-schema validate (non-blocking, not in `build`'s gate)

PR-only jobs (parallel, non-blocking):
┌─────────────────┐  ┌────────────┐  ┌─────────────┐
│ analyze-changes │  │ docs-build │  │ pin-impact  │  ← still feeds `build`
└─────────────────┘  └────────────┘  └─────────────┘
```

On `merge_group` events (merge queue), `lint`/`test`/`build` run against the queue's
temporary branch — the merged result — before the PR is allowed to land.

### Jobs Detail

| Job | Check Name | Timeout | Dependencies | Purpose |
|-----|------------|---------|--------------|---------|
| `changes` | `detect-changes` | 2 min | — | Path filter: `go:` and `docs:` outputs control downstream jobs |
| `validate` | `lint` | 20 min | changes | go-version, fmt, tidy, vet, lint, tool-version parity (golangci-lint pin across Makefile/ci.yml/docs), govulncheck doc parity; diff-based lint on PRs |
| `test` | `test` | 25 min | changes | Unit tests with race detection and coverage (`-race`); CGO enabled |
| `security` | `Security` | 15 min | changes | govulncheck (symbol scan, allowlist-gated), outdated deps check, sensitive file scan |
| `action-pins` | `action-pins` | 2 min | — | Fails if any third-party `uses:` ref is not pinned to a 40-char commit SHA (`go-kure/.github` composite action) |
| `coverage-check` | `Coverage Check` | 5 min | test | 80% threshold, Codecov upload, PR sticky comment |
| `build-binaries` | `Build kurel` | 10 min | changes, test | Build `kurel` linux/amd64 binary; uploaded as artifact |
| `docs-build` | `docs-build` | 15 min | changes | Hugo site build for docs; go + Hugo caches; runs the shared No-Downstream-References guard (`check-forbidden-terms` action, `--full-tree`) + a vendored-copy drift check + the canonical `check-doc-sync`/`check-links` actions (structure + rendered-link check) |
| `pin-impact` | `pin-impact` | 3 min | — | Renders and gates on the real impact of a `go-kure/.github` pin bump: resolves each referenced action's `scripts/*.sh` (and their `source`d siblings), intersects against the compare diff, fails if a consumed path changed (PR only, go-kure/launcher#358) |
| `build` | `build` | 1 min | validate, test, build-binaries, docs-build, coverage-check, action-pins, security, pin-impact | Aggregation gate |
| `cross-platform` | `Cross-Platform Build` | 15 min | build-binaries | Matrix: linux × amd64/arm64 (main + release/* only) |
| `validate-manifests` | `validate-manifests` | 10 min | changes | `kurel build` + `flux schema validate` against the `default` (embedded) and `ecosystem` (schemas.fluxoperator.dev) catalogs for a representative `examples/*.yaml` subset; `continue-on-error: true`, not in `build`'s gate (go-kure/launcher#292) |
| `analyze-changes` | `Analyze Changes` | 5 min | — | Changed files summary, breaking change warning for pkg/ (PR only) |

### Cross-Platform Matrix

Runs on main and `release/*` branches only (not PRs):

| OS | amd64 | arm64 |
|----|-------|-------|
| linux | ✅ | ✅ |

### Configuration

- Go Version: read from `mise.toml` (single source of truth)
- yq / lychee / Flux CLI versions: read from `mise.toml` at run time, same pattern as Go — no
  hand-copied literal exists in any workflow to fall out of sync
- Golangci-lint Version: `v2.13.2`
- govulncheck Version: `v1.7.0` (pinned via the `GOVULNCHECK_VERSION` workflow env)
- Coverage Threshold: `80%`
- Test Timeout: `5m` (longer than kure; builds include CGO)

### Features

- **No-Downstream-References guard** — `docs-build` runs the shared `go-kure/.github`
  `check-forbidden-terms` action, which scans `--full-tree` on **every** event so a PR and the merge
  queue produce identical results (scan parity). A drift-check step keeps the vendored copy that
  `scripts/release.sh` uses (`site/scripts/check-forbidden-terms.sh`) byte-identical to canonical
- **Doc-sync checks** — `docs-build` (Layers 1/2) and `doc-gate` (Layer 3) run the canonical
  `check-doc-sync`, `check-links` and `check-doc-gate` actions from `go-kure/.github`; launcher no
  longer vendors its own copies under `site/scripts/`
- **Manifest schema validation** — `validate-manifests` builds a representative subset of
  `examples/*.yaml` via `kurel build` and validates the output against
  [fluxcd/flux-schema](https://github.com/fluxcd/flux-schema)'s `default` (embedded) catalog plus
  its `ecosystem` catalog (fetched from schemas.fluxoperator.dev — network access required)
  (`make validate-manifests`, same command locally). `continue-on-error: true` for its first cycle
  and excluded from `build`'s aggregation gate and `DEVELOPMENT.md`'s required-checks list —
  promoting it to required is a deliberate follow-up (go-kure/launcher#292)
- **Pin-impact gate** — `pin-impact` (PR only) renders the real impact of a `go-kure/.github` pin
  bump before merge: which actions it touches, which `scripts/*.sh` each resolves to (one level of
  `source` included), intersected against the bump's actual diff. Fails closed on anything it can't
  confidently resolve — an unrecognized `action.yml` shape (a nested `uses:` step, more than one
  `run:` step, a `run:` step with no `scripts/*.sh` reference), an unrecognized or dot-segment
  `source` expression, or a non-ahead compare — rather than under-reporting. A maintainer who has
  reviewed a real hit and judged it safe adds the `pin-impact-ack` label to merge anyway — same
  convention as `check-doc-gate`'s `docs-skip` label; there is no other override. Ported from
  `go-kure/kure` (go-kure/kure#729) after go-kure/launcher#358 turned up the identical blind spot
  here. **Rerun gotcha:** the `strip-ack` step only runs when the triggering event's action was
  `synchronize` or `reopened`; re-running a stale/failed run of one of *those* (`gh run rerun`, or
  the Actions UI) replays that same original action and silently strips a freshly-added
  `pin-impact-ack` again before the gate re-checks it, even though nothing was pushed. A rerun of an
  `opened`/`labeled`/`unlabeled`-triggered run is unaffected — `strip-ack` skips it either way.
  **Scoped to same-repo PRs:** `strip-ack`'s `if:` also requires the PR's head repo to equal this
  repo, so it never runs at all on a fork PR — but that's moot, since the gate separately forces
  `PIN_IMPACT_ACK=false` unconditionally on forks; a fork PR has no acknowledgment path regardless
  of labels. Add (or re-add) the label rather than rerunning, on a same-repo PR; full writeup in
  `go-kure/.github`'s `docs/standards.md` § "Pin-impact-ack"
- **Path filtering** — `dorny/paths-filter` skips jobs when unrelated files change
- **Diff-based lint** — on PRs, lint only checks new/changed lines (`--new-from-rev`)
- **CGO enabled** — test job installs `build-essential` for cgo-dependent packages, guarded by
  `command -v gcc && command -v make` (both, see below). The guard is not an optimisation: the
  unguarded form added a hard dependency on outbound apt reachability, and when the mirror stalls
  the step produces no output until `timeout-minutes: 25` cancels the job — indistinguishable, from
  the check list, from a genuinely slow test suite (go-kure/launcher#473). The guard must check
  **`make` as well as `gcc`**: `build-essential` pulls in `make`, and the `test` job runs
  `make deps` while carrying no `make` guard of its own, so this step is the only thing that
  guarantees `make` there — a `gcc`-only guard would skip the install on an image with gcc but no
  make and break `make deps`
- **Binary artifact** — `kurel` linux/amd64 binary uploaded per run (7-day retention)
- **Cross-platform artifacts** — 5 binaries uploaded per main push (30-day retention)
- **Runs on draft PRs** — no draft gate on any job (2026-08-19, GitLab `mr-review` parity — draft
  blocks merge only, via branch protection, not what CI runs)
- **make install guard** — every job that calls `make` installs it first (runner image lacks it)
- **govulncheck allowlist** — the `Security` job runs `govulncheck -scan symbol -format json`, then
  gates the report through the shared `go-kure/.github` `govulncheck-gate` composite action (same
  fail-closed script kure uses), which blocks on any OSV ID with a reachable symbol trace that isn't
  in the action's `allowlist` input. The action fails closed: a missing, empty, or unparseable report
  is a gate error (exit 2), never a silent clean result, and reachable-vs-allowed advisories are
  printed to the job log so accepted risk stays visible rather than looking clean.
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
2. Reads Hugo, Go and yq versions from `mise.toml`
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

**File:** `.github/workflows/release-publish.yml`
**Reusable source:** `go-kure/.github/.github/workflows/release-publish.yml@main`

### Triggers

- Push of version tags: `v*` (triggered by `release-create.yml`)
- `workflow_dispatch` — manual re-publish of an existing tag

#### Re-publishing a tag after a failed run

**Scope: runs that failed _before_ `goreleaser` created the GitHub release.** `goreleaser` runs
ahead of `deploy-docs` and `post-release` (see Job Sequence below) and publishes a non-draft
release, so a failure in either of those later jobs leaves the release object already created.
Dispatching a fresh publish in that case would redo release publication instead of recovering the
failed step. Check before doing anything:

```bash
gh release view <tag> --repo go-kure/launcher
```

If the release exists, do **not** conclude the publish succeeded — `goreleaser` creates the release
object before it uploads anything. Read the **job conclusions** of the publish run:

```bash
gh run view <run-id> --repo go-kure/launcher --json attempt,startedAt,jobs \
  --jq '.startedAt as $a | "attempt \(.attempt) started \($a)",
        (.jobs[] | "  \(.name): \(.conclusion)"
                 + (if .startedAt < $a then "   <- CARRIED OVER (started \(.startedAt))" else "" end))'
```

Run against the sibling library repo's `v0.2.0-beta.11` publish run that prints:

```
attempt 3 started 2026-09-11T06:08:55Z
  release / Test: failure
  release / Validate tag and changelog: success   <- CARRIED OVER (started 2026-09-10T18:04:11Z)
  release / goreleaser: skipped
  release / post-release: skipped
  release / Deploy versioned docs: skipped
```

> ⚠ **A re-run makes the job list a mixture of attempts, and the default output does not say so.**
> `gh run view` reports the latest attempt, and a re-run of failed jobs carries the jobs it did not
> re-run into that attempt unchanged — original conclusion, original timestamps. Above, `Validate
> tag and changelog` is listed under attempt 3 while reporting attempt 1's start.
>
> **No job goes missing.** The hazard is the opposite and worse: a missing job sends you looking and
> you notice, whereas a carried-over row hands you a conclusion from an attempt you are not looking
> at, and it reads exactly like a current one. A carried-over `goreleaser: success` describes a
> publication that happened in some earlier attempt, not the one you are recovering.
>
> The comparison is what the `--jq` above does for you — the run-level `startedAt` is the attempt's
> own start, so any job starting before it belongs to an earlier attempt. Do not do this by eye; the
> two timestamps differ by a field the bare `--json jobs` form never prints.

Add `--attempt <n>` to inspect an older attempt; without it the command reports the latest one.

> ⛔ **`goreleaser: skipped` in the latest attempt does not mean it never ran.** It declares
> `needs: [test, validate]`, so *any* re-run whose `test` fails skips it again — while a release
> object created by an **earlier** attempt is still there. A `skipped` publisher next to an existing
> release means the two disagree, which is an escalation rather than a conclusion about publication.

**Confirm the run you are reading published this tag.** The run id is supplied independently of the
tag, and both tag pushes and manual dispatches create publish runs, so nothing so far has tied the
two together:

```bash
gh run view <run-id> --repo go-kure/launcher --json headBranch,headSha,event
```

`headBranch` carries the tag name on a tag-triggered run, and `headSha` the tagged commit. **If
`headBranch` is not the tag you are recovering, you are reading the wrong run.**

The job conclusion is the oracle, not the asset count. An asset count cannot tell a complete release
from one whose `goreleaser` job died right after creating it, and how many assets a *complete*
release carries is decided by `.goreleaser.yml` **at that tag** — so a previous release is not a
valid comparison either, and an artifact-matrix change would make a complete release look partial.
(In the sibling library repo the count is uninformative outright: it builds no binaries, so a fully
successful release there has zero assets.)

As a secondary check once `goreleaser` reports success, the tag's own `checksums.txt` is
self-describing — it names every archive that tag should carry, with one `.sbom.json` per archive
and one `checksums.txt.sigstore.json`:

```bash
gh release view <tag> --repo go-kure/launcher --json assets --jq '.assets[].name'
git show <tag>:.goreleaser.yml
```

**If the release object exists, this runbook covers exactly one recovery.** It applies when
`goreleaser` concluded `success` in the attempt you are reading and only a follow-up job failed:

- **`goreleaser` concluded `success`** — publication finished and only a follow-up job failed.
  Do not re-publish; recovery depends on why the follow-up failed:
  - *Transient failure* — `gh run rerun --failed <run-id>`. This does not re-run `goreleaser`:
    `--failed` re-runs the failed jobs and the jobs *downstream* of them, carrying successful
    upstream jobs over untouched. Measured on a publish run in the sibling library repo — attempt 3
    lists `Validate tag and changelog: success` with attempt 1's `started_at`, unchanged, even
    though it is a declared `needs:` of a job that was re-run. (The `--failed` help text reads
    "including dependencies", which invites the opposite reading; the API endpoint is
    `rerun-failed-jobs`.)
  - *The shared workflow itself needs a fix* — `--failed` pins the reusable workflow to the first
    attempt's SHA and so cannot pick the fix up, while a full re-run would redo publication against
    the release that already exists. Neither works. Drive the follow-up work directly instead:

    ```bash
    # deploy-docs. --ref is required: without it the workflow runs from the default branch and
    # deploys main's content into the version slot. set_latest defaults to false, so pass true
    # only when this tag is the latest stable.
    gh workflow run deploy-docs.yml --repo go-kure/launcher --ref <tag> \
      -f version_slot=<slot> -f version_label=<tag> -f set_latest=<true|false>

    # post-release (Go proxy refresh) — request the module so the proxy fetches it
    curl -fsS https://proxy.golang.org/github.com/go-kure/launcher/@v/<tag>.info
    ```

    > ⚠ **Known limitation — this does not recover a broken `deploy-docs.yml`.** `--ref` selects
    > both the workflow version *and* the content: `deploy-docs.yml` takes `version_slot`,
    > `version_label` and `set_latest` only, and its checkout has no `ref:`, so it builds whatever
    > the event ref points at. If the docs deploy failed because `deploy-docs.yml` or a docs script
    > **at that tag** is itself faulty, `--ref <tag>` re-runs the faulty version, and omitting
    > `--ref` builds `main`'s content into the version slot. There is no combination that pairs a
    > fixed workflow with the tag's content. Closing that needs a checkout-ref input on
    > `deploy-docs.yml`; until then this bullet only covers a *transient* or *environmental* docs
    > failure, not a defect baked into the tag.

**Every other shape stops here — escalate, do not delete.** A release object that exists while
`goreleaser` concluded anything other than `success` — `failure`, `cancelled`, `timed_out`,
`action_required`, or `skipped` in every attempt — means the release and the job that should own it
disagree. A job cancelled mid-upload concludes `cancelled` and is the case most likely to strand a
half-uploaded artifact set, so it belongs here rather than matching nothing. Resolving it requires
deciding whether the object is this run's to remove, and every signal available from the command
line is too weak to carry a deletion:

- `author` does not separate two runs of the same automation, and a release created through the
  automation's token by a human action reads as the automation.
- A `publishedAt` inside a `goreleaser` execution window needs both ends of that window, and picking
  *which* attempt's window to compare against is itself the question being asked.
- `createdAt` is not the release object's creation time at all — **it tracks the tag.** On the
  sibling library repo's hand-created release it reads `2026-09-10T18:03:56Z`, before the publish
  run started, while the object was created at `publishedAt: 2026-09-11T07:41:30Z` by a human.

Collect the tag, the run id, and the job conclusions from the command above — repeated with
`--attempt <n>` for each earlier attempt — and hand them to a maintainer. **Do not delete the
release object, and never move or delete the tag.** Deleting the wrong release is unrecoverable in
a way that waiting is not, and a release object can be replaced by hand once its provenance is
settled.

> Determining this state reliably is tracked as an extraction into a tested script shared by both
> repos, rather than a procedure re-derived by a reader — see `go-kure/.github` issue 205.

If the release does **not** exist, recover it. Which path applies depends on why the run failed:

| Situation | Recovery |
| --- | --- |
| Transient, `goreleaser` concluded `failure`; the shared workflow needs no change | `gh run rerun --failed <run-id>` |
| Transient, but `goreleaser` was `skipped` because an upstream job (`test`, `validate`) failed | `gh run rerun --failed <run-id>` — the upstream failure is what to recover. `goreleaser` re-runs as a job downstream of it, so no separate step is needed |
| Transient, but `goreleaser` concluded `cancelled` or `timed_out` | `gh run rerun <run-id>` — a **full** re-run. `--failed` selects jobs whose conclusion is `failure`, so a publisher that concluded some other way is never re-run and the release stays absent while the run reports done |
| The shared workflow needed a fix, and the failed run is under 30 days old | `gh run rerun <run-id>` — a **full** re-run, not `--failed` |
| No failed run remains, or it is over 30 days old | `gh workflow run release-publish.yml --repo go-kure/launcher --ref <tag>` |

The full-versus-failed distinction is load-bearing. GitHub resolves a reusable workflow referenced
by branch differently per re-run mode: re-running **all** jobs uses the called workflow from the
specified reference — here `@main`, so it picks up a fix — while re-running **failed jobs or a
single job** uses the called workflow from the commit SHA of the first attempt, so it does not.
Re-runs stay available for 30 days after the initial run.

The dispatch row depends on the shared workflow skipping its version-progression check for
`workflow_dispatch`. That check derives the previous tag as the second entry of
`git tag --sort=-v:refname`, which assumes the tag being released sorts first — true for a pushed
tag, false for a re-publish. Without the skip, a dispatch of an older tag fails `validate` as soon
as **two or more** newer tags exist (with exactly one, position 2 is the tag itself and the check
passes), and `goreleaser` never runs because it declares `needs: [test, validate]` — breaking the
dispatch path in precisely the long-lived case it exists for.

The tag itself must never be moved or deleted to force a fresh `push` event.

On the dispatch path, `--ref` must be the tag being published: the shared workflow checks out
`github.ref`, so dispatching from a branch would build that branch rather than the release. The
`guard-tag-ref` job rejects a non-tag ref outright rather than skipping, so a mistaken dispatch
fails loudly instead of leaving a green-looking run. The trigger also has to be present in the
workflow file _at that tag_, so dispatch only works for tags cut after it was added.

### Job Sequence

```
tag push (or workflow_dispatch)
  → guard-tag-ref (wrapper-local; fails unless github.ref is a v* tag)
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
        → triggers release-publish.yml (via tag push)
```

### Requirements

Secrets: `RELEASE_APP_ID`, `RELEASE_APP_PRIVATE_KEY` (GitHub App token, so tag push triggers
subsequent workflows — `GITHUB_TOKEN` pushes do not trigger workflows).

---

## PR Review Workflow

**File:** `.github/workflows/pr-review.yml`
**Reusable source:** `go-kure/.github/.github/workflows/pr-review.yml@main`

### Triggers

- Pull requests: `opened`, `synchronize`, `reopened`, on GitHub's default types
- `merge_group` (no filters): required so this check reports on the merge queue's temporary
  ref once it becomes a required status check — the queue payload has no `pull_request` field,
  so the existing fork skip below evaluates false and the job reports `skipped`/success as a
  no-op
- Runs on draft PRs the same as ready ones (2026-08-19, GitLab `mr-review` parity); skips fork PRs
- `ready_for_review` is not declared, same reasoning as `ci.yml`: it was kept as a rollout-window
  safety net while the callee (`pr-review.yml@main`, in `go-kure/.github`) still gated on
  `draft == false` (its own parity fix landed 2026-08-19, `46dfc88`) and dropped once that window
  closed.

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

`mise.toml` is the single source of truth. CI jobs need no sync — they read it at run time. Four
other places carry their own copy and do need syncing: every module's `go.mod` (root, `site/`,
`site/scripts/kuredepsync/`), `versions.yaml`'s `go.current`, the README.md shields.io badge, and
DEVELOPMENT.md's "Go X.Y.Z (managed by mise)" prerequisite line. `scripts/sync-go-version.sh`
propagates a `mise.toml` change into all of them in one pass — `make sync-go-version` runs it
locally, and it also runs as a Renovate `postUpgradeTasks` command so a bot-proposed mise bump
lands with every copy already in sync. `make check-go-version` verifies every module's `go.mod` against `mise.toml`;
`./scripts/sync-versions.sh check` separately verifies the root `go.mod` against `versions.yaml`'s
`go.current` and the README badge against `go.mod`.

### yq, lychee and Flux CLI Versions

Same pattern as Go: a `Read <tool> version from mise.toml` step greps the value and hard-fails if
empty, every downstream step (cache key, install URL, or the flux2 action's `version:` input)
references `steps.<tool>-version.outputs.version`, and no hand-copied literal exists anywhere else
in the workflow files to fall out of sync. For example:

```bash
YQ_VER=$(grep '^yq = ' mise.toml | sed 's/yq = "\(.*\)"/\1/')
if [ -z "$YQ_VER" ]; then
  echo "::error::Failed to parse yq version from mise.toml"
  exit 1
fi
```

This replaced three hardcoded `yq-4.44.6` cache-key/install pairs (in `ci.yml`'s `validate`,
`docs-build` and `doc-gate` jobs) plus a fourth in `deploy-docs.yml`, a hardcoded `lychee-0.24.2`
pair in `docs-build`, and a hardcoded `version: 2.9.4` on the `validate-manifests` job's Flux CLI
install step. The flux-schema plugin (`flux plugin install schema@<version>`) is a separate pin,
tracked only in `site/scripts/validate-manifests.sh`'s `SCHEMA_PLUGIN_VERSION` — a Renovate
customManager in `renovate.json` proposes its bumps; nothing in `mise.toml` covers it, since it is
a Flux plugin, not the Flux CLI itself.

### Caching

`setup-go` runs with `cache: false`; caching is done with explicit `actions/cache` steps.
The runners are ephemeral ARC pods, so nothing on disk survives between jobs — everything
useful must round-trip through the cache server.

**Module cache** (dependency-only, one combined step per Go job):

```yaml
- name: Cache Go modules
  uses: actions/cache@v6
  with:
    path: ~/go/pkg/mod
    key: ${{ runner.os }}-gomod-${{ hashFiles('**/go.sum') }}
    restore-keys: |
      ${{ runner.os }}-gomod-
```

**Go build cache** (`~/.cache/go-build`) uses split `actions/cache/restore` + `actions/cache/save`
so the log can show exact vs fallback restore (`cache-matched-key`). The key is **source-aware**
and **split by job purpose** so `validate` (non-race) and `test` (race+coverage) never overwrite
each other's entry:

```yaml
- name: Restore Go build cache
  id: gocache
  uses: actions/cache/restore@v6
  with:
    path: ~/.cache/go-build
    key: ${{ runner.os }}-${{ runner.arch }}-go-<GOVER>-gocache-<purpose>-deps-<go.sum hash>-src-<source hash>
    restore-keys: |
      ${{ runner.os }}-${{ runner.arch }}-go-<GOVER>-gocache-<purpose>-deps-<go.sum hash>-src-
      ${{ runner.os }}-${{ runner.arch }}-go-<GOVER>-gocache-<purpose>-
# ... compile / test ...
- name: Save Go build cache
  if: success() && steps.gocache.outputs.cache-hit != 'true'
  uses: actions/cache/save@v6
  with:
    path: ~/.cache/go-build
    key: ${{ steps.gocache.outputs.cache-primary-key }}
```

Purpose prefixes: `gocache-validate-`, `gocache-test-race-cover-`, `gocache-security-`,
`gocache-build-` (the `cross-platform` job adds `<os>-<arch>` because cross-compiled artifacts
differ per target). The source hash covers `**/*.go`, `go.mod`, `go.sum`, `Makefile`, and
`**/testdata/**`. The save runs only on a non-exact (fallback/miss) restore and only when the
run succeeded, so a broken build never publishes a cache.

**Cross-ref scoping caveat.** GitHub caches are ref-scoped: a `pull_request` cache lives on
`refs/pull/N/merge` and is **not** visible to the `merge_group` (merge-queue) run — verified
empirically. The only scope both PR and queue runs can read is the default branch (`main`).
So these caches are warmed by push-to-main runs; a code-changing PR and its queue run restore
main's cache via **restore-key fallback** (not an exact hit) and Go reuses unchanged package
entries internally. This lowers the absolute cost of both runs but does **not** deduplicate the
PR↔queue build — that duplication is inherent to the merge queue and cannot be removed with
GitHub-scoped caches.

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
