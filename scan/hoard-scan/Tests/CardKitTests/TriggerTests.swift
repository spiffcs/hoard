import CoreGraphics
import Testing

@testable import CardKit

private func busyScene(seed: UInt8 = 0) -> SceneSignature {
    var cells = [UInt8](repeating: 0, count: SceneSignature.columns * SceneSignature.rows)
    for i in cells.indices {
        cells[i] = UInt8((i * 7 + Int(seed)) % 200)
    }
    return SceneSignature(cells: cells)
}

private func flatScene(_ value: UInt8 = 128) -> SceneSignature {
    SceneSignature(cells: [UInt8](repeating: value,
                                  count: SceneSignature.columns * SceneSignature.rows))
}

private let card = CGRect(x: 0.3, y: 0.2, width: 0.4, height: 0.55)

private func sample(_ boxes: [CGRect], _ scene: SceneSignature,
                    focusSettled: Bool = true, rigMoving: Bool = false) -> TriggerSample {
    TriggerSample(boxes: boxes, scene: scene, focusSettled: focusSettled, rigMoving: rigMoving)
}

@discardableResult
private func feed(_ t: Trigger, _ n: Int, _ s: @autoclosure () -> TriggerSample) -> Bool {
    var fired = false
    for _ in 0..<n where t.observe(s()) == .fire { fired = true }
    return fired
}

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

@Test("a fire reports how long the card actually settled")
func fireReportsItsSettle() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(feed(t, 8, sample([card], busyScene())))
    #expect(t.phase == .capturing)
    #expect(t.snapshot.settleSamples > 0,
            "settle read \(t.snapshot.settleSamples): the fire-time value was lost")
    #expect(t.snapshot.settleSamples == t.snapshot.stable - 1)
}

@Test("furniture present when the trigger armed can never fire")
func backgroundNeverFires() {
    let t = Trigger()
    t.arm(with: sample([card], busyScene()))
    #expect(!feed(t, 20, sample([card], busyScene())))
}

@Test("a card that blinks out of detection still fires")
func blinkTolerated() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    var fired = false
    for boxes in [[card], [card], [], [card], [], [card], [card], [card], [card]] {
        if t.observe(sample(boxes, scene)) == .fire { fired = true }
    }
    #expect(fired)
}

@Test("blinks alone are not evidence")
func blinksCannotCarryAPass() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = t.observe(sample([card], scene))
    #expect(!feed(t, 20, sample([], scene)))
}

@Test("a bare desk that is changed and still does not fire")
func bareDeskDoesNotFire() {
    let t = Trigger()
    t.arm(with: sample([], busyScene()))
    #expect(!feed(t, 20, sample([], flatScene())))
}

@Test("a sliver inside the watched box is the same card")
func fragmentCountsAsStillness() {
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

@Test("a hunting lens freezes the machine rather than feeding it")
func focusHuntFreezes() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(!feed(t, 30, sample([card], busyScene(), focusSettled: false)))
}

@Test("a moving rig freezes the machine")
func rigMotionFreezes() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    #expect(!feed(t, 30, sample([card], busyScene(), rigMoving: true)))
}

@Test("grace freezes a pass rather than resetting it")
func graceDoesNotReset() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    let elsewhere = CGRect(x: 0.05, y: 0.05, width: 0.1, height: 0.1)
    _ = feed(t, 4, sample([card], scene))
    _ = t.observe(sample([elsewhere], scene))
    _ = t.observe(sample([elsewhere], scene))
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
    _ = feed(t, 7, sample([elsewhere], scene))
    #expect(t.phase == .searching)
}

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
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    _ = feed(t, 8, sample([card], scene))
    t.captureFinished(scene: scene)
    for boxes in [[], [card], [], [card], []] { _ = t.observe(sample(boxes, scene)) }
    #expect(t.phase == .hold, "an interleaved burst re-armed too eagerly")
    _ = feed(t, 3, sample([], scene))
    #expect(t.phase == .searching)
}

