import BorderKit
import CoreGraphics
import Testing

@testable import CardKit

private let m = LocatedCard.wideMargin
private let k = 1 + 2 * m

private func line(_ box: CGRect) -> Line {
    Line(text: "© 1995 Wizards of the Coast", box: box, confidence: 0.9,
         quad: Quad(topLeft: CGPoint(x: box.minX, y: box.maxY),
                    topRight: CGPoint(x: box.maxX, y: box.maxY),
                    bottomLeft: CGPoint(x: box.minX, y: box.minY),
                    bottomRight: CGPoint(x: box.maxX, y: box.minY)))
}

@Test("a line filling the band crop lands on the band, inset by the margin")
func bandFillMapsToTheBand() {
    let mapped = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 1)),
                          from: CardGeometry.band, margin: m)

    #expect(abs(mapped.box.minX - (m / k)) < 0.0001)
    #expect(abs(mapped.box.width - (1 / k)) < 0.0001)
    #expect(abs(mapped.box.maxY - (1 - (m + 0.82) / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (1 - (m + 1.0) / k)) < 0.0001)
}

@Test("Vision's y is flipped, not merely offset")
func visionYIsFlipped() {
    let lower = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 0.5)),
                         from: CardGeometry.band, margin: m)
    let upper = intoWide(line(CGRect(x: 0, y: 0.5, width: 1, height: 0.5)),
                         from: CardGeometry.band, margin: m)

    #expect(lower.box.midY < upper.box.midY)
    #expect(abs(lower.box.maxY - (1 - (m + 0.91) / k)) < 0.0001)
    #expect(abs(upper.box.minY - lower.box.maxY) < 0.0001)
}

@Test("the head crop maps to the top of the card, not the bottom")
func headMapsToTheTop() {
    let mapped = intoWide(line(CGRect(x: 0.1, y: 0.6, width: 0.5, height: 0.1)),
                          from: CardGeometry.head, margin: m)
    #expect(abs(mapped.box.maxY - (1 - (m + 0.09) / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (1 - (m + 0.12) / k)) < 0.0001)
    #expect(mapped.box.midY > 0.5)
}

@Test("the corners travel with the box and stay square")
func quadFollowsTheBox() {
    let mapped = intoWide(line(CGRect(x: 0.2, y: 0.3, width: 0.4, height: 0.2)),
                          from: CardGeometry.band, margin: m)
    guard let q = mapped.quad else {
        Issue.record("the quad was dropped, so the reader has no geometry")
        return
    }
    #expect(abs(q.topLeft.x - mapped.box.minX) < 0.0001)
    #expect(abs(q.topLeft.y - mapped.box.maxY) < 0.0001)
    #expect(abs(q.bottomRight.x - mapped.box.maxX) < 0.0001)
    #expect(abs(q.bottomRight.y - mapped.box.minY) < 0.0001)
    #expect(abs(q.topLeft.y - q.topRight.y) < 0.0001)
    #expect(abs(q.topLeft.x - q.bottomLeft.x) < 0.0001)
}

@Test("the whole card as its own crop is the identity, plus the margin")
func wholeCardCropIsJustTheInset() {
    let whole = CGRect(x: 0, y: 0, width: 1, height: 1)
    let mapped = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 1)), from: whole, margin: m)
    #expect(abs(mapped.box.minX - (m / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (m / k)) < 0.0001)
    #expect(abs(mapped.box.width - (1 / k)) < 0.0001)
    #expect(abs(mapped.box.height - (1 / k)) < 0.0001)
}
