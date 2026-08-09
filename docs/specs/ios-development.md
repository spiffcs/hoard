# Working on the iPhone app

The scanner is an iPhone app. This is how to build it, run it, and change it
after cloning the repo.

> Everything here is macOS-only and needs a real iPhone. The simulator has no
> camera and no Bonjour peer, so it can compile the app but cannot run it in any
> useful sense.

## What you are building

`scan/hoard-scan-ios` is **Hoardling**, a capture head. Point it at a card and
it captures, reads the title and collector line on-device with Vision, and sends
the finished reading to a Mac running `hoard` over the local network. The Mac
resolves the card against Scryfall, decides whether to auto-add it, and sends a
price back for the phone to flash and chime.

**The app does nothing on its own.** Open it cold with no Mac listening and it
says "Waiting for hoard…" forever — no preview, no reading, no output. That is
the first thing to know, because it is otherwise indistinguishable from a build
that is broken. (It is also the main obstacle to ever shipping this on the App
Store; see [app-store-release.md](app-store-release.md).)

It used to be one of two ways to scan, beside macOS Continuity Camera. It is now
the only one — Continuity was removed on 2026-08-05, along with the second read
pipeline that served it.

## Prerequisites

| | |
|---|---|
| **macOS + Xcode** | the full app, not just Command Line Tools |
| **The iOS platform payload** | `xcodebuild -downloadPlatform iOS`. Xcode lists the SDK before this is downloaded, so a missing payload shows up as a confusing destination error |
| **XcodeGen** | `brew install xcodegen`. The `.xcodeproj` is generated, not checked in |
| **An Apple Developer account** | with a team. Ad-hoc signing (what the Mac helper uses) cannot install on a device at all |
| **An iPhone** | iOS 18+, with Developer Mode on: Settings › Privacy & Security › Developer Mode. That is a one-time toggle and a reboot |

## First build

```sh
git clone https://github.com/spiffcs/hoard && cd hoard
brew install xcodegen
make scan-ios
```

The first run writes `scan/hoard-scan-ios/Signing.xcconfig`, guessing your team
from the keychain:

```
DEVELOPMENT_TEAM = ABCDE12345
```

That file is gitignored on purpose — a team identifier is account data, not
project configuration, and it should not travel with the repo. If the guess is
wrong or absent, put your team ID in by hand and re-run.

To install onto an attached, unlocked phone:

```sh
make scan-ios-install
```

## When signing fails

It will, the first time. Every failure here reports as **"No profiles for
'dev.spiffcs.hoard.scan.ios' were found"**, which is true and useless — the real
cause is one line above it in the output. These are the ones seen so far:

| error above the profile message | what it means |
|---|---|
| `No Accounts` | Xcode is not signed into the Apple Developer account that owns the team in `Signing.xcconfig`. Xcode › Settings › Accounts |
| `PLA Update available` | Apple revised the Program License Agreement and gates provisioning until it is accepted. Nothing to fix locally: sign in at developer.apple.com/account and accept the banner |
| `… isn't registered in your developer account` | the phone is attached and trusted, but its UDID is not on the team. `-allowProvisioningUpdates` does not reliably add it from the command line — register it once at developer.apple.com/account under Devices, or open the generated project in Xcode and pick the phone as the run destination. Its UDID: `xcrun devicectl list devices` |
| a destination error | the iOS platform payload is not installed: `xcodebuild -downloadPlatform iOS` |

To check that the code still compiles while any of that is being sorted out,
build without signing:

```sh
xcodebuild -project scan/hoard-scan-ios/HoardScan.xcodeproj -scheme HoardScan \
    -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
```

## Working in Xcode

```sh
cd scan/hoard-scan-ios && xcodegen generate && open HoardScan.xcodeproj
```

**Run `make scan-ios` once first.** On a fresh clone `xcodegen generate` fails
outright — `project.yml` names `Signing.xcconfig` as the config file for both
build configurations, and that file is gitignored, so it does not exist until
`build-scan-ios.sh` writes it:

```
2 Spec validations errors:
    - Target "HoardScan" has invalid config file path ".../Signing.xcconfig" for config "Release"
```

That is the fix, not a broken checkout.

**`project.yml` is the source of truth.** The `.xcodeproj` is a build artifact
and is gitignored, so anything you change in Xcode's project editor — build
settings, target membership, capabilities — is lost the next time anyone
regenerates. Change `project.yml` instead. The same goes for `Info.plist`, which
is generated from that file's `info.properties` block and is also gitignored.

Adding a source file is the exception that needs no thought: `sources:` names
the directory, so a new file under `Sources/` is picked up on the next generate.

## The whole loop

Both halves have to be running.

```sh
# On the Mac, once:
make all            # the hoard binary + bin/hoard-scan.app (the link's Mac end)

# Then, per session:
./hoard add
```

1. Open **Hoardling** on the phone and leave it on screen.
2. In the add session press <kbd>ctrl+p</kbd> to pair. hoard finds the phone and
   asks for the six digits on its Pair tab. The code is verified against the
   phone before it is saved, so a typo fails while you are still looking at the
   screen.
3. Press <kbd>ctrl+o</kbd> to open a session. Set cards down one at a time; the
   phone fires its own shutter when a card settles.

