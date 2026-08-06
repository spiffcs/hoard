// Deciding when a card has been put down.
//
// Written fresh for the iPhone head, but its *behaviour* is not new: the macOS
// trigger's numbers were fitted over nine live sessions and they are the
// specification here even though none of its code is. The tuning ledger in
// docs/scanner-tuning.md is the reference, and the two lessons that cost the
// most to learn are worth repeating at the top:
//
//   · Grace freezes the streak; it does not feed it or reset it. Widening grace
//     from 3 to 6 is what took card-to-card cadence from 9.6s to 5.1s. A value
//     of 3 looks equivalent and is not.
//   · Vision drops the card on roughly two samples in five while it sits
//     motionless. 220 of 522 stabilizing samples in one session returned no
//     rectangle at all. A trigger that treats an empty sample as "the card
//     left" never fires; one that treats it as "still there" fires at nothing.
//     Hence the blink rule, and its five guards.
//
// What changes on iOS is not the machine but its inputs. Exposure and white
// balance are locked, so the scene signature stops drifting with the room; and
// CoreMotion answers "did the rig move" outright, where the macOS side had to
// infer it from consecutive empty reads.

import CoreGraphics
import Foundation

/// What the trigger is doing.
public enum TriggerPhase: String, Sendable, Equatable {
    case off
    /// Armed, nothing worth watching yet.
    case searching
    /// Watching something that might settle.
    case stabilizing
    /// The shutter has been asked for.
    case capturing
    /// Parked on the card just shot, waiting for the scene to change.
    case hold
}

/// The knobs, with the macOS defaults that were measured rather than chosen.
public struct TriggerTuning: Sendable {
    /// Seconds between samples. Everything below is counted in samples, so this
    /// is the unit all the other numbers are denominated in.
    ///
    /// 0.033 is the camera's own frame rate rather than the 0.1 the macOS rig
    /// used, and the difference is not a tuning preference — that rig sampled a
    /// preview stream where Vision was the bottleneck, while the phone's tap
    /// delivers 30fps and was having two frames in three discarded.
    public var interval = 0.033
    /// Consecutive still samples before firing.
    ///
    /// Six samples at the interval above is 0.2s of proven stillness, against
    /// the 0.6s the macOS ledger settled on — and that ledger explicitly warns
    /// against shortening it, having measured 5 points of accuracy lost for 8%
    /// of settle. That warning does not transfer, which is the point:
    ///
    ///   - it was fitted on a rig with no motion sensor, so stillness had to be
    ///     inferred from the image alone; the phone knows outright whether it
    ///     moved;
    ///   - with drifting ambient exposure, which locked metering removes;
    ///   - and with no focus state, so a hunting lens fed blur into the streak
    ///     as if it were stillness.
    ///
    /// Three sources of false stillness the old rig had to outlast with time
    /// and this one detects directly. Walked down live from 0.6s to 0.2s: the
    /// share of captures reading nothing was unchanged at 50%, and commits went
    /// 8 to 11 with one queued either way. Re-measure before shortening it
    /// further — the next thing to give way is unlikely to announce itself.
    public var stableSamples = 6
    /// Bad samples tolerated inside one pass. Freezes the streak rather than
    /// feeding or resetting it; this is the cadence knob.
    public var graceSamples = 6
    /// IoU above which two rectangles are "the same, still".
    public var iou = 0.65
    /// The looser IoU used for background furniture and for HOLD matching.
    public var backgroundIoU = 0.5
    /// Abandoned passes before the background baseline is discarded. The
    /// baseline only ever *forgets*: nothing is added at runtime, because that
    /// is the memory that once killed auto capture at the exact spot every card
    /// lands.
    public var backgroundResetPasses = 8
    /// Pooled disruption samples before HOLD re-arms.
    public var rearmSamples = 3
    /// Mean per-cell luma change below which two frames are the same picture.
    public var stillDelta = 2.5
    /// How far the picture *inside the card's own rect* must differ from the
    /// captured card before it counts as a different card.
    ///
    /// Compared against a signature sampled inside the box, not across the
    /// frame, so it answers about the card rather than about the desk. Two
    /// different cards differ enormously by this measure; the same card
    /// jostled and settling back differs by sensor noise.
    ///
    /// Set well above noise and well below "two different card faces". The
    /// risk of setting it too low is a machine that re-fires on the card it
    /// just shot, which shows up as captures-per-commit rising — watch that
    /// number rather than this one.
    public var cardChanged = 12.0
    /// How far the scene must differ from the last capture before firing again,
    /// so a parked scene cannot photograph itself forever.
    public var sceneChanged = 6.0
    /// Luma spread below which there is nothing worth photographing — this is
    /// what stops a bare desk, changed and still, from firing.
    public var sceneDetail = 12.0

