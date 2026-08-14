#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scan-ios-common.sh
source "$(cd "$(dirname "$0")" && pwd)/scan-ios-common.sh"

root="$scan_ios_root"
proj_dir="$scan_ios_proj_dir"
action=archive

while [ $# -gt 0 ]; do
    case "$1" in
        --upload)   action=upload ;;
        --validate) action=validate ;;
        -h|--help)
            sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
            exit 0 ;;
        *)
            echo "unknown option: $1 (try --help)" >&2
            exit 2 ;;
    esac
    shift
done

out_dir="$proj_dir/.build/release"
archive_path="$out_dir/HoardScan.xcarchive"
export_dir="$out_dir/export"
opts_template="$proj_dir/ExportOptions-AppStore.plist"
derived_data="${HOARD_IOS_DERIVED_DATA:-$proj_dir/.build}"

asc_auth_args=()
asc_auth_desc=""

resolve_asc_auth() {
    local key_id="${HOARD_ASC_KEY_ID:-}" issuer="${HOARD_ASC_ISSUER_ID:-}"

    if [ -n "$key_id" ] && [ -n "$issuer" ]; then
        asc_auth_args=(--api-key "$key_id" --api-issuer "$issuer")
        if [ -n "${HOARD_ASC_KEY_PATH:-}" ]; then
            if [ ! -f "$HOARD_ASC_KEY_PATH" ]; then
                echo "HOARD_ASC_KEY_PATH is set but $HOARD_ASC_KEY_PATH does not exist" >&2
                exit 2
            fi
            asc_auth_args+=(--p8-file-path "$HOARD_ASC_KEY_PATH")
            asc_auth_desc="API key $key_id ($HOARD_ASC_KEY_PATH)"
        else
            local d found=""
            for d in "$PWD/private_keys" "$HOME/private_keys" "$HOME/.private_keys" \
                     "$HOME/.appstoreconnect/private_keys"; do
                [ -f "$d/AuthKey_$key_id.p8" ] && { found="$d/AuthKey_$key_id.p8"; break; }
            done
            if [ -z "$found" ]; then
                cat >&2 <<EOF

HOARD_ASC_KEY_ID is set to $key_id but AuthKey_$key_id.p8 is nowhere altool
looks. Put it in one of these, or set HOARD_ASC_KEY_PATH to wherever it is:

    ./private_keys/AuthKey_$key_id.p8
    ~/private_keys/AuthKey_$key_id.p8
    ~/.private_keys/AuthKey_$key_id.p8
    ~/.appstoreconnect/private_keys/AuthKey_$key_id.p8   <- the conventional home

The .p8 downloads exactly once, at the moment you create the key in App Store
Connect. There is no second chance: lose it and the key is revoked and a new
one issued. It is also a credential — chmod 600 it and keep it out of the repo
(nothing in .gitignore covers *.p8 by name today).
EOF
                exit 2
            fi
            asc_auth_desc="API key $key_id ($found)"
        fi
        return 0
    fi

    if [ -n "${HOARD_ASC_APPLE_ID:-}" ] && [ -n "${HOARD_ASC_PASSWORD:-}" ]; then
        asc_auth_args=(-u "$HOARD_ASC_APPLE_ID" -p "@env:HOARD_ASC_PASSWORD")
        asc_auth_desc="app-specific password for $HOARD_ASC_APPLE_ID"
        return 0
    fi

    return 1
}

