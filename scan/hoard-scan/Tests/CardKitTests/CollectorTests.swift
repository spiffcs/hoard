// The printing parser, tested against strings real captures actually produced.
//
// Every fixture in this file is copied from a capture log, mangling and all —
// `IM &` for `™ &`, `1093-2003` for `1993-2003`, `Inr.` for `Inc.`. Writing the
// tests against clean text would test a card layout nobody photographs.

import Testing

@testable import CardKit

// MARK: - Modern frames

@Test("the modern frame's three rows read as one printing")
func modernFrame() {
    let p = readPrinting(bandLines: [
        "Rebound",
        "R 0339",
        "MSC • EN ALEXANDER SKRIPNIKOV",
        "C MARVEL",
        "TM & © 2026 Wizards of the Coast",
    ])
    #expect(p.number == "339")
    #expect(p.rarity == "R")
    #expect(p.setCode == "MSC")
    #expect(p.language == "EN")
    #expect(p.year == 2026)
    #expect(p.numberSource == .ownRow)
}

@Test("the set row's separator is whatever the light made of it")
func manglеdSeparator() {
    // Observed across captures: "MSC•EN • ALERANDER", "MSC • EN A ALEXANDER",
    // "MSC •EN I ALEXANDER", "MAR• EN OBRIAN STE".
    for row in ["MSC•EN • ALEXANDER SKRIPNIKOV",
                "MSC • EN A ALEXANDER SKRIPNIKOV",
                "MSC •EN I ALEXANDER SKRIPNIKOV",
                "MAR• EN OBRIAN STELFREEZE"] {
        let p = readPrinting(bandLines: [row])
        #expect(!p.setCode.isEmpty, "no set code from \(row)")
        #expect(p.language == "EN", "no language from \(row)")
    }
}

@Test("newer frames print the pair with the rarity trailing")
func pairWithTrailingRarity() {
    // The second modern layout, from corpus reads. Missing it cost most of a
    // stratum: the power/toughness guard rejected every N/M standing alone, and
    // this form stands alone.
    let cases: [(String, String, String)] = [
        ("130/287 M", "130", "M"),
        ("235/280 U", "235", "U"),
        ("010/024 T", "10", "T"),
    ]
    for (line, number, rarity) in cases {
        let p = readPrinting(bandLines: [line])
        #expect(p.number == number, "number from \(line)")
        #expect(p.rarity == rarity, "rarity from \(line)")
        #expect(p.numberSource == .ownRow)
    }
}

@Test("the rarity letter is what separates a printing from a creature's body")
func rarityDistinguishesPairFromPowerToughness() {
    // Same shape, one letter apart. Without the letter it is the power and
    // toughness box and must never become a printing.
    #expect(readPrinting(bandLines: ["2/2"]).number.isEmpty)
    #expect(readPrinting(bandLines: ["2/2 M"]).number == "2")
}

@Test("a three-digit denominator is a set size, not a toughness")
func largeDenominatorIsAPrinting() {
    // Some frames print the rarity on the *following* line, leaving the pair
    // entirely alone — "012/216" then "UST • EN", "113/216" then "R". The
    // rarity rule cannot see those, so set size carries the decision instead.
    #expect(readPrinting(bandLines: ["012/216"]).number == "12")
    #expect(readPrinting(bandLines: ["113/216"]).number == "113")
    // And the guard still holds where it matters.
    #expect(readPrinting(bandLines: ["12/12"]).number.isEmpty)
}

@Test("tokens after the rarity do not derail the pair")
func trailingTokensIgnored() {
    // "002/004 P HASCON 2017" — a promo stamp trailing the rarity.
    let p = readPrinting(bandLines: ["002/004 P HASCON 2017"])
    #expect(p.number == "2")
    #expect(p.rarity == "P")
}

@Test("leading zeros are stripped, because the catalog is not keyed on them")
func zeroPadding() {
    #expect(readPrinting(bandLines: ["M 0087"]).number == "87")
    #expect(readPrinting(bandLines: ["R 0338"]).number == "338")
    #expect(readPrinting(bandLines: ["0412"]).number == "412")
}

