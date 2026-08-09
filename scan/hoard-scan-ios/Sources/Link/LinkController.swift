// The phone's half of the conversation with hoard.
//
// The phone advertises, the Mac connects. That is the opposite of what the
// process hierarchy suggests — the Mac spawns the helper, so the Mac feels like
// the server — and it is right for this: the phone owns the camera and sits in
// a stand for hours, while the helper starts and stops per scanning session.
// The long-lived thing advertises.
//
// What crosses the wire is small. Scan events out, verbs in, prices back. The
// pixels stay here.

import CardKit
import Foundation
import ScanLink
import ScanWire
import ImageIO
import SwiftUI
import UniformTypeIdentifiers

@MainActor
final class LinkController: ObservableObject {
    /// Shown on screen for the operator to type on the Mac. Persisted in the
    /// keychain and stable across launches, so a pairing made once keeps
    /// working; `newCode()` is the deliberate way to revoke one.
    @Published private(set) var code: PairingCode
    @Published private(set) var connected = false
    @Published private(set) var status = "Waiting for hoard…"
    /// The last price the Mac sent back, and a counter so the overlay restarts
    /// even when two identical prices land in a row.
    @Published private(set) var price = PriceResult()
    @Published private(set) var priceSequence = 0
    /// The second-copy offer, when one stands: the parent suppressed a
    /// sighting as a repeat and the operator may overrule it from here — the
    /// screen they are actually watching mid-pile. Cleared by the button, by
    /// the next result (a new card landed, the question is stale), or by the
    /// same 30s window the terminal's `+` honours.
    @Published private(set) var dupOffer: String?
    private var dupOfferAt = Date.distantPast

    /// Raised when the Mac asks for a capture. The view owns the camera, so the
    /// controller asks rather than reaches.
    var onCapture: (() -> Void)?
    /// Raised for auto-on/auto-off, so the trigger can be armed from the
    /// terminal exactly as it is on the local path.
    var onAuto: ((Bool) -> Void)?
    /// Raised for the parent's content-aware nudge. Geometry cannot tell a card
    /// stacked squarely on the pile from the card just shot; the terminal knows
    /// what it already processed.
    var onRearm: (() -> Void)?
    /// Fired when a result lands: the recognition-time rearm — see `.result`.
    var onResult: (() -> Void)?
    /// One-shot exposure bias for the next auto capture — the finish
    /// rescue's darker retake. Restored camera-side after that capture.
    var onEVBias: ((Double) -> Void)?
    /// Set the trigger's stillness knobs, for a tuning session.
    var onTune: ((Int, Double) -> Void)?
    /// Raised for torch-on/torch-off. Advertised in the feature list, so it has
    /// to do something: a capability announced and then ignored is worse than
    /// one never offered, because the terminal reports success either way.
    var onTorch: ((Bool) -> Void)?

    /// Whether hands-free is possible at all. False when the session refused a
    /// video tap; the ready event reports it honestly rather than advertising a
    /// trigger the parent would then send commands to.
    private var autoAvailable = false

    private var listener: PeerListener
    private let deviceName: String
    private var session: PeerSession?
    let sounds = Sounds()

    /// This phone's certificate and the Macs it has paired with.
    private let identityService = "dev.spiffcs.hoard.scan.ios.identity"
    private let pins = PinnedPeers(service: "dev.spiffcs.hoard.scan.ios.pins")

    /// Whether an unpaired Mac may complete a handshake right now.
    ///
    /// Open when nothing is paired — otherwise a fresh install could never
    /// pair at all — and whenever the person asks to add a Mac. Closed again
    /// the moment a pairing succeeds, which is what makes the window narrow
    /// enough for six digits to be an appropriate size of secret.
    ///
    /// Mirrored into `openGate` because this property is main-actor isolated
    /// and the TLS verify block that consults it runs on Network.framework's
    /// own queue. Two representations of one fact is a smell; the alternative
    /// is reading main-actor state from a network callback, which is worse.
    @Published private(set) var pairingOpen: Bool {
        didSet { openGate.set(pairingOpen) }
    }

