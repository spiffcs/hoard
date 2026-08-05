// The trigger, against the situations that actually happen on a desk.
//
// Every test here corresponds to something the tuning ledger records happening
// in a live session — a card detected on three samples in five, a sliver that
// is really the same card, a hand passing over the pile. Synthetic boxes and
// signatures, real scenarios.

import CoreGraphics
import Testing

@testable import CardKit

/// A frame with plenty of detail, so the "nothing worth photographing" gate
/// never fires by accident in tests that are about something else.
private func busyScene(seed: UInt8 = 0) -> SceneSignature {
    var cells = [UInt8](repeating: 0, count: SceneSignature.columns * SceneSignature.rows)
    for i in cells.indices {
        cells[i] = UInt8((i * 7 + Int(seed)) % 200)
    }
    return SceneSignature(cells: cells)
}

/// A frame with no variation at all — a bare mat.
private func flatScene(_ value: UInt8 = 128) -> SceneSignature {
    SceneSignature(cells: [UInt8](repeating: value,
                                  count: SceneSignature.columns * SceneSignature.rows))
}

private let card = CGRect(x: 0.3, y: 0.2, width: 0.4, height: 0.55)

private func sample(_ boxes: [CGRect], _ scene: SceneSignature,
                    focusSettled: Bool = true, rigMoving: Bool = false) -> TriggerSample {
    TriggerSample(boxes: boxes, scene: scene, focusSettled: focusSettled, rigMoving: rigMoving)
}

/// Feeds n samples and returns whether it fired.
@discardableResult
private func feed(_ t: Trigger, _ n: Int, _ s: @autoclosure () -> TriggerSample) -> Bool {
    var fired = false
    for _ in 0..<n where t.observe(s()) == .fire { fired = true }
    return fired
}

// MARK: - The basic pass

@Test("a card held still for six samples fires")
func firesOnStillCard() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(feed(t, 8, sample([card], busyScene())))
    #expect(t.phase == .capturing)
}

@Test("a card held for fewer than six samples does not fire")
func doesNotFireEarly() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(!feed(t, 4, sample([card], busyScene())))
}

@Test("furniture present when the trigger armed can never fire")
func backgroundNeverFires() {
    // A deck box on the mat, a card sleeve, the edge of the playmat. Whatever
    // was there when auto turned on is scenery until it moves.
    let t = Trigger()
    t.arm(with: sample([card], busyScene()))
    #expect(!feed(t, 20, sample([card], busyScene())))
}

// MARK: - The blink rule

@Test("a card that blinks out of detection still fires")
func blinkTolerated() {
    // Vision drops a motionless card on roughly two samples in five. A trigger
    // that treats an empty sample as "the card left" never fires at all.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    var fired = false
    // Seen, seen, gone, seen, gone, seen, seen, seen — a realistic pattern.
    for boxes in [[card], [card], [], [card], [], [card], [card], [card], [card]] {
        if t.observe(sample(boxes, scene)) == .fire { fired = true }
    }
    #expect(fired)
}

@Test("blinks alone are not evidence")
func blinksCannotCarryAPass() {
    // At most half a streak may be blinks. Otherwise an empty frame with a
    // still picture would fire the shutter at a bare desk.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = t.observe(sample([card], scene))
    #expect(!feed(t, 20, sample([], scene)))
}

@Test("a bare desk that is changed and still does not fire")
func bareDeskDoesNotFire() {
    // Lifting a card away leaves a scene that is changed, and still, and
    // completely empty. Detail is what separates it from a card sitting there.
    let t = Trigger()
    t.arm(with: sample([], busyScene()))
    #expect(!feed(t, 20, sample([], flatScene())))
}

// MARK: - Fragments

@Test("a sliver inside the watched box is the same card")
func fragmentCountsAsStillness() {
    // Borderless art crumbles under the detector: a motionless card alternates
    // between its whole self and pieces of itself.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    let sliver = CGRect(x: 0.35, y: 0.3, width: 0.1, height: 0.12)
    var fired = false
    for boxes in [[card], [sliver], [card], [sliver], [card], [sliver], [card]] {
        if t.observe(sample(boxes, scene)) == .fire { fired = true }
    }
    #expect(fired)
}

@Test("a card seen whole again after a sliver keeps the streak")
func fragmentReverseDirection() {
    // The streak latched onto a sliver and the card is now seen whole. Gated
    // on the picture being still, because a hand sweeping in also produces a
    // box containing what was being watched.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    let sliver = CGRect(x: 0.35, y: 0.3, width: 0.1, height: 0.12)
    var fired = false
    for boxes in [[sliver], [sliver], [card], [card], [card], [card], [card]] {
        if t.observe(sample(boxes, scene)) == .fire { fired = true }
    }
    #expect(fired)
}

// MARK: - Focus and motion

@Test("a hunting lens freezes the machine rather than feeding it")
func focusHuntFreezes() {
    // Blur reads as stillness. A trigger that counted hunting samples would
    // fire mid-hunt, on the blurriest frame available.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(!feed(t, 30, sample([card], busyScene(), focusSettled: false)))
}

