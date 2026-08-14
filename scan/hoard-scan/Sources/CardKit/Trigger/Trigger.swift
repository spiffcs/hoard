import CoreGraphics
import Foundation

public enum TriggerPhase: String, Sendable, Equatable {
    case off
    case searching
    case stabilizing
    case capturing
    case hold
}

public struct TriggerTuning: Sendable {
    public var interval = 0.033
    public var stableSamples = 6
    public var graceSamples = 6
    public var iou = 0.65
    public var backgroundIoU = 0.5
    public var backgroundResetPasses = 8
    public var rearmSamples = 3
    public var stillDelta = 2.5
    public var cardChanged = 12.0
    public var movedFaceMax = 20.0
    public var sceneChanged = 6.0
    public var sceneDetail = 12.0

    public init() {}
}

public struct TriggerSample: Sendable {
    public var boxes: [CGRect]
    public var scene: SceneSignature
    public var holdScene: SceneSignature?
    public var boxScenes: [SceneSignature]
    public var focusSettled: Bool
    public var rigMoving: Bool

    public init(boxes: [CGRect], scene: SceneSignature,
                holdScene: SceneSignature? = nil,
                boxScenes: [SceneSignature] = [],
                focusSettled: Bool = true, rigMoving: Bool = false) {
        self.boxes = boxes
        self.scene = scene
        self.holdScene = holdScene
        self.boxScenes = boxScenes
        self.focusSettled = focusSettled
        self.rigMoving = rigMoving
    }
}

public final class Trigger {
    public private(set) var phase: TriggerPhase = .off
    public var tuning = TriggerTuning()

    private var watched: CGRect?
    private(set) var cue: CGRect?
    private var stableCount = 0
    private var graceCount = 0
    private var blinkCount = 0
    private var realDetections = 0
    private var baseline: [CGRect] = []
    private var abandonedPasses = 0
    private var disruptCount = 0
    private var prevScene: SceneSignature?
    private var capturedScene: SceneSignature?

    public private(set) var rearmCause: RearmCause = .none

    public enum RearmCause: String, Sendable {
        case none
        case removed
        case replaced
        case moved
        case nudged
    }

    private var capturedCardScene: SceneSignature?
    private var capturedCardBox: CGRect?
    private var pendingCause: RearmCause = .removed

    private var lastBoxes = 0
    private var samplesInPhase = 0

    private var settleAtFire = 0

    private var lastHoldDelta: Double?
    private var lastFaceDelta: Double?
    private var samplesSinceCapture = 0

    public init() {}

    public var snapshot: Snapshot {
        Snapshot(phase: phase, baseline: baseline.count, boxes: lastBoxes,
                 cue: cue,
                 capturedBox: capturedCardBox,
                 settleSamples: phase == .capturing ? settleAtFire : samplesInPhase,
                 stable: stableCount, grace: graceCount, blink: blinkCount,
                 real: realDetections, abandoned: abandonedPasses,
                 disrupt: disruptCount,
                 holdDelta: lastHoldDelta, faceDelta: lastFaceDelta,
                 sinceCapture: samplesSinceCapture)
    }

    public struct Snapshot: Sendable, Equatable {
        public var phase: TriggerPhase
        public var baseline: Int
        public var boxes: Int
        public var cue: CGRect?
        public var capturedBox: CGRect?
        public var settleSamples: Int
        public var stable: Int
        public var grace: Int
        public var blink: Int
        public var real: Int
        public var abandoned: Int
        public var disrupt: Int
        public var holdDelta: Double?
        public var faceDelta: Double?
        public var sinceCapture: Int

        public var line: String {
            let hold = holdDelta.map { String(format: "%.1f", $0) } ?? "-"
            let facing = faceDelta.map { String(format: "%.1f", $0) } ?? "-"
            return "phase=\(phase) settle=\(settleSamples) baseline=\(baseline)"
                + " boxes=\(boxes) stable=\(stable)"
                + " grace=\(grace) blink=\(blink) real=\(real)"
                + " abandoned=\(abandoned) disrupt=\(disrupt)"
                + " hold=\(hold) face=\(facing) sinceCapture=\(sinceCapture)"
        }
    }

    public enum Decision: Equatable {
        case nothing
        case fire
        case phaseChanged(TriggerPhase)
    }

    public func arm(with sample: TriggerSample) {
        rearmCause = .none
        capturedCardScene = nil
        capturedCardBox = nil
        baseline = sample.boxes
        abandonedPasses = 0
        resetPass()
        phase = .searching
        prevScene = sample.scene
        capturedScene = nil
    }

    public func disarm() {
        phase = .off
        watched = nil
        cue = nil
        baseline = []
        resetPass()
    }

    public func forceRearm(cause: RearmCause = .nudged) {
        guard phase == .hold else { return }
        rearmCause = cause
        resetPass()
        disruptCount = 0
        watched = nil
        cue = nil
        phase = .searching
    }

    public func captureFinished(scene: SceneSignature?, cardScene: SceneSignature? = nil,
                                cardBox: CGRect? = nil) {
        capturedScene = scene
        capturedCardScene = cardScene
        capturedCardBox = cardBox
        phase = .hold
        disruptCount = 0
        samplesSinceCapture = 0
        lastHoldDelta = nil
        lastFaceDelta = nil
    }

    public func observe(_ sample: TriggerSample) -> Decision {
        lastBoxes = sample.boxes.count
        samplesInPhase += 1
        samplesSinceCapture += 1
        let phaseOnEntry = phase
        defer { if phase != phaseOnEntry { samplesInPhase = 0 } }
        defer { prevScene = sample.scene }
        switch phase {
        case .off, .capturing:
            return .nothing
        case .hold:
            return hold(sample)
        case .searching, .stabilizing:
            return stabilize(sample)
        }
    }

