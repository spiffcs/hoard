import CoreGraphics
import Foundation

public struct BorderReading: Encodable, Sendable {
    public init() {}

    public var color: String? = nil
    public var source: String? = nil
    public var abstain: String = ""
    public var anchorKind: String = ""
    public var t: Double = 0
    public var standoff: Double = 0
    public var ringBottom: Double = 0
    public var ringTop: Double = 0
    public var ringMAD: Double = 0
    public var ringChroma: Double = 0
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
    public var footerVMeasured: Double = 0
    public var titleVMeasured: Double = 0
    public var footerGlyphVMeasured: Double = 0
    public var footerLeftU: Double = 0
    public var footerRightU: Double = 0
    public var titleLeftU: Double = 0
    public var creditCandidateLeftU: Double = 0
    public var horizontalAnchor: Bool = false
    public var symbolCoverage: Double = 0
    public var symbolContrast: Double = 0

}

enum BorderGate {
    static let whiteTone: CGFloat = 1.30
    static let blackTone: CGFloat = -0.01
    static let minToneRange: CGFloat = 0.06
    static let minInnerDelta: CGFloat = 0.05
    static let maxRingMAD: CGFloat = 0.10
    static let minCardHeightPx: CGFloat = 500
    static let maxThetaDegrees: CGFloat = 25
    static let maxScaleDisagreement: CGFloat = 0.35

}

public enum SparkleGate {
    public static let accept: CGFloat = 0.52

    public static let acceptChroma: CGFloat = 0.68

    public static let acceptChromaContrast: CGFloat = 0.08

    public static let chromaVoteLumaFloor: CGFloat = 0.012

    public static let acceptLumaContrast: CGFloat = 0.02

    public static let searchU: CGFloat = 0.0238
    public static let searchV: CGFloat = 0.0216
    public static let searchCellsU = 15
    public static let searchCellsV = 19

    public static let coarseStride = 4
    public static let refineCandidates = 2
    public static let refineRadius = 3

    public static let minContrast: CGFloat = 0.005

    public static let firstFoilYear = 1999

    public static let maxSamples = 60_000
}
