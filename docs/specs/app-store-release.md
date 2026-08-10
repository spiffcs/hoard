# Shipping Hoardling on the App Store

**Status: audited 2026-08-06; everything up to an uploadable build done
2026-08-08.** `make scan-ios-release` produces a signed `HoardScan.ipa` and the
App Store Connect record exists. The app has never been through TestFlight or
review. See [ios-development.md](ios-development.md) for building and running
it, and **"What is left before review"** below for the remaining work — that
section is the current one; the blocker write-ups above it are kept for their
reasoning, not their status.

Channel decision, 2026-08-08: **full App Store submission**, with TestFlight as
the stage before it rather than an alternative to it. Open question 1 below is
therefore answered.

What landed on 2026-08-08, all verified against a built Release bundle rather
than asserted:

- The privacy manifest, the export-compliance key, Debug-only file sharing, and
  the version reconcile — steps 4 through 7.
- A release build path — step 8. Now working end to end: archive → export →
  a signed IPA. The two account items it waited on (an Xcode sign-in and an
  Apple Distribution certificate) were done the same day, and the bundle ID
  registered itself via `-allowProvisioningUpdates` on the first successful
  export.
- [scan-transport-encryption.md](scan-transport-encryption.md), which answers
  step 9's exploration half — five options, ranked, with a root-cause theory
  for the TLS-PSK failure and a one-hour experiment that would settle it.
- [scanner-limits.md](scanner-limits.md), which step 11 already cited and which
  did not exist. Every number in it was measured on 2026-08-08, not quoted.

Three of this page's own claims were wrong and are corrected in place below:
the `Info.plist` drift, the direction of the version drift, and the reason the
session log needed gating. The audit was right that each needed attention and
wrong about why — which is the ordinary way a doc rots.

## Blockers

### 1. Transport is authenticated but not encrypted — RESOLVED 2026-08-08, pending hardware validation

**The link now runs TLS with self-signed certificates pinned on first use**, the
model KDE Connect and Syncthing use for the same problem. `ScanLink` gained
`PeerIdentity` (a hand-rolled DER certificate, since iOS has no public API that
generates one) and `PeerTrust` (the pin store and verify-block policy); the
pairing proof is bound to the peer's certificate fingerprint so a relay cannot
forward it; and an already-pinned peer is not asked for the code at all, which
is what let the six-digit code become **per-launch and single-use** instead of
permanent. Landed in `64b6702`, 211/211 package tests green.

⚠️ **Loopback only. It has never run on real hardware** — see A2 in "What is
left before review", which is a gate on shipping any build to a tester.

The original analysis follows. Its *conclusion* was right and two of its
premises were not: the failure was never a mystery, and TLS-PSK does in fact
work on Apple platforms (TLS 1.2 only, and the selection block's completion
takes the identity rather than the key). PSK was measured working and then set
aside in favour of certificates, because a PSK keeps the pairing code
security-critical for the life of the link and a pinned certificate does not.
See [scan-transport-encryption.md](scan-transport-encryption.md) §10.

Confirmed at the time: `PeerLink.swift:226` builds `NWParameters.tcp`.
Plaintext. The `.tls` case at `PeerLink.swift:65` is error-message mapping only
— nothing in the tree ever negotiates TLS.

The pairing handshake itself is sound — HMAC-SHA256 over a fresh session id,
keyed by an HKDF-derived key, verified with
`HMAC.isValidAuthenticationCode` in constant time (`Pairing.swift`). Nobody can
*join* a session without the code. Anyone on the network can *read* one: card
names and prices cross the LAN in the clear.

TLS-PSK was built first and did not work — both ends sat in `.connecting`
forever with no error. `docs/sprint-iphone-capture-head.md` is a **dead
pointer**: that file was deleted in `72b09fc`. Its post-mortem is still
readable with `git show 72b09fc^:docs/sprint-iphone-capture-head.md`.

