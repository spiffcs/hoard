// hoard-scan — capture a Magic card image and OCR its title.
//
// Modes:
//   hoard-scan --list-devices   Print the available cameras as JSON, exit.
//   hoard-scan --image <path>   Headless: OCR an existing image file, print JSON, exit.
//   hoard-scan [--device <id>] [--rotate <deg>]
//                               Live session: open a camera preview window and keep
//                               it open, emitting one JSON event per line on stdout
//                               and reading commands (capture / rotate-left /
//                               rotate-right / frame-on / frame-off / torch-on /
//                               torch-off / effects / result {json} / quit) as
//                               lines on stdin. Space, ←/→, Z, T, V and Esc do the
//                               same things in the window itself.
//   hoard-scan --hud-demo       Live window with no camera, for eyeballing the
//                               price HUD: pipe `result {...}` lines on stdin.
//
// The window persisting across captures is the point: relaunching the helper per
// card costs a camera warm-up each time, and forces the user back to a keystroke
// in the terminal to reopen it.
//
// Capture is Continuity Camera (iPhone) only — webcams are never used; see
// availableCameras() for why.
//
// Output is newline-delimited JSON; see Event for the shapes.
// For --list-devices: {"devices": [{"id": "...", "name": "...", "kind": "..."}]}
//
// The Go side (internal/scan) manages this process and parses those events.

import AVFoundation
import AppKit
import CoreImage
import ImageIO
import Vision

// MARK: - JSON output

/// Event is one newline-delimited JSON message on stdout. The live session emits
/// a stream of these rather than a single object, because the window stays open
/// across many captures.
///
///   {"event":"ready","device":"…","rotation":90}   window is up and previewing
///   {"event":"scan","name":"…","candidates":[…]}   a capture was read
///   {"event":"rotation","rotation":180}            user turned the preview
///   {"event":"framing","state":"auto"}             auto-framing toggled; see state
///   {"event":"torch","state":"on"}                 phone torch toggled; see state
///   {"event":"error","message":"…"}                capture failed; session lives
///   {"event":"closed","rotation":90}               window closed; process exits
struct Event: Encodable {
    var event: String
    var name: String = ""
    var candidates: [String] = []
    /// The manual rotation currently in effect, so the caller can remember it
    /// and hand it back via --rotate next time.
    var rotation: Int = 0
    var message: String = ""
    var device: String = ""
    /// Read off the card's bottom border when present. Cards printed before
    /// Exodus (1998) carry no collector number at all, and the set code only
    /// became reliably printed with the M15 frame, so both are routinely empty
    /// and an empty read is ordinary rather than a failure.
    var collectorNumber: String = ""
    var setCode: String = ""
    /// Raw text of the bottom band, for tuning the read via --image.
    var bottomLines: [String] = []
    /// Every card the capture found, in reading order — a fanned spread yields
    /// one entry per card. Optional so events that carry no cards keep their
    /// old wire shape byte-for-byte; the flat fields above stay populated from
    /// the frame-wide read, so an older hoard keeps working against this
    /// helper.
    var cards: [CardEntry]? = nil
    /// Vision's confidence (0–1) in the line chosen as the title. Optional so
    /// events that carry no read keep their old wire shape; an older hoard
    /// ignores it.
    var confidence: Float? = nil
    /// Whether the collector band was anchored to a detected card rectangle
    /// (true) or fell back to the frame's lower half (false). An anchored band
    /// is the only one whose collector read deserves trust.
    var bandAnchored: Bool? = nil
    /// True when this scan was fired by the auto trigger rather than a capture
    /// command or the space key.
    var auto: Bool? = nil
    /// Capabilities this helper supports, advertised on the ready event so the
    /// parent can feature-detect instead of probing with commands.
    var features: [String]? = nil
    /// Auto-trigger state, carried by "auto" events only.
    var state: String? = nil
    /// Collector blocks beyond the primary flat fields — see CardEntry.
    var collectorAlts: [CollectorRead]? = nil
    /// The primary block's printed finish marker; see CollectorRead.finish.
    var finishHint: String? = nil
}

/// HUDCommand is the payload of the `result` stdin verb — the Go side's
/// scan.HUDResult. A tier means flash it with the tier's styling and sound;
/// a total means update the persistent session counter, always silently.
/// Amount is absent on an unpriced card, and always absent on the review
/// tier, which renders as "Needs Review" rather than a price.
struct HUDCommand: Decodable {
    var amount: Double?
    var tier: String? // bulk | win | jackpot | unpriced | review
    var total: Double?
}

/// Device is one camera the helper can capture from, as listed by --list-devices.
struct Device: Encodable {
    var id: String
    var name: String
    var kind: String
}

struct DeviceList: Encodable {
    var devices: [Device]
}

/// emit writes one JSON line to stdout, unbuffered. It must go straight to the
/// file handle rather than through print(): stdout is a pipe here, so buffered
/// writes would sit unflushed and the parent would block waiting for an event
/// that has already happened.
func emit<T: Encodable>(_ out: T) {
    guard let data = try? JSONEncoder().encode(out) else { return }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data("\n".utf8))
}

func fail(_ message: String, code: Int32 = 1) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(code)
}

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

/// collectorBandFraction is how far up the *card* the collector band reaches, as a
/// fraction of the card's own height.
///
/// The card's own height is the only sane reference. An earlier version measured
/// the band against the frame instead, which fails the moment the card doesn't
/// reach the bottom of the frame: a card resting mid-frame with desk below it puts
/// its collector block ~38% up, well outside any plausible frame-relative band, and
/// the read comes back completely empty. See findCard.
///
/// 0.15 covers the collector block, which lives in the bottom ~6%, with enough room
/// left over for the tilt of a hand-held card. It does not reliably exclude the
/// lowest line of rules text or a creature's power/toughness — past roughly 8° of
/// turn those come along too — so parseCollectorInfo, not this band, is what has to
/// tell a collector number from a "2/2" printed above it.
let collectorBandFraction: CGFloat = 0.15

/// frameBandFallback is the frame-relative band used only when no card rectangle
/// could be found. It is deliberately the whole lower half: without card bounds
/// there is no way to know where the border sits, and a band that is too tall
/// merely adds lines for the patterns below to reject, while one that is too short
/// silently reads nothing at all.
let frameBandFallback: CGFloat = 0.5

// Collector info as printed. "123/264" is the common form; the solo form covers
// cards that print the number without a set total, optionally with a trailing
// rarity letter. The set/language pair is the M15-frame second line, e.g. "MH3 • EN".
//
// The separator in the set/language line is matched loosely: at this glyph size
// Vision reports the printed bullet as "-", ".", "|" and friends about as often as
// "•", so requiring a real bullet drops perfectly good reads. A bare space counts
// too.
//
// What keeps that looseness safe is the trailing token: it must be one of the
// language codes Magic actually prints. Accepting any two letters there matches
// ordinary prose — "cards equal to the sacrificed" uppercases to a tidy
// "EQUAL TO" and yields a set code of "EQUAL".
let cardLanguages = "EN|DE|FR|IT|ES|PT|JA|JP|KO|RU|ZH|ZHS|ZHT|CS|CT|HE|LA|AR|SA|PH"
let collectorPairRE = try! NSRegularExpression(pattern: #"(\d{1,5})\s*/\s*(\d{1,5})"#)
// The rarity letter may trail the number (M15 frames: "330 R") or lead it
// (Marvel frames: "R 0330", where mythic's M also arrives as Cyrillic М until
// asciify folds it).
let collectorSoloRE = try! NSRegularExpression(pattern: #"^[A-Z]?\s*#?\s*(\d{1,5})\s*[A-Z]?$"#)
// The separator is captured, not skipped: modern frames print it as the foil
// marker — a star (★) on foil printings, a plain bullet (•) on nonfoil ones.
// Vision renders the star as "*", "+", and at this glyph size even as a
// letter (K, X, A, T — all observed live). Letter misreads get their own
// alternation that REQUIRES leading whitespace: "MSH KEN" is a starred
// border, but "KRAKEN" and "MOLTEN" contain the same letter-EN shape with no
// space, and matching those would boilerplate-kill real card titles. Older
// frames carry no marker; a bare-space match leaves both groups empty and
// the finish unknown.
let setLangRE = try! NSRegularExpression(
    pattern: #"\b([0-9A-Z]{3,5})(?:\s*([•·∙*★✦✧✶+.,:;|/\\―—–-])\s*|\s+([KXAT])\s*|\s+)(?:"#
        + cardLanguages + #")\b"#)

/// finishFromSeparator classifies a set/language separator glyph: star-shaped
/// reads (including the letter misreads) mean the printed foil marker,
/// dot-shaped ones the nonfoil bullet, and anything else (or nothing) stays
/// unknown rather than guessed.
func finishFromSeparator(_ sep: String) -> String {
    if sep.isEmpty { return "" }
    if "★✦✧✶*+KXAT".contains(sep) { return "foil" }
    if "•·∙.,:;|/\\―—–-".contains(sep) { return "nonfoil" }
    return ""
}

/// confusables maps the non-ASCII lookalikes Vision returns for this text to the
/// ASCII the patterns expect. With language correction off and glyphs barely 1% of
/// the frame tall, it will happily report a set code as "MHЗ" with a Cyrillic З, or
/// a Greek Ο for a zero — which then fails an [0-9A-Z] match for reasons invisible
/// in the emitted bottomLines.
let confusables: [Character: Character] = [
    // Cyrillic
    "А": "A", "В": "B", "Е": "E", "З": "3", "К": "K", "М": "M", "Н": "H", "О": "O",
    "Р": "P", "С": "C", "Т": "T", "У": "Y", "Х": "X", "І": "I", "Ѕ": "S", "Ј": "J",
    // Greek
    "Α": "A", "Β": "B", "Ε": "E", "Ζ": "Z", "Η": "H", "Ι": "I", "Κ": "K", "Μ": "M",
    "Ν": "N", "Ο": "O", "Ρ": "P", "Τ": "T", "Υ": "Y", "Χ": "X",
    // Fullwidth / typographic digits and slashes
    "０": "0", "１": "1", "２": "2", "３": "3", "４": "4", "５": "5", "６": "6",
    "７": "7", "８": "8", "９": "9", "⁄": "/", "∕": "/", "／": "/",
]

/// asciify folds lookalike glyphs to ASCII and uppercases the result, so the
/// patterns can stay strict about shape without also being strict about which
/// codepoint Vision happened to pick. Uppercasing is what lets a lowercase read
/// ("mh3 • en") still yield a set code.
func asciify(_ s: String) -> String {
    String(s.uppercased().map { confusables[$0] ?? $0 })
}

/// group returns a capture group of the first match, if any.
func group(_ re: NSRegularExpression, _ s: String, _ n: Int = 1) -> String? {
    let full = NSRange(s.startIndex..., in: s)
    guard let m = re.firstMatch(in: s, range: full), m.numberOfRanges > n,
          let r = Range(m.range(at: n), in: s) else { return nil }
    return String(s[r])
}

/// normalizeNumber drops the zero padding cards are printed with ("0123/0281"),
/// since Scryfall stores collector numbers unpadded.
func normalizeNumber(_ s: String) -> String {
    let trimmed = s.drop(while: { $0 == "0" })
    return trimmed.isEmpty ? "0" : String(trimmed)
}

/// looksLikeAYear reports whether a bare number is really a printing year. Every
/// card carries a copyright year in the same block as the collector number, and on
/// its own line it is indistinguishable from a collector number by shape alone.
/// Magic has no four-digit collector numbers in this range, so the range is safe
/// to exclude outright.
func looksLikeAYear(_ s: String) -> Bool {
    guard s.count == 4, let n = Int(s) else { return false }
    return n >= 1993 && n <= 2035
}

/// lowercaseCount is how many lowercase letters a line holds, which is how the
/// collector block is told apart from rules text. The border block is set in caps,
/// digits and small caps, so it reads as all-uppercase; prose does not. It matters
/// because a card's rules text can carry a collector-number shape of its own —
/// "Create a 2/2 white Cat creature token" — and the band cannot always exclude it.
func lowercaseCount(_ s: String) -> Int {
    s.filter { $0.isLowercase }.count
}

/// setLangFurniture gates which lines may yield a set code. The set/language
/// line is border print — "MSC ★ EN", "MH3 • EN I TOMAS HONZ" — set in caps,
/// so it carries almost no lowercase. Rules text does, and because setLangRE
/// tolerates a bare space between the code and the language, ordinary prose
/// matches it constantly once asciify uppercases everything: "…and put it into
/// your hand" yields set PUT, language Italian. Three captures of Eternal
/// Dragon shipped `PUT` that way in one live session, and a wrong set code is
/// the failure that stays invisible until valuation.
///
/// Only the extraction is gated. boilerplate still uses the same regex to kill
/// lines, where a generous match is the safe direction.
func setLangFurniture(_ s: String) -> Bool {
    let letters = s.filter { $0.isLetter }.count
    guard letters > 0 else { return true }
    return lowercaseCount(s) * 4 <= letters
}

/// CollectorRead is one parsed border block: a collector number, the set code
/// printed beside it, and the finish the set line's separator marked — "foil"
/// for the printed star, "nonfoil" for the bullet, "" when the frame carries
/// no marker.
struct CollectorRead: Encodable {
    var number = ""
    var set = ""
    var finish = ""
    /// Whether the number was read in "n/total" form. A bare number shares its
    /// shape with a mana cost and a power box; a pair with a plausible total
    /// does not, so the crop channel can trust one and not the other. Local
    /// only — the wire shape stays what parent binaries already parse.
    var pair = false

    enum CodingKeys: String, CodingKey {
        case number, set, finish
    }
}

/// parseCollectorInfo pulls every collector-number candidate out of the bottom
/// band's text. That covers both places the number appears — the bottom-left
/// block on M15-frame cards (2014 onward) and the bottom centre on older ones —
/// and, crucially, more than one card's border at once: a card scanned off the
/// top of a stack shows a sliver of the card beneath it, whose block parses
/// exactly as well as the target's (observed live). Reporting all candidates
/// lets the caller keep the one that matches a real printing.
///
/// `lines` should arrive bottom-most first. Candidates are ranked by how little
/// prose they contain, falling back to that bottom-up order for ties, so real
/// border blocks outrank rules text that merely looks like them. Each number is
/// paired with the set code read nearest it in the band — the two lines of a
/// border block arrive adjacent.
func parseCollectorInfo(_ lines: [String]) -> [CollectorRead] {
    let ranked = lines.enumerated()
        .sorted { a, b in
            let (la, lb) = (lowercaseCount(a.element), lowercaseCount(b.element))
            return la == lb ? a.offset < b.offset : la < lb
        }
        .map { (offset: $0.offset, text: asciify($0.element), raw: $0.element) }

    var sets: [(offset: Int, code: String, finish: String)] = []
    for l in ranked {
        // asciify has already folded the case away, so the prose test has to
        // run against what Vision actually read.
        guard setLangFurniture(l.raw) else { continue }
        if let s = group(setLangRE, l.text) {
            // The separator lands in group 2 (symbols) or 3 (letter misreads
            // of the star); whichever matched carries the finish.
            let sep = group(setLangRE, l.text, 2) ?? group(setLangRE, l.text, 3) ?? ""
            sets.append((l.offset, s, finishFromSeparator(sep)))
        }
    }
    func setNear(_ offset: Int) -> (code: String, finish: String) {
        // The number line and its set line print adjacently, but the band's
        // bottom-up ordering interleaves them with the right column's
        // copyright lines — so "near" has to reach a few indices, with
        // nearest-wins keeping a stacked neighbour's set from stealing in.
        let nearest = sets.min { abs($0.offset - offset) < abs($1.offset - offset) }
        guard let nearest, abs(nearest.offset - offset) <= 4 else { return ("", "") }
        return (nearest.code, nearest.finish)
    }

    var reads: [CollectorRead] = []
    var seen = Set<String>()
    func add(_ offset: Int, _ n: String, pair: Bool) {
        let near = setNear(offset)
        let read = CollectorRead(number: normalizeNumber(n), set: near.code,
                                 finish: near.finish, pair: pair)
        let key = read.number + "/" + read.set
        if !seen.contains(key) {
            seen.insert(key)
            reads.append(read)
        }
    }
    // Pair form first: it is the harder shape to fake. A creature's
    // power/toughness reads exactly like a collector pair and shares the band
    // on frames whose stat box sits low (observed live: "2/2" became collector
    // number 2); what separates them is the total — no printed set counts
    // fewer than 20 cards — with the numerator width of zero-padded prints
    // ("0087/0383") as a second signal.
    for l in ranked {
        if let n = group(collectorPairRE, l.text), let total = group(collectorPairRE, l.text, 2),
           (Int(total) ?? 0) >= 20 || n.count >= 3 {
            add(l.offset, n, pair: true)
        }
    }
    // Bare numbers after: a lone number is much easier to confuse with a
    // planeswalker's loyalty or a copyright year.
    for l in ranked {
        let t = l.text.trimmingCharacters(in: .whitespaces)
        if let n = group(collectorSoloRE, t), !looksLikeAYear(n) {
            add(l.offset, n, pair: false)
        }
    }
    return Array(reads.prefix(4))
}

// The copyright line of pre-M15 frames, where old frames hide their collector
// number. Two shapes matter: the range whose END year equals the printing's
// release year ("1993-2003" — 8th Edition, 2003; its 7th Edition sibling says
// "1993-2001"), and the collector pair at the line's tail ("… Wizards of the
// Coast, Inc. 95/350"). The dash arrives as "-", "–", "—" or, at this glyph
// size, a bare space — hence the optional separator.
let copyrightYearRE = try! NSRegularExpression(
    pattern: #"(?:19|20)\d{2}\s*[-–—]?\s*((?:19|20)\d{2})"#)
let copyrightTailPairRE = try! NSRegularExpression(
    pattern: #"(\d{1,5})\s*/\s*(\d{1,5})\s*[.,]?\s*$"#)
/// A lone copyright year, for the modern frame's "© 2024 Wizards of the Coast".
let copyrightLoneYearRE = try! NSRegularExpression(pattern: #"\b((?:19|20)\d{2})\b"#)
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

/// copyrightFurniture reports whether a line is (a fragment of) the bottom
/// copyright line — "™ & © 1993-2003 Wizards of the Coast, Inc. 95/350" and
/// the OCR manglings thereof ("te Coast, Inc", "Coast, Ine: 30/1", "1993-2003
/// Wizar"). Two signals are required: a brand token (coast, or a wizar…
/// prefix — Vision truncates the word) plus corroboration (©/™, an inc-shaped
/// token, a year range, or a collector pair), so that real titles sharing a
/// token — "Coast Watcher", "Wizard's Retort" — survive.
func copyrightFurniture(_ s: String) -> Bool {
    let tokens = s.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber })
    let brand = tokens.contains("coast") || tokens.contains(where: { $0.hasPrefix("wizar") })
    guard brand else { return false }
    if s.contains("©") || s.contains("™") { return true }
    if tokens.contains(where: { ["inc", "ine", "in", "ir", "lnc"].contains($0) }) { return true }
    let ascii = asciify(s)
    if group(copyrightYearRE, ascii) != nil { return true }
    if group(collectorPairRE, ascii) != nil { return true }
    // Old frames print a range ("1993-2003"); modern ones print a single year
    // ("™ & © 2024 Wizards of the Coast 418"). Requiring the range rejected
    // every modern copyright line outright, which cost both the release year
    // and the collector number printed beside it — and the ™/© that would
    // otherwise vouch for the line comes back as "Iм & C" at this glyph size.
    if let y = group(copyrightLoneYearRE, ascii), let n = Int(y), n >= 1993, n <= 2035 {
        return true
    }
    return false
}

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

/// findCard locates the card in the frame, so the collector band can be anchored
/// to the card's own bottom edge instead of the frame's.
///
/// Returns nil when nothing card-shaped stands out — a card on a same-coloured
/// surface, or one held at too steep an angle — which is the cue to fall back to
/// frameBandFallback.
func findCard(_ cg: CGImage) -> VNRectangleObservation? {
    let req = VNDetectRectanglesRequest()
    // A Magic card is 63x88mm, so 0.716 dead on. The tolerance either side absorbs
    // the perspective foreshortening of a hand-held phone; the helper never asks
    // the user to square the card up.
    req.minimumAspectRatio = 0.55
    req.maximumAspectRatio = 0.9
    // A framed card dominates the shot. This rejects specks and, more usefully,
    // the rectangles inside the card — the art box and the text box.
    req.minimumSize = 0.15
    req.minimumConfidence = 0.5
    req.quadratureTolerance = 25
    req.maximumObservations = 10

    do {
        try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req])
    } catch {
        return nil
    }
    // The tallest candidate is the card itself rather than one of its inner boxes.
    return (req.results ?? []).max { $0.boundingBox.height < $1.boundingBox.height }
}

/// collectorBand returns the region of interest to search for collector info: the
/// frame up to a ceiling set just above the detected card's bottom border, or the
/// frame's lower half when no card could be located. anchored reports which of
/// the two it was — a card-anchored band is the only one whose collector read
/// deserves trust downstream.
func collectorBand(_ cg: CGImage) -> (band: CGRect, anchored: Bool) {
    guard let card = findCard(cg) else {
        return (CGRect(x: 0, y: 0, width: 1, height: frameBandFallback), false)
    }
    // Work from the corner points, not the axis-aligned bounding box. A card is
    // never perfectly square to the camera, and for a tilted one the bounding box
    // bottom is its lowest *corner* — below the collector text, which runs on the
    // same tilt. The band therefore has to span the tilt as well as reach up the
    // card, or it misses the text entirely at around 8° of turn.
    let top = max(card.bottomLeft.y, card.bottomRight.y)
    // The card's height along its own edge, so the fraction stays a fraction of
    // the card however it is turned.
    let edge = hypot(card.topLeft.x - card.bottomLeft.x,
                     card.topLeft.y - card.bottomLeft.y)
    // Pad a little: the detected edge can sit just inside the printed border, and
    // the collector text runs very close to that border.
    let pad: CGFloat = 0.01

    // Only the *top* of the band is anchored to the card. It runs to the frame's
    // bottom edge and full width because whatever lies below and beside the card is
    // desk, which costs nothing to include and keeps this a superset of the region
    // a frame-relative band would have covered. Vision's recognition of text this
    // small is sensitive to the shape of the region it is given, and the wider strip
    // reads marginal borders more reliably than a tight crop does.
    let height = top + edge * collectorBandFraction + pad
    return (CGRect(x: 0, y: 0, width: 1, height: min(1, height)), true)
}

/// readCard runs Vision text recognition on a CGImage and returns the best guess
/// at the card's title, a few alternate lines, and the collector info printed
/// along the bottom border.
///
/// The image must already be upright: callers bake any EXIF orientation into the
/// pixels (see uprighted) before applying their own rotation. Passing an
/// orientation here as well would apply two rotations, which lands the title at
/// the bottom and makes the ranking below pick rules text instead.
func readCard(_ cg: CGImage) -> CardRead {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    // Pinning the language skips Vision's language identification. Card titles
    // with diacritics (Juzám Djinn, Lim-Dûl) still read under en-US script
    // handling — verified by fixture replay when this landed.
    request.recognitionLanguages = ["en-US"]

    // A second pass over the card's bottom border only. Language correction is off
    // here and that is not incidental: with it on, Vision "corrects" "123/264" and
    // set codes like "MH3" into dictionary words, which is the quietest possible
    // way for this to stop working.
    let bottom = VNRecognizeTextRequest()
    bottom.recognitionLevel = .accurate
    bottom.usesLanguageCorrection = false
    bottom.recognitionLanguages = ["en-US"]
    // Normalized, origin bottom-left. The frame is already upright and
    // rotation-normalized by this point, so the band is stable across orientations.
    // (A fixed bottom-fraction band for crops — skipping collectorBand's rect
    // detection — was tried and dropped: Vision's read is sensitive to the exact
    // band shape, and the ocr-mangle fixture's set code flipped MSH→MSC.)
    let (band, bandAnchored) = collectorBand(cg)
    bottom.regionOfInterest = band

    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    do {
        try handler.perform([request, bottom])
    } catch {
        return CardRead()
    }

    var read = CardRead()
    read.bandAnchored = bandAnchored

    // Bottom band first: it stands alone, so a title failure doesn't cost us the
    // collector number. Sorted bottom-most first, which is what parseCollectorInfo
    // relies on to prefer the border block over anything printed above it.
    let bottomLines = (bottom.results ?? [])
        .compactMap { obs -> (CGFloat, String)? in
            guard let cand = obs.topCandidates(1).first else { return nil }
            let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : (obs.boundingBox.minY, t)
        }
        .sorted { $0.0 < $1.0 }
        .map { $0.1 }
    read.bottomLines = bottomLines
    // Vision reports a region-of-interest request's boxes normalized to the
    // ROI, not to the image, so they have to be mapped back before they can be
    // compared with anything from the whole-frame pass. The band always spans
    // the full width from y=0, so the mapping is a scale of y alone.
    // Verified by measurement: the copyright row lands at 0.9375 of card
    // height mapped, and at 0.61 unmapped, on frames where both passes read it.
    read.bandLines = (bottom.results ?? []).compactMap { obs -> Line? in
        guard let cand = obs.topCandidates(1).first else { return nil }
        let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        let b = obs.boundingBox
        let mapped = CGRect(x: b.minX, y: b.minY * band.height,
                            width: b.width, height: b.height * band.height)
        func up(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x, y: p.y * band.height) }
        return Line(text: t, box: mapped, confidence: cand.confidence,
                    quad: Quad(topLeft: up(obs.topLeft), topRight: up(obs.topRight),
                               bottomLeft: up(obs.bottomLeft), bottomRight: up(obs.bottomRight)))
    }
    let collectorReads = parseCollectorInfo(bottomLines)
    read.collectorNumber = collectorReads.first?.number ?? ""
    read.setCode = collectorReads.first?.set ?? ""
    read.finishHint = collectorReads.first?.finish ?? ""
    read.collectorPair = collectorReads.first?.pair ?? false
    read.collectorAlts = Array(collectorReads.dropFirst())

    var lines: [Line] = []
    for obs in request.results ?? [] {
        guard let cand = obs.topCandidates(1).first else { continue }
        let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { continue }
        lines.append(Line(text: t, box: obs.boundingBox, confidence: cand.confidence,
                          quad: Quad(topLeft: obs.topLeft, topRight: obs.topRight,
                                     bottomLeft: obs.bottomLeft, bottomRight: obs.bottomRight)))
    }
    if lines.isEmpty {
        applyCopyrightCollector(&read, bottomLines)
        return read
    }

    // The card name is the top-most reasonably-wide, confident line. Rank the
    // upper portion of the card by position, preferring wider/high-confidence text.
    let ranked = lines.sorted { a, b in
        // Primary: higher on the card first.
        if abs(a.top - b.top) > 0.04 { return a.top > b.top }
        // Tie-break: wider (titles span most of the card) then more confident.
        if abs(a.width - b.width) > 0.05 { return a.width > b.width }
        return a.confidence > b.confidence
    }

    let plausible = ranked.filter { plausibleName($0.text) }
    let names = plausible.map { $0.text }
    // Prefer a line that reads like a title. Taking the top plausible line
    // outright let an old frame's copyright tail become the card's name at
    // full confidence — "008 Wizards of the Coast, Iac. 15/145", which the
    // multi trace had already logged as not title-like (observed live).
    // Boilerplate can never be the name: a frame that read nothing but its
    // own border has no title, and "" queues the card as unidentified, which
    // is honest. The middle fallback is what keeps single-word titles
    // (Ponder) working — titleLike rejects those by design.
    // A title-shaped line at the card's *foot* is furniture, and preferring
    // title-shaped lines is what let it through. The 8th Edition frame draws a
    // paintbrush where every earlier frame writes "Illus.", so its credit
    // arrives as a bare "Pete Venters" — two Title Case words, which no string
    // rule can tell from a card name — while the real title, "Tremor", is a
    // single word and titleLike rejects those by design. The credit therefore
    // won outright, and a live 8th Edition pile resolved as its own artists.
    //
    // Geometry settles what the string cannot, exactly as the flavour-credit
    // rule already does: a title sits at the top of whatever text was read, so
    // a candidate in the bottom of that span loses its title-shaped privilege
    // and the topmost readable line is preferred instead. Only the *privilege*
    // is withdrawn — such a line can still be the name when it is all there is,
    // which is what keeps a lone credit-shaped title working.
    let tops = plausible.map { $0.top }
    let lowest = tops.min() ?? 0, highest = tops.max() ?? 1
    let footLine = lowest + (highest - lowest) * 0.35
    let upper = plausible.filter { $0.top >= footLine || plausible.count < 3 }
    let upperNames = upper.map { $0.text }
    let primary = upperNames.first(where: { titleLike($0) })
        ?? upperNames.first(where: { !boilerplate($0) })
        ?? names.first(where: { titleLike($0) })
        ?? names.first(where: { !boilerplate($0) })
        ?? ""
    // Report several lines, best-guess first. The caller tries each against
    // Scryfall, so a card still resolves when the top-line guess is wrong —
    // which happens whenever the capture reaches Vision at an odd angle.
    //
    // Order is not cosmetic: the caller stops after the first handful of
    // lines, so anything worth resolving has to be near the front. A card's
    // rules text names the card, and on an old frame whose title band was
    // lost that is the only place the name survives — so those recovered
    // names sit directly behind the primary, ahead of the raw prose lines
    // they were mined from.
    var candidates: [String] = []
    var seenCandidates = Set<String>()
    func offer(_ s: String) {
        let key = normTitle(s)
        guard !key.isEmpty, !seenCandidates.contains(key) else { return }
        seenCandidates.insert(key)
        candidates.append(s)
    }
    offer(primary)
    for line in ranked {
        if let name = parseSelfReference(line.text) { offer(name) }
    }
    for n in names { offer(n) }
    candidates = Array(candidates.prefix(8))

    read.name = primary
    read.candidates = candidates
    read.lines = plausible
    read.nameConfidence = plausible.first(where: { $0.text == primary })?.confidence
        ?? plausible.first?.confidence ?? ranked.first!.confidence
    // The copyright line reads better in the full-resolution frame pass than
    // in the band crop — the band's tiny italic serif came back as fragments
    // in every observed old-frame capture — so both sources feed it.
    applyCopyrightCollector(&read, bottomLines + lines.map { $0.text })
    return read
}

/// plausibleName filters obvious non-title noise: very short tokens, pure
/// numbers and symbols. Shared by the single-card ranking and the multi-card
/// clustering, so both mean the same thing by "could be a card name".
func plausibleName(_ s: String) -> Bool {
    let letters = s.filter { $0.isLetter }
    return letters.count >= 3
}

// MARK: - Multi-card detection

// Grown from a spike over five captured fixtures (tight fan, booster-sized
// cascade, steady fan, loose spread, desk clutter), which established:
// rectangle outlines only survive for unoccluded cards (2 of 9 in the
// cascade), while the whole-frame text pass reads every visible title band —
// so title lines are the primary channel and crops are the refinement,
// contributing per-card candidates and the only readable collector info.
// Set $HOARD_SCAN_MULTI for stderr tracing of the decisions.

/// multiDebug traces the multi-card decisions to stderr when asked. Purely
/// diagnostic: nothing downstream parses these lines.
func multiDebug(_ s: String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_MULTI"] != nil else { return }
    FileHandle.standardError.write(Data("multi: \(s)\n".utf8))
}

/// cardRects finds every card-shaped quad in the frame. The detector runs with
/// looser thresholds than findCard's — a card at the edge of a fan shows less
/// of its outline than a lone framed card — and the containment pass keeps
/// only the outermost quads, dropping near-duplicate detections and the boxes
/// *inside* a card (the art frame and the rules box, which OCR to rules text
/// rather than titles).
func cardRects(_ cg: CGImage) -> [VNRectangleObservation] {
    let req = VNDetectRectanglesRequest()
    req.minimumAspectRatio = 0.15
    req.maximumAspectRatio = 1.0
    req.minimumSize = 0.08
    req.minimumConfidence = 0.3
    req.quadratureTolerance = 25
    req.maximumObservations = 16
    do {
        try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req])
    } catch {
        multiDebug("rect detection failed: \(error.localizedDescription)")
        return []
    }
    let rects = req.results ?? []

    func area(_ o: VNRectangleObservation) -> CGFloat {
        o.boundingBox.width * o.boundingBox.height
    }
    var kept: [VNRectangleObservation] = []
    for r in rects.sorted(by: { area($0) > area($1) }) {
        let bb = r.boundingBox
        let swallowed = kept.contains { k in
            let inter = k.boundingBox.intersection(bb)
            return !inter.isNull && inter.width * inter.height > 0.7 * bb.width * bb.height
        }
        if !swallowed { kept.append(r) }
    }
    multiDebug("\(rects.count) rects, \(kept.count) kept after containment dedup")
    return kept
}

