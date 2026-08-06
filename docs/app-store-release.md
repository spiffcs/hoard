# Shipping Hoard Scan on the App Store

**Status: placeholder.** Nothing here has been attempted. This is the list of
what stands between the app as it works today and a submission, written while
the details are fresh so a future session does not have to rediscover them.

The app currently installs to a registered device via `make scan-ios-install`
and has never been through TestFlight, App Store Connect, or review.

## The one that decides everything else

**The app does nothing without a Mac running hoard.**

Open it cold and it says "Waiting for hoard…" forever. There is no camera
preview until a Mac pairs, no card reading, no output. A reviewer with an
iPhone and no Mac sees a dead app, and *"app does not function"* is the most
common rejection there is.

This is not a detail to solve at submission time — it shapes the product. The
options, roughly in order of how much work they are:

- **Review notes plus a demo video.** Cheapest, and reviewers do accept
  companion-app arrangements (Apple Watch apps, camera remotes, DSLR tethers all
  work this way). Needs a written explanation and a video showing the pairing
  flow end to end. Might still draw a rejection that has to be appealed.
- **A standalone demo mode.** The app reads cards perfectly well on its own.
  `CardKit` imports no networking of any kind — the read pipeline is Vision and
  Core Graphics, and nothing in it needs a Mac. A mode
  that shows the read on screen without sending it anywhere would make the app
  self-evidently functional. This is the honest answer, and it is not much work:
  everything except the wire already runs on the phone.
- **Make the phone useful alone.** Local storage, a card list, export. That is a
  different product and a much bigger decision.

**Recommendation: build the demo mode.** It is small, it makes review
straightforward, and it is genuinely useful for testing the read pipeline
without a Mac in the loop.

## Blockers

### Transport is authenticated but not encrypted

Card reads, names and prices cross the local network in plaintext. The pairing
handshake is sound — HMAC-SHA256 over a fresh session id, keyed by the pairing
code, verified in constant time — so nobody can *join* a session without the
code, but anyone on the network can *read* one.

TLS-PSK was built first and did not work: both ends sat in `.connecting`
forever with no error. See `docs/sprint-iphone-capture-head.md` for what was
tried. This needs solving before submission, not because Apple will test for it
but because "sends your data unencrypted over the network" is a fair thing for a
privacy label to have to admit.

### No privacy manifest

`PrivacyInfo.xcprivacy` does not exist. Required since May 2024 for any app
using declared "required reason" APIs.

Checked: the pairing code lives in the **Keychain**, not `UserDefaults`
(`PairingStore.swift` uses `SecItem*` deliberately — its comment says the code
is "the only thing standing between a stranger and a session that auto-commits",
and UserDefaults is a readable plist). So the most common declaration does not
apply.

What still needs auditing is `SessionLog`: it opens a `FileHandle`, seeks and
appends, which may trip the file-timestamp category. That is a read of the code,
not a guess — do it before filling in the manifest.

### The session log is world-readable

`UIFileSharingEnabled: true` and `LSSupportsOpeningDocumentsInPlace: true` are
set so `SessionLog` can be pulled off the device from the Files app. That log
contains card names and prices. It is the right call for a development build and
wants a second look before shipping — either gate it behind a debug setting or
accept it and say so in the privacy disclosure.

## Chores

| | |
|---|---|
| **Bundle ID** | `dev.spiffcs.hoard.scan.ios` — fine, needs registering |
| **Signing** | `Signing.xcconfig` is gitignored; CI or a release script needs a real team |
| **Deployment target** | iOS 18.0. Narrow. Driven by `RecognizeTextRequest` and `RotationCoordinator`; lowering it means keeping an older Vision path |
| **Version** | `MARKETING_VERSION 0.1` |
| **Export compliance** | The app uses CryptoKit (HKDF, HMAC). The submission questionnaire has to be answered honestly; this is normally exempt but must be declared |
| **App icon** | Done, but derived from a 512px source and upscaled to 1024. A higher-resolution original would be a straight improvement |
| **Screenshots** | None. Needed for every required device size |
| **Privacy policy URL** | None. Required |
| **Age rating, category, description** | None |
| **TestFlight** | Never used. Should precede review by some margin |

## What is already fine

Worth recording so nobody re-solves it:

- **The shutter sound problem is gone.** It used to need
  `AudioServicesDisposeSystemSoundID(1108)`, an undocumented hack that would
  have had to come out before submission because some jurisdictions require a
  capture sound. Removing the photo path entirely removed the problem: the video
  tap makes no sound to suppress, and it captures at the same 4032x3024.
- **Usage descriptions are written and accurate** — `NSCameraUsageDescription`,
  `NSLocalNetworkUsageDescription`, `NSBonjourServices`.
- **No accounts, no analytics, no third-party SDKs.** Verified: the Swift
  package declares no external dependencies and there is no `Package.resolved`
  because there is nothing to resolve. Every target depends only on sibling
  targets and Apple frameworks. That makes the privacy label close to empty,
  which is a genuinely strong position.
- **The pairing code is in the Keychain**, not a plist.
- **Nothing leaves the local network.** Card reads go to a Mac on the same
  Wi-Fi. No servers, nothing to disclose as data collection.

## Open questions

1. **Is the App Store even the right channel?** The app is only useful to people
   already running hoard on a Mac. Ad-hoc distribution or TestFlight might serve
   the actual audience with none of the review overhead. Worth deciding before
   the work is done, not after.
2. **What is the app called on the home screen?** `CFBundleDisplayName` is
   "hoard scan". Fine, but the App Store name has to be unique across the store.
3. **Does the read pipeline's accuracy matter for review?** No — but see
   `docs/scanner-accuracy.md` before making claims in the description. Foreign
   language cards do not resolve, and planes and full-art cards with no printed
   title cannot be read at all.
