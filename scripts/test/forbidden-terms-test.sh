#!/usr/bin/env bash
# forbidden-terms-test.sh — self-test for the tools that write files the
# forbidden-terms guard (site/scripts/check-forbidden-terms.sh) scans
# (go-kure/launcher#1119): scripts/forbidden-terms-lib.sh, which
# scripts/vendor-flux-crds.sh and scripts/vendor-external-secrets-crds.sh read
# the guard's matcher through, and cliff.toml's postprocessors, which scrub the
# changelog the release script commits before its guard preflight.
#
# Every sample is built from the guard's TERMS_RE at run time, so this file
# names no term. A hit sample is a term as a whole word or in one of the guard's
# identifier shapes, some next to a non-ASCII letter; a near miss is a term
# inside a word the guard accepts. The guard's classes are ASCII under the C
# locale and Unicode under C.UTF-8, and it runs in whatever locale its runner
# sets, so every verdict is the guard's own, run --full-tree on a throwaway
# repository in both: a hit is refused in at least one, a near miss accepted in
# both. No network.
#
# Cases:
#   1  the guard refuses each hit sample and accepts each near miss, and
#      ft_hits (the External Secrets vendor's refusal) agrees on every one
#   2  a CRD-shaped file with every sample is refused; after ft_add_pragmas
#      (the Flux vendor's pragma pass) it is accepted, carries one pragma line
#      per hit line, and is the original bytes with its pragma lines stripped
#   3  the unscrubbed commit subjects are refused; rendered by git-cliff with
#      cliff.toml, they are accepted, and the near misses are kept as written.
#      git-cliff must be on PATH (mise installs it; CI's docs-build job installs
#      the pinned release): its Rust regex engine is the one that scrubs a
#      release's changelog, and no other engine stands in for it
#
# Run: bash scripts/test/forbidden-terms-test.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GUARD="$ROOT/site/scripts/check-forbidden-terms.sh"
# shellcheck source=scripts/forbidden-terms-lib.sh
source "$ROOT/scripts/forbidden-terms-lib.sh"
ft_load "$GUARD"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/forbidden-terms-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

fail=0
pass() { printf 'PASS %s\n' "$1"; }
flunk() {
  printf 'FAIL %s\n' "$1"
  shift
  printf '    %s\n' "$@"
  fail=1
}

