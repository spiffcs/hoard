import Foundation
import Network
import ScanWire

public final class PeerListener {
    public var onSession: ((PeerSession) -> Void)?
    public var onPeerVerified: ((PeerRole) -> Void)?
    public var onPeerLost: (() -> Void)?
    public var onError: ((String) -> Void)?
    public var onPaired: ((Data) -> Void)?

    public enum Advertisement: Sendable, Equatable {
        case up
        case down(String)
    }

    public var onAdvertisement: ((Advertisement) -> Void)?

    private let code: PairingCode
    private let name: String
    private let queue = DispatchQueue(label: "hoard-scan.listener")
    var halfSessionTimeout: TimeInterval = 5
    private(set) var listener: NWListener?
    private var restarts = 0
    private var restarting = false
    private var stopped = false
    private let stateLock = NSLock()
    private var pending: [String: PeerLink] = [:]
    private var accepting: [ObjectIdentifier: PeerLink] = [:]
    private let maxUnverified = 8
    private let helloPayloadLimit = 4096
    var helloTimeout: TimeInterval = 5
    private var refuseUntil = Date.distantPast

    private let trust: PeerTrust?
    private let pins: PinnedPeers?

    public init(
        name: String, code: PairingCode, trust: PeerTrust? = nil, pins: PinnedPeers? = nil
    ) {
        self.name = name
        self.code = code
        self.trust = trust
        self.pins = pins
    }

    public func start() throws {
        stateLock.lock()
        let previous = listener
        listener = nil
        stopped = false
        stateLock.unlock()
        previous?.cancel()

        let params = parameters(role: .control, trust: trust, queue: queue)
        let listener = try NWListener(using: params)
        listener.service = name.isEmpty
            ? NWListener.Service(type: scanServiceType)
            : NWListener.Service(name: name, type: scanServiceType)
        listener.newConnectionHandler = { [weak self] conn in
            self?.accept(conn)
        }
        listener.stateUpdateHandler = { [weak self] st in
            self?.listenerStateChanged(st)
        }
        listener.start(queue: queue)
        stateLock.lock()
        guard !stopped else {
            stateLock.unlock()
            listener.cancel()
            return
        }
        self.listener = listener
        stateLock.unlock()
    }

    func listenerStateChanged(_ state: NWListener.State) {
        switch state {
        case .ready:
            advertising()
        case .failed(let err):
            recover(from: LinkFailure(err))
        default:
            break
        }
    }

    private func advertising() {
        stateLock.lock()
        restarts = 0
        stateLock.unlock()
        onAdvertisement?(.up)
    }

    private func recover(from failure: LinkFailure) {
        stateLock.lock()
        guard !stopped else {
            stateLock.unlock()
            return
        }
        let alreadyRestarting = restarting
        let delay = alreadyRestarting ? nil : PeerListener.restartDelay(after: restarts)
        if delay != nil {
            restarts += 1
            restarting = true
        }
        stateLock.unlock()

        onError?(failure.reason)
        guard let delay else {
            if !alreadyRestarting { onAdvertisement?(.down(failure.reason)) }
            return
        }
        queue.asyncAfter(deadline: .now() + delay) { [weak self] in
            guard let self else { return }
            self.stateLock.lock()
            self.restarting = false
            let dismissed = self.stopped
            self.stateLock.unlock()
            guard !dismissed else { return }
            do {
                try self.start()
            } catch let error as NWError {
                self.recover(from: LinkFailure(error))
            } catch {
                self.recover(from: LinkFailure(
                    reason: "Could not get back on the network. Check Wi-Fi",
                    detail: "\(error)"))
            }
        }
    }

    static let restartDelays: [TimeInterval] = [0, 1, 4]

    static func restartDelay(after failures: Int) -> TimeInterval? {
        guard failures >= 0, failures < restartDelays.count else { return nil }
        return restartDelays[failures]
    }

    public func stop() {
        stateLock.lock()
        stopped = true
        let dying = listener
        listener = nil
        stateLock.unlock()
        dying?.cancel()
        queue.async {
            self.pending.values.forEach { $0.cancel() }
            self.pending.removeAll()
            self.accepting.values.forEach { $0.cancel() }
            self.accepting.removeAll()
        }
    }