    private let openGate = PairingGate()

    init(deviceName: String = "") {
        // Regenerated, not restored — the reverse of what this did until
        // 2026-08-08, and the reversal is a consequence of pinning rather than
        // a change of mind. The old comment was right on its own terms: while
        // the code was the *only* thing identifying a peer, rotating it
        // silently invalidated every pairing, so it had to be stable for the
        // life of the install. That made a six-digit secret a permanent one,
        // which is the worst combination available.
        //
        // Under trust-on-first-use the code identifies nothing. A paired Mac
        // is recognised by the certificate it pinned, and PeerEnds skips the
        // proof entirely for a peer it already knows — so the code is a
        // one-time introduction token, and a token that outlives its use is
        // just a password. It is therefore generated per launch, held only in
        // memory, and burned as soon as it has been used.
        //
        // PairingStore is deliberately no longer read or written. Nothing
        // needs the code to survive a launch, and a six-digit secret sitting
        // in the keychain forever is a liability with no remaining purpose.
        let fresh = PairingCode.random()
        self.code = fresh
        self.deviceName = deviceName
        let openAtLaunch = pins.all.isEmpty
        self.pairingOpen = openAtLaunch
        openGate.set(openAtLaunch)
        listener = LinkController.makeListener(
            name: deviceName, code: fresh, identityService: identityService,
            pins: pins, gate: openGate)
        wireListener()
    }

    /// Builds a listener carrying this phone's identity and trust policy.
    ///
    /// Static because it runs from `init` before `self` is available, and
    /// factored because rotation rebuilds the listener rather than mutating
    /// it — a listener whose key or trust can change under a live connection
    /// is a bug waiting for a busy session.
    private static func makeListener(
        name: String, code: PairingCode, identityService: String,
        pins: PinnedPeers, gate: PairingGate
    ) -> PeerListener {
        guard let identity = try? loadOrCreatePeerIdentity(service: identityService) else {
            // Plaintext rather than no link at all. A phone that cannot reach
            // its own keychain — locked before first unlock, most likely —
            // would otherwise refuse to advertise with no way to say why, and
            // the Mac would report a phone that is not there. The Pair tab
            // shows the downgrade; see `encrypted`.
            return PeerListener(name: name, code: code)
        }
        // Both closures are read per handshake. `pins.all` in particular must
        // not be snapshotted: the Mac pinned during this session has to be
        // recognised on its next connection, and the listener is not rebuilt
        // in between.
        return PeerListener(
            name: name, code: code,
            trust: PeerTrust(
                identity: identity, pinned: { pins.all }, acceptUnknown: { gate.isOpen }),
            pins: pins)
    }

    /// Whether the link this phone is advertising is encrypted.
    ///
    /// False only in the keychain-unavailable fallback above. Surfaced so the
    /// Pair tab can say so rather than letting a downgrade pass silently —
    /// which is the failure mode this whole change exists to remove.
    var encrypted: Bool { (try? loadOrCreatePeerIdentity(service: identityService)) != nil }

    /// How many Macs this phone has paired with.
    var pairedCount: Int { pins.all.count }

    private func wireListener() {
        listener.onSession = { [weak self] session in
            Task { @MainActor in self?.adopt(session) }
        }
        // Control only. A session is two connections and the preview one
        // carries the mirror window and fixture stills — nothing the person
        // holding a card is waiting on. Control carries every verb, every scan
        // event and every price, so a verified control connection is the point
        // at which "hoard connected" is true.
        listener.onPeerVerified = { [weak self] role in
            guard role == .control else { return }
            Task { @MainActor in self?.markVerified() }
        }
        listener.onPeerLost = { [weak self] in
            Task { @MainActor in self?.markLost() }
        }
        listener.onError = { [weak self] message in
            Task { @MainActor in self?.status = message }
        }
        // A Mac just paired and was pinned. Close the window and burn the code
        // it used, in that order — the rotation restarts the listener, so
        // doing it while the new session is being adopted would tear down the
        // connection that just succeeded. Deferred to the next main-actor turn
        // for exactly that reason.
        listener.onPaired = { [weak self] _ in
            // Close the window the instant a Mac pairs. Deliberately *not* a
            // rotation here: rotating rebuilds the listener, and rebuilding it
            // would cancel the connection that just succeeded. Closing the
            // gate is enough to make the code inert — an unpinned peer can no
            // longer complete TLS, so it never reaches the point of being
            // asked for digits — and the code itself is replaced the next time
            // pairing is opened. The Pair tab stops showing it in the
            // meantime, so a stale code is never on screen.
            Task { @MainActor in self?.pairingOpen = false }
        }
    }

