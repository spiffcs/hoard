import CoreGraphics
import Testing

@testable import BorderKit

@Test("the median is the middle of the sample, either parity")
func medianOfSamples() {
    #expect(medianOf([3, 1, 2]) == 2)
    #expect(medianOf([4, 1, 2, 3]) == 2.5)
    #expect(medianOf([]) == 0)
}

@Test("a ring half off the card says so in its deviation")
func madSeparatesOneSurfaceFromTwo() {
    let oneSurface: [CGFloat] = [0.50, 0.51, 0.49, 0.50, 0.51]
    let straddling: [CGFloat] = [0.05, 0.05, 0.05, 0.95, 0.95, 0.95]
    #expect(medianAbsoluteDeviation(oneSurface) < BorderGate.maxRingMAD)
    #expect(medianAbsoluteDeviation(straddling) > BorderGate.maxRingMAD)

    let blemished: [CGFloat] = [0.50, 0.51, 0.49, 0.50, 0.51, 0.95, 0.95]
    #expect(medianAbsoluteDeviation(blemished) < BorderGate.maxRingMAD)
}

@Test("otsu recovers a footer's own ink and paper points")
func otsuSplitsTheFooter() {
    var samples = [CGFloat](repeating: 0.08, count: 40)
    samples += [CGFloat](repeating: 0.92, count: 60)
    let split = try! #require(otsu(samples))
    #expect(abs(split.dark - 0.08) < 0.02)
    #expect(abs(split.bright - 0.92) < 0.02)
    #expect(abs(split.darkFraction - 0.40) < 0.02)
}

@Test("otsu refuses a sample too small to be a histogram")
func otsuNeedsEnoughSamples() {
    #expect(otsu([0.1, 0.9]) == nil)
}

@Test("the border verdicts live outside the card's own range of tones")
func borderGatesBracketTheCardsOwnTones() {
    #expect(BorderGate.whiteTone > 1)
    #expect(BorderGate.blackTone < 0)
}

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
    let lower = try! #require(g.point(u: g.anchorLeftU, v: g.anchorV + 0.1))
    #expect(abs(lower.y - 1000) < 0.001)
    #expect(abs(lower.x - 100) < 0.001)

    let right = try! #require(g.point(u: g.anchorLeftU + 0.5, v: g.anchorV))
    #expect(abs(right.x - (100 + 0.5 * 1000 * cardAspect)) < 0.001)
}

@Test("without a horizontal landmark there is no honest card space")
func cardSpaceRefusesWithoutALeftEdge() {
    var g = uprightGeometry()
    g = CardGeometry(
        heightPx: g.heightPx, origin: g.origin, theta: g.theta,
        halfSpanPx: g.halfSpanPx, anchorV: g.anchorV,
        anchorLeft: nil, anchorLeftU: g.anchorLeftU,
        scaleFromBaseline: g.scaleFromBaseline, scaleFromGlyph: g.scaleFromGlyph
    )
    #expect(g.point(u: 0.5, v: 0.5) == nil)
}
