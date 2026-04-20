#!/usr/bin/env bash
# inject-frontmatter.sh — Prepend Hugo front matter to launcher markdown files.
#
# Usage: bash scripts/inject-frontmatter.sh [LAUNCHER_ROOT]
#   LAUNCHER_ROOT defaults to .. (parent of site/)

set -euo pipefail

SITE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LAUNCHER_ROOT="${1:-$(cd "$SITE_DIR/.." && pwd)}"
GEN_DIR="$SITE_DIR/.generated"

rm -rf "$GEN_DIR"
mkdir -p "$GEN_DIR"

# inject_fm SOURCE_FILE TARGET_PATH TITLE WEIGHT
#   Copies SOURCE_FILE into .generated/TARGET_PATH with Hugo front matter prepended.
inject_fm() {
  local src="$1" target="$2" title="$3" weight="$4"
  local dest="$GEN_DIR/$target"

  if [[ ! -f "$src" ]]; then
    echo "WARNING: source file not found: $src" >&2
    return 0
  fi

  mkdir -p "$(dirname "$dest")"
  {
    echo "---"
    echo "title: \"$title\""
    echo "weight: $weight"
    echo "---"
    echo ""
    cat "$src"
  } > "$dest"
}

# ─── Mapping: source_path → target_path | title | weight ───

# Getting started
inject_fm "$LAUNCHER_ROOT/README.md"          "getting-started/introduction.md"  "Introduction"     10

# Concepts
inject_fm "$LAUNCHER_ROOT/docs/design.md"     "concepts/design.md"               "Design"           10

# Contributing
inject_fm "$LAUNCHER_ROOT/DEVELOPMENT.md"     "contributing/guide.md"            "Development Guide" 10

# Changelog
inject_fm "$LAUNCHER_ROOT/CHANGELOG.md"       "changelog/releases.md"            "Releases"         10

echo "Front matter injection complete. Output in $GEN_DIR"
