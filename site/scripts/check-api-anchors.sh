#!/usr/bin/env bash
# check-api-anchors.sh — check that the built docs site keeps its API-reference anchors
# and has no broken fragment link (go-kure/launcher#1335).
#
# Two reads of the built site, both on the HTML Hugo wrote (every *.html file):
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
# A read error is exit 2, never an empty scan. Ids and hrefs come from real tag attributes,
# with HTML entities and percent escapes decoded. Comments and script elements are cut out
# first. The reads work on the minified page Hugo writes with --minify, which the docs build
# uses: a tag, comment or script split over several lines is not read as one.
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

# An attribute value is quoted or, in a minified build, bare. A quoted value can hold `>`.
VALUE="(\"[^\"]*\"|'[^']*'|[^[:space:]>\"']+)"
ATTR="[[:space:]][^[:space:]=>/\"']+(=$VALUE)?"
INTAG="([^>\"']|\"[^\"]*\"|'[^']*')*"
ANYTAG="<[A-Za-z]$INTAG>"
HEADTAG="<h[1-6]([[:space:]]$INTAG)?>"
unquote() { sed -E "s/^[[:space:]]*[a-z-]+=//; s/^[\"']//; s/[\"']\$//"; }

# htmldec: the HTML character references in each line decoded: the numeric ones, and the few
# names an attribute value carries. Any other name stays as written.
htmldec() {
    local l n c
    while IFS= read -r l; do
        while [[ "$l" =~ \&#([0-9]+)\; || "$l" =~ \&#[xX]([0-9a-fA-F]+)\; ]]; do
            n="${BASH_REMATCH[1]}"
            if [[ "${BASH_REMATCH[0]}" == *[xX]* ]]; then n=$((16#$n)); else n=$((10#$n)); fi
            c="$(printf "\\U$(printf '%08x' "$n")" 2>/dev/null)" || c="?"
            l="${l/"${BASH_REMATCH[0]}"/"$c"}"
        done
        printf '%s\n' "$l"
    done | sed -E "s/&quot;/\"/g; s/&apos;/'/g; s/&lt;/</g; s/&gt;/>/g; s/&num;/#/g; s/&amp;/\&/g"
}

# pctdec STRING: sets REPLY to the string with its %XX escapes decoded. A decoded newline
# stays, and %00 stays as text, because a shell cannot hold a NUL byte.
pctdec() {
    local s="${1//%00/%2500}" out
    s="${s//\\/\\\\}"
    out="$(printf '%b' "${s//%/\\x}"; printf x)"
    REPLY="${out%x}"
}

# gr ARGS: grep, where no match is no error and a read error ends the run with exit 2.
# The callers read through a command substitution, so the exit reaches the script.
gr() {
    local rc=0
    grep "$@" || rc=$?
    [[ $rc -le 1 ]] || { echo "check-api-anchors: read failed (grep exit $rc): $*" >&2; exit 2; }
}

# strip FILE: the page with its comments and script elements cut out. A minified page is one
# line, so the cut works line by line. A read error ends the run with exit 2.
strip() {
    local rc=0
    sed -E 's/<!--([^-]|-[^-])*-->//g; s#<script([^>]*)>([^<]|<[^/]|</[^s]|</s[^c])*</script>##g' "$1" || rc=$?
    [[ $rc -eq 0 ]] || { echo "check-api-anchors: read failed (sed exit $rc): $1" >&2; exit 2; }
}

# attrs_of FILE TAG_PATTERN NAME: the value of every NAME attribute on the tags that match
# TAG_PATTERN, one per line. Each attribute is read whole, with its quoted value, so
# text such as `title="x id=y"` or `<p>id=y</p>` gives no id.
attrs_of() {
    strip "$1" | gr -o -E "$2" | gr -o -E "$ATTR" | gr -E "^[[:space:]]$3=" | unquote | htmldec
}

# ids_of FILE: every id on the page, one per line.
ids_of() { attrs_of "$1" "$ANYTAG" id; }

# heading_ids_of FILE: the ids of the h1 to h6 elements, in page order.
heading_ids_of() { attrs_of "$1" "$HEADTAG" id; }

# hrefs_of FILE: every href that carries a fragment, one per line.
hrefs_of() { attrs_of "$1" "$ANYTAG" href | gr -F '#'; }

page_file() { echo "$SITE/${1%/}/index.html"; }

if [[ ${#PRINT[@]} -gt 0 ]]; then
    for p in "${PRINT[@]}"; do
        f="$(page_file "$p")"
        [[ -f "$f" ]] || die "page not found: $p"
        ids="$(heading_ids_of "$f")"
        [[ -z "$ids" ]] || while IFS= read -r id; do echo "${p%/}/ $id"; done <<<"$ids"
    done
    exit 0
fi

[[ -f "$PINS" ]] || die "pin list not found: $PINS"

found="$(find "$SITE" -name '*.html' -type f | sort)" || die "cannot read $SITE"
[[ -n "$found" ]] || die "no pages (*.html) under $SITE"
mapfile -t PAGES <<<"$found"

declare -A HAVE=()    # "<file>|<id>" -> 1, for every file read
declare -A LOADED=()
load() {
    [[ -n "${LOADED[$1]:-}" ]] && return 0
    LOADED[$1]=1
    local id ids
    ids="$(ids_of "$1")"
    [[ -z "$ids" ]] || while IFS= read -r id; do HAVE["$1|$id"]=1; done <<<"$ids"
}

fail=0
report() { echo "FAIL $*"; fail=1; }

# 1. Pins.
npins=0
while read -r page id _ || [[ -n "${page:-}" ]]; do
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
    hrefs="$(hrefs_of "$f" | sort -u)"
    [[ -n "$hrefs" ]] || continue
    while IFS= read -r href; do
        # A scheme, or `//host`, is another site.
        if [[ "$href" =~ ^[A-Za-z][A-Za-z0-9+.-]*: || "$href" == //* ]]; then
            noutside=$((noutside + 1)); continue
        fi
        pctdec "${href#*#}"; frag="$REPLY"
        path="${href%%#*}"
        pctdec "${path%%\?*}"; path="$REPLY"
        [[ -n "$frag" ]] || continue
        if [[ -z "$path" ]]; then
            target="$f"
        else
            if [[ "$path" == /* ]]; then
                # Normalize first, so `/x/../launcher/...` is read as the page it names.
                path="$(realpath -m -s -- "$path")"
                b="${BASE%/}"
                if [[ "$path" != "$b" && "$path" != "$b"/* ]]; then noutside=$((noutside + 1)); continue; fi
                abs="$SITE${path#"$b"}"
            else
                abs="$(dirname "$f")/$path"
            fi
            abs="$(realpath -m -s -- "$abs")"
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
    done <<<"$hrefs"
done

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "check-api-anchors: $npins pinned anchors found, $nlinks fragment links resolved on ${#PAGES[@]} pages ($noutside outside the site, not read)"
