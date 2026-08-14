import CryptoKit
import Foundation
import Network
import Testing

@testable import ScanLink

private struct PSKConfig {
    var name: String
    var pinTLS13 = true
    var appendCiphersuite = true
    var verifyBlocks = true
    var serverAddsKey = true
    var selectionBlock = true
    var completeWithIdentity = false
    var pinTLS12 = false
    var customCiphersuite: UInt16?
}

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

private func dispatchData(_ data: Data) -> DispatchData {
    data.withUnsafeBytes { DispatchData(bytes: $0) }
}

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

    var report = "\nTLS-PSK loopback matrix — transport experiment stage 1\n"
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
