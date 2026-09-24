#!/usr/bin/env bash
# verify-merge.sh — build and test the tree a pull request's CI actually builds.
#
# `make precommit` / `mise run verify` build the branch. The `pull_request` CI
# run builds refs/pull/<N>/merge — the branch merged into its base — because
# that is what actions/checkout checks out on that event. The two can
# disagree: a textually clean merge can still fail to compile (main grew a
# call site to a helper the branch removed), with neither parent defective. And
# GitHub stamps every check run with the branch head's SHA, not the merge
# commit it compiled, so checking out the SHA on a red check and building it
# comes back green. This script builds the tree CI built.
#
# Usage: scripts/verify-merge.sh [--remote <name>] <pr-number>
#
# Run it from the PR's branch, after pushing. It fetches refs/pull/<N>/merge,
# refuses a merge ref whose head parent is not the local HEAD (GitHub
# regenerates the ref a few seconds after each push, so an early fetch names
# the previous head), extracts that tree into a throwaway directory and runs
# `go build ./...` and `go test ./...` there. The working tree is not touched.
#
# Exit codes:
#   0   merge ref builds and tests green
#   1   merge ref fails to build or its tests fail
#   2   not computable: no merge ref (PR closed, conflicting, not yet
#       computed, fetch failed), or the ref is stale for the local HEAD.
#       Never a pass.
#   64  usage error
#
# The merge queue re-tests the rebased result before anything lands on main,
# so this is a diagnostic for reproducing a red PR locally, not a gate that
# protects main.
set -euo pipefail

usage() {
	echo "usage: $0 [--remote <name>] <pr-number>" >&2
	exit 64
}

remote=origin
pr=""
while [ $# -gt 0 ]; do
	case "$1" in
	--remote)
		[ $# -ge 2 ] || usage
		remote="$2"
		shift 2
		;;
	-h | --help) usage ;;
	*)
		[ -z "$pr" ] || usage
		pr="$1"
		shift
		;;
	esac
done
case "$pr" in
'' | *[!0-9]*) usage ;;
esac

not_computable() {
	echo "verify-merge: not computable: $*" >&2
	echo "verify-merge: this is not a pass — nothing was built." >&2
	exit 2
}

head="$(git rev-parse --verify HEAD)"
ref="refs/pull/$pr/merge"

if ! git fetch --no-tags --quiet "$remote" "$ref"; then
	not_computable "$remote has no $ref (PR closed or merged, conflicting with its base, or GitHub has not computed it yet)"
fi
merge="$(git rev-parse --verify 'FETCH_HEAD^{commit}')"

read -r -a parents <<<"$(git rev-list --parents -n 1 "$merge")"
if [ "${#parents[@]}" -ne 3 ]; then
	not_computable "$ref ($merge) is not a two-parent merge commit"
fi
base="${parents[1]}"
merged_head="${parents[2]}"

if [ "$merged_head" != "$head" ]; then
	not_computable "$ref is stale for this checkout: it merges head $merged_head, local HEAD is $head. Push HEAD and re-run once GitHub regenerates the ref (a few seconds), or check out $merged_head."
fi

echo "verify-merge: $ref = $merge (base $base, head $head)"

tree="$(mktemp -d "${TMPDIR:-/tmp}/verify-merge.XXXXXX")"
trap 'rm -rf "$tree"' EXIT
git archive "$merge" | tar -x -C "$tree"

cd "$tree"
export GOWORK=off
if ! go build ./...; then
	echo "verify-merge: FAIL: $ref does not build (the branch alone may; see the errors above)" >&2
	exit 1
fi
if ! go test -timeout "${TEST_TIMEOUT:-5m}" ./...; then
	echo "verify-merge: FAIL: $ref tests fail" >&2
	exit 1
fi
echo "verify-merge: merge ref builds and tests green: $ref = $merge"
