// Feeding the trigger from the camera.
//
// The trigger itself is pure and lives in CardKit; this is the part that has to
// know about AVFoundation.
//
// **This file used to claim it pinned the tap to roughly 1080p. It does not,
// and never did.** A video data output delivers the active format's video
// dimensions, and the format is chosen for its stills — measured live, the tap
// delivers 4032x3024. So TriggerTuning's constants, every one of them fitted
// against 1080p buffers over nine macOS sessions, are being fed something four
// times larger.
//
// Left that way deliberately for now, because two things turned out to be true
// at once. It costs nothing measurable — the loop runs at 491 ms median against
// a 700 ms budget — and the silent capture path *needs* those pixels, because
// the still is lifted straight off this tap. `deliversPreviewSizedOutputBuffers`
// would restore the 1080p assumption and take the still down with it.
//
// What it may cost is accuracy rather than speed: 1.8 captures per commit
// against the ledger's 1.1. Whether the tap's size is a cause of that is
// unmeasured, and the honest state is that the comment used to assert something
// convenient and untrue.

import AVFoundation
import CardKit
import CoreMotion
import Foundation

/// TriggerRunner turns a video stream into fire decisions.
final class TriggerRunner: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate {
    /// Called on the main actor when the trigger wants a capture.
    var onFire: (@MainActor () -> Void)?
    /// Called on the main actor when the phase changes, for the wire.
    var onPhase: (@MainActor (TriggerPhase) -> Void)?
    /// The box to draw, raised on every sample — including nil, since the view
    /// needs to know when to stop drawing as much as when to start.
    ///
    /// No hold timer on the receiving end: the trigger already absorbs a
    /// detector blink. An empty sample burns grace, and only `graceSamples`
    /// consecutive bad ones abandon the pass. Blink rate is a per-sample
    /// property, so six samples is what transfers from the macOS rig, not its
    /// half-second wall clock.
    var onBox: (@MainActor (CGRect?) -> Void)?
    /// One diagnostic line, for the tuning log. Raised at the two moments that
    /// explain a session's waste: when the baseline is learned, and when the
    /// trigger decides to fire.
    var onTrace: (@MainActor (String) -> Void)?

    private let trigger = Trigger()
    private let output = AVCaptureVideoDataOutput()
    private let queue = DispatchQueue(label: "hoard-scan.trigger")
    private let motion = CMMotionManager()
    /// The capture device, kept so the trigger can be told whether the lens is
    /// currently hunting. `TriggerSample.focusSettled` exists for exactly that
    /// and was hardcoded true, which meant a blurred frame mid-refocus counted
    /// as evidence of stillness — the failure its own comment warns about.
    private weak var device: AVCaptureDevice?
    /// Set while a capture is in flight, so samples taken during the shutter and
    /// the read cannot be mistaken for evidence about what happens next.
    private let busy = Flag()
    private var lastSample = Date.distantPast
    private var armed = false

    /// How still the device has to be to count as parked, in g. Generous: a
    /// desk takes a knock when a card is put down, and treating that as "the
    /// rig moved" would freeze the trigger at exactly the wrong moment.
    private let motionThreshold = 0.08

    // MARK: - Session wiring

    /// Why hands-free is unavailable, when it is. Shown on the phone rather
    /// than inferred: "the footer says manual" is equally consistent with the
    /// tap failing, the parent never arming it, and the trigger being parked.
    private(set) var unavailableReason = ""

    /// The size of the buffers the tap is actually delivering.
    ///
    /// Measured rather than assumed. The file header claims these are
    /// preview-sized, and nothing in `attach` actually constrains them — a
    /// video data output delivers the active format's *video* dimensions, which
    /// on a format chosen for its 24 MP stills may be far larger than 1080p.
    /// If that is so, the trigger's constants are being fed something they were
    /// never fitted against, and a still could be taken from here instead of
    /// from the photo output.
    private(set) var bufferSize = CGSize.zero

