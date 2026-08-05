// The frame-wide Vision read: two text passes, then the title selection that
// turns their lines into a name and its alternates.

import CoreGraphics
import Foundation
import Vision

/// readCard runs Vision text recognition on a CGImage and returns the best guess
/// at the card's title, a few alternate lines, and the collector info printed
/// along the bottom border.
///
/// The image must already be upright: callers bake any EXIF orientation into the
/// pixels (see uprighted) before applying their own rotation. Passing an
/// orientation here as well would apply two rotations, which lands the title at
/// the bottom and makes the ranking below pick rules text instead.
func readCard(_ cg: CGImage) -> CardRead {
    // Normalized, origin bottom-left. The frame is already upright and
    // rotation-normalized by this point, so the band is stable across orientations.
    // (A fixed bottom-fraction band for crops — skipping collectorBand's rect
    // detection — was tried and dropped: Vision's read is sensitive to the exact
    // band shape, and the ocr-mangle fixture's set code flipped MSH→MSC.)
    let band = collectorBand(cg)
    let frame = frameTextRequest()
    let bottom = bandTextRequest(roi: band.region)

    let handler = VNImageRequestHandler(cgImage: cg, options: [:])
    do {
        try handler.perform([frame, bottom])
    } catch {
        return CardRead()
    }

    var read = CardRead()
    read.bandAnchored = band.anchored

    // Bottom band first: it stands alone, so a title failure doesn't cost us the
    // collector number.
    let bottomLines = bottomBandText(bottom)
    read.bottomLines = bottomLines
    read.bandLines = bandLines(bottom, band: band.region)
    let collectorReads = parseCollectorInfo(bottomLines)
    read.collectorNumber = collectorReads.first?.number ?? ""
    read.setCode = collectorReads.first?.set ?? ""
    read.finishHint = collectorReads.first?.finish ?? ""
    read.collectorPair = collectorReads.first?.pair ?? false
    read.collectorAlts = Array(collectorReads.dropFirst())

    let lines = recognizedLines(frame)
    if lines.isEmpty {
        applyCopyrightCollector(&read, bottomLines)
        return read
    }

    let ranked = rankByPosition(lines)
    let plausible = ranked.filter { plausibleName($0.text) }
    let primary = selectTitle(among: plausible)

    read.name = primary
    read.candidates = candidateList(primary: primary, ranked: ranked, plausible: plausible)
    read.lines = plausible
    read.nameConfidence = plausible.first(where: { $0.text == primary })?.confidence
        ?? plausible.first?.confidence ?? ranked.first!.confidence
    // The copyright line reads better in the full-resolution frame pass than
    // in the band crop — the band's tiny italic serif came back as fragments
    // in every observed old-frame capture — so both sources feed it.
    applyCopyrightCollector(&read, bottomLines + lines.map { $0.text })
    return read
}

// MARK: - The two passes

/// The Vision algorithm generation both passes are pinned to.
///
/// Left unset, a request uses `defaultRevision`, which is whatever the OS build
/// ships — so scan/fixtures' goldens silently describe the machine that
/// generated them rather than this code. That is already the suspected cause of
/// live-vs-replay disagreements on one machine (see the OCR-moved note in
/// docs/scanner-tuning.md), and it becomes a much bigger problem the moment a
/// second platform reads the same pixels.
///
/// Honest about what this buys: a revision pins the algorithm generation, not
/// the per-OS-build model weights behind it. It narrows drift, it does not
/// eliminate it. `--probe` prints this alongside the OS's supported set so the
/// two can be compared before a golden is trusted anywhere.
///
/// Falls back to `currentRevision` if the pinned one is ever dropped from a
/// future OS — a hard-coded constant that stops being supported would throw at
/// perform() time, which is a worse failure than reading with a newer engine.
let textRecognitionRevision: Int = {
    let preferred = VNRecognizeTextRequestRevision3
    return VNRecognizeTextRequest.supportedRevisions.contains(preferred)
        ? preferred : VNRecognizeTextRequest.currentRevision
}()

/// The whole-frame pass, which reads the title and the rules text.
private func frameTextRequest() -> VNRecognizeTextRequest {
    let request = VNRecognizeTextRequest()
    request.revision = textRecognitionRevision
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    // Pinning the language skips Vision's language identification. Card titles
    // with diacritics (Juzám Djinn, Lim-Dûl) still read under en-US script
    // handling — verified by fixture replay when this landed.
    request.recognitionLanguages = ["en-US"]
    return request
}

/// A second pass over the card's bottom border only. Language correction is off
/// here and that is not incidental: with it on, Vision "corrects" "123/264" and
/// set codes like "MH3" into dictionary words, which is the quietest possible
/// way for this to stop working.
private func bandTextRequest(roi: CGRect) -> VNRecognizeTextRequest {
    let bottom = VNRecognizeTextRequest()
    bottom.revision = textRecognitionRevision
    bottom.recognitionLevel = .accurate
    bottom.usesLanguageCorrection = false
    bottom.recognitionLanguages = ["en-US"]
    bottom.regionOfInterest = roi
    // minimumTextHeight is deliberately left at Vision's default. Lowering it to
    // 0.005 and 0.01 changed nothing across all 26 fixtures — the band read is
    // not floor-limited, so there is nothing here to win. See the band-upscale
    // entry in docs/scanner-tuning.md.
    return bottom
}

// MARK: - Observations to lines

