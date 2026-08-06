// Frame signatures — split out of GeometryTests when the border geometry moved
// to BorderKit. These test ScanKit's own scene comparison and stayed behind.

import Testing

@testable import ScanKit

// MARK: - Frame signatures

@Test("two readings of the same picture are the same picture")
func sceneDeltaOnIdenticalFrames() {
    let a = [UInt8](repeating: 120, count: sceneGridW * sceneGridH)
    #expect(sceneDelta(a, a) == 0)
}

@Test("signatures that cannot be compared read as maximally different")
func sceneDeltaRefusesMismatchedSignatures() {
    // Returning a large number rather than nil is what keeps the caller's
    // "has the picture changed enough" test honest when a signature is missing.
    #expect(sceneDelta([], [1, 2, 3]) == 255)
    #expect(sceneDelta([1, 2], [1, 2, 3]) == 255)
}

@Test("bare desk carries no detail and a card carries plenty")
func sceneDetailSeparatesDeskFromCard() {
    // Without this the shutter fires every time a card is removed: the empty
    // surface left behind is both changed and perfectly still.
    let desk = [UInt8](repeating: 130, count: sceneGridW * sceneGridH)
    let card = (0..<(sceneGridW * sceneGridH)).map { UInt8($0 % 2 == 0 ? 0 : 255) }
    #expect(sceneDetail(desk) < TriggerTuning.sceneDetail)
    #expect(sceneDetail(card) > TriggerTuning.sceneDetail)
}