@Test("a moving rig re-arms hold at once")
func rigMotionRearms() {
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

@Test("a scene nobody has touched cannot photograph itself twice")
func sceneMustChangeAfterCapture() {
    let t = Trigger()
    t.arm(with: sample([], flatScene()))
    let scene = busyScene()
    #expect(feed(t, 8, sample([card], scene)))
    t.captureFinished(scene: scene)
    t.forceRearm()
    #expect(!feed(t, 20, sample([card], scene)))
}

@Test("the baseline forgets after enough abandoned passes")
func baselineSelfHeals() {
    let t = Trigger()
    t.arm(with: sample([card], busyScene()))
    let scene = busyScene()
    let elsewhere = CGRect(x: 0.02, y: 0.02, width: 0.08, height: 0.08)
    for _ in 0..<10 {
        _ = t.observe(sample([elsewhere], scene))
        _ = feed(t, 8, sample([], flatScene()))
    }
    #expect(feed(t, 10, sample([card], scene)), "the baseline never healed")
}

@Test("two readings of the same picture are the same picture")
func signatureStillness() {
    #expect(busyScene().delta(to: busyScene()) == 0)
}

@Test("signatures that cannot be compared read as maximally different")
func signatureUnknown() {
    #expect(SceneSignature.unknown.delta(to: busyScene()) > 1000)
    #expect(busyScene().delta(to: .unknown) > 1000)
}

@Test("a bare mat carries no detail and a busy frame carries plenty")
func signatureDetail() {
    #expect(flatScene().detail < 1)
    #expect(busyScene().detail > 12)
}

@Test("the settle counter advances within a phase and resets on a change")
func settleCounterTracksThePhase() {
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
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let card = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == nil, "nothing in frame, nothing to draw")

    _ = t.observe(TriggerSample(boxes: [card], scene: scene))
    #expect(t.snapshot.cue == card, "the card in frame is the box to draw")

    let clutter = CGRect(x: 0.75, y: 0.75, width: 0.2, height: 0.2)
    _ = t.observe(TriggerSample(boxes: [clutter, card], scene: scene))
    #expect(t.snapshot.cue == card, "the cue must not jump to desk clutter")
}

@Test("the drawn box follows the card, it does not freeze where it first landed")
func cueFollowsTheCard() {
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let first = CGRect(x: 0.30, y: 0.25, width: 0.40, height: 0.50)
    let nudged = CGRect(x: 0.34, y: 0.27, width: 0.40, height: 0.50)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(boxes: [first], scene: scene))
    _ = t.observe(TriggerSample(boxes: [nudged], scene: scene))

    #expect(t.snapshot.cue == nudged,
            "the brackets must sit where the card is now, not where it arrived")
}

@Test("a detector blink does not drop the drawn box")
func cueSurvivesABlink() {
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    let card = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(boxes: [card], scene: scene))
    #expect(t.snapshot.cue == card)

    _ = t.observe(TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == card, "one empty sample is a blink, not a departure")
    _ = t.observe(TriggerSample(boxes: [], scene: scene))
    #expect(t.snapshot.cue == card, "still a blink")
}

@Test("disarming clears the tracked box")
func disarmClearsWatchedBox() {
    var t = Trigger()
    let scene = SceneSignature(cells: [UInt8](repeating: 100, count: 16 * 24))
    t.arm(with: TriggerSample(boxes: [], scene: scene))
    _ = t.observe(TriggerSample(
        boxes: [CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)], scene: scene))
    #expect(t.snapshot.cue != nil)

    t.disarm()
    #expect(t.snapshot.cue == nil)
}

private func face(_ v: UInt8) -> SceneSignature {
    SceneSignature(cells: [UInt8](repeating: v, count: 16 * 24))
}

private func sample(
    _ boxes: [CGRect], _ scene: SceneSignature, faces: [SceneSignature] = []
) -> TriggerSample {
    TriggerSample(boxes: boxes, scene: scene, holdScene: faces.first)
}

@Test("a card laid over another is a new card, not the old one settling")
func cardCoveringAnotherRearms() {
    var t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60))
    #expect(t.phase == .hold)

    for _ in 0..<Trigger().tuning.rearmSamples {
        _ = t.observe(sample([spot], frame, faces: [face(200)]))
    }
    #expect(t.phase != .hold, "a different card on the same spot must re-arm")
    #expect(t.rearmCause == .replaced, "cause = \(t.rearmCause)")
}

@Test("the same card settling back does not re-arm")
func sameCardSettlingDoesNotRearm() {
    var t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60))

    for _ in 0..<12 {
        let nudged = spot.offsetBy(dx: 0.004, dy: 0.004)
        _ = t.observe(sample([nudged], frame, faces: [face(62)]))
    }
    #expect(t.phase == .hold, "the same card must not re-arm on noise")
}

@Test("a card removed reports a different cause than a card replaced")
func removedIsDistinctFromReplaced() {
    var t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60))

    for _ in 0..<Trigger().tuning.rearmSamples { _ = t.observe(sample([], frame)) }
    #expect(t.rearmCause == .removed, "an empty frame is a removal")
}

@Test("the parent's nudge is marked as having no evidence behind it")
func nudgeIsMarkedAsSuch() {
    var t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60))

    t.forceRearm()
    #expect(t.rearmCause == .nudged)
    #expect(t.phase == .searching)
}

