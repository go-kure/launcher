#!/usr/bin/env bash
# check-revision-shown.sh — check that the built docs site shows the commit it was built from
# (go-kure/launcher#1336).
#
# scripts/gen-versions-toml.sh writes the short commit into the Hugo overlay as
# `commitSha`, and site/layouts/partials/menu-footer.html shows it in the sidebar
# footer of every page. This gate reads the built tree and fails if a page lacks the
# line, if the line shows another commit or label, or if the overlay itself carries a
# commit other than HEAD's. The last case is reported apart from a missing line, so a
# build in a tree without git metadata (commit `unknown`) is not mistaken for a
# missing partial.
#
# The commit is `git rev-parse --short HEAD`, the call the overlay script makes, so
# both sides abbreviate the same way and no length is fixed here.
#
# Usage: bash site/scripts/check-revision-shown.sh [options]
#   --site DIR       Built site. Default: site/public.
#   --overlay FILE   Hugo overlay the build used. Default: site/versions.toml.
#   --sha SHA        Expected short commit. Default: git rev-parse --short HEAD.
#   --label LABEL    Expected version label. Default: `version` in the overlay.
#   --repo-url URL   Commit links start here. Default: https://github.com/go-kure/launcher.
#   --nested PATH    A page below the home page, relative to --site. Default: getting-started/install.
# Exit 0 when every page shows the line, 1 when one does not, 2 on a usage or input error.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SITE="$REPO_ROOT/site/public"
OVERLAY="$REPO_ROOT/site/versions.toml"
SHA=""
LABEL=""
REPO_URL="https://github.com/go-kure/launcher"
NESTED="getting-started/install"

die() { echo "check-revision-shown: $*" >&2; exit 2; }

# An option without a value is a usage error (exit 2). `${2:?}` would end the shell with 1.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --site|--overlay|--sha|--label|--repo-url|--nested)
            [[ $# -ge 2 ]] || die "$1 needs a value"
            case "$1" in
                --site)     SITE="$2" ;;
                --overlay)  OVERLAY="$2" ;;
                --sha)      SHA="$2" ;;
                --label)    LABEL="$2" ;;
                --repo-url) REPO_URL="$2" ;;
                --nested)   NESTED="$2" ;;
            esac
            shift 2 ;;
        *) die "unknown option: $1" ;;
    esac
done

[[ -d "$SITE" ]] || die "built site not found: $SITE"
[[ -f "$OVERLAY" ]] || die "overlay not found: $OVERLAY"

if [[ -z "$SHA" ]]; then
    SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD)" || die "git rev-parse --short HEAD failed"
fi
[[ "$SHA" =~ ^[0-9a-f]{4,64}$ ]] || die "expected commit is not a hex abbreviation: $SHA"

# Read one `key = 'value'` line of the overlay's [params] table.
overlay_value() {
    sed -n "s/^[[:space:]]*$1 = '\\(.*\\)'[[:space:]]*\$/\\1/p" "$OVERLAY" | head -n 1
}

OVERLAY_SHA="$(overlay_value commitSha)"
OVERLAY_LABEL="$(overlay_value version)"
[[ -n "$LABEL" ]] || LABEL="$OVERLAY_LABEL"
[[ -n "$LABEL" ]] || die "no version label in $OVERLAY and none given"

fail=0
report() { echo "FAIL $*"; fail=1; }

# Cause one: the overlay itself carries another commit. Say so, whatever the pages show.
if [[ "$OVERLAY_SHA" != "$SHA" ]]; then
    report "overlay: commitSha is '${OVERLAY_SHA:-<missing>}', expected '$SHA' (the build ran without git metadata, or on another commit)"
fi

# Cause two: a page lacks the line. The footer is one sentence, so the page is flattened
# to one line first, and attribute quotes are optional because --minify drops them.
# Fields: "Version <label>, built from commit <a href=<repo>/commit/<sha>>sha</a>".
check_page() {
    local rel="$1" file="$SITE/$1" flat pattern
    [[ -f "$file" ]] || { report "$rel: page not found in $SITE"; return; }
    flat="$(tr '\n\t' '  ' < "$file")"
    pattern="Version ${LABEL//./\\.}, built from commit <a[^>]*href=\"?${REPO_URL//./\\.}/commit/${SHA}\"?[^>]*>${SHA}</a>"
    if ! grep -Eq -- "$pattern" <<<"$flat"; then
        report "$rel: no revision line for commit $SHA and version $LABEL"
    else
        echo "ok   $rel"
    fi
}

check_page "index.html"
check_page "${NESTED%/}/index.html"

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "check-revision-shown: 2 pages show commit $SHA (version $LABEL)"