    public init() {}
}

/// One sample's worth of evidence.
public struct TriggerSample: Sendable {
    /// Candidate card rectangles, normalized, largest first.
    public var boxes: [CGRect]
    /// A coarse luma grid of the frame; see SceneSignature.
    public var scene: SceneSignature
    /// The same grid sampled through the window the shutter fired on —
    /// `Snapshot.capturedBox`, held fixed since the capture.
    ///
    /// What lets the machine tell one card from another. Geometry alone cannot:
    /// a card laid on the spot the last one occupied produces a box in the same
    /// place, and the only thing that differs is the picture inside it.
    ///
    /// **The window must not move.** This was first sampled through each
    /// sample's own detector box, which was wrong in a way that only shows up
    /// on a real camera: `sceneSignature` takes one *point* sample per cell, so
    /// it is maximally sensitive to sub-cell shifts, and Vision's rectangle
    /// jitters every frame. Measured on 4032x3024 stills of a card that never
    /// moved — through an identical window the delta is 2.7, and shifting that
    /// window by half a percent of the card takes it to 13.6, past any usable
    /// threshold. A parked card reported itself replaced 16 times in one live
    /// minute, and each one committed another copy.
    ///
    /// Fixed, the same measurement separates cleanly: 2.7 for a static card
    /// against 29-50 for a card actually swapped in place.
    ///
    /// Nil is tolerated — a caller that cannot sample it gets the old
    /// geometry-only behaviour rather than an error.
    public var holdScene: SceneSignature?
    /// False while the lens is hunting. Blur reads as stillness, so a hunting
    /// lens freezes the machine entirely rather than feeding it.
    public var focusSettled: Bool
    /// True when the device itself is moving. The macOS side inferred this from
    /// two consecutive empty reads; a phone simply knows.
    public var rigMoving: Bool

    public init(boxes: [CGRect], scene: SceneSignature,
                holdScene: SceneSignature? = nil,
                focusSettled: Bool = true, rigMoving: Bool = false) {
        self.boxes = boxes
        self.scene = scene
        self.holdScene = holdScene
        self.focusSettled = focusSettled
        self.rigMoving = rigMoving
    }
}

/// Trigger decides when to fire.
///
/// Deliberately synchronous and free of any camera type: it takes samples and
/// returns decisions, which is what makes the whole machine testable without a
/// phone. Everything about *producing* a sample lives with the caller.
public final class Trigger {
    public private(set) var phase: TriggerPhase = .off
    public var tuning = TriggerTuning()

    /// The box currently being watched, and how long it has held.
    ///
    /// Deliberately *not* refreshed when a later box matches it: IoU is
    /// measured against a fixed reference so a slowly sliding card cannot
    /// ratchet its way across the frame by matching itself every sample.
    private var watched: CGRect?
    /// The same card, at its latest measured position — the rect to *draw*.
    ///
    /// `watched` is the wrong thing to put on screen for exactly the reason it
    /// is the right thing to match against: it is frozen at the geometry the
    /// card was first seen with, and IoU 0.65 on a card-shaped rect tolerates
    /// something like 10-15% of linear drift. Brackets drawn from it would sit
    /// visibly off the card while the trigger was perfectly happy.
    ///
    /// So this carries `watched`'s *identity* decisions with fresh geometry. It
    /// is observational: nothing reads it back, and no decision depends on it.
    private(set) var cue: CGRect?
    private var stableCount = 0
    private var graceCount = 0
    private var blinkCount = 0
    private var realDetections = 0
    /// Rectangles present the instant the trigger armed — desk furniture, deck
    /// boxes, the mat's edge. Only a box the baseline does not explain can arm.
    private var baseline: [CGRect] = []
    private var abandonedPasses = 0
    /// Pooled HOLD disruption. Decays on calm samples rather than resetting.
    private var disruptCount = 0
    private var prevScene: SceneSignature?
    private var capturedScene: SceneSignature?

