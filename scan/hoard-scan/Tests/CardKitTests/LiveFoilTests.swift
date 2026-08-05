import Testing

@testable import CardKit

/// The exact band a foil Deserted Temple produced, twice, over the wire.
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
    // The parser was right and the card still committed nonfoil, because the
    // finish was written to the Event's top-level field while the parent reads
    // the card entry. `CardList()` falls back to the top-level fields only when
    // `cards` is empty, so on any real capture the two never meet.
    //
    // Asserting on the parsed Printing would have passed throughout. This
    // asserts on what actually crosses the wire.
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
    // The top-level copy is the old-helper compatibility path and should agree.
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