@Test("a sample carrying no box signatures keeps the old behaviour")
func missingFacesFallBackToGeometry() {
    var t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame)) }
    t.captureFinished(scene: frame)
    for _ in 0..<12 { _ = t.observe(sample([spot], frame)) }
    #expect(t.phase == .hold, "geometry-only must still park on a present card")
}

@Test("a motionless card is not replaced, however the detector box wanders")
func jitteringBoxIsNotACardSwap() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)
    #expect(t.phase == .hold)

    for i in 0..<40 {
        let drift = CGFloat(i % 5) * 0.01 - 0.02
        let wandering = spot.offsetBy(dx: drift, dy: -drift)
        let ev = t.observe(sample([wandering], frame, faces: [face(60)]))
        #expect(ev != .fire, "a still card fired again at sample \(i)")
    }
    #expect(t.phase == .hold, "the machine left hold with no card having moved")
    #expect(t.rearmCause == .none, "nothing happened, so nothing caused a rearm")
}

@Test("the held window still catches a card swapped into the same spot")
func heldWindowStillSeesASwap() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)

    var rearmed = false
    for _ in 0..<12 {
        _ = t.observe(sample([spot], frame, faces: [face(160)]))
        if t.rearmCause != .none { rearmed = true; break }
    }
    #expect(rearmed, "a card laid over another must re-arm, so it can be scanned")
    #expect(t.rearmCause == .replaced, "it held the spot, so it was replaced")
}

private func sample(
    _ boxes: [CGRect], _ scene: SceneSignature,
    held: SceneSignature, boxFaces: [SceneSignature]
) -> TriggerSample {
    TriggerSample(boxes: boxes, scene: scene, holdScene: held, boxScenes: boxFaces)
}

@Test("a card that slides is moved, not replaced")
func slidingCardIsMovedNotReplaced() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)

    let slid = spot.offsetBy(dx: 0.05, dy: 0.03)
    var rearmed = false
    for _ in 0..<12 {
        _ = t.observe(sample([slid], frame, held: face(160), boxFaces: [face(60)]))
        if t.rearmCause != .none { rearmed = true; break }
    }
    #expect(rearmed, "the machine should still re-arm — the scene did change")
    #expect(t.rearmCause == .moved,
            "a card that kept its face only moved, cause = \(t.rearmCause)")
}

@Test("a different card in the same place is still replaced")
func differentCardStillReplaced() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)

    var rearmed = false
    for _ in 0..<12 {
        _ = t.observe(sample([spot], frame, held: face(160), boxFaces: [face(160)]))
        if t.rearmCause != .none { rearmed = true; break }
    }
    #expect(rearmed, "a card laid over another must re-arm, so it can be scanned")
    #expect(t.rearmCause == .replaced, "a new face is a new card, cause = \(t.rearmCause)")
}

@Test("detector jitter on a moved card does not make it a new one")
func jitterStaysWithinTheMovedBand() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)

    let slid = spot.offsetBy(dx: 0.05, dy: 0.03)
    for _ in 0..<12 {
        _ = t.observe(sample([slid], frame, held: face(160), boxFaces: [face(74)]))
        if t.rearmCause != .none { break }
    }
    #expect(t.rearmCause == .moved,
            "a 14-level face difference is jitter, cause = \(t.rearmCause)")
}

@Test("without per-box faces the machine keeps its old answer")
func noBoxFacesKeepsOldBehaviour() {
    var t = Trigger()
    let frame = face(100)
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60), cardBox: spot)

    for _ in 0..<12 {
        _ = t.observe(sample([spot], frame, faces: [face(160)]))
        if t.rearmCause != .none { break }
    }
    #expect(t.rearmCause == .replaced, "cause = \(t.rearmCause)")
}

@Test("a removal reports no face measurement rather than a stale one")
func removalClearsTheFaceDelta() {
    let t = Trigger()
    let spot = CGRect(x: 0.3, y: 0.25, width: 0.4, height: 0.5)
    let frame = face(100)

    t.arm(with: sample([], frame))
    for _ in 0..<8 { _ = t.observe(sample([spot], frame, faces: [face(60)])) }
    t.captureFinished(scene: frame, cardScene: face(60))
    #expect(t.phase == .hold)

    _ = t.observe(TriggerSample(boxes: [spot], scene: frame,
                                holdScene: face(200), boxScenes: [face(200)]))
    #expect(t.snapshot.faceDelta != nil, "the occupied branch must measure")

    _ = t.observe(TriggerSample(boxes: [], scene: frame, holdScene: face(200)))
    #expect(t.rearmCause == .none, "setup: must not have re-armed yet")
    #expect(t.snapshot.faceDelta == nil,
            "an empty frame reported face=\(String(describing: t.snapshot.faceDelta))")
}