Pairing is once per phone, kept in `scan.json` beside the database. **Generate a
new code** on the Pair tab revokes every Mac.

## Where the code lives

The app target is thin. Most of the work is in SwiftPM targets under
`scan/hoard-scan`, shared with the Mac side.

```
scan/hoard-scan-ios/
  project.yml               the source of truth for the Xcode project
  Sources/
    HoardScanApp.swift      the SwiftUI entry point
    Capture/
      CameraSession.swift   AVFoundation: the video tap, locked lens and metering
      TriggerRunner.swift   drives CardKit's trigger off the tap, plus CoreMotion
      PreviewLayerView.swift
    Link/
      LinkController.swift  the wire: what the phone sends, what it accepts
      PairingStore.swift    the code, in the Keychain (not UserDefaults)
      PairingView.swift, SessionView.swift, PriceOverlay.swift, CardOutline.swift
      SessionLog.swift      the on-device capture log
      Sounds.swift

scan/hoard-scan/            SwiftPM package, shared
  Sources/CardKit/          the read pipeline: title, collector line, trigger
  Sources/BorderKit/        the border reader — reached through CardKit, not
                            linked directly by the app
  Sources/ScanLink/         Bonjour + the pairing handshake (HMAC-SHA256)
  Sources/ScanWire/         the NDJSON contract with the Go side
  Sources/ScanKit/          the *Mac* end of the link. The phone does not use it
```

`ScanWire` is the contract with Go (`internal/scan`). Both ends speak it, so it
must not fork — that is why it is a target of its own rather than a struct on
each side.

The app declares three package dependencies in `project.yml` — `CardKit`,
`ScanLink`, `ScanWire` — and gets `BorderKit` through `CardKit`. `ScanKit` is
not among them and must not be added: it is the Mac's end of the link and links
AppKit.

## Tests

```sh
make scan-test        # SwiftPM units: trigger, parsers, border maths, wire
make scan-ios-test    # the same, on the iOS simulator — proves it builds for iOS
make cardkit-score    # CardKit over scan/corpus's labelled images: the accuracy table
make scan-check       # replay scan/fixtures' real photographs against goldens
```

`make cardkit-score` and `make scan-check` both run `CardKit` — the phone's
pipeline — through `bin/cardkit-probe`, a macOS shim over the same code. That is
what lets a read be scored without a phone in the loop. The two disagree by
design: the corpus is clean scans and tests parsing, the fixtures are
photographs off a desk and test capture. See `scan/fixtures/README.md`, which
also records where the current reader falls short.

Simulator Vision is not device Vision. Goldens must never be regenerated from
the simulator.

## Debugging

**Developer mode.** Off by default, toggled on the Pair tab. It puts two
readouts back on the scanning screen — the trigger's phase ("Waiting for a
card", "Holding still…") and the last card read, `(nothing read)` included —
and reveals the share sheet for the session log. Everything it shows is an
account of the machinery; with it off the screen shows a price, the auto
toggle, and any failure worth acting on. Turn it on before a tuning session,
off before handing the phone to anyone.

**The session log.** The app appends every capture to `Documents/` on the
device. Developer mode adds a share sheet on the Pair tab, and
`UIFileSharingEnabled` is set, so it is also reachable from the Files app, or:

```sh
xcrun devicectl device copy from --device <udid> \
    --domain-type appDataContainer --domain-identifier dev.spiffcs.hoard.scan.ios \
    --source Documents/ --destination ./pulled
```

**The Mac's view.** `HOARD_SCAN_LOG=/tmp/scan.log ./hoard add` tees every event,
timestamped, to a file — the only way to watch the feed while the TUI owns the
pipes. The phone's own trace lines are re-emitted on the helper's stderr and
land in the same file, so one log covers both sides of the wire.

**Raw stills.** `HOARD_SCAN_DEBUG_DIR=/tmp/frames` asks the phone to send each
capture's full-resolution JPEG back. Off otherwise, and deliberately: these are
4032x3024 per card, and not shipping them is most of the point of reading on the
phone. It is how the fixture set gets built.

**Trigger tuning.** `HOARD_SCAN_AUTO_STABLE` and `HOARD_SCAN_AUTO_INTERVAL` are
forwarded to the phone at session start. Read
[scanner-tuning.md](scanner-tuning.md)'s ledger before touching either — the
current values were fitted against recorded sessions, and the reasoning is
written down there precisely so it is not re-derived badly.

## Gotchas

- **The app must be foregrounded.** iOS suspends background apps and a suspended
  app stops advertising over Bonjour, so a phone you switched away from looks
  exactly like a phone that is not there. If <kbd>ctrl+o</kbd> finds nothing,
  this is usually why.
- **Same network.** Both devices on the same Wi-Fi, or the phone plugged in.
- **There is no standalone mode.** You cannot test a read without a Mac running
  `hoard`. Building one is the recommendation in
  [app-store-release.md](app-store-release.md) and has not been done.
- **The transport is authenticated but not encrypted.** The pairing handshake is
  sound, so nobody joins a session without the code, but anyone on the network
  can read one. Do not scan anything you would mind a stranger seeing on a
  network you do not trust.
- **One card per capture.** `CardKit` reads at most one card per frame. Fanned
  spreads are not supported.
