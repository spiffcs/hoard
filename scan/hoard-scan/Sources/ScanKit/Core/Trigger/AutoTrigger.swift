import CoreGraphics
import Foundation

/// AutoTrigger decides when a framed card has settled enough to shutter without
/// a keypress. It is deliberately camera-free — rectangle boxes in, fire/phase
/// callbacks out — so the state machine can be reasoned about (and traced)
/// apart from AVFoundation.
///
/// Every method must be called on the main thread; the controller hops there
/// after Vision finishes on its analysis queue, which keeps the machine
/// lock-free alongside the capture path (already main-thread).
final class AutoTrigger {
    enum Phase: String {
        case off, searching, stabilizing, capturing, hold
    }

    private(set) var phase: Phase = .off
    /// fire is called exactly once per SEARCHING→…→CAPTURING pass.
    var onFire: (() -> Void)?
    /// onPhase is called on every transition, for the preview overlay and the
    /// wire events.
    var onPhase: ((Phase) -> Void)?
    /// onBoxes is called every sample with the rectangles the trigger is
    /// actually considering — new-since-armed, background excluded — so the
    /// preview outline shows candidates, not the desk.
    var onBoxes: (([CGRect]) -> Void)?

    /// The scene signature is the candidate boxes sorted largest-first: stable
    /// enough to compare across samples, cheap enough to compare at 4 Hz.
    private var prevSig: [CGRect] = []
    private var lastNovel: [CGRect] = []
    private var heldSig: [CGRect] = []
    private var stableCount = 0
    private var graceCount = 0
    private var disruptCount = 0
    /// When the current stabilize pass began, so a fire can report how long
    /// the machine (not the human) took to settle on the card.
    private var stabilizeBegan: Date?
    /// When a completed streak first deferred its fire to a focus hunt; the
    /// TriggerTuning.focusWait valve fires anyway once this is old enough.
    private var fireDeferredAt: Date?
    /// Rectangles that are furniture, not cards: whatever was in frame when
    /// auto armed (a desk has notepads and coasters — rectangles all), plus
    /// anything that fired and then photographed as no-card. Only a rectangle
    /// not in this set can arm the trigger.
    private var background: [CGRect] = []
    private var needBaseline = true
    /// The frame signature from the previous sample, and the one taken when we
    /// last fired. Together they answer "has the picture stopped moving" and
    /// "is it a different picture from the one we already photographed".
    private var prevScene: [UInt8] = []
    private var capturedScene: [UInt8] = []
    private var stillCount = 0
    /// How much of the current streak came from detector blinks rather than
    /// real detections. Caps how far stillness alone may carry a pass.
    private var blinkCount = 0
    /// Stabilization passes abandoned since the last capture. A pass that
    /// starts and dies means something looked like a candidate and could not
    /// sustain it, which is what an absorbed card does to the trigger.
    private var abandonedPasses = 0

    func setEnabled(_ on: Bool) {
        if on {
            guard phase == .off else { return }
            stableCount = 0
            disruptCount = 0
            prevSig = []
            heldSig = []
            background = []
            needBaseline = true
            abandonedPasses = 0
            prevScene = []
            capturedScene = []
            stillCount = 0
            fireDeferredAt = nil
            move(to: .searching)
        } else {
            guard phase != .off else { return }
            move(to: .off)
        }
    }

    /// observe feeds one sampled frame's detected rectangles through the
    /// machine. focusSettled is the camera's word that the lens is not mid-
    /// hunt: a hunt blurs edges, so whatever the detector reports during one
    /// is noise — the machine freezes rather than mistaking blur for motion,
    /// and never fires into it (a capture mid-hunt is the out-of-focus scan).
    /// observe feeds one sampled frame's detected rectangles through the
    /// machine. focusSettled is the camera's word that the lens is not mid-
    /// hunt: a hunt blurs edges, so whatever the detector reports during one
    /// is noise — the machine freezes rather than mistaking blur for motion,
    /// and never fires into it (a capture mid-hunt is the out-of-focus scan).
    func observe(_ boxes: [CGRect], scene: [UInt8] = [], focusSettled: Bool = true) {
        let sig = boxes.sorted { $0.width * $0.height > $1.width * $1.height }
        if phase == .off {
            onBoxes?([])
            return
        }
        if needBaseline {
            background = sig
            needBaseline = false
            autoDebug("baseline: \(sig.count) background rect(s)")
        }
        let novel = candidates(in: sig)
        traceSample(sig, novel)
        lastNovel = novel
        onBoxes?(novel)
        // Whether the picture itself moved, decided before anything updates
        // the remembered frame. Both the fallback path and the dropout rule
        // below read it, so it is computed once, here.
        let sceneStill = !scene.isEmpty && !prevScene.isEmpty
            && sceneDelta(prevScene, scene) <= TriggerTuning.stillDelta
        // The stillness path, run before the rectangle machine so a card the
        // detector cannot hold still gets photographed. It is a floor, not a
        // replacement: whenever rectangles work they fire first and this never
        // reaches its count.
        let firedOnStillness = trackStillness(scene, still: sceneStill, focusSettled: focusSettled)
        if !scene.isEmpty { prevScene = scene }
        if firedOnStillness { return }
        switch phase {
        case .off, .capturing:
            return
        case .searching:
            beginPass(on: novel)
        case .stabilizing:
            advancePass(novel: novel, scene: scene,
                        sceneStill: sceneStill, focusSettled: focusSettled)
        case .hold:
            holdOn(novel: novel, focusSettled: focusSettled)
        }
    }

