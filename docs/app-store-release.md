# Shipping Hoardling on the App Store

**Status: audited 2026-08-06, nothing attempted.**
The app installs to a registered device
via `make scan-ios-install` and has never been through TestFlight, App Store
Connect, or review. See [ios-development.md](ios-development.md) for building
and running it.

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
forever with no error. See `docs/sprint-iphone-capture-head.md`.

Apple will not test for this. It matters because the privacy disclosure would
have to admit it, and because it is the one security claim the app currently
cannot make.

### 2. No privacy manifest

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

### 3. The session log is world-readable

`UIFileSharingEnabled: true` and `LSSupportsOpeningDocumentsInPlace: true` are
both set (`project.yml`), so `Documents/session-log.txt` is visible in the Files
app. That log contains card names and prices, and `SessionLog.startSession()`
only truncates it when a Mac connects — a phone that never pairs accumulates
forever.

Right call for a development build. Before shipping, either gate both keys
behind the Debug config or keep them and say so in the privacy disclosure.
Gating is cleaner: `project.yml` already splits settings per config.

### 4. No release build path

`build-scan-ios.sh` hardcodes `-configuration Debug` and produces
`.build/Build/Products/Debug-iphoneos/HoardScan.app`. There is no `archive`, no
`-exportArchive`, and no upload step anywhere in the repo. `project.yml` does
define a `Release` config, so nothing structural is in the way — the script just
has never needed it.

Needed: `xcodebuild archive` → `-exportArchive` with an `app-store-connect`
export plist → `xcrun altool`/`notarytool` upload, or the same three steps
through Xcode's Organizer. A `make scan-ios-release` target is the natural home.

## Chores

| | Current state | What to do |
|---|---|---|
| **Bundle ID** | `dev.spiffcs.hoard.scan.ios` | Register on developer.apple.com |
| **Team** | `[team-id-redacted]`, in gitignored `Signing.xcconfig` | Present locally; a release script needs it injected, not read from disk |
| **Deployment target** | iOS 18.0 | Narrow, driven by `RecognizeTextRequest` and `RotationCoordinator`. Lowering means keeping an older Vision path. Probably accept it |
| **Device family** | `TARGETED_DEVICE_FAMILY: "1"` — iPhone only | Fine, and it means no iPad screenshots |
| **Version** | `MARKETING_VERSION 0.1`, `CURRENT_PROJECT_VERSION 1` | Marketing version should be `1.0` at submission; build number must increment on every upload |
| **Info.plist drift** | `Info.plist` says `CFBundleShortVersionString 1.0`, `project.yml` says `MARKETING_VERSION 0.1` | Xcode's build setting wins, so the shipped value is `0.1`. Harmless today, confusing at release — make them agree |
| **Export compliance** | CryptoKit HKDF + HMAC, no `ITSAppUsesNonExemptEncryption` key | Add `ITSAppUsesNonExemptEncryption: false` to `project.yml`'s `info.properties`. Authentication-only crypto is exempt, and the key skips the questionnaire on every upload |
| **App icon** | `icon-1024.png` is a true 1024×1024 file, but `make-icon.swift` derives it from a 356px full-bleed crop of the 512px `.icns` | Upscaled ~3×. Ships, but a higher-resolution original is a straight improvement |
| **Screenshots** | None | Required for 6.9" iPhone. Awkward: the interesting screen is a camera pointed at a card |
| **Privacy policy URL** | None | Required. Can be short — the app collects nothing |
| **Support URL** | None | Required |
| **Age rating, category, description, keywords** | None | Category is probably Utilities or Reference |
| **TestFlight** | Never used | Should precede review by some margin |

## The order to do it in

Roughly dependency-ordered. Steps 1–2 are the two that can invalidate later work
if left late.

1. **Create the app record in App Store Connect.** This is the only
   authoritative check that the name `Hoardling` is free — a store search sees
   published apps but not unused reservations in other developers' accounts. Do
   it first; everything downstream carries the name.
2. **Ungate `lastRead` when not connected.** See the top of this page. One
   condition, and it is what makes the app reviewable.
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
    support page, age rating, category. Read `docs/scanner-limits.md` before
    making any accuracy claim: foreign-language cards do not resolve, and planes
    and full-art cards with no printed title cannot be read at all.
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

1. **Is the App Store even the right channel?** The app is only useful to people
   already running hoard on a Mac. TestFlight alone. 10,000 testers, a public
   link, no review beyond the first build — might serve the actual audience with
   a fraction of the overhead. Worth deciding before step 4, not after step 12.
2. **Does the read pipeline's accuracy matter for review?** No. But it matters
   for the description; see step 11.
