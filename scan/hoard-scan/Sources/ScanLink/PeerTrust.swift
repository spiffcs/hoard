// Who this end is willing to talk to.
//
// The link's identity model, in one place: this device presents the
// certificate from PeerIdentity, and it accepts a peer whose certificate
// fingerprint it has pinned. Trust on first use — the same shape KDE Connect
// and Syncthing use for a phone and a desktop on the same LAN, and for the
// same reason: there is no certificate authority in a house, and inventing one
// would be worse than not having one.
//
// The pairing code's job changes completely under this, and shrinks. It used
// to be the only thing standing between a stranger and write access, for the
// life of the install. Now it authenticates exactly one exchange — the first
// one, where the two ends learn each other's fingerprints — and after that the
// pinned certificate does the work. Six digits is a weak long-lived secret and
// a perfectly good introduction token, which is what Pairing.swift:14 was
// asking for: "If this ever leaves a home network, replace the code — not the
// TLS."
//
// The binding that makes that safe is in Pairing.swift's `proof`: a peer does
// not merely prove it knows the code, it proves it knows the code *and* is
// looking at the certificate it thinks it is. Without that a listener in the
// middle could relay a valid proof between two honest ends and pin itself to
// both.

import CryptoKit
import Foundation
import Network
import Security

/// Fingerprints this device has pinned, kept in the keychain.
///
/// The keychain rather than UserDefaults, for the reason PairingStore already
/// gives: a plist in the app container is read by any backup, any file-sharing
/// browse, and any device dump. A pinned fingerprint is not a secret — it is a
/// public hash — but the *set* of them is the authorisation list, and an
/// attacker who can add an entry to it does not need any of the rest of this.
public struct PinnedPeers {
    private let service: String

    public init(service: String) {
        self.service = service
    }

    private func query(_ fingerprint: Data) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: fingerprint.base64EncodedString(),
        ]
    }

    /// Records a peer as trusted. Idempotent.
    public func pin(_ fingerprint: Data, name: String) {
        var add = query(fingerprint)
        SecItemDelete(add as CFDictionary)
        add[kSecValueData as String] = Data(name.utf8)
        // Matches PairingStore: the Mac drives a phone sitting in a stand, and
        // a locked phone that cannot read its own pin list would refuse every
        // connection for reasons invisible from the terminal.
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }

    public func forget(_ fingerprint: Data) {
        SecItemDelete(query(fingerprint) as CFDictionary)
    }

    /// Every pinned fingerprint, as the set the verify block checks against.
    ///
    /// Read once when parameters are built rather than consulted per
    /// handshake: the verify block runs on Network.framework's queue during a
    /// handshake, and a keychain round trip there is both slow and able to
    /// block on a locked device.
    public var all: Set<Data> {
        var items: CFTypeRef?
        let status = SecItemCopyMatching([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecReturnAttributes as String: true,
            kSecMatchLimit as String: kSecMatchLimitAll,
        ] as CFDictionary, &items)
        guard status == errSecSuccess, let rows = items as? [[String: Any]] else { return [] }
        return Set(rows.compactMap { row in
            (row[kSecAttrAccount as String] as? String).flatMap { Data(base64Encoded: $0) }
        })
    }

    /// Forgets every peer. The "unpair everything" action, and what a rotated
    /// identity implies — a peer that pinned the old certificate cannot reach
    /// this device again anyway.
    public func forgetAll() {
        SecItemDelete([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
        ] as CFDictionary)
    }
}

/// What this end presents, and what it will accept.
///
/// The policy is read **live**, on every handshake, rather than captured when
/// parameters are built. That is not a refinement; a snapshot is wrong in two
/// ways that both fail silently:
///
///   * a peer pinned during this session is absent from a set captured before
///     it — so the Mac that just paired is a stranger again on its next
///     connection, and the failure looks like a network fault;
///   * the pairing window could not close without rebuilding the listener,
///     and rebuilding it tears down the connection that just succeeded.
///
/// An `NWListener` builds its parameters once and keeps them for its whole
/// life, so anything captured there is captured for the life of the process.
public struct PeerTrust {
    public let identity: PeerIdentity
    private let pinnedProvider: () -> Set<Data>
    private let acceptUnknownProvider: () -> Bool

    /// Fingerprints trusted right now.
    public var pinned: Set<Data> { pinnedProvider() }

