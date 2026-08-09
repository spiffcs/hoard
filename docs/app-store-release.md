# Shipping Hoardling on the App Store

**Status: audited 2026-08-06; steps 4–8 built and verified 2026-08-08.**
The app installs to a registered device via `make scan-ios-install` and has
never been through TestFlight, App Store Connect, or review. See
[ios-development.md](ios-development.md) for building and running it.

Channel decision, 2026-08-08: **full App Store submission**, with TestFlight as
the stage before it rather than an alternative to it. Open question 1 below is
therefore answered.

What landed on 2026-08-08, all verified against a built Release bundle rather
than asserted:

- The privacy manifest, the export-compliance key, Debug-only file sharing, and
  the version reconcile — steps 4 through 7.
- A release build path — step 8. `make scan-ios-release` archives successfully
  today; it stops at `-exportArchive`, which needs an account and a
  distribution certificate that do not exist yet. See that step for the exact
  error and why it is not a bug.
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

### 1. Transport is authenticated but not encrypted

Confirmed: `PeerLink.swift:226` builds `NWParameters.tcp`. Plaintext. The
`.tls` case at `PeerLink.swift:65` is error-message mapping only — nothing in
the tree ever negotiates TLS.

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

### 4. No release build path — BUILT 2026-08-08, blocked on an account

`make scan-ios-release` exists. **Archiving succeeds.** Export does not, and
the reason is an account, not a defect:

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

Three files, sibling-script rather than a flag on `build-scan-ios.sh`, because
the two paths share only their first four steps and diverge completely after:

- `scan-ios-common.sh` — the shared half: xcodegen check, `Signing.xcconfig`
  template, `ios_team_id()` (env, then the gitignored xcconfig, never a tracked
  file), project generation, build stamp.
- `release-scan-ios.sh` — archive → export → validate/upload, with a
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
| **Bundle ID** | `dev.spiffcs.hoard.scan.ios` | Register on developer.apple.com |
| **Team** | The team identifier lives only in gitignored `Signing.xcconfig` — never in a tracked file | Present locally; a release script needs it injected, not read from disk |
| **Deployment target** | iOS 18.0 | Narrow, driven by `RecognizeTextRequest` and `RotationCoordinator`. Lowering means keeping an older Vision path. Probably accept it |
| **Device family** | `TARGETED_DEVICE_FAMILY: "1"` — iPhone only | Fine, and it means no iPad screenshots |
| **Version** | ~~`MARKETING_VERSION 0.1`~~ → `1.0`, pointed at by the plist | **Done 2026-08-08.** Build number increments per upload via the UTC stamp; see blocker 4 |
| **~~Info.plist drift~~** | **This entry was wrong.** `Info.plist` is *gitignored* (`.gitignore:64`) — xcodegen regenerates it from `project.yml` every run, so there were never two tracked sources of truth | And the drift ran the other way: a Release build *before* any change already produced `1.0`, because a literal in the plist is not a reference — so `MARKETING_VERSION` was dead config, not the winner. Fixed by making the plist reference `$(MARKETING_VERSION)`, mirroring the `CFBundleVersion` pattern, so they cannot diverge again |
| **Export compliance** | `ITSAppUsesNonExemptEncryption: false` set and verified in a Release bundle | **Done 2026-08-08**, and re-verified honest: `CryptoKit` appears only in `ScanLink/Pairing.swift`, as `HKDF` and `HMAC` — no AES, no ChaChaPoly, no sealed box anywhere. **Revisit if the transport gains payload encryption**; see the ordering note under step 9 |
| **App icon** | `icon-1024.png` is a true 1024×1024 file, but `make-icon.swift` derives it from a 356px full-bleed crop of the 512px `.icns` | Upscaled ~3×. Ships, but a higher-resolution original is a straight improvement |
| **Screenshots** | None | Required for 6.9" iPhone. Awkward: the interesting screen is a camera pointed at a card |
| **Privacy policy URL** | None | Required. Can be short — the app collects nothing |
| **Support URL** | None | Required |
| **Age rating, category, description, keywords** | None | Category is probably Utilities or Reference |
| **TestFlight** | Never used | Should precede review by some margin |

