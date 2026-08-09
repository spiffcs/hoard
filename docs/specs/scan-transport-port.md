# Porting the phone link into Go

**Status:** **Stages 0 and A–F done.** `hoard` finds, pairs with and scans from
the phone with no helper process anywhere in the path, and a 10-minute pile has
been through it (§9, §10). **Only Stage G — the deletion — is outstanding**,
plus two gaps named in §10.3. Written 2026-08-09.

**The spike's headline, because it changes two decisions in this document:** a
bare Go binary *can* do local-network discovery on macOS 15 and *can* open TCP to
a LAN peer — but only through `mDNSResponder`, never through a raw multicast
socket, and **bundling it into a signed `.app` changes nothing either way**. The
bundle-identity premise this port was expected to founder on is not what gates
discovery. §6 and §8.2 carry the corrections.

**Goal.** For release there should be two things: the `hoard` TUI and Hoardling,
the iPhone app. There is a third — `bin/hoard-scan.app`, a macOS-only Swift
helper — and this document is what it actually does, what it would take for
`hoard` to do it directly, and what that costs.

Everything below is cited to a file and a line. Where a claim could not be
settled by reading, it was measured; where it has not been measured, it says so.

---

## 1. What the helper is

It is not a scanner. It owns no camera, no trigger, and no read pipeline —
`RemoteController.swift:11-18` says so outright, and `Package.swift:66-69`
confirms it by dependency: the `ScanKit` target links `ScanWire` and `ScanLink`
and neither AVFoundation nor BorderKit.

The iPhone is the camera and the reader both. It captures, reads the card with
CardKit, and sends finished events.

The helper is a **translator process**. It browses Bonjour for the phone, opens
two TLS connections to it, and turns the phone's binary frames into NDJSON lines
on stdout; verbs arriving on stdin become frames going the other way
(`RemoteController.swift:120-145`, `231-268`, `323-337`).

## 2. How `hoard` reaches the phone today

One `exec.CommandContext`, at `internal/scan/session_darwin.go:102`. Go writes
command lines to the helper's stdin (`capture`, `auto-on`, `rearm`, `chime`,
`torch-on`, `result {json}`, `quit` — `session_darwin.go:185-250`) and scans
JSON lines off its stdout (`session_darwin.go:140-178`). One-shot queries
(`--list-devices`, `--verify`) run the same binary and read its stdout
(`scan_darwin.go:45-71`).

**Go owns no transport.** `go.mod` has no mDNS dependency. `internal/scan` and
`internal/tui` contain no `net.Listen`, no `tls.`, no socket code of any kind.
The real Bonjour and TLS work is Swift, in the `ScanLink` target.

That is the fact that decides the shape of this work: **the helper is not a
wrapper around the link, it is the link.** Deleting it without replacing it
removes scanning entirely. This is a port.

## 3. Why it is smaller than 12,979 lines of Swift suggests

**The phone is the server.** `PeerEnds.swift:3-10` states it, and it is the
opposite of what the process hierarchy suggests — the Mac spawns the helper, so
the Mac feels like the server. It is the wrong way round for this: the phone owns
the camera and sits in a stand for hours, while the helper is started and stopped
per session. The long-lived thing advertises.

So only the **client half** needs porting. `PeerListener` and everything under it
(`PeerEnds.swift:19-292`, the larger half of that file) stays on the phone,
untouched. No iOS change is required by this port at all.

**Go already parses the wire.** `internal/scan/scan.go` is 429 working lines of
`Event` decoding and command construction. `ScanWire/Wire.swift` +
`ScanCommand.swift` (495 lines) have a Go counterpart already. The port swaps the
*source* of NDJSON lines from a pipe to a frame stream; the TUI, autoscan, review
and auto-commit paths do not move.

**The DER hand-rolling evaporates.** `PeerIdentity.swift:19-25` explains its own
length: iOS has no public API that generates a self-signed X.509 certificate, so
the file assembles one byte by byte in ASN.1. Go's `crypto/x509.CreateCertificate`
does this in about twenty lines.

**The translation layer disappears entirely.** `ScanKit`'s `LineProtocol`,
`PhotoDecode`, `RunLoopPump` and `CLI.swift` exist solely to write stdout for a
Go parent. With no subprocess there is no stdout to write to.