/// perspectiveCrop straightens one detected quad into an upright card image —
/// the step the single-card path never needed, since it reads the whole frame.
func perspectiveCrop(_ cg: CGImage, _ r: VNRectangleObservation, _ ctx: CIContext) -> CGImage? {
    let w = CGFloat(cg.width), h = CGFloat(cg.height)
    let ci = CIImage(cgImage: cg)
    func pt(_ p: CGPoint) -> CIVector { CIVector(x: p.x * w, y: p.y * h) }
    guard let f = CIFilter(name: "CIPerspectiveCorrection") else { return nil }
    f.setValue(ci, forKey: kCIInputImageKey)
    f.setValue(pt(r.topLeft), forKey: "inputTopLeft")
    f.setValue(pt(r.topRight), forKey: "inputTopRight")
    f.setValue(pt(r.bottomLeft), forKey: "inputBottomLeft")
    f.setValue(pt(r.bottomRight), forKey: "inputBottomRight")
    guard let out = f.outputImage else { return nil }
    return ctx.createCGImage(out, from: out.extent)
}

/// typeLineWords are tokens that mark a card's *type* line ("Legendary
/// Enchantment Creature — God"), which reads at title-like isolation and
/// title-like capitalization. Card names essentially never use these words,
/// so a token match is a safe rejection.
let typeLineWords: Set<String> = [
    "legendary", "creature", "enchantment", "planeswalker",
    "sorcery", "instant", "battle", "tribal", "snow",
]

/// titleLike judges whether a frame line could be a card's title band, by the
/// text alone. Geometry cannot do this job: in a booster-sized cascade the
/// stacked title bands sit closer together than a rules paragraph's spacing,
/// so any gap threshold either eats real titles or admits rules text. Shape
/// separates them instead — Magic titles are Title Case multi-word lines,
/// rules text is sentence case, type lines carry known type words. Filtered
/// generously: a survivor that isn't a card dies on the Go side's Scryfall
/// fuzzy match. Single-word names (Ponder, Opt) are rejected here but not
/// lost — a lone card always has an outline, so the crop channel carries it.
func titleLike(_ s: String) -> Bool {
    if boilerplate(s) { return false }
    let words = s.split(whereSeparator: { $0.isWhitespace })
    guard words.count >= 2 else { return false }
    // A leading dash is a flavor attribution ("—Doctor Doom"), never a
    // title. The first-letter guard below happens to reject these today;
    // explicit so the intent survives any loosening of that guard.
    if let first = s.first, "—–-―‒−".contains(first) { return false }
    guard let first = words.first?.first, first.isLetter else { return false }
    let tokens = words.map { String($0.lowercased().filter { $0.isLetter }) }
    if tokens.contains(where: { typeLineWords.contains($0) }) { return false }
    // Rules text that opens a line with its trigger word capitalizes like a
    // title ("Whenever Black Panther…", "When Parallel Thoughts comes into").
    // No card is named "When…", so the lead token alone is a safe rejection.
    if let lead = tokens.first, lead == "whenever" || lead == "when" { return false }
    // A card's rules text names the card itself, so the self-reference reads
    // as Title Case for exactly as long as the name runs and then trails off
    // into a sentence ("Dwarven Ruins comes into play tapped."). The idiom
    // that follows the name is the tell — and it is worth catching, because
    // these lines otherwise pass every test below and become the card's name.
    // parseSelfReference mines the name back out of them.
    if selfReferenceIdiom(tokens) != nil { return false }
    // The border block prints in small caps and reads as (nearly) all caps —
    // "KEy WALKER", "IN & C", a mangled set line — while real card titles are
    // Title Case with plenty of lowercase. A multi-word line with at most one
    // lowercase letter is frame furniture; left alone, an artist credit fuzzy-
    // resolves to a real card and ghosts into the queue (observed live: Kev
    // Walker the artist became Kiln Walker the card).
    let letters = s.filter { $0.isLetter }
    if letters.count >= 6 && letters.filter({ $0.isLowercase }).count <= 1 { return false }
    var caps = 0
    for w in words where w.first?.isUppercase == true { caps += 1 }
    // Titles capitalize everything but connectors; sentences capitalize
    // little. Strictly more than half keeps "Erebos. God of the Dead" and
    // rejects "companion Animals".
    return caps * 2 > words.count
}

/// flavorAttribution reports whether a line hangs directly beneath a flavor
/// quote — the "—Doctor Doom" under "Beneath me." An attribution names a
/// character, and in licensed sets the character is usually a card in the
/// same set, so the Scryfall backstop that kills other junk *vouches* for
/// this phantom instead (observed live: Aerial Doombot's flavor text queued
/// a Doctor Doom). OCR routinely drops the attribution dash, so the quote
/// above is the reliable signal. On a tilted card the axis-aligned boxes of
/// adjacent lines bleed into each other — the fixture's quote box vertically
/// *contains* its attribution — so the relation is "centered inside or just
/// below the quote's vertical span", not a clean gap between boxes. A
/// neighbouring card's real title band sits past an attribution line, a
/// bottom margin, and a border — well below the reach of the allowance.
func flavorAttribution(_ line: Line, among all: [Line]) -> Bool {
    let cy = line.box.midY
    for other in all {
        if other.box == line.box { continue } // a stray quote glyph on a title must not self-match
        guard endsQuoted(other.text) else { continue }
        // Vision origin is bottom-left: lower on the card is smaller Y.
        guard cy < other.box.maxY, cy > other.box.minY - 1.5 * line.box.height else { continue }
        if min(other.box.maxX, line.box.maxX) > max(other.box.minX, line.box.minX) {
            return true
        }
    }
    return false
}

/// endsQuoted matches a flavor quote's closing mark, whatever glyph Vision
/// chose for it.
func endsQuoted(_ s: String) -> Bool {
    guard let last = s.trimmingCharacters(in: .whitespaces).last else { return false }
    return "\"\u{201D}\u{00BB}'\u{2019}".contains(last)
}

/// normTitle reduces a read title to the characters worth comparing.
func normTitle(_ s: String) -> String {
    String(s.lowercased().unicodeScalars.filter { CharacterSet.alphanumerics.contains($0) })
}

/// editDistance is plain Levenshtein — titles are short and there are at most
/// a handful of comparisons per capture, so the simple table is plenty.
func editDistance(_ a: String, _ b: String) -> Int {
    let x = Array(a), y = Array(b)
    if x.isEmpty { return y.count }
    if y.isEmpty { return x.count }
    var prev = Array(0...y.count)
    var cur = [Int](repeating: 0, count: y.count + 1)
    for i in 1...x.count {
        cur[0] = i
        for j in 1...y.count {
            let sub = prev[j - 1] + (x[i - 1] == y[j - 1] ? 0 : 1)
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, sub)
        }
        swap(&prev, &cur)
    }
    return prev[y.count]
}

/// sameTitle reports whether two reads plausibly name the same card. The
/// tolerance exists because the frame pass and a perspective-corrected crop
/// routinely OCR the same printed title differently ("Ulamoz, tre" vs
/// "Ulamos, the") — and a missed match here becomes the same card twice in
/// the confirm queue, which a user will confirm twice and double-count.
func sameTitle(_ a: String, _ b: String) -> Bool {
    let x = normTitle(a), y = normTitle(b)
    if x.isEmpty || y.isEmpty { return false }
    if x == y { return true }
    if (x.count >= 8 && y.contains(x)) || (y.count >= 8 && x.contains(y)) { return true }
    return editDistance(x, y) * 4 <= max(x.count, y.count) // ≤ a quarter differs
}

/// selfReferenceIdiom finds where a line stops naming a card and starts
/// describing it, returning the index of the first idiom token. The runs are
/// deliberately short and must sit past the first token — there has to be a
/// name in front of them for the phrase to be self-reference at all.
func selfReferenceIdiom(_ tokens: [String]) -> Int? {
    let idioms = [["comes", "into"], ["enters", "the"], ["leaves", "play"]]
    guard tokens.count >= 2 else { return nil }
    for start in 1..<tokens.count {
        for idiom in idioms where start + idiom.count <= tokens.count {
            if Array(tokens[start..<(start + idiom.count)]) == idiom { return start }
        }
    }
    return nil
}

/// parseSelfReference recovers a card's name from its own rules text. Magic
/// cards name themselves, so an old frame whose title band was lost — the
/// common failure, where the serif title sits against the art and the band
/// crop returns fragments — is usually still named in plain text further down
/// ("Dwarven Ruins comes into play tapped."). Two cards were total losses in
/// one live session with their names sitting on the wire this way.
///
/// The result ships as an extra candidate only, never as the entry's name: it
/// is a guess built from a heuristic, and the resolver already owns choosing
/// among candidates.
func parseSelfReference(_ s: String) -> String? {
    var words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    if let lead = words.first?.lowercased().filter({ $0.isLetter }),
        lead == "when" || lead == "whenever" {
        words.removeFirst()
    }
    let tokens = words.map { String($0.lowercased().filter { $0.isLetter }) }
    guard let idx = selfReferenceIdiom(tokens) else { return nil }
    let lead = Array(words[0..<idx])
    // A name is Title Case throughout; a lowercase word means the run started
    // mid-sentence and the "name" would be a fragment of prose.
    guard lead.allSatisfy({ $0.first?.isUppercase == true }) else { return nil }
    let name = lead.joined(separator: " ")
        .trimmingCharacters(in: CharacterSet(charactersIn: " ,.:;"))
    // One word left over is as often a pronoun ("It comes into play") as a
    // name, and single-word names already have the crop channel.
    guard name.split(whereSeparator: { $0.isWhitespace }).count >= 2 else { return nil }
    return name
}

/// addCandidate records an alternate reading of an entry's title. The merge
/// ladder has to pick one name per card, but the reading it drops is often the
/// one downstream fuzzy matching could have used — so the loser rides along
/// instead of being discarded. Nothing empty, nothing already present, and the
/// same prefix cap the crop channel applies.
/// Dedup is exact-normalized, deliberately not sameTitle: its containment
/// tolerance would treat "Shivan Oasis" as already present in the rules line
/// "Shivan Oasis comes into play tapped." and drop the very reading worth
/// keeping.
func addCandidate(_ entry: inout CardEntry, _ name: String) {
    let t = name.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !t.isEmpty, entry.candidates.count < 8 else { return }
    let key = normTitle(t)
    guard !key.isEmpty, !entry.candidates.contains(where: { normTitle($0) == key }) else { return }
    entry.candidates.append(t)
}

/// artistCredit matches the "Illus. <Name>" line even when OCR has mangled the
/// credit word. On old frames it is set in the same small serif as the
/// copyright, and the exact `illus` prefix missed "Tins. Liz Danforth" — which
/// then read as a perfectly good Title Case name and won a live capture's
/// merge, burying Dwarven Ruins.
///
/// The credit word is only allowed to be wrong by a letter or two, and only
/// when what follows looks like a person: a mangled word alone is not enough,
/// or real two-word titles would start dying.
func artistCredit(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    // "Illus. Liz Danforth" — the credit word, then a two-word personal name.
    // Artists are credited first-and-last, and holding the count to exactly
    // two is what lets the credit word itself be read loosely.
    guard words.count == 3 else { return false }
    // The abbreviation's trailing period is the load-bearing signal. Magic
    // titles use commas for epithets ("Jaya Ballard, Task Mage"), never a
    // period after their first word, so this is the shape no real name has.
    // OCR turns the period into a comma often enough to accept both.
    guard let last = words[0].last, last == "." || last == "," else { return false }
    let head = words[0].lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    // Wide enough for the observed mangles — "Illus." came back as "Tins."
    // and "Tims.", both four edits out — and safe only because the period and
    // the two-word name have already narrowed the field this far.
    guard editDistance(head, "illus") <= 4 else { return false }
    return words.dropFirst().allSatisfy { $0.first?.isUppercase == true }
}

/// boilerplate matches the card frame's own print that reads at title-like
/// isolation and capitalization — the copyright border line, the artist
/// credit, and the collector block — which would otherwise become phantom
/// queue entries on every capture that shows a card's bottom.
func boilerplate(_ s: String) -> Bool {
    let t = s.lowercased()
    if t.contains("wizards of the coast") || t.hasPrefix("illus")
        || s.hasPrefix("™") || s.hasPrefix("©")
        || s.contains("•") { // the collector line's separator; never in a name
        return true
    }
    // The set/language line survives the bullet check whenever Vision reads
    // the bullet as "*" or a bare space ("MSH *EN ADI GRANOY"). If the line
    // parses as a set code beside a language code, it is the border, whatever
    // the separator became.
    if group(setLangRE, asciify(s)) != nil {
        return true
    }
    // Licensed frames add their own brand line, and "© MARVEL" reads as
    // "C MARVEL" or "O MARVEL." at this glyph size: a lone character in front
    // of a brand word is a mangled © symbol, not a card name.
    let words = t.split(whereSeparator: { $0.isWhitespace })
        .map { $0.trimmingCharacters(in: .punctuationCharacters) }
    if words.count == 2, words[0].count == 1, words[1] == "marvel" {
        return true
    }
    if artistCredit(s) {
        return true
    }
    // Old-frame copyright fragments ("Coast, Ine: 30/1", "te Coast, Inc")
    // survive every check above — no bullet, no full "wizards of the coast",
    // Title Case enough — and became phantom entries (observed live).
    return copyrightFurniture(s)
}

