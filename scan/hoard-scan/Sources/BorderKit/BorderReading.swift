// The reader's answer and the gates it has to clear. Abstaining is a first
// class outcome that always says which gate failed, so a silent border is
// diagnosable rather than mysterious.

import CoreGraphics
import Foundation

/// BorderReading is everything the reader saw, including when it refuses to
/// answer. The numbers ride along regardless of the verdict because that is what
/// the constants are fitted from — `cardkit-probe --image X --border`, swept
/// over the corpus by scan/corpus/border.sh; only `color` is a claim.
public struct BorderReading: Encodable, Sendable {
    public init() {}

    public var color: String? = nil        // "white" | "black", absent when abstaining
    public var source: String? = nil       // "footer" | "footer+ring"
    public var abstain: String = ""        // why, when color is absent
    public var anchorKind: String = ""     // which footer row the geometry came from
    /// Where the border sits in the card's own footer tone range: >1 is
    /// brighter than its paper, <0 darker than its ink. The decision.
    public var t: Double = 0
    /// Ring minus the frame just inside it — the corroborating check that the
    /// ring is a different surface at all.
    public var standoff: Double = 0
    public var ringBottom: Double = 0
    public var ringTop: Double = 0
    public var ringMAD: Double = 0
    public var ringChroma: Double = 0
    /// The card's own frame just inside the bottom border — the candidate
    /// reference for normalizing illumination, since it is the same surface
    /// under the same light and is always present.
    public var innerBottom: Double = 0
    public var innerMAD: Double = 0
    public var patchDark: Double = 0
    public var patchBright: Double = 0
    public var patchSeparation: Double = 0
    public var patchDarkFraction: Double = 0
    public var patchChroma: Double = 0
    public var clipHigh: Double = 0
    public var clipLow: Double = 0
    public var cardHeightPx: Double = 0
    public var scaleAgreement: Double = 0
    public var thetaDegrees: Double = 0
    public var footerText: String = ""
    public var titleText: String = ""
    /// Where the anchors actually sat, as a fraction of the frame's height
    /// measured down from the top. On a clean scan the card *is* the frame, so
    /// these are CardLayout.footerV and titleV measured directly — which is
    /// how those constants get fitted.
    public var footerVMeasured: Double = 0
    public var titleVMeasured: Double = 0
    public var footerGlyphVMeasured: Double = 0
    /// Horizontal extents of the anchors, as fractions of the frame's width.
    /// On a clean scan the card *is* the frame, so these read directly as card
    /// space — which is how the symbol reader's horizontal anchor gets chosen
    /// between them rather than assumed.
    public var footerLeftU: Double = 0
    public var footerRightU: Double = 0
    public var titleLeftU: Double = 0
    /// Whether a horizontal landmark was established at all, and what the type
    /// line's right margin holds if so. Reported, never acted on yet — this is
    /// the measurement the symbol reader will be built from.
    /// Left edge of the positional credit candidate, whether or not it won the
    /// anchor. On a clean scan the copyright row reads and wins, so this is the
    /// only way to measure the credit row's landmark for a frame whose live
    /// photographs anchor on it instead.
    public var creditCandidateLeftU: Double = 0
    public var horizontalAnchor: Bool = false
    public var symbolCoverage: Double = 0
    public var symbolContrast: Double = 0

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

/// What the sparkle reader has to clear to claim a foil, and how hard it is
/// allowed to look. Kept apart from `BorderGate` because it answers a different
/// question off the same pixels, and its constants come from a different
/// corpus — scan/foil-corpus rather than scan/corpus.
public enum SparkleGate {
    /// Correlation at or above this is a foil.
    ///
    /// Fitted on scan/foil-corpus with the template built from one session and
    /// scored on the other, so the separation is held out rather than
    /// remembered. End to end on session 2's stills, retro foils run
    /// 0.533-0.778 and retro nonfoils -0.427 to 0.443 — a clean 0.09 gap.
    ///
    /// The threshold is not in the middle of that gap, and the reason is a
    /// card outside the corpus. `scan/fixtures`' Sacred Ground is a confirmed
    /// nonfoil that scores **0.505**, higher than any nonfoil either live
    /// session produced, and Builder's Bane scores 0.509. So the bar sits just
    /// above them rather than where the corpus alone would put it. It costs
    /// nothing measured: the lowest true foil is 0.533.
    ///
    /// That margin is thin — 0.015 above the worst known nonfoil and 0.013
    /// below the weakest true foil — and it is thin because it is pinned by
    /// single cards on both sides. Widen the corpus before trusting it further.
    ///
    /// The five modern-frame foils reach 0.29-0.44. Their frame prints no
    /// sparkle at all, so that band is what "this region holds no marker"
    /// scores, and the bar clears it comfortably.
    public static let accept: CGFloat = 0.52

