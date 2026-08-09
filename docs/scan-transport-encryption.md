# Encrypting the link between Hoardling and the Mac

Options for closing blocker 1 in [app-store-release.md](app-store-release.md):
the phone-to-Mac link is authenticated but not encrypted. This is exploration,
not a decision — nothing here has been built.

**Verified 2026-08-08** against the working tree at `02ecda4` plus uncommitted
work, and against the MacOSX26.2 SDK headers on this machine. Claims sourced
from a header or a file are cited; claims that are reasoning rather than
measurement say so; things that could not be checked are marked **unverified**.

---

## Bottom line

Two findings change the shape of this problem from what the release doc assumed.

**The Mac end of the wire is Swift, not Go.** Both ends of the link compile the
same `ScanLink` target from the same source files. Every "can both ends do this
crypto" question answers itself: whatever CryptoKit offers, both ends have, and
a change written once applies to both. The `crypto/tls`-has-no-PSK problem that
would have killed option 1 outright does not exist here, because Go is not on
this wire at all.

**TLS-PSK works. Measured, not argued — see §10.** A twelve-configuration
matrix on loopback establishes a session with
`TLS_ECDHE_PSK_WITH_CHACHA20_POLY1305_SHA256` over TLS 1.2, forward-secret.
Option 1 is viable and is the recommendation.

⚠️ **§3 below reasons from the SDK headers to the conclusion that no PSK
ciphersuite is reachable at any TLS version, and that the failed attempt was
therefore already using the only legal configuration. Measurement refutes
both.** The header reading was correct — `tls_ciphersuite_t` names no PSK suite
— but the inference was not: a raw code point still constructs via
`tls_ciphersuite_t(rawValue:)`, and the stack negotiates it. §3 is left intact
below because the reasoning is worth seeing next to the result that overturned
it. **Read §10 first, and do not act on §3.**

The two real root causes, neither of which appears anywhere above, were: PSK is
a TLS **1.2** feature on Apple platforms, so pinning 1.3 kills the handshake
before the server starts; and the selection block's completion takes the
**identity**, not the key, with the wrong choice failing as "unknown PSK
identity" — an error that points at the peer rather than at the call site.

The listener-selection-block theory was **necessary but not sufficient**:
adding it to a 1.3-pinned configuration still fails, and still fails silently.

**And a warning that cuts across every option:** the six-digit pairing code is
already grindable offline by anyone who captures a handshake, so any encryption
keyed *only* on that code buys almost nothing against the attacker who is
recording the LAN. Every option below either includes an ephemeral key exchange
or does not actually deliver confidentiality. See §4.

---

## 1. What is actually true today

### The topology

The phone listens, the Mac browses (`PeerEnds.swift`, file header). The Mac's
helper is started and stopped per scanning session; the phone sits in a stand
for hours, so the long-lived thing advertises. A session is two TCP connections
matched by a session id — control and preview — because a 200 KB preview JPEG
queued ahead of a `capture` verb is head-of-line blocking on the one latency
that matters (`Pairing.swift`, `PeerRole`).

### Which end is which language

| Leg | Ends | Transport |
| --- | --- | --- |
| `hoard` (Go) ↔ `hoard-scan` (Swift) | same Mac | `os/exec` pipes, NDJSON |
| `hoard-scan` (Swift) ↔ Hoardling (Swift) | over the network | framed TCP, **this document** |

Verified: `grep -n "net\.\|http\.\|Dial\|Listen\|tls\." internal/scan/*.go`
returns nothing. `internal/scan/session_darwin.go` imports `os/exec` and wires
`stdin`/`stdout`; `internal/scan/scan_darwin.go:76` locates
`hoard-scan.app/Contents/MacOS/hoard-scan`. The Go package doc says it plainly:
the phone is reached "through an external macOS helper that translates the
phone's link into a pipe."

The Swift side is one SwiftPM package with a shared spine:

- `ScanWire` — framing and the NDJSON contract. Pure, no I/O.
- `ScanLink` — Network.framework plumbing. **Linked by both ends**: `ScanKit`
  depends on it (`Package.swift`), and the iOS app lists it as a product
  dependency (`scan/hoard-scan-ios/project.yml:50`).
- `ScanKit` — the Mac's end, translating frames to NDJSON on stdout.
- `CardKit` — the phone's read pipeline.

So the entire crypto surface for this problem is three files totalling 742
lines: `Pairing.swift` (122), `PeerEnds.swift` (328), `PeerLink.swift` (292),
plus `ScanWire/FrameCodec.swift` (149). Both ends get every line of it.

### The chokepoint

There is exactly one place parameters are built for the wire:

```swift
// PeerLink.swift:284
func parameters(role: PeerRole) -> NWParameters {
    let params = NWParameters.tcp
    params.includePeerToPeer = true
    if role == .control, let tcp = params.defaultProtocolStack
        .internetProtocol as? NWProtocolTCP.Options {
        tcp.noDelay = true
    }
    return params
}
```

Called by the listener at `PeerEnds.swift:103` and by the connection at
`PeerEnds.swift:312`, and by the loopback test at `LoopbackTests.swift:40`. The
browser builds a bare `NWParameters()` at `PeerEnds.swift:282`, which is correct
— a Bonjour browse does not do TLS.

**This matters for the root-cause analysis.** "The listener did not apply the
same options as the connection" is on the brief's list of suspects. In the
current tree it is structurally impossible: one function, both callers. Whether
that was true at the time of the failed attempt is **unverified** — the code
predates the first commit (§2) — but it is true of anything built from here.

### Confirming the release doc's claims

Both true; the line numbers have drifted.

| Claim in `app-store-release.md` | Actual |
| --- | --- |
| `PeerLink.swift:226` builds `NWParameters.tcp` | `PeerLink.swift:285`. Confirmed plaintext |
| the `.tls` case at `PeerLink.swift:65` is error-mapping only | `PeerLink.swift:68`, inside `LinkFailure.init(_ NWError)`. Maps to "Could not establish a secure link to the iPhone" — a string that can never be produced, because nothing negotiates TLS |