@Test("a moving rig freezes the machine")
func rigMotionFreezes() {
    // The phone knows it is being nudged. The macOS side had to infer this
    // from two consecutive empty reads, which is slower and less certain.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(!feed(t, 30, sample([card], busyScene(), rigMoving: true)))
}

// MARK: - Grace

@Test("grace freezes a pass rather than resetting it")
func graceDoesNotReset() {
    // The knob that took cadence from 9.6s to 5.1s. A few junk samples must
    // not throw away accumulated evidence, or every pass restarts forever.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    let elsewhere = CGRect(x: 0.05, y: 0.05, width: 0.1, height: 0.1)
    _ = feed(t, 4, sample([card], scene))
    // Two mismatched samples: tolerated, and the streak is not lost.
    _ = t.observe(sample([elsewhere], scene))
    _ = t.observe(sample([elsewhere], scene))
    // Two more of the real card should finish the six.
    var fired = false
    for _ in 0..<3 where t.observe(sample([card], scene)) == .fire { fired = true }
    #expect(fired, "grace reset the streak instead of freezing it")
}

@Test("a pass is abandoned once grace runs out")
func graceRunsOut() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    let elsewhere = CGRect(x: 0.05, y: 0.05, width: 0.1, height: 0.1)
    _ = t.observe(sample([card], scene))
    // One sample opened the pass; seven more burn through grace and abandon it.
    // Exactly that many, because an eighth would start a *new* pass on the
    // novel box — which is correct behaviour and would hide the abandonment.
    _ = feed(t, 7, sample([elsewhere], scene))
    #expect(t.phase == .searching)
}

// MARK: - HOLD

@Test("the shot card does not fire again while it sits there")
func holdParksOnTheCard() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    #expect(feed(t, 8, sample([card], scene)))
    t.captureFinished(scene: scene)
    #expect(!feed(t, 30, sample([card], scene)))
    #expect(t.phase == .hold)
}

@Test("disruption decays instead of resetting")
func holdDisruptionDecays() {
    // Placement disruption arrives in one- and two-sample bursts with settled
    // samples interleaved. A counter that zeroed on every calm sample would
    // saw between 1 and 2 forever and never re-arm.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = feed(t, 8, sample([card], scene))
    t.captureFinished(scene: scene)
    // disrupt, calm, disrupt, calm, disrupt — never three in a row.
    for boxes in [[], [card], [], [card], []] { _ = t.observe(sample(boxes, scene)) }
    #expect(t.phase == .hold, "an interleaved burst re-armed too eagerly")
    // Three clean disruptions in a row does re-arm.
    _ = feed(t, 3, sample([], scene))
    #expect(t.phase == .searching)
}

@Test("a moving rig re-arms hold at once")
func rigMotionRearms() {
    // Picking the phone up is unambiguous, and waiting three samples to admit
    // it is latency nobody asked for.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = feed(t, 8, sample([card], scene))
    t.captureFinished(scene: scene)
    _ = t.observe(sample([card], scene, rigMoving: true))
    #expect(t.phase == .searching)
}

@Test("the parent's nudge re-arms a parked trigger")
func forceRearm() {
    // Geometry cannot tell a card stacked squarely on the pile from the card
    // just shot. The parent knows what it already processed.
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = feed(t, 8, sample([card], scene))
    t.captureFinished(scene: scene)
    t.forceRearm()
    #expect(t.phase == .searching)
    #expect(feed(t, 8, sample([card], busyScene(seed: 90))))
}

@Test("the nudge does nothing outside hold")
func forceRearmOnlyInHold() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    t.forceRearm()
    #expect(t.phase == .searching)
}

// MARK: - The captured-scene gate

@Test("a scene nobody has touched cannot photograph itself twice")
func sceneMustChangeAfterCapture() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    #expect(feed(t, 8, sample([card], scene)))
    t.captureFinished(scene: scene)
    t.forceRearm()
    // Same picture as the capture: re-armed, but must not fire.
    #expect(!feed(t, 20, sample([card], scene)))
}

// MARK: - The background baseline

@Test("the baseline forgets after enough abandoned passes")
func baselineSelfHeals() {
    // Live: one background rectangle, then 46 seconds of the detector finding
    // the card and the filter deleting it. The baseline only ever forgets —
    // nothing is added at runtime, because that is the memory that once killed
    // auto capture at the exact spot every card lands.
    let t = Trigger()
    t.arm(with: sample([card], busyScene()))
    let scene = busyScene()
    let elsewhere = CGRect(x: 0.02, y: 0.02, width: 0.08, height: 0.08)
    // Abandon passes until the baseline is discarded.
    for _ in 0..<10 {
        _ = t.observe(sample([elsewhere], scene))
        _ = feed(t, 8, sample([], flatScene()))
    }
    #expect(feed(t, 10, sample([card], scene)), "the baseline never healed")
}

// MARK: - Signatures

@Test("two readings of the same picture are the same picture")
func signatureStillness() {
    #expect(busyScene().delta(to: busyScene()) == 0)
}

