# shellcheck shell=bash

scan_ios_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
scan_ios_proj_dir="$scan_ios_root/scan/hoard-scan-ios"
scan_ios_xcconfig="$scan_ios_proj_dir/Signing.xcconfig"

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

ios_guess_team() {
    local name
    name=$(security find-identity -v -p codesigning 2>/dev/null \
        | sed -n 's/^[[:space:]]*[0-9]*)[[:space:]]*[0-9A-F]*[[:space:]]*"\(Apple Development:[^"]*\)"[[:space:]]*$/\1/p' \
        | head -1)
    [ -n "$name" ] || return 0
    security find-certificate -c "$name" -p 2>/dev/null \
        | openssl x509 -noout -subject -nameopt sep_multiline,utf8 2>/dev/null \
        | sed -n 's/^[[:space:]]*OU=//p' | head -1
}

ios_ensure_signing_xcconfig() {
    [ -f "$scan_ios_xcconfig" ] && return 0
    local team
    team=$(ios_guess_team)
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

ios_team_id() {
    if [ -n "${HOARD_IOS_TEAM:-}" ]; then
        printf '%s\n' "$HOARD_IOS_TEAM"
        return 0
    fi
    [ -f "$scan_ios_xcconfig" ] || return 0
    sed -n 's/^[[:space:]]*DEVELOPMENT_TEAM[[:space:]]*=[[:space:]]*\([A-Za-z0-9]*\).*/\1/p' \
        "$scan_ios_xcconfig" | head -1
}

ios_generate_project() {
    echo "Generating the Xcode project…"
    (cd "$scan_ios_proj_dir" && xcodegen generate --quiet)
}

ios_build_stamp() {
    local d t s
    d=$(date -u +%y%m%d)
    t=$(date -u +%H%M)
    s=$(date -u +%S)
    printf '%d.%d.%d\n' "$((10#$d))" "$((10#$t))" "$((10#$s))"
}
