// Reading the printed foil sparkle: "foil, or not foil" on a frame that says
// nothing about it in text.
//
// The scanner's only finish evidence is the star the modern frame prints as its
// set/language separator, which `finishFromSeparator` classifies. Retro frames
// print no set row at all, so old foils reach the Go side with an empty finish
// and take the nonfoil default — silently, and foil is worth a multiple.
//
// Those frames do carry a marker: an eight-point starburst with a comet trail,
// printed at the text box's lower-left corner. It is ink at a fixed spot on the
// card, which is the whole reason it is usable — unlike the diffraction sheen,
// it does not move with the lamp.
//
// What was measured before this was written, on 45 retro captures from two live
// sessions on the same rig (scan/foil-corpus):
//
//   · Whole-card holographic chroma does not separate foil from nonfoil at all.
//     Hue spread over the text box, the art and the border overlaps completely
//     between the two classes — the desk lamp's cast and the card's own ink
//     dominate any regional colour measure. This is the approach to not retry.
//   · Normalised cross-correlation against a template of the marker does
//     separate, and by a wide margin. Fitted on one session and scored on the
//     other, retro foils run 0.36-0.89 against retro nonfoils' -0.41 to 0.47.
//
// Two things about the shape of this file are load-bearing rather than stylistic:
//
//  1. The search window is fitted, not slack. Widening it from ±0.025u/±0.022v
//     to ±0.037/±0.042 recovers one foil and admits two false positives — the
//     highest nonfoil jumps 0.470 to 0.676. More room to search is more chances
//     to match noise. Recall gets fixed by centring `sparkleU`/`sparkleV`
//     better, never by searching wider.
//  2. The verdict is scored at full resolution even though the search is not.
//     A variant that took the verdict from the cheap decimated pass ran 31×
//     faster and manufactured a false positive (Frenetic Raptor, a nonfoil,
//     0.470 to 0.503 — over the line). Decimated scores run high. Locate
//     cheaply; judge once, properly.

import CoreGraphics
import Foundation

/// SparkleReading is what the marker's neighbourhood held. Reported whether or
/// not a verdict follows, the same way `BorderReading` reports its ring
/// statistics — the constants here are fitted from these numbers, and a card
/// that abstained must not look identical to one never asked.
public struct SparkleReading: Sendable {
    /// Normalised cross-correlation with the template, at full resolution, at
    /// the located position. The decision.
    public let score: CGFloat
    /// Where the match landed, in card space, relative to the nominal centre.
    /// Reported so drift in `sparkleU`/`sparkleV` is measurable rather than
    /// inferred — a corpus whose offsets all lean one way is a constant that
    /// wants re-fitting.
    public let offsetU: CGFloat
    public let offsetV: CGFloat
    /// Spread of the patch the verdict was taken from. A patch with no
    /// structure — blown out, or crushed to black — correlates with whatever
    /// noise the template happens to hold, so it is refused rather than scored.
    public let contrast: CGFloat
    /// How many pixels were read. Carried so the cost gate is observable in
    /// live telemetry and not only in an offline bench: a previous attempt at
    /// this feature put auto-commit times up by more than tenfold, and the
    /// thing it lacked was a number that said so before a session did.
    public let samples: Int
}

/// A luma sampler in card space: u across the width, v down the height, both
/// 0…1 from the card's top-left. nil where the card is not.
///
/// Everything the sparkle reader does is expressed against this rather than
/// against pixels, and that is the point. The live path backs it with a
/// `PixelReader` and a reconstructed `CardGeometry`; the corpus harness backs
/// it with a flat crop whose card-space rect is known exactly. One
/// implementation of the search and the scoring, driven from both — the
/// alternative is a fitting harness that slowly stops describing the reader it
/// fitted.
public typealias CardSampler = (CGFloat, CGFloat) -> CGFloat?

