# Hoardling — the iPhone capture head

Point the phone at a card: it captures, reads the title and collector line
on-device with Vision, and sends the reading to a Mac running `hoard` over the
local network. The Mac resolves the card, decides whether to auto-add it, and
sends a price back for the phone to flash and chime.

**The app does nothing on its own.** Opened with no Mac listening it says
"Waiting for hoard…" forever — no preview, no reading — so a working build is
indistinguishable from a broken one until a Mac is listening.

## Prerequisites

- **Xcode**, the full app, plus the iOS platform payload
  (`xcodebuild -downloadPlatform iOS`). Xcode lists the SDK before the payload
  is downloaded, so a missing one surfaces as a confusing destination error.
- **XcodeGen** — `brew install xcodegen`. The `.xcodeproj` is generated.
- **An Apple Developer account with a team.** Ad-hoc signing cannot install on
  a device.
- **An iPhone on iOS 18+** with Developer Mode on (Settings › Privacy &
  Security › Developer Mode): one toggle and a reboot.

## Build and run

```sh
brew install xcodegen
make scan-ios            # also writes Signing.xcconfig, guessing your team
make scan-ios-install    # onto an attached, unlocked phone
```

`Signing.xcconfig` is gitignored — a team identifier is account data, not
project configuration. If the guess is wrong, set your team ID by hand and
re-run.

Then start the Mac half with `hoard add`, open Hoardling and leave it on
screen. <kbd>ctrl+p</kbd> pairs, using the six digits on the phone's Pair tab;
<kbd>ctrl+o</kbd> opens a session. Set cards down one at a time and the phone
fires its own shutter. Pairing is once per phone, stored in `scan.json`.

The link is TLS, with each device's self-signed certificate pinned at pairing.

## What catches people out

**Every signing failure reports as `No profiles for
'dev.spiffcs.hoard.scan.ios' were found`.** That is true and useless — the real
cause is the line above it: `No Accounts` (Xcode is not signed in),
`PLA Update available` (accept the agreement at developer.apple.com), or a
device UDID not registered to the team (`xcrun devicectl list devices`).

**`xcodegen generate` fails on a fresh clone** with `invalid config file path
".../Signing.xcconfig"`. Run `make scan-ios` once first — that is what writes
the file.

**`project.yml` is the source of truth.** The `.xcodeproj` and `Info.plist` are
generated and gitignored, so anything changed in Xcode's project editor is lost
on the next generate.

**The app must be foregrounded.** iOS suspends background apps, and a suspended
app stops advertising over Bonjour — so a phone you switched away from looks
exactly like a phone that is not there.

## More

The read pipeline and wire contract are SwiftPM targets under
`scan/hoard-scan/`, shared with the Go side. Test with `make scan-test` and
`make scan-ios-test` — never regenerate goldens from the simulator, whose
Vision is not device Vision. `HOARD_SCAN_LOG=/tmp/scan.log hoard add` tees
every event from both sides of the wire to one file.

Accuracy: [scanner-limits.md](../../docs/specs/scanner-limits.md). Trigger
constants: read [scanner-tuning.md](../../docs/specs/scanner-tuning.md) first.
