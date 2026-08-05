// One card, read.
//
// The shape of this differs from the macOS pipeline deliberately. That one leads
// with a frame-wide text pass and treats crops as refinement, because its
// rectangle detector "returns quads that span several cards" and could not be
// trusted to say where a card was. Document segmentation can, so this leads with
// the card: find it, correct its perspective, and read a known geometry.
//
// The practical difference is that every measurement downstream — where the band
// is, where the expansion symbol is — becomes a fraction of a card rather than a
// fraction of a photograph. Stage C established why that matters: a band taken
// as the bottom 18% of the *frame* read nothing at all while the rules text read
// perfectly, because the card only occupied the middle 40% and the crop was a
// picture of the desk.

import CoreGraphics
import CoreImage
import Foundation
import Vision

/// What one card's pixels said.
public struct CardReading: Sendable {
    public var title = ""
    /// Every line the whole-card pass read, in reading order — the caller does
    /// the fuzzy matching against a catalog, so alternates matter.
    public var lines: [String] = []
    public var printing = Printing()
    /// Where the card was found, normalized to the frame.
    public var cardBox: CGRect?
    /// Whether the card was located at all. A read from an unlocated card is a
    /// read of whatever happened to be in the frame, and the caller must be able
    /// to tell the difference.
    public var located = false
    public var bandLines: [String] = []
    /// What colour border the card is printed with, when it could be read.
    public var border = BorderReading()
    /// Whether the printing came from the fallback strip below the located
    /// card rather than from the band inside it.
    ///
    /// Reported so a session can tell how often the crop is stopping short. A
    /// high rate means the segmentation is clipping cards, which is a problem
    /// worth fixing at the source rather than papering over here forever.
    public var footerRecovered = false
    /// Where the milliseconds went. Carried on the reading rather than logged,
    /// so the phone can put a breakdown on its trace line and the corpus probe
    /// can bench the same numbers offline — one instrument, two places.
    public var timings = ReadTimings()

    public init() {}
}

/// Per-stage cost of one read, in milliseconds.
public struct ReadTimings: Sendable {
    public var locate = 0.0
    public var whole = 0.0
    public var band = 0.0
    public var total = 0.0
    public init() {}
    public var line: String {
        String(format: "locate=%.0f whole=%.0f band=%.0f", locate, whole, band)
    }
}

@inline(__always) func millis(since t: DispatchTime) -> Double {
    Double(DispatchTime.now().uptimeNanoseconds - t.uptimeNanoseconds) / 1e6
}

