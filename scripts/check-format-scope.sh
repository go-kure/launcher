#!/usr/bin/env bash
# check-format-scope.sh — require the (format) scope on commits that change what
# app.yaml accepts (go-kure/launcher#1279).
#
# AGENTS.md "Document-format changes" says a commit that changes the accepted
# app.yaml format must use the `(format)` scope, because cliff.toml groups those
# commits into the changelog's Document Format section and a consumer re-pinning
# launcher scans only that section. Nothing enforced it: a policy-type change landed
# as `fix(oam)` and the section stayed empty. This guard enforces it on a commit range.
#
# A commit fails when it changes a non-test file of the acceptance set below and
# neither:
#   - its subject starts with the scope pattern, the one cliff.toml groups by
#     (`--print-scope-pattern` prints it; the self-test fails if the two differ), nor
#   - its body carries a trailer line `Format-Scope: none (<reason>)` with a reason.
#     Use the trailer when the change leaves the accepted format as it was, such as
#     a comment, an error message or a refactor with no behaviour change.
#
# The acceptance set (v1) is the code that decides which documents parse:
#   pkg/oam/validate.go    type allowlists (components, traits, policies, parameters)
#   pkg/oam/types.go       the app.yaml model
#   pkg/oam/capability.go  capability declarations
#   pkg/oam/parser.go      the parse entry point
#   pkg/oam/package.go     the kurel.yaml document type
#   pkg/oam/profile.go     the cluster.yaml document type
#   pkg/oam/property_validate.go           the property validators
#   pkg/oam/property_validate_authored.go  the authored-property validators
#   pkg/oam/lowering.go                    the lowering engine, which refuses documents it cannot lower
#   pkg/oam/featuregate_check.go           the feature-gate check
#   pkg/oam/in_document_collisions.go      two applications of one document generating one object
#   pkg/oam/controller_claims.go           the objects a generated HelmRelease claims, for that check
#   pkg/oam/reserved_metadata.go           reserved label and annotation keys on generated objects
#   pkg/oam/rendered_reserved.go           which platform-reserved property values a document may hold
#   pkg/oam/object_kind_policy.go          the object kind policy
#   pkg/oam/component_ownership.go         the component label and the content it is stamped into
#   pkg/oam/component_label_check.go       foreign component label values and selectors
#   pkg/oam/lowering_raw.go                the raw lowering path a downstream lowering consumer runs;
#                                          kurel's app.yaml path does not
#   pkg/oam/builtin/       every .go file below it (the per-kind property validators)
#   pkg/oam/internal/      every .go file below it (the shared refusal tables and checks)
#   pkg/oam/model/         every .go file below it (the Model document and its loader)
# Under these prefixes a *_test.go file, a contract.go or doc.go file, and a file under
# testdata/ never count. A *_test.go file never counts anywhere. The repository holds no
# standalone schema files; the document schemas are the Go types above.
#
# Not caught: a change inside a counted file that does not alter what is accepted but is
# not marked by the trailer, and acceptance code outside this set. Two examples:
# pkg/oam/cross_document_collisions.go refuses a generated object produced by more than one
# document, but nothing calls it yet; and the rest of pkg/oam/ is still to be audited
# (go-kure/launcher#1367). Reviewers remain the backstop.
#
# Skips merge commits and Renovate's commits (its email, as check-issue-refs.sh does).
# In a shallow clone it exits 2 when a boundary commit lies inside the range, because
# rev-list then stops short without an error; a shallow clone with a complete range is
# still scanned.
#
# Usage: check-format-scope.sh [--root DIR] --commits [BASE] [--head REV]
#        check-format-scope.sh --print-scope-pattern
# Exit:  0 clean, 1 a commit lacks the scope, 2 usage or scan error.

set -euo pipefail

# Must equal the Document Format parser's message in cliff.toml (ERE, as git-cliff's
# regex crate reads it for these constructs).
SCOPE_RE='^(feat|fix|refactor|docs?)\(format\)'

# What must follow the scope. cliff.toml sets filter_unconventional, so a header without
# the colon and space (with or without the breaking marker) is dropped from the changelog
# even though SCOPE_RE matches its prefix.
SCOPE_TAIL_RE='^!?: '

