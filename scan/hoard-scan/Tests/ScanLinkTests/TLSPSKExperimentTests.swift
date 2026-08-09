// Does Network.framework's TLS-PSK actually complete a handshake between two
// ends we control?
//
// This is stage 1 of the experiment in docs/scan-transport-encryption.md, and
// it exists because the answer is currently unknown rather than known-no. An
// earlier TLS-PSK attempt was abandoned when both ends sat in `.connecting`
// forever with no error, but that attempt never reached a commit, so there is
// no code to read — only a post-mortem, in a doc that has since been deleted
// (recover it with `git show 72b09fc^:docs/sprint-iphone-capture-head.md`).
//
// The design doc's primary suspect was that the *listener* never installed a
// PSK selection block, which is a separate API from the one that offers a key
// and is required on the server side. That theory is now measured, and the
// answer is more interesting than yes or no — see the matrix below.
//
// This is a MATRIX rather than a single case, because the first run produced a
// named TLS failure rather than the historical hang, and "the handshake dies
// before PSK selection" has several candidate causes that are cheap to vary
// and expensive to reason about. One run over N configurations localises the
// cause; N runs each changing one variable is the same information at N times
// the wall clock, and invites stopping early on the first green.
//
// Three places can swallow a TLS handshake without a word, and all three are
// instrumented for every configuration:
//
//   1. the listener's PSK selection block — never called means the server side
//      is not reaching PSK negotiation at all;
//   2. either end's verify block — entered but never completed means the
//      handshake is parked on us, not on the protocol;
//   3. the connection state machine itself — which reports `.preparing` for
//      both "mid-handshake" and "stuck forever".
//
// Deliberately NOT going through PeerListener/PeerBrowser. Those call the
// shared `parameters(role:)`, and changing it would mean changing production
// code to run an experiment whose result might be "abandon this approach".
// Raw NWListener on loopback also isolates the TLS question from Bonjour
// resolution — if PSK cannot handshake here, discovery is irrelevant, and if
// it can, discovery is the next test rather than a confound in this one.

import CryptoKit
import Foundation
import Network
import Testing

@testable import ScanLink

/// One point in the configuration space.
///
/// Every field is something the previous attempt could plausibly have had
/// wrong, and every one of them is a line of setup that looks equally
/// reasonable written either way.
private struct PSKConfig {
    var name: String
    /// Pin the floor to TLS 1.3. The SDK exposes no PSK ciphersuites from the
    /// 1.2 era, so 1.3 is the only reachable PSK path — but pinning the floor
    /// and negotiating freely are different things, and one of them may refuse
    /// to produce a suite at all.
    var pinTLS13 = true
    /// Restrict to a single ciphersuite. Narrowing can turn "no mutually
    /// acceptable suite" into a hard failure that a free negotiation avoids.
    var appendCiphersuite = true
    /// Install verify blocks. With PSK there is no certificate to evaluate, so
    /// this may be inert — or it may make the stack expect a peer certificate
    /// that PSK never sends.
    var verifyBlocks = true
    /// The server offers a key with `add_pre_shared_key` as well as selecting
    /// one. Plausibly wrong: that call is how a *client* offers, and a server
    /// doing both may advertise a PSK identity it should only be answering.
    var serverAddsKey = true
    /// The server installs a selection block. The design doc's theory 1.
    var selectionBlock = true
    /// What the selection block hands to its completion: the identity, or the
    /// key. The header types both as a bare `dispatch_data_t` and names the
    /// parameter neither way, so the call site looks correct written either
    /// way — and passing the wrong one produces "unknown PSK identity", which
    /// reads as *the peer* offering something unrecognised rather than as this
    /// end answering the wrong question.
    var completeWithIdentity = false
    /// Pin the floor to TLS 1.2 explicitly, rather than leaving it unset.
    var pinTLS12 = false
    /// A ciphersuite by raw code point, for suites Apple's `tls_ciphersuite_t`
    /// does not name. The plain-PSK suite that free negotiation settles on has
    /// no ephemeral exchange at all, so a recorded session stays decryptable by
    /// anyone who later recovers the key — and the key is six digits. The
    /// ECDHE_PSK suites (RFC 8442) are the fix if the stack will negotiate one.
    var customCiphersuite: UInt16?
}

