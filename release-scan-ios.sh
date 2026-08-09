#!/usr/bin/env bash
# release-scan-ios.sh — archive, export and upload the iPhone capture head.
#
# The distribution sibling of build-scan-ios.sh. That one builds Debug onto the
# phone on your desk; this one builds Release, archives it, exports a signed
# .ipa and — given credentials — hands it to App Store Connect for TestFlight or
# review.
#
#   make scan-ios-release              archive + export, stop with an .ipa
#   make scan-ios-release-validate     … then run Apple's pre-upload checks
#   make scan-ios-release-upload       … then upload it
#
# Why a second script rather than `build-scan-ios.sh --release`. The two paths
# agree only on the first four steps — xcodegen, Signing.xcconfig, generate,
# version stamp — and those are factored into scan-ios-common.sh and shared.
# After that they have nothing in common. The Debug path discovers an attached
# device by hardware UDID, builds for that specific destination so provisioning
# errors name the phone, and installs with devicectl. This path never touches a
# device: it builds for a generic destination, needs a distribution certificate
# rather than a development one, has its own export options, and its own class
# of failure (credentials, App Store Connect state) that means nothing on the
# Debug path. Folding them together would have put a conditional inside almost
# every block of an already-long script, for no shared code beyond what is now
# in the common file.
#
# What this script does NOT do: it does not create the app record, register the
# bundle ID, or write store metadata. See docs/app-store-release.md — those are
# steps 1, 3 and 11, they are done in a browser, and until steps 1 and 3 are
# done the export below cannot be signed at all.
set -euo pipefail

# shellcheck source=scan-ios-common.sh
source "$(cd "$(dirname "$0")" && pwd)/scan-ios-common.sh"

root="$scan_ios_root"
proj_dir="$scan_ios_proj_dir"
action=archive          # archive | validate | upload

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
# The derived data path is overridable so a second build can run beside the
# Debug one without the two fighting over the same module cache. Both default
# to $proj_dir/.build, which is gitignored.
derived_data="${HOARD_IOS_DERIVED_DATA:-$proj_dir/.build}"

# ---------------------------------------------------------------------------
# App Store Connect credentials.
#
# Resolved BEFORE the archive rather than after, because the archive takes
# minutes and this check takes none, and learning that you have no API key
# after a four-minute build is the kind of thing that makes people stop using
# a script. Sets asc_auth_args and asc_auth_desc on success; returns 1 with
# nothing printed on failure, so the caller can decide whether that is fatal.
# ---------------------------------------------------------------------------
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
            # --p8-file-path takes an explicit path, which is the only way to
            # use a key that is not in one of altool's four magic directories.
            asc_auth_args+=(--p8-file-path "$HOARD_ASC_KEY_PATH")
            asc_auth_desc="API key $key_id ($HOARD_ASC_KEY_PATH)"
        else
            # With no explicit path altool searches, in this order:
            # ./private_keys, ~/private_keys, ~/.private_keys and
            # ~/.appstoreconnect/private_keys, for AuthKey_<id>.p8. Look in the
            # same places first, so a missing key is a sentence here instead of
            # an opaque altool error after it has already started a session.
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
        # @env: rather than the password itself, so it never appears in argv
        # where `ps` — and every process on the machine — can read it. altool
        # reads the named variable out of its own environment.
        asc_auth_args=(-u "$HOARD_ASC_APPLE_ID" -p "@env:HOARD_ASC_PASSWORD")
        asc_auth_desc="app-specific password for $HOARD_ASC_APPLE_ID"
        return 0
    fi

    return 1
}

# The block that runs when there are no credentials. Long on purpose: this is
# the step the user has not done yet, and the useful thing a script can do
# about a missing account credential is say precisely which one and where it
# comes from, rather than let a tool fail with its own vocabulary.
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

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
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
    # Not fatal. An upload from a dirty tree is a build nobody can reconstruct,
    # which matters when a TestFlight tester reports something six weeks later —
    # but refusing outright would be wrong for the one legitimate case, a
    # last-minute version bump that has not been committed yet.
    echo "warning: uploading from a dirty working tree — this build is not reproducible from a commit" >&2
fi

# ---------------------------------------------------------------------------
# Archive
# ---------------------------------------------------------------------------
# A stale .xcarchive at the same path is not overwritten by xcodebuild, it is
# refused, so clear it. Only this one path — never the whole .build directory,
# which holds the Debug build's derived data too.
rm -rf "$archive_path" "$export_dir"
mkdir -p "$out_dir"

echo "Archiving HoardScan (Release)…"
# 'generic/platform=iOS' rather than an attached device: an archive is a
# universal, unthinned build for every supported device, and naming a specific
# phone would both thin it and drag device registration into a step that has
# nothing to do with any device. (build-scan-ios.sh names the device on purpose,
# for the opposite reason — it wants provisioning errors to say WHICH phone.)
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

# What actually landed in the archive, read back from the built app rather than
# from project.yml — the settings have two possible homes (project.yml's
# info.properties and its build settings) and Xcode's build setting wins, so
# the only honest source is the product.
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

# ---------------------------------------------------------------------------
# Export
# ---------------------------------------------------------------------------
# The tracked template carries no team identifier; the copy gets one. See the
# comment at the top of ExportOptions-AppStore.plist.
opts="$out_dir/ExportOptions.generated.plist"
cp "$opts_template" "$opts"
/usr/libexec/PlistBuddy -c "Add :teamID string $team" "$opts" >/dev/null 2>&1 \
    || /usr/libexec/PlistBuddy -c "Set :teamID $team" "$opts" >/dev/null

echo "Exporting for App Store Connect…"
# Exported with a system-only PATH, and this is load-bearing rather than
# hygiene.
#
# The IPA step shells out to `/usr/bin/rsync -8aPhhE`. That absolute path is
# openrsync, which understands Apple's `-E` (--extended-attributes) — but
# rsync's local-copy mode spawns its other half by exec'ing `rsync --server`
# *through PATH*, and a Homebrew rsync (3.4.4 here) gets found first. Upstream
# rsync has no --extended-attributes; it spells that -X/--xattrs. So the client
# speaks Apple's dialect to a GNU-dialect server and the export dies with:
#
#     rsync: on remote machine: --extended-attributes: unknown option
#     error: exportArchive Copy failed
#
# "Copy failed" is all xcodebuild prints. The cause appears only as
# `[server=3.4.4]` inside the .xcdistributionlogs bundle, and the absolute path
# in the logged command line actively misleads — it looks like the system rsync
# ran, and it did; it was the child that was wrong. Measured on 2026-08-08: the
# identical export succeeds with Homebrew off PATH and fails with it on.
#
# Narrowing PATH rather than telling the user to `brew unlink rsync`: this is a
# build step with no legitimate need for anything outside the system
# directories, and a fix that survives a fresh checkout beats one that lives in
# somebody's shell history.
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

# ---------------------------------------------------------------------------
# Validate / upload
# ---------------------------------------------------------------------------
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

# altool, not notarytool. They are not alternatives and the naming invites the
# mistake: notarytool submits a macOS app for Developer ID notarization, which
# is the path for distributing OUTSIDE the store, and it has no iOS mode at all.
# App Store uploads still go through altool (or Transporter.app, or Xcode's
# Organizer, both of which are the same ContentDelivery framework with a window
# in front). Deprecation notices about altool are about --upload-package and the
# older iTMSTransporter shims, not --upload-app.
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
