#!/usr/bin/env bash
# build-scan-ios.sh — build the iPhone capture head.
#
# The sibling of build-scan.sh, and the same shape: generate what Xcode needs,
# build it, say where it went. The difference is signing. The macOS helper is
# ad-hoc signed (`codesign --sign -`) because TCC only needs a stable identity;
# an iOS app cannot be installed on a device that way at all, so this needs a
# real team and a provisioning profile.
#
#   make scan-ios            build for a device
#   make scan-ios-install    build and install onto an attached iPhone
#
# The team identifier lives in scan/hoard-scan-ios/Signing.xcconfig, which is
# gitignored. This script writes a template on first run and tells you what to
# put in it.
set -euo pipefail

root=$(cd "$(dirname "$0")" && pwd)
proj_dir="$root/scan/hoard-scan-ios"
xcconfig="$proj_dir/Signing.xcconfig"
install=false
[ "${1:-}" = "--install" ] && install=true

if ! command -v xcodegen >/dev/null 2>&1; then
    cat >&2 <<'EOF'
xcodegen not found. The Xcode project is generated from project.yml rather than
checked in, so it is needed to build:

    brew install xcodegen

(If you would rather not add the dependency, `xcodegen generate` once elsewhere
and commit the .xcodeproj — but then project.yml stops being the source of
truth, so pick one.)
EOF
    exit 2
fi

if [ ! -f "$xcconfig" ]; then
    team=$(security find-identity -v -p codesigning 2>/dev/null \
        | sed -n 's/.*(\([A-Z0-9]\{10\}\))"$/\1/p' | head -1)
    cat >"$xcconfig" <<EOF
// Local signing identity. Gitignored — a team identifier is account data, not
// project configuration, and it should not travel with the repo.
DEVELOPMENT_TEAM = ${team:-YOUR_TEAM_ID}
EOF
    echo "wrote $xcconfig${team:+ (team $team, guessed from your keychain)}"
    [ -z "$team" ] && {
        echo "  no signing identity found — put your team ID in that file" >&2
        exit 2
    }
fi

# The attached device's hardware UDID, read from devicectl's JSON.
#
# Not scraped from the table, for two reasons. The columns hold space-bearing
# values — the device name, the state ("connected (no DDI)"), the model
# ("iPhone 16 (iPhone17,3)") — so field counting picks up whatever lands there.
# And the only identifier the table prints is the CoreDevice UUID
# (8-4-4-4-12), which is not what -destination wants: that needs the hardware
# UDID, an 8-16 hex pair that appears in the JSON only. Matching "something
# UUID-shaped" finds the wrong one and fails with "unable to find a device".
# devicectl insists on a real file for --json-output; /dev/stdout is refused.
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

echo "Generating the Xcode project…"
(cd "$proj_dir" && xcodegen generate --quiet)

echo "Building HoardScan…"
build_args=(
    -project "$proj_dir/HoardScan.xcodeproj"
    -scheme HoardScan
    -configuration Debug
    -allowProvisioningUpdates
    -derivedDataPath "$proj_dir/.build"
)
# Naming the actual device rather than 'generic/platform=iOS' is worth the extra
# lookup: a generic destination has no device to check, so an unregistered phone
# reports as the useless "your team has no devices from which to generate a
# provisioning profile", while a specific one says which device and why.
if [ -n "$device" ]; then
    build_args+=(-destination "platform=iOS,id=$device")
else
    build_args+=(-destination 'generic/platform=iOS')
fi
# Every failure here reports as "No profiles for <bundle id> were found", which
# is true and useless — the cause is always one line above it and says something
# else. These are the ones seen so far, in the order they were hit.
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
    # devicectl replaces ios-deploy on modern Xcode. It needs the phone
    # unlocked, trusted, and with Developer Mode on (Settings › Privacy &
    # Security › Developer Mode) — that last one is a one-time reboot.
    echo "Installing to the attached device…"
    # Pull the identifier by shape, not by column. devicectl's table has
    # space-bearing values in almost every column — the device name, the state
    # ("connected (no DDI)") and the model ("iPhone 16 (iPhone17,3)") — so
    # counting fields picks up whatever happens to land there.
    device=$(xcrun devicectl list devices 2>/dev/null \
        | grep -oE '[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}' \
        | head -1)
    if [ -z "$device" ]; then
        echo "no connected device found — plug in an unlocked iPhone" >&2
        exit 2
    fi
    xcrun devicectl device install app --device "$device" "$app"
fi
