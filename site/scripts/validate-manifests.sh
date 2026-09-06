#!/usr/bin/env bash
# validate-manifests.sh — Builds a representative subset of examples/*.yaml via
# `kurel build` and validates the generated manifests against fluxcd/flux-schema's
# `default` (embedded) catalog plus its `ecosystem` catalog (fetched from
# schemas.fluxoperator.dev — network access required).
#
# Scope: 14 of the 15 numbered examples/*.yaml + examples/cluster-profiles/*.yaml
# pairs (go-kure/launcher#292). Two are deliberately excluded, each echoed below:
#   - 12-daemonset.yaml: mounts a hostPath volume, which is default-deny. `kurel
#     build` supplies no Policy, transform normalizes that to NoopPolicy
#     (pkg/oam/transform.go:489), and NoopPolicy.AllowHostPathVolumes() is false
#     (pkg/oam/policy.go:89), so enforceHostPathVolumes rejects it
#     (pkg/oam/builtin/components/enforce.go:155). That is by design, not a bug: the
#     example demonstrates the node-agent shape, and building it needs a policy this
#     command line cannot supply.
#   - examples/custom-capability/app.yaml: cannot build without a Go-registered
#     "redis-sidecar" trait handler; a documented library-extension example, not a
#     bug (examples/README.md, "Why this example cannot be run with `kurel build`").
#
# One resource is schema-skipped rather than excluded outright:
#   - 15-passthrough-minimal.yaml's SparkApplication is deliberately minimal (it
#     demonstrates the `passthrough` extension point, not a deployable resource) and
#     fails schema validation by design. Skipped via --skip-kind, not silently
#     dropped from the fixture set.
#
# Usage: bash site/scripts/validate-manifests.sh
# (invoked via `make validate-manifests`, which builds bin/kurel first)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

KUREL_BIN="${KUREL_BIN:-bin/kurel}"

# The sole pin site for the flux-schema plugin (ci.yml's validate-manifests job
# used to pre-warm the same version via a `plugins: schema@<version>` input;
# that was deleted as redundant — ensure_schema_plugin() below already installs
# on mismatch). Tracked by the flux-schema-plugin customManager in renovate.json.
SCHEMA_PLUGIN_VERSION="0.12.1"

# app.yaml -> cluster-profile pairs (examples/README.md's "Application examples"
# table), skipping the two exclusions noted above. 14-full-stack.yaml uses the
# alternate nginx-certmanager-vault profile — its table-listed primary
# (gateway-certmanager-aws) fails to build (unrelated pre-existing issue).
declare -A APP_PROFILE=(
  [01-webservice-minimal.yaml]=minimal
  [02-webservice-with-expose.yaml]=minimal
  [03-webservice-with-tls.yaml]=nginx-certmanager-vault
  [04-webservice-full.yaml]=nginx-certmanager-vault
  [05-worker-minimal.yaml]=minimal
  [06-worker-with-traits.yaml]=nginx-certmanager-vault
  [07-cronjob-minimal.yaml]=minimal
  [08-cronjob-full.yaml]=nginx-certmanager-vault
  [09-postgresql-minimal.yaml]=minimal
  [10-postgresql-ha.yaml]=minimal
  [11-helmchart.yaml]=minimal
  [13-statefulset.yaml]=minimal
  [14-full-stack.yaml]=nginx-certmanager-vault
  [15-passthrough-minimal.yaml]=minimal
)

echo "Skipping 12-daemonset.yaml: mounts a hostPath volume, which is default-deny under NoopPolicy (by design — kurel build supplies no Policy)"
echo "Skipping examples/custom-capability/app.yaml: no Go-registered handler for its custom trait (documented library-extension example)"

if [[ ! -x "$KUREL_BIN" ]]; then
  echo "ERROR: $KUREL_BIN not found or not executable — run 'make build-kurel' first" >&2
  exit 1
fi

# Ensure the flux-schema plugin is installed at the pinned version. A stale
# locally-installed plugin would otherwise silently pass a presence-only check and
# validate against different catalog rules than CI.
ensure_schema_plugin() {
  if ! command -v flux >/dev/null; then
    echo "ERROR: flux CLI not found — install via mise (see mise.toml)" >&2
    exit 1
  fi
  local installed
  installed="$(flux plugin list | awk -F'\t' '$1 ~ /^schema[[:space:]]*$/ {gsub(/[[:space:]]+$/, "", $2); print $2}')"
  if [[ "$installed" != "$SCHEMA_PLUGIN_VERSION" ]]; then
    echo "Installing flux-schema plugin ${SCHEMA_PLUGIN_VERSION} (found: ${installed:-none})..."
    flux plugin install "schema@${SCHEMA_PLUGIN_VERSION}"
  fi
}
ensure_schema_plugin

OUTDIR="$(mktemp -d)"
trap 'rm -rf "$OUTDIR"' EXIT

fail=0
for app in "${!APP_PROFILE[@]}"; do
  profile="${APP_PROFILE[$app]}"
  nn="${app%%-*}"
  out="$OUTDIR/$nn"
  mkdir -p "$out"
  echo "Building examples/$app (profile: $profile)..."
  if ! "$KUREL_BIN" build "examples/$app" --profile "examples/cluster-profiles/$profile.yaml" -o "$out" 2>&1; then
    echo "ERROR: kurel build failed for examples/$app (profile: $profile)" >&2
    fail=1
  fi
done

if [[ "$fail" -ne 0 ]]; then
  echo "ERROR: one or more examples failed to build; skipping schema validation" >&2
  exit 1
fi

echo "Validating generated manifests under $OUTDIR against flux-schema (default + ecosystem catalogs)..."
if ! flux schema validate "$OUTDIR" \
  --schema-location default \
  --schema-location ecosystem \
  --skip-kind sparkoperator.k8s.io/v1beta2/SparkApplication \
  --verbose; then
  echo "ERROR: flux schema validate reported one or more invalid resources" >&2
  exit 1
fi

echo "All generated manifests are schema-valid."
