// The camera, with the controls Continuity Camera never had.
//
// Everything in this file exists because the macOS helper cannot do it. The
// capability ledger in docs/scanner-tuning.md records what a Continuity Camera
// admits to: 1920x1440 stills, no zoom, no exposure control, no white balance
// lock, no torch, and — measured, not assumed — no focus modes at all, which
// makes App/FocusPolicy.swift inert on that hardware. Every one of those is
// available here.
//
// The scanning rig is the thing to keep in mind: a phone on a stand, pointed
// down at a fixed spot on a desk, and a person putting one card after another
// in that spot. Nothing about the scene changes except which card it is. That
// is an unusually good case for a camera — focus once and freeze it, meter once
// and freeze that too — and an unusually bad one for the automatic behaviour
// that assumes it is filming a person moving around a room.
//
// So the policy is: settle everything, then lock everything, and let the
// operator override any of it. Auto is the enemy of repeatability.

import AVFoundation
import CardKit
import Foundation
import UIKit

@MainActor
final class CameraSession: NSObject, ObservableObject {
    let session = AVCaptureSession()
    private(set) var device: AVCaptureDevice?

    /// What the session is doing, in words fit to show a person.
    @Published var status = "Starting…"
    @Published var lensPosition: Float = 0.5
    @Published var torchLevel: Float = 0
    @Published var locked = false
    @Published var lastCapture: CapturedFrame?
    @Published var busy = false

    /// The lens, fixed.
    ///
    /// Not a choice any more. The ultra-wide was offered for its macro range
    /// and rejected on image quality; the virtual devices are worse still,
    /// because zooming a .builtInTripleCamera past its switch-over point hands
    /// you the telephoto, whose minimum focus distance is *longer* — so the
    /// card goes soft at exactly the moment you zoom in to read it.
    private let lensType = AVCaptureDevice.DeviceType.builtInWideAngleCamera

    /// The angle that levels the preview against gravity.
    ///
    /// The macOS helper carries a whole saga about this — a manual ←/→
    /// correction persisted to scan.json — because Continuity Camera often
    /// cannot tell how the phone is being held and reports 0°. A phone knows
    /// its own orientation, so here the coordinator is simply right and there is
    /// nothing for the operator to correct.
    @Published var previewRotation: CGFloat = 90

    private var rotationCoordinator: AVCaptureDevice.RotationCoordinator?
    private var rotationObservation: NSKeyValueObservation?

    /// The hands-free trigger. Owned here because it needs the session and the
    /// device, and nothing else does.
    private let triggerRunner = TriggerRunner()
    /// Raised when the trigger asks for a capture.
    var onFire: (@MainActor () -> Void)? {
        get { triggerRunner.onFire }
        set { triggerRunner.onFire = newValue }
    }
    var onPhase: (@MainActor (TriggerPhase) -> Void)? {
        get { triggerRunner.onPhase }
        set { triggerRunner.onPhase = newValue }
    }
    /// The box to draw brackets on, raised per sample.
    ///
    /// Passed straight through rather than stored as an `@Published` here. This
    /// object is a `@StateObject` on the session screen, so publishing a rect
    /// that changes 30 times a second would invalidate the whole view that
    /// often — the camera preview's representable, both of the price's
    /// `fixedSize` texts, the header and the footer — to move twelve line
    /// segments. The cue owns its own observable leaf instead.
    var onCue: (@MainActor (CGRect?) -> Void)? {
        get { triggerRunner.onBox }
        set { triggerRunner.onBox = newValue }
    }

    var onTriggerTrace: (@MainActor (String) -> Void)? {
        get { triggerRunner.onTrace }
        set { triggerRunner.onTrace = newValue }
    }
    /// Whether a video tap could be attached. When false, hands-free is
    /// unavailable and the ready event should not advertise it.
    private(set) var autoAvailable = false

    /// Whether the trigger is armed. Published so the screen can offer a
    /// toggle: the parent arms it on connect, but a phone that can be switched
    /// to hands-free by hand is far easier to test and to recover.
    @Published var autoOn = false
    var autoUnavailableReason: String { triggerRunner.unavailableReason }

    func toggleAuto() {
        autoOn ? stopTrigger() : startTrigger()
    }

    /// The tap's real buffer size, for the telemetry line.
    var tapSize: CGSize { triggerRunner.bufferSize }

