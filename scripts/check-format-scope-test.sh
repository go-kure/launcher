#!/usr/bin/env bash
# check-format-scope-test.sh — self-test for check-format-scope.sh (go-kure/launcher#1279).
#
# The guard is only proven if a commit that changes what app.yaml accepts, under a
# non-format scope, makes it fail. Each case commits one change into a throwaway git
# repository and scans base..HEAD. The last cases read the real repository instead:
# the scope pattern must equal the one cliff.toml groups by, and past commits that
# changed acceptance code (the first added the disruption trait, go-kure/launcher#1277)
# are the controls.
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
# The builtin handlers refuse and build in one method, and their data tables refuse without
# building an error, so every non-test .go file under pkg/oam/builtin/ counts (go-kure/launcher#1310).
expect "builtin component handler" 1 "pkg/oam/builtin/components/typed_pod_rules.go" $DEV pkg/oam/builtin/components/typed_pod_rules.go 'fix(components): x'
expect "builtin component data table" 1 "pkg/oam/builtin/components/kind_required.go" $DEV pkg/oam/builtin/components/kind_required.go 'fix(oam): x'
expect "builtin trait schema" 1 "pkg/oam/builtin/traits/schema.go" $DEV pkg/oam/builtin/traits/schema.go 'feat(traits): x'
expect "builtin policy handler" 1 "pkg/oam/builtin/policies/placement.go" $DEV pkg/oam/builtin/policies/placement.go 'fix(oam): x'
expect "builtin registry" 1 "pkg/oam/builtin/registry/registry.go" $DEV pkg/oam/builtin/registry/registry.go 'feat(registry): x'
expect "builtin top-level handler" 1 "pkg/oam/builtin/networkpolicy.go" $DEV pkg/oam/builtin/networkpolicy.go 'fix(oam): x'
# Git quotes a non-ASCII path in --name-only output unless the guard reads NUL-delimited paths;
# pin core.quotePath=true so a caller's global setting cannot hide the case.
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.quotePath GIT_CONFIG_VALUE_0=true \
  expect "builtin handler with a non-ASCII name" 1 "pkg/oam/builtin/components/café.go" $DEV pkg/oam/builtin/components/café.go 'fix(components): x'
# A newline inside a file name must neither hide a counted file nor make a text file count.
expect "builtin handler with a newline in its name" 1 "pkg/oam/builtin/components/with" $DEV $'pkg/oam/builtin/components/with\nnewline.go' 'fix(components): x'
expect "text file whose name embeds a builtin go path" 0 "" $DEV $'pkg/oam/builtin/components/x.go\nnotes.txt' 'fix(components): x'
expect "builtin file with an empty trailer reason" 1 "Format-Scope" $DEV pkg/oam/builtin/components/typed_pod_rules.go $'fix(components): x\n\nFormat-Scope: none ()'
# The property validators, the lowering engine and the feature-gate check refuse documents too,
# and pkg/oam/internal/ holds refusal tables (go-kure/launcher#1314).
expect "property validator" 1 "pkg/oam/property_validate.go" $DEV pkg/oam/property_validate.go 'fix(oam): x'
expect "authored property validator" 1 "pkg/oam/property_validate_authored.go" $DEV pkg/oam/property_validate_authored.go 'fix(oam): x'
expect "lowering engine" 1 "pkg/oam/lowering.go" $DEV pkg/oam/lowering.go 'fix(oam): x'
expect "feature-gate check" 1 "pkg/oam/featuregate_check.go" $DEV pkg/oam/featuregate_check.go 'feat(oam): x'
expect "internal required-fields table" 1 "pkg/oam/internal/requiredfields/cilium.go" $DEV pkg/oam/internal/requiredfields/cilium.go 'fix(oam): x'
expect "internal Flux duration forms" 1 "pkg/oam/internal/fluxduration/fluxduration.go" $DEV pkg/oam/internal/fluxduration/fluxduration.go 'fix(oam): x'
expect "lowering engine with an empty trailer reason" 1 "Format-Scope" $DEV pkg/oam/lowering.go $'fix(oam): x\n\nFormat-Scope: none ()'
# The collision, ownership, reserved-key and kind-policy checks refuse what an authored document
# puts into its generated objects. The raw lowering path refuses authored raw documents. The
# Model document loader refuses an authored Model (go-kure/launcher#1362).
expect "in-document collision check" 1 "pkg/oam/in_document_collisions.go" $DEV pkg/oam/in_document_collisions.go 'fix(oam): x'
expect "controller claims" 1 "pkg/oam/controller_claims.go" $DEV pkg/oam/controller_claims.go 'fix(oam): x'
expect "reserved metadata keys" 1 "pkg/oam/reserved_metadata.go" $DEV pkg/oam/reserved_metadata.go 'fix(oam): x'
expect "rendered reserved values" 1 "pkg/oam/rendered_reserved.go" $DEV pkg/oam/rendered_reserved.go 'fix(oam): x'
expect "object kind policy" 1 "pkg/oam/object_kind_policy.go" $DEV pkg/oam/object_kind_policy.go 'feat(oam): x'
expect "component ownership" 1 "pkg/oam/component_ownership.go" $DEV pkg/oam/component_ownership.go 'fix(oam): x'
expect "component label check" 1 "pkg/oam/component_label_check.go" $DEV pkg/oam/component_label_check.go 'fix(oam): x'
expect "raw lowering path" 1 "pkg/oam/lowering_raw.go" $DEV pkg/oam/lowering_raw.go 'fix(oam): x'
expect "Model document loader" 1 "pkg/oam/model/load.go" $DEV pkg/oam/model/load.go 'test(format): x'