/// scanFrame is the whole capture read: the frame-wide single-card read the
/// event's flat fields have always carried, plus the per-card list a fanned
/// spread needs.
///
/// Title lines are the primary channel and are never excluded by geometry.
/// The fixtures were unambiguous about why: on a fan, the rectangle detector
/// returns quads that span *several* cards (an occluded outline completes
/// itself along a neighbor's edge), so "this line sits inside a detected
/// card" proves nothing. Crops refine instead — a crop whose title matches an
/// existing line entry upgrades it with the crop's richer candidates and any
/// collector read; a crop matching nothing is a card the frame pass missed.
/// Junk entries (keycaps, box lids, stray prose) survive to the event and die
/// on the Go side's Scryfall fuzzy match, which the clutter fixture showed
/// filters them for free.
func scanFrame(_ cg: CGImage) -> (read: CardRead, cards: [CardEntry]) {
    let t0 = Date()
    let read = readCard(cg)
    let frameMs = msSince(t0)
    let tRects = Date()
    let rects = cardRects(cg)
    let rectsMs = msSince(tRects)
    let tCrops = Date()

    // Entries carry their anchor height so the final list reads top-to-bottom,
    // the order a person reads a fan — and the title line's full box, so a
    // crop can be matched to its card by geometry when its title read can't.
    var entries: [(top: CGFloat, box: CGRect?, entry: CardEntry)] = []

    for line in read.lines {
        if !titleLike(line.text) {
            multiDebug("line not title-like: \"\(line.text)\"")
            continue
        }
        if flavorAttribution(line, among: read.lines) {
            multiDebug("line is a flavor attribution: \"\(line.text)\"")
            continue
        }
        if entries.contains(where: { sameTitle($0.entry.name, line.text) }) {
            multiDebug("line repeats an entry: \"\(line.text)\"")
            continue
        }
        multiDebug("line entry: \"\(line.text)\"")
        entries.append((line.top, line.box, CardEntry(name: line.text, candidates: [line.text],
                                                      confidence: line.confidence, source: "frame")))
    }

    for (i, r) in rects.enumerated() {
        guard let crop = perspectiveCrop(cg, r, sharedCIContext) else { continue }
        saveDebugImage(crop, "multi-rect-\(i).png")
        let cropRead = readCard(crop)
        if cropRead.name.isEmpty {
            continue // a quad that reads as nothing is desk, not card
        }
        var e = CardEntry(name: cropRead.name, candidates: Array(cropRead.candidates.prefix(8)),
                          confidence: cropRead.nameConfidence, source: "crop")
        if cropRead.copyrightYear > 0 {
            e.copyrightYear = cropRead.copyrightYear
        }
        // A bare number off a crop is a mana cost or power box as often as a
        // collector number; only a set-and-number pair is worth reporting.
        // The copyright-line tail is the exception: its signature and total
        // guard make it far harder to fake than a bare band number, and on
        // old frames it is the only number printed at all.
        // A pair-form number counts as its own corroboration: "29/143" carries
        // the set total, which a mana cost or power box has no way to fake, and
        // the total guard has already vetted it. Requiring a set code as well
        // used to be harmless, but the set line is the frailer read of the two
        // — once prose stopped fabricating set codes, a real number went with
        // the fake set it happened to be paired with (Brain Freeze, live).
        if !cropRead.collectorNumber.isEmpty
            && (!cropRead.setCode.isEmpty || cropRead.collectorPair) {
            e.setCode = cropRead.setCode
            e.collectorNumber = cropRead.collectorNumber
        } else if !cropRead.copyrightNumber.isEmpty {
            e.collectorNumber = cropRead.copyrightNumber
            e.numberSource = "copyright"
        }
        // The crop's band is anchored to this card, so its alternates and
        // finish marker are per-card by construction.
        if !cropRead.collectorAlts.isEmpty {
            e.collectorAlts = cropRead.collectorAlts
        }
        e.finishHint = cropRead.finishHint
        // mergeInto folds the crop's card-anchored printing and foil marker
        // into an existing entry. The name is decided rather than assumed: the
        // frame line is usually the better read, but when it is furniture and
        // the crop's is title-shaped, keeping the frame's was always wrong —
        // a whole live session's pins went that way, including a crop that had
        // read "Caller of the Claw" exactly while the frame offered the rules
        // fragment "When Caller of", and a "Gremal Dragon" that fuzzy-resolved
        // to the unrelated Green Dragon while the crop's "Eiteral Dragon"
        // would have landed on Eternal Dragon.
        //
        // Whichever name loses still ships as a candidate. Downstream owns
        // fuzzy matching, and it cannot choose a reading the helper dropped.
        func mergeInto(_ idx: Int, why: String) {
            if entries[idx].entry.collectorNumber.isEmpty && !e.collectorNumber.isEmpty {
                entries[idx].entry.setCode = e.setCode
                entries[idx].entry.collectorNumber = e.collectorNumber
                entries[idx].entry.numberSource = e.numberSource
            }
            if entries[idx].entry.copyrightYear == nil {
                entries[idx].entry.copyrightYear = e.copyrightYear
            }
            if entries[idx].entry.collectorAlts == nil {
                entries[idx].entry.collectorAlts = e.collectorAlts
            }
            if entries[idx].entry.finishHint.isEmpty {
                entries[idx].entry.finishHint = e.finishHint
            }
            let kept = entries[idx].entry.name
            let adopt = !titleLike(kept) && titleLike(e.name)
            if adopt {
                entries[idx].entry.name = e.name
                entries[idx].entry.confidence = e.confidence
            }
            addCandidate(&entries[idx].entry, adopt ? kept : e.name)
            let verb = adopt ? "adopts" : "pins"
            multiDebug("crop \(i) \(verb) \"\(entries[idx].entry.name)\" \(e.setCode.isEmpty ? "-" : e.setCode)/\(e.collectorNumber.isEmpty ? "-" : e.collectorNumber) (\(why), crop read \"\(e.name)\")")
        }
        let frameIdxs = entries.indices.filter { entries[$0].entry.source == "frame" }
        if let idx = entries.firstIndex(where: { sameTitle($0.entry.name, e.name) }) {
            if titleLike(e.name) || !titleLike(entries[idx].entry.name) {
                // The crop read the same title off straightened pixels —
                // usually the cleaner read — and may carry the printing.
                let replaced = entries[idx].entry.name
                entries[idx].entry = e
                addCandidate(&entries[idx].entry, replaced)
                multiDebug("crop \(i) refines \"\(e.name)\" \(e.setCode.isEmpty ? "-" : e.setCode)/\(e.collectorNumber.isEmpty ? "-" : e.collectorNumber)")
            } else {
                // sameTitle's containment tolerance also matches the rules
                // line that QUOTES the title ("If <name> is in your…"), and
                // replacing wholesale handed that junk downstream as the
                // card's name — where it broke both the resolver and the
                // nudge echo-swallow, and a keyword fallback line became a
                // phantom queue entry (observed live: "Haste" → Haste
                // Magic). Keep the frame's clean title; take the printing.
                mergeInto(idx, why: "junk crop title, frame name kept")
            }
        } else if let idx = entries.indices
            .filter({ i in
                guard let b = entries[i].box else { return false }
                return r.boundingBox.contains(CGPoint(x: b.midX, y: b.midY))
            })
            // Of the contained lines, the top-most is the card's title; a
            // surviving border-line entry sits at the bottom and must not
            // steal the printing (observed live).
            .max(by: { (entries[$0].box?.midY ?? 0) < (entries[$1].box?.midY ?? 0) }) {
            // The crop misread its title — the type line, a rules fragment —
            // but geometry says which card it is: the frame's title line sits
            // inside this very rectangle. Without this, the collector block
            // lands beside the real card instead of on it, and the card
            // queues as unverified (observed live).
            mergeInto(idx, why: "contains its title")
        } else if !titleLike(e.name), frameIdxs.count == 1 {
            // A crop whose "title" isn't title-like — "MEL", a type line, a
            // rules fragment — is a misread of a card that already has an
            // entry, not a new card. With exactly one real title in the
            // scene there is nothing to mispair with, so its printing lands
            // there; junk names resolving to real-but-unscanned cards is how
            // ghosts joined the review queue (observed live).
            mergeInto(frameIdxs[0], why: "only title in scene")
        } else if titleLike(e.name) || entries.isEmpty {
            entries.append((r.boundingBox.maxY, nil, e))
            multiDebug("crop \(i) adds \"\(e.name)\"")
        } else {
            multiDebug("crop \(i) discarded — junk title \"\(e.name)\" beside \(frameIdxs.count) real titles")
        }
    }

    timing("scanFrame frameOCR=\(frameMs)ms rects=\(rectsMs)ms "
        + "crops=\(rects.count) cropOCR=\(msSince(tCrops))ms total=\(msSince(t0))ms")

    entries.sort { $0.top > $1.top }
    var cards = entries.map { $0.entry }
    // When no entry carries collector info, the frame-wide border read is
    // attached to the top-most entry — the title position, which in a
    // single-card scene is the card the border belongs to, while surviving
    // phantom entries (misread border lines, rules fragments) sit below it.
    // This is what lets a borderless or hard-to-outline card still arrive
    // with its printing pinned. A wrong attachment in a fan is commit-safe by
    // construction: collector numbers are per-card within a set, so a
    // neighbour's number cannot verify against the wrong card's printings —
    // it queues, exactly as an unattached read would have.
    if !cards.isEmpty, !cards.contains(where: { !$0.collectorNumber.isEmpty }) {
        if !read.collectorNumber.isEmpty || !read.setCode.isEmpty {
            cards[0].collectorNumber = read.collectorNumber
            cards[0].setCode = read.setCode
        } else if !read.copyrightNumber.isEmpty {
            // The frame-wide copyright read is as card-anchored as the band
            // read in a single-card scene, and carries the same commit-safety:
            // on the Go side a copyright number can upgrade a match but never
            // veto one.
            cards[0].collectorNumber = read.copyrightNumber
            cards[0].numberSource = "copyright"
        }
        if cards[0].collectorAlts == nil, !read.collectorAlts.isEmpty {
            cards[0].collectorAlts = read.collectorAlts
        }
        if cards[0].finishHint.isEmpty {
            cards[0].finishHint = read.finishHint
        }
    }
    // The year is printing evidence whichever channel read the number; attach
    // it to the top-most card — in a single-card scene, the card itself.
    if !cards.isEmpty, cards[0].copyrightYear == nil, read.copyrightYear > 0 {
        cards[0].copyrightYear = read.copyrightYear
    }
    // Border colour, and only when the frame holds exactly one card. The
    // reading is anchored on a single footer line, so in a fan there is no way
    // to say which card it describes — and unlike a stray collector number,
    // which cannot verify against the wrong card's printings and so queues
    // harmlessly, a border attached to the wrong card would agree with
    // *something* and pick a printing. That asymmetry is why this one refuses
    // to guess rather than leaning on downstream verification.
    if cards.count == 1 {
        let border = readBorder(cg, read)
        borderDebug(border.color.map { "\($0) via \(border.source ?? "?")" }
            ?? "abstained: \(border.abstain)")
        if let color = border.color {
            cards[0].borderColor = color
            cards[0].borderSource = border.source
        }
    }
    return (read, cards)
}

/// decodePhoto turns a captured photo into a CGImage plus the orientation Vision
/// should read it at. macOS doesn't expose `AVCapturePhoto.metadata`, so the
/// orientation is recovered from the encoded file representation; if that isn't
/// available the raw representation is assumed upright.
func decodePhoto(_ photo: AVCapturePhoto) -> (CGImage, CGImagePropertyOrientation)? {
    if let data = photo.fileDataRepresentation(),
       let src = CGImageSourceCreateWithData(data as CFData, nil),
       let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) {
        let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
        let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
        return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
    }
    if let cg = photo.cgImageRepresentation() { return (cg, .up) }
    return nil
}

/// saveDebugImage writes an image to $HOARD_SCAN_DEBUG_DIR when that's set, so a
/// scan that reads the wrong line can be reproduced offline with --image.
func saveDebugImage(_ cg: CGImage, _ filename: String) {
    guard let dir = ProcessInfo.processInfo.environment["HOARD_SCAN_DEBUG_DIR"],
          !dir.isEmpty else { return }
    let url = URL(fileURLWithPath: dir).appendingPathComponent(filename)
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil)
    else { return }
    CGImageDestinationAddImage(dest, cg, nil)
    CGImageDestinationFinalize(dest)
    FileHandle.standardError.write(Data("hoard-scan: wrote \(url.path)\n".utf8))
}

/// sharedCIContext is the one CIContext for the whole process. Constructing a
/// CIContext spins up a Metal device, and doing that per capture (twice — here
/// and in scanFrame) was measurable per-card cost. CIContext is thread-safe.
let sharedCIContext = CIContext()

/// uprighted bakes an EXIF orientation into the pixels, returning an image that
/// reads correctly with no orientation tag. Normalizing once here is what keeps
/// the tag and the manual rotation from both being applied.
func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    if orientation == .up { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return sharedCIContext.createCGImage(ci, from: ci.extent) ?? cg
}

/// rotatedImage returns a copy of the image turned clockwise by a multiple of
/// 90°, so OCR sees exactly the framing the corrected preview showed. Doing this
/// in pixels rather than via an EXIF orientation tag keeps the direction
/// unambiguous. The input must already be upright — see uprighted.
func rotatedImage(_ cg: CGImage, clockwiseDegrees deg: Int) -> CGImage {
    let steps = ((deg / 90) % 4 + 4) % 4
    if steps == 0 { return cg }

    let w = cg.width, h = cg.height
    let sideways = steps % 2 == 1
    let outW = sideways ? h : w
    let outH = sideways ? w : h
    guard let ctx = CGContext(
        data: nil, width: outW, height: outH,
        bitsPerComponent: 8, bytesPerRow: 0,
        space: cg.colorSpace ?? CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue)
    else { return cg }

    ctx.translateBy(x: CGFloat(outW) / 2, y: CGFloat(outH) / 2)
    // CoreGraphics rotates counter-clockwise for a positive angle.
    ctx.rotate(by: -CGFloat(steps) * .pi / 2)
    ctx.translateBy(x: -CGFloat(w) / 2, y: -CGFloat(h) / 2)
    ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
    return ctx.makeImage() ?? cg
}

/// cgImage loads an image file through CGImageSource — the same decode a live
/// capture goes through — so --image exercises the real pipeline, including the
/// pixel formats NSImage would quietly normalize away.
func cgImage(fromFile path: String) -> (CGImage, CGImagePropertyOrientation)? {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { return nil }
    let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
    let raw = props?[kCGImagePropertyOrientation] as? UInt32 ?? 1
    return (cg, CGImagePropertyOrientation(rawValue: raw) ?? .up)
}

// MARK: - Border colour

// Before 1998 a card printed no collector number, so the copyright year is the
// only printing evidence it carries, and the year alone pins 24% of the
// pre-1998 printings that have a sibling. Border colour is the era's other
// discriminator and the catalog already stores it: year plus border pins 47%,
// and 4th Edition goes from 0% to 95%, since 4ED (white, 1995) and 4BB (black,
// 1995) differ in nothing else a camera can see.
//
// A pixel classifier over the perspective crop was built and removed once
// already — see docs/scanner-tuning.md. It failed because the crop does not
// reliably contain the border: Vision locks onto whichever edge has contrast,
// sometimes the card's outer cut and sometimes the printed frame just inside
// it, so an 8th Edition Gaea's Herald came back black off its gold-brown inner
// frame. Saturation could not separate the cases (0.40 for a real white border
// under tungsten against 0.43 for that frame). The missing signal was never in
// the pixels; it was knowing whether what we were looking at is the card.
//
// So nothing here reads the crop. Two changes make it work:
//
//  1. The geometry is anchored on text whose identity is already proven by its
//     *content* — the copyright line and the artist credit, which
//     copyrightFurniture and artistCredit recognize by what they say. Those sit
//     at a fixed fraction of card height, so two of them recover the card's own
//     scale, and the ring is sampled on the full-resolution frame from there.
//     No amount of edge contrast can fake "this line says Wizards of the Coast".
//
//  2. The ring is judged against the card's own two tones, never an absolute
//     threshold. On an old frame the footer is printed *on the border* — black
//     ink on a white one, white ink on a black one — so an Otsu split of that
//     single line yields the paper point and the ink point under whatever light
//     the desk happened to have. That is the direct answer to the tungsten
//     problem, and it is self-checking: if the reconstruction has drifted onto
//     an inner frame, the ring lands between the two tones instead of on one of
//     them, and we abstain rather than guess.

/// Magic cards are 63×88 mm.
let cardAspect: CGFloat = 63.0 / 88.0

/// borderDebug traces the border decision to stderr when asked, the way
/// multiDebug does for the multi-card pass. Purely diagnostic; nothing
/// downstream parses these.
func borderDebug(_ s: String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_BORDER"] != nil else { return }
    FileHandle.standardError.write(Data("border: \(s)\n".utf8))
}

/// Where the card's furniture sits in card space, with v running 0…1 down from
/// the card's top edge. Fitted on scan/corpus, where the card *is* the image so
/// the card rect is known exactly — see scan/corpus/border.sh.
enum CardLayout {
    /// Centre of the copyright row, the lowest text on the card.
    static let footerV: CGFloat = 0.9375
    /// Centre of the illustrator credit, one row above it — or of the two rows
    /// together, since Vision often returns them as a single observation.
    /// Anchoring on the wrong one of these costs ~1.5% of scale, which is most
    /// of the border's own thickness, so they are kept apart.
    static let creditV: CGFloat = 0.9212
    /// Centre of the title row.
    static let titleV: CGFloat = 0.0625
    /// Printed border thickness top and bottom, as a fraction of card height.
    static let borderV: CGFloat = 0.039
    /// How deep into that border to sample, as a fraction of its thickness —
    /// clear of both the card's cut edge and the printed frame inside it.
    static let ringDepth: CGFloat = 0.45
    /// A footer line's glyph-box height as a fraction of card height. The local
    /// scale estimate, which exists only to disagree with the long one.
    static let footerGlyphV: CGFloat = 0.0174
    /// Where to sample the card's own frame, just inside the border. The
    /// footer text is printed here — on old frames it sits on the coloured
    /// frame, not on the border, which is worth knowing because it means the
    /// footer's own two tones are *not* the card's paper and ink.
    static let innerV: CGFloat = 0.950
    /// Left edge of each footer row, as a fraction of card *width*. Measured
    /// across 120 pre-1998 corpus cards: the copyright row starts at 0.086
    /// (p10 0.083, p90 0.102) and the credit row at 0.097 (p10 0.089, p90
    /// 0.099) — the © glyph's box reaches a little further left than a letter.
    ///
    /// This is the card's horizontal landmark, and it has to be the *left*
    /// edge because the right one is wherever the sentence happened to end:
    /// across the same cards it ranges 0.42 to 0.62. Worth recording that
    /// docs/scanner-coverage.md called this footer "two centred rows" — it is
    /// left-aligned, and the measurement is what says so.
    ///
    /// **These hold for pre-1998 only, and the symbol reader cannot ship until
    /// that is fixed.** Measured per era, the copyright row's left edge sits at
    /// 0.086 before 1998, 0.260 from 1998–2002, and 0.594 on the M15 frame —
    /// the line moves across the card as the frame is redesigned. Using the
    /// pre-1998 value on a 7th Edition card threw the predicted symbol
    /// position clean off the image. The era is recoverable from the same line
    /// that anchors on it (parseCopyrightCollector already reads its year, and
    /// a lone year means pre-1998 while a range means later), so the fix is a
    /// lookup keyed on that rather than a single constant.
    static let copyrightLeftU: CGFloat = 0.086
    static let creditLeftU: CGFloat = 0.097

    /// leftU is the horizontal landmark: where the anchor line's left end sits
    /// as a fraction of card width. It is keyed on the *frame era* and on what
    /// the line actually starts with, because both move it — and it returns nil
    /// wherever that combination has never been measured.
    ///
    /// The prefix matters as much as the era, which is not obvious until you
    /// measure it. A line read as "™ & © 1993-2003 Wizards…" starts where the
    /// row starts; the same row read as "© 1993-2003 Wizards…" or "1993-2003
    /// Wizards…" has lost its opening and starts further right. Keyed only on
    /// era, the 2003-2014 stratum looked hopelessly bimodal — median 0.080 with
    /// a p90 of 0.214. Split by prefix, the trademark reads are 0.080 with a
    /// p10 of 0.075 and a p90 of 0.083, and the noise was entirely the lines
    /// that began late.
    ///
    /// nil is the important part. Guessing does not degrade gracefully: the
    /// landmark is at u≈0.08 and the expansion symbol at u≈0.87, so the lever
    /// is most of the card's width and a wrong offset throws the predicted
    /// symbol clean off the image — which is exactly what a 7th Edition card
    /// did while this was a single constant. Nothing but the symbol reader
    /// consumes it, and it would rather have no answer than a wrong one.
    static func leftU(kind: AnchorKind, prefix: LinePrefix, year: Int) -> CGFloat? {
        // A lone year, or none at all, is the pre-collector-number era: those
        // frames print no range, so there is nothing else it could be.
        if year == 0 || year < 1998 {
            switch prefix {
            case .copyrightGlyph, .trademark: return 0.086   // n=79, p10 .083 p90 .102
            case .illus: return 0.097                        // n=23, p10 .091 p90 .099
            case .year: return 0.102                         // n=8
            }
        }
        if year <= 2002 {
            switch prefix {
            case .trademark: return 0.231                    // n=5, p10 .228 p90 .236
            case .year: return 0.271                         // n=7, p10 .263 p90 .325
            // "© 1993-2001…" spread 0.099 to 0.274 in this era — unusable.
            case .copyrightGlyph, .illus: return nil
            }
        }
        // The 8th Edition frame. Only the trademark read is tight enough to be
        // a landmark, and it is measured on clean scans; note that this is
        // precisely the line a 1080p desk photograph of this frame fails to
        // read at all, so live captures mostly anchor on the credit row
        // instead — whose left edge here is still too loose to use (n=15,
        // IQR 0.067) and is deliberately left nil until it is measured.
        switch prefix {
        case .trademark: return kind == .copyright ? 0.080 : nil   // n=21, p10 .075 p90 .083
        case .copyrightGlyph, .year, .illus: return nil
        }
    }
    /// The expansion symbol, at the right end of the type line. The old frame
    /// (Ice Age's snowflake, 7th Edition's "7") puts it at 0.877/0.578; the
    /// redesigned 8th Edition frame at 0.867/0.590. One patch covers both.
    /// Core sets before 6th Edition print nothing here — 4th Edition's right
    /// margin is empty — and that absence is the signal.
    static let symbolU: CGFloat = 0.872
    static let symbolV: CGFloat = 0.584
}

/// PixelReader is one RGBA8 copy of a frame. Made only once an anchor has been
/// found, so a frame with no readable footer costs nothing.
final class PixelReader {
    let width: Int
    let height: Int
    private let data: UnsafeMutablePointer<UInt8>
    private let bytesPerRow: Int

    init?(_ cg: CGImage) {
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        let stride = w * 4
        let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: stride * h)
        guard let ctx = CGContext(
            data: buf, width: w, height: h, bitsPerComponent: 8, bytesPerRow: stride,
            space: CGColorSpaceCreateDeviceRGB(),
            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        else {
            buf.deallocate()
            return nil
        }
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        width = w; height = h; bytesPerRow = stride; data = buf
    }

    deinit { data.deallocate() }

    /// Sample at a pixel position, with y measured downward from the top — the
    /// bitmap's own order, and the convention the geometry below works in,
    /// since Vision's bottom-up y only makes the tilt maths harder to read.
    func rgb(_ x: CGFloat, _ y: CGFloat) -> (r: CGFloat, g: CGFloat, b: CGFloat)? {
        let px = Int(x.rounded(.down)), py = Int(y.rounded(.down))
        guard px >= 0, px < width, py >= 0, py < height else { return nil }
        let o = py * bytesPerRow + px * 4
        return (CGFloat(data[o]) / 255, CGFloat(data[o + 1]) / 255, CGFloat(data[o + 2]) / 255)
    }

    func luma(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return 0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b
    }

    /// Chroma as the channel spread. Enough to tell a neutral border from a
    /// gold or silver one without dragging in a colour space, and those are the
    /// only cases it has to reject.
    func chroma(_ x: CGFloat, _ y: CGFloat) -> CGFloat? {
        guard let c = rgb(x, y) else { return nil }
        return max(c.r, max(c.g, c.b)) - min(c.r, min(c.g, c.b))
    }
}

func medianOf(_ xs: [CGFloat]) -> CGFloat {
    guard !xs.isEmpty else { return 0 }
    let s = xs.sorted()
    return s.count % 2 == 1 ? s[s.count / 2] : (s[s.count / 2 - 1] + s[s.count / 2]) / 2
}

/// medianAbsoluteDeviation is the spread measure the ring wants: a ring that
/// has slipped half off the card straddles two very different surfaces, and
/// says so here, where a mean would just land somewhere in between.
func medianAbsoluteDeviation(_ xs: [CGFloat]) -> CGFloat {
    let m = medianOf(xs)
    return medianOf(xs.map { abs($0 - m) })
}

/// otsu splits samples in two at the threshold minimizing within-class
/// variance. On a footer line the two class means are the border's paper tone
/// and the credit's ink — the card's own black and white points, measured in
/// the light it was actually photographed under.
func otsu(_ samples: [CGFloat]) -> (dark: CGFloat, bright: CGFloat, darkFraction: CGFloat)? {
    guard samples.count >= 64 else { return nil }
    var hist = [Int](repeating: 0, count: 256)
    for s in samples { hist[max(0, min(255, Int(s * 255)))] += 1 }
    let total = samples.count
    var sum: CGFloat = 0
    for i in 0..<256 { sum += CGFloat(i) * CGFloat(hist[i]) }
    var sumB: CGFloat = 0, weightB = 0, best: CGFloat = -1, threshold = 0
    for i in 0..<256 {
        weightB += hist[i]
        if weightB == 0 { continue }
        let weightF = total - weightB
        if weightF == 0 { break }
        sumB += CGFloat(i) * CGFloat(hist[i])
        let meanB = sumB / CGFloat(weightB)
        let meanF = (sum - sumB) / CGFloat(weightF)
        let between = CGFloat(weightB) * CGFloat(weightF) * (meanB - meanF) * (meanB - meanF)
        if between > best { best = between; threshold = i }
    }
    guard best > 0 else { return nil }
    var dark: [CGFloat] = [], bright: [CGFloat] = []
    for s in samples {
        if Int(s * 255) <= threshold { dark.append(s) } else { bright.append(s) }
    }
    guard !dark.isEmpty, !bright.isEmpty else { return nil }
    return (dark.reduce(0, +) / CGFloat(dark.count),
            bright.reduce(0, +) / CGFloat(bright.count),
            CGFloat(dark.count) / CGFloat(total))
}