    private func accept(_ conn: NWConnection) {
        guard Date() >= refuseUntil, accepting.count < maxUnverified else {
            conn.cancel()
            return
        }
        let link = PeerLink(connection: conn, role: .control, queue: queue)
        link.limitPayloads(helloPayloadLimit)
        accepting[ObjectIdentifier(link)] = link
        queue.asyncAfter(deadline: .now() + helloTimeout) { [weak self, weak link] in
            guard let self, let link,
                  self.accepting[ObjectIdentifier(link)] === link else { return }
            self.accepting.removeValue(forKey: ObjectIdentifier(link))
            link.cancel()
        }
        link.onFrame = { [weak self, weak link] frame in
            guard let self, let link else { return }
            guard frame.kind == .ndjson,
                  let hello = try? JSONDecoder().decode(PeerHello.self, from: frame.payload)
            else { return }
            let known = self.pins.flatMap { store in
                link.peerFingerprint.map { store.all.contains($0) }
            } ?? false

            guard known || verifyProof(
                hello.proof, session: hello.session, code: self.code,
                ownFingerprint: self.trust?.identity.fingerprint) else {
                self.accepting.removeValue(forKey: ObjectIdentifier(link))
                link.cancel()
                self.refuseUntil = Date().addingTimeInterval(1)
                self.onError?("a peer failed the pairing check")
                return
            }
            if !known, let pins = self.pins, let seen = link.peerFingerprint {
                pins.pin(seen, name: self.name)
                self.onPaired?(seen)
            }
            link.onFrame = nil
            self.accepting.removeValue(forKey: ObjectIdentifier(link))
            link.assign(role: hello.role)
            link.limitPayloads(maxFramePayload)
            self.onPeerVerified?(hello.role)
            self.pair(link, session: hello.session)
        }
        link.onState = { [weak self, weak link] state in
            guard let self, let link else { return }
            switch state {
            case .failed, .cancelled:
                self.accepting.removeValue(forKey: ObjectIdentifier(link))
            default:
                break
            }
        }
        link.start()
    }

    private func pair(_ link: PeerLink, session: String) {
        guard let partner = pending.removeValue(forKey: session) else {
            pending[session] = link
            queue.asyncAfter(deadline: .now() + halfSessionTimeout) { [weak self, weak link] in
                guard let self, let link else { return }
                guard self.pending[session] === link else { return }
                self.pending.removeValue(forKey: session)
                link.cancel()
                self.onPeerLost?()
            }
            return
        }
        let control = link.role == .control ? link : partner
        let preview = link.role == .preview ? link : partner
        guard control !== preview else {
            link.cancel()
            partner.cancel()
            onError?("a peer opened two \(link.role.rawValue) connections")
            return
        }
        onSession?(PeerSession(control: control, preview: preview))
    }
}

public struct PeerSession {
    public let control: PeerLink
    public let preview: PeerLink

    public func cancel() {
        control.cancel()
        preview.cancel()
    }
}

public struct PeerService: Equatable, Sendable {
    public let name: String
    public let endpoint: NWEndpoint

    public var id: String { name }
}

public final class PeerBrowser {
    private let queue = DispatchQueue(label: "hoard-scan.browser")
    private var browser: NWBrowser?
    private var found: [String: PeerService] = [:]

    public init() {}

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
        return queue.sync { found.values.sorted { $0.name < $1.name } }
    }

    public func connect(
        to service: PeerService, code: PairingCode, trust: PeerTrust? = nil
    ) -> PeerSession {
        let session = UUID().uuidString
        func open(_ role: PeerRole) -> PeerLink {
            let conn = NWConnection(
                to: service.endpoint,
                using: parameters(role: role, trust: trust, queue: queue))
            let link = PeerLink(connection: conn, role: role, queue: queue)
            link.onReady = { [weak link] in
                guard let link else { return }
                let hello = PeerHello(
                    role: role, session: session,
                    proof: proof(session: session, code: code,
                                 peerFingerprint: link.peerFingerprint))
                if let frame = Frame.json(hello) {
                    link.send(frame)
                }
            }
            link.start()
            return link
        }
        return PeerSession(control: open(.control), preview: open(.preview))
    }
}
