// The foil sparkle reader's arithmetic and its refusals.
//
// scan/foil-corpus says whether the detector separates foil from nonfoil, and
// that is the question that matters — but it cannot say whether the *reader*
// is right, only whether it scores well. A transposed template, a search that
// never moves, or a normalisation that quietly divides by the wrong thing all
// show up there as a slightly worse table rather than as a failure. These say
// it directly.
//
// The load-bearing test is `findsAPerfectMatch`: a synthetic card built to
// contain the template exactly, at a known offset, which the reader has to
// locate and score at 1.0. It exercises the coarse pass, the refine pass and
// the full-resolution scoring in one, and it fails loudly for every way the
// card-space arithmetic can be wrong.

import CoreGraphics
import Testing

@testable import BorderKit

/// A card-space sampler that paints the template into the marker's
/// neighbourhood, offset by `du`/`dv`, over a flat background.
private func syntheticCard(du: CGFloat = 0, dv: CGFloat = 0,
                           background: CGFloat = 0.5) -> CardSampler {
    { u, v in
        // Where this (u, v) falls within the template's box, once the box is
        // moved by the offset being simulated.
        let su = (u - du - CardLayout.sparkleU) / SparkleTemplate.spanU + 0.5
        let sv = (v - dv - CardLayout.sparkleV) / SparkleTemplate.spanV + 0.5
        guard su >= 0, su < 1, sv >= 0, sv < 1 else { return background }
        let i = min(SparkleTemplate.cols - 1, Int(su * CGFloat(SparkleTemplate.cols)))
        let j = min(SparkleTemplate.rows - 1, Int(sv * CGFloat(SparkleTemplate.rows)))
        // Into a plausible luma range. Correlation is invariant to both, which
        // is the point of scaling them here rather than using the raw values.
        return 0.5 + sparkleTemplate[j * SparkleTemplate.cols + i] * 4
    }
}

@Test func templateIsNormalised() {
    #expect(sparkleTemplate.count == SparkleTemplate.cols * SparkleTemplate.rows)
    let mean = sparkleTemplate.reduce(0, +) / CGFloat(sparkleTemplate.count)
    #expect(abs(mean) < 1e-4, "template must be zero-mean, got \(mean)")
    let energy = sparkleTemplate.reduce(0) { $0 + $1 * $1 }
    #expect(abs(energy - 1) < 1e-4, "template must be unit-norm, got \(energy)")
    #expect(sparkleTemplateFittedFrom > 0, "a placeholder template shipped")
}

@Test func decimatedTemplateIsAlsoNormalised() {
    // Correlation is only meaningful between vectors normalised over the same
    // cells, so the decimated template has to be re-normalised rather than
    // sliced out of the full one.
    for step in [2, 4] {
        let t = sparkleTemplateDecimated(step)
        let energy = t.reduce(0) { $0 + $1 * $1 }
        #expect(abs(energy - 1) < 1e-4, "step \(step) not unit-norm: \(energy)")
    }
}

@Test func findsAPerfectMatch() throws {
    let r = try #require(sparkleScan(syntheticCard()))
    #expect(r.score > 0.99, "a card that is the template should score 1, got \(r.score)")
    #expect(abs(r.offsetU) < 0.002, "should have found it centred, du \(r.offsetU)")
    #expect(abs(r.offsetV) < 0.002, "should have found it centred, dv \(r.offsetV)")
}

@Test func findsAnOffsetMatch() throws {
    // Half the search window in each direction — the reason the window exists.
    let du = SparkleGate.searchU / 2, dv = SparkleGate.searchV / 2
    let r = try #require(sparkleScan(syntheticCard(du: du, dv: dv)))
    // Not 1.0: the search steps in whole cells and samples nearest-neighbour,
    // so an offset landing between cells blurs. Well clear of the 0.50 bar is
    // the property that matters.
    #expect(r.score > 0.85, "offset match should still score, got \(r.score)")
    // Located to within a couple of cells of where it was actually put.
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    #expect(abs(r.offsetU - du) <= cellU * 2, "du \(r.offsetU) want \(du)")
    #expect(abs(r.offsetV - dv) <= cellV * 2, "dv \(r.offsetV) want \(dv)")
}

@Test func aPerfectlyFlatPatchAbstains() {
    // Blown out or crushed to black: zero variance cannot be normalised, so
    // there is no correlation to take and the reader says nothing at all.
    #expect(sparkleScan({ _, _ in 0.5 }) == nil)
}