/// illusToken matches a line that opens with the illustrator abbreviation,
/// however mangled — "Illus.", "Illus:", "Tins.". Deliberately looser than
/// artistCredit, which demands the whole "Illus. First Last" shape because a
/// false positive *there* eats a card's name. Here a false positive only
/// yields geometry that then fails its own agreement and ring checks, so the
/// trade runs the other way: the strict form missed "Illus: © Jeff A: Menges"
/// (four words) and a bare "Illus." on its own line, and those are footers.
func illusToken(_ s: String) -> Bool {
    guard let first = s.split(whereSeparator: { $0.isWhitespace }).first else { return false }
    guard let last = first.last, last == "." || last == ":" || last == "," else { return false }
    let head = first.lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    return editDistance(head, "illus") <= 2
}

/// Which row of the footer an anchor landed on. They sit at different heights,
/// so the reconstruction has to know which one it is looking at.
enum AnchorKind: String {
    case copyright
    case credit
}

/// personalNameLine matches a bare "First Last" or "First M. Last" with nothing
/// else on the line. On its own this says almost nothing — it is exactly the
/// shape of a card name, which is why it may never identify a *title* — but at
/// the foot of a card it is the illustrator, and that is a position no title
/// occupies.
func personalNameLine(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    guard words.count == 2 || words.count == 3 else { return false }
    return words.allSatisfy { w in
        guard let f = w.first, f.isUppercase else { return false }
        // Allow a middle initial ("Monte M. Moore") but nothing else odd.
        return w.dropFirst().allSatisfy { $0.isLetter || $0 == "." || $0 == "'" || $0 == "-" }
    }
}

/// footerAnchor picks the line to reconstruct the card from: the lowest one
/// that says what it is. Recognition is by *content* — this line claims to be
/// the copyright or the illustrator credit — which is exactly the provenance
/// the deleted classifier lacked when it tried to read a border off whichever
/// edge happened to have contrast.
///
/// The 8th Edition frame forces one exception, and it is worth stating plainly
/// because it is the only anchor here not proven by what the line says. That
/// frame draws a paintbrush where every earlier one writes "Illus.", so its
/// credit arrives as a bare name and no content rule can ever match it — a
/// whole 8th Edition pile produced zero anchors for exactly this reason. What
/// identifies it instead is *where it is*: the bottom-most line on a card that
/// has several, which is a position a title never occupies. It is taken only
/// when no content-proven anchor exists, and everything downstream still has
/// to agree — the two scale estimates, the card fitting its frame, the ring
/// being one uniform surface, the tone landing outside the card's own range.
func footerAnchor(_ lines: [Line]) -> (line: Line, kind: AnchorKind)? {
    let proven = lines.compactMap { line -> (Line, AnchorKind)? in
        if copyrightFurniture(line.text) { return (line, .copyright) }
        if artistCredit(line.text) || illusToken(line.text) { return (line, .credit) }
        return nil
    }
    // Lowest on the card. Vision's y grows upward, so that is the minimum.
    if let best = proven.min(by: { $0.0.box.midY < $1.0.box.midY }) { return best }

    // Positional fallback: a bare personal name, lowest of at least three
    // lines, with real text above it. Fewer lines than that and "lowest" means
    // nothing — a lone credit-shaped line could as easily be the card's name.
    return positionalCredit(lines).map { ($0, .credit) }
}

/// positionalCredit finds the illustrator credit by where it sits rather than
/// what it says: the bottom-most bare personal name on a card that read several
/// lines. Split out from footerAnchor so the measurement can be reported even
/// when a content-proven anchor won.
func positionalCredit(_ lines: [Line]) -> Line? {
    guard lines.count >= 3 else { return nil }
    let ys = lines.map { $0.box.midY }
    guard let low = ys.min(), let high = ys.max(), high - low > 0.05 else { return nil }
    // "At the foot" is the bottom quarter of whatever was read, not literally
    // the lowest line: the power/toughness box and stray flavour fragments sit
    // down there too and are usually read *below* the credit.
    let foot = low + (high - low) * 0.25
    let candidates = lines.filter { $0.box.midY <= foot && personalNameLine($0.text) }
    guard let credit = candidates.min(by: { $0.box.midY < $1.box.midY }) else { return nil }
    let above = lines.filter { $0.box.midY > credit.box.midY + 0.02 }
    guard above.count >= 2 else { return nil }
    return credit
}

/// CardGeometry is the card's own scale and orientation, recovered from where
/// two known lines sit rather than from any detected edge.
struct CardGeometry {
    /// Card height in pixels.
    let heightPx: CGFloat
    /// Footer centre in pixels, y measured downward.
    let origin: CGPoint
    /// Text tilt, radians, in the same y-down frame.
    let theta: CGFloat
    /// Half the footer line's length in pixels — the span along the card that
    /// is provably printed on it, and therefore safe to sample across.
    let halfSpanPx: CGFloat
    /// Where in card space the anchor row sits — every offset is measured from
    /// here, so it depends on which of the two footer rows we latched onto.
    let anchorV: CGFloat
    /// The anchor line's left end in pixels, and where that sits in card space.
    /// nil when the line did not demonstrably start at its beginning, since a
    /// truncated read's left edge is wherever OCR gave up rather than a
    /// landmark. Only the symbol reader needs this; the border ring is sampled
    /// along the anchor's own span and never needs to know the card's width.
    let anchorLeft: CGPoint?
    let anchorLeftU: CGFloat
    /// The two independent estimates, kept so the caller can make them agree.
    let scaleFromBaseline: CGFloat
    let scaleFromGlyph: CGFloat
}

/// cardGeometry reconstructs the card from a footer line and, when there is
/// one, a title line above it.
///
/// Two estimates of the card's height, deliberately: the title-to-footer
/// baseline spans 86% of the card, so it is precise but assumes both anchors
/// are the lines we think they are; the footer's own glyph height assumes
/// nothing but is coarse. Neither is trusted alone. They have to agree, and
/// when they do not the frame is not what it looks like and we stop.
func cardGeometry(footer: Line, kind: AnchorKind, title: Line?, year: Int,
                  frameW: CGFloat, frameH: CGFloat) -> CardGeometry? {
    let anchorV = kind == .copyright ? CardLayout.footerV : CardLayout.creditV
    guard let quad = footer.quad else { return nil }
    // Into pixels, y downward.
    func px(_ p: CGPoint) -> CGPoint { CGPoint(x: p.x * frameW, y: (1 - p.y) * frameH) }
    let tl = px(quad.topLeft), tr = px(quad.topRight)
    let bl = px(quad.bottomLeft)
    let theta = atan2(tr.y - tl.y, tr.x - tl.x)
    let lengthPx = hypot(tr.x - tl.x, tr.y - tl.y)
    let glyphPx = hypot(bl.x - tl.x, bl.y - tl.y)
    guard lengthPx > 1, glyphPx > 0.5 else { return nil }

    let origin = CGPoint(x: (tl.x + tr.x + bl.x + px(quad.bottomRight).x) / 4,
                         y: (tl.y + tr.y + bl.y + px(quad.bottomRight).y) / 4)

    // The line's left end is a card landmark only when the line demonstrably
    // begins at its own beginning. A read that lost its opening — "the Coast,
    // inc. All rights reserved.", observed on a real photograph — starts
    // wherever OCR gave up, which is not a landmark at all. Requiring the
    // opener is the same provenance-by-content rule the vertical anchor uses.
    let leftU = lineOpener(footer.text, kind: kind)
        .flatMap { CardLayout.leftU(kind: kind, prefix: $0, year: year) }
    let anchorLeft = leftU != nil
        ? CGPoint(x: (tl.x + bl.x) / 2, y: (tl.y + bl.y) / 2) : nil
    let anchorLeftU = leftU ?? 0

    let fromGlyph = glyphPx / CardLayout.footerGlyphV
    var fromBaseline = fromGlyph
    if let title = title {
        // Distance between the two rows along the card's own downward axis,
        // so a tilted card measures the same as a square one.
        let titleMid = CGPoint(x: title.box.midX * frameW, y: (1 - title.box.midY) * frameH)
        let down = CGPoint(x: -sin(theta), y: cos(theta))
        let gap = (origin.x - titleMid.x) * down.x + (origin.y - titleMid.y) * down.y
        let span = anchorV - CardLayout.titleV
        if gap > 0, span > 0 { fromBaseline = gap / span }
    }
    return CardGeometry(heightPx: fromBaseline, origin: origin, theta: theta,
                        halfSpanPx: lengthPx / 2, anchorV: anchorV,
                        anchorLeft: anchorLeft, anchorLeftU: anchorLeftU,
                        scaleFromBaseline: fromBaseline, scaleFromGlyph: fromGlyph)
}

/// lineOpener reports whether a footer line starts where the printed row does.
/// The copyright row opens with © or ™ or its year; the credit row opens with
/// the illustrator abbreviation, which illusToken has already matched at the
/// front. Anything else is a line OCR joined partway through.
/// What the anchor line begins with. The landmark's position depends on this
/// as much as on the frame era, because a line that lost its opening starts
/// somewhere else entirely — see CardLayout.leftU.
enum LinePrefix {
    case trademark       // "™ &" — the row's true start from 1998 on
    case copyrightGlyph  // "©" or "™" alone
    case year            // the range or year, opening lost
    case illus           // the illustrator abbreviation
}

func lineOpener(_ s: String, kind: AnchorKind) -> LinePrefix? {
    let t = s.trimmingCharacters(in: .whitespaces)
    guard let token = t.split(whereSeparator: { $0.isWhitespace }).first else { return nil }
    switch kind {
    case .credit:
        return (illusToken(t) || artistCredit(t)) ? .illus : nil
    case .copyright:
        if token.contains("©") || token.contains("™") || token.hasPrefix("(") {
            return .copyrightGlyph
        }
        // "©1995" often loses its symbol and arrives as "01995" or "1995".
        if token.prefix(5).filter({ $0.isNumber }).count >= 4 { return .year }
        // From 1998 the line opens "™ & © 1993-2001", and the trademark mark
        // is what this glyph size mangles most: "TM", "TH", "IM", "Iм" have
        // all been observed. Two letters at the head of a line already known
        // to be a copyright line is the preamble, not prose — every way this
        // line can start *late* begins with a real word ("the Coast, inc.").
        let letters = token.filter { $0.isLetter }
        if letters.count <= 2 && letters.count == token.count { return .trademark }
        return nil
    }
}

extension CardGeometry {
    /// point maps a position in card space — u across the width, v down the
    /// height, both 0…1 from the card's top-left — into frame pixels. nil when
    /// no horizontal landmark was established, which is most of what makes
    /// this safe: without it there is no honest way to say where the card's
    /// right-hand side is, and the symbol lives there.
    func point(u: CGFloat, v: CGFloat) -> CGPoint? {
        guard let left = anchorLeft else { return nil }
        let widthPx = heightPx * cardAspect
        let along = CGPoint(x: cos(theta), y: sin(theta))
        let down = CGPoint(x: -sin(theta), y: cos(theta))
        let du = (u - anchorLeftU) * widthPx
        let dv = (v - anchorV) * heightPx
        return CGPoint(x: left.x + along.x * du + down.x * dv,
                       y: left.y + along.y * du + down.y * dv)
    }
}

/// RingStats is one sampled border ring.
struct RingStats {
    let median: CGFloat
    let mad: CGFloat
    let chroma: CGFloat
    let count: Int
}

/// borderRing samples one edge's border ring, walking along the card's own
/// axis rather than the frame's so a tilted card is sampled inside its border
/// rather than diagonally out of it. `edgeV` is the ring centre in card space.
func borderRing(_ px: PixelReader, _ g: CardGeometry, edgeV: CGFloat, samples: Int = 256) -> RingStats? {
    let along = CGPoint(x: cos(g.theta), y: sin(g.theta))
    let down = CGPoint(x: -sin(g.theta), y: cos(g.theta))
    // Signed distance from the footer row to the ring, down the card.
    let offset = (edgeV - g.anchorV) * g.heightPx
    var lumas: [CGFloat] = [], chromas: [CGFloat] = []
    lumas.reserveCapacity(samples)
    for i in 0..<samples {
        let t = (CGFloat(i) / CGFloat(samples - 1) - 0.5) * 2 * (g.halfSpanPx * 0.9)
        let x = g.origin.x + along.x * t + down.x * offset
        let y = g.origin.y + along.y * t + down.y * offset
        guard let l = px.luma(x, y), let c = px.chroma(x, y) else { continue }
        lumas.append(l); chromas.append(c)
    }
    guard lumas.count >= samples / 2 else { return nil }
    return RingStats(median: medianOf(lumas), mad: medianAbsoluteDeviation(lumas),
                     chroma: medianOf(chromas), count: lumas.count)
}

/// symbolInk measures how much of the type line's right margin is covered by
/// something other than the frame behind it — the expansion symbol, when the
/// set prints one.
///
/// Presence is the whole question here, and it is a far easier one than shape:
/// before Exodus the symbol is monochrome black with no rarity colour, so this
/// is "is there a glyph in that box" rather than "which glyph". It already
/// separates cases neither the year nor the border can — 4BB against Ice Age,
/// both black and both 1995, where 4th Edition's right margin is bare and Ice
/// Age's carries a snowflake.
///
/// The background is estimated from the same band just left of the symbol,
/// which is frame and nothing else, so this is a local contrast measure and
/// carries over from a scan to a desk lamp the way the border ratio does.
func symbolInk(_ px: PixelReader, _ g: CardGeometry) -> (coverage: CGFloat, contrast: CGFloat)? {
    let boxU: CGFloat = 0.055, boxV: CGFloat = 0.026
    func sample(_ centreU: CGFloat) -> [CGFloat] {
        var out: [CGFloat] = []
        for i in 0..<24 {
            for j in 0..<16 {
                let u = centreU + (CGFloat(i) / 23 - 0.5) * boxU
                let v = CardLayout.symbolV + (CGFloat(j) / 15 - 0.5) * boxV
                guard let p = g.point(u: u, v: v), let l = px.luma(p.x, p.y) else { continue }
                out.append(l)
            }
        }
        return out
    }
    let patch = sample(CardLayout.symbolU)
    // The reference sits a symbol's width to the left: still the type-line
    // band, never the type text itself, which ends well before it.
    let reference = sample(CardLayout.symbolU - boxU * 1.6)
    guard patch.count >= 200, reference.count >= 200 else { return nil }
    let base = medianOf(reference)
    let spread = medianAbsoluteDeviation(reference)
    // "Different from the frame" has to mean different by more than the frame
    // varies on its own, or every mottled old-frame background reads as ink.
    let threshold = max(0.10, spread * 4)
    let differing = patch.filter { abs($0 - base) > threshold }
    return (CGFloat(differing.count) / CGFloat(patch.count),
            abs(medianOf(patch) - base))
}

/// footerPatch samples the footer line's own box, giving Otsu both the ink and
/// the surface it is printed on.
func footerPatch(_ px: PixelReader, _ g: CardGeometry) -> (dark: CGFloat, bright: CGFloat,
                                                           darkFraction: CGFloat, chroma: CGFloat,
                                                           clipHigh: CGFloat, clipLow: CGFloat)? {
    let along = CGPoint(x: cos(g.theta), y: sin(g.theta))
    let down = CGPoint(x: -sin(g.theta), y: cos(g.theta))
    let glyphPx = CardLayout.footerGlyphV * g.heightPx
    var lumas: [CGFloat] = [], chromas: [CGFloat] = []
    for i in 0..<192 {
        let t = (CGFloat(i) / 191 - 0.5) * 2 * (g.halfSpanPx * 0.98)
        for j in -3...3 {
            let d = CGFloat(j) / 3 * glyphPx * 0.75
            let x = g.origin.x + along.x * t + down.x * d
            let y = g.origin.y + along.y * t + down.y * d
            guard let l = px.luma(x, y), let c = px.chroma(x, y) else { continue }
            lumas.append(l); chromas.append(c)
        }
    }
    guard let split = otsu(lumas) else { return nil }
    let hi = CGFloat(lumas.filter { $0 > 0.99 }.count) / CGFloat(lumas.count)
    let lo = CGFloat(lumas.filter { $0 < 0.01 }.count) / CGFloat(lumas.count)
    return (split.dark, split.bright, split.darkFraction, medianOf(chromas), hi, lo)
}

/// BorderReading is everything the reader saw, including when it refuses to
/// answer. The numbers ride along regardless of the verdict because that is
/// what --border-probe fits the constants from; only `color` is a claim.
struct BorderReading: Encodable {
    var color: String? = nil        // "white" | "black", absent when abstaining
    var source: String? = nil       // "footer" | "footer+ring"
    var abstain: String = ""        // why, when color is absent
    var anchorKind: String = ""     // which footer row the geometry came from
    /// Where the border sits in the card's own footer tone range: >1 is
    /// brighter than its paper, <0 darker than its ink. The decision.
    var t: Double = 0
    /// Ring minus the frame just inside it — the corroborating check that the
    /// ring is a different surface at all.
    var standoff: Double = 0
    var ringBottom: Double = 0
    var ringTop: Double = 0
    var ringMAD: Double = 0
    var ringChroma: Double = 0
    /// The card's own frame just inside the bottom border — the candidate
    /// reference for normalizing illumination, since it is the same surface
    /// under the same light and is always present.
    var innerBottom: Double = 0
    var innerMAD: Double = 0
    var patchDark: Double = 0
    var patchBright: Double = 0
    var patchSeparation: Double = 0
    var patchDarkFraction: Double = 0
    var patchChroma: Double = 0
    var clipHigh: Double = 0
    var clipLow: Double = 0
    var cardHeightPx: Double = 0
    var scaleAgreement: Double = 0
    var thetaDegrees: Double = 0
    var footerText: String = ""
    var titleText: String = ""
    /// Where the anchors actually sat, as a fraction of the frame's height
    /// measured down from the top. On a clean scan the card *is* the frame, so
    /// these are CardLayout.footerV and titleV measured directly — which is
    /// how those constants get fitted.
    var footerVMeasured: Double = 0
    var titleVMeasured: Double = 0
    var footerGlyphVMeasured: Double = 0
    /// Horizontal extents of the anchors, as fractions of the frame's width.
    /// On a clean scan the card *is* the frame, so these read directly as card
    /// space — which is how the symbol reader's horizontal anchor gets chosen
    /// between them rather than assumed.
    var footerLeftU: Double = 0
    var footerRightU: Double = 0
    var titleLeftU: Double = 0
    /// Whether a horizontal landmark was established at all, and what the type
    /// line's right margin holds if so. Reported, never acted on yet — this is
    /// the measurement the symbol reader will be built from.
    /// Left edge of the positional credit candidate, whether or not it won the
    /// anchor. On a clean scan the copyright row reads and wins, so this is the
    /// only way to measure the credit row's landmark for a frame whose live
    /// photographs anchor on it instead.
    var creditCandidateLeftU: Double = 0
    var horizontalAnchor: Bool = false
    var symbolCoverage: Double = 0
    var symbolContrast: Double = 0
}

/// Thresholds the reading has to clear. Every one of them exists because the
/// alternative is a silent wrong set, which is the most expensive mistake this
/// scanner can make: a wrong border always matches *some* printing, so unlike a
/// misread year or number it cannot fail closed on its own.
enum BorderGate {
    /// Where the border sits in the range of tones the card prints its own
    /// footer with: 0 is that line's ink, 1 is the surface it is printed on.
    ///
    /// The rule is physical rather than fitted. A white border is *brighter
    /// than the card's own paper* and a black border is *darker than its own
    /// ink*, so both verdicts live outside [0, 1] and the ambiguous middle is
    /// everything the card also prints with. Both endpoints move with the
    /// lamp, so their ratio does not — which is the whole reason to measure it
    /// this way.
    ///
    /// Absolute luminance was the first rule and it is what a lamp destroys.
    /// It looked perfect on clean scans — white 0.92…0.93, black 0.04…0.18 —
    /// and then a real session of white-bordered cards read **0.44…0.64**,
    /// overlapping where gold sits, and the reader went silent on all six.
    /// The same six score 1.36…2.44 here.
    static let whiteTone: CGFloat = 1.30
    static let blackTone: CGFloat = -0.01
    /// The footer's two tones must be far enough apart to divide by. Below
    /// this the line is not carrying legible ink and the ratio is noise.
    static let minToneRange: CGFloat = 0.06
    /// How far the ring must stand off the card's own frame just inside it.
    /// This is the check that does not care about illumination at all, and the
    /// one that catches the failure that killed the first attempt: a ring that
    /// has slipped onto the inner frame reads the *same surface* as the
    /// reference, so the gap collapses to nothing and we abstain instead of
    /// classifying a border we never actually looked at. Measured: white
    /// +0.168…+0.698, black −0.068…−0.616, no wrong signs in 52 cards.
    static let minInnerDelta: CGFloat = 0.05
    /// A ring straddling the card's cut edge is not one surface.
    static let maxRingMAD: CGFloat = 0.10
    /// Below this the border band is a couple of pixels and the ring aliases.
    static let minCardHeightPx: CGFloat = 500
    static let maxThetaDegrees: CGFloat = 25
    /// How far the two scale estimates may disagree before the frame is not
    /// what it looks like.
    static let maxScaleDisagreement: CGFloat = 0.35
}

