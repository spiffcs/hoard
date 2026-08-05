// The two ends of the link.
//
// The phone listens and the Mac browses, which is the opposite of what the
// process hierarchy suggests — the Mac spawns the helper, so the Mac feels like
// the server. It is the wrong way round for this: the phone owns the camera and
// sits in a stand for hours, while the Mac's helper is started and stopped per
// scanning session. The long-lived thing advertises.
//
// A session is two connections, matched by a session id in their hellos. See
// PeerRole for why two.

import Foundation
import Network
import ScanWire

// MARK: - The phone

/// PeerListener advertises the scanner and accepts sessions.
public final class PeerListener {
    /// Called once both connections of a session have arrived and said hello.
    public var onSession: ((PeerSession) -> Void)?
    public var onError: ((String) -> Void)?

    private let code: PairingCode
    /// Empty means "let Bonjour use the device's own name".
    ///
    /// Worth preferring: iOS 16 stopped handing apps the real device name
    /// without an entitlement, so `UIDevice.current.name` is a flat "iPhone"
    /// and every phone on the network would advertise identically. Bonjour's
    /// default service name is the name the user actually gave the device.
    private let name: String
    private let queue = DispatchQueue(label: "hoard-scan.listener")
    private var listener: NWListener?
    /// Connections that have said hello but whose partner has not arrived yet.
    private var pending: [String: PeerLink] = [:]
    /// Connections that are up but have not said hello yet.
    ///
    /// Held deliberately. A link only retained by its own callbacks is not
    /// retained at all — the closures capture it weakly to avoid a cycle, so
    /// without this the wrapper deallocates the moment `accept` returns and the
    /// receive loop fires into a nil `self`. The connection stays open, the
    /// handshake completes, and nothing ever arrives: a hang, not an error.
    private var accepting: [ObjectIdentifier: PeerLink] = [:]

    public init(name: String, code: PairingCode) {
        self.name = name
        self.code = code
    }

    public func start() throws {
        // The role only affects Nagle, and the listener accepts both kinds on
        // one port; control's parameters are the stricter of the two.
        let params = parameters(role: .control)
        let listener = try NWListener(using: params)
        listener.service = name.isEmpty
            ? NWListener.Service(type: scanServiceType)
            : NWListener.Service(name: name, type: scanServiceType)
        listener.newConnectionHandler = { [weak self] conn in
            self?.accept(conn)
        }
        listener.stateUpdateHandler = { [weak self] st in
            if case .failed(let err) = st {
                self?.onError?(LinkFailure(err).reason)
            }
        }
        listener.start(queue: queue)
        self.listener = listener
    }

    public func stop() {
        listener?.cancel()
        listener = nil
        pending.values.forEach { $0.cancel() }
        pending.removeAll()
        accepting.values.forEach { $0.cancel() }
        accepting.removeAll()
    }

    /// accept reads the hello, then either parks the connection or completes a
    /// session with its partner.
    ///
    /// The link starts as control and is corrected on the hello: a connection
    /// has to be up and reading before it can be told what it is for, and the
    /// role only selects Nagle on the *parameters*, which the listener already
    /// fixed when it started.
    private func accept(_ conn: NWConnection) {
        let link = PeerLink(connection: conn, role: .control, queue: queue)
        accepting[ObjectIdentifier(link)] = link
        link.onFrame = { [weak self, weak link] frame in
            guard let self, let link else { return }
            guard frame.kind == .ndjson,
                  let hello = try? JSONDecoder().decode(PeerHello.self, from: frame.payload)
            else { return }
            // The gate. A peer that cannot prove it knows the code gets its
            // connection dropped and never reaches the session — the scanner
            // auto-commits, so this is the difference between a companion app
            // and an open write endpoint on the local network.
            guard verifyProof(hello.proof, session: hello.session, code: self.code) else {
                self.accepting.removeValue(forKey: ObjectIdentifier(link))
                link.cancel()
                self.onError?("a peer failed the pairing check")
                return
            }
            // One hello per connection; everything after it is session traffic
            // and belongs to whoever took the session.
            link.onFrame = nil
            self.accepting.removeValue(forKey: ObjectIdentifier(link))
            link.assign(role: hello.role)
            self.pair(link, session: hello.session)
        }
        link.onState = { [weak self, weak link] state in
            guard let self, let link else { return }
            // A connection that fails or is cancelled before saying hello is
            // never coming back; drop it rather than leaking a link per failed
            // handshake for the life of the session.
            switch state {
            case .failed, .cancelled:
                self.accepting.removeValue(forKey: ObjectIdentifier(link))
            default:
                break
            }
        }
        link.start()
    }