    /// markVerified says connected as soon as the code has been proved, without
    /// waiting for the preview connection to finish arriving.
    ///
    /// Guarded on `session` rather than on `connected`: a live session has
    /// already said everything this would say, and re-announcing over it is how
    /// a reconnect mid-box would flicker.
    private func markVerified() {
        guard session == nil else { return }
        // The log's session boundary moved here with the status. A session now
        // begins when a Mac proves the code, not when its second connection
        // lands — and truncating in `adopt` would wipe the line below, which is
        // half of the measurement the boundary exists to preserve.
        SessionLog.startSession()
        connected = true
        status = "Connected"
        trace("peer verified")
    }

    /// releaseSession gives the whole session back when either leg dies.
    ///
    /// Cancelling the survivor is the half that is easy to forget: a session
    /// whose preview died still has a live control connection, and leaving it
    /// up means the Mac keeps believing in a session the phone has already
    /// abandoned. The survivor's own `.cancelled` callback then finds
    /// `session` nil and does nothing, which is what the identity guards are
    /// for.
    private func releaseSession() {
        let dead = session
        session = nil
        connected = false
        dead?.cancel()
    }

    /// markLost undoes markVerified when the partner connection never arrived.
    ///
    /// The cost of claiming a session early is having to give it back. Without
    /// this the header sits green over a link that was never assembled, which is
    /// worse than the delay this whole change removes.
    private func markLost() {
        guard session == nil else { return }
        connected = false
        status = "hoard started connecting but did not finish. Try again"
        trace("peer lost before the session was assembled")
    }

    /// setAutoAvailable records whether a video tap attached, and re-announces
    /// if the Mac is already listening.
    ///
    /// The re-announce is the point. The listener starts with the app, but the
    /// camera comes up a moment later on the scanning screen — so a Mac that
    /// connects in that gap gets a ready event saying hands-free is
    /// unavailable, never sends auto-on, and the trigger sits disarmed forever.
    /// The parent handles a second ready by design (it re-negotiates the whole
    /// feature list), which is what makes this safe rather than a hack.
    func setAutoAvailable(_ available: Bool) {
        let changed = autoAvailable != available
        autoAvailable = available
        if changed, connected { announceReady() }
    }

    /// start brings the listener up — again, if need be. Safe to call on
    /// every return to foreground: iOS tears the NWListener down on suspend
    /// and nothing else restarts it, so a phone switched away from and back
    /// used to stay invisible to the Mac's picker until the app was killed.
    /// PeerListener.start() rebuilds its listener each call; established
    /// sessions ride their own connections and are untouched.
    func start() {
        do {
            try listener.start()
            // Not while connected: a brief background can leave the session
            // alive, and stamping "Waiting" over a live link's "Connected"
            // would be the header lying in the other direction.
            if !connected { status = "Waiting for hoard, code \(code.display)" }
        } catch {
            SessionLog.write("listener failed: \(error.localizedDescription)")
            status = "Could not join the network. Check Wi-Fi and try again"
        }
    }

    func stop() {
        session?.cancel()
        session = nil
        listener.stop()
        connected = false
    }

    /// newCode rotates the pairing code and reopens the pairing window.
    ///
    /// "Add a Mac". It no longer revokes anything — that is `forgetMacs()`
    /// now, and separating the two is the point: rotating a code used to be
    /// the only way to revoke, so the two actions could not be taken
    /// independently, and the destructive one happened whether or not it was
    /// wanted.
    func newCode() {
        rotate(open: true)
    }

