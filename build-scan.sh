#!/usr/bin/env bash
# Build the macOS Swift scan helper into bin/hoard-scan.app.
# Requires Xcode's Swift toolchain. No-op meaning: the Go build does NOT produce
# this — run `./build-scan.sh` (or `make scan`) on macOS to enable the `ctrl+o`
# scan feature in `hoard add`.
#
# The helper owns no camera. It is the Mac end of the link to the iPhone running
# Hoardling (scan/hoard-scan-ios), which is what actually captures and
# reads the card; see docs/ios-development.md to build that side.
#
# The helper is a SwiftPM package (scan/hoard-scan/Package.swift), so this script
# builds the executable and then assembles the .app around it by hand. SwiftPM
# has no notion of an app bundle, and the bundle is not cosmetic: TCC attributes
# the local-network permission the Bonjour link needs to the bundle's signed
# identity, so a bare executable would prompt oddly or not at all.
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
	echo "hoard-scan is macOS-only; skipping." >&2
	exit 0
fi

ROOT="$(cd "$(dirname "$0")" && pwd)"
PKG="$ROOT/scan/hoard-scan"
PLIST="$ROOT/scan/hoard-scan/Info.plist"
ICON="$ROOT/scan/hoard-scan/hoard-scan.icns"
APP="$ROOT/bin/hoard-scan.app"
MACOS="$APP/Contents/MacOS"
RESOURCES="$APP/Contents/Resources"

rm -rf "$APP"
mkdir -p "$MACOS" "$RESOURCES"
cp "$PLIST" "$APP/Contents/Info.plist"
# A fresh CFBundleVersion every build — the helper logs it at startup, so the
# session log proves which helper build ran (see the matching stamp in
# build-scan-ios.sh).
plutil -replace CFBundleVersion -string "$(date +%y%m%d.%H%M.%S)" "$APP/Contents/Info.plist"

# The Dock icon. Optional: without it the app still runs, just with the generic
# executable icon (CFBundleIconFile simply finds nothing).
if [[ -f "$ICON" ]]; then
	cp "$ICON" "$RESOURCES/hoard-scan.icns"
fi

echo "Compiling hoard-scan…" >&2
swift build --package-path "$PKG" -c release
cp "$(swift build --package-path "$PKG" -c release --show-bin-path)/hoard-scan" "$MACOS/hoard-scan"

# Ad-hoc sign so the local-network (TCC) permission prompt is attributed to the app.
codesign --force --sign - --identifier dev.spiffcs.hoard.scan "$APP" 2>/dev/null || \
	echo "warning: codesign failed (unsigned build); network permission may prompt oddly." >&2

echo "Built $APP" >&2
