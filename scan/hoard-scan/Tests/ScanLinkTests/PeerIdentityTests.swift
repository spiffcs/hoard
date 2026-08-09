// Does a hand-rolled self-signed certificate actually work?
//
// docs/scan-transport-encryption.md §8.2 marked this **unverified**: "there is
// no public Apple API for generating a self-signed X.509 certificate on iOS.
// High confidence, not exhaustively checked." The design chosen on 2026-08-08
// rests entirely on that being surmountable, so it is measured here before
// anything is built on top of it — the same order that turned the TLS-PSK
// question from a mystery into two named bugs in an hour.
//
// Four things have to hold, and each fails in a different place:
//
//   1. Security parses the DER we emitted (a bad length byte fails here);
//   2. the keychain hands back a SecIdentity for it (this is the step with no
//      iOS API, and the reason the private key is created permanent);
//   3. TLS completes with that identity presented by both ends;
//   4. a peer whose fingerprint was not pinned is refused.
//
// Point 4 is the one worth writing a test for even when the rest is obviously
// working: a pinning verify block that accidentally returns true is
// indistinguishable from a correct one until someone attacks it.

import CryptoKit
import Foundation
import Network
import Security
import Testing

@testable import ScanLink

/// Keychain items are process-global and outlive a test run, so every test
/// scopes its own and cleans up. A shared label would make these tests pass or
/// fail depending on what ran before them.
private func scopedService(_ name: String) -> String {
    "dev.spiffcs.hoard.scan.test.\(name).\(UUID().uuidString.prefix(8))"
}

/// Serialized, and it has to be. The legacy keychain does not take concurrent
/// writes from one process: four of these running at once produced
/// `-25300 failed to generate CDSA key` on some tests and success on others,
/// varying run to run — which reads as a flaky certificate bug rather than as
/// contention, and sent one round of debugging at the DER encoder instead.
/// Nothing about the production path is serial; only this test bundle is.
@Suite(.serialized)
struct PeerIdentitySuite {

@Test("a generated identity parses, and is stable across loads")
func identityGeneration() throws {
    let service = scopedService("gen")
    defer { deletePeerIdentity(service: service) }

    let identity = try loadOrCreatePeerIdentity(service: service)

    // Point 1: Security parsed what we emitted. loadOrCreatePeerIdentity
    // already throws .malformedCertificate if not, but assert the shape too —
    // a certificate can parse and still carry the wrong public key.
    let cert = try #require(SecCertificateCreateWithData(nil, identity.certificateDER as CFData))
    #expect(SecCertificateCopyData(cert) as Data == identity.certificateDER)
    #expect(identity.fingerprint.count == 32)

    // Point 2, and the property the whole design hangs on: a second load
    // returns the same identity rather than minting another. If this drifts,
    // every peer that pinned the old fingerprint is silently locked out on the
    // next launch — which would look like a network fault, not an identity bug.
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

/// TLS parameters presenting `identity`, and pinning the peer to `expected`.
///
/// `expected == nil` means accept anything, which is only ever right during the
/// very first pairing exchange — and even then the fingerprint is bound to the
/// pairing code's HMAC before it is stored. It is a parameter here so the
/// refusal case can be tested against the acceptance case with one variable
/// changed.
private func pinnedParameters(
    identity: PeerIdentity, expected: Data?, queue: DispatchQueue,
    saw: @escaping (Data) -> Void
) -> NWParameters {
    let tls = NWProtocolTLS.Options()
    let sec = tls.securityProtocolOptions

    if let secIdentity = sec_identity_create(identity.secIdentity) {
        sec_protocol_options_set_local_identity(sec, secIdentity)
    }
    // Both ends present a certificate. Without this the phone would
    // authenticate the Mac and not the reverse, and the scanner auto-commits —
    // an unauthenticated writer is the whole thing the pairing gate exists to
    // stop.
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
            // Constant time. The comparison is against a value an attacker
            // supplies, and `==` on Data is not documented to be constant
            // time — cheap to do right, and Pairing.swift already sets the
            // precedent with isValidAuthenticationCode.
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

/// Stands up a mutually-authenticated TLS session on loopback between two
/// identities, with each end pinning what it is told to pin.
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

    // `.ready`, not a non-nil port: `listener.port` reports the requested `.any`
    // — that is, 0 — from construction, and dialing port 0 fails with
    // EADDRNOTAVAIL, which reads as a TLS problem and is not one.
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
    // Each end saw the other's real certificate, not its own reflected back —
    // a verify block wired to the wrong side would still pass the test above.
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

    // The phone pins the Mac. Someone else on the network connects with a
    // perfectly valid certificate of their own — which is the whole attack a
    // self-signed system has to survive, since anyone can mint one.
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