| Swift source | Lines | Go home | Note |
| --- | --- | --- | --- |
| `ScanWire/FrameCodec.swift` | 149 | `internal/scan/link/frame.go` | Pure stdlib |
| `ScanLink/Pairing.swift` | 150 | `internal/scan/link/pairing.go` | HKDF + HMAC, both stdlib in Go 1.26 |
| `ScanLink/PeerIdentity.swift` | 340 | `internal/scan/link/identity.go` | **Shrinks hard** — `crypto/x509` |
| `ScanLink/PeerTrust.swift` | 228 | `internal/scan/link/trust.go` | Pin set + `VerifyPeerCertificate` |
| `ScanLink/PeerEnds.swift` | 391 | `internal/scan/link/browse.go` | **Browser half only** |
| `ScanLink/PeerLink.swift` | 331 | `internal/scan/link/conn.go` | Client half only |
| `ScanKit/App/RemoteController.swift` | 374 | rewrite of `session_darwin.go` | Session lifecycle, reconnect, device pick |

---

## 4. The wire contract Go must match exactly

Every mismatch in this section fails as a **hang**, not an error — both ends sit
in `connecting` with nothing on stderr. So these are copied from the source
rather than described from memory.

**Framing** (`FrameCodec.swift:47-53`, `112-135`). One kind byte, then a
four-byte big-endian length, then the payload. Kinds: `0 ndjson`, `1 preview`,
`2 still`, `3 trace`. Payload ceiling 64 MiB.

An unknown kind is a **hard error, never a skip** (`FrameCodec.swift:106-119`).
Past that point the reader is parsing headers out of the middle of a payload; the
stream is already lost.

A reader must be a stream reader, not a decode function
(`FrameCodec.swift:78-82`): a TCP read can deliver half a header, three frames,
or a header now and its payload in four pieces over the next second.

**Two connections, not one** (`Pairing.swift:131-139`). `control` and `preview`.
A 200 KB preview JPEG queued ahead of a `scan` event is head-of-line blocking on
exactly the latency the design exists to protect. Nagle is disabled on control
only (`PeerLink.swift:326-329`) — a `capture` verb is eight bytes and coalescing
it would add up to 40 ms to a shutter press.

Note the receive side: the Mac must handle **every kind on both connections**.
`RemoteController.swift:127-136` records a bug where the preview callback dropped
non-preview frames on the floor, and fixture stills sent there arrived and
vanished with no error on either end.

**Hello.** The first frame on each connection (`Pairing.swift:143-150`):

```
{"role": "control"|"preview", "session": "<uuid>", "proof": "<base64>", "name": ""}
```

Sent on TLS-ready and **not before** (`PeerEnds.swift:369-385`). Relying on
`NWConnection` queueing sends made before the handshake completes was true and is
no longer sufficient: the proof commits to the peer's certificate fingerprint,
which does not exist until TLS has settled. A hello sent early carries a proof
bound to nothing, and the far side reports it as a wrong pairing code.

**Proof** (`Pairing.swift:53-59`, `94-106`). The key is `HKDF-SHA256` over the
six digits, salt `dev.spiffcs.hoard.scan.pairing.v1`, 32 bytes out. The proof is:

```
base64( HMAC-SHA256( key, session ‖ 0x00 ‖ peerFingerprint ) )
```

Both the `0x00` separator and the fingerprint binding are load-bearing. Without
the separator a session id ending in fingerprint-shaped bytes could be re-split
to authenticate a different pair of values. Without the binding a proof says only
"I know the code", which a relay forwards verbatim between two honest ends —
completing TLS with each separately — and both ends pin the relay. With it, the
proof says "I know the code *and* I am looking at the certificate whose
fingerprint is this", which the relay cannot produce.

The sender binds the fingerprint it **observed**; the verifier checks against its
**own** certificate, which it knows independently (`PeerEnds.swift:206-214`).

Verify in constant time. `Pairing.swift:108-124` uses
`HMAC.isValidAuthenticationCode` rather than `==` for this; Go's equivalent is
`hmac.Equal`.

**Fingerprint** (`PeerTrust.swift:146-149`). SHA-256 over the certificate's DER.
In Go that is exactly `sha256.Sum256(cert.Raw)`.

**Identity** (`PeerIdentity.swift:114-133`, `157-206`). P-256,
ECDSA-with-SHA-256, self-signed, X.509 v3 with **no extensions**, common name set
to the service string, 10-year lifetime, notBefore backdated 300 s.

Shape is unconstrained beyond parsing: the peer's verify block ignores everything
the certificate asserts about itself and authorises by fingerprint alone
(`PeerTrust.swift:180-201`), so `basicConstraints` and SANs would be decoration.
The common name is deliberately *not* the device name — it travels in the clear
on every handshake (`PeerIdentity.swift:135-139`).

**Trust — TOFU**, the Syncthing / KDE Connect shape. There is no certificate
authority in a house and inventing one would be worse than not having one
(`PeerTrust.swift:1-8`). Each end mints its certificate once and pins the
other's.

In Go this is `tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: …}`
doing the pin check by hand — the standard idiom, and the only way to express "no
CA, pinned leaf".

