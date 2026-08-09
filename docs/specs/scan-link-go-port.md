# Porting the phone link into Go

**Status:** planned, nothing built. Decided 2026-08-09.

**Goal.** `hoard` (the Go TUI) talks to Hoardling (the iPhone app) directly over
`_hoardscan._tcp`. When it does, `bin/hoard-scan.app` and everything that exists
to build it are deleted: the `scan` task, `build-scan.sh`, the `ScanKit` and
`hoard-scan` targets in `scan/hoard-scan/Package.swift`, and the subprocess
plumbing in `internal/scan`.

**This is a port, not a deletion.** Go currently owns no transport to the phone.
`go.mod` has no mDNS dependency; `internal/scan` and `internal/tui` contain no
`net.Listen`, no `tls.`, no socket code of any kind. The entire relationship
between the Go binary and the iPhone is one `exec.CommandContext` in
`internal/scan/session_darwin.go:107`, piping NDJSON over the helper's
stdin/stdout. The helper is not a wrapper around the link — it *is* the link.

---

## 1. Stage 0 — the spike that decides whether this is possible

Do this before writing anything else. It is a day at most, and every other stage
is wasted if it fails.

`build-scan.sh:13` and `scan/hoard-scan/Info.plist:29` both record the same
constraint, in the same words: macOS attributes the Local Network permission to
a **bundle's signed identity**, which is why the helper is assembled into a
`.app` rather than shipped as a bare executable.

A Go CLI has no bundle identity. So:

- Write ~50 lines of Go that browse `_hoardscan._tcp` and print what they find.
- Run it from Terminal.app, from iTerm, from a `make`-spawned shell, and from a
  Homebrew-installed path, with Hoardling advertising on the same network.
- Record for each: does the TCC prompt appear, whose name is on it, and does the
  browse return the phone?

**Pass:** results come back, and the prompt names something a user can
reasonably say yes to. **Fail:** the browse silently returns nothing — the known
macOS failure mode, and the one that looks like a bug rather than a permission.

If it fails, the fallback is a signed `.app` shell that is *only* a TCC identity
(no Swift, no ScanKit) with the Go binary inside it — smaller than today's
helper, but not the clean CLI this port is for. That tradeoff should be decided
with data from the spike, not now.

---

## 2. What has to be ported

Line counts are the current Swift, for scale.

| Swift source | Lines | Go home | Notes |
| --- | --- | --- | --- |
| `ScanWire/FrameCodec.swift` | 149 | `internal/scan/link/frame.go` | 5-byte header. Pure stdlib, no deps |
| `ScanLink/Pairing.swift` | 150 | `internal/scan/link/pairing.go` | HKDF + HMAC; Go 1.26 has both in stdlib |
| `ScanLink/PeerIdentity.swift` | 340 | `internal/scan/link/identity.go` | **Shrinks hard** — `crypto/x509` replaces Security.framework `SecKey` wrangling |
| `ScanLink/PeerTrust.swift` | 228 | `internal/scan/link/trust.go` | TOFU pin set + `VerifyPeerCertificate` |
| `ScanLink/PeerEnds.swift` | 391 | `internal/scan/link/browse.go` | **Browser half only** — the phone advertises, the Mac browses |
| `ScanLink/PeerLink.swift` | 331 | `internal/scan/link/conn.go` | Client half only |
| `ScanKit/App/RemoteController.swift` | 374 | rewrite of `session_darwin.go` | Session lifecycle, reconnect, device pick |

## 3. What does *not* have to be ported — the reason this is tractable

- **`ScanWire/Wire.swift` + `ScanCommand.swift` (495 lines).** Go already parses
  every one of these types: `internal/scan/scan.go` is 429 lines of Event and
  command handling that works today. The port swaps the *source* of NDJSON lines
  from a pipe to a frame stream. Everything downstream — the TUI, autoscan,
  review, auto-commit — is untouched.
- **`PeerListener` (most of `PeerEnds.swift`).** The phone advertises and the Mac
  connects; `PeerEnds.swift:3` states it outright. Go needs the client only.
- **`ScanKit`'s translation layer** (`LineProtocol`, `PhotoDecode`, `RunLoopPump`,
  `CLI.swift`, ~280 lines). These exist to turn frames into stdout lines for the
  Go parent. With no subprocess there is no stdout to write to.
- **The `--mirror` window.** A macOS AppKit preview, with no CLI equivalent. Its
  removal is a deliberate feature loss to confirm, not an oversight.

The `ScanKit` target and the `hoard-scan` executable target disappear entirely.
`CardKit`, `BorderKit`, `ScanWire` and `ScanLink` all stay — the iPhone links
those four, and `Package.swift` already says `ScanKit` is not among them.