@Test("signatures that cannot be compared read as maximally different")
func signatureUnknown() {
    // Never as still: a missing frame that read as stillness would fire the
    // shutter at nothing.
    #expect(SceneSignature.unknown.delta(to: busyScene()) > 1000)
    #expect(busyScene().delta(to: .unknown) > 1000)
}

@Test("a bare mat carries no detail and a busy frame carries plenty")
func signatureDetail() {
    #expect(flatScene().detail < 1)
    #expect(busyScene().detail > 12)
}

// MARK: - Settle measurement

@Test("the settle counter advances within a phase and resets on a change")
func settleCounterTracksThePhase() {
    // The number this exists to report is the wait from "holding still" to the
    // shutter, which the wire could not see: searching↔stabilizing transitions
    // are deliberately kept off it, so a session could show accuracy holding at
    // a third of the span while saying nothing about what that bought.
    //
    // Asserted on the counter alone rather than on a fire. Whether a given
    // sequence fires is the phase machine's business and is covered by the
    // tests above; borrowing that here made this fail for a reason that had
    // nothing to do with what it measures.
    var t = Trigger()
    t.tuning.stableSamples = 3
    let card = CGRect(x: 0.3, y: 0.3, width: 0.4, height: 0.4)
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.settleSamples == 0, "arming starts a phase, not a streak")

    var seen: [Int] = []
    var phases: [TriggerPhase] = []
    for _ in 1...8 {
        _ = t.observe(TriggerSample(boxes: [card], scene: scene))
        seen.append(t.snapshot.settleSamples)
        phases.append(t.snapshot.phase)
    }
    // Within one phase the count only ever climbs by one per sample, and it
    // returns to zero whenever the phase moves.
    for i in 1..<seen.count {
        if phases[i] == phases[i - 1] {
            #expect(seen[i] == seen[i - 1] + 1,
                    "count jumped \(seen[i - 1]) to \(seen[i]) inside one phase")
        } else {
            #expect(seen[i] == 0, "a phase change must reset the settle count")
        }
    }
    #expect(phases.contains { $0 != .searching },
            "eight still samples should have moved the machine off searching")
}

@Test("the snapshot reports the box to draw, at its latest position")
func snapshotReportsWatchedBox() {
    // The on-screen cue draws this box, so what it reports has to be the one
    // the machine actually settled on — not whichever is largest this instant.
    // The continuity rules live in the trigger; a view consuming this inherits
    // them, which is the whole reason it is surfaced rather than the raw boxes.
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let card = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == nil, "nothing in frame, nothing to draw")

    _ = t.observe(TriggerSample(boxes: [card], scene: scene))
    #expect(t.snapshot.cue == card, "the card in frame is the box to draw")

    // A second, smaller box does not steal the cue: the machine keeps the one
    // it was already watching, which is what stops the bracket teleporting.
    let clutter = CGRect(x: 0.75, y: 0.75, width: 0.2, height: 0.2)
    _ = t.observe(TriggerSample(boxes: [clutter, card], scene: scene))
    #expect(t.snapshot.cue == card, "the cue must not jump to desk clutter")
}

@Test("the drawn box follows the card, it does not freeze where it first landed")
func cueFollowsTheCard() {
    // The distinction between `cue` and `watched`, and the reason both exist.
    // `watched` is deliberately frozen so IoU is measured against a fixed
    // reference and a sliding card cannot ratchet across the frame by matching
    // itself every sample. Drawing that would pin the brackets to where the
    // card first appeared: IoU 0.65 on a card-shaped rect tolerates roughly
    // 10-15% of drift, which is plainly visible on screen.
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let first = CGRect(x: 0.30, y: 0.25, width: 0.40, height: 0.50)
    // Nudged a little: still the same card by IoU, but somewhere else.
    let nudged = CGRect(x: 0.34, y: 0.27, width: 0.40, height: 0.50)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(boxes: [first], scene: scene))
    _ = t.observe(TriggerSample(boxes: [nudged], scene: scene))

    #expect(t.snapshot.cue == nudged,
            "the brackets must sit where the card is now, not where it arrived")
}

@Test("a detector blink does not drop the drawn box")
func cueSurvivesABlink() {
    // No hold timer in the view: the machine already absorbs this. An empty
    // sample burns grace, and only graceSamples consecutive bad ones abandon
    // the pass — six of them, which at a 0.033s interval is ~0.2s. Blink rate
    // is a per-sample property, so the sample count is what transfers from the
    // macOS rig, not its 0.5s wall-clock hold.
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let card = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(boxes: [card], scene: scene))
    #expect(t.snapshot.cue == card)

    // Vision drops the card on roughly two samples in five.
    _ = t.observe(TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == card, "one empty sample is a blink, not a departure")
    _ = t.observe(TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == card, "still a blink")
}

@Test("disarming clears the tracked box")
func disarmClearsWatchedBox() {
    // Otherwise the brackets would sit on screen over a card the trigger is no
    // longer considering.
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(
        boxes: [CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)], scene: scene))
    #expect(t.snapshot.cue != nil)

    t.disarm()
    #expect(t.snapshot.cue == nil)
}