    /// captureFromVideo lifts the next tap frame as a still, already upright.
    ///
    /// The rotation is folded into the same CIImage pass as the conversion,
    /// which removes an entire 12-megapixel rasterization from the shutter.
    /// Before this the buffer became a CGImage here and the read path
    /// immediately turned it back into a CIImage to rotate it and rasterized it
    /// again — two full passes over 4032x3024 before anything had looked at a
    /// single pixel, on a loop budgeted at 700ms.
    ///
    /// Handing back `.up` rather than `.right` is what makes that safe: the
    /// pixels are already turned, so `uprighted` becomes the no-op its own
    /// guard provides, and nothing downstream applies the rotation twice. It
    /// also means a fixture written by `sendStill` is upright on disk, which
    /// the corpus tooling previously had to guess at.
    private func captureFromVideo() async -> CapturedFrame? {
        await withCheckedContinuation { (c: CheckedContinuation<CapturedFrame?, Never>) in
            triggerRunner.grabFrame { buffer in
                let ci = CIImage(cvPixelBuffer: buffer).oriented(.right)
                guard let cg = sharedContext.createCGImage(ci, from: ci.extent) else {
                    c.resume(returning: nil)
                    return
                }
                c.resume(returning: CapturedFrame(image: cg, orientation: .up))
            }
        }
    }

    func startTrigger() {
        guard autoAvailable else { return }
        autoOn = true
        triggerRunner.start()
    }
    func stopTrigger() {
        autoOn = false
        triggerRunner.stop()
    }
    func nudgeTrigger() { triggerRunner.nudge() }
    /// Why the trigger armed for the capture now in flight, for the wire.
    var fireCause: String? {
        let c = triggerRunner.lastFireCause
        return c == .none ? nil : c.rawValue
    }
    /// Apply a tuning sent by the parent. Takes effect on the next sample, so a
    /// session can be retuned without reconnecting.
    func tuneTrigger(stable: Int, interval: Double) {
        triggerRunner.tune(stable: stable, interval: interval)
    }
    func triggerCaptureBegan() { triggerRunner.captureBegan() }
    func triggerCaptureFinished() { triggerRunner.captureFinished() }

    // MARK: - Lifecycle

    func start() async {
        guard await AVCaptureDevice.requestAccess(for: .video) else {
            status = "Camera access denied. Grant it in Settings › hoard scan"
            return
        }
        configure()
    }

    /// configure builds the session around one principle: the app owns the
    /// format, not the preset.
    ///
    /// `sessionPreset = .photo` looks like the way to ask for full-resolution
    /// stills and is not — measured on macOS, it actively *downgraded* the
    /// device from its own 1920x1440 default to 1920x1080, because it picks a
    /// 16:9 format and that is a vertical crop of a 4:3 sensor. Setting
    /// activeFormat directly moves the session to .inputPriority, which is
    /// AVFoundation's way of saying the app has taken over. That is what we
    /// want: the preset has demonstrably worse taste than the sensor.
    private func configure() {
        session.beginConfiguration()
        session.sessionPreset = .inputPriority

        guard let device = AVCaptureDevice.default(
            lensType, for: .video, position: .back) else {
            session.commitConfiguration()
            status = "No wide camera on this device"
            return
        }
        self.device = device

        session.inputs.forEach { session.removeInput($0) }
        guard let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else {
            session.commitConfiguration()
            status = "Could not open the camera"
            return
        }
        session.addInput(input)

        // The largest still the device offers, chosen explicitly. maxPhoto is
        // per-format, so this has to happen before the output is told anything.
        if let best = bestPhotoFormat(device) {
            do {
                try device.lockForConfiguration()
                device.activeFormat = best
                device.unlockForConfiguration()
            } catch {
                // The framework's wording goes to the log; the screen gets a
                // sentence about the camera. AVFoundation writes for a
                // developer reading a crash report.
                SessionLog.write("camera format failed: \(error.localizedDescription)")
                status = "Could not set up the camera"
            }
        }
        // The tap's dimensions go to the trace line rather than to a label.
        // They were on screen when this app had tabs and a capture view to
        // debug; the scanning screen's job is a price, and the number reaches
        // the log on every capture anyway.
        let tap = CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)
        SessionLog.write("tap \(tap.width)x\(tap.height)")

        // The video tap, for the trigger — and now for capture too, so this is
        // the session's only output rather than its second one.
        autoAvailable = triggerRunner.attach(to: session, device: device)

        session.commitConfiguration()

