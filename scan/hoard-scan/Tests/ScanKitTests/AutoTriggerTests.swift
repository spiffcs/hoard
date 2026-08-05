// The auto-capture trigger's state machine.
//
// This is the one subsystem that had no offline test at all. `make scan-check`
// replays still frames through the parser and scan/corpus scores frame eras;
// neither can drive a machine whose whole behaviour lives in the *sequence* of
// samples. Every case below is a rule docs/scanner-tuning.md paid for with a
// live session, and the comment on each says what it cost — so a future tuning
// pass has to argue with the evidence rather than rediscover it.
//
// The knobs are referenced by name, never inlined, so retuning a default moves
// these tests with it instead of breaking them.

import CoreGraphics
import Testing

@testable import ScanKit

/// A card-shaped box somewhere in frame, in the normalized coordinates Vision
/// reports and the trigger consumes.
private let card = CGRect(x: 0.30, y: 0.30, width: 0.40, height: 0.55)

/// The same card as the detector often actually sees it on borderless art: a
/// high-contrast sliver of the real thing, wholly inside the true box.
private let sliver = CGRect(x: 0.35, y: 0.35, width: 0.10, height: 0.12)

/// Far enough away to share no meaningful overlap — a card that genuinely moved.
private let moved = card.offsetBy(dx: 0.35, dy: 0)

/// A frame signature with plenty of spread, so sceneDetail reads it as "there
/// is something here" rather than bare desk.
private func detailedScene() -> [UInt8] {
    (0..<(sceneGridW * sceneGridH)).map { $0 % 2 == 0 ? 0 : 255 }
}

/// Drives a trigger and records what it told its owner, so the tests can assert
/// on fires and phases rather than reaching into private state.
private final class Rig {
    let trigger = AutoTrigger()
    private(set) var fires = 0
    private(set) var phases: [AutoTrigger.Phase] = []

    init() {
        trigger.onFire = { [self] in fires += 1 }
        trigger.onPhase = { [self] p in phases.append(p) }
    }

    /// Arms the machine and feeds it one sample, which is what teaches it the
    /// background baseline. Anything passed here is furniture for the session.
    func arm(furniture: [CGRect] = []) {
        trigger.setEnabled(true)
        trigger.observe(furniture)
    }

    func observe(_ boxes: [CGRect], times: Int = 1, scene: [UInt8] = []) {
        for _ in 0..<times { trigger.observe(boxes, scene: scene) }
    }
}

@Test("a card set down and held still fires after exactly the full streak")
func stillCardFiresOnceTheStreakCompletes() {
    let rig = Rig()
    rig.arm()
    for sample in 1..<TriggerTuning.stableSamples {
        rig.observe([card])
        #expect(rig.fires == 0, "fired after only \(sample) still sample(s)")
    }
    rig.observe([card])
    #expect(rig.fires == 1)
    #expect(rig.trigger.phase == .capturing)
}

@Test("whatever was in frame when auto armed can never fire")
func furnitureNeverFires() {
    // A desk is full of rectangles — a notepad, a mousepad, a coaster. They get
    // no outline and no shutter, however long they sit there.
    let rig = Rig()
    rig.arm(furniture: [card])
    rig.observe([card], times: TriggerTuning.stableSamples * 4)
    #expect(rig.fires == 0)
    #expect(rig.trigger.phase == .searching)
}

@Test("grace freezes the streak rather than feeding or resetting it")
func graceFreezesTheStreak() {
    // Cutting AUTO_STABLE 6 → 4 to chase latency moved settle 8% and cost five
    // points of accuracy: settle is bound by how often a streak is *abandoned*,
    // not by how long it is. Grace is what stops a blinking detector abandoning
    // one, so it must neither advance the streak (a blank sample is not
    // evidence of stillness) nor reset it (nor is it evidence of motion).
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: 3)
    rig.observe([], times: TriggerTuning.graceSamples)  // dropouts, exactly at the bar
    #expect(rig.fires == 0)
    #expect(rig.trigger.phase == .stabilizing, "grace must not abandon the pass")

    // The streak resumes from three, so three more still samples finish it —
    // proof the dropouts neither counted nor cost anything.
    rig.observe([card], times: TriggerTuning.stableSamples - 3 - 1)
    #expect(rig.fires == 0)
    rig.observe([card])
    #expect(rig.fires == 1)
}

@Test("a dropout longer than grace abandons the pass")
func sustainedDropoutAbandonsThePass() {
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: 2)
    rig.observe([], times: TriggerTuning.graceSamples + 1)
    #expect(rig.fires == 0)
    #expect(rig.trigger.phase == .searching)
}

@Test("a hand still moving resets the streak instead of finishing it")
func realMotionResetsTheStreak() {
    // This is what keeps motion blur out of the captures: a hand that has not
    // let go jitters the detected bounds and never accumulates the streak.
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: TriggerTuning.stableSamples - 1)  // one sample short
    rig.observe([moved], times: TriggerTuning.graceSamples + 1)  // outlasts grace, resets
    #expect(rig.fires == 0)

    // Restarted at one, so it owes a full streak from there.
    rig.observe([moved], times: TriggerTuning.stableSamples - 2)
    #expect(rig.fires == 0)
    rig.observe([moved])
    #expect(rig.fires == 1)
}

