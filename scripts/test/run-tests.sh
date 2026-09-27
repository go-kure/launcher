#!/bin/bash
# scripts/test/run-tests.sh - runs every scripts/test/cases/*.sh in isolation
# and reports pass/fail. Today every case is a scripts/check-pin-impact.sh
# case (pin-impact-lib.sh): hermetic and network-independent, each builds a
# throwaway git repository and serves go-kure/.github content from a `curl`
# stub, so no case reaches GitHub.
#
# The cases, pin-impact-lib.sh and check-pin-impact.sh itself are kept in
# step with go-kure/kure's copies (same case file names), so a gap closed in
# one repository's checker can be ported to the other's case by case.
set -uo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

pass=0
fail=0
failed_names=()

for case_file in "$TEST_DIR"/cases/*.sh; do
    name="$(basename "$case_file" .sh)"
    # Each case runs as its own `bash` process: pin-impact-lib.sh's PI_ROOT/
    # PATH state and its EXIT trap must not leak between cases, and a case
    # that dies mid-run (an unmet expectation calling `exit 1`) must not take
    # the runner down with it. pin-impact-lib.sh clears the caller's
    # PIN_IMPACT_ACK, GITHUB_TOKEN and git repository-selection variables
    # itself, so an ambient value cannot change what a case exercises.
    if out=$(bash "$case_file" 2>&1); then
        pass=$((pass + 1))
        printf 'PASS %s\n' "$name"
    else
        fail=$((fail + 1))
        failed_names+=("$name")
        printf 'FAIL %s\n' "$name"
        printf '%s\n' "$out" | sed 's/^/    /'
    fi
done

echo ""
echo "$pass passed, $fail failed"

if [[ $fail -gt 0 ]]; then
    echo "Failed: ${failed_names[*]}"
    exit 1
fi
exit 0
