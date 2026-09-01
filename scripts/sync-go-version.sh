#!/bin/sh
# sync-go-version.sh — propagate the mise.toml [tools].go version into every
# module's go.mod (root, site/, site/scripts/kuredepsync/), every
# .github/workflows/*.yml(.yaml) GO_VERSION/go-version mirror, versions.yaml's
# go.current, the README.md shields.io Go badge, and DEVELOPMENT.md's
# "Go X.Y.Z (managed by mise)" prerequisite line. Ported
# verbatim from the Makefile's sync-go-version recipe (same sed patterns, same
# order) so Renovate's postUpgradeTasks can call it directly — postUpgradeTasks
# runs plain commands, not make targets, matching the convention already used
# for sync-tool-versions.sh in this repo.
#
# Run scripts/../Makefile's check-go-version afterwards (or 'make check-go-version'),
# and ./scripts/sync-versions.sh check (validate_gomod compares go.mod against
# versions.yaml's go.current AND README.md's badge, not against mise.toml
# directly — see the versions.yaml/README.md blocks below).
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MISE="mise.toml"
VERSIONS="versions.yaml"
README="README.md"
DEVELOPMENT="DEVELOPMENT.md"

if [ ! -f "$MISE" ]; then
	echo "Error: $MISE not found"
	exit 1
fi

GO_VER="$(grep '^go = ' "$MISE" | cut -d'"' -f2)"
if [ -z "$GO_VER" ]; then
	echo "Error: could not extract Go version from $MISE"
	exit 1
fi
echo "Syncing to Go version: $GO_VER"

# versions.yaml's go.current mirrors mise.toml the same way go.mod does --
# scripts/sync-versions.sh's validate_gomod() compares go.mod's directive
# against THIS field, not against mise.toml directly. A version bump that
# updated go.mod but not this field would trade one CI failure
# (make check-go-version) for another (sync-versions.sh check's
# validate_gomod), exactly as go-kure/kure#734 did. Written with yq -i, the
# same tool sync-eso-pin.sh-style scripts already use for versions.yaml writes.
if [ ! -f "$VERSIONS" ]; then
	echo "Error: $VERSIONS not found"
	exit 1
fi
yq -i ".go.current = \"$GO_VER\"" "$VERSIONS"

# A glob with no match expands to its own literal pattern string under
# POSIX sh (no nullglob) -- passing that straight to sed would try to open a
# file named literally ".github/workflows/*.yaml" and, under set -eu, abort
# the whole script right there, before the go.mod sed below ever runs. This
# repo currently has no .yaml workflow files at all, so the unguarded glob
# would abort on every single run. Guard explicitly so this script's own
# set -eu can't silently skip go.mod.
for f in .github/workflows/*.yml .github/workflows/*.yaml; do
	[ -e "$f" ] || continue
	sed -i -E "s/^([[:space:]]*)GO_VERSION: '[^']*'/\1GO_VERSION: '$GO_VER'/" "$f"
	sed -i "s/go-version: '[^']*'/go-version: '$GO_VER'/" "$f"
done
# This repo is three Go modules, not one (site/ and site/scripts/kuredepsync
# each have their own go.mod, currently on the same directive as root by
# coincidence, not by anything that keeps them that way) -- syncing only the
# root would leave the other two on the old pin the moment a bump actually
# happens, mixed-version module metadata with no check anywhere to catch it.
# Anchored on the directive's own text, not hardcoded to line 3: a
# line-number sed silently rewrites whatever happens to sit there if a
# go.mod's layout ever shifts (an inserted comment or blank line above `go`,
# e.g.), either missing the real directive or corrupting an unrelated line.
for gomod in go.mod site/go.mod site/scripts/kuredepsync/go.mod; do
	[ -f "$gomod" ] || { echo "Error: $gomod not found"; exit 1; }
	sed -i -E "s/^go [^[:space:]]+/go $GO_VER/" "$gomod"
	GOMOD_VER="$(grep -E '^go ' "$gomod" | head -1 | awk '{print $2}')"
	if [ "$GOMOD_VER" != "$GO_VER" ]; then
		echo "Error: $gomod's go directive reads '$GOMOD_VER' after sync, expected $GO_VER"
		exit 1
	fi
done

# validate_gomod() also hard-fails if README.md's shields.io Go badge doesn't
# match go.mod (scripts/sync-versions.sh) -- a check kure does not have.
# Confirmed exactly one such badge exists in this file.
if [ ! -f "$README" ]; then
	echo "Error: $README not found"
	exit 1
fi
sed -i -E "s#(img\.shields\.io/badge/go-)[0-9]+\.[0-9]+(\.[0-9]+)?(-blue)#\1$GO_VER\3#" "$README"

# Courtesy sync, not CI-gated: nothing currently checks this line (unlike the
# golangci-lint line directly below it in the same file, which
# sync-tool-versions.sh / check-tool-versions.sh already own -- see
# scripts/sync-tool-versions.sh:53). Left alone, it silently drifts exactly
# like every other pin this script exists to fix; this file is already in the
# gomod/mise packageRule's fileFilters for the golangci-lint line, so no
# renovate.json change is needed to cover this addition too.
if [ -f "$DEVELOPMENT" ]; then
	sed -i -E "s/^- Go [0-9]+\.[0-9]+(\.[0-9]+)? \(managed by mise\)\$/- Go $GO_VER (managed by mise)/" "$DEVELOPMENT"
fi

echo "Go version synced to $GO_VER"