    /// Unpairs every Mac. The revocation half of what `newCode` used to do.
    ///
    /// A Mac that is forgotten cannot complete a handshake at all on its next
    /// attempt, because its certificate is no longer pinned — it does not get
    /// as far as being asked for a code.
    func forgetMacs() {
        pins.forgetAll()
        rotate(open: true)
    }

    /// Burns the current code and rebuilds the listener.
    ///
    /// Called on every pairing, successful or abandoned, because a pairing
    /// token that is still valid after it has been used is a password. The
    /// listener is rebuilt rather than mutated for the reason its own comment
    /// gives: its code and trust are fixed at construction, and a live
    /// connection must not see either change underneath it.
    private func rotate(open: Bool) {
        code = PairingCode.random()
        pairingOpen = open
        stop()
        listener = LinkController.makeListener(
            name: deviceName, code: code, identityService: identityService,
            pins: pins, gate: openGate)
        wireListener()
        start()
    }

    private func adopt(_ session: PeerSession) {
        // No SessionLog.startSession() here: markVerified already opened the
        // log for this session, and it always runs first — adopt cannot be
        // reached without the control connection having verified.
        self.session = session
        connected = true
        status = "Connected"
        // Both halves are timestamped so the gap this change closes stays
        // measurable. `peer verified` lands in the log first and is what the
        // screen now follows; this line is the moment the session was actually
        // assembled. They should be close, and a pairing where they are not is
        // the peer-to-peer link taking its time — which is the whole reason the
        // screen no longer waits for it.
        trace("session adopted")
        session.control.onFrame = { [weak self] frame in
            Task { @MainActor in self?.handle(frame) }
        }
        // `control` weakly, and never the whole PeerSession: this closure is
        // stored *on* session.control, so capturing the struct that holds it
        // would be a link retaining itself through its own state handler.
        session.control.onState = { [weak self, weak control = session.control] state in
            Task { @MainActor in
                guard let self else { return }
                switch state {
                case .failed(let failure):
                    self.connected = false
                    // The phone's own screen gets the plain reason too; the
                    // framework's wording goes to the session log, which is
                    // where a diagnosis starts.
                    self.status = failure.reason
                    self.trace("link failed: \(failure.detail)")
                    // Released, not just marked disconnected. A dropped link is
                    // expected to recover, and the Mac recovers it by pairing
                    // again — which markVerified refuses to report while a
                    // session is still held, so the reconnect would sit on
                    // "Not connected" for exactly as long as this change exists
                    // to avoid. Dropping it also stops `send` and `trace` from
                    // writing into a dead connection.
                    //
                    // By identity, and only if this is still the live session: a
                    // late failure from the connection that was just replaced
                    // would otherwise tear down the reconnect that replaced it.
                    if self.session?.control === control { self.releaseSession() }
                case .cancelled:
                    // A clean Mac shutdown cancels rather than fails, and
                    // handling only `.failed` left `connected` true — a green
                    // header over a dead link — with the stale session never
                    // released, so markVerified's `session == nil` guard
                    // refused the next Mac until a second connection happened
                    // to adopt. Same identity check as above; a local stop()
                    // has already nilled the session, so this is a no-op there.
                    guard self.session?.control === control else { return }
                    self.releaseSession()
                    self.status = "hoard disconnected"
                    self.trace("link cancelled: the Mac closed the session")
                default:
                    break
                }
            }
        }
        // The preview leg carries only fixture stills, which is exactly why
        // its death used to be invisible: control kept verbs and prices
        // flowing while every still vanished into a dead socket, and a corpus
        // session found out when the debug dir came up empty. A dead preview
        // is a dead session — released the same way, so the Mac's reconnect
        // gets a fresh pair of connections.
        session.preview.onState = { [weak self, weak preview = session.preview] state in
            Task { @MainActor in
                guard let self else { return }
                switch state {
                case .failed(let failure):
                    guard self.session?.preview === preview else { return }
                    self.releaseSession()
                    self.connected = false
                    self.status = failure.reason
                    self.trace("preview leg failed: \(failure.detail)")
                case .cancelled:
                    guard self.session?.preview === preview else { return }
                    self.releaseSession()
                    self.connected = false
                    self.status = "hoard disconnected"
                    self.trace("preview leg cancelled")
                default:
                    break
                }
            }
        }
        // Readiness is the phone's to declare, and the feature list is what the
        // parent negotiates against — so it says only what is actually true
        // here. No "framing" (that is Center Stage, a Continuity Camera
        // concept), no "effects" (the macOS Video Effects panel). "hud" is
        // claimed because the phone renders the price itself, which is what
        // that flag means to the Go side: do not fall back to a plain chime.
        announceReady()
    }

