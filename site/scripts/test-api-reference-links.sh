#!/usr/bin/env bash
# Exercise the API-reference render hook with the same Hugo module and theme as the site.
set -euo pipefail

site_dir="$(cd "$(dirname "$0")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
cd "$fixture"
cp "$site_dir"/{hugo.toml,go.mod,go.sum} .
mkdir -p layouts/api-reference content/api-reference/overview
cp -R "$site_dir/layouts/api-reference/_markup" layouts/api-reference/
cat > docs-map.yaml <<'YAML'
packages:
  - readme: pkg/example/README.md
    mount: {target: api-reference/overview/_index.md}
YAML
printf '%s\n' '---' 'title: API reference' '---' > content/api-reference/_index.md
printf '%s\n' '---' 'title: Part' '---' '## Details' > content/api-reference/part.md
cat > content/api-reference/overview/_index.md <<'MD'
---
title: Overview
---
## Overview anchor

[Child](/site/content/api-reference/part.md?view=all#details)
[Mounted README](/pkg/example/README.md#overview-anchor)
[External](https://example.org/launcher/api-reference/part/)
MD

origin=https://www.gokure.dev
for base in /launcher/dev/ /launcher/v0.2/ /launcher/; do
    hugo --baseURL "$origin$base" --quiet
    page=public/api-reference/overview/index.html
    for href in "${base}api-reference/part/index.html?view=all#details" \
                "${base}api-reference/overview/index.html#overview-anchor" \
                'https://example.org/launcher/api-reference/part/'; do
        grep -Fq "href=\"$href\"" "$page" || { echo "Missing $href in $page" >&2; exit 1; }
    done
done

# Each bad link must fail for its own reason, even when it has no fragment.
expect_error() {
    printf '%s\n' '---' 'title: Broken' '---' "[Broken]($1)" > content/api-reference/broken.md
    if hugo --baseURL "${3:-$origin/launcher/dev/}" > build.log 2>&1; then
        echo "Build accepted $1" >&2; exit 1
    fi
    grep -Fq "$2" build.log || { cat build.log >&2; exit 1; }
}
expect_error "$origin/launcher/api-reference/part/" 'hard-codes a published API reference'
expect_error '/launcher/api-reference/part/#details' 'hard-codes a published API reference'
expect_error '/launcher/v0.1/api-reference/part/' 'hard-codes a published API reference'
expect_error '/site/content/api-reference/missing.md' 'has no published page'
expect_error '/pkg/unmounted/README.md' 'has no published page'
# A preview build under another hostname still rejects the published host.
expect_error "$origin/launcher/api-reference/part/" 'hard-codes a published API reference' http://localhost:1313/launcher/dev/
echo 'API-reference links passed for dev, versioned release and latest, including six rejected targets.'