        trackRotation(of: device)
        Task.detached { [session] in session.startRunning() }
        status = "Settling…"
        Task { await settleAndLock() }
    }

    /// trackRotation keeps the preview level as the phone turns, and republishes
    /// the angle so the layout can re-shape itself to match. The preview
    /// connection is the only thing rotated: the still is left alone and its
    /// orientation metadata is applied to the pixels once, downstream, so the
    /// capture and what was framed can never disagree by a quarter turn.
    private func trackRotation(of device: AVCaptureDevice) {
        let coordinator = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: nil)
        rotationCoordinator = coordinator
        applyRotation()
        rotationObservation = coordinator.observe(
            \.videoRotationAngleForHorizonLevelPreview, options: [.new]
        ) { [weak self] _, _ in
            Task { @MainActor in self?.applyRotation() }
        }
    }

    private func applyRotation() {
        guard let coordinator = rotationCoordinator else { return }
        let angle = coordinator.videoRotationAngleForHorizonLevelPreview
        previewRotation = angle
        if let conn = previewConnection, conn.isVideoRotationAngleSupported(angle) {
            conn.videoRotationAngle = angle
        }
    }

    /// Set by the preview view once its layer exists, so the session can level it.
    var previewConnection: AVCaptureConnection? {
        didSet { applyRotation() }
    }


    // MARK: - The locks

    /// settleAndLock lets the automatics find the scene once, then freezes all
    /// three of them.
    ///
    /// The order matters and the wait is not padding: focus, exposure and white
    /// balance each need a moment of continuous operation to converge, and
    /// locking mid-convergence pins whatever wrong value they were passing
    /// through. Once frozen, every card after this one is metered identically,
    /// which is what makes a border reader's thresholds mean the same thing
    /// twice.
    func settleAndLock() async {
        guard let device else { return }
        do {
            try device.lockForConfiguration()
            if device.isFocusModeSupported(.continuousAutoFocus) {
                device.focusMode = .continuousAutoFocus
            }
            if device.isExposureModeSupported(.continuousAutoExposure) {
                device.exposureMode = .continuousAutoExposure
            }
            if device.isWhiteBalanceModeSupported(.continuousAutoWhiteBalance) {
                device.whiteBalanceMode = .continuousAutoWhiteBalance
            }
            // Cards land in the middle of the frame; meter and focus for that
            // rather than for the desk around it.
            let centre = CGPoint(x: 0.5, y: 0.5)
            if device.isFocusPointOfInterestSupported { device.focusPointOfInterest = centre }
            if device.isExposurePointOfInterestSupported {
                device.exposurePointOfInterest = centre
            }
            // The card plane is close. Telling autofocus not to search past it
            // shortens every hunt it does make.
            if device.isAutoFocusRangeRestrictionSupported {
                device.autoFocusRangeRestriction = .near
            }
            device.unlockForConfiguration()
        } catch {
            SessionLog.write("camera configure failed: \(error.localizedDescription)")
            status = "Could not set up the camera"
            return
        }

        // Exposure and white balance lock now — they are about the room, and
        // the room is what it is whether or not a card is present.
        try? await Task.sleep(for: .milliseconds(1200))
        lockMetering()
        status = "Waiting for the first card"
    }

    /// Focus is *not* locked here, and that is the correction to an earlier
    /// version that did.
    ///
    /// Locking a moment after startup pins the lens to whatever was in frame
    /// then — which is an empty desk, at a distance no card will ever be at.
    /// Every capture afterwards was soft, nothing read a collector row, and
    /// every card went to review. The lens has no way back because nothing was
    /// watching for it.
    ///
    /// So: continuous autofocus until a read actually succeeds, then freeze
    /// where the card is; and thaw again after two consecutive reads that got
    /// nothing, which is the signature of a rig that has been moved. Exactly
    /// what the macOS FocusPolicy does, and the half of it that was missing.
    private var emptyReads = 0

    func focus(afterGoodRead good: Bool) {
        guard device != nil else { return }
        if good {
            emptyReads = 0
            guard !locked else { return }
            lockFocus()
            return
        }
        guard locked else { return }
        emptyReads += 1
        guard emptyReads >= 2 else { return }
        emptyReads = 0
        thawFocus()
    }

    private func lockFocus() {
        guard let device, device.isFocusModeSupported(.locked),
              (try? device.lockForConfiguration()) != nil else { return }
        lensPosition = device.lensPosition
        device.focusMode = .locked
        device.unlockForConfiguration()
        locked = true
        // The lens position was on screen as "locked at 0.243". It is the
        // right thing to log and the wrong thing to show: three decimal places
        // of a normalised focus value is not something anyone holding cards can
        // act on, and it reads like a debug overlay left switched on.
        SessionLog.write(String(format: "focus locked at %.3f", lensPosition))
        status = "Focused"
    }

    private func thawFocus() {
        guard let device, device.isFocusModeSupported(.continuousAutoFocus),
              (try? device.lockForConfiguration()) != nil else { return }
        device.focusMode = .continuousAutoFocus
        device.unlockForConfiguration()
        locked = false
        status = "Refocusing"
    }

    /// lockMetering freezes exposure and white balance, which is what makes a
    /// border reader's thresholds mean the same thing twice.
    private func lockMetering() {
        guard let device, (try? device.lockForConfiguration()) != nil else { return }
        defer { device.unlockForConfiguration() }
        if device.isExposureModeSupported(.locked) { device.exposureMode = .locked }
        if device.isWhiteBalanceModeSupported(.locked) { device.whiteBalanceMode = .locked }
    }


    /// unlock hands the automatics back, for reframing the rig.
    func unlock() {
        guard let device, (try? device.lockForConfiguration()) != nil else { return }
        defer { device.unlockForConfiguration() }
        if device.isFocusModeSupported(.continuousAutoFocus) {
            device.focusMode = .continuousAutoFocus
        }
        if device.isExposureModeSupported(.continuousAutoExposure) {
            device.exposureMode = .continuousAutoExposure
        }
        if device.isWhiteBalanceModeSupported(.continuousAutoWhiteBalance) {
            device.whiteBalanceMode = .continuousAutoWhiteBalance
        }
        locked = false
        status = "Auto"
    }

    /// refocus points the lens at a spot the operator tapped.
    ///
    /// The override for when the automatic policy is wrong: it locks on the
    /// first good read, and if that read happened at the wrong distance — a
    /// hand in frame, a card half in — every capture after it is soft with no
    /// obvious way to say so. A tap is the way to say so.
    ///
    /// `point` is normalized in device space, origin top-left, which is what
    /// the preview layer converts a tap into.
    func refocus(at point: CGPoint) {
        guard let device, (try? device.lockForConfiguration()) != nil else { return }
        defer { device.unlockForConfiguration() }
        if device.isFocusPointOfInterestSupported, device.isFocusModeSupported(.autoFocus) {
            device.focusPointOfInterest = point
            device.focusMode = .autoFocus
        }
        // Metering follows the lens: a tap means "this is the subject", and
        // exposing for somewhere else would be answering half the request.
        if device.isExposurePointOfInterestSupported,
           device.isExposureModeSupported(.autoExpose) {
            device.exposurePointOfInterest = point
            device.exposureMode = .autoExpose
        }
        locked = false
        emptyReads = 0
        status = "Refocusing…"
        // Re-lock once it has settled, so the tap ends in the same steady state
        // everything else depends on rather than leaving the lens hunting.
        Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(900))
            guard let self else { return }
            self.lockFocus()
            self.lockMetering()
        }
    }

    /// setLensPosition pins the lens to an exact distance. This is the control
    /// the macOS helper most wanted and could not have: there, locking meant
    /// freezing wherever autofocus happened to land, with no way to say where.
    func setLensPosition(_ p: Float) {
        guard let device, device.isFocusModeSupported(.locked),
              device.isLockingFocusWithCustomLensPositionSupported,
              (try? device.lockForConfiguration()) != nil else { return }
        device.setFocusModeLocked(lensPosition: p) { _ in }
        device.unlockForConfiguration()
        lensPosition = p
        SessionLog.write(String(format: "focus set to %.3f", p))
        status = "Focused"
    }


    /// setTorch lights the card. Worth being skeptical about rather than
    /// assuming it helps: an on-axis LED against a glossy foil is a specular
    /// glare generator, and glare across the title is already a documented
    /// failure mode. Hence a level rather than a switch.
    func setTorch(_ level: Float) {
        guard let device, device.hasTorch,
              (try? device.lockForConfiguration()) != nil else { return }
        defer { device.unlockForConfiguration() }
        if level <= 0.01 {
            device.torchMode = .off
        } else {
            try? device.setTorchModeOn(level: min(level, AVCaptureDevice.maxAvailableTorchLevel))
        }
        torchLevel = level
    }

    var hasTorch: Bool { device?.hasTorch ?? false }

    /// The shape of what the camera actually delivers, width over height, with
    /// the preview's own rotation applied.
    ///
    /// The preview must be given this and told to fit inside it. The default is
    /// to fill, which on a view of a different shape crops the frame to suit the
    /// view — so the operator frames a card against a picture the capture does
    /// not agree with. A card that looks like it fills a wide preview is in fact
    /// a small object in the middle of a tall frame, which is precisely how the
    /// first captures ended up using 16% of the sensor.
    var previewAspect: CGFloat {
        guard let device else { return 3.0 / 4.0 }
        let d = CMVideoFormatDescriptionGetDimensions(device.activeFormat.formatDescription)
        guard d.width > 0, d.height > 0 else { return 3.0 / 4.0 }
        let sideways = Int(previewRotation) % 180 == 0
        return sideways
            ? CGFloat(d.height) / CGFloat(d.width)
            : CGFloat(d.width) / CGFloat(d.height)
    }
    /// How close the rig can physically get before the lens gives up. This is
    /// the real ceiling on card resolution, now that zoom has been measured and
    /// found to be nearly a wash — filling the frame is a matter of distance,
    /// and this is the distance below which the wide lens cannot focus at all.
    /// The ultra-wide is the macro option when this runs out.
    var minFocusMM: Int { max(0, Int(device?.minimumFocusDistance ?? 0)) }

    // MARK: - Capture

    /// capture lifts the next frame straight off the video tap.
    ///
    /// There is no photo path any more, and no selector to choose one. The
    /// 24 MP AVCapturePhotoOutput route was measured against this one over a
    /// full session and lost on every axis that matters: the tap already
    /// delivers 4032x3024, the read never once came back empty across 61
    /// captures, and the shutter term was 149ms rather than the photo path's
    /// larger one. It also cost an undocumented workaround — disposing system
    /// sound 1108 — to suppress a shutter pop that broke the one-sound-per-card
    /// rule the audio design depends on.
    ///
    /// Nothing is recorded and nothing is stored; a frame is copied out of the
    /// stream the trigger is already watching.
    func capture() async -> CapturedFrame? {
        guard !busy else { return nil }
        busy = true
        defer { busy = false }
        let frame = await captureFromVideo()
        lastCapture = frame
        return frame
    }
}

