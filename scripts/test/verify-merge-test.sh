#!/usr/bin/env bash
# verify-merge-test.sh — self-test for scripts/verify-merge.sh.
#
# Builds a throwaway upstream repository that carries GitHub-shaped
# refs/pull/<N>/head and refs/pull/<N>/merge refs, clones it, and runs
# verify-merge.sh against each PR. Every case is one exit code the script must
# return; no network, no GitHub. Run: bash scripts/test/verify-merge-test.sh
#
# Cases:
#   1  merge ref builds and tests green                      -> 0
#   2  branch compiles, merge ref does not (semantic conflict) -> 1
#   3  both parents' tests pass, merged tests fail           -> 1
#   4  merge ref names an older head than local HEAD (stale) -> 2
#   5  PR has no merge ref (e.g. it conflicts)               -> 2
#   6  no PR number                                          -> 64
#   7  go is not on PATH                                     -> 2
#   8  module download fails (stub go, network down)         -> 2
#   9  merged go.mod needs a newer Go, GOTOOLCHAIN=local     -> 2
#  10  the build never sees module files the download wrote -> 0
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SUT="$ROOT/scripts/verify-merge.sh"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/verify-merge-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

UP="$WORK/upstream"
g() { git -C "$UP" "$@"; }

write() { mkdir -p "$(dirname "$UP/$1")"; printf '%s\n' "$2" > "$UP/$1"; }
commit() { g add -A && g commit -q -m "$1"; }

# A pull ref pair the way GitHub lays it out: head = branch tip, merge = a
# merge commit whose first parent is the base and second parent is the head.
pull_refs() { # <n> <branch> [merge-of-branch-ref]
	local n="$1" br="$2" head merge
	head="$(g rev-parse "$br")"
	g update-ref "refs/pull/$n/head" "$head"
	if [ "${3:-}" != "nomerge" ]; then
		g checkout -q --detach main
		g merge -q --no-ff --no-edit "$head" >/dev/null
		merge="$(g rev-parse HEAD)"
		g update-ref "refs/pull/$n/merge" "$merge"
		g checkout -q main
	fi
}

git init -q -b main "$UP"
write go.mod 'module example.invalid/fx

go 1.22'
write a.go 'package fx

func Helper() int { return 1 }'
write a_test.go 'package fx

import "testing"

func TestHelper(t *testing.T) {
	if Helper() < 1 {
		t.Fatal("helper")
	}
}'
commit base
BASE="$(g rev-parse HEAD)"

# PR 1: harmless addition.
g checkout -q -b pr1 "$BASE"
write c.go 'package fx

func C() int { return 3 }'
commit pr1

# PR 2: the branch removes Helper; main grows a new caller after the merge base.
g checkout -q -b pr2 "$BASE"
write a.go 'package fx

func Other() int { return 2 }'
write a_test.go 'package fx

import "testing"

func TestOther(t *testing.T) {
	if Other() != 2 {
		t.Fatal("other")
	}
}'
commit pr2

# PR 3: the branch changes Helper's value; main adds a test pinning the old one.
g checkout -q -b pr3 "$BASE"
write a.go 'package fx

func Helper() int { return 2 }'
commit pr3

# PR 4: head moves on after the merge ref was computed.
g checkout -q -b pr4 "$BASE"
write d.go 'package fx

func D() int { return 4 }'
commit pr4-first

# PR 5: head only, no merge ref.
g checkout -q -b pr5 "$BASE"
write e.go 'package fx

func E() int { return 5 }'
commit pr5

# PR 6: the branch raises the go directive past any installed toolchain.
g checkout -q -b pr6 "$BASE"
write go.mod 'module example.invalid/fx

go 1.999'
commit pr6

# main moves past the merge base.
g checkout -q main
write b.go 'package fx

func Use() int { return Helper() }'
write b_test.go 'package fx

import "testing"

func TestHelperIsOne(t *testing.T) {
	if Helper() != 1 {
		t.Fatal("helper must be 1")
	}
}'
commit main-grows

pull_refs 1 pr1
pull_refs 2 pr2
pull_refs 3 pr3
pull_refs 4 pr4
pull_refs 5 pr5 nomerge
pull_refs 6 pr6
# PR 4's branch advances; its merge ref is now stale.
g checkout -q pr4
write d.go 'package fx

