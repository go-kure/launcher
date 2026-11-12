#!/usr/bin/env bash
# check-api-anchors-test.sh — self-test for check-api-anchors.sh (go-kure/launcher#1335).
#
# The check is only proven if a built site with a lost anchor or a broken fragment link
# makes it fail. Each case writes a throwaway built tree and pin list into a temp
# directory, runs the check on them and asserts the exit status and, for a failure, the
# anchor or link it names. The fixtures live in this script, with no Hugo run and no
# network.
#
# Usage: bash site/scripts/check-api-anchors-test.sh
# (`make check-api-anchors` runs it first, then the check on the real build; CI runs that target)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="$REPO_ROOT/site/scripts/check-api-anchors.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0
n=0

# page SITE DIR BODY: write SITE/DIR/index.html. DIR is relative to the site root.
page() {
    mkdir -p "$1/$2"
    printf '<html><body><main>%s</main></body></html>\n' "$3" > "$1/$2/index.html"
}

# pins FILE LINE...: write a pin list.
pins() {
    local f="$1"
    shift
    printf '%s\n' "$@" > "$f"
}

# expect NAME WANT_RC WANT_TEXT -- GATE_ARGS...: run the check, assert exit status and text.
expect() {
    local name="$1" want_rc="$2" want_text="$3"
    shift 4
    n=$((n + 1))
    local out rc=0
    out="$(bash "$GATE" "$@" 2>&1)" || rc=$?
    if [[ "$rc" -ne "$want_rc" ]]; then
        fail=$((fail + 1))
        printf 'FAIL %s: exit %s, want %s\n%s\n' "$name" "$rc" "$want_rc" "$out" | sed 's/^/    /'
    elif [[ -n "$want_text" ]] && ! grep -Fq -- "$want_text" <<<"$out"; then
        fail=$((fail + 1))
        printf 'FAIL %s: output lacks "%s"\n%s\n' "$name" "$want_text" "$out" | sed 's/^/    /'
    else
        pass=$((pass + 1))
        printf 'PASS %s\n' "$name"
    fi
}

# 1: every pin is there and every fragment link resolves. The markup mixes the minified
# form (no attribute quotes) and the quoted form, an in-page link, a root-relative link
# with the base path and a relative link.
d="$WORK/clean"
page "$d/site" api-reference/a '<h1 id=a-title>A</h1><h2 id="a-one">One</h2><h2 id=a-two>Two</h2>
  <a href=#a-two>in page</a> <a href="/launcher/api-reference/b/#b-one">root relative</a>
  <a href=../b/#b-one>relative</a> <a href="#a-one">quoted</a>'
page "$d/site" api-reference/b '<h2 id=b-one>One</h2>'
pins "$d/pins.txt" '# a comment' '' 'api-reference/a/ a-one' 'api-reference/a/ a-two' 'api-reference/b/ b-one'
expect "pins found and fragment links resolve" 0 "3 pinned anchors found, 4 fragment links resolved on 2 pages" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 2: a pinned anchor is gone from its page.
d="$WORK/lost-pin"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
pins "$d/pins.txt" 'api-reference/a/ a-one' 'api-reference/a/ a-gone'
expect "pinned anchor missing from its page" 1 "api-reference/a/ a-gone" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 3: the pinned page itself is gone.
d="$WORK/lost-page"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
pins "$d/pins.txt" 'api-reference/gone/ x'
expect "pinned page missing" 1 "api-reference/gone/ x: page not found" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 4: an attribute that only ends in id is no anchor.
d="$WORK/data-id"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><div data-id=a-fake>x</div>'
pins "$d/pins.txt" 'api-reference/a/ a-fake'
expect "data-id is not an id" 1 "api-reference/a/ a-fake" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 5: an in-page fragment link has no target.
d="$WORK/broken-inpage"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href=#a-nowhere>x</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "broken in-page fragment link" 1 "link #a-nowhere" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 6: a link into another page names an id that page lacks.
d="$WORK/broken-cross"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href=../b/#b-moved>x</a>'
page "$d/site" api-reference/b '<h2 id=b-one>One</h2>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "broken fragment link into another page" 1 "link ../b/#b-moved" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 7: a link with a fragment names a page that does not exist.
d="$WORK/broken-page"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href="/launcher/api-reference/gone/#x">x</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "fragment link to a missing page" 1 "target page not found" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 8: a link to another site, a root-relative link outside the base path and a relative
# link that leaves the site are not read, and are counted.
d="$WORK/outside"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://example.org/x/#nope">ext</a> <a href=/other/#nope>root</a> <a href=../../../up/#nope>up</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "links outside the site are counted, not read" 0 "3 outside the site, not read" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 9: --base-path changes which root-relative links are read.
d="$WORK/base"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href=/x/api-reference/a/#a-one>x</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "--base-path" 0 "1 fragment links resolved" -- \
    --site "$d/site" --pins "$d/pins.txt" --base-path /x/

# 10: --print-pins lists the heading ids, in order, and nothing else. Its output is a
# valid pin list for the same site.
d="$WORK/print"
page "$d/site" api-reference/a '<h1 id=a-title>A</h1><div id=not-a-heading>x</div><h2 id="a-one">One</h2><h3 id=a-sub>Sub</h3>'
expect "--print-pins lists headings in order" 0 "api-reference/a/ a-title
api-reference/a/ a-one
api-reference/a/ a-sub" -- --site "$d/site" --print-pins api-reference/a/
bash "$GATE" --site "$d/site" --print-pins api-reference/a/ > "$d/pins.txt"
expect "--print-pins output is a valid pin list" 0 "3 pinned anchors found" -- \
    --site "$d/site" --pins "$d/pins.txt"
if grep -Fq not-a-heading "$d/pins.txt"; then
    fail=$((fail + 1)); n=$((n + 1)); echo "FAIL --print-pins listed a non-heading id"
else
    pass=$((pass + 1)); n=$((n + 1)); echo "PASS --print-pins skips non-heading ids"
fi

# 11 to 16: input errors exit 2 and never pass. An empty pin list and an empty site
# would otherwise read as a clean run.
d="$WORK/inputs"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
: > "$d/empty.txt"
printf '# only a comment\n\n' > "$d/comments.txt"
pins "$d/pins.txt" 'api-reference/a/ a-one'
mkdir -p "$d/nopages"
expect "empty pin list is refused" 2 "no pins" -- --site "$d/site" --pins "$d/empty.txt"
expect "comment-only pin list is refused" 2 "no pins" -- --site "$d/site" --pins "$d/comments.txt"
expect "site with no pages is refused" 2 "no pages" -- --site "$d/nopages" --pins "$d/pins.txt"
expect "missing site is refused" 2 "built site not found" -- --site "$d/none" --pins "$d/pins.txt"
expect "missing pin file is refused" 2 "pin list not found" -- --site "$d/site" --pins "$d/none.txt"
expect "unknown option is refused" 2 "unknown option" -- --nope
expect "option without a value is refused" 2 "--site needs a value" -- --site
expect "option followed by an option is refused" 2 "--pins needs a value" -- --pins --site

echo
echo "check-api-anchors-test: $pass of $n passed"
[[ $fail -eq 0 ]]