    /// announceReady tells the parent what this phone can do.
    ///
    /// Says only what is actually true: no "framing" (Center Stage is a
    /// Continuity Camera concept), no "effects" (the macOS Video Effects
    /// panel), and no "auto" until a video tap has actually attached.
    private func announceReady() {
        var features = ["torch", "hud", "border"]
        if autoAvailable { features += ["auto", "rearm"] }
        // The build stamp rides on ready so the session log proves which
        // build is running — build-scan-ios.sh overrides
        // CURRENT_PROJECT_VERSION with a per-build timestamp, and a phone
        // never reinstalled says so right here instead of via a tuning
        // session spent on stale code.
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion")
            as? String ?? "unstamped"
        send(Event(event: "ready", device: UIDevice.current.name,
                   appVersion: build, features: features))
    }

    // MARK: - Inbound

    private func handle(_ frame: Frame) {
        guard frame.kind == .ndjson, let line = frame.text else { return }
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let verb = ScanCommand(line: trimmed) else {
            // Reported, matching the Mac end: RemoteController answers an
            // unparseable stdin line with an error event, and this end used
            // to drop the same condition on the floor — so where the mangled
            // verb died depended on which hop it died at, and "parsed and
            // ignored" was indistinguishable from "worked" at the terminal.
            send(Event(event: "error", message: "Unknown command: \(trimmed)"))
            trace("unknown command: \(trimmed)")
            return
        }
        switch verb {
        case .capture:
            onCapture?()
        case .auto(let on):
            onAuto?(on)
        case .result(let payload):
            showResult(payload)
            // The card is recognized — the operator's next act is a
            // placement, so the trigger re-arms now and the box goes yellow
            // with the chime, instead of green lingering until the card is
            // physically dragged away. Safe early: the scene gate refuses to
            // re-fire on the unmoved card.
            onResult?()
        case .chime:
            // The fallback voice for a parent that does not know about tiers.
            sounds.play(voice: TierSettings.shared.bulkVoice)
        case .tune(let stable, let interval):
            onTune?(stable, interval)
            trace("trigger tuned stable=\(stable) interval=\(interval)")
        case .stills(let on):
            // Fixture capture. The Mac asks only when it has somewhere to put
            // them, so there is no toggle on this screen — the phone stays pure
            // capture and the parent owns the decision.
            sendStills = on
            trace("stills \(on ? "on" : "off")")
        case .rearm:
            onRearm?()
        case .evBias(let ev):
            onEVBias?(ev)
            trace("evbias \(ev) applied for the next capture")
        case .torch(let on):
            onTorch?(on)
            // Mirrored back so the terminal's indicator reflects the hardware
            // rather than what it asked for.
            send(Event(event: "torch", state: on ? "on" : "off"))
        case .quit:
            stop()
        default:
            // Rotation, framing, effects — either meaningless here or the
            // phone's own business. Ignored rather than errored: a newer hoard
            // talking to an older app is a supported pairing.
            break
        }
    }

