#!/usr/bin/env bash
# check-issue-refs-test.sh — self-test for check-issue-refs.sh (go-kure/launcher#400).
#
# The guard is only proven if a bare reference makes it fail. Each case writes one
# file into a throwaway git repository, runs the guard on that repository, and
# asserts the exit status and, for a failure, the reported file:line. The commit
# cases (go-kure/launcher#732) commit one message instead and scan base..HEAD.
# The fixtures live in this script so the guard's own scan of the real tree never
# sees them.
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

# ── commit messages (--commits, go-kure/launcher#732) ───────────────────────
# expect_commits <name> <want-rc: 0|1|2> <want-substring-or-empty> <author-email> <message> [base] [head]
# Commits one file with <message> by <author-email> on top of a base commit, then
# scans base..HEAD. [base] overrides the base the guard is given; [head], passed
# as --head, the other end (`base` names the base commit).
expect_commits() {
  local name="$1" want_rc="$2" want_out="$3" email="$4" message="$5" base="${6:-}" head="${7:-}" rc=0 out
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
  printf 'x\n' >"$WORK/repo/x.txt"
  git -C "$WORK/repo" add -A
  "${git[@]}" -c user.name=author -c "user.email=$email" commit -q -m "$message"
  out="$(bash "$GATE" --root "$WORK/repo" --commits "$base" "${head_arg[@]}" 2>&1)" || rc=$?
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

expect_commits "bare closing trailer" 1 "(fix: x): Closes #732" dev@example.com $'fix: x\n\nCloses #732'
expect_commits "bare ref in a subject" 1 "(fix: x (#732)): fix: x (#732)" dev@example.com 'fix: x (#732)'
expect_commits "ownerless ref in a body" 1 "): see launcher#278" dev@example.com $'fix: x\n\nsee launcher#278'
expect_commits "qualified closing trailer" 0 "1 commit message(s)" dev@example.com $'fix: x\n\nCloses go-kure/launcher#732'
expect_commits "pragma in a commit body" 0 "" dev@example.com $'fix: x\n\nquoted on purpose: #732 allow-ref'
expect_commits "Renovate-authored bare ref" 0 "1 Renovate commit(s) skipped" renovate@whitesourcesoftware.com $'chore(deps): update x\n\nCloses #732'
expect_commits "unresolvable base" 2 "is not a commit" dev@example.com 'fix: x' no-such-base
# CI passes the PR head with --head; a range that stops before the bare commit is clean.
expect_commits "head before the bare commit" 0 "0 commit message(s)" dev@example.com $'fix: x\n\nCloses #732' "" base
expect_commits "unresolvable head" 2 "is not a commit" dev@example.com 'fix: x' "" no-such-head

# ── shallow history (go-kure/launcher#1303: a range cut short must not read as clean) ──
# expect_shallow <name> <want-rc: 0|1|2> <want-substring-or-empty> <boundary: base|a|b|c> <message-of-a> [shim-mode] [nonl]
# History base <- a <- b <- c, then a `shallow` file naming <boundary> as the commit whose
# parents are unknown, as a --depth clone leaves it. The objects stay, so both ends of
# base..c resolve and only the cut can hide a commit: rev-list stops at the boundary
# without an error, so a (which carries <message-of-a>) is missing from the scan.
# shim-mode puts a `git` in front of the real one for the gate run only: `junk` makes
# `rev-parse --is-shallow-repository` print its own option name, as a git that does not know
# it does; `missing` makes `rev-parse --git-path shallow` name a file that does not exist.
# shape rewrites the boundary line: `nonl` drops the final newline and `crlf` ends the line with
# CR LF, both of which git accepts; `upper` writes the oid in uppercase hex, which git accepts
# too.
expect_shallow() {
  local name="$1" want_rc="$2" want_out="$3" boundary="$4" msg_a="$5" shim_mode="${6:-}" shape="${7:-}" rc=0 out nl='\n'
  local -a git=(git -C "$WORK/repo" -c core.hooksPath=/dev/null -c commit.gpgSign=false -c user.name=dev -c user.email=dev@example.com)
  local -a gate_env=()
  local oid_base oid_a oid_b oid_c real_git
  case "$shape" in
    nonl) nl='' ;;
    crlf) nl='\r\n' ;;
  esac
  if [[ -n "$shim_mode" ]]; then
    real_git="$(command -v git)"
    mkdir -p "$WORK/shim"
    cat >"$WORK/shim/git" <<SHIM
