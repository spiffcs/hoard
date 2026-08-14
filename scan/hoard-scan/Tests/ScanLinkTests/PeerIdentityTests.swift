import CryptoKit
import Foundation
import Network
import Security
import Testing

@testable import ScanLink

private func scopedService(_ name: String) -> String {
    "dev.spiffcs.hoard.scan.test.\(name).\(UUID().uuidString.prefix(8))"
}

@Suite(.serialized)
struct PeerIdentitySuite {
@Test("a generated identity parses, and is stable across loads")
func identityGeneration() throws {
    let service = scopedService("gen")
    defer { deletePeerIdentity(service: service) }

    let identity = try loadOrCreatePeerIdentity(service: service)

    let cert = try #require(SecCertificateCreateWithData(nil, identity.certificateDER as CFData))
    #expect(SecCertificateCopyData(cert) as Data == identity.certificateDER)
    #expect(identity.fingerprint.count == 32)

    let again = try loadOrCreatePeerIdentity(service: service)
    #expect(again.fingerprint == identity.fingerprint)
}

@Test("two identities are different")
func identitiesAreDistinct() throws {
    let a = scopedService("a"), b = scopedService("b")
    defer { deletePeerIdentity(service: a); deletePeerIdentity(service: b) }
    let one = try loadOrCreatePeerIdentity(service: a)
    let two = try loadOrCreatePeerIdentity(service: b)
    #expect(one.fingerprint != two.fingerprint)
}

private func pinnedParameters(
    identity: PeerIdentity, expected: Data?, queue: DispatchQueue,
    saw: @escaping (Data) -> Void
) -> NWParameters {
    let tls = NWProtocolTLS.Options()
    let sec = tls.securityProtocolOptions

    if let secIdentity = sec_identity_create(identity.secIdentity) {
        sec_protocol_options_set_local_identity(sec, secIdentity)
    }
    sec_protocol_options_set_peer_authentication_required(sec, true)

    sec_protocol_options_set_verify_block(
        sec,
        { _, trustRef, complete in
            let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
            guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
                  let leaf = chain.first
            else {
                complete(false)
                return
            }
            let der = SecCertificateCopyData(leaf) as Data
            let fingerprint = Data(SHA256.hash(data: der))
            saw(fingerprint)
            guard let expected else {
                complete(true)
                return
            }
            var diff: UInt8 = fingerprint.count == expected.count ? 0 : 1
            for (a, b) in zip(fingerprint, expected) { diff |= a ^ b }
            complete(diff == 0)
        },
        queue)

    return NWParameters(tls: tls, tcp: NWProtocolTCP.Options())
}

private func waitForIdentity(_ seconds: Double, _ check: () -> Bool) -> Bool {
    let deadline = Date().addingTimeInterval(seconds)
    while !check(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.02))
    }
    return check()
}

private struct HandshakeResult {
    var paired: Bool
    var clientState: String
    var serverState: String
    var serverSawClientFingerprint: Data?
    var clientSawServerFingerprint: Data?
}

private func handshake(
    server: PeerIdentity, client: PeerIdentity,
    serverPins: Data?, clientPins: Data?
) -> HandshakeResult {
    let queue = DispatchQueue(label: "hoard-scan.test.identity")
    var serverState: NWConnection.State?
    var clientState: NWConnection.State?
    var serverConn: NWConnection?
    var serverReady = false, clientReady = false, listenerReady = false
    var serverSaw: Data?, clientSaw: Data?

    guard let listener = try? NWListener(
        using: pinnedParameters(
            identity: server, expected: serverPins, queue: queue, saw: { serverSaw = $0 }),
        on: .any)
    else { return HandshakeResult(paired: false, clientState: "-", serverState: "no listener",
                                  serverSawClientFingerprint: nil, clientSawServerFingerprint: nil) }

    listener.newConnectionHandler = { conn in
        serverConn = conn
        conn.stateUpdateHandler = { st in
            serverState = st
            if case .ready = st { serverReady = true }
        }
        conn.start(queue: queue)
    }
    listener.stateUpdateHandler = { if case .ready = $0 { listenerReady = true } }
    listener.start(queue: queue)
    defer { listener.cancel(); serverConn?.cancel() }

    guard waitForIdentity(5, { listenerReady }), let port = listener.port else {
        return HandshakeResult(paired: false, clientState: "-", serverState: "listener not ready",
                               serverSawClientFingerprint: nil, clientSawServerFingerprint: nil)
    }

    let conn = NWConnection(
        host: .ipv4(.loopback), port: port,
        using: pinnedParameters(
            identity: client, expected: clientPins, queue: queue, saw: { clientSaw = $0 }))
    conn.stateUpdateHandler = { st in
        clientState = st
        if case .ready = st { clientReady = true }
    }
    conn.start(queue: queue)
    defer { conn.cancel() }

    _ = waitForIdentity(8) {
        (clientReady && serverReady) || {
            if case .failed = clientState { return true }
            if case .waiting = clientState { return true }
            return false
        }()
    }

    return HandshakeResult(
        paired: clientReady && serverReady,
        clientState: "\(clientState.map { "\($0)" } ?? "none")",
        serverState: "\(serverState.map { "\($0)" } ?? "none")",
        serverSawClientFingerprint: serverSaw,
        clientSawServerFingerprint: clientSaw)
}

@Test("two pinned identities complete a mutually authenticated TLS session",
      .timeLimit(.minutes(1)))
func pinnedHandshakeSucceeds() throws {
    let phoneService = scopedService("phone"), macService = scopedService("mac")
    defer { deletePeerIdentity(service: phoneService); deletePeerIdentity(service: macService) }

    let phone = try loadOrCreatePeerIdentity(service: phoneService)
    let mac = try loadOrCreatePeerIdentity(service: macService)

    let result = handshake(
        server: phone, client: mac,
        serverPins: mac.fingerprint, clientPins: phone.fingerprint)

    #expect(result.paired, """
        a mutually pinned TLS session did not establish
          client: \(result.clientState)
          server: \(result.serverState)
        """)
    #expect(result.clientSawServerFingerprint == phone.fingerprint)
    #expect(result.serverSawClientFingerprint == mac.fingerprint)
}

@Test("an unpinned peer is refused", .timeLimit(.minutes(1)))
func unpinnedPeerRefused() throws {
    let phoneService = scopedService("phone2")
    let macService = scopedService("mac2")
    let strangerService = scopedService("stranger")
    defer {
        deletePeerIdentity(service: phoneService)
        deletePeerIdentity(service: macService)
        deletePeerIdentity(service: strangerService)
    }

    let phone = try loadOrCreatePeerIdentity(service: phoneService)
    let mac = try loadOrCreatePeerIdentity(service: macService)
    let stranger = try loadOrCreatePeerIdentity(service: strangerService)

    let result = handshake(
        server: phone, client: stranger,
        serverPins: mac.fingerprint, clientPins: phone.fingerprint)

    #expect(!result.paired, """
        a peer whose fingerprint was never pinned was accepted
          client: \(result.clientState)
          server: \(result.serverState)
        """)
}
}