/// The marker's neighbourhood, in template cells rather than pixels. Sampled
/// through `CardGeometry.point`, so this is a fixed region of the *card*
/// whatever size the card is in frame.
public enum SparkleTemplate {
    /// Template dimensions. 52x34 cells spanning `spanU` x `spanV` of the card.
    public static let cols = 52
    public static let rows = 34
    /// The patch's extent in card space: one cell per pixel of a 630x880 card,
    /// which is the scale every constant here was fitted at. It holds the star
    /// and the head of its comet trail — the trail being the half of the marker
    /// that no other furniture on the card resembles.
    ///
    /// Span and cell count are tied together on purpose. Widening the span
    /// without adding cells samples a bigger region more coarsely, which is a
    /// different template wearing the same name: at 0.150 x 0.075 the corpus's
    /// held-out foils dropped from a 0.611 median to scores that could not be
    /// told from the nonfoils.
    public static let spanU: CGFloat = CGFloat(cols) / 630
    public static let spanV: CGFloat = CGFloat(rows) / 880
}

/// retroFrameFooter reports whether these lines came off a frame that prints a
/// sparkle when the card is foil.
///
/// The tell is the illustrator abbreviation. Retro frames credit the artist as
/// "Illus. Scott M. Fischer" on its own row; the M15 frame prints the name bare
/// on the set/language row — "MH3 • EN OLENA RICHARDS" — and never matches. So
/// this separates exactly the two populations that matter, and it costs a
/// string comparison against lines that have already been read.
///
/// It exists to keep the reader off frames whose answer is already known and
/// better: a modern frame prints its finish in text, and `finishFromSeparator`
/// reading it is worth more than any correlation score.
public func retroFrameFooter(_ lines: [String]) -> Bool {
    lines.contains { illusToken($0) || artistCredit($0) }
}

/// sparkle looks for the marker and reports how well it found it.
///
/// Returns nil — abstaining — when the card offers no horizontal landmark, or
/// when the patch carries no structure to judge. Absence of a *score* is not
/// evidence of absence of a *sparkle*, and neither is a low one: silence stays
/// silence, which is the same rule `Printing.finish` already documents.
/// The card *is* the image — the perspective-corrected flatten, where card
/// space and image space are the same thing.
///
/// This deliberately does not go through `CardGeometry`, and that is the
/// opposite of what the border reader does. The reasoning is not
/// inconsistency, it is where on the card each of them looks:
///
///   · The border ring sits at the very edge, which the flatten's quad cannot
///     be trusted to contain — Vision locks onto the cut edge sometimes and the
///     printed frame inside it other times. So the border reconstructs the card
///     from text whose identity is proven by content, and pays for that with a
///     dependence on `CardLayout.leftU`.
///   · The sparkle sits at u≈0.2, v≈0.89 — well inside the card, where a
///     percent or two of flatten error moves it less than the search window
///     absorbs.
///
/// Going through the geometry was tried first and does not work, for a reason
/// worth recording: `leftU` returns the 8th Edition frame's landmark for any
/// card whose copyright year is 2003 or later, but Legions and Scourge are
/// *2003 printings of the old frame*. Their copyright row starts near u=0.23
/// and the table says 0.080, so every card-space position derived from it lands
/// about 0.115 of a card-width to the right. Measured on live captures, the
/// marker was found at du -0.065 to -0.120 with the search window pinned at its
/// boundary. Widening the window to reach it does not rescue this — at
/// ±0.12u the nonfoils reach 0.70 and outscore the foils.
///
/// That is a real bug in `leftU`'s era detection and it is worth fixing on its
/// own terms, for the expansion-symbol reader that will need it. It is not this
/// feature's to fix, and this feature does not need it.
public func sparkleInCard(_ card: CGImage,
                          window: SparkleWindow = .fitted,
                          anchorShiftU: CGFloat = 0,
                          anchorShiftV: CGFloat = 0) -> SparkleVerdict? {
    guard let px = PixelReader(card) else { return nil }
    let w = CGFloat(card.width), h = CGFloat(card.height)
    // The anchor shifts displace the whole search in card space. U is
    // measurement-only (control patches). V is also the production correction
    // for a quad that cut the card short: the copyright row's own vertical
    // centre predicts the marker's V at half the quad's spread — measured
    // SD 0.0049 vs 0.0095 over 20 strong-peak foils, 2026-08-07 — so the
    // caller may re-centre V on it and leave U alone, where the same
    // measurement showed text anchoring is three times worse than the quad.
    return SparkleVerdict(
        luma: sparkleScan({ u, v in px.luma((u + anchorShiftU) * w, (v + anchorShiftV) * h) },
                          window: window),
        chroma: sparkleScan({ u, v in px.warmCool((u + anchorShiftU) * w, (v + anchorShiftV) * h) },
                            window: window, template: sparkleChromaTemplate))
}

