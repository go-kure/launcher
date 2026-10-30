#!/usr/bin/env bash
# vendor-flux-crds.sh — re-fetch the CustomResourceDefinitions of the Flux
# controllers' kinds that launcher ships as components, and rewrite each
# repository's SOURCE file (go-kure/launcher#883).
#
# The API modules of the Flux controllers that go.mod links ship no CRD, so
# the CRDs are copied from each controller's repository at the tag
# of the linked API module (api/vX.Y.Z), out of config/crd/bases/, one
# directory per repository under
# pkg/oam/builtin/components/testdata/upstream/, with the repository's
# LICENSE (Apache-2.0). SOURCE names the tag, the tag object and the git blob
# id of LICENSE and of each CRD as committed; the components tests check every
# blob id against the file, hold the tag to go.mod, and create each generated
# kind's object against its CRD to show the API server's answer for a required
# field left out. The one change to the bytes is an allow-term pragma comment
# line above a line that names a term the repository's forbidden-terms guard
# lists (a receiver type); for a file that carries one, an "# upstream" line in
# SOURCE names upstream's blob id, which the tests check the file against with
# the pragma lines stripped.
#
# Run by hand when go.mod moves one of the modules below; CI never runs it.
# It reads each module's version from go.mod and takes no argument.
#
# Usage: ./scripts/vendor-flux-crds.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
UPSTREAM_DIR="$REPO_ROOT/pkg/oam/builtin/components/testdata/upstream"

# Each repository: its name under github.com/fluxcd, the API module go.mod
# links, and the CRDs of the kinds launcher ships, by file name under
# config/crd/bases/.
REPOS=(
  "helm-controller|github.com/fluxcd/helm-controller/api|helm.toolkit.fluxcd.io_helmreleases.yaml"
  "image-automation-controller|github.com/fluxcd/image-automation-controller/api|image.toolkit.fluxcd.io_imageupdateautomations.yaml"
  "image-reflector-controller|github.com/fluxcd/image-reflector-controller/api|image.toolkit.fluxcd.io_imagepolicies.yaml image.toolkit.fluxcd.io_imagerepositories.yaml"
  "kustomize-controller|github.com/fluxcd/kustomize-controller/api|kustomize.toolkit.fluxcd.io_kustomizations.yaml"
  "notification-controller|github.com/fluxcd/notification-controller/api|notification.toolkit.fluxcd.io_alerts.yaml notification.toolkit.fluxcd.io_providers.yaml notification.toolkit.fluxcd.io_receivers.yaml"
  "source-controller|github.com/fluxcd/source-controller/api|source.toolkit.fluxcd.io_buckets.yaml source.toolkit.fluxcd.io_gitrepositories.yaml source.toolkit.fluxcd.io_helmcharts.yaml source.toolkit.fluxcd.io_helmrepositories.yaml source.toolkit.fluxcd.io_ocirepositories.yaml"
  "source-watcher|github.com/fluxcd/source-watcher/api/v2|source.extensions.fluxcd.io_artifactgenerators.yaml"
)

GUARD="$REPO_ROOT/site/scripts/check-forbidden-terms.sh"

# The terms the forbidden-terms guard lists, read from the guard so that this
# script names none, and the comment line a pragma for them is written as.
TERMS="$(sed -n "s/^TERMS_RE='\([a-z|]*\)'$/\1/p" "$GUARD")"
if [[ -z "$TERMS" ]]; then
  echo "ERROR: no TERMS_RE line in $GUARD" >&2
  exit 1
fi
PRAGMA_RE='^ *# allow-term:[a-z]+( allow-term:[a-z]+)*$'

WORK=""
trap 'rm -rf ${WORK:+"$WORK"}' EXIT
WORK="$(mktemp -d)"