Two constraints that are easy to miss:

- The phone sets `peer_authentication_required` (`PeerTrust.swift:174-178`), so
  Go **must present a client certificate**. A `tls.Config` with no
  `Certificates` fails the handshake.
- The pin set is read **live per handshake**, not snapshotted
  (`PeerTrust.swift:99-112`). A Mac pinned during this session must be trusted on
  its next connection without rebuilding anything.

**The pairing window.** `acceptUnknown` is open only during verify
(`RemoteController.swift:63-81`) — that is, only when a person has just typed a
code. During a scanning session the phone must already be pinned, so an impostor
cannot complete a handshake at all rather than being turned away a layer later.

**Once pinned, the code is not sent again.** `PeerEnds.swift:196-220`: a peer
whose fingerprint is already pinned skips the proof entirely. This is what makes
the phone's rotating code possible, and it means a Go port must tolerate the
pairing code being *wrong or absent* on an already-paired link.

**A trap worth naming.** `Pairing.swift:76-80` and `PeerIdentity.swift:10-16`
both record that TLS-PSK was built first and abandoned: with
`sec_protocol_options_add_pre_shared_key`, a TLS 1.3 ciphersuite and a permissive
verify block, both ends sat in `.connecting` forever — no error, no timeout.
Plain TCP over identical code paths paired in under a second, which is what
isolated it. Do not re-derive the PSK route from the pairing code's existence.

## 5. What is deliberately lost

**AWDL peer-to-peer.** Both ends set `params.includePeerToPeer = true`
(`PeerLink.swift:325`, `PeerEnds.swift:334`). `PeerLink.swift:302-303` says what
that buys: it "is what makes this work with no shared Wi-Fi network at all, and
over a USB-C tether's link-local interface."

Go sockets cannot do AWDL — it is a Network.framework capability with no
POSIX-socket equivalent. The two paths split:

- **Same Wi-Fi** — ordinary mDNS + TCP. Unaffected.
- **USB-C tether** — a real link-local interface with an IP address. mDNS over it
  works with ordinary sockets. Unaffected in principle, **unverified in
  practice**, and this is the path `docs/specs/app-store-release.md:306` calls
  both "likeliest to break and most used". It must be on the Stage F session.
- **No network at all** — lost. This is the AWDL-only case.

`hoard-scan.app` has never run its TLS transport on hardware either
(`app-store-release.md:303-305`: shipped green on loopback only), so the tether
path is unproven on *both* implementations. The port does not regress it; it
inherits an open question.

**Nothing else.** The `--mirror` preview window is not a loss — it was already
deleted, and `RemoteController.swift:19-26` records why: no preview frame was
ever sent, so it shipped as a permanently black rectangle. Do not resurrect it
without the phone-side sender in the same change.

## 6. Decisions taken

**mDNS: ~~hand-rolled~~ — SUPERSEDED by measurement, see §8.2.**

The decision taken before the spike was to hand-roll the browse as PTR → SRV →
A/AAAA over `224.0.0.251:5353` via `net.ListenMulticastUDP`, roughly 200 lines
and no new dependency — matching the Swift side's deliberate zero-dependency
posture (`PeerIdentity.swift:26-33`) and `go.mod`'s conservative 13 direct
requirements.

**It does not work on macOS 15.** Raw multicast now requires an Apple-granted
managed entitlement; the spike measured sends failing with EHOSTUNREACH and zero
packets received on a busy network, with no prompt ever offered. §8.2 has the
evidence and the three surviving options. What stands from the original decision:
only the browse half is needed, and advertising stays on the phone.

**Identity and pin storage: files, not the keychain.** Beside `scan.json` in
hoard's config dir (`internal/command/scan.go:105-111`), which the Go side
already owns and already writes pairing codes into. Portable, and it keeps the
identity path off the Security framework. (It was also chosen to keep cgo out of
the module entirely; §8.2 may reintroduce cgo for discovery regardless, but that
argues for fewer cgo call sites, not more.)

Mode `0600`, and the reason is `PinnedPeers`' own argument
(`PeerTrust.swift:33-36`): a pinned fingerprint is not a secret — it is a public
hash — but the **set** of them is the authorisation list, and an attacker who can
add an entry to it needs none of the rest of this.

Note this is a *weakening* against the helper, which keeps both in the login
keychain (`RemoteController.swift:49-54`). A file is readable by anything running
as the user; the keychain at least gates on unlock. Accepted deliberately: the
same directory already holds the pairing codes in cleartext
(`internal/command/scan.go:100`), so the file is not a new exposure, and the
private key is the only genuinely new secret in it.

## 7. Order of work

