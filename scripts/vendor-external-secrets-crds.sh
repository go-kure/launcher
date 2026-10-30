#!/usr/bin/env bash
# vendor-external-secrets-crds.sh — re-fetch the CustomResourceDefinitions of
# the External Secrets Operator's kinds that launcher ships as components
# (secretstore, clustersecretstore, externalsecret, clusterexternalsecret), and
# rewrite their SOURCE file (go-kure/launcher#1108).
#
# The API module go.mod links, github.com/external-secrets/external-secrets/apis,
# ships no CRD, so the CRDs are copied unmodified from the operator's
# repository, out of config/crds/bases/, into
# pkg/oam/builtin/components/testdata/upstream/external-secrets/ under the same
# path, with the repository's LICENSE (Apache-2.0). SOURCE names the tag, the
# tag object and the git blob id of LICENSE and of each CRD; the components
# tests check every blob id against the file, hold the tag object to go.mod,
# and create each generated kind's object against its CRD to show the API
# server's answer for a required field left out.
#
# go.mod links the API module at a pseudo-version, whose last field is the
# first 12 hex digits of a commit, not at a tag of its own. The tag passed is
# the operator's release tag of that commit, and must be a lightweight tag: its
# ref names the commit itself, with no tag object (ls-remote prints no "^{}"
# line). SOURCE's tag-object line names the object the tag ref names, here that
# commit, and the tests require its first 12 hex digits to be the
# pseudo-version's. The script checks with ls-remote that the tag names that
# commit, and refuses an annotated tag, a tag of another commit, and a go.mod
# link that is no pseudo-version.
#
# Run by hand when go.mod moves the API module; CI never runs it.
#
# Usage: ./scripts/vendor-external-secrets-crds.sh vX.Y.Z

set -euo pipefail

TAG="${1:-}"
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vX.Y.Z (the external-secrets release tag of the commit go.mod links)" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$REPO_ROOT/pkg/oam/builtin/components/testdata/upstream/external-secrets"
REPO="external-secrets/external-secrets"
MODULE="github.com/external-secrets/external-secrets/apis"
GUARD="$REPO_ROOT/site/scripts/check-forbidden-terms.sh"

# The CRDs of the kinds launcher ships, by path in the repository.
CRDS=(
  config/crds/bases/external-secrets.io_clusterexternalsecrets.yaml
  config/crds/bases/external-secrets.io_clustersecretstores.yaml
  config/crds/bases/external-secrets.io_externalsecrets.yaml
  config/crds/bases/external-secrets.io_secretstores.yaml
)

LINKED="$(awk -v m="$MODULE" '$1 == m { print $2 }' "$REPO_ROOT/go.mod")"
if [[ ! "$LINKED" =~ -[0-9]{14}-([0-9a-f]{12})$ ]]; then
  echo "ERROR: go.mod links $MODULE at ${LINKED:-no version}, not a pseudo-version" >&2
  exit 1
fi
REV="${BASH_REMATCH[1]}"

REFS="$(git ls-remote "https://github.com/$REPO.git" "refs/tags/$TAG" "refs/tags/$TAG^{}")"
TAG_OBJECT="$(awk -v r="refs/tags/$TAG" '$2 == r { print $1 }' <<<"$REFS")"
if [[ ! "$TAG_OBJECT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "ERROR: $REPO has no single tag $TAG (ls-remote gave: ${REFS:-nothing})" >&2
  exit 1
fi
if grep -qF "refs/tags/$TAG^{}" <<<"$REFS"; then
  echo "ERROR: $REPO tag $TAG is annotated; SOURCE's tag-object rule takes a lightweight tag, whose ref names the commit" >&2
  exit 1
fi
if [[ "${TAG_OBJECT:0:12}" != "$REV" ]]; then
  echo "ERROR: $REPO tag $TAG names $TAG_OBJECT, and go.mod links $MODULE at $LINKED, commit $REV" >&2
  exit 1
fi

# The forbidden-terms guard's terms and matcher, read from the guard so that
# this script names no term: the CRDs are copied unmodified, so one with a line
# the guard would refuse, a term inside an identifier included, is refused here
# rather than given a pragma.
# shellcheck source=scripts/forbidden-terms-lib.sh
source "$SCRIPT_DIR/forbidden-terms-lib.sh"
ft_load "$GUARD"

WORK=""
trap 'rm -rf ${WORK:+"$WORK"}' EXIT
WORK="$(mktemp -d)"
CLONE="$WORK/clone"
STAGE="$WORK/stage"

git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$TAG" --filter=blob:none --no-checkout "https://github.com/$REPO.git" "$CLONE"
HEAD="$(git -C "$CLONE" rev-parse HEAD)"
if [[ "$HEAD" != "$TAG_OBJECT" ]]; then
  echo "ERROR: the clone of $TAG is at $HEAD, and ls-remote names $TAG_OBJECT" >&2
  exit 1
fi

# Each file is read from the tag's tree, not a checkout, so that no checkout
# conversion (core.autocrlf, a filter) changes its bytes. The blob id is
# recorded before SOURCE is written (an id taken inside an echo would leave a
# failure unseen by set -e), and the bytes written must hash, unconverted, to it.
FILES=(LICENSE "${CRDS[@]}")
declare -A BLOB=()
for f in "${FILES[@]}"; do
  mkdir -p "$STAGE/$(dirname "$f")"
  git -C "$CLONE" cat-file blob "HEAD:$f" >"$STAGE/$f"
  BLOB[$f]="$(git -C "$CLONE" rev-parse "HEAD:$f")"
  written="$(git hash-object --no-filters "$STAGE/$f")"
  if [[ "$written" != "${BLOB[$f]}" ]]; then
    echo "ERROR: $REPO $f was written as blob $written, not the tag's ${BLOB[$f]}" >&2
    exit 1
  fi
  hits="$(ft_hits "$STAGE/$f")"
  if [[ -n "$hits" ]]; then
    echo "ERROR: $REPO $f names a term $GUARD lists (line ${hits%%$'\t'*}); it is vendored unmodified, so it cannot carry a pragma" >&2
    exit 1
  fi
done

{
  echo "# CRDs of external-secrets/external-secrets, copied unmodified by scripts/vendor-external-secrets-crds.sh."
  echo "# Licensed under the Apache License, Version 2.0; LICENSE is the project's copy of the licence, at the same tag."
  echo "# Each pin below is the git blob id of the committed file, which is upstream's."
  echo "# go.mod links the API module at a pseudo-version of the tag's commit, and the tag is lightweight: its"
  echo "# ref names that commit, with no tag object, so tag-object is the commit, and the components tests"
  echo "# require its first 12 hex digits to be the pseudo-version's."
  echo "tag $TAG"
  echo "tag-object $TAG_OBJECT"
  echo "licence LICENSE ${BLOB[LICENSE]}"
  for f in "${CRDS[@]}"; do
    echo "file $f ${BLOB[$f]}"
  done
} >"$STAGE/SOURCE"

rm -rf "$DEST"
mkdir -p "$DEST"
cp -r "$STAGE/." "$DEST/"
echo "vendored ${#CRDS[@]} CRDs from $REPO $TAG ($TAG_OBJECT) into ${DEST#"$REPO_ROOT"/}"