    private func stabilize(_ sample: TriggerSample) -> Decision {
        guard sample.focusSettled, !sample.rigMoving else { return .nothing }

        let novel = sample.boxes.filter { box in
            !baseline.contains { overlap($0, box) > tuning.backgroundIoU }
        }

        guard let candidate = novel.first else {
            return blinkOrBurn(sample)
        }
        realDetections += 1

        guard let watched else {
            self.watched = candidate
            cue = candidate
            stableCount = 1
            return enter(.stabilizing)
        }

        if overlap(watched, candidate) > tuning.iou {
            cue = candidate
            return count(sample)
        }
        if contains(watched, candidate) {
            return count(sample)
        }
        if contains(candidate, watched), isStill(sample) {
            self.watched = candidate
            cue = candidate
            return count(sample)
        }
        return burnGrace()
    }

    private func blinkOrBurn(_ sample: TriggerSample) -> Decision {
        guard watched != nil,
              isStill(sample),
              realDetections >= 2,
              blinkCount < tuning.stableSamples / 2,
              sample.scene.detail >= tuning.sceneDetail,
              sceneChangedSinceCapture(sample)
        else { return burnGrace() }
        blinkCount += 1
        return count(sample)
    }

    private func count(_ sample: TriggerSample) -> Decision {
        stableCount += 1
        graceCount = 0
        guard stableCount >= tuning.stableSamples else { return .nothing }
        guard sample.scene.detail >= tuning.sceneDetail,
              sceneChangedSinceCapture(sample)
        else { return .nothing }
        settleAtFire = samplesInPhase
        phase = .capturing
        return .fire
    }

    private func burnGrace() -> Decision {
        graceCount += 1
        guard graceCount > tuning.graceSamples else { return .nothing }
        abandonPass()
        return enter(.searching)
    }

    private func abandonPass() {
        abandonedPasses += 1
        resetPass()
        watched = nil
        cue = nil
        if rearmCause == .nudged { rearmCause = .none }
        if abandonedPasses >= tuning.backgroundResetPasses {
            baseline = []
            abandonedPasses = 0
        }
    }

    private func hold(_ sample: TriggerSample) -> Decision {
        guard sample.focusSettled else { return .nothing }
        if sample.rigMoving {
            disruptCount += tuning.rearmSamples
            return rearmIfDisrupted()
        }
        lastHoldDelta = sample.holdScene.flatMap { live in
            capturedCardScene.map { live.delta(to: $0) }
        }
        let stillThere = sample.boxes.indices.contains { i in
            guard let watched,
                  overlap(watched, sample.boxes[i]) > tuning.backgroundIoU
            else { return false }
            guard let live = sample.holdScene, let captured = capturedCardScene
            else { return true }
            return live.delta(to: captured) < tuning.cardChanged
        }
        if stillThere {
            disruptCount = max(0, disruptCount - 1)
            return .nothing
        }
        let occupied = sample.boxes.contains { box in
            watched.map { overlap($0, box) > tuning.backgroundIoU } ?? false
        }
        if occupied {
            pendingCause = stillLooksLikeTheCapturedCard(sample) ? .moved : .replaced
        } else {
            lastFaceDelta = nil
            pendingCause = .removed
        }
        disruptCount += 1
        return rearmIfDisrupted()
    }

    private func stillLooksLikeTheCapturedCard(_ sample: TriggerSample) -> Bool {
        guard let captured = capturedCardScene, !sample.boxScenes.isEmpty else {
            lastFaceDelta = nil
            return false
        }
        let closest = sample.boxScenes.map { $0.delta(to: captured) }.min()
        lastFaceDelta = closest
        return (closest ?? .infinity) < tuning.movedFaceMax
    }

    private func rearmIfDisrupted() -> Decision {
        guard disruptCount >= tuning.rearmSamples else { return .nothing }
        rearmCause = pendingCause
        resetPass()
        disruptCount = 0
        watched = nil
        cue = nil
        return enter(.searching)
    }

    private func enter(_ next: TriggerPhase) -> Decision {
        guard phase != next else { return .nothing }
        phase = next
        return .phaseChanged(next)
    }

    private func resetPass() {
        stableCount = 0
        graceCount = 0
        blinkCount = 0
        realDetections = 0
    }

    private func isStill(_ sample: TriggerSample) -> Bool {
        guard let prevScene else { return false }
        return prevScene.delta(to: sample.scene) <= tuning.stillDelta
    }

    private func sceneChangedSinceCapture(_ sample: TriggerSample) -> Bool {
        if let captured = capturedCardScene, let live = sample.holdScene {
            return live.delta(to: captured) >= tuning.cardChanged
        }
        guard let capturedScene else { return true }
        return capturedScene.delta(to: sample.scene) >= tuning.sceneChanged
    }
}

func overlap(_ a: CGRect, _ b: CGRect) -> Double {
    let i = a.intersection(b)
    guard !i.isNull, i.width > 0, i.height > 0 else { return 0 }
    let inter = Double(i.width * i.height)
    let union = Double(a.width * a.height) + Double(b.width * b.height) - inter
    return union > 0 ? inter / union : 0
}

func contains(_ outer: CGRect, _ inner: CGRect) -> Bool {
    let i = outer.intersection(inner)
    guard !i.isNull, inner.width > 0, inner.height > 0 else { return false }
    let covered = Double(i.width * i.height) / Double(inner.width * inner.height)
    return covered >= 0.8
}
