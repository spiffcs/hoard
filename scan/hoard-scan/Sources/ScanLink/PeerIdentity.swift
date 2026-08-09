// A self-signed certificate, generated on the device, pinned by fingerprint.
//
// This is the identity half of the transport. TLS needs each end to present
// something; there is no certificate authority in a house with one phone and
// one Mac, and there should not be. So each end mints its own certificate once,
// keeps the private key in the keychain forever, and the two ends learn each
// other's fingerprints during pairing — trust on first use, the same shape KDE
// Connect and Syncthing use for exactly this problem.
//
// Why not TLS-PSK, which was measured working first: a PSK derives the traffic
// key from the six-digit pairing code, so the code stays security-critical for
// the life of the link. A pinned certificate is 256 bits of key material that
// the code merely *introduces* — after the first pairing the code authenticates
// nothing, and its weakness stops mattering. Pairing.swift:14 says the same
// thing from the other direction: "If this ever leaves a home network, replace
// the code — not the TLS."
//
// The awkward part, and the reason this file is longer than it looks like it
// should be: **iOS has no public API that generates a self-signed X.509
// certificate.** macOS had SecIdentityCreate; it was never exposed on iOS and
// is not usable here. SecKeyCreateRandomKey gives a key pair and
// SecKeyCreateSignature signs bytes, but the certificate that wraps them has to
// be assembled here, byte by byte, in DER. That is what the ASN.1 section
// below is: not a general encoder, but exactly enough to emit one fixed
// certificate shape, which is why it can be this short and this testable.
//
// The alternative was apple/swift-certificates, which does this properly and in
// far fewer lines. Rejected deliberately: the package currently has no external
// dependencies at all — no .package(url:), no Package.resolved — and
// app-store-release.md leans on that for the privacy label, since
// required-reason API coverage is a property of the whole linked binary rather
// than of our own source. One fixed certificate shape is a bounded thing to
// hand-roll. A dependency graph is not a bounded thing to re-audit.

import CryptoKit
import Foundation
import Security

// MARK: - Just enough DER

/// DER type-length-value. Definite length only, which is all X.509 uses.
///
/// Lengths under 128 are a single byte; anything larger is a count-of-bytes
/// marker with the high bit set, then the length big-endian. Getting this
/// wrong produces a certificate that parses on one machine and not another,
/// so it is one function used by every caller rather than open-coded.
private func der(_ tag: UInt8, _ contents: [UInt8]) -> [UInt8] {
    var out: [UInt8] = [tag]
    let n = contents.count
    if n < 0x80 {
        out.append(UInt8(n))
    } else {
        var lengthBytes: [UInt8] = []
        var remaining = n
        while remaining > 0 {
            lengthBytes.insert(UInt8(remaining & 0xFF), at: 0)
            remaining >>= 8
        }
        out.append(UInt8(0x80 | lengthBytes.count))
        out.append(contentsOf: lengthBytes)
    }
    out.append(contentsOf: contents)
    return out
}

private func sequence(_ parts: [UInt8]...) -> [UInt8] { der(0x30, parts.flatMap { $0 }) }
private func set(_ contents: [UInt8]) -> [UInt8] { der(0x31, contents) }
private func utf8String(_ s: String) -> [UInt8] { der(0x0C, Array(s.utf8)) }
private func utcTime(_ s: String) -> [UInt8] { der(0x17, Array(s.utf8)) }

/// An INTEGER. DER integers are signed, so a leading byte ≥ 0x80 needs a zero
/// pad or the value reads as negative — which for a serial number means a
/// certificate some parsers reject and others silently accept with a different
/// serial than the one intended.
private func integer(_ bytes: [UInt8]) -> [UInt8] {
    var v = bytes
    while v.count > 1, v[0] == 0x00, v[1] < 0x80 { v.removeFirst() }
    if let first = v.first, first >= 0x80 { v.insert(0x00, at: 0) }
    return der(0x02, v)
}

/// A BIT STRING whose content is a whole number of bytes, so the leading
/// "unused bits" octet is always zero.
private func bitString(_ bytes: [UInt8]) -> [UInt8] { der(0x03, [0x00] + bytes) }

// Object identifiers, pre-encoded. Written as literal bytes rather than parsed
// from dotted strings because there are exactly four of them, they never
// change, and a general OID encoder would be more code than it saves — with
// its own failure mode of encoding subtly wrong and producing a certificate
// that names an algorithm nobody implements.
private let oidECPublicKey: [UInt8] = [0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01]
private let oidPrime256v1: [UInt8] = [0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07]
private let oidECDSAWithSHA256: [UInt8] = [0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x04, 0x03, 0x02]
private let oidCommonName: [UInt8] = [0x06, 0x03, 0x55, 0x04, 0x03]

// MARK: - The identity

public enum PeerIdentityError: Error, CustomStringConvertible {
    case keyGeneration(String)
    case signing(String)
    case malformedCertificate
    case keychain(OSStatus)

    public var description: String {
        switch self {
        case .keyGeneration(let s): return "could not generate a key pair: \(s)"
        case .signing(let s): return "could not sign the certificate: \(s)"
        case .malformedCertificate: return "the generated certificate did not parse"
        case .keychain(let status): return "keychain error \(status)"
        }
    }
}

