// Pre-M15 frames hide their collector number in the copyright line, which is
// the only printing evidence an old card carries besides its border.

import BorderKit
import Foundation

// The copyright line of pre-M15 frames, where old frames hide their collector
// number. Two shapes matter: the range whose END year equals the printing's
// release year ("1993-2003" — 8th Edition, 2003; its 7th Edition sibling says
// "1993-2001"), and the collector pair at the line's tail ("… Wizards of the
// Coast, Inc. 95/350"). copyrightYearRE moved to BorderKit with the footer
// predicates that use it.
let copyrightTailPairRE = try! NSRegularExpression(
    pattern: #"(\d{1,5})\s*/\s*(\d{1,5})\s*[.,]?\s*$"#)
/// A lone copyright year, for the modern frame's "© 2024 Wizards of the Coast".
/// The modern frame's version of the collector tail: one number, no total
/// ("™ & © 2024 Wizards of the Coast 418").
///
/// Tied to the brand word rather than merely anchored to the line end, and that
/// is the whole safety of it. A free-floating tail match harvested "350" — the
/// set *total* of a half-read "143/350" — and "14" off a truncated number,
/// both measured against a live session. Only punctuation and space may sit
/// between "COAST" and the number, which is the shape the modern frame
/// actually prints and nothing else is.
let copyrightTailSoloRE = try! NSRegularExpression(
    pattern: #"COAST[^0-9A-Z]{0,4}(\d{1,5})\s*[.,]?\s*$"#)



/// parseCopyrightCollector pulls the old-frame collector evidence out of a
/// capture's text: the pair at the tail of a copyright line (total ≥ 20, the
/// same guard that keeps a P/T out of the band parse — "Coast, Ine: 30/1"
/// dies here) and the range's end year. Number and year are independent
/// finds: a fragment may carry one without the other, and the year matters
/// even beside a trusted band number — it is what breaks a collector-number
/// tie between two printings ("95" is both 7th and 8th Edition; only one was
/// printed in 2003).
func parseCopyrightCollector(_ texts: [String]) -> (number: String, year: Int)? {
    var number = ""
    var year = 0
    for raw in texts {
        guard copyrightFurniture(raw) else { continue }
        let line = asciify(raw)
        if year == 0, let y = group(copyrightYearRE, line), let n = Int(y),
           n >= 1993, n <= 2035 {
            year = n
        }
        // Before collector numbers existed the copyright line was a lone year:
        // "© 1995 Wizards of the Coast, Inc. All rights reserved." The range
        // regex above needs two years and finds nothing there, so the whole
        // era shipped with no year at all — even off a pristine scan that read
        // the line perfectly. The year is the only printing evidence those
        // cards carry, so take it when there is no range to prefer.
        if year == 0, let y = group(copyrightLoneYearRE, line), let n = Int(y),
           n >= 1993, n <= 2035 {
            year = n
        }
        if number.isEmpty,
           let n = group(copyrightTailPairRE, line),
           let total = group(copyrightTailPairRE, line, 2), (Int(total) ?? 0) >= 20 {
            number = normalizeNumber(n)
        }
        // Modern frames print the number alone, with no set total to vouch for
        // it ("… Wizards of the Coast 418", observed live on Meltdown and a
        // Snow-Covered Wastes). Weaker than the pair, and it does not need to
        // be strong: this rides the wire as numberSource "copyright", which the
        // Go side may only ever upgrade a match with, never veto one.
        if number.isEmpty, let n = group(copyrightTailSoloRE, line), !looksLikeAYear(n) {
            number = normalizeNumber(n)
        }
        if !number.isEmpty && year != 0 { break }
    }
    if number.isEmpty && year == 0 { return nil }
    return (number, year)
}

/// applyCopyrightCollector folds a copyright-line read into a CardRead. A
/// trusted band number always wins the number slot — the copyright read is
/// the fallback for frames whose band gave nothing — but the year rides along
/// regardless, since it is evidence about the printing either way.
func applyCopyrightCollector(_ read: inout CardRead, _ texts: [String]) {
    guard let hit = parseCopyrightCollector(texts) else { return }
    read.copyrightYear = hit.year
    if read.collectorNumber.isEmpty {
        read.copyrightNumber = hit.number
    }
}