/// Everything the handshake did, recorded from the callbacks that can swallow
/// it. A class with a lock because the TLS callbacks arrive on their own
/// queues, and a data race here would be indistinguishable from the hang the
/// test is trying to characterise.
private final class PSKProbe: @unchecked Sendable {
    private let lock = NSLock()
    private var events: [String] = []

    private(set) var selectionCalled = false
    private(set) var clientVerifyCalled = false
    private(set) var serverVerifyCalled = false

    func note(_ s: String) {
        lock.lock()
        events.append(s)
        lock.unlock()
    }

    func selectionFired(identityHint: String) {
        lock.lock()
        selectionCalled = true
        events.append("listener: PSK selection block called, identity hint \(identityHint)")
        lock.unlock()
    }

    func verifyFired(_ side: String) {
        lock.lock()
        if side == "client" { clientVerifyCalled = true } else { serverVerifyCalled = true }
        events.append("\(side): verify block entered, completing true")
        lock.unlock()
    }

    var log: String {
        lock.lock()
        defer { lock.unlock() }
        return events.isEmpty
            ? "      (no callback ever fired)"
            : events.map { "      " + $0 }.joined(separator: "\n")
    }
}

/// `Data` as the `dispatch_data_t` the sec_protocol APIs want.
private func dispatchData(_ data: Data) -> DispatchData {
    data.withUnsafeBytes { DispatchData(bytes: $0) }
}

/// The PSK identity. Not secret — it travels in the ClientHello in the clear,
/// and its only job is telling a server which key to select. A fixed string is
/// right: the session id would make the two ends disagree, since the client
/// picks it after TLS is already up.
private let pskIdentity = Data("dev.spiffcs.hoard.scan.psk.v1".utf8)

private func pskParameters(
    code: PairingCode, isListener: Bool, config: PSKConfig,
    probe: PSKProbe, queue: DispatchQueue
) -> NWParameters {
    let tls = NWProtocolTLS.Options()
    let sec = tls.securityProtocolOptions

    let key = code.key.withUnsafeBytes { Data($0) }
    let keyDD = dispatchData(key)
    let identityDD = dispatchData(pskIdentity)

    if config.pinTLS13 {
        sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv13)
    }
    if config.pinTLS12 {
        sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv12)
        sec_protocol_options_set_max_tls_protocol_version(sec, .TLSv12)
    }
    if config.appendCiphersuite {
        sec_protocol_options_append_tls_ciphersuite(sec, tls_ciphersuite_t.AES_128_GCM_SHA256)
    }
    if let raw = config.customCiphersuite, let suite = tls_ciphersuite_t(rawValue: raw) {
        sec_protocol_options_append_tls_ciphersuite(sec, suite)
    }

    if !isListener || config.serverAddsKey {
        sec_protocol_options_add_pre_shared_key(
            sec, keyDD as __DispatchData, identityDD as __DispatchData)
    }

    if isListener, config.selectionBlock {
        sec_protocol_options_set_pre_shared_key_selection_block(
            sec,
            { _, identityHint, complete in
                let hint = identityHint.map { hint -> String in
                    var bytes = Data()
                    (hint as DispatchData).enumerateBytes { buf, _, _ in
                        bytes.append(contentsOf: buf)
                    }
                    return String(data: bytes, encoding: .utf8) ?? "\(bytes.count) non-utf8 bytes"
                } ?? "(none offered)"
                probe.selectionFired(identityHint: hint)
                complete((config.completeWithIdentity ? identityDD : keyDD) as __DispatchData)
            },
            queue)
    }

    if config.verifyBlocks {
        sec_protocol_options_set_verify_block(
            sec,
            { _, _, complete in
                probe.verifyFired(isListener ? "server" : "client")
                complete(true)
            },
            queue)
    }

    let params = NWParameters(tls: tls, tcp: NWProtocolTCP.Options())
    // Matches production's control-channel parameters so a pass here is
    // evidence about the real link rather than about a laboratory one.
    if let tcp = params.defaultProtocolStack.internetProtocol as? NWProtocolTCP.Options {
        tcp.noDelay = true
    }
    return params
}