Apple will not test for this. It matters because the privacy disclosure would
have to admit it, and because it is the one security claim the app currently
cannot make.

**It is worse than "readable on the LAN," and this page understated it.**
`Pairing.swift:9` says "the link is TLS with a pre-shared key," which is not
true of any tree that has ever existed. The rationale at `Pairing.swift:46-52`
then leans on that: it concedes six digits is a million possibilities and
grindable offline from a captured handshake, and names the compensating control
— "the real protection is that TLS-PSK requires an *online* guess per attempt."
That control was never built. So the weak code has no backstop, and any scheme
keyed *only* on the pairing code inherits the same problem; every viable option
needs an ephemeral Diffie-Hellman.

The options are worked through in
[scan-transport-encryption.md](scan-transport-encryption.md). Two findings from
it belong here. Both ends are **Swift** linking the same `ScanLink` target —
`internal/scan/` does no networking at all, it drives the helper over
`os/exec` and NDJSON pipes — so a fix is written once, and `crypto/tls`'s lack
of external PSK does not apply. And `RemoteController.swift:119` reports every
transport failure as "The phone did not accept that code," which is why the
last attempt failed silently; fixing it is a prerequisite to attempting
anything, not a nicety.

### 2. No privacy manifest — DONE 2026-08-08

`Resources/PrivacyInfo.xcprivacy` now exists and is confirmed present at the
`.app` root of a built Release bundle. The audit below held up on re-grep, with
one correction: **UserDefaults is more widespread than it says.** Besides
`DevMode.key` via `@AppStorage`, `Sources/Settings/TierSettings.swift` holds a
`UserDefaults` directly across seven `tiers.*` keys. Same category, same
`CA92.1`, so still exactly one entry — but the count is not two call sites.

The rest re-verified clean across the app target and all four linked package
targets: zero hits for file-timestamp, boot-time, disk-space, and active-keyboard
APIs, and `Package.swift` has no `.package(url:)` at all, so the empty
`NSPrivacyCollectedDataTypes` is honest rather than optimistic.

The original audit follows, since its reasoning is what to re-run against any
new dependency.

`PrivacyInfo.xcprivacy` does not exist anywhere under `scan/`. Required since
May 2024 for any app touching a "required reason" API.

The audit reverses both halves of the previous guess:

- **`SessionLog` is fine.** It uses `FileHandle(forWritingTo:)`, `seekToEnd()`
  and `write(contentsOf:)`. None of those are on Apple's file-timestamp list —
  that list is `creationDate`, `modificationDate`, `fileModificationDate`,
  `getattrlist`, `fstat`, `stat` and friends, and a grep for all of them across
  the app and every linked target returns nothing.
- **UserDefaults *does* apply.** `DevMode.key` is read through `@AppStorage` in
  both `PairingView.swift:17` and `SessionView.swift:36`, and `@AppStorage` is
  UserDefaults. The earlier note said this did not apply because the pairing
  code lives in the Keychain — true of the *code* (`PairingStore.swift` uses
  `SecItem*` deliberately), irrelevant to the toggle.

So the manifest needs exactly one entry:

```xml
<key>NSPrivacyAccessedAPITypes</key>
<array>
  <dict>
    <key>NSPrivacyAccessedAPIType</key>
    <string>NSPrivacyAccessedAPICategoryUserDefaults</string>
    <key>NSPrivacyAccessedAPITypeReasons</key>
    <array><string>CA92.1</string></array>
  </dict>
</array>
```

`CA92.1` is "access info from the app's own container only," which is exactly
what a developer-mode toggle does. Also set `NSPrivacyTracking` to `false`,
`NSPrivacyTrackingDomains` to an empty array, and `NSPrivacyCollectedDataTypes`
to an empty array — all three are honest here, see "What is already fine".

Add the file under `Resources/` and list it in `project.yml`'s `sources`.

Re-run the grep after any new dependency lands. Required-reason coverage is a
property of the whole linked binary, not of the app target's own source.

