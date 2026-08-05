// Every shape of bottom border the parser has actually been handed.
//
// The footer is where nearly all the printing evidence lives — set code,
// collector number, language, rarity, year, and the one glyph that says foil —
// and its layout has changed repeatedly across thirty years of frames. This
// file is the record of which shapes are known to work.
//
// Every fixture below is copied from a real capture log, mangling and all. Not
// one is invented. A synthetic footer tests a card layout nobody photographs:
// the interesting failures are all in what OCR does to small italic serif type
// at the bottom of a shiny card — `™M & O 2017`, `Wizards of tha C`,
// `MH3*EN` with the star fused to the code and no spaces anywhere.
//
// Measured on one live session: when the band reaches the footer, a set row
// comes back 98% of the time (93 of 95). The remaining failures are not
// parsing failures — they are captures where the crop never reached the footer
// at all, which is a different problem and is addressed upstream.

import Testing

@testable import CardKit

// MARK: - The modern set row

@Test("the set row survives every separator the light made of it")
func modernSetRowSeparators() {
    // The separator carries the finish, so its shape matters twice over.
    let cases: [(line: String, set: String, finish: String)] = [
        ("MH3 • EN OJOHANN BODIN", "MH3", "nonfoil"),
        ("THB • EN • ALEKSI BRICLOT", "THB", "nonfoil"),
        ("THB • EN ALEKSI BRICLOT", "THB", "nonfoil"),
        ("M3C • EN OLIE SETIAWAN", "M3C", "nonfoil"),
        ("MH3 • EN O STEVE ELLIS", "MH3", "nonfoil"),
        // The star, fused to the code with no space at all.
        ("MH3*EN", "MH3", "foil"),
        ("MH3*EN • L.A DRAWS", "MH3", "foil"),
        ("MH3 *EN No ROB ALEXANDER", "MH3", "foil"),
    ]
    for c in cases {
        let p = readPrinting(bandLines: [c.line])
        #expect(p.setCode == c.set, "set code from \(c.line)")
        #expect(p.language == "EN", "language from \(c.line)")
        #expect(p.finish == c.finish, "finish from \(c.line)")
    }
}

// MARK: - The collector row, in both modern layouts

@Test("the rarity-first row reads, zero padding and all")
func rarityFirstCollectorRow() {
    // "U 0210" — rarity, then a zero-padded number. The catalog is not keyed
    // on the padding, so it comes off.
    let cases: [(String, String, String)] = [
        ("U 0210", "210", "U"), ("U 0198", "198", "U"),
        ("R 0131", "131", "R"), ("R 0055", "55", "R"),
        ("R 0457", "457", "R"),
    ]
    for (line, number, rarity) in cases {
        let p = readPrinting(bandLines: [line])
        #expect(p.number == number, "number from \(line)")
        #expect(p.rarity == rarity, "rarity from \(line)")
    }
}

@Test("the number-over-total row reads, rarity trailing")
func pairWithTrailingRarityLayout() {
    let cases: [(String, String, Int, String)] = [
        ("151/254 R", "151", 254, "R"),
        ("228/297 R", "228", 297, "R"),
        ("188/259 M", "188", 259, "M"),
        ("183/259 R", "183", 259, "R"),
    ]
    for (line, number, total, rarity) in cases {
        let p = readPrinting(bandLines: [line])
        #expect(p.number == number, "number from \(line)")
        #expect(p.total == total, "total from \(line)")
        #expect(p.rarity == rarity, "rarity from \(line)")
    }
}

// MARK: - The copyright row

@Test("the copyright row yields its year however mangled")
func copyrightRowYears() {
    // The trademark glyph is the least reliable thing on a card, and the
    // company name barely fares better. The year is what this row is for.
    let cases: [(String, Int)] = [
        ("TM & C 2018 Wizards of the Coast", 2018),
        ("™M & © 2024 Wizards of the Coast", 2024),
        ("TM & © 2017 Wizards of the Coast", 2017),
        ("™M & O 2017 Wizards of the Coast", 2017),
        ("™M & © 2016 Wizards of the Coast", 2016),
        ("TM & © 2020 Wizards of the Coast", 2020),
        // The company name half-eaten, which happens at the frame's edge.
        ("TM & © 2024 Wizards of tha C", 2024),
    ]
    for (line, year) in cases {
        #expect(readPrinting(bandLines: [line]).year == year, "year from \(line)")
    }
}

@Test("older frames put the number at the tail of the copyright row")
func copyrightRowCarriesTheNumber() {
    // Two eras, two shapes. The 1998-2003 frames print a pair; some later ones
    // print a bare number with no total beside it.
    let pair = readPrinting(bandLines: [
        "IM & © 1993-2003 Wizards of the Coast, Inc. 93/350",
    ])
    #expect(pair.number == "93")
    #expect(pair.total == 350)
    #expect(pair.numberSource == .copyrightRow)

    let bare = readPrinting(bandLines: ["™M & © 2024 Wizards of the Coast 410"])
    #expect(bare.number == "410")
    #expect(bare.numberSource == .copyrightRow)
}

// MARK: - Whole footers, as they arrived