    /// showResult renders one resolved card: the tier's sound and the number.
    ///
    /// A total with no tier updates nothing visible and stays silent — that is
    /// the Go side's running counter, which lives in the terminal here rather
    /// than on the phone, so a card is never a two-sound event.
    private func showResult(_ payload: String) {
        guard let data = payload.data(using: .utf8),
              let cmd = try? JSONDecoder().decode(HUDCommand.self, from: data)
        else { return }
        // The second-copy offer rides the result verb with no tier and no
        // sound — the suppression it reports was deliberately silent, and a
        // tone here would turn every correct suppression into a stop.
        if let note = cmd.note, cmd.promote == true {
            dupOffer = note
            let stamp = Date()
            dupOfferAt = stamp
            // The offer hides itself. Nothing else is guaranteed to: on the
            // last card of a pile no next result ever arrives, and the banner
            // sat on screen indefinitely (live, 2026-08-08). Ten seconds is
            // long enough to read and act; the answering window itself stays
            // 30s, so a slow tap after the banner faded still lands.
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(10))
                guard let self, self.dupOfferAt == stamp else { return }
                self.dupOffer = nil
            }
            return
        }
        guard cmd.tier != nil else { return }
        // A real result supersedes the offer: a new card landed, so "was
        // that a second copy" no longer refers to what the operator sees.
        dupOffer = nil
        // The phone's own tier lines, not the Mac's. The wire carries the
        // amount alongside its three-tier verdict, and the Settings tab owns
        // the thresholds here — so a priced card is re-tiered locally and
        // review/unpriced pass through untouched.
        let tier = TierSettings.shared.tier(wire: cmd.tier, amount: cmd.amount)
        // The number that actually describes the experience: shutter to sound.
        // Everything else is a component of it, and components can all look
        // fine while the total feels slow.
        if let sent = captureSentAt {
            // net is the wire and the Mac's resolve — the whole round trip
            // minus what the phone already accounted for. Reported because the
            // two halves are tuned by different people in different files, and
            // a loop that grows says nothing about which half grew. Measured at
            // 8ms median over a 61-capture session, so a net that starts
            // showing tens of milliseconds is the network, not the parser.
            let loop = Int(Date().timeIntervalSince(sent) * 1000)
            trace("timing loop=\(loop)ms net=\(max(0, loop - captureLocalMS))ms tier=\(tier ?? "none")")
            captureSentAt = nil
        }
        if let voice = TierSettings.shared.voice(forTier: tier) {
            sounds.play(voice: voice)
        }
        price = PriceResult(amount: cmd.amount, name: cmd.name, tier: tier,
                            finish: cmd.finish)
        priceSequence += 1
    }

    // MARK: - Outbound

    /// sendStill ships one capture's pixels for the fixture set.
    ///
    /// Called after the read, never before: this is a multi-megabyte frame and
    /// the whole loop is budgeted at 700ms, so it must not sit in front of the
    /// scan event the operator is waiting on. It goes over the preview
    /// connection rather than the control one for the same reason — that link
    /// already exists to carry big droppable payloads, and a still queued ahead
    /// of a `result` would delay the sound.
    func sendStill(_ image: CGImage) {
        guard sendStills, let session else { return }
        // Bounded, because `send` is not: control frames must all arrive, so
        // it queues without limit, and a still is 4-8 MB — on a link slower
        // than the capture rate an unbounded queue buffers the session's
        // whole output in memory while latency grows without anyone saying
        // so. Two in flight rides out one slow send; beyond that this
        // capture's still is dropped and the log records the hole in the
        // fixture set. Claimed here, on the main actor, so two captures
        // cannot race past the same check; released by the wire completion.
        let claimed = stillsInFlight.update { n -> Bool in
            guard n < Self.maxStillsInFlight else { return false }
            n += 1
            return true
        }
        guard claimed else {
            SessionLog.write(
                "still dropped: \(Self.maxStillsInFlight) already queued on a slow link")
            return
        }
        let counter = stillsInFlight
        DispatchQueue.global(qos: .utility).async {
            guard let data = jpeg(image, quality: 0.95) else {
                counter.update { $0 -= 1 }
                return
            }
            session.preview.send(Frame(kind: .still, payload: data)) {
                counter.update { $0 -= 1 }
            }
        }
    }

    /// Stills claimed but not yet off the wire. Cross-thread — claimed on the
    /// main actor, released by the network completion — hence Locked, the
    /// capture head's pattern.
    private let stillsInFlight = Locked(0)
    private static let maxStillsInFlight = 2

    /// promote answers the second-copy offer — the parent's `+` key, tapped.
    /// The 30s window mirrors the terminal's; past it the parent answers the
    /// stale promote gracefully, so a race costs a status line, not a row.
    func promote() {
        guard dupOffer != nil, Date().timeIntervalSince(dupOfferAt) < 30 else {
            dupOffer = nil
            return
        }
        dupOffer = nil
        send(Event(event: "promote"))
        trace("promote sent: operator confirmed a second copy")
    }

    func send(_ event: Event) {
        guard let session, let frame = Frame.json(event) else { return }
        session.control.send(frame)
    }

    /// When the last scan left the phone, so the full card-to-price loop can be
    /// timed rather than only the half that happens here.
    /// Whether to ship each capture's full-resolution still back for fixtures.
    @Published private(set) var sendStills = false

    private var captureSentAt: Date?
    /// What the phone itself spent, so the round trip can be split into the
    /// part this device controls and the part the network and the Mac do.
    private var captureLocalMS = 0

    func markCaptureSent(at when: Date, localMS: Int) {
        captureSentAt = when
        captureLocalMS = localMS
    }

    /// sendScan reports one capture's read.
    func sendScan(
        _ reading: CardReading, rotation: Int, auto: Bool, fireReason: String? = nil,
        holdDelta: Double? = nil, faceDelta: Double? = nil
    ) {
        // A new read is going out, so whatever the standing offer referred to
        // is no longer what the camera sees; the read's own outcome (a fresh
        // offer included) supersedes it.
        dupOffer = nil
        send(reading.scanEvent(
            rotation: rotation, auto: auto ? true : nil, fireReason: fireReason,
            holdDelta: holdDelta, faceDelta: faceDelta))
    }

    /// sendAuto reports the trigger's phase, so the terminal's status line says
    /// the same things it does on the Continuity path.
    func sendAuto(state: String) {
        send(Event(event: "auto", state: state))
    }

    /// trace forwards a diagnostic line to the Mac's stderr, so HOARD_SCAN_LOG
    /// telemetry stays whole across the hop. Without it the phone's timing and
    /// trigger lines simply vanish, and those are the tuning loop.
    func trace(_ message: String) {
        // Both copies. The Mac's is the one that belongs in HOARD_SCAN_LOG
        // beside the rest of a session; the phone's is the one that survives
        // the Mac not having been started with logging on.
        SessionLog.write(message)
        guard let session else { return }
        session.control.send(Frame(kind: .trace, payload: Data(message.utf8)))
    }

}