---

## 4. The wire contract Go must match exactly

Any mismatch here fails as a hang, not an error, so these are copied from the
source rather than described.

**Framing** (`FrameCodec.swift`). One type byte, then a four-byte big-endian
length, then the payload. Kinds: `0 ndjson`, `1 preview`, `2 still`, `3 trace`.
Payload ceiling 64 MiB; an unverified connection is narrowed to hello size.
Unknown kind is a hard error, never a skip — past that point the reader is
parsing headers out of the middle of a payload.

**Two connections, not one** (`Pairing.swift:126`). `control` and `preview`.
A 200 KB preview JPEG queued ahead of a `scan` event is head-of-line blocking on
exactly the latency the design exists to protect.

**Hello.** First frame on each connection: `{role, session, proof, name}`.

**Proof** (`Pairing.swift`). Key is `HKDF<SHA256>` over the six digits, salt
`dev.spiffcs.hoard.scan.pairing.v1`, 32 bytes out. Proof is
`HMAC-SHA256(session ‖ 0x00 ‖ peerFingerprint)`, base64. The `0x00` separator
and the fingerprint binding are both load-bearing: without the binding, a relay
forwards valid proofs between two honest ends and gets pinned by both. Verify in
constant time.

**Fingerprint** (`PeerTrust.swift:148`). SHA-256 over the certificate DER —
which is `sha256.Sum256(cert.Raw)` in Go, exactly.

**Trust.** TOFU, the Syncthing/KDE Connect shape. Go presents a persistent
self-signed cert and pins the phone's; the phone pins Go's. In Go this is
`tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: …}` doing the pin
check by hand — the standard idiom, and the only way to express "no CA, pinned
leaf."

**A trap worth naming.** `Pairing.swift:76` records that TLS-**PSK** was built
first and abandoned: both ends sat in `.connecting` forever, no error, no
timeout. The shipped design is self-signed cert + fingerprint pinning. Do not
re-derive the PSK route from the pairing code's existence.

---

## 5. Open decisions

1. **mDNS dependency.** Go has no stdlib DNS-SD. Either take a dependency
   (`brutella/dnssd`, `grandcat/zeroconf`, `hashicorp/mdns`) or hand-roll
   PTR→SRV→A/AAAA over `224.0.0.251:5353` (~200 lines). The repo's dependency
   posture is conservative; this is the only new one the port needs.
2. **Where Go's identity and pin set live.** The phone uses the iOS keychain.
   The Mac helper uses the login keychain (`dev.spiffcs.hoard.scan.mac`). A Go
   CLI can use the macOS keychain, or files under hoard's config dir. Files are
   portable and simpler; the keychain matches what exists. Note `PinnedPeers`'
   own argument: a pinned fingerprint is not a secret, but the *set* of them is
   the authorisation list.
3. **`--mirror` removal** — confirm the preview window is acceptable to lose.
4. **Non-macOS.** `scan_other.go` returns `ErrUnsupported` everywhere today
   because the helper was macOS-only. A Go-native link has no such limit, so
   Linux could gain scanning for free. Decide whether that is in scope or
   deliberately deferred.

---

## 6. Order

- **Stage 0** — TCC spike (§1). Gate on it.
- **Stage A** — `frame.go` + `pairing.go`, with table tests against vectors
  generated from the Swift side. Pure functions, no I/O; both are testable to
  completion before anything opens a socket.
- **Stage B** — `identity.go` + `trust.go`. Cross-verify: a Go-generated cert
  fingerprinted by the Swift code and vice versa, so the two agree before either
  trusts the other.
- **Stage C** — `browse.go`. First point real hardware is required.
- **Stage D** — `conn.go`: dial both roles, hello, verify, frames up. Prove it
  against Hoardling with `hoard-scan.app` still present as the reference.
- **Stage E** — rewrite `session_darwin.go` (rename: it is no longer darwin-only)
  onto `link`, keeping `scan.go`'s Event surface unchanged so the TUI does not
  move.
- **Stage F** — live pile session. The scanner's own history says tuning and
  transport changes are only real after one; see `docs/specs/scanner-tuning.md`.
- **Stage G** — the deletion this was all for: `scan` task, `build-scan.sh`,
  `ScanKit` + `hoard-scan` targets, `ScanKitTests`, `internal/scan`'s subprocess
  path, `HOARD_SCAN`, and the `all` task's dependency on `scan`.

Nothing is deleted before Stage F passes. The helper is the reference
implementation while the Go side is being proved against it.
