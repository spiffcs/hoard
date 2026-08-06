// What one capture yielded, from Vision's raw lines up to the per-card shape
// the scan event reports.

import BorderKit
import CoreGraphics
import Foundation

// MARK: - OCR

// `Line` and `Quad` moved to BorderKit, which is the one thing the Mac and the
// phone share. They are re-exported here so this file still reads as the
// description of a capture.
/// CardRead is everything one capture yielded: the title guess and its alternates,
/// plus whatever the bottom border gave up. lines keeps the ranked plausible-name
/// lines with their geometry, for the multi-card clustering that runs after the
/// single-card read.
struct CardRead {
    var name: String = ""
    var candidates: [String] = []
    var collectorNumber: String = ""
    var setCode: String = ""
    var bottomLines: [String] = []
    var lines: [Line] = []
    /// The bottom-band pass's lines with their geometry, mapped back into
    /// whole-frame coordinates. The band aims at exactly where the footer is,
    /// so it reads the copyright and credit rows on frames where the
    /// whole-frame title pass misses them entirely — which is most of what the
    /// border reader was failing to anchor on.
    var bandLines: [Line] = []
    /// Vision's confidence in the line chosen as name.
    var nameConfidence: Float = 0
    /// Whether the collector band was anchored to a detected card rectangle.
    var bandAnchored: Bool = false
    /// Collector blocks beyond the primary one — a stacked card's sliver, or a
    /// second plausible read.
    var collectorAlts: [CollectorRead] = []
    /// The finish the primary block's separator marked: "foil" (printed star),
    /// "nonfoil" (bullet), or "" when the frame carries no marker.
    var finishHint = ""
    /// Whether collectorNumber was read in "n/total" form; see
    /// CollectorRead.pair. The crop channel needs it to tell a real number
    /// with an unreadable set line from a bare digit off the card face.
    var collectorPair = false
    /// A collector number read off the tail of the copyright line — the only
    /// place pre-8th-edition frames print one ("™ & © 1993-2003 Wizards of the
    /// Coast, Inc. 95/350", tiny italic serif). Kept apart from collectorNumber
    /// on purpose: the flat event fields must stay band-only, because a parent
    /// that predates provenance treats any flat number as trusted, and this
    /// glyph size misreads digits often enough that a copyright read may only
    /// ever upgrade a match, never veto one.
    var copyrightNumber = ""
    /// End year of the copyright range on the same line ("1993-2003" → 2003),
    /// which equals the printing's release year. 0 when unread.
    var copyrightYear = 0
}