### What the pairing actually buys

Sound, and the release doc describes it accurately. `PairingCode.key`
(`Pairing.swift:53`) is `HKDF<SHA256>` over the six ASCII digits with the fixed
salt `dev.spiffcs.hoard.scan.pairing.v1`, 32 bytes out. `proof()`
(`Pairing.swift:81`) is HMAC-SHA256 of a per-attempt session id under that key,
base64'd. `verifyProof()` (`Pairing.swift:92`) uses
`HMAC.isValidAuthenticationCode`, constant time.

The code is stored in the iOS keychain as a `kSecClassGenericPassword` under
service `dev.spiffcs.hoard.scan.ios`, account `pairing-code`, accessible
`kSecAttrAccessibleAfterFirstUnlock` (`PairingStore.swift`). It is stored as the
six digits, not as the derived key — so the key is re-derived per use and there
is no second copy to rotate.

The gate around it is better than the release doc credits. `PeerEnds.swift`
caps unverified connections at eight, holds pre-hello connections to a 4096-byte
frame ceiling, drops silent connections after five seconds, and arms a
one-second refusal on every failed proof — turning code guessing from wire speed
into one attempt per second, against a listener that only exists during a
session. That is a real online rate limit and it is why six digits is defensible
*for authentication*.

### Comments that will mislead the next reader

Not this document's job to fix, but worth recording since they are the first
thing anyone touching this will read:

- `Pairing.swift:9` — "So the link is TLS with a pre-shared key derived from a
  six-digit code". False. Contradicted seventy lines below in `proof()`'s own
  doc comment, which correctly says the link is plaintext.
- `Pairing.swift:51` — "The real protection is that TLS-PSK requires an *online*
  guess per attempt". The property is real; the mechanism named is not present.
  It is `refuseUntil` in `PeerEnds.swift` that provides it.
- `Package.swift`, `ScanLinkTests` — "a real listener, a real browser and a real
  TLS-PSK handshake in one process". There is no TLS in that test.
- `LoopbackTests.swift:132` — a failure message reading
  `(none — TLS never settled)`. A fossil of the attempt, and a good one: it
  means the harness was built to diagnose exactly this.

---

## 2. What the git history says about the failed attempt

**The TLS-PSK implementation is not recoverable.** It never landed.