### 3. The session log is world-readable — DONE 2026-08-08, premise was stale

Both keys are now Debug-only, verified absent from a built Release `Info.plist`
and present in Debug.

**The stated reason was already out of date.** `SessionLog.fileURL` writes to
`.cachesDirectory`, not `Documents` — it was moved there deliberately so the
file-sharing keys would not expose card names and prices. Gating was still the
right call; the exposure it was closing had already been closed elsewhere.

`Sources/Link/SessionLog.swift:11-13` still says the file-sharing keys "stay"
for the corpus workflow. That comment is now wrong and is worth fixing.

**How it is gated, because two obvious routes silently fail.**
`project.yml` splits *build settings* per config but not `info.properties`, and
neither declarative workaround survives measurement:

- `INFOPLIST_KEY_*` per config merges booleans into an existing plist, but the
  setting names are an allowlist of 95 in Xcode's `CoreBuildSystem.xcspec` and
  `INFOPLIST_KEY_UIFileSharingEnabled` is not among them. It drops the key with
  no warning. (It is also ignored outright without `GENERATE_INFOPLIST_FILE=YES`.)
- `$(SETTING)` expansion yields a *string*, so Release would ship the literal
  `"NO"` rather than a boolean or an absence.

What works is a `postBuildScripts` phase that strips both keys with PlistBuddy
when `CONFIGURATION != Debug`. It declares the built plist as an `inputFile` so
the build system orders it after `ProcessInfoPlistFile` rather than leaving that
to the scheduler; it sets `basedOnDependencyAnalysis: false` because the phase
rewrites its own input and a skipped run would ship the keys; and it re-reads
each key after deleting so a failure fails the build. That last part matters
more than it looks: every failure mode here otherwise looks exactly like
success.

The original note follows.

`UIFileSharingEnabled: true` and `LSSupportsOpeningDocumentsInPlace: true` are
both set (`project.yml`), so `Documents/session-log.txt` is visible in the Files
app. That log contains card names and prices, and `SessionLog.startSession()`
only truncates it when a Mac connects — a phone that never pairs accumulates
forever.

### 4. No release build path — DONE 2026-08-08, produces a signed IPA

`make scan-ios-release` archives, exports and emits a signed
`HoardScan.ipa` — verified as `Authority=Apple Distribution: Christopher
Phillips (5MTL28V684)`, bundle id `dev.spiffcs.hoard.scan.ios`, version `0.1.0`,
with the privacy manifest present and the file-sharing keys absent.

Two things stood in the way and both are recorded because neither was obvious.

**The account half**, now resolved — an Xcode sign-in and an Apple Distribution
certificate. Until both existed the export failed with:

```
error: exportArchive No Accounts
error: exportArchive No profiles for 'dev.spiffcs.hoard.scan.ios' were found
```

The first line is the cause and the second is noise, the same way it is on the
Debug path. The distinction is worth internalising: **archiving signs from the
keychain, but exporting has to ask Apple for a distribution profile, and
xcodebuild can only ask as an account Xcode is signed into.** A keychain
identity is not an account, so `security find-identity` showing a valid cert
tells you nothing about whether export will work. Separately, there is no
`Apple Distribution` certificate on this machine at all — only
`Apple Development` and two `Developer ID Application` — so export will fail a
second time on the certificate once the account is added.

Three files, sibling-script rather than a flag on `scripts/build-scan-ios.sh`, because
the two paths share only their first four steps and diverge completely after:

- `scripts/scan-ios-common.sh` — the shared half: xcodegen check, `Signing.xcconfig`
  template, `ios_team_id()` (env, then the gitignored xcconfig, never a tracked
  file), project generation, build stamp.
- `scripts/release-scan-ios.sh` — archive → export → validate/upload, with a
  credential preflight that fails *before* archiving so a missing key costs no
  build time.
- `ExportOptions-AppStore.plist` — tracked template with **no team ID**;
  the ID is injected at runtime into a generated copy.

