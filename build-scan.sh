#!/usr/bin/env bash
# Build the macOS Swift camera-scan helper into bin/hoard-scan.app.
# Requires Xcode's Swift toolchain (swiftc). No-op meaning: the Go build does NOT
# produce this — run `./build-scan.sh` (or `make scan`) on macOS to enable the
# `ctrl+o` scan feature in `hoard add`.
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
	echo "hoard-scan is macOS-only; skipping." >&2
	exit 0
fi

ROOT="$(cd "$(dirname "$0")" && pwd)"
SRC="$ROOT/scan/hoard-scan/main.swift"
PLIST="$ROOT/scan/hoard-scan/Info.plist"
APP="$ROOT/bin/hoard-scan.app"
MACOS="$APP/Contents/MacOS"

rm -rf "$APP"
mkdir -p "$MACOS"
cp "$PLIST" "$APP/Contents/Info.plist"

echo "Compiling hoard-scan…" >&2
swiftc -O \
	-framework AVFoundation -framework AppKit -framework Vision \
	-o "$MACOS/hoard-scan" \
	"$SRC"

# Ad-hoc sign so the camera (TCC) permission prompt is attributed to the app.
codesign --force --sign - --identifier dev.cphillips918.hoard.scan "$APP" 2>/dev/null || \
	echo "warning: codesign failed (unsigned build); camera permission may prompt oddly." >&2

echo "Built $APP" >&2