    /// Why the machine last left `.hold`.
    ///
    /// The parent cannot tell a new card from a second look at the old one, and
    /// has been guessing from a clock — a four-second window whose own comment
    /// concedes "a real scan can race the nudge onto the wire". The machine
    /// takes three distinct code paths here and then throws the distinction
    /// away, so the guess is reconstructing a fact that was available.
    public private(set) var rearmCause: RearmCause = .none

    public enum RearmCause: String, Sendable {
        /// Nothing has re-armed yet, or the pass was armed from scratch.
        case none
        /// No box overlapped the watched card any more: it left the frame.
        case removed
        /// A box held the place but its picture changed: swapped in place.
        case replaced
        /// The parent asked, with no physical evidence at all.
        case nudged
    }

    /// The picture inside the card that was last captured.
    private var capturedCardScene: SceneSignature?
    /// The window `capturedCardScene` was sampled through, kept so every later
    /// comparison uses the identical one. See `TriggerSample.holdScene`.
    private var capturedCardBox: CGRect?
    /// What the disruption looked like, banked until it crosses the threshold.
    private var pendingCause: RearmCause = .removed

    /// The most recent sample's box count, kept only for the snapshot.
    private var lastBoxes = 0
    /// Samples seen since the machine entered its current phase.
    ///
    /// A count rather than a clock, so this file stays free of `Date` and the
    /// whole machine stays testable without one. Multiplied by the interval it
    /// is the settle time — the wait from "holding still" to the shutter, which
    /// is the largest single term in the delay a person actually feels and was
    /// the one number the telemetry could not report.
    private var samplesInPhase = 0

    public init() {}

    /// A read-only look at the counters, for telemetry.
    ///
    /// The phone reported only its phase, which says a capture happened and
    /// nothing about why. That is enough to notice waste and useless for fixing
    /// it: one live session fired 30 times for 17 cards, and the numbers that
    /// would say whether the baseline had gone stale, whether the scene was
    /// churning, or whether the streak was being met by furniture were all
    /// private. Tuning a trigger by guessing at knobs has already cost this
    /// project twice; these are the numbers that make it arithmetic instead.
    public var snapshot: Snapshot {
        Snapshot(phase: phase, baseline: baseline.count, boxes: lastBoxes,
                 cue: cue,
                 capturedBox: capturedCardBox,
                 settleSamples: samplesInPhase,
                 stable: stableCount, grace: graceCount, blink: blinkCount,
                 real: realDetections, abandoned: abandonedPasses,
                 disrupt: disruptCount)
    }

    public struct Snapshot: Sendable, Equatable {
        public var phase: TriggerPhase
        public var baseline: Int
        public var boxes: Int
        /// The box to draw, normalized in the *unrotated* buffer's space with
        /// Vision's bottom-left origin. Nil when the machine is tracking none.
        ///
        /// The right box to draw rather than "the largest this instant": the
        /// continuity rules have already been applied — IoU matching, the
        /// sliver rule, the containment rule — so a view consuming this
        /// inherits a steady bracket without reimplementing any of it. The
        /// macOS overlay re-derives all of that precisely because it is handed
        /// raw per-sample boxes instead.
        public var cue: CGRect?
        /// The window to sample `TriggerSample.holdScene` through: the card's
        /// geometry as it was at the shutter, unchanged since. Nil outside
        /// `.hold`, or when the shutter could not sample one.
        ///
        /// Handed out rather than re-derived per frame *because* it must not
        /// move — a window that tracks the live detector box turns rectangle
        /// jitter into a phantom card swap.
        public var capturedBox: CGRect?
        /// Samples since the phase changed. Times the interval, the settle.
        public var settleSamples: Int
        public var stable: Int
        public var grace: Int
        public var blink: Int
        public var real: Int
        public var abandoned: Int
        public var disrupt: Int

