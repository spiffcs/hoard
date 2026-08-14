import Foundation
import Network
import ScanWire

public struct LinkFailure: Equatable, Sendable {
    public let reason: String
    public let detail: String

    public init(reason: String, detail: String) {
        self.reason = reason
        self.detail = detail
    }

    public init(_ error: NWError) {
        detail = "\(error)"
        switch error {
        case .posix(.ECONNABORTED), .posix(.ECONNRESET), .posix(.ENOTCONN),
             .posix(.EPIPE), .posix(.ETIMEDOUT):
            reason = "iPhone disconnected. Check that Hoardling is still running on the phone"
        case .posix(.ENETDOWN), .posix(.ENETUNREACH), .posix(.EHOSTUNREACH),
             .posix(.EHOSTDOWN), .posix(.ENETRESET):
            reason = "Lost the network to the iPhone. Both devices need the same Wi-Fi, or a cable"
        case .posix(.ECONNREFUSED):
            reason = "The iPhone refused the connection. Reopen Hoardling and try again"
        case .dns:
            reason = "Could not find the iPhone on this network"
        case .tls:
            reason = "Could not establish a secure link to the iPhone"
        default:
            reason = "iPhone disconnected"
        }
    }
}

public enum PeerState: Equatable, Sendable {
    case connecting
    case ready
    case failed(LinkFailure)
    case cancelled
}

public final class PeerLink {
    public private(set) var role: PeerRole
    public var onFrame: ((Frame) -> Void)?
    public var onState: ((PeerState) -> Void)?
    var onReady: (() -> Void)?

    private let connection: NWConnection
    private let queue: DispatchQueue
    private var reader = FrameReader()
    private let previewInFlight = SendGate()
    private(set) public var state: PeerState = .connecting

    private(set) public var peerFingerprint: Data?

    init(connection: NWConnection, role: PeerRole, queue: DispatchQueue) {
        self.connection = connection
        self.role = role
        self.queue = queue
    }

    public func start() {
        connection.stateUpdateHandler = { [weak self] st in
            guard let self else { return }
            switch st {
            case .ready:
                self.set(.ready)
                self.receive()
            case .failed(let err):
                self.set(.failed(LinkFailure(err)))
            case .waiting(let err):
                self.set(.connecting)
                self.log("link waiting: \(err.localizedDescription)")
            case .cancelled:
                self.set(.cancelled)
            default:
                break
            }
            if case .failed = st { self.previewInFlight.clear() }
            if case .cancelled = st { self.previewInFlight.clear() }
        }
        connection.start(queue: queue)
    }

    public func cancel() {
        connection.cancel()
    }

    func assign(role: PeerRole) {
        self.role = role
    }

    func limitPayloads(_ bytes: Int) {
        reader.limit = bytes
    }

    public func send(_ frame: Frame, completed: (() -> Void)? = nil) {
        guard let data = try? encode(frame) else {
            completed?()
            return
        }
        connection.send(content: data, completion: .contentProcessed { [weak self] err in
            if let err { self?.set(.failed(LinkFailure(err))) }
            completed?()
        })
    }

    @discardableResult
    public func sendDroppable(_ frame: Frame) -> Bool {
        guard let data = try? encode(frame) else { return false }
        guard !previewInFlight.testAndSet() else { return false }
        connection.send(content: data, completion: .contentProcessed { [weak self] err in
            guard let self else { return }
            self.previewInFlight.clear()
            if let err { self.set(.failed(LinkFailure(err))) }
        })
        return true
    }

    private func receive() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) {
            [weak self] data, _, complete, err in
            guard let self else { return }
            if let data, !data.isEmpty {
                do {
                    for frame in try self.reader.append(data) { self.onFrame?(frame) }
                } catch {
                    self.set(.failed(LinkFailure(
                        reason: "The iPhone sent something this version does not understand",
                        detail: "protocol error: \(error)")))
                    self.connection.cancel()
                    return
                }
            }
            if let err {
                self.set(.failed(LinkFailure(err)))
                return
            }
            if complete {
                self.set(.failed(LinkFailure(
                    reason: "The iPhone closed the connection",
                    detail: "peer closed cleanly")))
                return
            }
            self.receive()
        }
    }

    private func set(_ new: PeerState) {
        guard state != new else { return }
        if new == .ready, peerFingerprint == nil {
            peerFingerprint = ScanLink.peerFingerprint(of: connection)
        }
        state = new
        if new == .ready { onReady?(); onReady = nil }
        onState?(new)
    }

    private func log(_ message: String) {
        FileHandle.standardError.write(Data("scan: \(message)\n".utf8))
    }
}

final class SendGate: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false

    func testAndSet() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let was = value
        value = true
        return was
    }

    func clear() {
        lock.lock()
        value = false
        lock.unlock()
    }
}

func parameters(
    role: PeerRole, trust: PeerTrust? = nil,
    queue: DispatchQueue = DispatchQueue(label: "hoard-scan.tls"),
    sawPeer: @escaping (Data) -> Void = { _ in }
) -> NWParameters {
    let params: NWParameters
    if let trust {
        let tls = NWProtocolTLS.Options()
        applyTrust(trust, to: tls, queue: queue, sawPeer: sawPeer)
        params = NWParameters(tls: tls, tcp: NWProtocolTCP.Options())
    } else {
        params = NWParameters.tcp
    }
    params.includePeerToPeer = true
    if role == .control, let tcp = params.defaultProtocolStack
        .internetProtocol as? NWProtocolTCP.Options {
        tcp.noDelay = true
    }
    return params
}
