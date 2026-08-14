import AVFoundation
import CardKit
import CoreMotion
import Foundation

final class TriggerRunner: NSObject, AVCaptureVideoDataOutputSampleBufferDelegate {
    var onFire: (@MainActor () -> Void)?
    var onPhase: (@MainActor (TriggerPhase) -> Void)?
    private var lastFaces: [SceneSignature] = []
    private var lastBoxes: [CGRect] = []
    var onBox: (@MainActor (CGRect?) -> Void)?
    private let fireCauseBox = Locked<Trigger.RearmCause>(.none)
    var lastFireCause: Trigger.RearmCause { fireCauseBox.value }
    private let fireHoldDeltaBox = Locked<Double?>(nil)
    private let fireFaceDeltaBox = Locked<Double?>(nil)
    var lastFireHoldDelta: Double? { fireHoldDeltaBox.value }
    var lastFireFaceDelta: Double? { fireFaceDeltaBox.value }
    var onTrace: (@MainActor (String) -> Void)?

    private let trigger = Trigger()
    private let output = AVCaptureVideoDataOutput()
    private let queue = DispatchQueue(label: "hoard-scan.trigger")
    private let motion = CMMotionManager()
    private weak var device: AVCaptureDevice?
    private let busy = Flag()
    private var lastSample = Date.distantPast
    private let armed = Flag()

    private let motionThreshold = 0.08

    private(set) var unavailableReason = ""

    private let bufferSizeBox = Locked(CGSize.zero)
    var bufferSize: CGSize { bufferSizeBox.value }

    private var frameRequest: ((CVPixelBuffer) -> Void)?

    func grabFrame(_ handler: @escaping (CVPixelBuffer) -> Void) {
        queue.async { self.frameRequest = handler }
    }

    func cancelGrab() {
        queue.async { self.frameRequest = nil }
    }

    @discardableResult
    func attach(to session: AVCaptureSession, device: AVCaptureDevice) -> Bool {
        self.device = device
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
        pendingArm.set()
    }

    func stop() {
        armed.clear()
        motion.stopDeviceMotionUpdates()
        queue.async {
            self.trigger.disarm()
            self.raise(box: nil)
            self.raise(phase: self.trigger.phase)
        }
    }

    private func raise(box: CGRect?) {
        DispatchQueue.main.async { MainActor.assumeIsolated { self.onBox?(box) } }
    }

    private func raise(phase: TriggerPhase) {
        DispatchQueue.main.async { MainActor.assumeIsolated { self.onPhase?(phase) } }
    }

    func captureBegan() { busy.set() }

    var wantsFrame: Bool { frameRequest != nil }

    func captureFinished() {
        queue.async {
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
            self.trigger.captureFinished(scene: self.lastScene, cardScene: cardFace, cardBox: cardBox)
            self.busy.clear()
            self.raise(phase: self.trigger.phase)
        }
    }

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

    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        if frameRequest != nil, let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) {
            let request = frameRequest
            frameRequest = nil
            bufferSizeBox.value = CGSize(width: CVPixelBufferGetWidth(buffer),
                                         height: CVPixelBufferGetHeight(buffer))
            request?(buffer)
            return
        }
        guard armed.isSet, !busy.isSet || wantsFrame else { return }
        let now = Date()
        guard now.timeIntervalSince(lastSample) >= trigger.tuning.interval else { return }
        lastSample = now

        guard let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        bufferSizeBox.value = CGSize(width: CVPixelBufferGetWidth(buffer),
                                     height: CVPixelBufferGetHeight(buffer))
        let scene = sceneSignature(buffer)
        lastScene = scene
        let boxes = triggerRects(buffer)
        let held = trigger.snapshot.capturedBox.map { sceneSignature(buffer, in: $0) }
        lastBoxes = boxes
        lastFaces = boxes.map { sceneSignature(buffer, in: $0) }
        let sample = TriggerSample(
            boxes: boxes, scene: scene, holdScene: held, boxScenes: lastFaces,
            focusSettled: !(device?.isAdjustingFocus ?? false),
            rigMoving: isMoving())

        if pendingArm.isSet {
            pendingArm.clear()
            trigger.arm(with: sample)
            let armed = trigger.snapshot
            Task { @MainActor in self.onTrace?("trigger armed \(armed.line)") }
            raise(phase: trigger.phase)
            return
        }

        let decision = trigger.observe(sample)
        let cue = trigger.snapshot.cue
        raise(box: cue)

        switch decision {
        case .fire:
            busy.set()
            let fired = trigger.snapshot
            let settleMS = Int(Double(fired.settleSamples) * trigger.tuning.interval * 1000)
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

    private func isMoving() -> Bool {
        guard let motion = motion.deviceMotion else { return false }
        let a = motion.userAcceleration
        let magnitude = (a.x * a.x + a.y * a.y + a.z * a.z).squareRoot()
        return magnitude > motionThreshold
    }
}

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

    func update<R>(_ body: (inout T) -> R) -> R {
        lock.lock()
        defer { lock.unlock() }
        return body(&v)
    }
}

private func overlapFraction(_ a: CGRect, _ b: CGRect) -> CGFloat {
    let i = a.intersection(b)
    guard !i.isNull else { return 0 }
    let smaller = min(a.width * a.height, b.width * b.height)
    guard smaller > 0 else { return 0 }
    return (i.width * i.height) / smaller
}
