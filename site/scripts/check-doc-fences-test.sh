#!/usr/bin/env bash
# check-doc-fences-test.sh — self-test for check-doc-fences.sh (go-kure/launcher#442).
#
# The gate is only proven if a broken fence makes it fail. Every case below writes
# a throwaway Markdown file into a temp directory, runs the gate on it, and asserts
# the exit status and, for a failure, the reported file:line. The fixtures live in
# this script rather than as tracked .md files so the gate's own full-tree scan
# never sees a deliberately broken fence.
#
# The regression case the issue names — `traits: []` at spec level in the
# quickstart (go-kure/launcher#417) — is run against a mutated copy of the real
# site/content/getting-started/quickstart.md, not a hand-written stand-in, so the
# test breaks if the quickstart's fence ever stops being checked.
#
# Usage: bash site/scripts/check-doc-fences-test.sh
# (invoked via `make check-doc-fences`, which builds bin/kurel first)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="$REPO_ROOT/site/scripts/check-doc-fences.sh"
KUREL_BIN="${KUREL_BIN:-$REPO_ROOT/bin/kurel}"
case "$KUREL_BIN" in /*) ;; *) KUREL_BIN="$REPO_ROOT/$KUREL_BIN" ;; esac
export KUREL_BIN

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# A profile granting nothing and one granting `expose`, both local to the fixture
# root so no case depends on examples/ staying as it is.
cat >"$WORK/empty-profile.yaml" <<'EOF'
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: empty
spec: {}
EOF
cat >"$WORK/expose-profile.yaml" <<'EOF'
apiVersion: launcher.gokure.dev/v1alpha1
kind: ClusterProfile
metadata:
  name: expose
spec:
  capabilities:
    expose:
      rendering:
        controllerType: ingress
        ingressClassName: traefik
EOF

pass=0
fail=0

# expect <name> <want-rc: 0|1|2> <want-substring-or-empty> <file>
expect() {
  local name="$1" want_rc="$2" want_out="$3" file="$4" rc=0 out
  out="$(bash "$GATE" --root "$WORK" "$file" 2>&1)" || rc=$?
  if [[ "$rc" -ne "$want_rc" ]]; then
    echo "FAIL $name: exit $rc, want $want_rc"
    printf '%s\n' "$out" | sed 's/^/    /'
    fail=$((fail + 1))
    return
  fi
  if [[ -n "$want_out" ]] && ! grep -qF -- "$want_out" <<<"$out"; then
    echo "FAIL $name: output lacks '$want_out'"
    printf '%s\n' "$out" | sed 's/^/    /'
    fail=$((fail + 1))
    return
  fi
  echo "ok   $name"
  pass=$((pass + 1))
}

# ── build mode ────────────────────────────────────────────────────────────────

cat >"$WORK/build-ok.md" <<'EOF'
# ok

```yaml {check="build" profile="empty-profile.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
```
EOF
expect "build: a complete application builds" 0 "" build-ok.md

cat >"$WORK/build-bad.md" <<'EOF'
# bad

Some prose.

```yaml {check="build" profile="empty-profile.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
  traits: []
```
EOF
expect "build: an envelope defect fails at the fence's line" 1 "build-bad.md:5: kurel build failed" build-bad.md

cat >"$WORK/build-needs-grant.md" <<'EOF'
```yaml {check="build" profile="empty-profile.yaml"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
spec:
  components:
    - name: web
      type: webservice
      properties:
        image: nginx:1.27
        port: 80
      traits:
        - type: expose
          properties:
            rules:
              - host: hello.example.com
                paths:
                  - path: /
                    port: 80
```
EOF
expect "build: the profile is the one named, not a permissive default" 1 "build-needs-grant.md:1: kurel build failed" build-needs-grant.md
sed 's/empty-profile/expose-profile/' "$WORK/build-needs-grant.md" >"$WORK/build-granted.md"
expect "build: the same fence passes against a profile granting expose" 0 "" build-granted.md

cat >"$WORK/build-no-profile.md" <<'EOF'
```yaml {check="build"}
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: hello
```
EOF
expect "build: a build marker without a profile is refused" 1 "build-no-profile.md:1: build mode needs profile=" build-no-profile.md

sed 's/empty-profile.yaml/no-such-profile.yaml/' "$WORK/build-ok.md" >"$WORK/build-missing-profile.md"
expect "build: a profile path that does not exist is refused" 1 "build-missing-profile.md:3: profile not found" build-missing-profile.md

# ── snippet and template modes ────────────────────────────────────────────────

cat >"$WORK/snippet.md" <<'EOF'
```yaml {check="snippet"}
# app.yaml
apiVersion: launcher.gokure.dev/v1alpha1
kind: Application
metadata:
  name: my-app
```

```yaml {check="snippet"}
metadata:
  name: [unclosed
```
EOF
expect "snippet: malformed YAML fails at its own fence, not the first" 1 "snippet.md:9: snippet is not well-formed YAML" snippet.md

cat >"$WORK/snippet-ok.md" <<'EOF'
```yaml {check="snippet"}
metadata:
  name: my-app
```
EOF
expect "snippet: well-formed YAML passes without being a whole document" 0 "" snippet-ok.md

cat >"$WORK/template.md" <<'EOF'
```yaml {check="template"}
spec:
  components:
  - name: web
    properties:
      replicas: ${replicas}
      env: ${env}
```
EOF
expect "template: unresolved placeholders in well-formed YAML pass" 0 "" template.md

cat >"$WORK/template-bad.md" <<'EOF'
```yaml {check="template"}
spec:
  components:
  - name: web
     properties: ${replicas}
```
EOF
expect "template: malformed YAML still fails" 1 "template-bad.md:1: template is not well-formed YAML" template-bad.md

# ── marker and fence handling ─────────────────────────────────────────────────

cat >"$WORK/unmarked.md" <<'EOF'
```yaml
spec:
  traits: []
  name: [unclosed
```
EOF
expect "unmarked: a broken unmarked fence is not checked" 0 "" unmarked.md

cat >"$WORK/unknown-mode.md" <<'EOF'
```yaml {check="biuld"}
a: 1
```
EOF
expect "marker: an unknown mode is refused, not skipped" 1 "unknown-mode.md:1: unknown check mode" unknown-mode.md

# Goldmark, which parses the attribute block for Hugo, allows spaces around `=`, so
# `{check = "snippet"}` is as much a marker as `{check="snippet"}`.
cat >"$WORK/spaced-snippet.md" <<'EOF'
```yaml {check = "snippet"}
name: [unclosed
```
EOF
expect "marker: spaces around = still mark the fence" 1 "spaced-snippet.md:1: snippet is not well-formed YAML" spaced-snippet.md

sed 's/{check="build" profile="empty-profile.yaml"}/{check= "build"  profile ="empty-profile.yaml"}/' \
  "$WORK/build-ok.md" >"$WORK/spaced-build.md"
expect "marker: spaces around = still find the mode and the profile" 0 "checked build=1" spaced-build.md

# A block naming `check` that cannot be read as check=<mode> is refused, not
# taken for an unmarked fence.
cat >"$WORK/bare-check.md" <<'EOF'
```yaml {check}
name: [unclosed
```
EOF
expect "marker: a bare check with no value is refused" 1 "bare-check.md:1: check marker cannot be parsed" bare-check.md

cat >"$WORK/colon-check.md" <<'EOF'
```yaml {check: "snippet"}
name: [unclosed
```
EOF
expect "marker: check written with a colon is refused" 1 "colon-check.md:1: check marker cannot be parsed" colon-check.md

# The word inside another attribute's quoted value is not a marker.
cat >"$WORK/quoted-check.md" <<'EOF'
```yaml {title="then check = it"}
name: [unclosed
```
EOF
expect "marker: check inside a quoted value is not a marker" 0 "checked build=0 snippet=0" quoted-check.md

cat >"$WORK/wrong-lang.md" <<'EOF'
```bash {check="snippet"}
echo hi
```
EOF
expect "marker: a marker on a non-YAML fence is refused" 1 "wrong-lang.md:1: check marker on a 'bash' fence" wrong-lang.md

cat >"$WORK/unclosed.md" <<'EOF'
```yaml {check="snippet"}
a: 1
EOF
expect "fence: an unclosed marked fence is refused" 1 "unclosed.md:1: unclosed" unclosed.md

cat >"$WORK/list-item.md" <<'EOF'
1. Write the app:

   ```yaml {check="build" profile="empty-profile.yaml"}
   apiVersion: launcher.gokure.dev/v1alpha1
   kind: Application
   metadata:
     name: hello
   spec:
     components:
       - name: web
         type: webservice
         properties:
           image: nginx:1.27
           port: 80
   ```
EOF
expect "fence: an indented fence inside a list item is de-indented and built" 0 "" list-item.md

# The bare ``` before the quoted marker is shorter than the four-backtick opener,
# so it does not close the outer fence and the marker stays quoted.
cat >"$WORK/nested.md" <<'EOF'
````markdown
Close a fence with three backticks:
```
Then mark the next one:
```yaml {check="snippet"}
name: [unclosed
```
````
EOF
expect "fence: a marker quoted inside an outer fence is not a fence" 0 "" nested.md

# A ~~~ fence closes only on tildes: the backtick lines are body, so the malformed
# line after them is still inside the fence and must be checked.
cat >"$WORK/tilde.md" <<'EOF'
~~~yaml {check="snippet"}
a: |
  ```
  not a fence close
  ```
b: [bad
~~~
EOF
expect "fence: backticks do not close a tilde fence" 1 "tilde.md:1: snippet is not well-formed YAML" tilde.md

# A line starting with ``` whose info string holds a backtick is an inline code
# span, not a fence opener, so nothing after it is taken for fence body.
cat >"$WORK/backtick-info.md" <<'EOF'
```yaml {check="snippet"}``` marks a fence for checking.

Plain prose.
EOF
expect "fence: a backtick in the info string means not a fence" 0 "checked build=0 snippet=0" backtick-info.md

# A closing fence may be indented at most three spaces more than its opener; a
# deeper fence-like line (here inside a block scalar) is body, so the malformed
# line after it is still inside the fence and must be checked.
cat >"$WORK/deep-closer.md" <<'EOF'
```yaml {check="snippet"}
description: |
      ```
x: [bad
```
EOF
expect "fence: a fence-like line indented four or more spaces does not close" 1 "deep-closer.md:1: snippet is not well-formed YAML" deep-closer.md

# The backticks below are literal Markdown, not command substitution.
# shellcheck disable=SC2016
printf '```yaml {check="snippet"}\r\na: 1\r\n```\r\n' >"$WORK/crlf.md"
expect "fence: CRLF line endings close a fence" 0 "" crlf.md

# shellcheck disable=SC2016
printf -- '- item\n\n\t```yaml {check="snippet"}\n\ta:\n\t  b: 1\n\t```\n' >"$WORK/tab.md"
expect "fence: a tab-indented fence in a list item is de-indented" 0 "" tab.md

# With no FILE the gate lists tracked files via git; if git cannot list them the
# gate must not report a pass over zero files.
mkdir -p "$WORK/not-a-repo"
nogit_rc=0
nogit_out="$(GIT_CEILING_DIRECTORIES="$WORK" bash "$GATE" --root "$WORK/not-a-repo" 2>&1)" || nogit_rc=$?
if [[ "$nogit_rc" -eq 2 ]]; then
  echo "ok   scan: a failed git ls-files is an error, not a pass over zero files"
  pass=$((pass + 1))
else
  echo "FAIL scan: a failed git ls-files is an error, not a pass over zero files: exit $nogit_rc, want 2"
  printf '%s\n' "$nogit_out" | sed 's/^/    /'
  fail=$((fail + 1))
fi
# If the fence extractor itself fails, the file must not read as clean.
mkdir -p "$WORK/broken-awk"
printf '#!/bin/sh\nexit 1\n' >"$WORK/broken-awk/awk"
chmod +x "$WORK/broken-awk/awk"
awk_rc=0
awk_out="$(PATH="$WORK/broken-awk:$PATH" bash "$GATE" --root "$WORK" snippet-ok.md 2>&1)" || awk_rc=$?
if [[ "$awk_rc" -eq 2 ]] && grep -qF "snippet-ok.md: extracting fences failed" <<<"$awk_out"; then
  echo "ok   scan: a failed fence extraction is an error, not a clean file"
  pass=$((pass + 1))
else
  echo "FAIL scan: a failed fence extraction is an error, not a clean file: exit $awk_rc, want 2"
  printf '%s\n' "$awk_out" | sed 's/^/    /'
  fail=$((fail + 1))
fi

mkdir -p "$WORK/empty-repo"
git -C "$WORK/empty-repo" init -q
empty_rc=0
empty_out="$(bash "$GATE" --root "$WORK/empty-repo" 2>&1)" || empty_rc=$?
if [[ "$empty_rc" -eq 2 ]] && grep -qF "refusing to pass an empty scan" <<<"$empty_out"; then
  echo "ok   scan: a repository with no tracked Markdown is an error, not a pass"
  pass=$((pass + 1))
else
  echo "FAIL scan: a repository with no tracked Markdown is an error, not a pass: exit $empty_rc, want 2"
  printf '%s\n' "$empty_out" | sed 's/^/    /'
  fail=$((fail + 1))
fi

# ── the go-kure/launcher#417 regression, on the real quickstart ──────────────

QS=site/content/getting-started/quickstart.md
mkdir -p "$WORK/$(dirname "$QS")" "$WORK/examples/cluster-profiles"
cp "$REPO_ROOT/examples/cluster-profiles/"*.yaml "$WORK/examples/cluster-profiles/"
cp "$REPO_ROOT/$QS" "$WORK/$QS"
expect "quickstart: the real quickstart passes" 0 "" "$QS"

# Re-introduce the #417 defect: `traits: []` as a sibling of `components:` inside
# the marked Application fence. Assert the mutation landed, so a quickstart edit
# that moves the fence cannot turn this case into a vacuous pass.
awk '
  /^```yaml .*check="build"/ { infence = 1 }
  infence && /^```$/        { infence = 0 }
  { print }
  infence && /^spec:$/      { print "  traits: []"; done = 1 }
  END { exit done ? 0 : 1 }
' "$REPO_ROOT/$QS" >"$WORK/$QS" || {
  echo "FAIL quickstart mutant: no marked build fence with a spec: line found in $QS"
  fail=$((fail + 1))
}
qs_line="$(grep -n '^```yaml .*check="build"' "$REPO_ROOT/$QS" | head -1 | cut -d: -f1 || true)"
expect "quickstart: traits: [] at spec level fails the gate (go-kure/launcher#417)" 1 "$QS:$qs_line: kurel build failed" "$QS"

echo
echo "check-doc-fences self-test: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