/// readCard finds the card in a frame and reads it.
///
/// The image must already be upright — orientation baked into the pixels by the
/// caller. Applying an orientation here as well is how the title ends up at the
/// bottom and the ranking picks rules text.
///
/// Availability is annotated rather than declared in Package.swift, because the
/// package's floor is ScanKit's and ScanKit deliberately supports macOS 14 —
/// its Info.plist pins that for `.continuityCamera` and `RotationCoordinator`.
/// CardKit is the iPhone pipeline; it only builds for macOS so the corpus
/// harness can score it, and raising the whole package to buy that would drop
/// support the shipping helper still offers.
@available(macOS 15, iOS 18, *)
public func readCard(_ image: CGImage) async -> CardReading {
    var out = CardReading()
    let began = DispatchTime.now()
    let locateStart = DispatchTime.now()
    let found = locateCard(image)
    out.timings.locate = millis(since: locateStart)

    guard let card = found else {
        // No card located. Read the frame anyway and say so — a caller with no
        // better option can still fuzzy-match a title, but nothing positional
        // (band, symbol) means anything, so none of it is attempted.
        let t = DispatchTime.now()
        out.lines = await recognizeText(image, correctLanguage: true)
        out.timings.whole = millis(since: t)
        out.title = chooseTitle(from: out.lines)
        out.timings.total = millis(since: began)
        return out
    }

    out.located = true
    out.cardBox = normalize(card.bounds, in: image)

    let upright = card.image
    let wholeStart = DispatchTime.now()
    // The title pass reads the head of the card, then the whole card only if
    // that failed.
    //
    // Only `lines.prefix(3)` ever crosses the wire and only `chooseTitle` reads
    // the rest, so the rules box, the flavour text and the artist credit were
    // being recognised at accurate-mode cost and then dropped. Cropping them
    // out halved the read: 185ms to 91ms median on the corpus.
    //
    // These two passes do not overlap despite the `async let`, and that is
    // measured rather than assumed: on a live session the stage times summed to
    // the read (322ms of stages against a 348ms read) where overlapping would
    // have given 180ms. Vision queues them. Batching both onto one
    // `ImageRequestHandler.performAll` with a `regionOfInterest` each was tried
    // to recover that ~168ms and reverted — it took the corpus's name accuracy
    // from 82% to 5% while numbers held at 74%, which is the signature of both
    // results arriving in one bucket. Neither the correction flag nor the
    // region survived the round trip as a usable tag. The idea is sound and the
    // API exists; identifying which request produced which observations is the
    // unsolved part.
    async let whole = recognizeText(
        cropCard(upright, CardGeometry.head) ?? upright, correctLanguage: true)

    // The band, as a fraction of the card. Language correction off: with it on,
    // Vision "corrects" 123/264 and set codes like MH3 into dictionary words,
    // which is the quietest possible way for this to stop working.
    var bandLines: [String] = []
    let bandStart = DispatchTime.now()
    if let band = cropCard(upright, CardGeometry.band) {
        bandLines = await recognizeText(band, correctLanguage: false)
    }
    out.timings.band = millis(since: bandStart)

    out.lines = await whole
    out.timings.whole = millis(since: wholeStart)

    out.border = readBorder(upright)

    // If the band found no printing at all, look below the crop.
    //
    // Measured over one live session: 25 of 137 named captures came back with
    // no set code, no collector number and no year, and their bands were rules
    // text and a power/toughness box — `["next turn, you may play that card.",
    // "3/4"]`. That is not a card without a footer, it is a crop that stopped
    // short of one. Wurmcoil Larva was among them: a foil, committed nonfoil,
    // its star never in the image the parser saw.
    //
    // The aspect check cannot catch this. A quad missing the bottom sixth of a
    // card is still card-shaped — Wurmcoil's flatten measured 0.674 against a
    // card's 0.716, well inside the tolerance that exists to reject bad crops.
    //
    // So the fallback goes back to the original frame, below the located box,
    // where the missing rows physically are. It costs a second text pass only
    // on captures that already failed, and when the quad was right it reads
    // desk and finds nothing — no worse than the nothing already in hand.
    var printing = readPrinting(bandLines: bandLines)
    if printing.isEmpty, let recovered = await recoverFooter(image, box: card.bounds) {
        bandLines += recovered.lines
        printing = recovered.printing
        out.footerRecovered = true
    }

    out.bandLines = bandLines
    out.printing = printing
    out.title = chooseTitle(from: out.lines)
    out.timings.total = millis(since: began)
    return out
}

/// recoverFooter reads the strip just below a located card.
///
/// Only called when the band inside the card yielded no printing at all, which
/// on a card that prints one means the crop ended above it.
///
/// The strip starts a little *inside* the box rather than at its edge: the
/// clipped rows are immediately below wherever the quad stopped, and starting
/// flush would miss any that straddle the boundary. It runs to a third of a
/// card below, which is more than any observed clip and still far short of the
/// next card on the pile.
@available(macOS 15, iOS 18, *)
func recoverFooter(
    _ frame: CGImage, box: CGRect
) async -> (lines: [String], printing: Printing)? {
    let strip = CGRect(
        x: box.minX,
        y: box.maxY - box.height * FooterRecovery.overlap,
        width: box.width,
        height: box.height * (FooterRecovery.overlap + FooterRecovery.reach)
    ).integral.intersection(
        CGRect(x: 0, y: 0, width: frame.width, height: frame.height))

    guard strip.width >= 8, strip.height >= 8,
          let crop = frame.cropping(to: strip)
    else { return nil }

    // Language correction off, exactly as the band pass has it: with it on,
    // Vision rewrites 123/264 and set codes like MH3 into dictionary words.
    let lines = await recognizeText(crop, correctLanguage: false)
    let printing = readPrinting(bandLines: lines)
    // Nothing found means the quad was probably right and this read desk. Say
    // so by returning nil rather than handing back empty lines, so the caller
    // does not record a recovery that recovered nothing.
    return printing.isEmpty ? nil : (lines, printing)
}

