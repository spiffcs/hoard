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
    /// Called the moment a connection proves it knows the pairing code, before
    /// its partner has arrived.
    ///
    /// A session needs both connections, but the *fact of a paired Mac* is
    /// settled by the first hello that verifies — everything after it is a
    /// second TCP connect and a second round trip, which over peer-to-peer can
    /// take long enough to see. Waiting for `onSession` to say "connected" tells
    /// the operator something the phone already knew, late. The role comes with
    /// it so the consumer can decide what counts as usable: control carries
    /// every verb, event and price, and preview carries only the mirror window
    /// and fixture stills.
    ///
    /// May fire twice per session, once per connection. Consumers should be
    /// idempotent. Pair it with `onPeerLost` — anything acting on this is
    /// claiming a session that has not been assembled yet.
    public var onPeerVerified: ((PeerRole) -> Void)?
    /// Called when a verified connection's partner never arrived and the
    /// half-session was dropped. The counterpart to `onPeerVerified`.
    public var onPeerLost: (() -> Void)?
    public var onError: ((String) -> Void)?
    /// A peer just paired for the first time and was pinned.
    ///
    /// The phone uses this to burn the code it just showed: a pairing token
    /// that stays valid after it has been used is a password, and this one is
    /// six digits.
    public var onPaired: ((Data) -> Void)?

    /// Whether this phone is on the network at all.
    ///
    /// Separate from `onError` because that callback is not about the
    /// advertisement: it also carries a peer failing the pairing check and a
    /// peer opening two connections of one role, and neither says anything
    /// about whether the service is on the air. A consumer that rebuilt the
    /// listener on every `onError` would take the phone off the network every
    /// time someone mistyped six digits.
    public enum Advertisement: Sendable, Equatable {
        /// The listener reached `.ready`. The phone is findable, and any
        /// recovery that got it back here is over.
        case up
        /// The listener died and could not be brought back. Carries the plain
        /// reason, already out of framework vocabulary. A death that `recover`
        /// healed is never reported this way — `.up` is, when it comes back.
        case down(String)
    }

    /// Called when the advertisement comes up, and when it has gone down for
    /// good. The consumer's cue to correct what it is telling the person: a
    /// screen that still says "Waiting for hoard…" over a dead listener is
    /// waiting for something that cannot arrive.
    public var onAdvertisement: ((Advertisement) -> Void)?

    private let code: PairingCode
    /// Empty means "let Bonjour use the device's own name".
    ///
    /// Worth preferring: iOS 16 stopped handing apps the real device name
    /// without an entitlement, so `UIDevice.current.name` is a flat "iPhone"
    /// and every phone on the network would advertise identically. Bonjour's
    /// default service name is the name the user actually gave the device.
    private let name: String
    private let queue = DispatchQueue(label: "hoard-scan.listener")
    /// How long a verified connection waits for its partner before the
    /// half-session is dropped. Generous: the two connections are opened
    /// together, so the gap is one TCP handshake, and the only thing that makes
    /// it long is a peer-to-peer link still coming up. Settable so a test does
    /// not have to sit through it.
    var halfSessionTimeout: TimeInterval = 5
    /// Readable inside the module so a test can take the phone genuinely off
    /// the air — cancelling this is the only way to reproduce a dead
    /// advertisement, and a recovery test that cannot do that proves nothing.
    /// Written here only: `start` and `stop` own its life.
    private(set) var listener: NWListener?
    /// Consecutive deaths in the current episode, cleared the moment the
    /// service is advertising again.
    private var restarts = 0
    /// Whether a restart is already on its way, so two failure reports for one
    /// death do not each schedule one.
    private var restarting = false
    /// Set by `stop()` and cleared by `start()`. A restart scheduled a moment
    /// before the app took this listener down must not quietly put it back up.
    private var stopped = false
    /// Guards the four fields above.
    ///
    /// A lock rather than the serial `queue` the connection bookkeeping uses,
    /// because these are touched from both sides of that boundary: `start` and
    /// `stop` are called from the screen — `start` synchronously, since it
    /// throws — while the listener's own state arrives on `queue`. Hopping to
    /// the main queue instead was tried and is worse: it makes a library depend
    /// on the app's actor for its own bookkeeping, and a main queue nobody is
    /// draining (a test process, most of all) silently loses every restart.
    private let stateLock = NSLock()
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
    /// How many unverified connections may sit in `accepting` at once. A real
    /// session opens two; the allowance beyond that is retries. Anything more
    /// is not a Mac pairing, it is the local network being rude.
    private let maxUnverified = 8
    /// The frame ceiling for a connection that has not proved the code: a
    /// hello is a few hundred bytes, so nothing legitimate is bigger than
    /// this. Verified connections are restored to the full ceiling.
    private let helloPayloadLimit = 4096
    /// How long an accepted connection may sit silent before it is dropped.
    /// The hello is sent before the handshake even completes, so a peer that
    /// is quiet this long is not going to speak.
    var helloTimeout: TimeInterval = 5
    /// New connections are refused until this instant — pushed back on every
    /// failed pairing proof, which converts a code-guessing loop from
    /// wire-speed into one attempt a second. Success is never delayed: the
    /// throttle arms on failure only, so a fat-fingered code costs its owner
    /// one second, not a lockout.
    private var refuseUntil = Date.distantPast

    /// What this listener presents and accepts. `nil` is plaintext — the way
    /// this link worked until 2026-08-08, kept only for tests about framing.
    private let trust: PeerTrust?
    /// Where a newly paired peer gets recorded. Separate from `trust` because
    /// `trust` is a snapshot taken when parameters were built and this is a
    /// live store: pinning during a session must not mutate the set the
    /// running verify block closed over.
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
        // Restartable, deliberately: iOS tears an NWListener down when the
        // app suspends and never brings it back, so the phone's foreground
        // handler calls this again. The old listener is cancelled rather
        // than reused — a listener that has been through a suspension is
        // not reliably advertising even when its state claims otherwise.
        // Established sessions ride their own connections and are untouched.
        //
        // The old one still goes before the new one is built. That was already
        // the order and it is worth keeping now that a restart runs this path
        // by itself: two listeners claiming one Bonjour instance name have to
        // be resolved somehow, and the usual resolution is renaming the
        // newcomer — a phone that came back as "iPhone (2)" is invisible to a
        // Mac looking for the name it knows. Not measured; the order is free.
        stateLock.lock()
        let previous = listener
        listener = nil
        stopped = false
        stateLock.unlock()
        previous?.cancel()

        // The role only affects Nagle, and the listener accepts both kinds on
        // one port; control's parameters are the stricter of the two.
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
        // A `stop()` that landed while this was being built wins: it was asked
        // for after this start, and a listener that outlives the call that took
        // it down is one advertising a code the app has already rotated away
        // from.
        guard !stopped else {
            stateLock.unlock()
            listener.cancel()
            return
        }
        self.listener = listener
        stateLock.unlock()
    }

    /// listenerStateChanged is the listener's own health, handled in one place.
    ///
    /// Internal rather than private for the test: an NWListener cannot be made
    /// to fail on demand, and the recovery below is the part worth proving, so
    /// the state the framework would have delivered is delivered by hand.
    func listenerStateChanged(_ state: NWListener.State) {
        switch state {
        case .ready:
            advertising()
        case .failed(let err):
            recover(from: LinkFailure(err))
        default:
            // `.cancelled` included, deliberately: `stop()` and every restart
            // cancel the old listener, so treating a cancellation as a death
            // would have the phone fighting its own teardown.
            break
        }
    }

    /// advertising records that the service is on the air, and closes whatever
    /// recovery episode got it there.
    private func advertising() {
        stateLock.lock()
        restarts = 0
        stateLock.unlock()
        onAdvertisement?(.up)
    }

    /// recover brings a dead advertisement back.
    ///
    /// An NWListener that fails is not replaced by anything. Until this existed
    /// the failure was reported through `onError` and that was all, so a phone
    /// whose listener died went off Bonjour and stayed off — the same thing the
    /// operator sees when a session teardown takes the listener with it, and
    /// the same only-repairing-fixes-it dead end, reached from the other side.
    ///
    /// Shaped after `Sounds.restartEngine` on the phone: try, be idempotent —
    /// `start()` rebuilds rather than mutates — and say plainly when it cannot.
    /// The failure is reported first and always, because the phone *is* off the
    /// network at this instant whatever happens next, and hiding a real death
    /// behind a hoped-for recovery is the same dishonesty in the other
    /// direction.
    ///
    /// Bounded on purpose. The common death is transient and comes straight
    /// back; a listener that has failed three times in five seconds will not be
    /// fixed by a fourth attempt, and a restart loop against a permanent
    /// failure drains the battery while every attempt briefly looks like
    /// recovery. Giving up is not the end of the road — returning to the
    /// foreground calls `start()` again.
    private func recover(from failure: LinkFailure) {
        stateLock.lock()
        // A listener the app has already taken down did not fail; it was
        // dismissed. Neither news nor something to undo.
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
            // Either a restart is already on its way — two reports of one death
            // — or the episode is over and this phone is off the network until
            // something outside asks again.
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
                // `.ready` on the rebuilt listener is what reports success. A
                // `start()` that returns has created a listener, which is not
                // the same as one that is advertising.
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

    /// How long to wait before the restart answering `failures` consecutive
    /// deaths, or nil once the episode is over and the honest answer is that
    /// the phone is off the network.
    ///
    /// Immediate first, because the operator is mid-pile and the usual death
    /// is a blink; spaced after that, because a listener failing twice in a row
    /// is failing for a reason a third instant attempt will not change.
    static let restartDelays: [TimeInterval] = [0, 1, 4]

    static func restartDelay(after failures: Int) -> TimeInterval? {
        guard failures >= 0, failures < restartDelays.count else { return nil }
        return restartDelays[failures]
    }

    public func stop() {
        // `stopped` under the same lock as the listener it describes, and set
        // before the cancel rather than after: the cancellation is what a
        // pending restart would race, so the flag it consults has to be true
        // by the time anything can observe the teardown.
        stateLock.lock()
        stopped = true
        let dying = listener
        listener = nil
        stateLock.unlock()
        dying?.cancel()
        // The dictionaries live on `queue` — accept and pair mutate them
        // there — so the teardown hops rather than racing them from the main
        // actor ("Generate a new code" mid-handshake was a Dictionary race).
        queue.async {
            self.pending.values.forEach { $0.cancel() }
            self.pending.removeAll()
            self.accepting.values.forEach { $0.cancel() }
            self.accepting.removeAll()
        }
    }

    /// accept reads the hello, then either parks the connection or completes a
    /// session with its partner.
    ///
    /// The link starts as control and is corrected on the hello: a connection
    /// has to be up and reading before it can be told what it is for, and the
    /// role only selects Nagle on the *parameters*, which the listener already
    /// fixed when it started.
    private func accept(_ conn: NWConnection) {
        // The door itself is guarded: a failed proof arms a one-second
        // refusal (see refuseUntil), and the unverified pool is capped —
        // both against the same neighbor, one guessing codes at wire speed,
        // the other opening connections it never speaks on.
        guard Date() >= refuseUntil, accepting.count < maxUnverified else {
            conn.cancel()
            return
        }
        let link = PeerLink(connection: conn, role: .control, queue: queue)
        // Nothing bigger than a hello until the code is proved.
        link.limitPayloads(helloPayloadLimit)
        accepting[ObjectIdentifier(link)] = link
        // A connection that never says hello does not get to sit in the pool
        // until stop(): by identity, like the half-session reaper below.
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
            // The gate. A peer that cannot prove it knows the code gets its
            // connection dropped and never reaches the session — the scanner
            // auto-commits, so this is the difference between a companion app
            // and an open write endpoint on the local network.
            // An already-pinned peer does not present the code at all.
            //
            // This is what makes a rotating code possible, and it is the whole
            // point of the identity work: a peer that completed TLS against a
            // fingerprint this device pinned has already proved something
            // stronger than six digits, and demanding the digits again would
            // mean the code could never change without breaking every existing
            // pairing. That is exactly the constraint LinkController's comment
            // described when it explained why the code was static.
            //
            // Note the ordering this depends on: `acceptUnknown` false means an
            // unpinned peer cannot finish the handshake, so anything reaching
            // here with an unknown fingerprint is in an open pairing window.
            let known = self.pins.flatMap { store in
                link.peerFingerprint.map { store.all.contains($0) }
            } ?? false

            // Bound to *this* listener's own certificate. The peer signed the
            // fingerprint it observed, so a relay that terminated TLS and
            // re-opened it presents its own certificate to the peer and cannot
            // produce a proof that matches what is checked here — which is the
            // whole reason the fingerprint is in the MAC.
            guard known || verifyProof(
                hello.proof, session: hello.session, code: self.code,
                ownFingerprint: self.trust?.identity.fingerprint) else {
                self.accepting.removeValue(forKey: ObjectIdentifier(link))
                link.cancel()
                self.refuseUntil = Date().addingTimeInterval(1)
                self.onError?("a peer failed the pairing check")
                return
            }
            // One hello per connection; everything after it is session traffic
            // and belongs to whoever took the session.
            // Proved the code and, if this is a TLS link, proved which
            // certificate it was looking at. That is the trust-on-first-use
            // moment: from here the fingerprint authenticates and the code is
            // not needed again.
            if !known, let pins = self.pins, let seen = link.peerFingerprint {
                pins.pin(seen, name: self.name)
                self.onPaired?(seen)
            }
            link.onFrame = nil
            self.accepting.removeValue(forKey: ObjectIdentifier(link))
            link.assign(role: hello.role)
            link.limitPayloads(maxFramePayload)
            // Announced here rather than after `pair`, which is the whole point:
            // this is the earliest moment the phone can prove a paired Mac is on
            // the wire, and `pair` may park the connection for as long as the
            // partner takes to arrive.
            self.onPeerVerified?(hello.role)
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
            // A parked connection needs an end. Before `onPeerVerified` existed
            // a partner that never came was merely a leak — the entry sat in
            // `pending` until `stop()`, holding a live connection open for the
            // life of the app. Now it is also a lie: something has already been
            // told a paired Mac is here, and nothing would ever correct it.
            queue.asyncAfter(deadline: .now() + halfSessionTimeout) { [weak self, weak link] in
                guard let self, let link else { return }
                // By identity, not by key. The partner may have arrived and
                // taken this entry, and a new session could since have parked
                // under a fresh id — removing by key alone would tear down a
                // live connection to clean up a dead one.
                guard self.pending[session] === link else { return }
                self.pending.removeValue(forKey: session)
                link.cancel()
                self.onPeerLost?()
            }
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
        // Through the queue, not directly: the results handler writes `found`
        // on `queue`, and `cancel()` is asynchronous — a handler already
        // running when the deadline lapsed can still be mid-write here. The
        // sync hop serialises this read behind it.
        return queue.sync { found.values.sorted { $0.name < $1.name } }
    }

    /// connect opens both connections of a session.
    public func connect(
        to service: PeerService, code: PairingCode, trust: PeerTrust? = nil
    ) -> PeerSession {
        let session = UUID().uuidString
        func open(_ role: PeerRole) -> PeerLink {
            let conn = NWConnection(
                to: service.endpoint,
                using: parameters(role: role, trust: trust, queue: queue))
            let link = PeerLink(connection: conn, role: role, queue: queue)
            // The hello now waits for `.ready` rather than going out
            // immediately. It used to rely on NWConnection queueing sends made
            // before the handshake completes — true, and no longer sufficient:
            // the proof commits to the peer's certificate fingerprint, and
            // that does not exist until TLS has settled. A hello sent early
            // would carry a proof bound to nothing and fail the check on the
            // far side, which would report as a wrong pairing code.
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