// MARK: - The copyright-row number

@Test("1998-2003 frames print the number inside the copyright row")
func numberInCopyrightRow() {
    let cases: [(String, String, Int, Int)] = [
        ("IM & © 1993-2003 Wizards of the Coast, Inc. 93/350", "93", 350, 2003),
        ("TN & C 1993-2003 Wizards of the Coast, Inc. 112/350", "112", 350, 2003),
        ("TM & © 1093-2003 Wizards of the Coast, Inc. 15/145", "15", 145, 2003),
        ("© 1993-2003 Wizards of the Coast, Inc. 24/143", "24", 143, 2003),
        ("©1993-1999 Wizards of the Coast, Inc, 36/143", "36", 143, 1999),
    ]
    for (line, number, total, year) in cases {
        let p = readPrinting(bandLines: [line])
        #expect(p.number == number, "number from \(line)")
        #expect(p.total == total, "total from \(line)")
        #expect(p.year == year, "year from \(line)")
        #expect(p.numberSource == .copyrightRow)
    }
}

@Test("a mangled leading year does not cost the range's later end")
func mangledLeadingYear() {
    // "1093" is outside the plausible window and is dropped; 2003 survives, and
    // 2003 is the year that dates the printing.
    let p = readPrinting(bandLines: ["TM & © 1093-2003 Wizards of the Coast, Inc. 15/145"])
    #expect(p.year == 2003)
    #expect(p.yearFrom == nil)
}

// MARK: - Power/toughness must never become a printing

@Test("a bare N/M is a power/toughness box, not a collector number")
func powerToughnessRejected() {
    // Measured across thirteen captures: power/toughness always stands alone on
    // its own line, and the collector pair never does.
    for pt in ["0/1", "2/2", "1/1", "2/1", "1/3", " 4/4 "] {
        let p = readPrinting(bandLines: ["Illus. Amy Weber", pt])
        #expect(p.number.isEmpty, "\(pt) was read as a collector number")
    }
}

@Test("a sideboard marker is not a collector number")
func sideboardMarkerRejected() {
    // World Championship decks print SB and GB on their own line. Folding digit
    // lookalikes turns those into 58 and 68, which handed a collector number to
    // 18% of the corpus's pre-1998 gold stratum — cards that print none at all.
    // Folding may repair a number; it may not manufacture one.
    for marker in ["SB", "GB", "SI", "OO", "BS"] {
        let p = readPrinting(bandLines: [
            "Illus. Susan Van Camp",
            "© 1995 Wizards of the Coast, Inc. All rights reserved.",
            marker,
            "1/1",
        ])
        #expect(p.number.isEmpty, "\(marker) was read as collector number \(p.number)")
    }
}

@Test("a mostly-numeric token is still repaired")
func foldingStillRepairs() {
    // The rule above must not cost the case it was written for: a number with
    // one character misread should still fold.
    #expect(digitsOnly("O338") == "0338")
    #expect(digitsOnly("12S") == "125")
    #expect(digitsOnly("SB") == nil)
    #expect(digitsOnly("GB") == nil)
}

@Test("junk beside a number is dropped, not fatal")
func junkIsDropped() {
    // Straight from a live session: both of these reached the parser with the
    // number intact and were refused over one stray letter.
    #expect(digitsOnly("n0322") == "0322")
    #expect(digitsOnly("0338#") == "0338")
    // And the guards still hold.
    #expect(digitsOnly("Illus") == nil)
    #expect(digitsOnly("Volrath") == nil)
    #expect(digitsOnly("SB") == nil)
    // The one that proved the first version of this rule wrong: three letters
    // dropped leaves two real digits reading as a clean "72", which invented a
    // collector number on a card that prints none. Measured on the corpus.
    #expect(digitsOnly("a7KA2") == nil)
    #expect(digitsOnly("x1y2z3") == nil)
}

@Test("a collector row mangled by one letter still parses")
func mangledRowStillReads() {
    #expect(readPrinting(bandLines: ["n0322"]).number == "322")
    #expect(readPrinting(bandLines: ["R 0322"]).number == "322")
}

