// The reader's answer and the gates it has to clear. Abstaining is a first
// class outcome that always says which gate failed, so a silent border is
// diagnosable rather than mysterious.

import CoreGraphics
import Foundation

/// BorderReading is everything the reader saw, including when it refuses to
/// answer. The numbers ride along regardless of the verdict because that is
/// what --border-probe fits the constants from; only `color` is a claim.
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
