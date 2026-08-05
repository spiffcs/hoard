// The live session: camera, window, trigger wiring and the stdin verbs that
// drive them.

// macOS only. This is the camera, window and HUD half of ScanKit; the read
// pipeline under Core/ is what compiles for iOS. See Package.swift.
#if os(macOS)

import AVFoundation
import AppKit
import Foundation

// MARK: - Live capture (AppKit window + AVFoundation)

final class CaptureController: NSObject, AVCapturePhotoCaptureDelegate {
    /// The uniqueID of the camera to use; nil takes the highest-ranked available one.
    private let deviceID: String?
    /// Extra clockwise rotation, in degrees, applied on top of whatever the
    /// rotation coordinator reports. The coordinator returns 0° for some
    /// Continuity Camera setups — it can't always tell how the phone is being
    /// held — which leaves a portrait-held phone previewing sideways. The user
    /// corrects it once with ←/→ and the caller remembers the result.
    private var manualRotation: Int
    let session = AVCaptureSession()
    fileprivate let photoOutput = AVCapturePhotoOutput()
    private var window: NSWindow?
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var deviceName = "camera"
    private var rotationCoordinator: AVCaptureDevice.RotationCoordinator?
    private var rotationObservation: NSKeyValueObservation?
    /// The live capture device, kept so the torch can be toggled mid-session.
    private var device: AVCaptureDevice?
    /// Whether this device supports Center Stage (the system's auto-framing);
    /// gates the feature advertisement and the toggle.
    private var framingAvailable = false
    /// Whether this device carries a torch — the phone's flashlight, which
    /// Continuity Camera exposes and which is the only light macOS lets an
    /// app control (exposure bias is iOS-only).
    private var torchAvailable = false
    /// Whether the torch is currently on; session-scoped, never persisted —
    /// it drains the phone and AVFoundation kills it on session end anyway.
    private var torchOn = false
    /// The lens. See FocusPolicy for why a scanning session wants it frozen.
    private let focus = FocusPolicy()
    /// Whether auto-framing is currently on. Forced off at startup — Center
    /// Stage state persists system-wide (a FaceTime call leaves it on), and
    /// its "zoom to the subject" crop is exactly the sometimes-too-close
    /// startup framing that makes cards unscannable.
    private var autoFraming = false

    /// The price HUD and its sounds. The bank is lazy so the audio engine's
    /// first-time spin-up never delays camera readiness — it starts on the
    /// first resolved card instead.
    private let hud = PriceHUD()
    /// The auto-trigger's on-screen cue. It owns its own layer and its own
    /// blink-tolerance state; the controller only tells it what the trigger
    /// saw and which way up the preview is.
    private let outlines = OutlineOverlay()
    private lazy var sounds = SoundBank()

    // Auto-capture. The video output feeds the trigger only — stills always go
    // through photoOutput, so auto and manual captures are identical on the
    // wire apart from the auto tag.
    fileprivate let videoOutput = AVCaptureVideoDataOutput()
    private let analysisQueue = DispatchQueue(label: "hoard-scan.analysis")
    /// Confined to analysisQueue: captureOutput is its only reader and writer,
    /// and that queue is serial. Not shared state despite sitting here.
    fileprivate var lastAnalysis = Date.distantPast
    /// The one fact the video tap needs from the main thread. See ArmedGate.
    fileprivate let armed = ArmedGate()
    fileprivate let autoTrigger = AutoTrigger()
    /// Whether the session could attach a video tap; when false, auto mode is
    /// unavailable and the ready event doesn't advertise it.
    private var autoAvailable = false
    /// Whether the user asked for auto mode (--auto, auto-on, or the a key).
    private var autoRequested: Bool
    /// Set between an auto fire and its photo delegate, to tag the scan event.
    private var pendingAuto = false
    /// A monotonic counter so an auto session's debug images don't overwrite
    /// each other the way a single "capture-ocr.png" would.
    private var captureCount = 0
    /// When the last capture's processing ended. Video samples taken before
    /// this are stale — they queued up behind the OCR on the main thread and
    /// describe the shutter moment, not the present — and replaying them
    /// against HOLD faked a full disruption burst in a single millisecond
    /// (observed live: instant double-fires).
    private var lastCaptureFinishedAt = Date.distantPast