`git log --all -S "pre_shared_key"` matches two commits: `16761f4` ("ios: ios
application", the squashed commit that introduced the iOS app) and `72b09fc`.
Inspecting both:

- At `16761f4`, `Pairing.swift` mentions `sec_protocol_options_add_pre_shared_key`
  only in the post-mortem comment that is still there today. `PeerLink.swift` at
  that commit already builds `NWParameters.tcp`. The `-S` hit is the prose, not
  the code.
- `72b09fc` ("fix: update add commands") is the commit that *deleted*
  `docs/sprint-iphone-capture-head.md` — 406 lines. That is the doc the release
  doc still points at. The `-S` hit is the doc's own post-mortem section.

`git log --all --oneline -- scan/hoard-scan/Sources/ScanLink/` lists seven
commits, none of which introduce or remove TLS. `git fsck --lost-found` turns up
two dangling commits (`9d5e107`, `e6ad79b`); neither contains PSK code.

So the working PSK attempt existed only in the working tree before the first
commit of the iOS app. What survives is two prose accounts, and they agree.

### The deleted sprint doc, recovered

From `git show 72b09fc^:docs/sprint-iphone-capture-head.md`, the section "The
link is authenticated but not encrypted":

> TLS-PSK was built first and did not work. With
> `sec_protocol_options_add_pre_shared_key`, a TLS 1.3 ciphersuite
> (`AES_128_GCM_SHA256` — 1.3 folded PSK into the normal handshake, so the
> legacy `TLS_PSK_WITH_*` names are gone) and a permissive verify block, **both
> ends sat in `.connecting` forever** — no error, no timeout, nothing in the
> state handler. Plain TCP over the identical code paths pairs in under a
> second, which is what isolated it to TLS rather than to Bonjour, the framing
> or the hello.

That is the whole record. Four facts to work from: TLS 1.3, ciphersuite
`AES_128_GCM_SHA256`, a permissive verify block, and a hang with no error on
either end.

Worth noting the doc is not lost — it is one `git show` away, and the release
doc's pointer to it is a dead path. Consider restoring it or repointing.

### The doc is worth reading for a second reason

The same file records the `spinRunLoop` deadlock: a check that recorded the
phone's `ready` from a `DispatchQueue.main.async` block dispatched *from* a
block already running on the serial main queue, which cannot run until the outer
one returns. Symptom: "pairing verification timed out every time while the phone
plainly showed connected to hoard." That bug and the TLS hang were live in the
same tree at the same time, and they present identically from the terminal.

**It is not established that the TLS hang was ever isolated from the main-queue
deadlock.** The doc says plain TCP over the identical code paths worked, which
is real evidence — but the deadlock was timing-dependent, and TLS adds a
handshake delay in exactly the window where a race would flip. This is a genuine
possibility that the TLS-PSK attempt was partly or wholly a victim of a bug that
has since been fixed independently (`Flag` polled from the link's own queue,
`RemoteController.swift:128`). **Unverified, and it is a strong argument for
spending the cheap hour in §9 before ranking the options on the old result.**

---

## 3. What the SDK actually permits

Checked directly against
`$(xcrun --sdk macosx --show-sdk-path)/System/Library/Frameworks/Security.framework/Versions/A/Headers/`.

**`tls_ciphersuite_t` contains no PSK ciphersuite.** `grep -i psk
SecProtocolTypes.h` returns nothing. The full enum is the RSA and ECDHE suites,
then exactly three TLS 1.3 suites: `AES_128_GCM_SHA256` (0x1301),
`AES_256_GCM_SHA384` (0x1302), `CHACHA20_POLY1305_SHA256` (0x1303).

Consequences, and they are decisive:

- **TLS 1.3 is the only route to PSK through the public API.** There is no way
  to select `TLS_PSK_WITH_AES_128_GCM_SHA256` via
  `sec_protocol_options_append_tls_ciphersuite`, because that function takes a
  `tls_ciphersuite_t` and no such value exists. "Pin to TLS 1.2 and use the
  legacy PSK suites" — the standard internet advice for this symptom — is not
  available.
- **The previous attempt's configuration was correct.** TLS 1.3 with
  `AES_128_GCM_SHA256` is the only thing it could have been. Two of the brief's
  candidate root causes are refuted by this: the ciphersuite was not missing, and
  there is no TLS-1.2-configured-differently path to have taken instead.

The PSK API surface that does exist, in `SecProtocolOptions.h`:

| Line | API | Side |
| --- | --- | --- |
| 373 | `sec_protocol_options_add_pre_shared_key(options, psk, psk_identity)` | both |
| ~388 | `sec_protocol_options_set_tls_pre_shared_key_identity_hint(...)` | server |
| 439 | `sec_protocol_options_set_pre_shared_key_selection_block(options, block, queue)` | **server** |
| 767 | `sec_protocol_options_set_verify_block(options, block, queue)` | both |

The selection block's signature is the interesting one:

```c
// SecProtocolOptions.h:403, :420
typedef void (^sec_protocol_pre_shared_key_selection_complete_t)(dispatch_data_t _Nullable psk_identity);
typedef void (^sec_protocol_pre_shared_key_selection_t)(sec_protocol_metadata_t metadata,
                                                       dispatch_data_t _Nullable psk_identity_hint,
                                                       sec_protocol_pre_shared_key_selection_complete_t complete);
```

Two async completion blocks in the handshake path — the selection block and the
verify block — each of which must be invoked or the handshake has nowhere to go.

### Root-cause theories, ranked

**1. The listener never set a PSK selection block.** `add_pre_shared_key` is
documented as adding a key to the options; selecting one for an incoming
identity is a *separate* API with its own callback and its own queue. A client
that offers a PSK identity to a server which has no selection path gets no
answer — not a rejection, an absence. That produces a client stuck in
`.connecting` (waiting for ServerHello) and a server stuck in `.connecting`
(waiting for its own configuration to resolve), which is precisely "both ends
sat in `.connecting` forever, no error, no timeout." Nothing in the recovered
post-mortem mentions a selection block, and the natural way to write the code is
to add the PSK symmetrically through the shared `parameters()` function and
assume that is enough. **This is the primary theory.**

**2. The verify block never called its completion handler.** The post-mortem
records "a permissive verify block". `sec_protocol_options_set_verify_block`
takes a queue, and the block receives a `sec_protocol_verify_complete_t` that
*must* be invoked. A block that returns `true`, or returns without calling
complete, or is dispatched to a queue nothing is servicing, hangs the handshake
silently. This is the single most common Network.framework silent-hang in the
wild. A pure PSK TLS 1.3 handshake has no certificate, so arguably the block
should never fire — but "arguably should never fire" is not a thing to bet a
day of debugging on, and it costs one log line to rule out.

**3. PSK identity mismatch.** `add_pre_shared_key` takes an identity blob, and
in TLS 1.3 the client's offered identity is what the server matches on. Passing
an empty identity on one end, or deriving it differently, gives the server
nothing to select — which, combined with theory 1, is the same silence. Cheap to
rule out: log the identity bytes on both ends.

**4. TLS options not actually installed on one end.** *Probably refuted by
construction.* If one end negotiated TLS and the other stayed plain TCP, the
plain end's `FrameReader` would receive a TLS ClientHello whose first byte is
`0x16`, and `FrameKind(rawValue: 0x16)` is nil, so
`FrameCodec.swift:119` throws `unknownKind` and `PeerLink` sets
`.failed` with "The iPhone sent something this version does not understand".
That is a loud, specific error, and it is not what was observed. This reasoning
uses today's codec; whether the codec was identical at the time is
**unverified**.

**5. `includePeerToPeer` interacting with TLS.** No evidence either way.
Peer-to-peer connections run over AWDL, and whether Network.framework's TLS path
behaves identically there is **unverified**. Flagged because it would only show
up on real hardware, which makes it the theory a loopback test cannot kill.

**6. The `spinRunLoop` main-queue deadlock.** See §2. Cannot be ruled out from
the record.

### The Go question, answered and then dismissed

Go's `crypto/tls` has no external-PSK support in the standard library. It has
session-resumption PSKs, which are internal, and the `tls.Config` surface
exposes no way to install an out-of-band pre-shared key. If the Mac end were Go,
option 1 would be dead on arrival and options 3 and 4 would be the only live
ones. **It is not Go** (§1), so this constrains nothing. Recorded so nobody
re-derives it.

---

## 4. The thing that constrains every option

The six-digit code is a fine authenticator and a poor key.

`proof()` is HMAC-SHA256 of a session id under `HKDF(code)`, and the session id
travels in the clear in the same hello frame. An eavesdropper who captures one
hello has an offline verifier for the code: derive, HMAC, compare, one million
candidates, done in well under a second on a laptop. `Pairing.swift:48` already
says this out loud — "nothing fixes six digits against an offline attack" — and
correctly notes it does not matter for authentication, because forging a proof
requires being online against a listener that rate-limits and only exists during
a session.

It matters enormously for confidentiality. **Any scheme whose traffic key is a
pure function of the pairing code gives an attacker who recorded the session the
ability to decrypt all of it**, for the cost of a million HMACs. That is not a
smaller problem than plaintext; it is a slightly more expensive plaintext.

Two ways out, and every option below takes one of them:

- **Ephemeral Diffie-Hellman.** Each side generates a fresh X25519 keypair per
  session; the traffic key comes from the DH output, and the pairing code
  authenticates the exchange rather than keying it. A recorded session stays
  unreadable even if the code is later learned — forward secrecy. This is what
  TLS 1.3 does when PSK is combined with `psk_dhe_ke`, and what Noise's `psk0`
  patterns do.
- **A high-entropy pairing secret.** Replace six digits with something typed
  once and stored — a QR code carrying 32 random bytes, say. `Pairing.swift:14`
  anticipates exactly this: "If this ever leaves a home network, replace the code
  — not the TLS." Bigger product change; solves the problem completely.

**ANSWERED 2026-08-08 by measurement.** The question above was posed about TLS
1.3, which turns out not to be the relevant path at all — see §10. On the TLS
1.2 PSK path that Apple actually implements, the answer depends entirely on
which ciphersuite is requested, and the default is the bad one:

| Requested | Negotiated | Forward secrecy |
| --- | --- | --- |
| nothing (free negotiation) | `0x00A8` `TLS_PSK_WITH_AES_128_GCM_SHA256` | **No.** Traffic keys are a pure function of the pairing key |
| `0xCCAC` | `0xCCAC` `TLS_ECDHE_PSK_WITH_CHACHA20_POLY1305_SHA256` | **Yes** — ECDHE per session |

So option 1 does *not* inherit this problem, provided `0xCCAC` is appended
explicitly. Left to itself the stack settles on plain PSK, which is precisely
the "slightly more expensive plaintext" described above — a recorded session
plus a million HMACs recovers everything. **The ciphersuite append is a
security control, not a tuning knob.** Anyone deleting that line as
unnecessary-looking configuration silently removes forward secrecy, and nothing
about the connection's behaviour changes to say so.

Note the residual: forward secrecy protects a *recording*. An attacker who
grinds the code and is present live can still authenticate. That is the
authentication problem, unchanged, and the high-entropy-secret option above is
still the complete fix for it.

---

## 5. A cross-dependency that must not be missed

> **Whoever is adding `ITSAppUsesNonExemptEncryption: false` to `project.yml`
> must not treat that as settled if any option here except §8.5 is chosen.**

Chore row in `app-store-release.md`, step 5 in its ordering, currently
in flight: add `ITSAppUsesNonExemptEncryption: false` on the grounds that
"authentication-only crypto is exempt." Verified as of this writing the key is
**not yet present** — `grep ITSAppUsesNonExemptEncryption scan/hoard-scan-ios/`
returns nothing in either `project.yml` or `Info.plist`.

That justification is correct *today*, precisely because the link is not
encrypted. `Pairing.swift` uses HKDF and HMAC and nothing else, and
authentication is a recognised exemption. Adding bulk payload encryption changes
the facts the answer rests on:

| Option | What changes | Effect on the answer |
| --- | --- | --- |
| 1 — TLS-PSK | TLS via the OS's own stack | Most likely still `false`. Encryption provided by the operating system, via Apple's own APIs, for a standard protocol, is the best-trodden exemption there is |
| 2 — cert + pinning | same | Same as option 1 |
| 3 — app-layer AEAD | bespoke protocol, CryptoKit primitives | **Genuinely unclear.** CryptoKit is part of the OS, but the *protocol* is proprietary. This is the case where answering `false` is a judgement rather than a lookup |
| 4 — Noise | bespoke protocol, CryptoKit primitives | Same as option 3 |
| 5 — do nothing | nothing | `false` remains straightforwardly correct |

**This is a legal/compliance judgement and I am not qualified to make it. The
above is a flag, not advice.** What I can say concretely:

- The answer is set per-upload and shows up in App Store Connect. Getting it
  wrong is a compliance problem, not a build failure, so nothing will catch it.
- Answering `true` is not a disaster. An app doing standard AEAD over a LAN is
  ordinarily mass-market (ECCN 5D992), which means self-classification rather
  than a CCATS review, plus an annual self-classification report to BIS. France
  has its own declaration. This is paperwork, and it is the paperwork the
  questionnaire exists to route you into.
- **The order in the release doc's step list should change.** Step 5 (add the
  key) currently precedes step 9 (decide the transport). If the transport
  decision lands on option 3 or 4, step 5 has to be revisited. Either move the
  transport decision earlier, or annotate step 5 to say the value is contingent.