    /// candidates filters the desk out of a sample. Only a rectangle the
    /// baseline does not already explain can arm the trigger.
    private func candidates(in sig: [CGRect]) -> [CGRect] {
        sig.filter { b in
            !background.contains { iou($0, b) >= TriggerTuning.backgroundIoU }
        }
    }

    /// Per-sample firehose for diagnosing a card the trigger won't see: every
    /// sample's raw and candidate counts, with the largest box's size, gated
    /// behind its own env so ordinary traces stay readable.
    private func traceSample(_ sig: [CGRect], _ novel: [CGRect]) {
        guard ProcessInfo.processInfo.environment["HOARD_SCAN_AUTO_TRACE"] != nil else { return }
        let biggest = sig.first.map {
            String(format: "%.2fx%.2f", $0.width, $0.height)
        } ?? "-"
        autoDebug("sample \(phase.rawValue): rects=\(sig.count) novel=\(novel.count) biggest=\(biggest)")
    }

    /// countStillSample records one more sample of proven stillness and pulls
    /// the trigger when that completes the streak, reporting whether it fired.
    ///
    /// Four kinds of evidence reach here — a plain match, a fragment of a known
    /// box, a box that grew back into the whole card, and a detector blink over
    /// a still picture. They differ in what they prove, which is why each names
    /// itself in the trace; what they share is the accounting, and having it in
    /// one place is what keeps the streak, the grace reset and the fire check
    /// from drifting apart between them.
    @discardableResult
    private func countStillSample(_ why: @autoclosure () -> String) -> Bool {
        graceCount = 0
        stableCount += 1
        autoDebug(why())
        guard stableCount >= TriggerTuning.stableSamples else { return false }
        maybeFire(focusSettled: true)
        return true
    }

    /// beginPass starts stabilizing on the first sample showing a card the desk
    /// baseline does not explain.
    private func beginPass(on novel: [CGRect]) {
        guard !novel.isEmpty else { return }
        prevSig = novel
        stableCount = 1
        graceCount = 0
        blinkCount = 0
        stabilizeBegan = Date()
        move(to: .stabilizing)
    }

    /// advancePass judges one sample against the card the pass is watching:
    /// more evidence of stillness, tolerable flicker, or real motion.
    private func advancePass(novel: [CGRect], scene: [UInt8],
                             sceneStill: Bool, focusSettled: Bool) {
        // A focus hunt freezes the machine outright: no streak growth (a
        // blurred frame is not evidence of stillness), no grace burn or
        // reset (its jitter is not evidence of motion). A streak that
        // completed before the hunt fires the moment it ends — or when
        // the wait valve expires, so a wedged observation can't park us.
        if !focusSettled {
            if stableCount >= TriggerTuning.stableSamples {
                maybeFire(focusSettled: false)
            }
            return
        }
        // A bad sample — the detector missed the card, or its box jittered
        // past the IoU bar — is tolerated a few times with the streak
        // frozen: Vision flickers on foils and borderless frames. Only a
        // sustained miss (card gone) or sustained mismatch (hand still
        // moving) restarts anything.
        if novel.isEmpty {
            countBlinkOrBurnGrace(scene: scene, sceneStill: sceneStill)
            return
        }
        if fragmentsOf(prevSig, novel) {
            // Borderless art crumbles under the detector: a sample often
            // returns a high-contrast SLIVER of the very card it found
            // whole a beat earlier. A fragment inside the known box is
            // evidence of stillness, not motion — count it toward the
            // streak, but keep the remembered box at full size.
            countStillSample("fragment counted, stable \(stableCount)/\(TriggerTuning.stableSamples)")
            return
        }
        // The same relation the other way round. fragmentsOf only asked
        // whether the new boxes sit inside the remembered ones, so once
        // the streak had latched onto a sliver, the card reappearing whole
        // was not "inside" it and read as motion — the streak reset at the
        // exact moment the detector finally got it right. Live: a
        // motionless Flare of Cultivation alternated between 0.37x0.88 and
        // slivers as small as 0.08x0.13, and took 3,867ms to settle.
        //
        // A box that contains what we were watching is the detector
        // finding *more* of the same still card, which is better evidence,
        // not worse. Count it, and grow the remembered box so the streak
        // continues from the fuller read rather than the sliver.
        // Gated on the picture as well as the geometry: a hand sweeping in
        // also produces a box that contains what we were watching, and
        // that is motion, not a better look at a still card. Requiring the
        // frame to be unchanged separates the two for free.
        if sceneStill, fragmentsOf(novel, prevSig) {
            prevSig = novel
            countStillSample("card seen whole again, stable \(stableCount)/\(TriggerTuning.stableSamples)")
            return
        }
        if !matches(prevSig, novel) {
            burnGrace(movedTo: novel)
            return
        }
        // The remembered box is only advanced on a sample that did not fire,
        // so a capture is always taken against the geometry that earned it.
        if countStillSample("stable \(stableCount)/\(TriggerTuning.stableSamples), \(novel.count) candidate(s)") {
            return
        }
        prevSig = novel
    }

