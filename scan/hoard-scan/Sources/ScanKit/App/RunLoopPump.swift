// Waiting for a phone to admit it exists.
//
// Foundation and a deadline: no camera, no window, no platform fence. It is how
// the browse and the pairing check both wait for an answer without blocking the
// main queue they are already running on.

import Foundation
import ScanLink

/// spinRunLoop pumps the main run loop for up to `seconds`, returning as soon as
/// `ready()` is true.
///
/// Continuity Camera is published to AVFoundation asynchronously and only to a
/// process that is pumping its run loop, so a bare enumeration on a blocked main
/// thread reports "no iPhone" even when one is connected. Anything that needs a
/// complete device list has to wait like this. The same shape serves any
/// "AVFoundation will tell you eventually" wait, which is why it sits apart from
/// the discovery code it was written for.
func spinRunLoop(seconds: Double, until ready: () -> Bool) {
    let deadline = Date().addingTimeInterval(seconds)
    while !ready(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
    }
}

/// Flag is a boolean set on one queue and polled on another.
///
/// Small enough to look unnecessary and it is not: the pattern it serves is
/// "background callback records something, run-loop pump waits for it", and the
/// obvious alternative — hopping to the main queue to record it — deadlocks
/// whenever the waiter is itself running on the main queue. `ArmedGate` exists
/// for the same reason on the capture path.
final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false

    func set() {
        lock.lock()
        value = true
        lock.unlock()
    }

    var isSet: Bool {
        lock.lock()
        defer { lock.unlock() }
        return value
    }
}

/// The last `LinkFailure` a connection reported, readable from another queue.
///
/// The sibling of `Flag`, and it exists for the same reason: state is written
/// from the link's own queue and read from the main queue that `spinRunLoop`
/// is pumping, and hopping to the main queue to record it deadlocks — that
/// block cannot run until the pumping one returns. See the note on
/// `RemoteController.verify`, which is where that was measured.
final class LastFailure: @unchecked Sendable {
    private let lock = NSLock()
    private var failure: LinkFailure?

    func record(_ reason: LinkFailure) {
        lock.lock()
        // First failure wins. A connection tearing down reports a cascade, and
        // the later entries are consequences of the first — "iPhone
        // disconnected" after a TLS error names the symptom over the cause.
        if failure == nil { failure = reason }
        lock.unlock()
    }

    var value: LinkFailure? {
        lock.lock()
        defer { lock.unlock() }
        return failure
    }

    var isSet: Bool { value != nil }
}