/// readBorder runs the whole chain and returns what it saw. It answers only
/// when every gate passes; `abstain` always says which one did not.
func readBorder(_ cg: CGImage, _ read: CardRead) -> BorderReading {
    var out = BorderReading()
    // Both passes are offered. The whole-frame pass usually has the title too,
    // which is what makes the scale checkable; the band pass is aimed at the
    // footer and often the only one that read it at all.
    guard let anchor = footerAnchor(read.lines + read.bandLines) else {
        out.abstain = "no footer anchor"
        return out
    }
    let footer = anchor.line
    out.footerText = footer.text
    out.anchorKind = anchor.kind.rawValue
    out.footerVMeasured = Double(1 - footer.box.midY)
    out.footerLeftU = Double(footer.box.minX)
    out.footerRightU = Double(footer.box.maxX)
    if let c = positionalCredit(read.lines + read.bandLines) {
        out.creditCandidateLeftU = Double(c.box.minX)
    }

    let frameW = CGFloat(cg.width), frameH = CGFloat(cg.height)
    // The title is whichever plausible line sits highest above the footer.
    let title = read.lines.filter { $0.box.midY > footer.box.midY + 0.2 }
        .max { $0.box.midY < $1.box.midY }
    if let t = title {
        out.titleText = t.text
        out.titleVMeasured = Double(1 - t.box.midY)
        out.titleLeftU = Double(t.box.minX)
    }
    guard let g = cardGeometry(footer: footer, kind: anchor.kind, title: title,
                               year: read.copyrightYear,
                               frameW: frameW, frameH: frameH) else {
        out.abstain = "no geometry"
        return out
    }
    // Without a title there is only one scale estimate, and nothing to check
    // it against — which is how one card reconstructed 50% too tall while
    // reporting perfect agreement with itself. An unchecked estimate is not
    // evidence, so this abstains rather than trusting it.
    if title == nil {
        out.abstain = "no title anchor"
        return out
    }
    out.cardHeightPx = Double(g.heightPx)
    out.thetaDegrees = Double(g.theta * 180 / .pi)
    out.footerGlyphVMeasured = Double(footer.box.height * frameH / max(g.heightPx, 1))
    // The glyph estimate is allowed to be either one text row or two, because
    // Vision frequently returns the credit and copyright rows as a single
    // observation — "Illus: © Jeff A: Menges" is both of them at once — and
    // that box is twice as tall. Measured over the corpus, a merged anchor
    // read 0.032 of card height against a single row's 0.0176. Every card lost
    // to this check was a merged row whose baseline estimate was fine (median
    // error −1.6%), so scoring it as one row was rejecting good geometry for
    // having been read in a shape we had not accounted for.
    let ratio = g.scaleFromGlyph / max(g.scaleFromBaseline, 1)
    let disagreement = min(abs(ratio - 1), abs(ratio / 2 - 1))
    out.scaleAgreement = Double(1 - disagreement)

    guard let px = PixelReader(cg) else {
        out.abstain = "no pixels"
        return out
    }
    // The footer's two tones are the card's own printed black and white point,
    // measured under whatever light the desk had. Note what they are *not*:
    // the border. The first design assumed this line was printed on the border
    // itself, and on an old frame it is printed on the coloured frame inside
    // it — a white-bordered card's footer background reads 0.72 where its
    // border reads 0.93. That is exactly why the ratio works as a classifier
    // rather than as a normalization: the border is the thing that sits
    // *outside* the range the card prints its own footer with.
    guard let patch = footerPatch(px, g) else {
        out.abstain = "footer not bimodal"
        return out
    }
    out.patchDark = Double(patch.dark)
    out.patchBright = Double(patch.bright)
    out.patchSeparation = Double(patch.bright - patch.dark)
    out.patchDarkFraction = Double(patch.darkFraction)
    out.patchChroma = Double(patch.chroma)
    out.clipHigh = Double(patch.clipHigh)
    out.clipLow = Double(patch.clipLow)

    let bottomV = 1 - CardLayout.borderV * CardLayout.ringDepth
    let topV = CardLayout.borderV * CardLayout.ringDepth
    guard let bottom = borderRing(px, g, edgeV: bottomV) else {
        out.abstain = "no bottom ring"
        return out
    }
    out.ringBottom = Double(bottom.median)
    out.ringMAD = Double(bottom.mad)
    out.ringChroma = Double(bottom.chroma)
    let top = borderRing(px, g, edgeV: topV)
    if let top = top { out.ringTop = Double(top.median) }
    out.horizontalAnchor = g.anchorLeft != nil
    if let sym = symbolInk(px, g) {
        out.symbolCoverage = Double(sym.coverage)
        out.symbolContrast = Double(sym.contrast)
    }
    let innerStats = borderRing(px, g, edgeV: CardLayout.innerV)
    if let inner = innerStats {
        out.innerBottom = Double(inner.median)
        out.innerMAD = Double(inner.mad)
    }

    guard let inner = innerStats else {
        out.abstain = "no inner reference"
        return out
    }
    let delta = bottom.median - inner.median
    let toneRange = patch.bright - patch.dark
    guard toneRange >= BorderGate.minToneRange else {
        out.abstain = "footer tones too close"
        return out
    }
    let tone = (bottom.median - patch.dark) / toneRange
    out.t = Double(tone)
    out.standoff = Double(delta)

    // Everything above is measurement and is reported whatever happens. From
    // here down it is a claim, so every gate has to pass.
    if g.heightPx < BorderGate.minCardHeightPx { out.abstain = "card too small"; return out }
    // A card cannot be taller than the picture of it. Cheap, always available,
    // and it is what catches a reconstruction that has gone badly wrong rather
    // than slightly wrong — one corpus card came back 50% too tall while its
    // two scale estimates agreed with each other perfectly, because with no
    // title there was only ever one of them.
    if g.heightPx > frameH * 1.05 { out.abstain = "card larger than frame"; return out }
    if abs(out.thetaDegrees) > Double(BorderGate.maxThetaDegrees) { out.abstain = "too tilted"; return out }
    if disagreement > BorderGate.maxScaleDisagreement { out.abstain = "scales disagree"; return out }
    if bottom.mad > BorderGate.maxRingMAD { out.abstain = "ring not uniform"; return out }

    // Two signals, and both have to say the same thing. The tone position asks
    // whether the border falls outside the range the card prints itself with;
    // the standoff asks whether the ring is even a different surface from the
    // frame beside it. Requiring both is what makes the drift failure — a ring
    // that slid onto the inner frame — abstain rather than answer confidently
    // about a surface it never sampled.
    let verdict: String
    if tone >= BorderGate.whiteTone { verdict = "white" }
    else if tone <= BorderGate.blackTone { verdict = "black" }
    else { out.abstain = "between tones"; return out }

    if abs(delta) < BorderGate.minInnerDelta {
        out.abstain = "ring matches inner frame"
        return out
    }
    if (delta > 0) != (verdict == "white") {
        out.abstain = "tone and frame standoff disagree"
        return out
    }

    // A second edge is not required — a card resting low in frame can have its
    // top out of shot — but when it is there it has to agree, because a ring
    // that disagrees with the opposite ring is measuring something that is not
    // the border.
    // The opposite edge corroborates when it can, and vetoes only when it
    // actively says the other thing. It is not held to clearing the same bar
    // independently, because a card lying on a desk is not lit evenly: the
    // far edge is systematically dimmer, and the reference tones come from the
    // footer at the near edge. Measured on a live session of white-bordered
    // cards, the top ring ran 0.10–0.15 darker than the bottom, which was
    // enough to fail a strict check on three of six cards that were plainly
    // white. An indeterminate second opinion is not a contradiction — it just
    // does not earn the stronger source label.
    if let top = top {
        let topTone = (top.median - patch.dark) / toneRange
        let opposite = verdict == "white"
            ? topTone <= BorderGate.blackTone
            : topTone >= BorderGate.whiteTone
        if opposite {
            out.abstain = "edges disagree"
            return out
        }
        let agrees = verdict == "white"
            ? topTone >= BorderGate.whiteTone
            : topTone <= BorderGate.blackTone
        out.source = agrees ? "footer+ring" : "footer"
    } else {
        out.source = "footer"
    }
    out.color = verdict
    return out
}

// MARK: - Camera discovery

/// How long to wait for a Continuity Camera to publish itself before deciding
/// there isn't one. Override with HOARD_SCAN_WAIT (seconds) when a phone is slow
/// to appear.
let continuityWait = ProcessInfo.processInfo.environment["HOARD_SCAN_WAIT"]
    .flatMap(Double.init) ?? 2.5

/// availableCameras lists the iPhones offered via Continuity Camera.
///
/// This is deliberately iPhone-only. Built-in and USB webcams are fixed,
/// user-facing, and can't be aimed at a card on the desk, so falling back to one
/// yields a capture the OCR can't read — and the failure looks like bad OCR
/// rather than the wrong camera. Better to say no iPhone is connected.
func availableCameras() -> [AVCaptureDevice] {
    // Desk View (.deskViewCamera) is deliberately excluded too: the top-down
    // dewarped feed reads nicely but sits well below the sensor's full photo
    // resolution, so the collector number — already at the edge of what
    // Vision resolves — becomes a coin flip and the review queue fills up.
    AVCaptureDevice.DiscoverySession(
        deviceTypes: [.continuityCamera], mediaType: .video, position: .unspecified
    ).devices
}

/// noPhoneMessage is the one place the "connect an iPhone" guidance is worded.
let noPhoneMessage = """
no iPhone found — hoard scans with Continuity Camera only, not a webcam. \
Connect an iPhone by USB, or unlock it nearby with Continuity Camera enabled \
(Settings › General › AirPlay & Continuity). If you tapped Disconnect on the \
phone, toggle that setting off and on to re-offer it.
"""

/// spinRunLoop pumps the main run loop for up to `seconds`, returning as soon as
/// `ready()` is true. Continuity Camera is published to AVFoundation
/// asynchronously and only to a process that is pumping its run loop, so a bare
/// enumeration on a blocked main thread reports "no iPhone" even when one is
/// connected. Anything that needs a complete device list has to wait like this.
func spinRunLoop(seconds: Double, until ready: () -> Bool) {
    let deadline = Date().addingTimeInterval(seconds)
    while !ready(), Date() < deadline {
        RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
    }
}

/// hasContinuityCamera reports whether an iPhone has shown up yet.
func hasContinuityCamera() -> Bool { !availableCameras().isEmpty }

/// kindLabel is a short human tag shown next to a camera's name in the picker.
/// Everything discoverable is a Continuity Camera, so this is only interesting
/// when someone has two phones paired.
func kindLabel(_ d: AVCaptureDevice) -> String {
    d.deviceType == .continuityCamera ? "iPhone" : "camera"
}

// MARK: - Auto-capture trigger

/// autoDebug traces the auto-trigger's decisions to stderr when asked, mirroring
/// multiDebug. Purely diagnostic: nothing downstream parses these lines.
func autoDebug(_ s: @autoclosure () -> String) {
    guard ProcessInfo.processInfo.environment["HOARD_SCAN_AUTO"] != nil else { return }
    FileHandle.standardError.write(Data("auto: \(s())\n".utf8))
}

/// envDouble reads a numeric tunable from the environment, the same override
/// pattern HOARD_SCAN_WAIT uses, so thresholds can be tuned live without a
/// recompile.
func envDouble(_ name: String, _ fallback: Double) -> Double {
    ProcessInfo.processInfo.environment[name].flatMap(Double.init) ?? fallback
}

/// focusControl selects the focus policy: "lock" (continuous AF, frozen after
/// the first good read — every card sits at the same distance, so the hunt a
/// landing card provokes is pure cost), "continuous" (AF with the trigger's
/// hunt-aware fire gate but no lock), or "off" (no focus code at all — the
/// pre-focus-management behavior, byte for byte).
let focusControl = ProcessInfo.processInfo.environment["HOARD_SCAN_FOCUS"] ?? "lock"

/// autoFocusWait bounds how long a completed stability streak defers its fire
/// waiting for a focus hunt to end, in case the hunt observation wedges — the
/// trigger must never park forever on a KVO that stopped arriving.
let autoFocusWait = envDouble("HOARD_SCAN_FOCUS_WAIT", 1.5)

/// timing writes an always-on per-capture cost line to stderr. HOARD_SCAN_LOG
/// timestamps and tees stderr, so every telemetry run carries its own latency
/// breakdown without a knob — the cost is a couple of lines per card. Nothing
/// downstream parses these; they exist so a "the scanner feels slow" report
/// comes with numbers attached.
func timing(_ s: @autoclosure () -> String) {
    FileHandle.standardError.write(Data("timing: \(s())\n".utf8))
}

/// msSince is the whole milliseconds elapsed from a mark, for timing lines.
func msSince(_ mark: Date) -> Int { Int(Date().timeIntervalSince(mark) * 1000) }

/// How often the auto trigger samples the video stream. Vision's rectangle
/// detector on a ≤1080p buffer costs a few milliseconds, so 5 Hz is nearly free
/// and still reacts within a beat of a card being set down.
// Halved from 0.2: every trigger cost is denominated in samples, so this cuts
// settle, the HOLD re-arm and the searching dwell mechanically. triggerRects is
// ~9ms, so 10Hz is about 9% of one core, and captureOutput already throttles by
// elapsed time and drops late frames.
let autoInterval = envDouble("HOARD_SCAN_AUTO_INTERVAL", 0.1)
/// Consecutive still samples before firing (~0.6 s at the default interval). A
/// hand still moving jitters the detected bounds and never accumulates this
/// streak — which is also what keeps motion blur out of the captures.
// Six at the 0.1s period is 0.6s of proven stillness. Dropping it to four to
// chase latency was measured and reversed: it moved settle only 8% (1,666ms →
// 1,536ms) while waste rose 7% → 12%, because settle is not bound by how long
// the streak is. It is bound by how often the streak is abandoned — see
// autoGraceSamples.
let autoStableSamples = Int(envDouble("HOARD_SCAN_AUTO_STABLE", 6))
/// Consecutive changed samples before re-arming after a capture, so
/// auto-exposure flicker on a card that hasn't moved doesn't refire.
let autoRearmSamples = Int(envDouble("HOARD_SCAN_AUTO_REARM", 3))
// (Hold re-arming pools empty and moved samples into one disruption counter,
// bounded by autoRearmSamples — see the hold phase for why the kinds must not
// reset each other.)
/// Two samples "match" when every paired rectangle overlaps at least this much.
let autoIoU = envDouble("HOARD_SCAN_AUTO_IOU", 0.65)
/// Consecutive bad samples (detection dropout or box jitter) tolerated while a
/// card stabilizes. Vision's rectangle detection flickers on hard cards —
/// foils, borderless frames, low contrast against the desk (one borderless
/// card blinked out on a third of all samples) — and without tolerance a
/// single missed sample restarts the whole stillness streak. Real hand motion
/// fails sample after sample and still resets.
// Six, because this is the knob settle actually turns on. 73% of stabilization
// passes were being abandoned, and the reason is that Vision drops the card in
// *runs*: measured over one session, 18 of 80 dropout runs during stabilizing
// lasted longer than three samples, with several running 8-10. Every one of
// those killed a pass that was already progressing and sent it back to
// searching to start over.
//
// Grace freezes the streak rather than feeding it, so widening it does not
// weaken the evidence a shutter needs — the streak still requires its full
// count of genuinely still samples. It only stops giving up on a card that is
// sitting perfectly still while the detector blinks at it. The cost is that a
// card actually taken away takes 0.6s rather than 0.3s to let go of.
let autoGraceSamples = Int(envDouble("HOARD_SCAN_AUTO_GRACE", 6))
/// A rectangle overlapping a background rectangle at least this much is that
/// background rectangle, not a newly placed card.
let autoBackgroundIoU = envDouble("HOARD_SCAN_AUTO_BG_IOU", 0.5)
/// How many stabilization passes may be abandoned back-to-back before the
/// background baseline is treated as wrong. Measured against a live session:
/// 58 of 59 captures fired after fewer than 8 abandoned passes and the worst
/// healthy stretch was 6, while the stall that prompted this sat at 13. Eight
/// separates them with room on both sides.
let autoBackgroundResetPasses = Int(envDouble("HOARD_SCAN_AUTO_BG_RESET", 8))
/// Abandoned stabilization passes before the stillness path is allowed to fire
/// at all. It exists for cards the rectangle detector cannot hold, and those
/// abandon passes by definition; without this gate it ran in parallel with a
/// working detector and wasted 64% of its fires against the rectangle path's
/// 7%.
let autoStillAfterPasses = Int(envDouble("HOARD_SCAN_AUTO_STILL_AFTER", 3))
/// How long the on-screen bracket survives a detector blink, and how long it
/// takes to glide between positions. Presentation only — neither touches what
/// the trigger decides.
let outlineHoldSeconds = envDouble("HOARD_SCAN_OUTLINE_HOLD", 0.5)
let outlineEaseSeconds = envDouble("HOARD_SCAN_OUTLINE_EASE", 0.12)
/// Samples of frame-to-frame stillness before the scene alone may fire the
/// shutter. Six at the 0.1s period is 0.6s of a motionless picture — the same
/// evidence the rectangle path used to demand, gathered without needing a
/// rectangle. Set HOARD_SCAN_AUTO_STILL=0 to disable the path entirely.
let autoStillSamples = Int(envDouble("HOARD_SCAN_AUTO_STILL", 6))
/// Mean per-cell luma change below which two frames count as the same picture.
/// Above sensor noise, below a hand moving.
let autoStillDelta = envDouble("HOARD_SCAN_AUTO_STILL_DELTA", 2.5)
/// How much the picture must differ from the one we last captured before the
/// stillness path may fire again — what stops a parked scene re-firing.
let autoSceneChanged = envDouble("HOARD_SCAN_AUTO_SCENE_CHANGED", 6.0)
/// Spread of the middle of the frame below which there is nothing worth
/// photographing. Bare desk is smooth; a card is not.
let autoSceneDetail = envDouble("HOARD_SCAN_AUTO_SCENE_DETAIL", 12.0)

/// AutoTrigger decides when a framed card has settled enough to shutter without
/// a keypress. It is deliberately camera-free — rectangle boxes in, fire/phase
/// callbacks out — so the state machine can be reasoned about (and traced)
/// apart from AVFoundation.
///
/// Every method must be called on the main thread; the controller hops there
/// after Vision finishes on its analysis queue, which keeps the machine
/// lock-free alongside the capture path (already main-thread).
final class AutoTrigger {
    enum Phase: String {
        case off, searching, stabilizing, capturing, hold
    }

    private(set) var phase: Phase = .off
    /// fire is called exactly once per SEARCHING→…→CAPTURING pass.
    var onFire: (() -> Void)?
    /// onPhase is called on every transition, for the preview overlay and the
    /// wire events.
    var onPhase: ((Phase) -> Void)?
    /// onBoxes is called every sample with the rectangles the trigger is
    /// actually considering — new-since-armed, background excluded — so the
    /// preview outline shows candidates, not the desk.
    var onBoxes: (([CGRect]) -> Void)?

    /// The scene signature is the candidate boxes sorted largest-first: stable
    /// enough to compare across samples, cheap enough to compare at 4 Hz.
    private var prevSig: [CGRect] = []
    private var lastNovel: [CGRect] = []
    private var heldSig: [CGRect] = []
    private var stableCount = 0
    private var graceCount = 0
    private var disruptCount = 0
    /// When the current stabilize pass began, so a fire can report how long
    /// the machine (not the human) took to settle on the card.
    private var stabilizeBegan: Date?
    /// When a completed streak first deferred its fire to a focus hunt; the
    /// autoFocusWait valve fires anyway once this is old enough.
    private var fireDeferredAt: Date?
    /// Rectangles that are furniture, not cards: whatever was in frame when
    /// auto armed (a desk has notepads and coasters — rectangles all), plus
    /// anything that fired and then photographed as no-card. Only a rectangle
    /// not in this set can arm the trigger.
    private var background: [CGRect] = []
    private var needBaseline = true
    /// The frame signature from the previous sample, and the one taken when we
    /// last fired. Together they answer "has the picture stopped moving" and
    /// "is it a different picture from the one we already photographed".
    private var prevScene: [UInt8] = []
    private var capturedScene: [UInt8] = []
    private var stillCount = 0
    /// How much of the current streak came from detector blinks rather than
    /// real detections. Caps how far stillness alone may carry a pass.
    private var blinkCount = 0
    /// Stabilization passes abandoned since the last capture. A pass that
    /// starts and dies means something looked like a candidate and could not
    /// sustain it, which is what an absorbed card does to the trigger.
    private var abandonedPasses = 0

    func setEnabled(_ on: Bool) {
        if on {
            guard phase == .off else { return }
            stableCount = 0
            disruptCount = 0
            prevSig = []
            heldSig = []
            background = []
            needBaseline = true
            abandonedPasses = 0
            prevScene = []
            capturedScene = []
            stillCount = 0
            fireDeferredAt = nil
            move(to: .searching)
        } else {
            guard phase != .off else { return }
            move(to: .off)
        }
    }

    /// observe feeds one sampled frame's detected rectangles through the
    /// machine. focusSettled is the camera's word that the lens is not mid-
    /// hunt: a hunt blurs edges, so whatever the detector reports during one
    /// is noise — the machine freezes rather than mistaking blur for motion,
    /// and never fires into it (a capture mid-hunt is the out-of-focus scan).
    func observe(_ boxes: [CGRect], scene: [UInt8] = [], focusSettled: Bool = true) {
        let sig = boxes.sorted { $0.width * $0.height > $1.width * $1.height }
        if phase == .off {
            onBoxes?([])
            return
        }
        if needBaseline {
            background = sig
            needBaseline = false
            autoDebug("baseline: \(sig.count) background rect(s)")
        }
        let novel = sig.filter { b in
            !background.contains { iou($0, b) >= autoBackgroundIoU }
        }
        // Per-sample firehose for diagnosing a card the trigger won't see:
        // every sample's raw and candidate counts, with the largest box's
        // size, gated behind its own env so ordinary traces stay readable.
        if ProcessInfo.processInfo.environment["HOARD_SCAN_AUTO_TRACE"] != nil {
            let biggest = sig.first.map {
                String(format: "%.2fx%.2f", $0.width, $0.height)
            } ?? "-"
            autoDebug("sample \(phase.rawValue): rects=\(sig.count) novel=\(novel.count) biggest=\(biggest)")
        }
        lastNovel = novel
        onBoxes?(novel)
        // Whether the picture itself moved, decided before anything updates
        // the remembered frame. Both the fallback path and the dropout rule
        // below read it, so it is computed once, here.
        let sceneStill = !scene.isEmpty && !prevScene.isEmpty
            && sceneDelta(prevScene, scene) <= autoStillDelta
        // The stillness path, run before the rectangle machine so a card the
        // detector cannot hold still gets photographed. It is a floor, not a
        // replacement: whenever rectangles work they fire first and this never
        // reaches its count.
        let firedOnStillness = trackStillness(scene, still: sceneStill, focusSettled: focusSettled)
        if !scene.isEmpty { prevScene = scene }
        if firedOnStillness { return }
        switch phase {
        case .off, .capturing:
            return
        case .searching:
            if !novel.isEmpty {
                prevSig = novel
                stableCount = 1
                graceCount = 0
                blinkCount = 0
                stabilizeBegan = Date()
                move(to: .stabilizing)
            }
        case .stabilizing:
            // A focus hunt freezes the machine outright: no streak growth (a
            // blurred frame is not evidence of stillness), no grace burn or
            // reset (its jitter is not evidence of motion). A streak that
            // completed before the hunt fires the moment it ends — or when
            // the wait valve expires, so a wedged observation can't park us.
            if !focusSettled {
                if stableCount >= autoStableSamples {
                    maybeFire(focusSettled: false)
                }
                return
            }
            // A bad sample — the detector missed the card, or its box jittered
            // past the IoU bar — is tolerated a few times with the streak
            // frozen: Vision flickers on foils and borderless frames. Only a
            // sustained miss (card gone) or sustained mismatch (hand still
            // moving) restarts anything.
            if novel.isEmpty {
                // An empty sample is not evidence the card moved — it is
                // evidence the detector blinked, and it blinks constantly:
                // 220 of 522 stabilizing samples in one live session returned
                // no rectangle at all while a card sat motionless in frame.
                // Treating each of those as a bad sample burned grace and
                // restarted the streak, which is why settle ran at more than
                // twice its floor.
                //
                // The pixels settle it. If the picture has not changed since
                // the last sample, nothing moved, so the miss was the detector
                // and the card is still exactly where it was — count it toward
                // the streak. This is the same argument the fragment rule
                // already makes, with better evidence: frame-to-frame
                // stillness is a stronger proof that a card is holding still
                // than a box happening to land twice in the same place.
                //
                // Guarded hard, because the first cut of this was a disaster:
                // 82% of captures read nothing. A spurious box puts the
                // trigger in stabilizing, every later sample is empty, the
                // desk is perfectly still — and it counted its way to a
                // shutter on nothing, over and over. Stillness alone says the
                // picture is not moving; it does not say a card is there.
                //
                // So the blink only counts when the rest of the evidence
                // already agrees: the detector really saw the card at least
                // twice, the middle of the frame has something in it, the
                // scene differs from the one we last photographed, and at most
                // half the streak may be made of blinks. Anything less and it
                // is the old grace path, which is the safe direction.
                let realDetections = stableCount - blinkCount
                if sceneStill, realDetections >= 2, blinkCount < autoStableSamples / 2,
                    sceneDetail(scene) >= autoSceneDetail,
                    sceneDelta(capturedScene, scene) >= autoSceneChanged {
                    graceCount = 0
                    stableCount += 1
                    blinkCount += 1
                    autoDebug("detector blinked on a still scene, stable "
                        + "\(stableCount)/\(autoStableSamples)")
                    if stableCount >= autoStableSamples {
                        maybeFire(focusSettled: true)
                    }
                    return
                }
                graceCount += 1
                if graceCount > autoGraceSamples {
                    fireDeferredAt = nil
                    abandonPass()
                    move(to: .searching)
                }
                return
            }
            if fragmentsOf(prevSig, novel) {
                // Borderless art crumbles under the detector: a sample often
                // returns a high-contrast SLIVER of the very card it found
                // whole a beat earlier. A fragment inside the known box is
                // evidence of stillness, not motion — count it toward the
                // streak, but keep the remembered box at full size.
                graceCount = 0
                stableCount += 1
                autoDebug("fragment counted, stable \(stableCount)/\(autoStableSamples)")
                if stableCount >= autoStableSamples {
                    maybeFire(focusSettled: true)
                }
                return
            }
            // The same relation the other way round. fragmentsOf only asked
            // whether the new boxes sit inside the remembered ones, so once
            // the streak had latched onto a sliver, the card reappearing whole
            // was not "inside" it and read as motion — the streak reset at the
            // exact moment the detector finally got it right. Live: a
            // motionless Flare of Cultivation alternated between 0.37x0.88 and
            // slivers as small as 0.08x0.13, and took 3,867ms to settle.
            //
            // A box that contains what we were watching is the detector
            // finding *more* of the same still card, which is better evidence,
            // not worse. Count it, and grow the remembered box so the streak
            // continues from the fuller read rather than the sliver.
            // Gated on the picture as well as the geometry: a hand sweeping in
            // also produces a box that contains what we were watching, and
            // that is motion, not a better look at a still card. Requiring the
            // frame to be unchanged separates the two for free.
            if sceneStill, fragmentsOf(novel, prevSig) {
                graceCount = 0
                stableCount += 1
                prevSig = novel
                autoDebug("card seen whole again, stable \(stableCount)/\(autoStableSamples)")
                if stableCount >= autoStableSamples {
                    maybeFire(focusSettled: true)
                }
                return
            }
            if !matches(prevSig, novel) {
                graceCount += 1
                if graceCount > autoGraceSamples {
                    autoDebug("scene moved, streak reset (\(novel.count) candidate(s))")
                    stableCount = 1
                    graceCount = 0
                    blinkCount = 0
                    fireDeferredAt = nil
                    prevSig = novel
                } else {
                    autoDebug("flicker tolerated \(graceCount)/\(autoGraceSamples)")
                }
                return
            }
            graceCount = 0
            stableCount += 1
            autoDebug("stable \(stableCount)/\(autoStableSamples), \(novel.count) candidate(s)")
            if stableCount >= autoStableSamples {
                maybeFire(focusSettled: true)
                return
            }
            prevSig = novel
        case .hold:
            // A hunt's blur says nothing about the scene: freeze the counter
            // both ways. Counting it as disruption would re-arm on pure blur
            // — a refire on the very card just shot.
            if !focusSettled { return }
            // The held card flickers like any hard card: a blink of empty
            // detection is not a removal, and a jittered-but-overlapping box
            // is not a swap. What re-arms is accumulated DISRUPTION of either
            // kind — occlusion and box motion pool into one counter, because
            // a hand placing the next card on top of the pile (stacking is a
            // supported rhythm, not a mistake) alternates between the two.
            // Calm samples DECAY the counter rather than zeroing it: live
            // traces showed placement disruption arriving in 1–2 sample
            // bursts with settled samples interleaved, sawing a hard-reset
            // counter between 1 and 2 forever while the user reached for the
            // spacebar. An isolated blink still dies to the decay; a real
            // placement out-accumulates it. After the re-arm, the new top
            // card fires even though it sits exactly where the last one did —
            // novelty is judged against the desk baseline, never against the
            // card just shot.
            if !novel.isEmpty && holdMatches(heldSig, novel) {
                if disruptCount > 0 {
                    disruptCount -= 1
                }
            } else {
                disruptCount += 1
                autoDebug("disrupted \(disruptCount)/\(autoRearmSamples)")
                if disruptCount >= autoRearmSamples {
                    stableCount = 0
                    prevSig = novel
                    nudged = false // the scene really changed; fires announce again
                    move(to: .searching)
                }
            }
        }
    }

