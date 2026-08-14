import Foundation
import Testing

@testable import CardKit

@Test("a capture that read nothing sends no card")
func emptyReadSendsNoCard() {
    let ev = CardReading().scanEvent(rotation: 0)
    #expect(ev.cards?.isEmpty ?? true,
            "bare desk must not cross the wire as a card: \(ev.cards ?? [])")
    #expect(ev.name.isEmpty)
}

@Test("a title alone is still a card")
func titleOnlyIsACard() {
    var r = CardReading()
    r.title = "Prodigal Sorcerer"
    #expect(r.scanEvent(rotation: 0).cards?.count == 1)
}

@Test("a band read alone is still a card")
func bandOnlyIsACard() {
    var r = CardReading()
    r.bandLines = ["© 1995 Wizards of the Coast, Inc. All rights reserved."]
    #expect(r.scanEvent(rotation: 0).cards?.count == 1)
}

@Test("alternates alone are still a card")
func candidatesOnlyIsACard() {
    var r = CardReading()
    r.lines = ["Somethnig Mangled"]
    #expect(r.scanEvent(rotation: 0).cards?.count == 1)
}

@Test("a scrap of text is not a card")
func scrapIsNotACard() {
    for scrap in ["0/1", "2", "2/2", "\u{2014}", "7"] {
        var r = CardReading()
        r.lines = [scrap]
        r.title = chooseTitle(from: r.lines)
        #expect(r.scanEvent(rotation: 0).cards?.isEmpty ?? true,
                "\(scrap) crossed the wire as a card")
    }
}

@Test("a scrap with a real footer is still a card")
func scrapWithBandIsACard() {
    var r = CardReading()
    r.lines = ["2"]
    r.title = "2"
    r.bandLines = ["Illus. Doug Chaffee",
                   "™M & © 1993-2002 Wizards of the Coast, Inc. 46/350"]
    #expect(r.scanEvent(rotation: 0).cards?.count == 1,
            "a capture holding a printing must not be dropped")
}

@Test("the trigger's measurements cross only when they were taken")
func triggerDeltasCrossWhenMeasured() throws {
    var r = CardReading()
    r.title = "No-Dachi"

    let measured = r.scanEvent(rotation: 0, auto: true, fireReason: "replaced",
                               holdDelta: 34.3, faceDelta: 32.5)
    #expect(measured.holdDelta == 34.3)
    #expect(measured.faceDelta == 32.5)

    let json = try String(decoding: JSONEncoder().encode(measured), as: UTF8.self)
    #expect(json.contains("faceDelta"))

    let manual = r.scanEvent(rotation: 0)
    #expect(manual.holdDelta == nil)
    #expect(manual.faceDelta == nil)
    let bare = try String(decoding: JSONEncoder().encode(manual), as: UTF8.self)
    #expect(!bare.contains("faceDelta"),
            "an unmeasured delta must be absent, not zero: \(bare)")
    #expect(!bare.contains("holdDelta"))
}
