import CoreGraphics
import Testing

@testable import BorderKit

private func syntheticCard(du: CGFloat = 0, dv: CGFloat = 0,
                           background: CGFloat = 0.5) -> CardSampler {
    { u, v in
        let su = (u - du - CardLayout.sparkleU) / SparkleTemplate.spanU + 0.5
        let sv = (v - dv - CardLayout.sparkleV) / SparkleTemplate.spanV + 0.5
        guard su >= 0, su < 1, sv >= 0, sv < 1 else { return background }
        let i = min(SparkleTemplate.cols - 1, Int(su * CGFloat(SparkleTemplate.cols)))
        let j = min(SparkleTemplate.rows - 1, Int(sv * CGFloat(SparkleTemplate.rows)))
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
    for step in [2, 4] {
        let t = sparkleTemplateDecimated(step)
        let energy = t.reduce(0) { $0 + $1 * $1 }
        #expect(abs(energy - 1) < 1e-4, "step \(step) not unit-norm: \(energy)")
    }
}

@Test func wrongLengthTemplateDecimatesToNothing() {
    #expect(sparkleTemplateDecimated(2, [0.1, 0.2, 0.3]).isEmpty)
    #expect(sparkleTemplateDecimated(4, []).isEmpty)
    #expect(!sparkleTemplateDecimated(2).isEmpty, "the shipped template must still decimate")
}

@Test func findsAPerfectMatch() throws {
    let r = try #require(sparkleScan(syntheticCard()))
    #expect(r.score > 0.99, "a card that is the template should score 1, got \(r.score)")
    #expect(abs(r.offsetU) < 0.002, "should have found it centred, du \(r.offsetU)")
    #expect(abs(r.offsetV) < 0.002, "should have found it centred, dv \(r.offsetV)")
}

@Test func findsAnOffsetMatch() throws {
    let du = SparkleGate.searchU / 2, dv = SparkleGate.searchV / 2
    let r = try #require(sparkleScan(syntheticCard(du: du, dv: dv)))
    #expect(r.score > 0.85, "offset match should still score, got \(r.score)")
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    #expect(abs(r.offsetU - du) <= cellU * 2, "du \(r.offsetU) want \(du)")
    #expect(abs(r.offsetV - dv) <= cellV * 2, "dv \(r.offsetV) want \(dv)")
}

@Test func aPerfectlyFlatPatchAbstains() {
    #expect(sparkleScan({ _, _ in 0.5 }) == nil)
}

@Test func aNearlyFlatPatchIsRefusedRatherThanScored() throws {
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
    #expect(retroFrameFooter(["Illus. Scott M. Fischer"]))
    #expect(retroFrameFooter(["Ilus. Christopher Moeller"]))
    #expect(retroFrameFooter(["TM & © 1993-2003 Wizards", "Illus. Kari Christensen"]))
    #expect(!retroFrameFooter(["MH3 • EN OLENA RICHARDS"]))
    #expect(!retroFrameFooter(["R 0338", "™ & © 2024 Wizards of the Coast"]))
    #expect(!retroFrameFooter([]))
}

@Test func theM15FrameGetsItsOwnLandmark() {
    let m15 = FrameEvidence(year: 2024, hasSetCode: true, numberOnOwnRow: true)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark, frame: m15) == 0.593)

    let eighth = FrameEvidence(year: 2010)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark, frame: eighth) == 0.079)
}

@Test func twentyFourteenIsDecidedOnEvidenceNotOnTheYear() {
    let ambiguous = FrameEvidence(year: 2014)
    #expect(ambiguous.isM15 == false, "no evidence means the older frame")
    #expect(FrameEvidence(year: 2014, hasSetCode: true).isM15)
    #expect(FrameEvidence(year: 2014, numberOnOwnRow: true).isM15)

    #expect(FrameEvidence(year: 2021).isM15, "Snakeskin Veil reads no set code")
    #expect(FrameEvidence(year: 2004, hasSetCode: true).isM15 == false)
}