        /// One line, in the shape the rest of the telemetry uses.
        public var line: String {
            "phase=\(phase) settle=\(settleSamples) baseline=\(baseline)"
                + " boxes=\(boxes) stable=\(stable)"
                + " grace=\(grace) blink=\(blink) real=\(real)"
                + " abandoned=\(abandoned) disrupt=\(disrupt)"
        }
    }

    /// What the caller should do about this sample.
    public enum Decision: Equatable {
        case nothing
        case fire
        case phaseChanged(TriggerPhase)
    }

    // MARK: - Arming

    /// arm starts watching, learning whatever is in frame as background.
    ///
    /// The staging area should be clear when this happens: a card already
    /// sitting there reads as furniture until it is removed and re-placed.
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

    /// forceRearm is the parent's content-aware nudge.
    ///
    /// Geometry cannot tell a card stacked squarely on the pile from the card
    /// just shot; the parent knows what it already processed. It re-arms, the
    /// scene fires, and an identical read is the parent's to discard.
    public func forceRearm() {
        guard phase == .hold else { return }
        // No physical evidence whatsoever: a timer on the parent expired. This
        // is exactly the case the parent must be able to tell apart, and until
        // now it could only guess.
        rearmCause = .nudged
        resetPass()
        disruptCount = 0
        watched = nil
        cue = nil
        phase = .searching
    }

    /// captureFinished parks the machine on what it just shot.
    public func captureFinished(scene: SceneSignature?, cardScene: SceneSignature? = nil,
                                cardBox: CGRect? = nil) {
        capturedScene = scene
        // The picture *inside* the card that was just shot, which is what tells
        // a swapped card from the same one settling back. Optional so a caller
        // that cannot sample it keeps the frame-wide behaviour.
        capturedCardScene = cardScene
        // And the window it was sampled through, which every later comparison
        // has to reuse. Handing this back out is the whole point: the caller
        // cannot pin the window without it, and an unpinned window makes a
        // motionless card look replaced. See `TriggerSample.holdScene`.
        capturedCardBox = cardBox
        // Deliberately *not* learning a no-card shot as background: telemetry
        // showed that absorbing the scanning pile itself after one glared read.
        phase = .hold
        disruptCount = 0
    }

    // MARK: - The machine

