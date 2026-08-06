// What crosses the wire when the capture found nothing.
//
// The rule this pins already existed on the macOS side, where the `empty-desk`
// fixture's golden is `[]` and its note reads "a frame with no cards yields an
// empty list, not junk entries". CardKit did not honour it, and the cost was
// measured: seven of ten review cases in one live session were captures of bare
// desk, five of them firing 1.6-2.0s before the real card committed while the
// operator's hand was still moving.

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
    // The segmenter missing a card it could still read is a real card and a
    // legitimate review case. The guard asks what the capture learned, not
    // whether the geometry worked.
    var r = CardReading()
    r.title = "Prodigal Sorcerer"
    #expect(r.scanEvent(rotation: 0).cards?.count == 1)
}

@Test("a band read alone is still a card")
func bandOnlyIsACard() {
    // A glare-blown title over a perfectly readable footer: the printing
    // evidence is exactly what the review queue exists to resolve.
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
    // Live: a capture caught only a creature's power/toughness box, and
    // chooseTitle's fallback made "0/1" the title. It queued as
    // `couldn't identify "0/1"` — a review item with nothing to review.
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
    // The glare-blown title: the name failed but the footer names the
    // printing, which is exactly what a human can finish. Live, this arrived
    // with a title of "2" and a clean 46/350.
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
    // The Go side decides whether a repeat is a second physical card from
    // `faceDelta`, so the absent case has to stay absent. An omitted key
    // decodes to nil there and falls back to the timing floor; a key carrying
    // zero decodes to "identical picture", which is the strongest same-card
    // evidence there is. Encoding one as the other inverts the answer.
    var r = CardReading()
    r.title = "No-Dachi"

    let measured = r.scanEvent(rotation: 0, auto: true, fireReason: "replaced",
                               holdDelta: 34.3, faceDelta: 32.5)
    #expect(measured.holdDelta == 34.3)
    #expect(measured.faceDelta == 32.5)

    let json = try String(decoding: JSONEncoder().encode(measured), as: UTF8.self)
    #expect(json.contains("faceDelta"))

    // A manual shutter: no trigger decision, so no numbers behind one.
    let manual = r.scanEvent(rotation: 0)
    #expect(manual.holdDelta == nil)
    #expect(manual.faceDelta == nil)
    let bare = try String(decoding: JSONEncoder().encode(manual), as: UTF8.self)
    #expect(!bare.contains("faceDelta"),
            "an unmeasured delta must be absent, not zero: \(bare)")
    #expect(!bare.contains("holdDelta"))
}
