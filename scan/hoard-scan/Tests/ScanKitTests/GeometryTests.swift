// The border reader's arithmetic: the statistics that judge a sampled ring,
// and the card-space mapping the whole reconstruction rests on.
//
// scan/corpus can fit these constants against ground truth and scan/fixtures
// can confirm them under a desk lamp, but neither can say whether the maths is
// right — a wrong sign or a swapped axis shows up there as a slightly worse
// hit rate rather than as an error. These say it directly.

import CoreGraphics
import Testing

@testable import ScanKit

// MARK: - Statistics

@Test("the median is the middle of the sample, either parity")
func medianOfSamples() {
    #expect(medianOf([3, 1, 2]) == 2)
    #expect(medianOf([4, 1, 2, 3]) == 2.5)
    #expect(medianOf([]) == 0)
}

@Test("a ring half off the card says so in its deviation")
func madSeparatesOneSurfaceFromTwo() {
    // This is the whole point of preferring MAD to a mean: a ring straddling
    // the card's cut edge sits on two very different surfaces, where a mean
    // would just land somewhere in between and look like a plausible tone.
    let oneSurface: [CGFloat] = [0.50, 0.51, 0.49, 0.50, 0.51]
    let straddling: [CGFloat] = [0.05, 0.05, 0.05, 0.95, 0.95, 0.95]
    #expect(medianAbsoluteDeviation(oneSurface) < BorderGate.maxRingMAD)
    #expect(medianAbsoluteDeviation(straddling) > BorderGate.maxRingMAD)

    // And the robustness that makes MAD the right choice rather than a
    // variance: a couple of stray samples — a glare speck, a rounded corner —
    // do not move it, so the gate rejects a genuinely drifted ring instead of
    // any ring with a blemish on it.
    let blemished: [CGFloat] = [0.50, 0.51, 0.49, 0.50, 0.51, 0.95, 0.95]
    #expect(medianAbsoluteDeviation(blemished) < BorderGate.maxRingMAD)
}

@Test("otsu recovers a footer's own ink and paper points")
func otsuSplitsTheFooter() {
    // On a footer line the two class means are the border's paper tone and the
    // credit's ink — the card's own black and white points, measured under
    // whatever light the desk happened to have.
    var samples = [CGFloat](repeating: 0.08, count: 40)  // ink
    samples += [CGFloat](repeating: 0.92, count: 60)     // paper
    let split = try! #require(otsu(samples))
    #expect(abs(split.dark - 0.08) < 0.02)
    #expect(abs(split.bright - 0.92) < 0.02)
    #expect(abs(split.darkFraction - 0.40) < 0.02)
}

@Test("otsu refuses a sample too small to be a histogram")
func otsuNeedsEnoughSamples() {
    // Abstaining is a first-class outcome everywhere in this subsystem.
    #expect(otsu([0.1, 0.9]) == nil)
}

// MARK: - The tone scale

@Test("the border verdicts live outside the card's own range of tones")
func borderGatesBracketTheCardsOwnTones() {
    // tone = (ring - footerInk) / (footerPaper - footerInk), so 0 is the
    // card's own ink and 1 its own paper. A white border is brighter than the
    // paper and a black border darker than the ink, which is why both gates
    // sit outside [0, 1] and everything between them is ambiguous by
    // construction. Absolute luminance was the first rule and a desk lamp
    // destroyed it: white borders that scan at 0.92 read 0.44-0.64 in a live
    // session, and the same six score 1.36-2.44 on this scale.
    #expect(BorderGate.whiteTone > 1)
    #expect(BorderGate.blackTone < 0)
}

// MARK: - Card space

/// A card reconstructed upright, one pixel per card-height unit of 1/1000.
private func uprightGeometry() -> CardGeometry {
    CardGeometry(
        heightPx: 1000,
        origin: CGPoint(x: 500, y: 900),
        theta: 0,
        halfSpanPx: 200,
        anchorV: 0.9,
        anchorLeft: CGPoint(x: 100, y: 900),
        anchorLeftU: 0.1,
        scaleFromBaseline: 1000,
        scaleFromGlyph: 1000
    )
}

@Test("card space maps back onto the anchor it was built from")
func cardSpaceIsAnchoredWhereItSaysItIs() {
    let g = uprightGeometry()
    let anchor = try! #require(g.point(u: g.anchorLeftU, v: g.anchorV))
    #expect(abs(anchor.x - 100) < 0.001)
    #expect(abs(anchor.y - 900) < 0.001)
}

@Test("v runs down the card and u runs across it, scaled by the real aspect")
func cardSpaceAxesPointTheRightWay() {
    let g = uprightGeometry()
    // One tenth of the card's height further down is 100px down in an upright
    // frame — y measured downward, as the reconstruction does.
    let lower = try! #require(g.point(u: g.anchorLeftU, v: g.anchorV + 0.1))
    #expect(abs(lower.y - 1000) < 0.001)
    #expect(abs(lower.x - 100) < 0.001)

    // Across, u is a fraction of the *width*, which is the card's aspect times
    // its height — 63x88mm, not a square.
    let right = try! #require(g.point(u: g.anchorLeftU + 0.5, v: g.anchorV))
    #expect(abs(right.x - (100 + 0.5 * 1000 * cardAspect)) < 0.001)
}

@Test("without a horizontal landmark there is no honest card space")
func cardSpaceRefusesWithoutALeftEdge() {
    // A truncated read's left edge is wherever OCR gave up, not a landmark. A
    // guessed offset throws the symbol patch clean off the image, which is
    // exactly what a 7th Edition card did while that was a single constant.
    var g = uprightGeometry()
    g = CardGeometry(
        heightPx: g.heightPx, origin: g.origin, theta: g.theta,
        halfSpanPx: g.halfSpanPx, anchorV: g.anchorV,
        anchorLeft: nil, anchorLeftU: g.anchorLeftU,
        scaleFromBaseline: g.scaleFromBaseline, scaleFromGlyph: g.scaleFromGlyph
    )
    #expect(g.point(u: 0.5, v: 0.5) == nil)
}

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
