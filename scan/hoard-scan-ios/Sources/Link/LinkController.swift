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

    init(deviceName: String = "") {
        // Restored, not regenerated. A code that changes every launch makes
        // "paired" meaningless — the Mac remembers a code per phone, so a fresh
        // one silently invalidates that the next morning and the failure looks
        // like a network fault rather than an expired secret. Generated once,
        // then kept.
        let code = PairingStore.load() ?? {
            let fresh = PairingCode.random()
            PairingStore.save(fresh)
            return fresh
        }()
        self.code = code
        self.deviceName = deviceName
        listener = PeerListener(name: deviceName, code: code)
        wireListener()
    }

    private func wireListener() {
        listener.onSession = { [weak self] session in
            Task { @MainActor in self?.adopt(session) }
        }
        listener.onError = { [weak self] message in
            Task { @MainActor in self?.status = message }
        }
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

    func start() {
        do {
            try listener.start()
            status = "Waiting for hoard, code \(code.display)"
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

    /// newCode rotates the pairing code and restarts advertising.
    ///
    /// The way to revoke a Mac. Every existing pairing stops working, which is
    /// the point — and is why this is a deliberate action rather than something
    /// that happens on its own at launch.
    func newCode() {
        let fresh = PairingCode.random()
        PairingStore.save(fresh)
        code = fresh
        stop()
        // The listener holds its code from construction, so rotating means a
        // new one. Rebuilt rather than mutated: a listener whose key can change
        // under a live connection is a bug waiting for a busy session.
        listener = PeerListener(name: deviceName, code: fresh)
        wireListener()
        start()
    }

    private func adopt(_ session: PeerSession) {
        SessionLog.startSession()
        self.session = session
        connected = true
        status = "Connected"
        session.control.onFrame = { [weak self] frame in
            Task { @MainActor in self?.handle(frame) }
        }
        session.control.onState = { [weak self] state in
            Task { @MainActor in
                if case .failed(let failure) = state {
                    self?.connected = false
                    // The phone's own screen gets the plain reason too; the
                    // framework's wording goes to the session log, which is
                    // where a diagnosis starts.
                    self?.status = failure.reason
                    self?.trace("link failed: \(failure.detail)")
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
        send(Event(event: "ready", device: UIDevice.current.name, features: features))
    }

    // MARK: - Inbound

    private func handle(_ frame: Frame) {
        guard frame.kind == .ndjson, let line = frame.text else { return }
        guard let verb = ScanCommand(line: line.trimmingCharacters(in: .whitespacesAndNewlines))
        else { return }
        switch verb {
        case .capture:
            onCapture?()
        case .auto(let on):
            onAuto?(on)
        case .result(let payload):
            showResult(payload)
        case .chime:
            // The fallback voice for a parent that does not know about tiers.
            sounds.play(tier: "bulk")
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
        guard let tier = cmd.tier else { return }
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
            trace("timing loop=\(loop)ms net=\(max(0, loop - captureLocalMS))ms tier=\(tier)")
            captureSentAt = nil
        }
        sounds.play(tier: tier)
        price = PriceResult(amount: cmd.amount, tier: tier, finish: cmd.finish)
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
        DispatchQueue.global(qos: .utility).async {
            guard let data = jpeg(image, quality: 0.95) else { return }
            session.preview.send(Frame(kind: .still, payload: data))
        }
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
        _ reading: CardReading, rotation: Int, auto: Bool, fireReason: String? = nil
    ) {
        send(reading.scanEvent(
            rotation: rotation, auto: auto ? true : nil, fireReason: fireReason))
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

    /// preview offers a frame to the Mac's optional mirror window. Droppable by
    /// design — a stale preview frame is worse than a missing one — so this
    /// silently does nothing when the previous frame is still in flight.
    func preview(_ jpeg: Data) {
        session?.preview.sendDroppable(Frame(kind: .preview, payload: jpeg))
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