    init(deviceID: String?, rotation: Int, auto: Bool = false) {
        self.deviceID = deviceID
        self.manualRotation = ((rotation / 90) % 4 + 4) % 4 * 90
        self.autoRequested = auto
        super.init()
    }

    /// startDemo brings the window up with no camera at all — a black preview
    /// under a live HUD — so the price tiers' looks and sounds can be
    /// eyeballed by piping `result` lines on stdin. The capture session never
    /// runs; everything else (stdin, keys, shutdown) works as in live mode.
    func startDemo() {
        deviceName = "HUD demo"
        buildWindow()
        emit(Event(event: "ready", rotation: manualRotation, device: deviceName,
                   features: ["hud"]))
    }

    func start() {
        // Wait for the requested phone (or any phone) to publish itself rather
        // than giving up on a device that's a beat slow to appear.
        spinRunLoop(seconds: continuityWait) {
            guard let id = deviceID else { return hasContinuityCamera() }
            return availableCameras().contains { $0.uniqueID == id }
        }

        // An explicitly requested phone wins; a stale id (that phone walked away)
        // falls back to another paired one. There is deliberately no webcam
        // fallback — see availableCameras().
        let cameras = availableCameras()
        guard let device = deviceID.flatMap({ id in cameras.first { $0.uniqueID == id } })
            ?? cameras.first
        else {
            fail(noPhoneMessage)
        }
        guard let input = try? AVCaptureDeviceInput(device: device) else {
            fail("could not open \(device.localizedName)")
        }
        deviceName = device.localizedName
        // macOS exposes no camera zoom API (videoZoomFactor is iOS-only), but
        // it does expose Center Stage. Take app control and force it off so
        // every session starts on the full, uncropped frame — auto-framing's
        // subject-tracking crop is the "too close" startup the user can't
        // otherwise explain, because its state rides along from whatever app
        // used the camera last.
        framingAvailable = device.activeFormat.isCenterStageSupported
        AVCaptureDevice.centerStageControlMode = .app
        AVCaptureDevice.isCenterStageEnabled = false
        self.device = device
        // Continuity Camera does not bridge the phone's flashlight — hasTorch
        // is false there as of macOS today — so the torch feature usually
        // stays dark. The capability line makes that verifiable in a
        // HOARD_SCAN_LOG instead of a matter of memory.
        torchAvailable = device.hasTorch
        focus.start(on: device)
        let focusCaps = device.isFocusModeSupported(.continuousAutoFocus)
            ? "af" + (device.isFocusModeSupported(.locked) ? "+lock" : "") : "fixed"
        let caps = "scan: \(device.localizedName) [\(kindLabel(device))] torch=\(device.hasTorch) "
            + "centerStage=\(device.activeFormat.isCenterStageSupported) "
            + "focus=\(focusCaps) (policy \(TriggerTuning.focusControl))\n"
        FileHandle.standardError.write(Data(caps.utf8))
        guard session.canAddInput(input), session.canAddOutput(photoOutput) else {
            fail("could not configure capture session")
        }
        // Ask for full-resolution stills. The default preset is .high, which caps
        // the capture at video resolution and leaves the collector number under
        // 1% of the frame height — right at the edge of what Vision can resolve.
        // .photo does better, but not best: see raiseToBestFormat, which runs
        // once the session is live and undoes the preset's own crop.
        if session.canSetSessionPreset(.photo) {
            session.sessionPreset = .photo
        }
        session.addInput(input)
        session.addOutput(photoOutput)
        // Nothing is asked of photoOutput.maxPhotoDimensions here, and that is
        // deliberate. Reading the format for its largest still *before* the
        // session runs looks like the way to beat the preset's cap and
        // backfires: activeFormat is still the device's low-res default at this
        // point, so pinning the output to its maximum capped captures at
        // 640x480 — a third of the linear resolution the preset alone was
        // already giving. The dimensions are set in raiseToBestFormat instead,
        // after startRunning, where activeFormat means something.

        // The video tap is best-effort: a session that refuses it just means no
        // auto mode, which the ready event's feature list reports honestly.
        if session.canAddOutput(videoOutput) {
            videoOutput.alwaysDiscardsLateVideoFrames = true
            videoOutput.setSampleBufferDelegate(self, queue: analysisQueue)
            session.addOutput(videoOutput)
            autoAvailable = true
        }
        autoTrigger.onFire = { [weak self] in self?.autoFire() }
        autoTrigger.onPhase = { [weak self] phase in self?.autoPhaseChanged(phase) }
        autoTrigger.onBoxes = { [weak self] novel in
            guard let self else { return }
            outlines.show(novel, phase: autoTrigger.phase, rotation: effectiveRotation)
        }

        buildWindow()
        trackRotation(of: device)
        DispatchQueue.global(qos: .userInitiated).async { [session] in
            session.startRunning()
            // Re-apply once the session is live: before it starts, the preview
            // layer's connection may not exist yet, so the initial apply is a
            // silent no-op and the KVO observer only fires if the angle later
            // changes.
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                self.applyRotation()
                // Don't announce readiness until the photo output has an active
                // connection and the stream has had a moment to settle:
                // startRunning() returning isn't the same as being able to take a
                // picture, and a capture in that gap fails with an opaque
                // "operation could not be completed".
                spinRunLoop(seconds: 5) {
                    self.photoOutput.connection(with: .video)?.isActive == true
                }
                spinRunLoop(seconds: 0.75) { false }
                self.raiseToBestFormat()
                // Now that the session is live the active format is the one
                // that will actually be used, so this number means something.
                // It is the first thing to check when reads go soft.
                if let d = self.device {
                    let label = maxPhoto(d.activeFormat)
                        .map { "\($0.width)x\($0.height)" } ?? "unreported"
                    FileHandle.standardError.write(Data("scan: still=\(label)\n".utf8))
                }
                emit(Event(event: "ready", rotation: self.manualRotation,
                           device: self.deviceName,
                           features: (self.autoAvailable ? ["auto", "rearm"] : [])
                               + (self.framingAvailable ? ["framing"] : [])
                               + (self.torchAvailable ? ["torch"] : [])
                               + ["effects", "hud", "border"]))
                if self.autoRequested { self.setAuto(true) }
            }
        }
    }

    /// raiseToBestFormat puts the device on the format with the largest still it
    /// offers, and only then tells the photo output to use all of it.
    ///
    /// The ordering is the whole trick, and it is the opposite of the obvious
    /// one. `sessionPreset = .photo` does not pick the biggest format — it picks
    /// a 16:9 one, and on Continuity Camera that is a vertical crop of a 4:3
    /// sensor. Measured with `--probe`: the device wakes up on 1920x1440, the
    /// preset *drops* it to 1920x1080 at startRunning, and assigning
    /// activeFormat back afterwards is accepted. Doing the same thing before
    /// startRunning is the documented 640x480 trap, because activeFormat is
    /// still the low-res default to read from at that point.
    ///
    /// Setting activeFormat directly moves the session to .inputPriority, which
    /// is AVFoundation's way of saying the app owns the format now. That is what
    /// we want: the preset has already been shown to have worse taste than the
    /// device's own default.
    ///
    /// Refusals are logged and survived. A camera that will not budge keeps
    /// whatever the preset chose, which is exactly today's behaviour.
    private func raiseToBestFormat() {
        guard let device, let best = bestPhotoFormat(device) else { return }
        let current = maxPhoto(device.activeFormat).map { Int($0.width) * Int($0.height) } ?? 0
        guard let target = maxPhoto(best), Int(target.width) * Int(target.height) > current else {
            // Already on the best format the device offers — the preset and the
            // sensor happen to agree. Still worth pinning the output below.
            if let m = maxPhoto(device.activeFormat) { photoOutput.maxPhotoDimensions = m }
            return
        }
        do {
            try device.lockForConfiguration()
            device.activeFormat = best
            device.unlockForConfiguration()
        } catch {
            autoDebug("format raise refused: \(error.localizedDescription)")
            return
        }
        // Re-read rather than trusting the assignment: the point of this whole
        // method is that what the device reports and what it was asked for are
        // not the same thing.
        if let m = maxPhoto(device.activeFormat) {
            photoOutput.maxPhotoDimensions = m
        }
    }

    /// trackRotation keeps the preview upright as the phone is turned. A camera
    /// delivers frames in its own fixed orientation, so without this a
    /// portrait-held iPhone previews rotated 90°. The coordinator reports the
    /// angle that levels the horizon, and is KVO-observable so turning the phone
    /// mid-session re-levels the preview live.
    private func trackRotation(of device: AVCaptureDevice) {
        let coordinator = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: previewLayer)
        rotationCoordinator = coordinator
        applyRotation()
        rotationObservation = coordinator.observe(
            \.videoRotationAngleForHorizonLevelPreview, options: [.new]
        ) { [weak self] _, _ in
            DispatchQueue.main.async { self?.applyRotation() }
        }
    }

    /// autoPreviewAngle is what the coordinator thinks levels the preview; 0 when
    /// it can't tell how the phone is held.
    private var autoPreviewAngle: CGFloat {
        rotationCoordinator?.videoRotationAngleForHorizonLevelPreview ?? 0
    }

    /// effectiveRotation is the total turn the user is actually looking at: what
    /// the coordinator contributes to the preview plus their manual correction.
    /// The captured pixels get this same total, so OCR reads exactly the framing
    /// that was confirmed on screen.
    private var effectiveRotation: Int {
        (Int(autoPreviewAngle) + manualRotation) % 360
    }

    /// applyRotation pushes the effective angle onto the preview connection and
    /// refreshes the title.
    ///
    /// The still is deliberately left unrotated. The coordinator's *capture*
    /// angle can differ from its *preview* angle — here by a full 180° — so
    /// letting it rotate the photo turns the capture by a different amount than
    /// the preview showed, and OCR reads an upside-down card no matter what the
    /// user picks. Instead the whole turn is applied to the pixels afterwards
    /// from effectiveRotation, which keeps the two paths identical by
    /// construction.
    private func applyRotation() {
        if let conn = previewLayer?.connection {
            let angle = CGFloat(effectiveRotation)
            if conn.isVideoRotationAngleSupported(angle) { conn.videoRotationAngle = angle }
        }
        if let conn = photoOutput.connection(with: .video), conn.isVideoRotationAngleSupported(0) {
            conn.videoRotationAngle = 0
        }
        // The analysis buffers stay unrotated too: the trigger's rectangle
        // filter is orientation-free, and the outline drawing converts from
        // sensor space — a rotated buffer would put the cue a quarter-turn off.
        if let conn = videoOutput.connection(with: .video), conn.isVideoRotationAngleSupported(0) {
            conn.videoRotationAngle = 0
        }
        updateTitle()
    }

    /// rotate turns the preview a quarter-turn and remembers the choice. The
    /// parent is told so it can persist the correction without waiting for the
    /// window to close.
    private func rotate(clockwise: Bool) {
        manualRotation = (manualRotation + (clockwise ? 90 : 270)) % 360
        applyRotation()
        emit(Event(event: "rotation", rotation: manualRotation))
    }

    /// setAutoFraming toggles Center Stage — the system's subject-tracking
    /// zoom, and the only framing macOS lets an app adjust (the real zoom
    /// APIs are iOS-only). Off means the full, uncropped frame, which is what
    /// card scanning wants; the toggle exists for the desk setups where the
    /// tracked crop happens to frame the pile better. The parent is told so
    /// it can reflect the state without watching the window.
    fileprivate func setAutoFraming(_ on: Bool) {
        guard framingAvailable else {
            emit(Event(event: "error", message: "Auto-framing is not adjustable on this camera"))
            return
        }
        autoFraming = on
        AVCaptureDevice.isCenterStageEnabled = on
        updateTitle()
        emit(Event(event: "framing", state: on ? "auto" : "off"))
    }

    /// setTorch turns the phone's flashlight on or off to light the card —
    /// the one brightness control macOS offers (exposure bias is iOS-only).
    /// A refused lock or a thermally-limited torch reports an error and
    /// leaves the session up; the parent is told the state that actually
    /// took, so its mirror never drifts from the hardware.
    fileprivate func setTorch(_ on: Bool) {
        guard torchAvailable, let device else {
            emit(Event(event: "error", message: "No torch on this camera"))
            return
        }
        do {
            try device.lockForConfiguration()
            defer { device.unlockForConfiguration() }
            if on {
                try device.setTorchModeOn(level: AVCaptureDevice.maxAvailableTorchLevel)
            } else {
                device.torchMode = .off
            }
            torchOn = on
        } catch {
            emit(Event(event: "error",
                       message: "could not switch the torch: \(error.localizedDescription)"))
        }
        updateTitle()
        emit(Event(event: "torch", state: torchOn ? "on" : "off"))
    }

    /// updateTitle surfaces the current rotation — including what the automatic
    /// angle contributed — so a wrong orientation is diagnosable at a glance.
    private func updateTitle() {
        let total = Int(autoPreviewAngle) + manualRotation
        let mode = autoTrigger.phase == .off ? "" : "AUTO · "
        let framing = autoFraming ? " · FRAMED" : ""
        let torch = torchOn ? " · TORCH" : ""
        window?.title = "hoard · \(deviceName) · \(mode)\(total % 360)° "
            + "(auto \(Int(autoPreviewAngle))°)\(framing)\(torch) · Space capture · A auto · "
            + "←/→ rotate · Z framing · T torch · V effects · Esc cancel"
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 560),
            styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        win.title = "hoard · \(deviceName) · Space to capture · Esc to cancel"
        win.center()

        let view = PreviewView(frame: win.contentLayoutRect)
        view.autoresizingMask = [.width, .height]
        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspect
        view.previewLayer = preview
        view.wantsLayer = true
        view.layer = preview
        self.previewLayer = preview
        // Before the HUD attaches, so the price flash sits above the brackets.
        outlines.attach(to: preview, bounds: view.bounds)
        view.outlines = outlines
        hud.attach(to: preview, scale: win.backingScaleFactor)
        view.hud = hud
        view.onKey = { [weak self] key in
            switch key {
            case .space: self?.capture()
            case .escape: self?.shutdown()
            case .rotateLeft: self?.rotate(clockwise: false)
            case .rotateRight: self?.rotate(clockwise: true)
            case .framingToggle: self?.setAutoFraming(self?.autoFraming == false)
            case .torchToggle: self?.setTorch(self?.torchOn == false)
            case .effectsPanel: AVCaptureDevice.showSystemUserInterface(.videoEffects)
            case .autoToggle: self?.setAuto(self?.autoTrigger.phase == .off)
            }
        }
        win.contentView = view
        win.makeKeyAndOrderFront(nil)
        win.makeFirstResponder(view)
        NSApp.activate(ignoringOtherApps: true)
        self.window = win
    }

    /// When the last shutter was requested, for the capture timing line.
    private var captureRequestedAt: Date?

    private func capture() {
        // Re-level right before the shutter in case the phone moved since the
        // last KVO notification.
        applyRotation()
        // Any shutter — auto or manual — parks the trigger, so pressing space
        // in auto mode can't be followed by an auto fire on the same card.
        autoTrigger.captureBegan()
        captureRequestedAt = Date()
        photoOutput.capturePhoto(with: AVCapturePhotoSettings(), delegate: self)
    }

    /// autoFire is the trigger's shutter: identical to a space press except the
    /// resulting scan event is tagged auto. Silent by design — the parent
    /// chimes when the scan resolves (added or queued), and that is the one
    /// sound per card; a shutter pop on top made every card a two-beep event.
    private func autoFire() {
        pendingAuto = true
        capture()
    }

    /// setAuto turns the trigger on or off, keeping the window chrome honest.
    fileprivate func setAuto(_ on: Bool) {
        guard autoAvailable else {
            if on { emit(Event(event: "error", message: "Auto capture unavailable on this session")) }
            return
        }
        autoRequested = on
        autoTrigger.setEnabled(on)
        updateTitle()
    }

    /// autoPhaseChanged relays trigger transitions to the wire and the preview
    /// overlay. Only settled phases go on the wire — searching↔stabilizing
    /// flapping is visual, not protocol — and consecutive repeats are deduped.
    private var lastWireState = ""
    private func autoPhaseChanged(_ phase: AutoTrigger.Phase) {
        // Every transition passes through here, so this is the one place the
        // tap's copy can be kept honest.
        armed.set(phase != .off)
        outlines.redraw(phase: autoTrigger.phase, rotation: effectiveRotation)
        let wire: String
        switch phase {
        case .searching, .stabilizing: wire = "armed"
        case .capturing: wire = "capturing"
        case .hold: wire = "held"
        case .off: wire = "off"
        }
        guard wire != lastWireState else { return }
        lastWireState = wire
        emit(Event(event: "auto", rotation: manualRotation, state: wire))
    }

    /// shutdown closes the window and ends the process. The rotation rides along
    /// so a correction made just before closing isn't thrown away.
    func shutdown() {
        focus.restore()
        emit(Event(event: "closed", rotation: manualRotation))
        session.stopRunning()
        exit(0)
    }

    /// handle runs one command from the parent. These mirror the in-window keys,
    /// so the user can drive the camera from the terminal without switching
    /// windows — which is the point of keeping the session open.
    func handle(command: String) {
        guard let verb = ScanCommand(line: command) else {
            emit(Event(event: "error", message: "Unknown command: \(command)"))
            return
        }
        switch verb {
        case .capture: capture()
        case .rotate(let clockwise): rotate(clockwise: clockwise)
        case .framing(let on): setAutoFraming(on)
        case .torch(let on): setTorch(on)
        case .effects: AVCaptureDevice.showSystemUserInterface(.videoEffects)
        case .auto(let on): setAuto(on)
        case .tune(let stable, let interval):
            // The local helper already takes these from the environment at
            // startup, so arriving as a command is a no-op rather than a second
            // way to say the same thing. Named explicitly so the switch stays
            // exhaustive and a future build cannot silently drop it.
            autoDebug("tune ignored on the local path: stable=\(stable) interval=\(interval)")
        case .stills:
            // A no-op on this path, and deliberately not an error. The local
            // helper already writes every capture to HOARD_SCAN_DEBUG_DIR
            // itself (saveDebugImage) because the pixels never leave this
            // process; the verb exists for the phone, which has to be asked.
            break
        case .rearm: autoTrigger.forceRearm()
        case .chime: NSSound(named: "Glass")?.play()
        case .result(let payload): showResult(payload: payload)
        case .quit: shutdown()
        }
    }

    /// showResult renders one resolved card's price on the HUD: the tier sound
    /// and flash, and/or the running-total update. A malformed payload reports
    /// an error and keeps the session alive, like any bad command.
    private func showResult(payload: String) {
        guard let data = payload.data(using: .utf8), !payload.isEmpty,
              let cmd = try? JSONDecoder().decode(HUDCommand.self, from: data) else {
            emit(Event(event: "error", message: "Bad result payload"))
            return
        }
        if let tier = cmd.tier { sounds.play(tier: tier) }
        hud.show(cmd)
    }

    /// A failed capture reports an error but keeps the window open — one bad
    /// frame shouldn't tear down a session the user is mid-way through.
    func photoOutput(_ output: AVCapturePhotoOutput,
                     didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
        let wasAuto = pendingAuto
        pendingAuto = false
        if let error {
            // Park without absorbing: a transient capture failure says nothing
            // about whether the rectangle is a card.
            autoTrigger.captureFinished()
            lastCaptureFinishedAt = Date()
            // Same split the link uses: the terminal gets a sentence about the
            // card, the framework's wording goes to stderr with the rest of the
            // session's diagnostics. AVFoundation's own text here is written
            // for a developer reading a crash report, not for someone holding
            // a stack of cards.
            FileHandle.standardError.write(
                Data("scan: capture failed: \(error.localizedDescription)\n".utf8))
            emit(Event(event: "error", message: "The camera did not return a photo. Try that card again"))
            return
        }
        guard let (cg, orientation) = decodePhoto(photo) else {
            autoTrigger.captureFinished()
            lastCaptureFinishedAt = Date()
            emit(Event(event: "error", message: "No image from capture"))
            return
        }
        // Normalize the capture's own orientation first, then match the framing
        // the user corrected in the preview. Exactly one rotation each.
        captureCount += 1
        saveDebugImage(cg, "capture-\(captureCount)-raw.png")
        let tRotate = Date()
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: effectiveRotation)
        let rotateMs = msSince(tRotate)
        saveDebugImage(forOCR, "capture-\(captureCount)-ocr.png")
        let scan = scanFrame(forOCR)
        // A capture that read something proves the current lens distance is
        // right — freeze it there; one that read nothing counts toward the
        // thaw. Decided per capture, whatever fired it.
        focus.update(afterGoodRead: Event.readAnything(scan))
        // shutter+decode is everything before the pixel work: AVFoundation's
        // shutter latency, the photo decode, and the raw debug write.
        let preMs = captureRequestedAt.map { Int(tRotate.timeIntervalSince($0) * 1000) } ?? 0
        timing("capture \(captureCount) shutter+decode=\(preMs)ms rotate=\(rotateMs)ms "
            + "total=\(captureRequestedAt.map { msSince($0) } ?? 0)ms")
        autoTrigger.captureFinished()
        lastCaptureFinishedAt = Date()
        // Emit and stay live: the window persists so the next card can be framed
        // and captured without relaunching the camera.
        emit(Event.scan(scan, rotation: manualRotation, auto: wasAuto ? true : nil))
    }
}

// MARK: - Video tap (auto-trigger sampling)

extension CaptureController: AVCaptureVideoDataOutputSampleBufferDelegate {
    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        // Runs on analysisQueue. The time gate throttles to TriggerTuning.interval no
        // matter what frame rate the camera delivers, and Vision running
        // synchronously here self-throttles: late frames are discarded, never
        // queued behind a slow pass.
        guard armed.isArmed else { return }
        let now = Date()
        guard now.timeIntervalSince(lastAnalysis) >= TriggerTuning.interval else { return }
        lastAnalysis = now
        guard let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        let boxes = triggerRects(buffer)
        let scene = sceneSignature(buffer)
        let sampledAt = Date()
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            // Samples taken before the last capture finished are stale: they
            // queued behind the OCR and describe the shutter moment. Feeding
            // them to HOLD faked instant disruption bursts.
            guard sampledAt > self.lastCaptureFinishedAt else { return }
            // The trigger decides which of these are candidates (vs desk
            // furniture) and drives the outline through onBoxes.
            self.autoTrigger.observe(boxes, scene: scene, focusSettled: !self.focus.isHunting)
        }
    }
}

#endif