@Test func aNearlyFlatPatchIsRefusedRatherThanScored() throws {
    // The case the contrast floor exists for, and the one that actually occurs:
    // a patch with just enough noise to normalise, and nothing in it worth
    // correlating against. Left to itself that noise takes whatever score the
    // template's own structure happens to give it.
    var seed: UInt64 = 1
    let r = try #require(sparkleScan({ _, _ in
        seed = seed &* 6364136223846793005 &+ 1442695040888963407
        return 0.5 + CGFloat(seed >> 40) / CGFloat(UInt64(1) << 24) * 0.002
    }))
    #expect(r.contrast < SparkleGate.minContrast)
    #expect(r.score == 0, "a structureless patch must not carry a score")
}

@Test func aPatchOffTheCardAbstains() {
    #expect(sparkleScan({ _, _ in nil }) == nil)
}

@Test func invertedPolarityDoesNotScore() throws {
    // A contrast-inverted marker is not accepted, and this is intended rather
    // than an oversight.
    //
    // The search seeks the best *positive* correlation, so an inverted marker
    // does not come back as -1: it comes back as whatever weak positive peak
    // the search could find elsewhere in the window, near zero. Either way it
    // is nowhere near the bar.
    //
    // Matching |score| instead would accept it — and would also hand every
    // strongly-textured nonfoil patch a second chance at clearing the bar,
    // which is a bad trade for a case neither live session has produced.
    let r = try #require(sparkleScan({ u, v in
        guard let l = syntheticCard()(u, v) else { return nil }
        return 1 - l
    }))
    #expect(r.score < SparkleGate.accept, "inverted must not be accepted, got \(r.score)")
}

@Test func staysInsideItsSampleBudget() throws {
    let r = try #require(sparkleScan(syntheticCard()))
    #expect(r.samples <= SparkleGate.maxSamples,
            "read \(r.samples) pixels, ceiling \(SparkleGate.maxSamples)")
}

@Test func retroFooterKnowsTheFrames() {
    // Retro frames credit the artist on their own row; the M15 frame prints the
    // name bare on the set/language row.
    #expect(retroFrameFooter(["Illus. Scott M. Fischer"]))
    #expect(retroFrameFooter(["Ilus. Christopher Moeller"]))   // as OCR returns it
    #expect(retroFrameFooter(["TM & © 1993-2003 Wizards", "Illus. Kari Christensen"]))
    #expect(!retroFrameFooter(["MH3 • EN OLENA RICHARDS"]))
    #expect(!retroFrameFooter(["R 0338", "™ & © 2024 Wizards of the Coast"]))
    #expect(!retroFrameFooter([]))
}

// MARK: - CardLayout.leftU's frame families

@Test func theM15FrameGetsItsOwnLandmark() {
    // The bug this pins: every card from 2015 on was told its copyright row
    // starts at 0.080, the 8th Edition frame's value. It starts at 0.593 —
    // measured over 35 corpus cards with an IQR of 0.004. A landmark half a
    // card out throws everything derived from it clean off the image.
    let m15 = FrameEvidence(year: 2024, hasSetCode: true, numberOnOwnRow: true)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark, frame: m15) == 0.593)

    let eighth = FrameEvidence(year: 2010)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark, frame: eighth) == 0.079)
}

@Test func twentyFourteenIsDecidedOnEvidenceNotOnTheYear() {
    // Magic 2015 shipped in July 2014, so that one year holds both frames and
    // the year alone cannot separate them. The M15 frame is the first to print
    // a set/language row and the first to put the collector number on its own
    // line; either is enough.
    let ambiguous = FrameEvidence(year: 2014)
    #expect(ambiguous.isM15 == false, "no evidence means the older frame")
    #expect(FrameEvidence(year: 2014, hasSetCode: true).isM15)
    #expect(FrameEvidence(year: 2014, numberOnOwnRow: true).isM15)

    // After 2014 the year settles it on its own — a real M15 card whose set
    // code failed to read must not fall back to the 8th Edition landmark.
    #expect(FrameEvidence(year: 2021).isM15, "Snakeskin Veil reads no set code")
    // And before it, no amount of misparsed evidence promotes an old frame.
    // S.N.O.T. is a 2004 card whose joke-set text parses as set code CYRIL.
    #expect(FrameEvidence(year: 2004, hasSetCode: true).isM15 == false)
}

