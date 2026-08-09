# shellcheck shell=bash
# scan-ios-common.sh — the parts of the iPhone build that both paths need.
#
# Sourced, never executed. It exists because there are two ways to build the
# capture head and they agree on the front half and nothing else:
#
#   build-scan-ios.sh    Debug, onto the phone on your desk
#   release-scan-ios.sh  Release, archived and exported for App Store Connect
#
# The front half is: xcodegen has to be installed, Signing.xcconfig has to
# exist (it is gitignored, so on a fresh clone it does not), the project has to
# be regenerated from project.yml, and the build needs a version stamp. Those
# four are identical. Everything after them differs — one discovers an attached
# device and installs to it, the other never touches a device and has to think
# about distribution certificates, export options and upload credentials — so
# they are separate scripts rather than one script with a --release flag that
# would branch inside almost every block.

# The repository root, resolved from this file rather than from $0, so it is
# correct no matter which script sourced it or from where.
scan_ios_root=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
scan_ios_proj_dir="$scan_ios_root/scan/hoard-scan-ios"
scan_ios_xcconfig="$scan_ios_proj_dir/Signing.xcconfig"

# xcodegen is a hard dependency: the .xcodeproj is generated from project.yml
# and is gitignored, so there is nothing to build without it.
ios_require_xcodegen() {
    command -v xcodegen >/dev/null 2>&1 && return 0
    cat >&2 <<'EOF'
xcodegen not found. The Xcode project is generated from project.yml rather than
checked in, so it is needed to build:

    brew install xcodegen

(If you would rather not add the dependency, `xcodegen generate` once elsewhere
and commit the .xcodeproj — but then project.yml stops being the source of
truth, so pick one.)
EOF
    exit 2
}

# Write scan/hoard-scan-ios/Signing.xcconfig if it is missing, guessing the team
# from the keychain.
#
# This has to happen before `xcodegen generate`, not just before the build:
# project.yml names Signing.xcconfig as the config file for BOTH configurations,
# and xcodegen fails spec validation outright when the path does not resolve.
# That is the "invalid config file path" error in docs/ios-development.md, and
# it is why the file is created here rather than left to the developer.
ios_ensure_signing_xcconfig() {
    [ -f "$scan_ios_xcconfig" ] && return 0
    local team
    team=$(security find-identity -v -p codesigning 2>/dev/null \
        | sed -n 's/.*(\([A-Z0-9]\{10\}\))"$/\1/p' | head -1)
    cat >"$scan_ios_xcconfig" <<EOF
// Local signing identity. Gitignored — a team identifier is account data, not
// project configuration, and it should not travel with the repo.
DEVELOPMENT_TEAM = ${team:-YOUR_TEAM_ID}
EOF
    echo "wrote $scan_ios_xcconfig${team:+ (team $team, guessed from your keychain)}"
    [ -z "$team" ] && {
        echo "  no signing identity found — put your team ID in that file" >&2
        exit 2
    }
    return 0
}

# The team identifier, on stdout. Empty if it cannot be determined.
#
# $HOARD_IOS_TEAM wins, so a machine with several teams — or CI, which has no
# Signing.xcconfig at all — can inject one without writing to disk. Otherwise
# it is read out of the gitignored xcconfig. It is never read from a tracked
# file, because there is no tracked file that has it: a team identifier is
# account data and does not travel with the repo.
ios_team_id() {
    if [ -n "${HOARD_IOS_TEAM:-}" ]; then
        printf '%s\n' "$HOARD_IOS_TEAM"
        return 0
    fi
    [ -f "$scan_ios_xcconfig" ] || return 0
    sed -n 's/^[[:space:]]*DEVELOPMENT_TEAM[[:space:]]*=[[:space:]]*\([A-Za-z0-9]*\).*/\1/p' \
        "$scan_ios_xcconfig" | head -1
}

# Regenerate the .xcodeproj from project.yml.
ios_generate_project() {
    echo "Generating the Xcode project…"
    (cd "$scan_ios_proj_dir" && xcodegen generate --quiet)
}

# A per-build CFBundleVersion: YYMMDD.HHMM.SS in UTC with leading zeros
# stripped, e.g. 260808.1346.13, and at 01:05:03 UTC, 260808.105.3.
#
# On the Debug path this is only an identity token — the ready event carries it
# as appVersion so a session log proves which build the phone was running, and
# nothing parses it. On the release path it is the number App Store Connect
# orders uploads by, which is a much stricter contract, and the format has to
# satisfy both:
#
#   * CFBundleVersion admits at most three period-separated non-negative
#     integers. More than three, or a non-integer component, is rejected at
#     upload as ITMS-90060 ("must be a period-separated list of at most three
#     non-negative integers"). Three components, all integers: fine.
#
#   * App Store Connect refuses any upload whose build number is not strictly
#     greater than the last one in the same version train, comparing the
#     components as numbers. A timestamp is strictly increasing as long as the
#     clock is, which is the whole reason to keep this scheme rather than
#     invent a counter that would need somewhere to live.
#
# Two things about it that are not obvious:
#
#   * UTC, not local. This was `date +%y%m%d.%H%M.%S` in local time until the
#     release path needed it, and local time is not monotonic: on the DST
#     fall-back hour the wall clock repeats, so 01:59 can be followed an hour
#     later by 01:00, and the second upload of that day is REJECTED for going
#     backwards. One hour a year, unrecoverable without burning the rest of the
#     day's build numbers. UTC has no such hour. The Debug path was moved onto
#     the same clock rather than kept on the local one, because a stamp that
#     means two different things depending on which script printed it is worse
#     than a stamp that is an hour off from the wall clock.
#
#   * Leading zeros are stripped. Not for ordering — the components compare as
#     integers either way, so 260808.105.3 and 260808.0105.03 sort identically —
#     but so that Apple's validator is never handed an "0105" to have an opinion
#     about. The 10# prefix is load-bearing: bash reads a bare leading-zero
#     literal as octal, and $((08)) is not a valid octal number, so without it
#     every build between 08:00 and 09:59 would abort with "value too great for
#     base".
#
# Resolution is one second, which is finer than the fastest possible archive.
ios_build_stamp() {
    local d t s
    d=$(date -u +%y%m%d)
    t=$(date -u +%H%M)
    s=$(date -u +%S)
    printf '%d.%d.%d\n' "$((10#$d))" "$((10#$t))" "$((10#$s))"
}
