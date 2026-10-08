#!/usr/bin/env bash
# vendor-k8s-defaulters.sh — re-fetch the excerpt of the Kubernetes API server's
# defaulting code that the components package's defaulted-zero tables are held
# to (go-kure/launcher#790), and rewrite its SOURCE file.
#
# The excerpt lives under pkg/oam/builtin/components/testdata/upstream/kubernetes/
# and is read by TestKubernetesDefaulters_MatchVendoredSource, which parses it
# with go/ast and needs no network. The files are copied unmodified, Apache-2.0
# headers included, with kubernetes/kubernetes's LICENSE, the licence's full
# text; SOURCE names the tag, the tag object and the git blob id of each file
# and of LICENSE, and the test checks every blob id against the file.
#
# Run by hand when the linked k8s.io/api moves; CI never runs it. Pass the
# Kubernetes tag that matches the linked k8s.io/api (v0.X.Y pairs with
# v1.X.Y), then run the components tests: a defaulter that changed fails
# there, naming the field.
#
# Usage: ./scripts/vendor-k8s-defaulters.sh vX.Y.Z

set -euo pipefail

TAG="${1:-}"
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vX.Y.Z (the Kubernetes tag matching the linked k8s.io/api)" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$REPO_ROOT/pkg/oam/builtin/components/testdata/upstream/kubernetes"

# The files the test reads, by their path in kubernetes/kubernetes.
FILES=(
  pkg/apis/core/v1/defaults.go
  pkg/apis/core/v1/zz_generated.defaults.go
  pkg/apis/apps/v1/defaults.go
  pkg/apis/apps/v1/zz_generated.defaults.go
  pkg/apis/autoscaling/annotations.go
  pkg/apis/autoscaling/v2/defaults.go
  pkg/apis/networking/v1/defaults.go
  pkg/util/parsers/parsers.go
)

TAG_OBJECT="$(git ls-remote https://github.com/kubernetes/kubernetes.git "refs/tags/$TAG" | cut -f1)"
if [[ ! "$TAG_OBJECT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "ERROR: kubernetes/kubernetes has no single tag $TAG (ls-remote gave: ${TAG_OBJECT:-nothing})" >&2
  exit 1
fi

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

for f in "${FILES[@]}" LICENSE; do
  mkdir -p "$STAGE/$(dirname "$f")"
  curl -fsSL "https://raw.githubusercontent.com/kubernetes/kubernetes/$TAG/$f" -o "$STAGE/$f"
done

# Hash every file before writing SOURCE: a hash taken inside an echo would
# leave a failure unseen by set -e.
declare -A BLOB
for f in "${FILES[@]}" LICENSE; do
  BLOB[$f]="$(git hash-object "$STAGE/$f")"
done

{
  echo "# Excerpt of kubernetes/kubernetes, copied unmodified by scripts/vendor-k8s-defaulters.sh."
  echo "# Licensed under the Apache License, Version 2.0 (each file carries its header);"
  echo "# LICENSE is kubernetes/kubernetes's copy of the licence, at the same tag."
  echo "# The components tests check each file against its git blob id below."
  echo "tag $TAG"
  echo "tag-object $TAG_OBJECT"
  echo "licence LICENSE ${BLOB[LICENSE]}"
  for f in "${FILES[@]}"; do
    echo "file $f ${BLOB[$f]}"
  done
} > "$STAGE/SOURCE"

rm -rf "$DEST"
mkdir -p "$DEST"
cp -r "$STAGE/." "$DEST/"
echo "vendored ${#FILES[@]} files from kubernetes/kubernetes $TAG into ${DEST#"$REPO_ROOT"/}"
