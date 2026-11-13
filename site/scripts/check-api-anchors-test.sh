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

# Every run reads an empty content tree and an empty docs map unless the case names its own, so
# no case reads the repository it sits in.
NOCONTENT="$WORK/nocontent"
NOMAP="$WORK/nomap.yaml"
mkdir -p "$NOCONTENT/api-reference"
printf 'packages: []\n' > "$NOMAP"
DEFAULTS=(--content "$NOCONTENT" --docs-map "$NOMAP")

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
    out="$(bash "$GATE" "${DEFAULTS[@]}" "$@" 2>&1)" || rc=$?
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

# expect_out NAME WANT_RC WANT_OUTPUT -- GATE_ARGS...: run the check, assert exit status and the
# whole output, line for line.
expect_out() {
    local name="$1" want_rc="$2" want_out="$3"
    shift 4
    n=$((n + 1))
    local out rc=0
    out="$(bash "$GATE" "${DEFAULTS[@]}" "$@" 2>&1)" || rc=$?
    if [[ "$rc" -ne "$want_rc" || "$out" != "$want_out" ]]; then
        fail=$((fail + 1))
        printf 'FAIL %s: exit %s, want %s\n%s\n' "$name" "$rc" "$want_rc" "$out" | sed 's/^/    /'
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

# 9b: an absolute link to the published site is read against the local build.
d="$WORK/siteurl"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://www.gokure.dev/launcher/api-reference/b/#b-one" rel=external>ok</a>'
page "$d/site" api-reference/b '<h2 id=b-one>One</h2>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect_out "absolute site link to a present anchor passes" 0 \
    "check-api-anchors: 1 pinned anchors found, 1 fragment links resolved on 2 pages (0 outside the site, not read), 0 parts registered" -- \
    --site "$d/site" --pins "$d/pins.txt"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://www.gokure.dev/launcher/api-reference/b/#b-gone" rel=external>bad</a>'
expect "absolute site link to a missing anchor fails" 1 'no id "b-gone"' -- \
    --site "$d/site" --pins "$d/pins.txt"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://www.gokure.dev/launcher/api-reference/c/#c-one" rel=external>bad</a>'
expect "absolute site link to a missing page fails" 1 "target page not found" -- \
    --site "$d/site" --pins "$d/pins.txt"
# Another host that starts with the site host, and a path outside the base path, are other sites.
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://www.gokure.dev.example.org/launcher/api-reference/b/#nope">host</a>
  <a href="https://www.gokure.dev/other/#nope">path</a>'
expect "a longer host and a path outside the base are not read" 0 "2 outside the site, not read" -- \
    --site "$d/site" --pins "$d/pins.txt"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="https://docs.test/launcher/api-reference/b/#b-gone">bad</a>'
expect "--site-url names the published origin" 1 'no id "b-gone"' -- \
    --site "$d/site" --pins "$d/pins.txt" --site-url https://docs.test
expect "--site-url without a value is refused" 2 "--site-url needs a value" -- --site-url

# 9c: a hand-written part of a package page (site/content/api-reference/<package>/<part>.md)
# must be listed in the `guides` of a package in the docs map. The doc gate counts a guide as
# the package's docs and counts nothing else, so an unlisted part would never satisfy it.
d="$WORK/parts"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
mkdir -p "$d/content/api-reference/a"
printf '# index\n' > "$d/content/api-reference/_index.md"
printf '# one\n' > "$d/content/api-reference/a/p1.md"
printf '# two\n' > "$d/content/api-reference/a/p2.md"
printf 'packages:\n  - path: pkg/a\n    guides: [api-reference/a/p1, api-reference/a/p2]\n' > "$d/map.yaml"
expect "parts listed in guides pass and are counted" 0 "2 parts registered" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/map.yaml"
printf 'packages:\n  - path: pkg/a\n    guides:\n      - api-reference/a/p1\n' > "$d/map1.yaml"
expect "a part missing from guides fails" 1 "api-reference/a/p2: part is in no package guides" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/map1.yaml"
printf 'packages:\n  - path: pkg/a\n    mount: {target: api-reference/a.md}\n' > "$d/map0.yaml"
expect "a package with no guides fails for every part" 1 "api-reference/a/p1: part is in no package guides" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/map0.yaml"
printf '# overview\n' > "$d/content/api-reference/a/_index.md"
expect "an _index.md is the overview, not a part" 0 "2 parts registered" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/map.yaml"
expect "missing content tree is refused" 2 "content tree not found" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/none" --docs-map "$d/map.yaml"
expect "missing docs map is refused" 2 "docs map not found" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/none.yaml"
printf 'packages: [\n' > "$d/bad.yaml"
expect "unreadable docs map is refused" 2 "cannot read guides" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/bad.yaml"
# A find that fails only on the content tree must stop the run, not read as zero parts.
mkdir -p "$d/bin"
printf '#!/bin/sh\ncase "$*" in *"%s"*) exit 1;; esac\nexec %s "$@"\n' "$d/content" "$(command -v find)" > "$d/bin/find"
chmod +x "$d/bin/find"
PATH="$d/bin:$PATH" expect "a failing read of the parts is exit 2" 2 "cannot read $d/content/api-reference" -- \
    --site "$d/site" --pins "$d/pins.txt" --content "$d/content" --docs-map "$d/map.yaml"
expect "--content without a value is refused" 2 "--content needs a value" -- --content

# 10: --print-pins lists the heading ids, in order, and nothing else. Its output is a
# valid pin list for the same site.
d="$WORK/print"
page "$d/site" api-reference/a '<h1 id=a-title>A</h1><div id=not-a-heading>x</div><h2 id="a-one">One</h2><h3 id=a-sub>Sub</h3>'
expect_out "--print-pins lists headings in order" 0 "api-reference/a/ a-title
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

# 11: text that reads like an id is no id: not page text, not another attribute's value.
d="$WORK/fake-id"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><p>example id=ghost text</p><a title="x id=ghost2" href=#a-one>y</a>'
pins "$d/pins.txt" 'api-reference/a/ ghost'
expect "id= in page text is no id" 1 "api-reference/a/ ghost: no element" -- \
    --site "$d/site" --pins "$d/pins.txt"
pins "$d/pins.txt" 'api-reference/a/ ghost2'
expect "id= inside another attribute is no id" 1 "api-reference/a/ ghost2: no element" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 12: a page that is not an index.html is read too.
d="$WORK/not-index"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
printf '<html><body><a href=/launcher/api-reference/a/#nope>x</a></body></html>\n' > "$d/site/404.html"
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "broken link on a page that is not index.html" 1 "404.html: link /launcher/api-reference/a/#nope" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 13: percent escapes and HTML entities in an href are decoded before the link is read.
d="$WORK/encoded"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>
  <a href="/launcher/api-reference/%61/#a%2Done">pct</a> <a href="/launcher/api-reference/a/?q=&#34;#a-one">entity</a>
  <a href="/launcher/api-reference/a/?x=1&amp;y=2#a-one">amp</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "encoded links resolve" 0 "3 fragment links resolved" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 14: a path with `..` is normalized before it is classed as inside or outside the base path.
d="$WORK/dotdot"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href=/else/../launcher/api-reference/a/#ghost>x</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "dot-dot path into the site is read" 1 "no id \"ghost\"" -- \
    --site "$d/site" --pins "$d/pins.txt"

# 15: a read error is exit 2, never an empty scan. A stub sed, the first reader of a page, fails on one page.
d="$WORK/read-error"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
page "$d/site" bad-page '<p>x</p>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
mkdir -p "$d/bin"
printf '#!/bin/sh\nfor a in "$@"; do case "$a" in *bad-page*) exit 2 ;; esac; done\nexec %s "$@"\n' "$(command -v sed)" > "$d/bin/sed"
chmod +x "$d/bin/sed"
PATH="$d/bin:$PATH" expect "read error is exit 2" 2 "read failed" -- --site "$d/site" --pins "$d/pins.txt"

# 16: a quoted attribute value can hold `>`. The tag ends at its own `>`.
d="$WORK/quoted-gt"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a title="a > b" href=#missing>x</a><p title="x id=ghost > y">t</p>'
pins "$d/pins.txt" 'api-reference/a/ ghost'
expect "link after a quoted > is read" 1 "link #missing" -- --site "$d/site" --pins "$d/pins.txt"
expect "id inside a quoted > is no id" 1 "api-reference/a/ ghost: no element" -- --site "$d/site" --pins "$d/pins.txt"

# 17: markup in a comment or a script element is no element.
d="$WORK/raw-text"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><!-- <h2 id=ghost>gone</h2> -->
  <script>const s = "<h2 id=ghost2>example</h2>"; // </div></script><h3 id=a-two>Two</h3>'
pins "$d/pins.txt" 'api-reference/a/ ghost'
expect "id in a comment is no id" 1 "api-reference/a/ ghost: no element" -- --site "$d/site" --pins "$d/pins.txt"
pins "$d/pins.txt" 'api-reference/a/ ghost2'
expect "id in a script string is no id" 1 "api-reference/a/ ghost2: no element" -- --site "$d/site" --pins "$d/pins.txt"
pins "$d/pins.txt" 'api-reference/a/ a-two'
expect "element after a script is read" 0 "1 pinned anchors found" -- --site "$d/site" --pins "$d/pins.txt"

# 18: character references are decoded before a link is filtered or split.
d="$WORK/refs"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href="&num;missing">x</a><a href=/laun&#99;her/api-reference/a/#gone>y</a><a href="&#35;a-one">ok</a><a href="&#x23;a-one">ok</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "&num; link is read" 1 'link #missing' -- --site "$d/site" --pins "$d/pins.txt"
expect "numeric reference in a path is read" 1 'no id "gone"' -- --site "$d/site" --pins "$d/pins.txt"
page "$d/ok" api-reference/a '<h2 id=a-one>One</h2><a href="&#35;a-one">ok</a><a href="&#x23;a-one">ok</a>'
expect "numeric references for # resolve" 0 "1 fragment links resolved" -- --site "$d/ok" --pins "$d/pins.txt"

# 19: a percent escape for a newline or a NUL byte alters no fragment.
d="$WORK/pct-bytes"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2><a href="#a-one%0A">x</a><a href="#a-one%00">y</a>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
expect "encoded newline is no match" 1 "link #a-one%0A" -- --site "$d/site" --pins "$d/pins.txt"
expect "encoded NUL is no match" 1 'no id "a-one%00"' -- --site "$d/site" --pins "$d/pins.txt"

# 20: a pin on the last line of the list is read when the file has no trailing newline.
d="$WORK/no-newline"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
printf 'api-reference/a/ a-one\napi-reference/a/ a-gone' > "$d/pins.txt"
expect "last pin without a newline is checked" 1 "api-reference/a/ a-gone: no element" -- --site "$d/site" --pins "$d/pins.txt"

# 21: a find that fails is exit 2, never a broken anchor. A stub find fails.
d="$WORK/find-error"
page "$d/site" api-reference/a '<h2 id=a-one>One</h2>'
pins "$d/pins.txt" 'api-reference/a/ a-one'
mkdir -p "$d/bin"
printf '#!/bin/sh\nexit 1\n' > "$d/bin/find"
chmod +x "$d/bin/find"
PATH="$d/bin:$PATH" expect "find error is exit 2" 2 "cannot read" -- --site "$d/site" --pins "$d/pins.txt"

# 22 to 29: input errors exit 2 and never pass. An empty pin list and an empty site
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
