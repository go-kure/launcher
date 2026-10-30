#!/usr/bin/env bash
# vendor-prometheus-operator-gates.sh — re-fetch the excerpt of the Prometheus
# operator's code that the version gates of the alertmanager, prometheus and
# thanosruler kinds are held to (go-kure/launcher#935, go-kure/launcher#980,
# go-kure/launcher#976), and rewrite its SOURCE file.
#
# The excerpt lives under
# pkg/oam/builtin/components/testdata/upstream/prometheus-operator/ and is read
# by TestAlertmanagerVersionGates_MatchVendoredSource,
# TestPrometheusVersionGates_MatchVendoredSource and
# TestThanosRulerVersionGates_MatchVendoredSource, which parse it with go/ast
# and need no network. The files are copied unmodified, Apache-2.0
# headers included, with the operator's LICENSE, the licence's full text, and
# its NOTICE, which the licence asks a redistributor to pass on; SOURCE names
# the tag, the tag object and the git blob id of each file, of LICENSE and of
# NOTICE, and the test checks every blob id against the file.
#
# Beside the code, it copies the CustomResourceDefinitions of the
# monitoring.coreos.com kinds launcher writes, unmodified from the tag's
# example/prometheus-operator-crd/ (the linked module ships none): the
# components tests create each generated kind's object against its CRD to show
# the API server's answer for a required field left out (go-kure/launcher#883).
# SOURCE names each on a `file` line with its git blob id, checked as the Go
# files are.
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

TAG_OBJECT="$(git ls-remote "https://github.com/$REPO.git" "refs/tags/$TAG" | cut -f1)"
if [[ ! "$TAG_OBJECT" =~ ^[0-9a-f]{40}$ ]]; then
  echo "ERROR: $REPO has no single tag $TAG (ls-remote gave: ${TAG_OBJECT:-nothing})" >&2
  exit 1
fi

UPSTREAM=""
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE" ${UPSTREAM:+"$UPSTREAM"}' EXIT
UPSTREAM="$(mktemp -d)"

# The tag's tree, sparse: the packages below, pkg/operator, and the root files
# (LICENSE, NOTICE), which a cone-mode sparse checkout always holds.
PACKAGES=(pkg/alertmanager pkg/prometheus pkg/prometheus/server pkg/prometheus/validation pkg/thanos)
git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$TAG" --filter=blob:none --sparse "https://github.com/$REPO.git" "$UPSTREAM"
git -C "$UPSTREAM" sparse-checkout set pkg/alertmanager pkg/prometheus pkg/thanos pkg/operator example/prometheus-operator-crd

# The files the tests read, by their path in prometheus-operator/prometheus-operator:
# every Go file of each package of PACKAGES, its tests aside, so that the tests
# walk each one for a version comparison, the operator's defaults, and the
# label-name scheme it picks by Prometheus version (go-kure/launcher#1018).
# pkg/prometheus/agent is left out: no kind of launcher's runs a Prometheus
# agent.
shopt -s nullglob
FILES=()
for pkg in "${PACKAGES[@]}"; do
  found=0
  for f in "$UPSTREAM/$pkg"/*.go; do
    [[ "$f" == *_test.go ]] && continue
    FILES+=("${f#"$UPSTREAM"/}")
    found=1
  done
  if (( found == 0 )); then
    echo "ERROR: $REPO $TAG has no Go file in $pkg" >&2
    exit 1
  fi
done
shopt -u nullglob
FILES+=(pkg/operator/defaults.go pkg/operator/validations.go)

# The CRDs of the kinds launcher writes: alertmanager, podmonitor,
# prometheus-probe, prometheus, prometheusrule, servicemonitor and thanosruler.
for plural in alertmanagers podmonitors probes prometheuses prometheusrules servicemonitors thanosrulers; do
  FILES+=("example/prometheus-operator-crd/monitoring.coreos.com_${plural}.yaml")
done

# Each file is read from the tag's tree, not the checkout, so that no checkout
# conversion (core.autocrlf, a filter) changes its bytes.
for f in "${FILES[@]}" LICENSE NOTICE; do
  mkdir -p "$STAGE/$(dirname "$f")"
  git -C "$UPSTREAM" cat-file blob "HEAD:$f" > "$STAGE/$f"
done

# Record the tag tree's blob id of every file before writing SOURCE (an id
# taken inside an echo would leave a failure unseen by set -e), and check that
# the bytes written hash, unconverted, to that id.
declare -A BLOB
for f in "${FILES[@]}" LICENSE NOTICE; do
  BLOB[$f]="$(git -C "$UPSTREAM" rev-parse "HEAD:$f")"
  written="$(git hash-object --no-filters "$STAGE/$f")"
  if [[ "$written" != "${BLOB[$f]}" ]]; then
    echo "ERROR: $f was written as blob $written, not the tag's ${BLOB[$f]}" >&2
    exit 1
  fi
done

{
  echo "# Excerpt of prometheus-operator/prometheus-operator, copied unmodified by scripts/vendor-prometheus-operator-gates.sh."
  echo "# Licensed under the Apache License, Version 2.0 (each Go file carries its header; each CRD is generated from the project's types);"
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
