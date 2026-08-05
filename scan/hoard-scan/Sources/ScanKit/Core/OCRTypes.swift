// What one capture yielded, from Vision's raw lines up to the per-card shape
// the scan event reports.

import CoreGraphics
import Foundation

// MARK: - OCR

/// A recognized text line with its full normalized bounding box (Vision origin
/// is bottom-left) and confidence. The box matters beyond ranking: multi-card
/// clustering needs horizontal position to tell which card a line belongs to,
/// which top-and-width alone cannot say.
struct Line {
    let text: String
    let box: CGRect
    let confidence: Float
    /// The line's four corners. Vision hands these over already —
    /// VNRecognizedTextObservation is a VNRectangleObservation — and the axis-
    /// aligned box above throws away the one thing they add: how far the text
    /// is turned. The border reader needs that, because a tilted card's border
    /// ring is not a horizontal strip. nil only for lines built by hand.
    var quad: Quad? = nil

    var top: CGFloat { box.maxY }
    var width: CGFloat { box.width }
}

/// Quad is a Vision rectangle's corners in normalized frame coordinates
/// (origin bottom-left), kept apart from CGRect because the whole point is
/// that it need not be axis-aligned.
struct Quad {
    let topLeft: CGPoint
    let topRight: CGPoint
    let bottomLeft: CGPoint
    let bottomRight: CGPoint
}

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

/// CardEntry is one card of a capture, as the scan event reports it: the title
/// guess with alternates, and collector info when a crop could read it. The
/// per-card shape exists because pooling a frame's reads cross-pairs cards —
/// one card's name with another's printing — observed live before this shape
/// existed, when a fanned capture emitted the top-most title beside the front
/// card's collector number.
struct CardEntry: Encodable {
    var name: String = ""
    var candidates: [String] = []
    var collectorNumber: String = ""
    var setCode: String = ""
    /// Vision's confidence in this entry's title read.
    var confidence: Float = 0
    /// Which channel produced the entry: "crop" (a perspective-corrected card
    /// rectangle — collector info, when present, is card-anchored by
    /// construction) or "frame" (the frame-wide title pass, which never carries
    /// collector info).
    var source: String = ""
    /// Collector blocks beyond the primary one, so the caller can keep the
    /// number that actually matches a printing when a stacked card's border
    /// shares the band.
    var collectorAlts: [CollectorRead]? = nil
    /// The primary block's printed finish marker; see CollectorRead.finish.
    var finishHint: String = ""
    /// Where collectorNumber came from: nil/"" is the trusted collector band,
    /// "copyright" the old-frame copyright-line tail (upgrade-only evidence —
    /// see CardRead.copyrightNumber). Optional so events keep their old wire
    /// shape when no copyright read happened.
    var numberSource: String? = nil
    /// End year of the copyright range, when one was read; see
    /// CardRead.copyrightYear.
    var copyrightYear: Int? = nil
    /// The printed border, "white" or "black", when it could be read off the
    /// card's own edge. Absent means the reader declined — never "" and never
    /// "unknown", because absence is the honest shape for "no evidence" and an
    /// empty string invites a downstream `!= ""` that treats it as one.
    ///
    /// Optional so a capture that reads no border keeps the old wire shape
    /// byte-for-byte. Deliberately not mirrored onto the flat Event fields,
    /// for the reason numberSource exists: a parent that predates provenance
    /// treats anything flat as trusted.
    var borderColor: String? = nil
    /// How the border was established: "footer+ring" when the opposite edge
    /// agreed, "footer" when only one edge was in shot. The Go side may hold
    /// the weaker one to a lower bar.
    var borderSource: String? = nil
}
