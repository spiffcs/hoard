import Testing

@testable import CardKit

@Test("the set row survives every separator the light made of it")
func modernSetRowSeparators() {
    let cases: [(line: String, set: String, finish: String)] = [
        ("MH3 • EN OJOHANN BODIN", "MH3", "nonfoil"),
        ("THB • EN • ALEKSI BRICLOT", "THB", "nonfoil"),
        ("THB • EN ALEKSI BRICLOT", "THB", "nonfoil"),
        ("M3C • EN OLIE SETIAWAN", "M3C", "nonfoil"),
        ("MH3 • EN O STEVE ELLIS", "MH3", "nonfoil"),
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

@Test("the rarity-first row reads, zero padding and all")
func rarityFirstCollectorRow() {
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

@Test("the copyright row yields its year however mangled")
func copyrightRowYears() {
    let cases: [(String, Int)] = [
        ("TM & C 2018 Wizards of the Coast", 2018),
        ("™M & © 2024 Wizards of the Coast", 2024),
        ("TM & © 2017 Wizards of the Coast", 2017),
        ("™M & O 2017 Wizards of the Coast", 2017),
        ("™M & © 2016 Wizards of the Coast", 2016),
        ("TM & © 2020 Wizards of the Coast", 2020),
        ("TM & © 2024 Wizards of tha C", 2024),
    ]
    for (line, year) in cases {
        #expect(readPrinting(bandLines: [line]).year == year, "year from \(line)")
    }
}

@Test("older frames put the number at the tail of the copyright row")
func copyrightRowCarriesTheNumber() {
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

@Test("a complete modern footer yields everything at once")
func completeModernFooter() {
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

@Test("rules text and a power box yield nothing, and say so")
func bandThatMissedTheFooter() {
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
    let oldFrame = readPrinting(bandLines: [
        "Illus. Amy Weber",
        "©1994 Wizards of the Coast, Inc. All rights reserved",
    ])
    let missed = readPrinting(bandLines: ["next turn, you may play that card.", "3/4"])

    #expect(oldFrame.year != nil, "an old frame still dates itself")
    #expect(missed.year == nil, "a missed footer dates nothing")
    #expect(missed.isEmpty)
}

@Test("the recovery strip reaches below the card, and overlaps its edge")
func recoveryStripGeometry() {
    #expect(FooterRecovery.overlap > 0,
            "a strip starting flush with the box would cut straddling rows")
    #expect(FooterRecovery.reach > 0.16,
            "the worst observed clip lost about a sixth of the card")
    #expect(FooterRecovery.reach < 0.5,
            "half a card below is the next card on the pile, not this one's footer")
}

@Test("recovery runs only when the band found no printing")
func recoveryOnlyOnAnEmptyBand() {
    let reached = readPrinting(bandLines: ["R 0112", "MH3*EN • L.A DRAWS"])
    #expect(!reached.isEmpty, "this band reached the footer; do not go looking")

    let missed = readPrinting(bandLines: ["with lifelink.", "3/3"])
    #expect(missed.isEmpty, "this band did not; the strip below is worth reading")

    let oldFrame = readPrinting(bandLines: [
        "Illus. Amy Weber", "©1994 Wizards of the Coast, Inc.",
    ])
    #expect(!oldFrame.isEmpty, "a year is printing evidence")
}
