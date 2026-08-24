#!/bin/sh
# sync-tool-versions.sh — rewrite the golangci-lint version pinned by hand in
# Makefile, .github/workflows/ci.yml, docs/github-workflows.md and
# DEVELOPMENT.md from the mise.toml [tools] source of truth.
#
# The inverse of scripts/check-tool-versions.sh — keep every copy equal, or
# the checker will fail on a file this cannot fix, or Renovate's
# postUpgradeTasks will sync a file the checker does not police.
#
# Run scripts/check-tool-versions.sh afterwards (or 'mise run check-tool-versions').
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MISE="mise.toml"
MISE_VAL="$(grep -E '^golangci-lint = "' "$MISE" | head -1 | cut -d'"' -f2)"
if [ -z "$MISE_VAL" ]; then
	echo "Error: golangci-lint not found in $MISE [tools]"
	exit 1
fi

sed -i -E "s/^GOLANGCI_LINT_VERSION := v[0-9.]+/GOLANGCI_LINT_VERSION := v$MISE_VAL/" Makefile
sed -i -E "s/^([[:space:]]*GOLANGCI_LINT_VERSION: )'v[0-9.]+'/\\1'v$MISE_VAL'/" .github/workflows/ci.yml
# shellcheck disable=SC2016 # literal markdown backticks, not unexpanded vars
sed -i -E "s/^- Golangci-lint Version: \`v[0-9.]+\`\$/- Golangci-lint Version: \`v$MISE_VAL\`/" docs/github-workflows.md
sed -i -E "s/^- golangci-lint [0-9.]+ \(managed by mise\)\$/- golangci-lint $MISE_VAL (managed by mise)/" DEVELOPMENT.md

echo "Synced golangci-lint pins to $MISE_VAL (Makefile, ci.yml, docs/github-workflows.md, DEVELOPMENT.md)"