func D() int { return 44 }'
commit pr4-second
g update-ref refs/pull/4/head "$(g rev-parse HEAD)"
g checkout -q main

# A PATH with the tools the script needs besides go, and one whose go is a stub
# failing the way an unreachable module proxy does.
NOGO="$WORK/nogo-bin"
mkdir -p "$NOGO"
for t in git tar mktemp mkdir rm cp; do ln -s "$(command -v "$t")" "$NOGO/$t"; done
STUB="$WORK/stub-bin"
mkdir -p "$STUB"
printf '%s\n' '#!/bin/sh' \
	'echo "go: example.invalid/dep@v1.0.0: Get \"https://proxy.golang.org/example.invalid/dep/@v/v1.0.0.mod\": dial tcp: lookup proxy.golang.org: no such host" >&2' \
	'exit 1' >"$STUB/go"
chmod +x "$STUB/go"
# A go whose `mod download` appends a malformed line to the go.mod it is given and
# the go.sum beside it, then passes everything else to the real go: if the build
# ever saw the module files the download wrote to, it would fail to parse them.
# (The fixture has no dependencies, so only go.mod is certain to be read.)
SCRIBBLE="$WORK/scribble-bin"
mkdir -p "$SCRIBBLE"
# The $ expressions are the stub's own, written out literally.
# shellcheck disable=SC2016
printf '%s\n' '#!/bin/sh' \
	'if [ "$1" = mod ] && [ "$2" = download ]; then' \
	'	modfile=go.mod' \
	'	for a in "$@"; do case "$a" in -modfile=*) modfile="${a#-modfile=}" ;; esac; done' \
	'	echo "written-by-mod-download" >>"$modfile"' \
	'	echo "written-by-mod-download" >>"${modfile%.mod}.sum"' \
	'	exit 0' \
	'fi' \
	"exec '$(command -v go)' \"\$@\"" >"$SCRIBBLE/go"
chmod +x "$SCRIBBLE/go"

fail=0
# CASE_ENV: NAME=value assignments for the script's environment in one case.
CASE_ENV=()
run_case() { # <name> <branch> <want-rc> <want-substring> [args...]
	local name="$1" br="$2" want="$3" sub="$4" clone out rc
	shift 4
	clone="$WORK/clone-$name"
	git clone -q "$UP" "$clone"
	git -C "$clone" checkout -q "origin/$br" 2>/dev/null || git -C "$clone" checkout -q "$br"
	set +e
	out="$(cd "$clone" && env "${CASE_ENV[@]}" "$BASH" "$SUT" "$@" 2>&1)"
	rc=$?
	set -e
	if [ "$rc" -ne "$want" ] || ! grep -qF -- "$sub" <<<"$out"; then
		echo "FAIL $name: rc=$rc (want $want), want output containing: $sub"
		while IFS= read -r line; do printf '    %s\n' "$line"; done <<<"$out"
		fail=1
	else
		echo "ok   $name (rc=$rc)"
	fi
}

run_case green-merge      pr1  0  "merge ref builds and tests green" 1
run_case merge-no-compile pr2  1  "undefined: Helper"                2
run_case merge-test-fails pr3  1  "helper must be 1"                 3
run_case stale-merge-ref  pr4  2  "stale"                            4
run_case no-merge-ref     pr5  2  "not computable"                   5
run_case no-pr-number     pr1  64 "usage:"
# A missing or unusable Go is the environment, not the merge ref: never exit 1.
CASE_ENV=("PATH=$NOGO")
run_case no-go-on-path    pr1  2  "go is not on PATH"                1
CASE_ENV=("PATH=$STUB:$PATH")
run_case module-download  pr1  2  "go mod download failed"           1
CASE_ENV=(GOTOOLCHAIN=local)
run_case toolchain-local  pr6  2  "go mod download failed"           6
# The build sees the go.mod/go.sum the merge ref carries, not ones the download
# wrote to.
CASE_ENV=("PATH=$SCRIBBLE:$PATH")
run_case download-on-a-copy pr1 0 "merge ref builds and tests green" 1
CASE_ENV=()

exit "$fail"