Nothing is deleted before Stage F passes. The helper is the reference
implementation while the Go side is being proved against it — which is the whole
reason the port comes first and the deletion second.

- **Stage 0** — the TCC spike. **Done, 2026-08-09 (§8).** Passed, by a different
  route than expected; it also settled the mDNS API choice that Stage C depends
  on. One item it did *not* settle stays open: `_hoardscan._tcp` has never been
  browsed with the phone present (§8.3).
- **Stage A** — `frame.go` + `pairing.go`. **Done, 2026-08-09.**
  `internal/scan/link/`, cross-verified against vectors generated by the real
  Swift code; 15 tests green, `go vet` and `-race` clean, nothing else in the
  tree touched. Two things worth carrying forward:
  - **Go's `crypto/hkdf` agrees with CryptoKit's `deriveKey` on the first try**,
    with `info: ""` matching CryptoKit's omitted info. That was the single most
    likely place for a silent mismatch and it is now pinned by vector.
  - **The `FrameReader` accumulator has no counterpart and is not missing.**
    Swift needs ~60 lines of it because Network.framework *pushes* bytes — a
    read can deliver half a header. Go pulls, so `io.ReadFull` is the whole
    answer. `iotest.OneByteReader` proves the property rather than assuming it.
- **Stage B** — `identity.go` + `trust.go`. **Done, 2026-08-09.** Cross-verified
  in both directions, and the two ends agree:
  - **Security.framework parses a Go-minted certificate and computes the
    identical fingerprint** using `PeerTrust.swift`'s own `fingerprint(of:)` —
    `57a8e0…c729` from both sides, subject CN read back intact. This was the
    direction that gated the port: a certificate the phone cannot parse is a
    handshake that hangs.
  - **Go parses a certificate `PeerIdentity.swift`'s own
    `selfSignedCertificate` produced** and agrees on its fingerprint — which is
    also the first time that hand-rolled ASN.1 encoder has been checked against
    a parser other than Apple's.
  - Both certificates are structurally identical where it matters: X.509 v3,
    ECDSA-with-SHA-256, P-256, self-signed, **zero extensions**, positive serial.
  - Pinning is tested through a **real TLS handshake**, not by calling the
    verify function directly — the logic is installed via `crypto/tls`, and a
    test that bypasses that proves the function works without proving it is
    wired up. The server half sets `RequireAnyClientCert`, so the tests would
    fail if Go stopped presenting a certificate, which is what the phone's
    `peer_authentication_required` demands.
- **Stage C** — `browse.go`, over `dns-sd` behind the `Finder` interface per
  §8.2. **Done and proved live, 2026-08-09** — see §9.
- **Stage D** — `conn.go`: dial both roles, hello, verify, frames up.
  **Done and proved live, 2026-08-09** — see §9. `hoard-scan.app` was not
  involved in any part of it.
- **Stage E** — rewrite the session onto `link`, keeping `scan.go`'s `Event`
  surface unchanged so the TUI does not move. **Done, 2026-08-09.**
  `session_darwin.go`, `scan_darwin.go` and `scan_other.go` are deleted;
  `session.go` and `client.go` replace them with no build tags. The `Event`,
  `Device` and `HUDResult` types and both TUI interfaces are untouched, so
  `internal/tui` did not move. `ErrHelperMissing` — "hoard-scan helper not
  found; build it with ./build-scan.sh" — is gone, because there is nothing left
  to build.

  One simplification fell out: **`scan.json`'s codes map is gone.** Under trust
  on first use the code authorises exactly one exchange and the phone burns it
  immediately, so storing it was keeping a spent secret. `NeedsPairing` now
  reads from the pin set — "do I hold this phone's certificate fingerprint" —
  which is the condition that actually governs whether a session can open,
  rather than a proxy for it.
- **Stage F** — live pile session. **Done over Wi-Fi, 2026-08-09 (§10); the
  USB-C tether is still unproven.**
- **Stage G** — the deletion this was all for.

**Stage G deletion list:**

| Target | Path |
| --- | --- |
| Build script | `build-scan.sh` |
| Task | `scan:` (`Taskfile.yaml:163`) **and `all`'s dependency on it** (`Taskfile.yaml:47`) |
| SwiftPM targets | `ScanKit`, `ScanKitTests`, `hoard-scan` (`Package.swift:69,75,76`) |
| Sources | `Sources/ScanKit/`, `Sources/hoard-scan/`, `Tests/ScanKitTests/` |
| Bundle assets | `scan/hoard-scan/Info.plist`, `scan/hoard-scan/hoard-scan.icns` |
| Go subprocess path | `internal/scan/scan_darwin.go` entire; the `exec`/pipe plumbing in `session_darwin.go` |
| Go symbols | `ErrHelperMissing` (`scan.go:414`), the `HOARD_SCAN` lookup (`scan_darwin.go:79`) |
| Doc references | `docs/specs/ios-development.md:117,137`, `pre-public-review.md:77`, `scanner-tuning.md:15,1240`, `release-engineering.md:231` |

