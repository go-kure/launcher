#!/usr/bin/env bash
# check-format-scope-test.sh — self-test for check-format-scope.sh (go-kure/launcher#1279).
#
# The guard is only proven if a commit that changes what app.yaml accepts, under a
# non-format scope, makes it fail. Each case commits one change into a throwaway git
# repository and scans base..HEAD. Three cases read the real repository instead:
# the scope pattern must equal the one cliff.toml groups by, and the commit that
# added the disruption trait (go-kure/launcher#1277) is the positive control.
#
# Usage: bash scripts/check-format-scope-test.sh
# (invoked via `make check-format-scope`, before the guard runs on the real range)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="$REPO_ROOT/scripts/check-format-scope.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0
skip=0

# check <name> <want-rc> <want-substring-or-empty> <rc> <out>
check() {
  local name="$1" want_rc="$2" want_out="$3" rc="$4" out="$5"
  if [[ "$rc" -ne "$want_rc" ]]; then
    echo "FAIL $name: exit $rc, want $want_rc"
    printf '%s\n' "$out" | sed 's/^/    /'
    fail=$((fail + 1))
    return
  fi
  if [[ -n "$want_out" && "$out" != *"$want_out"* ]]; then
    echo "FAIL $name: output lacks \"$want_out\""
    printf '%s\n' "$out" | sed 's/^/    /'
    fail=$((fail + 1))
    return
  fi
  pass=$((pass + 1))
}

# expect <name> <want-rc: 0|1|2> <want-substring-or-empty> <author-email> <path> <message> [base] [head]
# Commits <path> with <message> by <author-email> on top of a base commit, then scans
# base..HEAD. [base] overrides the base given to the guard; [head] "base" scans the
# empty range base..base, anything else is passed as --head.
expect() {
  local name="$1" want_rc="$2" want_out="$3" email="$4" path="$5" message="$6" base="${7:-}" head="${8:-}" rc=0 out
  local -a head_arg=()
  # No hooks and no signing from the caller's global git config.
  local -a git=(git -C "$WORK/repo" -c core.hooksPath=/dev/null -c commit.gpgSign=false)
  rm -rf "$WORK/repo"
  mkdir -p "$WORK/repo"
  git -C "$WORK/repo" init -q
  "${git[@]}" -c user.name=base -c user.email=base@example.com commit -q --allow-empty -m 'base'
  local base_oid
  base_oid="$(git -C "$WORK/repo" rev-parse HEAD)"
  [[ -n "$base" ]] || base="$base_oid"
  [[ "$head" != "base" ]] || head="$base_oid"
  [[ -z "$head" ]] || head_arg=(--head "$head")
  mkdir -p "$WORK/repo/$(dirname "$path")"
  printf 'x\n' >"$WORK/repo/$path"
  git -C "$WORK/repo" add -A
  "${git[@]}" -c user.name=author -c "user.email=$email" commit -q -m "$message"
  out="$(bash "$GATE" --root "$WORK/repo" --commits "$base" "${head_arg[@]}" 2>&1)" || rc=$?
  check "$name" "$want_rc" "$want_out" "$rc" "$out"
}

DEV=dev@example.com

