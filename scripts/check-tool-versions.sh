#!/bin/sh
# check-tool-versions.sh — enforce golangci-lint version parity between the
# mise.toml [tools] source of truth and the pins duplicated by hand in
# Makefile, .github/workflows/ci.yml, docs/github-workflows.md and
# DEVELOPMENT.md.
#
# mise.toml is the only copy Renovate's `mise` datasource actually bumps.
# Every other copy is a plain string a human — or Renovate's own
# postUpgradeTasks sync command — must keep in step by hand; this script is
# what enforces that "by hand" doesn't mean "silently drifts", the way it
# already did once (CI pinned 2.10.1 while mise.toml said 2.13.1, so CI and
# developers ran different linters).
#
# CI images do not have mise installed, so this is a plain POSIX shell
# script that CI can run directly — it must NOT depend on mise.
#
# Exit non-zero on any mismatch. Run scripts/sync-tool-versions.sh to fix.
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MISE="mise.toml"
MISE_VAL="$(grep -E '^golangci-lint = "' "$MISE" | head -1 | cut -d'"' -f2)"
if [ -z "$MISE_VAL" ]; then
	echo "✗ golangci-lint: not found in $MISE [tools]"
	exit 1
fi

ERRORS=0

# check LABEL FILE ACTUAL — ACTUAL is the caller-extracted value, already
# stripped of quoting/prefix noise, compared against mise.toml's bare version.
check() {
	label="$1"; file="$2"; actual="$3"
	if [ -z "$actual" ]; then
		echo "✗ $label: not found in $file"
		ERRORS=$((ERRORS + 1))
		return
	fi
	if [ "$actual" != "$MISE_VAL" ]; then
		echo "✗ $label: $file has $actual (expected $MISE_VAL from $MISE)"
		ERRORS=$((ERRORS + 1))
		return
	fi
	echo "✓ $label ($actual)"
}

# Makefile: GOLANGCI_LINT_VERSION := v2.13.1
# Exactly one match required — head -1 of a duplicate assignment would let a
# stray second line win silently while this checker reports the first.
mk_count="$(grep -cE '^GOLANGCI_LINT_VERSION := v' Makefile || true)"
if [ "$mk_count" -ne 1 ]; then
	echo "✗ Makefile: expected exactly 1 GOLANGCI_LINT_VERSION assignment, found $mk_count"
	ERRORS=$((ERRORS + 1))
else
	mk_val="$(grep -E '^GOLANGCI_LINT_VERSION := v' Makefile | sed -E 's/^GOLANGCI_LINT_VERSION := v//')"
	check "Makefile" "Makefile" "$mk_val"
fi

# .github/workflows/ci.yml: GOLANGCI_LINT_VERSION: 'v2.13.1'
ci_count="$(grep -cE "^[[:space:]]*GOLANGCI_LINT_VERSION: 'v" .github/workflows/ci.yml || true)"
if [ "$ci_count" -ne 1 ]; then
	echo "✗ ci.yml: expected exactly 1 GOLANGCI_LINT_VERSION assignment, found $ci_count"
	ERRORS=$((ERRORS + 1))
else
	ci_val="$(grep -E "^[[:space:]]*GOLANGCI_LINT_VERSION: 'v" .github/workflows/ci.yml | sed -E "s/.*: 'v([^']*)'.*/\\1/")"
	check "ci.yml" ".github/workflows/ci.yml" "$ci_val"
fi

# docs/github-workflows.md: - Golangci-lint Version: `v2.13.1`
# Exactly one match required — a renamed/deleted line must fail, not silently
# pass. (kure's own check-go-version treats zero matches as a pass; this
# script deliberately does not copy that.)
# shellcheck disable=SC2016 # the backticks below are literal markdown, not
# unexpanded variables — single-quoting is correct (double quotes would let
# them trigger real command substitution).
doc_count="$(grep -cE '^- Golangci-lint Version: `v[0-9.]+`$' docs/github-workflows.md || true)"
if [ "$doc_count" -ne 1 ]; then
	echo "✗ docs/github-workflows.md: expected exactly 1 'Golangci-lint Version' line, found $doc_count"
	ERRORS=$((ERRORS + 1))
else
	# shellcheck disable=SC2016 # same false positive as above
	doc_val="$(grep -E '^- Golangci-lint Version: `v[0-9.]+`$' docs/github-workflows.md | sed -E 's/.*`v([0-9.]+)`.*/\1/')"
	check "docs/github-workflows.md" "docs/github-workflows.md" "$doc_val"
fi

# DEVELOPMENT.md: - golangci-lint 2.13.1 (managed by mise)  (no "v" prefix)
dev_count="$(grep -cE '^- golangci-lint [0-9.]+ \(managed by mise\)$' DEVELOPMENT.md || true)"
if [ "$dev_count" -ne 1 ]; then
	echo "✗ DEVELOPMENT.md: expected exactly 1 golangci-lint version line, found $dev_count"
	ERRORS=$((ERRORS + 1))
else
	dev_val="$(grep -E '^- golangci-lint [0-9.]+ \(managed by mise\)$' DEVELOPMENT.md | sed -E 's/^- golangci-lint ([0-9.]+) .*/\1/')"
	check "DEVELOPMENT.md" "DEVELOPMENT.md" "$dev_val"
fi

if [ "$ERRORS" -gt 0 ]; then
	echo "Found $ERRORS mismatch(es). Run 'scripts/sync-tool-versions.sh' (or 'mise run sync-tool-versions') to fix."
	exit 1
fi
echo "All pinned tool versions consistent."