**Stays.** `CardKit`, `BorderKit`, `ScanWire` and `ScanLink` — the iPhone links
all four (`Package.swift:33-40`). `cardkit-probe` and its Taskfile targets — that
is the corpus and fixture harness, unrelated to the link.

**Changes source rather than disappearing.** `HOARD_SCAN_LOG` telemetry
(`session_darwin.go:113-126`). The phone's `trace` frames currently reach the log
by way of the helper re-emitting them on stderr
(`RemoteController.swift:254-260`); after the port they come straight off the
frame stream. The `!` stderr prefix loses its meaning; `<` and `~` keep theirs.

**Also gone, and worth saying out loud:** `ErrUnsupported` on non-darwin
(`scan_other.go`) exists *only* because the helper was macOS-only. A Go-native
link has no such limit, so Linux would gain scanning essentially for free.
**Open decision:** in scope, or deliberately deferred so the port is judged on
one platform at a time.

---

## 8. Stage 0 — the TCC spike

> The premise stated in this section is what the spike set out to test. It did
> not survive contact — see the Findings below before relying on any of it.

`build-scan.sh:11-15` and `scan/hoard-scan/Info.plist:28-31` independently record
the same constraint, in the same words: macOS attributes the Local Network
permission to a **bundle's signed identity**, which is why the helper is
assembled into a `.app` rather than shipped as a bare executable. That is also
why `runCLI` sets `NSApplication.activationPolicy` before browsing
(`CLI.swift:88-93`) — the browse, it says, needs the bundle to be an app.

Note that these are two source comments making the same claim, which is weaker
evidence than it looks: they are one belief written down twice, not two
independent observations. Nothing in the repository measures it.

A Go CLI has no bundle identity. Either its grant attaches to the responsible
process — the terminal — or the browse silently returns nothing, which is the
macOS failure mode that reads as a bug rather than a permission.

This was the gating risk, and it is not the crypto. It could not be settled by
reading — so it was measured.

**The spike.** A standalone Go program that opens a multicast UDP socket on
`224.0.0.251:5353`, sends a PTR query, and prints every PTR / SRV / A record that
comes back; plus, once the first result came in, three follow-ups to work out
*which* part was blocked — the same binary inside a signed `.app`, the
`DNSServiceBrowse` API via cgo, and plain outbound TCP to a LAN peer.

**The design point that made it readable.** A silent empty result is
indistinguishable from "nothing is advertising", so the probe binds the multicast
group on port 5353 itself rather than an ephemeral port. It therefore receives
*every* mDNS packet on the segment, and "saw 40 packets from 9 hosts, none of them
`_hoardscan._tcp`" is a different answer from "saw nothing at all". Only the
second implicates the permission. `dns-sd -B _services._dns-sd._udp` ran alongside
as an independent control.

This mattered: the phone was never present, and the spike still returned a clear
verdict, because the *other* devices on the network were the control.

### Findings — run 2026-08-09, macOS 15.6 (24G84)

**The phone was not present for these runs**, so `_hoardscan._tcp` end-to-end is
still unproven. It did not need to be: the gating question is whether a bare Go
binary can do local-network discovery at all, and the network was busy enough to
answer that on its own. `dns-sd -B _services._dns-sd._udp` listed a dozen service
types (`_airplay`, `_raop`, `_smb`, `_companion-link`, `_rfb`, …) throughout.

Host: en0 up at 192.168.1.175, `MULTICAST` flag set, route to `224.0.0.251`
present in the table. Terminal responsible process: `com.cmuxterm.app`.

| Path | Result |
| --- | --- |
| General egress (HTTPS) | 200 — networking is not blocked |
| Unicast UDP → LAN host `192.168.1.1:53` | write succeeded |
| Outbound TCP → LAN host, ports 80/443/53 | **all three connected** |
| Raw multicast **send** → `224.0.0.251:5353` | `sendto: no route to host` (EHOSTUNREACH) |
| Raw multicast **join** on explicit `en0` | **succeeded** — IGMP membership accepted |
| Raw multicast **receive**, 5 s on a busy network | **0 packets, 0 hosts** |
| Same Go binary inside an ad-hoc-signed `.app` with `NSLocalNetworkUsageDescription` + `NSBonjourServices` | **0 packets — identical** |
| `DNSServiceBrowse` via cgo, bare unbundled binary | **2 real instances found** |
| `DNSServiceResolve` → host + port, same binary | **resolved to `<host>.local.:49722`** |
| `net.LookupHost` on the resolved `.local.` name | **returned v4 + v6 addresses** |
| TCC prompt, at any point, in any run | **never appeared** |