# ── must fail: an acceptance file changes under a scope other than (format) ──
expect "validate.go under fix(oam)" 1 "fix(oam): x" $DEV pkg/oam/validate.go 'fix(oam): x'
expect "validate.go under feat(traits)" 1 "pkg/oam/validate.go" $DEV pkg/oam/validate.go 'feat(traits): add a trait'
expect "validate.go with no scope" 1 "feat: x" $DEV pkg/oam/validate.go 'feat: x'
expect "types.go" 1 "pkg/oam/types.go" $DEV pkg/oam/types.go 'fix(oam): x'
expect "capability.go" 1 "pkg/oam/capability.go" $DEV pkg/oam/capability.go 'refactor(oam): x'
expect "parser.go" 1 "pkg/oam/parser.go" $DEV pkg/oam/parser.go 'chore: x'
expect "package.go (kurel.yaml document)" 1 "pkg/oam/package.go" $DEV pkg/oam/package.go 'fix(oam): x'
expect "profile.go (cluster.yaml document)" 1 "pkg/oam/profile.go" $DEV pkg/oam/profile.go 'fix(oam): x'
expect "scope must lead the subject" 1 "docs: mention fix(format)" $DEV pkg/oam/validate.go 'docs: mention fix(format)'
expect "scope on a type outside the changelog group" 1 "chore(format): x" $DEV pkg/oam/validate.go 'chore(format): x'
# cliff.toml sets filter_unconventional: a header without the colon never reaches the
# Document Format section, so the prefix match alone must not be enough.
expect "format scope without the colon" 1 "fix(format) x" $DEV pkg/oam/validate.go 'fix(format) x'
expect "format scope with a breaking marker and no colon" 1 "fix(format)!x" $DEV pkg/oam/validate.go 'fix(format)!x'
expect "format scope followed by text" 1 "fix(format)ted: x" $DEV pkg/oam/validate.go 'fix(format)ted: x'
expect "trailer with an empty reason" 1 "Format-Scope" $DEV pkg/oam/validate.go $'fix(oam): x\n\nFormat-Scope: none ()'
expect "trailer with a blank reason" 1 "Format-Scope" $DEV pkg/oam/validate.go $'fix(oam): x\n\nFormat-Scope: none (   )'
expect "trailer with no parentheses" 1 "Format-Scope" $DEV pkg/oam/validate.go $'fix(oam): x\n\nFormat-Scope: none'
expect "trailer on the subject line only" 1 "Format-Scope" $DEV pkg/oam/validate.go 'fix(oam): x Format-Scope: none (reason)'

# ── must pass ────────────────────────────────────────────────────────────────
expect "validate.go under fix(format)" 0 "1 commit(s)" $DEV pkg/oam/validate.go 'fix(format): x'
expect "validate.go under feat(format)" 0 "" $DEV pkg/oam/validate.go 'feat(format): x'
expect "validate.go under refactor(format)" 0 "" $DEV pkg/oam/validate.go 'refactor(format): x'
expect "validate.go under docs(format)" 0 "" $DEV pkg/oam/validate.go 'docs(format): x'
expect "validate.go under doc(format)" 0 "" $DEV pkg/oam/validate.go 'doc(format): x'
expect "format scope with a breaking marker" 0 "" $DEV pkg/oam/validate.go 'feat(format)!: x'
expect "test file beside the acceptance code" 0 "" $DEV pkg/oam/validate_test.go 'fix(oam): x'
expect "file outside the acceptance list" 0 "" $DEV pkg/oam/lowering.go 'fix(oam): x'
expect "same file name in another directory" 0 "" $DEV pkg/other/validate.go 'fix(oam): x'
expect "trailer with a reason" 0 "" $DEV pkg/oam/validate.go $'fix(oam): x\n\nFormat-Scope: none (only a comment changed)'
expect "trailer after other trailers" 0 "" $DEV pkg/oam/validate.go $'fix(oam): x\n\nCloses go-kure/launcher#1\nFormat-Scope: none (error text only)'
expect "Renovate-authored change" 0 "1 Renovate commit(s) skipped" renovate@whitesourcesoftware.com pkg/oam/validate.go 'chore(deps): x'
# CI passes the PR head with --head; a range that stops before the commit is clean.
expect "head before the acceptance commit" 0 "0 commit(s)" $DEV pkg/oam/validate.go 'fix(oam): x' "" base

# ── usage errors ─────────────────────────────────────────────────────────────
expect "unresolvable base" 2 "is not a commit" $DEV pkg/oam/validate.go 'fix(oam): x' no-such-base
expect "unresolvable head" 2 "is not a commit" $DEV pkg/oam/validate.go 'fix(oam): x' "" no-such-head

