# forbidden-terms-lib.sh — the forbidden-terms guard's own matcher, for the
# scripts that write files the guard scans (go-kure/launcher#1119).
#
# Sourced, not run. ft_load reads TERMS_RE and the line_words function out of
# the vendored guard (site/scripts/check-forbidden-terms.sh), so that a script
# finds exactly the lines the guard refuses, whole words and identifier
# segments alike, and names no term itself. A guard re-synced with new shapes
# reaches every script through this file, with nothing restated.
#
# Usage:
#   source "$REPO_ROOT/scripts/forbidden-terms-lib.sh"
#   ft_load "$REPO_ROOT/site/scripts/check-forbidden-terms.sh"
#   ft_hits FILE               # "<line>\t<word> [<word>...]" per line the guard reports
#   ft_add_pragmas IN OUT      # IN with an allow-term pragma line above each such line

# ft_load GUARD: defines TERMS_RE and line_words as the guard does.
ft_load() {
  local guard="$1" def
  TERMS_RE="$(sed -n "s/^TERMS_RE='\([a-z|]*\)'$/\1/p" "$guard")"
  if [[ -z "$TERMS_RE" ]]; then
    echo "ERROR: no TERMS_RE line in $guard" >&2
    return 1
  fi
  def="$(sed -n '/^line_words() {$/,/^}$/p' "$guard")"
  if [[ -z "$def" ]]; then
    echo "ERROR: no line_words function in $guard" >&2
    return 1
  fi
  eval "$def"
}

# FT_LOCALES: the locales a line is matched in. The guard's grep classes
# ([[:lower:]], [[:alnum:]], \b) are ASCII under C and Unicode under C.UTF-8, so
# a term next to a non-ASCII letter can be a hit in one and not the other; the
# guard runs in whatever locale its runner sets. A line is a hit when it is one
# in either.
FT_LOCALES=(C C.UTF-8)

# ft_hits FILE: one "<line number>\t<words>" line for each line of FILE that
# names a term in a shape the guard matches, in any of FT_LOCALES, its words
# lowercase and space-separated in TERMS_RE order. A line holding a term only
# inside a longer word (one the guard does not match) is no hit.
ft_hits() {
  local file="$1" line ln text loc found w words
  { LC_ALL=C grep -niE "$TERMS_RE" "$file" || true; } | while IFS= read -r line; do
    ln="${line%%:*}"
    text="${line#*:}"
    found=" "
    for loc in "${FT_LOCALES[@]}"; do
      found+="$(LC_ALL="$loc" line_words "$text") "
    done
    words=""
    for w in ${TERMS_RE//|/ }; do
      if [[ "$found" == *" $w "* ]]; then
        words+="$w "
      fi
    done
    if [[ -n "$words" ]]; then
      printf '%s\t%s\n' "$ln" "${words% }"
    fi
  done
}

# ft_add_pragmas IN OUT: writes IN to OUT with an allow-term pragma comment line
# ("# allow-term:<word> ...", indented as the line below it) above each line
# ft_hits reports. The guard accepts a pragma on the line above a hit.
ft_add_pragmas() {
  local in="$1" out="$2" hits
  hits="$(mktemp)"
  ft_hits "$in" >"$hits"
  # FILENAME, not FNR == NR: with no hit the first file is empty, and FNR == NR
  # would then hold for IN's lines too.
  awk -F'\t' '
    FILENAME == ARGV[1] { w = $2; gsub(/ /, " allow-term:", w); tag[$1] = "allow-term:" w; next }
    (FNR in tag) { match($0, /^ */); print substr($0, 1, RLENGTH) "# " tag[FNR] }
    { print }' "$hits" "$in" >"$out"
  rm -f "$hits"
}
