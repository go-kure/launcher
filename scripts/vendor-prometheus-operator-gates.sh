#!/usr/bin/env bash
# vendor-prometheus-operator-gates.sh — re-fetch the excerpt of the Prometheus
# operator's code that the alertmanager kind's version gates are held to
# (go-kure/launcher#935), and rewrite its SOURCE file.
#
# The excerpt lives under
# pkg/oam/builtin/components/testdata/upstream/prometheus-operator/ and is read
# by TestAlertmanagerVersionGates_MatchVendoredSource, which parses it with
# go/ast and needs no network. The files are copied unmodified, Apache-2.0
# headers included, with the operator's LICENSE, the licence's full text, and
# its NOTICE, which the licence asks a redistributor to pass on; SOURCE names
# the tag, the tag object and the git blob id of each file, of LICENSE and of
# NOTICE, and the test checks every blob id against the file.
#
# Run by hand when the linked
# github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring moves;
# CI never runs it. Pass the operator tag that go.mod links, then run the
# components tests: a gate that was added, moved or dropped fails there,
# naming the function and the version.
#
# Usage: ./scripts/vendor-prometheus-operator-gates.sh vX.Y.Z

set -euo pipefail

TAG="${1:-}"
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vX.Y.Z (the prometheus-operator tag go.mod links)" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$REPO_ROOT/pkg/oam/builtin/components/testdata/upstream/prometheus-operator"
REPO="prometheus-operator/prometheus-operator"

LINKED="$(awk '$1 == "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring" { print $2 }' "$REPO_ROOT/go.mod")"
if [[ "$LINKED" != "$TAG" ]]; then
  echo "ERROR: go.mod links the monitoring API at ${LINKED:-no version}, not $TAG: vendor the tag go.mod links" >&2
  exit 1
fi

# The files the test reads, by their path in prometheus-operator/prometheus-operator.
FILES=(
  pkg/alertmanager/statefulset.go
  pkg/alertmanager/amcfg.go
  pkg/alertmanager/operator.go
  pkg/operator/defaults.go
)

TAG_OBJECT="$(git ls-remote "https://github.com/$REPO.git" "refs/tags/$TAG" | cut -f1)"
if [[ ! "$TAG_OBJECT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "ERROR: $REPO has no single tag $TAG (ls-remote gave: ${TAG_OBJECT:-nothing})" >&2
  exit 1
fi

UPSTREAM=""
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE" ${UPSTREAM:+"$UPSTREAM"}' EXIT
UPSTREAM="$(mktemp -d)"

# The test reads only the pkg/alertmanager files above, so refuse a tag where
# another file of the package (its tests aside) may compare the Alertmanager
# version: its gates would go unclassified. A file counts where it imports
# semver, reads a version's Major, Minor or Patch, or calls a comparison
# method; the test's own detection, read as text, and broader.
git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$TAG" --filter=blob:none --sparse "https://github.com/$REPO.git" "$UPSTREAM"
git -C "$UPSTREAM" sparse-checkout set pkg/alertmanager
COMPARES='blang/semver|\.(Major|Minor|Patch)\b|\.(GTE|GT|LTE|LT|EQ|NE|Compare|Equals)\('
# grep exits 1 for no match and 2 for an error; only 0 is a usable scan, as
# the vendored files themselves match.
SCAN_RC=0
SCANNED="$(grep -rlE --include='*.go' --exclude='*_test.go' -- "$COMPARES" "$UPSTREAM/pkg/alertmanager")" || SCAN_RC=$?
if (( SCAN_RC != 0 )); then
  echo "ERROR: scanning pkg/alertmanager at $TAG for version comparisons failed (grep exit $SCAN_RC)" >&2
  exit 1
fi
UNREAD=()
while IFS= read -r f; do
  f="${f#"$UPSTREAM"/}"
  [[ " ${FILES[*]} " == *" $f "* ]] || UNREAD+=("$f")
done <<< "$SCANNED"
if (( ${#UNREAD[@]} > 0 )); then
  echo "ERROR: $TAG compares the Alertmanager version outside the vendored files: ${UNREAD[*]}; vendor them, and classify their gates in the test" >&2
  exit 1
fi

for f in "${FILES[@]}" LICENSE NOTICE; do
  mkdir -p "$STAGE/$(dirname "$f")"
  curl -fsSL "https://raw.githubusercontent.com/$REPO/$TAG/$f" -o "$STAGE/$f"
done

# Hash every file before writing SOURCE: a hash taken inside an echo would
# leave a failure unseen by set -e.
declare -A BLOB
for f in "${FILES[@]}" LICENSE NOTICE; do
  BLOB[$f]="$(git hash-object "$STAGE/$f")"
done

{
  echo "# Excerpt of prometheus-operator/prometheus-operator, copied unmodified by scripts/vendor-prometheus-operator-gates.sh."
  echo "# Licensed under the Apache License, Version 2.0 (each file carries its header);"
  echo "# LICENSE is the project's copy of the licence and NOTICE its notice file, at the same tag."
  echo "# The components tests check each file against its git blob id below."
  echo "tag $TAG"
  echo "tag-object $TAG_OBJECT"
  echo "licence LICENSE ${BLOB[LICENSE]}"
  echo "notice NOTICE ${BLOB[NOTICE]}"
  for f in "${FILES[@]}"; do
    echo "file $f ${BLOB[$f]}"
  done
} > "$STAGE/SOURCE"

rm -rf "$DEST"
mkdir -p "$DEST"
cp -r "$STAGE/." "$DEST/"
echo "vendored ${#FILES[@]} files from $REPO $TAG into ${DEST#"$REPO_ROOT"/}"
