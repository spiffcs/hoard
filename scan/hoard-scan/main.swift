// hoard-scan — capture a Magic card image and OCR its title.
//
// Modes:
//   hoard-scan --list-devices   Print the available cameras as JSON, exit.
//   hoard-scan --image <path>   Headless: OCR an existing image file, print JSON, exit.
//   hoard-scan [--device <id>] [--rotate <deg>]
//                               Live session: open a camera preview window and keep
//                               it open, emitting one JSON event per line on stdout
//                               and reading commands (capture / rotate-left /
//                               rotate-right / quit) as lines on stdin. Space, ←/→
//                               and Esc do the same things in the window itself.
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

    var top: CGFloat { box.maxY }
    var width: CGFloat { box.width }
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

/// CollectorRead is one parsed border block: a collector number, the set code
/// printed beside it, and the finish the set line's separator marked — "foil"
/// for the printed star, "nonfoil" for the bullet, "" when the frame carries
/// no marker.
struct CollectorRead: Encodable {
    var number = ""
    var set = ""
    var finish = ""
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
        .map { (offset: $0.offset, text: asciify($0.element)) }

    var sets: [(offset: Int, code: String, finish: String)] = []
    for l in ranked {
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
    func add(_ offset: Int, _ n: String) {
        let near = setNear(offset)
        let read = CollectorRead(number: normalizeNumber(n), set: near.code, finish: near.finish)
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
            add(l.offset, n)
        }
    }
    // Bare numbers after: a lone number is much easier to confuse with a
    // planeswalker's loyalty or a copyright year.
    for l in ranked {
        let t = l.text.trimmingCharacters(in: .whitespaces)
        if let n = group(collectorSoloRE, t), !looksLikeAYear(n) {
            add(l.offset, n)
        }
    }
    return Array(reads.prefix(4))
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
    let collectorReads = parseCollectorInfo(bottomLines)
    read.collectorNumber = collectorReads.first?.number ?? ""
    read.setCode = collectorReads.first?.set ?? ""
    read.finishHint = collectorReads.first?.finish ?? ""
    read.collectorAlts = Array(collectorReads.dropFirst())

