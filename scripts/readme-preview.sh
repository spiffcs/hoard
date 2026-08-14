#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

SRC="${1:-README.md}"
OUT=.tmp/readme-preview.html
REPO_ROOT="$PWD"

[ -f "$SRC" ] || { echo "readme-preview: $SRC does not exist"; exit 1; }
command -v gh >/dev/null || { echo "readme-preview: needs the gh CLI (brew install gh)"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "readme-preview: gh is not logged in — run 'gh auth login'"; exit 1; }

BODY=$(gh api --method POST /markdown \
  -f mode=gfm \
  -f context=spiffcs/hoard \
  -f text="$(cat "$SRC")")

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