Runs were repeated with Claude Code's own sandbox disabled and were byte-for-byte
identical, so the sandbox is not the cause.

**Three conclusions, and two of them overturn decisions in this document.**

**1. Hand-rolled mDNS over raw multicast does not work on macOS 15.** This is not
a TCC prompt the user can accept — no prompt is ever offered. The socket opens,
the multicast join is *accepted*, and the kernel then delivers nothing and
refuses sends with EHOSTUNREACH. Direct multicast requires
`com.apple.developer.networking.multicast`, a **managed entitlement Apple grants
only on written application**, and it is separate from and stricter than the
Local Network privacy control. §6's decision to hand-roll ~200 lines of
PTR→SRV→A/AAAA is therefore not viable on the one platform that has to work.

**2. Bundle identity is not what gates discovery.** This is the load-bearing
premise of `build-scan.sh:11-15` and `Info.plist:28-31` — the stated reason the
helper is assembled into a `.app` at all — and the measurement does not support
it, in either direction. The same binary bundled and signed with the same plist
keys as the helper got exactly the same nothing; and `DNSServiceBrowse` from a
bare, unbundled binary found real devices immediately. Whatever the `.app` is
buying, it is not the browse.

That also kills the fallback this document proposed. A signed `.app` shell around
the Go binary would not have helped, and it is now measured rather than assumed.

**3. The port is viable, by a different route than planned.** The **whole
discovery chain** works from a bare, unbundled binary — browse → resolve →
hostname → IP addresses — which matters because browse alone yields a service
name, not something dialable. Discovery goes through `mDNSResponder`, the same
path `NWBrowser` sits on; this is the API the Swift helper has been using all
along, reached differently. And everything after discovery already works
unentitled: outbound TCP to a LAN peer connected first try, three times.

So every link in the chain is now measured except one:

| Link | Status |
| --- | --- |
| Find the phone (browse + resolve + address) | measured working, bare binary |
| Open TCP to it | measured working, bare binary |
| TLS, self-signed certs, fingerprint pinning | Go stdlib; no OS permission involved |
| Framing, HKDF, HMAC proof | Go stdlib; pure functions |
| Parse the phone's events | **already exists** — `internal/scan/scan.go` |
| Phone-side changes | **none** — it is already the server |
| `_hoardscan._tcp` with the phone present | **not yet measured** (§8.3) |

(A note for whoever writes `browse.go`: `DNSServiceResolve` is a *continuous*
query, not a one-shot. The probe's callback fired four times for one instance.
Cancel it on the first usable result or the caller sees duplicates.)

### 8.2 The mDNS decision, reopened

§6 chose hand-rolling. That is now measured as impossible on macOS 15. The three
remaining options, none of them free:

**a. `DNSServiceBrowse` / `DNSServiceResolve` via cgo.** The supported API, proved
working above. Cost: **cgo on the darwin build**, and that is not a small ask —
`.goreleaser.yaml:6` records that the module is pure Go on purpose (`modernc.org/
sqlite` was chosen for it) and all three release targets build `CGO_ENABLED=0`
(`.goreleaser.yaml:11,23,35`). Turning cgo on for darwin breaks cross-compiling
the darwin artifacts from Linux CI.

**b. Shell out to `/usr/bin/dns-sd` and parse it.** No cgo, no dependency, and it
demonstrably works from this terminal. It reintroduces a subprocess — but not the
kind the user is trying to eliminate: `dns-sd` is an OS component, not something
hoard ships, builds, signs, or asks anyone to install. "The TUI and Hoardling are
the only two things" survives that distinction intact. Cost: parsing a
human-readable CLI table, which is a real fragility.

**c. Keep a Swift helper, but only for discovery.** A far smaller binary than
today's — browse and resolve, print JSON, exit — with Go owning TLS, framing,
pairing and the session. Preserves the status quo's worst property (a second
build artifact) while removing most of its size.

Recommendation: **(b) first, (a) if parsing proves fragile.** (b) is the only
option that both ships one binary and keeps the release pipeline as it is; it can
be swapped for (a) behind the same internal interface without touching anything
above `browse.go`.

### 8.3 Still outstanding

- **`_hoardscan._tcp` with the phone present.** Everything above used other
  devices' services as the probe. Re-run `cgobrowse _hoardscan._tcp` and
  `dns-sd -B _hoardscan._tcp` with Hoardling open before Stage C is called done.