**`altool`, not `notarytool`.** notarytool exists on this machine but has no
iOS mode — it is Developer ID notarization for distribution outside the store.

Two decisions inside it that are easy to get wrong:

- The build stamp moved to **UTC**. `date +%y%m%d.%H%M.%S` is a valid
  `CFBundleVersion` and monotonic — except across the DST fall-back hour, where
  local wall-clock repeats and the upload is rejected for going backwards,
  unrecoverably. Leading zeros are stripped with `10#` arithmetic, which is
  load-bearing: a bare `$((08))` is invalid octal and would abort every build
  between 08:00 and 09:59.
- `manageAppVersionAndBuildNumber = false` in the export plist. It defaults to
  *YES*, which silently rewrites `CFBundleVersion` at export to Xcode's own
  counter — discarding the stamp that ties a build to a moment and a git rev,
  and which `LinkController.swift:323` reports as `appVersion`.

## Chores

| | Current state | What to do |
|---|---|---|
| **Bundle ID** | `dev.spiffcs.hoard.scan.ios` | **Done 2026-08-08.** Registered automatically by `-allowProvisioningUpdates` on the first successful export; no manual step was needed |
| **Team** | The team identifier lives only in gitignored `Signing.xcconfig` — never in a tracked file | Present locally; a release script needs it injected, not read from disk |
| **Deployment target** | iOS 18.0 | Narrow, driven by `RecognizeTextRequest` and `RotationCoordinator`. Lowering means keeping an older Vision path. Probably accept it |
| **Device family** | `TARGETED_DEVICE_FAMILY: "1"` — iPhone only | Fine, and it means no iPad screenshots |
| **Version** | ~~`MARKETING_VERSION 0.1`~~ → `0.1.0`, pointed at by the plist | **Done 2026-08-08.** Deliberately **not** `1.0`: the phone app tracks the Go binary's version, because the two halves are useless apart and a user quoting a version in a bug report should not have to say which one. Build number increments per upload via the UTC stamp; see blocker 4 |
| **~~Info.plist drift~~** | **This entry was wrong.** `Info.plist` is *gitignored* (`.gitignore:64`) — xcodegen regenerates it from `project.yml` every run, so there were never two tracked sources of truth | And the drift ran the other way: a Release build *before* any change already produced `1.0`, because a literal in the plist is not a reference — so `MARKETING_VERSION` was dead config, not the winner. Fixed by making the plist reference `$(MARKETING_VERSION)`, mirroring the `CFBundleVersion` pattern, so they cannot diverge again |
| **Export compliance** | `ITSAppUsesNonExemptEncryption: false` set and verified in a Release bundle | **Done 2026-08-08**, and re-verified honest: `CryptoKit` appears only in `ScanLink/Pairing.swift`, as `HKDF` and `HMAC` — no AES, no ChaChaPoly, no sealed box anywhere. **Revisit if the transport gains payload encryption**; see the ordering note under step 9 |
| **App icon** | `icon-1024.png` is a true 1024×1024 file, but `make-icon.swift` derives it from a 356px full-bleed crop of the 512px `.icns` | Upscaled ~3×. Ships, but a higher-resolution original is a straight improvement |
| **Screenshots** | None | Required for 6.9" iPhone. Awkward: the interesting screen is a camera pointed at a card — and it shows nothing at all until A1 is built |
| **Privacy policy URL** | None | Required. Can be short — the app collects nothing |
| **Support URL** | None | Required |
| **Age rating, category, description, keywords** | None | Category is probably Utilities or Reference |
| **TestFlight** | Never used | Should precede review by some margin. External testers need a beta description, a feedback email, and Beta App Review on the first build of a version |

## What is left before review

