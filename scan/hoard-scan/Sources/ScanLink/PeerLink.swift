// One connection, framed.
//
// A thin wrapper over NWConnection that speaks Frames instead of bytes, and
// that knows the one thing about this link that is not generic: preview frames
// are droppable and control frames are not.
//
// The dropping is the interesting part. The obvious implementation enqueues
// every preview frame and lets the network catch up, which on a slow link
// produces a preview that lags further behind reality the longer the session
// runs — the worst possible failure for something whose only job is to show
// what is under the camera *now*. So a preview frame is discarded if the
// previous one has not finished sending. Latency stays bounded; frame rate
// degrades instead.

import Foundation
import Network
import ScanWire

/// Where a link is in its life.
/// Why a link went away, in two registers.
///
/// `reason` is for the person at the terminal and says what to do about it.
/// `detail` is the framework's own words, kept for the log and never shown.
///
/// The split exists because the raw text is genuinely unhelpful in the place it
/// was being shown. A backgrounded app produced "The operation couldn't be
/// completed. (Network.NWError error 53 - Software caused connection abort)" in
/// the middle of a scanning session — a sentence that names an errno, blames
/// software, and does not mention the iPhone, the app, or the one thing that
/// fixes it.
public struct LinkFailure: Equatable, Sendable {
    public let reason: String
    public let detail: String

    public init(reason: String, detail: String) {
        self.reason = reason
        self.detail = detail
    }

    /// Translates a Network.framework error into something actionable.
    ///
    /// The mapping is coarse on purpose. Nearly every failure here has the same
    /// handful of causes — the app went to the background, the phone locked,
    /// the two devices stopped sharing a network — so the message that helps is
    /// the one naming the likely fix, not the one naming the errno.
    public init(_ error: NWError) {
        detail = "\(error)"
        switch error {
        case .posix(.ECONNABORTED), .posix(.ECONNRESET), .posix(.ENOTCONN),
             .posix(.EPIPE), .posix(.ETIMEDOUT):
            // Overwhelmingly this is iOS suspending the app. Phrased as
            // something to go and check rather than a rule to have followed:
            // by the time this is on screen the disconnect has happened, and
            // telling someone what they should have done does not get them
            // scanning again. The Pair tab's troubleshooting section carries
            // the standing advice, which is where a rule belongs.
            reason = "iPhone disconnected. Check to make sure Hoardling is still open"
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
    /// The peer went away. Carries a reason written for the person at the
    /// terminal, and the framework's own text separately for the log — never a
    /// `closed` event, which would tear down the add session the user is in the
    /// middle of.
    case failed(LinkFailure)
    case cancelled
}

/// PeerLink is one framed connection.
public final class PeerLink {
    /// What this connection carries.
    ///
    /// Settable once rather than `let`, because the listener does not know it
    /// until the hello arrives — the connection has to be up and reading before
    /// it can be told what it is for. `assign` is the only mutation and it
    /// happens before the link is handed to anyone.
    public private(set) var role: PeerRole
    /// Called on `queue` for every complete frame received.
    public var onFrame: ((Frame) -> Void)?
    public var onState: ((PeerState) -> Void)?

    private let connection: NWConnection
    private let queue: DispatchQueue
    private var reader = FrameReader()
    /// Set while a droppable send is in flight. See the file header.
    private var previewInFlight = false
    private(set) public var state: PeerState = .connecting

    init(connection: NWConnection, role: PeerRole, queue: DispatchQueue) {
        self.connection = connection
        self.role = role
        self.queue = queue
    }

    /// start brings the connection up and begins reading.
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
                // Waiting is not failure — the interface may be coming up — but
                // a link that waits forever looks identical to a hang from the
                // outside, so it is surfaced rather than swallowed.
                self.set(.connecting)
                self.log("link waiting: \(err.localizedDescription)")
            case .cancelled:
                self.set(.cancelled)
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    public func cancel() {
        connection.cancel()
    }

    /// assign records what the peer said this connection is for. Called once,
    /// by the listener, on the hello.
    func assign(role: PeerRole) {
        self.role = role
    }

    /// send queues a frame that must arrive. Control traffic only.
    public func send(_ frame: Frame) {
        guard let data = try? encode(frame) else { return }
        connection.send(content: data, completion: .contentProcessed { [weak self] err in
            if let err { self?.set(.failed(LinkFailure(err))) }
        })
    }

    /// sendDroppable queues a frame only if the previous one has finished.
    ///
    /// Returns whether it was sent, so a caller can count what it is losing —
    /// a preview that is dropping most frames is a diagnosable condition, and
    /// silently degrading is how it stops being one.
    @discardableResult
    public func sendDroppable(_ frame: Frame) -> Bool {
        guard !previewInFlight, let data = try? encode(frame) else { return false }
        previewInFlight = true
        connection.send(content: data, completion: .contentProcessed { [weak self] err in
            guard let self else { return }
            self.previewInFlight = false
            if let err { self.set(.failed(LinkFailure(err))) }
        })
        return true
    }

    // MARK: - Reading

    private func receive() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) {
            [weak self] data, _, complete, err in
            guard let self else { return }
            if let data, !data.isEmpty {
                do {
                    for frame in try self.reader.append(data) { self.onFrame?(frame) }
                } catch {
                    // A framing error means the two ends disagree about the
                    // protocol. Reading on past it parses the next header out
                    // of the middle of a payload, so the stream is already
                    // lost; say so and stop.
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
        state = new
        onState?(new)
    }

    private func log(_ message: String) {
        FileHandle.standardError.write(Data("scan: \(message)\n".utf8))
    }
}

/// Parameters shared by both ends: peer-to-peer enabled, Nagle off on control.
///
/// Plain TCP, with the pairing check done in the hello — see proof() in
/// Pairing.swift for why, and for what that does and does not buy.
///
/// `includePeerToPeer` is what makes this work with no shared Wi-Fi network at
/// all, and over a USB-C tether's link-local interface. Nagle off matters
/// because the control traffic is tiny — a `capture` verb is eight bytes — and
/// waiting to coalesce it would add up to 40 ms to a shutter press.
func parameters(role: PeerRole) -> NWParameters {
    let params = NWParameters.tcp
    params.includePeerToPeer = true
    if role == .control, let tcp = params.defaultProtocolStack
        .internetProtocol as? NWProtocolTCP.Options {
        tcp.noDelay = true
    }
    return params
}