    /// pair holds the first connection of a session until its partner arrives.
    private func pair(_ link: PeerLink, session: String) {
        guard let partner = pending.removeValue(forKey: session) else {
            pending[session] = link
            return
        }
        let control = link.role == .control ? link : partner
        let preview = link.role == .preview ? link : partner
        // Two connections claiming the same role is a peer that is confused or
        // not ours. Drop both rather than hand back a session whose "preview"
        // is a second control channel.
        guard control !== preview else {
            link.cancel()
            partner.cancel()
            onError?("a peer opened two \(link.role.rawValue) connections")
            return
        }
        onSession?(PeerSession(control: control, preview: preview))
    }
}

/// A matched pair of connections.
public struct PeerSession {
    public let control: PeerLink
    public let preview: PeerLink

    public func cancel() {
        control.cancel()
        preview.cancel()
    }
}

// MARK: - The Mac

/// A scanner advertising itself on the network.
public struct PeerService: Equatable, Sendable {
    public let name: String
    public let endpoint: NWEndpoint

    /// The stable-ish identity the Go side uses to remember a choice. Bonjour
    /// names are user-visible and can change; this is the best available, and
    /// the picker falls back to name matching when it does not resolve.
    public var id: String { name }
}

/// PeerBrowser finds scanners.
public final class PeerBrowser {
    private let queue = DispatchQueue(label: "hoard-scan.browser")
    private var browser: NWBrowser?
    private var found: [String: PeerService] = [:]

    public init() {}

    /// browse collects services for `seconds` and returns what it saw.
    ///
    /// A blocking sweep rather than a subscription because the caller is
    /// `--list-devices`, which is a one-shot question. It mirrors the patience
    /// `spinRunLoop` already shows a Continuity Camera, and for the same
    /// reason: a device that is a beat slow to appear is not an absent device.
    public func browse(seconds: Double) -> [PeerService] {
        let params = NWParameters()
        params.includePeerToPeer = true
        let browser = NWBrowser(
            for: .bonjour(type: scanServiceType, domain: nil), using: params)
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            for result in results {
                if case .service(let name, _, _, _) = result.endpoint {
                    self?.found[name] = PeerService(name: name, endpoint: result.endpoint)
                }
            }
        }
        browser.start(queue: queue)
        self.browser = browser

        let deadline = Date().addingTimeInterval(seconds)
        while Date() < deadline {
            RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
        }
        browser.cancel()
        return found.values.sorted { $0.name < $1.name }
    }

    /// connect opens both connections of a session.
    public func connect(to service: PeerService, code: PairingCode) -> PeerSession {
        let session = UUID().uuidString
        func open(_ role: PeerRole) -> PeerLink {
            let conn = NWConnection(to: service.endpoint, using: parameters(role: role))
            let link = PeerLink(connection: conn, role: role, queue: queue)
            link.start()
            // The hello goes out immediately; NWConnection queues sends made
            // before ready and flushes them when the handshake completes, so
            // there is nothing to wait for.
            let hello = PeerHello(
                role: role, session: session,
                proof: proof(session: session, code: code))
            if let frame = Frame.json(hello) {
                link.send(frame)
            }
            return link
        }
        return PeerSession(control: open(.control), preview: open(.preview))
    }
}
