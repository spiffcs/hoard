#!/usr/bin/env bash
# fetch-scan-fixtures.sh — download the scanner's fixture frames.
#
#   make scan-fixtures
#
# The 28 frames the scanner's regression sweep replays are ~58MB of 1920x1080
# camera stills. They are not in git, and that is deliberate: they are read by
# exactly one thing — scan/fixtures/sweep.sh, via `make scan-check`, which needs
# macOS, Xcode and a Swift build — and CI never runs it. Carrying them in the
# repository charged every contributor 58MB on clone for a check almost none of
# them would run.
#
# What stays in git is the half that is actually the test: the goldens. They are
# 112KB of JSON, they diff readably in review, and they are the assertions. Only
# the inputs moved.
#
# They live on ghcr.io as an OCI artifact, pinned by manifest digest in
# scan/fixtures/frames.oci. That choice is about integrity rather than storage:
# in a registry the digest *is* the address, so a pull cannot return bytes other
# than the ones recorded — the guarantee is structural instead of something this
# script asserts with a checksum of its own. It also keeps the fixtures out of
# GitHub Releases, which exists for releases; an archive published there would
# have to be marked prerelease forever, because install.sh resolves hoard's
# version through /releases/latest and would otherwise try to install the
# fixtures.

set -euo pipefail
cd "$(dirname "$0")/.."

DIR=scan/fixtures/frames
MANIFEST=scan/fixtures/frames.oci

[ -f "$MANIFEST" ] || { echo "missing $MANIFEST" >&2; exit 1; }
REF=$(awk '/^ref:/{print $2}' "$MANIFEST")
DIGEST=$(awk '/^digest:/{print $2}' "$MANIFEST")
[ -n "$REF" ] && [ -n "$DIGEST" ] || { echo "$MANIFEST is incomplete" >&2; exit 1; }

# Deliberately NOT pinned in .binny.yaml. That toolchain is bootstrapped by every
# CI job including the release, and oras is needed by exactly one darwin-gated,
# hand-run task that CI never executes — putting it there made an intermittent
# Linux install failure able to break a release. `make scan-check` already needs
# macOS, Xcode and a Swift toolchain, so one brew install is proportionate.
ORAS=".tool/oras"
[ -x "$ORAS" ] || ORAS=$(command -v oras || true)
[ -n "$ORAS" ] || { echo "oras not found — run: brew install oras" >&2; exit 1; }

# Already correct? Say so and stop. This runs as a dependency of scan-check, so
# it has to be cheap and quiet on the common path.
if [ -f "$DIR/.stamp" ] && [ "$(cat "$DIR/.stamp")" = "$DIGEST" ]; then
  echo "scan fixtures present ($(ls "$DIR"/*.png 2>/dev/null | wc -l | tr -d ' ') frames)"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fetching $REF@${DIGEST:0:19}… (~58MB, once)"
# oras verifies the digest of everything it pulls, so a corrupted or substituted
# layer fails here rather than being extracted. If the registry answers at all,
# it answers with these exact bytes.
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

# Every golden must have a frame to replay. A golden without one would otherwise
# be skipped in silence by the sweep's glob, and a check that quietly covers less
# than it did is the failure this arrangement must not introduce.
missing=0
for g in scan/fixtures/*.golden.json; do
  name=$(basename "$g" .golden.json)
  [ -f "$DIR/$name.png" ] || { echo "no frame for golden $name" >&2; missing=1; }
done
[ "$missing" = 0 ] || { echo "artifact does not cover every golden" >&2; exit 1; }

printf '%s' "$DIGEST" >"$DIR/.stamp"
echo "extracted $(ls "$DIR"/*.png | wc -l | tr -d ' ') frames to $DIR"