# ── shallow history (a range cut short must not read as clean) ──────────────
# expect_shallow <name> <want-rc> <want-substring-or-empty> <boundary: base|a|b|c> <message-of-a>
# History base <- a (validate.go) <- b <- c, then a `shallow` file naming <boundary>
# as the commit whose parents are unknown, as a --depth clone leaves it. The objects
# stay, so both ends of base..c resolve and only the cut can hide a commit. A boundary
# commit reads as a root commit that adds every file, so b and c carry the format scope:
# without the cut check they scan clean and a (hidden) fails to show, as in a real clone.
expect_shallow() {
  local name="$1" want_rc="$2" want_out="$3" boundary="$4" msg_a="$5" rc=0 out
  local -a git=(git -C "$WORK/repo" -c core.hooksPath=/dev/null -c commit.gpgSign=false -c user.name=dev -c user.email=dev@example.com)
  local oid_base oid_a oid_b oid_c
  rm -rf "$WORK/repo"
  mkdir -p "$WORK/repo/pkg/oam"
  git -C "$WORK/repo" init -q
  "${git[@]}" commit -q --allow-empty -m 'base'
  oid_base="$(git -C "$WORK/repo" rev-parse HEAD)"
  printf 'x\n' >"$WORK/repo/pkg/oam/validate.go"
  git -C "$WORK/repo" add -A
  "${git[@]}" commit -q -m "$msg_a"
  oid_a="$(git -C "$WORK/repo" rev-parse HEAD)"
  "${git[@]}" commit -q --allow-empty -m 'fix(format): b'
  oid_b="$(git -C "$WORK/repo" rev-parse HEAD)"
  "${git[@]}" commit -q --allow-empty -m 'fix(format): c'
  oid_c="$(git -C "$WORK/repo" rev-parse HEAD)"
  case "$boundary" in
    base) printf '%s\n' "$oid_base" >"$WORK/repo/.git/shallow" ;;
    a) printf '%s\n' "$oid_a" >"$WORK/repo/.git/shallow" ;;
    b) printf '%s\n' "$oid_b" >"$WORK/repo/.git/shallow" ;;
    c) printf '%s\n' "$oid_c" >"$WORK/repo/.git/shallow" ;;
  esac
  out="$(bash "$GATE" --root "$WORK/repo" --commits "$oid_base" --head "$oid_c" 2>&1)" || rc=$?
  check "$name" "$want_rc" "$want_out" "$rc" "$out"
}

# The cut hides the offending commit, so only the intersection check can catch it.
expect_shallow "boundary inside the range, offender hidden" 2 "shallow history inside the range" b 'fix(oam): x'
expect_shallow "boundary at the head" 2 "git fetch --unshallow" c 'fix(oam): x'
# The base is the boundary: base..c is complete, so the guard still reads it.
expect_shallow "shallow clone, range complete, clean" 0 "3 commit(s)" base 'fix(format): x'
expect_shallow "shallow clone, range complete, offender flagged" 1 "pkg/oam/validate.go" base 'fix(oam): x'

# ── the pattern is the one cliff.toml groups by ──────────────────────────────
# A scope the guard accepts but cliff.toml does not group would pass CI and still
# leave the change out of the Document Format section of the changelog.
rc=0
cliff_line="$(grep -F 'group = "Document Format"' "$REPO_ROOT/cliff.toml")" || rc=$?
if [[ "$rc" -ne 0 || -z "$cliff_line" ]]; then
  check "cliff.toml has a Document Format parser" 0 "" 1 "no line with group = \"Document Format\" in cliff.toml"
else
  cliff_pat="${cliff_line#*message = \"}"
  cliff_pat="${cliff_pat%%\", group*}"
  # TOML basic string: `\\` is one backslash.
  cliff_pat="${cliff_pat//\\\\/\\}"
  rc=0
  gate_pat="$(bash "$GATE" --print-scope-pattern 2>&1)" || rc=$?
  if [[ "$rc" -eq 0 && "$gate_pat" == "$cliff_pat" ]]; then
    check "scope pattern equals cliff.toml" 0 "" 0 ""
  else
    check "scope pattern equals cliff.toml" 0 "" 1 "guard (rc=$rc): $gate_pat"$'\n'"cliff.toml:        $cliff_pat"
  fi
fi

# ── positive control: the commit that added the disruption trait ─────────────
# It touched pkg/oam/validate.go under feat(traits), so the guard must fail it.
# A shallow checkout (push events) lacks the commit; say so instead of passing.
control=2f7e966e
if git -C "$REPO_ROOT" rev-parse --verify --quiet "$control^{commit}" >/dev/null &&
   git -C "$REPO_ROOT" rev-parse --verify --quiet "$control^^{commit}" >/dev/null; then
  rc=0
  out="$(bash "$GATE" --root "$REPO_ROOT" --commits "$control^" --head "$control" 2>&1)" || rc=$?
  check "positive control: the disruption-trait commit fails" 1 "${control:0:8}" "$rc" "$out"
else
  echo "SKIP positive control: $control is not in this clone (shallow checkout)"
  skip=$((skip + 1))
fi

echo "check-format-scope-test: $pass passed, $fail failed, $skip skipped"
[[ "$fail" -eq 0 ]]