/// One captured still, with the orientation still to be applied.
///
/// The orientation travels beside the pixels rather than being baked in here,
/// because baking it in twice is how the title ends up at the bottom of the
/// card — the read path calls `uprighted` exactly once, and this is what it
/// calls it with.
struct CapturedFrame {
    let image: CGImage
    let orientation: CGImagePropertyOrientation
}

// MARK: - Format choice

/// The largest still a format will produce.
func maxPhotoDimensions(_ format: AVCaptureDevice.Format) -> CMVideoDimensions? {
    format.supportedMaxPhotoDimensions
        .max { Int($0.width) * Int($0.height) < Int($1.width) * Int($1.height) }
}

/// bestPhotoFormat picks by still size, then prefers the format that can also
/// deliver a usable video stream — the trigger will want one later, and a format
/// chosen for stills alone can leave the preview at an awkward frame rate.
///
/// It still ranks on `supportedMaxPhotoDimensions` even though nothing takes a
/// photo any more, and that is deliberate rather than left over. The value is a
/// proxy for "this is the sensor's full-readout format", which is exactly the
/// format whose *video* tap is 4032x3024 — the one the measured session ran on.
/// Ranking on video dimensions directly is the obvious tidy-up and is not worth
/// making blind: it would change which format is chosen on some devices, and
/// the only way to know whether that is an improvement is a live session.
func bestPhotoFormat(_ device: AVCaptureDevice) -> AVCaptureDevice.Format? {
    func pixels(_ f: AVCaptureDevice.Format) -> Int {
        guard let m = maxPhotoDimensions(f) else { return 0 }
        return Int(m.width) * Int(m.height)
    }
    let best = device.formats.map(pixels).max() ?? 0
    guard best > 0 else { return device.formats.last }
    return device.formats
        .filter { pixels($0) == best }
        .max { a, b in
            let ra = a.videoSupportedFrameRateRanges.first?.maxFrameRate ?? 0
            let rb = b.videoSupportedFrameRateRanges.first?.maxFrameRate ?? 0
            return ra < rb
        }
}

/// One CIContext for the process. Constructing one spins up a Metal device, and
/// doing that per capture is measurable per-card cost.
private let sharedContext = CIContext()
