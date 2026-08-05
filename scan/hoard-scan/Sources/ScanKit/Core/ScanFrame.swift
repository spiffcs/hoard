import CoreGraphics
import Foundation
import Vision

// MARK: - Multi-card detection

// Grown from a spike over five captured fixtures (tight fan, booster-sized
// cascade, steady fan, loose spread, desk clutter), which established:
// rectangle outlines only survive for unoccluded cards (2 of 9 in the
// cascade), while the whole-frame text pass reads every visible title band —
// so title lines are the primary channel and crops are the refinement,
// contributing per-card candidates and the only readable collector info.
// Set $HOARD_SCAN_MULTI for stderr tracing of the decisions.

/// FrameScan is what one capture yielded: the frame-wide single-card read the
/// event's flat fields have always carried, plus the per-card list a fanned
/// spread needs.
struct FrameScan {
    var read: CardRead
    var cards: [CardEntry]
}

/// A card the capture is accumulating, carrying the two things the merge needs
/// beyond the entry itself: its anchor height, so the final list reads
/// top-to-bottom the way a person reads a fan, and the title line's full box,
/// so a crop can be matched to its card by geometry when its title read can't.
private struct Candidate {
    var top: CGFloat
    var box: CGRect?
    var entry: CardEntry
}

/// scanFrame is the whole capture read.
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
func scanFrame(_ cg: CGImage) -> FrameScan {
    let t0 = Date()
    let read = readCard(cg)
    let frameMs = msSince(t0)
    let tRects = Date()
    let rects = cardRects(cg)
    let rectsMs = msSince(tRects)
    let tCrops = Date()

    var entries = titleBandCandidates(in: read)
    for (i, r) in rects.enumerated() {
        placeCrop(cg, r, index: i, into: &entries)
    }

    timing("scanFrame frameOCR=\(frameMs)ms rects=\(rectsMs)ms "
        + "crops=\(rects.count) cropOCR=\(msSince(tCrops))ms total=\(msSince(t0))ms")

    entries.sort { $0.top > $1.top }
    var cards = entries.map { $0.entry }
    attachFrameWideEvidence(to: &cards, from: read)
    attachBorder(to: &cards, in: cg, read: read)
    return FrameScan(read: read, cards: cards)
}

/// titleBandCandidates picks the card titles out of the whole-frame text pass.
/// This is the channel that survives fanning, where outlines do not.
private func titleBandCandidates(in read: CardRead) -> [Candidate] {
    var entries: [Candidate] = []
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
        entries.append(Candidate(
            top: line.top, box: line.box,
            entry: CardEntry(name: line.text, candidates: [line.text],
                             confidence: line.confidence, source: "frame")))
    }
    return entries
}

/// cropEntry turns one straightened card image's read into an entry, keeping
/// only the printing evidence a crop is actually entitled to report.
private func cropEntry(from cropRead: CardRead) -> CardEntry {
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
    return e
}

/// placeCrop decides which card one detected rectangle belongs to: an existing
/// entry it refines, an existing entry it only donates its printing to, or a
/// card the frame pass missed entirely.
private func placeCrop(_ cg: CGImage, _ r: VNRectangleObservation, index i: Int,
                       into entries: inout [Candidate]) {
    guard let crop = perspectiveCrop(cg, r, sharedCIContext) else { return }
    saveDebugImage(crop, "multi-rect-\(i).png")
    let cropRead = readCard(crop)
    if cropRead.name.isEmpty {
        return // a quad that reads as nothing is desk, not card
    }
    let e = cropEntry(from: cropRead)

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
        entries.append(Candidate(top: r.boundingBox.maxY, box: nil, entry: e))
        multiDebug("crop \(i) adds \"\(e.name)\"")
    } else {
        multiDebug("crop \(i) discarded — junk title \"\(e.name)\" beside \(frameIdxs.count) real titles")
    }
}

/// attachFrameWideEvidence hands the whole-frame read's printing to the
/// top-most card when no card carried its own.
///
/// The top-most entry is the title position, which in a single-card scene is
/// the card the border belongs to, while surviving phantom entries (misread
/// border lines, rules fragments) sit below it. This is what lets a borderless
/// or hard-to-outline card still arrive with its printing pinned. A wrong
/// attachment in a fan is commit-safe by construction: collector numbers are
/// per-card within a set, so a neighbour's number cannot verify against the
/// wrong card's printings — it queues, exactly as an unattached read would
/// have.
private func attachFrameWideEvidence(to cards: inout [CardEntry], from read: CardRead) {
    guard !cards.isEmpty else { return }
    if !cards.contains(where: { !$0.collectorNumber.isEmpty }) {
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
    if cards[0].copyrightYear == nil, read.copyrightYear > 0 {
        cards[0].copyrightYear = read.copyrightYear
    }
}

/// attachBorder reads the border colour, and only when the frame holds exactly
/// one card.
///
/// The reading is anchored on a single footer line, so in a fan there is no way
/// to say which card it describes — and unlike a stray collector number, which
/// cannot verify against the wrong card's printings and so queues harmlessly, a
/// border attached to the wrong card would agree with *something* and pick a
/// printing. That asymmetry is why this one refuses to guess rather than
/// leaning on downstream verification.
private func attachBorder(to cards: inout [CardEntry], in cg: CGImage, read: CardRead) {
    guard cards.count == 1 else { return }
    let border = readBorder(cg, read)
    borderDebug(border.color.map { "\($0) via \(border.source ?? "?")" }
        ?? "abstained: \(border.abstain)")
    if let color = border.color {
        cards[0].borderColor = color
        cards[0].borderSource = border.source
    }
}

extension Event {
    /// scan builds the capture event, which the live session and `--image`
    /// both emit. It is one function because the two used to spell it out with
    /// twelve identical arguments each — and a field added to one of them was
    /// a field silently missing from the other, on a wire an older hoard
    /// binary still has to parse.
    static func scan(_ s: FrameScan, rotation: Int, auto: Bool? = nil) -> Event {
        Event(event: "scan", name: s.read.name, candidates: s.read.candidates,
              rotation: rotation,
              collectorNumber: s.read.collectorNumber, setCode: s.read.setCode,
              bottomLines: s.read.bottomLines, cards: s.cards,
              confidence: s.read.nameConfidence, bandAnchored: s.read.bandAnchored,
              auto: auto,
              collectorAlts: s.read.collectorAlts.isEmpty ? nil : s.read.collectorAlts,
              finishHint: s.read.finishHint.isEmpty ? nil : s.read.finishHint)
    }

    /// readAnything reports whether a capture yielded a card at all, which is
    /// the --image mode's exit status and the focus lock's feedback.
    static func readAnything(_ s: FrameScan) -> Bool {
        !s.read.name.isEmpty || !s.cards.isEmpty
    }
}