    /// maybeFire pulls the trigger when the lens agrees, defers when it is
    /// mid-hunt, and gives up deferring once the wait valve expires.
    /// trackStillness advances the pixel-stillness path and reports whether it
    /// fired.
    ///
    /// Three things must hold together, and each one is load-bearing:
    ///
    ///   still — consecutive frames are the same picture, so nothing is moving
    ///   changed — the picture differs from the one we last captured, so a
    ///     scene nobody has touched cannot photograph itself forever
    ///   detail — the middle of the frame has structure, so lifting a card away
    ///     leaves something changed and still that is nevertheless bare desk
    ///
    /// Without the third the shutter would fire every time a card was removed.
    private func trackStillness(_ scene: [UInt8], still: Bool, focusSettled: Bool) -> Bool {
        guard autoStillSamples > 0, !scene.isEmpty else { return false }
        guard phase == .searching || phase == .stabilizing else {
            stillCount = 0
            return false
        }
        // Only once rectangles have actually been failing. A detector that is
        // holding the card will fire first and better; pre-empting it is how
        // this path spent two thirds of its shutters on nothing.
        guard abandonedPasses >= autoStillAfterPasses else {
            stillCount = 0
            return false
        }
        // Blur reads as stillness — every edge softens and stops moving — so a
        // focus hunt must not accumulate evidence here either.
        guard focusSettled else { return false }
        guard still else {
            stillCount = 0
            return false
        }
        stillCount += 1
        guard stillCount >= autoStillSamples else { return false }
        guard sceneDetail(scene) >= autoSceneDetail else { return false }
        guard sceneDelta(capturedScene, scene) >= autoSceneChanged else { return false }
        autoDebug("still for \(stillCount) samples, firing without a rectangle "
            + "(\(lastNovel.count) candidate(s))")
        stillCount = 0
        fire()
        return true
    }

    private func maybeFire(focusSettled: Bool) {
        if focusSettled {
            fire()
            return
        }
        guard let since = fireDeferredAt else {
            fireDeferredAt = Date()
            autoDebug("streak complete, waiting out a focus hunt")
            return
        }
        if Date().timeIntervalSince(since) >= autoFocusWait {
            autoDebug("focus never settled in \(Int(autoFocusWait * 1000))ms, firing anyway")
            fire()
        }
    }

    /// fire is the one auto-shutter path: reports how long the machine took
    /// to settle on the card, then moves to capturing and pulls the trigger.
    private func fire() {
        if let t = stabilizeBegan {
            timing("settle=\(msSince(t))ms")
            stabilizeBegan = nil
        }
        fireDeferredAt = nil
        abandonedPasses = 0
        // The picture we are about to photograph. Until the scene differs from
        // it, the stillness path has nothing new to shoot.
        capturedScene = prevScene
        stillCount = 0
        move(to: .capturing)
        onFire?()
    }

    /// abandonPass records a stabilization pass that started and died, and
    /// condemns the background baseline once that has happened enough times in
    /// a row.
    ///
    /// The baseline is learned once, from whatever sat in frame the instant
    /// auto armed, and is never re-learned — so a card already on the desk at
    /// that moment becomes furniture for the rest of the session, invisible at
    /// exactly the spot every card lands. Live: `baseline: 1 background
    /// rect(s)`, then 46 seconds of the detector finding the card and the
    /// filter deleting it, ending only when the user physically lifted the
    /// card and put it back far enough off the learned box to read as novel.
    ///
    /// Repeated abandoned passes are the tell, and a better one than "every
    /// rectangle was swallowed": an idle desk whose real furniture is
    /// correctly absorbed never enters stabilizing at all, so it cannot
    /// trigger this. Only a scene that keeps *almost* producing a candidate
    /// can, which is precisely what a half-absorbed card does.
    ///
    /// It only ever forgets. Nothing is added to the baseline at runtime —
    /// that is the memory that once killed auto capture at the exact spot
    /// every card lands, and clearing is the safe direction: the worst case is
    /// one wasted capture on real furniture, after which HOLD parks on it.
    private func abandonPass() {
        abandonedPasses += 1
        guard abandonedPasses >= autoBackgroundResetPasses, !background.isEmpty else { return }
        autoDebug("\(abandonedPasses) passes abandoned with nothing captured, "
            + "clearing the \(background.count)-rect background baseline")
        background = []
        abandonedPasses = 0
    }

    /// captureBegan holds the machine while any capture — auto or the space
    /// key — is in flight, so a manual shutter in auto mode can't double-fire.
    func captureBegan() {
        guard phase != .off else { return }
        if phase != .capturing { move(to: .capturing) }
    }

    /// nudged marks that the current arming came from the parent's rearm
    /// nudge rather than the scene changing — the fire it produces is a quiet
    /// recheck, not a capture worth announcing.
    private(set) var nudged = false

    /// forceRearm is the parent's content-aware nudge: geometry cannot tell a
    /// card stacked squarely on the pile from the card just shot, but the
    /// parent knows what it already processed — it re-arms, the scene fires,
    /// and an identical read is its to discard.
    func forceRearm() {
        guard phase == .hold else { return }
        stableCount = 0
        disruptCount = 0
        prevSig = []
        nudged = true
        autoDebug("rearm nudge from parent")
        move(to: .searching)
    }

    /// captureFinished parks the machine on the candidates it just shot; only
    /// a changed scene (card swapped, removed, or stacked over) re-arms it.
    ///
    /// A shot that reads as no card is deliberately NOT learned as background.
    /// That rule existed to silence furniture that fired once — but telemetry
    /// showed it absorbing the scanning pile itself after one glared empty
    /// read, killing auto capture at the exact spot every subsequent card
    /// lands on. HOLD already stops a no-card rectangle from re-firing until
    /// it moves, which is all the protection furniture needs.
    func captureFinished() {
        guard phase != .off else { return }
        heldSig = lastNovel
        disruptCount = 0
        nudged = false
        move(to: .hold)
    }

    private func move(to next: Phase) {
        guard next != phase else { return }
        autoDebug("\(phase.rawValue) → \(next.rawValue)")
        phase = next
        onPhase?(next)
    }

    private func matches(_ a: [CGRect], _ b: [CGRect]) -> Bool {
        guard a.count == b.count else { return false }
        for (x, y) in zip(a, b) where iou(x, y) < autoIoU { return false }
        return true
    }

    /// holdMatches is the forgiving variant for the parked phase: the shot
    /// card's box wobbles as exposure hunts over foil, and holding it only
    /// needs "still roughly the same rectangle", not stillness.
    private func holdMatches(_ a: [CGRect], _ b: [CGRect]) -> Bool {
        guard a.count == b.count else { return false }
        for (x, y) in zip(a, b) where iou(x, y) < autoBackgroundIoU { return false }
        return true
    }

    /// fragmentsOf reports whether every current box sits (almost) inside some
    /// remembered box — the crumbled detection a borderless card produces
    /// while sitting perfectly still.
    private func fragmentsOf(_ prev: [CGRect], _ cur: [CGRect]) -> Bool {
        guard !prev.isEmpty, !cur.isEmpty else { return false }
        return cur.allSatisfy { c in
            prev.contains { p in
                let inter = p.intersection(c)
                guard !inter.isNull, !inter.isEmpty else { return false }
                let area = c.width * c.height
                return area > 0 && (inter.width * inter.height) / area >= 0.8
            }
        }
    }

    private func iou(_ a: CGRect, _ b: CGRect) -> CGFloat {
        let inter = a.intersection(b)
        if inter.isNull || inter.isEmpty { return 0 }
        let i = inter.width * inter.height
        let u = a.width * a.height + b.width * b.height - i
        return u > 0 ? i / u : 0
    }
}

/// triggerRects runs the rectangle detector the trigger samples with. Only the
/// boxes matter here, never the text — but which boxes matters a great deal:
/// the raw detector also returns the rectangles *inside* a card (the art frame
/// and the text box) and any speck of desk clutter, and since the stability
/// check compares whole rectangle sets between samples, one flickering speck
/// resets the stillness streak forever. So this filters harder than cardRects:
/// a real size floor, a containment pass that keeps only outermost boxes, and
/// a cap at the few largest — the cards, not their furniture.
///
/// No orientation is passed on purpose: Vision's aspect-ratio bounds are
/// shorter-dimension over longer-dimension, so a sideways card passes the same
/// filter and the trigger doesn't care which way up the sensor is.
/// sceneGridW/H is the resolution of the frame signature — coarse on purpose.
/// It has to answer "did anything move" and "is there detail here", not "what
/// is this", and a coarse grid is both cheap and immune to sensor noise.
let sceneGridW = 16
let sceneGridH = 24

/// sceneSignature reduces a video frame to a small luma grid.
///
/// It exists because the fire decision cannot keep depending on Vision finding
/// a rectangle. A borderless card has no border: the art runs to the edge, so
/// the only edge is card-against-desk, and the detector loses it constantly —
/// measured at 93 stabilization passes started against 21 fired in one live
/// session. The user's experience of that is a scanner that will not fire, and
/// the user's response is to nudge the card, which restarts the cycle.
///
/// Pixels do not have that problem. Whether the scene is *moving* is answerable
/// without knowing what is in it.
///
/// Deliberately cheap: this runs on every sample beside triggerRects, on the
/// analysis queue, at twice the old rate.
func sceneSignature(_ buffer: CVPixelBuffer) -> [UInt8] {
    CVPixelBufferLockBaseAddress(buffer, .readOnly)
    defer { CVPixelBufferUnlockBaseAddress(buffer, .readOnly) }
    let w = CVPixelBufferGetWidth(buffer), h = CVPixelBufferGetHeight(buffer)
    guard w >= sceneGridW, h >= sceneGridH else { return [] }

    // 420f/420v carry luma in plane 0; a packed BGRA buffer has no planes and
    // its bytes are interleaved. Both are handled, and anything else declines.
    let planar = CVPixelBufferGetPlaneCount(buffer) > 0
    guard let base = planar
        ? CVPixelBufferGetBaseAddressOfPlane(buffer, 0)
        : CVPixelBufferGetBaseAddress(buffer) else { return [] }
    let stride = planar
        ? CVPixelBufferGetBytesPerRowOfPlane(buffer, 0)
        : CVPixelBufferGetBytesPerRow(buffer)
    let step = planar ? 1 : 4
    let px = base.assumingMemoryBound(to: UInt8.self)

    var grid = [UInt8](repeating: 0, count: sceneGridW * sceneGridH)
    for gy in 0..<sceneGridH {
        let y = min(h - 1, (gy * h) / sceneGridH + h / (sceneGridH * 2))
        for gx in 0..<sceneGridW {
            let x = min(w - 1, (gx * w) / sceneGridW + w / (sceneGridW * 2))
            if planar {
                grid[gy * sceneGridW + gx] = px[y * stride + x]
            } else {
                // BGRA: a green-weighted approximation is close enough to luma
                // for a motion test and avoids three multiplies per cell.
                let o = y * stride + x * step
                grid[gy * sceneGridW + gx] =
                    UInt8((Int(px[o]) + 2 * Int(px[o + 1]) + Int(px[o + 2])) / 4)
            }
        }
    }
    return grid
}

/// sceneDelta is the mean absolute difference between two signatures, or a
/// large number when they cannot be compared.
func sceneDelta(_ a: [UInt8], _ b: [UInt8]) -> Double {
    guard !a.isEmpty, a.count == b.count else { return 255 }
    var sum = 0
    for i in 0..<a.count { sum += abs(Int(a[i]) - Int(b[i])) }
    return Double(sum) / Double(a.count)
}

/// sceneDetail is the spread of the middle of the frame — high where a card
/// with art and text sits, low on bare desk. It is what stops the stillness
/// path firing at an empty surface, which is otherwise both changed and still
/// the moment a card is lifted away.
func sceneDetail(_ g: [UInt8]) -> Double {
    guard !g.isEmpty else { return 0 }
    var v: [Double] = []
    for gy in (sceneGridH / 4)..<(3 * sceneGridH / 4) {
        for gx in (sceneGridW / 4)..<(3 * sceneGridW / 4) {
            v.append(Double(g[gy * sceneGridW + gx]))
        }
    }
    guard v.count > 1 else { return 0 }
    let m = v.reduce(0, +) / Double(v.count)
    return (v.map { ($0 - m) * ($0 - m) }.reduce(0, +) / Double(v.count)).squareRoot()
}

func triggerRects(_ buffer: CVPixelBuffer) -> [CGRect] {
    let req = VNDetectRectanglesRequest()
    req.minimumAspectRatio = 0.3
    req.maximumAspectRatio = 1.0
    req.minimumSize = 0.1
    // Low enough to keep seeing the hard cards — foils and borderless frames
    // flicker at higher bars — while the size floor, the containment pass and
    // the background baseline absorb what a lower bar lets through.
    req.minimumConfidence = 0.35
    req.quadratureTolerance = 25
    req.maximumObservations = 8
    do {
        try VNImageRequestHandler(cvPixelBuffer: buffer, options: [:]).perform([req])
    } catch {
        return []
    }
    let boxes = (req.results ?? []).map { $0.boundingBox }
        .sorted { $0.width * $0.height > $1.width * $1.height }
    var kept: [CGRect] = []
    for b in boxes {
        let swallowed = kept.contains { k in
            let inter = k.intersection(b)
            return !inter.isNull && inter.width * inter.height > 0.7 * b.width * b.height
        }
        if !swallowed { kept.append(b) }
    }
    // The largest few are the cards; anything past that is noise whose coming
    // and going would only reset the stillness streak.
    return Array(kept.prefix(4))
}

// MARK: - Sound synthesis

/// SoundBank plays the price tiers' casino sounds — a low woody knock for
/// bulk, a bright service bell for a win, and a harp-run glissando for a
/// jackpot (the owner's picks from the 2026-08 audition of synthesized
/// candidates). Everything is synthesized into PCM buffers once at init —
/// additive sine bursts with exponential decay — so the app bundles no audio
/// files and owes no one a license. Engine failure — no output device,
/// aggregate-device weirdness — degrades to the system Glass chime, never to
/// silence.
///
/// A queued card gets its own voice — a soft two-note rise, the sound of a
/// question being asked — because review is a request ("is this right?"),
/// not a price outcome.
///
/// A tier's sound can also be replaced outright: HOARD_SCAN_SOUND_BULK /
/// _WIN / _JACKPOT / _REVIEW each take a path to an audio file (anything
/// NSSound reads — wav, aiff, mp3, m4a), for users who film or publish
/// their scanning sessions and need audio they hold a license to
/// distribute. An unreadable path reports one error event and falls back to
/// the synth.
///
/// Main-thread only, like the rest of the controller's state.
final class SoundBank {
    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private var buffers: [String: AVAudioPCMBuffer] = [:]
    /// User-supplied replacements by tier, loaded from the env; these win
    /// over the synth buffers and play even when the engine failed.
    private var custom: [String: NSSound] = [:]
    /// The custom sound last started, stopped before the next play so rapid
    /// scans cut tails exactly like the engine path's player.stop().
    private var playing: NSSound?
    private var ok = false

    /// One synthesis event: freqs sound together from t for dur seconds,
    /// decaying exponentially — a struck, ringing thing.
    private typealias Strike = (t: Double, freqs: [Double], dur: Double, amp: Double)

    init() {
        let env = ProcessInfo.processInfo.environment
        let volume = Float(max(0, min(1, envDouble("HOARD_SCAN_HUD_VOLUME", 1.0))))
        for tier in ["bulk", "win", "jackpot", "review"] {
            guard let path = env["HOARD_SCAN_SOUND_\(tier.uppercased())"], !path.isEmpty else {
                continue
            }
            if let snd = NSSound(contentsOf: URL(fileURLWithPath: path), byReference: true) {
                snd.volume = volume
                custom[tier] = snd
            } else {
                emit(Event(event: "error",
                           message: "could not load \(tier) sound \(path); using the built-in"))
            }
        }
        let format = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 2)
        guard let format else { return }
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: format)
        engine.mainMixerNode.outputVolume = Float(envDouble("HOARD_SCAN_HUD_VOLUME", 1.0))
        do {
            try engine.start()
        } catch {
            return
        }
        // Bulk: a low woody knock, gone in 50ms.
        buffers["bulk"] = render(format, [(0, [420, 840, 1260], 0.05, 0.55)], length: 0.12)
        // Win: a single bright service bell (fundamental plus an inharmonic
        // 2.76× partial, which is what reads as "bell" rather than "tone").
        buffers["win"] = render(format, [(0, [2093, 5777], 0.4, 0.42)], length: 0.55)
        // Jackpot: a pentatonic sweep, two octaves in under a second, landing
        // on a held octave chord — a harp run up to the top of the machine.
        // Offsets are fixed, never random: the sound being identical every
        // time is what makes it recognizable.
        var gliss: [Strike] = []
        let penta = [523.25, 587.33, 659.25, 783.99, 880.0,
                     1046.5, 1174.7, 1318.5, 1568.0, 1760.0] // C D E G A ×2
        for (i, f) in penta.enumerated() {
            gliss.append((Double(i) * 0.055, [f], 0.12, 0.32))
        }
        gliss.append((0.62, [2093.0, 1046.5, 4186.0], 0.9, 0.48))
        buffers["jackpot"] = render(format, gliss, length: 1.9)
        // Review: two soft notes rising a fourth — "hm-hmm?" — the upward
        // inflection of a question. Warm low partials (f + 2f) keep it
        // marimba-ish and unhurried, nothing like the win bell's brightness.
        buffers["review"] = render(format, [
            (0.00, [440, 880], 0.12, 0.34),   // A4
            (0.16, [587.33, 1174.7], 0.28, 0.38), // D5, held — the "?"
        ], length: 0.6)
        ok = true
    }

    /// render sums the strikes into one stereo buffer, soft-clipped so
    /// overlapping notes saturate rather than wrap.
    private func render(_ format: AVAudioFormat, _ strikes: [Strike],
                        length: Double) -> AVAudioPCMBuffer? {
        let sr = format.sampleRate
        let frames = AVAudioFrameCount(length * sr)
        guard let buf = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames),
              let channels = buf.floatChannelData else { return nil }
        buf.frameLength = frames
        let left = channels[0], right = channels[1]
        for s in strikes {
            let start = Int(s.t * sr)
            let count = Int(s.dur * sr)
            let tau = s.dur / 5
            for i in 0..<count where start + i < Int(frames) {
                let t = Double(i) / sr
                var v = 0.0
                for f in s.freqs { v += sin(2 * .pi * f * t) }
                v *= s.amp * exp(-t / tau) / Double(s.freqs.count)
                left[start + i] += Float(v)
                right[start + i] += Float(v)
            }
        }
        for i in 0..<Int(frames) {
            left[i] = tanh(left[i])
            right[i] = tanh(right[i])
        }
        return buf
    }

    /// play cuts whatever tail is still ringing and starts the tier's sound —
    /// a rapid next card should clip the last fanfare, not queue behind it.
    /// Unknown tiers and the unpriced shrug keep the familiar Glass chime.
    func play(tier: String) {
        // Both paths stop first, whichever played last: tails never stack.
        playing?.stop()
        player.stop()
        if let snd = custom[tier] {
            playing = snd
            snd.play()
            return
        }
        guard ok, let buf = buffers[tier] else {
            NSSound(named: "Glass")?.play()
            return
        }
        player.scheduleBuffer(buf)
        player.play()
    }
}

// MARK: - Price HUD

/// PriceHUD renders resolved prices over the camera preview: a transient
/// tier-styled flash of the amount just scanned, a persistent running session
/// total in the corner, and a coin shower for jackpots. All Core Animation
/// layers on top of the preview layer; all animation is *explicit*, so the
/// disabled-actions transactions the outline drawing uses never interfere.
///
/// Layer coordinates are y-up (the preview layer is not flipped): the total
/// pins near y=0 (the bottom), coins rain toward -y, flashes float toward +y.
final class PriceHUD {
    private let container = CALayer()
    private let totalLayer = CATextLayer()
    private var scale: CGFloat = 2
    private weak var preview: AVCaptureVideoPreviewLayer?

    private static let gold = NSColor(calibratedRed: 1, green: 0.84, blue: 0, alpha: 1)

    /// videoRect is where the video actually shows: .resizeAspect letterboxes
    /// the frame inside the view, and a HUD pinned to the *view* corner lands
    /// in the black bars beside a portrait feed. Falls back to the whole view
    /// when there is no video to measure (the demo, or before the session
    /// starts), where the two are the same thing anyway.
    private var videoRect: CGRect {
        let bounds = container.bounds
        guard let preview else { return bounds }
        let r = preview.layerRectConverted(fromMetadataOutputRect: CGRect(x: 0, y: 0, width: 1, height: 1))
        guard !r.isNull, !r.isInfinite, r.width > 40, r.height > 40 else { return bounds }
        return r.intersection(bounds)
    }

    /// attach hangs the HUD's layers off the preview layer, above the outline.
    func attach(to host: AVCaptureVideoPreviewLayer, scale: CGFloat) {
        self.scale = scale
        self.preview = host
        container.frame = host.bounds
        totalLayer.font = NSFont.monospacedDigitSystemFont(ofSize: 26, weight: .bold)
        totalLayer.fontSize = 26
        totalLayer.alignmentMode = .right
        totalLayer.foregroundColor = NSColor.white.cgColor
        totalLayer.shadowColor = NSColor.black.cgColor
        totalLayer.shadowOpacity = 0.9
        totalLayer.shadowRadius = 2
        totalLayer.shadowOffset = .zero
        totalLayer.contentsScale = scale
        totalLayer.isHidden = true
        container.addSublayer(totalLayer)
        host.addSublayer(container)
        layout(bounds: host.bounds)
    }

    /// layout re-pins the HUD to the view. Called from PreviewView.layout()
    /// inside its disabled-actions transaction, so resizes never animate.
    func layout(bounds: CGRect) {
        container.frame = bounds
        repinTotal()
    }

    /// repinTotal puts the running total just inside the video frame's top
    /// right (layer coords are y-up: the top is maxY). Re-run on every show
    /// too, not just view layout — the video rect settles after the session
    /// starts and moves when the preview is rotated, neither of which lays
    /// out the view.
    private func repinTotal() {
        let rect = videoRect
        totalLayer.frame = CGRect(x: rect.maxX - 252, y: rect.maxY - 48, width: 240, height: 34)
    }

    /// setScale re-rasterizes the text for the current display — without this
    /// a window dragged to a different-density screen renders blurry.
    func setScale(_ s: CGFloat) {
        scale = s
        totalLayer.contentsScale = s
    }