# HITS: each term as a whole word and in each identifier shape the guard
# matches, and next to a non-ASCII letter, which C and C.UTF-8 read apart
# (é after a capitalised term is a lowercase letter under C.UTF-8 and no letter
# under C). MISSES: each term inside a word the guard accepts in both.
HITS=()
MISSES=()
for t in ${TERMS_RE//|/ }; do
  HITS+=("$t" "${t^^}" "new${t^}Client" "v2${t^}" "Test_${t^}" "${t}Client" "${t^}Client" "${t^^}_HOST" "${t^^}2")
  HITS+=("é${t^}Client" "x${t^}É" "x${t^}é" "é${t}" "${t}é" "${t^}Ö")
  # The Kelvin sign (U+212A) and the long s (U+017F), written as UTF-8 bytes:
  # they fold to ASCII letters under a case-insensitive regex, and are no word
  # characters under C.
  HITS+=($'\xe2\x84\xaa'"${t}" "${t}"$'\xc5\xbf')
  MISSES+=("new${t^}s" "${t}line" "${t^}s" "x${t}" "é${t}line" "x${t}é")
done

# guard_verdict DIR: "refused" when the guard, run --full-tree on DIR's tracked
# files, refuses them in any of FT_LOCALES; "accepted" when it accepts them in
# all.
guard_verdict() {
  local loc
  for loc in "${FT_LOCALES[@]}"; do
    if ! (cd "$1" && LC_ALL="$loc" bash "$GUARD" --full-tree >/dev/null 2>&1); then
      echo refused
      return
    fi
  done
  echo accepted
}

# guard_report DIR: the guard's first refusals of DIR, for a failure message.
guard_report() {
  local loc
  for loc in "${FT_LOCALES[@]}"; do
    (cd "$1" && LC_ALL="$loc" bash "$GUARD" --full-tree 2>&1 | head -3) || true
  done
}

# repo_with NAME FILE...: a throwaway repository holding each FILE under pkg/,
# where the guard scans, tracked.
repo_with() {
  local dir="$WORK/$1" f
  shift
  git init -q "$dir"
  mkdir -p "$dir/pkg"
  for f in "$@"; do
    cp "$f" "$dir/pkg/"
  done
  git -C "$dir" add -A
  echo "$dir"
}

# Case 1: the guard's verdict and ft_hits's, sample by sample.
bad=()
i=0
for s in "${HITS[@]}" "${MISSES[@]}"; do
  i=$((i + 1))
  want=refused
  if ((i > ${#HITS[@]})); then
    want=accepted
  fi
  f="$WORK/s$i.yaml"
  printf 'name: %s\n' "$s" >"$f"
  got="$(guard_verdict "$(repo_with "r$i" "$f")")"
  hits="$(ft_hits "$f")"
  if [[ "$got" != "$want" ]]; then
    bad+=("guard $got '$s', want $want: the samples no longer match the guard's shapes")
  elif [[ "$want" == refused && -z "$hits" ]] || [[ "$want" == accepted && -n "$hits" ]]; then
    bad+=("ft_hits disagrees with the guard on '$s' (guard: $got)")
  fi
done
if ((${#bad[@]} == 0)); then
  pass "guard-and-ft_hits-agree (${#HITS[@]} hits, ${#MISSES[@]} near misses, locales ${FT_LOCALES[*]})"
else
  flunk guard-and-ft_hits-agree "${bad[@]}"
fi

# Case 2: the Flux vendor's pragma pass on a CRD-shaped file.
crd="$WORK/crd.yaml"
{
  echo "spec:"
  echo "  versions:"
  for s in "${HITS[@]}" "${MISSES[@]}"; do
    printf '    - description: the %s field\n' "$s"
  done
} >"$crd"
pragmad="$WORK/pragmad/crd.yaml"
mkdir -p "$WORK/pragmad"
ft_add_pragmas "$crd" "$pragmad"
PRAGMA_RE='^ *# allow-term:[a-z]+( allow-term:[a-z]+)*$'
bad=()
[[ "$(guard_verdict "$(repo_with raw "$crd")")" == refused ]] || bad+=("the guard accepts the file before the pragma pass: the case proves nothing")
[[ "$(guard_verdict "$(repo_with pragmad "$pragmad")")" == accepted ]] || bad+=("the guard refuses the file after the pragma pass: $(guard_report "$WORK/pragmad")")
n="$(grep -cE "$PRAGMA_RE" "$pragmad" || true)"
[[ "$n" == "${#HITS[@]}" ]] || bad+=("$n pragma lines, want one per hit line, ${#HITS[@]}")
cmp -s <(grep -vE "$PRAGMA_RE" "$pragmad") "$crd" || bad+=("the file without its pragma lines is not the original bytes")
if ((${#bad[@]} == 0)); then
  pass flux-vendor-pragma-pass
else
  flunk flux-vendor-pragma-pass "${bad[@]}"
fi

# Case 3: cliff.toml's postprocessors, run by git-cliff, on commit subjects
# holding every sample.
subjects="$WORK/subjects.md"
for s in "${HITS[@]}" "${MISSES[@]}"; do
  printf 'feat: add the %s option\n' "$s"
done >"$subjects"
if [[ "$(guard_verdict "$(repo_with unscrubbed "$subjects")")" == refused ]]; then
  pass changelog-fixture-bites
else
  flunk changelog-fixture-bites "the guard accepts the unscrubbed subjects: the case proves nothing"
fi

if ! command -v git-cliff >/dev/null 2>&1; then
  flunk changelog-scrub "git-cliff is not on PATH; install it (mise install, or the pinned release) and run again"
  exit 1
fi
repo="$WORK/cliff-repo"
git init -q "$repo"
while IFS= read -r subject; do
  git -C "$repo" commit -q --allow-empty -m "$subject"
done <"$subjects"
changelog="$WORK/CHANGELOG.md"
(cd "$repo" && git-cliff --config "$ROOT/cliff.toml" --unreleased) >"$changelog" 2>"$WORK/git-cliff.err" \
  || { flunk changelog-scrub "git-cliff failed: $(head -3 "$WORK/git-cliff.err")"; exit 1; }
bad=()
n="$(grep -c ' option$' "$changelog" || true)"
[[ "$n" == "$((${#HITS[@]} + ${#MISSES[@]}))" ]] || bad+=("the changelog holds $n entries, want one per subject, $((${#HITS[@]} + ${#MISSES[@]}))")
[[ "$(guard_verdict "$(repo_with scrubbed "$changelog")")" == accepted ]] \
  || bad+=("the guard refuses the scrubbed changelog: $(guard_report "$WORK/scrubbed")")
for s in "${MISSES[@]}"; do
  grep -qF "the $s option" "$changelog" || bad+=("the near miss '$s' was scrubbed")
done
if ((${#bad[@]} == 0)); then
  pass "changelog-scrub (git-cliff $(git-cliff --version | sed 's/^git-cliff //'))"
else
  flunk changelog-scrub "${bad[@]}"
fi

exit "$fail"
