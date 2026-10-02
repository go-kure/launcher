# GitHub Workflows Documentation

This document provides an overview of all GitHub Actions workflows used in the launcher project.

**Last Updated:** 2026-09-30

---

## Workflow Summary

| Workflow | File | Triggers | Purpose |
|----------|------|----------|---------|
| [CI](#ci-workflow) | `ci.yml` | push, PR, merge_group, schedule, manual | Testing, linting, building, cross-platform binaries |
| [Deploy Docs](#deploy-docs-workflow) | `deploy-docs.yml` | push to main (docs paths), `workflow_dispatch` | Multi-version docs deployment |
| [Release](#releasing) | `release.yml` | manual | The one manual release workflow: release, promote, or start the next version |
| [Release / Publish](#releasing) (automatic on tag) | `release-publish.yml` | tag push, `workflow_dispatch` | GoReleaser (kurel binaries, checksums, SBOM, cosign signature), docs deploy, proxy refresh |
| [PR Review](#pr-review-workflow) | `pr-review.yml` | pull_request, merge_group | Two-pass AI code review via claude-max-proxy |
| [Claude](#claude-workflow) | `claude.yml` | issue/comment/review events mentioning `@claude` | @claude AI assistant |

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
- Pull requests to any branch — on `opened`, `synchronize`, `reopened`, `labeled` and
  `unlabeled`. There is no base-branch filter: a cherry-pick PR to a `release/vX.Y` branch needs
  the same required `lint`/`test`/`build` checks as a PR to `main`. The label events exist because two overrides are labels: `pin-impact-ack`
  (pin-impact gate, which queries the PR's current labels) and `docs-skip` (doc-gate, which reads
  the label from the event payload). Without them, adding either label would not re-evaluate the
  failed check until an unrelated push. The cost is real: every PR, Renovate PRs included, gets
  its full label set at creation, and each label add or remove starts a whole-pipeline run that
  the concurrency group cancels in favour of the next — one superseded run per label.
  `strip-ack` removing a stale `pin-impact-ack` on a new commit starts no run: it uses
  `GITHUB_TOKEN`, and a label change made with that token does not trigger workflows (only
  `workflow_dispatch` and `repository_dispatch` are exempt from that rule). The trigger is kept deliberately
  (go-kure/launcher#445). Narrowing it to those
  two labels would need a label-aware concurrency key and a `build` job that cannot report green on
  a no-op run; otherwise a skipped run cancels the real one and leaves a false green.
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
┌─────────────────┐  ┌────────────┐
│ analyze-changes │  │ docs-build │
└─────────────────┘  └────────────┘

PR and merge-queue job (no needs):
┌─────────────┐
│ pin-impact  │  ← still feeds `build`
└─────────────┘
```

On `merge_group` events (merge queue), `lint`/`test`/`build` run against the queue's
temporary branch — the merged result — before the PR is allowed to land. `pin-impact` is skipped
on a push; on a merge-queue run it checks only that the merged tree's `go-kure/.github` pins agree
(see the pin-impact gate below).

### Which tree a PR run builds

On `pull_request` events, `actions/checkout` checks out `refs/pull/<N>/merge`: the branch
merged into its base, not the branch. Yet every check run on the PR is stamped with the
**branch head's** SHA. A red PR check can therefore name a commit that builds green — a
textually clean merge can still fail to compile, for example when `main` grew a call site to
a helper the branch removed. Checking out the SHA on the check and rebuilding it does not
reproduce that failure; rebuilding the merge ref does:

```bash
mise run verify-merge <n>       # or: bash scripts/verify-merge.sh <n>
```

`scripts/verify-merge.sh` fetches `refs/pull/<n>/merge`, extracts it into a throwaway
directory, fetches its modules (`go mod download`) and runs `go build ./...` and
`go test ./...` there, leaving the working tree untouched. Run it from the PR's branch after
pushing. Exit `0` is green, `1` means the merge ref fails to build or test, and `2` means
**not computable** — the PR has no merge ref (it is closed or conflicts with its base), the
ref was generated for a different head than the local `HEAD` (GitHub regenerates it a few
seconds after each push), `go` is not on `PATH`, or `go mod download` fails in the merged
tree (the local toolchain cannot run its `go.mod`, e.g. under `GOTOOLCHAIN=local`; the module
proxy or network is unreachable; a requirement cannot be fetched at all). `2` never reads as
a pass. The download runs against a copy of `go.mod`/`go.sum`, so the build still sees the
`go.sum` the ref carries, as CI does.
`make verify-merge PR=<n>` runs the same script but is pass/fail only: make exits with its own
`2` on any recipe failure, so a failing merge ref and a not-computable one look alike there.
The merge ref reflects the base as of GitHub's last computation, which is what the PR run
built; the merge queue re-tests against the current `main` before anything lands, so this is
a local diagnostic, not a gate that protects `main`. The `lint` job runs its fixture
self-test (`make test-verify-merge`).

### Jobs Detail

| Job | Check Name | Timeout | Dependencies | Purpose |
|-----|------------|---------|--------------|---------|
| `changes` | `detect-changes` | 2 min | — | Path filter: `go:` and `docs:` outputs control downstream jobs |
| `validate` | `lint` | 20 min | changes | go-version, fmt, tidy, vet, lint, tool-version parity (golangci-lint pin across Makefile/ci.yml/docs), govulncheck doc parity, verify-merge self-test, `check-pin-impact.sh` cases (`make test-pin-impact`); diff-based lint on PRs |
| `test` | `test` | 25 min | changes | Unit tests with race detection and coverage (`-race`); CGO enabled |
| `security` | `Security` | 15 min | changes | govulncheck (symbol scan, allowlist-gated), outdated deps check, sensitive file scan |
| `action-pins` | `action-pins` | 2 min | — | Fails if any third-party `uses:` ref is not pinned to a 40-char commit SHA (`go-kure/.github` composite action) |
| `issue-refs` | `issue-refs` | 2 min | — | Self-tests then runs `scripts/check-issue-refs.sh` over the whole tracked tree: rejects a bare `#N` or an ownerless `name#N` reference (go-kure/launcher#400; `make check-issue-refs`) |
| `coverage-check` | `Coverage Check` | 5 min | test | 80% threshold, Codecov upload, PR sticky comment |
| `build-binaries` | `Build kurel` | 10 min | changes, test | Build `kurel` linux/amd64 binary; uploaded as artifact |
| `docs-build` | `docs-build` | 15 min | changes | Hugo site build for docs; go + Hugo caches; runs the shared No-Downstream-References guard (`check-forbidden-terms` action, `--full-tree`) + a vendored-copy drift check + the canonical `check-doc-sync`/`check-links` actions (structure + rendered-link check) + the documentation YAML fence check (`make check-doc-fences`) |
| `pin-impact` | `pin-impact` | 3 min | — | On a merge-queue run, only checks that every `go-kure/.github` reference in the merged tree pins the same commit. On a PR, renders and gates on the real impact of a `go-kure/.github` pin bump: resolves each referenced action's `$GITHUB_ACTION_PATH` script (and the siblings those `source` or run), intersects against the compare diff, fails if a consumed path changed and refuses any shape it cannot resolve (go-kure/launcher#358) |
| `build` | `build` | 1 min | validate, test, build-binaries, docs-build, coverage-check, action-pins, security, pin-impact, issue-refs | Aggregation gate |
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
- Golangci-lint Version: `v2.14.0`
- govulncheck Version: `v1.8.0` (pinned via the `GOVULNCHECK_VERSION` workflow env)
- Coverage Threshold: `80%`
- Test Timeout: `5m` per test binary (the `test` job's `go test -timeout`); `TEST_TIMEOUT` in the
  `Makefile` is the local copy and matches it

### Features

- **No-Downstream-References guard** — `docs-build` runs the shared `go-kure/.github`
  `check-forbidden-terms` action, which scans `--full-tree` on **every** event so a PR and the merge
  queue produce identical results (scan parity). Drift-check steps keep the vendored copy that the
  release script uses (`site/scripts/check-forbidden-terms.sh`) and the vendored release guide
  (`docs/releasing.md`) byte-identical to their canonical files, checked out at a ref the
  `Resolve pinned guard revision` step derives from the guard action's own `uses:@<sha>` pin —
  never a second, independently-maintained `ref:` literal. `scripts/vendor-guard.sh`
  re-fetches and re-vendors both files from the same pin, run as a `renovate.json`
  `postUpgradeTasks` command whenever the `go-kure/.github` github-actions dependency bumps
  (go-kure/launcher#475), so the vendored copies and the pin can't drift apart; safe to run by hand
  too. See `go-kure/.github`'s `docs/standards.md` § "Adopting the trusted Actions lane" for the
  full mechanism and go-kure/kure#813 for the original migration off a two-pin design
- **Doc-sync checks** — `docs-build` (Layers 1/2) and `doc-gate` (Layer 3) run the canonical
  `check-doc-sync`, `check-links` and `check-doc-gate` actions from `go-kure/.github`; launcher no
  longer vendors its own copies under `site/scripts/`
- **Documentation YAML fence check** — `docs-build` runs `make check-doc-fences`
  (`site/scripts/check-doc-fences.sh`, go-kure/launcher#442), which checks every YAML fence
  marked with a `check` attribute in its info string and reports a failing fence as
  `file:line`. `build` fences must `kurel build` against their named `profile`; `snippet` and
  `template` fences must be well-formed YAML. Unmarked fences are not checked, so adding a page
  cannot break the build. It runs in `docs-build`, not `lint`, so a documentation-only change
  cannot skip it; `Makefile`, `go.mod` and `go.sum` are in the `docs:` path filter because the
  check builds `kurel`. The target runs the gate's self-test
  (`site/scripts/check-doc-fences-test.sh`) first — broken-fence fixtures that must fail, plus the
  quickstart with `traits: []` re-introduced at spec level (go-kure/launcher#417) — so a gate that
  can no longer fail goes red as well. Marker syntax: `DEVELOPMENT.md` § "Checking documentation
  YAML fences"
- **Issue-reference guard** — `issue-refs` runs `make check-issue-refs`'s two steps
  (go-kure/launcher#400): the self-test (`scripts/check-issue-refs-test.sh`, fixtures that must
  fail and must pass, each in a throwaway git repository), then `scripts/check-issue-refs.sh` on
  the tracked `*.go`, `*.md`, `*.sh`, `*.yml`, `*.yaml`, `*.toml` and `*.json` files. It rejects a
  bare `#N` of two to five digits and an ownerless `name#N` such as `launcher#N` or `kure#N`,
  because neither is a reference that `go doc`, pkg.go.dev or GitHub can resolve or link. It
  scans the whole tree rather than the changed lines, and has no path filter, so a PR and the
  merge queue get the same result. Exempt: `CHANGELOG.md` (generated from commit subjects),
  anything under a `testdata/` directory, and any line carrying `allow-ref`; within a line,
  Markdown link targets `](...)`, URLs, printf verbs with the `#` flag and shell prefix trims
  `${name#...}` are ignored.
  Convention and known gaps: `AGENTS.md` § "Issue references"
- **Manifest schema validation** — `validate-manifests` builds a representative subset of
  `examples/*.yaml` via `kurel build` and validates the output against
  [fluxcd/flux-schema](https://github.com/fluxcd/flux-schema)'s `default` (embedded) catalog plus
  its `ecosystem` catalog (fetched from schemas.fluxoperator.dev — network access required)
  (`make validate-manifests`, same command locally). `continue-on-error: true` for its first cycle
  and excluded from `build`'s aggregation gate and `DEVELOPMENT.md`'s required-checks list —
  promoting it to required is a deliberate follow-up (go-kure/launcher#292)
- **Pin-impact gate** — `pin-impact` renders, on a PR, a `go-kure/.github` pin bump's real effect
  (which `scripts/*.sh` a referenced action actually runs, whether the compare touches any of them)
  into the job summary and fails on a match, so a bump touching consumed code cannot merge
  unreviewed. The old pins are read from the first parent of the checked-out test merge, the base
  branch as the PR would merge onto it now, not from the event payload's `base.sha`, which can
  still name the base from when the PR was opened: a bump that landed on `main` since then would
  otherwise be reported as the PR's own. `scripts/check-pin-impact.sh` is a vendored copy of `go-kure/kure`'s, first ported
  (go-kure/kure#729) after go-kure/launcher#358 turned up the identical blind spot here, and since
  brought level with its hardened form (go-kure/kure#731). It follows three things, and nothing
  else:
  - **Pins.** It reads `uses: go-kure/.github/.github/actions/<subpath>@<sha>`, including nested
    or dotted subpaths. It also reads the `ref: <sha>` in the `with:` mapping of a block-style
    checkout whose same `with:` mapping holds `repository: go-kure/.github`, whatever the key
    order. The repository name is matched case-insensitively and with or without a trailing `.git`
    (`actions/checkout` clones `https://github.com/<repository>`, the same repository either way),
    and the SHA may be written in either case. Keys are read as YAML reads them: `"uses":`,
    `'ref':` and `repository :` are the plain keys, in a workflow and an `action.yml`.
  - **Action scripts.** It follows each `$GITHUB_ACTION_PATH/<rel>.sh` or
    `${GITHUB_ACTION_PATH}/<rel>.sh` in an action's single `run:` step, written as one whole word:
    the path, at most a closing quote, then whitespace, `;&|)<>` or the line end. The word must be
    the command run: at a line start or after a separator, optionally behind a shell keyword
    (`if`, `then`, `else`, `elif`, `do`, `while`, `until` or `!`) and `exec`, `bash`, `sh`,
    `source` or `.` with options. The path is resolved from the action's own directory, counting
    its `..` hops.
  - **Sibling scripts.** It follows, transitively, a whole line
    `source "$SCRIPT_DIR/<name>.sh"` or `[exec] [bash|sh] "$SCRIPT_DIR/<name>.sh" [args]`. It
    trusts only `SCRIPT_DIR` defined as exactly `$(dirname "$0")` or
    `$(cd "$(dirname "$0")" && pwd)`, optionally with `&>/dev/null`, `>/dev/null`,
    `>/dev/null 2>&1` or `2>/dev/null` before the `&&` and `pwd -P` for `pwd`, and either with
    `"${BASH_SOURCE[0]}"` for `"$0"`. The definition may sit behind `declare -r`, `readonly` or
    `export`; `cd --`, `dirname --`, `1>` for `>` and a space after `&>` or `>` are the same
    definition. The run-when-executed guard `if [[ "${BASH_SOURCE[0]}" == "$0" ]]`
    (also `!=`, `"${0}"` and `; then`), alone on its line, names no directory and is accepted.

  It refuses rather than guesses on:
  - **Pins.** Inconsistent pins. Any `go-kure/.github` reference in a `uses:` or `repository:`
    context that yields no 40-hex pin: `@main`, a flow-mapping checkout, a checkout with a branch,
    another expression or no `ref:`, and similar. Only two are exempt: a `ref:` that is exactly
    `${{ steps.<id>.outputs.<name> }}` (the `docs-build` job's vendored-guard checkout, whose ref
    the "Resolve pinned guard revision" step derives from the `check-forbidden-terms` pin), and a
    job-level reusable-workflow call at a non-SHA ref. A reusable-workflow call pinned to a SHA, or
    one inside a step, is refused. A `go-kure/.github` checkout step is also refused when it has a
    `ref:` other than a key at the column of the `with:` mapping's first child (under `env:` or
    another key, in a block scalar body, at the step's own level), a line that is no key the scan
    parses (`a b:`, `a/b:`, the rest of a multi-line value), a value that does not end on its line
    (an unterminated or escaped quote), more than one `ref:` in any letter case, or more than one
    `with:`. A `repository: go-kure/.github` anywhere else in a step (under `env:`, deeper under
    `with:`, at the step's own level) is refused too: it used to mark the step as that checkout,
    so another repository's `ref:` in the same step was read as a pin.
  - **Workflow YAML a line scan cannot read**, whatever it names. A `uses:` or `repository:`
    value that is not whole on its own line: empty, continued on the next line, a block scalar,
    an alias, anchor, tag or flow collection, or a quoted value with an escape (`\` in double
    quotes, `''` in single quotes) or no closing quote. A `repository:` given as an expression
    (`${{ github.repository_owner }}/.github`). A `uses` or `repository` key that does not start
    its line (a flow mapping, a tagged or anchored key) or is not in lower case. A quoted key
    with an escape sequence, and a `? ` complex key. A `uses:` expression is refused only when it
    names `go-kure/.github`: GitHub does not evaluate expressions in `uses:`. Both
    `.github/workflows/*.yml` and `*.yaml` are scanned.
  - **Actions.** An action that is not a composite action (JavaScript or Docker), a `using` in a
    flow mapping or not in lower case included. A nested `uses:`, quoted, in a flow mapping or in
    any letter case, or more than one `run:` step (flow-mapping, quoted and `RUN:` steps counted;
    the lines of a `run: |` body are text, not keys). A `github.action_path` expression. Any
    `GITHUB_ACTION_PATH` mention that is not one whole `$GITHUB_ACTION_PATH/<path>` or
    `${GITHUB_ACTION_PATH}/<path>` word (reassigned, cut down with `${GITHUB_ACTION_PATH%/*}`, a
    bare trailing `/`, or a suffix after the path), and the runner's `_actions` directory by path.
    In an action that mentions `GITHUB_ACTION_PATH`: `dirname`, `realpath`, `readlink`, a
    parameter trim (`${name%...}`, `${name#...}`, `${name/...}`, `${name:offset}`), and a
    `$GITHUB_ACTION_PATH/<path>` word other than as the command run (assigned, or passed as an
    argument). A quoted key with an escape sequence, or a `? ` complex key. A non-`.sh` or
    unaccounted-for script reference. Whether the runner reads `USES:`, `Using:` or `RUN:` as its
    key is not established here, so each is taken as that key.
  - **Paths.** A path that climbs above the repository root, or that has a `.`, `..` or empty
    (`//`) segment where it cannot be normalised.
  - **Scripts.** Any line using `$SCRIPT_DIR` in another shape, which includes `if !`, a wrapper
    command, `$( )`, a pipe and a non-`.sh` sibling. Any `SCRIPT_DIR=` assignment other than the
    trusted definitions. The word `SCRIPT_DIR` in any other form (`SCRIPT_DIR+=`,
    `SCRIPT_DIR[0]=`, `read SCRIPT_DIR`, `for SCRIPT_DIR in`, `n=SCRIPT_DIR`). Name indirection:
    `${!name}` (the array-keys form `${!name[@]}` is allowed), a `declare -n`, `local -n` or
    `typeset -n` nameref, `eval`, and a `declare`, `typeset`, `local`, `export` or `readonly`
    whose variable name holds a `$` or a backtick (`declare -g "$n+=/lib"`). Any other way of
    computing the script's own directory (`dirname "$0"`, `${0%/*}`, `BASH_SOURCE`, `BASH_ARGV`,
    a positional slice `${@:...}` or `${*:...}`, and `$_` or `${_}`, which holds the script's
    path right after an exempted `$0` message). Any `$0` outside a message to stderr
    (`echo "usage: $0 ..." >&2` or `1>&2`), a `sed -n '<lines>p' "$0"` read of the script itself
    or an awk record (`f($0`, `, $0`, ` = $0`, `$0 ~`, `$0 !~`): `x=$0`, `a=($0)`, `printf -v`,
    `read <<<"$0"`, a function argument or a message to another fd would carry the directory
    under another name. A line naming `$GITHUB_ACTION_PATH` or the runner's `_actions`
    directory. Any other `source` or script invocation at a command position, and a sibling that
    cannot be fetched.
  - **`$SCRIPT_DIR` that is not the script's own directory.** A file sourced from a script in
    another directory that names `SCRIPT_DIR` at all, since it shares its caller's. A script run
    as its own process that uses `$SCRIPT_DIR` before a trusted definition, since it reads an
    inherited one. A relative definition, `$(dirname "$0")`, in any walked script while any walked
    script changes the working directory (`cd`, `pushd` or `popd` outside a `$(cd` or `(cd`
    subshell).
  - **The compare.** A compare that is not `ahead` (a pin rollback or unrelated history), and a
    file count near GitHub's ~300-file pagination cap.

  It does not see the following. Its threat model is a trusted organisation's own files: it
  catches shapes written by accident that would hide consumed code, not a determined adversary,
  and a shape built to evade a line scan can still pass.
  - A sibling reached without naming `$SCRIPT_DIR`, `$0` or the checkout: a hard-coded absolute
    path, a name found on `PATH`, or a path assembled at run time (`/proc/self`, a variable filled
    from a file).
  - Job-level reusable-workflow calls at a non-SHA ref
    (`go-kure/.github/.github/workflows/<file>.yml@main` here). They run at their own ref, so no
    pin bump changes them.
  - The order in which a script runs: a trusted `SCRIPT_DIR` definition inside a function or a
    branch that never runs still counts as defining it for the lines below.
  - A `,$0` outside awk, which is taken for an awk record (`for p in {x,$0}`).
  - A `$0` message to stderr that is read back: the stderr exemption assumes stderr is not
    redirected into a file or a capture (`exec 2>f`, `$(f 2>&1)`) that the script then reads.
  - The script's path taken from the call stack with `caller`, which names no `$0`.
  - An assignment through a name built at run time other than by a declaration builtin:
    `printf -v "$n"`, `read "$n"`, `mapfile "$n"`. A consumed script assigns through
    `printf -v "$destination"`, so refusing it would abort real runs.
  - A declaration builtin not written as a plain word at a command position:
    `\declare -g "$n+=/lib"`, `d=declare; $d -g …`, or one continued onto the next line with `\`.
  - In an action's `run:` step, the action path carried past the command and cut to its directory
    by a tool other than `dirname`, `realpath`, `readlink` or a trim (`sed`, `awk`, `cut`, a
    Python one-liner). It can be carried by `$_` after the command, an array assignment
    `x=("$GITHUB_ACTION_PATH/…")`, or an argument on a `\` continuation line.
  - The action path read through a name built at run time
    (`n=GITHUB_ACTION; n+=_PATH; "${!n}/…"`).

  It also aborts, harmlessly but falsely, on:
  - another directory derived from the script's own, such as
    `ROOT=$(cd "$(dirname "$0")/.." && pwd)`. Following it would mean tracking an arbitrary
    variable through every script that sources or inherits it, and real scripts reuse such names
    for argument-derived paths;
  - `uses:` or `repository:` text anywhere in a single-line workflow value
    (`run: grep -n "repository:" ci.yml`, `with: { repository: foo/bar }`), and `ref:` text
    anywhere in a `go-kure/.github` checkout step, and a ` #` inside a quoted value there (read
    as a comment, which leaves the quote open);
  - a second `repository: go-kure/.github` outside the `with:` mapping of a checkout that already
    names it there, such as under `env:`;
  - `${!prefix@}`, and `export SCRIPT_DIR` or `readonly SCRIPT_DIR` on a line of its own;
  - `dirname`, `realpath`, `readlink` or a parameter trim anywhere in an action that mentions
    `GITHUB_ACTION_PATH`, and a `$GITHUB_ACTION_PATH/<path>` command behind a wrapper (`env`,
    `timeout`), an environment assignment (`VAR=1 "$GITHUB_ACTION_PATH/…"`), a shell option
    (`bash --noprofile`), a `{ …; }` group or a `case` arm.

  The refusal paths, plus the no-change, inert, affected and acknowledged outcomes and the
  merge-queue mode, are pinned by hermetic cases in `scripts/test/cases/` — the same cases, under the same file names, as
  `go-kure/kure`'s — which the `lint` job runs on every push and merge-queue run, and on a PR
  whenever its `go` path filter matches (a workflow change, this script or anything under
  `scripts/test/` does) (`make test-pin-impact`, also part of `mise run verify` and
  `make precommit`);
  `scripts/test/pin-impact-lib.sh` stubs `curl` and builds a throwaway git repository per case,
  so no case touches the network. A maintainer who has reviewed a real hit and judged it safe adds
  the `pin-impact-ack` label to merge anyway — same convention as `check-doc-gate`'s `docs-skip`
  label; there is no other override. **Rerun gotcha:** the `strip-ack` step only runs when the triggering event's action was
  `synchronize` or `reopened`; re-running a stale/failed run of one of *those* (`gh run rerun`, or
  the Actions UI) replays that same original action and silently strips a freshly-added
  `pin-impact-ack` again before the gate re-checks it, even though nothing was pushed. A rerun of an
  `opened`/`labeled`/`unlabeled`-triggered run is unaffected — `strip-ack` skips it either way.
  **Scoped to same-repo PRs:** `strip-ack`'s `if:` also requires the PR's head repo to equal this
  repo, so it never runs at all on a fork PR — but that's moot, since the gate separately forces
  `PIN_IMPACT_ACK=false` unconditionally on forks; a fork PR has no acknowledgment path regardless
  of labels. Add (or re-add) the label rather than rerunning, on a same-repo PR; full writeup in
  `go-kure/.github`'s `docs/standards.md` § "Pin-impact-ack". **In the merge queue** the job runs
  `check-pin-impact.sh --consistency` instead. It checks only that every `go-kure/.github`
  reference in the merged tree pins the same commit. It reads no base, fetches nothing and has no
  label override. Two PRs that each passed on their own can combine into mixed pins: one adds a
  reference at the old pin while the other bumps the rest. On `main`, that tree is every later
  PR's base, and the gate refuses an inconsistent base before it reads the label, so it would
  refuse the repair PR too (go-kure/kure#951). The queue now ejects the PR instead; rebase it onto
  `main` and align its pins. The impact itself is still checked only on the PR (go-kure/kure#730)
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
  - Currently allowlisted: `GO-2026-6596` (Cilium HTTPRoute cross-namespace redirect). Upstream
    fixed it in 1.17.17, 1.18.11 and 1.19.5, and the 1.20 line Launcher pins is not affected. The
    unreviewed vuln DB report carries an `introduced 0` range with no fix, a placeholder for the
    1.18.0-1.18.11 range it could not map, so it flags every version and no bump can clear it. It
    lists no symbols, and Launcher reaches only the cilium CRD API types and package init (through
    kure's cilium builders), never Gateway API translation. Remove the entry once the vuln DB report
    is corrected (the `introduced 0` placeholder range dropped); tracked in go-kure/launcher#675.

---

## Deploy Docs Workflow

**File:** `.github/workflows/deploy-docs.yml`
**Name:** `Deploy Docs`

### Triggers

- **Push to main** (paths: `site/**`, `docs/**`, `*.md`, `CHANGELOG.md`, `DEVELOPMENT.md`,
  `scripts/gen-versions-toml.sh`)
- **Manual dispatch** with inputs: `version_slot`, `version_label`, `set_latest`.
  `version_slot` must be `dev` or start with `v` (e.g. `v0.1`), as one path segment, because a
  root write replaces everything under `launcher/` except `dev/` and the `v*/` slots.
  `version_label` must be `dev` or a release tag (`v0.1.0`, `v0.1.0-alpha.1`); with
  `set_latest=true` it must be a release tag. `set_latest` must be `true` or `false`. The
  `Determine version parameters` step checks all three before anything is built and fails the
  run on any other value. The
  inputs reach the shell only through environment variables, never as `${{ }}` expressions in a
  `run:` script, so a value is never parsed as shell syntax.

### How It Works

1. Determines version parameters (dev for push to main, explicit slot for manual dispatch)
2. Reads Hugo, Go and yq versions from `mise.toml`
3. Runs `scripts/gen-versions-toml.sh` to generate versioned Hugo config overlay
4. Builds the Hugo site targeting `https://www.gokure.dev/launcher/<slot>/`
5. If `set_latest=true`, also builds at `https://www.gokure.dev/launcher/`
6. Checks out `go-kure/go-kure.github.io` and deploys to the `launcher/` subdirectory through the
   shared `deploy-docs-push` action from `go-kure/.github`. When `set_latest=true`, the action
   fetches the tags again right before writing the root and writes `launcher/` only if the label
   is still the highest stable tag (the same rule Publish uses) and the checkout is that tag's
   commit. If the label is no longer the highest stable tag, it deploys the slot and leaves the
   root untouched. If it is, but the label is not an existing tag or the checked-out commit is not
   the tag's, the deploy fails and pushes nothing. Dispatch again with the highest existing stable
   tag as both the ref and the label, its slot, and `set_latest=true`
   (`gh workflow run deploy-docs.yml --ref <tag> -f version_slot=<vX.Y> -f version_label=<tag> -f set_latest=true`):
   the label itself when only the ref was wrong. An older tag would deploy only its slot and leave
   the root untouched.

**Credentials.** The launcher checkout sets `persist-credentials: false`: the repository is
public, so the action's tag fetch needs no token, and no later step can read the job token from
`.git/config`. The `go-kure.github.io` checkout keeps `DEPLOY_TOKEN` persisted, because the action
pushes with the checkout's own credential. That checkout runs after both Hugo builds, so the
deploy step is the only workflow step that follows it. This orders the steps; it does not isolate
the token, which stays on the runner for the rest of the job where a process left running by an
earlier step could still read it.

### Trigger Matrix

| Event | Deploys To | BaseURL |
|-------|-----------|---------|
| Push to `main` (docs paths) | `launcher/dev/` | `www.gokure.dev/launcher/dev/` |
| `workflow_dispatch` | `launcher/<slot>/` | `www.gokure.dev/launcher/<slot>/` |
| `workflow_dispatch` + `set_latest=true` | `launcher/<slot>/` + `launcher/` | both |

### Concurrency

Per-slot group (`deploy-docs-<slot>`) with `cancel-in-progress: false` — two deploys **to the same
slot** queue rather than cancel, so neither is dropped.

Deploys to *different* slots are not serialised: the group name includes the slot, so a `v1.2`
deploy and a `v1.3` deploy run at the same time and push to the same docs repository. The
`deploy-docs-push` action handles that race. When a push is rejected because the other deploy
moved the pages branch, it starts again from the new tip, writes this deploy's slot again (keeping
the other slot's content), re-checks the root decision, and pushes. It retries up to five
attempts, then fails the deploy. Any other push failure fails the deploy immediately.

Not covered: two deploys to the *same* slot are queued, not re-checked, so an older patch release
deployed after a newer one still replaces that slot's content. A tag cut before this workflow
version runs the deploy from its own ref, so its deploy uses the workflow as it was at that tag.

### Preservation

Only the target slot is replaced. Other `launcher/v*/`, `launcher/dev/`, `CNAME`, and `.nojekyll`
are preserved. The root `launcher/` files are only overwritten when `set_latest=true`, the label
is still the highest stable tag when the deploy writes, and the deploy's checkout is that tag's
commit. When the label ranks highest but is not an existing tag, or its tag is not the
checked-out commit, the deploy fails (see How It Works); when it does not rank highest, the slot
still deploys. A root write replaces everything directly under `launcher/` except `dev/` and the
`v*/` slots.

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

## Releasing

**Release** (`.github/workflows/release.yml`) is the one manual release workflow: pick the branch,
what to do, and whether it is a dry run. It calls `go-kure/.github`'s shared `release.yml`, whose
script makes the release commits, the tag and, when `main` moves to a new line, the release branch.
**Release / Publish** (`.github/workflows/release-publish.yml`) then runs by itself on the tag.
How to release, what each option does, release branches and the recovery procedure for a failed
publish are on the Releasing page (`docs/releasing.md`), which is the same text in every go-kure
repository: it is vendored from `go-kure/.github`, and CI's `docs-build` job byte-compares it at
the pinned revision.

What is specific to launcher:

- **What Publish produces:** the `kurel` binaries for linux × amd64/arm64 as `tar.gz` archives,
  `checksums.txt`, an SBOM per archive, and a cosign signature bundle of the checksums
  (`.goreleaser.yml`). How many assets a complete release carries is decided by `.goreleaser.yml`
  at that tag.
- **`guard-tag-ref`:** the wrapper's own job refuses a `workflow_dispatch` on anything but a `v*`
  tag before the privileged publisher starts. The check that a tag has no release yet runs inside
  the shared publisher.

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

- Issue comments and PR review comments (when `@claude` is mentioned)
- Issues opened or assigned
- PR reviews submitted

No `pull_request` trigger: a `pull_request` event carries no `@claude` mention, so the job
would only start and immediately skip (go-kure/.github#222, fixed org-wide in
go-kure/.github#223).

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

**Module cache** (dependency-only, split restore + save per Go job, like the build cache
below):

```yaml
- name: Restore Go modules cache
  id: gomod
  uses: actions/cache/restore@v6
  with:
    path: ~/go/pkg/mod
    key: ${{ runner.os }}-gomod-${{ hashFiles('**/go.sum') }}
    restore-keys: |
      ${{ runner.os }}-gomod-
# ... end of job ...
- name: Save Go modules cache
  if: success() && steps.gomod.outputs.cache-hit != 'true' && github.ref == 'refs/heads/main'
  uses: actions/cache/save@v6
  with:
    path: ~/go/pkg/mod
    key: ${{ steps.gomod.outputs.cache-primary-key }}
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
  if: success() && steps.gocache.outputs.cache-hit != 'true' && github.ref == 'refs/heads/main'
  uses: actions/cache/save@v6
  with:
    path: ~/.cache/go-build
    key: ${{ steps.gocache.outputs.cache-primary-key }}
```

Purpose prefixes: `gocache-validate-`, `gocache-test-race-cover-`, `gocache-security-`,
`gocache-build-` (the `cross-platform` job adds `<os>-<arch>` because cross-compiled artifacts
differ per target). The source hash covers `**/*.go`, `go.mod`, `go.sum`, `Makefile`, and
`**/testdata/**`. The save runs only on a non-exact (fallback/miss) restore, only when the
run succeeded (so a broken build never publishes a cache), and only on `main` (see below).

**Cross-ref scoping caveat.** GitHub caches are ref-scoped: a `pull_request` cache lives on
`refs/pull/N/merge` and is **not** visible to the `merge_group` (merge-queue) run — verified
empirically. The only scope both PR and queue runs can read is the default branch (`main`).
So these caches are warmed by push-to-main runs; a code-changing PR and its queue run restore
main's cache via **restore-key fallback** (not an exact hit) and Go reuses unchanged package
entries internally. This lowers the absolute cost of both runs but does **not** deduplicate the
PR↔queue build — that duplication is inherent to the merge queue and cannot be removed with
GitHub-scoped caches.

**Saves are default-branch only.** Because nothing but the PR itself can read a PR-scoped entry,
and the `gh-readonly-queue/*` branch a queue run saves to is deleted right after the run, every
save step is gated on `github.ref == 'refs/heads/main'`. Saving from those refs never helps a
later run; it only fills the size-capped cache server, whose LRU eviction then pushes out the
`main` entries every run actually restores. The same rule applies to any new cache step: use
split restore/save with the save gated to `main`, never the combined `actions/cache` (which
saves on a miss from any ref). The `docs-build` Hugo modules cache follows the rule too, since it
also carries `~/go/pkg/mod`. Only the small tool-binary caches keyed on a pinned version (yq,
Hugo, lychee) keep the combined form: their key rarely changes, so they write almost nothing.

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