    /// Set to have the next sample handed over as a still, bypassing the photo
    /// output entirely. The whole reason this exists: a frame taken from the
    /// video stream plays no shutter sound, because there is no shutter.
    private var frameRequest: ((CVPixelBuffer) -> Void)?

    /// grabFrame asks for the next video buffer as a still.
    func grabFrame(_ handler: @escaping (CVPixelBuffer) -> Void) {
        frameRequest = handler
    }

    /// attach adds the video tap to a running session.
    ///
    /// Best-effort: a session that refuses the output just means no hands-free
    /// mode, which the ready event's feature list reports honestly rather than
    /// advertising a trigger that will never fire.
    @discardableResult
    func attach(to session: AVCaptureSession, device: AVCaptureDevice) -> Bool {
        self.device = device
        // Already attached — a lens switch reconfigures the session and would
        // otherwise report hands-free as unavailable the second time round,
        // because canAddOutput is false for an output already on the session.
        if session.outputs.contains(output) { return true }
        guard session.canAddOutput(output) else {
            unavailableReason = "This camera format will not run a video tap"
            return false
        }
        output.alwaysDiscardsLateVideoFrames = true
        output.videoSettings = [
            kCVPixelBufferPixelFormatTypeKey as String:
                kCVPixelFormatType_420YpCbCr8BiPlanarFullRange,
        ]
        output.setSampleBufferDelegate(self, queue: queue)
        session.addOutput(output)
        // Preview-sized buffers rather than the full sensor. See the file
        // header: this is what keeps the trigger's constants meaningful.
        // Zero, deliberately, and now load-bearing beyond the read: the
        // preview layer's coordinate converters describe boxes against the
        // *unrotated* picture, so pinning the tap here is what lets the cue map
        // onto the preview with a y-flip and nothing else.
        output.connections.first?.videoRotationAngle = 0
        return true
    }

    func start() {
        guard !armed else { return }
        armed = true
        if motion.isDeviceMotionAvailable {
            motion.deviceMotionUpdateInterval = 0.05
            motion.startDeviceMotionUpdates()
        }
        // Armed on the next sample, not here: the baseline has to be learned
        // from a real frame, and learning it from nothing would mean every
        // piece of desk furniture reads as a new card the first time it is seen.
        pendingArm.set()
    }

    func stop() {
        armed = false
        motion.stopDeviceMotionUpdates()
        trigger.disarm()
    }

    /// captureBegan parks the trigger for the duration of a shutter.
    func captureBegan() { busy.set() }

    /// Samples are still delivered while busy when a frame has been requested —
    /// otherwise a video-mode capture would wait for a tap that is switched off
    /// for the duration of the capture it is trying to serve.
    var wantsFrame: Bool { frameRequest != nil }

    /// captureFinished parks the trigger on what was just shot.
    func captureFinished() {
        trigger.captureFinished(scene: lastScene)
        busy.clear()
        Task { @MainActor in self.onPhase?(self.trigger.phase) }
    }

    /// nudge is the parent's content-aware re-arm.
    /// tune sets the stillness knobs mid-session.
    ///
    /// Guarded because these two set the floor on every capture's wait, and a
    /// zero or negative value would either spin the sampler or freeze it.
    func tune(stable: Int, interval: Double) {
        queue.async {
            self.trigger.tuning.stableSamples = max(1, stable)
            self.trigger.tuning.interval = max(0.01, interval)
        }
    }

    func nudge() {
        trigger.forceRearm()
        Task { @MainActor in self.onPhase?(self.trigger.phase) }
    }

    private let pendingArm = Flag()
    private var lastScene: SceneSignature?

    // MARK: - Sampling

    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        guard armed, !busy.isSet || wantsFrame else { return }
        // Throttle to the trigger's own interval rather than a constant.
        //
        // This was hardcoded at 0.1s "regardless of the camera's frame rate",
        // which is a macOS-era assumption: there the trigger ran off a preview
        // stream and Vision was the bottleneck. Here the tap delivers 30fps and
        // two frames in three were being thrown away, so the interval could
        // never be tuned even though it sets the floor on the whole wait —
        // stabilising is 6 samples at 0.1s, which is 600ms of a ~1.1s
        // hold-still-to-sound, the largest single term in it.
        //
        // Vision running synchronously here still self-throttles: late frames
        // are discarded rather than queued behind a slow pass, so asking for a
        // shorter interval than the work takes costs nothing but is not free
        // evidence either.
        let now = Date()
        guard now.timeIntervalSince(lastSample) >= trigger.tuning.interval else { return }
        lastSample = now