@Test func theOlderErasKeepTheirFittedLandmarks() {
    #expect(CardLayout.leftU(kind: .copyright, prefix: .copyrightGlyph,
                             frame: FrameEvidence(year: 1995)) == 0.086)
    #expect(CardLayout.leftU(kind: .credit, prefix: .illus,
                             frame: FrameEvidence(year: 1995)) == 0.097)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark,
                             frame: FrameEvidence(year: 2001)) == 0.233)
    // An unread year reads as pre-1998, and the corpus agrees: those cards
    // anchor on their credit row at 0.097, the pre-1998 value exactly.
    #expect(CardLayout.leftU(kind: .credit, prefix: .illus,
                             frame: FrameEvidence(year: 0)) == 0.097)
}

@Test func aLandmarkThatWasNeverMeasuredStaysNil() {
    // The whole point of the nil: the lever from the landmark to the card's far
    // side is most of a width, so a guessed offset throws a predicted position
    // off the image entirely. No answer beats a wrong one.
    #expect(CardLayout.leftU(kind: .copyright, prefix: .copyrightGlyph,
                             frame: FrameEvidence(year: 2001)) == nil)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .year,
                             frame: FrameEvidence(year: 2010)) == nil)
    #expect(CardLayout.leftU(kind: .credit, prefix: .trademark,
                             frame: FrameEvidence(year: 2024, hasSetCode: true)) == nil)
}

// MARK: - The chroma-contrast vote

@Test func chromaContrastVotesFoilOnItsOwn() {
    // The sheen is colour variation whatever shape it takes: a reading whose
    // luma correlation failed outright still answers foil when the warm-cool
    // patch carries real spread. Measured live: a Glowrider read at luma
    // -0.13 carried chroma contrast 0.14 on a genuine foil, and Charitable
    // Levy — whose stamp prints under its rules text — never clears the luma
    // bar and always carries 0.09-0.14.
    let flatLuma = SparkleReading(score: -0.13, offsetU: 0, offsetV: 0,
                                  contrast: 0.015, samples: 1)
    let sheen = SparkleReading(score: 0.16, offsetU: 0, offsetV: 0,
                               contrast: 0.14, samples: 1)
    let v = SparkleVerdict(luma: flatLuma, chroma: sheen)
    #expect(v.isFoil)
    #expect(v.channel == "chroma")
}

@Test func neutralChromaDoesNotVote() {
    // The floor sits 0.025 above the highest nonfoil on any rig (0.055,
    // colourful art bleeding into the window). A patch at that level is a
    // nonfoil's furniture, not a sheen.
    let flatLuma = SparkleReading(score: 0.40, offsetU: 0, offsetV: 0,
                                  contrast: 0.03, samples: 1)
    let furniture = SparkleReading(score: 0.63, offsetU: 0, offsetV: 0,
                                   contrast: 0.055, samples: 1)
    let v = SparkleVerdict(luma: flatLuma, chroma: furniture)
    #expect(!v.isFoil)
    #expect(v.channel.isEmpty)
}

@Test func lumaStillOutranksChromaInTheLog() {
    // Both channels clearing their bars is a luma verdict: the correlation is
    // the stronger claim and the log should attribute accordingly.
    let star = SparkleReading(score: 0.75, offsetU: 0, offsetV: 0,
                              contrast: 0.06, samples: 1)
    let sheen = SparkleReading(score: 0.7, offsetU: 0, offsetV: 0,
                               contrast: 0.12, samples: 1)
    let v = SparkleVerdict(luma: star, chroma: sheen)
    #expect(v.isFoil)
    #expect(v.channel == "luma")
}

@Test func flatLumaBlocksTheChromaVote() {
    // Printed colour art is loud on the warm-cool axis and silent on luma —
    // the one combination sheen cannot produce. The measured case: a modern
    // nonfoil at chroma contrast 0.090 with luma spread 0.008.
    let flat = SparkleReading(score: 0.18, offsetU: 0, offsetV: 0,
                              contrast: 0.008, samples: 1)
    let art = SparkleReading(score: 0.69, offsetU: 0, offsetV: 0,
                             contrast: 0.09, samples: 1)
    let v = SparkleVerdict(luma: flat, chroma: art)
    #expect(!v.isFoil)
    // The observer string may still note the chroma score would have voted —
    // that is reporting, not a verdict. What must not appear is the voting
    // channel's own name.
    #expect(v.channel != "chroma")
}
