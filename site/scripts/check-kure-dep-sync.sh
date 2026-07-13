#!/usr/bin/env bash
# check-kure-dep-sync.sh — Guards launcher against leading the kure release it
# imports on a shared DIRECT dependency.
#
# Go's Minimum Version Selection makes launcher's effective version of a shared dep
# max(launcher, kure), so launcher can never fall *below* the kure it imports — only
# race *ahead*, compiling kure's own code against a dependency version kure never
# released against. This guard flags only that "ahead" direction, and only for deps
# that are DIRECT requires of both launcher and the imported kure. Indirect deps are
# MVS-governed (they float to the max across launcher's whole graph) and are out of
# scope. See AGENTS.md § "Shared dependencies with kure".
#
# Modes:
#   --base <ref>   Diff-scoped enforcement. Compares HEAD go.mod against <ref>'s and
#                  fails only when a PR *introduces or increases* launcher's lead over
#                  the imported kure. Existing drift is grandfathered. Used by PR CI,
#                  merge-queue, and local make check/precommit.
#   --report       Whole-tree visibility. Lists current shared-direct lead, never
#                  fails. Used by push/schedule CI and for local inspection.
#
# The script is self-contained: it fetches the base ref itself so a workflow reorder
# can't move it ahead of an external fetch step and silently break it.
#
# Usage: check-kure-dep-sync.sh --base origin/main
#        check-kure-dep-sync.sh --report

set -euo pipefail

export GOWORK=off  # workspace go.work must not perturb module resolution

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HELPER_DIR="$REPO_ROOT/site/scripts/kuredepsync"
KURE_MODULE="github.com/go-kure/kure"

MODE=""
BASE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base) MODE="base"; BASE="${2:-}"; [[ -n "$BASE" ]] || { echo "ERROR: --base needs a ref" >&2; exit 2; }; shift 2 ;;
    --report) MODE="report"; shift ;;
    -h|--help) sed -n '2,29p' "$0"; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$MODE" ]] || { echo "usage: $0 --base <ref> | --report" >&2; exit 2; }

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# Build the helper once up front (separate nested module, so `go run <dir>` from the
# main module can't resolve it; building also avoids `go run`'s "exit status 1" stderr
# noise while propagating the real exit code). File args passed to it are absolute.
HELPER_BIN="$TMPDIR/kuredepsync"
( cd "$HELPER_DIR" && GOWORK=off go build -o "$HELPER_BIN" . )
run_helper() { "$HELPER_BIN" "$@"; }

# kure_version_of <go.mod path> — the imported kure require version, parsed by the
# helper via modfile (robust against comment/replace noise, no line-scraping).
kure_version_of() {
  local ver
  ver="$("$HELPER_BIN" --print-kure-version "$1")" \
    || { echo "ERROR: could not read $KURE_MODULE version from $1" >&2; exit 2; }
  printf '%s' "$ver"
}

# kure_gomod_for <version> — download that kure release and echo its go.mod path.
kure_gomod_for() {
  local ver="$1" path
  GOWORK=off go mod download "$KURE_MODULE@$ver" \
    || { echo "ERROR: go mod download $KURE_MODULE@$ver failed" >&2; exit 2; }
  path="$(GOWORK=off go env GOMODCACHE)/cache/download/$KURE_MODULE/@v/$ver.mod"
  [[ -f "$path" ]] || { echo "ERROR: kure go.mod not found at $path" >&2; exit 2; }
  printf '%s' "$path"
}

LAUNCHER_HEAD="$REPO_ROOT/go.mod"
KURE_HEAD_VER="$(kure_version_of "$LAUNCHER_HEAD")"
KURE_HEAD="$(kure_gomod_for "$KURE_HEAD_VER")"

if [[ "$MODE" == "report" ]]; then
  run_helper --launcher-head "$LAUNCHER_HEAD" \
    --kure-head "$KURE_HEAD" --kure-head-version "$KURE_HEAD_VER" --report
  exit 0
fi

# --base mode: resolve the base go.mod, fetching the ref ourselves.
resolve_base() {
  local ref="$1"
  # Try as-is; if the object is missing, fetch it.
  if ! git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref^{commit}" >/dev/null; then
    local remote="origin" branch="$ref"
    [[ "$ref" == origin/* ]] && branch="${ref#origin/}"
    git -C "$REPO_ROOT" fetch --no-tags --depth=1 "$remote" "$branch" >/dev/null 2>&1 || true
  fi
  git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref^{commit}" >/dev/null
}

if ! resolve_base "$BASE"; then
  # In CI a required check must never silently pass: an unresolved base means the guard
  # cannot enforce, so fail hard. Locally (offline precommit), degrade to report-only.
  if [[ "${CI:-}" == "true" ]]; then
    echo "ERROR: could not resolve base ref '$BASE' in CI; refusing to skip enforcement." >&2
    exit 2
  fi
  echo "WARN: could not resolve base ref '$BASE' (offline?); reporting only, not enforcing." >&2
  run_helper --launcher-head "$LAUNCHER_HEAD" \
    --kure-head "$KURE_HEAD" --kure-head-version "$KURE_HEAD_VER" --report
  exit 0
fi

LAUNCHER_BASE="$TMPDIR/launcher-base.mod"
git -C "$REPO_ROOT" show "$BASE:go.mod" > "$LAUNCHER_BASE"
KURE_BASE="$(kure_gomod_for "$(kure_version_of "$LAUNCHER_BASE")")"

run_helper \
  --launcher-head "$LAUNCHER_HEAD" --kure-head "$KURE_HEAD" --kure-head-version "$KURE_HEAD_VER" \
  --launcher-base "$LAUNCHER_BASE" --kure-base "$KURE_BASE"