ACCEPT_PATHS=(
  pkg/oam/validate.go
  pkg/oam/types.go
  pkg/oam/capability.go
  pkg/oam/parser.go
  pkg/oam/package.go
  pkg/oam/profile.go
  pkg/oam/property_validate.go
  pkg/oam/property_validate_authored.go
  pkg/oam/lowering.go
  pkg/oam/featuregate_check.go
  pkg/oam/in_document_collisions.go
  pkg/oam/controller_claims.go
  pkg/oam/reserved_metadata.go
  pkg/oam/rendered_reserved.go
  pkg/oam/object_kind_policy.go
  pkg/oam/component_ownership.go
  pkg/oam/component_label_check.go
  pkg/oam/lowering_raw.go
)

# The builtin component, trait and policy handlers refuse and build in one method, and their
# data tables refuse without building an error, so every non-test .go file under these prefixes
# counts too, except the file names below (contract metadata and the package comment).
# pkg/oam/internal/ holds the shared refusal tables and checks the validators call.
# pkg/oam/model/ is the Model document (a launcher.gokure.dev/v1alpha1 kind) and its loader.
ACCEPT_PREFIXES=(pkg/oam/builtin/ pkg/oam/internal/ pkg/oam/model/)
ACCEPT_SKIP_NAMES=(contract.go doc.go)

RENOVATE_EMAIL="renovate@whitesourcesoftware.com"

ROOT="."
MODE=""
BASE="origin/main"
HEAD_REV=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) ROOT="${2:-}"; [[ -n "$ROOT" ]] || { echo "ERROR: --root needs a directory" >&2; exit 2; }; shift 2 ;;
    --commits)
      MODE="commits"
      if [[ $# -ge 2 && "$2" != -* ]]; then BASE="$2"; shift; fi
      shift ;;
    --head) HEAD_REV="${2:-}"; [[ -n "$HEAD_REV" ]] || { echo "ERROR: --head needs a revision" >&2; exit 2; }; shift 2 ;;
    --print-scope-pattern) printf '%s\n' "$SCOPE_RE"; exit 0 ;;
    -h|--help) sed -n '2,/^# Exit:/p' "$0"; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [[ "$MODE" != "commits" ]]; then
  echo "ERROR: pass --commits [BASE] to name the range to check" >&2
  exit 2
fi
HEAD_REV="${HEAD_REV:-HEAD}"

# resolve REV: the commit REV names in ROOT, or exit 2.
resolve() {
  local oid
  if ! oid="$(git -C "$ROOT" rev-parse --verify --quiet "$1^{commit}")"; then
    echo "ERROR: '$1' is not a commit in $ROOT; run git fetch origin, or pass --commits <base>" >&2
    exit 2
  fi
  printf '%s\n' "$oid"
}

# touched_acceptance COMMIT: prints each acceptance file COMMIT changes.
touched_acceptance() {
  local commit="$1" path accept
  # -z prints each path raw and NUL-terminated, so core.quotePath cannot wrap a non-ASCII path in
  # quotes and hide it from the prefix and suffix tests, and a newline inside a name cannot split
  # one path into two. The loop reads the NUL records directly. A failing git prints one empty
  # record, which no path can be, and the loop then stops with exit 2.
  while IFS= read -r -d '' path; do
    if [[ -z "$path" ]]; then
      echo "ERROR: cannot list the files of commit $commit in $ROOT" >&2
      exit 2
    fi
    for accept in "${ACCEPT_PATHS[@]}"; do
      if [[ "$path" == "$accept" ]]; then
        printf '%s\n' "$path"
      fi
    done
    if builtin_acceptance "$path"; then
      printf '%s\n' "$path"
    fi
  done < <(git -C "$ROOT" diff-tree -z --root --no-commit-id -r --name-only "$commit" || printf '\0')
}

