import CoreGraphics
import Testing

@testable import CardKit

private let desertedTempleBand = [
    "stands as a tribute to the fleeung nature",
    "of human ways.",
    "202 / Wizards of the Coast",
    "R 0301",
    "MH3 *EN No ROB ALEXANDER",
]

@Test("the exact live band reads foil")
func liveDesertedTemple() {
    let p = readPrinting(bandLines: desertedTempleBand)
    #expect(p.setCode == "MH3")
    #expect(p.finish == "foil", "got finish=\(p.finish)")
}

@Test("the finish reaches the field the parent reads")
func finishReachesTheCardEntry() {
    var r = CardReading()
    r.title = "Deserted Temple"
    r.lines = ["Deserted Temple"]
    r.bandLines = desertedTempleBand
    r.printing = readPrinting(bandLines: desertedTempleBand)

    let ev = r.scanEvent(rotation: 0)
    let card = ev.cards?.first
    #expect(card != nil)
    let msg = "card entry finishHint = \(card?.finishHint ?? "nil"), which is "
        + "the field CardList() hands the parent"
    #expect(card?.finishHint == "foil", "\(msg)")
    #expect(ev.finishHint == "foil")
}

@Test("no marker leaves both copies empty")
func noMarkerLeavesBothEmpty() {
    var r = CardReading()
    r.title = "Seasinger"
    r.lines = ["Seasinger"]
    r.bandLines = ["Illus. Amy Weber", "©1994 Wizards of the Coast, Inc."]
    r.printing = readPrinting(bandLines: r.bandLines)
    let ev = r.scanEvent(rotation: 0)
    #expect(ev.cards?.first?.finishHint == "")
    #expect(ev.finishHint == nil, "silence must not become a claim")
}

@Test("a sparkle-read finish crosses the wire the same way a printed one does")
func sparkleFinishReachesTheCardEntry() {
    var r = CardReading()
    r.title = "Glowrider"
    r.lines = ["Glowrider"]
    r.bandLines = [
        "Illus. Scott M. Fischer",
        "TM & © 1993-2003 Wizards of the Coast, Inc. 15/145",
    ]
    r.printing = readPrinting(bandLines: r.bandLines)
    #expect(r.printing.finish == "", "the band alone must say nothing")
    r.printing.finish = "foil"
    r.printing.finishSource = "sparkle"

    let ev = r.scanEvent(rotation: 0)
    #expect(ev.cards?.first?.finishHint == "foil")
    #expect(ev.cards?.first?.finishSource == "sparkle",
            "telemetry must be able to tell which signal answered")
}

@Test("the separator's provenance is recorded too")
func separatorFinishCarriesItsSource() {
    let p = readPrinting(bandLines: desertedTempleBand)
    #expect(p.finish == "foil")
    #expect(p.finishSource == "separator")
}

@Test("no finish means no source")
func silenceCarriesNoProvenance() {
    let p = readPrinting(bandLines: ["Illus. Amy Weber", "©1994 Wizards of the Coast, Inc."])
    #expect(p.finish == "")
    #expect(p.finishSource == "")
    var r = CardReading()
    r.title = "Seasinger"
    r.lines = ["Seasinger"]
    r.bandLines = ["Illus. Amy Weber", "©1994 Wizards of the Coast, Inc."]
    r.printing = p
    #expect(r.scanEvent(rotation: 0).cards?.first?.finishSource == nil,
            "an absent finish must not ship an empty provenance string")
}

@Test("the copyright row re-centres the marker search vertically")
func companyRowAnchorsV() {
    func lineAt(vMid: CGFloat, _ text: String) -> Line {
        let midY = 1 - (vMid - 0.82) / 0.18
        return Line(text: text, box: CGRect(x: 0.1, y: midY - 0.02,
                                            width: 0.8, height: 0.04),
                    confidence: 1, quad: nil)
    }
    let nominal = lineAt(vMid: 0.889 + 0.0671,
                         "TM & © 2024 Wizards of the Coast")
    #expect(abs(companyAnchorShiftV([nominal])) < 0.001)

    let low = lineAt(vMid: 0.889 + 0.0671 + 0.015,
                     "TM & © 2024 Wizards of the Coast")
    #expect(abs(companyAnchorShiftV([low]) - 0.015) < 0.001)
}

@Test("no copyright row, or an implausible one, leaves the anchor alone")
func companyRowAnchorRefusals() {
    #expect(companyAnchorShiftV([]) == 0)
    let prose = Line(text: "whenever it attacks, draw a card.",
                     box: CGRect(x: 0.1, y: 0.5, width: 0.8, height: 0.04),
                     confidence: 1, quad: nil)
    #expect(companyAnchorShiftV([prose]) == 0)
    let absurd = Line(text: "TM & © 2024 Wizards of the Coast",
                      box: CGRect(x: 0.1, y: 0.9, width: 0.8, height: 0.04),
                      confidence: 1, quad: nil)
    #expect(companyAnchorShiftV([absurd]) == 0)
}
