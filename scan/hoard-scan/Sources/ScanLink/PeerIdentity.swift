import CryptoKit
import Foundation
import Security

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

private func integer(_ bytes: [UInt8]) -> [UInt8] {
    var v = bytes
    while v.count > 1, v[0] == 0x00, v[1] < 0x80 { v.removeFirst() }
    if let first = v.first, first >= 0x80 { v.insert(0x00, at: 0) }
    return der(0x02, v)
}

private func bitString(_ bytes: [UInt8]) -> [UInt8] { der(0x03, [0x00] + bytes) }

private let oidECPublicKey: [UInt8] = [0x06, 0x07, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01]
private let oidPrime256v1: [UInt8] = [0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07]
private let oidECDSAWithSHA256: [UInt8] = [0x06, 0x08, 0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x04, 0x03, 0x02]
private let oidCommonName: [UInt8] = [0x06, 0x03, 0x55, 0x04, 0x03]

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

public struct PeerIdentity {
    public let certificateDER: Data
    public let secIdentity: SecIdentity

    public var fingerprint: Data {
        Data(SHA256.hash(data: certificateDER))
    }
}

private func commonName(_ label: String) -> [UInt8] {
    sequence(set(sequence(oidCommonName, utf8String(label))))
}

private func utcTimeString(_ date: Date) -> [UInt8] {
    let fmt = DateFormatter()
    fmt.dateFormat = "yyMMddHHmmss'Z'"
    fmt.timeZone = TimeZone(identifier: "UTC")
    fmt.locale = Locale(identifier: "en_US_POSIX")
    fmt.calendar = Calendar(identifier: .gregorian)
    return utcTime(fmt.string(from: date))
}

private func selfSignedCertificate(
    privateKey: SecKey, publicKey: SecKey, label: String, lifetime: TimeInterval
) throws -> Data {
    var error: Unmanaged<CFError>?
    guard let raw = SecKeyCopyExternalRepresentation(publicKey, &error) as Data? else {
        throw PeerIdentityError.keyGeneration(
            (error?.takeRetainedValue()).map { "\($0)" } ?? "no external representation")
    }
    let spki = sequence(sequence(oidECPublicKey, oidPrime256v1), bitString([UInt8](raw)))

    var serialBytes = [UInt8](repeating: 0, count: 16)
    _ = SecRandomCopyBytes(kSecRandomDefault, serialBytes.count, &serialBytes)
    serialBytes[0] &= 0x7F

    let now = Date()
    let name = commonName(label)
    let algorithm = sequence(oidECDSAWithSHA256)

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

private let identityLock = NSLock()

public func loadOrCreatePeerIdentity(
    service: String, lifetime: TimeInterval = 60 * 60 * 24 * 365 * 10
) throws -> PeerIdentity {
    identityLock.lock()
    defer { identityLock.unlock() }
    if let existing = try? existingIdentity(service: service) { return existing }

    let tag = Data(service.utf8)
    let attributes: [String: Any] = [
        kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
        kSecAttrKeySizeInBits as String: 256,
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
        guard SecCertificateCopySubjectSummary(certificate) as String? == service else { continue }
        return PeerIdentity(
            certificateDER: SecCertificateCopyData(certificate) as Data, secIdentity: identity)
    }
    throw PeerIdentityError.keychain(errSecItemNotFound)
}

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