- **The USB-C tether path**, which is the one `app-store-release.md:306` calls
  most used and likeliest to break, and which no implementation has ever run.
- **Whether `dns-sd`/`DNSServiceBrowse` still work from a distributed binary.**
  These runs were all from one terminal on a machine that has been developing
  this software; a fresh Mac may prompt where this one did not. The absence of a
  prompt here is not evidence that no prompt exists.
- ~~**What the `.app` was actually buying.**~~ **Answered 2026-08-09, see §8.4.**

### 8.4 What the `.app` is buying: nothing that discovery needs

The open question from the first run — the Go binary cannot browse bundled or
unbundled, yet the helper works in the field, so what does the bundle do? — was
put to the API the helper actually uses.

A bare `swift nwbrowse.swift`, no bundle, no `Info.plist`, no
`NSApplication.setActivationPolicy`, running `NWBrowser` over
`.bonjour(type:domain:)` with `includePeerToPeer = true` — the same construction
as `PeerEnds.swift:332-336` — **found real devices immediately.**

So the constraint recorded in `build-scan.sh:11-15` and `Info.plist:28-31` does
not describe macOS 15.6's behaviour, for Swift any more than for Go. Discovery
through `mDNSResponder` needs no bundle; raw multicast is blocked *with* one. The
bundle is orthogonal to both.

Two caveats keep this from being a licence to delete the bundle today:

- **No prompt was ever offered in any run**, so this says nothing about *first
  contact* on a machine that has never granted Local Network access. The bundle
  may still determine what a prompt says, and whether one appears at all. Every
  measurement here is from a development machine with existing state.
- **`LSMinimumSystemVersion` is 14.0** (`Info.plist:26`). The behaviour was
  measured on 15.6 only, and the comment being wrong now does not mean it was
  wrong when written.

What this does settle: the bundle is not load-bearing for the *port*. Stage G can
delete it on the same evidence as everything else — after Stage F, and after one
run on a Mac that has never seen this app.

### 8.5 Regenerating the cross-check vectors

`internal/scan/link/testdata/vectors.json` is not hand-written and must never be
hand-edited. It is emitted by compiling **byte-identical copies** of
`ScanWire/FrameCodec.swift` and `ScanLink/Pairing.swift` into a single scratch
module together with a generator, which is what makes `proof()` and
`PairingCode.key` — both `internal`, so unreachable from another module —
callable without touching the repository's own targets.

The distinction that makes this worth the trouble: the expected values come from
the code the phone runs, not from someone reading that code and writing the same
values down a second time. A misreading reproduced on both sides is a test that
passes and a link that hangs.

To regenerate after a change to either Swift file:

1. Copy the two sources into a scratch SwiftPM package as one executable target
   (verify with `shasum` that the copies match the originals).
2. Add a `main.swift` that emits the JSON — framing constants and kind numbers,
   `encode` output for payloads either side of the 128/256/65536 boundaries, one
   multi-frame stream, derived keys, proofs both bound and unbound, the negative
   binding cases, and the encoded hello.
3. `swift build -c release && ./.build/release/vectorgen > vectors.json`.
4. Copy over `testdata/vectors.json` and run `go test ./internal/scan/link/`.

If a regenerated file changes any existing value, that is a **wire-breaking
change to the phone protocol**, not a test to update: every paired phone on the
old build stops being able to complete a handshake.

**`testdata/certvectors.json`** is generated the same way, with one unavoidable
difference. `selfSignedCertificate` is `private`, so file-scoped, and cannot be
called even from the same module — the caller has to be *appended to a copy of
`PeerIdentity.swift` itself*. Everything above the appended marker stays
byte-identical; the addition generates an **ephemeral** key pair
(`kSecAttrIsPermanent: false`), so regenerating writes nothing to any keychain.

The generator takes a Go certificate as DER hex, which comes from:

```
HOARD_LINK_DUMP_CERT=1 go test ./internal/scan/link/ -run TestDumpIdentityForInterop -v
```

and records, for that certificate: whether `SecCertificateCreateWithData`
parsed it, what `PeerTrust.swift`'s `fingerprint(of:)` computed, and what
subject Security read back. It then mints one of the phone's own and records the
same. Both directions are then checked by `interop_test.go` **with no Swift
toolchain present**, so the suite still runs on Linux CI.

### 8.6 Reproducing

Probe sources are in the session scratchpad, not the repo — `tccspike/`
(hand-rolled multicast, ~330 lines, the seed `browse.go` would have been),
`cgobrowse/` + `cgoresolve/` (DNSServiceBrowse/Resolve via cgo), `tcptest/`, `nwbrowse.swift` (bare NWBrowser). Rebuild with
`go build`; `cgobrowse` needs `CGO_ENABLED=1` and `-framework CoreServices`.

