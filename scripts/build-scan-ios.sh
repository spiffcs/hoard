#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scan-ios-common.sh
source "$(cd "$(dirname "$0")" && pwd)/scan-ios-common.sh"

proj_dir="$scan_ios_proj_dir"
install=false
[ "${1:-}" = "--install" ] && install=true

ios_require_xcodegen
ios_ensure_signing_xcconfig

devjson=$(mktemp -t hoard-devicectl)
trap 'rm -f "$devjson"' EXIT
xcrun devicectl list devices --json-output "$devjson" >/dev/null 2>&1 || true
device=$(python3 -c 'import json,sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    raise SystemExit
for dev in d.get("result", {}).get("devices", []):
    udid = dev.get("hardwareProperties", {}).get("udid")
    if udid:
        print(udid)
        break
' "$devjson" 2>/dev/null)

ios_generate_project

echo "Building HoardScan…"
stamp="$(ios_build_stamp)"
echo "Build stamp: $stamp (git $(git rev-parse --short HEAD 2>/dev/null || echo '?')$(git diff --quiet 2>/dev/null || echo '-dirty'))"
build_args=(
    -project "$proj_dir/HoardScan.xcodeproj"
    -scheme HoardScan
    -configuration Debug
    -allowProvisioningUpdates
    -derivedDataPath "$proj_dir/.build"
    CURRENT_PROJECT_VERSION="$stamp"
)
if [ -n "$device" ]; then
    build_args+=(-destination "platform=iOS,id=$device")
else
    build_args+=(-destination 'generic/platform=iOS')
fi
if ! xcodebuild "${build_args[@]}" build; then
    cat >&2 <<'EOF'

"No profiles were found" is the symptom, not the cause. Look at the error
line above it; so far it has always been one of these:

  * "No Accounts" — Xcode is not signed into the Apple Developer account that
    owns the team in Signing.xcconfig. Xcode › Settings › Accounts.

  * "PLA Update available" — Apple has revised the Program License Agreement
    and gates provisioning until it is accepted. There is nothing to fix
    locally: sign in at developer.apple.com/account and accept the banner.

  * "isn't registered in your developer account" — the phone is attached and
    trusted but its UDID is not on the team. -allowProvisioningUpdates does not
    reliably add it from the command line; register it once at
    developer.apple.com/account under Devices, or open the generated project in
    Xcode.app and pick the phone as the run destination. Its UDID:
        xcrun devicectl list devices

  * A destination error — the iOS platform payload is not installed. Xcode
    lists the SDK before the payload is downloaded.
        xcodebuild -downloadPlatform iOS

To check that the code itself still compiles while any of the above is being
sorted out, build without signing:

    xcodebuild -project scan/hoard-scan-ios/HoardScan.xcodeproj -scheme HoardScan \
        -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
EOF
    exit 1
fi

app="$proj_dir/.build/Build/Products/Debug-iphoneos/HoardScan.app"
echo "Built $app"

if $install; then
    echo "Installing to the attached device…"
    device=$(xcrun devicectl list devices 2>/dev/null \
        | grep -oE '[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}' \
        | head -1)
    if [ -z "$device" ]; then
        echo "no connected device found — plug in an unlocked iPhone" >&2
        exit 2
    fi
    xcrun devicectl device install app --device "$device" "$app"
fi