/// jpeg encodes a capture for the wire.
///
/// ImageIO rather than UIImage.jpegData, because the frame is a CGImage and
/// wrapping it in a UIImage only to unwrap it costs a copy of a 12-megapixel
/// buffer. Quality is high but not lossless: these are fixtures a border reader
/// is fitted on, and compression that softens an edge would be fitting the
/// codec rather than the card.
func jpeg(_ image: CGImage, quality: Double) -> Data? {
    let out = NSMutableData()
    guard let dest = CGImageDestinationCreateWithData(
        out, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
    CGImageDestinationAddImage(dest, image, [
        kCGImageDestinationLossyCompressionQuality: quality,
    ] as CFDictionary)
    guard CGImageDestinationFinalize(dest) else { return nil }
    return out as Data
}

/// A Bool that the pairing window is read through, safe to touch from the TLS
/// verify block's queue.
///
/// The capture head's `Flag` pattern, and the same reason: `pairingOpen` lives
/// on the main actor and Network.framework asks this question from its own
/// queue mid-handshake. Hopping to the main actor to answer would deadlock a
/// handshake behind whatever the UI is doing.
final class PairingGate: @unchecked Sendable {
    private let lock = NSLock()
    private var open = false

    func set(_ value: Bool) {
        lock.lock()
        open = value
        lock.unlock()
    }

    var isOpen: Bool {
        lock.lock()
        defer { lock.unlock() }
        return open
    }
}