/// This device's TLS identity: a P-256 key pair and the self-signed
/// certificate that carries its public half.
public struct PeerIdentity {
    public let certificateDER: Data
    public let secIdentity: SecIdentity

    /// SHA-256 over the certificate's DER. This is the pinned value — what one
    /// end stores about the other, and what `PeerTrust` compares on every
    /// subsequent connection.
    ///
    /// Over the whole certificate rather than over the public key alone: both
    /// are defensible, and the whole-certificate hash is what Syncthing's
    /// device ID and KDE Connect's pinning both use, so anyone arriving from
    /// those systems finds what they expect. It also means a certificate that
    /// is regenerated with the same key still reads as a new identity, which
    /// is the conservative direction to fail in.
    public var fingerprint: Data {
        Data(SHA256.hash(data: certificateDER))
    }
}

/// The subject and issuer name. Self-signed, so they are the same value.
///
/// Deliberately not the pairing code, the device name, or anything else a
/// person chose: a certificate's common name travels in the clear on every
/// handshake, and the device name is "Chris's iPhone".
private func commonName(_ label: String) -> [UInt8] {
    sequence(set(sequence(oidCommonName, utf8String(label))))
}

/// UTCTime, which X.509 uses for dates before 2050 and which is two-digit-year
/// by definition. Fixed to UTC and to the POSIX locale: a device in a
/// non-Gregorian regional calendar otherwise emits a date no parser accepts,
/// and it does so only on that device.
private func utcTimeString(_ date: Date) -> [UInt8] {
    let fmt = DateFormatter()
    fmt.dateFormat = "yyMMddHHmmss'Z'"
    fmt.timeZone = TimeZone(identifier: "UTC")
    fmt.locale = Locale(identifier: "en_US_POSIX")
    fmt.calendar = Calendar(identifier: .gregorian)
    return utcTime(fmt.string(from: date))
}

/// Builds a self-signed P-256 certificate around `publicKey`, signed by
/// `privateKey`.
private func selfSignedCertificate(
    privateKey: SecKey, publicKey: SecKey, label: String, lifetime: TimeInterval
) throws -> Data {
    var error: Unmanaged<CFError>?
    guard let raw = SecKeyCopyExternalRepresentation(publicKey, &error) as Data? else {
        throw PeerIdentityError.keyGeneration(
            (error?.takeRetainedValue()).map { "\($0)" } ?? "no external representation")
    }
    // X9.63 uncompressed point: 0x04 ‖ X ‖ Y, which is exactly what a
    // subjectPublicKey BIT STRING carries for an EC key.
    let spki = sequence(sequence(oidECPublicKey, oidPrime256v1), bitString([UInt8](raw)))

    // A random 16-byte serial. X.509 wants positive and unique; unique matters
    // because a peer that pins by fingerprint may still index by serial.
    var serialBytes = [UInt8](repeating: 0, count: 16)
    _ = SecRandomCopyBytes(kSecRandomDefault, serialBytes.count, &serialBytes)
    serialBytes[0] &= 0x7F

    let now = Date()
    let name = commonName(label)
    let algorithm = sequence(oidECDSAWithSHA256)

    // v3 with no extensions. Extensions are OPTIONAL in the ASN.1, and this
    // certificate makes no claim that needs one: it is not a CA, it is not
    // being matched against a hostname, and the peer authorises it by
    // fingerprint rather than by anything the certificate asserts about
    // itself. Adding basicConstraints or a SAN would be decoration that the
    // verify block ignores.
    let version = der(0xA0, integer([0x02]))
    let tbs = sequence(
        version,
        integer(serialBytes),
        algorithm,
        name,
        sequence(utcTimeString(now.addingTimeInterval(-300)),
                 utcTimeString(now.addingTimeInterval(lifetime))),
        name,
        spki)

    guard let signature = SecKeyCreateSignature(
        privateKey, .ecdsaSignatureMessageX962SHA256, Data(tbs) as CFData, &error) as Data?
    else {
        throw PeerIdentityError.signing(
            (error?.takeRetainedValue()).map { "\($0)" } ?? "unknown")
    }

    return Data(sequence(tbs, algorithm, bitString([UInt8](signature))))
}

/// Serializes identity creation across the process.
///
/// The legacy macOS keychain does not take concurrent key generation from one
/// process: two threads calling `SecKeyCreateRandomKey` with
/// `kSecAttrIsPermanent` at the same time produce `-25300 failed to generate
/// CDSA key` on one of them, intermittently. The error names neither
/// contention nor the keychain, so it reads as a certificate bug and sends
/// debugging somewhere else entirely — it did exactly that twice while this
/// file was being written.
///
/// A lock is the right shape rather than a workaround. Identity creation
/// happens once in the life of an install and is not on any hot path, so
/// serializing it costs nothing measurable, and the alternative is a rare
/// startup failure whose message points at the wrong thing.
private let identityLock = NSLock()

