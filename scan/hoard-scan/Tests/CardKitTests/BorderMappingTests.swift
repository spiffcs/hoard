// Moving a recognized line from a card-space crop into the wide flatten.
//
// Three coordinate systems meet in `intoWide` and two disagree about which way
// is up. Getting it wrong samples the opposite end of the card and still
// returns a plausible number — the border reader would report a confident
// verdict about the wrong surface, which is precisely the failure this whole
// change exists to remove. So every step is pinned against hand-computed
// values rather than against whatever the code currently does.

import BorderKit
import CoreGraphics
import Testing

@testable import CardKit

/// The arithmetic the tests below check against, written once in the open:
/// the card sits inset by `wideMargin` on every side, so card-space `c` lands
/// at `(m + c) / (1 + 2m)` of the wide image, measured downward.
private let m = LocatedCard.wideMargin      // 0.10
private let k = 1 + 2 * m                   // 1.20

private func line(_ box: CGRect) -> Line {
    Line(text: "© 1995 Wizards of the Coast", box: box, confidence: 0.9,
         quad: Quad(topLeft: CGPoint(x: box.minX, y: box.maxY),
                    topRight: CGPoint(x: box.maxX, y: box.maxY),
                    bottomLeft: CGPoint(x: box.minX, y: box.minY),
                    bottomRight: CGPoint(x: box.maxX, y: box.minY)))
}

@Test("a line filling the band crop lands on the band, inset by the margin")
func bandFillMapsToTheBand() {
    // The band is the bottom 18% of the card, so a line filling it spans card
    // space 0.82 to 1.0 — which in the wide image is (0.1+0.82)/1.2 down to
    // (0.1+1.0)/1.2, and Vision counts the box's origin up from the bottom.
    let mapped = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 1)),
                          from: CardGeometry.band, margin: m)

    #expect(abs(mapped.box.minX - (m / k)) < 0.0001)
    #expect(abs(mapped.box.width - (1 / k)) < 0.0001)
    #expect(abs(mapped.box.maxY - (1 - (m + 0.82) / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (1 - (m + 1.0) / k)) < 0.0001)
}

@Test("Vision's y is flipped, not merely offset")
func visionYIsFlipped() {
    // The half of the band crop nearer the card's *bottom* is the half Vision
    // numbers *lower*. An implementation that offset without flipping would put
    // this at the other end of the band and read the wrong strip of the card —
    // and would still return a number, which is what makes it worth a test.
    let lower = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 0.5)),
                         from: CardGeometry.band, margin: m)
    let upper = intoWide(line(CGRect(x: 0, y: 0.5, width: 1, height: 0.5)),
                         from: CardGeometry.band, margin: m)

    // Nearer the card's bottom means nearer the wide image's bottom, which is a
    // *smaller* Vision y.
    #expect(lower.box.midY < upper.box.midY)
    // The lower half spans card 0.91 to 1.0.
    #expect(abs(lower.box.maxY - (1 - (m + 0.91) / k)) < 0.0001)
    // And the upper half spans card 0.82 to 0.91, so they meet exactly.
    #expect(abs(upper.box.minY - lower.box.maxY) < 0.0001)
}

@Test("the head crop maps to the top of the card, not the bottom")
func headMapsToTheTop() {
    // The other end, because a flip that is wrong in one direction is often
    // right in the other by accident.
    let mapped = intoWide(line(CGRect(x: 0.1, y: 0.6, width: 0.5, height: 0.1)),
                          from: CardGeometry.head, margin: m)
    // Head is the top 30%: Vision y 0.6-0.7 is card 0.09 to 0.12 downward.
    #expect(abs(mapped.box.maxY - (1 - (m + 0.09) / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (1 - (m + 0.12) / k)) < 0.0001)
    // Well above the middle of the card, which is where a title belongs.
    #expect(mapped.box.midY > 0.5)
}

@Test("the corners travel with the box and stay square")
func quadFollowsTheBox() {
    // The reader takes its scale off the quad, not the box, so a quad left
    // behind or mapped by a different rule would put the geometry somewhere the
    // box does not agree with — and nothing downstream would notice.
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
    // Square, because this pipeline reads an already-rectified card.
    #expect(abs(q.topLeft.y - q.topRight.y) < 0.0001)
    #expect(abs(q.topLeft.x - q.bottomLeft.x) < 0.0001)
}

@Test("the whole card as its own crop is the identity, plus the margin")
func wholeCardCropIsJustTheInset() {
    // The head pass falls back to the whole card when the crop fails, and that
    // path has to map too — it was the one that silently went unmapped.
    let whole = CGRect(x: 0, y: 0, width: 1, height: 1)
    let mapped = intoWide(line(CGRect(x: 0, y: 0, width: 1, height: 1)), from: whole, margin: m)
    #expect(abs(mapped.box.minX - (m / k)) < 0.0001)
    #expect(abs(mapped.box.minY - (m / k)) < 0.0001)
    #expect(abs(mapped.box.width - (1 / k)) < 0.0001)
    #expect(abs(mapped.box.height - (1 / k)) < 0.0001)
}
