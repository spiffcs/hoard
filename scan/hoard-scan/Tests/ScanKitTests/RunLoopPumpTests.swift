// The wait that must not deadlock.
//
// `verify` polls a Flag from inside a block already running on the main queue.
// The obvious alternative — hop to the main queue to record the answer —
// deadlocks, because the main queue is serial and the block doing the waiting
// has not returned yet. That was measured live as a pairing that always timed
// out while the phone sat there showing "connected to hoard". These tests pin
// the two pieces that shape holds together with.

// macOS only, like everything in ScanKit. See Package.swift.
#if os(macOS)

import Foundation
import Testing

@testable import ScanKit

@Test("a Flag starts clear and latches when set")
func flagLatches() {
    let f = Flag()
    #expect(!f.isSet)
    f.set()
    #expect(f.isSet)
    // Latching, not toggling: there is no way back, because a code that was
    // accepted cannot become un-accepted.
    f.set()
    #expect(f.isSet)
}

@Test("a Flag set on another queue is visible to the poller")
func flagCrossesQueues() {
    // This is the whole reason the type exists rather than a plain Bool: the
    // link's own queue sets it and the main queue polls it.
    let f = Flag()
    let done = DispatchSemaphore(value: 0)
    DispatchQueue.global().async {
        f.set()
        done.signal()
    }
    #expect(done.wait(timeout: .now() + 5) == .success)
    #expect(f.isSet)
}

@Test("concurrent setters do not race")
func flagConcurrentSetters() {
    // Under the thread sanitiser this is the test that would catch dropping
    // the lock; without it, it is at least a smoke test that many setters and
    // a reader coexist.
    let f = Flag()
    DispatchQueue.concurrentPerform(iterations: 200) { _ in
        f.set()
        _ = f.isSet
    }
    #expect(f.isSet)
}

@Test("spinRunLoop returns as soon as the condition holds")
func spinRunLoopReturnsEarly() {
    // The deadline is the ceiling, not the wait. A verify that always burned
    // its full six seconds would make every successful pairing feel broken.
    let f = Flag()
    DispatchQueue.global().asyncAfter(deadline: .now() + 0.05) { f.set() }
    let start = Date()
    spinRunLoop(seconds: 5) { f.isSet }
    let elapsed = Date().timeIntervalSince(start)
    #expect(f.isSet)
    #expect(elapsed < 2, "returned after \(elapsed)s; it should not wait out the deadline")
}

@Test("spinRunLoop gives up at its deadline")
func spinRunLoopHonoursDeadline() {
    // The other half: a wrong pairing code means nothing ever arrives, and the
    // wait has to end by itself rather than hang the helper.
    let start = Date()
    spinRunLoop(seconds: 0.2) { false }
    let elapsed = Date().timeIntervalSince(start)
    #expect(elapsed >= 0.2)
    #expect(elapsed < 5, "overshot its deadline by a lot: \(elapsed)s")
}

@Test("spinRunLoop with an already-true condition returns immediately")
func spinRunLoopAlreadyReady() {
    let start = Date()
    spinRunLoop(seconds: 5) { true }
    #expect(Date().timeIntervalSince(start) < 1)
}

#endif