@Test func theOlderErasKeepTheirFittedLandmarks() {
    #expect(CardLayout.leftU(kind: .copyright, prefix: .copyrightGlyph,
                             frame: FrameEvidence(year: 1995)) == 0.086)
    #expect(CardLayout.leftU(kind: .credit, prefix: .illus,
                             frame: FrameEvidence(year: 1995)) == 0.097)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .trademark,
                             frame: FrameEvidence(year: 2001)) == 0.233)
    #expect(CardLayout.leftU(kind: .credit, prefix: .illus,
                             frame: FrameEvidence(year: 0)) == 0.097)
}

@Test func aLandmarkThatWasNeverMeasuredStaysNil() {
    #expect(CardLayout.leftU(kind: .copyright, prefix: .copyrightGlyph,
                             frame: FrameEvidence(year: 2001)) == nil)
    #expect(CardLayout.leftU(kind: .copyright, prefix: .year,
                             frame: FrameEvidence(year: 2010)) == nil)
    #expect(CardLayout.leftU(kind: .credit, prefix: .trademark,
                             frame: FrameEvidence(year: 2024, hasSetCode: true)) == nil)
}

@Test func chromaContrastVotesFoilOnItsOwn() {
    let flatLuma = SparkleReading(score: -0.13, offsetU: 0, offsetV: 0,
                                  contrast: 0.015, samples: 1)
    let sheen = SparkleReading(score: 0.16, offsetU: 0, offsetV: 0,
                               contrast: 0.14, samples: 1)
    let v = SparkleVerdict(luma: flatLuma, chroma: sheen)
    #expect(v.isFoil)
    #expect(v.channel == "chroma")
}

@Test func neutralChromaDoesNotVote() {
    let flatLuma = SparkleReading(score: 0.40, offsetU: 0, offsetV: 0,
                                  contrast: 0.03, samples: 1)
    let furniture = SparkleReading(score: 0.63, offsetU: 0, offsetV: 0,
                                   contrast: 0.055, samples: 1)
    let v = SparkleVerdict(luma: flatLuma, chroma: furniture)
    #expect(!v.isFoil)
    #expect(v.channel.isEmpty)
}

@Test func flatLumaCorrelationIsNotAFoil() {
    let flatEcho = SparkleReading(score: 0.607, offsetU: 0, offsetV: 0,
                                  contrast: 0.017, samples: 1)
    let neutral = SparkleReading(score: 0.3, offsetU: 0, offsetV: 0,
                                 contrast: 0.02, samples: 1)
    let v = SparkleVerdict(luma: flatEcho, chroma: neutral)
    #expect(!v.isFoil)
    #expect(v.channel.isEmpty)

    let star = SparkleReading(score: 0.607, offsetU: 0, offsetV: 0,
                              contrast: 0.027, samples: 1)
    let real = SparkleVerdict(luma: star, chroma: neutral)
    #expect(real.isFoil)
    #expect(real.channel == "luma")
}

@Test func lumaStillOutranksChromaInTheLog() {
    let star = SparkleReading(score: 0.75, offsetU: 0, offsetV: 0,
                              contrast: 0.06, samples: 1)
    let sheen = SparkleReading(score: 0.7, offsetU: 0, offsetV: 0,
                               contrast: 0.12, samples: 1)
    let v = SparkleVerdict(luma: star, chroma: sheen)
    #expect(v.isFoil)
    #expect(v.channel == "luma")
}

@Test func flatLumaBlocksTheChromaVote() {
    let flat = SparkleReading(score: 0.18, offsetU: 0, offsetV: 0,
                              contrast: 0.008, samples: 1)
    let art = SparkleReading(score: 0.69, offsetU: 0, offsetV: 0,
                             contrast: 0.09, samples: 1)
    let v = SparkleVerdict(luma: flat, chroma: art)
    #expect(!v.isFoil)
    #expect(v.channel != "chroma")
}
