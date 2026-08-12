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
    /// The picture inside each box on the most recent sample, so the capture
    /// can hand the shot card's own signature back to the trigger.
    private var lastFaces: [SceneSignature] = []
    /// The boxes those signatures came from, so the shot card's own face can be
    /// found again when the capture completes.
    private var lastBoxes: [CGRect] = []
    /// The box to draw, raised on every sample — including nil, since the view
    /// needs to know when to stop drawing as much as when to start.
    ///
    /// No hold timer on the receiving end: the trigger already absorbs a
    /// detector blink. An empty sample burns grace, and only `graceSamples`
    /// consecutive bad ones abandon the pass. Blink rate is a per-sample
    /// property, so six samples is what transfers from the macOS rig, not its
    /// half-second wall clock.
    var onBox: (@MainActor (CGRect?) -> Void)?
    /// Why the trigger armed for the capture now in flight. Written on the
    /// capture queue at fire time, read on the main actor for the wire —
    /// hence the lock, like every other value that crosses that line here.
    private let fireCauseBox = Locked<Trigger.RearmCause>(.none)
    var lastFireCause: Trigger.RearmCause { fireCauseBox.value }
    /// The two measurements behind `lastFireCause`, as they stood on the
    /// sample that fired.
    ///
    /// Latched beside the cause and from the same frozen snapshot, because the
    /// scene keeps moving while the capture runs: read live off the trigger at
    /// send time they would describe the shutter's aftermath rather than the
    /// decision, which is the mistake the `let fired = trigger.snapshot` below
    /// already exists to avoid.
    private let fireHoldDeltaBox = Locked<Double?>(nil)
    private let fireFaceDeltaBox = Locked<Double?>(nil)
    var lastFireHoldDelta: Double? { fireHoldDeltaBox.value }
    var lastFireFaceDelta: Double? { fireFaceDeltaBox.value }
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
    /// Whether the trigger is running: set on the main actor by start/stop,
    /// consulted on the capture queue by every sample — a Flag, not a Bool,
    /// for exactly that reason.
    private let armed = Flag()

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
    private let bufferSizeBox = Locked(CGSize.zero)
    var bufferSize: CGSize { bufferSizeBox.value }

    /// Set to have the next sample handed over as a still, bypassing the photo
    /// output entirely. The whole reason this exists: a frame taken from the
    /// video stream plays no shutter sound, because there is no shutter.
    ///
    /// Confined to the trigger queue: it used to be written on the main actor
    /// and read here, a torn closure read away from a crash, so grabFrame and
    /// cancelGrab hop instead of touching it directly.
    private var frameRequest: ((CVPixelBuffer) -> Void)?

    /// grabFrame asks for the next video buffer as a still.
    func grabFrame(_ handler: @escaping (CVPixelBuffer) -> Void) {
        queue.async { self.frameRequest = handler }
    }

    /// cancelGrab withdraws a pending still request whose waiter gave up —
    /// the capture timeout's half of the bargain that no continuation is
    /// resumed twice and none waits forever.
    func cancelGrab() {
        queue.async { self.frameRequest = nil }
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
        guard !armed.isSet else { return }
        armed.set()
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
        armed.clear()
        motion.stopDeviceMotionUpdates()
        // The trigger's own state is queue-confined; every touch of it from
        // the main actor hops, the way tune() always did.
        queue.async {
            self.trigger.disarm()
            // Say so. `disarm` clears the trigger's own cue and phase, but the
            // view draws from the last values *raised*, and clearing `armed`
            // above means `captureOutput` returns before it can raise
            // anything ever again — so without this the brackets the operator
            // was last shown stay on screen for the rest of the session, and
            // the footer keeps claiming the trigger is waiting for a card.
            //
            // Raised from inside the hop rather than beside it, and through
            // the same main queue the sample path uses. Both queues are FIFO:
            // a sample already in flight is enqueued ahead of this block, so
            // its box is enqueued ahead of this nil, and the clear lands last
            // rather than being overwritten by the frame that beat it.
            self.raise(box: nil)
            self.raise(phase: self.trigger.phase)
        }
    }

    /// Hands a box or a phase to the main actor in the order it was decided.
    ///
    /// `DispatchQueue.main`, never an unstructured `Task` — Tasks enqueued to
    /// an actor carry no ordering guarantee, so a stale value could land after
    /// a fresh one. That mattered visibly for the box at 30 samples a second
    /// (the brackets would step backwards a frame); it matters for the phase
    /// because `stop` has to be the last word, and a `.stabilizing` from the
    /// sample before it must not arrive afterwards.
    private func raise(box: CGRect?) {
        DispatchQueue.main.async { MainActor.assumeIsolated { self.onBox?(box) } }
    }

    private func raise(phase: TriggerPhase) {
        DispatchQueue.main.async { MainActor.assumeIsolated { self.onPhase?(phase) } }
    }

    /// captureBegan parks the trigger for the duration of a shutter.
    func captureBegan() { busy.set() }

    /// Samples are still delivered while busy when a frame has been requested —
    /// otherwise a video-mode capture would wait for a tap that is switched off
    /// for the duration of the capture it is trying to serve.
    var wantsFrame: Bool { frameRequest != nil }

    /// captureFinished parks the trigger on what was just shot.
    ///
    /// The whole body runs on the trigger queue: it reads the sampler's own
    /// state (lastBoxes, lastFaces, lastScene) and mutates the unlocked
    /// Trigger, both of which are queue-confined. It used to run on the main
    /// actor, reading Swift arrays the queue was mutating at 30Hz — a COW
    /// refcount race, i.e. heap corruption on a long enough session.
    func captureFinished() {
        queue.async {
            // The shot card's own picture goes with it, so `hold` can tell a
            // card laid on the same spot from this one settling back. The card
            // being watched is the one that fired, so its signature is the one
            // whose box still overlaps.
            let watched = self.trigger.snapshot.cue
            let cardFace = watched.flatMap { box -> SceneSignature? in
                guard let i = self.lastBoxes.indices.first(where: {
                    overlapFraction(self.lastBoxes[$0], box) > 0.5
                }), i < self.lastFaces.count else { return nil }
                return self.lastFaces[i]
            }
            let cardBox = watched.flatMap { box -> CGRect? in
                self.lastBoxes.first(where: { overlapFraction($0, box) > 0.5 })
            }
            // The window travels with the picture. Every later comparison
            // samples through this exact rect, which is what keeps a
            // motionless card reading as motionless.
            self.trigger.captureFinished(scene: self.lastScene, cardScene: cardFace, cardBox: cardBox)
            self.busy.clear()
            self.raise(phase: self.trigger.phase)
        }
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

    func rearmForResult() {
        queue.async {
            self.trigger.forceRearm(cause: .none)
            self.raise(phase: self.trigger.phase)
        }
    }

    func nudge() {
        queue.async {
            self.trigger.forceRearm()
            self.raise(phase: self.trigger.phase)
        }
    }

    private let pendingArm = Flag()
    private var lastScene: SceneSignature?

    // MARK: - Sampling

    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        // A pending still request is served before every trigger gate: it is
        // the manual shutter, and it must fire with hands-free off. It used
        // to sit below the `armed` guard, so toggling auto off and pressing
        // capture parked the whole session on a frame this method was never
        // going to hand over.
        if frameRequest != nil, let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) {
            let request = frameRequest
            frameRequest = nil
            bufferSizeBox.value = CGSize(width: CVPixelBufferGetWidth(buffer),
                                         height: CVPixelBufferGetHeight(buffer))
            request?(buffer)
            return
        }
        guard armed.isSet, !busy.isSet || wantsFrame else { return }
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
        bufferSizeBox.value = CGSize(width: CVPixelBufferGetWidth(buffer),
                                     height: CVPixelBufferGetHeight(buffer))
        let scene = sceneSignature(buffer)
        lastScene = scene
        let boxes = triggerRects(buffer)
        // One signature per box, sampled inside it. Costs the same 384 point
        // samples each as the frame-wide grid, and it is what lets the trigger
        // tell a card laid over another from the same card sitting still.
        // One signature, sampled through the window the shutter fired on and
        // held fixed since — never through this frame's own box. A per-box
        // signature tracks the detector's jitter, and `sceneSignature` point
        // samples one pixel per cell, so a card that has not moved reads as a
        // different card. See `TriggerSample.holdScene` for the measurement.
        //
        // Cheaper too: one grid instead of one per candidate box.
        let held = trigger.snapshot.capturedBox.map { sceneSignature(buffer, in: $0) }
        lastBoxes = boxes
        // The per-box faces, for two jobs: the shutter records the card it
        // fired on, and HOLD asks whether anything in frame still looks like
        // that card — which is how a card that slid is told from one laid over
        // it. Still never compared frame to frame, where the detector's jitter
        // would make a motionless card look new; only ever against the capture.
        lastFaces = boxes.map { sceneSignature(buffer, in: $0) }
        let sample = TriggerSample(
            boxes: boxes, scene: scene, holdScene: held, boxScenes: lastFaces,
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
            raise(phase: trigger.phase)
            return
        }

        let decision = trigger.observe(sample)
        // Raised for every sample rather than only on a decision: the box moves
        // continuously while the phase does not, and the cue tracks the box.
        let cue = trigger.snapshot.cue
        raise(box: cue)

        switch decision {
        case .fire:
            busy.set()
            // Captured before the capture runs, so it describes the decision
            // rather than whatever the scene became afterwards.
            let fired = trigger.snapshot
            // Reported in milliseconds as well as samples, so a run at one
            // interval can be read against a run at another without arithmetic.
            let settleMS = Int(Double(fired.settleSamples) * trigger.tuning.interval * 1000)
            // Why the machine armed for this fire — a placement the trigger
            // watched happen, or a timer that expired on the parent. The parent
            // has been inferring this from a clock; now it is told.
            let cause = trigger.rearmCause
            fireCauseBox.value = cause
            fireHoldDeltaBox.value = fired.holdDelta
            fireFaceDeltaBox.value = fired.faceDelta
            Task { @MainActor in
                self.onTrace?(
                    "trigger fire \(fired.line) settleMS=\(settleMS) cause=\(cause.rawValue)")
                self.onFire?()
            }
        case .phaseChanged(let phase):
            raise(phase: phase)
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

    /// testAndSet sets the flag and reports whether it was already set — the
    /// one-shot guard where a timed-out capture and a late frame race to be
    /// the single resumption of one continuation.
    func testAndSet() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let was = value
        value = true
        return was
    }

    var isSet: Bool {
        lock.lock()
        defer { lock.unlock() }
        return value
    }
}

