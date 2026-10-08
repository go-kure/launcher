#!/usr/bin/env bash
# vendor-alertmanager-features.sh — re-fetch the copies of Alertmanager's
# featurecontrol/featurecontrol.go that the alertmanager kind's table of
# feature flags is held to (go-kure/launcher#950), and rewrite their SOURCE
# file.
#
# The copies live under
# pkg/oam/builtin/components/testdata/upstream/alertmanager/, one directory per
# minor version, and are read by TestAlertmanagerFeatureFlags_MatchVendoredSource,
# which parses them with go/ast and needs no network. Each minor is taken at
# its latest release (no prerelease). The files are copied unmodified,
# Apache-2.0 headers included, with each release's NOTICE, which the licence
# asks a redistributor to pass on, and the project's LICENSE, the same at every
# release; SOURCE names each tag, its tag object and the git blob id of each
# file, and the test checks every blob id against the file.
#
# Run by hand when Alertmanager releases a minor version or a patch of one;
# CI never runs it. Pass the first and the last minor, then run the components
# tests: a name that was added or dropped fails there, naming the version.
#
# Usage: ./scripts/vendor-alertmanager-features.sh 0.27 0.34

set -euo pipefail

FIRST="${1:-}"
LAST="${2:-}"
if [[ ! "$FIRST" =~ ^0\.[0-9]+$ || ! "$LAST" =~ ^0\.[0-9]+$ || "${FIRST#0.}" -gt "${LAST#0.}" ]]; then
  echo "usage: $0 0.FIRST 0.LAST (the minors of Alertmanager to vendor, in order)" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$REPO_ROOT/pkg/oam/builtin/components/testdata/upstream/alertmanager"
REPO="prometheus/alertmanager"
URL="https://github.com/$REPO.git"

# Every release tag, vX.Y.Z only, with its tag object.
declare -A OBJECT
while read -r oid ref; do
  tag="${ref#refs/tags/}"
  [[ "$tag" =~ ^v0\.[0-9]+\.[0-9]+$ ]] && OBJECT[$tag]="$oid"
done < <(git ls-remote --tags --refs "$URL")

# The latest release of each minor, in order.
TAGS=()
for ((minor = ${FIRST#0.}; minor <= ${LAST#0.}; minor++)); do
  latest=""
  for tag in "${!OBJECT[@]}"; do
    if [[ "$tag" == "v0.$minor."* ]] && { [[ -z "$latest" ]] || [[ "$(printf '%s\n%s\n' "$latest" "$tag" | sort -V | tail -1)" == "$tag" ]]; }; then
      latest="$tag"
    fi
  done
  if [[ -z "$latest" ]]; then
    echo "ERROR: $REPO has no release of 0.$minor" >&2
    exit 1
  fi
  TAGS+=("$latest")
done

UPSTREAM=""
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE" ${UPSTREAM:+"$UPSTREAM"}' EXIT
UPSTREAM="$(mktemp -d)"

# A bare, blobless clone: each file is read from its tag's tree, not a
# checkout, so that no checkout conversion (core.autocrlf, a filter) changes
# its bytes.
git clone --quiet --bare --filter=blob:none "$URL" "$UPSTREAM"

declare -A BLOB
put() { # tag path-in-repo path-in-stage
  mkdir -p "$STAGE/$(dirname "$3")"
  git -C "$UPSTREAM" cat-file blob "$1:$2" > "$STAGE/$3"
  # Recorded before SOURCE is written (an id taken inside an echo would leave
  # a failure unseen by set -e), and checked against the bytes written.
  BLOB[$3]="$(git -C "$UPSTREAM" rev-parse "$1:$2")"
  written="$(git hash-object --no-filters "$STAGE/$3")"
  if [[ "$written" != "${BLOB[$3]}" ]]; then
    echo "ERROR: $3 was written as blob $written, not the tag's ${BLOB[$3]}" >&2
    exit 1
  fi
}

last="${TAGS[${#TAGS[@]}-1]}"
put "$last" LICENSE LICENSE
for tag in "${TAGS[@]}"; do
  if [[ "$(git -C "$UPSTREAM" rev-parse "$tag:LICENSE")" != "${BLOB[LICENSE]}" ]]; then
    echo "ERROR: $tag has another LICENSE than $last: vendor each" >&2
    exit 1
  fi
  if [[ "$(git -C "$UPSTREAM" rev-parse "$tag^{}")" != "$(git -C "$UPSTREAM" rev-parse "${OBJECT[$tag]}^{}")" ]]; then
    echo "ERROR: $tag in the clone is not the tag ls-remote listed" >&2
    exit 1
  fi
  put "$tag" NOTICE "$tag/NOTICE"
  put "$tag" featurecontrol/featurecontrol.go "$tag/featurecontrol/featurecontrol.go"
done

{
  echo "# Copies of prometheus/alertmanager's featurecontrol.go, one per minor at its latest release,"
  echo "# copied unmodified by scripts/vendor-alertmanager-features.sh."
  echo "# Licensed under the Apache License, Version 2.0 (each file carries its header);"
  echo "# LICENSE is the project's copy of the licence, the same at each tag, and each NOTICE its notice file at that tag."
  echo "# The components tests check each file against its git blob id below."
  echo "licence LICENSE ${BLOB[LICENSE]}"
  for tag in "${TAGS[@]}"; do
    echo "tag $tag ${OBJECT[$tag]}"
    echo "notice $tag/NOTICE ${BLOB[$tag/NOTICE]}"
    echo "file $tag/featurecontrol/featurecontrol.go ${BLOB[$tag/featurecontrol/featurecontrol.go]}"
  done
} > "$STAGE/SOURCE"

rm -rf "$DEST"
mkdir -p "$DEST"
cp -r "$STAGE/." "$DEST/"
echo "vendored ${TAGS[*]} from $REPO into ${DEST#"$REPO_ROOT"/}"
