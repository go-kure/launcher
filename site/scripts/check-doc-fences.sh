#!/usr/bin/env bash
# check-doc-fences.sh — checks the YAML fences in the documentation that are
# marked for checking (go-kure/launcher#442).
#
# Checking is opt-in per fence. A fence is checked only when its info string
# carries a `check` attribute after the language, in the attribute-block form Hugo
# already parses and GitHub ignores:
#
#   ```yaml {check="build" profile="examples/cluster-profiles/minimal.yaml"}
#   ```yaml {check="snippet"}
#   ```yaml {check="template"}
#
# An unmarked fence is never checked, so adding a page or an example cannot fail
# this gate by accident. A marked fence must satisfy its declared mode:
#
#   build     a complete Application; `kurel build` must succeed against the
#             ClusterProfile named by `profile=` (a path relative to the repository
#             root, required). This is the only mode that runs the envelope and the
#             handlers, so it is the one that catches a defect like
#             go-kure/launcher#417 (`traits: []` at spec level).
#   snippet   a fragment illustrating part of a document (an envelope header, one
#             stanza); it must be well-formed YAML, and is not expected to build.
#   template  a package template whose `${...}` placeholders are unresolved by
#             construction; it must be well-formed YAML. Resolving it would need the
#             package's parameters and values, which the fence alone does not carry.
#
# "Well-formed" means yq parses it. yq accepts a duplicated mapping key, which
# kurel's parser rejects, so a snippet or template with one still passes.
#
# A marker that cannot be honoured is a failure, never a skip: an unknown mode, a
# marker on a non-YAML fence, a build fence without a profile or with a profile
# that does not exist, and a marked fence that is never closed. Each failure is
# reported as <file>:<line of the opening fence>: <reason>.
#
# Usage: bash site/scripts/check-doc-fences.sh [--root DIR] [FILE...]
#   With no FILE, every tracked *.md under DIR (default: the repository root) is
#   scanned. FILE and profile= paths are relative to DIR.
#   KUREL_BIN (default bin/kurel, relative to DIR) is needed only if a build fence
#   is present; yq is needed only if a snippet or template fence is present.
# (invoked via `make check-doc-fences`, which builds bin/kurel and runs the
#  self-test, site/scripts/check-doc-fences-test.sh, first)
#
# Exit: 0 every marked fence satisfies its mode; 1 at least one does not;
#       2 the scan itself could not run: a usage error, a missing FILE or tool,
#         git unable to list the files (or listing none), or fence extraction
#         failing.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ "${1:-}" == "--root" ]]; then
  [[ $# -ge 2 ]] || { echo "usage: $0 [--root DIR] [FILE...]" >&2; exit 2; }
  ROOT="$(cd "$2" && pwd)"
  shift 2
fi
cd "$ROOT"

KUREL_BIN="${KUREL_BIN:-bin/kurel}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

files=()
if [[ $# -gt 0 ]]; then
  files=("$@")
else
  # Listed into a file first: a git failure inside a process substitution would
  # not trip set -e, and the scan would pass over zero files.
  if ! git ls-files -z -- '*.md' >"$WORK/files" 2>"$WORK/git.err"; then
    echo "ERROR: git ls-files failed in $ROOT — cannot list the Markdown files to scan: $(cat "$WORK/git.err")" >&2
    exit 2
  fi
  while IFS= read -r -d '' f; do files+=("$f"); done <"$WORK/files"
  if [[ ${#files[@]} -eq 0 ]]; then
    echo "ERROR: no tracked *.md files under $ROOT — refusing to pass an empty scan" >&2
    exit 2
  fi
fi

# extract_fences FILE OUTDIR — writes each marked fence's body (de-indented by the
# opening fence's own indentation, as CommonMark does for a fence in a list item)
# to OUTDIR/<n>.yaml and prints one record per marked fence:
#   <n> TAB <line of opening fence> TAB closed|UNCLOSED TAB <info string>
# Fence rules follow CommonMark closely enough for documentation: a run of three or
# more backticks or tildes opens a fence; only a run of the same character, at
# least as long, alone on its line and indented at most three columns more than
# the opener, closes it; anything between — including a line that looks like
# another fence — is body. A backtick fence whose info string contains a backtick
# is not a fence. A trailing CR is ignored, and a tab counts as one column of
# indentation. The opener's context is not parsed: a fence-looking line inside a
# four-space indented code block is taken for a fence.
extract_fences() {
  awk -v outdir="$2" '
    function lead(s) { match(s, /^[ \t]*/); return RLENGTH }
    BEGIN { infence = 0; n = 0 }
    {
      line = $0
      sub(/\r$/, "", line)
      if (!infence) {
        if (!match(line, /^[ \t]*(````*|~~~~*)/)) next
        oplen = RLENGTH
        indent = lead(line)
        run = substr(line, indent + 1, oplen - indent)
        info = substr(line, oplen + 1)
        fchar = substr(run, 1, 1)
        if (fchar == "`" && index(info, "`")) next
        sub(/^[ \t]+/, "", info); sub(/[ \t]+$/, "", info)
        infence = 1; flen = length(run); start = NR
        marked = (info ~ /\{([^}]*[ \t,])?check=/)
        if (marked) { n++; path = outdir "/" n ".yaml"; printf "" > path }
        next
      }
      s = line
      sub(/^[ \t]*/, "", s); sub(/[ \t]*$/, "", s)
      t = s; gsub(fchar, "", t)
      if (s != "" && t == "" && length(s) >= flen && lead(line) - indent <= 3) {
        if (marked) { close(path); printf "%d\t%d\tclosed\t%s\n", n, start, info }
        infence = 0
        next
      }
      if (marked) {
        body = line
        for (i = 0; i < indent && substr(body, 1, 1) ~ /[ \t]/; i++) body = substr(body, 2)
        print body > path
      }
    }
    END {
      if (infence && marked) { close(path); printf "%d\t%d\tUNCLOSED\t%s\n", n, start, info }
    }
  ' "$1"
}

# attr NAME ATTRS — prints the value of NAME="value" or NAME=value in ATTRS.
attr() {
  local re_q="(^|[[:space:],{])$1=\"([^\"]*)\"" re_u="(^|[[:space:],{])$1=([^[:space:],}\"]+)"
  if [[ "$2" =~ $re_q ]] || [[ "$2" =~ $re_u ]]; then
    printf '%s' "${BASH_REMATCH[2]}"
  fi
}

need_kurel() {
  if [[ ! -x "$KUREL_BIN" ]]; then
    echo "ERROR: $KUREL_BIN not found or not executable — run 'make build-kurel' first" >&2
    exit 2
  fi
}
need_yq() {
  if ! command -v yq >/dev/null; then
    echo "ERROR: yq not found — install via mise (see mise.toml)" >&2
    exit 2
  fi
}

failures=0
declare -A count=([build]=0 [snippet]=0 [template]=0)
report() { echo "$1:$2: $3" >&2; failures=$((failures + 1)); }
# oneline ERR BODY — the tool's message on one line, with the temp path of the
# extracted body replaced by "<fence>" (line numbers in it count from the fence's
# first body line, not from the top of the Markdown file).
oneline() { local e="${1//"$2"/<fence>}"; printf '%s' "${e//$'\n'/ }"; }

fid=0
for f in "${files[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: $f: no such file under $ROOT" >&2
    exit 2
  fi
  fid=$((fid + 1))
  out="$WORK/$fid"
  mkdir -p "$out"
  # Records go to a file first, for the same reason as the file list above: an
  # extractor failure inside a process substitution would read as a clean file.
  if ! extract_fences "$f" "$out" >"$out/records"; then
    echo "ERROR: $f: extracting fences failed" >&2
    exit 2
  fi
  while IFS=$'\t' read -r n line state info; do
    body="$out/$n.yaml"
    lang="${info%%[[:space:]\{]*}"
    attrs=""
    [[ "$info" == *"{"* ]] && attrs="{${info#*\{}"
    mode="$(attr check "$attrs")"

    if [[ "$state" == "UNCLOSED" ]]; then
      report "$f" "$line" "unclosed marked fence"
      continue
    fi
    if [[ "$lang" != "yaml" && "$lang" != "yml" ]]; then
      report "$f" "$line" "check marker on a '${lang:-unlabelled}' fence; only yaml fences are checked"
      continue
    fi

    case "$mode" in
      build)
        profile="$(attr profile "$attrs")"
        if [[ -z "$profile" ]]; then
          report "$f" "$line" "build mode needs profile=<path to a ClusterProfile>"
          continue
        fi
        if [[ ! -f "$profile" ]]; then
          report "$f" "$line" "profile not found: $profile"
          continue
        fi
        need_kurel
        count[build]=$((count[build] + 1))
        if ! err="$("$KUREL_BIN" build "$body" --profile "$profile" 2>&1 >/dev/null)"; then
          report "$f" "$line" "kurel build failed against $profile: $(oneline "$err" "$body")"
        fi
        ;;
      snippet | template)
        need_yq
        count[$mode]=$((count[$mode] + 1))
        if ! err="$(yq eval '.' "$body" 2>&1 >/dev/null)"; then
          report "$f" "$line" "$mode is not well-formed YAML: $(oneline "$err" "$body")"
        fi
        ;;
      *)
        report "$f" "$line" "unknown check mode '${mode}'; want build, snippet or template"
        ;;
    esac
  done <"$out/records"
done

echo "check-doc-fences: ${#files[@]} file(s); checked build=${count[build]} snippet=${count[snippet]} template=${count[template]}; $failures failure(s)"
[[ "$failures" -eq 0 ]] || exit 1