/// Loads this device's identity, generating it on first use.
///
/// `service` scopes the keychain items so the phone and the Mac do not collide
/// when both run on one machine during testing, and so a future second link
/// does not inherit this one's identity.
public func loadOrCreatePeerIdentity(
    service: String, lifetime: TimeInterval = 60 * 60 * 24 * 365 * 10
) throws -> PeerIdentity {
    identityLock.lock()
    defer { identityLock.unlock() }
    if let existing = try? existingIdentity(service: service) { return existing }

    // kSecAttrIsPermanent puts the private key in the keychain as it is
    // created, which is what later lets SecItemCopyMatching hand back a
    // SecIdentity — an identity is not a storable thing in its own right, it
    // is the keychain noticing that it holds both halves.
    let tag = Data(service.utf8)
    let attributes: [String: Any] = [
        kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
        kSecAttrKeySizeInBits as String: 256,
        // Whichever keychain the platform defaults to, deliberately not
        // `kSecUseDataProtectionKeychain: true`. That flag looks like the
        // modern choice and is wrong here: the data-protection keychain
        // requires a keychain-access-group entitlement, so it fails with
        // `-34018 errSecMissingEntitlement` for anything not signed with a
        // real team — which includes this package's own test bundle and the
        // ad-hoc-signed macOS helper (`codesign --sign -`, see build-scan.sh).
        // On iOS the data-protection keychain is the only one there is, so the
        // default already selects it; on macOS the default is the file-based
        // keychain, which an unsigned binary can use. Measured both ways.
        kSecPrivateKeyAttrs as String: [
            kSecAttrIsPermanent as String: true,
            kSecAttrApplicationTag as String: tag,
            kSecAttrLabel as String: service,
        ],
    ]
    var error: Unmanaged<CFError>?
    guard let privateKey = SecKeyCreateRandomKey(attributes as CFDictionary, &error) else {
        throw PeerIdentityError.keyGeneration(
            (error?.takeRetainedValue()).map { "\($0)" } ?? "unknown")
    }
    guard let publicKey = SecKeyCopyPublicKey(privateKey) else {
        throw PeerIdentityError.keyGeneration("no public key from the generated pair")
    }

    let der = try selfSignedCertificate(
        privateKey: privateKey, publicKey: publicKey, label: service, lifetime: lifetime)
    guard let certificate = SecCertificateCreateWithData(nil, der as CFData) else {
        // The one check that catches a DER bug: Security parses what was just
        // emitted, so a malformed certificate fails here rather than as an
        // unexplained handshake failure days later.
        throw PeerIdentityError.malformedCertificate
    }

    let addStatus = SecItemAdd([
        kSecClass as String: kSecClassCertificate,
        kSecValueRef as String: certificate,
        kSecAttrLabel as String: service,
    ] as CFDictionary, nil)
    guard addStatus == errSecSuccess || addStatus == errSecDuplicateItem else {
        throw PeerIdentityError.keychain(addStatus)
    }

    return try existingIdentity(service: service)
}

/// Fetches the identity the keychain already holds, if any.
///
/// **`kSecAttrLabel` does not filter a `kSecClassIdentity` query.** Passing it
/// alongside `kSecClass: kSecClassIdentity` is accepted, returns
/// `errSecSuccess`, and hands back *an* identity — whichever the keychain
/// reaches first — rather than the one asked for. It looks exactly like a
/// working lookup, and it fails in the worst available direction: two devices
/// that should have distinct identities silently share one, so pinning
/// authenticates nothing and every stranger is accepted. It was caught here
/// only because a test asserted that two identities differ. Do not reintroduce
/// the label as a query predicate; match on the certificate, which is the
/// thing that actually carries the name.
private func existingIdentity(service: String) throws -> PeerIdentity {
    var items: CFTypeRef?
    let status = SecItemCopyMatching([
        kSecClass as String: kSecClassIdentity,
        kSecReturnRef as String: true,
        kSecMatchLimit as String: kSecMatchLimitAll,
    ] as CFDictionary, &items)
    guard status == errSecSuccess, let identities = items as? [SecIdentity] else {
        throw PeerIdentityError.keychain(status)
    }

    for identity in identities {
        var certificate: SecCertificate?
        guard SecIdentityCopyCertificate(identity, &certificate) == errSecSuccess,
              let certificate
        else { continue }
        // The common name, which selfSignedCertificate set to `service`. Read
        // back from the certificate rather than from a keychain attribute so
        // the match is against what the certificate actually says.
        guard SecCertificateCopySubjectSummary(certificate) as String? == service else { continue }
        return PeerIdentity(
            certificateDER: SecCertificateCopyData(certificate) as Data, secIdentity: identity)
    }
    throw PeerIdentityError.keychain(errSecItemNotFound)
}

/// Removes this device's identity. Only for tests and for a "forget this
/// device" action — regenerating means every peer that pinned the old
/// fingerprint must pair again.
public func deletePeerIdentity(service: String) {
    SecItemDelete([
        kSecClass as String: kSecClassCertificate,
        kSecAttrLabel as String: service,
    ] as CFDictionary)
    SecItemDelete([
        kSecClass as String: kSecClassKey,
        kSecAttrApplicationTag as String: Data(service.utf8),
    ] as CFDictionary)
}