explain_missing_credentials() {
    cat >&2 <<'EOF'

No App Store Connect credentials found, so there is nothing to upload with.
Both of these work; the first is the one to set up.

  1. An App Store Connect API key (preferred — no 2FA, no expiry, revocable,
     and the only one that works unattended)

     appstoreconnect.apple.com › Users and Access › Integrations › App Store
     Connect API › Team Keys › +

     Give it the "App Manager" role — "Developer" cannot upload builds. You
     get three things, and you get the third one ONCE:

         Key ID       10 characters, shown in the table
         Issuer ID    a UUID, shown once above the table for the whole team
         AuthKey_<KEYID>.p8   downloads on creation and never again

     Then:

         mkdir -p ~/.appstoreconnect/private_keys
         mv ~/Downloads/AuthKey_XXXXXXXXXX.p8 ~/.appstoreconnect/private_keys/
         chmod 600 ~/.appstoreconnect/private_keys/AuthKey_XXXXXXXXXX.p8

         export HOARD_ASC_KEY_ID=XXXXXXXXXX
         export HOARD_ASC_ISSUER_ID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

     HOARD_ASC_KEY_PATH overrides the search if you keep the .p8 elsewhere.

  2. An app-specific password (works, but it is your Apple ID)

     appleid.apple.com › Sign-In and Security › App-Specific Passwords › +

         export HOARD_ASC_APPLE_ID=you@example.com
         export HOARD_ASC_PASSWORD=abcd-efgh-ijkl-mnop

     Prefer stashing it in the keychain over an environment variable:

         xcrun altool --store-password-in-keychain-item HOARD_ASC \
             -u you@example.com -p abcd-efgh-ijkl-mnop

     and then pass `-p @keychain:HOARD_ASC` by hand — this script only wires up
     the @env: form, deliberately, because a keychain item name is machine
     state and guessing at one would be worse than making you type it.

None of these go in a tracked file. They are account credentials, the same
reason DEVELOPMENT_TEAM lives in the gitignored Signing.xcconfig.

Nothing was built. This check runs before the archive on purpose — the archive
takes minutes, this takes none, and being told after a four-minute build that
there was never anything to upload with is how a script gets abandoned. Run
`make scan-ios-release` if you want the .ipa without the upload.
EOF
}

ios_require_xcodegen
ios_ensure_signing_xcconfig

team=$(ios_team_id)
if [ -z "$team" ]; then
    cat >&2 <<EOF
No development team. Put one in $scan_ios_xcconfig:

    DEVELOPMENT_TEAM = ABCDE12345

or set HOARD_IOS_TEAM in the environment. It is the ten-character identifier in
the parentheses of your signing identity:

    security find-identity -v -p codesigning
EOF
    exit 2
fi

if [ "$action" != archive ]; then
    resolve_asc_auth || { explain_missing_credentials; exit 2; }
fi

ios_generate_project

stamp=$(ios_build_stamp)
rev=$(git -C "$root" rev-parse --short HEAD 2>/dev/null || echo '?')
dirty=""
git -C "$root" diff --quiet 2>/dev/null || dirty="-dirty"
echo "Build stamp: $stamp (git $rev$dirty, team $team)"
if [ -n "$dirty" ] && [ "$action" = upload ]; then
    echo "warning: uploading from a dirty working tree — this build is not reproducible from a commit" >&2
fi

rm -rf "$archive_path" "$export_dir"
mkdir -p "$out_dir"

echo "Archiving HoardScan (Release)…"
if ! xcodebuild archive \
    -project "$proj_dir/HoardScan.xcodeproj" \
    -scheme HoardScan \
    -configuration Release \
    -destination 'generic/platform=iOS' \
    -archivePath "$archive_path" \
    -derivedDataPath "$derived_data" \
    -allowProvisioningUpdates \
    CURRENT_PROJECT_VERSION="$stamp"; then
    cat >&2 <<EOF

The archive failed. Read the error above this block first; a Release archive
fails for different reasons than the Debug build does, and the ones seen so far
are all account state rather than code:

  * "No profiles for 'dev.spiffcs.hoard.scan.ios' were found" with nothing
    else wrong — the bundle ID is not registered. Until it exists on
    developer.apple.com there is no App ID to build a distribution profile
    against, and -allowProvisioningUpdates cannot invent one.
        developer.apple.com/account › Identifiers › + › App IDs
    This is step 3 of docs/app-store-release.md and it has not been done.

  * "No Accounts" or "PLA Update available" — the same two as the Debug build.
    Xcode › Settings › Accounts, or accept the agreement at
    developer.apple.com/account. Provisioning is gated on the agreement, and
    Apple revises it without warning.

  * "doesn't include signing certificate 'Apple Distribution'" — the team has
    a development certificate but no distribution one. They are different
    certificates and a development one cannot sign an App Store build. Xcode
    creates it if the account has the Admin or Account Holder role:
        Xcode › Settings › Accounts › Manage Certificates › + › Apple Distribution
    Check what you have:
        security find-identity -v -p codesigning

  * A destination error — the iOS platform payload is not installed:
        xcodebuild -downloadPlatform iOS

To prove the Release configuration still COMPILES while any of the above is
being sorted out, build it unsigned. This is signing-free and takes no account:

    xcodebuild -project scan/hoard-scan-ios/HoardScan.xcodeproj -scheme HoardScan \\
        -destination 'generic/platform=iOS' -configuration Release \\
        CODE_SIGNING_ALLOWED=NO build
EOF
    exit 1
fi
echo "Archived $archive_path"