#!/bin/bash
case "\$SHIM_MODE:\$*" in
  junk:*--is-shallow-repository*) echo "--is-shallow-repository"; exit 0 ;;
  missing:*"--git-path shallow"*) echo "$WORK/no-such-shallow-file"; exit 0 ;;
esac
exec "$real_git" "\$@"
SHIM
    chmod +x "$WORK/shim/git"
    gate_env=(env "PATH=$WORK/shim:$PATH" "SHIM_MODE=$shim_mode")
  fi
  rm -rf "$WORK/repo"
  mkdir -p "$WORK/repo"
  git -C "$WORK/repo" init -q
  "${git[@]}" commit -q --allow-empty -m 'base'
  oid_base="$(git -C "$WORK/repo" rev-parse HEAD)"
  "${git[@]}" commit -q --allow-empty -m "$msg_a"
  oid_a="$(git -C "$WORK/repo" rev-parse HEAD)"
  "${git[@]}" commit -q --allow-empty -m 'fix: b'
  oid_b="$(git -C "$WORK/repo" rev-parse HEAD)"
  "${git[@]}" commit -q --allow-empty -m 'fix: c'
  oid_c="$(git -C "$WORK/repo" rev-parse HEAD)"
  case "$boundary" in
    base) printf "%s$nl" "$oid_base" >"$WORK/repo/.git/shallow" ;;
    a) printf "%s$nl" "$oid_a" >"$WORK/repo/.git/shallow" ;;
    b) printf "%s$nl" "$oid_b" >"$WORK/repo/.git/shallow" ;;
    c) printf "%s$nl" "$oid_c" >"$WORK/repo/.git/shallow" ;;
  esac
  case "$shape" in
    upper) tr 'a-f' 'A-F' <"$WORK/repo/.git/shallow" >"$WORK/shallow.upper" && mv "$WORK/shallow.upper" "$WORK/repo/.git/shallow" ;;
  esac
  out="$("${gate_env[@]}" bash "$GATE" --root "$WORK/repo" --commits "$oid_base" --head "$oid_c" 2>&1)" || rc=$?
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

# The cut hides the offending commit a, so only the intersection check can catch it.
expect_shallow "boundary inside the range, offender hidden" 2 "shallow history inside the range" b $'fix: x\n\nCloses #732'
expect_shallow "boundary at the head" 2 "git fetch --unshallow" c 'fix: x'
# The commit directly above the base is inside the range, so it is refused too, although its
# parent happens to be the base here: the guard cannot tell that from a cut (policy, not a gap).
expect_shallow "boundary directly above the base" 2 "shallow history inside the range" a 'fix: x'
# The base is the boundary: base..c is complete, so the guard still reads it.
expect_shallow "shallow clone, range complete, clean" 0 "3 commit message(s)" base 'fix: x'
expect_shallow "shallow clone, range complete, offender flagged" 1 "Closes #732" base $'fix: x\n\nCloses #732'
# The guard must not fail open (go-kure/launcher#1303 review): an answer that is neither true
# nor false, an unreadable boundary file and a final boundary with no newline all exit 2.
expect_shallow "shallow probe prints junk" 2 "cannot read the shallow boundary" b $'fix: x\n\nCloses #732' junk
expect_shallow "shallow file cannot be opened" 2 "cannot read the shallow boundary" b $'fix: x\n\nCloses #732' missing
expect_shallow "boundary without a final newline" 2 "shallow history inside the range" b $'fix: x\n\nCloses #732' "" nonl
# Git also accepts an uppercase oid and a CR LF line end; the guard reads both as the cut they are.
# (A line that is no oid is not tested: git itself dies on it before the guard reads the file.)
expect_shallow "boundary oid in uppercase hex" 2 "shallow history inside the range" b $'fix: x\n\nCloses #732' "" upper
expect_shallow "boundary line ends in CR LF" 2 "shallow history inside the range" b $'fix: x\n\nCloses #732' "" crlf

echo "check-issue-refs-test: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