# ── must pass ────────────────────────────────────────────────────────────────
expect "validate.go under fix(format)" 0 "1 commit(s)" $DEV pkg/oam/validate.go 'fix(format): x'
expect "validate.go under feat(format)" 0 "" $DEV pkg/oam/validate.go 'feat(format): x'
expect "validate.go under refactor(format)" 0 "" $DEV pkg/oam/validate.go 'refactor(format): x'
expect "validate.go under docs(format)" 0 "" $DEV pkg/oam/validate.go 'docs(format): x'
expect "validate.go under doc(format)" 0 "" $DEV pkg/oam/validate.go 'doc(format): x'
expect "format scope with a breaking marker" 0 "" $DEV pkg/oam/validate.go 'feat(format)!: x'
expect "test file beside the acceptance code" 0 "" $DEV pkg/oam/validate_test.go 'fix(oam): x'
expect "file outside the acceptance list" 0 "" $DEV pkg/oam/provenance.go 'fix(oam): x'
expect "same file name in another directory" 0 "" $DEV pkg/other/validate.go 'fix(oam): x'
expect "trailer with a reason" 0 "" $DEV pkg/oam/validate.go $'fix(oam): x\n\nFormat-Scope: none (only a comment changed)'
expect "trailer after other trailers" 0 "" $DEV pkg/oam/validate.go $'fix(oam): x\n\nCloses go-kure/launcher#1\nFormat-Scope: none (error text only)'
expect "Renovate-authored change" 0 "1 Renovate commit(s) skipped" renovate@whitesourcesoftware.com pkg/oam/validate.go 'chore(deps): x'
# CI passes the PR head with --head; a range that stops before the commit is clean.
expect "head before the acceptance commit" 0 "0 commit(s)" $DEV pkg/oam/validate.go 'fix(oam): x' "" base
# A builtin file under the format scope or the trailer passes, as the six listed files do.
expect "builtin handler under fix(format)" 0 "1 commit(s)" $DEV pkg/oam/builtin/components/typed_pod_rules.go 'fix(format): x'
expect "builtin handler with a trailer reason" 0 "" $DEV pkg/oam/builtin/components/typed_pod_rules.go $'refactor(components): x\n\nFormat-Scope: none (comment only)'
expect "lowering engine under fix(format)" 0 "1 commit(s)" $DEV pkg/oam/lowering.go 'fix(format): x'
expect "lowering engine with a trailer reason" 0 "" $DEV pkg/oam/lowering.go $'refactor(oam): x\n\nFormat-Scope: none (comment only)'
expect "property validator under feat(format)" 0 "" $DEV pkg/oam/property_validate.go 'feat(format): x'
expect "internal file under fix(format)" 0 "" $DEV pkg/oam/internal/requiredfields/cilium.go 'fix(format): x'
# Not part of the rule: tests and the package comment under pkg/oam/internal/, and the
# non-authorable synthesis types in pkg/oam/netpol/.
expect "internal test file" 0 "" $DEV pkg/oam/internal/requiredfields/requiredfields_test.go 'fix(oam): x'
expect "internal package comment" 0 "" $DEV pkg/oam/internal/requiredfields/doc.go 'docs(oam): x'
expect "netpol synthesis types" 0 "" $DEV pkg/oam/netpol/types.go 'fix(oam): x'
expect "test file beside the lowering engine" 0 "" $DEV pkg/oam/lowering_test.go 'fix(oam): x'
# Ruled out by go-kure/launcher#1362: an error type and its text (the refusal is decided
# elsewhere), a classifier of refusals, a registry check, and a check of a document set that
# nothing calls. Tests, the package comment and testdata under pkg/oam/model/ stay out too.
expect "name collision error type" 0 "" $DEV pkg/oam/name_collision.go 'fix(oam): x'
expect "hook group name error type" 0 "" $DEV pkg/oam/hook_group_name.go 'fix(oam): x'
expect "policy refusal classifier" 0 "" $DEV pkg/oam/policy_refusal.go 'fix(oam): x'
expect "lowering target registry check" 0 "" $DEV pkg/oam/lowering_targets.go 'fix(oam): x'
expect "cross-document collision check" 0 "" $DEV pkg/oam/cross_document_collisions.go 'fix(oam): x'
expect "Model package comment" 0 "" $DEV pkg/oam/model/doc.go 'docs(oam): x'
expect "Model testdata .go" 0 "" $DEV pkg/oam/model/testdata/x.go 'test(oam): x'
expect "Model test file" 0 "" $DEV pkg/oam/model/load_test.go 'test(format): x'
# Not part of the rule: tests, contract metadata, the package comment, vendored input,
# non-Go files, and a path that only contains the prefix.
expect "builtin test file" 0 "" $DEV pkg/oam/builtin/components/typed_pod_rules_test.go 'fix(components): x'
expect "builtin components contract.go" 0 "" $DEV pkg/oam/builtin/components/contract.go 'fix(components): x'
expect "builtin top-level contract.go" 0 "" $DEV pkg/oam/builtin/contract.go 'fix(oam): x'
expect "builtin policies doc.go" 0 "" $DEV pkg/oam/builtin/policies/doc.go 'docs(oam): x'
expect "builtin vendored testdata .go" 0 "" $DEV pkg/oam/builtin/components/testdata/upstream/kubernetes/pkg/apis/core/v1/x.go 'chore(deps): x'
expect "builtin README" 0 "" $DEV pkg/oam/builtin/components/README.md 'docs(components): x'
expect "prefix inside another path" 0 "" $DEV pkg/other/pkg/oam/builtin/components/x.go 'fix(oam): x'
expect "sibling directory sharing the prefix" 0 "" $DEV pkg/oam/builtinx/foo.go 'fix(oam): x'

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