    var lines: [Line] = []
    for obs in request.results ?? [] {
        guard let cand = obs.topCandidates(1).first else { continue }
        let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { continue }
        lines.append(Line(text: t, box: obs.boundingBox, confidence: cand.confidence))
    }
    if lines.isEmpty {
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
    let primary = names.first ?? ranked.first!.text
    // Report several lines, best-guess first. The caller tries each against
    // Scryfall, so a card still resolves when the top-line guess is wrong —
    // which happens whenever the capture reaches Vision at an odd angle.
    var candidates = Array(names.prefix(8))
    if candidates.first != primary { candidates.insert(primary, at: 0) }

    read.name = primary
    read.candidates = candidates
    read.lines = plausible
    read.nameConfidence = plausible.first?.confidence ?? ranked.first!.confidence
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
    guard let first = words.first?.first, first.isLetter else { return false }
    let tokens = words.map { String($0.lowercased().filter { $0.isLetter }) }
    if tokens.contains(where: { typeLineWords.contains($0) }) { return false }
    // Rules text that opens a line with its trigger word capitalizes like a
    // title ("Whenever Black Panther…"). No card is named "Whenever …", so the
    // lead token alone is a safe rejection.
    if tokens.first == "whenever" { return false }
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
    return false
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
    let read = readCard(cg)
    let rects = cardRects(cg)
    let ciContext = CIContext()

    // Entries carry their anchor height so the final list reads top-to-bottom,
    // the order a person reads a fan — and the title line's full box, so a
    // crop can be matched to its card by geometry when its title read can't.
    var entries: [(top: CGFloat, box: CGRect?, entry: CardEntry)] = []

    for line in read.lines {
        if !titleLike(line.text) {
            multiDebug("line not title-like: \"\(line.text)\"")
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
        guard let crop = perspectiveCrop(cg, r, ciContext) else { continue }
        saveDebugImage(crop, "multi-rect-\(i).png")
        let cropRead = readCard(crop)
        if cropRead.name.isEmpty {
            continue // a quad that reads as nothing is desk, not card
        }
        var e = CardEntry(name: cropRead.name, candidates: Array(cropRead.candidates.prefix(8)),
                          confidence: cropRead.nameConfidence, source: "crop")
        // A bare number off a crop is a mana cost or power box as often as a
        // collector number; only a set-and-number pair is worth reporting.
        if !cropRead.setCode.isEmpty && !cropRead.collectorNumber.isEmpty {
            e.setCode = cropRead.setCode
            e.collectorNumber = cropRead.collectorNumber
        }
        // The crop's band is anchored to this card, so its alternates and
        // finish marker are per-card by construction.
        if !cropRead.collectorAlts.isEmpty {
            e.collectorAlts = cropRead.collectorAlts
        }
        e.finishHint = cropRead.finishHint
        // mergeInto folds the crop's card-anchored printing and foil marker
        // into an existing entry without touching its (better) name.
        func mergeInto(_ idx: Int, why: String) {
            if entries[idx].entry.collectorNumber.isEmpty && !e.collectorNumber.isEmpty {
                entries[idx].entry.setCode = e.setCode
                entries[idx].entry.collectorNumber = e.collectorNumber
            }
            if entries[idx].entry.collectorAlts == nil {
                entries[idx].entry.collectorAlts = e.collectorAlts
            }
            if entries[idx].entry.finishHint.isEmpty {
                entries[idx].entry.finishHint = e.finishHint
            }
            multiDebug("crop \(i) pins \"\(entries[idx].entry.name)\" \(e.setCode.isEmpty ? "-" : e.setCode)/\(e.collectorNumber.isEmpty ? "-" : e.collectorNumber) (\(why), crop read \"\(e.name)\")")
        }
        let frameIdxs = entries.indices.filter { entries[$0].entry.source == "frame" }
        if let idx = entries.firstIndex(where: { sameTitle($0.entry.name, e.name) }) {
            // The crop read the same title off straightened pixels — usually
            // the cleaner read — and may carry the printing.
            entries[idx].entry = e
            multiDebug("crop \(i) refines \"\(e.name)\" \(e.setCode.isEmpty ? "-" : e.setCode)/\(e.collectorNumber.isEmpty ? "-" : e.collectorNumber)")
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
    if !cards.isEmpty, !cards.contains(where: { !$0.collectorNumber.isEmpty }),
       !read.collectorNumber.isEmpty || !read.setCode.isEmpty {
        cards[0].collectorNumber = read.collectorNumber
        cards[0].setCode = read.setCode
        if cards[0].collectorAlts == nil, !read.collectorAlts.isEmpty {
            cards[0].collectorAlts = read.collectorAlts
        }
        if cards[0].finishHint.isEmpty {
            cards[0].finishHint = read.finishHint
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

/// uprighted bakes an EXIF orientation into the pixels, returning an image that
/// reads correctly with no orientation tag. Normalizing once here is what keeps
/// the tag and the manual rotation from both being applied.
func uprighted(_ cg: CGImage, _ orientation: CGImagePropertyOrientation) -> CGImage {
    if orientation == .up { return cg }
    let ci = CIImage(cgImage: cg).oriented(orientation)
    return CIContext().createCGImage(ci, from: ci.extent) ?? cg
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

/// How often the auto trigger samples the video stream. Vision's rectangle
/// detector on a ≤1080p buffer costs a few milliseconds, so 5 Hz is nearly free
/// and still reacts within a beat of a card being set down.
let autoInterval = envDouble("HOARD_SCAN_AUTO_INTERVAL", 0.2)
/// Consecutive still samples before firing (~0.6 s at the default interval). A
/// hand still moving jitters the detected bounds and never accumulates this
/// streak — which is also what keeps motion blur out of the captures.
let autoStableSamples = Int(envDouble("HOARD_SCAN_AUTO_STABLE", 3))
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
let autoGraceSamples = Int(envDouble("HOARD_SCAN_AUTO_GRACE", 3))
/// A rectangle overlapping a background rectangle at least this much is that
/// background rectangle, not a newly placed card.
let autoBackgroundIoU = envDouble("HOARD_SCAN_AUTO_BG_IOU", 0.5)

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
    /// Rectangles that are furniture, not cards: whatever was in frame when
    /// auto armed (a desk has notepads and coasters — rectangles all), plus
    /// anything that fired and then photographed as no-card. Only a rectangle
    /// not in this set can arm the trigger.
    private var background: [CGRect] = []
    private var needBaseline = true

    func setEnabled(_ on: Bool) {
        if on {
            guard phase == .off else { return }
            stableCount = 0
            disruptCount = 0
            prevSig = []
            heldSig = []
            background = []
            needBaseline = true
            move(to: .searching)
        } else {
            guard phase != .off else { return }
            move(to: .off)
        }
    }

    /// observe feeds one sampled frame's detected rectangles through the
    /// machine.
    func observe(_ boxes: [CGRect]) {
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
        switch phase {
        case .off, .capturing:
            return
        case .searching:
            if !novel.isEmpty {
                prevSig = novel
                stableCount = 1
                graceCount = 0
                move(to: .stabilizing)
            }
        case .stabilizing:
            // A bad sample — the detector missed the card, or its box jittered
            // past the IoU bar — is tolerated a few times with the streak
            // frozen: Vision flickers on foils and borderless frames. Only a
            // sustained miss (card gone) or sustained mismatch (hand still
            // moving) restarts anything.
            if novel.isEmpty {
                graceCount += 1
                if graceCount > autoGraceSamples {
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
                    move(to: .capturing)
                    onFire?()
                }
                return
            }
            if !matches(prevSig, novel) {
                graceCount += 1
                if graceCount > autoGraceSamples {
                    autoDebug("scene moved, streak reset (\(novel.count) candidate(s))")
                    stableCount = 1
                    graceCount = 0
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
                move(to: .capturing)
                onFire?()
                return
            }
            prevSig = novel
        case .hold:
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
                emit(Event(event: "ready", rotation: self.manualRotation,
                           device: self.deviceName,
                           features: self.autoAvailable ? ["auto", "rearm"] : nil))
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

    /// updateTitle surfaces the current rotation — including what the automatic
    /// angle contributed — so a wrong orientation is diagnosable at a glance.
    private func updateTitle() {
        let total = Int(autoPreviewAngle) + manualRotation
        let mode = autoTrigger.phase == .off ? "" : "AUTO · "
        window?.title = "hoard — \(deviceName) · \(mode)\(total % 360)° "
            + "(auto \(Int(autoPreviewAngle))°) · Space capture · A auto · ←/→ rotate · Esc cancel"
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
        outline.strokeColor = NSColor.systemYellow.cgColor
        outline.isHidden = true
        preview.addSublayer(outline)
        view.outlineLayer = outline
        view.onKey = { [weak self] key in
            switch key {
            case .space: self?.capture()
            case .escape: self?.shutdown()
            case .rotateLeft: self?.rotate(clockwise: false)
            case .rotateRight: self?.rotate(clockwise: true)
            case .autoToggle: self?.setAuto(self?.autoTrigger.phase == .off)
            }
        }
        win.contentView = view
        win.makeKeyAndOrderFront(nil)
        win.makeFirstResponder(view)
        NSApp.activate(ignoringOtherApps: true)
        self.window = win
    }

    private func capture() {
        // Re-level right before the shutter in case the phone moved since the
        // last KVO notification.
        applyRotation()
        // Any shutter — auto or manual — parks the trigger, so pressing space
        // in auto mode can't be followed by an auto fire on the same card.
        autoTrigger.captureBegan()
        photoOutput.capturePhoto(with: AVCapturePhotoSettings(), delegate: self)
    }

    /// autoFire is the trigger's shutter: identical to a space press except the
    /// resulting scan event is tagged auto. A nudge-armed fire is a quiet
    /// recheck of a scene the parent may already know — no shutter pop, so a
    /// slow moment between cards doesn't sound like the scanner acting up.
    private func autoFire() {
        pendingAuto = true
        if !autoTrigger.nudged {
            NSSound(named: "Pop")?.play()
        }
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

    /// updateOutlines traces the trigger's cue around the cards themselves —
    /// yellow while one settles, green once it's shot — rather than bordering
    /// the whole window. Drawing what the detector actually sees also makes a
    /// reluctant trigger diagnosable at a glance: no outline means no
    /// rectangle, a jumping outline means the scene never reads as still.
    ///
    /// Vision reports boxes normalized with a bottom-left origin in buffer
    /// space; flipping y gives metadata-output space, and the preview layer's
    /// own converter carries that into layer coordinates, aspect fit and
    /// preview rotation included.
    private func updateOutlines(_ boxes: [CGRect]) {
        lastBoxes = boxes
        guard let preview = previewLayer,
              let outline = (window?.contentView as? PreviewView)?.outlineLayer else { return }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        let phase = autoTrigger.phase
        if phase == .off || boxes.isEmpty {
            outline.isHidden = true
            return
        }
        let path = CGMutablePath()
        for b in boxes {
            let metadataRect = CGRect(x: b.minX, y: 1 - b.maxY, width: b.width, height: b.height)
            path.addRect(preview.layerRectConverted(fromMetadataOutputRect: metadataRect))
        }
        outline.path = path
        outline.strokeColor = outlineColor(phase)
        outline.lineWidth = phase == .capturing ? 5 : 3
        outline.isHidden = false
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
        emit(Event(event: "closed", rotation: manualRotation))
        session.stopRunning()
        exit(0)
    }

    /// handle runs one command from the parent. These mirror the in-window keys,
    /// so the user can drive the camera from the terminal without switching
    /// windows — which is the point of keeping the session open.
    func handle(command: String) {
        switch command {
        case "capture": capture()
        case "rotate-left": rotate(clockwise: false)
        case "rotate-right": rotate(clockwise: true)
        case "auto-on": setAuto(true)
        case "auto-off": setAuto(false)
        case "rearm": autoTrigger.forceRearm()
        case "quit": shutdown()
        default: emit(Event(event: "error", message: "unknown command: \(command)"))
        }
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
        let forOCR = rotatedImage(uprighted(cg, orientation), clockwiseDegrees: effectiveRotation)
        saveDebugImage(forOCR, "capture-\(captureCount)-ocr.png")
        let (read, cards) = scanFrame(forOCR)
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
        let sampledAt = Date()
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            // Samples taken before the last capture finished are stale: they
            // queued behind the OCR and describe the shutter moment. Feeding
            // them to HOLD faked instant disruption bursts.
            guard sampledAt > self.lastCaptureFinishedAt else { return }
            // The trigger decides which of these are candidates (vs desk
            // furniture) and drives the outline through onBoxes.
            self.autoTrigger.observe(boxes)
        }
    }
}

/// PreviewView hosts the camera preview layer and forwards key presses.
final class PreviewView: NSView {
    enum Key { case space, escape, rotateLeft, rotateRight, autoToggle }
    var previewLayer: AVCaptureVideoPreviewLayer?
    /// The auto-trigger's cue: an outline traced around each rectangle the
    /// trigger currently sees, kept sized to the view by layout().
    var outlineLayer: CAShapeLayer?
    var onKey: ((Key) -> Void)?
    override var acceptsFirstResponder: Bool { true }
    override func keyDown(with event: NSEvent) {
        switch event.keyCode {
        case 49: onKey?(.space)        // space
        case 53: onKey?(.escape)       // esc
        case 123: onKey?(.rotateLeft)  // left arrow
        case 124: onKey?(.rotateRight) // right arrow
        case 0: onKey?(.autoToggle)    // a
        default: super.keyDown(with: event)
        }
    }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        outlineLayer?.frame = bounds
        CATransaction.commit()
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

AVCaptureDevice.requestAccess(for: .video) { granted in
    DispatchQueue.main.async {
        if !granted { fail("camera access denied — grant it in System Settings › Privacy › Camera") }
        controller.start()
    }
}
app.run()