    /// show renders one result: tier flash (and jackpot shower), then the
    /// silent total update.
    func show(_ cmd: HUDCommand) {
        if let tier = cmd.tier {
            flash(amount: cmd.amount, tier: tier)
            if tier == "jackpot" { coinShower() }
        }
        if let total = cmd.total { setTotal(total) }
    }

    private func setTotal(_ total: Double) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        repinTotal()
        totalLayer.string = String(format: "$%.2f", total)
        totalLayer.isHidden = false
        CATransaction.commit()
        // A brief gold pulse, so silently-landing money (a review confirm) is
        // still visible in the corner of the eye.
        let pulse = CABasicAnimation(keyPath: "foregroundColor")
        pulse.fromValue = Self.gold.cgColor
        pulse.toValue = NSColor.white.cgColor
        pulse.duration = 0.6
        totalLayer.add(pulse, forKey: "pulse")
    }

    /// flash floats the just-scanned amount up the middle of the frame. Each
    /// flash is its own layer with a timed removal, so rapid scans overlap
    /// harmlessly instead of fighting over one layer's animations.
    private func flash(amount: Double?, tier: String) {
        let text: String
        if tier == "review" {
            // A queued card isn't a win yet: the terminal has the review, and
            // the printing (so the price) is still unverified.
            text = "Needs Review"
        } else if let amount {
            text = String(format: "+$%.2f", amount)
        } else {
            text = "$—"
        }
        let size: CGFloat
        let color: NSColor
        let weight: NSFont.Weight
        switch tier {
        case "win": (size, color, weight) = (40, Self.gold, .bold)
        case "jackpot": (size, color, weight) = (56, Self.gold, .heavy)
        default: (size, color, weight) = (28, .systemGray, .semibold)
        }

        let layer = CATextLayer()
        layer.string = text
        layer.font = NSFont.monospacedDigitSystemFont(ofSize: size, weight: weight)
        layer.fontSize = size
        layer.alignmentMode = .center
        layer.foregroundColor = color.cgColor
        layer.contentsScale = scale
        if tier == "win" || tier == "jackpot" {
            layer.shadowColor = Self.gold.cgColor
            layer.shadowOpacity = tier == "jackpot" ? 0.95 : 0.8
            layer.shadowRadius = tier == "jackpot" ? 12 : 8
            layer.shadowOffset = .zero
        } else {
            layer.shadowColor = NSColor.black.cgColor
            layer.shadowOpacity = 0.8
            layer.shadowRadius = 2
            layer.shadowOffset = .zero
        }
        let rect = videoRect
        layer.frame = CGRect(x: rect.minX, y: rect.midY - size, width: rect.width, height: size * 1.4)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        layer.opacity = 0 // model value: invisible outside the animation
        container.addSublayer(layer)
        CATransaction.commit()

        let duration = 1.6
        let fade = CAKeyframeAnimation(keyPath: "opacity")
        fade.values = [0, 1, 1, 0]
        fade.keyTimes = [0, 0.08, 0.62, 1]
        let pop = CAKeyframeAnimation(keyPath: "transform.scale")
        pop.values = tier == "jackpot" ? [0.4, 1.18, 1.0, 1.0] : [0.6, 1.06, 1.0, 1.0]
        pop.keyTimes = [0, 0.12, 0.24, 1]
        let rise = CAKeyframeAnimation(keyPath: "position.y")
        rise.values = [layer.position.y, layer.position.y, layer.position.y + 44]
        rise.keyTimes = [0, 0.55, 1]
        let group = CAAnimationGroup()
        group.animations = [fade, pop, rise]
        group.duration = duration
        layer.add(group, forKey: "flash")
        DispatchQueue.main.asyncAfter(deadline: .now() + duration + 0.1) {
            layer.removeFromSuperlayer()
        }
    }

    /// coinShower rains gold coins from the top edge for a beat. The cell keeps
    /// emitting until the *layer's* birthRate multiplier hits zero — zeroing
    /// only the cell's is the classic eternal-shower bug — and the layer is
    /// removed outright once the last coin has fallen.
    private func coinShower() {
        let rect = videoRect
        let emitter = CAEmitterLayer()
        emitter.frame = container.bounds
        emitter.emitterShape = .line
        emitter.emitterPosition = CGPoint(x: rect.midX, y: rect.maxY + 16)
        emitter.emitterSize = CGSize(width: rect.width, height: 1)

        let cell = CAEmitterCell()
        cell.contents = Self.coinImage
        cell.birthRate = 70
        cell.lifetime = 2.5
        cell.velocity = 320
        cell.velocityRange = 140
        cell.emissionLongitude = -.pi / 2 // straight down in y-up coords
        cell.emissionRange = .pi / 8
        cell.yAcceleration = -600 // gravity pulls toward y=0
        cell.spin = 2
        cell.spinRange = 6
        cell.scale = 0.5
        cell.scaleRange = 0.25
        cell.alphaSpeed = -0.35
        emitter.emitterCells = [cell]
        container.addSublayer(emitter)

        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            emitter.birthRate = 0
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3.5) {
            emitter.removeFromSuperlayer()
        }
    }

    /// coinImage is the emitter's particle: the 🪙 emoji rasterized once — the
    /// app bundles no image assets. It replaced a hand-drawn gold disc with a
    /// $ glyph, which at particle size read as a bitcoin token.
    private static let coinImage: CGImage? = {
        let side: CGFloat = 64
        let image = NSImage(size: NSSize(width: side, height: side), flipped: false) { rect in
            let glyph = NSAttributedString(string: "🪙", attributes: [
                .font: NSFont.systemFont(ofSize: 52),
            ])
            glyph.draw(at: CGPoint(x: rect.midX - glyph.size().width / 2,
                                   y: rect.midY - glyph.size().height / 2))
            return true
        }
        var proposed = CGRect(origin: .zero, size: NSSize(width: side, height: side))
        return image.cgImage(forProposedRect: &proposed, context: nil, hints: nil)
    }()
}

// MARK: - Live capture (AppKit window + AVFoundation)

final class CaptureController: NSObject, AVCapturePhotoCaptureDelegate {
    /// The uniqueID of the camera to use; nil takes the highest-ranked available one.
    private let deviceID: String?
    /// Extra clockwise rotation, in degrees, applied on top of whatever the
    /// rotation coordinator reports. The coordinator returns 0° for some
    /// Continuity Camera setups — it can't always tell how the phone is being
    /// held — which leaves a portrait-held phone previewing sideways. The user
    /// corrects it once with ←/→ and the caller remembers the result.
    private var manualRotation: Int
    let session = AVCaptureSession()
    fileprivate let photoOutput = AVCapturePhotoOutput()
    private var window: NSWindow?
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private var deviceName = "camera"
    private var rotationCoordinator: AVCaptureDevice.RotationCoordinator?
    private var rotationObservation: NSKeyValueObservation?
    /// The live capture device, kept so the torch can be toggled mid-session.
    private var device: AVCaptureDevice?
    /// Whether this device supports Center Stage (the system's auto-framing);
    /// gates the feature advertisement and the toggle.
    private var framingAvailable = false
    /// Whether this device carries a torch — the phone's flashlight, which
    /// Continuity Camera exposes and which is the only light macOS lets an
    /// app control (exposure bias is iOS-only).
    private var torchAvailable = false
    /// Whether the torch is currently on; session-scoped, never persisted —
    /// it drains the phone and AVFoundation kills it on session end anyway.
    private var torchOn = false
    /// Focus management (HOARD_SCAN_FOCUS): the lens observation, whether it
    /// is mid-hunt right now, whether we froze it, and how many consecutive
    /// captures read as nothing while frozen (two thaws it — the rig moved).
    private var focusObservation: NSKeyValueObservation?
    private var focusHunting = false
    private var focusHuntBegan: Date?
    private var focusLocked = false
    private var emptyReadsWhileLocked = 0
    /// Whether auto-framing is currently on. Forced off at startup — Center
    /// Stage state persists system-wide (a FaceTime call leaves it on), and
    /// its "zoom to the subject" crop is exactly the sometimes-too-close
    /// startup framing that makes cards unscannable.
    private var autoFraming = false

    /// The price HUD and its sounds. The bank is lazy so the audio engine's
    /// first-time spin-up never delays camera readiness — it starts on the
    /// first resolved card instead.
    private let hud = PriceHUD()
    private lazy var sounds = SoundBank()

    // Auto-capture. The video output feeds the trigger only — stills always go
    // through photoOutput, so auto and manual captures are identical on the
    // wire apart from the auto tag.
    fileprivate let videoOutput = AVCaptureVideoDataOutput()
    private let analysisQueue = DispatchQueue(label: "hoard-scan.analysis")
    fileprivate var lastAnalysis = Date.distantPast
    fileprivate let autoTrigger = AutoTrigger()
    /// Whether the session could attach a video tap; when false, auto mode is
    /// unavailable and the ready event doesn't advertise it.
    private var autoAvailable = false
    /// Whether the user asked for auto mode (--auto, auto-on, or the a key).
    private var autoRequested: Bool
    /// Set between an auto fire and its photo delegate, to tag the scan event.
    private var pendingAuto = false
    /// A monotonic counter so an auto session's debug images don't overwrite
    /// each other the way a single "capture-ocr.png" would.
    private var captureCount = 0
    /// When the last capture's processing ended. Video samples taken before
    /// this are stale — they queued up behind the OCR on the main thread and
    /// describe the shutter moment, not the present — and replaying them
    /// against HOLD faked a full disruption burst in a single millisecond
    /// (observed live: instant double-fires).
    private var lastCaptureFinishedAt = Date.distantPast

    init(deviceID: String?, rotation: Int, auto: Bool = false) {
        self.deviceID = deviceID
        self.manualRotation = ((rotation / 90) % 4 + 4) % 4 * 90
        self.autoRequested = auto
        super.init()
    }

    /// startDemo brings the window up with no camera at all — a black preview
    /// under a live HUD — so the price tiers' looks and sounds can be
    /// eyeballed by piping `result` lines on stdin. The capture session never
    /// runs; everything else (stdin, keys, shutdown) works as in live mode.
    func startDemo() {
        deviceName = "HUD demo"
        buildWindow()
        emit(Event(event: "ready", rotation: manualRotation, device: deviceName,
                   features: ["hud"]))
    }

