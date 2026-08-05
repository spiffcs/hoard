// The one piece of state the video tap shares with the main thread.
//
// This cannot test the bug it was written for — that race lives in
// captureOutput, which needs a camera — so it tests the thing that replaced the
// racing read, and does it under contention so Thread Sanitizer has something
// to look at. Run `swift test --sanitize=thread` to make that meaningful.

import Foundation
import Testing

@testable import ScanKit

@Test("the gate reports what was last written")
func gateHoldsItsValue() {
    let gate = ArmedGate()
    #expect(gate.isArmed == false, "a session starts with auto off")
    gate.set(true)
    #expect(gate.isArmed)
    gate.set(false)
    #expect(gate.isArmed == false)
}

@Test("concurrent readers and a writer do not race")
func gateSurvivesContention() async {
    // The real access pattern: one writer on the main thread flipping the bit
    // on every trigger transition, and a reader hammering it from another queue
    // once per delivered video frame.
    let gate = ArmedGate()
    let writes = 5_000

    await withTaskGroup(of: Void.self) { group in
        group.addTask {
            for i in 0..<writes { gate.set(i % 2 == 0) }
        }
        for _ in 0..<3 {
            group.addTask {
                var seen = 0
                for _ in 0..<writes where gate.isArmed { seen += 1 }
                // The count is timing-dependent and deliberately unasserted;
                // what matters is that the reads are well-defined at all.
                _ = seen
            }
        }
    }

    gate.set(true)
    #expect(gate.isArmed, "the gate must still be usable after contention")
}