---

## 6. What is being protected, honestly

Worth stating before ranking, because it is easy to over- or under-sell.

On the wire, per `ScanWire`: NDJSON events carrying card names, collector
numbers, set codes, raw OCR candidate lines, and — from the Mac back to the
phone — resolved prices and tier information. Preview JPEGs of whatever is under
the camera. Full-resolution stills when a debug dir is set. Trace lines.

The realistic adversary is someone on the same Wi-Fi. What they learn is which
cards a person owns and what those cards are worth. That is inventory data about
objects of real value in a physical location, which is a more interesting
disclosure than "this is a Lightning Bolt" suggests — the aggregate is a
shopping list. It
is still not medical records, and `Pairing.swift`'s framing that authentication
was the property that mattered was a defensible call for a personal tool.

It stops being defensible at the moment the app is on the App Store, because
then the claim is being made to strangers about their networks.

Note also that the link often is not on Wi-Fi at all: `includePeerToPeer = true`
means a USB-C tether or a direct AWDL link, where the exposure is much smaller.
Unknown what fraction of real sessions that is. **Unverified.**

---

## 7. One thing to fix regardless of which option wins

Whatever happens to the transport, the Mac's diagnostic story for a failed link
is actively misleading, and this is why the last attempt failed *silently*.

`RemoteController.verify()` (`RemoteController.swift:119`) waits `verifyTimeout`
for the phone's `ready` event, and on timeout reports:

```swift
fail("The phone did not accept that code. Check the digits on its Pair tab", code: 5)
```

The doc comment justifies it: the phone only sends `ready` after the pairing
check passes, so not seeing it means a wrong code. That inference is sound only
when the transport underneath is known-good. **A TLS handshake that never
completes produces this identical message**, and it names the one cause that is
not the problem. Anyone debugging a future handshake failure will spend their
first hour re-reading six digits off a phone screen.

Minimum fix before attempting any transport change: distinguish "the link never
reached `.ready`" from "the link is ready and `ready` never arrived." The state
is right there — `PeerLink.state` is public and `client.control.state` is
already being read for the loopback test's failure message. Two messages instead
of one, perhaps twenty lines. This is a prerequisite for options 1, 2, 3 and 4
alike, and it is the highest-value twenty lines in this document.

