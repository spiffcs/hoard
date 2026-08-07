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

@_exported import BorderKit
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
    /// What the foil sparkle reader saw at the text box's lower-left corner —
    /// nil when it could not be asked at all. See BorderKit's Sparkle.swift.
    ///
    /// On the reading rather than on `border` even though both answer questions
    /// about pixels: the border reconstructs the card from text anchors and this
    /// reads the flatten directly, so they share no geometry and coupling their
    /// reporting would imply otherwise.
    public var sparkle: SparkleVerdict? = nil
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
    public var border = 0.0
    /// The foil sparkle pass. Zero when the frame prints no marker, which is
    /// most captures — reported separately from `border` so a regression in one
    /// cannot hide inside the other.
    public var sparkle = 0.0
    public var total = 0.0
    public init() {}
    public var line: String {
        String(format: "locate=%.0f whole=%.0f band=%.0f border=%.0f sparkle=%.0f",
               locate, whole, band, border, sparkle)
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
    // The head crop when there is one, the whole card when there is not — and
    // which it was matters downstream, because the border reader needs these
    // lines in card space and that is the rect they came from.
    let headRect = cropCard(upright, CardGeometry.head) != nil
        ? CardGeometry.head : CGRect(x: 0, y: 0, width: 1, height: 1)
    async let whole = recognizeLines(
        cropCard(upright, CardGeometry.head) ?? upright, correctLanguage: true)

    // The band, as a fraction of the card. Language correction off: with it on,
    // Vision "corrects" 123/264 and set codes like MH3 into dictionary words,
    // which is the quietest possible way for this to stop working.
    var bandRead: [Line] = []
    let bandStart = DispatchTime.now()
    if let band = cropCard(upright, CardGeometry.band) {
        bandRead = await recognizeLines(band, correctLanguage: false)
    }
    out.timings.band = millis(since: bandStart)

    let headRead = await whole
    out.lines = headRead.map(\.text)
    out.timings.whole = millis(since: wholeStart)

    var bandLines: [String] = bandRead.map(\.text)

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

    // The border, last, because it wants the year the footer just gave up.
    //
    // Against `card.wide`, never `upright`: the quad cannot be trusted to
    // contain the border, and a reader handed pixels the border is not in has
    // no way to say so — it measured the inner frame and reported a confident
    // number about the wrong surface. The wide flatten guarantees the border is
    // in shot; anchoring on the footer text is what finds it there.
    if let wide = card.wide {
        let borderStart = DispatchTime.now()
        out.border = readBorder(
            wide,
            lines: headRead.map {
                intoWide($0, from: headRect, margin: card.wideMarginUsed)
            },
            bandLines: bandRead.map {
                intoWide($0, from: CardGeometry.band, margin: card.wideMarginUsed)
            },
            frame: FrameEvidence(year: printing.year ?? 0,
                                 hasSetCode: !printing.setCode.isEmpty,
                                 numberOnOwnRow: printing.numberSource == .ownRow))
        out.timings.border = millis(since: borderStart)
    } else {
        out.border.abstain = "no wide crop"
    }

    // The foil sparkle, promoted to a finish.
    //
    // Three conditions, and the order is the argument. The frame has to be one
    // that prints the marker at all, or a low score means nothing — a modern
    // frame has no sparkle to find and would be scored against an empty patch.
    // The card must not already have said "foil" in text, because when it did
    // there is nothing to add. And the score has to clear a bar set above every
    // nonfoil the corpus has ever produced.
    //
    // What this deliberately does *not* do is claim nonfoil. A score below the
    // bar is not evidence the card is not foil — it is the absence of evidence
    // that it is — and `Printing.finish` is documented as refusing exactly that
    // inference. A miss costs what today costs: the Go side's nonfoil default,
    // flagged as a guess.
    //
    // A "foil" here outranks a separator that said "nonfoil". That is a real
    // decision and not an oversight: the sparkle is a printed graphic hundreds
    // of pixels across and the separator is a three-pixel glyph Vision mangles
    // routinely. Live, a foil Charitable Levy committed nonfoil off a set row
    // whose own set code parsed as the token "CARD".
    // Asked whenever the card has not already said "foil" in text. That is the
    // whole gate, and it is deliberately not narrower.
    //
    // The obvious extra gate — only look at frames that print a marker, told by
    // an `Illus.` credit row — was built, measured, and removed. It reads off
    // OCR, and OCR is exactly what fails on these captures: Victimize, Consuming
    // Corruption and Charitable Levy are all foils whose credit row did not
    // come back, so the gate skipped three of eleven foils to save a millisecond
    // on cards that were going to score 0.35 anyway. A gate that fails on the
    // same inputs as the thing it guards is not protection.
    //
    // What makes running it everywhere safe is that a modern frame has no marker
    // to find and says so: the five in the corpus score 0.35-0.44 against a bar
    // of 0.50. `retroFrameFooter` survives as reported telemetry so that claim
    // stays checkable.
    //
    // The one hard gate is the year, and it is not a heuristic: foil cards did
    // not exist before Urza's Legacy in February 1999, so a card whose
    // copyright says 1994 is nonfoil no matter what its pixels correlate with.
    // That matters because it is exactly where the reader is weakest — the
    // template is fitted on 2001-2024 frames and the pre-1998 frame lays its
    // text box out differently, so the search runs to the edge of its window
    // and scores whatever it finds there. Four of scan/fixtures' pre-1998 cards
    // cleared the bar on noise before this gate existed.
    //
    // An unread year does *not* skip, which is a deliberate reversal of what
    // `CardLayout.leftU` does with the same silence.
    //
    // Treating "no year" as pre-1998 was tried and measured. It refuses
    // Victimize and Consuming Corruption — both foils in scan/foil-corpus whose
    // copyright row never reads — to buy refusing two fixtures whose years are
    // missing because a finger is across the footer. That is fixing real reads
    // to accommodate broken test captures, which is backwards: those fixtures
    // want recapturing, not a gate built around them.
    if printing.finish != "foil", (printing.year ?? 9999) >= SparkleGate.firstFoilYear {
        let sparkleStart = DispatchTime.now()
        out.sparkle = sparkleInCard(upright)
        out.timings.sparkle = millis(since: sparkleStart)
        if let s = out.sparkle, s.isFoil {
            printing.finish = "foil"
            // The channel rides along in the source, so a session's log says
            // which one is carrying the answer rather than only that a sparkle
            // did. The two were added at different times for different failures
            // and their rates are worth being able to tell apart later.
            // "sparkle-luma" today; the channel is spelled out because the
            // colour channel is measured beside it and may one day answer.
            printing.finishSource = "sparkle-" + s.channel
        }
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

/// intoWide moves a line's box from a card-space crop into the wide flatten.
///
/// Three coordinate systems meet here and two of them disagree about which way
/// is up, so this is written out rather than folded into an expression:
///
///   · Vision normalizes to the image it was given, origin bottom left.
///   · Card space (`CardGeometry.band` and friends) runs top down.
///   · The wide flatten holds the card inset by `wideMargin` on every side, so
///     card space occupies `[m, 1-m] / (1 + 2m)` of it.
///
/// Getting this wrong samples the opposite end of the card and still returns a
/// plausible number, which is the failure mode that costs a session before
/// anyone notices — so `BorderMappingTests` pins each step against a hand-built
/// box.
func intoWide(_ line: Line, from crop: CGRect, margin m: CGFloat) -> Line {
    let k = 1 + 2 * m
    // Vision's y counts up from the bottom of the crop; card space counts down
    // from the card's top, so the box's edges swap roles on the way across.
    let cardTop = crop.minY + (1 - line.box.maxY) * crop.height
    let cardBottom = crop.minY + (1 - line.box.minY) * crop.height
    let cardLeft = crop.minX + line.box.minX * crop.width
    let cardRight = crop.minX + line.box.maxX * crop.width

    let wideTop = (m + cardTop) / k
    let wideBottom = (m + cardBottom) / k
    let box = CGRect(x: (m + cardLeft) / k, y: 1 - wideBottom,
                     width: (cardRight - cardLeft) / k,
                     height: wideBottom - wideTop)

    // One point, through the same three systems. The corners matter as much as
    // the box: the reader takes the card's tilt and scale off the footer row's
    // own quad, and a Line without one has no geometry to offer.
    func point(_ p: CGPoint) -> CGPoint {
        let cardX = crop.minX + p.x * crop.width
        let cardY = crop.minY + (1 - p.y) * crop.height
        return CGPoint(x: (m + cardX) / k, y: 1 - (m + cardY) / k)
    }
    let quad = line.quad.map {
        Quad(topLeft: point($0.topLeft), topRight: point($0.topRight),
             bottomLeft: point($0.bottomLeft), bottomRight: point($0.bottomRight))
    }
    return Line(text: line.text, box: box, confidence: line.confidence, quad: quad)
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
    await recognizeLines(cg, correctLanguage: correctLanguage).map(\.text)
}

@available(macOS 15, iOS 18, *)
/// recognizeLines is the same call, keeping where each line sat.
///
/// The boxes were thrown away here, and that is what kept the phone on a border
/// reader that guesses from the crop's outer edge: the good one anchors its
/// geometry on lines whose identity is proven by their *content*, and needs to
/// know where those lines are. Vision hands the box over with the string, so
/// this costs nothing — it is the same request, read more completely.
func recognizeLines(_ cg: CGImage, correctLanguage: Bool) async -> [Line] {
    var request = RecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = correctLanguage
    request.recognitionLanguages = [Locale.Language(identifier: "en-US")]
    guard let observations = try? await request.perform(on: cg) else { return [] }
    return observations.compactMap { o -> Line? in
        guard let c = o.topCandidates(1).first else { return nil }
        let text = c.string.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        // A quad, squared off the box.
        //
        // The reader takes the card's scale and tilt from the footer row's own
        // corners, and iOS 18's RecognizedTextObservation offers only an
        // axis-aligned box. That costs nothing *here* and would elsewhere: this
        // pipeline reads a card that has already been perspective-corrected, so
        // the footer is horizontal by construction and a squared quad is the
        // true one. ScanKit reads the original frame, where the card can be
        // tilted, and takes real corners from the older Vision API.
        let b = o.boundingBox.cgRect
        return Line(text: text, box: b, confidence: c.confidence,
                    quad: Quad(topLeft: CGPoint(x: b.minX, y: b.maxY),
                               topRight: CGPoint(x: b.maxX, y: b.maxY),
                               bottomLeft: CGPoint(x: b.minX, y: b.minY),
                               bottomRight: CGPoint(x: b.maxX, y: b.minY)))
    }
}

/// measureAnchorOnFlatCard reads an image that *is* a card and reports where its
/// footer anchor sat, in card space.
///
/// Corpus tooling for fitting `CardLayout.leftU`. It deliberately skips
/// `locateCard` and the wide flatten: those exist to find a card inside a
/// photograph, and running them over an image that is already nothing but card
/// adds their error to a measurement whose whole purpose is to be exact.
@available(macOS 15, iOS 18, *)
public func measureAnchorOnFlatCard(_ image: CGImage) async -> AnchorFit? {
    // Both passes, same as a real read: the band is where the footer usually
    // reads, and the whole-card pass catches the rest.
    var lines = await recognizeLines(image, correctLanguage: false)
    var bandText: [String] = []
    if let band = cropCard(image, CardGeometry.band) {
        let read = await recognizeLines(band, correctLanguage: false)
        bandText = read.map(\.text)
        lines += read.map { intoWhole($0, from: CardGeometry.band) }
    }
    let printing = readPrinting(bandLines: bandText)
    guard let m = measureAnchor(lines, year: printing.year ?? 0) else { return nil }
    return AnchorFit(anchor: m, year: printing.year ?? 0,
                     setCode: printing.setCode,
                     numberSource: printing.numberSource.rawValue)
}

/// AnchorFit is one corpus card's anchor measurement plus the fields that are
/// candidates for telling its frame family apart. The year cannot do it alone —
/// 8th Edition shipped in July 2003 and Legions and Scourge are 2003 printings
/// of the frame it replaced — so the fit needs the alternatives beside it.
public struct AnchorFit: Sendable {
    public let anchor: AnchorMeasurement
    public let year: Int
    public let setCode: String
    public let numberSource: String
}

/// intoWhole moves a line's box from a card-space crop back onto the whole card.
/// The crop's own normalisation is undone, so both passes report in one space.
func intoWhole(_ line: Line, from crop: CGRect) -> Line {
    // Vision's y counts up from the bottom of the crop; card space counts down
    // from the card's top, so the box's edges swap roles on the way across.
    let top = crop.minY + (1 - line.box.maxY) * crop.height
    let bottom = crop.minY + (1 - line.box.minY) * crop.height
    let box = CGRect(x: crop.minX + line.box.minX * crop.width,
                     y: 1 - bottom,
                     width: line.box.width * crop.width,
                     height: bottom - top)
    return Line(text: line.text, box: box, confidence: line.confidence, quad: nil)
}