# builtin_acceptance PATH: true for a non-test Go file under one of ACCEPT_PREFIXES, other
# than a skipped name or a file under testdata/.
builtin_acceptance() {
  local path="$1" skip prefix under=1
  for prefix in "${ACCEPT_PREFIXES[@]}"; do
    [[ "$path" == "$prefix"* ]] && under=0
  done
  [[ "$under" == 0 && "$path" == *.go && "$path" != *_test.go ]] || return 1
  [[ "$path" != */testdata/* ]] || return 1
  for skip in "${ACCEPT_SKIP_NAMES[@]}"; do
    [[ "${path##*/}" != "$skip" ]] || return 1
  done
  return 0
}

# trailer_state BODY: "ok" for a Format-Scope trailer with a reason, "empty" for one
# without, "none" for no such line. BODY is the message after the subject.
trailer_state() {
  local line state="none"
  local re='^Format-Scope: none \((.*)\)[[:space:]]*$'
  while IFS= read -r line; do
    if [[ "$line" =~ $re && "${BASH_REMATCH[1]}" =~ [^[:space:]] ]]; then
      echo ok
      return
    elif [[ "$line" == Format-Scope:* ]]; then
      state="empty"
    fi
  done <<<"$1"
  echo "$state"
}

base="$(resolve "$BASE")" || exit 2
head="$(resolve "$HEAD_REV")" || exit 2
if ! commits="$(git -C "$ROOT" rev-list --no-merges "$base..$head")"; then
  echo "ERROR: git rev-list $BASE..$HEAD_REV failed in $ROOT" >&2
  exit 2
fi

# A shallow clone stops rev-list at its boundary commits without an error, so the range
# can look complete when it is not. A boundary commit inside base..head means history
# was cut inside the range: refuse to call the rest clean. A shallow clone whose range
# is complete (every boundary is outside it) is still scanned.
if [[ "$(git -C "$ROOT" rev-parse --is-shallow-repository)" == "true" ]]; then
  if ! shallow_file="$(git -C "$ROOT" rev-parse --path-format=absolute --git-path shallow)" ||
     ! in_range="$(git -C "$ROOT" rev-list "$base..$head")"; then
    echo "ERROR: cannot read the shallow boundary of $ROOT" >&2
    exit 2
  fi
  declare -A cut=()
  while IFS= read -r oid; do
    [[ -z "$oid" ]] || cut[$oid]=1
  done <"$shallow_file"
  while IFS= read -r oid; do
    if [[ -n "$oid" && -n "${cut[$oid]:-}" ]]; then
      echo "ERROR: shallow history inside the range $BASE..$HEAD_REV (cut at ${oid:0:8}): fetch more history (git fetch --unshallow)" >&2
      exit 2
    fi
  done <<<"$in_range"
fi

found=0
scanned=0
skipped=0
while IFS= read -r commit; do
  [[ -n "$commit" ]] || continue
  if ! meta="$(git -C "$ROOT" log -1 --format='%ae %s' "$commit")" ||
     ! body="$(git -C "$ROOT" log -1 --format='%b' "$commit")"; then
    echo "ERROR: cannot read commit $commit in $ROOT" >&2
    exit 2
  fi
  email="${meta%% *}"
  subject="${meta#* }"
  if [[ "${email,,}" == "$RENOVATE_EMAIL" ]]; then
    skipped=$((skipped + 1))
    continue
  fi
  scanned=$((scanned + 1))

  touched="$(touched_acceptance "$commit")" || exit 2
  [[ -n "$touched" ]] || continue
  if [[ "$subject" =~ $SCOPE_RE ]]; then
    scope_tail="${subject:${#BASH_REMATCH[0]}}"
    [[ "$scope_tail" =~ $SCOPE_TAIL_RE ]] && continue
  fi
  state="$(trailer_state "$body")"
  [[ "$state" == "ok" ]] && continue

  note=""
  [[ "$state" == "empty" ]] && note=" [Format-Scope trailer has no reason: write Format-Scope: none (<reason>)]"
  while IFS= read -r path; do
    echo "${commit:0:8} ($subject): $path$note"
  done <<<"$touched"
  found=$((found + 1))
done <<<"$commits"

if [[ "$found" -gt 0 ]]; then
  echo "" >&2
  echo "check-format-scope: $found commit(s) in $BASE..$HEAD_REV change what app.yaml accepts without the (format) scope." >&2
  echo "Reword the subject to feat(format), fix(format), refactor(format) or docs(format), or, if the accepted format is unchanged," >&2
  echo "add the body line 'Format-Scope: none (<reason>)'. See AGENTS.md \"Document-format changes\"." >&2
  echo "A flagged commit that is already on main means $BASE is stale: run git fetch origin and re-run." >&2
  exit 1
fi
echo "check-format-scope: OK ($scanned commit(s) in $BASE..$HEAD_REV, $skipped Renovate commit(s) skipped)"
