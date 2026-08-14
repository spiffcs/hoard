#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

DIR=scan/fixtures/frames
MANIFEST=scan/fixtures/frames.oci

[ -f "$MANIFEST" ] || { echo "missing $MANIFEST" >&2; exit 1; }
REF=$(awk '/^ref:/{print $2}' "$MANIFEST")
DIGEST=$(awk '/^digest:/{print $2}' "$MANIFEST")
[ -n "$REF" ] && [ -n "$DIGEST" ] || { echo "$MANIFEST is incomplete" >&2; exit 1; }

ORAS=".tool/oras"
[ -x "$ORAS" ] || ORAS=$(command -v oras || true)
[ -n "$ORAS" ] || { echo "oras not found — run: brew install oras" >&2; exit 1; }

if [ -f "$DIR/.stamp" ] && [ "$(cat "$DIR/.stamp")" = "$DIGEST" ]; then
  echo "scan fixtures present ($(ls "$DIR"/*.png 2>/dev/null | wc -l | tr -d ' ') frames)"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fetching $REF@${DIGEST:0:19}… (~58MB, once)"
"$ORAS" pull "$REF@$DIGEST" --output "$tmp" 2>&1 | sed 's/^/  /' || {
  echo "pull failed. If the package is private, authenticate:" >&2
  echo "  gh auth token | oras login ghcr.io -u <you> --password-stdin" >&2
  exit 1
}

archive="$tmp/scan-fixtures.tar.gz"
[ -f "$archive" ] || { echo "artifact did not contain scan-fixtures.tar.gz" >&2; exit 1; }

rm -rf "$DIR"
mkdir -p "$DIR"
tar -xzf "$archive" -C "$DIR"

missing=0
for g in scan/fixtures/*.golden.json; do
  name=$(basename "$g" .golden.json)
  [ -f "$DIR/$name.png" ] || { echo "no frame for golden $name" >&2; missing=1; }
done
[ "$missing" = 0 ] || { echo "artifact does not cover every golden" >&2; exit 1; }

printf '%s' "$DIGEST" >"$DIR/.stamp"
echo "extracted $(ls "$DIR"/*.png | wc -l | tr -d ' ') frames to $DIR"