/// SparkleVerdict is the marker read on both channels, and which one answered.
///
/// **Why there are two.** The reader correlated luma alone for its first three
/// live sessions, and on a pile that was entirely foil it read foil on 37% of
/// captures. The repeats are what showed why: one physical Glowrider, 24
/// captures in one session, scored -0.231 to 0.820. A marker that swings a full
/// point across one card on one desk is not being missed, it is being *lit*
/// differently — the sparkle is diffractive, and how much brightness it throws
/// back is a fact about the angle rather than about the card.
///
/// Its *colour* is far steadier. Measured inside the exact patch the reader
/// judges, on captures where the luma reader scored zero:
///
///     Glowrider     luma MAD 0.0067   warm-cool MAD 0.0863    13x
///     Trap Digger   luma MAD 0.0056   warm-cool MAD 0.0275     4.9x
///
/// The marker is there in both; on those frames it is simply written in colour
/// and not in brightness — a cool silver starburst on a warm tan box, near
/// identical in luminance. A 2024 retro reprint throws the marker back gold,
/// which luma sees perfectly well, which is why the luma-only reader worked at
/// all and why both channels are kept rather than one swapped for the other.
///
/// **This is not the chroma idea that was already refuted.** That one measured
/// hue spread over whole regions — the text box, the art, the border — and
/// found foil and nonfoil completely overlapped, because a regional colour
/// statistic is dominated by the lamp's cast and the card's own ink. This
/// correlates the *marker's shape* on a colour axis. Same template, same search,
/// same normalisation; only the channel differs. A regional average and a
/// template match are not the same measurement and do not share a result.
public struct SparkleVerdict: Sendable {
    /// The luma read. Nil when the patch ran off the card.
    public let luma: SparkleReading?
    /// The warm-cool read, over the same window with the same template.
    public let chroma: SparkleReading?

    /// Whether the marker was found: the luma correlation, or the warm-cool
    /// patch carrying real colour spread. **The chroma *score* still does not
    /// vote** — the history below is about that score, and it stands.
    ///
    /// It was built to vote, and on `scan/foil-corpus` it looked like the answer:
    /// with its own fitted template and a bar at 0.68, either-channel took 27 of
    /// 27 retro foils against luma's 24, at 0 of 18 retro nonfoils and 0 of 5
    /// modern frames. Held out — template fitted on session 1, scored on session
    /// 2 — 13 of 13 against luma's 11. It rescued Charitable Levy, the one foil
    /// that has never cleared the luma bar in any session or corpus.
    ///
    /// Then it was scored on `scan/fixtures`, which is a different rig, and the
    /// ordering inverts:
    ///
    ///     Eternal Dragon    confirmed nonfoil   chroma 0.796
    ///     Cephalid Looter   confirmed nonfoil   chroma 0.782
    ///     Meltdown          confirmed foil      chroma 0.633
    ///     ocr-mangle        confirmed foil      chroma 0.000
    ///
    /// The two highest-scoring cards in that set are both nonfoils, and they
    /// outscore every foil in it. No threshold fixes that — it is not a bar in
    /// the wrong place, it is the channel ranking the classes backwards on
    /// photographs it was not fitted on.
    ///
    /// The likely reason, and the thing to test before trying again: the
    /// warm-cool axis carries the text box's own boundary, which every retro
    /// card has. `scan/foil-corpus` is one desk under one lamp, so a template
    /// fitted on it can encode that rig's colour cast and score beautifully
    /// in-corpus while having learned the furniture rather than the marker. The
    /// luma channel survives the move between rigs; this one does not, yet.
    ///
    /// So the chroma *score* ships as a measurement. `sparkleChromaScore` is
    /// on the wire and in `--sparkle-score`, which is what a second corpus —
    /// shot on another rig, with nonfoils — would be judged against.
    ///
    /// The chroma *contrast* is a different question with a different answer,
    /// and it votes: colour variation inside the marker patch is foil sheen
    /// regardless of whether its pattern matches any template, which is
    /// exactly the evidence that survives a stamp printed under rules text or
    /// caught misaligned. See `SparkleGate.acceptChromaContrast` for the
    /// cross-rig measurement that separates it from the score's failure.
    public var isFoil: Bool {
        if (luma?.score ?? -1) >= SparkleGate.accept { return true }
        return (chroma?.contrast ?? -1) >= SparkleGate.acceptChromaContrast
            && (luma?.contrast ?? -1) >= SparkleGate.chromaVoteLumaFloor
    }