    func start() {
        // Wait for the requested phone (or any phone) to publish itself rather
        // than giving up on a device that's a beat slow to appear.
        spinRunLoop(seconds: continuityWait) {
            guard let id = deviceID else { return hasContinuityCamera() }
            return availableCameras().contains { $0.uniqueID == id }
        }

        // An explicitly requested phone wins; a stale id (that phone walked away)
        // falls back to another paired one. There is deliberately no webcam
        // fallback — see availableCameras().
        let cameras = availableCameras()
        guard let device = deviceID.flatMap({ id in cameras.first { $0.uniqueID == id } })
            ?? cameras.first
        else {
            fail(noPhoneMessage)
        }
        guard let input = try? AVCaptureDeviceInput(device: device) else {
            fail("could not open \(device.localizedName)")
        }
        deviceName = device.localizedName
        // macOS exposes no camera zoom API (videoZoomFactor is iOS-only), but
        // it does expose Center Stage. Take app control and force it off so
        // every session starts on the full, uncropped frame — auto-framing's
        // subject-tracking crop is the "too close" startup the user can't
        // otherwise explain, because its state rides along from whatever app
        // used the camera last.
        framingAvailable = device.activeFormat.isCenterStageSupported
        AVCaptureDevice.centerStageControlMode = .app
        AVCaptureDevice.isCenterStageEnabled = false
        self.device = device
        // Continuity Camera does not bridge the phone's flashlight — hasTorch
        // is false there as of macOS today — so the torch feature usually
        // stays dark. The capability line makes that verifiable in a
        // HOARD_SCAN_LOG instead of a matter of memory.
        torchAvailable = device.hasTorch
        setUpFocus(device)
        let focusCaps = device.isFocusModeSupported(.continuousAutoFocus)
            ? "af" + (device.isFocusModeSupported(.locked) ? "+lock" : "") : "fixed"
        let caps = "scan: \(device.localizedName) [\(kindLabel(device))] torch=\(device.hasTorch) "
            + "centerStage=\(device.activeFormat.isCenterStageSupported) "
            + "focus=\(focusCaps) (policy \(focusControl))\n"
        FileHandle.standardError.write(Data(caps.utf8))
        guard session.canAddInput(input), session.canAddOutput(photoOutput) else {
            fail("could not configure capture session")
        }
        // Ask for full-resolution stills. The default preset is .high, which caps
        // the capture at video resolution (1080p on Continuity Camera) and leaves
        // the collector number under 1% of the frame height — right at the edge of
        // what Vision can resolve. .photo gives the sensor's full frame instead.
        if session.canSetSessionPreset(.photo) {
            session.sessionPreset = .photo
        }
        session.addInput(input)
        session.addOutput(photoOutput)
        // Deliberately NOT setting photoOutput.maxPhotoDimensions. Asking the
        // format for its largest still looks like the way to beat the preset's
        // cap, and it backfires: read before the session runs, activeFormat is
        // still the device's low-res default, so pinning the output to *its*
        // maximum capped captures at 640x480 — a third of the linear resolution
        // the preset alone was already giving. Measured on Continuity Camera,
        // which reports 1920x1080 either way; the opt-in bought nothing and
        // cost two thirds of the frame. The still size is reported after
        // startRunning instead, where it is true.

        // The video tap is best-effort: a session that refuses it just means no
        // auto mode, which the ready event's feature list reports honestly.
        if session.canAddOutput(videoOutput) {
            videoOutput.alwaysDiscardsLateVideoFrames = true
            videoOutput.setSampleBufferDelegate(self, queue: analysisQueue)
            session.addOutput(videoOutput)
            autoAvailable = true
        }
        autoTrigger.onFire = { [weak self] in self?.autoFire() }
        autoTrigger.onPhase = { [weak self] phase in self?.autoPhaseChanged(phase) }
        autoTrigger.onBoxes = { [weak self] novel in self?.updateOutlines(novel) }

        buildWindow()
        trackRotation(of: device)
        DispatchQueue.global(qos: .userInitiated).async { [session] in
            session.startRunning()
            // Re-apply once the session is live: before it starts, the preview
            // layer's connection may not exist yet, so the initial apply is a
            // silent no-op and the KVO observer only fires if the angle later
            // changes.
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                self.applyRotation()
                // Don't announce readiness until the photo output has an active
                // connection and the stream has had a moment to settle:
                // startRunning() returning isn't the same as being able to take a
                // picture, and a capture in that gap fails with an opaque
                // "operation could not be completed".
                spinRunLoop(seconds: 5) {
                    self.photoOutput.connection(with: .video)?.isActive == true
                }
                spinRunLoop(seconds: 0.75) { false }
                // Now that the session is live the active format is the one
                // that will actually be used, so this number means something.
                // It is the first thing to check when reads go soft.
                if let d = self.device {
                    let dims = d.activeFormat.supportedMaxPhotoDimensions
                        .max { Int($0.width) * Int($0.height) < Int($1.width) * Int($1.height) }
                    let label = dims.map { "\($0.width)x\($0.height)" } ?? "unreported"
                    FileHandle.standardError.write(Data("scan: still=\(label)\n".utf8))
                }
                emit(Event(event: "ready", rotation: self.manualRotation,
                           device: self.deviceName,
                           features: (self.autoAvailable ? ["auto", "rearm"] : [])
                               + (self.framingAvailable ? ["framing"] : [])
                               + (self.torchAvailable ? ["torch"] : [])
                               + ["effects", "hud", "border"]))
                if self.autoRequested { self.setAuto(true) }
            }
        }
    }

    /// trackRotation keeps the preview upright as the phone is turned. A camera
    /// delivers frames in its own fixed orientation, so without this a
    /// portrait-held iPhone previews rotated 90°. The coordinator reports the
    /// angle that levels the horizon, and is KVO-observable so turning the phone
    /// mid-session re-levels the preview live.
    private func trackRotation(of device: AVCaptureDevice) {
        let coordinator = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: previewLayer)
        rotationCoordinator = coordinator
        applyRotation()
        rotationObservation = coordinator.observe(
            \.videoRotationAngleForHorizonLevelPreview, options: [.new]
        ) { [weak self] _, _ in
            DispatchQueue.main.async { self?.applyRotation() }
        }
    }

    /// autoPreviewAngle is what the coordinator thinks levels the preview; 0 when
    /// it can't tell how the phone is held.
    private var autoPreviewAngle: CGFloat {
        rotationCoordinator?.videoRotationAngleForHorizonLevelPreview ?? 0
    }

    /// effectiveRotation is the total turn the user is actually looking at: what
    /// the coordinator contributes to the preview plus their manual correction.
    /// The captured pixels get this same total, so OCR reads exactly the framing
    /// that was confirmed on screen.
    private var effectiveRotation: Int {
        (Int(autoPreviewAngle) + manualRotation) % 360
    }

    /// applyRotation pushes the effective angle onto the preview connection and
    /// refreshes the title.
    ///
    /// The still is deliberately left unrotated. The coordinator's *capture*
    /// angle can differ from its *preview* angle — here by a full 180° — so
    /// letting it rotate the photo turns the capture by a different amount than
    /// the preview showed, and OCR reads an upside-down card no matter what the
    /// user picks. Instead the whole turn is applied to the pixels afterwards
    /// from effectiveRotation, which keeps the two paths identical by
    /// construction.
    private func applyRotation() {
        if let conn = previewLayer?.connection {
            let angle = CGFloat(effectiveRotation)
            if conn.isVideoRotationAngleSupported(angle) { conn.videoRotationAngle = angle }
        }
        if let conn = photoOutput.connection(with: .video), conn.isVideoRotationAngleSupported(0) {
            conn.videoRotationAngle = 0
        }
        // The analysis buffers stay unrotated too: the trigger's rectangle
        // filter is orientation-free, and the outline drawing converts from
        // sensor space — a rotated buffer would put the cue a quarter-turn off.
        if let conn = videoOutput.connection(with: .video), conn.isVideoRotationAngleSupported(0) {
            conn.videoRotationAngle = 0
        }
        updateTitle()
    }

    /// rotate turns the preview a quarter-turn and remembers the choice. The
    /// parent is told so it can persist the correction without waiting for the
    /// window to close.
    private func rotate(clockwise: Bool) {
        manualRotation = (manualRotation + (clockwise ? 90 : 270)) % 360
        applyRotation()
        emit(Event(event: "rotation", rotation: manualRotation))
    }

    /// setUpFocus points continuous autofocus at where cards land and starts
    /// watching the lens: a hunt blurs every edge in frame, so the trigger is
    /// told to treat those samples as noise rather than motion, and the fire
    /// is deferred until the hunt ends. HOARD_SCAN_FOCUS=off skips all of it.
    private func setUpFocus(_ device: AVCaptureDevice) {
        guard focusControl != "off" else { return }
        do {
            try device.lockForConfiguration()
            if device.isFocusPointOfInterestSupported {
                device.focusPointOfInterest = CGPoint(x: 0.5, y: 0.5)
            }
            if device.isFocusModeSupported(.continuousAutoFocus) {
                device.focusMode = .continuousAutoFocus
            }
            device.unlockForConfiguration()
        } catch {
            autoDebug("focus setup refused: \(error.localizedDescription)")
        }
        focusObservation = device.observe(\.isAdjustingFocus, options: [.new]) { [weak self] dev, _ in
            let hunting = dev.isAdjustingFocus
            DispatchQueue.main.async {
                guard let self, self.focusHunting != hunting else { return }
                self.focusHunting = hunting
                if hunting {
                    self.focusHuntBegan = Date()
                    autoDebug("focus hunt began")
                } else if let t = self.focusHuntBegan {
                    autoDebug("focus hunt ended (\(msSince(t))ms)")
                    self.focusHuntBegan = nil
                }
            }
        }
    }

    /// updateFocusLock freezes the lens after a capture that actually read a
    /// card — every card in a session sits at the same distance, so the hunt
    /// each landing card provokes is pure settle-time cost — and thaws it
    /// after two consecutive empty reads, the signature of a moved rig. Only
    /// the "lock" policy does any of this.
    private func updateFocusLock(afterGoodRead good: Bool) {
        guard focusControl == "lock", let device else { return }
        if good {
            emptyReadsWhileLocked = 0
            guard !focusLocked, device.isFocusModeSupported(.locked) else { return }
            do {
                try device.lockForConfiguration()
                device.focusMode = .locked
                device.unlockForConfiguration()
                focusLocked = true
                autoDebug("focus locked after a good read")
            } catch {
                autoDebug("focus lock refused: \(error.localizedDescription)")
            }
        } else if focusLocked {
            emptyReadsWhileLocked += 1
            guard emptyReadsWhileLocked >= 2,
                  device.isFocusModeSupported(.continuousAutoFocus) else { return }
            do {
                try device.lockForConfiguration()
                device.focusMode = .continuousAutoFocus
                device.unlockForConfiguration()
                focusLocked = false
                emptyReadsWhileLocked = 0
                autoDebug("focus unlocked after consecutive empty reads")
            } catch {
                autoDebug("focus unlock refused: \(error.localizedDescription)")
            }
        }
    }

    /// setAutoFraming toggles Center Stage — the system's subject-tracking
    /// zoom, and the only framing macOS lets an app adjust (the real zoom
    /// APIs are iOS-only). Off means the full, uncropped frame, which is what
    /// card scanning wants; the toggle exists for the desk setups where the
    /// tracked crop happens to frame the pile better. The parent is told so
    /// it can reflect the state without watching the window.
    fileprivate func setAutoFraming(_ on: Bool) {
        guard framingAvailable else {
            emit(Event(event: "error", message: "auto-framing is not adjustable on this camera"))
            return
        }
        autoFraming = on
        AVCaptureDevice.isCenterStageEnabled = on
        updateTitle()
        emit(Event(event: "framing", state: on ? "auto" : "off"))
    }

    /// setTorch turns the phone's flashlight on or off to light the card —
    /// the one brightness control macOS offers (exposure bias is iOS-only).
    /// A refused lock or a thermally-limited torch reports an error and
    /// leaves the session up; the parent is told the state that actually
    /// took, so its mirror never drifts from the hardware.
    fileprivate func setTorch(_ on: Bool) {
        guard torchAvailable, let device else {
            emit(Event(event: "error", message: "no torch on this camera"))
            return
        }
        do {
            try device.lockForConfiguration()
            defer { device.unlockForConfiguration() }
            if on {
                try device.setTorchModeOn(level: AVCaptureDevice.maxAvailableTorchLevel)
            } else {
                device.torchMode = .off
            }
            torchOn = on
        } catch {
            emit(Event(event: "error",
                       message: "could not switch the torch: \(error.localizedDescription)"))
        }
        updateTitle()
        emit(Event(event: "torch", state: torchOn ? "on" : "off"))
    }

    /// updateTitle surfaces the current rotation — including what the automatic
    /// angle contributed — so a wrong orientation is diagnosable at a glance.
    private func updateTitle() {
        let total = Int(autoPreviewAngle) + manualRotation
        let mode = autoTrigger.phase == .off ? "" : "AUTO · "
        let framing = autoFraming ? " · FRAMED" : ""
        let torch = torchOn ? " · TORCH" : ""
        window?.title = "hoard — \(deviceName) · \(mode)\(total % 360)° "
            + "(auto \(Int(autoPreviewAngle))°)\(framing)\(torch) · Space capture · A auto · "
            + "←/→ rotate · Z framing · T torch · V effects · Esc cancel"
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 560),
            styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        win.title = "hoard — \(deviceName) · Space to capture · Esc to cancel"
        win.center()

        let view = PreviewView(frame: win.contentLayoutRect)
        view.autoresizingMask = [.width, .height]
        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspect
        view.previewLayer = preview
        view.wantsLayer = true
        view.layer = preview
        self.previewLayer = preview
        let outline = CAShapeLayer()
        outline.frame = view.bounds
        outline.fillColor = nil
        outline.lineWidth = 3
        // Rounded caps soften the corner brackets — see addCornerBrackets.
        outline.lineCap = .round
        outline.lineJoin = .round
        outline.strokeColor = NSColor.systemYellow.cgColor
        outline.isHidden = true
        preview.addSublayer(outline)
        view.outlineLayer = outline
        hud.attach(to: preview, scale: win.backingScaleFactor)
        view.hud = hud
        view.onKey = { [weak self] key in
            switch key {
            case .space: self?.capture()
            case .escape: self?.shutdown()
            case .rotateLeft: self?.rotate(clockwise: false)
            case .rotateRight: self?.rotate(clockwise: true)
            case .framingToggle: self?.setAutoFraming(self?.autoFraming == false)
            case .torchToggle: self?.setTorch(self?.torchOn == false)
            case .effectsPanel: AVCaptureDevice.showSystemUserInterface(.videoEffects)
            case .autoToggle: self?.setAuto(self?.autoTrigger.phase == .off)
            }
        }
        win.contentView = view
        win.makeKeyAndOrderFront(nil)
        win.makeFirstResponder(view)
        NSApp.activate(ignoringOtherApps: true)
        self.window = win
    }

    /// When the last shutter was requested, for the capture timing line.
    private var captureRequestedAt: Date?

    private func capture() {
        // Re-level right before the shutter in case the phone moved since the
        // last KVO notification.
        applyRotation()
        // Any shutter — auto or manual — parks the trigger, so pressing space
        // in auto mode can't be followed by an auto fire on the same card.
        autoTrigger.captureBegan()
        captureRequestedAt = Date()
        photoOutput.capturePhoto(with: AVCapturePhotoSettings(), delegate: self)
    }

    /// autoFire is the trigger's shutter: identical to a space press except the
    /// resulting scan event is tagged auto. Silent by design — the parent
    /// chimes when the scan resolves (added or queued), and that is the one
    /// sound per card; a shutter pop on top made every card a two-beep event.
    private func autoFire() {
        pendingAuto = true
        capture()
    }

    /// setAuto turns the trigger on or off, keeping the window chrome honest.
    fileprivate func setAuto(_ on: Bool) {
        guard autoAvailable else {
            if on { emit(Event(event: "error", message: "auto capture unavailable on this session")) }
            return
        }
        autoRequested = on
        autoTrigger.setEnabled(on)
        updateTitle()
    }

    /// autoPhaseChanged relays trigger transitions to the wire and the preview
    /// overlay. Only settled phases go on the wire — searching↔stabilizing
    /// flapping is visual, not protocol — and consecutive repeats are deduped.
    private var lastWireState = ""
    private func autoPhaseChanged(_ phase: AutoTrigger.Phase) {
        updateOutlines(lastBoxes)
        let wire: String
        switch phase {
        case .searching, .stabilizing: wire = "armed"
        case .capturing: wire = "capturing"
        case .hold: wire = "held"
        case .off: wire = "off"
        }
        guard wire != lastWireState else { return }
        lastWireState = wire
        emit(Event(event: "auto", rotation: manualRotation, state: wire))
    }

    /// The rectangles the trigger last saw, kept so a phase change can recolor
    /// the outline without waiting for the next sample.
    private var lastBoxes: [CGRect] = []
    /// The box the cue is currently drawn around, and when it was last
    /// confirmed by a real detection — together they let the bracket ride out
    /// a detector blink instead of flickering off.
    private var outlineBox: CGRect?
    private var outlineHeldSince: Date?

    /// updateOutlines traces the trigger's cue at the corners of the one
    /// dominant rectangle — yellow while it settles, green once it's shot —
    /// rather than boxing everything the detector sees. Four viewfinder
    /// brackets read as deliberate framing where the full rectangles,
    /// re-fit on every Vision sample, slid and flickered like the display
    /// was struggling; and one frame beats several, because the extra boxes
    /// were desk clutter and deck boxes the trigger weighs but the user
    /// shouldn't have to look at. The cue still makes a reluctant trigger
    /// diagnosable at a glance: no brackets means no rectangle, jumping
    /// brackets mean the scene never reads as still.
    ///
    /// Vision reports boxes normalized with a bottom-left origin in the
    /// *unrotated* analysis buffer; bracketRect turns each into preview-layer
    /// coordinates, rotation and letterboxing included.
    /// updateOutlines draws the one cue the user sees. It is presentation, not
    /// machinery — the trigger has already made its decision by the time this
    /// runs — and it should look like the app is calmly locked onto a card.
    ///
    /// Drawing the raw per-sample truth does the opposite. Vision drops the
    /// card on roughly two samples in five, so hiding on an empty sample
    /// blinked the brackets five times a second; the largest box changes
    /// between samples, so the cue teleported between candidates; and
    /// disabling implicit animation made every one of those a hard jump. The
    /// result read as chaos and looked broken even while the scan succeeded.
    ///
    /// So: hold the last cue briefly through a blink, ease between positions
    /// instead of snapping, and keep tracking the same box rather than
    /// whichever is largest this instant.
    private func updateOutlines(_ boxes: [CGRect]) {
        lastBoxes = boxes
        guard previewLayer != nil,
              let outline = (window?.contentView as? PreviewView)?.outlineLayer else { return }
        let phase = autoTrigger.phase
        if phase == .off {
            outlineHeldSince = nil
            outlineBox = nil
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            outline.isHidden = true
            CATransaction.commit()
            return
        }
        // Prefer the box nearest the one already being drawn, so a steady hand
        // keeps a steady bracket even when the detector reorders its results.
        var main: CGRect?
        if let held = outlineBox, !boxes.isEmpty {
            main = boxes.min {
                hypot($0.midX - held.midX, $0.midY - held.midY)
                    < hypot($1.midX - held.midX, $1.midY - held.midY)
            }
        } else {
            main = boxes.max { $0.width * $0.height < $1.width * $1.height }
        }
        if let m = main {
            outlineBox = m
            outlineHeldSince = Date()
        } else if let since = outlineHeldSince,
                  Date().timeIntervalSince(since) < outlineHoldSeconds {
            main = outlineBox      // a blink, not a departure: keep the cue up
        } else {
            outlineHeldSince = nil
            outlineBox = nil
        }
        guard let box = main, let r = bracketRect(for: box) else {
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            outline.isHidden = true
            CATransaction.commit()
            return
        }
        let path = CGMutablePath()
        addCornerBrackets(to: path, around: r)
        CATransaction.begin()
        // A short ease is what turns jitter into a cue that feels alive rather
        // than nervous. The shutter itself stays instant — nothing should lag
        // behind the moment of capture.
        CATransaction.setAnimationDuration(phase == .capturing ? 0 : outlineEaseSeconds)
        outline.path = path
        outline.strokeColor = outlineColor(phase)
        outline.lineWidth = phase == .capturing ? 5 : 3
        outline.isHidden = false
        CATransaction.commit()
    }

    /// bracketRect maps a Vision box from the unrotated analysis buffer into
    /// preview-layer coordinates by hand. The obvious route —
    /// layerRectConverted(fromMetadataOutputRect:) — turns the box by the
    /// wrong sense when the preview is rotated: at 270° the cue landed
    /// mirrored through the frame's center, its bottom edge framing the
    /// card's middle (observed live). The capture path already owns the
    /// correct convention — the preview connection and rotatedImage both
    /// turn the buffer *clockwise* by effectiveRotation, and OCR reading
    /// those captures upright proves it — so the box takes the same
    /// clockwise turn, and only the full-frame extent (rotation-symmetric,
    /// so immune to the converter's sense) is asked of the layer.
    private func bracketRect(for b: CGRect) -> CGRect? {
        guard let preview = previewLayer else { return nil }
        let video = preview.layerRectConverted(
            fromMetadataOutputRect: CGRect(x: 0, y: 0, width: 1, height: 1))
        guard !video.isNull, video.width > 1, video.height > 1 else { return nil }
        // Buffer space, normalized, top-left origin.
        var r = CGRect(x: b.minX, y: 1 - b.maxY, width: b.width, height: b.height)
        // The same clockwise turn the preview shows.
        switch ((effectiveRotation % 360) + 360) % 360 {
        case 90:
            r = CGRect(x: 1 - r.maxY, y: r.minX, width: r.height, height: r.width)
        case 180:
            r = CGRect(x: 1 - r.maxX, y: 1 - r.maxY, width: r.width, height: r.height)
        case 270:
            r = CGRect(x: r.minY, y: 1 - r.maxX, width: r.height, height: r.width)
        default:
            break
        }
        // Scale into the displayed video area; layer coordinates are y-up,
        // so the display-space top edge is the layer-space maxY.
        return CGRect(x: video.minX + r.minX * video.width,
                      y: video.minY + (1 - r.maxY) * video.height,
                      width: r.width * video.width,
                      height: r.height * video.height)
    }

    /// addCornerBrackets appends four L-shaped marks squaring a rect's
    /// corners, arms proportional to the card but capped so a close-up card
    /// doesn't wear giant angles.
    private func addCornerBrackets(to path: CGMutablePath, around r: CGRect) {
        let arm = min(min(r.width, r.height) * 0.22, 34)
        // Bottom-left, bottom-right, top-right, top-left (layer coords, y-up).
        path.move(to: CGPoint(x: r.minX, y: r.minY + arm))
        path.addLine(to: CGPoint(x: r.minX, y: r.minY))
        path.addLine(to: CGPoint(x: r.minX + arm, y: r.minY))
        path.move(to: CGPoint(x: r.maxX - arm, y: r.minY))
        path.addLine(to: CGPoint(x: r.maxX, y: r.minY))
        path.addLine(to: CGPoint(x: r.maxX, y: r.minY + arm))
        path.move(to: CGPoint(x: r.maxX, y: r.maxY - arm))
        path.addLine(to: CGPoint(x: r.maxX, y: r.maxY))
        path.addLine(to: CGPoint(x: r.maxX - arm, y: r.maxY))
        path.move(to: CGPoint(x: r.minX + arm, y: r.maxY))
        path.addLine(to: CGPoint(x: r.minX, y: r.maxY))
        path.addLine(to: CGPoint(x: r.minX, y: r.maxY - arm))
    }

    private func outlineColor(_ phase: AutoTrigger.Phase) -> CGColor {
        switch phase {
        case .stabilizing:
            return NSColor.systemYellow.cgColor
        case .capturing:
            return NSColor.systemGreen.cgColor
        case .hold:
            // Parked on the shot card: a quiet green says "already counted".
            return NSColor.systemGreen.withAlphaComponent(0.45).cgColor
        case .searching, .off:
            return NSColor.white.withAlphaComponent(0.35).cgColor
        }
    }

    /// shutdown closes the window and ends the process. The rotation rides along
    /// so a correction made just before closing isn't thrown away.
    func shutdown() {
        // A frozen lens is session state: hand the camera back hunting
        // normally for whatever app uses it next.
        if focusLocked, let device, device.isFocusModeSupported(.continuousAutoFocus),
           (try? device.lockForConfiguration()) != nil {
            device.focusMode = .continuousAutoFocus
            device.unlockForConfiguration()
        }
        emit(Event(event: "closed", rotation: manualRotation))
        session.stopRunning()
        exit(0)
    }

    /// handle runs one command from the parent. These mirror the in-window keys,
    /// so the user can drive the camera from the terminal without switching
    /// windows — which is the point of keeping the session open.
    func handle(command: String) {
        // A command is a verb plus an optional payload after the first space —
        // only `result` carries one today.
        let parts = command.split(separator: " ", maxSplits: 1)
        switch parts.first.map(String.init) ?? "" {
        case "capture": capture()
        case "rotate-left": rotate(clockwise: false)
        case "rotate-right": rotate(clockwise: true)
        case "frame-on": setAutoFraming(true)
        case "frame-off": setAutoFraming(false)
        case "torch-on": setTorch(true)
        case "torch-off": setTorch(false)
        // The system Video Effects panel: Studio Light (the only software
        // lighting macOS offers, since the torch isn't bridged), plus the
        // system's own Center Stage and Desk View toggles.
        case "effects": AVCaptureDevice.showSystemUserInterface(.videoEffects)
        case "auto-on": setAuto(true)
        case "auto-off": setAuto(false)
        case "rearm": autoTrigger.forceRearm()
        case "chime": NSSound(named: "Glass")?.play()
        case "result": showResult(payload: parts.count > 1 ? String(parts[1]) : "")
        case "quit": shutdown()
        default: emit(Event(event: "error", message: "unknown command: \(command)"))
        }
    }

    /// showResult renders one resolved card's price on the HUD: the tier sound
    /// and flash, and/or the running-total update. A malformed payload reports
    /// an error and keeps the session alive, like any bad command.
    private func showResult(payload: String) {
        guard let data = payload.data(using: .utf8), !payload.isEmpty,
              let cmd = try? JSONDecoder().decode(HUDCommand.self, from: data) else {
            emit(Event(event: "error", message: "bad result payload"))
            return
        }
        if let tier = cmd.tier { sounds.play(tier: tier) }
        hud.show(cmd)
    }

    /// A failed capture reports an error but keeps the window open — one bad
    /// frame shouldn't tear down a session the user is mid-way through.
    func photoOutput(_ output: AVCapturePhotoOutput,
                     didFinishProcessingPhoto photo: AVCapturePhoto, error: Error?) {
        let wasAuto = pendingAuto
        pendingAuto = false
        if let error {
            // Park without absorbing: a transient capture failure says nothing
            // about whether the rectangle is a card.
            autoTrigger.captureFinished()
            lastCaptureFinishedAt = Date()
            emit(Event(event: "error", message: "capture failed: \(error.localizedDescription)"))
            return
        }
        guard let (cg, orientation) = decodePhoto(photo) else {
            autoTrigger.captureFinished()
            lastCaptureFinishedAt = Date()
            emit(Event(event: "error", message: "no image from capture"))
            return
        }
        // Normalize the capture's own orientation first, then match the framing
        // the user corrected in the preview. Exactly one rotation each.
        captureCount += 1
        saveDebugImage(cg, "capture-\(captureCount)-raw.png")
        let tRotate = Date()
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: effectiveRotation)
        let rotateMs = msSince(tRotate)
        saveDebugImage(forOCR, "capture-\(captureCount)-ocr.png")
        let (read, cards) = scanFrame(forOCR)
        // A capture that read something proves the current lens distance is
        // right — freeze it there; one that read nothing counts toward the
        // thaw. Decided per capture, whatever fired it.
        updateFocusLock(afterGoodRead: !read.name.isEmpty || !cards.isEmpty)
        // shutter+decode is everything before the pixel work: AVFoundation's
        // shutter latency, the photo decode, and the raw debug write.
        let preMs = captureRequestedAt.map { Int(tRotate.timeIntervalSince($0) * 1000) } ?? 0
        timing("capture \(captureCount) shutter+decode=\(preMs)ms rotate=\(rotateMs)ms "
            + "total=\(captureRequestedAt.map { msSince($0) } ?? 0)ms")
        autoTrigger.captureFinished()
        lastCaptureFinishedAt = Date()
        // Emit and stay live: the window persists so the next card can be framed
        // and captured without relaunching the camera.
        emit(Event(event: "scan", name: read.name, candidates: read.candidates,
                   rotation: manualRotation,
                   collectorNumber: read.collectorNumber, setCode: read.setCode,
                   bottomLines: read.bottomLines, cards: cards,
                   confidence: read.nameConfidence, bandAnchored: read.bandAnchored,
                   auto: wasAuto ? true : nil,
                   collectorAlts: read.collectorAlts.isEmpty ? nil : read.collectorAlts,
                   finishHint: read.finishHint.isEmpty ? nil : read.finishHint))
    }
}

// MARK: - Video tap (auto-trigger sampling)

extension CaptureController: AVCaptureVideoDataOutputSampleBufferDelegate {
    func captureOutput(_ output: AVCaptureOutput,
                       didOutput sampleBuffer: CMSampleBuffer,
                       from connection: AVCaptureConnection) {
        // Runs on analysisQueue. The time gate throttles to autoInterval no
        // matter what frame rate the camera delivers, and Vision running
        // synchronously here self-throttles: late frames are discarded, never
        // queued behind a slow pass.
        guard autoTrigger.phase != .off else { return }
        let now = Date()
        guard now.timeIntervalSince(lastAnalysis) >= autoInterval else { return }
        lastAnalysis = now
        guard let buffer = CMSampleBufferGetImageBuffer(sampleBuffer) else { return }
        let boxes = triggerRects(buffer)
        let scene = sceneSignature(buffer)
        let sampledAt = Date()
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            // Samples taken before the last capture finished are stale: they
            // queued behind the OCR and describe the shutter moment. Feeding
            // them to HOLD faked instant disruption bursts.
            guard sampledAt > self.lastCaptureFinishedAt else { return }
            // The trigger decides which of these are candidates (vs desk
            // furniture) and drives the outline through onBoxes.
            self.autoTrigger.observe(boxes, scene: scene, focusSettled: !self.focusHunting)
        }
    }
}

/// PreviewView hosts the camera preview layer and forwards key presses.
final class PreviewView: NSView {
    enum Key {
        case space, escape, rotateLeft, rotateRight, framingToggle, torchToggle,
             effectsPanel, autoToggle
    }
    var previewLayer: AVCaptureVideoPreviewLayer?
    /// The auto-trigger's cue: an outline traced around each rectangle the
    /// trigger currently sees, kept sized to the view by layout().
    var outlineLayer: CAShapeLayer?
    /// The price HUD, kept sized to the view like the outline.
    var hud: PriceHUD?
    var onKey: ((Key) -> Void)?
    override var acceptsFirstResponder: Bool { true }
    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 49: onKey?(.space)        // space
        case 53: onKey?(.escape)       // esc
        case 123: onKey?(.rotateLeft)  // left arrow
        case 124: onKey?(.rotateRight) // right arrow
        case 6: onKey?(.framingToggle) // z
        case 17: onKey?(.torchToggle)  // t
        case 9: onKey?(.effectsPanel)  // v
        case 0: onKey?(.autoToggle)    // a
        default: super.keyDown(with: event)
        }
    }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        outlineLayer?.frame = bounds
        hud?.layout(bounds: bounds)
        CATransaction.commit()
    }

    /// A window dragged to a different-density display re-rasterizes the HUD
    /// text, or it renders blurry there.
    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        if let scale = window?.backingScaleFactor { hud?.setScale(scale) }
    }
}

// MARK: - App lifecycle

/// Supplies the Dock menu and routes every quit path through the controller.
///
/// Quitting must not go straight to `exit()`: hoard is waiting on stdout for a
/// `closed` event to know the camera window is gone. `CaptureController.shutdown`
/// emits that first, so closing from the Dock leaves the add session in exactly
/// the state it would be in after pressing esc.
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let controller: CaptureController

    init(controller: CaptureController) {
        self.controller = controller
    }

    /// Right-click (or click-and-hold) on the Dock icon.
    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        let menu = NSMenu()
        // A nil target sends the action up the responder chain to NSApp, so this
        // lands in applicationShouldTerminate below just like ⌘Q does.
        menu.addItem(withTitle: "Quit hoard scan",
                     action: #selector(NSApplication.terminate(_:)),
                     keyEquivalent: "")
        return menu
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        controller.shutdown() // emits "closed", stops the session, exits 0
        return .terminateNow
    }

    /// Closing the capture window should end the helper, not leave a menu-bar
    /// ghost with no way back to the camera.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

/// Builds the minimal menu bar an activation-policy `.regular` app needs.
///
/// Without a main menu AppKit gives the app no ⌘Q at all, which is how the
/// helper ended up only closable from the terminal.
func installMainMenu() {
    let appMenu = NSMenu()
    appMenu.addItem(withTitle: "Quit hoard scan",
                    action: #selector(NSApplication.terminate(_:)),
                    keyEquivalent: "q")

    let appItem = NSMenuItem()
    appItem.submenu = appMenu

    let main = NSMenu()
    main.addItem(appItem)
    NSApp.mainMenu = main
}

// MARK: - Entry point

let args = Array(CommandLine.arguments.dropFirst())

if args.contains("--list-devices") {
    // Become a real (if dock-less) app: AVFoundation only advertises Continuity
    // Camera to a GUI process with a running run loop.
    NSApplication.shared.setActivationPolicy(.accessory)

    // Ask for camera access first: device names are only populated once granted,
    // and it puts the permission prompt at picker time rather than mid-capture.
    var granted = false
    var answered = false
    AVCaptureDevice.requestAccess(for: .video) { g in
        granted = g
        answered = true
    }
    spinRunLoop(seconds: 120) { answered }
    if !granted {
        fail("camera access denied — grant it in System Settings › Privacy & Security › Camera")
    }

    // Give a nearby iPhone a moment to publish itself before concluding it isn't
    // there; returns as soon as one appears.
    spinRunLoop(seconds: continuityWait, until: hasContinuityCamera)

    let devices = availableCameras().map {
        Device(id: $0.uniqueID, name: $0.localizedName, kind: kindLabel($0))
    }
    if devices.isEmpty {
        fail(noPhoneMessage, code: 4)
    }
    emit(DeviceList(devices: devices))
    exit(0)
}

// Default to a quarter-turn clockwise: a portrait-held iPhone hands over a
// landscape frame, so the capture arrives turned 90° counter-clockwise. hoard
// passes --rotate explicitly (its own saved value), so this default only applies
// when the helper is run by hand.
var requestedRotation = 90
if let i = args.firstIndex(of: "--rotate"), i + 1 < args.count {
    requestedRotation = Int(args[i + 1]) ?? 90
}

// --border-probe reads one image and reports everything the border reader saw,
// verdict or not. It exists to fit the constants in CardLayout and BorderGate
// against scan/corpus, where the card fills the frame and the card rect is
// therefore known exactly — which is the one thing that corpus can say about
// the border, and the reason it cannot say anything about the *crop*.
if let i = args.firstIndex(of: "--border-probe") {
    guard i + 1 < args.count else { fail("--border-probe requires a file path") }
    let path = args[i + 1]
    guard let (cg, orientation) = cgImage(fromFile: path) else {
        fail("could not read image: \(path)")
    }
    let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: requestedRotation)
    let read = readCard(forOCR)
    var reading = readBorder(forOCR, read)
    reading.footerText = reading.footerText.isEmpty ? "" : reading.footerText
    emit(reading)
    exit(reading.color == nil ? 3 : 0)
}

if let i = args.firstIndex(of: "--image") {
    guard i + 1 < args.count else { fail("--image requires a file path") }
    guard let (cg, orientation) = cgImage(fromFile: args[i + 1]) else {
        fail("could not read image: \(args[i + 1])")
    }
    // Byte-for-byte the live pipeline, so this mode reproduces real scans.
    let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: requestedRotation)
    // Write the same debug bitmap the live path does, so HOARD_SCAN_DEBUG_DIR can
    // be used to tune the bottom band against a still photo.
    saveDebugImage(forOCR, "capture-ocr.png")
    let (read, cards) = scanFrame(forOCR)
    emit(Event(event: "scan", name: read.name, candidates: read.candidates,
               rotation: requestedRotation,
               collectorNumber: read.collectorNumber, setCode: read.setCode,
               bottomLines: read.bottomLines, cards: cards,
               confidence: read.nameConfidence, bandAnchored: read.bandAnchored,
               collectorAlts: read.collectorAlts.isEmpty ? nil : read.collectorAlts,
               finishHint: read.finishHint.isEmpty ? nil : read.finishHint))
    exit(read.name.isEmpty && cards.isEmpty ? 3 : 0)
}

// Live mode: request camera access, then run the AppKit event loop.
var requestedDevice: String?
if let i = args.firstIndex(of: "--device"), i + 1 < args.count {
    requestedDevice = args[i + 1]
}
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let controller = CaptureController(deviceID: requestedDevice, rotation: requestedRotation,
                                   auto: args.contains("--auto"))

// Held in a top-level binding: NSApplication does not retain its delegate.
let appDelegate = AppDelegate(controller: controller)
app.delegate = appDelegate
installMainMenu()

// Commands arrive as bare lines on stdin (capture / rotate-left / rotate-right /
// quit) so the parent can drive the camera while the terminal keeps focus. A
// closed stdin means the parent is gone, so shut down rather than linger with an
// orphaned window.
Thread.detachNewThread {
    while let line = readLine(strippingNewline: true) {
        let cmd = line.trimmingCharacters(in: .whitespaces)
        if cmd.isEmpty { continue }
        DispatchQueue.main.async { controller.handle(command: cmd) }
    }
    DispatchQueue.main.async { controller.shutdown() }
}

if args.contains("--hud-demo") {
    // No camera, no permission prompt: the demo exists to see the HUD.
    DispatchQueue.main.async { controller.startDemo() }
} else {
    AVCaptureDevice.requestAccess(for: .video) { granted in
        DispatchQueue.main.async {
            if !granted { fail("camera access denied — grant it in System Settings › Privacy › Camera") }
            controller.start()
        }
    }
}
app.run()