---

## 8. The options

### 8.1 — Retry TLS-PSK on Network.framework

**How it works.** Build the TLS options in `parameters()`, install the derived
key as the PSK on both ends, add a selection block on the listener, and let TLS
1.3 do the rest. The PSK is `PairingCode.key` — the 32 bytes HKDF already
produces — and the PSK identity is a fixed constant, or the session id if a
per-session identity is wanted. Handshake happens below the framing; `PeerLink`
and `ScanWire` are untouched.

Concretely, `parameters()` becomes something like: create
`sec_protocol_options_t` via `NWProtocolTLS.Options()`, call
`sec_protocol_options_add_pre_shared_key(opts.securityProtocolOptions, psk,
identity)`, `sec_protocol_options_append_tls_ciphersuite(...,
tls_ciphersuite_AES_128_GCM_SHA256)`, pin min and max version to TLS 1.3, and on
the listener additionally `sec_protocol_options_set_pre_shared_key_selection_block`
whose body calls `complete(offeredIdentity)` when the identity matches. Build
the parameters as `NWParameters(tls: tlsOptions, tcp: tcpOptions)` rather than
mutating `NWParameters.tcp` — the latter requires inserting into
`defaultProtocolStack.applicationProtocols` at index 0 and getting the ordering
right, which is an avoidable way to lose an afternoon.

**Files and size.** `PeerLink.swift` `parameters()` — but it needs to stop being
one function, because the listener needs the selection block and the client does
not. Probably `parameters(role:code:)` plus a `listenerParameters(code:)`, ~60
lines total. `PeerEnds.swift:103` and `:312` get the code threaded through them
— note `PeerListener` already holds `code` privately, and `PeerBrowser.connect`
already takes one, so no plumbing changes shape. Call it **80 lines across two
files**, plus tests.

**What could go wrong.** It hangs again, which is exactly what happened last
time and why §9 exists. Also: `includePeerToPeer` plus TLS on AWDL is untested
(§3, theory 5) and is the one failure a loopback test cannot reproduce. Also:
whether the negotiated key exchange is forward-secret is unknown (§4).

**Failure mode if misconfigured.** Silent. This is the option's defining
weakness. A wrong identity, an unset selection block, or an uninvoked completion
handler each produce a connection that stays `.connecting` with no error, no
alert and no timeout, and — until §7 is fixed — a Mac-side message blaming the
pairing code. There is no partial success: TLS either completes or the link is
mute.

**Apple.** No entitlement. ATS does not apply — that is a URL-loading policy and
this is a raw `NWConnection`, so `NSAllowsLocalNetworking` and friends are
irrelevant here. `NSLocalNetworkUsageDescription` and `NSBonjourServices` are
already present and correct (`Info.plist:25`, `:31`). Export compliance: most
likely still `false`, being OS-provided TLS (§5).

**Debugging.** The good news is that TLS is legible from outside in a way a
bespoke protocol is not.

- `sudo log stream --predicate 'subsystem == "com.apple.network"' --level debug`
  on the Mac while connecting. Network.framework logs handshake progress and
  names the protocol state it is stuck in.
- `tcpdump -i any -n port <port> -w /tmp/psk.pcap` and look for ClientHello and
  ServerHello. This is the single highest-value signal: it splits "client never
  sent" from "server never answered" from "server answered and client stalled",
  which maps one-to-one onto theories 1–3.
- `print()` inside the selection block and the verify block, on entry and on
  completion. Last time's failure was silent because nothing was instrumented
  inside the two blocks that can swallow a handshake.
- A hard watchdog: `queue.asyncAfter(deadline: .now() + 10)` that logs
  `connection.state` and cancels. `NWConnection` does not time out a stalled TLS
  handshake on its own, which is why "no timeout" appeared in the post-mortem —
  that was not the framework being broken, it was there being nothing to time it.
- `sec_protocol_metadata_get_negotiated_tls_ciphersuite` and friends on success,
  logged once, to record what was actually negotiated (see §4).

### 8.2 — Self-signed certificate with pinning

**How it works.** The phone (the listener) holds a self-signed certificate and
private key; the Mac pins it via `sec_protocol_options_set_verify_block`, with
trust anchored to a fingerprint carried in the pairing exchange rather than to a
CA. Since the phone's hello is the natural place for a fingerprint, and the Mac
speaks first today, this needs a protocol round trip added — or the fingerprint
gets folded into the pairing code, which six digits cannot carry.

**Why this is worse than it sounds.** *There is no public Apple API that
generates an X.509 certificate.* `SecCertificateCreateWithData` parses DER; it
does not produce it. `SecKeyCreateRandomKey` gives a keypair and nothing to wrap
it in. Producing a `sec_identity_t` for `sec_protocol_options_set_local_identity`
therefore requires either hand-writing an ASN.1 DER encoder for a certificate —
a genuinely unpleasant few hundred lines with a long tail of encoding bugs that
manifest as, yes, silent handshake failures — or taking a dependency on
`apple/swift-certificates`.

That dependency is a real cost here, not a nit. `app-store-release.md` lists as
a verified strength: "No accounts, no analytics, no third-party SDKs. Verified
against `Package.swift`: every target depends only on sibling targets and Apple
frameworks. There is no `Package.resolved` because there is nothing to resolve."
This option spends that property, and spends it on the least attractive
mechanism in the document. (I have not exhaustively enumerated the Security
framework for a certificate-generation entry point; **treat "no public API" as
high-confidence rather than proven**.)

**Files and size.** `Pairing.swift` (fingerprint in the hello, plus a reverse
hello), `PeerEnds.swift` (identity on the listener, verify block on the client),
`PeerLink.swift` (`parameters()`), a new certificate-generation file or a new
package dependency. **300+ lines and a dependency**, most of it in service of a
handshake that a PSK does in three API calls.

**Failure mode.** Silent, same as option 1, plus a new class: a certificate that
is subtly malformed is rejected without a useful diagnostic, and DER bugs are
not visible in a packet capture without decoding the cert by hand.