    /// Which channel carried the verdict, for the log. Empty when none did.
    ///
    /// "chroma-only" survives as the observer it always was: the score
    /// channel would have voted here and is not allowed to. Its rate in
    /// session logs is the evidence a second-rig corpus would be judged
    /// against.
    public var channel: String {
        if (luma?.score ?? -1) >= SparkleGate.accept { return "luma" }
        if (chroma?.contrast ?? -1) >= SparkleGate.acceptChromaContrast,
           (luma?.contrast ?? -1) >= SparkleGate.chromaVoteLumaFloor { return "chroma" }
        if (chroma?.score ?? -1) >= SparkleGate.acceptChroma { return "chroma-only" }
        return ""
    }

    /// The luma score, kept under its old name so the wire and every fixture
    /// that already reads `sparkleScore` keep meaning what they meant.
    public var score: CGFloat { luma?.score ?? 0 }
}

/// sparklePatch takes one template-shaped sample of the card, decimated by
/// `step`, offset from the nominal centre by `du`/`dv` in card space.
///
/// Returns nil if any cell falls outside the card — a patch that runs off the
/// image is not the patch the template describes.
///
/// Public because the fitting harness builds the template from exactly these
/// samples. The *search* is not shared with it: brute-force alignment is a
/// corpus-time luxury that has no business in a shipping read.
public func sparklePatch(du: CGFloat, dv: CGFloat, step: Int,
                         _ sample: CardSampler) -> [CGFloat]? {
    var out: [CGFloat] = []
    out.reserveCapacity((SparkleTemplate.rows / step + 1) * (SparkleTemplate.cols / step + 1))
    var j = 0
    while j < SparkleTemplate.rows {
        let v = CardLayout.sparkleV + dv
            + (CGFloat(j) / CGFloat(SparkleTemplate.rows - 1) - 0.5) * SparkleTemplate.spanV
        var i = 0
        while i < SparkleTemplate.cols {
            let u = CardLayout.sparkleU + du
                + (CGFloat(i) / CGFloat(SparkleTemplate.cols - 1) - 0.5) * SparkleTemplate.spanU
            guard let l = sample(u, v) else { return nil }
            out.append(l)
            i += step
        }
        j += step
    }
    return out
}

/// sparkleNormalise makes a patch zero-mean and unit-norm.
///
/// This is what carries the reader from a clean scan to a desk lamp:
/// correlation after this step depends on the patch's *shape* and not at all on
/// how bright or contrasty the lamp made it — the same "judge against the
/// card's own tones" rule the border ring lives by.
public func sparkleNormalise(_ p: [CGFloat]) -> [CGFloat]? {
    guard !p.isEmpty else { return nil }
    let mean = p.reduce(0, +) / CGFloat(p.count)
    var out = p.map { $0 - mean }
    let energy = sqrt(out.reduce(0) { $0 + $1 * $1 })
    guard energy > 1e-6 else { return nil }
    for i in out.indices { out[i] /= energy }
    return out
}

/// sparkleCorrelate is a dot product, which is all normalised cross-correlation
/// is once both sides have been through `sparkleNormalise`.
public func sparkleCorrelate(_ a: [CGFloat], _ b: [CGFloat]) -> CGFloat {
    var sum: CGFloat = 0
    for i in a.indices where i < b.count { sum += a[i] * b[i] }
    return sum
}

/// How far the reader may hunt for the marker, and how finely. Fitted values in
/// `SparkleGate`; overridable so the corpus harness can sweep a wider window to
/// *measure* where the marker actually landed, which is how a systematic error
/// in the card-space reconstruction gets found rather than absorbed.
public struct SparkleWindow: Sendable {
    public let u: CGFloat, v: CGFloat
    public let cellsU: Int, cellsV: Int
    public init(u: CGFloat, v: CGFloat, cellsU: Int, cellsV: Int) {
        self.u = u; self.v = v; self.cellsU = cellsU; self.cellsV = cellsV
    }
    public static let fitted = SparkleWindow(
        u: SparkleGate.searchU, v: SparkleGate.searchV,
        cellsU: SparkleGate.searchCellsU, cellsV: SparkleGate.searchCellsV)
}

