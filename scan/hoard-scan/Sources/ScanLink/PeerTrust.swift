import CryptoKit
import Foundation
import Network
import Security

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

    public func pin(_ fingerprint: Data, name: String) {
        var add = query(fingerprint)
        SecItemDelete(add as CFDictionary)
        add[kSecValueData as String] = Data(name.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }

    public func forget(_ fingerprint: Data) {
        SecItemDelete(query(fingerprint) as CFDictionary)
    }

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

    public func forgetAll() {
        SecItemDelete([
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
        ] as CFDictionary)
    }
}

public struct PeerTrust {
    public let identity: PeerIdentity
    private let pinnedProvider: () -> Set<Data>
    private let acceptUnknownProvider: () -> Bool

    public var pinned: Set<Data> { pinnedProvider() }

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

    public init(identity: PeerIdentity, pinned: Set<Data>, acceptUnknown: Bool) {
        self.init(identity: identity, pinned: { pinned }, acceptUnknown: { acceptUnknown })
    }
}

func fingerprint(of certificate: SecCertificate) -> Data {
    Data(SHA256.hash(data: SecCertificateCopyData(certificate) as Data))
}

func constantTimeEqual(_ a: Data, _ b: Data) -> Bool {
    var diff: UInt8 = a.count == b.count ? 0 : 1
    for (x, y) in zip(a, b) { diff |= x ^ y }
    return diff == 0
}

func applyTrust(
    _ trust: PeerTrust, to options: NWProtocolTLS.Options,
    queue: DispatchQueue, sawPeer: @escaping (Data) -> Void
) {
    let sec = options.securityProtocolOptions
    if let identity = sec_identity_create(trust.identity.secIdentity) {
        sec_protocol_options_set_local_identity(sec, identity)
    }
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
            if trust.pinned.contains(where: { constantTimeEqual($0, seen) }) {
                complete(true)
            } else {
                complete(trust.acceptUnknown)
            }
        },
        queue)
}

func peerFingerprint(of connection: NWConnection) -> Data? {
    guard let metadata = connection.metadata(definition: NWProtocolTLS.definition)
        as? NWProtocolTLS.Metadata
    else { return nil }
    var found: Data?
    _ = sec_protocol_metadata_access_peer_certificate_chain(
        metadata.securityProtocolMetadata,
        { certificate in
            guard found == nil else { return }
            found = fingerprint(of: sec_certificate_copy_ref(certificate).takeRetainedValue())
        })
    return found
}