@Test("a complete modern footer yields everything at once")
func completeModernFooter() {
    // The whole point of reaching the bottom border: one crop, five facts.
    let p = readPrinting(bandLines: [
        "Deathtouch, lifelink",
        "R 0112",
        "MH3*EN • L.A DRAWS",
        "™M & © 2024 Wizards of the Coast",
        "3/3",
    ])
    #expect(p.number == "112")
    #expect(p.rarity == "R")
    #expect(p.setCode == "MH3")
    #expect(p.language == "EN")
    #expect(p.finish == "foil")
    #expect(p.year == 2024)
}

@Test("a complete 2018 footer, pair layout")
func complete2018Footer() {
    let p = readPrinting(bandLines: [
        "about your wishes, though—they amuse",
        "076/269 R",
        "5/6",
        "DOM•ENIO MAGALI VILLENEUVE",
        "TN & C 2018 Wizards of the Coast",
    ])
    #expect(p.number == "76")
    #expect(p.total == 269)
    #expect(p.setCode == "DOM")
    #expect(p.finish == "nonfoil")
    #expect(p.year == 2018)
}

@Test("a pre-Exodus footer yields a year and refuses the rest")
func preExodusFooter() {
    // No set row, no collector number, no marker of any kind. The honest
    // answer is a year and three empty fields — inventing a number here is
    // how a card acquires a printing it never had.
    let p = readPrinting(bandLines: [
        "Illus. Amy Weber",
        "©1994 Wizards of the Coast, Inc. All rights reserved",
        "0/1",
    ])
    #expect(p.year == 1994)
    #expect(p.number.isEmpty)
    #expect(p.setCode.isEmpty)
    #expect(p.finish.isEmpty, "no marker was printed, so none is claimed")
}

// MARK: - What a missed footer looks like

@Test("rules text and a power box yield nothing, and say so")
func bandThatMissedTheFooter() {
    // The fingerprint of a crop that stopped short of the bottom border,
    // straight from four live captures. Every one committed as nonfoil with no
    // set code and no collector number, and one of them — Wurmcoil Larva — was
    // a foil.
    //
    // The parser is right to return nothing here. Nothing in these lines is
    // printing evidence, and the fix belongs upstream, in the crop.
    for band in [
        ["Lly-usa", "with lifelink.", "Even a scrap of Phyrexian machinery can be",
         "lethal.", "3/3"],
        ["next turn, you may play that card.", "3/4"],
        ["from the flouers and ferns.", "1/7"],
        ["your mana pool.\"", "4/5"],
    ] {
        let p = readPrinting(bandLines: band)
        #expect(p.isEmpty, "a band of rules text must yield no printing: \(band)")
    }
}

@Test("a missed footer is distinguishable from a card that prints none")
func missedFooterIsNotAnOldFrame() {
    // Both come back with no set code, so the difference has to be visible
    // some other way or a recovery pass cannot know when to run. A card that
    // prints no footer still prints a copyright year; a crop that missed the
    // footer has no year either.
    let oldFrame = readPrinting(bandLines: [
        "Illus. Amy Weber",
        "©1994 Wizards of the Coast, Inc. All rights reserved",
    ])
    let missed = readPrinting(bandLines: ["next turn, you may play that card.", "3/4"])

    #expect(oldFrame.year != nil, "an old frame still dates itself")
    #expect(missed.year == nil, "a missed footer dates nothing")
    #expect(missed.isEmpty)
}

// MARK: - The recovery strip

@Test("the recovery strip reaches below the card, and overlaps its edge")
func recoveryStripGeometry() {
    // Two properties the fallback depends on, and both are easy to get wrong
    // in a way that still looks plausible.
    //
    // It has to start *inside* the box: the clipped rows sit immediately below
    // wherever the quad stopped, and a strip flush with the edge would cut any
    // that straddle it. And it has to reach far enough down to clear the worst
    // clip observed — roughly a sixth of a card — without running so far that
    // it starts reading whatever else is on the desk.
    #expect(FooterRecovery.overlap > 0,
            "a strip starting flush with the box would cut straddling rows")
    #expect(FooterRecovery.reach > 0.16,
            "the worst observed clip lost about a sixth of the card")
    #expect(FooterRecovery.reach < 0.5,
            "half a card below is the next card on the pile, not this one's footer")
}

@Test("recovery runs only when the band found no printing")
func recoveryOnlyOnAnEmptyBand() {
    // The condition, stated as the parser sees it. A band that yielded any
    // printing evidence at all is a band that reached the footer, and a second
    // pass over the desk below it could only add noise.
    let reached = readPrinting(bandLines: ["R 0112", "MH3*EN • L.A DRAWS"])
    #expect(!reached.isEmpty, "this band reached the footer; do not go looking")

    let missed = readPrinting(bandLines: ["with lifelink.", "3/3"])
    #expect(missed.isEmpty, "this band did not; the strip below is worth reading")

    // And an old frame that prints no set row still dates itself, so it is not
    // mistaken for a miss and does not trigger a pointless second pass.
    let oldFrame = readPrinting(bandLines: [
        "Illus. Amy Weber", "©1994 Wizards of the Coast, Inc.",
    ])
    #expect(!oldFrame.isEmpty, "a year is printing evidence")
}