    public func observe(_ sample: TriggerSample) -> Decision {
        lastBoxes = sample.boxes.count
        samplesInPhase += 1
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
        // A moving rig or a hunting lens freezes everything: no streak growth,
        // no grace burn, no reset. Blur reads as stillness, and a rig being
        // nudged produces boxes that look exactly like a card being placed.
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
            // The match is what keeps the streak; the candidate is where the
            // card is *now*, which is what the cue wants.
            cue = candidate
            return count(sample)
        }
        // A sliver of a card already being watched is that card, not a new one.
        // Borderless art crumbles under the detector, and a motionless card can
        // alternate between its whole self and fragments for seconds.
        if contains(watched, candidate) {
            return count(sample)
        }
        // The reverse: the streak latched onto a sliver and the card is now
        // seen whole again. Gated on the picture being still, because a hand
        // sweeping in also produces a box containing what was being watched.
        if contains(candidate, watched), isStill(sample) {
            self.watched = candidate
            cue = candidate
            return count(sample)
        }
        return burnGrace()
    }

    /// blinkOrBurn decides what an empty sample means.
    ///
    /// All five guards, because the first cut of this was a disaster: without
    /// them 82% of captures read nothing. An empty sample counts as evidence
    /// only when everything else says the card is still sitting there.
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
        // The last two gates before a shutter. Detail stops a bare desk firing;
        // changed stops a parked scene photographing itself forever.
        guard sample.scene.detail >= tuning.sceneDetail,
              sceneChangedSinceCapture(sample)
        else { return .nothing }
        phase = .capturing
        return .fire
    }

    /// burnGrace tolerates a bad sample without feeding the streak.
    ///
    /// The distinction that matters: grace *freezes* progress rather than
    /// resetting it. Widening this from 3 to 6 is what halved card-to-card
    /// cadence, and it did so by abandoning fewer passes, not by shortening
    /// any of them.
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
        // The baseline self-heals. Live: one background rectangle, then 46
        // seconds of the detector finding the card and the filter deleting it.
        if abandonedPasses >= tuning.backgroundResetPasses {
            baseline = []
            abandonedPasses = 0
        }
    }

    /// hold waits for the scene to change enough to be a different card.
    private func hold(_ sample: TriggerSample) -> Decision {
        guard sample.focusSettled else { return .nothing }
        // A moving rig is disruption by definition, and the phone knows it
        // without having to infer it from failed reads.
        if sample.rigMoving {
            disruptCount += tuning.rearmSamples
            return rearmIfDisrupted()
        }
        // Same place *and* same picture.
        //
        // Geometry alone said a card was still there whenever any box overlapped
        // the watched rect — so a card laid on the spot the last one occupied
        // read as "unchanged", disruption decayed, and the machine never
        // re-armed. That card was not double-counted, it was **never scanned**,
        // and nothing in the telemetry said so.
        //
        // A caller that supplies no box signatures gets the old geometry-only
        // behaviour, which keeps the machine usable from a test that does not
        // care about content.
        let stillThere = sample.boxes.indices.contains { i in
            guard let watched,
                  overlap(watched, sample.boxes[i]) > tuning.backgroundIoU
            else { return false }
            guard let live = sample.holdScene, let captured = capturedCardScene
            else { return true }
            return live.delta(to: captured) < tuning.cardChanged
        }
        if stillThere {
            // Decay, never zero. Placement disruption arrives in one- and
            // two-sample bursts with settled samples interleaved; a hard reset
            // saws between 1 and 2 forever while the user reaches for the next
            // card.
            disruptCount = max(0, disruptCount - 1)
            return .nothing
        }
        // Which of the two disruptions this is, for the parent. A box still
        // sitting on the watched rect means the card was swapped in place; no
        // box there at all means it left.
        let occupied = sample.boxes.contains { box in
            watched.map { overlap($0, box) > tuning.backgroundIoU } ?? false
        }
        pendingCause = occupied ? .replaced : .removed
        disruptCount += 1
        return rearmIfDisrupted()
    }

    private func rearmIfDisrupted() -> Decision {
        guard disruptCount >= tuning.rearmSamples else { return .nothing }
        rearmCause = pendingCause
        // Novelty is judged against the desk baseline, never against the card
        // just shot — so the next card fires even sitting in exactly the same
        // place.
        resetPass()
        disruptCount = 0
        watched = nil
        cue = nil
        return enter(.searching)
    }

    // MARK: - Helpers

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

    /// Whether anything has changed since the shutter, so a parked scene cannot
    /// photograph itself forever.
    ///
    /// Asks about the *card* when it can, and falls back to the frame when it
    /// cannot. The frame-wide test was the only one available and it is blunt in
    /// both directions: a card is a fraction of the frame, so swapping one for a
    /// completely different card barely moves a mean over 384 cells spread
    /// across the desk — two similar cards in the same spot never fired at all —
    /// while a hand crossing a corner moves it easily.
    private func sceneChangedSinceCapture(_ sample: TriggerSample) -> Bool {
        if let captured = capturedCardScene, let live = sample.holdScene {
            return live.delta(to: captured) >= tuning.cardChanged
        }
        guard let capturedScene else { return true }
        return capturedScene.delta(to: sample.scene) >= tuning.sceneChanged
    }
}

/// overlap is intersection over union.
func overlap(_ a: CGRect, _ b: CGRect) -> Double {
    let i = a.intersection(b)
    guard !i.isNull, i.width > 0, i.height > 0 else { return 0 }
    let inter = Double(i.width * i.height)
    let union = Double(a.width * a.height) + Double(b.width * b.height) - inter
    return union > 0 ? inter / union : 0
}

/// contains reports whether `inner` is essentially inside `outer` — the
/// fragment relation. Not strict containment: a detector's idea of a sliver
/// wobbles a few percent past the edge of the box it came from.
func contains(_ outer: CGRect, _ inner: CGRect) -> Bool {
    let i = outer.intersection(inner)
    guard !i.isNull, inner.width > 0, inner.height > 0 else { return false }
    let covered = Double(i.width * i.height) / Double(inner.width * inner.height)
    return covered >= 0.8
}
