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

# Fixtures are literal file text, so a `${...}` in single quotes must not expand.
# shellcheck disable=SC2016

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
expect "ownerless other repository" 1 "a.md:1:" a.md 'removal tracked in kure#539'
expect "ownerless ref as link text" 1 "a.md:1:" a.md 'see [kure#539](https://github.com/go-kure/kure/issues/539)'
expect "ref beside a relative link" 1 "a.md:1:" a.md 'see [the section](design.md#12-foo) and kure#539'
expect "ref in an indexed Go call" 1 "a.go:1:" a.go 'callbacks[i]("fixed in #227")'
expect "unspaced ref in an indexed Go call" 1 "a.go:1:" a.go 'callbacks[i]("#227")'
expect "unspaced partial ref in an indexed Go call" 1 "a.go:1:" a.go 'callbacks[i]("launcher#278")'
expect "ref after a shell prefix trim" 1 "x.sh:1:" x.sh 'echo "${ports#80}" # see #227'
expect "ref after an array element trim" 1 "x.sh:1:" x.sh 'echo "${items[0]#80}" # see #227'
expect "ref inside an array subscript" 1 "x.sh:1:" x.sh 'echo "${items["tracked in #227"]#80}"'
expect "ref after a starred Go format verb" 1 "a.go:1:" a.go 'fmt.Printf("%#12.*x", 3, value) // see #227'
# Neither pattern above sees these: `_` counts as part of a word.
expect "emphasised bare ref" 1 "a.md:1:" a.md 'fixed in _#227_ last week'
expect "emphasised bare ref at line start" 1 "a.md:1:" a.md '_#227_ and later'
expect "emphasised partial ref" 1 "a.md:1:" a.md 'see _kure#539_'
expect "emphasised partial ref with an underscore" 1 "a.md:1:" a.md 'see _my_repo#539_'
expect "ref after a URL" 1 "a.md:1:" a.md 'see https://example.com/x and #227'
expect "ref after a single-quoted URL" 1 "x.sh:1:" x.sh "url='https://example.com/';#227"
expect "ref after a percent sign" 1 "a.md:1:" a.md 'done to 100% #227 next'
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
expect "relative link with a numbered anchor" 0 "" a.md 'see [x](file.md#12-foo) and [y](../foo.md#34-bar)'
expect "link target with nested parentheses" 0 "" a.md 'see [section](design(v2).md#12-foo)'
# A URL is stripped before matching, whatever precedes its anchor.
expect "bare URL with a numbered anchor" 0 "" a.md 'see https://example.com/page#12-foo'
expect "root-page URL anchor" 0 "" a.md 'see https://example.com/#12-foo'
expect "Go format verb with flags" 0 "" a.go 'fmt.Printf("%+#12.6g %-#8x", value, n)'
expect "indexed Go format verbs" 0 "" a.go 'fmt.Printf("%#12[1]x %#12.6[1]x", value)'
expect "starred Go format verb" 0 "" a.go 'fmt.Printf("%#12.*x", 3, value)'
expect "starred and indexed Go format verbs" 0 "" a.go 'fmt.Printf("%#*x %#.*x %#[2]*[1]x", 3, value)'
expect "shell prefix trim" 0 "" x.sh 'echo "${ports#80}"'
expect "shell longest prefix trim" 0 "" x.sh 'x=${value##123}'
expect "array element prefix trim" 0 "" x.sh 'echo "${items[0]#80}"'
expect "array longest prefix trim" 0 "" x.sh 'echo "${items[@]##80}"'
expect "emphasised qualified ref" 0 "" a.md 'see _go-kure/kure#539_'
expect "emphasised qualified ref, repository name led by an underscore" 0 "" a.md 'see _owner/_repo#539_'
expect "underscores inside a word" 0 "" a.go 'a_#12_b'
expect "double underscores" 0 "" a.md '__#12__'
expect "single-digit name#N" 0 "" a.go 't.Fatalf("resolve#2: %v", err)'
expect "HTML entity" 0 "" a.md 'a &#1234; entity'
expect "Go format verb" 0 "" a.go 'fmt.Printf("%#12.6g", value)'
expect "qualified name with a hyphen" 0 "" a.go '// see owner/kure-launcher#278'
expect "qualified name with a dot" 0 "" a.go '// see owner/foo.launcher#278'
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