    /// The same bar on the warm-cool channel — see `SparkleVerdict`.
    ///
    /// Higher than luma's, and it has to be. The warm-cool axis carries the
    /// card's furniture as well as its marker — the text box's edge and the
    /// border are a warm-to-cool step on every card, foil or not — so the whole
    /// nonfoil population scores higher here than it does on luma. Held out
    /// (template fitted on session 1, scored on session 2) the nonfoils reach
    /// **0.637** against luma's 0.455, and the bar sits above that.
    ///
    /// The first attempt at this channel used luma's 0.52 and produced **nine
    /// false positives out of eighteen nonfoils**. That number is the reason
    /// this constant is not simply `accept`.
    ///
    /// It also has to clear the *modern* frames, and that is what sets it. A
    /// modern frame prints no marker at all, so the five in the corpus are the
    /// reader's "nothing is there" control — and on this channel they reach
    /// 0.672 where on luma they stop at 0.437. The comment in `readCard` that
    /// justifies running the sparkle on every frame rests on that control, so
    /// the bar has to stay above it or that reasoning stops being true.
    ///
    /// 0.68, which on the full corpus accepts 27 of 27 retro foils against
    /// luma's 24, with 0 of 18 retro nonfoils and 0 of 5 modern frames. Held out
    /// (fit on session 1, score on session 2) the same rule takes 13 of 13
    /// against luma's 11. The two channels miss different cards — see
    /// `SparkleVerdict` — which is what makes taking either of them safe.
    public static let acceptChroma: CGFloat = 0.68

    /// Half-width of the search window, in card space, and how many cells that
    /// is divided into. One cell is a pixel of a 630x880 card.
    ///
    /// **These are fitted, not slack.** Widening to 0.037/0.042 recovers one
    /// foil and admits two false positives — the highest nonfoil goes from
    /// 0.470 to 0.676. Do not widen them to chase recall; re-centre
    /// `CardLayout.sparkleU`/`sparkleV` instead.
    public static let searchU: CGFloat = 0.0238
    public static let searchV: CGFloat = 0.0216
    public static let searchCellsU = 15
    public static let searchCellsV = 19

    /// The coarse pass steps four cells at a time on a 4x-decimated template;
    /// the refine pass walks every cell within this radius of the best coarse
    /// hits. Together ~50,700 pixel reads against the brute force's 2,137,512,
    /// for identical verdicts on all 50 corpus cards.
    public static let coarseStride = 4
    public static let refineCandidates = 2
    public static let refineRadius = 3

    /// Below this the patch has no structure to judge — a blown highlight or a
    /// crushed shadow correlates with whatever the template happens to hold.
    /// Measured as median absolute deviation of the patch's luma.
    public static let minContrast: CGFloat = 0.005

    /// Premium foils began with Urza's Legacy, February 1999. A card printed
    /// before that is nonfoil as a matter of fact, whatever its pixels
    /// correlate with, and no score should be able to say otherwise.
    ///
    /// This is not belt-and-braces — it is load-bearing. The template is fitted
    /// on 2001-2024 frames, and the pre-1998 frame lays its text box out
    /// differently enough that the search reaches the edge of its window and
    /// scores whatever noise it finds there. Four pre-1998 cards in
    /// scan/fixtures cleared 0.50 that way: Seasinger (1994), Control Magic
    /// (1995), Builder's Bane, and one 2003 card this does not catch.
    public static let firstFoilYear = 1999

    /// The cost ceiling, asserted rather than hoped for. A previous attempt at
    /// this feature put auto-commit times up more than tenfold, and what it
    /// lacked was a number that said so before a live session did.
    public static let maxSamples = 60_000
}