# ── real-repo controls for the builtin path rule (go-kure/launcher#1310) ─────────
# 5a69e39a changed five production files under pkg/oam/builtin/components/ under a
# non-format subject, so the guard must fail it and name one of them.
# 660315cd changed six production files under fix(format), so the guard must pass it.
control_builtin_fail=5a69e39a
if git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_builtin_fail^{commit}" >/dev/null &&
   git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_builtin_fail^^{commit}" >/dev/null; then
  rc=0
  out="$(bash "$GATE" --root "$REPO_ROOT" --commits "$control_builtin_fail^" --head "$control_builtin_fail" 2>&1)" || rc=$?
  check "positive control: the builtin handler commit fails" 1 "typed_pod_rules.go" "$rc" "$out"
else
  echo "SKIP positive control: $control_builtin_fail is not in this clone (shallow checkout)"
  skip=$((skip + 1))
fi
control_builtin_pass=660315cd
if git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_builtin_pass^{commit}" >/dev/null &&
   git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_builtin_pass^^{commit}" >/dev/null; then
  rc=0
  out="$(bash "$GATE" --root "$REPO_ROOT" --commits "$control_builtin_pass^" --head "$control_builtin_pass" 2>&1)" || rc=$?
  check "negative control: the fix(format) builtin commit passes" 0 "" "$rc" "$out"
else
  echo "SKIP negative control: $control_builtin_pass is not in this clone (shallow checkout)"
  skip=$((skip + 1))
fi

# ── real-repo control for the validation and lowering files (go-kure/launcher#1314) ──
# 9e8c1d1d changed pkg/oam/lowering.go under a fix(oam) subject with no trailer, so the guard
# must fail it and name that file.
control_lowering_fail=9e8c1d1d
if git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_lowering_fail^{commit}" >/dev/null &&
   git -C "$REPO_ROOT" rev-parse --verify --quiet "$control_lowering_fail^^{commit}" >/dev/null; then
  rc=0
  out="$(bash "$GATE" --root "$REPO_ROOT" --commits "$control_lowering_fail^" --head "$control_lowering_fail" 2>&1)" || rc=$?
  check "positive control: the lowering engine commit fails" 1 "pkg/oam/lowering.go" "$rc" "$out"
else
  echo "SKIP positive control: $control_lowering_fail is not in this clone (shallow checkout)"
  skip=$((skip + 1))
fi

# ── real-repo controls for the collision and reserved-key checks (go-kure/launcher#1362) ──
# 3503a228 changed pkg/oam/in_document_collisions.go under fix(oam)! so that two unnamed Lists
# are no longer refused as one object. 5dcd15b0 changed pkg/oam/reserved_metadata.go and
# pkg/oam/component_label_check.go under fix(oam) so that another component's label value on an
# ArtifactGenerator is no longer refused. Neither has a trailer, so the guard must fail both.
for control_spec in "3503a228 pkg/oam/in_document_collisions.go the in-document collision commit" \
                    "5dcd15b0 pkg/oam/reserved_metadata.go the reserved-key commit"; do
  read -r control control_path control_name <<<"$control_spec"
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$control^{commit}" >/dev/null &&
     git -C "$REPO_ROOT" rev-parse --verify --quiet "$control^^{commit}" >/dev/null; then
    rc=0
    out="$(bash "$GATE" --root "$REPO_ROOT" --commits "$control^" --head "$control" 2>&1)" || rc=$?
    check "positive control: $control_name fails" 1 "$control_path" "$rc" "$out"
  else
    echo "SKIP positive control: $control is not in this clone (shallow checkout)"
    skip=$((skip + 1))
  fi
done

echo "check-format-scope-test: $pass passed, $fail failed, $skip skipped"
[[ "$fail" -eq 0 ]]