    /// countBlinkOrBurnGrace handles a sample where the detector returned
    /// nothing at all.
    ///
    /// An empty sample is not evidence the card moved — it is evidence the
    /// detector blinked, and it blinks constantly: 220 of 522 stabilizing
    /// samples in one live session returned no rectangle at all while a card
    /// sat motionless in frame. Treating each of those as a bad sample burned
    /// grace and restarted the streak, which is why settle ran at more than
    /// twice its floor.
    ///
    /// The pixels settle it. If the picture has not changed since the last
    /// sample, nothing moved, so the miss was the detector and the card is
    /// still exactly where it was — count it toward the streak. This is the
    /// same argument the fragment rule already makes, with better evidence:
    /// frame-to-frame stillness is a stronger proof that a card is holding
    /// still than a box happening to land twice in the same place.
    ///
    /// Guarded hard, because the first cut of this was a disaster: 82% of
    /// captures read nothing. A spurious box puts the trigger in stabilizing,
    /// every later sample is empty, the desk is perfectly still — and it
    /// counted its way to a shutter on nothing, over and over. Stillness alone
    /// says the picture is not moving; it does not say a card is there.
    ///
    /// So the blink only counts when the rest of the evidence already agrees:
    /// the detector really saw the card at least twice, the middle of the frame
    /// has something in it, the scene differs from the one we last
    /// photographed, and at most half the streak may be made of blinks.
    /// Anything less and it is the old grace path, which is the safe direction.
    private func countBlinkOrBurnGrace(scene: [UInt8], sceneStill: Bool) {
        let realDetections = stableCount - blinkCount
        if sceneStill, realDetections >= 2, blinkCount < TriggerTuning.stableSamples / 2,
            sceneDetail(scene) >= TriggerTuning.sceneDetail,
            sceneDelta(capturedScene, scene) >= TriggerTuning.sceneChanged {
            blinkCount += 1
            countStillSample("detector blinked on a still scene, stable "
                + "\(stableCount)/\(TriggerTuning.stableSamples)")
            return
        }
        graceCount += 1
        if graceCount > TriggerTuning.graceSamples {
            fireDeferredAt = nil
            abandonPass()
            move(to: .searching)
        }
    }

    /// burnGrace spends one sample of tolerance on boxes that no longer match
    /// what the pass is watching, and restarts the streak once the tolerance is
    /// gone — real hand motion fails sample after sample and still resets.
    private func burnGrace(movedTo novel: [CGRect]) {
        graceCount += 1
        guard graceCount > TriggerTuning.graceSamples else {
            autoDebug("flicker tolerated \(graceCount)/\(TriggerTuning.graceSamples)")
            return
        }
        autoDebug("scene moved, streak reset (\(novel.count) candidate(s))")
        stableCount = 1
        graceCount = 0
        blinkCount = 0
        fireDeferredAt = nil
        prevSig = novel
    }

