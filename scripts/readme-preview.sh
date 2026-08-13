#!/usr/bin/env bash
# readme-preview.sh — see a Markdown file the way GitHub will draw it, before
# pushing it.
#
#   make readme-preview                        # README.md
#   task readme-preview -- CONTRIBUTING.md     # or any other file
#
# The second form is `task`, not `make`, on purpose. The Makefile shim forwards
# the target name and nothing else, so extra words on a `make` line are read as
# further make goals: `make readme-preview -- CONTRIBUTING.md` previews the
# README, says "Wrote ...", and exits 0. Measured. It is wrong silently, which is
# why the working spelling is the one written down.
#
# The reason this posts to GitHub's own /markdown endpoint rather than using a
# local renderer: GFM is not CommonMark, and the part that matters here is the
# sanitizer. The README opens with raw HTML — <p align="center"> around an <img>
# carrying a width — and whether those attributes survive is exactly what a local
# renderer guesses at. GitHub's answer is the only one worth previewing, and it
# is not obvious: it silently appends `style="max-width: 100%;"` to every image.
#
# Two things this is faithful about and one it is not:
#
#   - the content column is pinned to GitHub's 1012px. A fixed-pixel logo never
#     scales, so what the eye actually judges is its ratio against the text
#     beside it, and a wider preview pane makes the same image look bigger.
#   - relative image paths resolve against the repo via <base>, so the local
#     PNGs are what you see — no push, no committed asset, no CDN round trip.
#   - autolinks are NOT faithful. mode=gfm is needed for tables and strikethrough
#     (this README has tables), but it also turns bare #123 and @name into links,
#     which a repo README does not do. Layout is right; treat those as noise.
#
# Output goes to .tmp/, which is gitignored. Nothing here writes to docs/.

set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${1:-README.md}"
OUT=.tmp/readme-preview.html
REPO_ROOT="$PWD"

# Check the inputs, because the failures here are all quiet ones. A missing file
# would otherwise be posted as the empty string and come back as a valid, empty,
# exit-0 render; an expired token comes back as a JSON error document that is
# also perfectly good HTML to write to disk.
[ -f "$SRC" ] || { echo "readme-preview: $SRC does not exist"; exit 1; }
command -v gh >/dev/null || { echo "readme-preview: needs the gh CLI (brew install gh)"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "readme-preview: gh is not logged in — run 'gh auth login'"; exit 1; }

# context= is what makes issue and PR references resolve against this repo
# rather than being left as text.
BODY=$(gh api --method POST /markdown \
  -f mode=gfm \
  -f context=spiffcs/hoard \
  -f text="$(cat "$SRC")")

# The artifact is the only evidence. gh exits 0 on an empty body, and the wrapper
# below would dress that up into a page that opens, looks deliberate, and shows
# nothing — which reads as "the README is fine" rather than "the render failed".
# A real render of the smallest doc here is several KB; 200 bytes is clear of
# both that and of anything an empty response produces.
[ "${#BODY}" -gt 200 ] || { echo "readme-preview: render came back empty — is the API reachable?"; exit 1; }

mkdir -p .tmp
cat > "$OUT" <<HTML
<!doctype html>
<html><head>
<meta charset="utf-8">
<title>$SRC — GitHub preview</title>
<base href="file://$REPO_ROOT/">
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/github-markdown-css@5/github-markdown.css">
<style>
  body { margin: 0; background: #fff; }
  .page { max-width: 1012px; margin: 0 auto; padding: 16px; }
  .markdown-body { border: 1px solid #d1d9e0; border-radius: 6px; padding: 32px; }
  @media (prefers-color-scheme: dark) {
    body { background: #0d1117; } .markdown-body { border-color: #3d444d; }
  }
</style>
</head><body><div class="page"><article class="markdown-body">
$BODY
</article></div></body></html>
HTML

echo "Wrote $OUT — rerun after an edit, then reload the tab"
open "$OUT"