@Test("a card seen only as a sliver still counts as holding still")
func fragmentsCountTowardTheStreak() {
    // Borderless art crumbles under the detector: a motionless Flare of
    // Cultivation alternated between 0.37x0.88 and slivers as small as
    // 0.08x0.13, and took 3,867ms to settle before fragments counted.
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: 2)
    rig.observe([sliver], times: TriggerTuning.stableSamples - 2)
    #expect(rig.fires == 1)
}

@Test("the card reappearing whole is better evidence, not motion")
func wholeCardAfterASliverAlsoCounts() {
    // The symmetric half of the fragment rule, and the reason it had to be
    // symmetric: fragmentsOf only asked whether the new boxes sit inside the
    // remembered one, so a streak that had latched onto a sliver treated the
    // card reappearing *whole* as motion and reset at the exact moment the
    // detector finally got it right.
    let scene = detailedScene()
    let rig = Rig()
    rig.arm()
    rig.observe([sliver], times: 2, scene: scene)  // streak latches onto a sliver
    rig.observe([card], times: TriggerTuning.stableSamples - 2, scene: scene)
    #expect(rig.fires == 1)
}

@Test("a card growing back into view is only stillness if the picture agrees")
func growingBoxWithoutASteadyPictureIsMotion() {
    // The gate on the rule above. A hand sweeping in also produces a box that
    // contains what we were watching, and that is motion, not a better look at
    // a still card — so without a steady picture the growth must not count.
    let rig = Rig()
    rig.arm()
    rig.observe([sliver], times: 2)  // no scene: the picture cannot vouch
    rig.observe([card], times: TriggerTuning.stableSamples - 2)
    #expect(rig.fires == 0)
}

@Test("the card just photographed does not fire again while it sits there")
func holdDoesNotRefireOnTheCardJustShot() {
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: TriggerTuning.stableSamples)
    #expect(rig.fires == 1)
    rig.trigger.captureFinished()
    #expect(rig.trigger.phase == .hold)
    rig.observe([card], times: TriggerTuning.stableSamples * 4)
    #expect(rig.fires == 1, "auto-exposure flicker on a parked card refired")
}

@Test("hold re-arms only once disruption accumulates")
func holdRearmsOnSustainedDisruption() {
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: TriggerTuning.stableSamples)
    rig.trigger.captureFinished()
    rig.observe([], times: TriggerTuning.rearmSamples - 1)
    #expect(rig.trigger.phase == .hold)
    rig.observe([])
    #expect(rig.trigger.phase == .searching)
}

@Test("calm samples decay the disruption counter instead of zeroing it")
func calmSamplesDecayRatherThanReset() {
    // Separate empty/moved counters reset each other and parked the trigger.
    // One pooled counter that *decays* survives placement disruption arriving
    // in 1-2 sample bursts with settled samples interleaved, while an isolated
    // blink still dies — which is the case this pins.
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: TriggerTuning.stableSamples)
    rig.trigger.captureFinished()
    for _ in 0..<(TriggerTuning.rearmSamples * 3) {
        rig.observe([])      // one disrupted sample
        rig.observe([card])  // one calm sample, which decays it again
    }
    #expect(rig.trigger.phase == .hold)
}

@Test("the parent's nudge re-arms from hold and marks the fire quiet")
func forceRearmOnlyAppliesInHold() {
    let rig = Rig()
    rig.arm()
    rig.trigger.forceRearm()
    #expect(rig.trigger.nudged == false, "a nudge outside hold must do nothing")

    rig.observe([card], times: TriggerTuning.stableSamples)
    rig.trigger.captureFinished()
    rig.trigger.forceRearm()
    #expect(rig.trigger.phase == .searching)
    #expect(rig.trigger.nudged)
}

@Test("a disabled trigger ignores everything")
func disabledTriggerIgnoresSamples() {
    let rig = Rig()
    rig.observe([card], times: TriggerTuning.stableSamples * 2)
    #expect(rig.fires == 0)
    #expect(rig.trigger.phase == .off)
}

@Test("the stillness path stays out of the way while rectangles are working")
func stillnessDoesNotPreemptAWorkingDetector() {
    // Run in parallel with a working detector, the pixel-stillness path wasted
    // 64% of its fires against the rectangle path's 7%. It is a floor, not a
    // replacement: it may only fire after passes have actually been failing.
    let scene = detailedScene()
    let rig = Rig()
    rig.arm()
    rig.observe([card], times: TriggerTuning.stableSamples - 1, scene: scene)
    #expect(rig.fires == 0, "stillness fired while the detector was holding the card")
    rig.observe([card], scene: scene)
    #expect(rig.fires == 1)
}