    /// holdOn parks on the card just photographed, and decides when the scene
    /// has changed enough to go looking again.
    ///
    /// The held card flickers like any hard card: a blink of empty detection is
    /// not a removal, and a jittered-but-overlapping box is not a swap. What
    /// re-arms is accumulated DISRUPTION of either kind — occlusion and box
    /// motion pool into one counter, because a hand placing the next card on
    /// top of the pile (stacking is a supported rhythm, not a mistake)
    /// alternates between the two.
    ///
    /// Calm samples DECAY the counter rather than zeroing it: live traces
    /// showed placement disruption arriving in 1–2 sample bursts with settled
    /// samples interleaved, sawing a hard-reset counter between 1 and 2 forever
    /// while the user reached for the spacebar. An isolated blink still dies to
    /// the decay; a real placement out-accumulates it. After the re-arm, the
    /// new top card fires even though it sits exactly where the last one did —
    /// novelty is judged against the desk baseline, never against the card just
    /// shot.
    private func holdOn(novel: [CGRect], focusSettled: Bool) {
        // A hunt's blur says nothing about the scene: freeze the counter
        // both ways. Counting it as disruption would re-arm on pure blur
        // — a refire on the very card just shot.
        if !focusSettled { return }
        if !novel.isEmpty && holdMatches(heldSig, novel) {
            if disruptCount > 0 {
                disruptCount -= 1
            }
            return
        }
        disruptCount += 1
        autoDebug("disrupted \(disruptCount)/\(TriggerTuning.rearmSamples)")
        guard disruptCount >= TriggerTuning.rearmSamples else { return }
        stableCount = 0
        prevSig = novel
        nudged = false // the scene really changed; fires announce again
        move(to: .searching)
    }

    /// maybeFire pulls the trigger when the lens agrees, defers when it is
    /// mid-hunt, and gives up deferring once the wait valve expires.
    /// trackStillness advances the pixel-stillness path and reports whether it
    /// fired.
    ///
    /// Three things must hold together, and each one is load-bearing:
    ///
    ///   still — consecutive frames are the same picture, so nothing is moving
    ///   changed — the picture differs from the one we last captured, so a
    ///     scene nobody has touched cannot photograph itself forever
    ///   detail — the middle of the frame has structure, so lifting a card away
    ///     leaves something changed and still that is nevertheless bare desk
    ///
    /// Without the third the shutter would fire every time a card was removed.
    private func trackStillness(_ scene: [UInt8], still: Bool, focusSettled: Bool) -> Bool {
        guard TriggerTuning.stillSamples > 0, !scene.isEmpty else { return false }
        guard phase == .searching || phase == .stabilizing else {
            stillCount = 0
            return false
        }
        // Only once rectangles have actually been failing. A detector that is
        // holding the card will fire first and better; pre-empting it is how
        // this path spent two thirds of its shutters on nothing.
        guard abandonedPasses >= TriggerTuning.stillAfterPasses else {
            stillCount = 0
            return false
        }
        // Blur reads as stillness — every edge softens and stops moving — so a
        // focus hunt must not accumulate evidence here either.
        guard focusSettled else { return false }
        guard still else {
            stillCount = 0
            return false
        }
        stillCount += 1
        guard stillCount >= TriggerTuning.stillSamples else { return false }
        guard sceneDetail(scene) >= TriggerTuning.sceneDetail else { return false }
        guard sceneDelta(capturedScene, scene) >= TriggerTuning.sceneChanged else { return false }
        autoDebug("still for \(stillCount) samples, firing without a rectangle "
            + "(\(lastNovel.count) candidate(s))")
        stillCount = 0
        fire()
        return true
    }

    private func maybeFire(focusSettled: Bool) {
        if focusSettled {
            fire()
            return
        }
        guard let since = fireDeferredAt else {
            fireDeferredAt = Date()
            autoDebug("streak complete, waiting out a focus hunt")
            return
        }
        if Date().timeIntervalSince(since) >= TriggerTuning.focusWait {
            autoDebug("focus never settled in \(Int(TriggerTuning.focusWait * 1000))ms, firing anyway")
            fire()
        }
    }

    /// fire is the one auto-shutter path: reports how long the machine took
    /// to settle on the card, then moves to capturing and pulls the trigger.
    private func fire() {
        if let t = stabilizeBegan {
            timing("settle=\(msSince(t))ms")
            stabilizeBegan = nil
        }
        fireDeferredAt = nil
        abandonedPasses = 0
        // The picture we are about to photograph. Until the scene differs from
        // it, the stillness path has nothing new to shoot.
        capturedScene = prevScene
        stillCount = 0
        move(to: .capturing)
        onFire?()
    }

