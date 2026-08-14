import CardKit
import Foundation
import ScanLink
import ScanWire
import ImageIO
import SwiftUI
import UniformTypeIdentifiers

@MainActor
final class LinkController: ObservableObject {
    @Published private(set) var code: PairingCode
    @Published private(set) var connected = false
    @Published private(set) var status = "Waiting for hoard…"
    @Published private(set) var price = PriceResult()
    @Published private(set) var priceSequence = 0
    @Published private(set) var dupOffer: String?
    private var dupOfferAt = Date.distantPast
    static let dupOfferWindow: TimeInterval = 30

    var onCapture: (() -> Void)?
    var onAuto: ((Bool) -> Void)?
    var onRearm: (() -> Void)?
    var onResult: (() -> Void)?
    var onEVBias: ((Double) -> Void)?
    var onTune: ((Int, Double) -> Void)?
    var onTorch: ((Bool) -> Void)?

    private var autoAvailable = false

    private var listener: PeerListener
    private let deviceName: String
    private var session: PeerSession?
    let sounds = Sounds()

    private let identityService = "dev.spiffcs.hoard.scan.ios.identity"
    private let pins = PinnedPeers(service: "dev.spiffcs.hoard.scan.ios.pins")

    @Published private(set) var pairingOpen: Bool {
        didSet { openGate.set(pairingOpen) }
    }

    private let openGate = PairingGate()