@Test("a Fallen Empires creature yields a year and no number")
func preExodusFrame() {
    let p = readPrinting(bandLines: [
        "as normal during your untap phase.",
        "Illus. Amy Weber",
        "©1994 Wizards of the Coast, Inc. All rights reserved",
        "0/1",
    ])
    #expect(p.year == 1994)
    #expect(p.number.isEmpty, "pre-Exodus cards print no collector number")
    #expect(p.setCode.isEmpty, "nor a set code")
    #expect(p.numberSource == .none)
}

// MARK: - Refusals

@Test("prose does not donate a set code")
func proseIsNotASetRow() {
    for line in ["Bury Seasinger if you control no islands.",
                 "Search your library for up to two basic",
                 "as normal during your untap phase.",
                 "onto the battlefield tapped and the"] {
        #expect(readPrinting(bandLines: [line]).setCode.isEmpty, "set code from: \(line)")
    }
}

@Test("a year outside the window is not a year")
func implausibleYears() {
    #expect(readPrinting(bandLines: ["© 1892 Wizards of the Coast"]).year == nil)
    #expect(readPrinting(bandLines: ["© 2099 Wizards of the Coast"]).year == nil)
}

@Test("a bare four-digit number is not a copyright year")
func bareNumberIsNotAYear() {
    // "1999" alone is a collector number on a large set, not a date. Without the
    // company row's fingerprint it must not be read as one.
    let p = readPrinting(bandLines: ["1999"])
    #expect(p.year == nil)
    #expect(p.number == "1999")
}

@Test("an empty band reads as empty rather than as anything")
func emptyBand() {
    #expect(readPrinting(bandLines: []).isEmpty)
    #expect(readPrinting(bandLines: ["", "   "]).isEmpty)
}

// MARK: - The foil marker

@Test("the star between set and language reads as foil")
func foilStarFromSetRow() {
    // Straight from a live capture: a foil Deserted Temple committed as nonfoil
    // because CardKit threw the separator away before anything could look at
    // it. Vision rendered the star as "*", glued to the code with no space.
    let p = readPrinting(bandLines: ["R 0301", "MH3*EN © ROB ALEXANDER"])
    #expect(p.setCode == "MH3")
    #expect(p.finish == "foil", "MH3*EN is a starred, foil printing")
}

@Test("the bullet reads as nonfoil")
func bulletIsNonfoil() {
    for row in ["MSC • EN ALEXANDER SKRIPNIKOV",
                "MSC•EN • ALEXANDER SKRIPNIKOV",
                "MAR• EN OBRIAN STELFREEZE"] {
        #expect(readPrinting(bandLines: [row]).finish == "nonfoil", "from \(row)")
    }
}

@Test("every glyph Vision produces for a star reads as foil")
func starMisreadsAllReadFoil() {
    // The star is small and Vision is inventive about it. These are the forms
    // the macOS path recorded live.
    for sep in ["*", "+", "★", "✦"] {
        let p = readPrinting(bandLines: ["MH3\(sep)EN ROB ALEXANDER"])
        #expect(p.finish == "foil", "separator \(sep) should read foil")
    }
    // And the letter misreads, which only count immediately before a language.
    for letter in ["K", "X", "A", "T"] {
        let p = readPrinting(bandLines: ["MSH \(letter)EN ROB ALEXANDER"])
        #expect(p.finish == "foil", "\(letter)EN is a starred border")
        #expect(p.language == "EN")
    }
}

@Test("a word that merely contains a letter-EN shape is not a foil set row")
func letterMisreadDoesNotEatWords() {
    // "KRAKEN" and "MOLTEN" carry the same shape. Reading them as set rows
    // would boilerplate-kill real cards, which is why the letter form has to
    // sit alone directly before the language.
    for line in ["KRAKEN", "MOLTEN", "SHAKEN THE EARTH"] {
        let p = readPrinting(bandLines: [line])
        #expect(p.finish != "foil", "\(line) must not read as a foil marker")
    }
}