## The order to do it in

Roughly dependency-ordered. Steps 1–2 are the two that can invalidate later work
if left late. **Steps 4–8 are done** (2026-08-08); what remains needs an Apple
account, a device, or a decision.

**One ordering correction:** step 5 must not be finished before step 9.
`ITSAppUsesNonExemptEncryption: false` is correct for a transport that encrypts
nothing, and stays correct if step 9 lands on TLS or on disclose-and-ship. It
becomes a genuine judgement call if step 9 lands on application-layer payload
encryption. The key is set today on the strength of the crypto being
authentication-only; treat it as provisional until the transport is decided.

The account work, in the order it unblocks things — items 1 and 2 are the
current hard stop, and everything below waits on them:

1. Sign Xcode into the Apple Developer account (Xcode › Settings › Accounts).
2. Create an **Apple Distribution** certificate. Needs Admin or Account Holder
   on the team. None exists today.
3. Register the bundle ID (step 3 below).
4. Create the App Store Connect record (step 1 below).
5. An App Store Connect API key with the **App Manager** role — Developer
   cannot upload. The `.p8` downloads exactly once; `*.p8` and `private_keys/`
   are now gitignored because altool searches `./private_keys`.

1. **Create the app record in App Store Connect.** This is the only
   authoritative check that the name `Hoardling` is free — a store search sees
   published apps but not unused reservations in other developers' accounts. Do
   it first; everything downstream carries the name.
2. **Ungate `lastRead` when not connected.** **This step is unactionable as
   written and needs a decision.** "See the top of this page" points at nothing
   — no such section exists — and the gate at `SessionView.swift:237` is
   `developerMode`, not connection state, so "when not connected" does not
   describe the code. The comment above it records a deliberate product call:
   both footer lines were hidden because `"(nothing read)"` reads as an error
   mid-box when it is usually just a card halfway onto the mat.

   The underlying worry is presumably real — an App Store reviewer has no Mac
   to pair with, so they open the app, grant camera access, and see a live
   preview that never visibly does anything. Whatever fixes that is the actual
   requirement. Restate it before building it.
3. **Register the bundle ID** `dev.spiffcs.hoard.scan.ios` and confirm the team
   still provisions.
4. **Add `PrivacyInfo.xcprivacy`** with the UserDefaults entry, and wire it into
   `project.yml`.
5. **Add `ITSAppUsesNonExemptEncryption: false`** to `project.yml`.
6. **Decide on the session log** — gate the two file-sharing keys to Debug, or
   keep them and disclose.
7. **Reconcile the versions** — `MARKETING_VERSION` to `1.0` in both
   `project.yml` and `Info.plist`.
8. **Add a release build path** — `make scan-ios-release` doing archive +
   export. Step 4 through 7 all need to actually land in a Release build, and
   nothing today produces one.
9. **Decide the transport question.** Either fix TLS-PSK, or write the privacy
   disclosure that admits plaintext-on-LAN. This gates the disclosure text, not
   the build, so it can run in parallel with 4–8.
10. **Take screenshots** on a 6.9" device.
11. **Write the store metadata** — description, keywords, privacy policy page,
    support page, age rating, category. Read
    [scanner-limits.md](scanner-limits.md) before making any accuracy claim.
    It exists now, and it is stricter than this page's summary was: name reads
    scored **87%** on 231 clean digital scans and that number is *generous by
    construction*, because the corpus scores names leniently with a prefix
    match either way. Foil recall measured **53%** overall with a 23–69% spread
    across three rigs on identical code. Planar cards are not merely unread —
    the aspect gate accepts them, so each one emits a card entry carrying
    rules-box text as its title. Sections 12 and 13 of that doc give sentences
    that are safe to publish and the overstatements to avoid.
12. **Upload to TestFlight** and run a real box of cards through it.
13. **Submit**, with review notes explaining the Mac companion and a video of
    the pairing flow.

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
(`internal/scan/scan.go`) and `remoteKind`
(`ScanKit/App/RemoteController.swift`) both carry the name as a wire value,
matched by string equality, and it is also what the camera picker shows as a
device's description. They move together or pairing breaks.

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