Everything through "produce a signed, uploadable build" is **done and
verified** (2026-08-08). `make scan-ios-release` archives, exports, and emits
`HoardScan.ipa` signed by `Apple Distribution: Christopher Phillips
(5MTL28V684)`, carrying the privacy manifest, `ITSAppUsesNonExemptEncryption`,
`CFBundleShortVersionString 0.1.0`, and no file-sharing keys. The App Store
Connect record exists and the bundle ID is registered — `-allowProvisioningUpdates`
registered it during the first successful export, so nobody needs to do it by
hand.

What follows is only what remains. It is grouped by what would stop a
submission, because that is the order it bites in.

### A. Engineering — must be true of the build that ships

**A1. The reviewer has no Mac, and today the app shows them nothing.**
*This is the one most likely to get the app rejected, and it has no owner yet.*
The read pipeline runs entirely on-device, so the app genuinely works alone —
it identifies a card without any network at all. It just never says so:
`SessionView.swift:237` gates the read line behind `developerMode`, so a
reviewer grants camera access, points it at a card, and watches a live preview
do nothing observable. That reads as a broken app.

The fix is small and is the original intent of the old "ungate `lastRead`"
note, which had lost its cross-reference: **show the read result when no Mac is
connected.** The comment that hid it is still correct for the connected case —
`"(nothing read)"` mid-box reads as an error when it is usually a card halfway
onto the mat — and that case is exactly when a Mac *is* driving. Unconnected,
there is no queue to watch and nothing else on screen, so the objection does
not apply. Decide and build before uploading; a demo video in review notes is
a weaker substitute, not an equivalent.

**A2. The TLS transport has never run on hardware.** Shipped in `64b6702`,
green on loopback only. `includePeerToPeer` over AWDL and the iOS
suspend/restart path at `PeerEnds.swift:94` have never been through TLS, and
the USB-C tether is both likeliest to break and most used. **Both ends must be
rebuilt and re-paired together** — a phone on the new build against a Mac on
the old helper cannot pair at all, and it presents as "phone not found."

Session to run before any build goes to a tester: pair over Wi-Fi (code shown,
`hoard scan pair`, Pair tab flips to "1 Mac paired" and the code disappears) →
reconnect without pairing → relaunch the phone, proving a rotated code does not
break the pinned Mac → USB-C tether → background and foreground mid-session →
then a real box of cards.

**A3. `docs/scanner-limits.md` is untracked.** Every accuracy claim in the
store description has to trace to it, and it is not in the repository.

### B. App Store Connect — the fields review will not start without

| | Notes |
| --- | --- |
| **Screenshots, 6.9" iPhone** | Required. Awkward here: the interesting screen is a camera pointed at a card, and it will look like nothing without A1 |
| **Description, subtitle, keywords** | Read [scanner-limits.md](scanner-limits.md) first. §12 and §13 give sentences that are defensible and the overstatements to avoid |
| **Privacy policy URL** | Required. Can be short and true: nothing is collected, nothing leaves the local network |
| **Support URL** | Required. A repository page is acceptable |
| **Category** | Utilities or Reference |
| **Age rating questionnaire** | All-negative answers; nothing here triggers a rating |
| **App privacy ("data collection")** | Answer *no collection*. Verified: no third-party SDKs, no analytics, no accounts, no `.package(url:)` at all |
| **Copyright, contact details** | Trivial, but they block submission |

### C. Upload

**C1. App Store Connect API key**, role **App Manager** — a Developer role
cannot upload. The `.p8` downloads exactly once. Put it in
`~/.appstoreconnect/private_keys/`, `chmod 600`, then export
`HOARD_ASC_KEY_ID` and `HOARD_ASC_ISSUER_ID`. `*.p8` and `private_keys/` are
gitignored.

**C2.** `./scripts/release-scan-ios.sh --validate` first — Apple's pre-upload checks,
no build spent — then `--upload`.

**C3. TestFlight.** Internal testing needs nothing further. **External** testing
needs a beta app description, a feedback email, and a **Beta App Review** on
the first build of each version. Builds expire after 90 days.

### D. Submission

