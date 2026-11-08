#!/usr/bin/env bash
# check-revision-shown-test.sh — self-test for check-revision-shown.sh (go-kure/launcher#1336).
#
# The gate is only proven if a site without the revision line makes it fail. Each case
# below writes a throwaway built tree and overlay into a temp directory, runs the gate on
# them and asserts the exit status and, for a failure, the page or cause it names. The
# fixtures live in this script, with no Hugo run and no network.
#
# Usage: bash site/scripts/check-revision-shown-test.sh
# (`make check-revision-shown` runs it first, then the gate on the real build; CI runs that target)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="$REPO_ROOT/site/scripts/check-revision-shown.sh"
REPO_URL="https://github.com/go-kure/launcher"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0
n=0

# footer LABEL SHA QUOTE: the sidebar footer as the partial writes it. QUOTE is '"' for a
# plain build and empty for a minified one, where attribute quotes are dropped.
footer() {
    local label="$1" sha="$2" q="$3"
    printf '<div id=R-footer><p>Version %s, built from commit <a href=%s%s/commit/%s%s>%s</a></p><p>Built with Hugo</p></div>\n' \
        "$label" "$q" "$REPO_URL" "$sha" "$q" "$sha"
}

# mksite DIR HOME_FOOTER NESTED_FOOTER: home page and one nested page. An empty footer
# argument writes the page with no revision line.
mksite() {
    local dir="$1" home="$2" nested="$3"
    mkdir -p "$dir/getting-started/install"
    printf '<html><body><main>home</main>\n%s</body></html>\n' "$home" > "$dir/index.html"
    printf '<html><body><main>install</main>\n%s</body></html>\n' "$nested" > "$dir/getting-started/install/index.html"
}

# mkoverlay FILE LABEL SHA: the part of the overlay the gate reads.
mkoverlay() {
    printf "[params]\n  version = '%s'\n  latestTag = 'v0.0.1'\n  commitSha = '%s'\n\n  [[params.versions]]\n    identifier = 'dev'\n" "$2" "$3" > "$1"
}

# expect NAME WANT_RC WANT_TEXT -- GATE_ARGS...: run the gate, assert exit status and text.
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

SHA7="a1b2c3d"
SHA12="a1b2c3d4e5f6"

# 1 and 2: the line is on both pages. A 12-character commit proves the length is not fixed.
for sha in "$SHA7" "$SHA12"; do
    d="$WORK/ok-${#sha}"
    mksite "$d/site" "$(footer dev "$sha" '"')" "$(footer dev "$sha" '"')"
    mkoverlay "$d/overlay.toml" dev "$sha"
    expect "line on both pages, ${#sha}-character commit" 0 "2 pages show commit $sha" -- \
        --site "$d/site" --overlay "$d/overlay.toml" --sha "$sha"
done

# 3: a minified build drops the attribute quotes and the gate still finds the line.
d="$WORK/minified"
mksite "$d/site" "$(footer dev "$SHA7" '')" "$(footer dev "$SHA7" '')"
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "minified markup without attribute quotes" 0 "2 pages show commit" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 4: a release label other than dev.
d="$WORK/release"
mksite "$d/site" "$(footer v0.1.0-alpha.1 "$SHA7" '"')" "$(footer v0.1.0-alpha.1 "$SHA7" '"')"
mkoverlay "$d/overlay.toml" v0.1.0-alpha.1 "$SHA7"
expect "release label with dots and a hyphen" 0 "version v0.1.0-alpha.1" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 5: no revision line anywhere. This is today's main: the overlay carries the commit and the
# pages drop it. The home page is named, and the overlay is not blamed.
d="$WORK/none"
mksite "$d/site" "" ""
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "no revision line names the home page" 1 "index.html: no revision line" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"
none_out="$(bash "$GATE" --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7" 2>&1 || true)"
n=$((n + 1))
if grep -Fq 'overlay:' <<<"$none_out"; then
    fail=$((fail + 1)); echo "FAIL no revision line blames the overlay too"
elif ! grep -Fq 'FAIL index.html' <<<"$none_out"; then
    fail=$((fail + 1)); echo "FAIL the no-line run printed no page failure, so the control proves nothing"
else
    pass=$((pass + 1)); echo "PASS no revision line does not blame the overlay"
fi

# 6: the commit on the pages is another commit.
d="$WORK/other-sha"
mksite "$d/site" "$(footer dev 9999999 '"')" "$(footer dev 9999999 '"')"
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "pages show another commit" 1 "no revision line for commit $SHA7" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 7: the build ran without git metadata, so the overlay says unknown and the line is absent.
# The overlay cause is named apart from the page cause.
d="$WORK/unknown"
mksite "$d/site" "" ""
mkoverlay "$d/overlay.toml" dev unknown
expect "overlay commit unknown names the overlay" 1 "overlay: commitSha is 'unknown'" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 8: the line shows "unknown" as a commit.
d="$WORK/unknown-line"
mksite "$d/site" "$(footer dev unknown '"')" "$(footer dev unknown '"')"
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "a page showing unknown as the commit fails" 1 "no revision line for commit $SHA7" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 9: the home page has the line and the nested page has none. The nested page is named.
d="$WORK/home-only"
mksite "$d/site" "$(footer dev "$SHA7" '"')" ""
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "line on the home page only names the nested page" 1 "getting-started/install/index.html: no revision line" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 10: the page shows another version label.
d="$WORK/other-label"
mksite "$d/site" "$(footer v9.9.9 "$SHA7" '"')" "$(footer v9.9.9 "$SHA7" '"')"
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "pages show another version label" 1 "no revision line for commit $SHA7 and version dev" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 11: the nested page file is missing. A missing page fails and never passes.
d="$WORK/missing-page"
mksite "$d/site" "$(footer dev "$SHA7" '"')" "$(footer dev "$SHA7" '"')"
rm "$d/site/getting-started/install/index.html"
mkoverlay "$d/overlay.toml" dev "$SHA7"
expect "a missing page fails" 1 "page not found" -- \
    --site "$d/site" --overlay "$d/overlay.toml" --sha "$SHA7"

# 12: a missing site is a usage error, not a pass.
expect "a missing site is a usage error" 2 "built site not found" -- \
    --site "$WORK/no-such-site" --overlay "$WORK/ok-7/overlay.toml" --sha "$SHA7"

# 13: an option without its value is a usage error (exit 2), and so is an unknown option.
# A shell parameter check would end the gate with 1, which reads as a failed revision check.
expect "an option without a value is a usage error" 2 "--site needs a value" -- \
    --overlay "$WORK/ok-7/overlay.toml" --site
expect "a last option without a value names that option" 2 "--sha needs a value" -- \
    --site "$WORK/ok-7/site" --sha
expect "an unknown option is a usage error" 2 "unknown option: --nope" -- \
    --nope

echo ""
echo "$pass passed, $fail failed ($n cases)"
[[ "$fail" -eq 0 ]]
