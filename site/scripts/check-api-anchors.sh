#!/usr/bin/env bash
# check-api-anchors.sh — check that the built docs site keeps its API-reference anchors
# and has no broken fragment link (go-kure/launcher#1335).
#
# Two reads of the built site, both on the HTML Hugo wrote:
#   1. Pins. site/api-reference-anchors.txt lists `<page> <id>` pairs, the heading ids the
#      oversized API-reference pages had before they were split. Each pinned id must still
#      exist on its page, so a link from outside that names an old section lands on a
#      page that still has it. A moved section leaves a stub heading with the same text.
#   2. Fragment links. Every `href` with a `#fragment` on every built page must resolve to
#      an id on the page it points at. The shared link check does not read fragments, so
#      a missing anchor passes it.
#
# Links to another site, root-relative links outside the base path and relative links
# that leave the site are not read. They are counted in the last line.
#
# Usage: bash site/scripts/check-api-anchors.sh [options]
#   --site DIR            Built site. Default: site/public.
#   --pins FILE           Pin list. Default: site/api-reference-anchors.txt.
#   --base-path PATH      Path the site is served under. Default: /launcher/.
#   --print-pins PAGE...  Print one `<page> <id>` line for each heading id on each built
#                         page (for example api-reference/oam/) and exit. This writes a pin list.
# Exit 0 when every pin and every fragment link resolves, 1 when one does not, 2 on a usage
# or input error. An empty pin list or a site with no pages is an input error.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SITE="$REPO_ROOT/site/public"
PINS="$REPO_ROOT/site/api-reference-anchors.txt"
BASE="/launcher/"
PRINT=()

die() { echo "check-api-anchors: $*" >&2; exit 2; }

# An option without a value is a usage error (exit 2), and so is an option followed by another
# option (a value starting with `--`). `${2:?}` would end the shell with 1.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --site|--pins|--base-path)
            [[ $# -ge 2 && "$2" != --* ]] || die "$1 needs a value"
            case "$1" in
                --site)      SITE="$2" ;;
                --pins)      PINS="$2" ;;
                --base-path) BASE="$2" ;;
            esac
            shift 2 ;;
        --print-pins)
            shift
            [[ $# -ge 1 && "$1" != --* ]] || die "--print-pins needs a page"
            while [[ $# -gt 0 && "$1" != --* ]]; do PRINT+=("$1"); shift; done ;;
        *) die "unknown option: $1" ;;
    esac
done

[[ -d "$SITE" ]] || die "built site not found: $SITE"
SITE="$(cd "$SITE" && pwd)"
[[ "$BASE" == /* && "$BASE" == */ ]] || die "--base-path must start and end with a slash: $BASE"

# An attribute value is quoted or, in a minified build, bare. The leading space keeps
# `data-id=` and `aria-id=` from reading as `id=`.
VALUE="(\"[^\"]*\"|'[^']*'|[^[:space:]>\"']+)"
unquote() { sed -E "s/^[[:space:]]*[a-z-]+=//; s/^[\"']//; s/[\"']\$//"; }

# ids_of FILE: every id on the page, one per line.
ids_of() { { grep -o -E "[[:space:]]id=$VALUE" "$1" || true; } | unquote; }

# heading_ids_of FILE: the ids of the h1 to h6 elements, in page order.
heading_ids_of() {
    { grep -o -E "<h[1-6][^>]*[[:space:]]id=$VALUE" "$1" || true; } | sed -E 's/.*[[:space:]]id=/id=/' | unquote
}

# hrefs_of FILE: every href that carries a fragment, one per line.
hrefs_of() { { grep -o -E "[[:space:]]href=$VALUE" "$1" || true; } | unquote | grep -F '#' || true; }

page_file() { echo "$SITE/${1%/}/index.html"; }

if [[ ${#PRINT[@]} -gt 0 ]]; then
    for p in "${PRINT[@]}"; do
        f="$(page_file "$p")"
        [[ -f "$f" ]] || die "page not found: $p"
        while IFS= read -r id; do echo "${p%/}/ $id"; done < <(heading_ids_of "$f")
    done
    exit 0
fi

[[ -f "$PINS" ]] || die "pin list not found: $PINS"

mapfile -t PAGES < <(find "$SITE" -name index.html -type f | sort)
[[ ${#PAGES[@]} -gt 0 ]] || die "no pages (index.html) under $SITE"

declare -A HAVE=()    # "<file>|<id>" -> 1, for every file read
declare -A LOADED=()
load() {
    [[ -n "${LOADED[$1]:-}" ]] && return 0
    LOADED[$1]=1
    local id
    while IFS= read -r id; do HAVE["$1|$id"]=1; done < <(ids_of "$1")
}

fail=0
report() { echo "FAIL $*"; fail=1; }

# 1. Pins.
npins=0
while read -r page id _; do
    [[ -z "${page:-}" || "$page" == \#* ]] && continue
    [[ -n "${id:-}" ]] || die "pin list line has a page and no id: $page"
    npins=$((npins + 1))
    f="$(page_file "$page")"
    if [[ ! -f "$f" ]]; then
        report "$page $id: page not found in $SITE"
        continue
    fi
    load "$f"
    [[ -n "${HAVE[$f|$id]:-}" ]] || report "$page $id: no element with that id on the built page"
done < "$PINS"
[[ $npins -gt 0 ]] || die "no pins in $PINS: refusing to pass an empty list"

# 2. Fragment links, on every built page.
nlinks=0
noutside=0
for f in "${PAGES[@]}"; do
    rel="${f#"$SITE"/}"
    while IFS= read -r href; do
        # A scheme, or `//host`, is another site.
        if [[ "$href" =~ ^[A-Za-z][A-Za-z0-9+.-]*: || "$href" == //* ]]; then
            noutside=$((noutside + 1)); continue
        fi
        frag="${href#*#}"
        path="${href%%#*}"
        path="${path%%\?*}"
        [[ -n "$frag" ]] || continue
        if [[ -z "$path" ]]; then
            target="$f"
        else
            if [[ "$path" == /* ]]; then
                if [[ "$path" != "$BASE"* ]]; then noutside=$((noutside + 1)); continue; fi
                abs="$SITE/${path#"$BASE"}"
            else
                abs="$(dirname "$f")/$path"
            fi
            abs="$(realpath -m -- "$abs")"
            if [[ "$abs" != "$SITE" && "$abs" != "$SITE"/* ]]; then noutside=$((noutside + 1)); continue; fi
            [[ -d "$abs" ]] && abs="$abs/index.html"
            target="$abs"
        fi
        nlinks=$((nlinks + 1))
        if [[ ! -f "$target" ]]; then
            report "$rel: link $href: target page not found"
            continue
        fi
        load "$target"
        [[ -n "${HAVE[$target|$frag]:-}" ]] || report "$rel: link $href: no id \"$frag\" on ${target#"$SITE"/}"
    done < <(hrefs_of "$f" | sort -u)
done

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "check-api-anchors: $npins pinned anchors found, $nlinks fragment links resolved on ${#PAGES[@]} pages ($noutside outside the site, not read)"