**D1. Review notes.** Explain that the Mac companion is required, that it is
free and open source, and how to reach it. Include the pairing flow. If A1 is
built, the app demonstrates itself and this is context rather than a
workaround.

**D2. Demo video** of pairing, since the reviewer cannot reproduce it.

**D3. Export compliance** is already answered in the build via
`ITSAppUsesNonExemptEncryption: false`, so the per-upload questionnaire is
skipped. **Re-confirm this is still honest**: it was set when the link
encrypted nothing. The link now runs TLS, which is exempt as platform-provided
encryption — but the answer is no longer trivially true and should be stated
deliberately rather than inherited.

### Not blocking, worth doing

- **App icon** is upscaled ~3× from a 356px crop (`make-icon.swift`). Ships as
  is; a higher-resolution original is a straight improvement.
- **`Pairing.swift:9`** still says "the link is TLS with a pre-shared key."
  It is now TLS with a pinned certificate. The sentence has been wrong in two
  different directions and should be fixed once.

## The metadata, ready to paste

Drafted 2026-08-08 against App Store Connect's actual version page. Every
accuracy sentence traces to a measurement in
[scanner-limits.md](scanner-limits.md) §12; nothing here is a claim that
document's §13 tells us to avoid.

**Read this first.** The Fan Content notice below is **mandatory and its wording
is fixed by Wizards** — see [data-licensing.md](data-licensing.md) §7, which
records it as the clearest single gap in that audit. It must appear in the
description. It is not optional and it is not paraphrasable.

### Promotional Text (170 characters, editable without a submission)

161 characters. One paragraph, one line — see the warning below about line
breaks.

```
Point your iPhone at a card and it lands in your collection on your Mac. No typing, no clicking through dropdowns. Reads on device — nothing leaves your network.
```

### Description

**Paste these lines exactly as they are.** App Store Connect **preserves every
newline**, so each paragraph below is deliberately one long unwrapped line — it
will wrap itself to the reader's device. Re-wrapping it to look tidy in an
editor puts hard breaks mid-sentence on the public product page. The blank
lines and the bullet lines are the only intentional breaks.

Note the second paragraph of "WHAT YOU NEED": it is the single most important
sentence for review, because a reviewer without a Mac has to understand
immediately why the app is built the way it is.

```
Hoardling is the camera for hoard, a Magic: The Gathering collection tracker that runs on your Mac.

Put a card on the mat. Hoardling reads its name, set and collector number on the phone, and the card appears in your collection on the Mac. Put the next one down and keep going — no typing, no barcode, no scrolling a dropdown to find the right printing.

WHAT IT DOES

• Reads modern English cards in good light in about a tenth of a second.
• Recognises cards from every frame era, from 1993 originals to current sets.
• On a labelled corpus of 214 English printings spanning every era, it identified the card name on 87% and the exact collector number on 78%.
• When it is unsure it asks rather than guesses: uncertain reads go to a review queue instead of into your collection.
• The foil detector is deliberately conservative — across three test rigs it was correct on 51 of the 52 cards it gave a verdict on. Cards it records on a guess are flagged, and "hoard guessed" lists every one.
• Everything runs on device. No card image leaves your phone and your Mac.

WHAT IT DOES NOT DO

• It reads English cards. Cards printed in other languages will not resolve.
• It does not judge condition, and it does not grade.
• It reads the front face of double-faced cards.
• It cannot tell apart printings that differ only by a variant marker.

WHAT YOU NEED

A Mac on the same Wi-Fi running hoard, which is free and open source. Pairing is a six-digit code shown on the phone and typed on the Mac, once. After that the two recognise each other on their own.

Without the Mac, Hoardling will still read a card and show you what it sees — but there is nowhere to put it. hoard is where a collection lives.

The link between phone and Mac is encrypted and stays on your local network. There are no accounts, no analytics, and no third-party SDKs.

hoard is unofficial Fan Content permitted under the Fan Content Policy. Not approved/endorsed by Wizards. Portions of the materials used are property of Wizards of the Coast. ©Wizards of the Coast LLC.
```