app_plist="$archive_path/Products/Applications/HoardScan.app/Info.plist"
if [ -f "$app_plist" ]; then
    pb() { /usr/libexec/PlistBuddy -c "Print :$1" "$app_plist" 2>/dev/null || echo '?'; }
    marketing=$(pb CFBundleShortVersionString)
    echo "  bundle  $(pb CFBundleIdentifier)"
    echo "  version $marketing (build $(pb CFBundleVersion))"
    case "$marketing" in
        0.*) echo "  note: MARKETING_VERSION is still $marketing — App Store submissions" >&2
             echo "        conventionally start at 1.0. See docs/app-store-release.md step 7." >&2 ;;
    esac
fi

opts="$out_dir/ExportOptions.generated.plist"
cp "$opts_template" "$opts"
/usr/libexec/PlistBuddy -c "Add :teamID string $team" "$opts" >/dev/null 2>&1 \
    || /usr/libexec/PlistBuddy -c "Set :teamID $team" "$opts" >/dev/null

echo "Exporting for App Store Connect…"
if ! env PATH="/usr/bin:/bin:/usr/sbin:/sbin" xcodebuild -exportArchive \
    -archivePath "$archive_path" \
    -exportPath "$export_dir" \
    -exportOptionsPlist "$opts" \
    -allowProvisioningUpdates; then
    cat >&2 <<EOF

The export failed. The archive at

    $archive_path

is fine and is still there — export re-signs it, so this is a signing or an
App Store Connect problem, never a compile one. Re-run this script to retry
just this step once it is fixed; the archive is rebuilt but nothing is lost.

As on the Debug path, "No profiles for 'dev.spiffcs.hoard.scan.ios' were
found" is the symptom and the line ABOVE it is the cause. In the order they
have actually been hit:

  * "exportArchive No Accounts" — this is the one you get today, and it is not
    about profiles at all. The archive signs fine because a development
    certificate in the keychain is enough for that; export has to ASK Apple for
    a distribution profile, and xcodebuild can only ask as an account that
    Xcode knows about. The keychain is not an account.
        Xcode › Settings › Accounts › + › Apple ID
    Then re-run. Nothing else on this machine changes.

  * "No profiles for ... were found" with no other error — the account is
    signed in but the App ID does not exist yet, so there is nothing to make a
    distribution profile against.
        developer.apple.com/account › Identifiers › + › App IDs
    Step 3 of docs/app-store-release.md.

  * "exportArchive: No signing certificate 'Apple Distribution' found" — see
    the archive block above. Development and distribution are different
    certificates and the archive succeeding proves nothing about the second.

  * "Team ... does not have permission to create ..." — the Apple ID is a
    Developer, not an App Manager or Admin, on this team.

The export options are in $opts if you want to
see exactly what was asked for; the tracked template beside it explains why
each key is set the way it is.
EOF
    exit 1
fi

ipa=$(find "$export_dir" -maxdepth 1 -name '*.ipa' -print -quit)
if [ -z "$ipa" ]; then
    echo "export reported success but produced no .ipa in $export_dir" >&2
    exit 1
fi
echo "Exported $ipa"

if [ "$action" = archive ]; then
    cat <<EOF

Not uploaded — that needs --upload. What is left before one can succeed:

  * The app record must exist in App Store Connect and the bundle ID
    dev.spiffcs.hoard.scan.ios must be registered. Steps 1 and 3 of
    docs/app-store-release.md.
  * Credentials. Run this again with --validate to have them checked and, if
    they are missing, be told exactly what to obtain.

    ./release-scan-ios.sh --validate    Apple's pre-upload checks, no build spent
    ./release-scan-ios.sh --upload      the real thing
EOF
    exit 0
fi

echo "Authenticating with $asc_auth_desc"
case "$action" in
    validate)
        echo "Validating $ipa with App Store Connect…"
        xcrun altool --validate-app -f "$ipa" -t ios "${asc_auth_args[@]}"
        echo "Validated. Nothing was uploaded and no build number was spent."
        ;;
    upload)
        echo "Uploading $ipa to App Store Connect…"
        xcrun altool --upload-app -f "$ipa" -t ios "${asc_auth_args[@]}"
        cat <<EOF

Uploaded build $stamp.

It is not visible yet: App Store Connect processes for a few minutes to an hour
before the build appears under TestFlight, and it emails if processing fails.
Export compliance is answered by ITSAppUsesNonExemptEncryption in the app's
Info.plist — if App Store Connect asks the question anyway, that key did not
make it into this build.
EOF
        ;;
esac
