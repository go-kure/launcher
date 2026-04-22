#!/usr/bin/env bash
# check-mounts.sh — Verify every file referenced by inject-frontmatter.sh exists.
#
# Exits non-zero if any mounted file is missing.
#
# Usage: bash scripts/check-mounts.sh [LAUNCHER_ROOT]

set -euo pipefail

SITE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LAUNCHER_ROOT="${1:-$(cd "$SITE_DIR/.." && pwd)}"

MOUNTED_FILES=(
  "README.md"
  "DEVELOPMENT.md"
  "CHANGELOG.md"
  "docs/design.md"
)

missing=0

for file in "${MOUNTED_FILES[@]}"; do
  if [[ ! -f "$LAUNCHER_ROOT/$file" ]]; then
    echo "ERROR: mounted file not found: $file" >&2
    ((missing++))
  fi
done

if [[ $missing -gt 0 ]]; then
  echo "FATAL: $missing mounted file(s) missing. Fix mounts or source files." >&2
  exit 1
fi

echo "All $((${#MOUNTED_FILES[@]})) mounted files verified."
