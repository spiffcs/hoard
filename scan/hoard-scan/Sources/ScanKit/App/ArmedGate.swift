import Foundation
import os

/// ArmedGate carries one bit — is the auto trigger armed — from the main thread
/// to the video tap's analysis queue.
///
/// It exists because of a genuine ordering problem rather than a stylistic one.
/// `AutoTrigger` is main-thread-confined by design, and that confinement is
/// what lets the whole state machine run lock-free (see its doc comment). The
/// video tap, however, runs on its own serial queue and has to decide whether
/// to do Vision work *before* hopping to main — hopping first would pay the
/// ~9ms of rectangle detection on every delivered frame, which is precisely
/// what the throttle exists to avoid.
///
/// So the tap needs to read one fact the main thread owns. Reading
/// `autoTrigger.phase` across the queue boundary was the obvious way and it was
/// a data race: main mutates the phase inside `move(to:)` while the analysis
/// queue reads it. Copying just this bit under a lock keeps the tap cheap and
/// leaves the state machine's invariant intact — nothing reaches into the
/// trigger from another thread.
///
/// One bit rather than the phase itself, deliberately: the tap only ever needs
/// "is there any point sampling", and every other distinction the phase draws
/// belongs to the machine that owns it.
final class ArmedGate {
    private let armed = OSAllocatedUnfairLock(initialState: false)

    /// Read from the analysis queue, once per delivered frame.
    var isArmed: Bool { armed.withLock { $0 } }

    /// Written from the main thread on every trigger transition.
    func set(_ value: Bool) { armed.withLock { $0 = value } }
}