/// How far the recovery strip reaches, as fractions of the card's height.
enum FooterRecovery {
    /// How far back *inside* the box to start. The clipped rows sit
    /// immediately below wherever the quad stopped, and a strip starting flush
    /// with the edge would cut any that straddle it.
    static let overlap = 0.06
    /// How far below the box to read. Generous against every clip observed —
    /// the worst lost roughly a sixth of the card — and still well short of
    /// whatever is lying on the desk beyond it.
    static let reach = 0.33
}

/// Where things are on a card, in card space: x and y as fractions of the
/// card's own width and height, origin top left.
public enum CardGeometry {
    /// The bottom strip carrying the collector row, the set row and the
    /// copyright. Measured generous rather than tight — a card is 63x88mm and
    /// the printed matter sits in roughly the last sixth, but frames vary and a
    /// crop that clips the copyright row costs more than one carrying a line of
    /// rules text.
    public static let band = CGRect(x: 0, y: 0.82, width: 1, height: 0.18)

    /// The top of the card, where the title is.
    ///
    /// Generous: the title bar sits at roughly y 0.03-0.12 and this takes
    /// nearly three times that, stopping well above the type line at y 0.54 —
    /// the most common wrong answer `chooseTitle` has to filter out. Measured
    /// as insensitive between 0.30 and 0.60, so the slack is free.
    public static let head = CGRect(x: 0, y: 0, width: 1, height: 0.30)

    /// The expansion symbol's neighbourhood, at the right end of the type line.
    /// `docs/scanner-symbol-plan.md` measures the symbol's centre at (0.877,
    /// 0.578) on the old frame and (0.867, 0.590) on the 8th Edition frame; this
    /// window spans both with room to spare, because the constants have not been
    /// re-measured at this resolution and a patch that misses is worth nothing.
    public static let symbol = CGRect(x: 0.78, y: 0.53, width: 0.19, height: 0.11)
}

/// cropCard cuts a card-space rect out of an already-perspective-corrected card.
public func cropCard(_ card: CGImage, _ rect: CGRect) -> CGImage? {
    let px = CGRect(
        x: rect.minX * CGFloat(card.width), y: rect.minY * CGFloat(card.height),
        width: rect.width * CGFloat(card.width), height: rect.height * CGFloat(card.height)
    ).integral.intersection(CGRect(x: 0, y: 0, width: card.width, height: card.height))
    guard px.width >= 8, px.height >= 8 else { return nil }
    return card.cropping(to: px)
}

private func normalize(_ r: CGRect, in image: CGImage) -> CGRect {
    CGRect(x: r.minX / CGFloat(image.width), y: r.minY / CGFloat(image.height),
           width: r.width / CGFloat(image.width), height: r.height / CGFloat(image.height))
}

// MARK: - Text

/// recognizeText is the one Vision text call, on the modern Swift API.
///
/// The revision is pinned for the same reason the macOS path pins it: left
/// unset, a request uses whatever the OS build ships, so a golden describes the
/// machine that made it rather than this code.
@available(macOS 15, iOS 18, *)
func recognizeText(_ cg: CGImage, correctLanguage: Bool) async -> [String] {
    var request = RecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = correctLanguage
    request.recognitionLanguages = [Locale.Language(identifier: "en-US")]
    guard let observations = try? await request.perform(on: cg) else { return [] }
    return observations.compactMap {
        let s = $0.topCandidates(1).first?.string
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return (s?.isEmpty ?? true) ? nil : s
    }
}