**Apple.** Same as option 1.

**Debugging.** Same tooling as option 1, plus `openssl x509 -in cert.der -inform
der -text -noout` to confirm the generated certificate is well-formed before
blaming the handshake. That is at least a real check the PSK path does not have
an equivalent of.

**Assessment.** Dominated. It solves the same problem as option 1 with strictly
more machinery, and its only genuine advantage — that certificate handshakes are
better-trodden than PSK handshakes — is cancelled by having to hand-build the
certificate that makes it non-standard again.

### 8.3 — Application-layer AEAD over the existing framing

**How it works.** Keep plain TCP. After the pairing proof verifies, both ends
derive symmetric keys and every subsequent frame payload is sealed with
ChaCha20-Poly1305 before `encode()` and opened after `FrameReader` yields it.

Done naively — keys derived from `PairingCode.key` alone — this is **not worth
building**, for the reason in §4: the code is offline-grindable from a captured
hello, so the traffic key is too. The version worth considering adds an
ephemeral exchange:

1. Mac generates `Curve25519.KeyAgreement.PrivateKey`, sends its public key in
   `PeerHello`, and binds it into the proof: HMAC over `session ‖ macPub`
   instead of `session` alone. Binding is what stops a MITM from swapping the
   key; the proof is already the authenticator, it just needs to cover more.
