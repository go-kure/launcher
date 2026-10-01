#!/usr/bin/env bash
# check-issue-refs-test.sh — self-test for check-issue-refs.sh (go-kure/launcher#400).
#
# The guard is only proven if a bare reference makes it fail. Each case writes one
# file into a throwaway git repository, runs the guard on that repository, and
# asserts the exit status and, for a failure, the reported file:line. The fixtures
# live in this script so the guard's own scan of the real tree never sees them.
#
# Usage: bash scripts/check-issue-refs-test.sh
# (invoked via `make check-issue-refs`, before the guard runs on the real tree)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="$REPO_ROOT/scripts/check-issue-refs.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

# expect <name> <want-rc: 0|1> <want-substring-or-empty> <path> <content>
# Starts from an empty repository each time, so one case cannot mask another.
expect() {
  local name="$1" want_rc="$2" want_out="$3" path="$4" content="$5" rc=0 out
  rm -rf "$WORK/repo"
  mkdir -p "$WORK/repo/$(dirname "$path")"
  printf '%s\n' "$content" >"$WORK/repo/$path"
  git -C "$WORK/repo" init -q
  git -C "$WORK/repo" add -A
  out="$(bash "$GATE" --root "$WORK/repo" 2>&1)" || rc=$?
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

# ── must fail ────────────────────────────────────────────────────────────────
expect "bare ref in a Go comment" 1 "a.go:2:" a.go $'package a\n// fixed in (#227)'
expect "slash-joined pair" 1 "a.go:1:" a.go '// go-kure/launcher#227/#242 routing target'
expect "pre- prefix" 1 "a.go:1:" a.go '// the pre-#444 assertion'
expect "partial launcher ref" 1 "a.go:1:" a.go '// see launcher#278'
expect "bare ref at line start" 1 "notes.md:1:" notes.md '#123 is the tracking issue'
expect "bare ref in a shell comment" 1 "x.sh:1:" x.sh '# Re-introduce the #417 defect'
expect "bare ref in a Go string" 1 "a.go:1:" a.go 't.Errorf("want #227 component label")'
expect "five-digit ref" 1 "a.md:1:" a.md 'tracked in #12345.'
expect "bare ref in YAML" 1 "c.yaml:1:" c.yaml 'key: value # see #321'
expect "ref beside an anchor" 1 "a.md:1:" a.md 'see [the section](#12-foo) and #99'
expect "hyphen before a partial ref" 1 "a.go:1:" a.go '// the pre-launcher#278 shape'
expect "dots before a partial ref" 1 "a.go:1:" a.go '// see ...launcher#278'
expect "colon in the file name" 1 "a:b.md:1:" a:b.md 'see #99'
expect "pragma-like file name" 1 "a:b:allow-ref.md:1:" a:b:allow-ref.md 'see #99'

# ── must pass ────────────────────────────────────────────────────────────────
expect "qualified launcher ref" 0 "" a.go '// fixed in (go-kure/launcher#227, go-kure/launcher#242)'
expect "other repository" 0 "" a.md 'removal tracked in [go-kure/kure#539](https://github.com/go-kure/kure/issues/539)'
expect "Markdown anchor" 0 "" a.md 'see [the section](#12-foo)'
expect "HTML entity" 0 "" a.md 'a &#1234; entity'
expect "Go format verb" 0 "" a.go 'fmt.Printf("%#12.6g", value)'
expect "single digit" 0 "" a.md 'step #1 comes first'
expect "hex colour" 0 "" a.md 'color: #abcdef and #12ab34'
expect "six-digit number" 0 "" a.md 'id #123456'
expect "pragma" 0 "" a.go '// #227 is quoted on purpose allow-ref'
expect "generated changelog" 0 "" CHANGELOG.md '- fix: something (#227)'
expect "testdata fixture" 0 "" pkg/x/testdata/in.yaml 'name: app # #227'
expect "root testdata fixture" 0 "" testdata/in.yaml 'name: app # #227'
expect "file type out of scope" 0 "" a.txt 'see #227'

echo "check-issue-refs-test: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