    /// Whether a peer with an unknown fingerprint may complete TLS right now.
    ///
    /// True only while pairing is open — the phone showing a code, the Mac
    /// having just been given one. TLS is not the gate in that window; the
    /// pairing proof is, and it runs immediately after on the hello. False the
    /// rest of the time, so an unpinned peer cannot even finish a handshake.
    public var acceptUnknown: Bool { acceptUnknownProvider() }

    public init(
        identity: PeerIdentity,
        pinned: @escaping () -> Set<Data>,
        acceptUnknown: @escaping () -> Bool
    ) {
        self.identity = identity
        self.pinnedProvider = pinned
        self.acceptUnknownProvider = acceptUnknown
    }

    /// A fixed policy. For tests, and for a caller whose answer genuinely
    /// cannot change while the listener is up.
    public init(identity: PeerIdentity, pinned: Set<Data>, acceptUnknown: Bool) {
        self.init(identity: identity, pinned: { pinned }, acceptUnknown: { acceptUnknown })
    }
}

/// SHA-256 of a certificate's DER, the pinned value.
func fingerprint(of certificate: SecCertificate) -> Data {
    Data(SHA256.hash(data: SecCertificateCopyData(certificate) as Data))
}

/// Constant-time equality. The value on one side is attacker-supplied, and
/// `Data`'s `==` is not documented to be constant time. Pairing.swift already
/// sets this precedent with `isValidAuthenticationCode`.
func constantTimeEqual(_ a: Data, _ b: Data) -> Bool {
    var diff: UInt8 = a.count == b.count ? 0 : 1
    for (x, y) in zip(a, b) { diff |= x ^ y }
    return diff == 0
}

/// Applies this end's identity and pinning policy to a set of TLS options.
///
/// `sawPeer` is called with the peer's fingerprint as the handshake verifies
/// it, which is how the hello step learns what to bind the pairing proof to.
/// It is not a security decision — that is `complete()` below — it is how a
/// fingerprint reaches code that runs after the handshake.
func applyTrust(
    _ trust: PeerTrust, to options: NWProtocolTLS.Options,
    queue: DispatchQueue, sawPeer: @escaping (Data) -> Void
) {
    let sec = options.securityProtocolOptions
    if let identity = sec_identity_create(trust.identity.secIdentity) {
        sec_protocol_options_set_local_identity(sec, identity)
    }
    // Both ends present a certificate. Without this the Mac would authenticate
    // the phone and not the reverse, and the scanner auto-commits — an
    // unauthenticated writer is the entire thing the pairing gate exists to
    // stop.
    sec_protocol_options_set_peer_authentication_required(sec, true)

    sec_protocol_options_set_verify_block(
        sec,
        { _, trustRef, complete in
            let secTrust = sec_trust_copy_ref(trustRef).takeRetainedValue()
            guard let chain = SecTrustCopyCertificateChain(secTrust) as? [SecCertificate],
                  let leaf = chain.first
            else {
                complete(false)
                return
            }
            let seen = fingerprint(of: leaf)
            sawPeer(seen)
            // Self-signed by design, so the system's own trust evaluation is
            // not consulted at all — it would reject every certificate here,
            // correctly and uselessly. Pinning is the whole policy.
            // Asked now, not when this block was installed. See PeerTrust.
            if trust.pinned.contains(where: { constantTimeEqual($0, seen) }) {
                complete(true)
            } else {
                complete(trust.acceptUnknown)
            }
        },
        queue)
}

/// The peer's certificate fingerprint, read from a live connection.
///
/// Not from the verify block, deliberately. An `NWListener` builds its
/// `NWParameters` once and every accepted connection shares them, so a
/// callback installed there fires per handshake with no way to say *which*
/// connection it belongs to — fine for a single-connection test, wrong for a
/// listener that holds several half-sessions at once, and wrong in the
/// direction that attributes one peer's fingerprint to another's connection.
/// The metadata hangs off the connection itself, so it cannot be confused.
func peerFingerprint(of connection: NWConnection) -> Data? {
    guard let metadata = connection.metadata(definition: NWProtocolTLS.definition)
        as? NWProtocolTLS.Metadata
    else { return nil }
    var found: Data?
    // The leaf is first, and it is the only one that matters: the chain is
    // self-signed and one element long by construction.
    _ = sec_protocol_metadata_access_peer_certificate_chain(
        metadata.securityProtocolMetadata,
        { certificate in
            guard found == nil else { return }
            found = fingerprint(of: sec_certificate_copy_ref(certificate).takeRetainedValue())
        })
    return found
}