        guard let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        bufferSize = CGSize(width: CVPixelBufferGetWidth(buffer),
                            height: CVPixelBufferGetHeight(buffer))
        // A pending still request is served before anything else: it is the
        // shutter, and everything below is about deciding when to press it.
        if let request = frameRequest {
            frameRequest = nil
            request(buffer)
            return
        }
        let scene = sceneSignature(buffer)
        lastScene = scene
        let boxes = triggerRects(buffer)
        let sample = TriggerSample(
            boxes: boxes, scene: scene,
            // A hunting lens produces blur, and blur is indistinguishable from
            // stillness to a rectangle detector. Reading the device rather than
            // assuming settled is what lets the streak be shortened safely: the
            // machine now freezes while focus moves instead of banking frames
            // it should not trust.
            focusSettled: !(device?.isAdjustingFocus ?? false),
            rigMoving: isMoving())

        if pendingArm.isSet {
            pendingArm.clear()
            trigger.arm(with: sample)
            // The baseline's size is the single most useful number here. Desk
            // furniture the trigger has *not* absorbed is what lets an empty
            // staging area look like a card.
            let armed = trigger.snapshot
            Task { @MainActor in self.onTrace?("trigger armed \(armed.line)") }
            Task { @MainActor in self.onPhase?(self.trigger.phase) }
            return
        }

        let decision = trigger.observe(sample)
        // Raised for every sample rather than only on a decision: the box moves
        // continuously while the phase does not, and the cue tracks the box.
        let cue = trigger.snapshot.cue
        // DispatchQueue rather than an unstructured Task: Tasks enqueued to an
        // actor carry no ordering guarantee, and at 30 samples a second a
        // reorder would show as the brackets stepping backwards a frame. The
        // main queue is FIFO.
        DispatchQueue.main.async { MainActor.assumeIsolated { self.onBox?(cue) } }

        switch decision {
        case .fire:
            busy.set()
            // Captured before the capture runs, so it describes the decision
            // rather than whatever the scene became afterwards.
            var fired = trigger.snapshot
            // Reported in milliseconds as well as samples, so a run at one
            // interval can be read against a run at another without arithmetic.
            let settleMS = Int(Double(fired.settleSamples) * trigger.tuning.interval * 1000)
            _ = settleMS
            Task { @MainActor in
                self.onTrace?("trigger fire \(fired.line) settleMS=\(settleMS)")
                self.onFire?()
            }
        case .phaseChanged(let phase):
            Task { @MainActor in self.onPhase?(phase) }
        case .nothing:
            break
        }
    }

    /// isMoving asks the IMU whether the rig is being handled.
    ///
    /// The macOS trigger had to infer this from two consecutive empty reads,
    /// which is both slow and ambiguous — a card that simply failed to detect
    /// looks the same. A phone knows, for free, every 50 ms.
    private func isMoving() -> Bool {
        guard let motion = motion.deviceMotion else { return false }
        let a = motion.userAcceleration
        let magnitude = (a.x * a.x + a.y * a.y + a.z * a.z).squareRoot()
        return magnitude > motionThreshold
    }
}

/// A boolean shared between the capture queue and the main actor.
///
/// Same reason `Flag` exists in the helper: the trigger runs on its own queue
/// and the session state it has to consult lives elsewhere, and hopping queues
/// to read one bit would serialise a ten-per-second sampler behind the UI.
final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false

    func set() {
        lock.lock()
        value = true
        lock.unlock()
    }

    func clear() {
        lock.lock()
        value = false
        lock.unlock()
    }

    var isSet: Bool {
        lock.lock()
        defer { lock.unlock() }
        return value
    }
}