2. Phone verifies the proof (which now covers the Mac's key), generates its own
   ephemeral, replies with a `PeerHelloAck` carrying its public key and an HMAC
   over `session ‖ macPub ‖ phonePub`.
3. Both derive `HKDF<SHA256>(sharedSecret, salt: session, info: role ‖ direction)`
   → four 32-byte keys, one per (connection, direction).
4. Every frame after that: `ChaChaPoly.seal(payload, using: key, nonce:
   Nonce(counter), authenticating: header)`. The 5-byte header goes in as
   additional data so the kind and length are covered.

**Nonces.** 96-bit, a per-direction counter, never transmitted — both ends
count. Never reused because each direction of each connection has its own key
and its own monotonic counter. A subtlety specific to this codebase:
`sendDroppable` (`PeerLink.swift:186`) discards frames *before* sending, so the
counter must increment only on frames that actually reach `connection.send`, or
the receiver's count diverges. And `send` is called from arbitrary threads —
that is why `SendGate` exists — so the counter needs the same lock treatment.
Getting this wrong yields a stream that decrypts for a while and then
permanently fails, which is at least loud.

**Replay and reordering.** TCP gives ordered, exactly-once delivery within a
connection, so the receiver can simply require the counter to equal its
expectation and fail the link otherwise. That covers replay within a session.
Across sessions, the session id in the HKDF salt plus fresh ephemerals means a
recorded frame cannot be replayed into a new session at all.

**Rekeying.** Not needed and worth saying why rather than adding it
speculatively: at 30 frames/second, 2^32 frames is over four years of continuous
scanning. Fail the connection on counter exhaustion; do not build a rekey path
for a case that cannot occur.

**Files and size.**

| File | Change |
| --- | --- |
| `Pairing.swift` | ephemeral keypairs, proof binds the public keys, `PeerHelloAck`, key schedule. ~80 lines |
| new `ScanLink/SealedLink.swift` | seal/open, counters, locking. ~120 lines |
| `PeerLink.swift` | `installKeys(...)`; route `send`/`sendDroppable`/`receive` through it. ~40 lines changed |
| `PeerEnds.swift` | listener seals after `verifyProof` and before `limitPayloads(maxFramePayload)`; client waits for the ack. ~60 lines |
| `FrameCodec.swift` | optionally a `sealed` frame kind. ~5 lines |
| `Tests/ScanLinkTests/` | new. Tamper, replay, counter-gap, wrong-code cases |

Roughly **300 lines across five files**, written once for both ends.

**What could go wrong.**

- *It is hand-rolled crypto.* The primitives are CryptoKit's and are fine; the
  protocol around them is ours, and protocols are where this goes wrong. The
  mitigating facts are that it is small, that it is a well-known shape (§8.4),
  and that the codebase already has a loopback harness that runs both ends in
  one process.
- *Version skew is a live hazard and there is no version field.* `PeerHello` has
  four fields — `role`, `session`, `proof`, `name` — and no protocol version.
  The phone is installed by hand via `make scan-ios-install` while the Mac
  helper is built from the repo, so mismatched builds are the normal state of
  this project, not an edge case. A new Mac talking to an old phone gets no
  `PeerHelloAck` — `Codable` silently ignores the unknown `epk` field — and
  hangs. **Add a version field to `PeerHello` and a real "the phone is running
  an older build" error before shipping any wire change.** This applies to
  option 4 equally.
- *The hello stays in the clear.* It reveals the device name and a session UUID.
  Acceptable — it is what Bonjour already broadcasts.

**Failure mode if misconfigured.** Loud, and this is the option's real
advantage. A key mismatch, a counter divergence or a tampered frame all surface
as an authentication failure from `ChaChaPoly.open`, at a specific frame, on a
line of our own code, where a message can be written. Compare option 1, where
every misconfiguration is the same silence. Given that the last attempt died of
silence, this is not a small consideration.

**Apple.** No entitlement, no ATS interaction. **Export compliance is the open
question** — this is the option that most clearly makes
`ITSAppUsesNonExemptEncryption` a judgement call rather than a lookup (§5).

**Debugging.** Everything is in our own code and can print. Log the derived key
fingerprint (first four bytes of a hash, never the key) on both ends at
handshake — mismatched fingerprints identify a key-schedule bug in one line. Log
sequence numbers on open failure. The loopback test runs both ends in-process,
so a failure is a stack trace rather than a packet capture. A `tcpdump` still
confirms bytes are moving and no longer legible, which is a genuinely satisfying
end-to-end check and something to put in the doc when it lands.

### 8.4 — A Noise handshake

**How it works.** Noise_NNpsk0 or Noise_XXpsk0 over the existing TCP: a
standardised handshake pattern with a specified key schedule, using X25519,
SHA-256 and ChaChaPoly — every one of which CryptoKit provides.

**The honest framing:** §8.3, done correctly, *is* a Noise pattern. Ephemeral
DH on both sides, a pre-shared key mixed in, HKDF chaining, ChaChaPoly transport
keys with per-direction counters — that is Noise_NKpsk0/NNpsk0 with the names
changed. The real choice is not "AEAD or Noise", it is "our key schedule or the
specified one."

**Arguments for the specified one.** The pattern is analysed. The key schedule
has no room for the mistake where a transcript hash omits something it should
have covered. `psk0` mixes the PSK at a defined point rather than wherever
seemed natural.

**Arguments against.** There is no Apple-blessed Noise implementation, and no
established dependency-free Swift one that would not be a new third-party
dependency in a package that has none (§8.2). Implementing Noise correctly from
the spec is *more* code than §8.3, not less — the full state machine, the
handshake-pattern machinery, the transport split — and hand-implementing a
specified protocol has the same category of bug as hand-designing a small one,
minus the ability to keep the whole thing on one screen.

**Files and size.** ~500 lines for a from-spec `NNpsk0`, or §8.3's 300 plus a
dependency.

**Assessment.** The right thing to do here is not "implement Noise" — it is
"write §8.3, then check it against the Noise spec's key schedule and adopt that
schedule where they differ." Same code size, most of the rigour, no dependency.
Treat Noise as the specification §8.3 should be reviewed against rather than as
a separate option.

### 8.5 — Do nothing, and disclose

**How it works.** Ship as-is. Write the truth in the privacy disclosure and in
the app.

**What the disclosure has to say.** Something like: *Hoardling sends card
information to the hoard app on your Mac over your local network. This
connection is authenticated with the pairing code, so only your Mac can connect
— but it is not encrypted. Someone else on the same network who is actively
monitoring it could see the names and prices of the cards you scan. Use a
network you trust, or connect your iPhone to your Mac with a cable.*

That last sentence is doing real work and is worth keeping: `includePeerToPeer`
means a USB-C tether is a supported configuration, and it makes the disclosure
actionable instead of merely alarming.

**Files and size.** Zero code. Store metadata, the privacy policy page, and —
this is the part not to skip — a line in the app's Pair tab, which already has a
troubleshooting section. A disclosure that lives only in a policy page nobody
reads is not really a disclosure.

**What could go wrong.** Nothing technically. The costs are that it is the one
security claim the app cannot make, that App Store review is unlikely to object
but a security-minded user reasonably might, and that a scanning session on a
shop's or a tournament's Wi-Fi genuinely does leak an inventory.

**Apple.** Nothing required. `ITSAppUsesNonExemptEncryption: false` stays
straightforwardly correct (§5). No review risk identified — Apple does not test
for LAN encryption, and the release doc already says so.

**Assessment.** A legitimate answer, and it should stay on the table rather than
be treated as the null option. It is the correct answer if the alternatives all
turn out expensive, and it is strictly better than shipping a half-working
handshake. What makes it uncomfortable is not the risk profile — it is that the
fix looks cheap (§9), so choosing not to do it needs the experiment run first to
justify it.

---

## 9. Recommendation

### Ranking

1. **§8.1, TLS-PSK, retried with the selection block** — *if the experiment
   below passes.* Standard protocol, no hand-rolled key schedule, smallest
   diff (~80 lines), cleanest export-compliance answer, and it puts the crypto
   in Apple's stack rather than ours. The reason it was abandoned is now a
   specific, named, testable theory rather than a mystery.
2. **§8.3, application-layer AEAD with an ephemeral X25519 exchange, key
   schedule reviewed against Noise `NNpsk0`** — the fallback, and a genuinely
   good one. More code (~300 lines), the burden of a hand-rolled protocol, and
   an export-compliance question that becomes a judgement call. Buys something
   real in exchange: **every failure mode is loud and lands on a line of our own
   code**, which given the history is worth a lot.
3. **§8.5, disclose** — correct if 1 and 2 both prove expensive. Not a failure;
   just not the first choice while a one-hour experiment might make option 1
   work.
4. **§8.4, Noise as a separate implementation** — right ideas, wrong packaging.
   Fold its key schedule into option 2 instead.
5. **§8.2, self-signed certificates** — dominated. Only reconsider if PSK proves
   impossible *and* the pinning story becomes necessary for some reason not
   visible today.

### What I would do

**Spend one hour on the experiment before ranking anything for real.** The
argument is not that option 1 is certainly right — it is that the cost of
finding out has collapsed. The old attempt failed against a mystery; there is
now a named primary suspect (§3, theory 1), an SDK-verified constraint that the
configuration was otherwise correct, a test harness that already stands up a real
listener and a real browser in one process, and a plausible confound (§2, the
main-queue deadlock) that has since been fixed independently. Deciding against
option 1 on the strength of a result obtained under those conditions would be
deciding on stale evidence.

If it works, option 1 is the better end state by a clear margin. If it hangs
again with all three callbacks instrumented, that is a decisive result too — go
to option 2 with no further deliberation, and the hour bought the certainty that
the fallback was necessary.

### The smallest experiment

Two stages. Do not skip the second.

**Stage 1 — loopback, ~1 hour.** `Tests/ScanLinkTests/LoopbackTests.swift`
already stands up a `PeerListener`, browses for it, and connects, in one process
(`LoopbackTests.swift:100`). Add a variant that builds TLS-PSK parameters, and
instrument the three things that can swallow a handshake:

- a PSK selection block on the listener that logs the offered identity hint and
  calls `complete(identity)`;
- a verify block on both ends that logs on entry and unconditionally calls
  `complete(true)`;
- a 10-second watchdog that logs `connection.state` on both ends and cancels.

Run it with `swift test --package-path scan/hoard-scan --filter Loopback`
alongside `sudo log stream --predicate 'subsystem == "com.apple.network"'`.

Three outcomes, all informative:

| Result | Meaning | Next |
| --- | --- | --- |
| Session pairs | Theory 1 confirmed — the missing selection block was the bug | Go to stage 2 |
| Hangs, selection block **never logged** | The server side is not reaching PSK selection at all — options wiring, or identity mismatch | Log the identity bytes; if still nothing, abandon option 1 |
| Hangs, selection block logged and completed | Something below the API. Read the packet capture; if ServerHello never appears, abandon option 1 | Option 2 |

Also record, on success, the negotiated key exchange via
`sec_protocol_metadata_t` — the forward-secrecy question in §4 needs an answer
before option 1 can be called finished.

**Stage 2 — real hardware, ~30 minutes.** A loopback pass is necessary and not
sufficient. Two failure modes only exist on real hardware: `includePeerToPeer`
over AWDL (§3, theory 5), and iOS suspending and restarting the listener
mid-session, which `PeerEnds.swift:94` already handles for plain TCP and which
TLS has never been through. Run a real phone and a real Mac on Wi-Fi, then again
on a USB-C tether, then background the app and foreground it. If the tether case
fails, option 1 is out regardless of what loopback said.

**Before either stage: fix §7.** Twenty lines to stop the Mac reporting every
transport failure as a wrong pairing code. Do it first — it is the difference
between an experiment that reports its result and one that repeats history.

---

## Appendix: things this document could not verify

- Whether the original TLS-PSK attempt set a PSK selection block. The code never
  reached a commit (§2) and the post-mortem does not mention it. The primary
  root-cause theory rests on the absence of a mention, which is suggestive and
  not proof.
- Whether the original attempt was confounded by the `spinRunLoop` main-queue
  deadlock that was live in the same tree (§2).
- Whether `parameters()` was a single shared function at the time of the
  attempt, or whether the listener and client were configured separately.
- Whether Network.framework's TLS 1.3 PSK path negotiates `psk_dhe_ke`
  (forward-secret) or `psk_ke`. Not in the headers; needs the metadata read in
  stage 1.
- Whether Network.framework TLS behaves identically over AWDL peer-to-peer.
  Only stage 2 answers this.
- That there is no public Apple API for generating a self-signed X.509
  certificate on iOS (§8.2). High confidence, not exhaustively checked.
- The export-compliance classification of a bespoke AEAD protocol built on
  CryptoKit (§5). Flagged as an open question for the owner; not a question I
  can answer.
- What fraction of real sessions run over a USB-C tether rather than Wi-Fi (§6),
  which is what determines how much the exposure actually matters in practice.

---

## 10. Stage 1 result — measured 2026-08-08

**TLS-PSK works.** The experiment is
`Tests/ScanLinkTests/TLSPSKExperimentTests.swift`, a matrix of twelve
configurations against a raw `NWListener` and `NWConnection` on loopback. Run
it with `swift test --package-path scan/hoard-scan --filter tlsPSKHandshake`.

The working configuration:

```
TLS 1.2 (not 1.3), ciphersuite 0xCCAC appended explicitly,
both ends call sec_protocol_options_add_pre_shared_key,
listener also installs a selection block whose completion is handed
the IDENTITY,
negotiated: 0xCCAC TLS_ECDHE_PSK_WITH_CHACHA20_POLY1305_SHA256, TLS 1.2
```

### Three findings, each isolated by a single-variable comparison

**1. Pinning TLS 1.3 kills it.** Every configuration with
`set_min_tls_protocol_version(.TLSv13)` failed `-9858`, with the server
connection never starting — the client aborts before the listener sees
anything. Removing the pin was the difference between the selection block never
running and running correctly. External PSK on Apple platforms is a TLS **1.2**
feature; §3's reading of `tls_ciphersuite_t` was right about the constraint and
wrong about which direction it pointed. Apple's own forum answer says the same:
PSK "requires TLSv1.2 on Apple platforms."

**2. The selection block's completion takes the identity, not the key.** This
is the bug that cost the most to find and is invisible in review. The header
types the completion parameter as a bare `dispatch_data_t` and names it neither
way, so `complete(key)` and `complete(identity)` are both entirely plausible
readings of an API whose *other* call in the same file
(`add_pre_shared_key(options, key, identity)`) takes both. Passing the key
fails with **`-9864: unknown PSK identity`** — which reads as *the peer* having
offered something unrecognised, sending you to look at the client, the identity
bytes, and the key derivation. All three are fine. The proof is one row of the
matrix against another, identical but for this:

```
TLS 1.2 pinned + complete(identity)  →  PAIRED
TLS 1.2 pinned + complete(key)       →  FAILED -9864 unknown PSK identity
```

**3. The server needs `add_pre_shared_key` as well as the selection block.**
Installing only the selection block fails `-9824`, and the block never runs —
so the server must both hold the key and be able to select it. This refutes the
tidier-looking design where offering is the client's job and selecting is the
server's.

### What this says about the original failure

The design doc's theory 1 — a missing selection block — is **confirmed as
necessary but was not sufficient**. Adding it to an otherwise-1.3-pinned
configuration still fails, and still fails without ever running the block, which
is exactly the "silence, not rejection" signature the post-mortem recorded. Any
attempt that reached for TLS 1.3 because it is the modern choice would have hit
this and had nothing to read. That the old attempt reported a *hang* where this
one reports `-9858` is unexplained; the `spinRunLoop` deadlock noted in §2 is
the most likely reason the failure could not even surface as an error.

### Still to do

- **Stage 2 on real hardware.** Loopback proves the handshake, not the link.
  `includePeerToPeer` over AWDL and the iOS suspend/restart path in
  `PeerEnds.swift:94` have never been through TLS. The USB-C tether case is the
  one most likely to break, and it is the case that matters most.
- **§7 first.** `RemoteController.swift:119` still reports every transport
  failure as "The phone did not accept that code." Attempting stage 2 without
  fixing it is repeating the conditions that made the first attempt
  uninformative.
- **Export compliance.** TLS from the platform's own stack is the easy answer
  and `ITSAppUsesNonExemptEncryption: false` is defensible; §5's warning applied
  to hand-rolled payload encryption, which this is not.