for entry in "${REPOS[@]}"; do
  IFS='|' read -r name module crds <<<"$entry"
  repo="fluxcd/$name"

  version="$(awk -v m="$module" '$1 == m { print $2 }' "$REPO_ROOT/go.mod")"
  if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "ERROR: go.mod links $module at ${version:-no version}, not a release" >&2
    exit 1
  fi
  # The API module's own tag: its tree holds the CRDs generated from the
  # types go.mod links.
  tag="api/$version"

  tag_object="$(git ls-remote "https://github.com/$repo.git" "refs/tags/$tag" | cut -f1)"
  if [[ ! "$tag_object" =~ ^[0-9a-f]{40}$ ]]; then
    echo "ERROR: $repo has no single tag $tag (ls-remote gave: ${tag_object:-nothing})" >&2
    exit 1
  fi

  clone="$WORK/$name"
  stage="$WORK/$name.stage"
  git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$tag" --filter=blob:none --no-checkout "https://github.com/$repo.git" "$clone"

  files=(LICENSE)
  for crd in $crds; do
    files+=("config/crd/bases/$crd")
  done

  # Each file is read from the tag's tree, not a checkout, so that no checkout
  # conversion (core.autocrlf, a filter) changes its bytes. The blob id is
  # recorded before SOURCE is written (an id taken inside an echo would leave
  # a failure unseen by set -e), and the bytes written must hash, unconverted,
  # to it.
  declare -A blob=()
  for f in "${files[@]}"; do
    mkdir -p "$stage/$(dirname "$f")"
    git -C "$clone" cat-file blob "HEAD:$f" >"$stage/$f"
    blob[$f]="$(git -C "$clone" rev-parse "HEAD:$f")"
    written="$(git hash-object --no-filters "$stage/$f")"
    if [[ "$written" != "${blob[$f]}" ]]; then
      echo "ERROR: $repo $f was written as blob $written, not the tag's ${blob[$f]}" >&2
      exit 1
    fi
  done

  # The repository's forbidden-terms guard scans every tracked file under pkg/,
  # and an upstream CRD may name one of its terms for a reason of its own (a
  # receiver type). The guard's escape hatch, an allow-term pragma, goes in as a
  # YAML comment line above each such line, indented as it is. SOURCE pins the
  # committed bytes; for a file given a pragma it also names upstream's blob id,
  # which the file must hash to without its pragma lines, checked here as the
  # tests check it.
  declare -A committed=()
  for f in "${files[@]:1}"; do
    awk -v terms="$TERMS" '
      BEGIN { n = split(terms, t, "|") }
      {
        low = tolower($0); tags = ""
        for (i = 1; i <= n; i++)
          if (match(low, "(^|[^a-z0-9_])" t[i] "([^a-z0-9_]|$)")) tags = tags (tags == "" ? "" : " ") "allow-term:" t[i]
        if (tags != "") { match($0, /^ */); print substr($0, 1, RLENGTH) "# " tags }
        print
      }' "$stage/$f" >"$stage/$f.pragma"
    mv "$stage/$f.pragma" "$stage/$f"
    stripped="$(grep -vE "$PRAGMA_RE" "$stage/$f" | git hash-object --no-filters --stdin)"
    if [[ "$stripped" != "${blob[$f]}" ]]; then
      echo "ERROR: $repo $f is blob $stripped without its pragma lines, not the tag's ${blob[$f]}" >&2
      exit 1
    fi
    committed[$f]="$(git hash-object --no-filters "$stage/$f")"
  done

  {
    echo "# CRDs of fluxcd/$name, copied by scripts/vendor-flux-crds.sh."
    echo "# Licensed under the Apache License, Version 2.0; LICENSE is the project's copy of the licence, at the same tag."
    echo "# Each pin below is the git blob id of the committed file. A line naming a term the forbidden-terms"
    echo "# guard lists is preceded by an allow-term pragma comment line; an \"# upstream\" line names upstream's"
    echo "# blob id of a file that carries one, which the components tests check with those lines stripped."
    echo "tag $tag"
    echo "tag-object $tag_object"
    echo "licence LICENSE ${blob[LICENSE]}"
    for f in "${files[@]:1}"; do
      echo "file $f ${committed[$f]}"
      if [[ "${committed[$f]}" != "${blob[$f]}" ]]; then
        echo "# upstream $f ${blob[$f]}"
      fi
    done
  } >"$stage/SOURCE"
  unset blob committed

  dest="$UPSTREAM_DIR/$name"
  rm -rf "$dest"
  mkdir -p "$dest"
  cp -r "$stage/." "$dest/"
  echo "vendored $(( ${#files[@]} - 1 )) CRDs from $repo $tag into ${dest#"$REPO_ROOT"/}"
done