@Test("an old frame with no marker leaves the finish unknown")
func noMarkerStaysUnknown() {
    // Silence is not "nonfoil". Pre-2003 frames print no marker at all, and
    // claiming one would state as read what was never printed.
    let p = readPrinting(bandLines: [
        "Illus. Amy Weber",
        "©1994 Wizards of the Coast, Inc. All rights reserved",
    ])
    #expect(p.finish.isEmpty)
}

// MARK: - The bare number at the tail of a copyright row

@Test("a copyright row ending in a bare number yields that number")
func bareTrailingCollectorNumber() {
    // Live, twice: Marionette Apprentice queued as "printing unverified: 2
    // printings" while its band held a perfectly legible 410. Only the pair
    // form was handled, so this layout read its year and dropped its number.
    for line in ["™M & © 2024 Wizards of the Coast 410",
                 "TM & © 2024 Wizards of the Coast 410"] {
        let p = readPrinting(bandLines: [line, "1/2"])
        #expect(p.number == "410", "from \(line)")
        #expect(p.year == 2024)
        #expect(p.numberSource == .copyrightRow,
                "small italic digits stay upgrade-only evidence")
    }
}

@Test("the copyright year is never mistaken for the number")
func trailingYearIsNotANumber() {
    // The failure this guard exists for: a row whose last token *is* the date.
    for line in ["© 1995 Wizards of the Coast",
                 "Wizards of the Coast 2024",
                 "™ & © 1993-2003 Wizards of the Coast"] {
        #expect(readPrinting(bandLines: [line]).number.isEmpty,
                "\(line) has no collector number to read")
    }
}

@Test("a paired number still wins over the bare form")
func pairFormTakesPrecedence() {
    let p = readPrinting(bandLines: ["© 1993-2003 Wizards of the Coast, Inc. 24/143"])
    #expect(p.number == "24")
    #expect(p.total == 143)
}

@Test("an own-row number is not overwritten by the copyright row")
func ownRowWins() {
    // The band often carries both. The card's own number row is the better
    // evidence and must not be displaced by digits scraped off the credit line.
    let p = readPrinting(bandLines: [
        "R 0301",
        "™M & © 2024 Wizards of the Coast 410",
    ])
    #expect(p.number == "301")
    #expect(p.numberSource == .ownRow)
}

@Test("prose ending in a number does not become a printing")
func proseTailIsNotANumber() {
    // trailingNumber only ever runs on a line the year and company have already
    // identified, so rules text ending in a digit is never offered to it.
    for line in ["Bury Seasinger if you control no islands. 4",
                 "put a +1/+1 counter on it 2"] {
        #expect(readPrinting(bandLines: [line]).number.isEmpty, "from \(line)")
    }
}

@Test("junk fused onto the language still yields the set code")
func languageWithGluedJunk() {
    // Live: a Zahid queued holding a perfect 076/269 because its set row read
    // "DOM•ENIO MAGALI VILLENEUVE" — `DOM • EN` with two characters of the
    // credit glyph fused on. Number 76 matches three printings; only the set
    // code separates them.
    let p = readPrinting(bandLines: [
        "076/269 R",
        "DOM•ENIO MAGALI VILLENEUVE",
        "TN & C 2018 Wizards of the Coast",
    ])
    #expect(p.setCode == "DOM")
    #expect(p.language == "EN")
    #expect(p.number == "76")
    #expect(p.finish == "nonfoil", "the bullet is still the separator")
}

@Test("a name after a word is not a set row")
func gluedLanguageDoesNotEatNames() {
    // The shape the two-character cap exists for. "Illus" is 3-5 alphanumerics
    // with a letter, so it clears the set-code test; the artist must not then
    // be read as a language.
    for line in ["Illus Enrico Sacchetti", "Illus. Espen Grundetjern",
                 "Illus Ittoku", "Illus Deruchenko Alexander"] {
        let p = readPrinting(bandLines: [line])
        #expect(p.setCode.isEmpty, "\(line) read set code \(p.setCode)")
    }
}