/// sparkleScan is the reader itself, over any card-space sampler.
public func sparkleScan(_ sample: CardSampler,
                        window: SparkleWindow = .fitted,
                        template: [CGFloat] = sparkleTemplate) -> SparkleReading? {
    var reads = 0

    func patch(du: CGFloat, dv: CGFloat, step: Int) -> [CGFloat]? {
        guard let p = sparklePatch(du: du, dv: dv, step: step, sample) else { return nil }
        reads += p.count
        return p
    }
    let normalise = sparkleNormalise
    let correlate = sparkleCorrelate

    // The search grid is defined in card space, in cells, so a card at any
    // size in frame is searched over the same physical region at the same
    // physical granularity. One cell is a pixel of a 630x880 card, which is
    // the scale the constants were fitted at.
    let cellU = window.u / CGFloat(window.cellsU)
    let cellV = window.v / CGFloat(window.cellsV)

    // Coarse: a 4x-decimated template over the whole window at stride 4.
    var coarse: [(score: CGFloat, i: Int, j: Int)] = []
    let t4 = sparkleTemplateDecimated(4, template)
    var i = -window.cellsU
    while i <= window.cellsU {
        var j = -window.cellsV
        while j <= window.cellsV {
            if let p = patch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV, step: 4),
               let n = normalise(p) {
                coarse.append((correlate(n, t4), i, j))
            }
            j += SparkleGate.coarseStride
        }
        i += SparkleGate.coarseStride
    }
    guard !coarse.isEmpty else { return nil }
    coarse.sort { $0.score > $1.score }

    // Refine: a 2x-decimated template at stride 1 around the best two coarse
    // hits. Two rather than one because the coarse surface is noisy enough that
    // the true peak is sometimes its runner-up; three bought nothing measurable.
    let t2 = sparkleTemplateDecimated(2, template)
    var bestScore = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for candidate in coarse.prefix(SparkleGate.refineCandidates) {
        for di in -SparkleGate.refineRadius...SparkleGate.refineRadius {
            for dj in -SparkleGate.refineRadius...SparkleGate.refineRadius {
                let i = candidate.i + di, j = candidate.j + dj
                guard abs(i) <= window.cellsU, abs(j) <= window.cellsV else { continue }
                guard let p = patch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV, step: 2),
                      let n = normalise(p) else { continue }
                let s = correlate(n, t2)
                if s > bestScore { bestScore = s; bestI = i; bestJ = j }
            }
        }
    }
    guard bestScore > -CGFloat.infinity else { return nil }
    let bestDU = CGFloat(bestI) * cellU, bestDV = CGFloat(bestJ) * cellV

    // Judge once, at full resolution, at the position the cheap passes found.
    guard let final = patch(du: bestDU, dv: bestDV, step: 1) else { return nil }
    let spread = medianAbsoluteDeviation(final)
    guard let n = normalise(final) else { return nil }
    guard spread >= SparkleGate.minContrast else {
        // Structure-free patch. Reported rather than scored: a blown highlight
        // and a genuinely absent marker are different problems and the corpus
        // needs to tell them apart.
        return SparkleReading(score: 0, offsetU: bestDU, offsetV: bestDV,
                              contrast: spread, samples: reads)
    }
    return SparkleReading(score: correlate(n, template),
                          offsetU: bestDU, offsetV: bestDV,
                          contrast: spread, samples: reads)
}

/// sparkleTemplateDecimated samples the template on the same lattice a
/// decimated patch uses, then re-normalises — correlation is only meaningful
/// between two vectors normalised over the *same* cells.
func sparkleTemplateDecimated(_ step: Int, _ source: [CGFloat] = sparkleTemplate) -> [CGFloat] {
    var out: [CGFloat] = []
    var j = 0
    while j < SparkleTemplate.rows {
        var i = 0
        while i < SparkleTemplate.cols {
            out.append(source[j * SparkleTemplate.cols + i])
            i += step
        }
        j += step
    }
    let mean = out.reduce(0, +) / CGFloat(out.count)
    for i in out.indices { out[i] -= mean }
    let energy = sqrt(out.reduce(0) { $0 + $1 * $1 })
    guard energy > 1e-6 else { return out }
    for i in out.indices { out[i] /= energy }
    return out
}