⚠️ **"Hoardling will still read a card and show you what it sees" depends on
A1.** If the unconnected read-out is not built, delete that sentence — it would
be false, and it is exactly the sentence a reviewer will test.

### Keywords (100 character limit, commas, no spaces)

87 characters.

```
mtg,magic,trading card,scanner,collection,inventory,catalog,tcg,binder,cards,price,deck
```

86 characters. Deliberately excludes "Hoardling" and the category name — both
are already indexed, so spending characters on them is waste.

### Support URL / Marketing URL

Both need to exist before submission and neither does yet. The repository's
GitHub page satisfies Support. Marketing URL is **optional** — leave it blank
rather than pointing it at the same page.

### Version / Copyright

- **Version**: `0.1.0` — must match `CFBundleShortVersionString` in the build,
  and it tracks the Go binary rather than App Store convention. See the Version
  row in Chores.

  ⚠️ **Unverified: whether App Store Connect accepts a leading `0`.** The format
  is certainly legal — one to three period-separated non-negative integers — but
  Apple may separately require the first component to be greater than zero for a
  public release. Nobody here has submitted a `0.x` app, so treat this as
  untested. `./scripts/release-scan-ios.sh --validate` runs Apple's own pre-upload
  checks and is the cheap way to find out, once the API key exists. If it is
  refused, the smallest honest answer is `1.0.0` in App Store Connect while the
  binary stays at `0.1.0` — but do not pre-emptively concede the point.
- **Copyright**: `2026 Christopher Phillips`

### Fields that do not apply

Leave every one of these alone; each is a place to accidentally create work.

| Field | Why not |
| --- | --- |
| **Routing App Coverage File** | For apps that give directions. Not this |
| **App Clip** | None, and it would need a build containing one |
| **iMessage App** | The Messages framework is not linked |
| **Game Center** | Not used |
| **Sign-In Information** | **There are no accounts.** Leave the username and password blank and say so in the review notes — supplying a fake credential is worse than none |

### Build → export compliance

Already answered inside the binary: `ITSAppUsesNonExemptEncryption: false` is
in the Info.plist, so the per-upload questionnaire is skipped and **no
documentation upload is required**.

**Confirm this deliberately rather than inheriting it.** The key was set when
the link encrypted nothing. The link now runs TLS. The answer stays `false`
because the exemption covers encryption provided by the operating system, and
the app calls Network.framework and Security rather than shipping its own
cipher — the only hand-written cryptography is the certificate's DER encoding,
which is a data format, not an algorithm. If that reasoning ever stops holding,
this becomes a legal declaration made wrongly, so re-read it before each
submission.

### App Review Information

**Contact**: your name, phone and email. Used only if review has a question.

**Notes** — paste this. Same rule as the description: the paragraphs are
single unwrapped lines on purpose.

```
Hoardling is the camera for hoard, a free and open-source Magic: The Gathering collection tracker that runs on macOS. The phone reads the card; the Mac keeps the collection.

NO ACCOUNT IS REQUIRED. There is no sign-in of any kind, so the sign-in fields are intentionally blank.

TO TEST WITHOUT A MAC:
Launch the app and allow camera access, then point the camera at any Magic card in reasonable light. The card's name, set and collector number appear on screen. This read runs entirely on the device using Vision — no network is involved, and no Mac is needed to see it work.

The "Pair" tab shows a six-digit code used to introduce a Mac running hoard on the same local network. Without a Mac there is nothing to pair with, and the code is inert. An attached video demonstrates the full pairing and scanning flow.

PRIVACY: no data is collected. No card image or scan result leaves the device except to a Mac the user has explicitly paired with, on their own local network. There are no analytics and no third-party SDKs.

hoard for macOS: <repository URL>
```

⚠️ The "TO TEST WITHOUT A MAC" paragraph **is a promise that A1 makes true**.
Do not submit these notes until it is built and verified on a device.