---

## 9. Stages C and D, proved against hardware

2026-08-09, macOS 15.6, iPhone running Hoardling build `260809.1550.46`, both on
the same Wi-Fi. **`bin/hoard-scan.app` took no part in any of this** — it was
never launched, and the Go binary has no bundle, no `Info.plist` and no
entitlement.

| Step | Result |
| --- | --- |
| Browse `_hoardscan._tcp` | found `"Billionaires are Parasites"` |
| Resolve | `b43085fa-…-….local:56573` |
| TCP dial | connected, over IPv6 link-local `fe80::…%en0` |
| TLS + pairing proof, first contact | phone accepted a certificate it had never seen |
| Both connections matched | phone traced `session adopted` |
| `ready` from the phone | `features: torch, hud, border, auto, rearm` |
| Pin, then **reconnect with no code** | succeeded, pairing window closed |
| Command up the link (`chime`) | sent, no session error |

### What the hardware taught that reading did not

**The phone's address is not stable, and must never be cached.** Across two runs
minutes apart the hostname *and* port both changed —
`e6225d49-…local:56571` then `b43085fa-…local:56573`. iOS rotates its private
`.local` hostname and takes a fresh ephemeral port per launch. Every session
must re-resolve.

**The instance name is stable, and is the right key.** `"Billionaires are
Parasites"` survived both runs unchanged. That matters twice over: it is what
`scanPrefs.Codes` is already keyed by (`internal/command/scan.go:100`), so
existing pairings carry across the port, and it is a *three-word* name, which
exercised the instance-name-is-the-last-column parsing that a single-word test
device would have left unproven.

**The link came up over IPv6 link-local**, not IPv4 — `fe80::…%en0`, zone and
all. Nothing downstream may assume a v4 address.

**Reconnect without a code works**, which is the property the whole
trust-on-first-use design rests on (`PeerEnds.swift:196-220`): the phone burns
the code once paired, so if a pinned peer could not reconnect silently, the
second session of every install would fail.

### Still outstanding

- **Stage E** — the `Event` surface on top of frames, replacing the subprocess.
- **Stage F** — a live pile session, including the USB-C tether, which remains
  unproven on *both* implementations (`app-store-release.md:303-306`).
- **A fresh Mac.** Every measurement here is from a machine that has been
  developing this software. No TCC prompt has ever appeared; that is not
  evidence that none will on a machine seeing `_hoardscan._tcp` for the first
  time.

---

## 10. Stage F — a live pile over the Go link

2026-08-09, 18:39–18:50 local. `./hoard add` against a scratch database, one
phone, Wi-Fi. Telemetry via `HOARD_SCAN_LOG`.

### 10.1 That it really was the port

Worth establishing rather than assuming, because a stale `hoard` on `PATH`
would have looked identical from the outside. The old helper writes
`scan: helper build <stamp>` to its stderr on every ready
(`RemoteController.swift:248-250`), which the log tees with the `!` prefix.
**That line appears zero times.** The `!` lines present — `session adopted`,
`stills on`, `trigger tuned` — are the phone's own trace frames arriving
directly off the wire, which is the path only the port has.

### 10.2 What the session did

| | |
| --- | --- |
| Duration | 10 min 42 s, one session, **one** `ready` — no reconnects |
| Scan events | 146 |
| Committed | 49 |
| Queued for review | 12 |
| Dropped as duplicate / nudge echo | 41 |
| Killed (nothing identifiable) | 14 |
| **Link errors, transport failures** | **0** |
| Tier mix | 28 bulk, 19 win, 8 review |

Latency, from the session's own timing lines:

| | min | p50 | p90 | max | mean |
| --- | --- | --- | --- | --- | --- |
| Whole loop (ms) | 317 | **459** | 1026 | 1588 | 543 |
| Network leg (ms) | 9 | **54** | 610 | 1188 | 165 |

A 54 ms median network leg over Wi-Fi, with no dropped connections in ten
minutes of continuous scanning, is the transport working. The p90 of 610 ms is
the still-transfer path, not the control path — control frames are the small
ones and Nagle is off on that leg only.

The cards were deleted afterwards by hand, so the database is empty by
intent; the write path is unchanged by this port and was never in question.

### 10.3 What Stage F did *not* cover

- **The USB-C tether.** Unproven here and unproven on the Swift helper too
  (`app-store-release.md:303-306` calls it "likeliest to break and most used").
  This is the single largest remaining risk in the transport, and it is
  inherited rather than introduced.
- **A fresh Mac.** Every measurement in this document comes from a machine that
  has been developing this software for weeks. No TCC prompt has ever appeared;
  that is not evidence that none will on first contact elsewhere.