    init(deviceName: String = "") {
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

    private static func makeListener(
        name: String, code: PairingCode, identityService: String,
        pins: PinnedPeers, gate: PairingGate
    ) -> PeerListener {
        guard let identity = try? loadOrCreatePeerIdentity(service: identityService) else {
            return PeerListener(name: name, code: code)
        }
        return PeerListener(
            name: name, code: code,
            trust: PeerTrust(
                identity: identity, pinned: { pins.all }, acceptUnknown: { gate.isOpen }),
            pins: pins)
    }

    var encrypted: Bool { (try? loadOrCreatePeerIdentity(service: identityService)) != nil }

    var pairedCount: Int { pins.all.count }

    private func wireListener() {
        listener.onSession = { [weak self] session in
            Task { @MainActor in self?.adopt(session) }
        }
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
        listener.onAdvertisement = { [weak self] state in
            Task { @MainActor in self?.advertisementChanged(state) }
        }
        listener.onPaired = { [weak self] _ in
            Task { @MainActor in self?.pairingOpen = false }
        }
    }

    private func markVerified() {
        guard session == nil else { return }
        SessionLog.startSession()
        connected = true
        status = "Connected"
        trace("peer verified")
    }

    private func releaseSession() {
        let dead = session
        session = nil
        connected = false
        dead?.cancel()
    }

    private func markLost() {
        guard session == nil else { return }
        connected = false
        status = "hoard started connecting but did not finish. Try again"
        trace("peer lost before the session was assembled")
    }

    func setAutoAvailable(_ available: Bool) {
        let changed = autoAvailable != available
        autoAvailable = available
        if changed, connected { announceReady() }
    }

    private func advertisementChanged(_ state: PeerListener.Advertisement) {
        switch state {
        case .up:
            SessionLog.write("advertising")
            if !connected { status = "Waiting for hoard…" }
        case .down(let reason):
            SessionLog.write("advertisement down, not recovered: \(reason)")
            status = "Off the network. Check Wi-Fi, then switch away and back"
        }
    }

    func start() {
        do {
            try listener.start()
            if !connected { status = "Waiting for hoard…" }
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

    func endSession() {
        trace("session ended: the Mac said quit; still advertising")
        releaseSession()
        status = "hoard disconnected"
    }

    func newCode() {
        rotate(open: true)
    }

    func forgetMacs() {
        pins.forgetAll()
        rotate(open: true)
    }

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
        self.session = session
        connected = true
        status = "Connected"
        trace("session adopted")
        session.control.onFrame = { [weak self] frame in
            Task { @MainActor in self?.handle(frame) }
        }
        session.control.onState = { [weak self, weak control = session.control] state in
            Task { @MainActor in
                guard let self else { return }
                switch state {
                case .failed(let failure):
                    self.connected = false
                    self.status = failure.reason
                    self.trace("link failed: \(failure.detail)")
                    if self.session?.control === control { self.releaseSession() }
                case .cancelled:
                    guard self.session?.control === control else { return }
                    self.releaseSession()
                    self.status = "hoard disconnected"
                    self.trace("link cancelled: the Mac closed the session")
                default:
                    break
                }
            }
        }
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
        announceReady()
    }

    private func announceReady() {
        var features = ["torch", "hud", "border"]
        if autoAvailable { features += ["auto", "rearm"] }
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion")
            as? String ?? "unstamped"
        send(Event(event: "ready", device: UIDevice.current.name,
                   appVersion: build, features: features))
    }

    private func handle(_ frame: Frame) {
        guard frame.kind == .ndjson, let line = frame.text else { return }
        let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let verb = ScanCommand(line: trimmed) else {
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
            onResult?()
        case .chime:
            if let voice = TierSettings.shared.bulkVoice {
                sounds.play(voice: voice)
            }
        case .tune(let stable, let interval):
            onTune?(stable, interval)
            trace("trigger tuned stable=\(stable) interval=\(interval)")
        case .stills(let on):
            sendStills = on
            trace("stills \(on ? "on" : "off")")
        case .rearm:
            onRearm?()
        case .evBias(let ev):
            onEVBias?(ev)
            trace("evbias \(ev) applied for the next capture")
        case .torch(let on):
            onTorch?(on)
            send(Event(event: "torch", state: on ? "on" : "off"))
        case .quit:
            endSession()
        default:
            break
        }
    }

    private func showResult(_ payload: String) {
        guard let data = payload.data(using: .utf8),
              let cmd = try? JSONDecoder().decode(HUDCommand.self, from: data)
        else { return }
        if let note = cmd.note, cmd.promote == true {
            dupOffer = note
            let stamp = Date()
            dupOfferAt = stamp
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(Self.dupOfferWindow))
                guard let self, self.dupOfferAt == stamp else { return }
                self.dupOffer = nil
            }
            return
        }
        guard cmd.tier != nil else { return }
        let tier = TierSettings.shared.tier(wire: cmd.tier, amount: cmd.amount)
        if let sent = captureSentAt {
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

    func sendStill(_ image: CGImage) {
        guard sendStills, let session else { return }
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

    private let stillsInFlight = Locked(0)
    private static let maxStillsInFlight = 2

    func promote() {
        guard dupOffer != nil,
              Date().timeIntervalSince(dupOfferAt) < Self.dupOfferWindow
        else {
            dupOffer = nil
            return
        }
        dupOffer = nil
        send(Event(event: "promote"))
        trace("promote sent: operator confirmed a second copy")
    }

    func dismissDupOffer() {
        guard dupOffer != nil else { return }
        dupOffer = nil
        trace("promote dismissed: operator confirmed the suppression")
    }

    func send(_ event: Event) {
        guard let session, let frame = Frame.json(event) else { return }
        session.control.send(frame)
    }

    @Published private(set) var sendStills = false

    private var captureSentAt: Date?
    private var captureLocalMS = 0

    func markCaptureSent(at when: Date, localMS: Int) {
        captureSentAt = when
        captureLocalMS = localMS
    }

    func sendScan(
        _ reading: CardReading, rotation: Int, auto: Bool, fireReason: String? = nil,
        holdDelta: Double? = nil, faceDelta: Double? = nil
    ) {
        send(reading.scanEvent(
            rotation: rotation, auto: auto ? true : nil, fireReason: fireReason,
            holdDelta: holdDelta, faceDelta: faceDelta))
    }

    func sendAuto(state: String) {
        send(Event(event: "auto", state: state))
    }

    func trace(_ message: String) {
        SessionLog.write(message)
        guard let session else { return }
        session.control.send(Frame(kind: .trace, payload: Data(message.utf8)))
    }

}

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