    /// abandonPass records a stabilization pass that started and died, and
    /// condemns the background baseline once that has happened enough times in
    /// a row.
    ///
    /// The baseline is learned once, from whatever sat in frame the instant
    /// auto armed, and is never re-learned — so a card already on the desk at
    /// that moment becomes furniture for the rest of the session, invisible at
    /// exactly the spot every card lands. Live: `baseline: 1 background
    /// rect(s)`, then 46 seconds of the detector finding the card and the
    /// filter deleting it, ending only when the user physically lifted the
    /// card and put it back far enough off the learned box to read as novel.
    ///
    /// Repeated abandoned passes are the tell, and a better one than "every
    /// rectangle was swallowed": an idle desk whose real furniture is
    /// correctly absorbed never enters stabilizing at all, so it cannot
    /// trigger this. Only a scene that keeps *almost* producing a candidate
    /// can, which is precisely what a half-absorbed card does.
    ///
    /// It only ever forgets. Nothing is added to the baseline at runtime —
    /// that is the memory that once killed auto capture at the exact spot
    /// every card lands, and clearing is the safe direction: the worst case is
    /// one wasted capture on real furniture, after which HOLD parks on it.
    private func abandonPass() {
        abandonedPasses += 1
        guard abandonedPasses >= TriggerTuning.backgroundResetPasses, !background.isEmpty else { return }
        autoDebug("\(abandonedPasses) passes abandoned with nothing captured, "
            + "clearing the \(background.count)-rect background baseline")
        background = []
        abandonedPasses = 0
    }

    /// captureBegan holds the machine while any capture — auto or the space
    /// key — is in flight, so a manual shutter in auto mode can't double-fire.
    func captureBegan() {
        guard phase != .off else { return }
        if phase != .capturing { move(to: .capturing) }
    }

    /// nudged marks that the current arming came from the parent's rearm
    /// nudge rather than the scene changing — the fire it produces is a quiet
    /// recheck, not a capture worth announcing.
    private(set) var nudged = false

    /// forceRearm is the parent's content-aware nudge: geometry cannot tell a
    /// card stacked squarely on the pile from the card just shot, but the
    /// parent knows what it already processed — it re-arms, the scene fires,
    /// and an identical read is its to discard.
    func forceRearm() {
        guard phase == .hold else { return }
        stableCount = 0
        disruptCount = 0
        prevSig = []
        nudged = true
        autoDebug("rearm nudge from parent")
        move(to: .searching)
    }

    /// captureFinished parks the machine on the candidates it just shot; only
    /// a changed scene (card swapped, removed, or stacked over) re-arms it.
    ///
    /// A shot that reads as no card is deliberately NOT learned as background.
    /// That rule existed to silence furniture that fired once — but telemetry
    /// showed it absorbing the scanning pile itself after one glared empty
    /// read, killing auto capture at the exact spot every subsequent card
    /// lands on. HOLD already stops a no-card rectangle from re-firing until
    /// it moves, which is all the protection furniture needs.
    func captureFinished() {
        guard phase != .off else { return }
        heldSig = lastNovel
        disruptCount = 0
        nudged = false
        move(to: .hold)
    }

    private func move(to next: Phase) {
        guard next != phase else { return }
        autoDebug("\(phase.rawValue) → \(next.rawValue)")
        phase = next
        onPhase?(next)
    }

    private func matches(_ a: [CGRect], _ b: [CGRect]) -> Bool {
        guard a.count == b.count else { return false }
        for (x, y) in zip(a, b) where iou(x, y) < TriggerTuning.iou { return false }
        return true
    }

    /// holdMatches is the forgiving variant for the parked phase: the shot
    /// card's box wobbles as exposure hunts over foil, and holding it only
    /// needs "still roughly the same rectangle", not stillness.
    private func holdMatches(_ a: [CGRect], _ b: [CGRect]) -> Bool {
        guard a.count == b.count else { return false }
        for (x, y) in zip(a, b) where iou(x, y) < TriggerTuning.backgroundIoU { return false }
        return true
    }

    /// fragmentsOf reports whether every current box sits (almost) inside some
    /// remembered box — the crumbled detection a borderless card produces
    /// while sitting perfectly still.
    private func fragmentsOf(_ prev: [CGRect], _ cur: [CGRect]) -> Bool {
        guard !prev.isEmpty, !cur.isEmpty else { return false }
        return cur.allSatisfy { c in
            prev.contains { p in
                let inter = p.intersection(c)
                guard !inter.isNull, !inter.isEmpty else { return false }
                let area = c.width * c.height
                return area > 0 && (inter.width * inter.height) / area >= 0.8
            }
        }
    }

    private func iou(_ a: CGRect, _ b: CGRect) -> CGFloat {
        let inter = a.intersection(b)
        if inter.isNull || inter.isEmpty { return 0 }
        let i = inter.width * inter.height
        let u = a.width * a.height + b.width * b.height - i
        return u > 0 ? i / u : 0
    }
}