**Attachment**: a demo video of pairing (`.mp4`). Not optional in practice —
the reviewer cannot reproduce the Mac half.

### App Store Version Release

**Manually release this version.** The first release should not go live the
moment it is approved: approval can land overnight, and the Mac helper's
download page and the pairing instructions should be up and correct before
anyone can install the phone app that needs them.

### Screenshots — the size problem, before anyone tries

App Store Connect wants **1242 × 2688** or **1284 × 2778** (or those
transposed). The attached device is an **iPhone 16**, whose screen is
**1179 × 2556**. It cannot produce either size natively, and the Simulator has
no camera, so the screens worth showing cannot be captured there at all.

The aspect ratios are nearly identical — 0.4613 against 0.4622 — so the
practical route is to capture on the real device and rescale:

```
sips -Z 2778 shot.png --out tall.png          # scale to the target height
sips -p 2778 1284 tall.png --out final.png    # pad the ~3px of width
```

Apple checks dimensions, not provenance. Ten slots exist; three or four honest
ones are better than ten padded with variations.

## What is already fine

Worth recording so nobody re-solves it:

- **No shutter-sound hack.** It used to need
  `AudioServicesDisposeSystemSoundID(1108)`, undocumented and unshippable —
  some jurisdictions require a capture sound. Removing the photo path removed
  the problem: the video tap makes no sound to suppress and captures at the same
  4032×3024.
- **Usage descriptions are written and accurate** — `NSCameraUsageDescription`,
  `NSLocalNetworkUsageDescription`, `NSBonjourServices` (`_hoardscan._tcp`).
- **No accounts, no analytics, no third-party SDKs.** Verified against
  `Package.swift`: every target depends only on sibling targets and Apple
  frameworks. There is no `Package.resolved` because there is nothing to
  resolve. The privacy label is close to empty, which is a genuinely strong
  position.
- **The pairing code is in the Keychain**, not a plist (`PairingStore.swift`,
  `SecItem*`).
- **Nothing leaves the local network.** Reads go to a Mac on the same Wi-Fi. No
  servers, nothing to disclose as data collection.
- **The read runs entirely on-device.** `CardKit` imports no networking of any
  kind — Vision and Core Graphics only.

| Where | Spelling |
| --- | --- |
| App Store name | `Hoardling`, subtitle `Card scanner for hoard` |
| `CFBundleDisplayName` | `hoardling` — lowercase, as `hoard` presents itself |
| Prose and UI copy | `Hoardling` — a coined proper noun |
| The macOS helper bundle | `hoard`. It is not Hoardling; only the phone app is |

**Not yet reserved.** See step 1. If it is gone, `Wyrmling`, `Hoard Lens`, and
`Hoardcam` were the runners-up that also showed no exact match.

One thing to know before renaming again: `scan.KindRemote`
(`internal/scan/scan.go`) is what the camera picker shows as a device's
description.

This used to be a cross-language contract — `remoteKind` in
`ScanKit/App/RemoteController.swift` carried the same string, matched by string
equality, and the two had to move together or pairing broke. **That is no longer
true.** The helper was deleted on 2026-08-09 and hoard sets the value itself, so
renaming it is a local change. The name still appears in the phone's
user-facing error text, which is prose rather than a wire value.

## Open questions

1. ~~**Is the App Store even the right channel?**~~ **Answered 2026-08-08: full
   App Store submission.** The question was mis-framed — TestFlight is the
   stage *before* the store, not an alternative to it, which is why it is
   already step 12. Stopping there would have spared only screenshots,
   keywords, category, and age rating; it would not have spared the bundle ID,
   the App Store Connect record, a signed Release archive, export compliance,
   or the privacy manifest, since required-reason validation happens at
   *upload* rather than at review. TestFlight builds also expire after 90 days,
   so testers reinstall on a cadence — a worse product than shipping.
2. **Does the read pipeline's accuracy matter for review?** No. But it matters
   for the description; see step 11.
