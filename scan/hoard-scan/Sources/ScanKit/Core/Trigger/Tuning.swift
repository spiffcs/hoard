// The trigger's knobs, all overridable from the environment so a session can
// be tuned without a recompile. The comments are a ledger: most of these look
// like obvious wins in isolation and are not. See docs/scanner-tuning.md.
//
// Grouped into a namespace rather than left loose, so that inside the state
// machine a configured threshold (TriggerTuning.stableSamples) never reads like
// one of the counters beside it (stableCount). Same values, same environment
// variables, same defaults.

import Foundation

/// envDouble reads a numeric tunable from the environment, the same override
/// pattern HOARD_SCAN_WAIT uses, so thresholds can be tuned live without a
/// recompile.
func envDouble(_ name: String, _ fallback: Double) -> Double {
    ProcessInfo.processInfo.environment[name].flatMap(Double.init) ?? fallback
}

/// What the auto-capture trigger is willing to call "a card that has settled".
enum TriggerTuning {
    /// focusControl selects the focus policy: "lock" (continuous AF, frozen after
    /// the first good read — every card sits at the same distance, so the hunt a
    /// landing card provokes is pure cost), "continuous" (AF with the trigger's
    /// hunt-aware fire gate but no lock), or "off" (no focus code at all — the
    /// pre-focus-management behavior, byte for byte).
    static let focusControl = ProcessInfo.processInfo.environment["HOARD_SCAN_FOCUS"] ?? "lock"

    /// focusWait bounds how long a completed stability streak defers its fire
    /// waiting for a focus hunt to end, in case the hunt observation wedges — the
    /// trigger must never park forever on a KVO that stopped arriving.
    static let focusWait = envDouble("HOARD_SCAN_FOCUS_WAIT", 1.5)

    /// How often the auto trigger samples the video stream. Vision's rectangle
    /// detector on a ≤1080p buffer costs a few milliseconds, so 5 Hz is nearly free
    /// and still reacts within a beat of a card being set down.
    // Halved from 0.2: every trigger cost is denominated in samples, so this cuts
    // settle, the HOLD re-arm and the searching dwell mechanically. triggerRects is
    // ~9ms, so 10Hz is about 9% of one core, and captureOutput already throttles by
    // elapsed time and drops late frames.
    static let interval = envDouble("HOARD_SCAN_AUTO_INTERVAL", 0.1)

    /// Consecutive still samples before firing (~0.6 s at the default interval). A
    /// hand still moving jitters the detected bounds and never accumulates this
    /// streak — which is also what keeps motion blur out of the captures.
    // Six at the 0.1s period is 0.6s of proven stillness. Dropping it to four to
    // chase latency was measured and reversed: it moved settle only 8% (1,666ms →
    // 1,536ms) while waste rose 7% → 12%, because settle is not bound by how long
    // the streak is. It is bound by how often the streak is abandoned — see
    // graceSamples.
    static let stableSamples = Int(envDouble("HOARD_SCAN_AUTO_STABLE", 6))

    /// Consecutive changed samples before re-arming after a capture, so
    /// auto-exposure flicker on a card that hasn't moved doesn't refire.
    // (Hold re-arming pools empty and moved samples into one disruption counter,
    // bounded by rearmSamples — see the hold phase for why the kinds must not
    // reset each other.)
    static let rearmSamples = Int(envDouble("HOARD_SCAN_AUTO_REARM", 3))

    /// Two samples "match" when every paired rectangle overlaps at least this much.
    static let iou = envDouble("HOARD_SCAN_AUTO_IOU", 0.65)

    /// Consecutive bad samples (detection dropout or box jitter) tolerated while a
    /// card stabilizes. Vision's rectangle detection flickers on hard cards —
    /// foils, borderless frames, low contrast against the desk (one borderless
    /// card blinked out on a third of all samples) — and without tolerance a
    /// single missed sample restarts the whole stillness streak. Real hand motion
    /// fails sample after sample and still resets.
    // Six, because this is the knob settle actually turns on. 73% of stabilization
    // passes were being abandoned, and the reason is that Vision drops the card in
    // *runs*: measured over one session, 18 of 80 dropout runs during stabilizing
    // lasted longer than three samples, with several running 8-10. Every one of
    // those killed a pass that was already progressing and sent it back to
    // searching to start over.
    //
    // Grace freezes the streak rather than feeding it, so widening it does not
    // weaken the evidence a shutter needs — the streak still requires its full
    // count of genuinely still samples. It only stops giving up on a card that is
    // sitting perfectly still while the detector blinks at it. The cost is that a
    // card actually taken away takes 0.6s rather than 0.3s to let go of.
    static let graceSamples = Int(envDouble("HOARD_SCAN_AUTO_GRACE", 6))

    /// A rectangle overlapping a background rectangle at least this much is that
    /// background rectangle, not a newly placed card.
    static let backgroundIoU = envDouble("HOARD_SCAN_AUTO_BG_IOU", 0.5)

    /// How many stabilization passes may be abandoned back-to-back before the
    /// background baseline is treated as wrong. Measured against a live session:
    /// 58 of 59 captures fired after fewer than 8 abandoned passes and the worst
    /// healthy stretch was 6, while the stall that prompted this sat at 13. Eight
    /// separates them with room on both sides.
    static let backgroundResetPasses = Int(envDouble("HOARD_SCAN_AUTO_BG_RESET", 8))

    /// Abandoned stabilization passes before the stillness path is allowed to fire
    /// at all. It exists for cards the rectangle detector cannot hold, and those
    /// abandon passes by definition; without this gate it ran in parallel with a
    /// working detector and wasted 64% of its fires against the rectangle path's
    /// 7%.
    static let stillAfterPasses = Int(envDouble("HOARD_SCAN_AUTO_STILL_AFTER", 3))

    /// Samples of frame-to-frame stillness before the scene alone may fire the
    /// shutter. Six at the 0.1s period is 0.6s of a motionless picture — the same
    /// evidence the rectangle path used to demand, gathered without needing a
    /// rectangle. Set HOARD_SCAN_AUTO_STILL=0 to disable the path entirely.
    static let stillSamples = Int(envDouble("HOARD_SCAN_AUTO_STILL", 6))

    /// Mean per-cell luma change below which two frames count as the same picture.
    /// Above sensor noise, below a hand moving.
    static let stillDelta = envDouble("HOARD_SCAN_AUTO_STILL_DELTA", 2.5)

    /// How much the picture must differ from the one we last captured before the
    /// stillness path may fire again — what stops a parked scene re-firing.
    static let sceneChanged = envDouble("HOARD_SCAN_AUTO_SCENE_CHANGED", 6.0)

    /// Spread of the middle of the frame below which there is nothing worth
    /// photographing. Bare desk is smooth; a card is not.
    static let sceneDetail = envDouble("HOARD_SCAN_AUTO_SCENE_DETAIL", 12.0)
}

/// How the on-screen cue behaves. Presentation only — nothing here touches what
/// the trigger decides, which is why it is a separate namespace from the knobs
/// that do.
enum OutlineTuning {
    /// How long the bracket survives a detector blink, and how long it takes to
    /// glide between positions.
    static let holdSeconds = envDouble("HOARD_SCAN_OUTLINE_HOLD", 0.5)
    static let easeSeconds = envDouble("HOARD_SCAN_OUTLINE_EASE", 0.12)
}