/// bottomBandText is the band's text, bottom-most first — which is what
/// parseCollectorInfo relies on to prefer the border block over anything
/// printed above it.
private func bottomBandText(_ request: VNRecognizeTextRequest) -> [String] {
    (request.results ?? [])
        .compactMap { obs -> (CGFloat, String)? in
            guard let cand = obs.topCandidates(1).first else { return nil }
            let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
            return t.isEmpty ? nil : (obs.boundingBox.minY, t)
        }
        .sorted { $0.0 < $1.0 }
        .map { $0.1 }
}

/// bandLines maps the band pass's boxes back into frame coordinates.
///
/// Vision reports a region-of-interest request's boxes normalized to the ROI,
/// not to the image, so they have to be mapped back before they can be compared
/// with anything from the whole-frame pass. The band always spans the full width
/// from y=0, so the mapping is a scale of y alone. Verified by measurement: the
/// copyright row lands at 0.9375 of card height mapped, and at 0.61 unmapped, on
/// frames where both passes read it.
private func bandLines(_ request: VNRecognizeTextRequest, band: CGRect) -> [Line] {
    (request.results ?? []).compactMap { obs -> Line? in
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
}

private func recognizedLines(_ request: VNRecognizeTextRequest) -> [Line] {
    (request.results ?? []).compactMap { obs -> Line? in
        guard let cand = obs.topCandidates(1).first else { return nil }
        let t = cand.string.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        return Line(text: t, box: obs.boundingBox, confidence: cand.confidence,
                    quad: Quad(topLeft: obs.topLeft, topRight: obs.topRight,
                               bottomLeft: obs.bottomLeft, bottomRight: obs.bottomRight))
    }
}

// MARK: - Choosing the name

/// The card name is the top-most reasonably-wide, confident line. Rank the
/// upper portion of the card by position, preferring wider/high-confidence text.
private func rankByPosition(_ lines: [Line]) -> [Line] {
    lines.sorted { a, b in
        // Primary: higher on the card first.
        if abs(a.top - b.top) > 0.04 { return a.top > b.top }
        // Tie-break: wider (titles span most of the card) then more confident.
        if abs(a.width - b.width) > 0.05 { return a.width > b.width }
        return a.confidence > b.confidence
    }
}

/// selectTitle picks the card's name out of the ranked, plausible lines.
///
/// Prefer a line that reads like a title. Taking the top plausible line
/// outright let an old frame's copyright tail become the card's name at
/// full confidence — "008 Wizards of the Coast, Iac. 15/145", which the
/// multi trace had already logged as not title-like (observed live).
/// Boilerplate can never be the name: a frame that read nothing but its
/// own border has no title, and "" queues the card as unidentified, which
/// is honest. The middle fallback is what keeps single-word titles
/// (Ponder) working — titleLike rejects those by design.
///
/// A title-shaped line at the card's *foot* is furniture, and preferring
/// title-shaped lines is what let it through. The 8th Edition frame draws a
/// paintbrush where every earlier frame writes "Illus.", so its credit
/// arrives as a bare "Pete Venters" — two Title Case words, which no string
/// rule can tell from a card name — while the real title, "Tremor", is a
/// single word and titleLike rejects those by design. The credit therefore
/// won outright, and a live 8th Edition pile resolved as its own artists.
///
/// Geometry settles what the string cannot, exactly as the flavour-credit
/// rule already does: a title sits at the top of whatever text was read, so
/// a candidate in the bottom of that span loses its title-shaped privilege
/// and the topmost readable line is preferred instead. Only the *privilege*
/// is withdrawn — such a line can still be the name when it is all there is,
/// which is what keeps a lone credit-shaped title working.
private func selectTitle(among plausible: [Line]) -> String {
    let names = plausible.map { $0.text }
    let tops = plausible.map { $0.top }
    let lowest = tops.min() ?? 0, highest = tops.max() ?? 1
    let footLine = lowest + (highest - lowest) * 0.35
    let upper = plausible.filter { $0.top >= footLine || plausible.count < 3 }
    let upperNames = upper.map { $0.text }
    return upperNames.first(where: { titleLike($0) })
        ?? upperNames.first(where: { !boilerplate($0) })
        ?? names.first(where: { titleLike($0) })
        ?? names.first(where: { !boilerplate($0) })
        ?? ""
}

/// candidateList reports several readings, best-guess first. The caller tries
/// each against Scryfall, so a card still resolves when the top-line guess is
/// wrong — which happens whenever the capture reaches Vision at an odd angle.
///
/// Order is not cosmetic: the caller stops after the first handful of lines, so
/// anything worth resolving has to be near the front. A card's rules text names
/// the card, and on an old frame whose title band was lost that is the only
/// place the name survives — so those recovered names sit directly behind the
/// primary, ahead of the raw prose lines they were mined from.
private func candidateList(primary: String, ranked: [Line], plausible: [Line]) -> [String] {
    var candidates: [String] = []
    var seen = Set<String>()
    func offer(_ s: String) {
        let key = normTitle(s)
        guard !key.isEmpty, !seen.contains(key) else { return }
        seen.insert(key)
        candidates.append(s)
    }
    offer(primary)
    for line in ranked {
        if let name = parseSelfReference(line.text) { offer(name) }
    }
    for n in plausible.map({ $0.text }) { offer(n) }
    return Array(candidates.prefix(8))
}

/// plausibleName filters obvious non-title noise: very short tokens, pure
/// numbers and symbols. Shared by the single-card ranking and the multi-card
/// clustering, so both mean the same thing by "could be a card name".
func plausibleName(_ s: String) -> Bool {
    let letters = s.filter { $0.isLetter }
    return letters.count >= 3
}