/// A value shared between the capture queue and the main actor, behind a
/// lock — Flag's shape for payloads that are not a Bool (the tap's buffer
/// size, the fire diagnostics). Reads and writes are individually atomic;
/// counters that must check-and-increment as one transaction go through
/// `update`, because a get-then-set from two threads is how an in-flight
/// count drifts and sticks.
final class Locked<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var v: T
    init(_ v: T) { self.v = v }
    var value: T {
        get {
            lock.lock()
            defer { lock.unlock() }
            return v
        }
        set {
            lock.lock()
            v = newValue
            lock.unlock()
        }
    }

    /// update runs one compound read-modify-write under the lock.
    func update<R>(_ body: (inout T) -> R) -> R {
        lock.lock()
        defer { lock.unlock() }
        return body(&v)
    }
}

/// How much of the smaller rect the two share, for matching a box to the one
/// the trigger settled on. Not IoU: the trigger's watched rect is frozen at the
/// geometry the card was first seen with, so a fresher box of the same card can
/// be offset from it and still plainly be the same thing.
private func overlapFraction(_ a: CGRect, _ b: CGRect) -> CGFloat {
    let i = a.intersection(b)
    guard !i.isNull else { return 0 }
    let smaller = min(a.width * a.height, b.width * b.height)
    guard smaller > 0 else { return 0 }
    return (i.width * i.height) / smaller
}