private func waitForPSK(_ seconds: Double, _ check: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while !check(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    return check()
}

private func describe(_ state: NWConnection.State?) -> String {
    guard let state else { return "(never started)" }
    switch state {
    case .setup: return "setup"
    case .waiting(let e): return "waiting(\(e))"
    case .preparing: return "preparing"
    case .ready: return "ready"
    case .failed(let e): return "failed(\(e))"
    case .cancelled: return "cancelled"
    @unknown default: return "unknown"
    }
}

private func isTerminal(_ state: NWConnection.State?) -> Bool {
    switch state {
    case .failed, .waiting: return true
    default: return false
    }
}

private struct Outcome {
    var paired: Bool
    var clientState: String
    var serverState: String
    var selectionCalled: Bool
    var log: String
    var negotiated: String?
}

/// Stands up one listener and one connection on loopback under `config`, and
/// reports what happened. Never throws on a handshake failure — a failure is a
/// result here, not an error.
private func attempt(_ config: PSKConfig) -> Outcome {
    let code = PairingCode.random()
    let probe = PSKProbe()
    let queue = DispatchQueue(label: "hoard-scan.test.psk.\(config.name)")

    var serverState: NWConnection.State?
    var clientState: NWConnection.State?
    var serverConn: NWConnection?
    var serverReady = false
    var clientReady = false
    var listenerReady = false

    guard let listener = try? NWListener(
        using: pskParameters(
            code: code, isListener: true, config: config, probe: probe, queue: queue),
        on: .any)
    else {
        return Outcome(
            paired: false, clientState: "-", serverState: "-",
            selectionCalled: false, log: "      NWListener could not be constructed",
            negotiated: nil)
    }

    listener.newConnectionHandler = { conn in
        serverConn = conn
        conn.stateUpdateHandler = { st in
            serverState = st
            probe.note("server: \(describe(st))")
            if case .ready = st { serverReady = true }
        }
        conn.start(queue: queue)
    }
    listener.stateUpdateHandler = { st in
        if case .failed(let err) = st { probe.note("listener: failed(\(err))") }
        if case .ready = st { listenerReady = true }
    }
    listener.start(queue: queue)
    defer { listener.cancel(); serverConn?.cancel() }

    // Wait for `.ready`, not merely for a non-nil port. `listener.port` reports
    // the requested `.any` — that is, 0 — from the moment it is constructed,
    // so polling for non-nil returns instantly with a port nothing is bound to,
    // the client then dials port 0, and the resulting EADDRNOTAVAIL reads as a
    // transport failure that has nothing to do with TLS. It cost one run here,
    // and it is exactly the kind of confound this experiment exists to keep out
    // of the PSK answer.
    guard waitForPSK(5, { listenerReady }), let port = listener.port else {
        return Outcome(
            paired: false, clientState: "-", serverState: "-",
            selectionCalled: false, log: "      the listener never became ready", negotiated: nil)
    }

    let client = NWConnection(
        host: .ipv4(.loopback), port: port,
        using: pskParameters(
            code: code, isListener: false, config: config, probe: probe, queue: queue))
    client.stateUpdateHandler = { st in
        clientState = st
        probe.note("client: \(describe(st))")
        if case .ready = st { clientReady = true }
    }
    client.start(queue: queue)
    defer { client.cancel() }

    // Six seconds is far past a loopback handshake, so reaching it means stuck
    // rather than slow — and a stuck run is itself a distinct result from a
    // failed one, which is why `isTerminal` is checked separately.
    _ = waitForPSK(6) { (clientReady && serverReady) || isTerminal(clientState) }

    var negotiated: String?
    if clientReady, serverReady,
       let md = client.metadata(definition: NWProtocolTLS.definition) as? NWProtocolTLS.Metadata {
        let suite = sec_protocol_metadata_get_negotiated_tls_ciphersuite(md.securityProtocolMetadata)
        let version = sec_protocol_metadata_get_negotiated_tls_protocol_version(md.securityProtocolMetadata)
        negotiated = "ciphersuite \(suite.rawValue), version \(version.rawValue)"
    }

    return Outcome(
        paired: clientReady && serverReady,
        clientState: describe(clientState),
        serverState: describe(serverState),
        selectionCalled: probe.selectionCalled,
        log: probe.log,
        negotiated: negotiated)
}

@Test("TLS-PSK completes a handshake over loopback", .timeLimit(.minutes(3)))
func tlsPSKHandshake() throws {
    // Ordered cheapest-hypothesis-first, but all of them run: the point is the
    // shape of the whole matrix, not the first green cell. A single passing
    // configuration answers "is option 1 viable"; the pattern across the rest
    // answers "and what was wrong before", which is what stops this being
    // re-litigated in six months.
    let configs: [PSKConfig] = [
        PSKConfig(name: "baseline (everything on)"),
        PSKConfig(name: "no verify blocks", verifyBlocks: false),
        PSKConfig(name: "server selects only, does not add", serverAddsKey: false),
        PSKConfig(name: "server selects only, no verify", verifyBlocks: false, serverAddsKey: false),
        PSKConfig(name: "free ciphersuite negotiation", appendCiphersuite: false),
        PSKConfig(name: "no version pin", pinTLS13: false),
        PSKConfig(name: "minimal: keys + selection only",
                  appendCiphersuite: false, verifyBlocks: false),
        PSKConfig(name: "no selection block (the old shape)", selectionBlock: false),
        // The row above that reached PSK selection did so only once the 1.3
        // pin came off, and then failed on "unknown PSK identity" — so these
        // vary the two things that result implicates: what the completion is
        // handed, and whether 1.2 wants to be asked for explicitly.
        PSKConfig(name: "no pin + complete(identity)",
                  pinTLS13: false, completeWithIdentity: true),
        PSKConfig(name: "no pin + free suite + complete(identity)",
                  pinTLS13: false, appendCiphersuite: false, completeWithIdentity: true),
        PSKConfig(name: "TLS 1.2 pinned + complete(identity)",
                  pinTLS13: false, appendCiphersuite: false,
                  completeWithIdentity: true, pinTLS12: true),
        PSKConfig(name: "TLS 1.2 pinned + complete(key)",
                  pinTLS13: false, appendCiphersuite: false, pinTLS12: true),
        PSKConfig(name: "TLS 1.2, server selects only, complete(identity)",
                  pinTLS13: false, appendCiphersuite: false, serverAddsKey: false,
                  completeWithIdentity: true, pinTLS12: true),
        // Forward secrecy. The suite free negotiation lands on is plain PSK:
        // the traffic keys come from the pairing key alone, with no ephemeral
        // exchange, so one recorded session plus one recovered code decrypts
        // everything — and the code is a million possibilities. These four ask
        // whether the stack will negotiate an ECDHE_PSK suite instead, which
        // would make a recording worthless without the live handshake.
        PSKConfig(name: "ECDHE_PSK AES_128_GCM (0xD001)",
                  pinTLS13: false, appendCiphersuite: false,
                  completeWithIdentity: true, customCiphersuite: 0xD001),
        PSKConfig(name: "ECDHE_PSK CHACHA20 (0xCCAC)",
                  pinTLS13: false, appendCiphersuite: false,
                  completeWithIdentity: true, customCiphersuite: 0xCCAC),
        PSKConfig(name: "DHE_PSK AES_128_GCM (0x00AA)",
                  pinTLS13: false, appendCiphersuite: false,
                  completeWithIdentity: true, customCiphersuite: 0x00AA),
        PSKConfig(name: "ECDHE_PSK AES_256_GCM (0xD002)",
                  pinTLS13: false, appendCiphersuite: false,
                  completeWithIdentity: true, customCiphersuite: 0xD002),
    ]

    var report = "\nTLS-PSK loopback matrix — docs/scan-transport-encryption.md stage 1\n"
    var anyPaired = false

    for config in configs {
        let outcome = attempt(config)
        anyPaired = anyPaired || outcome.paired
        report += """

            \(outcome.paired ? "PAIRED " : "FAILED ") \(config.name)
                  selection block called: \(outcome.selectionCalled)
                  client: \(outcome.clientState)
                  server: \(outcome.serverState)
            \(outcome.negotiated.map { "      negotiated: \($0)\n" } ?? "")\(outcome.log)

            """
    }

    print(report)
    #expect(anyPaired, """
        No TLS-PSK configuration established a handshake on loopback. Per the design \
        doc's stage-1 table this is the decisive negative result: option 1 is dead and \
        the fallback is application-layer AEAD (§8.3). The matrix below is the evidence.
        \(report)
        """)
}
